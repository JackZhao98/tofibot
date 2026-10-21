package extensions

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// notionLikeSchema mirrors the shape of a hosted page-update tool: one nested
// "data" object whose required fields depend on data.command. filler pads the
// descriptions so the schema is several KiB, as real ones are; new_str is
// declared after the padding so any cut-off drops it.
func notionLikeSchema(filler int) map[string]any {
	pad := strings.Repeat("Explanatory text for the model. ", filler)
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"data": map[string]any{
				"type":        "object",
				"description": pad,
				"properties": map[string]any{
					"page_id":    map[string]any{"type": "string"},
					"command":    map[string]any{"type": "string", "enum": []any{"replace_content", "update_properties", "insert_content_after", "replace_content_range"}},
					"properties": map[string]any{"type": "object", "description": pad},
					"new_str":    map[string]any{"type": "string", "description": "Replacement markdown. Required by replace_content."},
				},
				"required": []any{"page_id", "command"},
				"oneOf": []any{
					map[string]any{"properties": map[string]any{"command": map[string]any{"const": "replace_content"}}, "required": []any{"new_str"}},
					map[string]any{"properties": map[string]any{"command": map[string]any{"const": "update_properties"}}, "required": []any{"properties"}},
					map[string]any{"properties": map[string]any{"command": map[string]any{"enum": []any{"insert_content_after", "replace_content_range"}}}, "required": []any{"new_str"}},
				},
			},
		},
		"required": []any{"data"},
	}
}

type notionFixture struct {
	manager *Manager
	calls   *atomic.Int32
	reject  *atomic.Bool
}

func newNotionFixture(t *testing.T, schema map[string]any, tools ...string) notionFixture {
	t.Helper()
	var calls atomic.Int32
	var reject atomic.Bool
	srv := fixtureServer("notion", "1")
	if len(tools) == 0 {
		tools = []string{"notion-update-page"}
	}
	for _, name := range tools {
		tool := fixtureTool(name, fixtureDescription("Update a page: "+name))
		tool.InputSchema = schema
		srv.AddTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			if reject.Load() {
				return fixtureError(`The "replace_content" command requires a "new_str" parameter.`), nil
			}
			return fixtureText("updated"), nil
		})
	}
	httpSrv := fixtureHTTPServer(srv)
	t.Cleanup(httpSrv.Close)
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("notion", MCPServerConfig{URL: httpSrv.URL}, false); err != nil {
		t.Fatal(err)
	}
	return notionFixture{manager: m, calls: &calls, reject: &reject}
}

