package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sse renders Anthropic stream events, each as event: + data: lines.
func sse(events ...string) string {
	var b strings.Builder
	for _, data := range events {
		var head struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(data), &head)
		b.WriteString("event: " + head.Type + "\ndata: " + data + "\n\n")
	}
	return b.String()
}

const (
	evStart = `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"usage":{"input_tokens":100,"cache_read_input_tokens":2000,"cache_creation_input_tokens":300,"output_tokens":1}}}`
	evStop  = `{"type":"message_stop"}`
)

func evDelta(stop string) string {
	return `{"type":"message_delta","delta":{"stop_reason":"` + stop + `","stop_sequence":null},"usage":{"output_tokens":42}}`
}

type capturedRequest struct {
	header http.Header
	body   map[string]any
	raw    string
}

// anthropicServer serves one canned stream per request, recording each body.
func anthropicServer(t *testing.T, responses ...func(w http.ResponseWriter)) (*anthropicProvider, *[]capturedRequest) {
	t.Helper()
	var mu sync.Mutex
	var captured []capturedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		i := len(captured)
		captured = append(captured, capturedRequest{header: r.Header.Clone(), body: body, raw: string(raw)})
		mu.Unlock()
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if i >= len(responses) {
			t.Errorf("unexpected request %d", i+1)
			w.WriteHeader(500)
			return
		}
		responses[i](w)
	}))
	t.Cleanup(server.Close)
	return &anthropicProvider{apiKey: "sk-test", baseURL: server.URL}, &captured
}

func streamBody(events ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(events...))
	}
}

