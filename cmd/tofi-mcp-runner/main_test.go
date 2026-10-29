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

func TestServerBoundsSlowClients(t *testing.T) {
	srv := newServer("127.0.0.1:0", nil)
	if srv.ReadHeaderTimeout <= 0 || srv.ReadTimeout <= 0 || srv.IdleTimeout <= 0 {
		t.Fatalf("a slow client can hold a connection forever: %+v", srv)
	}
	if srv.WriteTimeout != 0 {
		t.Fatal("a write timeout would cut long MCP streams")
	}
}
