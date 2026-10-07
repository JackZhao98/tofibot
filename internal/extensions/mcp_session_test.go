package extensions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// countingMCPTransport serves a fixture MCP handler in memory, counts
// handshakes (server/discover), listings and calls, and can fail tools/call
// below the JSON-RPC layer to simulate a dropped connection.
type countingMCPTransport struct {
	handler   http.Handler
	discovers atomic.Int32
	listings  atomic.Int32
	calls     atomic.Int32
	dropCalls atomic.Bool
}

func (tr *countingMCPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "synthetic.invalid" {
		return nil, errors.New("unexpected synthetic destination")
	}
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	switch {
	case bytes.Contains(body, []byte(`"method":"server/discover"`)):
		tr.discovers.Add(1)
	case bytes.Contains(body, []byte(`"method":"tools/list"`)):
		tr.listings.Add(1)
	case bytes.Contains(body, []byte(`"method":"tools/call"`)):
		if tr.dropCalls.Load() {
			return nil, errors.New("synthetic connection reset")
		}
		tr.calls.Add(1)
	}
	rec := httptest.NewRecorder()
	tr.handler.ServeHTTP(rec, req)
	response := rec.Result()
	response.Request = req
	return response, nil
}

func newCountingMCPManager(t *testing.T, servers map[string]MCPServerConfig, register func(*mcp.Server)) (*Manager, *countingMCPTransport) {
	t.Helper()
	backend := fixtureServer("session", "1")
	register(backend)
	tr := &countingMCPTransport{handler: fixtureHTTPHandler(backend)}
	path := filepath.Join(t.TempDir(), "mcp.json")
	config, _ := json.Marshal(serverFile{MCPServers: servers})
	if err := os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	return NewManager(Config{MCPConfigPath: path, HTTPTransport: func(string) (http.RoundTripper, error) { return tr, nil }}), tr
}

// rechecksLikeApproval mirrors approveMCPCall, which rechecks readiness up to
// three times (pre-question, before the wait, after the decision).
func rechecksLikeApproval(gateCalls *atomic.Int32) MCPCallGate {
	return func(ctx context.Context, call MCPCallApproval) error {
		gateCalls.Add(1)
		for i := 0; i < 3; i++ {
			if err := call.Recheck(ctx); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestMCPCallReusesSearchSessionForEveryReadinessCheck(t *testing.T) {
	manager, tr := newCountingMCPManager(t, map[string]MCPServerConfig{"mail": {URL: "https://synthetic.invalid/mcp"}}, func(s *mcp.Server) {
		s.AddTool(fixtureTool("send_note"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("sent"), nil
		})
	})
	var gateCalls atomic.Int32
	p, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, rechecksLikeApproval(&gateCalls))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"mail","query":"send"}`)); err != nil {
		t.Fatal(err)
	}
	if tr.discovers.Load() != 1 {
		t.Fatalf("search handshakes=%d", tr.discovers.Load())
	}
	call := discoveryTool(t, p, "call_mcp_tool")
	for i := 1; i <= 2; i++ {
		got, err := call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_mail__send_note","arguments":{}}`))
		if err != nil || got != "sent" {
			t.Fatalf("call %d: %q %v", i, got, err)
		}
		// Five readiness checks per invocation (own + three gate rechecks +
		// pre-dispatch) all reuse the live discovery session.
		if tr.discovers.Load() != 1 || tr.calls.Load() != int32(i) || gateCalls.Load() != int32(i) {
			t.Fatalf("call %d: handshakes=%d calls=%d gate=%d", i, tr.discovers.Load(), tr.calls.Load(), gateCalls.Load())
		}
	}
}

