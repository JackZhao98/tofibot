package agent

import (
	"encoding/json"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func validationFailure(id string) provider.Message {
	return provider.Message{Role: "tool", ToolCallID: id, ToolFailed: true, ToolOutcome: outcomePtr(tooloutcome.New(tooloutcome.Validation, "invalid_arguments", "not_executed", "Repair schema", "repair_arguments"))}
}

func repairTranscript() []provider.Message {
	var transcript []provider.Message
	for _, id := range []string{"a", "b", "c"} {
		transcript = append(transcript, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "mcp_x__query", Arguments: `{"q":1}`}}}, validationFailure(id))
	}
	return append(transcript,
		provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "ok", Name: "mcp_x__query", Arguments: `{"q":2}`}}},
		provider.Message{Role: "tool", ToolCallID: "ok", Content: "rows"},
	)
}

func TestRepairBudgetResetSurvivesLedgerRebuild(t *testing.T) {
	transcript := repairTranscript()
	if got := toolRecoveryGuard(transcript, "mcp_x__query", `{"q":3}`); got != nil {
		t.Fatalf("success did not reset the repair budget: %+v", got)
	}
	transcript = append(transcript,
		provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "d", Name: "mcp_x__query", Arguments: `{"q":4}`}}},
		validationFailure("d"),
	)
	if got := toolRecoveryGuard(transcript, "mcp_x__query", `{"q":5}`); got != nil {
		t.Fatalf("one repair after the success exhausted the budget: %+v", got)
	}
}

func TestRepairBudgetResetSurvivesApprovalExpiryResume(t *testing.T) {
	messages := repairTranscript()
	messages = append(messages, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "wait", Name: "request_approval", Arguments: `{}`}}})
	c := &Continuation{Version: continuationVersion, Kind: ContinuationInputWait, QuestionID: "q", WaitingToolCallID: "wait", WaitingToolName: "request_approval", Messages: messages}
	_, records, err := ResumeApprovalExpiry(c)
	if err != nil {
		t.Fatal(err)
	}
	identity := tooloutcome.DefaultIdentity("mcp_x__query", json.RawMessage(`{"q":3}`))
	if got := ApprovalExpiryGuard(records, identity, 0); got != nil {
		t.Fatalf("repair budget reappeared after resume: %+v", got)
	}
}
