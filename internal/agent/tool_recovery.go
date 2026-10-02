package agent

import (
	"encoding/json"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const toolRepairLimit = 3

// Backend-owned recovery records outlive provider transcript compaction.
type ToolRecoveryRecord struct {
	Call    provider.ToolCall   `json:"call"`
	Outcome tooloutcome.Outcome `json:"outcome"`
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
			if o := tooloutcome.Parse(msg.Content); o != nil && recoveryStatus(o.Status) {
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
	repairs := 0
	canonical := func(raw string) string {
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			return raw
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
	key := func(name, raw string) string {
		if name != "call_mcp_tool" {
			return name
		}
		var in struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(raw), &in)
		return name + "/" + in.Name
	}
	target := key(name, args)
	for _, record := range records {
		call := record.Call
		if key(call.Name, call.Arguments) != target {
			continue
		}
		o := record.Outcome
		if o.Status == tooloutcome.Validation {
			repairs++
		}
		if canonical(call.Arguments) == canonical(args) && (o.Status == tooloutcome.Uncertain || o.Status == tooloutcome.Denied || o.Status == tooloutcome.Permanent || (o.Status == tooloutcome.Transient && o.NextAction != "retry")) {
			return &o
		}
	}
	if repairs >= toolRepairLimit {
		o := tooloutcome.New(tooloutcome.Permanent, "repair_budget_exhausted", "not_executed", "Argument/schema repair budget exhausted for this tool. Explain the blocker and use another permitted capability or request corrected information.", "explain_blocker")
		o.RepairLimit = toolRepairLimit
		return &o
	}
	return nil
}
