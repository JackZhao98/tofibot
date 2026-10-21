package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	pairingErrorBody = `{"type":"error","error":{"type":"invalid_request_error","message":"messages.7: ` + "`web_search`" + ` tool use with id ` + "`srvtoolu_x`" + ` was found without a corresponding ` + "`web_search_tool_result`" + ` block"}}`
	thinkingBlock    = `{"type":"thinking","thinking":"t","signature":"sig"}`
	searchUse        = `{"type":"server_tool_use","id":"srvtoolu_s","name":"web_search","input":{"query":"q"}}`
	searchResult     = `{"type":"web_search_tool_result","tool_use_id":"srvtoolu_s","content":[{"type":"web_search_result","title":"T","url":"https://example.com","encrypted_content":"enc"}]}`
	fetchUse         = `{"type":"server_tool_use","id":"srvtoolu_f","name":"web_fetch","input":{"url":"https://example.com"}}`
	fetchResult      = `{"type":"web_fetch_tool_result","tool_use_id":"srvtoolu_f","content":{"type":"web_fetch_result","url":"https://example.com","content":{"type":"document","title":"T","source":{"type":"text","media_type":"text/plain","data":"body"}}}}`
	citedText        = `{"type":"text","text":"answer","citations":[{"type":"web_search_result_location","url":"https://example.com","title":"T","encrypted_index":"i","cited_text":"c"}]}`
)

// historyWith builds a conversation whose first assistant turn replays the
// given raw blocks.
func historyWith(blocks ...string) []Message {
	raw := make([]json.RawMessage, len(blocks))
	for i, b := range blocks {
		raw[i] = json.RawMessage(b)
	}
	content, _ := json.Marshal(raw)
	return []Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "answer", ReasoningItems: []ReasoningItem{{Provider: anthropicReasoningSource, Content: content}}},
		{Role: "user", Content: "next"},
	}
}

// assertPaired fails when a request body holds a server_tool_use without its
// result block or a result without its use.
func assertPaired(t *testing.T, body string) {
	t.Helper()
	var payload struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, m := range payload.Messages {
		var blocks []struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ToolUseID string `json:"tool_use_id"`
		}
		if json.Unmarshal(m.Content, &blocks) != nil {
			continue
		}
		uses, results := map[string]bool{}, map[string]bool{}
		for _, b := range blocks {
			if b.Type == "server_tool_use" {
				uses[b.ID] = true
			} else if serverToolResultType(b.Type) {
				results[b.ToolUseID] = true
			}
		}
		for id := range uses {
			if !results[id] {
				t.Fatalf("server_tool_use %s travels without its result: %s", id, body)
			}
		}
		for id := range results {
			if !uses[id] {
				t.Fatalf("server tool result for %s travels without its use: %s", id, body)
			}
		}
	}
}

func chatWith(t *testing.T, msgs []Message, replies ...func(w http.ResponseWriter)) (*anthropicProvider, *[]capturedRequest, error) {
	t.Helper()
	p, captured := anthropicServer(t, replies...)
	p.apiKey = "sk-pair-" + t.Name()
	_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-sonnet-5-5", ReasoningEffort: "high", Tools: []Tool{{Name: "say", Description: "s"}}, Messages: msgs})
	return p, captured, err
}

// A history cut in the middle of a pair (the use kept, the result lost, or the
// reverse) never reaches the API unpaired: the pair is dropped whole, with the
// citations that pointed into it.
func TestReplayedServerToolBlocksAlwaysTravelInPairs(t *testing.T) {
	ok := streamBody(evStart, evDelta("end_turn"), evStop)
	for name, blocks := range map[string][]string{
		"search use without result":  {thinkingBlock, searchUse, citedText},
		"search result without use":  {thinkingBlock, searchResult, citedText},
		"fetch use without result":   {thinkingBlock, fetchUse, citedText},
		"fetch result without use":   {thinkingBlock, fetchResult, citedText},
		"second pair cut mid-way":    {thinkingBlock, searchUse, searchResult, fetchUse, citedText},
		"mismatched ids":             {thinkingBlock, searchUse, strings.Replace(searchResult, "srvtoolu_s", "srvtoolu_other", 1), citedText},
		"complete pairs stay paired": {thinkingBlock, searchUse, searchResult, fetchUse, fetchResult, citedText},
	} {
		t.Run(name, func(t *testing.T) {
			_, captured, err := chatWith(t, historyWith(blocks...), ok)
			if err != nil {
				t.Fatal(err)
			}
			body := (*captured)[0].raw
			assertPaired(t, body)
			if !strings.Contains(body, `"signature":"sig"`) {
				t.Fatalf("the thinking block was lost: %s", body)
			}
			complete := name == "complete pairs stay paired"
			keepsFirstPair := complete || name == "second pair cut mid-way"
			if keepsFirstPair != strings.Contains(body, "srvtoolu_s") {
				t.Fatalf("complete pairs must be kept and broken ones dropped: %s", body)
			}
			if !complete && strings.Contains(body, "citations") {
				t.Fatalf("a citation survived the loss of its result: %s", body)
			}
		})
	}
}

