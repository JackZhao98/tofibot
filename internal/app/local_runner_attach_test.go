package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

func noComputerRunnerServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	srv, _, _ := runnerHub(t)
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, MCPConfigPath: filepath.Join(dir, "mcp.json"), IsolatedWorkspace: true,
		LocalRunnerURL: srv.URL + mcprunner.AccountPathPrefix + runnerAccountA, LocalRunnerTokenFunc: tokenFn(runnerAccountA)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir, srv.URL + "/a/" + runnerAccountA
}

// An account without a computer is served by the container. Attaching a VM
// later must not send its local connections to the empty guest Runner.
func TestAttachingAComputerKeepsConnectionsOnTheContainer(t *testing.T) {
	s, _, want := noComputerRunnerServer(t)
	s.computerFactory = func() (computer.Provider, error) { return newFakeComputer("firecracker"), nil }
	if base, _, err := s.localRunnerConfig(); err != nil || base != want {
		t.Fatalf("before attach: %q %v", base, err)
	}
	// Start-up has not recorded the state yet (a hot attach can come first).
	_ = os.Remove(s.runnerMigration.path)
	s.runnerMigration.switched.Store(false)
	if err := s.AttachVM(context.Background()); err != nil {
		t.Fatal(err)
	}
	if base, token, err := s.localRunnerConfig(); err != nil || base != want || token != mcprunner.AccountToken(runnerTestMaster, runnerAccountA) {
		t.Fatalf("after attach: %q %q %v", base, token, err)
	}
	if _, err := s.DetachVM(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if base, _, err := s.localRunnerConfig(); err != nil || base != want {
		t.Fatalf("after detach: %q %v", base, err)
	}
}

func TestNoComputerAccountWritesTheRunnerStateFile(t *testing.T) {
	s, dir, _ := noComputerRunnerServer(t)
	_ = s
	state, err := os.ReadFile(filepath.Join(dir, "local-runner.json"))
	if err != nil || !strings.Contains(string(state), `"state":"container"`) {
		t.Fatalf("state: %s %v", state, err)
	}
	var again Server
	again.initLocalRunner(Config{DataDir: dir})
	if !again.runnerMigration.switched.Load() {
		t.Fatal("the state was not remembered across a restart")
	}
}
