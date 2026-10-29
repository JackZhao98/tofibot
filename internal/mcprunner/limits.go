package mcprunner

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// Limits caps what one child process of an account may consume. They are set in
// the child itself, between the uid change and the plugin's first instruction,
// by the Runner's own binary acting as a launcher (see RunLauncher).
//
// RLIMIT_NPROC counts every process of the uid, so it bounds the account as a
// whole against a fork bomb, not one plugin. Memory and disk are not limited
// here; see the residual risks in docs/decisions/0071-runner-container.md.
type Limits struct {
	Nproc  uint64 // processes of the uid
	Nofile uint64 // open files per process
	Fsize  uint64 // bytes of one file a process may grow
}

// DefaultLimits are used by the multi-account Runner when it isolates uids.
var DefaultLimits = Limits{Nproc: 128, Nofile: 1024, Fsize: 1 << 30}

func (l Limits) zero() bool { return l == Limits{} }

// launcherArg is argv[1] of the Runner binary acting as a launcher.
const launcherArg = "__tofi-limits"

// ErrLowDisk is returned by an install or import when the data filesystem is
// nearly full. Nothing was written.
var ErrLowDisk = errors.New("not enough free disk space to install")

// launcherExecutable is the binary that acts as the launcher; tests replace it
// with a copy every uid can execute.
var launcherExecutable = os.Executable

// launch returns the command and arguments to give to exec so that the child
// starts with the limits applied. Without limits (single-tenant, development) it
// returns them unchanged.
func (l Limits) launch(command string, args []string) (string, []string) {
	if l.zero() {
		return command, args
	}
	self, err := launcherExecutable()
	if err != nil {
		return command, args
	}
	wrapped := []string{launcherArg, strconv.FormatUint(l.Nproc, 10), strconv.FormatUint(l.Nofile, 10), strconv.FormatUint(l.Fsize, 10), "--", command}
	return self, append(wrapped, args...)
}

// RunLauncher is called first thing by every binary that starts plugins (the
// Runner's main and the tests' TestMain). When the process was started as a
// launcher it sets the limits, replaces itself with the real command and never
// returns; otherwise it returns false at once.
func RunLauncher(argv []string) bool {
	if len(argv) >= 5 && argv[1] == sandboxArg {
		runSandboxLauncher(argv) // never returns
		return true
	}
	if len(argv) < 7 || argv[1] != launcherArg || argv[5] != "--" {
		return false
	}
	var values [3]uint64
	for i := range values {
		v, err := strconv.ParseUint(argv[2+i], 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "launcher: invalid limit")
			os.Exit(125)
		}
		values[i] = v
	}
	if err := applyLimits(Limits{Nproc: values[0], Nofile: values[1], Fsize: values[2]}); err != nil {
		fmt.Fprintln(os.Stderr, "launcher: cannot set resource limit:", err)
		os.Exit(125)
	}
	path, err := exec.LookPath(argv[6])
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher:", err)
		os.Exit(127)
	}
	err = syscall.Exec(path, argv[6:], os.Environ())
	fmt.Fprintln(os.Stderr, "launcher:", err)
	os.Exit(126)
	return true
}

// applyLimits sets the non-zero limits on the calling process (soft = hard).
func applyLimits(l Limits) error {
	for _, r := range []struct {
		resource int
		value    uint64
	}{{unix.RLIMIT_NPROC, l.Nproc}, {unix.RLIMIT_NOFILE, l.Nofile}, {unix.RLIMIT_FSIZE, l.Fsize}} {
		if r.value == 0 {
			continue
		}
		if err := unix.Setrlimit(r.resource, &unix.Rlimit{Cur: r.value, Max: r.value}); err != nil {
			return err
		}
	}
	return nil
}

// checkFreeSpace refuses an installation when fewer than min bytes are free on
// the filesystem holding path.
func checkFreeSpace(path string, min uint64) error {
	if min == 0 {
		return nil
	}
	free, err := freeBytes(path)
	if err != nil {
		return fmt.Errorf("cannot determine free disk space: %w", err)
	}
	if free < min {
		return ErrLowDisk
	}
	return nil
}

// freeBytes is replaced by tests.
var freeBytes = func(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
