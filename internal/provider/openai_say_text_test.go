package provider

import (
	"strings"
	"testing"
)

const sayArgsText = `{"purpose":"answer","text":"A synthetic reply."}`

// The Responses converter maps a say function_call item to a tool call, and
// the same arguments arriving as output_text stay text. Pinning both shows the
// provider never turns text into a call or drops a declared call.
func TestResponsesStreamKeepsSayCallAndSayTextApart(t *testing.T) {
	o := &openaiResponses{}
	asCall := sseStream([]sseEvent{
		{"response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "say"}}},
		{"response.function_call_arguments.delta", map[string]any{"output_index": 0, "item_id": "fc_1", "delta": sayArgsText}},
		{"response.output_item.done", map[string]any{"output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "say", "arguments": sayArgsText}}},
		completedSSE,
	}, true)
	got, err := o.parseStream(strings.NewReader(asCall), nil)
	if err != nil || len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "say" || got.ToolCalls[0].Arguments != sayArgsText || got.Content != "" {
		t.Fatalf("call stream: %+v err=%v", got, err)
	}
	asText := sseStream([]sseEvent{
		{"response.output_item.added", map[string]any{"output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant"}}},
		{"response.output_text.delta", map[string]any{"output_index": 0, "item_id": "msg_1", "delta": sayArgsText}},
		{"response.output_item.done", map[string]any{"output_index": 0, "item": map[string]any{"type": "message", "id": "msg_1", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": sayArgsText}}}}},
		completedSSE,
	}, true)
	got, err = o.parseStream(strings.NewReader(asText), nil)
	if err != nil || len(got.ToolCalls) != 0 || got.Content != sayArgsText {
		t.Fatalf("text stream: %+v err=%v", got, err)
	}
}
