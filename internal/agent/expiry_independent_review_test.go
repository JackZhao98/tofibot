package agent

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"testing"
)

func TestReviewOrdinaryResumeDoesNotPermanentlyBlockUnexecutedTail(t *testing.T) {
	tail := provider.ToolCall{ID: "old-tail", Name: "search_history", Arguments: `{"query":"approved task"}`}
	c := &Continuation{Version: 1, QuestionID: "q", WaitingToolCallID: "ask", WaitingToolName: "ask_user_question", Messages: []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "ask", Name: "ask_user_question", Arguments: `{}`}, tail}}}, SkippedToolCalls: []provider.ToolCall{tail}}
	messages, err := resumeContinuation(c, "The requested detail is supplied.")
	if err != nil {
		t.Fatal(err)
	}
	if o := toolRecoveryRecordsGuard(toolRecoveryRecords(messages), tail.Name, tail.Arguments); o != nil {
		t.Fatalf("fresh authorized read after ordinary human answer is permanently blocked: %+v", *o)
	}
}

type reviewResumeProvider struct{ calls int }

func (p *reviewResumeProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "fresh-tail", Name: "search_history", Arguments: `{"query":"approved task"}`}}}, nil
	}
	return &provider.ChatResponse{Content: "Fresh authorized observation completed."}, nil
}
func (p *reviewResumeProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestReviewNormalResumeExecutesFreshProposalOnce(t *testing.T) {
	tail := provider.ToolCall{ID: "old-tail", Name: "search_history", Arguments: `{"query":"approved task"}`}
	c := &Continuation{Version: 1, QuestionID: "q", WaitingToolCallID: "ask", WaitingToolName: "ask_user_question", Messages: []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "ask", Name: "ask_user_question", Arguments: `{}`}, tail}}}, SkippedToolCalls: []provider.ToolCall{tail}}
	p := &reviewResumeProvider{}
	exec := models.NewExecutionContext("synthetic-resume", "synthetic-bot", t.TempDir())
	defer exec.Cancel()
	executions := 0
	var observed []provider.Message
	result, err := RunAgentLoop(AgentConfig{Ctx: context.Background(), Provider: p, Model: "test-model", ToolsOnly: true, Continuation: c, ResumeResult: "The requested detail is supplied.", OnMessage: func(m provider.Message) { observed = append(observed, m) }, ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "search_history", Parameters: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}, "required": []string{"query"}}}, HandlerCtx: func(context.Context, map[string]interface{}) (string, error) {
		executions++
		return "synthetic observed history", nil
	}}}}, exec)
	if err != nil || result == nil || executions != 1 || p.calls != 2 {
		t.Fatalf("result=%+v executions=%d requests=%d err=%v", result, executions, p.calls, err)
	}
	skipped, fresh := 0, 0
	for _, m := range observed {
		if m.Role == "tool" && m.ToolCallID == tail.ID && m.ToolOutcome != nil && m.ToolOutcome.Code == "batch_skipped" && m.ToolOutcome.Certainty == "not_executed" {
			skipped++
		}
		if m.Role == "tool" && m.ToolCallID == "fresh-tail" && !m.ToolFailed {
			fresh++
		}
	}
	if skipped != 1 || fresh != 1 {
		t.Fatalf("historical skips=%d fresh results=%d", skipped, fresh)
	}
}

func TestReviewSkippedHistoryDoesNotWeakenExpiryOrDenialFence(t *testing.T) {
	call := provider.ToolCall{ID: "blocked", Name: "write", Arguments: `{"target":"synthetic"}`}
	for _, status := range []string{tooloutcome.Expired, tooloutcome.Denied, tooloutcome.Uncertain, tooloutcome.Permanent} {
		o := tooloutcome.New(status, "synthetic_blocker", "not_executed", "Still blocked.", "explain_blocker")
		if status == tooloutcome.Uncertain {
			o.Certainty = "unknown"
		}
		records := []ToolRecoveryRecord{{Call: call, Outcome: tooloutcome.New(tooloutcome.Permanent, "batch_skipped", "not_executed", "Old call skipped.", "replan_after_input")}, {Call: call, Outcome: o}}
		if got := toolRecoveryRecordsGuard(records, call.Name, call.Arguments); got == nil || got.Status != status {
			t.Fatalf("%s fence=%+v", status, got)
		}
	}
}
