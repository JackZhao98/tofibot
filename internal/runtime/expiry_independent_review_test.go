package runtime

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/agent"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReviewExpiryDoesNotExceedOriginalStepBudget(t *testing.T) {
	var requests atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Synthetic incomplete conclusion."}}}})
	}))
	defer model.Close()
	e, err := New(Config{Provider: "openai_completions", Model: "test", BaseURL: model.URL})
	if err != nil {
		t.Fatal(err)
	}
	c, err := decodeContinuation(expiryRuntimeCheckpoint(t, 0), Request{RunID: "r", BotID: "b"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	c.Step = agent.MaxStepsWithProgressReports
	c.LLMCalls = agent.MaxStepsWithProgressReports
	c.ModelUsage = map[string]agent.ModelUsage{"test": {APICallCount: agent.MaxStepsWithProgressReports}}
	raw, err := encodeContinuation(Request{RunID: "r", BotID: "b"}, "test", c)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Run(context.Background(), Request{RunID: "r", BotID: "b", Model: "test", Continuation: raw, ApprovalExpiryRecovery: true})
	if requests.Load() != 0 {
		t.Fatalf("original %d-step budget already exhausted, but expiry made %d extra model request(s), err=%v", agent.MaxStepsWithProgressReports, requests.Load(), err)
	}
}

func TestReviewExpiryUsesOnlyRemainingSteps(t *testing.T) {
	for _, tc := range []struct {
		name            string
		step            int
		requestTools    bool
		requests, tools int
		wantError       bool
	}{
		{"zero", 300, false, 0, 0, true},
		{"beyond", 301, false, 0, 0, true},
		{"one-summary-only", 299, false, 1, 0, false},
		{"one-tool-refused", 299, true, 1, 0, true},
		{"two-bounded", 298, true, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests, executions atomic.Int32
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in struct {
					Tools []any `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
					t.Error(err)
					return
				}
				n := requests.Add(1)
				if tc.step == 299 && len(in.Tools) != 0 {
					t.Error("last original step exposed tools")
				}
				if n == 2 && len(in.Tools) != 0 {
					t.Error("final summary request exposed tools")
				}
				message := map[string]any{"content": "Synthetic incomplete summary."}
				if n == 1 && tc.requestTools {
					message = map[string]any{"tool_calls": []any{map[string]any{"id": "synthetic-safe", "type": "function", "function": map[string]string{"name": "search_history", "arguments": "{}"}}}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}})
			}))
			defer model.Close()
			e, err := New(Config{Provider: "openai_completions", Model: "test", BaseURL: model.URL})
			if err != nil {
				t.Fatal(err)
			}
			c, err := decodeContinuation(expiryRuntimeCheckpoint(t, 0), Request{RunID: "r", BotID: "b"}, "test")
			if err != nil {
				t.Fatal(err)
			}
			c.Step = tc.step
			c.LLMCalls = tc.step
			c.ModelUsage = map[string]agent.ModelUsage{"test": {APICallCount: tc.step}}
			raw, err := encodeContinuation(Request{RunID: "r", BotID: "b"}, "test", c)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Run(context.Background(), Request{RunID: "r", BotID: "b", Model: "test", Continuation: raw, ApprovalExpiryRecovery: true, Tools: []Tool{{Name: "search_history", ApprovalExpiryReadOnly: true, Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
				executions.Add(1)
				return "synthetic history", nil
			}}}})
			if (err != nil) != tc.wantError || requests.Load() != int32(tc.requests) || executions.Load() != int32(tc.tools) {
				t.Fatalf("requests=%d tools=%d err=%v", requests.Load(), executions.Load(), err)
			}
		})
	}
}