func errorBody(status int, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func asJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// toolTurnStream streams thinking (with signature), a redacted block, text,
// and two parallel tool calls whose argument deltas interleave.
var toolTurnStream = []string{
	evStart,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Need two "}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"lookups."}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"redacted_thinking","data":"opaque-data"}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"text","text":""}}`,
	`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Checking "}}`,
	`{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"both."}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_a","name":"lookup","input":{}}}`,
	`{"type":"content_block_start","index":4,"content_block":{"type":"tool_use","id":"toolu_b","name":"fetch","input":{}}}`,
	`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}`,
	`{"type":"content_block_delta","index":4,"delta":{"type":"input_json_delta","partial_json":"{\"url\":\"https://x\"}"}}`,
	`{"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"\"tofi\"}"}}`,
	`{"type":"content_block_stop","index":3}`,
	`{"type":"content_block_stop","index":4}`,
	`{"type":"ping"}`,
	evDelta("tool_use"),
	evStop,
}

func TestAnthropicStreamParsesThinkingTextAndParallelToolCalls(t *testing.T) {
	p, _ := anthropicServer(t, streamBody(toolTurnStream...))
	var content, reasoning strings.Builder
	args := map[int]string{}
	names := map[int]string{}
	resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "claude-opus-5-5", Messages: []Message{{Role: "user", Content: "go"}}}, func(d StreamDelta) {
		content.WriteString(d.Content)
		reasoning.WriteString(d.Reasoning)
		for _, c := range d.ToolCalls {
			args[c.Index] += c.Arguments
			if c.Name != "" {
				names[c.Index] = c.Name
			}
		}
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Content != "Checking both." || content.String() != resp.Content {
		t.Fatalf("content = %q streamed %q", resp.Content, content.String())
	}
	if resp.Reasoning != "Need two lookups." || reasoning.String() != "Need two lookups." {
		t.Fatalf("reasoning = %q streamed %q", resp.Reasoning, reasoning.String())
	}
	want := []ToolCall{{ID: "toolu_a", Name: "lookup", Arguments: `{"q":"tofi"}`}, {ID: "toolu_b", Name: "fetch", Arguments: `{"url":"https://x"}`}}
	if asJSON(t, resp.ToolCalls) != asJSON(t, want) {
		t.Fatalf("tool calls = %+v", resp.ToolCalls)
	}
	if args[3] != `{"q":"tofi"}` || args[4] != `{"url":"https://x"}` || names[3] != "lookup" || names[4] != "fetch" {
		t.Fatalf("streamed tool deltas = %v %v", args, names)
	}
	if resp.Usage != (Usage{InputTokens: 2400, OutputTokens: 42, CacheReadTokens: 2000, CacheWriteTokens: 300}) {
		t.Fatalf("usage = %+v", resp.Usage)
	}
	if len(resp.ReasoningItems) != 1 || resp.ReasoningItems[0].Provider != "anthropic" || resp.ReasoningItems[0].EncryptedContent != "" {
		t.Fatalf("reasoning items = %+v", resp.ReasoningItems)
	}
	wantBlocks := `[{"signature":"sig-abc","thinking":"Need two lookups.","type":"thinking"},{"data":"opaque-data","type":"redacted_thinking"},{"text":"Checking both.","type":"text"},{"id":"toolu_a","input":{"q":"tofi"},"name":"lookup","type":"tool_use"},{"id":"toolu_b","input":{"url":"https://x"},"name":"fetch","type":"tool_use"}]`
	if string(resp.ReasoningItems[0].Content) != wantBlocks {
		t.Fatalf("captured blocks =\n%s\nwant\n%s", resp.ReasoningItems[0].Content, wantBlocks)
	}
}

func TestAnthropicRequestShape(t *testing.T) {
	p, captured := anthropicServer(t, streamBody(evStart, evDelta("end_turn"), evStop))
	first, _ := anthropicServer(t, streamBody(toolTurnStream...))
	prior, err := first.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5", Messages: []Message{{Role: "user", Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}

	req := &ChatRequest{
		Model:           "claude-opus-5-5",
		ReasoningEffort: "xhigh",
		System:          "You are Tofi.",
		Tools: []Tool{
			{Name: "lookup", Description: "Look up", Parameters: map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}},
			{Name: "fetch", Description: "Fetch", Parameters: map[string]any{"type": "object"}},
		},
		Messages: []Message{
			{Role: "user", Content: "look", ImageURLs: []string{"data:image/png;base64,AAAA", "https://example.com/a.jpg"}},
			{Role: "assistant", Content: prior.Content, ToolCalls: prior.ToolCalls, ReasoningItems: prior.ReasoningItems},
			{Role: "tool", ToolCallID: "toolu_a", ToolName: "lookup", Content: "found"},
			{Role: "tool", ToolCallID: "toolu_b", ToolName: "fetch", Content: "boom", ToolFailed: true, ImageURLs: []string{"data:image/jpeg;base64,BBBB"}},
		},
	}
	if _, err := p.Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	got := (*captured)[0]
	if got.header.Get("x-api-key") != "sk-test" || got.header.Get("anthropic-version") != "2023-06-01" {
		t.Fatalf("headers = %v", got.header)
	}
	if !strings.Contains(got.header.Get("anthropic-beta"), "thinking-binding-controls-2026-08-01") {
		t.Fatalf("anthropic-beta = %q", got.header.Get("anthropic-beta"))
	}
	body := got.body
	if body["stream"] != true || body["max_tokens"] != float64(64000) || body["model"] != "claude-opus-5-5" {
		t.Fatalf("top-level = stream %v max_tokens %v model %v", body["stream"], body["max_tokens"], body["model"])
	}
	if asJSON(t, body["system"]) != `[{"cache_control":{"type":"ephemeral"},"text":"You are Tofi.","type":"text"}]` {
		t.Fatalf("system = %s", asJSON(t, body["system"]))
	}
	if asJSON(t, body["thinking"]) != `{"block_binding":{"prefix_mismatch_behavior":"drop_block"},"display":"summarized","type":"adaptive"}` {
		t.Fatalf("thinking = %s", asJSON(t, body["thinking"]))
	}
	if asJSON(t, body["output_config"]) != `{"effort":"xhigh"}` {
		t.Fatalf("output_config = %s", asJSON(t, body["output_config"]))
	}
	for _, field := range []string{"temperature", "top_p", "tool_choice"} {
		if _, ok := body[field]; ok {
			t.Fatalf("unexpected %s", field)
		}
	}
	tools := body["tools"].([]any)
	if len(tools) != 2 || tools[0].(map[string]any)["cache_control"] != nil || asJSON(t, tools[1].(map[string]any)["cache_control"]) != `{"type":"ephemeral"}` {
		t.Fatalf("tools = %s", asJSON(t, tools))
	}
	if tools[0].(map[string]any)["input_schema"] == nil {
		t.Fatalf("tool schema missing: %s", asJSON(t, tools[0]))
	}

	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages = %s", asJSON(t, msgs))
	}
	user := asJSON(t, msgs[0])
	wantUser := `{"content":[{"source":{"data":"AAAA","media_type":"image/png","type":"base64"},"type":"image"},{"source":{"type":"url","url":"https://example.com/a.jpg"},"type":"image"},{"cache_control":{"type":"ephemeral"},"text":"look","type":"text"}],"role":"user"}`
	if user != wantUser {
		t.Fatalf("user turn =\n%s\nwant\n%s", user, wantUser)
	}
	// The assistant turn replays the captured blocks verbatim, in order.
	assistant := msgs[1].(map[string]any)
	if asJSON(t, assistant["content"]) != string(prior.ReasoningItems[0].Content) {
		t.Fatalf("assistant replay =\n%s\nwant\n%s", asJSON(t, assistant["content"]), prior.ReasoningItems[0].Content)
	}
	// Both tool results share one user turn; the failure carries is_error and
	// its image; the last block holds the rolling cache breakpoint.
	results := asJSON(t, msgs[2])
	wantResults := `{"content":[{"content":"found","tool_use_id":"toolu_a","type":"tool_result"},{"cache_control":{"type":"ephemeral"},"content":[{"text":"boom","type":"text"},{"source":{"data":"BBBB","media_type":"image/jpeg","type":"base64"},"type":"image"}],"is_error":true,"tool_use_id":"toolu_b","type":"tool_result"}],"role":"user"}`
	if results != wantResults {
		t.Fatalf("tool results =\n%s\nwant\n%s", results, wantResults)
	}
	if n := strings.Count(got.raw, `"cache_control"`); n != 4 {
		t.Fatalf("cache breakpoints = %d, want 4 (max)", n)
	}
}

