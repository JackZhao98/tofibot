package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"time"
)

const maxMCPApprovalPayloadBytes = 32 << 10

// At most this many cards (and so fresh reviews) per exact action and run when
// evidence keeps changing under a pending review. Afterwards the closure is
// permanent and the recovery guard fences identical retries.
const mcpReviewRetryCards = 3

// approveMCPCall binds one reviewed or human decision to the exact run, MCP
// configuration snapshot, tool and argument bytes. Claiming precedes execution: an
// uncertain remote result must never cause an automatic replay.
func (s *Server) approveMCPCall(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval) error {
	err := s.approveMCPCallOnce(ctx, c, r, call, true)
	if out, ok := tooloutcome.FromError(err); ok && out.Status == tooloutcome.Transient && out.Code == "mcp_review_context_missing" {
		if cards, e := s.mcpReviewCards(r, call); e != nil || cards >= mcpReviewRetryCards {
			return tooloutcome.New(tooloutcome.Permanent, out.Code, out.Certainty, "Evidence kept changing while this exact action was reviewed; its fresh-review limit is reached. Do not retry this MCP action; an identical call stays blocked. Use another permitted route or explain the blocker to the user.", "replan").Err()
		}
	}
	return err
}

func (s *Server) mcpReviewCards(r Run, call extensions.MCPCallApproval) (int, error) {
	var n int
	err := s.store.db.QueryRow(`SELECT COUNT(*) FROM mcp_call_approvals WHERE run_id=? AND action_hash=?`, r.ID, mcpApprovalHash(call)).Scan(&n)
	return n, err
}

// A card closed only because its evidence moved under a pending review (or
// human resume) may be replaced by a fresh card once the model retries.
func mcpReviewRetryable(q Question) bool {
	d := q.Approval
	if d == nil || d.Review == nil || d.Review.Status != "context_required" || d.Review.ContextFailure == nil || !mcpContextTransient(d.Review.ContextFailure.Code) {
		return false
	}
	return q.Status == questionCancelled || q.Status == questionAnswered && q.AnsweredBy != autoReviewActor && string(q.Answer) == "true"
}

