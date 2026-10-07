package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICodexChatUsesStreamingAndAggregatesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload["stream"] != true {
			t.Errorf("stream = %v, want true", payload["stream"])
			return
		}
		if payload["model"] != "gpt-5.6-luna" {
			t.Errorf("model = %v, want prefix stripped model", payload["model"])
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			"event: response.output_text.delta\ndata: {\"delta\":\"hello \"}\n\n",
			"event: response.output_item.added\ndata: {\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_item_1\",\"call_id\":\"call_1\",\"name\":\"save_memory\"}}\n\n",
			"event: response.function_call_arguments.delta\ndata: {\"output_index\":1,\"delta\":\"{\\\"content\\\":\\\"x\\\"}\"}\n\n",
			"event: response.output_text.delta\ndata: {\"delta\":\"world\"}\n\n",
			"event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":7}}}\n\n",
		}, "")))
	}))
	defer server.Close()

	codex := &openAICodex{responses: &openaiResponses{
		apiKey:  "access-token",
		baseURL: server.URL,
		noStore: true,
	}}
	got, err := codex.Chat(context.Background(), &ChatRequest{
		Model:    "codex-gpt-5.6-luna",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if got.Content != "hello world" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Usage.InputTokens != 12 || got.Usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v", got.Usage)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "call_1" || got.ToolCalls[0].Name != "save_memory" || got.ToolCalls[0].Arguments != `{"content":"x"}` {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
}

func TestOpenAICodexChatPropagatesStreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.failed\ndata: {\"response\":{\"error\":{\"code\":\"bad\",\"message\":\"nope\"}}}\n\n"))
	}))
	defer server.Close()
	codex := &openAICodex{responses: &openaiResponses{apiKey: "token", baseURL: server.URL}}
	_, err := codex.Chat(context.Background(), &ChatRequest{Model: "codex-gpt-5.6-luna"})
	if err == nil || !strings.Contains(err.Error(), "response failed: [bad] nope") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAICodexChatPropagatesCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	codex := &openAICodex{responses: &openaiResponses{apiKey: "token", baseURL: server.URL}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := codex.Chat(ctx, &ChatRequest{Model: "codex-gpt-5.6-luna"})
	if err == nil {
		t.Fatal("Chat() error = nil, want cancellation error")
	}
}

func TestOpenAICodexFixtureHeadersAndCallIDRoundTrip(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			t.Errorf("request = %s %s, want POST /responses", r.Method, r.URL.Path)
			return
		}
		for name, want := range map[string]string{
			"Authorization":      "Bearer access-token",
			"ChatGPT-Account-Id": "account-123",
			"originator":         "tofi",
			"User-Agent":         "tofi/1.0",
		} {
			if got := r.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
				return
			}
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if payload["stream"] != true || payload["store"] != false {
			t.Errorf("stream/store = %v/%v, want true/false", payload["stream"], payload["store"])
			return
		}
		if payload["model"] != "gpt-5.6-luna" {
			t.Errorf("model = %v, want codex prefix removed", payload["model"])
			return
		}
		if requests == 2 {
			encoded, _ := json.Marshal(payload["input"])
			input := string(encoded)
			if !strings.Contains(input, `"call_id":"call-response-1"`) || strings.Contains(input, `"id":"item-response-1"`) {
				t.Errorf("second input did not preserve call_id-only tool round trip: %s", input)
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests == 1 {
			_, _ = io.WriteString(w, strings.Join([]string{
				"event: response.output_item.added\ndata: {\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"item-response-1\",\"call_id\":\"call-response-1\",\"name\":\"save_memory\"}}\n\n",
				"event: response.function_call_arguments.delta\ndata: {\"output_index\":0,\"delta\":\"{\\\"content\\\":\\\"saved\\\"}\"}\n\n",
				"event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n",
			}, ""))
			return
		}
		_, _ = io.WriteString(w, strings.Join([]string{
			"event: response.output_text.delta\ndata: {\"delta\":\"saved\"}\n\n",
			"event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}\n\n",
		}, ""))
	}))
	defer server.Close()

	providerValue, err := newOpenAICodex("access-token\x00account-123", &providerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	codex := providerValue.(*openAICodex)
	codex.responses.baseURL = server.URL

	first, err := codex.Chat(context.Background(), &ChatRequest{Model: "codex-gpt-5.6-luna", Messages: []Message{{Role: "user", Content: "save"}}})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(first.ToolCalls) != 1 || first.ToolCalls[0].ID != "call-response-1" {
		t.Fatalf("first tool calls = %+v, want call_id", first.ToolCalls)
	}
	second, err := codex.ChatStream(context.Background(), &ChatRequest{
		Model: "codex-gpt-5.6-luna",
		Messages: []Message{
			{Role: "user", Content: "save"},
			{Role: "assistant", ToolCalls: first.ToolCalls},
			{Role: "tool", ToolCallID: first.ToolCalls[0].ID, Content: "saved by backend"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	if second.Content != "saved" || requests != 2 {
		t.Fatalf("second response = %+v, requests=%d", second, requests)
	}
}

func TestOpenAICodexGPT6RequestCarriesReasoning(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: response.completed\ndata: {\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}))
	defer server.Close()
	codex := &openAICodex{responses: &openaiResponses{apiKey: "token", baseURL: server.URL, noStore: true}}
	if _, err := codex.Chat(context.Background(), &ChatRequest{Model: "codex-gpt-6-luna", ReasoningEffort: "high", Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	reasoning, _ := payload["reasoning"].(map[string]any)
	if payload["model"] != "gpt-6-luna" || reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("payload model=%v reasoning=%v", payload["model"], payload["reasoning"])
	}
	include, _ := payload["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %v", payload["include"])
	}
}
