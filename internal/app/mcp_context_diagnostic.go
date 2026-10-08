package app

import "errors"

// MCPContextFailure is host-generated diagnostic metadata, never approval
// evidence. It contains no identifiers, content, provider errors or SQL text.
// Counts from LIMIT 201 are lower bounds; byte sizes saturate at 1 MiB.
type MCPContextFailure struct {
	Code            mcpContextFailureCode `json:"code"`
	Observed        *int                  `json:"observed,omitempty"`
	Limit           *int                  `json:"limit,omitempty"`
	ObservedAtLeast bool                  `json:"observed_at_least,omitempty"`
}

type mcpContextFailureCode string

const (
	mcpContextRunRead           mcpContextFailureCode = "run_read_failed"
	mcpContextRunBinding        mcpContextFailureCode = "run_binding_changed"
	mcpContextUserUnavailable   mcpContextFailureCode = "user_context_unavailable"
	mcpContextIntentRead        mcpContextFailureCode = "intent_read_failed"
	mcpContextIntentInvalid     mcpContextFailureCode = "intent_invalid"
	mcpContextIntentProvenance  mcpContextFailureCode = "intent_provenance_unverified"
	mcpContextInstructionsRead  mcpContextFailureCode = "instructions_read_failed"
	mcpContextAttachmentsRead   mcpContextFailureCode = "attachments_read_failed"
	mcpContextNonText           mcpContextFailureCode = "non_text_context"
	mcpContextAttachmentScope   mcpContextFailureCode = "attachment_scope_unavailable"
	mcpContextAttachmentsLimit  mcpContextFailureCode = "attachment_bindings_limit_exceeded"
	mcpContextMessagesQuery     mcpContextFailureCode = "messages_query_failed"
	mcpContextMessagesScan      mcpContextFailureCode = "messages_scan_failed"
	mcpContextMessagesIteration mcpContextFailureCode = "messages_iteration_failed"
	mcpContextMessagesLimit     mcpContextFailureCode = "messages_limit_exceeded"
	mcpContextMemoriesQuery     mcpContextFailureCode = "memories_query_failed"
	mcpContextMemoriesScan      mcpContextFailureCode = "memories_scan_failed"
	mcpContextMemoriesIteration mcpContextFailureCode = "memories_iteration_failed"
	mcpContextMemoriesLimit     mcpContextFailureCode = "memories_limit_exceeded"
	mcpContextSummaryRead       mcpContextFailureCode = "summary_read_failed"
	mcpContextToolsQuery        mcpContextFailureCode = "tool_results_query_failed"
	mcpContextToolsScan         mcpContextFailureCode = "tool_results_scan_failed"
	mcpContextToolsIteration    mcpContextFailureCode = "tool_results_iteration_failed"
	mcpContextToolsOutcome      mcpContextFailureCode = "tool_result_outcome_invalid"
	mcpContextToolsUTF8         mcpContextFailureCode = "tool_result_utf8_invalid"
	mcpContextToolsLimit        mcpContextFailureCode = "tool_results_limit_exceeded"
	mcpContextRefusalsQuery     mcpContextFailureCode = "human_refusals_query_failed"
	mcpContextRefusalsScan      mcpContextFailureCode = "human_refusals_scan_failed"
	mcpContextRefusalsIteration mcpContextFailureCode = "human_refusals_iteration_failed"
	mcpContextRefusalsInvalid   mcpContextFailureCode = "human_refusal_invalid"
	mcpContextRefusalsLimit     mcpContextFailureCode = "human_refusals_limit_exceeded"
	mcpContextEncoding          mcpContextFailureCode = "context_encoding_failed"
	mcpContextBytesLimit        mcpContextFailureCode = "context_bytes_limit_exceeded"
	mcpContextSnapshotEncoding  mcpContextFailureCode = "context_snapshot_encoding_failed"
	mcpContextDigestChanged     mcpContextFailureCode = "context_digest_changed"
	mcpContextUnclassified      mcpContextFailureCode = "unclassified_context_failure"

	mcpContextToolsBinding        mcpContextFailureCode = "tool_result_binding_changed"
	mcpContextToolsUncertain      mcpContextFailureCode = "tool_result_uncertain_effect"
	mcpContextGroupRound          mcpContextFailureCode = "group_round_binding_changed"
	mcpContextAccountUnavailable  mcpContextFailureCode = "authorization_account_unavailable"
	mcpContextTargetBinding       mcpContextFailureCode = "target_run_binding_changed"
	mcpContextAncestryUnavailable mcpContextFailureCode = "ancestry_unavailable"
	mcpContextAncestryCycle       mcpContextFailureCode = "ancestry_cycle"
	mcpContextAncestryEnded       mcpContextFailureCode = "ancestry_ended_or_uncertain"
	mcpContextAncestryImported    mcpContextFailureCode = "ancestry_import_boundary"
	mcpContextAncestryMessage     mcpContextFailureCode = "ancestry_message_unavailable"
	mcpContextMembership          mcpContextFailureCode = "membership_unavailable"
	mcpContextRetryInheritance    mcpContextFailureCode = "retry_inherits_authority"
	mcpContextAssignmentBinding   mcpContextFailureCode = "assignment_binding_changed"
	mcpContextReturnBinding       mcpContextFailureCode = "return_binding_changed"
	mcpContextDelegationRoot      mcpContextFailureCode = "delegation_root_unsupported"
	mcpContextScheduleRoot        mcpContextFailureCode = "schedule_root_unavailable"
	mcpContextScheduleRootAmbig   mcpContextFailureCode = "schedule_root_ambiguous"
	mcpContextOccurrenceAuth      mcpContextFailureCode = "occurrence_authorization_unavailable"
	mcpContextScheduleRevoked     mcpContextFailureCode = "schedule_revised_or_revoked"
	mcpContextOccurrenceChanged   mcpContextFailureCode = "occurrence_instruction_changed"
	mcpContextSourceChain         mcpContextFailureCode = "schedule_source_chain_changed"
	mcpContextSourceUnknown       mcpContextFailureCode = "schedule_source_unknown"
	mcpContextSourceProvenance    mcpContextFailureCode = "schedule_source_provenance_changed"
	mcpContextInstructionsInvalid mcpContextFailureCode = "instructions_invalid"
	mcpContextLaterUserLimit      mcpContextFailureCode = "later_user_messages_limit_exceeded"
	mcpContextRestrictionsBudget  mcpContextFailureCode = "restrictions_exceed_review_size"
	mcpContextResumeChanged       mcpContextFailureCode = "resume_context_changed"
)

