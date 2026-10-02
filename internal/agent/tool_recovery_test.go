package agent

import (
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"testing"
)

func TestToolRecoveryGuardBudgetsAndUncertainEffects(t *testing.T) {
	var transcript []provider.Message
	for _, id := range []string{"a", "b", "c"} {
		transcript = append(transcript, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "call_mcp_tool", Arguments: `{"name":"write","arguments":{}}`}}}, provider.Message{Role: "tool", ToolCallID: id, Content: tooloutcome.New(tooloutcome.Validation, "invalid_arguments", "not_executed", "Repair schema", "repair_arguments").JSON()})
	}
	if got := toolRecoveryGuard(transcript, "call_mcp_tool", `{"name":"write","arguments":{"fixed":true}}`); got == nil || got.Code != "repair_budget_exhausted" {
		t.Fatalf("guard=%+v", got)
	}
	if got := toolRecoveryGuard(transcript, "call_mcp_tool", `{"name":"inspect","arguments":{}}`); got != nil {
		t.Fatal("blocked independent verification", got)
	}
	for _, status := range []string{tooloutcome.Uncertain, tooloutcome.Denied, tooloutcome.Transient} {
		messages := []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "lost", Name: "publish", Arguments: `{"b":2,"a":1}`}}}, {Role: "tool", ToolCallID: "lost", Content: tooloutcome.New(status, "fixture", "unknown", "Blocked synthetic action", "verify_effect").JSON()}}
		if got := toolRecoveryGuard(messages, "publish", `{"a":1,"b":2}`); got == nil || got.Status != status {
			t.Fatalf("replayed %s: %+v", status, got)
		}
	}
}
