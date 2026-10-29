//go:build linux && amd64

package mcprunner

const seccompAuditArch = 0xC000003E // AUDIT_ARCH_X86_64

// seccompDenyX32: the x32 ABI exists only on x86_64.
const seccompDenyX32 = true
