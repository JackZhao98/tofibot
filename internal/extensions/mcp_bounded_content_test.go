package extensions

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBoundedContentLargePayloadIsCutAsTextQuickly(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for b.Len() < 20<<20 {
		b.WriteString(`{"id":"0123456789","name":"element","note":"x"},`)
	}
	b.WriteString(`{"id":"end"}]`)
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
	start := time.Now()
	out := boundedContent(res, 50000)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("took %v", d)
	}
	if !strings.Contains(out, "[truncated: the first 50000 of") || len(out) > 50000+400 {
		t.Fatalf("not a bounded text cut: len=%d", len(out))
	}
}

func TestBoundedContentMidSizeJSONKeepsJSONCut(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 3000; i++ {
		b.WriteString(`{"id":"0123456789","name":"element"},`)
	}
	b.WriteString(`{"id":"end"}]`)
	res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
	out := boundedContent(res, 20000)
	if len(out) > 20000 || strings.Contains(out, "[truncated:") || !strings.HasSuffix(out, "]") {
		t.Fatalf("unexpected cut: len=%d", len(out))
	}
}
