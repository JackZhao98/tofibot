package app

// Human-input continuations are safe checkpoints, not a general tool replay
// log. Once claimed, a crash must interrupt the run: subsequent tool effects
// may already have happened and must never be guessed or replayed.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"time"
)

const maxInputCheckpointBytes = 16 << 20

func migrateInputContinuations(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_input_waits (
		run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
		question_id TEXT NOT NULL UNIQUE REFERENCES questions(id) ON DELETE CASCADE,
		checkpoint_json TEXT NOT NULL,
		state TEXT NOT NULL CHECK(state IN ('waiting','claimed')),
		created_at TEXT NOT NULL
	);
	CREATE TRIGGER IF NOT EXISTS input_wait_terminal_cleanup AFTER UPDATE OF status ON runs
	WHEN NEW.status NOT IN ('queued','running','waiting')
	BEGIN
		UPDATE questions SET status=CASE WHEN NEW.status IN ('cancelled','interrupted') THEN 'cancelled' ELSE 'run_done' END,
			updated_at=NEW.updated_at WHERE run_id=NEW.id AND status='pending';
		DELETE FROM run_input_waits WHERE run_id=NEW.id;
	END;`)
	return err
}

// SaveInputContinuation commits the checkpoint and waiting state together.
// A fast answer is allowed to arrive before this transaction; the worker will
// notice it immediately after its current execution has fully unwound.
func (s *Store) SaveInputContinuation(ctx context.Context, runID, questionID string, checkpoint json.RawMessage) error {
	if len(checkpoint) == 0 || len(checkpoint) > maxInputCheckpointBytes || !json.Valid(checkpoint) {
		return errors.New("invalid user-input checkpoint")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRowContext(ctx, `SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
	if err != nil {
		return err
	}
	if r.Status != "running" {
		return context.Canceled
	}
	q, err := scanQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id=?`, questionID))
	if err != nil {
		return err
	}
	if q.RunID != r.ID || q.BotID != r.BotID || q.ConversationID != r.ConversationID || q.Status == questionRunDone {
		return errors.New("question does not belong to the active run")
	}
	var steered bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_steering WHERE old_run_id=?)`, runID).Scan(&steered); err != nil {
		return err
	}
	if steered {
		return errors.New("user input superseded by a newer request")
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO run_input_waits(run_id,question_id,checkpoint_json,state,created_at) VALUES(?,?,?,'waiting',?)
		ON CONFLICT(run_id) DO UPDATE SET question_id=excluded.question_id,checkpoint_json=excluded.checkpoint_json,state='waiting',created_at=excluded.created_at
		WHERE run_input_waits.state='claimed' AND (run_input_waits.question_id<>excluded.question_id
		OR EXISTS(SELECT 1 FROM questions WHERE id=excluded.question_id AND type='approval' AND status='expired'))`, runID, questionID, string(checkpoint), now())
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return errors.New("input checkpoint already exists")
	}
	r.Status, r.Error, r.UpdatedAt = runWaiting, "", now()
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status=?,error=NULL,updated_at=? WHERE id=?`, r.Status, r.UpdatedAt, r.ID); err != nil {
		return err
	}
	if err = insertRecoveryEvent(tx, r.ConversationID, "run", r, r.UpdatedAt); err != nil {
		return err
	}
	if q.Type == questionApproval && q.Status == questionExpired {
		q.Resumable = true
		if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), r.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// refreshInputWaits promotes only answered/cancelled/expired checkpointed
// waits. Scheduled collaboration waits share the status but never this table.
func (s *Store) refreshInputWaits(conv string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT q.id FROM questions q JOIN run_input_waits w ON w.question_id=q.id JOIN runs r ON r.id=w.run_id
		WHERE r.conversation_id=? AND r.status='waiting' AND w.state='waiting'`, conv)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		q, e := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
		if e != nil {
			return e
		}
		if q.Status == questionPending && q.ExpiresAt != "" {
			deadline, e := time.Parse(time.RFC3339Nano, q.ExpiresAt)
			if e != nil {
				return e
			}
			if !time.Now().Before(deadline) {
				q.Status, q.UpdatedAt, q.Resumable = questionExpired, now(), q.Type == questionApproval
				if _, e = tx.Exec(`UPDATE questions SET status=?,updated_at=? WHERE id=? AND status='pending'`, q.Status, q.UpdatedAt, q.ID); e != nil {
					return e
				}
				if e = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), q.UpdatedAt); e != nil {
					return e
				}
			}
		}
		// An expired approval remains a resumable wait. Only a fresh card can
		// release it; expiration is neither denial nor permission to execute.
		if q.Type == questionApproval && q.Status == questionExpired {
			continue
		}
		if q.Status != questionAnswered && q.Status != questionCancelled && q.Status != questionExpired {
			continue
		}
		if _, e = tx.Exec(`UPDATE runs SET status='queued',updated_at=? WHERE id=? AND status='waiting'`, now(), q.RunID); e != nil {
			return e
		}
		r, e := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, q.RunID))
		if e != nil {
			return e
		}
		if e = insertRecoveryEvent(tx, r.ConversationID, "run", r, r.UpdatedAt); e != nil {
			return e
		}
	}
	return tx.Commit()
}

// claimInputContinuation is also the ordinary queued->running transition.
// Consumption and claim are atomic, so concurrent workers/duplicate answers
// cannot both resume. A claimed checkpoint is never eligible at startup.
func (s *Store) claimInputContinuation(ctx context.Context, runID string) (json.RawMessage, Question, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, Question{}, false, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		return nil, Question{}, false, err
	}
	if status != "queued" {
		return nil, Question{}, false, nil
	}
	var data, questionID, state string
	err = tx.QueryRowContext(ctx, `SELECT checkpoint_json,question_id,state FROM run_input_waits WHERE run_id=?`, runID).Scan(&data, &questionID, &state)
	var q Question
	if err != nil && err != sql.ErrNoRows {
		return nil, q, false, err
	}
	if err == nil {
		if state != "waiting" {
			return nil, q, false, errors.New("input continuation already claimed")
		}
		q, err = scanQuestion(tx.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM questions WHERE id=?`, questionID))
		if err != nil {
			return nil, q, false, err
		}
		if q.RunID != runID || (q.Type == questionApproval && q.Status == questionExpired) || (q.Status != questionAnswered && q.Status != questionCancelled && q.Status != questionExpired) {
			return nil, q, false, errors.New("input continuation is not ready")
		}
		if _, err = tx.ExecContext(ctx, `UPDATE run_input_waits SET state='claimed' WHERE run_id=? AND state='waiting'`, runID); err != nil {
			return nil, q, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE runs SET status='running',error=NULL,updated_at=? WHERE id=? AND status='queued'`, now(), runID); err != nil {
		return nil, q, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, q, false, err
	}
	return json.RawMessage(data), q, true, nil
}

func (s *Store) hasInputWait(conv string) bool {
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits w JOIN runs r ON r.id=w.run_id WHERE r.conversation_id=? AND r.status='waiting' AND w.state='waiting')`, conv).Scan(&found)
	return found
}

