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
const autoReviewPolicyVersion = "mcp-public-read-v3"
const autoReviewTimeout = 30 * time.Second
const autoReviewValidity = 2 * time.Minute

// These entries are independent of trusted_read_only_tools. Only a separately
// verified exact public, side-effect-free operation can be added in source.
// Remote metadata, user settings and model output cannot populate this list.
// Production intentionally has NO entries. Tests install synthetic policies.
type verifiedMCPReviewPolicy struct {
	Server, Tool, ConfigFingerprint, SchemaDigest, Provenance string
	ReviewContract                                            string
	ArgumentsSafe                                             func(json.RawMessage) bool
	ContextComplete                                           func(mcpReviewContext) bool
}

type MCPReviewDisplay struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Reason string `json:"reason"`
	Model  string `json:"model"`
}

type mcpReviewContext struct {
	Intent            string                 `json:"user_intent"`
	IntentMessageID   string                 `json:"user_intent_message_id"`
	Instructions      string                 `json:"bot_instructions"`
	Messages          []Message              `json:"conversation_context"`
	MessageProvenance []mcpMessageProvenance `json:"host_message_provenance"`
	Memories          []Memory               `json:"memories"`
	Summary           string                 `json:"conversation_summary"`
	SummaryVersion    int64                  `json:"summary_version"`
	SourceRunBinding  *mcpScheduleRunBinding `json:"host_source_run_binding,omitempty"`
	SourceToolResults []ToolActivity         `json:"untrusted_source_tool_results,omitempty"`
	ScheduleLineage   *mcpScheduleLineage    `json:"schedule_lineage,omitempty"`
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
// verified policy must additionally prove this call is self-contained: calls
// requiring tool results, visual state, delegation or schedule ancestry stay
// human. This snapshot is recomputed inside the atomic claim transaction.
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
	raw, err := json.Marshal(x)
	if err != nil || len(raw) > 64<<10 || !utf8.Valid(raw) {
		return x, "", errors.New("complete context exceeds review limit")
	}
	return x, digestBytes(raw), nil
}

func (s *Server) verifiedReviewPolicy(call extensions.MCPCallApproval, x mcpReviewContext) *verifiedMCPReviewPolicy {
	for i := range s.autoReviewPolicies {
		p := &s.autoReviewPolicies[i]
		if p.Server == call.Server && p.Tool == call.Tool && p.ConfigFingerprint == call.ConfigVersion && p.SchemaDigest == digestBytes(call.Schema) && p.Provenance != "" && p.ArgumentsSafe != nil && p.ContextComplete != nil && p.ArgumentsSafe(call.Arguments) && p.ContextComplete(x) {
			return p
		}
	}
	return nil
}

type mcpReviewResult struct{ Decision, Reason, ContextDigest string }

// Reject duplicate/unknown keys, trailing JSON, missing context binding and
// any tool attempt. A returned reason is untrusted plain text, never authority.
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
	for d.More() {
		t, err = d.Token()
		key, ok := t.(string)
		if err != nil || !ok || key != "decision" && key != "reason" && key != "context_digest" {
			return out, errors.New("invalid response")
		}
		if _, exists := values[key]; exists {
			return out, errors.New("duplicate response key")
		}
		var v string
		if d.Decode(&v) != nil {
			return out, errors.New("invalid response")
		}
		values[key] = v
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return out, errors.New("invalid response")
	}
	out = mcpReviewResult{values["decision"], values["reason"], values["context_digest"]}
	if out.Decision != "allow" && out.Decision != "deny" && out.Decision != "needs_human" && out.Decision != "context_gap" || strings.TrimSpace(out.Reason) == "" || len(out.Reason) > 600 || out.ContextDigest != expected {
		return out, errors.New("invalid response")
	}
	return out, nil
}

