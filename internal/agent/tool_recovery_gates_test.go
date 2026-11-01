package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestOwnGatesDoNotSpendTheRepairBudget(t *testing.T) {
	var transcript []provider.Message
	args := `{"name":"mcp_x__query","arguments":{}}`
	for i, code := range []string{"schema_required", "needs_observation", "schema_required", "schema_required", "invalid_arguments", "invalid_arguments"} {
		id := string(rune('a' + i))
		transcript = append(transcript, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "call_mcp_tool", Arguments: args}}}, provider.Message{Role: "tool", ToolCallID: id, ToolOutcome: outcomePtr(tooloutcome.New(tooloutcome.Validation, code, "not_executed", "problem "+code, "repair_arguments"))})
	}
	if got := toolRecoveryGuard(transcript, "call_mcp_tool", args); got != nil {
		t.Fatalf("gates and two real errors exhausted the budget: %+v", got)
	}
}

func TestRepairBudgetMessageNamesProblemAndWayOut(t *testing.T) {
	var transcript []provider.Message
	args := `{"name":"mcp_x__query","arguments":{}}`
	for _, id := range []string{"a", "b", "c"} {
		transcript = append(transcript, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "call_mcp_tool", Arguments: args}}}, provider.Message{Role: "tool", ToolCallID: id, ToolOutcome: outcomePtr(tooloutcome.New(tooloutcome.Validation, "invalid_arguments", "not_executed", "Problem: minItems: array length 1 is less than 2 for data_source_urls", "repair_arguments"))})
	}
	got := toolRecoveryGuard(transcript, "call_mcp_tool", args)
	if got == nil || got.Code != "repair_budget_exhausted" {
		t.Fatalf("guard=%+v", got)
	}
	for _, want := range []string{"data_source_urls", "next run", "Other tools stay available"} {
		if !strings.Contains(got.Message, want) {
			t.Fatalf("message lacks %q: %s", want, got.Message)
		}
	}
}

func TestSuccessfulCallResetsRepairBudget(t *testing.T) {
	raw := `{"name":"mcp_x__query","arguments":{}}`
	id := tooloutcome.DefaultIdentity("call_mcp_tool", json.RawMessage(raw))
	var ledger []ToolRecoveryRecord
	for _, c := range []string{"a", "b", "c"} {
		ledger = append(ledger, ToolRecoveryRecord{Call: provider.ToolCall{ID: c, Name: "call_mcp_tool", Arguments: raw}, Identity: &id, Outcome: tooloutcome.New(tooloutcome.Validation, "invalid_arguments", "not_executed", "p", "repair_arguments")})
	}
	if toolRecoveryCallGuardAt(ledger, id, "", 0) == nil {
		t.Fatal("budget should be exhausted before the success")
	}
	ledger = pruneRepairsAfterSuccess(ledger, id)
	if got := toolRecoveryCallGuardAt(ledger, id, "", 0); got != nil {
		t.Fatalf("budget not reset: %+v", got)
	}
}
