//go:build linux

package mcprunner

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ---- the launcher: root-only setup, then drop everything ----

// runSandboxLauncher is the plugin side of confine. It runs as root with the
// Runner's capabilities, joins the account's cgroup and network namespace, then
// gives up every privilege and replaces itself with the plugin. It never returns.
//
// Order matters: joins first (they need CAP_SYS_ADMIN), then no_new_privs, then
// the uid change (which clears the permitted and effective sets), then an
// explicit capability clear and a check that root cannot come back, then the
// hardening seccomp filter, then exec.
func runSandboxLauncher(argv []string) {
	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "sandbox launcher: "+format+"\n", a...)
		os.Exit(125)
	}
	if len(argv) < 5 || argv[3] != "--" {
		fail("invalid arguments")
	}
	var plan launchPlan
	if err := json.Unmarshal([]byte(argv[2]), &plan); err != nil || plan.Uid <= 0 || plan.Gid <= 0 {
		fail("invalid plan")
	}
	if os.Geteuid() != 0 {
		fail("must start as root")
	}
	// Namespace changes are per thread; the exec at the end must run on this one.
	runtime.LockOSThread()
	if plan.CgroupProcs != "" {
		f, err := os.OpenFile(plan.CgroupProcs, os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString(strconv.Itoa(os.Getpid()))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			fail("cannot join the account cgroup: %v", err)
		}
	}
	if plan.NetNS != "" {
		f, err := os.Open(plan.NetNS)
		if err != nil {
			fail("cannot open the account network namespace: %v", err)
		}
		err = unix.Setns(int(f.Fd()), unix.CLONE_NEWNET)
		f.Close()
		if err != nil {
			fail("cannot join the account network namespace: %v", err)
		}
	}
	// SysV IPC and POSIX message queues: a namespace of their own per process.
	if err := unix.Unshare(unix.CLONE_NEWIPC); err != nil {
		fail("cannot unshare the IPC namespace: %v", err)
	}
	if err := applyLimits(Limits{Nproc: plan.Nproc, Nofile: plan.Nofile, Fsize: plan.Fsize}); err != nil {
		fail("cannot set resource limits: %v", err)
	}
	if err := dropToAccount(plan.Uid, plan.Gid); err != nil {
		fail("%v", err)
	}
	if plan.RequireFilter || seccompAuditArch != 0 {
		if err := installHardeningFilter(); err != nil {
			fail("cannot install the seccomp filter: %v", err)
		}
	}
	path, err := exec.LookPath(argv[4])
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher:", err)
		os.Exit(127)
	}
	err = syscall.Exec(path, argv[4:], os.Environ())
	fmt.Fprintln(os.Stderr, "launcher:", err)
	os.Exit(126)
}

// dropToAccount becomes uid/gid with no supplementary group, no capability in
// any set, no ambient capability and no_new_privs, and proves it.
func dropToAccount(uid, gid int) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %w", err)
	}
	if err := unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0); err != nil {
		return fmt.Errorf("clearing ambient capabilities: %w", err)
	}
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("setresgid: %w", err)
	}
	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("setresuid: %w", err)
	}
	var hdr = unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capset(&hdr, &data[0]); err != nil {
		return fmt.Errorf("clearing capabilities: %w", err)
	}
	// Proof, not trust: nothing is left and root cannot come back.
	hdr = unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	if err := unix.Capget(&hdr, &data[0]); err != nil {
		return fmt.Errorf("reading capabilities: %w", err)
	}
	for _, d := range data {
		if d.Effective != 0 || d.Permitted != 0 || d.Inheritable != 0 {
			return errors.New("capabilities remain after the uid change")
		}
	}
	if v, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0); err != nil || v != 1 {
		return errors.New("no_new_privs is not set")
	}
	if syscall.Setuid(0) == nil || syscall.Setresuid(0, 0, 0) == nil {
		return errors.New("root can be regained")
	}
	return nil
}

