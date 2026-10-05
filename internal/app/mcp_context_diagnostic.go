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
)

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
