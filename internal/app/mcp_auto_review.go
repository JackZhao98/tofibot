package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const autoReviewActor = "auto-review"
const autoReviewPolicyVersion = "mcp-all-external-v5"
const autoReviewProvenance = "host-mcp-review-binding-v5"
const autoReviewTimeout = 30 * time.Second
const autoReviewValidity = 2 * time.Minute

type MCPReviewDisplay struct {
	Source               string             `json:"source"`
	Status               string             `json:"status"`
	Reason               string             `json:"reason"`
	Model                string             `json:"model"`
	RiskLevel            string             `json:"risk_level,omitempty"`
	ConfirmationRequired bool               `json:"confirmation_required"`
	PolicyVersion        string             `json:"policy_version,omitempty"`
	ContextFailure       *MCPContextFailure `json:"context_failure,omitempty"`
	// FailureCategory names why an "unavailable" review produced no judgment
	// (one of the mcpReviewFailure* constants). Never carries content.
	FailureCategory string `json:"failure_category,omitempty"`
}

// Reviewer failure categories. They are stored on the review row, shown in the
// stored reason and logged once per failed request; none carries arguments,
// prompt or response content.
const (
	mcpReviewFailureNotConfigured = "reviewer_not_configured"
	mcpReviewFailureInputInvalid  = "input_invalid"
	mcpReviewFailureInputTooLarge = "input_too_large"
	mcpReviewFailureProvider      = "provider_error"
	mcpReviewFailureTimeout       = "timeout"
	mcpReviewFailureMalformed     = "malformed_response"
	mcpReviewFailureEmpty         = "empty_response"

	mcpReviewInputLimit = 100 << 10
)

// mcpReviewFailure is a technical gap of the reviewer request. Detail is a
// fixed token (validation rule, HTTP status class, byte count), never text
// from the prompt, the provider body or the response.
type mcpReviewFailure struct {
	Category string
	Detail   string
}

func (f *mcpReviewFailure) Error() string {
	if f.Detail == "" {
		return f.Category
	}
	return f.Category + ": " + f.Detail
}

// Only a malformed or empty answer earns one fresh request: the reviewer was
// reached and answered, so a second identical request may well succeed.
// Timeouts, provider errors and oversized input are not retried.
func (f *mcpReviewFailure) retryable() bool {
	return f.Category == mcpReviewFailureMalformed || f.Category == mcpReviewFailureEmpty
}

// mcpReviewFailureReason is the stored, user-visible reason for a failed
// review: the named category and its token, never upstream content.
func mcpReviewFailureReason(f *mcpReviewFailure) string {
	const tail = " No policy judgment or execution permission was established."
	var what string
	switch f.Category {
	case mcpReviewFailureNotConfigured:
		what = "no reviewer model is configured"
	case mcpReviewFailureInputInvalid:
		what = "the review packet could not be encoded"
	case mcpReviewFailureInputTooLarge:
		what = "the review packet exceeded the reviewer input limit"
	case mcpReviewFailureProvider:
		what = "the reviewer model request failed"
	case mcpReviewFailureTimeout:
		what = "the reviewer did not answer within the time budget"
	case mcpReviewFailureMalformed:
		what = "the reviewer answer failed validation, also after one retry"
	case mcpReviewFailureEmpty:
		what = "the reviewer returned no answer, also after one retry"
	default:
		what = "the reviewer was unavailable"
	}
	return "AutoReview failed (" + f.Error() + "): " + what + "." + tail
}

// mcpReviewParseError names the validation rule a reviewer answer failed.
type mcpReviewParseError struct{ Rule string }

func (e *mcpReviewParseError) Error() string { return "invalid reviewer response: " + e.Rule }

// Memories and summaries are deliberately absent: they are untrusted, change
// independently of authorization and only add size and digest volatility.
type mcpReviewContext struct {
	Intent             string                  `json:"user_intent"`
	IntentMessageID    string                  `json:"user_intent_message_id"`
	Instructions       string                  `json:"bot_instructions"`
	Messages           []Message               `json:"conversation_context"`
	MessageProvenance  []mcpMessageProvenance  `json:"host_message_provenance"`
	LaterUserMessages  []mcpRestrictionMessage `json:"later_user_messages_restrictions_only,omitempty"`
	HumanRefusals      []mcpHumanRefusal       `json:"host_human_mcp_refusals,omitempty"`
	ToolResults        []ToolActivity          `json:"untrusted_tool_results,omitempty"`
	SourceRunBinding   *mcpScheduleRunBinding  `json:"host_source_run_binding,omitempty"`
	SourceToolResults  []ToolActivity          `json:"untrusted_source_tool_results,omitempty"`
	ScheduleLineage    *mcpScheduleLineage     `json:"schedule_lineage,omitempty"`
	Delegation         *mcpDelegationLineage   `json:"host_delegation_lineage,omitempty"`
	AttachmentBoundary *mcpAttachmentBoundary  `json:"host_attachment_boundary,omitempty"`
	Bounds             *mcpEvidenceBounds      `json:"host_evidence_bounds,omitempty"`
}

