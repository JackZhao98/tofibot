package mcprunner

// A small seccomp-bpf assembler for the sandbox launcher. The container runtime's
// default profile (Docker's) is the first filter and is kept; this one is added
// on top, for the plugin process only. It matters because the Runner container
// holds CAP_SYS_ADMIN, and a runtime derives its default profile from the
// container's capabilities: with CAP_SYS_ADMIN, unshare(2), setns(2) and mount(2)
// are allowed in that profile for every process, a plugin included. A plugin
// that could create a user namespace would hold every capability inside it.

const (
	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20
	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfK   = 0x00
	bpfRET = 0x06
	bpfAND = 0x50 // BPF_ALU|BPF_AND
	bpfALU = 0x04

	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000

	seccompDataNr   = 0
	seccompDataArch = 4
	seccompDataArg0 = 16
)

// bpfIns is one classic BPF instruction.
type bpfIns struct {
	Code   uint16
	Jt, Jf uint8
	K      uint32
}

// seccompPolicy describes the filter. Numbers are for one architecture.
type seccompPolicy struct {
	Arch uint32 // AUDIT_ARCH_*; a call from any other ABI is refused
	// Denied syscalls fail with EPERM.
	Denied []uint32
	// NoSys fail with ENOSYS (clone3: libc then falls back to clone, whose flags
	// the filter can read).
	NoSys []uint32
	// Clone is the clone(2) number; a call that asks for any namespace in
	// CloneNamespaceMask fails with EPERM. Threads and processes are unaffected.
	Clone uint32
}

// CLONE_NEWNS|NEWCGROUP|NEWUTS|NEWIPC|NEWUSER|NEWPID|NEWNET
const cloneNamespaceMask = 0x00020000 | 0x02000000 | 0x04000000 | 0x08000000 | 0x10000000 | 0x20000000 | 0x40000000

const (
	errnoEPERM  = 1
	errnoENOSYS = 38
)

// build assembles the program: allow everything not listed.
func (p seccompPolicy) build() []bpfIns {
	type ref struct {
		at    int
		label string
	}
	var prog []bpfIns
	var refs []ref
	labels := map[string]int{}
	emit := func(i bpfIns) { prog = append(prog, i) }
	jump := func(k uint32, label string) { // if A == k goto label, else fall through
		refs = append(refs, ref{at: len(prog), label: label})
		emit(bpfIns{Code: bpfJMP | bpfJEQ | bpfK, K: k})
	}
	// Architecture first: a 32-bit call on a 64-bit kernel has other numbers.
	emit(bpfIns{Code: bpfLD | bpfW | bpfABS, K: seccompDataArch})
	refs = append(refs, ref{at: len(prog), label: "ok-arch"})
	emit(bpfIns{Code: bpfJMP | bpfJEQ | bpfK, K: p.Arch})
	emit(bpfIns{Code: bpfRET | bpfK, K: seccompRetErrno | errnoEPERM})
	labels["ok-arch"] = len(prog)
	emit(bpfIns{Code: bpfLD | bpfW | bpfABS, K: seccompDataNr})
	for _, nr := range p.Denied {
		jump(nr, "eperm")
	}
	for _, nr := range p.NoSys {
		jump(nr, "enosys")
	}
	if p.Clone != 0 {
		jump(p.Clone, "clone")
	}
	emit(bpfIns{Code: bpfRET | bpfK, K: seccompRetAllow})
	labels["clone"] = len(prog)
	emit(bpfIns{Code: bpfLD | bpfW | bpfABS, K: seccompDataArg0})
	emit(bpfIns{Code: bpfALU | bpfAND | bpfK, K: cloneNamespaceMask})
	refs = append(refs, ref{at: len(prog), label: "allow"})
	// A == 0 means no namespace flag: jump to allow; otherwise fall through to EPERM.
	emit(bpfIns{Code: bpfJMP | bpfJEQ | bpfK, K: 0})
	labels["eperm"] = len(prog)
	emit(bpfIns{Code: bpfRET | bpfK, K: seccompRetErrno | errnoEPERM})
	labels["enosys"] = len(prog)
	emit(bpfIns{Code: bpfRET | bpfK, K: seccompRetErrno | errnoENOSYS})
	labels["allow"] = len(prog)
	emit(bpfIns{Code: bpfRET | bpfK, K: seccompRetAllow})
	for _, r := range refs {
		prog[r.at].Jt = uint8(labels[r.label] - r.at - 1)
	}
	return prog
}
