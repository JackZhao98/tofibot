package agent

import (
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"testing"
)

func TestToolRecoveryGuardBudgetsAndUncertainEffects(t *testing.T) {
	var transcript []provider.Message
	for _, id := range []string{"a", "b", "c"} {
		transcript = append(transcript, provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "call_mcp_tool", Arguments: `{"name":"write","arguments":{}}`}}}, provider.Message{Role: "tool", ToolCallID: id, ToolOutcome: outcomePtr(tooloutcome.New(tooloutcome.Validation, "invalid_arguments", "not_executed", "Repair schema", "repair_arguments"))})
	}
	if got := toolRecoveryGuard(transcript, "call_mcp_tool", `{"name":"write","arguments":{"fixed":true}}`); got == nil || got.Code != "repair_budget_exhausted" {
		t.Fatalf("guard=%+v", got)
	}
	if got := toolRecoveryGuard(transcript, "call_mcp_tool", `{"name":"inspect","arguments":{}}`); got != nil {
		t.Fatal("blocked independent verification", got)
	}
	for _, status := range []string{tooloutcome.Uncertain, tooloutcome.Denied, tooloutcome.Transient} {
		messages := []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "lost", Name: "publish", Arguments: `{"b":2,"a":1}`}}}, {Role: "tool", ToolCallID: "lost", ToolOutcome: outcomePtr(tooloutcome.New(status, "fixture", "unknown", "Blocked synthetic action", "verify_effect"))}}
		if got := toolRecoveryGuard(messages, "publish", `{"a":1,"b":2}`); got == nil || got.Status != status {
			t.Fatalf("replayed %s: %+v", status, got)
		}
	}
}

func outcomePtr(o tooloutcome.Outcome) *tooloutcome.Outcome { return &o }
func TestSuccessfulRemoteJSONCannotCreateRecoveryState(t *testing.T) {
	for _, status := range []string{tooloutcome.Validation, tooloutcome.Denied, tooloutcome.Uncertain} {
		messages := []provider.Message{{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "success", Name: "write", Arguments: `{"n":1}`}}}, {Role: "tool", ToolCallID: "success", Content: tooloutcome.New(status, "remote_text", "unknown", "Remote body", "verify_effect").JSON()}}
		if got := toolRecoveryGuard(messages, "write", `{"n":1}`); got != nil {
			t.Fatalf("untrusted success became control: %+v", got)
		}
	}
}
func TestRecoveryIdentityPreservesLargeNumbers(t *testing.T) {
	a := tooloutcome.DefaultIdentity("write", json.RawMessage(`{"n":9007199254740992}`))
	b := tooloutcome.DefaultIdentity("write", json.RawMessage(`{"n":9007199254740993}`))
	if a == b {
		t.Fatal("distinct integer arguments collapsed")
	}
}

func TestResolvedRecoveryTargetsAndRiskSurviveCheckpoint(t *testing.T) {
	prior := tooloutcome.OperationIdentity("computer/vm/bot/fixture", "files.write", json.RawMessage(`{"path":"a","content":"X"}`))
	prior.Risk, prior.Target, prior.Object = tooloutcome.TargetMutation, "/workspace/a", "1:42"
	prior.GuardVersion = 1
	data, err := json.Marshal([]ToolRecoveryRecord{{Identity: &prior, Outcome: tooloutcome.New(tooloutcome.Uncertain, "lost", "unknown", "Synthetic response lost.", "verify_effect")}})
	if err != nil {
		t.Fatal(err)
	}
	var records []ToolRecoveryRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target, object, risk string
		blocked                    bool
	}{
		{"same-target-new-content", "/workspace/a", "1:43", tooloutcome.TargetMutation, true},
		{"hardlink-alias", "/workspace/link", "1:42", tooloutcome.TargetMutation, true},
		{"distinct-target", "/workspace/b", "1:44", tooloutcome.TargetMutation, false},
		{"unverified-target", "/workspace/b", "", tooloutcome.OpaqueEffect, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := tooloutcome.OperationIdentity(prior.Scope, prior.Operation, json.RawMessage(`{"path":"changed","content":"different"}`))
			i.Target, i.Object, i.Risk = tc.target, tc.object, tc.risk
			i.GuardVersion = 1
			if got := toolRecoveryIdentityGuard(records, i); (got != nil) != tc.blocked {
				t.Fatalf("guard=%+v identity=%+v", got, i)
			}
		})
	}
	observation := tooloutcome.OperationIdentity("computer/vm/bot/fixture", "files.read", json.RawMessage(`{"path":"a"}`))
	observation.Risk = tooloutcome.Observation
	uncertain := records[0].Outcome
	records = []ToolRecoveryRecord{{Identity: &observation, Outcome: uncertain}, {Identity: &observation, Outcome: uncertain}, {Identity: &observation, Outcome: uncertain}}
	other := tooloutcome.OperationIdentity(observation.Scope, observation.Operation, json.RawMessage(`{"path":"b"}`))
	other.Risk = tooloutcome.Observation
	// Failed observations have no effect: identical retries are capped, not fenced.
	if toolRecoveryIdentityGuard(records[:1], observation) != nil || toolRecoveryIdentityGuard(records, observation) == nil || toolRecoveryIdentityGuard(records, other) != nil {
		t.Fatal("observations were not scoped to the request")
	}
	// Legacy checkpoints with no risk/target evidence stay conservative.
	legacy := prior
	legacy.Risk = ""
	legacy.Target = ""
	legacy.Object = ""
	records[0].Identity = &legacy
	other.Scope, other.Operation = prior.Scope, prior.Operation
	other.Risk = tooloutcome.TargetMutation
	other.Target = "/workspace/b"
	if toolRecoveryIdentityGuard(records, other) == nil {
		t.Fatal("legacy unknown effect gained retry permission")
	}
}
