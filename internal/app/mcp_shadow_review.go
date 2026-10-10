package app

import (
	"context"
	"encoding/json"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Shadow work has a server lifetime and a bounded request, independent of the
// tool's completion/suspension. It can publish advice but never claim a call.
func (s *Server) startShadowMCPReview(work func(context.Context)) {
	s.shadowReviewMu.Lock()
	defer s.shadowReviewMu.Unlock()
	if s.shadowReviewClosed {
		return
	}
	if s.shadowReviewContext == nil {
		s.shadowReviewContext, s.shadowReviewCancel = context.WithCancel(context.Background())
	}
	ctx := s.shadowReviewContext
	s.shadowReviewWG.Add(1)
	go func() { defer s.shadowReviewWG.Done(); work(ctx) }()
}

func (s *Server) stopShadowMCPReviews() {
	s.shadowReviewMu.Lock()
	s.shadowReviewClosed = true
	if s.shadowReviewCancel != nil {
		s.shadowReviewCancel()
	}
	s.shadowReviewMu.Unlock()
	s.shadowReviewWG.Wait()
}

// A host-exempt read needs a durable advisory card, not a pending approval.
// There is deliberately no mcp_call_approvals row or answer/actor for this card.
func (s *Server) shadowExemptMCPCall(ctx context.Context, c Conversation, r Run, call extensions.MCPCallApproval, payload string) error {
	s.mcpApprovalMu.Lock()
	defer s.mcpApprovalMu.Unlock()
	var reserved bool
	if err := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_auto_reviews WHERE run_id=? AND action_hash=?)`, r.ID, mcpApprovalHash(call)).Scan(&reserved); err != nil {
		return err
	}
	if reserved {
		return nil
	}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "External tool review advice", Type: questionApproval, Approval: &ApprovalDetails{Action: "Call " + call.Tool, Target: "MCP server " + call.Server, Impact: "Shadow advice does not change the existing host execution policy.", Payload: payload}})
	if err != nil {
		return err
	}
	in.Approval.ReviewOnly = true
	q, err := s.store.CreateQuestion(c.ID, r, in)
	if err != nil {
		return err
	}
	if _, err = s.store.Event(c.ID, "question", q.Card()); err != nil {
		return err
	}
	if payload == "" {
		return s.store.setMCPReviewDisplay(q.ID, MCPReviewDisplay{autoReviewActor, "shadow_context_required", "Complete arguments are unavailable for safe review; the original host execution policy remains in force.", "codex-auto-review", "", false, autoReviewPolicyVersion, nil, ""})
	}
	return s.reviewNewMCPProposal(ctx, c, r, call, q)
}

func (s *Server) finishShadowMCPReview(c Conversation, r Run, call extensions.MCPCallApproval, id string, initial autoReviewSettings, result mcpReviewResult, failure *mcpReviewFailure) error {
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
	var reviewStatus, snapshotDigest string
	if err = tx.QueryRow(`SELECT status,context_digest FROM mcp_auto_reviews WHERE question_id=?`, id).Scan(&reviewStatus, &snapshotDigest); err != nil {
		return err
	}
	status := "shadow_" + result.Decision
	switch {
	case settings != initial || reviewStatus != "reviewing":
		status, result.Reason = "shadow_invalidated", "Settings changed; this advice has no execution authority."
	case s.extensions == nil || !s.extensions.MCPCallCurrent(call):
		status, result.Reason = "shadow_setup_required", "Tool configuration changed during review; this advice has no execution authority."
	case snapshotDigest != result.ContextDigest:
		status, result.Reason = "shadow_context_required", "The advice does not match the immutable proposal snapshot."
	case result.Decision == "context_gap" || result.RiskLevel == "unknown" && result.Decision != "":
		status = "shadow_context_required"
	case result.Decision == "":
		status = "shadow_unavailable"
	}
	// Advice never changes a human answer, expiry, cancellation or run state.
	failureCategory, failureDetail := mcpReviewFailureColumns(failure)
	display := &MCPReviewDisplay{autoReviewActor, status, result.Reason, "codex-auto-review", result.RiskLevel, result.ConfirmationRequired, autoReviewPolicyVersion, nil, ""}
	if status == "shadow_unavailable" {
		display.FailureCategory = failureCategory
	}
	q.Approval.Review = display
	q.UpdatedAt = now()
	raw, _ := json.Marshal(q.Approval)
	if _, err = tx.Exec(`UPDATE mcp_auto_reviews SET status=?,decision=?,reason=?,risk_level=?,confirmation_required=?,failure_category=?,failure_detail=? WHERE question_id=?`, status, result.Decision, result.Reason, result.RiskLevel, boolInt(result.ConfirmationRequired), failureCategory, failureDetail, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE questions SET approval_json=?,updated_at=? WHERE id=?`, string(raw), q.UpdatedAt, id); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, c.ID, "question", q.Card(), q.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// A prior approval/claim remains authoritative after switching to the original
// exemption path. Turning Auto off is never permission to replay its action.
func (s *Server) hasMCPApprovalHistory(r Run, call extensions.MCPCallApproval) (bool, error) {
	var exists bool
	err := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE run_id=? AND action_hash=?) OR EXISTS(SELECT 1 FROM mcp_call_execution_claims WHERE run_id=? AND action_hash=?)`, r.ID, mcpApprovalHash(call), r.ID, mcpApprovalHash(call)).Scan(&exists)
	return exists, err
}
