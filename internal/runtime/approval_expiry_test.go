package runtime

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func expiryRuntimeCheckpoint(t *testing.T, elapsed time.Duration) json.RawMessage {
	t.Helper()
	c := &agent.Continuation{Version: 1, QuestionID: "q", WaitingToolCallID: "expired-call", WaitingToolName: "write", Messages: []provider.Message{{Role: "user", Content: "Synthetic task"}, {Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "expired-call", Name: "write", Arguments: `{"target":"synthetic"}`}, {ID: "batch-tail", Name: "send", Arguments: `{}`}}}}, SkippedToolCalls: []provider.ToolCall{{ID: "batch-tail", Name: "send", Arguments: `{}`}}, ActiveElapsedNanos: int64(elapsed)}
	raw, err := encodeContinuation(Request{RunID: "r", BotID: "b"}, "test", c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestApprovalExpiryBoundedReadOnlyAndCrossToolBypass(t *testing.T) {
	var requests, observations, effects atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Tools    []any `json:"tools"`
			Messages []struct {
				Role, Content string
				CallID        string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Fatal("decode")
		}
		n := requests.Add(1)
		if n == 1 {
			seenExpired, seenSkipped := false, false
			for _, m := range in.Messages {
				if m.CallID == "expired-call" {
					seenExpired = strings.Contains(m.Content, "approval_expired") && strings.Contains(m.Content, "not_executed")
				}
				if m.CallID == "batch-tail" {
					seenSkipped = strings.Contains(m.Content, "execution skipped")
				}
			}
			if !seenExpired || !seenSkipped {
				t.Error("lost interrupted batch outcomes")
			}
			calls := []any{}
			for _, call := range []provider.ToolCall{{ID: "safe", Name: "search_history", Arguments: `{}`}, {ID: "bypass", Name: "other_provider", Arguments: `{"target":"changed"}`}, {ID: "third", Name: "search_history", Arguments: `{}`}} {
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]string{"name": call.Name, "arguments": call.Arguments}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": calls}}}})
		} else {
			if len(in.Tools) != 0 {
				t.Error("second request exposed tools")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Observed local history. Approval expired; the requested write and send were not executed."}}}})
		}
	}))
	defer model.Close()
	e, err := New(Config{Provider: "openai_completions", Model: "test", BaseURL: model.URL})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{RunID: "r", BotID: "b", Model: "test", Continuation: expiryRuntimeCheckpoint(t, 0), ApprovalExpiryRecovery: true, Tools: []Tool{
		{Name: "search_history", ApprovalExpiryReadOnly: true, Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			observations.Add(1)
			return "local history", nil
		}},
		{Name: "other_provider", ApprovalExpiryReadOnly: true, Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { effects.Add(1); return "forbidden", nil }},
		{Name: "write", Execute: func(context.Context, json.RawMessage) (string, error) { effects.Add(1); return "forbidden", nil }},
	}}
	result, err := e.Run(context.Background(), req)
	if err != nil || result.Content == "" || requests.Load() != 2 || observations.Load() != 1 || effects.Load() != 0 {
		t.Fatalf("result=%+v err=%v requests=%d read=%d effects=%d", result, err, requests.Load(), observations.Load(), effects.Load())
	}
}

func TestApprovalExpiryModelBudgetAndUnavailableFallback(t *testing.T) {
	for _, kind := range []string{"budget", "timeout", "unavailable", "empty", "summary-tools", "none"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				switch kind {
				case "timeout":
					select {
					case <-r.Context().Done():
					case <-time.After(250 * time.Millisecond):
					}
					return
				case "unavailable":
					http.Error(w, "synthetic unavailable", 503)
					return
				case "empty":
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": ""}}}})
					return
				case "summary-tools":
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"tool_calls": []any{map[string]any{"id": "call", "type": "function", "function": map[string]string{"name": "search_history", "arguments": "{}"}}}}}}})
					return
				default:
					if n > 1 {
						t.Error("unexpected extra request")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "Incomplete: approval expired."}}}})
				}
			}))
			defer server.Close()
			duration := time.Minute
			elapsed := time.Duration(0)
			if kind == "budget" {
				elapsed = duration
			}
			if kind == "timeout" {
				duration = 30 * time.Millisecond
			}
			e, err := New(Config{Provider: "openai_completions", Model: "test", BaseURL: server.URL, MaxDuration: duration})
			if err != nil {
				t.Fatal(err)
			}
			req := Request{RunID: "r", BotID: "b", Model: "test", Continuation: expiryRuntimeCheckpoint(t, elapsed), ApprovalExpiryRecovery: true}
			if kind == "summary-tools" {
				req.Tools = []Tool{{Name: "search_history", ApprovalExpiryReadOnly: true, Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "read", nil }}}
			}
			result, err := e.Run(context.Background(), req)
			if kind == "none" {
				if err != nil || result.Content == "" || calls.Load() != 1 {
					t.Fatalf("direct summary %+v %v %d", result, err, calls.Load())
				}
			} else if err == nil {
				t.Fatal("missing fallback signal")
			}
			if calls.Load() > 2 {
				t.Fatal("unbounded retry")
			}
			if kind == "budget" && calls.Load() != 0 {
				t.Fatal("exceeded original budget")
			}
		})
	}
}

func TestApprovalExpiryRecoveryLedgerBlocksChangedArgumentsAndUncertainty(t *testing.T) {
	raw := expiryRuntimeCheckpoint(t, 0)
	c, err := decodeContinuation(raw, Request{RunID: "r", BotID: "b"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	uncertain := tooloutcome.New(tooloutcome.Uncertain, "lost_effect", "unknown", "Verify effect", "verify_effect")
	c.ToolRecovery = []agent.ToolRecoveryRecord{{Call: provider.ToolCall{ID: "prior", Name: "opaque_effect", Arguments: `{}`}, Outcome: uncertain}}
	_, records, err := agent.ResumeApprovalExpiry(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"write", "opaque_effect"} {
		if agent.ApprovalExpiryGuard(records, tooloutcome.DefaultIdentity(name, json.RawMessage(`{"changed":true}`)), c.RecoveryEpoch) == nil {
			t.Fatalf("ledger lost %s", name)
		}
	}
}