func (s *Server) approveMCPCallOnce(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, retry bool) error {
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return err
	}
	if call.Server == "" || call.Tool == "" || call.ConfigVersion == "" {
		return mcpReviewBlocked("setup_required", "The exact external tool configuration is incomplete. Approval cannot repair this setup gap.")
	}
	settings, err := s.store.getAutoReviewSettings()
	if err != nil {
		return err
	}
	if s.extensions != nil && settings.Mode != "auto" {
		requiresHuman, current := s.extensions.MCPCallRequiresHuman(call)
		if current && !requiresHuman {
			prior, err := s.hasMCPApprovalHistory(r, call)
			if err != nil {
				return err
			}
			if !prior {
				if !mcpSchemaAvailable(call.Schema) {
					return mcpReviewBlocked("setup_required", "The exact input schema is unavailable.")
				}
				if call.Recheck != nil {
					if err := call.Recheck(ctx); err != nil {
						return err
					}
				}
				if settings.Mode == "shadow" {
					payload, _ := mcpApprovalPayload(call.Arguments)
					return s.shadowExemptMCPCall(ctx, c, r, call, payload)
				}
				return nil
			}
		}
	}

	payload, err := mcpApprovalPayload(call.Arguments)
	if err != nil {
		return err
	}
	hash := mcpApprovalHash(call)
	var id, status, claimed, expires string
	var answer sql.NullString
	var newQuestion *Question
	loadErr := func() error {
		s.mcpApprovalMu.Lock()
		defer s.mcpApprovalMu.Unlock()
		err = s.store.db.QueryRow(`SELECT q.id,q.status,q.answer_json,a.claimed_at,COALESCE(q.expires_at,'')
		FROM mcp_call_approvals a JOIN questions q ON q.id=a.question_id
		WHERE a.run_id=? AND a.action_hash=? AND q.conversation_id=? AND q.bot_id=?
		ORDER BY q.created_at DESC,q.id DESC LIMIT 1`, r.ID, hash, c.ID, r.BotID).Scan(&id, &status, &answer, &claimed, &expires)
		if err == nil && claimed != "" && s.mcpProposalFence(call) == mcpProposalReadOnly {
			// An approval is one-shot per dispatch. For an effect that makes the
			// identical call a refusal below. A read-only proposal (owner-trusted
			// read-only tool or remote readOnlyHint, the scheduled fence's own
			// predicate) has nothing to replay: its repeat is reviewed afresh on
			// its own card with its own claim, never executed on the used one.
			id, status, answer, claimed, expires = "", "", sql.NullString{}, "", ""
			err = s.store.db.QueryRow(`SELECT q.id,q.status,q.answer_json,a.claimed_at,COALESCE(q.expires_at,'')
			FROM mcp_call_approvals a JOIN questions q ON q.id=a.question_id
			WHERE a.run_id=? AND a.action_hash=? AND q.conversation_id=? AND q.bot_id=? AND a.claimed_at=''
			ORDER BY q.created_at DESC,q.id DESC LIMIT 1`, r.ID, hash, c.ID, r.BotID).Scan(&id, &status, &answer, &claimed, &expires)
		}
		if err == nil && retry && claimed == "" && settings.Mode == "auto" {
			prior, e := s.store.GetQuestion(id)
			if e != nil {
				return e
			}
			cards, e := s.mcpReviewCards(r, call)
			if e != nil {
				return e
			}
			if mcpReviewRetryable(prior) && cards < mcpReviewRetryCards {
				err = sql.ErrNoRows // Review the current evidence on a fresh card.
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			if !mcpSchemaAvailable(call.Schema) {
				return mcpReviewBlocked("setup_required", "The exact input schema is incomplete. Approval cannot repair this setup gap.")
			}
			if call.Recheck != nil {
				if err := call.Recheck(ctx); err != nil {
					return err
				}
			}
			in, normalizeErr := normalizeQuestionInput(askQuestionInput{
				Question: "Allow this external tool call?",
				Type:     questionApproval,
				Approval: &ApprovalDetails{
					Action:  "Call " + call.Tool,
					Target:  "MCP server " + call.Server,
					Impact:  fmt.Sprintf("The external server may change data or contact others. Review the complete %d-byte argument payload below before approving.", len(call.Arguments)),
					Payload: payload,
				},
			})
			if normalizeErr != nil {
				return normalizeErr
			}
			q, createErr := s.store.CreateQuestion(c.ID, r, in)
			if createErr != nil {
				return createErr
			}
			if _, createErr = s.store.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) VALUES(?,?,?)`, q.ID, r.ID, hash); createErr != nil {
				_, _ = s.store.db.Exec(`DELETE FROM questions WHERE id=? AND status=?`, q.ID, questionPending)
				return createErr
			}
			if settings.Mode != "auto" {
				if _, createErr = s.store.Event(c.ID, "question", q.Card()); createErr != nil {
					_, _ = s.store.db.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status=?`, questionCancelled, now(), q.ID, questionPending)
					return createErr
				}
			}
			id, status, expires = q.ID, q.Status, q.ExpiresAt
			newQuestion = &q
		} else if err != nil {
			return err
		}
		return nil
	}()
	if loadErr != nil {
		return loadErr
	}
	if newQuestion != nil {
		if err := s.reviewNewMCPProposal(ctx, c, r, call, *newQuestion); err != nil {
			return err
		}
		q, err := s.store.GetQuestion(id)
		if err != nil {
			return err
		}
		status, expires = q.Status, q.ExpiresAt
		answer = sql.NullString{String: string(q.Answer), Valid: len(q.Answer) > 0}
	}
	if claimed != "" {
		// Refused before dispatch: the earlier dispatch keeps its own certainty.
		return tooloutcome.ApprovalAlreadyClaimed("This exact external tool call already used its approval in this run.")
	}
	current, readErr := s.store.GetQuestion(id)
	if readErr != nil {
		return readErr
	}
	if err := mcpReviewDisplayBlock(current); err != nil {
		return err
	}
	status, expires = current.Status, current.ExpiresAt
	answer = sql.NullString{String: string(current.Answer), Valid: len(current.Answer) > 0}
	// Main's durable terminal expiry wins over later setup/readiness gaps.
	// In particular, old cards without a schema cannot reopen an expired action.
	deadline, parseErr := time.Parse(time.RFC3339Nano, expires)
	if status == questionExpired || (parseErr == nil && !time.Now().Before(deadline) && status == questionAnswered && strings.TrimSpace(answer.String) == "true") {
		return s.parkExpiredMCPApproval(ctx, c, id)
	}
	if status == questionPending || status == questionAnswered && answer.Valid && strings.TrimSpace(answer.String) == "true" {
		if !mcpSchemaAvailable(call.Schema) {
			return mcpReviewBlocked("setup_required", "The exact input schema is incomplete. Approval cannot repair this setup gap.")
		}
		if call.Recheck != nil {
			if err := call.Recheck(ctx); err != nil {
				return err
			}
		}
	}
	if status == questionPending {
		result, waitErr := s.WaitQuestion(ctx, id)
		if waitErr != nil {
			return waitErr
		}
		_ = result
		q, readErr := s.store.GetQuestion(id)
		if readErr != nil {
			return readErr
		}
		answer = sql.NullString{String: string(q.Answer), Valid: len(q.Answer) > 0}
		status, expires = q.Status, q.ExpiresAt
	}
	deadline, parseErr = time.Parse(time.RFC3339Nano, expires)
	if status == questionExpired || (parseErr == nil && !time.Now().Before(deadline) && status == questionAnswered && strings.TrimSpace(answer.String) == "true") {
		return s.parkExpiredMCPApproval(ctx, c, id)
	}
	if status != questionAnswered || !answer.Valid || strings.TrimSpace(answer.String) != "true" {
		return tooloutcome.New(tooloutcome.Denied, "approval_denied", "not_executed", "External tool call was not approved. Do not execute or repeat it.", "explain_blocker").Err()
	}
	if parseErr != nil {
		return tooloutcome.New(tooloutcome.Permanent, "invalid_approval_deadline", "not_executed", "External tool approval has an invalid validity window; request a fresh review before execution.", "explain_blocker").Err()
	}
	if err := s.extensionToolActive(ctx, c, r); err != nil {
		return err
	}
	if call.Recheck != nil {
		if err := call.Recheck(ctx); err != nil {
			return err
		}
	}
	result, err := s.claimMCPApproval(ctx, c, r, call, id)
	if errors.Is(err, errAutoReviewInvalidated) {
		// The same tool call re-reads its card; only a model retry starts a
		// fresh review.
		return s.approveMCPCallOnce(ctx, c, r, call, false)
	}
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		if err == nil && !time.Now().Before(deadline) {
			return s.parkExpiredMCPApproval(ctx, c, id)
		}
		return tooloutcome.New(tooloutcome.Uncertain, "approval_claim_failed", "unknown", "External tool approval was already used or is no longer valid; verify the existing result before proposing another action.", "verify_effect").Err()
	}
	if call.OnClaim != nil {
		call.OnClaim()
	}
	return ctx.Err()
}

