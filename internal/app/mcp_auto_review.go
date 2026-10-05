package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
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
	Source               string `json:"source"`
	Status               string `json:"status"`
	Reason               string `json:"reason"`
	Model                string `json:"model"`
	RiskLevel            string `json:"risk_level,omitempty"`
	ConfirmationRequired bool   `json:"confirmation_required"`
	PolicyVersion        string `json:"policy_version,omitempty"`
}

type mcpReviewContext struct {
	Intent            string                 `json:"user_intent"`
	IntentMessageID   string                 `json:"user_intent_message_id"`
	Instructions      string                 `json:"bot_instructions"`
	Messages          []Message              `json:"conversation_context"`
	MessageProvenance []mcpMessageProvenance `json:"host_message_provenance"`
	Memories          []Memory               `json:"memories"`
	Summary           string                 `json:"conversation_summary"`
	HumanRefusals     []mcpHumanRefusal      `json:"host_human_mcp_refusals,omitempty"`
	ToolResults       []ToolActivity         `json:"untrusted_tool_results,omitempty"`
	SummaryVersion    int64                  `json:"summary_version"`
	SourceRunBinding  *mcpScheduleRunBinding `json:"host_source_run_binding,omitempty"`
	SourceToolResults []ToolActivity         `json:"untrusted_source_tool_results,omitempty"`
	ScheduleLineage   *mcpScheduleLineage    `json:"schedule_lineage,omitempty"`
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

func (s *Server) reviewAccountID() string {
	if s.accountID != "" {
		return s.accountID
	}
	return s.instance.ID
}

// Read the complete bounded durable context, never a truncated summary. The
// host provenance must establish the necessary authorization. Unsupported
// non-text context is explicit; tool names never determine completeness.
// This snapshot is recomputed inside the atomic claim transaction.
func readMCPReviewContext(db reviewQuerier, c Conversation, r Run) (mcpReviewContext, string, error) {
	var x mcpReviewContext
	var trigger, parent, kind sql.NullString
	if err := db.QueryRow(`SELECT trigger_message_id,parent_run_id,kind FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, r.ID, c.ID, r.BotID).Scan(&trigger, &parent, &kind); err != nil || trigger.String != r.TriggerMessageID || parent.String != r.ParentRunID || kind.String != r.Kind {
		return x, "", errors.New("run context binding changed")
	}
	if r.TriggerMessageID == "" || r.ParentRunID != "" || r.Kind != "" && r.Kind != "chat" {
		return x, "", errors.New("complete user context unavailable")
	}
	var role, conv, intentSource string
	var intentKind, sender sql.NullString
	if err := db.QueryRow(`SELECT m.content,m.role,m.conversation_id,m.kind,m.sender_bot_id,`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.id=?`, r.TriggerMessageID).Scan(&x.Intent, &role, &conv, &intentKind, &sender, &intentSource); err != nil || role != "user" || conv != c.ID || sender.String != "" || intentKind.String != "" && intentKind.String != "user_message" || strings.TrimSpace(x.Intent) == "" {
		return x, "", errors.New("user intent unavailable")
	}
	if intentSource != mcpHostUserIngress {
		return x, "", errors.New("host-verified user intent provenance unavailable")
	}
	x.IntentMessageID = r.TriggerMessageID
	if err := db.QueryRow(`SELECT instructions FROM bots WHERE id=?`, r.BotID).Scan(&x.Instructions); err != nil {
		return x, "", err
	}
	var attachments int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE conversation_id=?`, c.ID).Scan(&attachments); err != nil || attachments != 0 {
		return x, "", errors.New("non-text context requires human review")
	}
	rows, err := db.Query(`SELECT m.id,m.seq,m.role,m.kind,m.content,COALESCE(m.sender_bot_id,''),`+mcpMessageProvenanceSQL+` FROM messages m WHERE m.conversation_id=? ORDER BY m.seq LIMIT 201`, c.ID)
	if err != nil {
		return x, "", err
	}
	for rows.Next() {
		var m Message
		var source string
		var kind sql.NullString
		if err = rows.Scan(&m.ID, &m.Seq, &m.Role, &kind, &m.Content, &m.SenderBotID, &source); err != nil {
			break
		}
		m.Kind = kind.String
		x.Messages = append(x.Messages, m)
		x.MessageProvenance = append(x.MessageProvenance, mcpMessageProvenance{m.ID, source})
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.Messages) > 200 {
		return x, "", errors.New("complete conversation exceeds review limit")
	}
	rows, err = db.Query(`SELECT id,content,revision FROM memories WHERE conversation_id=? AND (bot_id IS NULL OR bot_id=?) ORDER BY id LIMIT 201`, c.ID, r.BotID)
	if err != nil {
		return x, "", err
	}
	for rows.Next() {
		var m Memory
		if err = rows.Scan(&m.ID, &m.Content, &m.Revision); err != nil {
			break
		}
		x.Memories = append(x.Memories, m)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.Memories) > 200 {
		return x, "", errors.New("complete memory unavailable")
	}
	err = db.QueryRow(`SELECT version,content FROM summaries WHERE conversation_id=? ORDER BY version DESC LIMIT 1`, c.ID).Scan(&x.SummaryVersion, &x.Summary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return x, "", errors.New("complete summary unavailable")
	}
	rows, err = db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE conversation_id=? ORDER BY started_at,run_id,call_id LIMIT 201`, c.ID)
	if err != nil {
		return x, "", errors.New("complete tool-result context unavailable")
	}
	for rows.Next() {
		var a ToolActivity
		var truncated int
		var outcome string
		if err = rows.Scan(&a.ConversationID, &a.BotID, &a.RunID, &a.CallID, &a.Name, &a.Arguments, &a.Result, &a.Status, &truncated, &a.StartedAt, &a.UpdatedAt, &outcome); err != nil {
			break
		}
		a.Truncated, a.Outcome = truncated != 0, tooloutcome.Parse(outcome)
		if outcome != "" && a.Outcome == nil || !utf8.ValidString(a.Arguments) || !utf8.ValidString(a.Result) {
			err = errors.New("complete tool-result or effect context unavailable")
			break
		}
		// Preserve incompleteness and uncertainty as untrusted evidence. Only
		// facts needed by this proposal are context gaps; exact replay claims
		// remain the backend fence for an already dispatched uncertain effect.
		x.ToolResults = append(x.ToolResults, a)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.ToolResults) > 200 {
		return x, "", errors.New("complete tool-result context unavailable or exceeds limit")
	}
	rows, err = db.Query(`SELECT q.id,q.run_id,a.action_hash,q.approval_json FROM questions q JOIN mcp_call_approvals a ON a.question_id=q.id WHERE q.conversation_id=? AND q.status='answered' AND q.answer_json='false' AND COALESCE(q.answered_by,'')<>'' AND q.answered_by<>? ORDER BY q.created_at,q.id LIMIT 201`, c.ID, autoReviewActor)
	if err != nil {
		return x, "", err
	}
	for rows.Next() {
		var refusal mcpHumanRefusal
		var raw string
		if err = rows.Scan(&refusal.QuestionID, &refusal.RunID, &refusal.ActionHash, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &refusal.Approval); err != nil || refusal.Approval == nil {
			err = errors.New("human refusal context unavailable")
			break
		}
		refusal.Approval.Review = nil // Prior model advice is not human provenance.
		x.HumanRefusals = append(x.HumanRefusals, refusal)
	}
	rowErr = rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || len(x.HumanRefusals) > 200 {
		return x, "", errors.New("complete human refusal context unavailable")
	}
	raw, err := json.Marshal(x)
	if err != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return x, "", errors.New("complete context exceeds review limit")
	}
	return x, digestBytes(raw), nil
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