// hardeningDenied are the syscalls the plugin may not make, by name so that a
// kernel or architecture that lacks one is simply not listed.
func hardeningPolicy() seccompPolicy {
	return seccompPolicy{
		Arch: seccompAuditArch,
		Denied: []uint32{
			unix.SYS_UNSHARE, unix.SYS_SETNS, unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_PIVOT_ROOT, unix.SYS_CHROOT,
			unix.SYS_MOUNT_SETATTR, unix.SYS_MOVE_MOUNT, unix.SYS_OPEN_TREE, unix.SYS_FSOPEN, unix.SYS_FSCONFIG, unix.SYS_FSMOUNT, unix.SYS_FSPICK,
			unix.SYS_KEYCTL, unix.SYS_ADD_KEY, unix.SYS_REQUEST_KEY, unix.SYS_BPF, unix.SYS_PERF_EVENT_OPEN, unix.SYS_USERFAULTFD,
			unix.SYS_IO_URING_SETUP, unix.SYS_IO_URING_ENTER, unix.SYS_IO_URING_REGISTER,
			unix.SYS_KEXEC_LOAD, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT, unix.SYS_SWAPON, unix.SYS_SWAPOFF,
		},
		NoSys:   []uint32{unix.SYS_CLONE3},
		Clone:   unix.SYS_CLONE,
		DenyX32: seccompDenyX32,
	}
}

func installHardeningFilter() error {
	policy := hardeningPolicy()
	if policy.Arch == 0 {
		return errors.New("no filter is defined for this architecture")
	}
	prog := policy.build()
	filters := make([]unix.SockFilter, len(prog))
	for i, in := range prog {
		filters[i] = unix.SockFilter{Code: in.Code, Jt: in.Jt, Jf: in.Jf, K: in.K}
	}
	fprog := unix.SockFprog{Len: uint16(len(filters)), Filter: &filters[0]}
	// TSYNC: every thread of the launcher carries the filter, not only this one.
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&fprog)))
	if errno != 0 {
		return errno
	}
	return nil
}

// ---- the host side ----

type sandboxHost struct {
	cfg SandboxConfig
	cg  *cgroupTree
	net *netHost
}

func newSandboxHost(cfg SandboxConfig) (*sandboxHost, error) {
	h := &sandboxHost{cfg: cfg}
	if cfg.RequireSeccomp {
		if mode, err := seccompModeOfSelf(); err != nil || mode == 0 {
			return nil, fmt.Errorf("sandbox: the Runner container runs without a seccomp filter (mode %d, %v); start it with the runtime's default profile", mode, err)
		}
	} else if mode, err := seccompModeOfSelf(); err == nil && mode == 0 {
		log.Printf("runner: SECURITY: the Runner container runs without a seccomp filter; plugins still get the launcher's own filter")
	}
	if cfg.Cgroups != SandboxOff {
		cg, err := newCgroupTree()
		if err := unavailable(cfg.Cgroups, "per-account cgroups", err); err != nil {
			return nil, err
		}
		if err == nil {
			h.cg = cg
		}
	}
	if cfg.Network != SandboxOff {
		n, err := newNetHost(cfg)
		if err := unavailable(cfg.Network, "per-account network namespaces", err); err != nil {
			return nil, err
		}
		if err == nil {
			h.net = n
		}
	}
	return h, nil
}

func seccompModeOfSelf() (int, error) {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "Seccomp:"); ok {
			return strconv.Atoi(strings.TrimSpace(rest))
		}
	}
	return 0, errors.New("no Seccomp line in /proc/self/status")
}

// openAccount builds the sandbox of one account. Nothing is left behind on failure.
func (h *sandboxHost) openAccount(uid, gid int) (*accountSandbox, error) {
	s := &accountSandbox{host: h, uid: uid, gid: gid}
	if h.cg != nil {
		procs, kill, closeCG, err := h.cg.open(uid, h.cfg.MemoryBytes, h.cfg.Pids)
		if err != nil {
			if uerr := unavailable(h.cfg.Cgroups, "the account cgroup", err); uerr != nil {
				return nil, uerr
			}
		} else {
			s.cgroupProcs, s.killFn = procs, kill
			s.closers = append(s.closers, closeCG)
		}
	}
	if h.net != nil {
		ns, closeNet, err := h.net.open(uid)
		if err != nil {
			s.Close()
			return nil, fmt.Errorf("account network: %w", err)
		}
		s.netns = ns
		s.closers = append(s.closers, closeNet)
	}
	return s, nil
}

// ---- cgroups (v2) ----

