package mcprunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Account sandbox: what keeps one account's plugin processes away from another's
// and from the Runner. The uid separation in isolation.go covers files and
// signals. The sandbox adds what a uid does not cover:
//
//   - network: every account has its own network namespace. Its loopback, its
//     TCP/UDP listeners and its abstract unix sockets exist only there. The way
//     out is a veth pair into the Runner, forwarded to the Runner's egress
//     interface only, so the host firewall policy (TOFI-RUNNER-EGRESS) that
//     applies to the Runner's address applies to every plugin;
//   - memory and processes: every account has its own cgroup (v2) with a memory
//     and a pids limit;
//   - disk: every account has a size quota on its state and tmp (diskquota.go);
//   - privileges: a plugin process starts through the launcher below, which
//     does the root-only setup (cgroup, namespaces) and then drops everything
//     before the plugin's first instruction.
//
// Docs: docs/decisions/0071-runner-container.md, deploy/self-host/README.md.

// SandboxMode says how hard a missing sandbox feature is.
type SandboxMode string

const (
	// SandboxOff does not use the feature.
	SandboxOff SandboxMode = "off"
	// SandboxAuto uses the feature when the host allows it and logs a SECURITY
	// line when it does not.
	SandboxAuto SandboxMode = "auto"
	// SandboxRequired refuses to start the Runner without the feature.
	SandboxRequired SandboxMode = "required"
)

// ParseSandboxMode reads an environment value; empty means def.
func ParseSandboxMode(value string, def SandboxMode) (SandboxMode, error) {
	switch SandboxMode(value) {
	case "":
		return def, nil
	case SandboxOff, SandboxAuto, SandboxRequired:
		return SandboxMode(value), nil
	}
	return "", fmt.Errorf("%q is not off, auto or required", value)
}

// SandboxConfig configures the per-account sandbox of a Hub. The zero value
// turns every part off (tests, development).
type SandboxConfig struct {
	// Network gives every account its own network namespace.
	Network SandboxMode
	// Cgroups gives every account its own cgroup with the limits below.
	Cgroups SandboxMode
	// MemoryBytes is memory.max of one account (all its plugins together).
	MemoryBytes uint64
	// Pids is pids.max of one account.
	Pids uint64
	// DiskBytes is the size quota of one account's state and tmp. Zero turns the
	// quota off. It is a soft limit: it is checked before an install and on a timer.
	DiskBytes uint64
	// DiskInterval is the period of the quota check (default 20 seconds).
	DiskInterval time.Duration
	// RequireSeccomp refuses to start when the Runner itself runs without a
	// seccomp filter (container runtime without its default profile).
	RequireSeccomp bool
	// EgressInterface overrides the interface plugin traffic may leave through
	// (default: the interface of the default route).
	EgressInterface string
}

// Sensible defaults for one account. The Runner container itself is capped at
// 1 GiB in deploy/self-host/compose.yaml, so these are shares of that.
const (
	DefaultAccountMemory   = 384 << 20
	DefaultAccountPids     = 128
	DefaultAccountDisk     = 2 << 30
	DefaultDiskCheckPeriod = 20 * time.Second
)

func (c SandboxConfig) withDefaults() SandboxConfig {
	if c.Network == "" {
		c.Network = SandboxOff
	}
	if c.Cgroups == "" {
		c.Cgroups = SandboxOff
	}
	if c.MemoryBytes == 0 {
		c.MemoryBytes = DefaultAccountMemory
	}
	if c.Pids == 0 {
		c.Pids = DefaultAccountPids
	}
	if c.DiskInterval <= 0 {
		c.DiskInterval = DefaultDiskCheckPeriod
	}
	return c
}

func (c SandboxConfig) enabled() bool {
	return c.Network != SandboxOff || c.Cgroups != SandboxOff || c.DiskBytes > 0 || c.RequireSeccomp
}