// Strict v5: all five fields, correct types, no duplicates/unknown fields/tools.
// Missing context cannot be repaired by confirmation. A denial is terminal.
func parseMCPReview(resp *provider.ChatResponse, expected string) (mcpReviewResult, error) {
	var out mcpReviewResult
	if resp == nil || len(resp.ToolCalls) > 0 || len(resp.Content) == 0 || len(resp.Content) > 4096 || !utf8.ValidString(resp.Content) {
		return out, errors.New("invalid response")
	}
	d := json.NewDecoder(strings.NewReader(resp.Content))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return out, errors.New("invalid response")
	}
	values := map[string]string{}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return out, errors.New("invalid or duplicate response key")
		}
		seen[key] = true
		switch key {
		case "confirmation_required":
			var value any
			if d.Decode(&value) != nil {
				return out, errors.New("invalid confirmation field")
			}
			flag, ok := value.(bool)
			if !ok {
				return out, errors.New("invalid confirmation field")
			}
			out.ConfirmationRequired = flag
		case "decision", "reason", "context_digest", "risk_level":
			var v string
			if d.Decode(&v) != nil {
				return out, errors.New("invalid response field")
			}
			values[key] = v
		default:
			return out, errors.New("unknown response field")
		}
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF || len(seen) != 5 {
		return out, errors.New("incomplete response")
	}
	out.Decision, out.Reason, out.ContextDigest, out.RiskLevel = values["decision"], values["reason"], values["context_digest"], values["risk_level"]
	if out.Decision != "allow" && out.Decision != "deny" && out.Decision != "needs_human" && out.Decision != "context_gap" || strings.TrimSpace(out.Reason) == "" || len(out.Reason) > 600 || out.ContextDigest != expected || out.RiskLevel != "low" && out.RiskLevel != "medium" && out.RiskLevel != "high" && out.RiskLevel != "unknown" {
		return mcpReviewResult{}, errors.New("invalid response")
	}
	if out.Decision == "allow" && out.RiskLevel == "unknown" || out.Decision == "needs_human" && !out.ConfirmationRequired || out.Decision == "context_gap" && (out.RiskLevel != "unknown" || out.ConfirmationRequired) || out.Decision == "deny" && out.ConfirmationRequired {
		return mcpReviewResult{}, errors.New("contradictory response")
	}
	return out, nil
}