// These restrictions originate only from native MCP approval rows and genuine
// human answers. They cannot grant consent or be supplied by remote metadata.
type mcpHumanRefusal struct {
	QuestionID string           `json:"question_id"`
	RunID      string           `json:"run_id"`
	ActionHash string           `json:"action_hash"`
	Approval   *ApprovalDetails `json:"exact_refused_proposal"`
}

type reviewQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func digestBytes(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func mcpReviewDigest(x mcpReviewContext, call extensions.MCPCallApproval) string {
	raw, err := json.Marshal(struct {
		Context                                            mcpReviewContext
		Server, Tool, Config, ArgumentsDigest, Description string
		Schema                                             json.RawMessage
	}{x, call.Server, call.Tool, call.ConfigVersion, digestBytes(call.Arguments), call.Description, call.Schema})
	if err != nil {
		return ""
	}
	return digestBytes(raw)
}

// A proposal is read-only for the scheduled effect fence when the owner
// configured the exact tool as trusted read-only (host policy) or the remote
// tools/list carried readOnlyHint for it (untrusted hint, captured on the
// proposal at dispatch). Neither exempts the call from review itself; an
// absent or false hint reads as an effect.
func (s *Server) mcpProposalFence(call extensions.MCPCallApproval) mcpProposalFence {
	if s.extensions != nil {
		if requiresHuman, current := s.extensions.MCPCallRequiresHuman(call); current && !requiresHuman {
			return mcpProposalReadOnly
		}
	}
	if call.ReadOnlyHint {
		return mcpProposalReadOnly
	}
	return mcpProposalEffect
}

func (s *Server) reviewAccountID() string {
	if s.accountID != "" {
		return s.accountID
	}
	return s.instance.ID
}

// Read the bounded evidence window for a direct or group chat run: the host
// verified trigger, conversation text up to it, later human restrictions,
// this run's own records and human refusals. Only the trigger's provenance
// establishes authorization; size never does. Recomputed inside the claim tx.
func readMCPReviewContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, string, error) {
	x, err := readMCPChatContext(db, c, r)
	if err != nil {
		return mcpReviewContext{}, "", err
	}
	digest, err := fitMCPReviewContext(&x, mcpEvidenceBudget)
	if err != nil {
		return x, "", err
	}
	return x, digest, nil
}

func readMCPChatContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, error) {
	var x mcpReviewContext
	var trigger, parent, kind sql.NullString
	if err := db.QueryRow(`SELECT trigger_message_id,parent_run_id,kind FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, r.ID, c.ID, r.BotID).Scan(&trigger, &parent, &kind); err != nil {
		return x, mcpContextFail(mcpContextRunRead)
	}
	if trigger.String != r.TriggerMessageID || parent.String != r.ParentRunID || kind.String != r.Kind {
		return x, mcpContextFail(mcpContextRunBinding)
	}
	if r.TriggerMessageID == "" || r.Kind != "" && r.Kind != "chat" && r.Kind != runKindGroupChat || r.ParentRunID != "" && r.Kind != runKindGroupChat {
		return x, mcpContextFail(mcpContextUserUnavailable)
	}
	if r.Kind == runKindGroupChat {
		if err := verifyMCPGroupRound(db, c, r); err != nil {
			return x, err
		}
	}
	var role, conv, intentSource string
	var intentSeq int64
	var intentKind, sender sql.NullString
	if err := db.QueryRow(`SELECT m.content,m.role,m.conversation_id,m.kind,m.sender_bot_id,m.seq,`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.id=?`, r.TriggerMessageID).Scan(&x.Intent, &role, &conv, &intentKind, &sender, &intentSeq, &intentSource); err != nil {
		return x, mcpContextFail(mcpContextIntentRead)
	}
	if role != "user" || conv != c.ID || sender.String != "" || intentKind.String != "" && intentKind.String != "user_message" || strings.TrimSpace(x.Intent) == "" || !utf8.ValidString(x.Intent) {
		return x, mcpContextFail(mcpContextIntentInvalid)
	}
	if intentSource != mcpHostUserIngress {
		return x, mcpContextFail(mcpContextIntentProvenance)
	}
	x.IntentMessageID = r.TriggerMessageID
	if err := db.QueryRow(`SELECT instructions FROM bots WHERE id=?`, r.BotID).Scan(&x.Instructions); err != nil {
		return x, mcpContextFail(mcpContextInstructionsRead)
	}
	var err error
	if x.AttachmentBoundary, err = readMCPAttachmentBoundary(db, c.ID, r.TriggerMessageID, intentSeq); err != nil {
		return x, err
	}
	bounds := &mcpEvidenceBounds{}
	boundMCPIntent(&x, bounds)
	if x.Messages, x.MessageProvenance, err = readMCPMessageWindow(db, c.ID, intentSeq, false, bounds); err != nil {
		return x, err
	}
	if x.LaterUserMessages, err = readMCPLaterUserMessages(db, c.ID, intentSeq); err != nil {
		return x, err
	}
	if x.ToolResults, err = readMCPRunToolEvidence(db, r, false, bounds); err != nil {
		return x, err
	}
	if x.HumanRefusals, err = readMCPHumanRefusals(db, c.ID, bounds); err != nil {
		return x, err
	}
	x.Bounds = bounds.orNil()
	return x, nil
}

// An invited group member shares its round's user trigger. Every hop must be
// a group turn in the same conversation with that exact trigger.
func verifyMCPGroupRound(db reviewQuerier, c Conversation, r Run) error {
	var kind string
	if err := db.QueryRow(`SELECT kind FROM conversations WHERE id=?`, c.ID).Scan(&kind); err != nil || kind != "group" {
		return mcpContextFail(mcpContextGroupRound)
	}
	seen := map[string]bool{r.ID: true}
	for id := r.ParentRunID; id != ""; {
		if seen[id] || len(seen) > maxMCPAuthorizationAncestry {
			return mcpContextFail(mcpContextGroupRound)
		}
		seen[id] = true
		p, err := scanRun(db.QueryRow(mcpScheduleRunSQL, id))
		if err != nil || p.Kind != runKindGroupChat || p.ConversationID != c.ID || p.TriggerMessageID != r.TriggerMessageID {
			return mcpContextFail(mcpContextGroupRound)
		}
		id = p.ParentRunID
	}
	return nil
}

type mcpReviewResult struct {
	Decision, Reason, ContextDigest, RiskLevel string
	ConfirmationRequired                       bool
}

// This predicate is shared by completion and atomic claim. Reviewer assessment
// is advice; backend bindings, denials and action claims remain authoritative.
func mcpReviewDisposition(x mcpReviewResult) string {
	if x.Decision != "allow" && x.Decision != "deny" && x.Decision != "needs_human" && x.Decision != "context_gap" || x.Decision == "allow" && x.RiskLevel == "unknown" || x.Decision == "needs_human" && !x.ConfirmationRequired || x.Decision == "context_gap" && (x.RiskLevel != "unknown" || x.ConfirmationRequired) || x.Decision == "deny" && x.ConfirmationRequired {
		return "unavailable"
	}
	if x.Decision == "deny" {
		return "policy_denied"
	}
	if x.Decision == "context_gap" || x.RiskLevel == "unknown" {
		return "context_required"
	}
	if x.RiskLevel != "low" && x.RiskLevel != "medium" && x.RiskLevel != "high" {
		return "unavailable"
	}
	if x.RiskLevel == "high" || x.ConfirmationRequired || x.Decision == "needs_human" {
		return "human_required"
	}
	if x.Decision == "allow" {
		return "approved"
	}
	return "unavailable"
}

// stripReviewCodeFence removes one Markdown code fence (optionally tagged
// json) that wraps the whole answer. This is the only tolerated deviation:
// the fence carries no meaning, and the inner object still faces every
// schema, type, digest and contradiction check unchanged. Prose before or
// after the fence is not tolerated.
func stripReviewCodeFence(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) < 6 || !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return text, false
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(trimmed, "```"), "```")
	if rest := strings.TrimPrefix(strings.TrimPrefix(inner, "json"), "JSON"); rest != inner && (rest == "" || rest[0] == '\n' || rest[0] == '\r' || rest[0] == ' ') {
		inner = rest
	}
	return strings.TrimSpace(inner), true
}

