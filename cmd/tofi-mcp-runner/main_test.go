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

func TestSandboxDefaultsRequireNetworkNamespacesAndCapAccounts(t *testing.T) {
	for _, name := range []string{"TOFI_MCP_RUNNER_NETNS", "TOFI_MCP_RUNNER_CGROUPS", "TOFI_MCP_RUNNER_ACCOUNT_MEMORY_MB", "TOFI_MCP_RUNNER_ACCOUNT_PIDS", "TOFI_MCP_RUNNER_ACCOUNT_DISK_MB", "TOFI_MCP_RUNNER_REQUIRE_SECCOMP"} {
		t.Setenv(name, "")
	}
	cfg, err := sandboxFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Network != "required" || cfg.Cgroups != "auto" || cfg.MemoryBytes == 0 || cfg.Pids == 0 || cfg.DiskBytes == 0 {
		t.Fatalf("defaults: %+v", cfg)
	}
	t.Setenv("TOFI_MCP_RUNNER_ACCOUNT_MEMORY_MB", "64")
	t.Setenv("TOFI_MCP_RUNNER_ACCOUNT_DISK_MB", "0")
	t.Setenv("TOFI_MCP_RUNNER_NETNS", "off")
	cfg, err = sandboxFromEnv()
	if err != nil || cfg.MemoryBytes != 64<<20 || cfg.DiskBytes != 0 || cfg.Network != "off" {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
	t.Setenv("TOFI_MCP_RUNNER_NETNS", "maybe")
	if _, err := sandboxFromEnv(); err == nil {
		t.Fatal("a bad mode was accepted")
	}
}
