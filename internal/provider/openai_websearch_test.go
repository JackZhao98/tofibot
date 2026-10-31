package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const webSearchSSE = `event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"web_search_call","id":"ws_1","status":"in_progress"}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"tofi release","sources":[{"type":"url","url":"https://example.com/a?utm_source=openai"}]}}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":1,"item":{"type":"web_search_call","id":"ws_2","status":"completed","action":{"type":"search","query":"tofi changelog"}}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Found it."}

event: response.output_text.annotation.added
data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com/a?utm_source=openai","title":"A","start_index":0,"end_index":5}}

event: response.output_text.annotation.added
data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.org/b?utm_source=openai&x=1","title":"B","start_index":0,"end_index":5}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":2,"item":{"type":"message","content":[{"type":"output_text","text":"Found it.","annotations":[{"type":"url_citation","url":"https://example.org/b?utm_source=openai&x=1","title":"B"}]}]}}

event: response.completed
data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":4}}}

`

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return payload
}

func hasHostedSearch(payload map[string]any) bool {
	tools, _ := payload["tools"].([]any)
	for _, raw := range tools {
		if m, ok := raw.(map[string]any); ok && m["type"] == "web_search" {
			return true
		}
	}
	return false
}

func TestOpenAIResponsesSendsHostedSearchOnlyWhenEnabled(t *testing.T) {
	o := &openaiResponses{}
	tools := []Tool{{Name: "save_memory", Parameters: map[string]any{"type": "object"}}}
	on := o.buildPayload(&ChatRequest{Model: "gpt-4o", Tools: tools, WebSearch: &WebSearchSpec{MaxUses: 3}}, true)
	if !hasHostedSearch(map[string]any{"tools": toAnySlice(on["tools"])}) {
		t.Fatalf("hosted search missing: %v", on["tools"])
	}
	if got := on["include"].([]string); len(got) != 1 || got[0] != "web_search_call.action.sources" {
		t.Fatalf("include = %v", got)
	}
	if n := len(on["tools"].([]map[string]interface{})); n != 2 {
		t.Fatalf("tools = %d, want function + hosted search", n)
	}
	off := o.buildPayload(&ChatRequest{Model: "gpt-4o", Tools: tools}, true)
	if hasHostedSearch(map[string]any{"tools": toAnySlice(off["tools"])}) {
		t.Fatal("hosted search sent while disabled")
	}
	if _, ok := off["include"]; ok {
		t.Fatalf("include sent without a reason: %v", off["include"])
	}
	only := o.buildPayload(&ChatRequest{Model: "gpt-4o", WebSearch: &WebSearchSpec{}}, true)
	if n := len(only["tools"].([]map[string]interface{})); n != 1 {
		t.Fatalf("search alone: tools = %d", n)
	}
}

func toAnySlice(v any) []any {
	typed, _ := v.([]map[string]interface{})
	out := make([]any, 0, len(typed))
	for _, m := range typed {
		out = append(out, m)
	}
	return out
}

func TestOpenAIResponsesStreamParsesSearchesAndCitations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasHostedSearch(decodeBody(t, r)) {
			t.Error("request carries no hosted search")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(webSearchSSE))
	}))
	defer server.Close()
	o := &openaiResponses{apiKey: "k", baseURL: server.URL}
	got, err := o.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.5-streams", WebSearch: &WebSearchSpec{MaxUses: 3}, Messages: []Message{{Role: "user", Content: "hi"}}}, func(StreamDelta) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "Found it." {
		t.Fatalf("content = %q", got.Content)
	}
	if got.WebSearchRequests != 2 || len(got.WebSearches) != 2 {
		t.Fatalf("searches = %+v requests=%d", got.WebSearches, got.WebSearchRequests)
	}
	first, second := got.WebSearches[0], got.WebSearches[1]
	if first.ID != "ws_1" || first.Query != "tofi release" || len(first.Sources) != 1 || first.Sources[0].URL != "https://example.com/a" {
		t.Fatalf("first search = %+v", first)
	}
	// The citation the search action did not list lands on the last search,
	// without the provider's utm tag; the one it listed is not repeated.
	if len(second.Sources) != 1 || second.Sources[0].URL != "https://example.org/b?x=1" || second.Sources[0].Title != "B" {
		t.Fatalf("second search = %+v", second)
	}
	if strings.Contains(first.Sources[0].URL+second.Sources[0].URL, "utm_source") {
		t.Fatal("utm tag kept")
	}
}