// unavailable applies a mode to a failure: required stops, auto logs, off ignores.
func unavailable(mode SandboxMode, what string, err error) error {
	if err == nil {
		return nil
	}
	switch mode {
	case SandboxRequired:
		return fmt.Errorf("sandbox: %s is required but unavailable: %w", what, err)
	case SandboxAuto:
		log.Printf("runner: SECURITY: sandbox: %s is unavailable and the Runner continues without it: %v", what, err)
	}
	return nil
}

// cgroupBase is the directory the Runner delegates to its accounts: the root of
// the cgroup view when the container has its own cgroup namespace (its own
// cgroup is "/"), otherwise the container's cgroup inside the host-wide view.
func cgroupBase(mount, own string) string {
	if own == "" || own == "/" {
		return mount
	}
	return filepath.Join(mount, own)
}

// checkCgroupRemount decides whether the Runner may make a read-only cgroup view
// writable. Only a private cgroup namespace qualifies: there the view holds just
// the container's own subtree. In the host's namespace the same remount would
// open the whole host cgroup tree to a container that holds CAP_SYS_ADMIN.
func checkCgroupRemount(own string) error {
	if own == "" || own == "/" {
		return nil
	}
	return fmt.Errorf("the container shares the host's cgroup namespace (its cgroup is %s) and the cgroup tree is read-only; "+
		"start the Runner container with a private cgroup namespace (compose: `cgroup: private`, docker run: `--cgroupns private`)", own)
}

// cgroupRemountFailure explains a refused remount of the container's own cgroup
// view and names the fix.
func cgroupRemountFailure(cause error) error {
	return fmt.Errorf("the cgroup tree is read-only and cannot be remounted: %w "+
		"(Docker's default AppArmor profile forbids the remount: load the tofi-runner profile and start the Runner with it, "+
		"which `sudo tofi restart` does on an installed host; without AppArmor the host policy, e.g. SELinux, must allow it)", cause)
}

// launchPlan is what the Runner hands the launcher as argv[2] (JSON). The
// launcher runs as root, so it does the privileged setup, then drops to Uid/Gid
// with no capability left and no way to gain one.
type launchPlan struct {
	Uid int `json:"uid"`
	Gid int `json:"gid"`
	// CgroupProcs is the cgroup.procs file of the account's cgroup ("" = none).
	CgroupProcs string `json:"cgroup_procs,omitempty"`
	// NetNS is a /proc/<pid>/ns/net path of the account's namespace ("" = none).
	NetNS  string `json:"netns,omitempty"`
	Nproc  uint64 `json:"nproc,omitempty"`
	Nofile uint64 `json:"nofile,omitempty"`
	Fsize  uint64 `json:"fsize,omitempty"`
	// RequireFilter makes the launcher refuse to start the child when it cannot
	// install the hardening seccomp filter on this architecture.
	RequireFilter bool `json:"require_filter,omitempty"`
}

// sandboxArg is argv[1] of the Runner binary acting as the sandbox launcher.
const sandboxArg = "__tofi-sandbox"

// netnsHoldArg is argv[1] of the process that owns an account's network namespace.
const netnsHoldArg = "__tofi-netns"

// accountSandbox is the sandbox of one open account. It is created when the
// account is opened and closed when the account is closed.
type accountSandbox struct {
	host *sandboxHost
	uid  int
	gid  int
	// cgroupProcs and netns are set for the parts that are active.
	cgroupProcs string
	netns       string
	closers     []func()
	killFn      func() // kills every process of the account's cgroup
}

// killAll kills every process in the account's cgroup (uid-based killing is done by the caller).
func (s *accountSandbox) killAll() {
	if s != nil && s.killFn != nil {
		s.killFn()
	}
}

func (s *accountSandbox) plan(cred *syscall.Credential, l Limits) launchPlan {
	return launchPlan{Uid: int(cred.Uid), Gid: int(cred.Gid), CgroupProcs: s.cgroupProcs, NetNS: s.netns, Nproc: l.Nproc, Nofile: l.Nofile, Fsize: l.Fsize, RequireFilter: true}
}