func (s *Server) requestMCPReview(ctx context.Context, call extensions.MCPCallApproval, x mcpReviewContext, digest string) (mcpReviewResult, error) {
	ctx, cancel := context.WithTimeout(ctx, autoReviewTimeout)
	defer cancel()
	p := s.autoReviewProvider
	if p == nil {
		if s.provider != "openai_codex" || s.codex == nil {
			return mcpReviewResult{}, errors.New("reviewer unavailable")
		}
		credential, err := s.codex.Credential(ctx)
		if err != nil {
			return mcpReviewResult{}, errors.New("reviewer unavailable")
		}
		p, err = provider.New("openai_codex", credential) // No retry/fallback wrapper.
		if err != nil {
			return mcpReviewResult{}, errors.New("reviewer unavailable")
		}
	}
	raw, err := mcpReviewInput(call, x, digest)
	if err != nil || len(raw) > 100<<10 {
		return mcpReviewResult{}, errors.New("review input unavailable")
	}
	resp, err := p.Chat(ctx, &provider.ChatRequest{Model: "codex-auto-review", System: mcpAutoReviewPrompt, Messages: []provider.Message{{Role: "user", Content: string(raw)}}, Tools: nil})
	if err != nil || ctx.Err() != nil {
		return mcpReviewResult{}, errors.New("reviewer request failed or timed out")
	}
	return parseMCPReview(resp, digest)
}