func TestMCPReadinessHandshakeCountCacheAndInvalidation(t *testing.T) {
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	manager, tr := newCountingMCPManager(t, map[string]MCPServerConfig{"mail": cfg}, func(s *mcp.Server) {
		s.AddTool(fixtureTool("send_note"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("sent"), nil
		})
	})
	clock := time.Unix(1_800_000_000, 0)
	manager.now = func() time.Time { return clock }
	cached := []CachedMCPTool{{Name: "mcp_mail__send_note", Server: "mail", RemoteName: "send_note", Parameters: map[string]any{"type": "object"}, SchemaVersion: manager.schemaVersion("mail", cfg)}}
	var gateCalls atomic.Int32
	run := func() (*Prepared, runtimeCall) {
		p, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", cached, rechecksLikeApproval(&gateCalls))
		if err != nil {
			t.Fatal(err)
		}
		call := discoveryTool(t, p, "call_mcp_tool")
		return p, func() (string, error) {
			return call.Execute(context.Background(), json.RawMessage(`{"name":"mcp_mail__send_note","arguments":{}}`))
		}
	}
	first, call := run()
	defer first.Close()
	// Cold cached reference: exactly one handshake, and the call reuses it.
	if got, err := call(); err != nil || got != "sent" || tr.discovers.Load() != 1 || tr.listings.Load() != 0 {
		t.Fatalf("cold call=%q err=%v handshakes=%d listings=%d", got, err, tr.discovers.Load(), tr.listings.Load())
	}
	// Within the TTL, a later call in the same run needs no handshake.
	clock = clock.Add(10 * time.Second)
	if _, err := call(); err != nil || tr.discovers.Load() != 1 {
		t.Fatalf("warm call err=%v handshakes=%d", err, tr.discovers.Load())
	}
	// Another run in the same process reuses the cached readiness; its only
	// handshake opens the session the call itself needs.
	second, otherCall := run()
	defer second.Close()
	if _, err := otherCall(); err != nil || tr.discovers.Load() != 2 {
		t.Fatalf("second run err=%v handshakes=%d", err, tr.discovers.Load())
	}
	// A dropped connection invalidates both the session and the cache.
	tr.dropCalls.Store(true)
	_, err := call()
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "mcp_result_unknown" || tr.discovers.Load() != 2 {
		t.Fatalf("dropped call outcome=%+v err=%v handshakes=%d", o, err, tr.discovers.Load())
	}
	if manager.mcpReadyFresh("mail", cfg) {
		t.Fatal("connection failure left readiness cached")
	}
	tr.dropCalls.Store(false)
	if _, err := call(); err != nil || tr.discovers.Load() != 3 {
		t.Fatalf("post-failure call err=%v handshakes=%d", err, tr.discovers.Load())
	}
	// The other run reuses the readiness the first run just re-proved.
	if _, err := otherCall(); err != nil || tr.discovers.Load() != 3 {
		t.Fatalf("other run after re-proof err=%v handshakes=%d", err, tr.discovers.Load())
	}
	// After the TTL (for example a long human wait) a fresh handshake runs once.
	clock = clock.Add(mcpReadinessTTL + time.Second)
	if _, err := call(); err != nil || tr.discovers.Load() != 4 {
		t.Fatalf("expired call err=%v handshakes=%d", err, tr.discovers.Load())
	}
	// A configuration change is a different fingerprint, not a cache hit.
	changed := cfg
	changed.ToolDenylist = []string{"other"}
	if manager.mcpReadyFresh("mail", changed) {
		t.Fatal("readiness reused across configuration change")
	}
}

type runtimeCall func() (string, error)

