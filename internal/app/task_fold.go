package app

// Part results fold under the lead's card (divide and conquer, line A). A message
// a member sends from the run that holds a part (an acknowledgement, a note, the
// result) stays a stored message, goes to the giver's inbox and the history as
// before; only the timeline folds it under the parent card. The server names the
// part with Message.TaskID, derived from the message's run and never stored. A
// delegation message of the group is never folded: it is the visible plan.

import "strings"

// partTaskSQL selects the part a message belongs to: the task whose assignee run
// (or result-submitting run) sent it, when that task has a parent.
const partTaskSQL = `SELECT w.id FROM messages m JOIN work_items w ON (w.assignee_run_id=m.run_id OR w.submit_run_id=m.run_id)
 WHERE m.id=? AND m.run_id<>'' AND m.role='assistant' AND COALESCE(m.kind,'') IN ('','segment')
 AND COALESCE(m.client_message_id,'') NOT LIKE 'delegation:%' AND w.parent_task_id<>'' AND w.kind='task' ORDER BY w.created_at LIMIT 1`

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
	rows, err := s.db.Query(`SELECT m.id,w.id FROM messages m JOIN work_items w ON (w.assignee_run_id=m.run_id OR w.submit_run_id=m.run_id)
 WHERE m.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) AND m.run_id<>'' AND COALESCE(m.client_message_id,'') NOT LIKE 'delegation:%'
 AND w.parent_task_id<>'' AND w.kind='task' ORDER BY w.created_at DESC`, args...)
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
