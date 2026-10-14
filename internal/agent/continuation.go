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

	// Kind is ContinuationInputWait (parked on a human answer, the only kind
	// v1 knew) or ContinuationBoundary (a crash-resume snapshot taken at a
	// model-response or tool-result boundary). Attempt is the run claim
	// generation that wrote the snapshot.
	Kind    string `json:"kind,omitempty"`
	Attempt int    `json:"attempt,omitempty"`

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

	QuestionID        string `json:"question_id"`
	WaitingToolCallID string `json:"waiting_tool_call_id"`
	WaitingToolName   string `json:"waiting_tool_name"`
	// SkippedToolCalls are the calls of the last assistant turn that have no
	// result yet and come after the waiting call. For a boundary snapshot (no
	// waiting call) it is every unresolved call of that turn, possibly none.
	SkippedToolCalls []provider.ToolCall `json:"skipped_tool_calls"`

	// RecoveryEpoch is the ledger window for observation retry limits.
	RecoveryEpoch int `json:"recovery_epoch,omitempty"`
}

// continuationVersion is written by this build. Version 1 payloads (human
// input waits only, no Kind) still decode and are treated as input_wait.
const continuationVersion = 2

const (
	ContinuationInputWait = "input_wait"
	ContinuationBoundary  = "boundary"
)

// EffectiveKind returns the effective kind: a payload without one (every v1
// payload) is a human-input wait.
func (c *Continuation) EffectiveKind() string {
	if c.Kind == "" {
		return ContinuationInputWait
	}
	return c.Kind
}

// ValidateContinuation rejects malformed or internally inconsistent state
// before it can be used to construct a new provider request.
func ValidateContinuation(c *Continuation) error {
	if c == nil {
		return errors.New("agent continuation is required")
	}
	if c.Version != 1 && c.Version != continuationVersion {
		return fmt.Errorf("unsupported agent continuation version %d (this build reads versions 1 and %d)", c.Version, continuationVersion)
	}
	kind := c.EffectiveKind()
	if kind != ContinuationInputWait && kind != ContinuationBoundary {
		return fmt.Errorf("unsupported agent continuation kind %q", c.Kind)
	}
	if c.Version == 1 && c.Kind != "" && c.Kind != ContinuationInputWait {
		return errors.New("agent continuation version 1 cannot carry a boundary snapshot")
	}
	if c.Attempt < 0 {
		return errors.New("agent continuation has negative attempt")
	}
	if c.LLMCalls < 0 || c.Step < 0 || c.AssistantTurnIndex < 0 || c.ToolCallsSinceReport < 0 || c.ActiveElapsedNanos < 0 || c.RecoveryEpoch < 0 {
		return errors.New("agent continuation has negative counters")
	}
	if c.TotalUsage.InputTokens < 0 || c.TotalUsage.OutputTokens < 0 {
		return errors.New("agent continuation has negative usage")
	}
	if kind == ContinuationInputWait && (strings.TrimSpace(c.QuestionID) == "" || strings.TrimSpace(c.WaitingToolCallID) == "" || strings.TrimSpace(c.WaitingToolName) == "") {
		return errors.New("agent continuation is missing waiting tool state")
	}
	if kind == ContinuationBoundary && (c.QuestionID != "" || c.WaitingToolCallID != "" || c.WaitingToolName != "") {
		return errors.New("agent boundary continuation must not carry waiting tool state")
	}
	if len(c.Messages) == 0 {
		return errors.New("agent continuation has no messages")
	}
	for _, record := range c.ToolRecovery {
		if record.Call.ID == "" || record.Call.Name == "" || record.Outcome.Version != 1 || !recoveryStatus(record.Outcome.Status) || record.Outcome.Certainty == "" || record.Outcome.NextAction == "" {
			return errors.New("agent continuation has invalid tool recovery state")
		}
	}

	if kind == ContinuationBoundary {
		unresolved := unresolvedLastTurnCalls(c.Messages)
		if len(unresolved) != len(c.SkippedToolCalls) {
			return errors.New("agent boundary continuation unresolved calls do not match transcript")
		}
		for i, call := range unresolved {
			if call.ID == "" || call.ID != c.SkippedToolCalls[i].ID || call.Name != c.SkippedToolCalls[i].Name || call.Arguments != c.SkippedToolCalls[i].Arguments {
				return errors.New("agent boundary continuation unresolved call does not match transcript")
			}
		}
	} else if err := validateWaitingBatch(c); err != nil {
		return err
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
	// ModelUsage tracks input and output tokens only; TotalUsage also carries
	// cache reads/writes, so compare just the fields both sides record.
	if trackedUsage.InputTokens != c.TotalUsage.InputTokens || trackedUsage.OutputTokens != c.TotalUsage.OutputTokens || trackedCalls != c.LLMCalls {
		return errors.New("agent continuation usage counters do not match model usage")
	}
	return nil
}

func validateWaitingBatch(c *Continuation) error {
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
	return nil
}

// unresolvedLastTurnCalls returns the calls of the last assistant turn that
// has tool calls and no tool result yet, in dispatch order.
func unresolvedLastTurnCalls(messages []provider.Message) []provider.ToolCall {
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 {
			last = i
			break
		}
	}
	if last < 0 {
		return nil
	}
	resolved := map[string]bool{}
	for _, msg := range messages[last+1:] {
		if msg.Role == "tool" {
			resolved[msg.ToolCallID] = true
		}
	}
	var out []provider.ToolCall
	for _, call := range messages[last].ToolCalls {
		if !resolved[call.ID] {
			out = append(out, call)
		}
	}
	return out
}

