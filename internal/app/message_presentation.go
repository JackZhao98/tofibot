package app

import "strings"

// alias is a compile-time query alias, never request data. Old scheduled
// triggers were stored with role=user and an empty kind. Recognize only the
// exact durable occurrence trigger, not text or merely the presence of run_id.
func legacyScheduledMessageSQL(alias string) string {
	return `(` + alias + `.role='user' AND ` + alias + `.kind='' AND COALESCE(` + alias + `.sender_bot_id,'')=''
		AND EXISTS (SELECT 1 FROM runs schedule_run JOIN schedule_occurrences occurrence ON occurrence.run_id=schedule_run.id
			WHERE schedule_run.id=` + alias + `.run_id AND schedule_run.conversation_id=` + alias + `.conversation_id
			AND schedule_run.kind='schedule' AND schedule_run.trigger_message_id=` + alias + `.id))`
}

// Project historical triggers without rewriting user data or old event JSON.
// The bounded lookup also works for pagination, search and event replay.
func (s *Store) hydrateScheduledMessageKinds(messages []Message) error {
	var candidates []int
	for i, m := range messages {
		if m.Kind == "" && m.Role == "user" && m.SenderBotID == "" && m.RunID != "" {
			candidates = append(candidates, i)
		}
	}
	for len(candidates) > 0 {
		n := min(len(candidates), 200)
		batch := candidates[:n]
		candidates = candidates[n:]
		args := make([]any, len(batch))
		for i, index := range batch {
			args[i] = messages[index].ID
		}
		rows, err := s.db.Query(`SELECT m.id,m.conversation_id,m.run_id FROM messages m WHERE m.id IN (`+strings.TrimSuffix(strings.Repeat("?,", n), ",")+`) AND `+legacyScheduledMessageSQL("m"), args...)
		if err != nil {
			return err
		}
		type identity struct{ id, conversation, run string }
		matched := make(map[identity]bool)
		for rows.Next() {
			var key identity
			if err = rows.Scan(&key.id, &key.conversation, &key.run); err != nil {
				rows.Close()
				return err
			}
			matched[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, index := range batch {
			m := &messages[index]
			if matched[identity{m.ID, m.ConversationID, m.RunID}] {
				m.Kind = "scheduled_task"
			}
		}
	}
	return nil
}