func TestAnthropicReplayFallsBackWhenTurnWasEdited(t *testing.T) {
	first, _ := anthropicServer(t, streamBody(toolTurnStream...))
	prior, err := first.Chat(context.Background(), &ChatRequest{Model: "claude-sonnet-5-5", Messages: []Message{{Role: "user", Content: "go"}}})
	if err != nil {
		t.Fatal(err)
	}
	edited := prior.ToolCalls[:1]
	msgs, replayed := convertAnthropicMessages([]Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: prior.Content, ToolCalls: edited, ReasoningItems: prior.ReasoningItems},
		{Role: "tool", ToolCallID: "toolu_a", Content: "ok"},
	}, true)
	if !replayed {
		t.Fatal("thinking not replayed")
	}
	got := asJSON(t, msgs[1].Content)
	want := `[{"signature":"sig-abc","thinking":"Need two lookups.","type":"thinking"},{"data":"opaque-data","type":"redacted_thinking"},{"text":"Checking both.","type":"text"},{"id":"toolu_a","input":{"q":"tofi"},"name":"lookup","type":"tool_use"}]`
	if got != want {
		t.Fatalf("assistant blocks =\n%s\nwant\n%s", got, want)
	}
}

func TestAnthropicOmitReasoningReplayDropsThinkingAndBeta(t *testing.T) {
	first, _ := anthropicServer(t, streamBody(toolTurnStream...))
	prior, _ := first.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5", Messages: []Message{{Role: "user", Content: "go"}}})
	p, captured := anthropicServer(t, streamBody(evStart, evDelta("end_turn"), evStop))
	_, err := p.Chat(context.Background(), &ChatRequest{
		Model:               "claude-opus-5-5",
		OmitReasoningReplay: true,
		Messages: []Message{
			{Role: "user", Content: "go"},
			{Role: "assistant", Content: prior.Content, ToolCalls: prior.ToolCalls, ReasoningItems: prior.ReasoningItems},
			{Role: "tool", ToolCallID: "toolu_a", Content: "a"},
			{Role: "tool", ToolCallID: "toolu_b", Content: "b"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := (*captured)[0]
	if strings.Contains(got.raw, "thinking\"") && strings.Contains(got.raw, "sig-abc") || strings.Contains(got.raw, "opaque-data") || strings.Contains(got.raw, "block_binding") || got.header.Get("anthropic-beta") != "" {
		t.Fatalf("replay leaked: beta=%q body=%s", got.header.Get("anthropic-beta"), got.raw)
	}
}

func TestAnthropicReplayRejectionRetriesOnceWithoutThinking(t *testing.T) {
	first, _ := anthropicServer(t, streamBody(toolTurnStream...))
	prior, _ := first.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5", Messages: []Message{{Role: "user", Content: "go"}}})
	rejection := `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid ` + "`signature`" + ` in ` + "`thinking`" + ` block."}}`
	p, captured := anthropicServer(t, errorBody(400, rejection), streamBody(evStart, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`, evDelta("end_turn"), evStop))
	resp, err := p.ChatStream(context.Background(), &ChatRequest{
		Model: "claude-opus-5-5",
		Messages: []Message{
			{Role: "user", Content: "go"},
			{Role: "assistant", Content: prior.Content, ToolCalls: prior.ToolCalls, ReasoningItems: prior.ReasoningItems},
			{Role: "tool", ToolCallID: "toolu_a", Content: "a"},
			{Role: "tool", ToolCallID: "toolu_b", Content: "b"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if !resp.ReasoningReplayRejected || resp.Content != "done" || len(*captured) != 2 {
		t.Fatalf("resp=%+v requests=%d", resp, len(*captured))
	}
	if !strings.Contains((*captured)[0].raw, "sig-abc") || strings.Contains((*captured)[1].raw, "sig-abc") {
		t.Fatal("retry must drop the replayed thinking")
	}
}

func TestAnthropicStopReasons(t *testing.T) {
	text := []string{evStart, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`}
	t.Run("max_tokens", func(t *testing.T) {
		p, _ := anthropicServer(t, streamBody(append(text, evDelta("max_tokens"), evStop)...))
		_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		var incomplete *IncompleteResponseError
		if !errors.As(err, &incomplete) || incomplete.Reason != "max_tokens" || IsRetryable(err) || IsContextOverflow(err) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("context window", func(t *testing.T) {
		p, _ := anthropicServer(t, streamBody(append(text, evDelta("model_context_window_exceeded"), evStop)...))
		_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		if !IsContextOverflow(err) {
			t.Fatalf("err = %v, want context overflow", err)
		}
	})
	t.Run("refusal", func(t *testing.T) {
		refusal := `{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","category":"cyber","explanation":"declined"}},"usage":{"output_tokens":3}}`
		p, _ := anthropicServer(t, streamBody(append(text, refusal, evStop)...))
		_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		var refused *RefusalError
		if !errors.As(err, &refused) || refused.Category != "cyber" || IsRetryable(err) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("eof without message_stop", func(t *testing.T) {
		p, _ := anthropicServer(t, streamBody(append(text, evDelta("end_turn"))...))
		_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		if !errors.Is(err, ErrStreamIncomplete) || !IsRetryable(err) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAnthropicErrorClassification(t *testing.T) {
	overloaded := `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	t.Run("529 retried", func(t *testing.T) {
		p, captured := anthropicServer(t, errorBody(529, overloaded), errorBody(429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`), streamBody(evStart, evDelta("end_turn"), evStop))
		r := NewRetryProvider(p, RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
		if _, err := r.ChatStream(context.Background(), &ChatRequest{Model: "claude-haiku-4-5"}, func(StreamDelta) {}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(*captured) != 3 {
			t.Fatalf("requests = %d", len(*captured))
		}
	})
	t.Run("in-stream overloaded is a 529", func(t *testing.T) {
		p, _ := anthropicServer(t, streamBody(evStart, overloaded))
		_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		apiErr, ok := AsAPIError(err)
		if !ok || apiErr.StatusCode != 529 || !IsRetryable(err) || !IsRateLimited(err) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("prompt too long is overflow, not retried", func(t *testing.T) {
		p, captured := anthropicServer(t, errorBody(400, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 1000123 tokens > 1000000 maximum"}}`))
		r := NewRetryProvider(p, RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond})
		_, err := r.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		if !IsContextOverflow(err) || len(*captured) != 1 {
			t.Fatalf("err = %v requests = %d", err, len(*captured))
		}
	})
	t.Run("401 not retried", func(t *testing.T) {
		p, captured := anthropicServer(t, errorBody(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
		r := NewRetryProvider(p, RetryConfig{MaxRetries: 3, BaseDelay: time.Millisecond})
		_, err := r.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
		if apiErr, ok := AsAPIError(err); !ok || apiErr.StatusCode != 401 || len(*captured) != 1 {
			t.Fatalf("err = %v requests = %d", err, len(*captured))
		}
	})
}

func TestAnthropicStreamIdleWatchdog(t *testing.T) {
	previous := streamIdleTimeout
	streamIdleTimeout = 80 * time.Millisecond
	defer func() { streamIdleTimeout = previous }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse(evStart))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	p := &anthropicProvider{apiKey: "k", baseURL: server.URL}
	_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
	if !IsStreamIdle(err) {
		t.Fatalf("err = %v, want StreamIdleError", err)
	}
}

func TestAnthropicStreamWallCap(t *testing.T) {
	previousIdle, previousWall := streamIdleTimeout, streamWallCap
	streamIdleTimeout, streamWallCap = time.Second, 150*time.Millisecond
	defer func() { streamIdleTimeout, streamWallCap = previousIdle, previousWall }()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			if _, err := io.WriteString(w, sse(`{"type":"ping"}`)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}))
	defer server.Close()
	p := &anthropicProvider{apiKey: "k", baseURL: server.URL}
	_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-opus-5-5"})
	var wall *StreamWallCapError
	if !errors.As(err, &wall) || !IsStreamWatchdog(err) {
		t.Fatalf("err = %v, want StreamWallCapError", err)
	}
}

func TestAnthropicEffortMapping(t *testing.T) {
	cases := []struct {
		model, effort        string
		thinking, outputConf string
		maxTokens            int
	}{
		{"claude-opus-5-5", "", `{"display":"summarized","type":"adaptive"}`, "", 32000},
		{"claude-opus-5-5", "low", `{"display":"summarized","type":"adaptive"}`, `{"effort":"low"}`, 32000},
		{"claude-opus-5-5", "none", `{"display":"summarized","type":"adaptive"}`, `{"effort":"low"}`, 32000},
		{"claude-sonnet-5-5", "medium", `{"display":"summarized","type":"adaptive"}`, `{"effort":"medium"}`, 32000},
		{"claude-fable-5-1", "max", `{"display":"summarized","type":"adaptive"}`, `{"effort":"max"}`, 64000},
		{"claude-opus-4-6", "xhigh", `{"type":"adaptive"}`, `{"effort":"high"}`, 64000},
		{"claude-opus-9", "high", `{"display":"summarized","type":"adaptive"}`, `{"effort":"high"}`, 64000},
		{"claude-haiku-4-5", "", "", "", 32000},
		{"claude-haiku-4-5-20251001", "low", `{"budget_tokens":1024,"type":"enabled"}`, "", 32000},
		{"claude-haiku-4-5", "high", `{"budget_tokens":16384,"type":"enabled"}`, "", 32384},
		{"claude-haiku-4-5", "max", `{"budget_tokens":49152,"type":"enabled"}`, "", 64000},
		{"claude-opus-4-1", "max", `{"budget_tokens":16000,"type":"enabled"}`, "", 32000},
	}
	for _, tc := range cases {
		payload, _ := (&anthropicProvider{}).buildPayload(&ChatRequest{Model: tc.model, ReasoningEffort: tc.effort, Messages: []Message{{Role: "user", Content: "hi"}}})
		thinking := ""
		if v, ok := payload["thinking"]; ok {
			thinking = asJSON(t, v)
		}
		outputConf := ""
		if v, ok := payload["output_config"]; ok {
			outputConf = asJSON(t, v)
		}
		if thinking != tc.thinking || outputConf != tc.outputConf || payload["max_tokens"] != tc.maxTokens {
			t.Errorf("%s/%q: thinking=%s output_config=%s max_tokens=%v", tc.model, tc.effort, thinking, outputConf, payload["max_tokens"])
		}
	}
}

func TestAnthropicBudgetThinkingSkipsToolRoundWithoutThinking(t *testing.T) {
	req := &ChatRequest{Model: "claude-haiku-4-5", ReasoningEffort: "medium", Tools: []Tool{{Name: "lookup"}}, Messages: []Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "toolu_a", Name: "lookup", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "toolu_a", Content: "ok"},
	}}
	payload, betas := (&anthropicProvider{}).buildPayload(req)
	if _, ok := payload["thinking"]; ok || len(betas) != 0 {
		t.Fatalf("thinking=%v betas=%v", payload["thinking"], betas)
	}
	req.Messages = req.Messages[:1]
	payload, betas = (&anthropicProvider{}).buildPayload(req)
	if asJSON(t, payload["thinking"]) != `{"budget_tokens":4096,"type":"enabled"}` || len(betas) != 1 || betas[0] != anthropicInterleavedBeta {
		t.Fatalf("thinking=%v betas=%v", payload["thinking"], betas)
	}
}

func TestAnthropicReplayIgnoresOpenAIItemsAndViceVersa(t *testing.T) {
	openaiItem := ReasoningItem{ID: "rs_1", EncryptedContent: "enc"}
	anthropicItem := ReasoningItem{Provider: "anthropic", Content: json.RawMessage(`[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"c1","name":"x","input":{}}]`)}
	msg := Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "x", Arguments: "{}"}}, ReasoningItems: []ReasoningItem{openaiItem}}
	if _, replayed := convertAnthropicMessages([]Message{{Role: "user", Content: "u"}, msg}, true); replayed {
		t.Fatal("anthropic replayed an OpenAI item")
	}
	msg.ReasoningItems = []ReasoningItem{anthropicItem}
	if hasReasoningReplay([]Message{msg}) {
		t.Fatal("OpenAI replay detection counted an Anthropic item")
	}
	if got := (&openaiResponses{}).reasoningInput(msg.ReasoningItems); len(got) != 0 {
		t.Fatalf("OpenAI replayed an Anthropic item: %v", got)
	}
}

func TestAnthropicRollingCacheBreakpointMarksPreviousRequestEnd(t *testing.T) {
	msgs, _ := convertAnthropicMessages([]Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "a", Name: "x", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "a", Content: "r1"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "b", Name: "x", Arguments: "{}"}}},
		{Role: "tool", ToolCallID: "b", Content: "r2"},
	}, false)
	marked := func(m anthropicMessage) bool {
		return strings.Contains(asJSON(t, m.Content[len(m.Content)-1]), "cache_control")
	}
	if len(msgs) != 5 || marked(msgs[0]) || !marked(msgs[2]) || !marked(msgs[4]) {
		t.Fatalf("messages = %s", asJSON(t, msgs))
	}
}

func TestAnthropicUsageCost(t *testing.T) {
	u := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 500_000, CacheWriteTokens: 100_000}
	// Opus 5.5: 400k uncached × $4 + 500k reads × $0.20 + 100k writes × $5 + 1M out × $20.
	want := 1.6 + 0.1 + 0.5 + 20.0
	if got := CalculateCost("claude-opus-5-5", u); got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
	if got := CalculateCost("gpt-5", Usage{InputTokens: 1_000_000}); got != 1.25 {
		t.Fatalf("uncached OpenAI cost = %v", got)
	}
}