func TestMCPKnownToolNameResolvesWithoutSearch(t *testing.T) {
	params := map[string]any{"type": "object", "required": []string{"target"}, "properties": map[string]any{"target": map[string]any{"type": "string"}}, "additionalProperties": false}
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp", TrustedReadOnlyTools: []string{"read_inbox"}}
	manager, tr := newCountingMCPManager(t, map[string]MCPServerConfig{"mail": cfg}, func(s *mcp.Server) {
		write := fixtureTool("write_note")
		write.InputSchema = params
		s.AddTool(write, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("wrote"), nil
		})
		s.AddTool(fixtureTool("read_inbox"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText("inbox"), nil
		})
	})
	stale := []CachedMCPTool{
		{Name: "mcp_mail__write_note", Server: "mail", RemoteName: "write_note", Parameters: map[string]any{"type": "object"}, SchemaVersion: "stale"},
		{Name: "mcp_mail__retired", Server: "mail", RemoteName: "retired", Parameters: map[string]any{"type": "object"}, SchemaVersion: "stale"},
	}
	var gateCalls atomic.Int32
	p, err := manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", stale, rechecksLikeApproval(&gateCalls))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := discoveryTool(t, p, "call_mcp_tool")
	execute := func(raw string) (string, error) { return call.Execute(context.Background(), json.RawMessage(raw)) }

	// Unseen but known: resolved through bounded discovery and executed.
	if got, err := execute(`{"name":"mcp_mail__read_inbox","arguments":{}}`); err != nil || got != "inbox" {
		t.Fatalf("known name=%q err=%v", got, err)
	}
	if tr.discovers.Load() != 1 || tr.listings.Load() != 1 || tr.calls.Load() != 1 {
		t.Fatalf("handshakes=%d listings=%d calls=%d", tr.discovers.Load(), tr.listings.Load(), tr.calls.Load())
	}
	if call.Identity(json.RawMessage(`{"name":"mcp_mail__read_inbox","arguments":{}}`)).Risk != tooloutcome.Observation {
		t.Fatal("resolved source lost owner-reviewed read-only risk")
	}
	// A stale cached schema reference is re-resolved from the current server.
	if got, err := execute(`{"name":"mcp_mail__write_note","arguments":{"target":"desk"}}`); err != nil || got != "wrote" {
		t.Fatalf("stale reference=%q err=%v", got, err)
	}
	// Invalid arguments come back with the current schema for a one-step repair.
	_, err = execute(`{"name":"mcp_mail__write_note","arguments":{"target":7}}`)
	if o, ok := tooloutcome.FromError(err); !ok || o.Status != tooloutcome.Validation || o.Code != "invalid_arguments" || !strings.Contains(o.Message, "Current input schema: ") || !strings.Contains(o.Message, `"target"`) {
		t.Fatalf("invalid arguments outcome=%+v err=%v", o, err)
	}
	// Models echo the server's display case; the name resolves case-insensitively.
	if got, err := execute(`{"name":"mcp_MAIL__read_inbox","arguments":{}}`); err != nil || got != "inbox" {
		t.Fatalf("case-variant name=%q err=%v", got, err)
	}
	// Truly absent tools and unknown servers still require a schema.
	for raw, code := range map[string]string{
		`{"name":"mcp_mail__retired","arguments":{}}`:     "stale_schema",
		`{"name":"mcp_mail__missing","arguments":{}}`:     "schema_required",
		`{"name":"mcp_ghost__read_inbox","arguments":{}}`: "schema_required",
	} {
		_, err := execute(raw)
		if o, ok := tooloutcome.FromError(err); !ok || o.Code != code || o.Certainty != "not_executed" {
			t.Fatalf("%s outcome=%+v err=%v", raw, o, err)
		}
	}
	if tr.listings.Load() != 1 || tr.calls.Load() != 3 || gateCalls.Load() != 3 {
		t.Fatalf("listings=%d calls=%d gate=%d", tr.listings.Load(), tr.calls.Load(), gateCalls.Load())
	}
}

func TestMCPKnownToolNameUnreachableServerFailsClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	manager := NewManager(Config{MCPConfigPath: path, DiscoveryTimeout: 200 * time.Millisecond})
	if err := manager.SaveMCP("mail", MCPServerConfig{URL: "http://127.0.0.1:1/synthetic"}, false); err != nil {
		t.Fatal(err)
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = discoveryTool(t, p, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_mail__read_inbox","arguments":{}}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "schema_required" || strings.Contains(o.Message, "127.0.0.1") {
		t.Fatalf("unreachable outcome=%+v err=%v", o, err)
	}
}

func TestMCPServerForToolNameLongestSanitizedPrefix(t *testing.T) {
	servers := []string{"a", "a_b", "Mail.Box"}
	for name, want := range map[string]string{
		"mcp_a__x":         "a",
		"mcp_a_b__x":       "a_b",
		"mcp_mail_box__x":  "Mail.Box",
		"mcp_a__":          "",
		"mcp_unknown__x":   "",
		"search_mcp_tools": "",
	} {
		if got := mcpServerForToolName(servers, name); got != want {
			t.Fatalf("%s -> %q, want %q", name, got, want)
		}
	}
}

func TestMCPSchemaHintIsBounded(t *testing.T) {
	params := map[string]any{"type": "object", "required": []string{"v"}, "properties": map[string]any{"v": map[string]any{"type": "string", "description": strings.Repeat("界", 4096)}}}
	err := withMCPSchemaHint(validateMCPArguments(params, map[string]any{}), params)
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "invalid_arguments" || !strings.HasSuffix(o.Message, "[schema truncated]") || len(o.Message) > maxMCPSchemaHintBytes+512 {
		t.Fatalf("hint len=%d outcome=%+v", len(o.Message), o)
	}
}
