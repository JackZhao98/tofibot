package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/google/uuid"
	"time"
)

// RenewExpiredApproval creates a fresh review window, never an approval. It
// atomically moves the parked checkpoint and exact MCP binding to a new ID.
// Duplicate requests return that same card; late answers to the old ID fail.
func (s *Store) RenewExpiredApproval(id string) (Question, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Question{}, err
	}
	defer tx.Rollback()
	var renewed string
	if e := tx.QueryRow(`SELECT new_id FROM question_renewals WHERE original_id=?`, id).Scan(&renewed); e == nil {
		return scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, renewed))
	} else if e != sql.ErrNoRows {
		return Question{}, e
	}
	q, err := scanQuestion(tx.QueryRow(`SELECT `+questionColumns+` FROM questions WHERE id=?`, id))
	if err != nil {
		return q, err
	}
	if q.Type != questionApproval || q.Status != questionExpired {
		return q, ErrQuestionNotPending
	}
	var checkpoint string
	if err = tx.QueryRow(`SELECT w.checkpoint_json FROM run_input_waits w JOIN runs r ON r.id=w.run_id
		JOIN conversations c ON c.id=r.conversation_id JOIN bots b ON b.id=r.bot_id
		WHERE w.question_id=? AND w.state='waiting' AND r.status='waiting' AND c.archived=0 AND b.archived=0`, id).Scan(&checkpoint); err != nil {
		return q, errors.New("approval has no active resumable wait")
	}
	newID := uuid.NewString()
	updated, err := runtime.RenewContinuationQuestion(json.RawMessage(checkpoint), id, newID)
	if err != nil {
		return q, err
	}
	created := now()
	expires := time.Now().UTC().Add(defaultQuestionWait).Format(time.RFC3339Nano)
	if _, err = tx.Exec(`INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,options_json,min_selections,max_selections,status,created_at,expires_at,updated_at,fields_json,source_url,allow_other,approval_json)
		SELECT ?,run_id,conversation_id,bot_id,type,prompt,options_json,min_selections,max_selections,'pending',?,?,?,fields_json,source_url,allow_other,approval_json FROM questions WHERE id=?`, newID, created, expires, created, id); err != nil {
		return q, err
	}
	if _, err = tx.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash) SELECT ?,run_id,action_hash FROM mcp_call_approvals WHERE question_id=? AND claimed_at=''`, newID, id); err != nil {
		return q, err
	}
	if _, err = tx.Exec(`UPDATE run_input_waits SET question_id=?,checkpoint_json=? WHERE question_id=? AND state='waiting'`, newID, string(updated), id); err != nil {
		return q, err
	}
	if _, err = tx.Exec(`INSERT INTO question_renewals(original_id,new_id) VALUES(?,?)`, id, newID); err != nil {
		return q, err
	}
	q.ID, q.Status, q.CreatedAt, q.ExpiresAt, q.UpdatedAt = newID, questionPending, created, expires, created
	q.Answer, q.AnsweredBy = nil, ""
	if err = insertRecoveryEvent(tx, q.ConversationID, "question", q.Card(), created); err != nil {
		return q, err
	}
	return q, tx.Commit()
}