func continuationActiveElapsed(cfg *AgentConfig, runStart time.Time) time.Duration {
	elapsed := time.Since(runStart)
	if cfg != nil && cfg.ToolsOnly && cfg.UserWaitDuration != nil {
		elapsed -= min(elapsed, max(0, cfg.UserWaitDuration()))
	}
	return max(0, elapsed)
}

// snapshotContinuation captures the loop state needed to resume a ToolsOnly
// run from this point. It sets no waiting-tool fields; callers add them for a
// human-input suspension, otherwise the result is a boundary snapshot whose
// SkippedToolCalls are the last assistant turn's unresolved calls.
func snapshotContinuation(state *AgentState, cfg *AgentConfig, messages []provider.Message, assistantTurnIndex int, runStart time.Time, budgetWrapUp, finalResponseRepaired, finalRepairPending, finalRepairReserved, finalRepairFinalPending bool, toolCallsSinceReport int, reportRequired bool, recovery []ToolRecoveryRecord, recoveryEpoch int) *Continuation {
	state = state.WithMessages(messages)
	attempt := 0
	if cfg != nil {
		attempt = cfg.Attempt
	}
	return &Continuation{
		Version:                 continuationVersion,
		Kind:                    ContinuationBoundary,
		Attempt:                 attempt,
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
		SkippedToolCalls:        append([]provider.ToolCall(nil), unresolvedLastTurnCalls(messages)...),
		RecoveryEpoch:           recoveryEpoch,
	}
}

func newSuspendedResult(state *AgentState, cfg *AgentConfig, model string, messages []provider.Message, assistantTurnIndex int, runStart time.Time, budgetWrapUp, finalResponseRepaired, finalRepairPending, finalRepairReserved, finalRepairFinalPending bool, toolCallsSinceReport int, reportRequired bool, questionID string, waiting provider.ToolCall, recovery []ToolRecoveryRecord) *AgentResult {
	state = state.WithMessages(messages)
	c := snapshotContinuation(state, cfg, messages, assistantTurnIndex, runStart, budgetWrapUp, finalResponseRepaired, finalRepairPending, finalRepairReserved, finalRepairFinalPending, toolCallsSinceReport, reportRequired, recovery, 0)
	c.Kind = ContinuationInputWait
	c.QuestionID = questionID
	c.WaitingToolCallID = waiting.ID
	c.WaitingToolName = waiting.Name
	c.SkippedToolCalls = append([]provider.ToolCall(nil), waitingBatchCalls(messages, waiting.ID)...)
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
		Continuation:   c,
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

// ResumeToolResult is the backend-recorded settlement of a tool call that has
// no result in a boundary checkpoint (it was running or queued when the
// service stopped). Content is the tool message the model will read.
type ResumeToolResult struct {
	Content string
	Outcome *tooloutcome.Outcome
	Failed  bool
}

// RestartInterruptedOutcome settles a call that was running when the service
// stopped. A read-only call is safe to repeat; anything else is an unverified
// effect and must be checked before any new attempt.
func RestartInterruptedOutcome(observation bool) tooloutcome.Outcome {
	if observation {
		return tooloutcome.New(tooloutcome.Transient, "restart_interrupted", "no_side_effects", "The service restarted while this read-only call was running. It had no side effects; call it again if the result is still needed.", "retry")
	}
	return tooloutcome.New(tooloutcome.Uncertain, "restart_interrupted_effect", "unknown", "Execution was interrupted by a service restart. Verify the recorded target state before any new workflow attempts this effect.", "verify_effect")
}

// RestartSkippedOutcome settles a call that was queued behind an interrupted one.
func RestartSkippedOutcome() tooloutcome.Outcome {
	return tooloutcome.New(tooloutcome.Permanent, "batch_skipped", "not_executed", "This call was queued behind a call that a service restart interrupted and was not executed. Do not replay the old batch; decide whether to call a tool again based on the resumed context.", "replan_after_restart")
}

// resumeBoundary rebuilds the provider transcript of a boundary checkpoint.
// Every call of the last assistant turn that has no result gets one tool
// message: the backend-recorded settlement when there is one, otherwise a
// not-executed skip. With no unresolved call nothing is added and the model is
// simply asked again.
func resumeBoundary(c *Continuation, settled map[string]ResumeToolResult) ([]provider.Message, error) {
	if err := ValidateContinuation(c); err != nil {
		return nil, err
	}
	if c.EffectiveKind() != ContinuationBoundary {
		return nil, errors.New("agent continuation is not a boundary snapshot")
	}
	messages := append([]provider.Message(nil), c.Messages...)
	for _, call := range c.SkippedToolCalls {
		result, ok := settled[call.ID]
		if !ok {
			skipped := RestartSkippedOutcome()
			result = ResumeToolResult{Content: skipped.JSON(), Outcome: &skipped, Failed: true}
		}
		messages = append(messages, provider.Message{
			Role:        "tool",
			Content:     result.Content,
			ToolFailed:  result.Failed,
			ToolCallID:  call.ID,
			ToolName:    call.Name,
			ToolOutcome: result.Outcome,
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
				records = append(records, ToolRecoveryRecord{Call: call, Outcome: o, Batch: toolCallBatch(m.ToolCalls)})
			}
		}
	}
	return messages, records, nil
}

// ApprovalExpiryGuard checks identity within the suspended run's RecoveryEpoch.
func ApprovalExpiryGuard(records []ToolRecoveryRecord, identity tooloutcome.Identity, epoch int) *tooloutcome.Outcome {
	return toolRecoveryIdentityGuardAt(records, identity, epoch)
}
