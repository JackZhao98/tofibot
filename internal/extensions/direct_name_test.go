package extensions

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestResolveDirectNameMatchesCallMCPToolResolution(t *testing.T) {
	srv := fixtureServer("fixture", "1")
	for _, name := range []string{"echo", "denied", "Dup", "dup"} {
		srv.AddTool(fixtureTool(name), func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return fixtureText(req.Params.Name), nil
		})
	}
	httpSrv := fixtureHTTPServer(srv)
	defer httpSrv.Close()
	mgr := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := mgr.SaveMCP("fixture", MCPServerConfig{URL: httpSrv.URL, ToolDenylist: []string{"denied"}}, false); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	call := discoveryTool(t, p, "call_mcp_tool")
	ctx := context.Background()
	// Deferred: nothing was searched, yet a known exact name resolves.
	if got, ok := call.ResolveDirectName(ctx, "mcp_fixture__echo"); !ok || got != "mcp_fixture__echo" {
		t.Fatalf("known name: %q %v", got, ok)
	}
	// A case-folded echo of the server prefix resolves to the canonical name.
	if got, ok := call.ResolveDirectName(ctx, "MCP_FIXTURE__echo"); !ok || got != "mcp_fixture__echo" {
		t.Fatalf("case-folded name: %q %v", got, ok)
	}
	for _, name := range []string{"mcp_fixture__denied", "mcp_fixture__missing", "mcp_nope__echo", "echo", "", "call_mcp_tool"} {
		if got, ok := call.ResolveDirectName(ctx, name); ok {
			t.Fatalf("%q resolved to %q", name, got)
		}
	}
	// Two remote tools sanitize to the same run name: never rerouted.
	for _, name := range []string{"mcp_fixture__dup", "mcp_fixture__dup__2"} {
		if got, ok := call.ResolveDirectName(ctx, name); ok {
			t.Fatalf("ambiguous %q resolved to %q", name, got)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := call.ResolveDirectName(cancelled, "mcp_fixture__echo"); ok {
		t.Fatal("resolved on a cancelled context")
	}
}
