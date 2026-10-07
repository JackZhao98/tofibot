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
			if got := toolRecoveryIdentityGuardAt(records, i, 0); (got != nil) != tc.blocked {
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
	if toolRecoveryIdentityGuardAt(records[:1], observation, 0) != nil || toolRecoveryIdentityGuardAt(records, observation, 0) == nil || toolRecoveryIdentityGuardAt(records, other, 0) != nil {
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
	if toolRecoveryIdentityGuardAt(records, other, 0) == nil {
		t.Fatal("legacy unknown effect gained retry permission")
	}
}

func TestUncertainShellExecFencesReformattedCommand(t *testing.T) {
	shell := func(args string) tooloutcome.Identity {
		return tooloutcome.OperationIdentity("computer/microvm/bot/b1", "shell.exec", json.RawMessage(args))
	}
	priorArgs := `{"command":"rm  -rf\tbuild","timeout_sec":30}`
	prior := shell(priorArgs)
	lost := tooloutcome.New(tooloutcome.Uncertain, "lost", "unknown", "Response lost.", "verify_effect")
	records := []ToolRecoveryRecord{{Call: provider.ToolCall{ID: "a", Name: "computer_shell", Arguments: priorArgs}, Identity: &prior, Outcome: lost}}
	for _, args := range []string{`{"command":"rm -rf build"}`, `{"command":" rm -rf build ","timeout_sec":90}`} {
		if toolRecoveryCallGuardAt(records, shell(args), args, 0) == nil {
			t.Fatalf("reformatted uncertain command replayed: %s", args)
		}
	}
	action := `{"computer_id":"microvm","action":"shell.exec","args":{"command":"rm -rf build"}}`
	if toolRecoveryCallGuardAt(records, shell(`{"command":"rm -rf build"}`), action, 0) == nil {
		t.Fatal("computer_action form replayed the uncertain command")
	}
	if got := toolRecoveryCallGuardAt(records, shell(`{"command":"ls build"}`), `{"command":"ls build"}`, 0); got != nil {
		t.Fatalf("different command fenced: %+v", got)
	}
}

func TestApprovalExpiryGuardUsesRunRecoveryEpoch(t *testing.T) {
	obs := tooloutcome.DefaultIdentity("list_skills", json.RawMessage(`{}`))
	failed := tooloutcome.New(tooloutcome.Permanent, "observation_failed", "no_side_effects", "failed", "explain_blocker")
	var records []ToolRecoveryRecord
	for i := 0; i < observationRetryLimit; i++ {
		records = append(records, ToolRecoveryRecord{Call: provider.ToolCall{ID: "x", Name: "list_skills", Arguments: `{}`}, Identity: &obs, Outcome: failed, Epoch: 1})
	}
	if ApprovalExpiryGuard(records, obs, 1) == nil {
		t.Fatal("failures in the current epoch were not capped")
	}
	if got := ApprovalExpiryGuard(records, obs, 2); got != nil {
		t.Fatalf("a later successful action did not reset the window: %+v", got)
	}
}