func TestOpenAICodexParsesSearchAndRespectsNonStreamedResponse(t *testing.T) {
	complete := `{"output":[{"type":"web_search_call","id":"ws_9","status":"completed","action":{"type":"search","query":"q"}},{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[{"type":"url_citation","url":"https://e.example/p","title":"P"}]}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	got, err := (&openaiResponses{}).parseResponse(complete)
	if err != nil {
		t.Fatal(err)
	}
	if got.WebSearchRequests != 1 || len(got.WebSearches) != 1 || len(got.WebSearches[0].Sources) != 1 || got.WebSearches[0].Sources[0].Title != "P" {
		t.Fatalf("parsed = %+v", got.WebSearches)
	}
}

func TestOpenAIHostedSearchRefusalFallsBackOnceOnBothPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		make func(url string) Provider
		mdl  string
	}{
		{"api key", `{"error":{"message":"Tool 'web_search' is not supported with this model.","type":"invalid_request_error"}}`,
			func(u string) Provider { return &openaiResponses{apiKey: "k", baseURL: u} }, "gpt-refuse-api"},
		{"codex", `{"detail":"Unsupported tool type: web_search"}`,
			func(u string) Provider {
				return &openAICodex{responses: &openaiResponses{apiKey: "t", baseURL: u, noStore: true}}
			}, "codex-gpt-refuse-codex"},
		{"invalid value", `{"error":{"message":"Invalid value: 'web_search'. Supported values are: 'function'."}}`,
			func(u string) Provider { return &openaiResponses{apiKey: "k", baseURL: u} }, "gpt-refuse-invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var withTool, withoutTool atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hasHostedSearch(decodeBody(t, r)) {
					withTool.Add(1)
					w.WriteHeader(400)
					_, _ = w.Write([]byte(tc.body))
					return
				}
				withoutTool.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"plain\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{}}}\n\n"))
			}))
			defer server.Close()
			p := tc.make(server.URL)
			req := func() *ChatRequest {
				return &ChatRequest{Model: tc.mdl, WebSearch: &WebSearchSpec{MaxUses: 3}, Messages: []Message{{Role: "user", Content: "hi"}}}
			}
			got, err := p.ChatStream(context.Background(), req(), nil)
			if err != nil || got.Content != "plain" {
				t.Fatalf("got=%+v err=%v", got, err)
			}
			if withTool.Load() != 1 || withoutTool.Load() != 1 {
				t.Fatalf("first turn: with=%d without=%d", withTool.Load(), withoutTool.Load())
			}
			// Remembered: the next turn never sends the tool again.
			if _, err = p.ChatStream(context.Background(), req(), nil); err != nil {
				t.Fatal(err)
			}
			if withTool.Load() != 1 || withoutTool.Load() != 2 {
				t.Fatalf("second turn: with=%d without=%d", withTool.Load(), withoutTool.Load())
			}
		})
	}
}

func TestOpenAIHostedSearchOtherErrorsAreNotRefusals(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"message":"Item 'ws_1' of type 'web_search_call' was provided without its required following item."}}`))
	}))
	defer server.Close()
	o := &openaiResponses{apiKey: "k", baseURL: server.URL}
	_, err := o.ChatStream(context.Background(), &ChatRequest{Model: "gpt-norefuse", WebSearch: &WebSearchSpec{}, Messages: []Message{{Role: "user", Content: "hi"}}}, nil)
	if err == nil {
		t.Fatal("error swallowed")
	}
	if calls.Load() != 1 || o.webSearchRefused("gpt-norefuse") {
		t.Fatalf("a malformed-history error disabled search: calls=%d", calls.Load())
	}
}
