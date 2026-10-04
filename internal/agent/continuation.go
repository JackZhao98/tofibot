package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// SuspensionError is the narrow control signal used by a bounded runtime tool
// to stop an agent loop for durable human input. It is intentionally an
// interface so the agent package does not import its runtime adapter.
// Implementations must not be converted into model-visible tool errors.
type SuspensionError interface {
	error
	UserInputQuestionID() string
}

func suspensionQuestionID(err error) (string, bool) {
	var control SuspensionError
	if !errors.As(err, &control) {
		return "", false
	}
	questionID := strings.TrimSpace(control.UserInputQuestionID())
	return questionID, questionID != ""
}

// Continuation is the agent-owned portion of a durable ToolsOnly suspension.
// It deliberately retains provider messages rather than user-facing messages:
// assistant tool calls and their provider IDs are protocol state, not a
// reconstructible presentation detail.
type Continuation struct {
	Version int `json:"version"`

	Messages     []provider.Message    `json:"messages"`
	ToolRecovery []ToolRecoveryRecord  `json:"tool_recovery,omitempty"`
	TotalUsage   provider.Usage        `json:"total_usage"`
	ModelUsage   map[string]ModelUsage `json:"model_usage"`
	LLMCalls     int                   `json:"llm_calls"`
	Step         int                   `json:"step"`

	AssistantTurnIndex      int   `json:"assistant_turn_index"`
	ToolCallsSinceReport    int   `json:"tool_calls_since_report,omitempty"`
	ReportRequired          bool  `json:"report_required,omitempty"`
	ActiveElapsedNanos      int64 `json:"active_elapsed_nanos"`
	BudgetWrapUp            bool  `json:"budget_wrap_up"`
	FinalResponseRepaired   bool  `json:"final_response_repaired"`
	FinalRepairPending      bool  `json:"final_repair_pending"`
	FinalRepairReserved     bool  `json:"final_repair_reserved"`
	FinalRepairFinalPending bool  `json:"final_repair_final_pending"`

	QuestionID        string              `json:"question_id"`
	WaitingToolCallID string              `json:"waiting_tool_call_id"`
	WaitingToolName   string              `json:"waiting_tool_name"`
	SkippedToolCalls  []provider.ToolCall `json:"skipped_tool_calls"`
}

const continuationVersion = 1

// ValidateContinuation rejects malformed or internally inconsistent state
// before it can be used to construct a new provider request.
func ValidateContinuation(c *Continuation) error {
	if c == nil {
		return errors.New("agent continuation is required")
	}
	if c.Version != continuationVersion {
		return fmt.Errorf("unsupported agent continuation version %d", c.Version)
	}
	if c.LLMCalls < 0 || c.Step < 0 || c.AssistantTurnIndex < 0 || c.ToolCallsSinceReport < 0 || c.ActiveElapsedNanos < 0 {
		return errors.New("agent continuation has negative counters")
	}
	if c.TotalUsage.InputTokens < 0 || c.TotalUsage.OutputTokens < 0 {
		return errors.New("agent continuation has negative usage")
	}
	if strings.TrimSpace(c.QuestionID) == "" || strings.TrimSpace(c.WaitingToolCallID) == "" || strings.TrimSpace(c.WaitingToolName) == "" {
		return errors.New("agent continuation is missing waiting tool state")
	}
	if len(c.Messages) == 0 {
		return errors.New("agent continuation has no messages")
	}
	for _, record := range c.ToolRecovery {
		if record.Call.ID == "" || record.Call.Name == "" || record.Outcome.Version != 1 || !recoveryStatus(record.Outcome.Status) || record.Outcome.Certainty == "" || record.Outcome.NextAction == "" {
			return errors.New("agent continuation has invalid tool recovery state")
		}
	}

	var waitingBatch []provider.ToolCall
	seenWaiting := false
	for _, msg := range c.Messages {
		if msg.Role != "assistant" {
			continue
		}
		for i, call := range msg.ToolCalls {
			if call.ID != c.WaitingToolCallID {
				continue
			}
			if seenWaiting || call.Name != c.WaitingToolName {
				return errors.New("agent continuation has invalid waiting tool call")
			}
			seenWaiting = true
			waitingBatch = append(waitingBatch, msg.ToolCalls[i+1:]...)
		}
	}
	if !seenWaiting {
		return errors.New("agent continuation waiting tool is absent from transcript")
	}
	for _, msg := range c.Messages {
		if msg.Role == "tool" && msg.ToolCallID == c.WaitingToolCallID {
			return errors.New("agent continuation waiting tool is already resolved")
		}
	}
	if len(waitingBatch) != len(c.SkippedToolCalls) {
		return errors.New("agent continuation skipped tool batch does not match waiting batch")
	}
	for i, call := range waitingBatch {
		if call.ID == "" || call.ID != c.SkippedToolCalls[i].ID || call.Name != c.SkippedToolCalls[i].Name || call.Arguments != c.SkippedToolCalls[i].Arguments {
			return errors.New("agent continuation skipped tool call does not match transcript")
		}
		for _, msg := range c.Messages {
			if msg.Role == "tool" && msg.ToolCallID == call.ID {
				return errors.New("agent continuation skipped tool is already resolved")
			}
		}
	}
	var trackedUsage provider.Usage
	trackedCalls := 0
	for model, usage := range c.ModelUsage {
		if strings.TrimSpace(model) == "" || usage.InputTokens < 0 || usage.OutputTokens < 0 || usage.TotalCostUSD < 0 || usage.APICallCount < 0 {
			return errors.New("agent continuation has invalid model usage")
		}
		trackedUsage.InputTokens += usage.InputTokens
		trackedUsage.OutputTokens += usage.OutputTokens
		trackedCalls += usage.APICallCount
	}
	if trackedUsage != c.TotalUsage || trackedCalls != c.LLMCalls {
		return errors.New("agent continuation usage counters do not match model usage")
	}
	return nil
}

