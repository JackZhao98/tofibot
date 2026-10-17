package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/saystream"
)

// A say call's argument JSON, cut so the text arrives in several fragments.
func sayFragments() (whole string, fragments []string, text string) {
	text = "First the finding, then the \"quoted\" detail,\nand a last line."
	b, _ := json.Marshal(map[string]string{"purpose": "answer", "text": text})
	whole = string(b)
	runes := []rune(whole)
	for len(runes) > 9 {
		fragments = append(fragments, string(runes[:9]))
		runes = runes[9:]
	}
	return whole, append(fragments, string(runes)), text
}

// sayDecoding follows provider tool-call deltas the way the host does: a call
// starts at its ID and name, its argument fragments feed one decoder.
type sayDecoding struct {
	dec    map[int]*saystream.Decoder
	pieces []string
}

func (s *sayDecoding) onDelta(d StreamDelta) {
	for _, c := range d.ToolCalls {
		if s.dec == nil {
			s.dec = map[int]*saystream.Decoder{}
		}
		if c.ID != "" || c.Name != "" {
			if c.Name == "say" {
				s.dec[c.Index] = &saystream.Decoder{}
			} else {
				delete(s.dec, c.Index)
			}
		}
		if dec := s.dec[c.Index]; dec != nil && c.Arguments != "" {
			if piece := dec.Feed(c.Arguments); piece != "" {
				s.pieces = append(s.pieces, piece)
			}
		}
	}
}

func (s *sayDecoding) assertProgressive(t *testing.T, provider, text string) {
	t.Helper()
	if got := strings.Join(s.pieces, ""); got != text {
		t.Fatalf("%s: streamed text = %q, want %q", provider, got, text)
	}
	if len(s.pieces) < 3 {
		t.Fatalf("%s: text arrived in %d piece(s), want it progressively: %q", provider, len(s.pieces), s.pieces)
	}
}

func TestAnthropicStreamsSayTextProgressively(t *testing.T) {
	whole, fragments, text := sayFragments()
	events := []string{evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Let me answer."}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_say","name":"say","input":{}}}`}
	for _, f := range fragments {
		events = append(events, `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":`+asJSON(t, f)+`}}`)
	}
	events = append(events, `{"type":"content_block_stop","index":1}`, evDelta("tool_use"), evStop)
	p, _ := anthropicServer(t, streamBody(events...))
	var got sayDecoding
	resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "claude-haiku-5-5", Messages: []Message{{Role: "user", Content: "go"}}}, got.onDelta)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != whole {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	got.assertProgressive(t, "anthropic", text)
}

func responsesSayEvents(t *testing.T, whole string, fragments []string, withDeltas bool) []sseEvent {
	t.Helper()
	call := map[string]any{"type": "function_call", "id": "fc_say", "call_id": "call_say", "name": "say"}
	events := []sseEvent{{"response.output_item.added", map[string]any{"output_index": 0, "item": call}}}
	if withDeltas {
		for _, f := range fragments {
			events = append(events, sseEvent{"response.function_call_arguments.delta", map[string]any{"output_index": 0, "item_id": "fc_say", "delta": f}})
		}
	}
	done := map[string]any{"type": "function_call", "id": "fc_say", "call_id": "call_say", "name": "say", "arguments": whole, "status": "completed"}
	return append(events,
		sseEvent{"response.function_call_arguments.done", map[string]any{"output_index": 0, "item_id": "fc_say", "arguments": whole}},
		sseEvent{"response.output_item.done", map[string]any{"output_index": 0, "item": done}},
		completedSSE)
}

func TestOpenAIResponsesStreamsSayTextProgressively(t *testing.T) {
	whole, fragments, text := sayFragments()
	var got sayDecoding
	resp, err := (&openaiResponses{}).parseStream(strings.NewReader(sseStream(responsesSayEvents(t, whole, fragments, true), true)), got.onDelta)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "say" || resp.ToolCalls[0].Arguments != whole {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	got.assertProgressive(t, "openai responses", text)
}

// The Codex backend is the same Responses client behind a different base URL.
func TestCodexStreamsSayTextProgressively(t *testing.T) {
	whole, fragments, text := sayFragments()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseStream(responsesSayEvents(t, whole, fragments, true), true))
	}))
	defer server.Close()
	codex := &openAICodex{responses: &openaiResponses{apiKey: "token", baseURL: server.URL, noStore: true}}
	var got sayDecoding
	resp, err := codex.ChatStream(context.Background(), &ChatRequest{Model: "codex-gpt-5.6-luna", Messages: []Message{{Role: "user", Content: "go"}}}, got.onDelta)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != whole {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	got.assertProgressive(t, "codex", text)
}

