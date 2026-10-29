package mcprunner

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestSandboxModesParse(t *testing.T) {
	for in, want := range map[string]SandboxMode{"": SandboxAuto, "off": SandboxOff, "auto": SandboxAuto, "required": SandboxRequired} {
		if got, err := ParseSandboxMode(in, SandboxAuto); err != nil || got != want {
			t.Fatalf("%q -> %q %v", in, got, err)
		}
	}
	if _, err := ParseSandboxMode("yes", SandboxAuto); err == nil {
		t.Fatal("an unknown mode was accepted")
	}
	// required stops, auto goes on, off ignores.
	if err := unavailable(SandboxRequired, "x", os.ErrPermission); err == nil {
		t.Fatal("required mode swallowed a failure")
	}
	if unavailable(SandboxAuto, "x", os.ErrPermission) != nil || unavailable(SandboxOff, "x", os.ErrPermission) != nil {
		t.Fatal("auto and off must not stop the Runner")
	}
}

func TestAccountNetworksNeverOverlapAndStayInsideTheirRange(t *testing.T) {
	seen := map[netip.Addr]int{}
	for uid := 1; uid < MaxUID; uid++ {
		host, guest, err := accountAddrs(uid)
		if err != nil {
			t.Fatal(err)
		}
		if !accountNetPrefix.Contains(host) || !accountNetPrefix.Contains(guest) {
			t.Fatalf("uid %d: %s/%s outside %s", uid, host, guest, accountNetPrefix)
		}
		for _, a := range []netip.Addr{host, guest} {
			if other, dup := seen[a]; dup {
				t.Fatalf("uid %d and %d share %s", uid, other, a)
			}
			seen[a] = uid
		}
		// Both ends of a /30 share one subnet.
		p, _ := host.Prefix(30)
		if !p.Contains(guest) {
			t.Fatalf("uid %d: %s and %s are not in one /30", uid, host, guest)
		}
	}
	if _, _, err := accountAddrs(0); err == nil {
		t.Fatal("uid 0 got an account network")
	}
	h, p := vethNames(65533)
	if len(h) > 15 || len(p) > 15 || !strings.HasPrefix(h, "tv") || !strings.HasPrefix(p, "tp") {
		t.Fatalf("interface names %q %q", h, p)
	}
}

// The forward rules are what keep an account from reaching another account,
// the App's network or the Runner itself. Order matters: the first match wins.
func TestAccountFirewallOnlyLetsAccountsOutThroughTheEgressInterface(t *testing.T) {
	forward, input, nat := accountRules("eth1")
	flat := func(rules [][]string) []string {
		var out []string
		for _, r := range rules {
			out = append(out, strings.Join(r, " "))
		}
		return out
	}
	f := flat(forward)
	want := []string{
		"-i tv+ -o tv+ -j DROP",
		"-i tv+ -o eth1 -j ACCEPT",
		"-i tv+ -j DROP",
		"-o tv+ -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT",
		"-o tv+ -j DROP",
	}
	if strings.Join(f, "\n") != strings.Join(want, "\n") {
		t.Fatalf("forward rules:\n%s", strings.Join(f, "\n"))
	}
	if got := flat(input); len(got) != 1 || got[0] != "-i tv+ -j DROP" {
		t.Fatalf("input rules: %v", got)
	}
	if got := flat(nat); len(got) != 1 || !strings.Contains(got[0], "-s 100.64.0.0/14 -o eth1 -j MASQUERADE") {
		t.Fatalf("nat rules: %v", got)
	}
}

// ---- seccomp program ----

// runBPF interprets the classic BPF subset the assembler emits.
func runBPF(prog []bpfIns, arch, nr, arg0 uint32) uint32 {
	var a uint32
	for pc := 0; pc < len(prog); pc++ {
		in := prog[pc]
		switch {
		case in.Code == bpfLD|bpfW|bpfABS:
			switch in.K {
			case seccompDataArch:
				a = arch
			case seccompDataNr:
				a = nr
			case seccompDataArg0:
				a = arg0
			default:
				panic("unexpected load")
			}
		case in.Code == bpfALU|bpfAND|bpfK:
			a &= in.K
		case in.Code == bpfJMP|bpfJEQ|bpfK:
			if a == in.K {
				pc += int(in.Jt)
			} else {
				pc += int(in.Jf)
			}
		case in.Code == bpfRET|bpfK:
			return in.K
		default:
			panic("unexpected instruction")
		}
	}
	panic("fell off the program")
}