// If an unpaired block still gets through and the API answers with the pairing
// error, the turn is retried once with every replayed server tool block
// stripped, and search stays enabled for the credential.
func TestPairingErrorRetriesOnceWithServerBlocksStrippedAndDoesNotDisableSearch(t *testing.T) {
	ok := streamBody(evStart, evDelta("end_turn"), evStop)
	p, captured, err := chatWith(t, historyWith(thinkingBlock, searchUse, searchResult, citedText),
		errorBody(400, pairingErrorBody), ok, ok)
	if err != nil {
		t.Fatalf("the pairing error failed the turn: %v", err)
	}
	if len(*captured) != 2 {
		t.Fatalf("want exactly one retry, got %d requests", len(*captured))
	}
	if !strings.Contains((*captured)[0].raw, "srvtoolu_s") {
		t.Fatal("test premise: the first request should replay the pair")
	}
	retry := (*captured)[1].raw
	if strings.Contains(retry, "server_tool_use") || strings.Contains(retry, "web_search_tool_result") || strings.Contains(retry, "citations") {
		t.Fatalf("the retry still carries server blocks: %s", retry)
	}
	if !strings.Contains(retry, `"signature":"sig"`) {
		t.Fatalf("the retry lost the thinking block: %s", retry)
	}
	// The credential still gets search on the next request.
	req := &ChatRequest{Model: "claude-sonnet-5-5", WebSearch: &WebSearchSpec{}, WebFetch: &WebFetchSpec{}, Messages: []Message{{Role: "user", Content: "q"}}}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	last := (*captured)[len(*captured)-1].raw
	if !strings.Contains(last, "web_search_20250305") || !strings.Contains(last, "web_fetch_20250910") {
		t.Fatalf("a malformed-history error disabled the tools: %s", last)
	}
}

// Only "this credential cannot use the tool" errors are refusals; an error
// that merely mentions the tool never is.
func TestOnlyCapabilityErrorsAreToolRefusals(t *testing.T) {
	mk := func(status int, msg string) error {
		return NewAPIError("anthropic", status, `{"type":"error","error":{"type":"invalid_request_error","message":"`+msg+`"}}`)
	}
	for _, c := range []struct {
		name   string
		err    error
		search bool
		fetch  bool
	}{
		{"search not enabled", mk(400, "The web_search tool is not enabled for this organization."), true, false},
		{"search not supported by model", mk(400, "tools.0: web_search_20250305 is not supported for this model"), true, false},
		{"search forbidden", mk(403, "Web search is not permitted for this API key"), true, false},
		{"fetch not enabled", mk(400, "The web_fetch tool is not enabled for this organization."), false, true},
		{"pairing error on search", mk(400, "messages.7: `web_search` tool use with id `srvtoolu_x` was found without a corresponding `web_search_tool_result` block"), false, false},
		{"pairing error on fetch", mk(400, "messages.3: `web_fetch` tool use with id `srvtoolu_y` was found without a corresponding `web_fetch_tool_result` block"), false, false},
		{"invalid input mentioning search", mk(400, "tools.0.web_search: max_uses must be positive"), false, false},
		{"server error", mk(500, "web_search is not enabled"), false, false},
	} {
		if got := isAnthropicWebSearchRejection(c.err); got != c.search {
			t.Errorf("%s: search refusal=%v want %v", c.name, got, c.search)
		}
		if got := isAnthropicWebFetchRejection(c.err); got != c.fetch {
			t.Errorf("%s: fetch refusal=%v want %v", c.name, got, c.fetch)
		}
	}
	if !isAnthropicServerToolPairingError(mk(400, "messages.7: `web_search` tool use with id `srvtoolu_x` was found without a corresponding `web_search_tool_result` block")) {
		t.Error("the production pairing error is not recognised")
	}
	if isAnthropicServerToolPairingError(mk(400, "The web_search tool is not enabled for this organization.")) {
		t.Error("a capability error was taken for a pairing error")
	}
}
