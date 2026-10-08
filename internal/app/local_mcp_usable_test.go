package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func gogStatusServer(t *testing.T, connected *atomic.Bool, calls *atomic.Int32) *Server {
	t.Helper()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/plugins/gog/gog/status" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		if connected.Load() {
			_, _ = w.Write([]byte(`{"connected":true,"email":"synthetic@example.com"}`))
			return
		}
		_, _ = w.Write([]byte(`{"connected":false,"email":""}`))
	}))
	t.Cleanup(runner.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("synthetic-token"), 0600); err != nil {
		t.Fatal(err)
	}
	return &Server{localRunnerURL: runner.URL, localRunnerTokenFile: token}
}

func TestUnconnectedGogIsHiddenFromRuns(t *testing.T) {
	var connected atomic.Bool
	var calls atomic.Int32
	s := gogStatusServer(t, &connected, &calls)
	ctx := context.Background()
	if s.mcpServerUsable(ctx, "local_gog") {
		t.Fatal("gog offered without a connected Google account")
	}
	if !s.mcpServerUsable(ctx, "remote_fixture") {
		t.Fatal("other servers must stay offered")
	}
	connected.Store(true)
	if s.mcpServerUsable(ctx, "local_gog") || calls.Load() != 1 {
		t.Fatalf("status must be cached within its TTL; calls=%d", calls.Load())
	}
	s.gogStatus.checked = time.Now().Add(-gogStatusTTL - time.Second)
	if !s.mcpServerUsable(ctx, "local_gog") || calls.Load() != 2 {
		t.Fatalf("connected gog not offered after TTL; calls=%d", calls.Load())
	}
}

func TestUnknownGogStatusKeepsServerOffered(t *testing.T) {
	s := &Server{localRunnerURL: "http://127.0.0.1:1", localRunnerTokenFile: filepath.Join(t.TempDir(), "missing")}
	if !s.mcpServerUsable(context.Background(), "local_gog") {
		t.Fatal("an unreadable status must not hide the server")
	}
}

func TestOnlyUnconnectedGogDropsMCPDiscoveryTools(t *testing.T) {
	var connected atomic.Bool
	var calls atomic.Int32
	s := gogStatusServer(t, &connected, &calls)
	dir := t.TempDir()
	manager := extensions.NewManager(extensions.Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: dir, DiscoveryTimeout: time.Second, ServerUsable: s.mcpServerUsable})
	s.extensions = manager
	if err := manager.SaveMCP("local_gog", extensions.MCPServerConfig{URL: "http://127.0.0.1:1/mcp/gog"}, false); err != nil {
		t.Fatal(err)
	}
	prepared, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	for _, tool := range s.withoutIdleMCPTools(prepared.Tools) {
		if mcpDiscoveryToolNames[tool.Name] {
			t.Fatalf("%s offered while gog is not connected", tool.Name)
		}
	}
	connected.Store(true)
	s.gogStatus.checked = time.Time{}
	if _, ok := toolNamed(s.withoutIdleMCPTools(prepared.Tools), "search_mcp_tools"); !ok {
		t.Fatal("search_mcp_tools hidden with gog connected")
	}
}