func (s *Server) requestMCPReview(ctx context.Context, call extensions.MCPCallApproval, x mcpReviewContext, digest, contract, provenance string) (mcpReviewResult, error) {
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
	raw, err := mcpReviewInput(call, x, digest, contract, provenance)
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
// again. The original question remains answerable by a human throughout.
func (s *Server) reviewNewMCPProposal(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, q Question) error {
	settings, err := s.store.getAutoReviewSettings()
	if err != nil {
		return err
	}
	if settings.Mode == "off" {
		return nil
	}
	current, err := s.store.GetQuestion(q.ID)
	if err != nil {
		return err
	}
	q = current
	if terminalErr := mcpReviewTerminal(ctx, s.store.db, q, r, call); terminalErr != nil {
		if _, ok := tooloutcome.FromError(terminalErr); !ok {
			return terminalErr
		}
		if err := s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "terminal", terminalErr.Error(), "codex-auto-review"}); err != nil {
			return err
		}
		return terminalErr
	}
	// A human may settle the card before review begins. Never impersonate or
	// overwrite that decision, and do not spend a reviewer request on it.
	if q.Status == questionAnswered {
		return nil
	}
	if call.Server == "" || call.Tool == "" || call.ConfigVersion == "" || !mcpSchemaAvailable(call.Schema) || s.reviewAccountID() == "" || s.extensions == nil || !s.extensions.MCPCallCurrent(call) {
		return s.closeMCPReviewGap(q.ID, "setup_required", "Current tool configuration, schema or connection binding is unavailable. Approval cannot repair this setup gap.")
	}
	x, digest, contextErr := s.readMCPReviewContext(s.store.db, c, r)
	digest = mcpReviewDigest(x, call)
	if contextErr != nil {
		return s.closeMCPReviewGap(q.ID, "context_required", "Necessary durable user authorization or context is missing, changed or exceeds the bounded evidence limit.")
	}
	p := s.verifiedReviewPolicy(call, x)
	if p == nil {
		return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "not_eligible", "No matching independently verified operation qualification permits automatic execution. The existing execution policy requires a human decision; this is not an intrinsic-risk finding.", "codex-auto-review"})
	}
	_, err = s.store.db.Exec(`INSERT INTO mcp_auto_reviews(question_id,account_id,conversation_id,run_id,action_hash,server,tool,config_fingerprint,arguments_digest,policy_version,context_digest,provenance,mode,settings_revision,status,decision,reason,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'reviewing','','',?)`, q.ID, s.reviewAccountID(), c.ID, r.ID, mcpApprovalHash(call), call.Server, call.Tool, call.ConfigVersion, digestBytes(call.Arguments), autoReviewPolicyVersion, digest, p.Provenance, settings.Mode, settings.Revision, q.ExpiresAt)
	if err != nil {
		var reserved bool
		if e := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_auto_reviews WHERE run_id=? AND action_hash=?)`, r.ID, mcpApprovalHash(call)).Scan(&reserved); e != nil || !reserved {
			return s.closeMCPReviewGap(q.ID, "unavailable", "The exact review could not be reserved. No policy judgment or automatic execution permission was established.")
		}
		// A duplicate card still follows the existing human execution policy,
		// but cannot reserve or replay another reviewer request for this action.
		return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "not_reviewed", "No new review was reserved for this exact proposal. Existing action-level review and execution claims remain authoritative.", "codex-auto-review"})
	}
	if err = s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "reviewing", "Assessing the exact proposal's authorization, effects and data flow.", "codex-auto-review"}); err != nil {
		return err
	}
	result, requestErr := s.requestMCPReview(ctx, call, x, digest, p.ReviewContract, p.Provenance)
	if requestErr != nil {
		result = mcpReviewResult{"", "AutoReview was unavailable, timed out or returned an invalid response. No policy judgment or execution permission was established.", digest}
	}
	return s.finishMCPReview(ctx, c, r, call, q.ID, settings, result)
}

func (s *Server) closeMCPReviewGap(id, status, reason string) error {
	if err := s.store.setMCPReviewDisplay(id, MCPReviewDisplay{autoReviewActor, status, reason, "codex-auto-review"}); err != nil {
		return err
	}
	// A genuine human answer racing a technical gap remains authoritative.
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
	} else if q.Status == questionAnswered && q.AnsweredBy != autoReviewActor {
		status = "human_decided"
	} else if s.extensions == nil || !mcpSchemaAvailable(call.Schema) || !s.extensions.MCPCallCurrent(call) {
		status = "setup_required"
		result.Reason = "The current tool configuration or schema binding changed. No execution is permitted until setup is verified."
	} else if contextErr != nil || currentDigest != result.ContextDigest || result.Decision == "context_gap" {
		status = "context_required"
		if result.Decision != "context_gap" {
			result.Reason = "Necessary authorization or context changed. A complete current evidence packet is required."
		}
	} else if initial.Revision != settings.Revision || reviewStatus != "reviewing" {
		status = "invalidated"
		result.Reason = "The automatic decision was invalidated by changed execution settings or review state. It cannot authorize execution."
	} else if result.Decision == "" {
		status = "unavailable"
	} else if initial.Mode == "shadow" {
		status = "shadow"
	} else if result.Decision == "deny" {
		status = "policy_denied"
	} else if initial.Mode == "auto" && settings.Mode == "auto" && result.Decision == "allow" && q.Status == questionPending {
		deadline, _ := time.Parse(time.RFC3339Nano, q.ExpiresAt)
		status = "approved"
		q.Status = questionAnswered
		q.Answer = json.RawMessage("true")
		q.AnsweredBy = autoReviewActor
		short := time.Now().UTC().Add(autoReviewValidity)
		if short.Before(deadline) {
			q.ExpiresAt = short.Format(time.RFC3339Nano)
		}
	}
	if q.Status == questionPending && mcpReviewClosesProposal(status) {
		q.Status = questionCancelled
		if outcome, ok := tooloutcome.FromError(blocked); ok && outcome.Status == tooloutcome.Expired {
			q.Status = questionExpired
		}
	}
	q.UpdatedAt = now()
	q.Approval.Review = &MCPReviewDisplay{autoReviewActor, status, result.Reason, "codex-auto-review"}
	raw, _ := json.Marshal(q.Approval)
	if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status=?,decision=?,reason=?,expires_at=? WHERE question_id=?`, status, result.Decision, result.Reason, q.ExpiresAt, id); err != nil {
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
