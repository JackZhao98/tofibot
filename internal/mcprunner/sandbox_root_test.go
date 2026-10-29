//go:build linux

package mcprunner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These tests need the real thing: Linux, root with the Runner's capabilities
// (SYS_ADMIN, NET_ADMIN, SETUID, SETGID, CHOWN, DAC_OVERRIDE, FOWNER), iproute2,
// util-linux and iptables, net.ipv4.ip_forward=1 and a default route. Run them
// in a container shaped like the Runner:
//
//	docker run --rm --cap-add SYS_ADMIN --cap-add NET_ADMIN --cap-add SETUID --cap-add SETGID \
//	  --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --sysctl net.ipv4.ip_forward=1 \
//	  -v $PWD:/src -w /src golang:1.25.11-bookworm sh -c \
//	  'apt-get update -qq && apt-get install -y -qq iproute2 iptables util-linux >/dev/null && go test ./internal/mcprunner -run Sandbox -v -count=1'
//
// or directly on a Linux host as root: sudo go test ./internal/mcprunner -run Sandbox -v -count=1
// Every test skips (with the reason) when its requirement is missing.

// TestSandboxHelperProcess is the program the tests run as an account's child.
// The test binary is both launcher and helper.
func TestSandboxHelperProcess(t *testing.T) {
	i := -1
	for n, a := range os.Args {
		if a == "--sandbox-helper" {
			i = n
		}
	}
	if i < 0 {
		return
	}
	args := os.Args[i+1:]
	switch args[0] {
	case "status": // prints the lines of /proc/self/status the tests judge
		data, _ := os.ReadFile("/proc/self/status")
		for _, line := range strings.Split(string(data), "\n") {
			for _, key := range []string{"Uid:", "Gid:", "Groups:", "CapInh:", "CapPrm:", "CapEff:", "CapAmb:", "NoNewPrivs:", "Seccomp:"} {
				if strings.HasPrefix(line, key) {
					fmt.Println(line)
				}
			}
		}
		cg, _ := os.ReadFile("/proc/self/cgroup")
		fmt.Println("cgroup", strings.TrimSpace(string(cg)))
	case "listen": // listen <network> <addr> <seconds>
		ln, err := net.Listen(args[1], args[2])
		if err != nil {
			fmt.Println("LISTEN-FAIL", err)
			os.Exit(1)
		}
		secs, _ := strconv.Atoi(args[3])
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte("hello-from-listener"))
				c.Close()
			}
		}()
		time.Sleep(time.Duration(secs) * time.Second)
	case "dial": // dial <network> <addr>
		c, err := net.DialTimeout(args[1], args[2], 2*time.Second)
		if err != nil {
			fmt.Println("DIAL-FAIL", err)
			os.Exit(3)
		}
		buf := make([]byte, 64)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := c.Read(buf)
		fmt.Printf("DIAL-OK %s\n", buf[:n])
	case "syscalls": // each of these must fail
		_, _, e1 := unix.Syscall(unix.SYS_UNSHARE, unix.CLONE_NEWUSER, 0, 0)
		_, _, e2 := unix.Syscall(unix.SYS_UNSHARE, unix.CLONE_NEWNET, 0, 0)
		e3 := unix.Mount("tmpfs", "/mnt", "tmpfs", 0, "")
		e4 := unix.Setuid(0)
		_, _, e5 := unix.Syscall6(unix.SYS_CLONE3, 0, 0, 0, 0, 0, 0)
		fmt.Println("unshare-user", e1, "unshare-net", e2, "mount", e3, "setuid0", e4, "clone3", e5)
	case "shm": // shm <create|attach> <key>
		key, _ := strconv.Atoi(args[2])
		flags := 0o600
		if args[1] == "create" {
			flags |= unix.IPC_CREAT
		}
		id, _, errno := unix.Syscall(unix.SYS_SHMGET, uintptr(key), 4096, uintptr(flags))
		if errno != 0 {
			fmt.Println("SHM-FAIL", errno)
			os.Exit(4)
		}
		fmt.Println("SHM-OK", id)
		if args[1] == "create" {
			time.Sleep(3 * time.Second)
		}
	case "alloc": // touch N MiB
		mib, _ := strconv.Atoi(args[1])
		buf := make([]byte, mib<<20)
		for i := 0; i < len(buf); i += 4096 {
			buf[i] = 1
		}
		fmt.Println("ALLOC-OK")
	case "fork": // start N sleepers; report how many started
		n, _ := strconv.Atoi(args[1])
		started := 0
		for i := 0; i < n; i++ {
			p, err := os.StartProcess("/bin/sleep", []string{"sleep", "5"}, &os.ProcAttr{})
			if err != nil {
				break
			}
			_ = p
			started++
		}
		fmt.Println("STARTED", started)
	case "write": // write <path>
		err := os.WriteFile(args[1], []byte("x"), 0o600)
		fmt.Println("WRITE", err)
	}
	os.Exit(0)
}

