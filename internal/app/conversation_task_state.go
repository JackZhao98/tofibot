package app

import (
	"database/sql"
	"time"
)

// ConversationTaskState is a durable workspace summary, separate from unread
// message counts. It names only recorded work and links to its source record.
type ConversationTaskState struct {
	Status          string `json:"status"`
	RunID           string `json:"run_id,omitempty"`
	QuestionID      string `json:"question_id,omitempty"`
	DraftID         string `json:"draft_id,omitempty"`
	DraftStatus     string `json:"draft_status,omitempty"`
	DraftDemo       bool   `json:"draft_demo,omitempty"`
	ResultMessageID string `json:"result_message_id,omitempty"`
	FailureReason   string `json:"failure_reason,omitempty"`
	CanRetry        bool   `json:"can_retry,omitempty"`
}

func (s *Store) fillConversationTaskStates(byID map[string]*Conversation) error {
	if len(byID) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT conversation_id,id,expires_at FROM questions WHERE status='pending' ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var conv, id string
		var expires sql.NullString
		if err = rows.Scan(&conv, &id, &expires); err != nil {
			rows.Close()
			return err
		}
		c := byID[conv]
		if c == nil || c.TaskState != nil || expiredTaskDeadline(expires.String) {
			continue
		}
		c.TaskState = &ConversationTaskState{Status: "needs_attention", QuestionID: id}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	rows, err = s.db.Query(`SELECT conversation_id,id,status,demo FROM mail_drafts WHERE status IN ('pending','sending','unknown') ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var conv, id, status string
		var demo int
		if err = rows.Scan(&conv, &id, &status, &demo); err != nil {
			rows.Close()
			return err
		}
		if c := byID[conv]; c != nil && c.TaskState == nil {
			taskStatus := "needs_attention"
			if status == "sending" {
				taskStatus = "executing"
			}
			c.TaskState = &ConversationTaskState{Status: taskStatus, DraftID: id, DraftStatus: status, DraftDemo: demo != 0}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	// Active work takes precedence over historical terminal runs. Among terminal
	// runs, creation order wins, so an old failure cannot mask a newer success.
	// A delegated run may execute in another conversation while its user is
	// still waiting in the origin. Project that run into both scopes, but only
	// link to messages and drafts actually visible in each scope.
	rows, err = s.db.Query(`WITH scoped AS (
		SELECT r.id,r.conversation_id,r.status,r.error,r.trigger_message_id,r.created_at,
		r.conversation_id AS scope_conversation_id FROM runs r WHERE r.kind<>'summary'
		UNION ALL
		SELECT r.id,r.conversation_id,r.status,r.error,r.trigger_message_id,r.created_at,
		r.origin_conversation_id AS scope_conversation_id FROM runs r
		WHERE r.kind<>'summary' AND r.origin_conversation_id IS NOT NULL
		AND r.origin_conversation_id<>'' AND r.origin_conversation_id<>r.conversation_id
	), ranked AS (
		SELECT r.id,r.scope_conversation_id,r.conversation_id AS source_conversation_id,
		r.status,r.error,r.trigger_message_id,r.created_at,
		ROW_NUMBER() OVER(PARTITION BY r.scope_conversation_id ORDER BY
		CASE WHEN r.status IN ('queued','running','waiting') THEN 0 ELSE 1 END,
		r.created_at DESC,r.id DESC) AS rank
		FROM scoped r
	)
	SELECT r.id,r.scope_conversation_id,r.source_conversation_id,r.status,r.error,r.trigger_message_id,r.created_at,
		(SELECT m.id FROM messages m WHERE m.run_id=r.id AND m.conversation_id=r.scope_conversation_id AND m.role='assistant'
		AND m.kind NOT IN ('notice','message_ref','bot_result','progress') ORDER BY m.seq DESC LIMIT 1),
		(SELECT d.id FROM mail_drafts d WHERE d.run_id=r.id AND d.conversation_id=r.scope_conversation_id ORDER BY d.created_at DESC,d.id DESC LIMIT 1),
		(SELECT d.status FROM mail_drafts d WHERE d.run_id=r.id AND d.conversation_id=r.scope_conversation_id ORDER BY d.created_at DESC,d.id DESC LIMIT 1),
		(SELECT d.demo FROM mail_drafts d WHERE d.run_id=r.id AND d.conversation_id=r.scope_conversation_id ORDER BY d.created_at DESC,d.id DESC LIMIT 1)
	FROM ranked r WHERE r.rank=1`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, conv, sourceConv, status, created string
		var reason, trigger, result, draftID, draftStatus sql.NullString
		var draftDemo sql.NullInt64
		if err = rows.Scan(&id, &conv, &sourceConv, &status, &reason, &trigger, &created, &result, &draftID, &draftStatus, &draftDemo); err != nil {
			rows.Close()
			return err
		}
		c := byID[conv]
		if c == nil || c.TaskState != nil {
			continue
		}
		if (status == "done" || status == "failed" || status == "interrupted" || status == "cancelled") && newerTaskTimestamp(c.LastUserMessageAt, created) {
			continue
		}
		state := &ConversationTaskState{RunID: id, DraftID: draftID.String, DraftStatus: draftStatus.String, DraftDemo: draftDemo.Int64 != 0}
		switch status {
		case "queued", "running":
			state.Status = "executing"
		case "waiting":
			state.Status = "waiting"
		case "done":
			state.Status = "completed"
			state.ResultMessageID = result.String
			if sourceConv != conv && state.ResultMessageID == "" {
				state.Status = "waiting"
			}
			if draftStatus.String == "declined" {
				state.Status = "cancelled"
			}
		case "failed", "interrupted":
			state.Status = "failed"
			state.FailureReason = reason.String
			if sourceConv != conv {
				state.FailureReason = "协作成员未完成此步骤。"
			}
			// A draft may already have caused an external send. Never offer an
			// automatic run retry from this summary for such a run.
			state.CanRetry = sourceConv == conv && trigger.String != "" && !c.Archived && draftID.String == ""
		case "cancelled":
			state.Status = "cancelled"
		default:
			continue
		}
		c.TaskState = state
	}
	err = rows.Err()
	rows.Close()
	return err
}

func expiredTaskDeadline(raw string) bool {
	if raw == "" {
		return false
	}
	deadline, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil && !time.Now().UTC().Before(deadline)
}

func newerTaskTimestamp(a, b string) bool {
	left, le := time.Parse(time.RFC3339Nano, a)
	right, re := time.Parse(time.RFC3339Nano, b)
	return le == nil && re == nil && left.After(right)
}