const cgroupMount = "/sys/fs/cgroup"

type cgroupTree struct {
	accounts string
}

// newCgroupTree prepares the delegated subtree: the Runner's own processes move
// into a leaf (cgroup v2 allows controllers below a cgroup only when it holds no
// process of its own) and the memory and pids controllers are enabled for the
// accounts below it.
func newCgroupTree() (*cgroupTree, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(cgroupMount, &st); err != nil {
		return nil, err
	}
	if st.Type != unix.CGROUP2_SUPER_MAGIC {
		return nil, errors.New("cgroup v2 is not mounted at " + cgroupMount)
	}
	base := cgroupMount
	if own, err := ownCgroup(); err == nil && own != "/" {
		base = filepath.Join(cgroupMount, own)
	}
	core := filepath.Join(base, "tofi-core")
	err := os.Mkdir(core, 0o755)
	if errors.Is(err, syscall.EROFS) {
		// Docker mounts the cgroup tree read-only; with CAP_SYS_ADMIN the Runner can
		// remount its own (namespaced) view read-write. A host policy that forbids
		// mount (an AppArmor profile) makes this fail, and the error says so.
		if merr := unix.Mount("", cgroupMount, "", unix.MS_REMOUNT|unix.MS_BIND, ""); merr != nil {
			return nil, fmt.Errorf("the cgroup tree is read-only and cannot be remounted: %w", merr)
		}
		err = os.Mkdir(core, 0o755)
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	pids, _ := os.ReadFile(filepath.Join(base, "cgroup.procs"))
	for _, pid := range strings.Fields(string(pids)) {
		_ = os.WriteFile(filepath.Join(core, "cgroup.procs"), []byte(pid), 0)
	}
	if err := os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte("+memory +pids"), 0); err != nil {
		return nil, fmt.Errorf("cannot delegate the memory and pids controllers: %w", err)
	}
	accounts := filepath.Join(base, "tofi-accounts")
	if err := os.Mkdir(accounts, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(accounts, "cgroup.subtree_control"), []byte("+memory +pids"), 0); err != nil {
		return nil, fmt.Errorf("cannot enable the controllers for accounts: %w", err)
	}
	return &cgroupTree{accounts: accounts}, nil
}

func ownCgroup() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Clean("/" + strings.TrimSpace(rest)), nil
		}
	}
	return "", errors.New("no cgroup v2 entry")
}

// open creates the account's cgroup with its limits. It returns the file the
// launcher writes its pid to, a function that kills every process in it, and a
// function that removes it.
func (t *cgroupTree) open(uid int, memory, pids uint64) (procs string, kill, closeFn func(), err error) {
	dir := filepath.Join(t.accounts, strconv.Itoa(uid))
	if err = os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", nil, nil, err
	}
	set := func(file, value string) error { return os.WriteFile(filepath.Join(dir, file), []byte(value), 0) }
	if err = set("memory.max", strconv.FormatUint(memory, 10)); err != nil {
		_ = os.Remove(dir)
		return "", nil, nil, fmt.Errorf("memory.max: %w", err)
	}
	if err = set("pids.max", strconv.FormatUint(pids, 10)); err != nil {
		_ = os.Remove(dir)
		return "", nil, nil, fmt.Errorf("pids.max: %w", err)
	}
	_ = set("memory.swap.max", "0")
	_ = set("memory.oom.group", "1") // an OOM kills the account's plugins together, not the Runner
	kill = func() { _ = set("cgroup.kill", "1") }
	closeFn = func() {
		kill()
		for i := 0; i < 100; i++ {
			if err := os.Remove(dir); err == nil || errors.Is(err, os.ErrNotExist) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		log.Printf("runner: cgroup %s could not be removed", dir)
	}
	return filepath.Join(dir, "cgroup.procs"), kill, closeFn, nil
}

// ---- network namespaces ----

type netHost struct {
	egress   string
	upstream string // DNS server the forwarders ask
}