// Evidence that changed while a review or approval was pending is not a
// standing gap: a retry reviews the current evidence afresh.
func mcpContextTransient(code mcpContextFailureCode) bool {
	return code == mcpContextDigestChanged || code == mcpContextResumeChanged
}

// One plain line for the model and the card: what failed, never content.
func mcpContextFailureReason(code mcpContextFailureCode) string {
	switch code {
	case mcpContextUserUnavailable, mcpContextIntentRead, mcpContextIntentInvalid:
		return "this task has no reviewable native user request behind it"
	case mcpContextIntentProvenance:
		return "the triggering message is not verified as typed by the user"
	case mcpContextNonText:
		return "the triggering message has attachments the reviewer cannot read"
	case mcpContextAttachmentScope:
		return "an attachment is bound outside this conversation's history"
	case mcpContextRefusalsInvalid:
		return "a recorded human refusal is unreadable"
	case mcpContextToolsOutcome, mcpContextToolsUTF8, mcpContextToolsBinding:
		return "a tool record of this task is corrupted or bound elsewhere"
	case mcpContextToolsUncertain:
		return "an earlier step of this scheduled task has an unverified effect"
	case mcpContextGroupRound:
		return "this group turn is not linked to its user message"
	case mcpContextAccountUnavailable:
		return "the account that authorized this task is unavailable"
	case mcpContextRunBinding, mcpContextTargetBinding:
		return "this task's run record changed during review"
	case mcpContextAncestryUnavailable, mcpContextAncestryCycle, mcpContextAncestryMessage:
		return "the chain of tasks that led here cannot be traced to its origin"
	case mcpContextAncestryEnded:
		return "an earlier task in this chain failed, stopped or has an unverified effect"
	case mcpContextAncestryImported:
		return "this task chain includes imported history"
	case mcpContextMembership:
		return "a bot in this task chain is no longer a member of its conversation"
	case mcpContextRetryInheritance:
		return "a retried task cannot reuse the earlier task's authority"
	case mcpContextAssignmentBinding, mcpContextReturnBinding:
		return "a hand-off between bots in this chain does not match its record"
	case mcpContextDelegationRoot:
		return "this delegated task does not start from a user request or schedule"
	case mcpContextScheduleRoot, mcpContextScheduleRootAmbig, mcpContextOccurrenceAuth:
		return "this scheduled run's occurrence record is unavailable"
	case mcpContextScheduleRevoked:
		return "the schedule was edited, paused or removed after this run started"
	case mcpContextOccurrenceChanged:
		return "the scheduled instruction no longer matches the schedule"
	case mcpContextSourceChain, mcpContextSourceProvenance:
		return "the schedule's original request record changed"
	case mcpContextSourceUnknown:
		return "the schedule has no recorded user request (re-create it to allow automatic review)"
	case mcpContextDigestChanged:
		return "the authorizing request or a task record changed during review"
	case mcpContextResumeChanged:
		return "the reviewed evidence changed before the approved call resumed"
	case mcpContextRefusalsLimit, mcpContextLaterUserLimit, mcpContextRestrictionsBudget:
		return "this conversation's human refusals and later messages exceed what one review can hold"
	case mcpContextBytesLimit:
		return "the required authorization evidence alone exceeds the review size"
	}
	return "the review could not read its required records"
}

type mcpContextError struct{ failure MCPContextFailure }

func (e *mcpContextError) Error() string {
	return "MCP review context unavailable: " + string(e.failure.Code)
}

func mcpContextFail(code mcpContextFailureCode) error {
	return &mcpContextError{failure: MCPContextFailure{Code: code}}
}

func mcpContextLimitFail(code mcpContextFailureCode, observed, limit, cap int, atLeast bool) error {
	if observed > cap {
		observed, atLeast = cap, true
	}
	return &mcpContextError{failure: MCPContextFailure{Code: code, Observed: &observed, Limit: &limit, ObservedAtLeast: atLeast}}
}

func mcpContextDiagnostic(err error) *MCPContextFailure {
	if err == nil {
		return nil
	}
	var failure *mcpContextError
	if errors.As(err, &failure) {
		copy := failure.failure
		return &copy
	}
	// Other readers (including scheduled ancestry) retain their checks. Do not
	// infer a cause or expose an arbitrary error string for an untyped failure.
	return &MCPContextFailure{Code: mcpContextUnclassified}
}
