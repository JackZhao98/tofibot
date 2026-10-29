//go:build !linux

package mcprunner

import (
	"errors"
	"os"
)

// The sandbox needs Linux namespaces, cgroups and seccomp.

type sandboxHost struct{}

func newSandboxHost(cfg SandboxConfig) (*sandboxHost, error) {
	err := errors.New("only Linux has namespaces, cgroups and seccomp")
	for _, mode := range []SandboxMode{cfg.Network, cfg.Cgroups} {
		if uerr := unavailable(mode, "the account sandbox", err); uerr != nil {
			return nil, uerr
		}
	}
	return nil, nil
}

func (h *sandboxHost) openAccount(uid, gid int) (*accountSandbox, error) {
	return nil, errors.New("the account sandbox needs Linux")
}

func runSandboxLauncher(argv []string) {
	os.Stderr.WriteString("sandbox launcher: needs Linux\n")
	os.Exit(125)
}