func (s *Store) preservesInputWait(runID string) bool {
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits w JOIN runs r ON r.id=w.run_id WHERE r.id=? AND r.status IN ('waiting','queued') AND w.state='waiting')`, runID).Scan(&found)
	return found
}

// A newer human message is steering, not an answer to the old card. Settle
// parked waits at this safe boundary and carry their completed artifacts to
// the successor, exactly as running work does at its next model boundary.
func supersedeInputWaitsTx(tx *sql.Tx, conv string) error {
	rows, err := tx.Query(`SELECT r.id FROM runs r JOIN run_input_waits w ON w.run_id=r.id
		WHERE r.conversation_id=? AND r.status IN ('waiting','queued') AND w.state='waiting'
		AND EXISTS(SELECT 1 FROM run_steering link JOIN runs n ON n.id=link.new_run_id WHERE link.old_run_id=r.id AND n.status='queued')`, conv)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		var successor string
		if err = tx.QueryRow(`SELECT n.id FROM run_steering link JOIN runs n ON n.id=link.new_run_id WHERE link.old_run_id=? AND n.status='queued' ORDER BY n.queue_seq DESC,n.created_at DESC,n.id DESC LIMIT 1`, id).Scan(&successor); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE run_attachments SET run_id=? WHERE run_id=?`, successor, id); err != nil {
			return err
		}
		if err = interruptToolActivitiesTx(tx, id, now()); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE stream_drafts SET status='cancelled',updated_at=? WHERE run_id=? AND status='active'`, now(), id); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE runs SET status='cancelled',error='steered by newer user message',updated_at=? WHERE id=?`, now(), id); err != nil {
			return err
		}
		r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, id))
		if err != nil {
			return err
		}
		if err = insertRecoveryEvent(tx, r.ConversationID, "run", r, r.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

// Resume values remain ordinary tool output. Missing/expired secrets are
// explicitly marked for re-request; never restore a cleared vault reference
// or place a password value in the provider transcript/checkpoint.
func (s *Server) inputResumeResult(q Question) string {
	if q.Status != questionAnswered {
		b, _ := json.Marshal(map[string]string{"status": q.Status})
		return string(b)
	}
	if q.Type == questionApproval {
		var mcpApproval bool
		_ = s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM mcp_call_approvals WHERE question_id=?)`, q.ID).Scan(&mcpApproval)
		if mcpApproval {
			if string(q.Answer) != "true" {
				return tooloutcome.New(tooloutcome.Denied, "approval_denied", "not_executed", "The human did not approve this external tool call. Do not execute or repeat it.", "explain_blocker").JSON()
			}
			return tooloutcome.New("approval_recorded", "approval_recorded", "not_executed", "The human recorded approval; the external action has not executed. Reinspect current state and refresh the MCP schema if needed, then propose the exact approved call.", "reinspect_and_call").JSON()
		}
	}
	if q.Type != questionForm {
		return string(q.Answer)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(q.Answer, &fields) != nil {
		return `{"status":"invalid_answer"}`
	}
	for _, field := range q.Fields {
		if field.Type != "password" {
			continue
		}
		var answer formSecretAnswer
		if json.Unmarshal(fields[field.ID], &answer) != nil || answer.SecretRef == "" {
			continue
		}
		valid := false
		if v := s.secretVault; v != nil {
			v.mu.Lock()
			r, ok := v.records[answer.SecretRef]
			created, e := time.Parse(time.RFC3339Nano, r.CreatedAt)
			valid = ok && e == nil && time.Since(created) <= 15*time.Minute && r.Status == "ready" && r.RunID == q.RunID && r.BotID == q.BotID && r.ConversationID == q.ConversationID
			v.mu.Unlock()
		}
		if !valid {
			fields[field.ID] = json.RawMessage(`{"status":"secret_expired","value_hidden":true,"action_required":"Ask the human for this password again with ask_user_form; never infer it or request it in plain text."}`)
		}
	}
	b, _ := json.Marshal(fields)
	return string(b)
}