// Close releases the account's namespace and cgroup.
func (s *accountSandbox) Close() {
	if s == nil {
		return
	}
	for i := len(s.closers) - 1; i >= 0; i-- {
		s.closers[i]()
	}
	s.closers = nil
}

// confine decides how a child of an account is started. With a sandbox the
// launcher starts as root (the returned credential is nil) and changes to the
// account's uid itself, after the privileged setup. Without one it is the
// resource-limit launcher and a credential applied by the kernel at exec.
func confine(cred *syscall.Credential, limits Limits, sb *accountSandbox, command string, args []string) (string, []string, *syscall.Credential) {
	if sb == nil || cred == nil {
		c, a := limits.launch(command, args)
		return c, a, cred
	}
	self, err := launcherExecutable()
	if err != nil {
		// Never fall back to starting the plugin unconfined.
		return "/nonexistent-tofi-launcher", nil, cred
	}
	blob, _ := json.Marshal(sb.plan(cred, limits))
	return self, append([]string{sandboxArg, string(blob), "--", command}, args...), nil
}

// Addressing. Account uid U owns the /30 at 100.64.0.0 + 4U inside 100.64.0.0/14
// (carrier-grade NAT space, which the Runner's egress policy already treats as
// private). The Runner end is .1, the account end .2. A uid is never reused, so
// the address is never reused for another account.
var accountNetBase = netip.MustParseAddr("100.64.0.0")

// accountNetPrefix is every account address together.
var accountNetPrefix = netip.MustParsePrefix("100.64.0.0/14")

// accountAddrs returns the Runner-side and account-side address of a uid.
func accountAddrs(uid int) (host, guest netip.Addr, err error) {
	if uid <= 0 || uid >= 1<<16 {
		return host, guest, fmt.Errorf("uid %d has no account network", uid)
	}
	b := accountNetBase.As4()
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n += uint32(uid) * 4
	at := func(off uint32) netip.Addr {
		v := n + off
		return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
	}
	return at(1), at(2), nil
}

// vethNames are the interface names of an account: Runner side, account side.
func vethNames(uid int) (host, peer string) {
	return fmt.Sprintf("tv%d", uid), fmt.Sprintf("tp%d", uid)
}

// ---- tree size, for the disk quota ----

// treeBytes sums the bytes of the regular files below dir, counting a hard-linked
// file once and never following a link. Allocated blocks are what a quota is
// about, so a sparse file counts what it occupies.
func treeBytes(dir string) (int64, error) {
	var total int64
	seen := map[[2]uint64]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // a file that went away mid-walk
			}
			return err
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			total += 4096
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			total += info.Size()
			return nil
		}
		if st.Nlink > 1 {
			key := [2]uint64{uint64(st.Dev), uint64(st.Ino)}
			if seen[key] {
				return nil
			}
			seen[key] = true
		}
		total += int64(st.Blocks) * 512
		return nil
	})
	return total, err
}

// accountRules are the firewall rules of the Runner container that confine the
// accounts' networks. They are applied once; the veth names all start with "tv".
func accountRules(egress string) (forward, input, nat [][]string) {
	forward = [][]string{
		{"-i", "tv+", "-o", "tv+", "-j", "DROP"},    // account to account
		{"-i", "tv+", "-o", egress, "-j", "ACCEPT"}, // account to the Runner's way out, nowhere else
		{"-i", "tv+", "-j", "DROP"},                 // the runner_link side, the Runner itself
		{"-o", "tv+", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"-o", "tv+", "-j", "DROP"}, // nothing starts a connection into an account
	}
	input = [][]string{{"-i", "tv+", "-j", "DROP"}} // an account never talks to the Runner process (port 8390)
	nat = [][]string{{"-s", accountNetPrefix.String(), "-o", egress, "-j", "MASQUERADE"}}
	return
}