// Strict v5: all five fields, correct types, no duplicates/unknown fields/tools.
// Missing context cannot be repaired by confirmation. A denial is terminal.
// Every rejection names its rule so the failure can be recorded without the
// response text; fenced reports whether a code fence was stripped.
func parseMCPReview(resp *provider.ChatResponse, expected string) (out mcpReviewResult, fenced bool, err error) {
	rule := func(name string) (mcpReviewResult, bool, error) {
		return mcpReviewResult{}, fenced, &mcpReviewParseError{Rule: name}
	}
	if resp == nil {
		return rule("nil_response")
	}
	if len(resp.ToolCalls) > 0 {
		return rule("tool_call")
	}
	if strings.TrimSpace(resp.Content) == "" {
		if resp.Reasoning != "" {
			return rule("no_text_with_reasoning")
		}
		return rule("no_text")
	}
	if len(resp.Content) > 4096 {
		return rule("too_long")
	}
	if !utf8.ValidString(resp.Content) {
		return rule("invalid_utf8")
	}
	content, fenced := stripReviewCodeFence(resp.Content)
	d := json.NewDecoder(strings.NewReader(content))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return rule("not_json")
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return rule("not_json")
		}
		if seen[key] {
			return rule("duplicate_key")
		}
		seen[key] = true
		switch key {
		case "confirmation_required":
			var value any
			if d.Decode(&value) != nil {
				return rule("not_json")
			}
			flag, ok := value.(bool)
			if !ok {
				return rule("invalid_field_type")
			}
			out.ConfirmationRequired = flag
		case "decision", "reason", "context_digest", "risk_level":
			var v string
			if d.Decode(&v) != nil {
				return rule("invalid_field_type")
			}
			values[key] = v
		default:
			return rule("unknown_field")
		}
	}
	if _, err = d.Token(); err != nil {
		return rule("not_json")
	}
	if d.Decode(new(any)) != io.EOF {
		return rule("trailing_text")
	}
	if len(seen) != 5 {
		return rule("missing_field")
	}
	out.Decision, out.Reason, out.ContextDigest, out.RiskLevel = values["decision"], values["reason"], values["context_digest"], values["risk_level"]
	if out.Decision != "allow" && out.Decision != "deny" && out.Decision != "needs_human" && out.Decision != "context_gap" {
		return rule("invalid_decision")
	}
	if strings.TrimSpace(out.Reason) == "" {
		return rule("empty_reason")
	}
	if len(out.Reason) > 600 {
		return rule("reason_too_long")
	}
	if out.ContextDigest != expected {
		return rule("digest_mismatch")
	}
	if out.RiskLevel != "low" && out.RiskLevel != "medium" && out.RiskLevel != "high" && out.RiskLevel != "unknown" {
		return rule("invalid_risk_level")
	}
	if out.Decision == "allow" && out.RiskLevel == "unknown" || out.Decision == "needs_human" && !out.ConfirmationRequired || out.Decision == "context_gap" && (out.RiskLevel != "unknown" || out.ConfirmationRequired) || out.Decision == "deny" && out.ConfirmationRequired {
		return rule("contradictory")
	}
	return out, fenced, nil
}

