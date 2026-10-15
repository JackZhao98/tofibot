package app

import (
	"fmt"
	"strings"
	"time"
)

// maxScheduleStates bounds the schedule states a scheduled run sees.
const maxScheduleStates = 12

// scheduleStateNote gives a scheduled run the current state of this
// conversation's schedules and of its Bot's own schedules, from records.
// Reports otherwise repeated statuses from old messages ("the 02:30 run
// failed") long after a later run succeeded.
func (s *Server) scheduleStateNote(c Conversation, r Run, loc *time.Location) string {
	if r.Kind != runKindSchedule && r.scheduleTask == nil {
		return ""
	}
	seen := map[string]bool{}
	var schedules []Schedule
	add := func(list []Schedule, err error) {
		if err != nil {
			return
		}
		for _, x := range list {
			if !seen[x.ID] {
				seen[x.ID] = true
				schedules = append(schedules, x)
			}
		}
	}
	add(s.store.ListSchedules(c.ID))
	add(s.store.ListBotSchedules(r.BotID, false))
	if len(schedules) == 0 {
		return ""
	}
	if len(schedules) > maxScheduleStates {
		schedules = schedules[:maxScheduleStates]
	}
	var b strings.Builder
	b.WriteString("[schedule states from records; newer than any earlier message]")
	for _, x := range schedules {
		name := strings.TrimSpace(x.Title)
		if name == "" {
			name = trimRunes(strings.Join(strings.Fields(x.Content), " "), 60)
		}
		when := x.Kind
		if x.DailyTime != "" {
			when += " " + x.DailyTime
		}
		state := "not run yet"
		if x.LastRunID != "" {
			state = "last run " + x.ExecutionStatus
			if last, err := s.store.GetRun(x.LastRunID); err == nil {
				if at, err := time.Parse(time.RFC3339Nano, last.UpdatedAt); err == nil {
					state += " at " + at.In(loc).Format("2006-01-02 15:04")
				}
			}
		}
		fmt.Fprintf(&b, "\n- %s (%s, %s): %s", name, when, x.Status, state)
	}
	b.WriteString("\n[/schedule states]")
	return b.String()
}
