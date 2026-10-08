package extensions

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestMCPDispatchFenceAllowsOAuthCacheLockAndBlocksConfigurationEdit(t *testing.T) {
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	cfg := MCPServerConfig{URL: "http://127.0.0.1:1/synthetic"}
	if err := m.SaveMCP("fixture", cfg, false); err != nil {
		t.Fatal(err)
	}
	call := MCPCallApproval{Server: "fixture", ConfigVersion: metadataFingerprint("fixture", cfg), Arguments: json.RawMessage(`{}`)}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := m.executeCurrentMCPCall(call, func() (string, error) { m.mu.Lock(); m.mu.Unlock(); close(entered); <-release; return "synthetic", nil })
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("dispatch deadlocked on OAuth cache mutex")
	}
	edited := make(chan error, 1)
	go func() { edited <- m.SaveMCP("fixture", MCPServerConfig{URL: "http://127.0.0.1:1/changed"}, true) }()
	select {
	case <-edited:
		t.Fatal("configuration changed during dispatch")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-edited; err != nil {
		t.Fatal(err)
	}
	effects := 0
	if _, err := m.executeCurrentMCPCall(call, func() (string, error) { effects++; return "", nil }); err == nil || effects != 0 {
		t.Fatal("stale config dispatched")
	}
}
