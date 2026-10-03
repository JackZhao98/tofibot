package app

import (
	"errors"
	"strings"
)

type SchedulePatch struct {
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Content     *string       `json:"content"`
	Expected    *EditBaseline `json:"expected,omitempty"`
}

// PatchSchedule changes presentation or future instructions only. It never
// recalculates timing or rewrites an already claimed occurrence/trigger.
func (s *Store) PatchSchedule(id string, patch SchedulePatch) (Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return Schedule{}, err
	}
	if patch.Title == nil && patch.Description == nil && patch.Content == nil {
		return Schedule{}, errors.New("provide title, description or content")
	}
	if err := validateMetadataPatch(patch.Title, patch.Description); err != nil {
		return Schedule{}, err
	}
	if err := validateEditExpectation(patch.Expected, patch.Title, patch.Description, patch.Content); err != nil {
		return Schedule{}, err
	}
	if patch.Content != nil && (strings.TrimSpace(*patch.Content) == "" || len([]rune(*patch.Content)) > maxScheduleContent) {
		return Schedule{}, errors.New("content is required and must be at most 32768 characters")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Schedule{}, err
	}
	defer tx.Rollback()
	x, err := scanSchedule(tx.QueryRow(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE id=? AND status<>'deleted'`, id))
	if err != nil {
		return Schedule{}, err
	}
	if err = requireConversationActiveTx(tx, x.ConversationID); err != nil {
		return Schedule{}, err
	}
	if err = requireActiveMemberTx(tx, x.ConversationID, x.BotID); err != nil {
		return Schedule{}, err
	}
	if err = checkEditExpectation(patch.Expected, patch.Title, patch.Description, patch.Content, x.Title, x.Description, x.Content); err != nil {
		return Schedule{}, err
	}
	if patch.Title != nil {
		x.Title = compactWhitespace(*patch.Title)
	}
	if patch.Description != nil {
		x.Description = compactWhitespace(*patch.Description)
	}
	if patch.Content != nil {
		x.Content = *patch.Content
	}
	x.UpdatedAt = now()
	if _, err = tx.Exec(`UPDATE schedules SET title=?,description=?,content=?,updated_at=? WHERE id=?`, x.Title, x.Description, x.Content, x.UpdatedAt, id); err != nil {
		return Schedule{}, err
	}
	if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, x.UpdatedAt); err != nil {
		return Schedule{}, err
	}
	if err = tx.Commit(); err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(id)
}