// sandboxFixture is a Hub with the real sandbox and two installed accounts.
type sandboxFixture struct {
	hub          *Hub
	root         string
	specA, specB Spec
	uidA, uidB   int
}

func newSandboxFixture(t *testing.T, mutate func(*SandboxConfig)) *sandboxFixture {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	cfg := SandboxConfig{Network: SandboxRequired, Cgroups: SandboxAuto, MemoryBytes: 96 << 20, Pids: 32, DiskBytes: 8 << 20}
	if mutate != nil {
		mutate(&cfg)
	}
	hub, root := func() (h *Hub, r string) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Skipf("sandbox unavailable here: %v", rec)
			}
		}()
		return newSandboxHub(t, cfg)
	}()
	f := &sandboxFixture{hub: hub, root: root}
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodPost, "/a/"+id+"/v1/plugins", tokenOf(id), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
			t.Fatalf("install %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	for _, id := range []string{accountA, accountB} {
		tenant, err := hub.tenant(id)
		if err != nil {
			t.Fatal(err)
		}
		if tenant.sandbox == nil {
			t.Fatal("the account has no sandbox")
		}
		spec, err := tenant.runner.gogSpec("gog")
		if err != nil {
			t.Fatal(err)
		}
		launcher, _ := launcherExecutable()
		spec.Command = launcher
		spec.SecretEnv = nil
		uid, _ := hub.reg.uid(id)
		if id == accountA {
			f.specA, f.uidA = spec, uid
		} else {
			f.specB, f.uidB = spec, uid
		}
	}
	return f
}

// newSandboxHub builds a Hub like the Runner does; a host that cannot offer the
// sandbox skips the test instead of failing it.
func newSandboxHub(t *testing.T, cfg SandboxConfig) (*Hub, string) {
	t.Helper()
	fakeGog(t)
	root := t.TempDir()
	shareTestBinary(t)
	traversableForAccounts(t, root)
	traversableForAccounts(t, filepath.Dir(gogBinary))
	if err := os.Chmod(gogBinary, 0755); err != nil {
		t.Fatal(err)
	}
	hub, err := NewHub(HubConfig{Root: root, Master: testMaster, MinFreeBytes: MinFreeDisabled, IsolateUIDs: true, Sandbox: cfg})
	if err != nil {
		t.Skipf("the sandbox is not available on this host: %v", err)
	}
	t.Cleanup(hub.Close)
	return hub, root
}

func helper(ctx context.Context, spec Spec, args ...string) (string, error) {
	all := append([]string{"-test.run=^TestSandboxHelperProcess$", "--", "--sandbox-helper"}, args...)
	out, err := runGog(ctx, spec, nil, all...)
	return strings.TrimSpace(string(out)), err
}

func mustHelper(t *testing.T, spec Spec, args ...string) string {
	t.Helper()
	out, err := helper(context.Background(), spec, args...)
	if err != nil {
		t.Fatalf("helper %v: %v (%s)", args, err, out)
	}
	return out
}

// Item 3: a plugin process has no capability, no_new_privs, a seccomp filter,
// its own uid and no group, and cannot create a namespace, mount or become root.
func TestSandboxPluginProcessKeepsNoPrivilege(t *testing.T) {
	f := newSandboxFixture(t, nil)
	out := mustHelper(t, f.specA, "status")
	for _, want := range []string{
		"Uid:\t" + strconv.Itoa(f.uidA) + "\t" + strconv.Itoa(f.uidA),
		"CapInh:\t0000000000000000", "CapPrm:\t0000000000000000", "CapEff:\t0000000000000000", "CapAmb:\t0000000000000000",
		"NoNewPrivs:\t1", "Seccomp:\t2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Groups:\t\n") && !strings.Contains(out, "Groups:\t") {
		t.Errorf("no Groups line:\n%s", out)
	}
	if line := lineWith(out, "Groups:"); strings.TrimSpace(strings.TrimPrefix(line, "Groups:")) != "" {
		t.Errorf("supplementary groups survive: %q", line)
	}
	out = mustHelper(t, f.specA, "syscalls")
	for _, want := range []string{"unshare-user operation not permitted", "unshare-net operation not permitted", "mount operation not permitted", "setuid0 operation not permitted", "clone3 function not implemented"} {
		if !strings.Contains(out, want) {
			t.Errorf("syscalls: want %q in %q", want, out)
		}
	}
}

