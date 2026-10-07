package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func replayHistory() []Message {
	return []Message{
		{Role: "user", Content: "check"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{"q":"x"}`}}, ReasoningItems: []ReasoningItem{{ID: "rs_1", EncryptedContent: "enc-1", Summary: []string{"plan"}}, {ID: "rs_skip"}}},
		{Role: "tool", ToolCallID: "call_1", ToolName: "lookup", Content: "found"},
	}
}

func TestResponsesRequestCarriesPromptCacheKeyAndReasoningReplay(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"ok\"}\n\n")
	}))
	defer server.Close()
	codex := &openAICodex{responses: &openaiResponses{apiKey: "token", baseURL: server.URL, noStore: true}}
	if _, err := codex.ChatStream(context.Background(), &ChatRequest{Model: "codex-gpt-5.6-luna", ReasoningEffort: "high", PromptCacheKey: "run-1", Messages: replayHistory()}, nil); err != nil {
		t.Fatal(err)
	}
	if body["prompt_cache_key"] != "run-1" {
		t.Fatalf("prompt_cache_key = %v", body["prompt_cache_key"])
	}
	encoded, _ := json.Marshal(body["input"])
	var input []map[string]any
	_ = json.Unmarshal(encoded, &input)
	if len(input) != 4 || input[1]["type"] != "reasoning" || input[2]["type"] != "function_call" || input[3]["type"] != "function_call_output" {
		t.Fatalf("input order = %s", encoded)
	}
	if input[1]["encrypted_content"] != "enc-1" || input[1]["id"] != nil {
		t.Fatalf("reasoning item = %v, want encrypted content without unstored id", input[1])
	}
	if summary, _ := json.Marshal(input[1]["summary"]); string(summary) != `[{"text":"plan","type":"summary_text"}]` {
		t.Fatalf("summary = %s", summary)
	}

	// Stored responses may reference the item id; non-reasoning models never replay.
	stored := &openaiResponses{}
	if got := stored.reasoningInput(replayHistory()[1].ReasoningItems); len(got) != 1 || got[0].(map[string]interface{})["id"] != "rs_1" {
		t.Fatalf("stored reasoning input = %v", got)
	}
	payload := stored.buildPayload(&ChatRequest{Model: "gpt-4o", Messages: replayHistory()}, true)
	if raw, _ := json.Marshal(payload["input"]); strings.Contains(string(raw), "reasoning") || payload["prompt_cache_key"] != nil {
		t.Fatalf("non-reasoning payload = %s", raw)
	}
}

func TestResponsesReasoningReplayRejectionRetriesOnceWithout(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		hasReasoning := strings.Contains(string(raw), `"type":"reasoning"`)
		if n == 1 {
			if !hasReasoning {
				t.Errorf("first request omitted reasoning: %s", raw)
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"The encrypted content for item rs_1 could not be verified."}}`)
			return
		}
		if hasReasoning {
			t.Errorf("retry still replayed reasoning: %s", raw)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"done\"}\n\n")
	}))
	defer server.Close()
	p := &openaiResponses{apiKey: "token", baseURL: server.URL, noStore: true}
	resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6", Messages: replayHistory()}, nil)
	if err != nil || resp.Content != "done" || !resp.ReasoningReplayRejected || requests.Load() != 2 {
		t.Fatalf("resp=%+v err=%v requests=%d", resp, err, requests.Load())
	}

	// Already omitted, or an unrelated 400, is not retried.
	requests.Store(0)
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad model"}}`)
	}))
	defer unrelated.Close()
	p.baseURL = unrelated.URL
	if _, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6", Messages: replayHistory()}, nil); err == nil || requests.Load() != 1 {
		t.Fatalf("unrelated 400 err=%v requests=%d", err, requests.Load())
	}
}

func TestResponsesStreamCapturesReasoningItems(t *testing.T) {
	stream := strings.Join([]string{
		"event: response.output_item.done\ndata: {\"item\":{\"type\":\"reasoning\",\"id\":\"rs_9\",\"encrypted_content\":\"enc-9\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"think\"}]}}\n\n",
		"event: response.output_item.added\ndata: {\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"lookup\"}}\n\n",
		"event: response.function_call_arguments.delta\ndata: {\"output_index\":1,\"delta\":\"{}\"}\n\n",
	}, "")
	resp, err := (&openaiResponses{}).parseStream(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ReasoningItems) != 1 || resp.ReasoningItems[0].ID != "rs_9" || resp.ReasoningItems[0].EncryptedContent != "enc-9" || resp.ReasoningItems[0].Summary[0] != "think" || resp.Reasoning != "think" {
		t.Fatalf("reasoning = %+v / %q", resp.ReasoningItems, resp.Reasoning)
	}
}

func TestResponsesStreamIdleTimeoutIsTransient(t *testing.T) {
	previous := streamIdleTimeout
	streamIdleTimeout = 80 * time.Millisecond
	defer func() { streamIdleTimeout = previous }()
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	p := &openaiResponses{apiKey: "token", baseURL: server.URL}
	start := time.Now()
	_, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6"}, nil)
	var idle *StreamIdleError
	if !errors.As(err, &idle) || !IsStreamIdle(err) || !IsRetryable(err) {
		t.Fatalf("err = %v, want retryable stream idle error", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("idle watchdog did not abort promptly")
	}
}

func TestRetryObserverReceivesStreamBackoff(t *testing.T) {
	inner := &sequenceProvider{errs: []error{NewAPIError("openai", 503, "busy")}}
	r := NewRetryProvider(inner, RetryConfig{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, OnRetry: func(int, error, time.Duration) {}})
	var attempts []int
	ctx := WithRetryObserver(context.Background(), func(attempt int, err error, delay time.Duration) {
		attempts = append(attempts, attempt)
	})
	if _, err := r.ChatStream(ctx, &ChatRequest{Model: "m", PromptCacheKey: "k"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0] != 1 || inner.last.PromptCacheKey != "k" {
		t.Fatalf("attempts=%v last=%+v", attempts, inner.last)
	}
	if fallback := copyRequestWithModel(&ChatRequest{Model: "a", PromptCacheKey: "k", OmitReasoningReplay: true}, "b"); fallback.Model != "b" || fallback.PromptCacheKey != "k" || !fallback.OmitReasoningReplay {
		t.Fatalf("fallback = %+v", fallback)
	}
}

type sequenceProvider struct {
	errs []error
	last *ChatRequest
}

func (p *sequenceProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *sequenceProvider) ChatStream(_ context.Context, req *ChatRequest, _ func(StreamDelta)) (*ChatResponse, error) {
	p.last = req
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		return nil, err
	}
	return &ChatResponse{Content: "ok"}, nil
}
