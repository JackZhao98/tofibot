package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A schema a search page shows whole is accepted from the cache, so the
// reveal does not repeat in every run; a compacted one is never cached.
func TestCachedSchemaBoundMatchesSearchPage(t *testing.T) {
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	servers := map[string]MCPServerConfig{"mail": cfg}
	manager, _ := newCountingMCPManager(t, servers, func(s *mcp.Server) {})
	props := map[string]any{}
	for i := 0; i < 90; i++ {
		props[fmt.Sprintf("field_%03d", i)] = map[string]any{"type": "string", "description": strings.Repeat("d", 100)}
	}
	cached := CachedMCPTool{Name: "mcp_mail__big", Server: "mail", RemoteName: "big", Parameters: map[string]any{"type": "object", "properties": props}, SchemaVersion: manager.schemaVersion("mail", cfg)}
	if data, _ := json.Marshal(cached.Parameters); len(data) < 8<<10 || len(data) > maxCachedSchemaBytes {
		t.Fatalf("fixture size %d", len(data))
	}
	if !validCachedMCPTool(cached, servers, manager) {
		t.Fatal("a schema within the page bound was refused")
	}
	cached.SchemaCompacted = true
	if validCachedMCPTool(cached, servers, manager) {
		t.Fatal("a compacted schema was accepted as whole")
	}
}

// A schema too large even compacted is named, and calling it returns the
// schema without spending a repair.
func TestOmittedSchemaIsNamed(t *testing.T) {
	srv := fixtureServer("wide", "1")
	tool := fixtureTool("wide-tool", fixtureDescription("Wide tool"))
	props := map[string]any{}
	for i := 0; i < 700; i++ {
		props[fmt.Sprintf("field_with_a_long_name_%04d_padding", i)] = map[string]any{"type": "string"}
	}
	tool.InputSchema = map[string]any{"type": "object", "properties": props}
	srv.AddTool(tool, fixtureNoop)
	f := newNotionFixtureWithServer(t, srv)
	p := f.prepare(t)
	ctx := context.Background()
	out, err := discoveryTool(t, p, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"notion","query":"*"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Omitted int      `json:"omitted_schema_count"`
		Names   []string `json:"omitted_schema_tools"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Omitted != 1 || len(res.Names) != 1 || res.Names[0] != "mcp_notion__wide-tool" {
		t.Fatalf("omission not named: %s", out)
	}
	_, err = discoveryTool(t, p, "call_mcp_tool").Execute(ctx, json.RawMessage(`{"name":"mcp_notion__wide-tool","arguments":{}}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "schema_required" || !strings.Contains(o.Message, "field_with_a_long_name_0000") {
		t.Fatalf("reveal lacks the schema: %v", err)
	}
}
