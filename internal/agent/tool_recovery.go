package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const toolRepairLimit = 3

// observationRetryLimit caps identical failed observations per epoch.
const observationRetryLimit = 3

// Backend-owned recovery records outlive provider transcript compaction.
type ToolRecoveryRecord struct {
	Identity *tooloutcome.Identity  `json:"identity,omitempty"`
	Evidence []tooloutcome.Identity `json:"evidence,omitempty"`
	Call     provider.ToolCall      `json:"call"`
	Outcome  tooloutcome.Outcome    `json:"outcome"`
	// Epoch is the number of successful non-observation actions before the
	// failure; a later successful action resets observation retry counts.
	Epoch int `json:"epoch,omitempty"`
	// Batch identifies the assistant turn that issued the call (its first
	// call ID). Validation failures in one batch spend one repair together.
	Batch string `json:"batch,omitempty"`
}

// invalidArgumentsOutcome tells the model its own call was malformed. Empty
// arguments are a model/stream-side fault; the call was not executed.
func invalidArgumentsOutcome(name, args string, err error) tooloutcome.Outcome {
	msg := fmt.Sprintf("Error parsing arguments for %s: %v", name, err)
	if strings.TrimSpace(args) == "" {
		msg = fmt.Sprintf("The %s call arrived with empty arguments and was not executed. Resend it with a complete JSON object.", name)
	}
	return tooloutcome.New(tooloutcome.Validation, "invalid_json", "not_executed", msg, "repair_arguments")
}

// toolCallBatch names the assistant turn that issued calls.
func toolCallBatch(calls []provider.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].ID
}

func recoveryStatus(status string) bool {
	return status == tooloutcome.Expired || status == tooloutcome.Validation || status == tooloutcome.Uncertain || status == tooloutcome.Denied || status == tooloutcome.Permanent || status == tooloutcome.Transient
}