// A backend that sends the arguments only in its done event shows the text at
// once, still complete: correct, just not progressive.
func TestResponsesSayWithoutArgumentDeltasStillDecodes(t *testing.T) {
	whole, fragments, text := sayFragments()
	var got sayDecoding
	if _, err := (&openaiResponses{}).parseStream(strings.NewReader(sseStream(responsesSayEvents(t, whole, fragments, false), true)), got.onDelta); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.pieces, "") != text || len(got.pieces) != 1 {
		t.Fatalf("pieces = %q", got.pieces)
	}
}

func TestChatCompletionsStreamsSayTextProgressively(t *testing.T) {
	whole, fragments, text := sayFragments()
	var body strings.Builder
	chunk := func(tc map[string]any) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{tc}}}}})
		body.WriteString("data: " + string(raw) + "\n\n")
	}
	chunk(map[string]any{"index": 0, "id": "call_say", "type": "function", "function": map[string]any{"name": "say", "arguments": ""}})
	for _, f := range fragments {
		chunk(map[string]any{"index": 0, "function": map[string]any{"arguments": f}})
	}
	body.WriteString("data: [DONE]\n\n")
	var got sayDecoding
	resp, err := (&openaiChatCompletions{}).parseStream(strings.NewReader(body.String()), got.onDelta)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != whole {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	got.assertProgressive(t, "chat completions", text)
}

// Gemini returns a function call whole, so there is nothing to stream
// incrementally: the text arrives in one piece, complete.
func TestGeminiSayArrivesWholeNotIncrementally(t *testing.T) {
	_, _, text := sayFragments()
	args, _ := json.Marshal(map[string]any{"purpose": "answer", "text": text})
	var fc map[string]any
	_ = json.Unmarshal(args, &fc)
	raw, _ := json.Marshal(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{map[string]any{"functionCall": map[string]any{"name": "say", "args": fc}}}}}}})
	var got sayDecoding
	resp, err := (&geminiProvider{}).parseStream(strings.NewReader("data: "+string(raw)+"\n\n"), got.onDelta)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "say" {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if strings.Join(got.pieces, "") != text || len(got.pieces) != 1 {
		t.Fatalf("gemini pieces = %q, want the whole text in one piece", got.pieces)
	}
}

// Strict mode is requested for the say tool and for nothing else; Anthropic
// keeps tool_choice on auto.
func TestStrictToolSchemaIsPerTool(t *testing.T) {
	sayParams := map[string]any{"type": "object", "properties": map[string]any{"purpose": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}}, "required": []string{"purpose", "text"}, "additionalProperties": false}
	tools := []Tool{
		{Name: "say", Description: "d", Parameters: sayParams, Strict: true},
		{Name: "lookup", Description: "d", Parameters: map[string]any{"type": "object"}},
	}
	msgs := []Message{{Role: "user", Content: "hi"}}
	payload := (&openaiResponses{}).buildPayload(&ChatRequest{Model: "gpt-5.5", Messages: msgs, Tools: tools}, true)
	got := map[string]any{}
	for _, tool := range payload["tools"].([]map[string]interface{}) {
		got[tool["name"].(string)] = tool["strict"]
	}
	if got["say"] != true || got["lookup"] != false {
		t.Fatalf("strict flags = %v", got)
	}
	a, _ := (&anthropicProvider{}).buildPayload(&ChatRequest{Model: "claude-sonnet-5-5", Messages: msgs, Tools: tools})
	if _, ok := a["tool_choice"]; ok {
		t.Fatalf("anthropic tool_choice must stay auto (omitted): %v", a["tool_choice"])
	}
}