func TestSeccompProgramDeniesNamespacesAndMountsButNothingElse(t *testing.T) {
	const arch, unshare, mount, clone, clone3, read = 0xC000003E, 272, 165, 56, 435, 0
	prog := seccompPolicy{Arch: arch, Denied: []uint32{unshare, mount}, NoSys: []uint32{clone3}, Clone: clone}.build()
	eperm, enosys := uint32(seccompRetErrno|errnoEPERM), uint32(seccompRetErrno|errnoENOSYS)
	threadFlags := uint32(0x00000100 | 0x00000200 | 0x00000400 | 0x00000800 | 0x00010000 | 0x00040000 | 0x00100000 | 0x00200000) // VM FS FILES SIGHAND THREAD SYSVSEM SETTLS PARENT_SETTID
	for name, c := range map[string]struct {
		arch, nr, arg uint32
		want          uint32
	}{
		"read":                   {arch, read, 0, seccompRetAllow},
		"unshare":                {arch, unshare, 0, eperm},
		"mount":                  {arch, mount, 0, eperm},
		"clone3":                 {arch, clone3, 0, enosys},
		"fork":                   {arch, clone, 0x11, seccompRetAllow}, // SIGCHLD
		"thread":                 {arch, clone, threadFlags, seccompRetAllow},
		"clone user namespace":   {arch, clone, 0x10000000 | 0x11, eperm},
		"clone net namespace":    {arch, clone, 0x40000000, eperm},
		"clone mount namespace":  {arch, clone, 0x00020000, eperm},
		"clone pid namespace":    {arch, clone, 0x20000000, eperm},
		"other ABI, even a read": {0x40000003, read, 0, eperm},
	} {
		if got := runBPF(prog, c.arch, c.nr, c.arg); got != c.want {
			t.Errorf("%s: got %#x, want %#x", name, got, c.want)
		}
	}
}

// ---- confine ----

