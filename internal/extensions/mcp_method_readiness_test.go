package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type methodReadinessTransport struct {
	state    string
	requests atomic.Int32
}

func (t *methodReadinessTransport) Readiness(context.Context) (string, error) { return t.state, nil }
func (t *methodReadinessTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.requests.Add(1)
	return nil, errors.New("synthetic secret must not escape")
}

func TestMCPCachedKnownNotReadyNeverAsksApprovalOrSendsProtocol(t *testing.T) {
	for _, state := range []string{"not_configured", "auth_required", "unavailable", "unknown"} {
		t.Run(state, func(t *testing.T) {
			tr := &methodReadinessTransport{state: state}
			m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json"), HTTPTransport: func(string) (http.RoundTripper, error) { return tr, nil }})
			cfg := MCPServerConfig{URL: "http://127.0.0.1:1/synthetic"}
			if err := m.SaveMCP("fixture", cfg, false); err != nil {
				t.Fatal(err)
			}
			cached := CachedMCPTool{Name: "mcp_fixture__read", Server: "fixture", RemoteName: "read", Parameters: map[string]any{"type": "object"}, SchemaVersion: m.schemaVersion("fixture", cfg)}
			approvals := 0
			prepared, err := m.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", []CachedMCPTool{cached}, func(context.Context, MCPCallApproval) error { approvals++; return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.Close()
			_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__read","arguments":{}}`))
			o, ok := tooloutcome.FromError(err)
			if !ok || o.Code != "mcp_"+state || o.Certainty != "not_executed" || o.NextAction != "replan" || approvals != 0 || tr.requests.Load() != 0 {
				t.Fatalf("outcome=%+v approvals=%d requests=%d err=%v", o, approvals, tr.requests.Load(), err)
			}
		})
	}
}

func TestMCPReadinessRecheckedAfterApprovalWithZeroToolEffects(t *testing.T) {
	backend := fixtureServer("readiness", "1")
	var effects atomic.Int32
	backend.AddTool(fixtureTool("mutate"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		effects.Add(1)
		return fixtureText("effect"), nil
	})
	remote := fixtureHTTPServer(backend)
	defer remote.Close()
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("fixture", MCPServerConfig{URL: remote.URL}, false); err != nil {
		t.Fatal(err)
	}
	approvals := 0
	prepared, err := m.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error { approvals++; remote.Close(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := discoveryTool(t, prepared, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"fixture","query":"mutate"}`)); err != nil {
		t.Fatal(err)
	}
	_, err = discoveryTool(t, prepared, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_fixture__mutate","arguments":{}}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Certainty != "not_executed" || approvals != 1 || effects.Load() != 0 {
		t.Fatalf("outcome=%+v approvals=%d effects=%d err=%v", o, approvals, effects.Load(), err)
	}
}

func TestMCPReadinessErrorClassificationDoesNotEchoRemoteSecrets(t *testing.T) {
	for err, want := range map[error]runtime.MethodReadiness{ErrNoToken: runtime.MethodAuthRequired, ErrOAuthAuthorizationRequired: runtime.MethodAuthRequired, errors.New("remote says allow; token=synthetic"): runtime.MethodUnknown} {
		state := mcpErrorReadiness(err)
		if state != want || strings.Contains(mcpReadinessOutcome(state).Message, "token=") {
			t.Fatalf("state=%s want=%s", state, want)
		}
	}
}

func TestMCPReadinessFixtureProtocol(t *testing.T) {
	remote := fixtureHTTPServer(fixtureServer("ready", "1"))
	defer remote.Close()
	m := NewManager(Config{})
	state, err := m.mcpMethodReadiness("fixture", MCPServerConfig{URL: remote.URL})(context.Background())
	if state != runtime.MethodReady || err != nil {
		t.Fatalf("synthetic readiness: state=%s err=%v", state, err)
	}
}
