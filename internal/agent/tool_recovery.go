package agent

import (
	"encoding/json"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const toolRepairLimit = 3

// Backend-owned recovery records outlive provider transcript compaction.
type ToolRecoveryRecord struct {
	Identity *tooloutcome.Identity `json:"identity,omitempty"`
	Call     provider.ToolCall     `json:"call"`
	Outcome  tooloutcome.Outcome   `json:"outcome"`
}

func recoveryStatus(status string) bool {
	return status == tooloutcome.Validation || status == tooloutcome.Uncertain || status == tooloutcome.Denied || status == tooloutcome.Permanent || status == tooloutcome.Transient
}

func toolRecoveryRecords(messages []provider.Message) []ToolRecoveryRecord {
	calls := map[string]provider.ToolCall{}
	var records []ToolRecoveryRecord
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = call
		}
		if msg.Role == "tool" {
			if o := msg.ToolOutcome; o != nil && recoveryStatus(o.Status) {
				if call, ok := calls[msg.ToolCallID]; ok {
					records = append(records, ToolRecoveryRecord{Call: call, Outcome: *o})
				}
			}
		}
	}
	return records
}

// Legacy checkpoints can derive their records from the intact transcript.
func toolRecoveryGuard(messages []provider.Message, name, args string) *tooloutcome.Outcome {
	return toolRecoveryRecordsGuard(toolRecoveryRecords(messages), name, args)
}

func toolRecoveryRecordsGuard(records []ToolRecoveryRecord, name, args string) *tooloutcome.Outcome {
	return toolRecoveryIdentityGuard(records, tooloutcome.DefaultIdentity(name, json.RawMessage(args)))
}

func toolRecoveryIdentityGuard(records []ToolRecoveryRecord, identity tooloutcome.Identity) *tooloutcome.Outcome {
	repairs := 0
	for _, record := range records {
		prior := tooloutcome.DefaultIdentity(record.Call.Name, json.RawMessage(record.Call.Arguments))
		if record.Identity != nil {
			prior = *record.Identity
		}
		if prior.Scope != identity.Scope || prior.Operation != identity.Operation {
			continue
		}
		o := record.Outcome
		// No backend verification has resolved this dispatch. Changed wrapper
		// arguments, path aliases or defaults do not prove that its effect is
		// absent. Observations use separate operations and remain available.
		if o.Status == tooloutcome.Uncertain {
			return &o
		}
		if o.Status == tooloutcome.Validation {
			repairs++
		}
		if prior.ArgumentsHash == identity.ArgumentsHash && (o.Status == tooloutcome.Denied || o.Status == tooloutcome.Permanent || (o.Status == tooloutcome.Transient && o.NextAction != "retry")) {
			return &o
		}
	}
	if repairs >= toolRepairLimit {
		o := tooloutcome.New(tooloutcome.Permanent, "repair_budget_exhausted", "not_executed", "Argument/schema repair budget exhausted for this operation. Explain the blocker and use another permitted capability or request corrected information.", "explain_blocker")
		o.RepairLimit = toolRepairLimit
		return &o
	}
	return nil
}