func toolRecoveryRecords(messages []provider.Message) []ToolRecoveryRecord {
	calls := map[string]provider.ToolCall{}
	batches := map[string]string{}
	var records []ToolRecoveryRecord
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			calls[call.ID] = call
			batches[call.ID] = toolCallBatch(msg.ToolCalls)
		}
		if msg.Role == "tool" {
			if o := msg.ToolOutcome; o != nil && recoveryStatus(o.Status) {
				if call, ok := calls[msg.ToolCallID]; ok {
					records = append(records, ToolRecoveryRecord{Call: call, Outcome: *o, Batch: batches[call.ID]})
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

// Transcript-derived legacy records carry no epoch.
func toolRecoveryRecordsGuard(records []ToolRecoveryRecord, name, args string) *tooloutcome.Outcome {
	return toolRecoveryCallGuardAt(records, tooloutcome.DefaultIdentity(name, json.RawMessage(args)), args, 0)
}

// toolRecoveryIdentityGuardAt checks identity within the run's epoch.
func toolRecoveryIdentityGuardAt(records []ToolRecoveryRecord, identity tooloutcome.Identity, epoch int) *tooloutcome.Outcome {
	return toolRecoveryCallGuardAt(records, identity, "", epoch)
}

// toolRecoveryCallGuardAt fences replays. A failed observation has no
// effect, so an identical retry is allowed (its precondition may have been
// fixed) until observationRetryLimit failures accrue within one epoch. args
// are the candidate's raw tool arguments, when known.
func toolRecoveryCallGuardAt(records []ToolRecoveryRecord, identity tooloutcome.Identity, args string, epoch int) *tooloutcome.Outcome {
	repairs := 0
	repairBatches := map[string]bool{}
	observationFailures := 0
	for _, record := range records {
		prior := tooloutcome.DefaultIdentity(record.Call.Name, json.RawMessage(record.Call.Arguments))
		if record.Identity != nil {
			prior = *record.Identity
		}
		o := record.Outcome
		if o.Status == tooloutcome.Permanent && o.Code == "batch_skipped" && o.Certainty == "not_executed" {
			// This records a stale provider call, not a denial of its capability.
			// Fresh calls still pass normal schema, identity and approval checks.
			continue
		}
		if o.Status == tooloutcome.Expired && prior.Scope == identity.Scope && prior.Operation == identity.Operation {
			// Changing arguments cannot turn an expired proposal into permission.
			return &o
		}
		if identity.Risk == tooloutcome.Observation && prior.Risk == tooloutcome.Observation && prior.Scope == identity.Scope && prior.Operation == identity.Operation &&
			(o.Status == tooloutcome.Permanent || o.Status == tooloutcome.Uncertain || o.Status == tooloutcome.Transient) {
			if prior.ArgumentsHash == identity.ArgumentsHash && record.Epoch == epoch {
				observationFailures++
			}
			continue
		}
		if o.Status == tooloutcome.Uncertain {
			if identity.ResolutionRequired {
				continue
			} // executor must resolve and recheck before dispatch
			if uncertainIdentityBlocks(prior, identity) || sameShellCommand(prior, record.Call.Arguments, identity, args) {
				return &o
			}
			for _, evidence := range record.Evidence {
				if uncertainIdentityBlocks(evidence, identity) {
					return &o
				}
			}
			continue
		}
		if prior.Scope != identity.Scope || prior.Operation != identity.Operation {
			continue
		}
		if o.Status == tooloutcome.Validation && (record.Batch == "" || !repairBatches[record.Batch]) {
			// Several malformed parallel calls from one model turn are one
			// repair attempt, not one per call.
			repairs++
			if record.Batch != "" {
				repairBatches[record.Batch] = true
			}
		}
		if prior.ArgumentsHash == identity.ArgumentsHash && (o.Status == tooloutcome.Denied || o.Status == tooloutcome.Permanent || (o.Status == tooloutcome.Transient && o.NextAction != "retry")) {
			return &o
		}
	}
	if observationFailures >= observationRetryLimit {
		o := tooloutcome.New(tooloutcome.Permanent, "observation_retry_limit", "not_executed", "This identical observation has failed repeatedly since the last successful action. Change its arguments, fix its precondition with another action first, or explain the blocker.", "explain_blocker")
		o.RetryLimit = observationRetryLimit
		return &o
	}
	if repairs >= toolRepairLimit {
		o := tooloutcome.New(tooloutcome.Permanent, "repair_budget_exhausted", "not_executed", "Argument/schema repair budget exhausted for this operation. Explain the blocker and use another permitted capability or request corrected information.", "explain_blocker")
		o.RepairLimit = toolRepairLimit
		return &o
	}
	return nil
}

func uncertainIdentityBlocks(prior, candidate tooloutcome.Identity) bool {
	if prior.Scope != candidate.Scope || prior.Operation != candidate.Operation {
		return false
	}
	if prior.Risk == tooloutcome.Observation && candidate.Risk == tooloutcome.Observation {
		return prior.ArgumentsHash == candidate.ArgumentsHash
	}
	if prior.Risk == tooloutcome.TargetMutation && candidate.Risk == tooloutcome.TargetMutation && prior.Target != "" && candidate.Target != "" {
		// A missing file's inode is unknown after a lost create response. Later
		// lookups cannot attribute the effect; legacy unbound evidence is also
		// insufficient. Keep the entire write operation fenced in either case.
		if prior.Operation == "files.write" && strings.HasPrefix(prior.Scope, "computer/") && (prior.GuardVersion != 1 || candidate.GuardVersion != 1 || prior.Object == "") {
			return true
		}
		return prior.Target == candidate.Target || (prior.Object != "" && prior.Object == candidate.Object)
	}
	if strings.HasPrefix(prior.Scope, "computer/") && !strings.HasPrefix(prior.Operation, "files.") && prior.Risk == tooloutcome.OpaqueEffect && candidate.Risk == tooloutcome.OpaqueEffect {
		// A lost VM action response (click, key, shell command) fences only its
		// exact replay; a different action of the same kind is a new decision.
		// File writes whose target could not be resolved stay fully fenced.
		return prior.ArgumentsHash == candidate.ArgumentsHash
	}
	return true
}

// A lost shell.exec response fences the command itself; whitespace and
// timeout_sec changes are the same replay.
func sameShellCommand(prior tooloutcome.Identity, priorArgs string, candidate tooloutcome.Identity, candidateArgs string) bool {
	if prior.Operation != "shell.exec" || candidate.Operation != "shell.exec" || prior.Scope != candidate.Scope || !strings.HasPrefix(prior.Scope, "computer/") {
		return false
	}
	command := shellCommandKey(priorArgs)
	return command != "" && command == shellCommandKey(candidateArgs)
}

// shellCommandKey reads computer_shell {command} or computer_action
// {action: shell.exec, args: {command}} with whitespace runs collapsed.
func shellCommandKey(raw string) string {
	var in struct {
		Command string `json:"command"`
		Action  string `json:"action"`
		Args    struct {
			Command string `json:"command"`
		} `json:"args"`
	}
	if json.Unmarshal([]byte(raw), &in) != nil {
		return ""
	}
	if in.Action == "shell.exec" {
		in.Command = in.Args.Command
	}
	return strings.Join(strings.Fields(in.Command), " ")
}