func TestConfineStartsTheLauncherAsRootOnlyWithASandbox(t *testing.T) {
	cred := &syscall.Credential{Uid: 20001, Gid: 20001}
	limits := Limits{Nproc: 7, Nofile: 8, Fsize: 9}
	// No sandbox: the kernel applies the credential, the limits launcher wraps the command.
	if c, a, got := confine(cred, limits, nil, "/bin/echo", []string{"hi"}); got != cred || c == "/bin/echo" || a[0] != launcherArg {
		t.Fatalf("no sandbox: %s %v %v", c, a, got)
	}
	// A sandbox: the launcher is root (nil credential) and carries the whole plan.
	sb := &accountSandbox{uid: 20001, gid: 20001, cgroupProcs: "/sys/fs/cgroup/x/cgroup.procs", netns: "/proc/9/ns/net"}
	c, a, got := confine(cred, limits, sb, "./plugin", []string{"--flag"})
	if got != nil {
		t.Fatal("the sandbox launcher must start as root and drop privileges itself")
	}
	if len(a) != 5 || a[0] != sandboxArg || a[2] != "--" || a[3] != "./plugin" || a[4] != "--flag" {
		t.Fatalf("argv of %s: %v", c, a)
	}
	var plan launchPlan
	if err := json.Unmarshal([]byte(a[1]), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Uid != 20001 || plan.Gid != 20001 || plan.NetNS != sb.netns || plan.CgroupProcs != sb.cgroupProcs || plan.Nproc != 7 || plan.Nofile != 8 || plan.Fsize != 9 || !plan.RequireFilter {
		t.Fatalf("plan: %+v", plan)
	}
	// The plugin's argv never carries the account id (it is visible in /proc).
	if strings.Contains(strings.Join(a, " "), accountA) {
		t.Fatal("account id on the command line")
	}
	// Without a credential there is nobody to drop to.
	if _, _, got := confine(nil, limits, sb, "/bin/echo", nil); got != nil {
		t.Fatal("a nil credential must stay nil")
	}
}

func TestConfineNeverFallsBackToAnUnconfinedStart(t *testing.T) {
	old := launcherExecutable
	launcherExecutable = func() (string, error) { return "", os.ErrNotExist }
	defer func() { launcherExecutable = old }()
	c, _, _ := confine(&syscall.Credential{Uid: 1, Gid: 1}, Limits{}, &accountSandbox{}, "/bin/echo", nil)
	if c == "/bin/echo" {
		t.Fatal("the plugin would have started outside the sandbox")
	}
}

func TestSandboxLauncherArgumentIsNotTakenForAnOrdinaryStart(t *testing.T) {
	if RunLauncher([]string{"tofi-mcp-runner", "serve"}) {
		t.Fatal("ordinary start taken for the launcher")
	}
}

// ---- disk quota ----

func writeBytes(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTreeBytesCountsHardLinksOnceAndNeverFollowsLinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeBytes(t, filepath.Join(outside, "huge"), 1<<20)
	writeBytes(t, filepath.Join(dir, "a"), 64<<10)
	if err := os.Link(filepath.Join(dir, "a"), filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	n, err := treeBytes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n < 64<<10 || n >= 128<<10 {
		t.Fatalf("counted %d bytes: a hard link was counted twice or a symlink was followed", n)
	}
}

func TestAccountOverQuotaLosesTmpThenStopsAndIsRefusedUntilUnder(t *testing.T) {
	hub, _ := newTestHub(t, nil)
	dir := t.TempDir()
	limit := uint64(256 << 10)
	meter := newQuotaMeter(dir, limit)
	hub.mu.Lock()
	hub.tenants["quota-test"] = &tenant{id: "quota-test", dir: dir, quota: meter}
	hub.mu.Unlock()
	t.Cleanup(func() { hub.mu.Lock(); delete(hub.tenants, "quota-test"); hub.mu.Unlock() })

	if err := meter.admit(); err != nil {
		t.Fatalf("an empty account was refused: %v", err)
	}
	// Over quota through tmp/: the sweep clears tmp and the account is under again.
	writeBytes(t, filepath.Join(dir, "tmp", "scratch"), 512<<10)
	writeBytes(t, filepath.Join(dir, "state", "plugins", "p", "data"), 8<<10)
	if over := hub.sweepQuota(); len(over) != 1 || over[0] != "quota-test" {
		t.Fatalf("sweep reported %v", over)
	}
	if _, err := os.Stat(filepath.Join(dir, "tmp", "scratch")); !os.IsNotExist(err) {
		t.Fatal("tmp was not cleared")
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "plugins", "p", "data")); err != nil {
		t.Fatal("the sweep deleted installed plugin data")
	}
	if err := meter.admit(); err != nil {
		t.Fatalf("under quota after clearing tmp, still refused: %v", err)
	}
	// Over quota through installed data: nothing is deleted, installs and starts are refused.
	writeBytes(t, filepath.Join(dir, "state", "plugins", "p", "big"), 512<<10)
	if over := hub.sweepQuota(); len(over) != 1 {
		t.Fatalf("sweep reported %v", over)
	}
	if err := meter.admit(); err != ErrOverQuota {
		t.Fatalf("an account over quota was admitted: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "state", "plugins", "p", "big")); err != nil {
		t.Fatal(err)
	}
	hub.sweepQuota()
	if err := meter.admit(); err != nil {
		t.Fatalf("still refused after removing the data: %v", err)
	}
}

func TestQuotaIsOffByDefaultAndAdmitsEverything(t *testing.T) {
	if newQuotaMeter(t.TempDir(), 0) != nil {
		t.Fatal("a zero quota must mean no quota")
	}
	var m *quotaMeter
	if m.admit() != nil {
		t.Fatal("no meter must admit")
	}
}

func TestRunnerRefusesInstallAndStartWhenAdmitSaysNo(t *testing.T) {
	r, err := NewWithOptions(nil, 0, Options{Admit: func() error { return ErrOverQuota }})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.admit(); err != ErrOverQuota {
		t.Fatalf("admit: %v", err)
	}
}

// A part that came up must not be reported as unavailable, in any mode.
func TestSandboxAvailablePartIsNeverReportedUnavailable(t *testing.T) {
	for _, mode := range []SandboxMode{SandboxRequired, SandboxAuto, SandboxOff} {
		if err := unavailable(mode, "part", nil); err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
	}
	if err := unavailable(SandboxRequired, "part", errors.New("missing")); err == nil {
		t.Fatal("a required part that failed must be an error")
	}
}

func TestSeccompProgramRefusesTheX32ABI(t *testing.T) {
	const arch, unshare, read = 0xC000003E, 272, 0
	eperm := uint32(seccompRetErrno | errnoEPERM)
	with := seccompPolicy{Arch: arch, Denied: []uint32{unshare}, DenyX32: true}.build()
	without := seccompPolicy{Arch: arch, Denied: []uint32{unshare}}.build()
	for name, nr := range map[string]uint32{"x32 unshare": unshare | x32SyscallBit, "x32 read": read | x32SyscallBit} {
		if got := runBPF(without, arch, nr, 0); got != seccompRetAllow {
			t.Fatalf("%s: the unfixed filter was expected to let it through, got %#x", name, got)
		}
		if got := runBPF(with, arch, nr, 0); got != eperm {
			t.Errorf("%s: got %#x, want EPERM", name, got)
		}
	}
	if got := runBPF(with, arch, read, 0); got != seccompRetAllow {
		t.Errorf("plain read: %#x", got)
	}
	if got := runBPF(with, arch, unshare, 0); got != eperm {
		t.Errorf("plain unshare: %#x", got)
	}
}

// fakeProc builds a /proc look-alike: <pid>/fd/<n> links to the given files.
func fakeProc(t *testing.T, pid int, targets ...string) {
	t.Helper()
	root := t.TempDir()
	fdDir := filepath.Join(root, strconv.Itoa(pid), "fd")
	if err := os.MkdirAll(fdDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i, target := range targets {
		if err := os.Symlink(target, filepath.Join(fdDir, strconv.Itoa(i+3))); err != nil {
			t.Fatal(err)
		}
	}
	old := procDir
	procDir = root
	t.Cleanup(func() { procDir = old })
}

func TestDeletedButOpenFilesCountTowardTheQuota(t *testing.T) {
	dir := t.TempDir()
	hold := t.TempDir()
	// What /proc shows for an unlinked file: a link whose text ends in " (deleted)".
	ghost := filepath.Join(hold, "ghost (deleted)")
	writeBytes(t, ghost, 512<<10)
	live := filepath.Join(hold, "live")
	writeBytes(t, live, 512<<10)
	fakeProc(t, 4242, ghost, ghost, live)
	if n := deletedOpenBytes([]int{4242}); n < 512<<10 || n >= 1024<<10 {
		t.Fatalf("held bytes %d: want the ghost once and not the live file", n)
	}
	meter := newQuotaMeter(dir, 256<<10)
	meter.held = func() int64 { return deletedOpenBytes([]int{4242}) }
	if _, err := meter.measure(); err != nil {
		t.Fatal(err)
	}
	if err := meter.admit(); err != ErrOverQuota {
		t.Fatalf("an account holding a deleted 512 KiB file was admitted: %v", err)
	}
}

func TestHostFreeSpaceFloorKillsTheLargestAccountAndRefusesStarts(t *testing.T) {
	hub, _ := newTestHub(t, func(c *HubConfig) { c.MinFreeBytes = 1 << 30 })
	oldFree, oldKill := freeBytes, killPID
	defer func() { freeBytes, killPID = oldFree, oldKill }()
	var killed []int
	killPID = func(pid int) error { killed = append(killed, pid); return nil }
	freeBytes = func(string) (uint64, error) { return 1 << 20, nil }

	small, big := t.TempDir(), t.TempDir()
	writeBytes(t, filepath.Join(small, "f"), 8<<10)
	writeBytes(t, filepath.Join(big, "f"), 512<<10)
	tenants := []*tenant{
		{id: "small", dir: small, quota: newQuotaMeter(small, 1<<30)},
		{id: "big", dir: big, quota: newQuotaMeter(big, 1<<30)},
	}
	if got := hub.enforceHostFloor(tenants); got != "big" {
		t.Fatalf("stopped %q, want the largest account", got)
	}
	if err := hub.admitHost(); !errors.Is(err, ErrLowDisk) {
		t.Fatalf("a start below the floor was admitted: %v", err)
	}
	// An unreadable disk refuses starts but kills nobody.
	freeBytes = func(string) (uint64, error) { return 0, errors.New("statfs") }
	if got := hub.enforceHostFloor(tenants); got != "" {
		t.Fatalf("killed %q on a failed reading", got)
	}
	if err := hub.admitHost(); err == nil {
		t.Fatal("a failed free-space reading admitted a start")
	}
	freeBytes = func(string) (uint64, error) { return 2 << 30, nil }
	if hub.enforceHostFloor(tenants) != "" || hub.admitHost() != nil {
		t.Fatal("above the floor nothing should happen")
	}
}