func newNetHost(cfg SandboxConfig) (*netHost, error) {
	for _, tool := range []string{"ip", "unshare", "iptables"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("%s is not installed in the Runner image", tool)
		}
	}
	if data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err != nil || strings.TrimSpace(string(data)) != "1" {
		return nil, errors.New("net.ipv4.ip_forward is not 1 (set sysctls: net.ipv4.ip_forward=1 on the Runner container)")
	}
	egress := cfg.EgressInterface
	if egress == "" {
		var err error
		if egress, err = defaultRouteInterface(); err != nil {
			return nil, err
		}
	}
	upstream, err := resolvConfNameserver("/etc/resolv.conf")
	if err != nil {
		return nil, err
	}
	n := &netHost{egress: egress, upstream: upstream}
	if err := n.applyRules(); err != nil {
		return nil, err
	}
	return n, nil
}

func (n *netHost) iptables(args ...string) error {
	out, err := exec.Command("iptables", append([]string{"-w", "5"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables %s: %w: %.200s", strings.Join(args, " "), err, out)
	}
	return nil
}

func (n *netHost) applyRules() error {
	forward, input, nat := accountRules(n.egress)
	const fwd, in = "TOFI-ACCOUNTS", "TOFI-ACCOUNTS-IN"
	for _, chain := range []string{fwd, in} {
		_ = n.iptables("-N", chain)
		if err := n.iptables("-F", chain); err != nil {
			return err
		}
	}
	for _, r := range forward {
		if err := n.iptables(append([]string{"-A", fwd}, r...)...); err != nil {
			return err
		}
	}
	for _, r := range input {
		if err := n.iptables(append([]string{"-A", in}, r...)...); err != nil {
			return err
		}
	}
	for _, hook := range [][]string{{"FORWARD", fwd}, {"INPUT", in}} {
		if n.iptables("-C", hook[0], "-j", hook[1]) != nil {
			if err := n.iptables("-I", hook[0], "1", "-j", hook[1]); err != nil {
				return err
			}
		}
	}
	for _, r := range nat {
		if n.iptables(append([]string{"-t", "nat", "-C", "POSTROUTING"}, r...)...) != nil {
			if err := n.iptables(append([]string{"-t", "nat", "-A", "POSTROUTING"}, r...)...); err != nil {
				return err
			}
		}
	}
	// IPv6 gets no route out of an account (no address is configured); this only closes the Runner side.
	if _, err := exec.LookPath("ip6tables"); err == nil {
		_ = exec.Command("ip6tables", "-w", "5", "-I", "INPUT", "1", "-i", "tv+", "-j", "DROP").Run()
	}
	return nil
}

func defaultRouteInterface() (string, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 8 && fields[1] == "00000000" && fields[7] == "00000000" {
			return fields[0], nil
		}
	}
	return "", errors.New("the Runner has no default route, so plugins would have no way out")
}

func resolvConfNameserver(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			return net.JoinHostPort(fields[1], "53"), nil
		}
	}
	return "", errors.New("no nameserver in " + path)
}

