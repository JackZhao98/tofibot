package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// Trimming old tool output must never touch a replayed assistant turn: the
// server tool use and its result live in one reasoning item and stay whole.
func TestMicroCompactLeavesReplayedServerToolPairsWhole(t *testing.T) {
	pair := []byte(`[{"type":"thinking","thinking":"t","signature":"s"},{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}},{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[]}]`)
	var msgs []provider.Message
	msgs = append(msgs, provider.Message{Role: "assistant", Content: "a", ReasoningItems: []provider.ReasoningItem{{Provider: "anthropic", Content: pair}}})
	for i := 0; i < 10; i++ {
		msgs = append(msgs, provider.Message{Role: "tool", ToolCallID: "c", ToolName: "x", Content: strings.Repeat("x", 600)})
	}
	out := microCompact(msgs, 6)
	if len(out) != len(msgs) || !bytes.Equal(out[0].ReasoningItems[0].Content, pair) {
		t.Fatal("trimming changed a replayed server tool pair")
	}
	if !strings.Contains(out[1].Content, microCompactMarker) {
		t.Fatal("test premise: old tool output should have been trimmed")
	}
}
