//go:build linux && !amd64 && !arm64

package mcprunner

// No hardening filter is defined for this architecture; the launcher refuses to
// start a plugin without one.
const seccompAuditArch = 0

// seccompDenyX32: the x32 ABI exists only on x86_64.
const seccompDenyX32 = false