func runTool(timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %.200s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

// open creates the account's network namespace: a process that owns it (it only
// waits), a veth pair to the Runner, addresses, routes, and a DNS forwarder on
// 127.0.0.11 inside the namespace, where the container's resolv.conf points.
func (n *netHost) open(uid int) (netns string, closeFn func(), err error) {
	hostIP, guestIP, err := accountAddrs(uid)
	if err != nil {
		return "", nil, err
	}
	hv, pv := vethNames(uid)
	holder := exec.Command("unshare", "--net", "--", "/bin/sh", "-c", "ip link set lo up; echo ready; exec cat >/dev/null")
	stdin, err := holder.StdinPipe()
	if err != nil {
		return "", nil, err
	}
	stdout, err := holder.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	if err := holder.Start(); err != nil {
		return "", nil, err
	}
	var stops []func()
	cleanup := func() {
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
		_ = stdin.Close()
		_ = holder.Process.Kill()
		_ = holder.Wait()
		_ = exec.Command("ip", "link", "del", hv).Run() // gone with the namespace; this covers a half-built pair
	}
	ready := make(chan error, 1)
	go func() {
		line, rerr := bufio.NewReader(stdout).ReadString('\n')
		if rerr != nil || strings.TrimSpace(line) != "ready" {
			ready <- fmt.Errorf("namespace holder did not start: %v %q", rerr, line)
			return
		}
		ready <- nil
	}()
	select {
	case err := <-ready:
		if err != nil {
			cleanup()
			return "", nil, err
		}
	case <-time.After(10 * time.Second):
		cleanup()
		return "", nil, errors.New("namespace holder timed out")
	}
	ns := fmt.Sprintf("/proc/%d/ns/net", holder.Process.Pid)
	pid := strconv.Itoa(holder.Process.Pid)
	steps := [][]string{
		{"ip", "link", "add", hv, "type", "veth", "peer", "name", pv},
		{"ip", "link", "set", pv, "netns", pid},
		{"ip", "link", "set", hv, "addrgenmode", "none"}, // no IPv6 link-local address on the Runner side
		{"ip", "addr", "add", hostIP.String() + "/30", "dev", hv},
		{"ip", "link", "set", hv, "up"},
		{"nsenter", "--net=" + ns, "--", "ip", "link", "set", pv, "name", "eth0"},
		{"nsenter", "--net=" + ns, "--", "ip", "link", "set", "eth0", "addrgenmode", "none"},
		{"nsenter", "--net=" + ns, "--", "ip", "addr", "add", guestIP.String() + "/30", "dev", "eth0"},
		{"nsenter", "--net=" + ns, "--", "ip", "link", "set", "eth0", "up"},
		{"nsenter", "--net=" + ns, "--", "ip", "route", "add", "default", "via", hostIP.String()},
	}
	for _, step := range steps {
		if err := runTool(10*time.Second, step[0], step[1:]...); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	dns, err := startDNSForwarder(ns, n.upstream)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("DNS forwarder: %w", err)
	}
	stops = append(stops, dns)
	return ns, cleanup, nil
}

// ---- DNS inside the namespace ----

// inNetns runs fn on a thread that has joined the namespace and puts the thread
// back afterwards. A thread that cannot be put back stays locked, so Go ends it
// instead of reusing it.
func inNetns(path string, fn func() error) (err error) {
	runtime.LockOSThread()
	orig, err := os.Open(fmt.Sprintf("/proc/self/task/%d/ns/net", unix.Gettid()))
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer orig.Close()
	target, err := os.Open(path)
	if err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer target.Close()
	if err := unix.Setns(int(target.Fd()), unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		return err
	}
	defer func() {
		if rerr := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); rerr != nil {
			if err == nil {
				err = rerr
			}
			return // stay locked
		}
		runtime.UnlockOSThread()
	}()
	return fn()
}

// startDNSForwarder listens on 127.0.0.11:53 (UDP and TCP) in the namespace and
// forwards to upstream from the Runner's own namespace. The container's
// resolv.conf names 127.0.0.11, so plugins resolve names unchanged, and the
// account's namespace needs no route to any resolver.
func startDNSForwarder(ns, upstream string) (stop func(), err error) {
	var udp net.PacketConn
	var tcp net.Listener
	err = inNetns(ns, func() error {
		var lerr error
		if udp, lerr = net.ListenPacket("udp4", "127.0.0.11:53"); lerr != nil {
			return lerr
		}
		if tcp, lerr = net.Listen("tcp4", "127.0.0.11:53"); lerr != nil {
			udp.Close()
			return lerr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	limit := newDNSLimiter(maxDNSInFlight)
	wg.Add(2)
	go func() {
		defer wg.Done()
		serveDNSUDP(udp, upstream, limit)
	}()
	go func() {
		defer wg.Done()
		for {
			conn, aerr := tcp.Accept()
			if aerr != nil {
				return
			}
			if !limit.acquire() {
				conn.Close()
				continue
			}
			go func() {
				defer limit.release()
				forwardDNSOverTCP(conn, upstream)
			}()
		}
	}()
	return func() { udp.Close(); tcp.Close(); wg.Wait() }, nil
}

func forwardDNSOverTCP(client net.Conn, upstream string) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(10 * time.Second))
	var size [2]byte
	if _, err := io.ReadFull(client, size[:]); err != nil {
		return
	}
	query := make([]byte, binary.BigEndian.Uint16(size[:]))
	if _, err := io.ReadFull(client, query); err != nil {
		return
	}
	conn, err := net.DialTimeout("tcp", upstream, 3*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(append(size[:], query...)); err != nil {
		return
	}
	_, _ = io.Copy(client, io.LimitReader(conn, 66000))
}
