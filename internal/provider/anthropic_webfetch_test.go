package provider

import (
	"context"
	"strings"
	"testing"
)

// A server-side fetch streams a server_tool_use block (url in input deltas) and
// a web_fetch_tool_result block (whole) before the model's own text and call.
var webFetchStream = []string{
	evStart,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Read it."}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-wf"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_f1","name":"web_fetch","input":{}}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"url\":"}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"https://example.com/page\"}"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_f1","content":{"type":"web_fetch_result","url":"https://example.com/page","retrieved_at":"2026-10-21T10:00:00Z","content":{"type":"document","title":"Synthetic Page","source":{"type":"text","media_type":"text/plain","data":"page body"}}}}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_say","name":"say","input":{}}}`,
	`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"purpose\":\"answer\",\"text\":\"ok\"}"}}`,
	`{"type":"content_block_stop","index":3}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":42,"server_tool_use":{"web_fetch_requests":1}}}`,
	evStop,
}

func TestAnthropicServerSideWebFetchIsDeclaredParsedAndReplayed(t *testing.T) {
	p, captured := anthropicServer(t, streamBody(webFetchStream...), streamBody(evStart, evDelta("end_turn"), evStop))
	say := Tool{Name: "say", Description: "speak", Parameters: map[string]any{"type": "object"}}
	req := &ChatRequest{Model: "claude-sonnet-5-5", Tools: []Tool{say}, WebSearch: &WebSearchSpec{MaxUses: 3}, WebFetch: &WebFetchSpec{MaxUses: 2, MaxContentTokens: 9000},
		Messages: []Message{{Role: "user", Content: "what is it"}}}
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	first := (*captured)[0].raw
	for _, want := range []string{`"type":"web_fetch_20250910"`, `"name":"web_fetch"`, `"max_uses":2`, `"max_content_tokens":9000`} {
		if !strings.Contains(first, want) {
			t.Fatalf("fetch tool not declared (%s): %s", want, first)
		}
	}
	if s, f, o := strings.Index(first, `"name":"web_search"`), strings.Index(first, `"name":"web_fetch"`), strings.Index(first, `"name":"say"`); !(s >= 0 && s < f && f < o) {
		t.Fatalf("lookups must be listed first, search then fetch: %s", first)
	}
	if len(resp.WebFetches) != 1 || resp.WebFetches[0].URL != "https://example.com/page" || resp.WebFetches[0].Title != "Synthetic Page" ||
		resp.WebFetches[0].RetrievedAt == "" || resp.WebFetchRequests != 1 {
		t.Fatalf("fetches=%+v counted=%d", resp.WebFetches, resp.WebFetchRequests)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "say" {
		t.Fatalf("the model's own call was lost: %+v", resp.ToolCalls)
	}
	next := &ChatRequest{Model: "claude-sonnet-5-5", Tools: []Tool{say}, WebFetch: req.WebFetch, Messages: []Message{
		{Role: "user", Content: "what is it"},
		{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls, ReasoningItems: resp.ReasoningItems},
		{Role: "tool", ToolCallID: "toolu_say", ToolName: "say", Content: "Delivered"},
	}}
	if _, err := p.Chat(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	second := (*captured)[1].raw
	for _, want := range []string{`"type":"server_tool_use"`, `"url":"https://example.com/page"`, `"type":"web_fetch_tool_result"`, `"data":"page body"`} {
		if !strings.Contains(second, want) {
			t.Fatalf("replay lacks %s: %s", want, second)
		}
	}
}

func TestAnthropicWebFetchErrorIsAResultNotAFailure(t *testing.T) {
	events := []string{evStart,
		`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_f2","name":"web_fetch","input":{"url":"https://example.com/x"}}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_f2","content":{"type":"web_fetch_tool_error","error_code":"url_not_accessible"}}}`,
		`{"type":"content_block_stop","index":1}`,
		evDelta("end_turn"), evStop}
	p, _ := anthropicServer(t, streamBody(events...))
	resp, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-sonnet-5-5", WebFetch: &WebFetchSpec{}, Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.WebFetches) != 1 || resp.WebFetches[0].Error != "url_not_accessible" || resp.WebFetches[0].URL != "https://example.com/x" || resp.WebFetches[0].Title != "" {
		t.Fatalf("%+v", resp.WebFetches)
	}
}

func TestAnthropicRequestWithoutWebFetchDeclaresNoPageReader(t *testing.T) {
	p, captured := anthropicServer(t, streamBody(evStart, evDelta("end_turn"), evStop))
	if _, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-sonnet-5-5", WebSearch: &WebSearchSpec{}, Messages: []Message{{Role: "user", Content: "q"}}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains((*captured)[0].raw, "web_fetch") {
		t.Fatal("web fetch declared without being asked")
	}
}

// Fetch is optional: an account without it keeps its searches and its turn, and
// the refusal is remembered for the credential, separately from search.
func TestAnthropicWebFetchRejectionDropsOnlyFetchAndIsRemembered(t *testing.T) {
	p, captured := anthropicServer(t,
		errorBody(400, `{"type":"error","error":{"type":"invalid_request_error","message":"The web_fetch tool is not enabled for this organization."}}`),
		streamBody(evStart, evDelta("end_turn"), evStop),
		streamBody(evStart, evDelta("end_turn"), evStop))
	p.apiKey = "sk-webfetch-refused-test"
	req := &ChatRequest{Model: "claude-sonnet-5-5", WebSearch: &WebSearchSpec{MaxUses: 2}, WebFetch: &WebFetchSpec{MaxUses: 2}, Tools: []Tool{{Name: "x"}}, Messages: []Message{{Role: "user", Content: "q"}}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("refusal was not absorbed: %v", err)
	}
	first, retry := (*captured)[0].raw, (*captured)[1].raw
	if !strings.Contains(first, "web_fetch_20250910") || strings.Contains(retry, "web_fetch") || !strings.Contains(retry, "web_search_20250305") {
		t.Fatalf("the retry must drop fetch and keep search:\n%s\n%s", first, retry)
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if len(*captured) != 3 || strings.Contains((*captured)[2].raw, "web_fetch") || !strings.Contains((*captured)[2].raw, "web_search_20250305") {
		t.Fatal("a refused credential must not declare fetch again, and must keep search")
	}
}

// A search rejection must not be mistaken for a fetch rejection.
func TestAnthropicWebSearchRejectionKeepsFetch(t *testing.T) {
	p, captured := anthropicServer(t,
		errorBody(400, `{"type":"error","error":{"type":"invalid_request_error","message":"The web_search tool is not enabled for this organization."}}`),
		streamBody(evStart, evDelta("end_turn"), evStop))
	p.apiKey = "sk-search-only-refused-test"
	req := &ChatRequest{Model: "claude-sonnet-5-5", WebSearch: &WebSearchSpec{}, WebFetch: &WebFetchSpec{}, Messages: []Message{{Role: "user", Content: "q"}}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if retry := (*captured)[1].raw; strings.Contains(retry, "web_search") || !strings.Contains(retry, "web_fetch_20250910") {
		t.Fatalf("the retry must drop search and keep fetch: %s", retry)
	}
}