func lineWith(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// Item 1: a listener of account A (TCP on loopback, on A's own address, a unix
// socket in the abstract namespace) is unreachable from account B.
func TestSandboxAccountsDoNotShareANetwork(t *testing.T) {
	f := newSandboxFixture(t, nil)
	_, guestA, _ := accountAddrs(f.uidA)
	hostA, _, _ := accountAddrs(f.uidA)
	listeners := []struct{ network, bind, dialA string }{
		{"tcp", "127.0.0.1:47001", "127.0.0.1:47001"},
		{"tcp", "0.0.0.0:47002", guestA.String() + ":47002"},
		{"unix", "@tofi-sandbox-test", "@tofi-sandbox-test"},
	}
	for _, l := range listeners {
		l := l
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = helper(context.Background(), f.specA, "listen", l.network, l.bind, "6")
		}()
		deadline := time.Now().Add(4 * time.Second)
		for {
			if out, err := helper(context.Background(), f.specA, "dial", l.network, l.dialA); err == nil && strings.Contains(out, "hello-from-listener") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("A's own listener %s %s never answered A", l.network, l.bind)
			}
			time.Sleep(100 * time.Millisecond)
		}
		// B tries the same address, and A's address on the veth, and the Runner end of A's link.
		for _, target := range []string{l.dialA, guestA.String() + ":47002", hostA.String() + ":47002"} {
			network := "tcp"
			if l.network == "unix" {
				network = "unix"
				if target != l.dialA {
					continue
				}
			}
			if out, err := helper(context.Background(), f.specB, "dial", network, target); err == nil || strings.Contains(out, "DIAL-OK") {
				t.Errorf("account B reached A's %s listener at %s: %q", l.network, target, out)
			}
		}
		<-done
	}
	// B has its own loopback and its own resolver address, and no route to A's /30.
	if out := mustHelper(t, f.specB, "listen", "tcp", "127.0.0.1:47001", "0"); strings.Contains(out, "LISTEN-FAIL") {
		t.Errorf("B cannot use the port A used on its own loopback: %s", out)
	}
	if out, err := helper(context.Background(), f.specB, "dial", "tcp", "127.0.0.11:53"); err != nil {
		t.Errorf("B's DNS forwarder is not listening: %v %s", err, out)
	}
}

// IPC keys are per process namespace: A's segment does not exist for B.
func TestSandboxAccountsDoNotShareSysVIPC(t *testing.T) {
	f := newSandboxFixture(t, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = helper(context.Background(), f.specA, "shm", "create", "424242")
	}()
	time.Sleep(time.Second)
	if out, err := helper(context.Background(), f.specB, "shm", "attach", "424242"); err == nil {
		t.Errorf("B attached A's shared memory: %s", out)
	}
	<-done
}

// Item 1, filesystem part: each account has its own writable area and cannot
// write in the other's, in /tmp, or in the Runner's state.
func TestSandboxAccountsShareNoWritablePath(t *testing.T) {
	f := newSandboxFixture(t, nil)
	own := filepath.Join(f.root, "accounts", accountA, "tmp", "mine")
	if out := mustHelper(t, f.specA, "write", own); !strings.Contains(out, "WRITE <nil>") {
		t.Fatalf("A cannot write its own tmp: %s", out)
	}
	for _, path := range []string{
		filepath.Join(f.root, "accounts", accountA, "tmp", "planted-by-b"),
		filepath.Join(f.root, "accounts", accountB, "state", "planted"),
		filepath.Join(f.root, "uids.json"),
	} {
		target := path
		if strings.Contains(path, accountA) {
			// B writing into A's tmp
			if out := mustHelper(t, f.specB, "write", target); strings.Contains(out, "WRITE <nil>") {
				t.Errorf("B wrote %s", target)
			}
			continue
		}
		if out := mustHelper(t, f.specA, "write", target); strings.Contains(out, "WRITE <nil>") {
			t.Errorf("A wrote %s", target)
		}
	}
}

