package app

// Part results fold under the lead's card (divide and conquer, line A). Only the
// message that IS the part's work report to the lead folds: the result the
// holder's run left on its task (work_items.result_message_id). Everything else a
// member says stays an ordinary message: a note or a mid-run message (spoken to
// the person), a reply to the person or to a message the person queued for the
// run, approval cards, questions, forms, errors and messages with files. The
// server names the part with Message.TaskID, derived and never stored. A
// delegation message of the group never folds: it is the visible plan.

import (
	"database/sql"
	"strings"
)

// partFoldConditions is the part of the fold query that holds for one message
// m joined to its task w. A run that read a message of the person (queued
// steering) or that a person's message started answers the person, so its
// result is not a report to the lead.
const partFoldConditions = ` m.role='assistant' AND m.run_id<>'' AND COALESCE(m.kind,'') IN ('','segment')
 AND COALESCE(m.client_message_id,'') NOT LIKE 'delegation:%' AND w.parent_task_id<>'' AND w.kind='task'
 AND NOT EXISTS(SELECT 1 FROM attachment_messages am WHERE am.message_id=m.id)
 AND NOT EXISTS(SELECT 1 FROM bot_inbox i WHERE i.kind='user_message' AND i.consumed_by_run=m.run_id)
 AND NOT EXISTS(SELECT 1 FROM runs r JOIN messages u ON u.id=r.trigger_message_id WHERE r.id=m.run_id AND u.role='user')`

// partTaskSQL selects the part a message is the result of, when that task has a parent.
const partTaskSQL = `SELECT w.id FROM messages m JOIN work_items w ON w.result_message_id=m.id
 WHERE m.id=? AND` + partFoldConditions + ` ORDER BY w.created_at LIMIT 1`

func partTaskForMessage(q queryer, messageID string) string {
	if messageID == "" {
		return ""
	}
	var id string
	if q.QueryRow(partTaskSQL, messageID).Scan(&id) != nil {
		return ""
	}
	return id
}

// hydrateMessagePartTasks fills TaskID for a page of history.
func (s *Store) hydrateMessagePartTasks(msgs []Message) error {
	idx := map[string]int{}
	args := []any{}
	for i, m := range msgs {
		if m.Role == "assistant" && m.RunID != "" && (m.Kind == "" || m.Kind == "segment") {
			idx[m.ID] = i
			args = append(args, m.ID)
		}
	}
	if len(args) == 0 {
		return nil
	}
	rows, err := s.db.Query(`SELECT m.id,w.id FROM messages m JOIN work_items w ON w.result_message_id=m.id
 WHERE m.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) AND`+partFoldConditions+` ORDER BY w.created_at DESC`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, taskID string
		if err = rows.Scan(&messageID, &taskID); err != nil {
			return err
		}
		// The oldest task wins, as in partTaskSQL: the rows come newest first, so the last write is the oldest.
		msgs[idx[messageID]].TaskID = taskID
	}
	return rows.Err()
}

// announceFoldedResultTx sends the message of a part's result again, with its
// task_id, when the link to the task is made. A holder's say(answer, task_id)
// posts the message while the run works and the task takes it as its result only
// when the run ends; a client that read the message from the stream before then
// has no task_id, so it would keep the words as a bubble. The client replaces the
// message by its id.
func announceFoldedResultTx(tx *sql.Tx, messageID string) error {
	if messageID == "" {
		return nil
	}
	taskID := partTaskForMessage(tx, messageID)
	if taskID == "" {
		return nil
	}
	m, err := scanMsg(tx.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE id=?`, messageID))
	if err != nil {
		return err
	}
	m.TaskID = taskID
	return insertEventTx(tx, m.ConversationID, "message", m, now())
}
