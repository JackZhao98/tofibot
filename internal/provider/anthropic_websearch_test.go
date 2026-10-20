package provider

import (
	"context"
	"strings"
	"testing"
)

// A server-side search streams a server_tool_use block (query in input deltas),
// a web_search_tool_result block (whole), then text and the model's own tool call.
var webSearchStream = []string{
	evStart,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Check it."}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-ws"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query\":"}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"synthetic show\"}"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","title":"Synthetic Show","url":"https://example.com/show","page_age":"3 days","encrypted_content":"enc"}]}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_say","name":"say","input":{}}}`,
	`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"purpose\":\"answer\",\"text\":\"ok\"}"}}`,
	`{"type":"content_block_stop","index":3}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42,"server_tool_use":{"web_search_requests":1}}}`,
	evStop,
}

func TestAnthropicServerSideWebSearchIsDeclaredParsedAndReplayed(t *testing.T) {
	p, captured := anthropicServer(t, streamBody(webSearchStream...), streamBody(evStart, evDelta("end_turn"), evStop))
	say := Tool{Name: "say", Description: "speak", Parameters: map[string]any{"type": "object"}}
	req := &ChatRequest{Model: "claude-opus-5-5", ReasoningEffort: "high", Tools: []Tool{say}, WebSearch: &WebSearchSpec{MaxUses: 3},
		Messages: []Message{{Role: "user", Content: "what is it"}}}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	first := (*captured)[0].raw
	if !strings.Contains(first, `"type":"web_search_20250305"`) || !strings.Contains(first, `"max_uses":3`) {
		t.Fatalf("server tool not declared: %s", first)
	}
	if len(resp.WebSearches) != 1 || resp.WebSearches[0].Query != "synthetic show" || len(resp.WebSearches[0].Sources) != 1 ||
		resp.WebSearches[0].Sources[0].URL != "https://example.com/show" || resp.WebSearchRequests != 1 {
		t.Fatalf("searches=%+v billed=%d", resp.WebSearches, resp.WebSearchRequests)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "say" {
		t.Fatalf("the model's own call was lost: %+v", resp.ToolCalls)
	}
	// The next request replays the turn with the search blocks intact.
	next := &ChatRequest{Model: "claude-opus-5-5", ReasoningEffort: "high", Tools: []Tool{say}, WebSearch: req.WebSearch, Messages: []Message{
		{Role: "user", Content: "what is it"},
		{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls, ReasoningItems: resp.ReasoningItems},
		{Role: "tool", ToolCallID: "toolu_say", ToolName: "say", Content: "Delivered"},
	}}
	if _, err := p.Chat(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	second := (*captured)[1].raw
	for _, want := range []string{`"type":"server_tool_use"`, `"query":"synthetic show"`, `"type":"web_search_tool_result"`, `"encrypted_content":"enc"`} {
		if !strings.Contains(second, want) {
			t.Fatalf("replay lacks %s: %s", want, second)
		}
	}
}

func TestAnthropicWebSearchErrorIsAResultNotAFailure(t *testing.T) {
	events := []string{evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_2","name":"web_search","input":{"query":"q"}}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_2","content":{"type":"web_search_tool_result_error","error_code":"max_uses_exceeded"}}}`,
		`{"type":"content_block_stop","index":1}`,
		evDelta("end_turn"), evStop}
	p, _ := anthropicServer(t, streamBody(events...))
	resp, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-haiku-4-5", WebSearch: &WebSearchSpec{}, Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.WebSearches) != 1 || resp.WebSearches[0].Error != "max_uses_exceeded" || len(resp.WebSearches[0].Sources) != 0 {
		t.Fatalf("%+v", resp.WebSearches)
	}
}

func TestAnthropicRequestWithoutWebSearchDeclaresNoServerTool(t *testing.T) {
	p, captured := anthropicServer(t, streamBody(evStart, evDelta("end_turn"), evStop))
	if _, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5", Tools: []Tool{{Name: "x"}}, Messages: []Message{{Role: "user", Content: "q"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains((*captured)[0].raw, "web_search") {
		t.Fatal("web search declared without being asked")
	}
}

func TestAnthropicWebSearchRejectionRetriesOnceWithoutSearchAndIsRemembered(t *testing.T) {
	p, captured := anthropicServer(t,
		errorBody(400, `{"type":"error","error":{"type":"invalid_request_error","message":"The web_search tool is not enabled for this organization."}}`),
		streamBody(evStart, evDelta("end_turn"), evStop),
		streamBody(evStart, evDelta("end_turn"), evStop))
	p.apiKey = "sk-websearch-refused-test"
	req := &ChatRequest{Model: "claude-opus-5-5", WebSearch: &WebSearchSpec{MaxUses: 2}, Tools: []Tool{{Name: "x"}}, Messages: []Message{{Role: "user", Content: "q"}}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("refusal was not absorbed: %v", err)
	}
	if !strings.Contains((*captured)[0].raw, "web_search_20250305") || strings.Contains((*captured)[1].raw, "web_search") {
		t.Fatalf("first request must declare search, the retry must not:\n%s\n%s", (*captured)[0].raw, (*captured)[1].raw)
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(*captured) != 3 || strings.Contains((*captured)[2].raw, "web_search") {
		t.Fatal("a refused credential must not declare search again")
	}
}