// classifyMCPReviewFailure maps one reviewer request outcome to a failure
// category. The detail is an error class, HTTP status or validation rule;
// provider bodies and response text never reach it.
func classifyMCPReviewFailure(ctx context.Context, resp *provider.ChatResponse, err error, digest string) (mcpReviewResult, bool, *mcpReviewFailure) {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		detail := "deadline_exceeded"
		if errors.Is(ctx.Err(), context.Canceled) || ctx.Err() == nil && errors.Is(err, context.Canceled) {
			detail = "context_canceled"
		}
		return mcpReviewResult{}, false, &mcpReviewFailure{mcpReviewFailureTimeout, detail}
	}
	if err != nil {
		detail := "request_failed"
		var incomplete *provider.IncompleteResponseError
		var refusal *provider.RefusalError
		var idle *provider.StreamIdleError
		var wall *provider.StreamWallCapError
		if api, ok := provider.AsAPIError(err); ok {
			detail = "http_" + strconv.Itoa(api.StatusCode)
		} else if errors.As(err, &incomplete) {
			detail = "incomplete_" + strings.Map(func(r rune) rune {
				if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' {
					return r
				}
				return '_'
			}, strings.ToLower(incomplete.Reason))
		} else if errors.As(err, &refusal) {
			detail = "refusal"
		} else if errors.As(err, &idle) {
			detail = "stream_idle"
		} else if errors.As(err, &wall) {
			detail = "stream_wall_cap"
		} else if errors.Is(err, provider.ErrStreamIncomplete) {
			detail = "stream_incomplete"
		}
		return mcpReviewResult{}, false, &mcpReviewFailure{mcpReviewFailureProvider, detail}
	}
	result, fenced, parseErr := parseMCPReview(resp, digest)
	if parseErr == nil {
		return result, fenced, nil
	}
	var rule *mcpReviewParseError
	if !errors.As(parseErr, &rule) {
		return mcpReviewResult{}, fenced, &mcpReviewFailure{mcpReviewFailureMalformed, "unknown"}
	}
	switch rule.Rule {
	case "nil_response", "no_text", "no_text_with_reasoning":
		return mcpReviewResult{}, fenced, &mcpReviewFailure{mcpReviewFailureEmpty, rule.Rule}
	}
	return mcpReviewResult{}, fenced, &mcpReviewFailure{mcpReviewFailureMalformed, rule.Rule}
}

// One failure line per failed reviewer request. It names the run, tool,
// category, model and latency; arguments, prompt and response text never
// appear here.
func logMCPReviewFailure(r Run, call extensions.MCPCallApproval, model string, attempt int, latency time.Duration, f *mcpReviewFailure) {
	log.Printf("[auto-review] reviewer failure run=%s server=%s tool=%s category=%s detail=%s model=%s attempt=%d latency_ms=%d", r.ID, call.Server, call.Tool, f.Category, f.Detail, model, attempt, latency.Milliseconds())
}

// requestMCPReview makes the reviewer request for one reserved review. Every
// attempt is a fresh request with the identical bound input and digest; a
// malformed or empty answer earns exactly one more attempt inside the same
// autoReviewTimeout budget. The reservation row is untouched here, so a retry
// is never a second review. Reviewer usage is attributed to the run.
func (s *Server) requestMCPReview(ctx context.Context, r Run, call extensions.MCPCallApproval, x mcpReviewContext, digest string) (mcpReviewResult, *mcpReviewFailure) {
	ctx, cancel := context.WithTimeout(ctx, autoReviewTimeout)
	defer cancel()
	p, model := s.autoReviewProvider, codexReviewModel
	if p == nil {
		var err error
		p, model, err = s.backgroundProvider(ctx, backgroundReview) // No retry/fallback wrapper.
		if err != nil {
			f := &mcpReviewFailure{mcpReviewFailureNotConfigured, ""}
			logMCPReviewFailure(r, call, s.backgroundModel(backgroundReview), 0, 0, f)
			return mcpReviewResult{}, f
		}
	}
	raw, err := mcpReviewInput(call, x, digest)
	if err != nil {
		f := &mcpReviewFailure{mcpReviewFailureInputInvalid, "encoding"}
		logMCPReviewFailure(r, call, model, 0, 0, f)
		return mcpReviewResult{}, f
	}
	if len(raw) > mcpReviewInputLimit {
		f := &mcpReviewFailure{mcpReviewFailureInputTooLarge, strconv.Itoa(len(raw)) + "_bytes_limit_" + strconv.Itoa(mcpReviewInputLimit)}
		logMCPReviewFailure(r, call, model, 0, 0, f)
		return mcpReviewResult{}, f
	}
	var failure *mcpReviewFailure
	for attempt := 1; attempt <= 2; attempt++ {
		started := time.Now()
		resp, err := p.Chat(ctx, &provider.ChatRequest{Model: model, System: mcpAutoReviewPrompt, Messages: []provider.Message{{Role: "user", Content: string(raw)}}, Tools: nil})
		if resp != nil {
			if e := s.store.recordAuxiliaryModelUsage(r.ID, model, resp.Usage.InputTokens, resp.Usage.OutputTokens); e != nil {
				log.Printf("[usage] reviewer usage for run %s: %v", r.ID, e)
			}
		}
		var result mcpReviewResult
		var fenced bool
		result, fenced, failure = classifyMCPReviewFailure(ctx, resp, err, digest)
		if failure == nil {
			if fenced {
				log.Printf("[auto-review] reviewer answer tolerated run=%s server=%s tool=%s note=code_fence model=%s attempt=%d latency_ms=%d", r.ID, call.Server, call.Tool, model, attempt, time.Since(started).Milliseconds())
			}
			return result, nil
		}
		logMCPReviewFailure(r, call, model, attempt, time.Since(started), failure)
		if !failure.retryable() || ctx.Err() != nil {
			break
		}
	}
	return mcpReviewResult{}, failure
}