func continuationActiveElapsed(cfg *AgentConfig, runStart time.Time) time.Duration {
	elapsed := time.Since(runStart)
	if cfg != nil && cfg.ToolsOnly && cfg.UserWaitDuration != nil {
		elapsed -= min(elapsed, max(0, cfg.UserWaitDuration()))
	}
	return max(0, elapsed)
}

func newSuspendedResult(state *AgentState, cfg *AgentConfig, model string, messages []provider.Message, assistantTurnIndex int, runStart time.Time, budgetWrapUp, finalResponseRepaired, finalRepairPending, finalRepairReserved, finalRepairFinalPending bool, toolCallsSinceReport int, reportRequired bool, questionID string, waiting provider.ToolCall, recovery []ToolRecoveryRecord) *AgentResult {
	state = state.WithMessages(messages)
	return &AgentResult{
		TotalUsage:     state.TotalUsage,
		TotalCost:      state.Tracker.TotalCost(),
		Model:          model,
		LLMCalls:       state.LLMCalls,
		LoadedSkills:   mapKeys(state.LoadedSkills),
		Messages:       state.NewMessages(),
		ModelBreakdown: state.Tracker.ModelBreakdown(),
		Trace:          state.Trace,
		Suspended:      true,
		QuestionID:     questionID,
		Continuation: &Continuation{
			Version:                 continuationVersion,
			Messages:                append([]provider.Message(nil), messages...),
			ToolRecovery:            append([]ToolRecoveryRecord(nil), recovery...),
			TotalUsage:              state.TotalUsage,
			ModelUsage:              state.Tracker.ModelBreakdown(),
			LLMCalls:                state.LLMCalls,
			Step:                    state.Step,
			AssistantTurnIndex:      assistantTurnIndex,
			ToolCallsSinceReport:    toolCallsSinceReport,
			ReportRequired:          reportRequired,
			ActiveElapsedNanos:      continuationActiveElapsed(cfg, runStart).Nanoseconds(),
			BudgetWrapUp:            budgetWrapUp,
			FinalResponseRepaired:   finalResponseRepaired,
			FinalRepairPending:      finalRepairPending,
			FinalRepairReserved:     finalRepairReserved,
			FinalRepairFinalPending: finalRepairFinalPending,
			QuestionID:              questionID,
			WaitingToolCallID:       waiting.ID,
			WaitingToolName:         waiting.Name,
			SkippedToolCalls:        append([]provider.ToolCall(nil), waitingBatchCalls(messages, waiting.ID)...),
		},
	}
}

func waitingBatchCalls(messages []provider.Message, waitingID string) []provider.ToolCall {
	for _, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for i, call := range msg.ToolCalls {
			if call.ID == waitingID {
				return msg.ToolCalls[i+1:]
			}
		}
	}
	return nil
}

func resumeContinuation(c *Continuation, result string, outcomes ...*tooloutcome.Outcome) ([]provider.Message, error) {
	if err := ValidateContinuation(c); err != nil {
		return nil, err
	}
	messages := append([]provider.Message(nil), c.Messages...)
	var outcome *tooloutcome.Outcome
	if len(outcomes) > 0 {
		outcome = outcomes[0]
	}
	messages = append(messages, provider.Message{
		Role:        "tool",
		Content:     result,
		ToolOutcome: outcome, ToolFailed: outcome != nil && outcome.Status != "approval_recorded",
		ToolCallID: c.WaitingToolCallID,
		ToolName:   c.WaitingToolName,
	})
	for _, call := range c.SkippedToolCalls {
		skipped := tooloutcome.New(tooloutcome.Permanent, "batch_skipped", "not_executed", "This old queued call was not executed after the human-input boundary. Do not replay the interrupted batch. A fresh proposal must be revalidated and independently authorized using the resumed context.", "replan_after_input")
		messages = append(messages, provider.Message{
			Role:        "tool",
			ToolFailed:  true,
			Content:     "Tool error: execution skipped after human input suspension. Do not reuse this stale tool call; decide whether to call a tool again based on the resumed context.",
			ToolCallID:  call.ID,
			ToolName:    call.Name,
			ToolOutcome: &skipped,
		})
	}
	return messages, nil
}

// ResumeApprovalExpiry resolves the interrupted batch without dispatching it.
// Recovery records remain backend control metadata rather than model output.
func ResumeApprovalExpiry(c *Continuation) ([]provider.Message, []ToolRecoveryRecord, error) {
	o := tooloutcome.New(tooloutcome.Expired, "approval_window_expired", "not_executed", "Approval expired. This call was not executed. Do not retry it, change its arguments, or switch providers to perform the same effect. Summarize completed and incomplete work.", "finish_summary")
	messages, err := resumeContinuation(c, o.JSON(), &o)
	if err != nil {
		return nil, nil, err
	}
	records := append([]ToolRecoveryRecord(nil), c.ToolRecovery...)
	if c.ToolRecovery == nil {
		records = toolRecoveryRecords(c.Messages)
	}

	// The waiting call precedes the appended result; add its exact identity.
	for _, m := range c.Messages {
		for _, call := range m.ToolCalls {
			if call.ID == c.WaitingToolCallID {
				records = append(records, ToolRecoveryRecord{Call: call, Outcome: o})
			}
		}
	}
	return messages, records, nil
}

func ApprovalExpiryGuard(records []ToolRecoveryRecord, identity tooloutcome.Identity) *tooloutcome.Outcome {
	return toolRecoveryIdentityGuard(records, identity)
}
