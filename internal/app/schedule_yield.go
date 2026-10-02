package app

import (
	"database/sql"
	"encoding/json"
)

// A waiting run has ended its model turn, not completed its scheduled task.
// Its durable child owns the next execution; the source resumes in a new
// follow-up with its own receipt requirement. No model text can create this
// state without an actual return-capable delegation in the database.
const runWaiting = "waiting"

func scheduledContinuationTx(tx *sql.Tx, source Run) (pending bool, returned string, err error) {
	if err = requireCurrentMemberTx(tx, source.ConversationID, source.BotID); err != nil {
		return false, "", err
	}
	rows, err := tx.Query(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE parent_run_id=? ORDER BY queue_seq,created_at,id`, source.ID)
	if err != nil {
		return false, "", err
	}
	var children []Run
	for rows.Next() {
		child, scanErr := scanRun(rows)
		if scanErr != nil {
			rows.Close()
			return false, "", scanErr
		}
		children = append(children, child)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, "", err
	}
	for _, child := range children {
		if child.BotID == source.BotID || child.TriggerMessageID == "" {
			continue
		}
		sameGroup := child.ConversationID == source.ConversationID && child.Kind == runKindGroupTask
		crossMessage := false
		if child.ConversationID != source.ConversationID {
			target, ok, err := runReturnTargetTx(tx, child)
			if err != nil {
				return false, "", err
			}
			crossMessage = ok && target.ID == source.ID
		}
		if !sameGroup && !crossMessage {
			continue
		}
		if child.Status == "queued" || child.Status == "running" || child.Status == runWaiting {
			if err := requireCurrentMemberTx(tx, child.ConversationID, child.BotID); err != nil {
				return false, "", err
			}
			pending = true
		}
	}
	// A cross-conversation child (including a nested return or retry) may
	// finish before this turn exits. Validate its queued continuation against
	// the logical requester, not merely a matching Bot or origin string.
	rows, err = tx.Query(`WITH RECURSIVE family(id) AS (
		SELECT id FROM runs WHERE parent_run_id=?
		UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
	) SELECT id,parent_run_id FROM runs WHERE id IN family AND kind=? AND conversation_id=? AND bot_id=? AND status='queued' ORDER BY queue_seq,created_at,id`, source.ID, runKindFollowup, source.ConversationID, source.BotID)
	if err != nil {
		return false, "", err
	}
	type candidate struct{ id, parent string }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err = rows.Scan(&item.id, &item.parent); err != nil {
			rows.Close()
			return false, "", err
		}
		candidates = append(candidates, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, "", err
	}
	for _, item := range candidates {
		predecessor, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, item.parent))
		if err != nil {
			return false, "", err
		}
		target, ok, err := runReturnTargetTx(tx, predecessor)
		if err != nil {
			return false, "", err
		}
		if ok && target.ID == source.ID && predecessor.Status == "done" {
			returned = item.id
		}
	}
	return pending, returned, nil
}

func (s *Store) hasScheduledContinuation(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, id))
	if err != nil || r.Status != "running" {
		return false, err
	}
	pending, returned, err := scheduledContinuationTx(tx, r)
	return pending || returned != "", err
}

func (s *Store) yieldScheduledRun(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, id))
	if err != nil || r.Status != "running" {
		return false, err
	}
	pending, returned, err := scheduledContinuationTx(tx, r)
	if err != nil || (!pending && returned == "") {
		return false, err
	}
	r.Status, r.Error, r.UpdatedAt = runWaiting, "", now()
	if returned != "" {
		r.Status = "done" // The queued source follow-up still owns completion.
		if _, err = tx.Exec(`UPDATE run_attachments SET run_id=? WHERE run_id=?`, returned, id); err != nil {
			return false, err
		}
	}
	if _, err = tx.Exec(`UPDATE runs SET status=?,error=NULL,updated_at=? WHERE id=? AND status='running'`, r.Status, r.UpdatedAt, id); err != nil {
		return false, err
	}
	// An assignment acknowledgement is not a final scheduled answer. Keep
	// tool/dispatch evidence but close the speculative final stream silently.
	if _, err = tx.Exec(`UPDATE stream_drafts SET content='',status='done',revision=revision+1,updated_at=? WHERE run_id=? AND status='active'`, r.UpdatedAt, id); err != nil {
		return false, err
	}
	data, _ := json.Marshal(r)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, r.ConversationID, "run", string(data), r.UpdatedAt); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Atomically replace a waiting turn with a durable source continuation. Its
// status can become done only in the same transaction that enqueues the return;
// the occurrence therefore never observes a false all-done gap.
func settleScheduledWaitTx(tx *sql.Tx, requester Run, followup Run) error {
	if requester.Status != runWaiting {
		return nil
	}
	var inputWait bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM run_input_waits WHERE run_id=?)`, requester.ID).Scan(&inputWait); err != nil {
		return err
	}
	if inputWait {
		return nil
	}
	requester.Status, requester.Error, requester.UpdatedAt = "done", "", now()
	if _, err := tx.Exec(`UPDATE runs SET status='done',error=NULL,updated_at=? WHERE id=? AND status=?`, requester.UpdatedAt, requester.ID, runWaiting); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE run_attachments SET run_id=? WHERE run_id=?`, followup.ID, requester.ID); err != nil {
		return err
	}
	data, _ := json.Marshal(requester)
	_, err := tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, requester.ConversationID, "run", string(data), requester.UpdatedAt)
	return err
}