// Begin once, before the provider request. Resumes/restarts cannot issue it
// again: the reservation row is inserted once, and the only repeat request is
// the single in-process retry inside requestMCPReview for a malformed or
// empty answer, under the same reservation, budget, input and digest. A
// native human answer never substitutes for the v5 risk assessment.
func (s *Server) reviewNewMCPProposal(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, q Question) error {
	settings, err := s.store.getAutoReviewSettings()
	if err != nil {
		return err
	}
	if settings.Mode == "off" {
		return nil
	}
	return s.reviewMCPProposal(ctx, c, r, call, q, settings)
}

func (s *Server) reviewMCPProposal(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, q Question, settings autoReviewSettings) error {
	shadow := settings.Mode == "shadow"
	gap := func(status, reason string, failure ...*MCPContextFailure) error {
		var diagnostic *MCPContextFailure
		if len(failure) != 0 {
			diagnostic = failure[0]
		}
		if shadow {
			return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "shadow_" + status, reason, "codex-auto-review", "", false, autoReviewPolicyVersion, diagnostic, ""})
		}
		return s.closeMCPReviewGap(q.ID, status, reason, diagnostic)
	}
	current, err := s.store.GetQuestion(q.ID)
	if err != nil {
		return err
	}
	q = current
	if terminalErr := mcpReviewTerminal(ctx, s.store.db, q, r, call); !shadow && terminalErr != nil {
		if _, ok := tooloutcome.FromError(terminalErr); !ok {
			return terminalErr
		}
		if err := s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "terminal", terminalErr.Error(), "codex-auto-review", "", false, autoReviewPolicyVersion, nil, ""}); err != nil {
			return err
		}
		return terminalErr
	}
	// A human denial before review remains final without spending a request.
	// A human approval cannot bypass the v5 risk and context assessment.
	if !shadow && q.Status == questionAnswered && strings.TrimSpace(string(q.Answer)) == "false" {
		return nil
	}
	// A human refusal of this exact action anywhere in the conversation or its
	// chain is final; it is enforced here, not left to the bounded packet.
	if !shadow {
		refused, err := mcpHumanRefusedAction(s.store.db, c, r, mcpApprovalHash(call))
		if err != nil {
			return err
		}
		if refused {
			return gap("policy_denied", "A person already refused this exact action in this conversation. It cannot be proposed again.")
		}
	}
	if call.Server == "" || call.Tool == "" || call.ConfigVersion == "" || !mcpSchemaAvailable(call.Schema) || s.reviewAccountID() == "" || s.extensions == nil || !s.extensions.MCPCallCurrent(call) {
		return gap("setup_required", "Current tool configuration, schema or connection binding is unavailable. Approval cannot repair this setup gap.")
	}
	x, digest, contextErr := s.readMCPReviewContextFor(s.store.db, c, r, s.mcpProposalFence(call))
	digest = mcpReviewDigest(x, call)
	if contextErr != nil {
		diagnostic := mcpContextDiagnostic(contextErr)
		return gap("context_required", "Necessary durable user authorization is missing or unverifiable: "+mcpContextFailureReason(diagnostic.Code)+".", diagnostic)
	}
	if latest, e := s.store.getAutoReviewSettings(); e != nil || latest != settings {
		return gap("invalidated", "AutoReview settings changed before this review could begin.")
	}
	snapshot, err := json.Marshal(x)
	if err != nil {
		return gap("context_required", "The exact durable evidence snapshot could not be retained.", mcpContextDiagnostic(mcpContextFail(mcpContextSnapshotEncoding)))
	}
	if err := s.retireTransientMCPReview(r, call, q.ID); err != nil {
		return err
	}
	_, err = s.store.db.Exec(`INSERT INTO mcp_auto_reviews(question_id,account_id,conversation_id,run_id,action_hash,server,tool,config_fingerprint,arguments_digest,schema_digest,policy_version,context_digest,context_snapshot,provenance,mode,settings_revision,status,decision,reason,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'reviewing','','',?)`, q.ID, s.reviewAccountID(), c.ID, r.ID, mcpApprovalHash(call), call.Server, call.Tool, call.ConfigVersion, digestBytes(call.Arguments), digestBytes(call.Schema), autoReviewPolicyVersion, digest, string(snapshot), autoReviewProvenance, settings.Mode, settings.Revision, q.ExpiresAt)
	if err != nil {
		var reserved bool
		if e := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_auto_reviews WHERE run_id=? AND action_hash=?)`, r.ID, mcpApprovalHash(call)).Scan(&reserved); e != nil || !reserved {
			return gap("unavailable", "The exact review could not be reserved. No policy judgment or automatic execution permission was established.")
		}
		// A duplicate cannot reserve or replay another reviewer request. Claim
		// must still validate the exact card's review and action-level fences.
		return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "not_reviewed", "No new review was reserved for this exact proposal. Existing action-level review and execution claims remain authoritative.", "codex-auto-review", "", false, autoReviewPolicyVersion, nil, ""})
	}
	displayStatus := "reviewing"
	if shadow {
		displayStatus = "shadow_reviewing"
	}
	if err = s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, displayStatus, "Assessing the exact proposal's authorization, effects and data flow.", "codex-auto-review", "", false, autoReviewPolicyVersion, nil, ""}); err != nil {
		return err
	}
	request := func(reviewCtx context.Context) error {
		result, failure := s.requestMCPReview(reviewCtx, r, call, x, digest)
		if failure != nil {
			result = mcpReviewResult{Reason: mcpReviewFailureReason(failure), ContextDigest: digest, RiskLevel: "unknown"}
		}
		return s.finishMCPReview(reviewCtx, c, r, call, q.ID, settings, result, failure)
	}
	if shadow {
		s.startShadowMCPReview(func(reviewCtx context.Context) { _ = request(reviewCtx) })
		return nil
	}
	return request(ctx)
}

// The review reservation is unique per run and exact action. A predecessor
// closed only because its evidence moved releases its reservation, keeping
// its row under a retired key, so a retried card can be reviewed afresh.
func (s *Server) retireTransientMCPReview(r Run, call extensions.MCPCallApproval, current string) error {
	hash := mcpApprovalHash(call)
	var prior string
	err := s.store.db.QueryRow(`SELECT question_id FROM mcp_auto_reviews WHERE run_id=? AND action_hash=?`, r.ID, hash).Scan(&prior)
	if errors.Is(err, sql.ErrNoRows) || err == nil && prior == current {
		return nil
	}
	if err != nil {
		return err
	}
	q, err := s.store.GetQuestion(prior)
	if err != nil || !mcpReviewRetryable(q) {
		return err
	}
	_, err = s.store.db.Exec(`UPDATE mcp_auto_reviews SET action_hash=action_hash||':retired:'||question_id WHERE question_id=? AND run_id=? AND action_hash=? AND status='context_required' AND NOT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE question_id=? AND claimed_at<>'')`, prior, r.ID, hash, prior)
	return err
}

// mcpReviewFailureColumns is the stored (category, detail) pair; both empty
// when the reviewer answered.
func mcpReviewFailureColumns(f *mcpReviewFailure) (string, string) {
	if f == nil {
		return "", ""
	}
	return f.Category, f.Detail
}

func (s *Server) closeMCPReviewGap(id, status, reason string, diagnostic *MCPContextFailure) error {
	if err := s.store.setMCPReviewDisplay(id, MCPReviewDisplay{autoReviewActor, status, reason, "codex-auto-review", "", false, autoReviewPolicyVersion, diagnostic, ""}); err != nil {
		return err
	}
	// Keep a genuine human answer truthful; it cannot repair a technical gap.
	q, err := s.store.GetQuestion(id)
	if err != nil {
		return err
	}
	return mcpReviewDisplayBlock(q)
}

func (s *Store) setMCPReviewDisplay(id string, display MCPReviewDisplay) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return err
	}
	if q.Approval == nil {
		return errors.New("missing proposal")
	}
	q.Approval.Review = &display
	if q.Status == questionPending && mcpReviewClosesProposal(display.Status) {
		q.Status = questionCancelled
		if display.Status == "terminal" {
			deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt)
			if e != nil || !time.Now().Before(deadline) {
				q.Status = questionExpired
			}
		}
	}
	q.UpdatedAt = now()
	raw, _ := json.Marshal(q.Approval)
	if _, err = tx.Exec(`UPDATE questions SET status=?,approval_json=?,updated_at=? WHERE id=?`, q.Status, string(raw), q.UpdatedAt, id); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), q.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) finishMCPReview(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, id string, initial autoReviewSettings, result mcpReviewResult, failure *mcpReviewFailure) error {
	if initial.Mode == "shadow" {
		return s.finishShadowMCPReview(c, r, call, id, initial, result, failure)
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return err
	}
	settings, err := readAutoReviewSettings(tx)
	if err != nil {
		return err
	}
	x, _, contextErr := s.readMCPReviewContextFor(tx, c, r, s.mcpProposalFence(call))
	currentDigest := mcpReviewDigest(x, call)
	status := "human_required"
	var reviewStatus string
	if err = tx.QueryRow(`SELECT status FROM mcp_auto_reviews WHERE question_id=?`, id).Scan(&reviewStatus); err != nil {
		return err
	}
	var blocked error
	var contextFailure *MCPContextFailure
	terminalErr := mcpReviewTerminal(ctx, tx, q, r, call)
	if terminalErr != nil {
		if _, ok := tooloutcome.FromError(terminalErr); !ok {
			return terminalErr
		}
		status, blocked = "terminal", terminalErr
		result.Reason = terminalErr.Error()
	} else if q.Status == questionAnswered && strings.TrimSpace(string(q.Answer)) == "false" {
		status = "human_decided"
	} else if s.extensions == nil || !mcpSchemaAvailable(call.Schema) || !s.extensions.MCPCallCurrent(call) {
		status, result.Reason = "setup_required", "The current tool configuration or schema binding changed."
	} else if contextErr != nil || currentDigest != result.ContextDigest {
		contextFailure = mcpContextDiagnostic(contextErr)
		if contextErr == nil {
			contextFailure = mcpContextDiagnostic(mcpContextFail(mcpContextDigestChanged))
		}
		status, result.Reason = "context_required", "Necessary authorization changed during review: "+mcpContextFailureReason(contextFailure.Code)+"."
	} else if initial != settings || reviewStatus != "reviewing" {
		status, result.Reason = "invalidated", "The review was invalidated by changed settings or state; it cannot authorize execution."
	} else {
		status = mcpReviewDisposition(result)
		if (status == "approved" || status == "human_required") && q.Status == questionAnswered && q.AnsweredBy != autoReviewActor {
			status = "human_decided"
		}
		if status == "approved" && q.Status == questionPending {
			deadline, _ := time.Parse(time.RFC3339Nano, q.ExpiresAt)
			q.Status, q.Answer, q.AnsweredBy = questionAnswered, json.RawMessage("true"), autoReviewActor
			short := time.Now().UTC().Add(autoReviewValidity)
			if short.Before(deadline) {
				q.ExpiresAt = short.Format(time.RFC3339Nano)
			}
		}
	}

	if q.Status == questionPending && mcpReviewClosesProposal(status) {
		q.Status = questionCancelled
		if outcome, ok := tooloutcome.FromError(blocked); ok && outcome.Status == tooloutcome.Expired {
			q.Status = questionExpired
		}
	}
	q.UpdatedAt = now()
	failureCategory, failureDetail := mcpReviewFailureColumns(failure)
	display := &MCPReviewDisplay{autoReviewActor, status, result.Reason, "codex-auto-review", result.RiskLevel, result.ConfirmationRequired, autoReviewPolicyVersion, contextFailure, ""}
	if status == "unavailable" {
		display.FailureCategory = failureCategory
	}
	q.Approval.Review = display
	raw, _ := json.Marshal(q.Approval)
	if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status=?,decision=?,reason=?,risk_level=?,confirmation_required=?,expires_at=?,failure_category=?,failure_detail=? WHERE question_id=?`, status, result.Decision, result.Reason, result.RiskLevel, boolInt(result.ConfirmationRequired), q.ExpiresAt, failureCategory, failureDetail, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE questions SET status=?,answer_json=?,answered_by=?,expires_at=?,approval_json=?,updated_at=? WHERE id=?`, q.Status, nullString(string(q.Answer)), nullString(q.AnsweredBy), q.ExpiresAt, string(raw), q.UpdatedAt, id); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if blocked != nil {
		return blocked
	}
	return mcpReviewDisplayBlock(q)
}
