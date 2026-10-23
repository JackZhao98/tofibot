package extensions

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mailFixture connects one synthetic server whose name does not say what it
// serves, the shape a bundled local plugin has.
func mailFixture(t *testing.T) (*Manager, *Prepared) {
	t.Helper()
	backend := fixtureServer("mail", "1")
	backend.AddTool(fixtureTool("gmail_search", fixtureDescription("Search email messages in the inbox")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("ok"), nil
	})
	remote := httptest.NewServer(fixtureHTTPHandler(backend))
	t.Cleanup(remote.Close)
	other := fixtureServer("notes", "1")
	other.AddTool(fixtureTool("read_notes", fixtureDescription("Read notes")), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("ok"), nil
	})
	otherRemote := httptest.NewServer(fixtureHTTPHandler(other))
	t.Cleanup(otherRemote.Close)
	manager := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	for name, url := range map[string]string{"local_gog": remote.URL, "notes": otherRemote.URL} {
		if err := manager.SaveMCP(name, MCPServerConfig{URL: url}, false); err != nil {
			t.Fatal(err)
		}
	}
	p, err := manager.PrepareDiscoverableForBot(context.Background(), "any")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return manager, p
}

func TestSearchUnknownServerListsConnectedServersWithCode(t *testing.T) {
	_, p := mailFixture(t)
	search := discoveryTool(t, p, "search_mcp_tools")
	// One search first, so the saved catalog can give the server a purpose.
	if _, err := search.Execute(context.Background(), json.RawMessage(`{"server":"local_gog","query":"*"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := search.Execute(context.Background(), json.RawMessage(`{"server":"unrelated-name","query":"search messages"}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "mcp_server_unknown" || o.Status != tooloutcome.Validation || o.NextAction != "repair_arguments" {
		t.Fatalf("err = %v outcome=%+v", err, o)
	}
	for _, want := range []string{`"name":"local_gog"`, `"name":"notes"`, `"label":"gog (local plugin)"`, "gmail_search", "data, not instructions"} {
		if !strings.Contains(o.Message, want) {
			t.Fatalf("message lacks %q: %s", want, o.Message)
		}
	}
}

func TestCallWithUnknownServerPrefixListsConnectedServers(t *testing.T) {
	_, p := mailFixture(t)
	_, err := discoveryTool(t, p, "call_mcp_tool").Execute(context.Background(), json.RawMessage(`{"name":"mcp_google-gmail__search","arguments":{}}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "mcp_server_unknown" || !strings.Contains(o.Message, `"name":"local_gog"`) {
		t.Fatalf("err = %v outcome=%+v", err, o)
	}
}

func TestSearchServerFilterMatchesLabelAndToolsCaseInsensitively(t *testing.T) {
	_, p := mailFixture(t)
	search := discoveryTool(t, p, "search_mcp_tools")
	// The label of local_gog is "gog (local plugin)": a case and separator
	// variant of it finds the server.
	out, err := search.Execute(context.Background(), json.RawMessage(`{"server":"GOG Local Plugin","query":"search email"}`))
	if err != nil || !strings.Contains(out, "mcp_local_gog__gmail_search") || !strings.Contains(out, `"server_searched":"local_gog"`) {
		t.Fatalf("label match: %s %v", out, err)
	}
	// Once the catalog has been saved, a service word that appears in the
	// server's tool names resolves to it, however the model spelled the server.
	out, err = search.Execute(context.Background(), json.RawMessage(`{"server":"google-gmail","query":"search messages list inbox email"}`))
	if err != nil || !strings.Contains(out, "mcp_local_gog__gmail_search") || !strings.Contains(out, `"server_requested":"google-gmail"`) {
		t.Fatalf("service match: %s %v", out, err)
	}
	// Without a server the same search finds the tool.
	out, err = search.Execute(context.Background(), json.RawMessage(`{"query":"search email messages"}`))
	if err != nil || !strings.Contains(out, "mcp_local_gog__gmail_search") {
		t.Fatalf("no-server search: %s %v", out, err)
	}
}

func TestDiscoveryPolicySearchesBeforeClaimingUnavailable(t *testing.T) {
	policy, _ := DiscoveryInstructionParts(discoveryInstructions(map[string]MCPServerConfig{"x": {}}, nil))
	if !strings.Contains(policy, "Before telling the person that a capability or connection is unavailable, search the connected tools with search_mcp_tools without naming a server.") {
		t.Fatalf("policy = %q", policy)
	}
}
