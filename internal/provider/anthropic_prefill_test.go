package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnthropicHTTPPrefillRefusalSurfacesAsCodedError(t *testing.T) {
	var sent []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`)
	}))
	defer server.Close()
	p := &anthropicProvider{apiKey: "k", baseURL: server.URL}
	_, err := p.Chat(context.Background(), &ChatRequest{Model: "claude-sonnet-5-5", Messages: []Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}}})
	if RequestRejectionCode(err) != RequestPrefillRejected || IsRetryable(err) {
		t.Fatalf("err = %v, want coded and not retryable", err)
	}
	var body struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if json.Unmarshal(sent, &body) != nil || len(body.Messages) != 3 || body.Messages[2].Role != "user" {
		t.Fatalf("request body ended with %s, want a closing user turn", sent)
	}
}

func TestAnthropicRequestNeverEndsWithAssistantTurn(t *testing.T) {
	cases := map[string][]Message{
		"history ends with the assistant":    {{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}},
		"trailing user turn renders empty":   {{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "   "}},
		"assistant text then dropped system": {{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "system", Content: "ignored"}},
		"ends with a tool result":            {{Role: "user", Content: "q"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "t1", Name: "x", Arguments: `{}`}}}, {Role: "tool", ToolCallID: "t1", Content: "r"}},
		"ends with user text":                {{Role: "assistant", Content: "a"}, {Role: "user", Content: "q"}},
	}
	for name, msgs := range cases {
		for _, replay := range []bool{false, true} {
			roles := AnthropicTurnRoles(msgs, replay)
			if len(roles) == 0 || roles[len(roles)-1] != "user" {
				t.Errorf("%s (replay=%v): turns = %v", name, replay, roles)
			}
			for i := 1; i < len(roles); i++ {
				if roles[i] == roles[i-1] {
					t.Errorf("%s: adjacent %s turns in %v", name, roles[i], roles)
				}
			}
		}
	}
}

func TestAnthropicPrefillRejectionIsCodedAndNeverRetried(t *testing.T) {
	body := `{"type":"error","error":{"type":"invalid_request_error","message":"This model does not support assistant message prefill. The conversation must end with a user message."}}`
	raw := NewAPIError("anthropic", 400, body)
	coded := codeRequestRejection(raw)
	if RequestRejectionCode(coded) != RequestPrefillRejected {
		t.Fatalf("code = %q for %v", RequestRejectionCode(coded), coded)
	}
	if api, ok := AsAPIError(coded); !ok || api.StatusCode != 400 {
		t.Fatalf("the original API error must stay reachable: %v", coded)
	}
	if IsRetryable(coded) || IsRetryable(fmt.Errorf("wrapped: %w", coded)) {
		t.Fatal("a prefill rejection must never be retried")
	}
	// Other 400s and other statuses keep their own handling.
	if other := codeRequestRejection(NewAPIError("anthropic", 400, `{"error":{"message":"max_tokens too large"}}`)); RequestRejectionCode(other) != "" {
		t.Fatalf("unrelated 400 coded as %q", RequestRejectionCode(other))
	}
	if other := codeRequestRejection(NewAPIError("anthropic", 529, "prefill overloaded")); RequestRejectionCode(other) != "" || !IsRetryable(other) {
		t.Fatalf("a 529 must stay retryable and uncoded: %v", other)
	}
	if codeRequestRejection(errors.New("boom")) == nil || codeRequestRejection(nil) != nil {
		t.Fatal("non-API errors pass through unchanged")
	}
}
