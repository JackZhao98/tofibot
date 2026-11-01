package extensions

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundedContentSaysHowMuchWasLeftOut(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("plain text ", 2000)}}}
	out := boundedContent(res, 1000)
	if !strings.Contains(out, "the first 1000 of ") || !strings.Contains(out, "cursor") {
		t.Fatal(out[len(out)-250:])
	}
}

func TestBoundedContentKeepsJSONValid(t *testing.T) {
	items := make([]map[string]any, 500)
	for i := range items {
		items[i] = map[string]any{"id": i, "title": strings.Repeat("t", 80)}
	}
	raw, _ := json.Marshal(items)
	out := boundedContent(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, 20_000)
	var back []any
	if err := json.Unmarshal([]byte(out), &back); err != nil || len(out) > 20_000 {
		t.Fatalf("err=%v len=%d", err, len(out))
	}
}
