package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type syntheticMCPGateTransport struct{ handler http.Handler }

func (tr syntheticMCPGateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "synthetic.invalid" {
		return nil, errors.New("unexpected synthetic destination")
	}
	rec := httptest.NewRecorder()
	tr.handler.ServeHTTP(rec, req)
	response := rec.Result()
	response.Request = req
	return response, nil
}

func TestMCPCallGatePrecedesRemoteCallAndDoesNotTrustAnnotations(t *testing.T) {
	backend := fixtureServer("gate", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("publish", func(tool *mcp.Tool) { tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true} }), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureText("published"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://synthetic.invalid/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	var approvals atomic.Int32
	gate := func(_ context.Context, call MCPCallApproval) error {
		approvals.Add(1)
		if call.Server != "fixture" || call.Tool != "publish" || call.ConfigVersion == "" || string(call.Arguments) != `{}` {
			t.Fatalf("incorrect approval request: %+v", call)
		}
		return errors.New("denied")
	}
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, gate)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"publish"}`)); err != nil {
		t.Fatal(err)
	}
	_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__publish","arguments":{}}`))
	if err == nil || !strings.Contains(err.Error(), "denied") || approvals.Load() != 1 || remoteCalls.Load() != 0 {
		t.Fatalf("denial err=%v approvals=%d remote=%d", err, approvals.Load(), remoteCalls.Load())
	}
}

func TestMCPCallGateMigratesLegacyServerWithoutPolicyField(t *testing.T) {
	backend := fixtureServer("legacy", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("unknown_action"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureText("called"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	path := filepath.Join(t.TempDir(), "mcp.json")
	config, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"legacy": {URL: "https://synthetic.invalid/mcp"}}})
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{MCPConfigPath: path, HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"legacy","query":"unknown"}`)); err != nil {
		t.Fatal(err)
	}
	_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_legacy__unknown_action","arguments":{}}`))
	if err == nil || !strings.Contains(err.Error(), "approval gate") || remoteCalls.Load() != 0 {
		t.Fatalf("legacy call bypassed gate: err=%v remote=%d", err, remoteCalls.Load())
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	var approvals atomic.Int32
	prepared, err = manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error {
		approvals.Add(1)
		return errors.New("human denied")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"legacy","query":"unknown"}`)); err != nil {
		t.Fatal(err)
	}
	_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_legacy__unknown_action","arguments":{}}`))
	if err == nil || !strings.Contains(err.Error(), "human denied") || approvals.Load() != 1 || remoteCalls.Load() != 0 {
		t.Fatalf("legacy call did not request approval: err=%v approvals=%d remote=%d", err, approvals.Load(), remoteCalls.Load())
	}
}

func TestMCPCallGateDoesNotReplayUnknownRemoteResult(t *testing.T) {
	backend := fixtureServer("unknown", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("mutate"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureError("result unavailable after synthetic side effect"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	if err := manager.SaveMCP("fixture", MCPServerConfig{URL: "https://synthetic.invalid/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	var claims atomic.Int32
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error {
		if claims.Add(1) != 1 {
			return errors.New("approval already claimed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"mutate"}`)); err != nil {
		t.Fatal(err)
	}
	call := discoveryTool(t, prepared, "call_mcp_tool")
	args := json.RawMessage(`{"name":"mcp_fixture__mutate","arguments":{}}`)
	if _, err := call.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "returned an error") {
		t.Fatalf("uncertain first result: %v", err)
	}
	if _, err := call.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "already claimed") || remoteCalls.Load() != 1 {
		t.Fatalf("replayed unknown result: err=%v remote=%d", err, remoteCalls.Load())
	}
}

func TestMCPCallGateTrustedReadOnlyRequiresOwnerConfiguration(t *testing.T) {
	backend := fixtureServer("gate", "1")
	var remoteCalls atomic.Int32
	backend.AddTool(fixtureTool("read_report"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		remoteCalls.Add(1)
		return fixtureText("report"), nil
	})
	transport := syntheticMCPGateTransport{fixtureHTTPHandler(backend)}
	path := filepath.Join(t.TempDir(), "mcp.json")
	config, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"fixture": {URL: "https://synthetic.invalid/mcp", TrustedReadOnlyTools: []string{"read_report"}}}})
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(Config{MCPConfigPath: path, HTTPTransport: func(string) (http.RoundTripper, error) { return transport, nil }})
	var reviewCalls atomic.Int32
	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error { reviewCalls.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"read"}`)); err != nil {
		t.Fatal(err)
	}
	call := discoveryTool(t, prepared, "call_mcp_tool")
	if call.Identity == nil || call.Identity(json.RawMessage(`{"name":"mcp_fixture__read_report","arguments":{}}`)).Risk != tooloutcome.Observation {
		t.Fatal("owner-reviewed read-only risk was not retained")
	}
	if call.Identity(json.RawMessage(`{"name":"mcp_fixture__unseen_read","arguments":{}}`)).Risk != tooloutcome.OpaqueEffect {
		t.Fatal("unseen capability gained read-only classification")
	}
	result, err := discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__read_report","arguments":{}}`))
	if err != nil || result != "report" || remoteCalls.Load() != 1 || reviewCalls.Load() != 1 {
		t.Fatalf("read result=%q err=%v remote=%d", result, err, remoteCalls.Load())
	}
}

func TestMCPReadOnlyExemptionSurvivesSettingsButNotTargetChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	manager := NewManager(Config{MCPConfigPath: path})
	if err := manager.SaveMCP("new", MCPServerConfig{URL: "https://first.example/mcp"}, false); err != nil {
		t.Fatal(err)
	}
	servers, err := loadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	newServer := servers["new"]
	newServer.TrustedReadOnlyTools = []string{"lookup"}
	config, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"new": newServer, "legacy": {URL: "https://old.example/mcp"}}})
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveMCP("new", MCPServerConfig{URL: "https://first.example/mcp"}, true); err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveMCP("legacy", MCPServerConfig{URL: "https://old.example/mcp"}, true); err != nil {
		t.Fatal(err)
	}
	servers, err = loadServers(path)
	if err != nil || !trustedReadOnlyTool(servers["new"], "lookup") {
		t.Fatalf("ordinary update changed policy: %+v, %v", servers, err)
	}
	if err := manager.SaveMCP("new", MCPServerConfig{URL: "https://different.example/mcp"}, true); err != nil {
		t.Fatal(err)
	}
	servers, err = loadServers(path)
	if err != nil || len(servers["new"].TrustedReadOnlyTools) != 0 {
		t.Fatalf("target change retained trust: %+v, %v", servers["new"], err)
	}
}
