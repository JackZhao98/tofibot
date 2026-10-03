package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAdapterURLIdentityInvalidatesSchemaAndApprovalBindings(t *testing.T) {
	backend := fixtureServer("adapter-identity-fixture", "1")
	var calls atomic.Int32
	backend.AddTool(fixtureTool("publish", func(tool *mcp.Tool) { tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true} }), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return fixtureText("unexpected"), nil
	})
	remote := fixtureHTTPServer(backend)
	defer remote.Close()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	old := MCPServerConfig{URL: remote.URL + "?tofi_adapter=" + strings.Repeat("a", 64)}
	if err := manager.SaveMCP("fixture", old, false); err != nil {
		t.Fatal(err)
	}
	var versions []string
	gate := func(_ context.Context, call MCPCallApproval) error {
		versions = append(versions, call.ConfigVersion)
		return errors.New("synthetic denied")
	}
	first, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	listing, err := discoveryTool(t, first, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"publish"}`))
	if err != nil {
		t.Fatal(err)
	}
	var cached struct {
		Tools []CachedMCPTool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(listing), &cached); err != nil || len(cached.Tools) != 1 {
		t.Fatal("missing schema reference")
	}
	args := json.RawMessage(`{"name":"mcp_fixture__publish","arguments":{}}`)
	if _, err := discoveryTool(t, first, "call_mcp_tool").Execute(context.Background(), args); err == nil || len(versions) != 1 || calls.Load() != 0 {
		t.Fatal("first call bypassed approval")
	}
	_ = first.Close()
	next := MCPServerConfig{URL: remote.URL + "?tofi_adapter=" + strings.Repeat("b", 64)}
	if err := manager.SaveMCP("fixture", next, true); err != nil {
		t.Fatal(err)
	}
	second, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", cached.Tools, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := discoveryTool(t, second, "call_mcp_tool").Execute(context.Background(), args); err == nil || len(versions) != 1 || calls.Load() != 0 {
		t.Fatal("old schema remained callable across adapter identity change")
	}
	fresh, err := discoveryTool(t, second, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"publish"}`))
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Tools []CachedMCPTool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(fresh), &updated); err != nil || len(updated.Tools) != 1 || updated.Tools[0].SchemaVersion == cached.Tools[0].SchemaVersion {
		t.Fatal("schema cache identity did not change")
	}
	if _, err := discoveryTool(t, second, "call_mcp_tool").Execute(context.Background(), args); err == nil || len(versions) != 2 || versions[0] == versions[1] || calls.Load() != 0 {
		t.Fatal("approval binding did not change or annotation granted execution")
	}
	if versions[0] != metadataFingerprint("fixture", old) || versions[1] != metadataFingerprint("fixture", next) {
		t.Fatal("approval did not use current config fingerprint")
	}
}
