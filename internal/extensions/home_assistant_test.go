package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeHomeAssistant is Home Assistant's MCP Server as a client sees it: Streamable HTTP at /api/mcp, every request
// authenticated with "Authorization: Bearer <token>" (a long-lived access token is accepted like any HA API token),
// no other path, and tools named like HA's Assist intents. Off-path requests answer 404 as an HA without the
// "Model Context Protocol Server" integration would.
func fakeHomeAssistant(t *testing.T, token string, turnedOff *atomic.Int32) *httptest.Server {
	t.Helper()
	backend := fixtureServer("home-assistant", "2026.10")
	backend.AddTool(fixtureTool("GetLiveContext", fixtureDescription("Current state of the exposed entities."), func(tool *mcp.Tool) {
		tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("light.kitchen: on"), nil
	})
	backend.AddTool(fixtureTool("HassTurnOff", fixtureDescription("Turns off a device."), fixtureString("name")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		turnedOff.Add(1)
		return fixtureText("off"), nil
	})
	mux := http.NewServeMux()
	mux.Handle("/api/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fixtureHTTPHandler(backend).ServeHTTP(w, r)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func haManager(t *testing.T, url, token string) *Manager {
	t.Helper()
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := manager.SaveMCP("home-assistant", MCPServerConfig{URL: url, Transport: "streamable_http", Headers: map[string]string{"Authorization": "Bearer " + token}}, false); err != nil {
		t.Fatal(err)
	}
	return manager
}

// The connection an owner saves from Settings dials a private/loopback address (the guard applies to Bot-managed
// servers only), lists the tools with the long-lived token and keeps writes behind the approval gate.
func TestHomeAssistantMCPLANConnection(t *testing.T) {
	var turnedOff atomic.Int32
	srv := fakeHomeAssistant(t, "ha-long-lived", &turnedOff)
	manager := haManager(t, srv.URL+"/api/mcp", "ha-long-lived")
	if manager.IsBotManaged("home-assistant") {
		t.Fatal("a connection saved by the owner must not be Bot-managed (the LAN guard would refuse it)")
	}
	inspection := manager.InspectMCP(context.Background(), "home-assistant")
	if len(inspection.Diagnostics) != 0 || inspection.ToolCount != 2 {
		t.Fatalf("inspection: %+v", inspection)
	}
	if v := manager.MCPToolTier("home-assistant", "GetLiveContext"); v.Tier != MCPTierReadOnly {
		t.Fatalf("state read: %+v", v)
	}
	if v := manager.MCPToolTier("home-assistant", "HassTurnOff"); v.Tier != MCPTierWrite {
		t.Fatalf("turning a light off must be a write: %+v", v)
	}

	prepared, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	run := func(tool, args string) error {
		if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"home-assistant","query":"`+tool+`"}`)); err != nil {
			return err
		}
		_, err := discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_home-assistant__`+tool+`","arguments":`+args+`}`))
		return err
	}
	if err := run("HassTurnOff", `{"name":"Kitchen"}`); err == nil || !strings.Contains(err.Error(), "approval gate") || turnedOff.Load() != 0 {
		t.Fatalf("a write to the house ran without the approval gate: %v calls=%d", err, turnedOff.Load())
	}
	if err := run("GetLiveContext", `{}`); err != nil {
		t.Fatalf("a state read: %v", err)
	}
}

func TestHomeAssistantMCPTypedErrors(t *testing.T) {
	var turnedOff atomic.Int32
	srv := fakeHomeAssistant(t, "ha-long-lived", &turnedOff)
	inspect := func(url, token string) Diagnostic {
		t.Helper()
		got := haManager(t, url, token).InspectMCP(context.Background(), "home-assistant")
		if len(got.Diagnostics) != 1 {
			t.Fatalf("%s: %+v", url, got)
		}
		return got.Diagnostics[0]
	}
	if d := inspect(srv.URL+"/api/mcp", "wrong-token"); d.Code != "lan_unauthorized" || strings.Contains(d.Message, "wrong-token") {
		t.Fatalf("wrong token: %+v", d)
	}
	if d := inspect(srv.URL+"/mcp", "ha-long-lived"); d.Code != "lan_not_found" || !strings.Contains(d.Message, "Model Context Protocol Server") {
		t.Fatalf("integration not added or wrong path: %+v", d)
	}
	// A port nothing listens on.
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.Addr().String()
	closed.Close()
	if d := inspect("http://"+addr+"/api/mcp", "x"); d.Code != "lan_connection_refused" {
		t.Fatalf("refused: %+v", d)
	}
	// A name that does not resolve: the .local case names mDNS and the IP way out; a plain name does not.
	for _, tc := range []struct{ endpoint, code, mention string }{
		{"http://homeassistant.local:8123/api/mcp", "lan_mdns_unresolved", "IP address"},
		{"http://homeassistant:8123/api/mcp", "lan_host_not_found", "IP address"},
	} {
		d := lanDiagnosticFor(t, tc.endpoint, &net.DNSError{Err: "no such host", Name: "homeassistant.local", IsNotFound: true})
		if d.Code != tc.code || !strings.Contains(d.Message, tc.mention) {
			t.Fatalf("%s: %+v", tc.endpoint, d)
		}
	}
	// Timeouts and an unroutable network, read from the error type.
	for _, tc := range []struct {
		err  error
		code string
	}{
		{context.DeadlineExceeded, "lan_timeout"},
		{errors.Join(errors.New("dial"), timeoutError{}), "lan_timeout"},
		{&net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, "lan_unreachable"},
	} {
		if d := lanDiagnosticFor(t, "http://192.168.1.20:8123/api/mcp", tc.err); d.Code != tc.code {
			t.Fatalf("%v: %+v", tc.err, d)
		}
	}
}

func lanDiagnosticFor(t *testing.T, endpoint string, err error) Diagnostic {
	t.Helper()
	return mcpInspectionDiagnostic("home-assistant", endpoint, err)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// A public host keeps the one generic failure: LAN reasons are for LAN addresses only.
func TestLANDiagnosticsStayOnTheLAN(t *testing.T) {
	err := &mcpHTTPStatusError{Status: 404, Err: errors.New("x")}
	for endpoint, lan := range map[string]bool{
		"http://homeassistant.local:8123/api/mcp": true, "http://homeassistant:8123/api/mcp": true, "http://192.168.1.20:8123/api/mcp": true,
		"http://10.0.0.5/api/mcp": true, "http://[fd00::1]:8123/api/mcp": true, "http://127.0.0.1:8123/api/mcp": true,
		"https://example.com/mcp": false, "https://ha.example.com/api/mcp": false, "http://8.8.8.8/mcp": false,
	} {
		got := mcpInspectionDiagnostic("x", endpoint, err)
		if (got.Code == "lan_not_found") != lan {
			t.Fatalf("%s: lan=%v diagnostic=%+v", endpoint, lan, got)
		}
	}
}