func (s *Server) parkExpiredMCPApproval(ctx context.Context, c Conversation, id string) error {
	if err := s.store.expireApproval(id); err != nil {
		return err
	}
	if q, err := s.store.GetQuestion(id); err == nil {
		_, _ = s.store.Event(c.ID, "question", q.Card())
	}
	if runtime.CanSuspend(ctx) {
		return runtime.SuspendForUserInput(ctx, id)
	}
	return tooloutcome.New(tooloutcome.Expired, "approval_window_expired", "not_executed", "External tool approval expired. This workflow must conclude without executing or retrying the proposal.", "finish_summary").Err()
}

func mcpApprovalHash(call extensions.MCPCallApproval) string {
	encoded, _ := json.Marshal(struct {
		Server        string          `json:"server"`
		Tool          string          `json:"tool"`
		ConfigVersion string          `json:"config_version"`
		Arguments     json.RawMessage `json:"arguments"`
	}{call.Server, call.Tool, call.ConfigVersion, call.Arguments})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func mcpApprovalPayload(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || len(raw) > maxMCPApprovalPayloadBytes || !json.Valid(raw) || !utf8.Valid(raw) {
		return "", errors.New("external tool arguments are invalid or exceed the 32 KiB review limit")
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", errors.New("external tool arguments must be a JSON object")
	}
	if containsMCPSecretField(value) {
		return "", errors.New("external tool arguments contain a private field; use a dedicated protected input flow")
	}
	return string(raw), nil
}

func containsMCPSecretField(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			name := strings.ToLower(key)
			name = strings.Map(func(r rune) rune {
				if r == '_' || r == '-' || r == ' ' {
					return -1
				}
				return r
			}, name)
			for _, term := range []string{"password", "passwd", "secret", "token", "authorization", "credential", "privatekey", "apikey"} {
				if strings.Contains(name, term) {
					return true
				}
			}
			if containsMCPSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsMCPSecretField(child) {
				return true
			}
		}
	}
	return false
}