func (f notionFixture) prepare(t *testing.T) *Prepared {
	t.Helper()
	p, err := f.manager.PrepareDiscoverableForBotWithCallGate(context.Background(), "bot", nil, func(context.Context, MCPCallApproval) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const validReplace = `{"name":"mcp_notion__notion-update-page","arguments":{"data":{"page_id":"p1","command":"replace_content","new_str":"hello"}}}`

// The model's only view of a tool's arguments is the search result. It must
// carry the nested structure exactly as the server sent it.
func TestSearchResultCarriesNestedSchemaIntact(t *testing.T) {
	schema := notionLikeSchema(20)
	f := newNotionFixture(t, schema)
	p := f.prepare(t)
	out, err := discoveryTool(t, p, "search_mcp_tools").Execute(context.Background(), json.RawMessage(`{"server":"notion","query":"update page"}`))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Tools []struct {
			Name   string         `json:"name"`
			Schema map[string]any `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Tools) != 1 {
		t.Fatalf("%v %s", err, out)
	}
	if !reflect.DeepEqual(res.Tools[0].Schema, jsonRoundTrip(t, schema)) {
		t.Fatalf("schema changed on the way to the model: %v", res.Tools[0].Schema)
	}
	for _, want := range []string{`"new_str"`, `"replace_content"`, `"oneOf"`, `"command"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

// A page of schemas must fit what the agent keeps of a tool result in an
// unattended run (24000 characters); beyond that it cuts the middle out.
func TestSearchPageFitsTheUnattendedResultBudget(t *testing.T) {
	schema := notionLikeSchema(120) // about 9 KiB: three fit the old 28 KiB page, not 24000
	names := []string{"notion-update-page", "notion-update-page-b", "notion-update-page-c", "notion-update-page-d"}
	f := newNotionFixture(t, schema, names...)
	p := f.prepare(t)
	search := discoveryTool(t, p, "search_mcp_tools")
	shown := 0
	raw := json.RawMessage(`{"server":"notion","query":"*"}`)
	for page := 0; page < 8; page++ {
		out, err := search.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 24000 {
			t.Fatalf("page is %d bytes; the agent would cut its middle", len(out))
		}
		var res struct {
			Tools []struct {
				Schema map[string]any `json:"input_schema"`
			} `json:"tools"`
			Next *int `json:"next_tool_offset"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("page is not intact JSON: %v", err)
		}
		for _, tool := range res.Tools {
			if !reflect.DeepEqual(tool.Schema, jsonRoundTrip(t, schema)) {
				t.Fatal("a listed schema is incomplete")
			}
			shown++
		}
		if res.Next == nil {
			break
		}
		raw = json.RawMessage(`{"server":"notion","query":"*","tool_offset":` + itoa(*res.Next) + `}`)
	}
	if shown != len(names) {
		t.Fatalf("pages showed %d of %d tools", shown, len(names))
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

// Names alone (the metadata catalog stores descriptions with a placeholder
// schema) never make a tool callable: the first call answers with the real
// schema and executes nothing.
func TestKnownNameCallShowsRealSchemaBeforeRunning(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(20))
	p := f.prepare(t)
	ctx := context.Background()
	// Build the placeholder-schema metadata the catalog search keeps.
	if _, err := discoveryTool(t, p, "search_mcp_catalog").Execute(ctx, json.RawMessage(`{"query":"update page"}`)); err != nil {
		t.Fatal(err)
	}
	call := discoveryTool(t, p, "call_mcp_tool")
	_, err := call.Execute(ctx, json.RawMessage(`{"name":"mcp_notion__notion-update-page","arguments":{"page_id":"p1","content":"x"}}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "schema_required" || o.Certainty != "not_executed" {
		t.Fatalf("blind call: %v", err)
	}
	if !strings.Contains(o.Message, `"new_str"`) || !strings.Contains(o.Message, `"oneOf"`) || strings.Contains(o.Message, `Input schema: {"type":"object"}`) {
		t.Fatalf("reveal lacks the real schema: %s", o.Message)
	}
	if f.calls.Load() != 0 {
		t.Fatal("tool ran on a blind call")
	}
	// The schema is now shown, so a conforming call runs.
	if out, err := call.Execute(ctx, json.RawMessage(validReplace)); err != nil || out != "updated" {
		t.Fatalf("%q %v", out, err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("calls=%d", f.calls.Load())
	}
}

func TestDirectNameCallAlsoShowsSchemaFirst(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(20))
	p := f.prepare(t)
	ctx := context.Background()
	call := discoveryTool(t, p, "call_mcp_tool")
	name, ok := call.ResolveDirectName(ctx, "mcp_notion__notion-update-page")
	if !ok {
		t.Fatal("not resolved")
	}
	_, err := call.Execute(ctx, json.RawMessage(`{"name":"`+name+`","arguments":{"data":{"page_id":"p1","command":"update_properties"}}}`))
	if o, _ := tooloutcome.FromError(err); o.Code != "schema_required" || f.calls.Load() != 0 {
		t.Fatalf("direct call ran blind: %v calls=%d", err, f.calls.Load())
	}
}

func TestSearchedToolRunsWithoutExtraRoundTrip(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(20))
	p := f.prepare(t)
	ctx := context.Background()
	if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"notion","query":"update"}`)); err != nil {
		t.Fatal(err)
	}
	if out, err := discoveryTool(t, p, "call_mcp_tool").Execute(ctx, json.RawMessage(validReplace)); err != nil || out != "updated" {
		t.Fatalf("%q %v", out, err)
	}
}

// When the remote rejects arguments after dispatch, the model gets the full
// schema next to the remote's words, so it can repair instead of giving up.
func TestRemoteRejectionCarriesSchemaForRepair(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(20))
	f.reject.Store(true)
	p := f.prepare(t)
	ctx := context.Background()
	if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"notion","query":"update"}`)); err != nil {
		t.Fatal(err)
	}
	// Valid for the local check (oneOf is satisfied) but rejected remotely.
	_, err := discoveryTool(t, p, "call_mcp_tool").Execute(ctx, json.RawMessage(validReplace))
	o, ok := tooloutcome.FromError(err)
	if !ok {
		t.Fatalf("no outcome: %v", err)
	}
	schemaAt := strings.Index(o.Message, `"new_str"`)
	detailsAt := strings.Index(o.Message, "Untrusted tool-reported details:")
	if schemaAt < 0 || detailsAt < 0 || schemaAt > detailsAt || !strings.Contains(o.Message, `requires a "new_str" parameter`) {
		t.Fatalf("message lacks schema before details: %s", o.Message)
	}
}

// A local schema failure used to cut the schema at 4 KiB, dropping fields
// declared later in a nested schema.
func TestLocalValidationHintKeepsLateFields(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(100))
	p := f.prepare(t)
	ctx := context.Background()
	if _, err := discoveryTool(t, p, "search_mcp_tools").Execute(ctx, json.RawMessage(`{"server":"notion","query":"update"}`)); err != nil {
		t.Fatal(err)
	}
	_, err := discoveryTool(t, p, "call_mcp_tool").Execute(ctx, json.RawMessage(`{"name":"mcp_notion__notion-update-page","arguments":{"data":{"page_id":"p1","command":"replace_content"}}}`))
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != "invalid_arguments" || !strings.Contains(o.Message, `"new_str"`) || strings.Contains(o.Message, "schema truncated") {
		t.Fatalf("%v", err)
	}
}
