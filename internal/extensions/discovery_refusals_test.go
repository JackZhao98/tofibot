package extensions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Argument refusals of the discovery tools are typed repairs that state the
// value received and the accepted range.
func TestDiscoveryRefusalsNameValueAndRule(t *testing.T) {
	f := newNotionFixture(t, notionLikeSchema(1))
	p := f.prepare(t)
	search, call := discoveryTool(t, p, "search_mcp_tools"), discoveryTool(t, p, "call_mcp_tool")
	cases := []struct {
		tool  string
		raw   string
		wants []string
	}{
		{"search", `{"server":"notion","query":"x","limit":99}`, []string{"1 and 10", "99"}},
		{"search", `{"server":"notion","query":"*","tool_offset":-3}`, []string{"-3"}},
		{"search", `{"query":"*"}`, []string{"set server", "list_mcp_servers"}},
		{"search", `{"server":"notion","query":"x","bogus":1}`, []string{"bogus"}},
		{"call", `{"name":"mcp_notion__nope","arguments":{}}`, []string{"mcp_notion__nope", "search_mcp_tools"}},
		{"call", `{"name":"x","arguments":[]}`, []string{"JSON object"}},
	}
	for _, tc := range cases {
		tool := search
		if tc.tool == "call" {
			tool = call
		}
		_, err := tool.Execute(context.Background(), json.RawMessage(tc.raw))
		o, ok := tooloutcome.FromError(err)
		if !ok {
			t.Fatalf("%s: untyped error %v", tc.raw, err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(o.Message, want) {
				t.Fatalf("%s: message lacks %q: %s", tc.raw, want, o.Message)
			}
		}
	}
}