// Item 2: the account's processes are in a cgroup with the configured limits,
// a plugin that allocates past the limit is killed alone, and a fork bomb stops
// at pids.max.
func TestSandboxMemoryAndPidsAreCappedPerAccount(t *testing.T) {
	f := newSandboxFixture(t, nil)
	if f.hub.sbx == nil || f.hub.sbx.cg == nil {
		t.Skip("cgroup v2 is not writable here (see the README: AppArmor/read-only cgroup view)")
	}
	out := mustHelper(t, f.specA, "status")
	if want := "/tofi-accounts/" + strconv.Itoa(f.uidA); !strings.Contains(out, want) {
		t.Fatalf("A's child is not in its account cgroup (%s):\n%s", want, out)
	}
	read := func(uid int, file string) string {
		data, err := os.ReadFile(filepath.Join(f.hub.sbx.cg.accounts, strconv.Itoa(uid), file))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	if got := read(f.uidA, "memory.max"); got != strconv.Itoa(96<<20) {
		t.Errorf("memory.max = %s", got)
	}
	if got := read(f.uidA, "pids.max"); got != "32" {
		t.Errorf("pids.max = %s", got)
	}
	// 256 MiB against a 96 MiB limit: the helper is killed, the Runner and B are not.
	if out, err := helper(context.Background(), f.specA, "alloc", "256"); err == nil || strings.Contains(out, "ALLOC-OK") {
		t.Errorf("A allocated past its memory limit: %v %s", err, out)
	}
	if out := mustHelper(t, f.specB, "alloc", "32"); !strings.Contains(out, "ALLOC-OK") {
		t.Errorf("B could not allocate within its own limit after A's overrun: %s", out)
	}
	// 200 sleepers against pids.max=32 (rlimit 128 is higher, so the cgroup is what stops it).
	out = mustHelper(t, f.specA, "fork", "200")
	n, _ := strconv.Atoi(strings.TrimPrefix(lineWith(out, "STARTED"), "STARTED "))
	if n == 0 || n >= 32 {
		t.Errorf("fork bomb started %d processes against pids.max 32: %s", n, out)
	}
	if out := mustHelper(t, f.specB, "fork", "10"); !strings.Contains(out, "STARTED 10") {
		t.Errorf("B was starved by A's fork bomb: %s", out)
	}
}

// Item 2, disk: a child that fills its tmp is cleared by the sweep and the other
// account is not affected.
func TestSandboxDiskQuotaClearsARunawayTmp(t *testing.T) {
	f := newSandboxFixture(t, func(c *SandboxConfig) { c.DiskBytes = 1 << 20 })
	tmpA := filepath.Join(f.root, "accounts", accountA, "tmp")
	big := make([]byte, 3<<20)
	if err := os.WriteFile(filepath.Join(tmpA, "fill"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if over := f.hub.sweepQuota(); len(over) != 1 || over[0] != accountA {
		t.Fatalf("sweep: %v", over)
	}
	if _, err := os.Stat(filepath.Join(tmpA, "fill")); !os.IsNotExist(err) {
		t.Fatal("the runaway file survived")
	}
	if out := mustHelper(t, f.specB, "write", filepath.Join(f.root, "accounts", accountB, "tmp", "ok")); !strings.Contains(out, "WRITE <nil>") {
		t.Fatalf("B affected: %s", out)
	}
}

// The sandbox is torn down with the account: no namespace holder, veth or cgroup is left.
func TestSandboxIsRemovedWhenTheAccountClosesOrIsPurged(t *testing.T) {
	f := newSandboxFixture(t, nil)
	hv, _ := vethNames(f.uidA)
	if _, err := os.Stat("/sys/class/net/" + hv); err != nil {
		t.Fatalf("the veth of an open account is missing: %v", err)
	}
	if err := f.hub.Purge(accountA); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/sys/class/net/" + hv); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat("/sys/class/net/" + hv); !os.IsNotExist(err) {
		t.Fatal("the veth of a purged account is still there")
	}
	if f.hub.sbx.cg != nil {
		if _, err := os.Stat(filepath.Join(f.hub.sbx.cg.accounts, strconv.Itoa(f.uidA))); !os.IsNotExist(err) {
			t.Fatal("the cgroup of a purged account is still there")
		}
	}
}
