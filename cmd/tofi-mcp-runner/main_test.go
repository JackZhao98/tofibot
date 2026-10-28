package main

import (
	"os"
	"strings"
	"testing"
)

func TestAccountsModeRefusesToShareOneUIDUnlessAskedByName(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: isolation is available")
	}
	t.Setenv("TOFI_MCP_RUNNER_ALLOW_SHARED_UID", "")
	t.Setenv("TOFI_MCP_RUNNER_ROOT", t.TempDir())
	err := serveAccounts()
	if err == nil || !strings.Contains(err.Error(), "must run as root") {
		t.Fatalf("started without per-account uids: %v", err)
	}
}
