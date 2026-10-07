package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

var errAutoReviewInvalidated = errors.New("AutoReview decision invalidated")

func (s *Server) claimMCPApproval(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, id string) (sql.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return nil, err
	}
	var claimed string
	if err = tx.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, id).Scan(&claimed); err != nil {
		return nil, err
	}
	if claimed != "" {
		return nil, tooloutcome.New(tooloutcome.Uncertain, "approval_already_claimed", "unknown", "This approval already claimed execution. Verify the existing result before proposing another action.", "verify_effect").Err()
	}
	// A duplicate card cannot bypass an existing human denial/cancellation of
	// the same exact proposal, even when separate runtimes produced the cards.
	var denied bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals a JOIN questions q ON q.id=a.question_id WHERE a.run_id=? AND a.action_hash=? AND q.id<>? AND (q.status='cancelled' OR (q.status='answered' AND q.answer_json='false')))`, r.ID, mcpApprovalHash(call), id).Scan(&denied); err != nil {
		return nil, err
	}
	if denied {
		return nil, tooloutcome.New(tooloutcome.Denied, "approval_denied", "not_executed", "This exact proposal was denied or cancelled. Do not execute or repeat it.", "explain_blocker").Err()
	}
	if q.Status == questionPending {
		return nil, errAutoReviewInvalidated
	}
	settings, settingsErr := readAutoReviewSettings(tx)
	if settingsErr != nil {
		return nil, settingsErr
	}
	if q.AnsweredBy == autoReviewActor || settings.Mode == "auto" {
		var account, conv, run, hash, server, tool, config, args, schema, policy, contextDigest, snapshot, provenance, status, expiry, decision, risk string
		var revision int64
		var confirmation int
		err = tx.QueryRow(`SELECT account_id,conversation_id,run_id,action_hash,server,tool,config_fingerprint,arguments_digest,schema_digest,policy_version,context_digest,context_snapshot,provenance,status,settings_revision,expires_at,decision,risk_level,confirmation_required FROM mcp_auto_reviews WHERE question_id=?`, id).Scan(&account, &conv, &run, &hash, &server, &tool, &config, &args, &schema, &policy, &contextDigest, &snapshot, &provenance, &status, &revision, &expiry, &decision, &risk, &confirmation)
		x, _, contextErr := s.readMCPReviewContext(tx, c, r)
		digest := mcpReviewDigest(x, call)
		disposition := mcpReviewDisposition(mcpReviewResult{Decision: decision, RiskLevel: risk, ConfirmationRequired: confirmation == 1})
		automatic := q.AnsweredBy == autoReviewActor
		contextValid := contextErr == nil && contextDigest == digest
		if !automatic && !contextValid && contextErr == nil && err == nil {
			contextValid = mcpHumanResumeContextMatches(ctx, tx, c, r, call, q, snapshot, contextDigest, x)
		}
		decisionPermitsClaim := disposition == "approved" || !automatic && disposition == "human_required"
		statusPermitsClaim := status == "approved" || !automatic && (status == "human_required" || status == "human_decided")
		valid := err == nil && contextValid && confirmation >= 0 && confirmation <= 1 && decisionPermitsClaim && statusPermitsClaim && provenance == autoReviewProvenance && settings.Mode == "auto" && settings.Revision == revision && account == s.reviewAccountID() && conv == c.ID && run == r.ID && hash == mcpApprovalHash(call) && server == call.Server && tool == call.Tool && config == call.ConfigVersion && args == digestBytes(call.Arguments) && schema == digestBytes(call.Schema) && policy == autoReviewPolicyVersion && expiry == q.ExpiresAt && s.extensions != nil && s.extensions.MCPCallCurrent(call)
		deadline, e := time.Parse(time.RFC3339Nano, expiry)
		valid = valid && e == nil && time.Now().Before(deadline)
		if !valid {
			if !automatic {
				// Retain the truthful human answer, but it cannot repair missing review
				// bindings or override a denial/unknown effect. Never impersonate it.
				state := disposition
				if state != "policy_denied" && state != "context_required" {
					state = "unavailable"
				}
				return nil, mcpReviewBlocked(state, "The exact v5 review or approval binding is invalid. No execution permission was established.")
			}
			// Never renew or reuse a model decision. A human may answer the
			// original remaining window; an expired card uses existing fences.
			if q.Status == questionAnswered {
				q.Status = questionPending
			}
			deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt)
			if e != nil || !time.Now().Before(deadline) {
				q.Status = questionExpired
			}
			q.Answer = nil
			q.AnsweredBy = ""
			q.UpdatedAt = now()
			reviewState, reason := "invalidated", "The automatic decision's execution binding is no longer valid. It cannot authorize execution."
			var contextFailure *MCPContextFailure
			if q.Status == questionExpired {
				reviewState, reason = "terminal", "This proposal expired and remains non-executable."
			} else if contextErr != nil || contextDigest != digest {
				contextFailure = mcpContextDiagnostic(contextErr)
				if contextErr == nil {
					contextFailure = mcpContextDiagnostic(mcpContextFail(mcpContextDigestChanged))
				}
				q.Status, reviewState, reason = questionCancelled, "context_required", "Necessary authorization changed after review: "+mcpContextFailureReason(contextFailure.Code)+"."
			} else if s.extensions == nil || !mcpSchemaAvailable(call.Schema) || !s.extensions.MCPCallCurrent(call) {
				q.Status, reviewState, reason = questionCancelled, "setup_required", "The current tool configuration or schema binding is unavailable. Approval cannot repair this setup gap."
			}
			q.Approval.Review = &MCPReviewDisplay{autoReviewActor, reviewState, reason, "codex-auto-review", "", false, autoReviewPolicyVersion, contextFailure}
			raw, _ := json.Marshal(q.Approval)
			if _, err = tx.Exec(`UPDATE questions SET status=?,answer_json=NULL,answered_by=NULL,approval_json=?,updated_at=? WHERE id=?`, q.Status, string(raw), q.UpdatedAt, id); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status=? WHERE question_id=?`, reviewState, id); err != nil {
				return nil, err
			}
			if err = insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			return nil, errAutoReviewInvalidated
		}
	}
	result, err := tx.Exec(`UPDATE mcp_call_approvals SET claimed_at=? WHERE question_id=? AND run_id=? AND action_hash=? AND claimed_at=''
	AND EXISTS(SELECT 1 FROM questions q JOIN runs r ON r.id=q.run_id JOIN conversations c ON c.id=q.conversation_id JOIN bots b ON b.id=q.bot_id
	WHERE q.id=? AND q.conversation_id=? AND q.bot_id=? AND q.status='answered' AND q.answer_json='true' AND julianday(q.expires_at)>julianday(?) AND r.status='running' AND c.archived=0 AND b.archived=0)`, now(), id, r.ID, mcpApprovalHash(call), id, c.ID, r.BotID, now())
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 1 {
		// The action, rather than only its card ID, owns the execution claim.
		// This also fences duplicate cards from separate runtime instances.
		res, e := tx.Exec(`INSERT OR IGNORE INTO mcp_call_execution_claims(run_id,action_hash,question_id,claimed_at) VALUES(?,?,?,?)`, r.ID, mcpApprovalHash(call), id, now())
		if e != nil {
			return nil, e
		}
		inserted, e := res.RowsAffected()
		if e != nil {
			return nil, e
		}
		if inserted != 1 {
			return nil, tooloutcome.New(tooloutcome.Uncertain, "approval_already_claimed", "unknown", "This exact external tool proposal already claimed execution. Verify its existing result before another action.", "verify_effect").Err()
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