// Begin once, before the provider request. Resumes/restarts cannot issue it
// again. A native human answer never substitutes for the v5 risk assessment.
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
	gap := func(status, reason string) error {
		if shadow {
			return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "shadow_" + status, reason, "codex-auto-review", "", false, autoReviewPolicyVersion})
		}
		return s.closeMCPReviewGap(q.ID, status, reason)
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
		if err := s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "terminal", terminalErr.Error(), "codex-auto-review", "", false, autoReviewPolicyVersion}); err != nil {
			return err
		}
		return terminalErr
	}
	// A human denial before review remains final without spending a request.
	// A human approval cannot bypass the v5 risk and context assessment.
	if !shadow && q.Status == questionAnswered && strings.TrimSpace(string(q.Answer)) == "false" {
		return nil
	}
	if call.Server == "" || call.Tool == "" || call.ConfigVersion == "" || !mcpSchemaAvailable(call.Schema) || s.reviewAccountID() == "" || s.extensions == nil || !s.extensions.MCPCallCurrent(call) {
		return gap("setup_required", "Current tool configuration, schema or connection binding is unavailable. Approval cannot repair this setup gap.")
	}
	x, digest, contextErr := s.readMCPReviewContext(s.store.db, c, r)
	digest = mcpReviewDigest(x, call)
	if contextErr != nil {
		return gap("context_required", "Necessary durable user authorization or context is missing, changed or exceeds the bounded evidence limit.")
	}
	if latest, e := s.store.getAutoReviewSettings(); e != nil || latest != settings {
		return gap("invalidated", "AutoReview settings changed before this review could begin.")
	}
	_, err = s.store.db.Exec(`INSERT INTO mcp_auto_reviews(question_id,account_id,conversation_id,run_id,action_hash,server,tool,config_fingerprint,arguments_digest,schema_digest,policy_version,context_digest,provenance,mode,settings_revision,status,decision,reason,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'reviewing','','',?)`, q.ID, s.reviewAccountID(), c.ID, r.ID, mcpApprovalHash(call), call.Server, call.Tool, call.ConfigVersion, digestBytes(call.Arguments), digestBytes(call.Schema), autoReviewPolicyVersion, digest, autoReviewProvenance, settings.Mode, settings.Revision, q.ExpiresAt)
	if err != nil {
		var reserved bool
		if e := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_auto_reviews WHERE run_id=? AND action_hash=?)`, r.ID, mcpApprovalHash(call)).Scan(&reserved); e != nil || !reserved {
			return gap("unavailable", "The exact review could not be reserved. No policy judgment or automatic execution permission was established.")
		}
		// A duplicate cannot reserve or replay another reviewer request. Claim
		// must still validate the exact card's review and action-level fences.
		return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "not_reviewed", "No new review was reserved for this exact proposal. Existing action-level review and execution claims remain authoritative.", "codex-auto-review", "", false, autoReviewPolicyVersion})
	}
	displayStatus := "reviewing"
	if shadow {
		displayStatus = "shadow_reviewing"
	}
	if err = s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, displayStatus, "Assessing the exact proposal's authorization, effects and data flow.", "codex-auto-review", "", false, autoReviewPolicyVersion}); err != nil {
		return err
	}
	request := func(reviewCtx context.Context) error {
		result, requestErr := s.requestMCPReview(reviewCtx, call, x, digest)
		if requestErr != nil {
			result = mcpReviewResult{Reason: "AutoReview was unavailable, timed out or returned an invalid response. No policy judgment or execution permission was established.", ContextDigest: digest, RiskLevel: "unknown"}
		}
		return s.finishMCPReview(reviewCtx, c, r, call, q.ID, settings, result)
	}
	if shadow {
		s.startShadowMCPReview(func(reviewCtx context.Context) { _ = request(reviewCtx) })
		return nil
	}
	return request(ctx)
}

func (s *Server) closeMCPReviewGap(id, status, reason string) error {
	if err := s.store.setMCPReviewDisplay(id, MCPReviewDisplay{autoReviewActor, status, reason, "codex-auto-review", "", false, autoReviewPolicyVersion}); err != nil {
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

func (s *Server) finishMCPReview(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, id string, initial autoReviewSettings, result mcpReviewResult) error {
	if initial.Mode == "shadow" {
		return s.finishShadowMCPReview(c, r, call, id, initial, result)
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
	x, _, contextErr := s.readMCPReviewContext(tx, c, r)
	currentDigest := mcpReviewDigest(x, call)
	status := "human_required"
	var reviewStatus string
	if err = tx.QueryRow(`SELECT status FROM mcp_auto_reviews WHERE question_id=?`, id).Scan(&reviewStatus); err != nil {
		return err
	}
	var blocked error
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
		status, result.Reason = "context_required", "Necessary authorization or context changed. A complete current evidence packet is required."
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
	q.Approval.Review = &MCPReviewDisplay{autoReviewActor, status, result.Reason, "codex-auto-review", result.RiskLevel, result.ConfirmationRequired, autoReviewPolicyVersion}
	raw, _ := json.Marshal(q.Approval)
	if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status=?,decision=?,reason=?,risk_level=?,confirmation_required=?,expires_at=? WHERE question_id=?`, status, result.Decision, result.Reason, result.RiskLevel, boolInt(result.ConfirmationRequired), q.ExpiresAt, id); err != nil {
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
