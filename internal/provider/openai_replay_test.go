package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const completedEvent = "event: response.completed\ndata: {\"response\":{\"usage\":{}}}\n\n"

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
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"ok\"}\n\n"+completedEvent)
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
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"done\"}\n\n"+completedEvent)
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
		completedEvent,
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

func TestReasoningReplayRejectionMatchesOnlyReasoningForms(t *testing.T) {
	for body, want := range map[string]bool{
		`{"error":{"message":"Item with id 'rs_abc' not found."}}`:                  true,
		`{"error":{"message":"Invalid encrypted_content for reasoning."}}`:          true,
		`{"error":{"message":"A reasoning item was provided without its output."}}`: true,
		`{"error":{"message":"Unknown parameter: 'users_list'."}}`:                  false,
		`{"error":{"message":"parameters_schema is invalid"}}`:                      false,
		`{"error":{"message":"reasoning.effort 'max' is not supported"}}`:           false,
		`{"error":{"message":"Encrypted file upload rejected"}}`:                    false,
	} {
		if got := isReasoningReplayRejection(NewAPIError("openai", 400, body)); got != want {
			t.Errorf("%s: got %v, want %v", body, got, want)
		}
	}
}

func TestReasoningReplayRetryFailureFallsThroughToLegacy(t *testing.T) {
	var responses, legacy atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			legacy.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"legacy\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		if responses.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"error":{"message":"Item with id 'rs_1' not found."}}`)
			return
		}
		_, _ = io.WriteString(w, `{"error":{"message":"No tool output found for function call call_1."}}`)
	}))
	defer server.Close()
	p := &openaiResponses{apiKey: "token", baseURL: server.URL, legacy: &openaiChatCompletions{apiKey: "token", baseURL: server.URL}}
	resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6", Messages: replayHistory(), Tools: []Tool{{Name: "lookup"}}}, nil)
	if err != nil || resp.Content != "legacy" || responses.Load() != 2 || legacy.Load() != 1 {
		t.Fatalf("resp=%+v err=%v responses=%d legacy=%d", resp, err, responses.Load(), legacy.Load())
	}
}

func TestResponsesStreamWithoutCompletedIsTransient(t *testing.T) {
	stream := "event: response.output_text.delta\ndata: {\"delta\":\"half\"}\n\n"
	_, err := (&openaiResponses{}).parseStream(strings.NewReader(stream), nil)
	if !errors.Is(err, ErrStreamIncomplete) || !IsRetryable(err) {
		t.Fatalf("err = %v, want retryable incomplete stream", err)
	}
	if _, err := (&openaiResponses{}).parseStream(strings.NewReader(stream+completedEvent), nil); err != nil {
		t.Fatal(err)
	}
	inner := &sequenceProvider{errs: []error{fmt.Errorf("stream: %w", ErrStreamIncomplete)}}
	r := NewRetryProvider(inner, RetryConfig{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, OnRetry: func(int, error, time.Duration) {}})
	if _, err := r.ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil); err != nil {
		t.Fatalf("unforwarded incomplete stream was not retried: %v", err)
	}
}

func TestStreamWatchdogDefaultsOutlastSilentReasoning(t *testing.T) {
	if streamIdleTimeout < 300*time.Second || streamWallCap < 600*time.Second {
		t.Fatalf("idle=%s wall=%s", streamIdleTimeout, streamWallCap)
	}
}

func TestResponsesStreamWallCapStartsAfterHeaders(t *testing.T) {
	previousIdle, previousWall := streamIdleTimeout, streamWallCap
	streamIdleTimeout, streamWallCap = time.Second, 150*time.Millisecond
	defer func() { streamIdleTimeout, streamWallCap = previousIdle, previousWall }()
	trickle := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Slow-Headers") != "" {
			time.Sleep(300 * time.Millisecond) // header wait is not the wall cap's concern
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"ok\"}\n\n"+completedEvent)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for {
			if _, err := io.WriteString(w, ": keep-alive\n\n"); err != nil {
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
	defer trickle.Close()
	p := &openaiResponses{apiKey: "token", baseURL: trickle.URL}
	_, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6"}, nil)
	var wall *StreamWallCapError
	if !errors.As(err, &wall) || !IsStreamWatchdog(err) || IsStreamIdle(err) {
		t.Fatalf("err = %v, want wall-cap abort", err)
	}
	p.headers = map[string]string{"X-Slow-Headers": "1"}
	if resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6"}, nil); err != nil || resp.Content != "ok" {
		t.Fatalf("slow headers: resp=%+v err=%v", resp, err)
	}
}

func TestRetryProviderLeavesWatchdogRetryToCaller(t *testing.T) {
	for _, watchdog := range []error{&StreamIdleError{Idle: time.Second}, fmt.Errorf("x: %w", &StreamWallCapError{Cap: time.Second})} {
		inner := &sequenceProvider{errs: []error{watchdog, watchdog}}
		r := NewRetryProvider(inner, RetryConfig{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, OnRetry: func(int, error, time.Duration) {}})
		if _, err := r.ChatStream(context.Background(), &ChatRequest{Model: "m"}, nil); !IsStreamWatchdog(err) || len(inner.errs) != 1 {
			t.Fatalf("stream err=%v remaining=%d", err, len(inner.errs))
		}
		if _, err := r.Chat(context.Background(), &ChatRequest{Model: "m"}); !IsStreamWatchdog(err) || len(inner.errs) != 0 {
			t.Fatalf("chat err=%v remaining=%d", err, len(inner.errs))
		}
	}
}

func TestInStreamReasoningRejectionRetriesOnceWithout(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			if !strings.Contains(string(raw), `"type":"reasoning"`) {
				t.Errorf("first request omitted reasoning")
			}
			_, _ = io.WriteString(w, "event: response.failed\ndata: {\"response\":{\"error\":{\"code\":\"invalid_request_error\",\"message\":\"Item with id 'rs_1' not found.\"}}}\n\n")
			return
		}
		if strings.Contains(string(raw), `"type":"reasoning"`) {
			t.Errorf("retry still replayed reasoning")
		}
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"delta\":\"done\"}\n\n"+completedEvent)
	}))
	defer server.Close()
	p := &openaiResponses{apiKey: "token", baseURL: server.URL, noStore: true}
	resp, err := p.ChatStream(context.Background(), &ChatRequest{Model: "gpt-5.6", Messages: replayHistory()}, nil)
	if err != nil || resp.Content != "done" || !resp.ReasoningReplayRejected || requests.Load() != 2 {
		t.Fatalf("resp=%+v err=%v requests=%d", resp, err, requests.Load())
	}
	// An unrelated in-stream failure keeps its error and is not resent here.
	if isReasoningReplayRejection(&ResponseFailedError{Code: "server_error", Message: "The server had an error"}) {
		t.Fatal("unrelated response.failed matched")
	}
}
