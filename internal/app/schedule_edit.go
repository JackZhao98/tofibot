package app

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// SchedulePatch edits a schedule. Timing fields are optional and use the same
// names and meanings as ScheduleSpec; ExpectedTiming is the opt-in baseline
// for them, as Expected is for title, description and content.
type SchedulePatch struct {
	Title           *string                 `json:"title"`
	Description     *string                 `json:"description"`
	Content         *string                 `json:"content"`
	Kind            *string                 `json:"kind,omitempty"`
	RunAt           *string                 `json:"run_at,omitempty"`
	IntervalSeconds *int64                  `json:"interval_seconds,omitempty"`
	DailyTime       *string                 `json:"daily_time,omitempty"`
	Weekdays        *[]string               `json:"weekdays,omitempty"`
	Timezone        *string                 `json:"timezone,omitempty"`
	Expected        *EditBaseline           `json:"expected,omitempty"`
	ExpectedTiming  *ScheduleTimingBaseline `json:"expected_timing,omitempty"`
}

// ScheduleTimingBaseline holds the timing the editor opened with. Any field
// that is set must still equal the stored value when a timing change is
// applied, else the edit conflicts (ErrEditConflict). NextAtUTC is compared
// for one-time schedules only, because a recurring cursor advances on its own.
type ScheduleTimingBaseline struct {
	Kind            *string   `json:"kind,omitempty"`
	Timezone        *string   `json:"timezone,omitempty"`
	IntervalSeconds *int64    `json:"interval_seconds,omitempty"`
	DailyTime       *string   `json:"daily_time,omitempty"`
	Weekdays        *[]string `json:"weekdays,omitempty"`
	NextAtUTC       *string   `json:"next_at_utc,omitempty"`
}

func (p SchedulePatch) hasTiming() bool {
	return p.Kind != nil || p.RunAt != nil || p.IntervalSeconds != nil || p.DailyTime != nil || p.Weekdays != nil || p.Timezone != nil
}

func checkTimingExpectation(b *ScheduleTimingBaseline, x Schedule) error {
	if b == nil {
		return nil
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"kind", b.Kind == nil || *b.Kind == x.Kind},
		{"timezone", b.Timezone == nil || *b.Timezone == x.Timezone},
		{"interval_seconds", b.IntervalSeconds == nil || *b.IntervalSeconds == x.IntervalSeconds},
		{"daily_time", b.DailyTime == nil || *b.DailyTime == x.DailyTime},
		{"weekdays", b.Weekdays == nil || sameStrings(*b.Weekdays, x.Weekdays)},
		{"next_at_utc", b.NextAtUTC == nil || x.Kind != scheduleOnce || *b.NextAtUTC == x.NextAtUTC},
	} {
		if !c.ok {
			return fmt.Errorf("%w: %s", ErrEditConflict, c.name)
		}
	}
	return nil
}

func clockKind(kind string) bool { return kind == scheduleDaily || kind == scheduleWeekly }

func sameStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// planScheduleTiming merges a timing patch onto the stored schedule and runs
// the creation validation and next-time calculation (validateScheduleSpec) on
// the result; it has no calculator of its own. changed is false when the patch
// restates the stored timing, in which case nothing is recomputed.
//
// Fields that do not belong to the effective kind are dropped when the kind
// changes, so the caller names only what the new kind needs. When kind stays
// the same, unset fields keep their stored value. An interval schedule given no
// run_at first runs one interval from now. A timezone-only change on a
// once or interval schedule keeps its instant and only relabels the zone.
func planScheduleTiming(x Schedule, p SchedulePatch, at time.Time) (spec ScheduleSpec, next time.Time, changed bool, err error) {
	kind := x.Kind
	if p.Kind != nil {
		kind = strings.ToLower(strings.TrimSpace(*p.Kind))
	}
	kindChanged := kind != x.Kind
	spec = ScheduleSpec{Content: x.Content, Kind: kind, Timezone: x.Timezone}
	if !kindChanged {
		spec.IntervalSeconds, spec.DailyTime = x.IntervalSeconds, x.DailyTime
		spec.Weekdays = append([]string(nil), x.Weekdays...)
	} else if clockKind(kind) && clockKind(x.Kind) {
		spec.DailyTime = x.DailyTime // daily and weekly share the wall-clock time
	}
	if p.Timezone != nil {
		if spec.Timezone = strings.TrimSpace(*p.Timezone); spec.Timezone == "" {
			return spec, time.Time{}, false, schedErr("schedule_timezone_invalid", "timezone must not be empty")
		}
	}
	if p.RunAt != nil {
		spec.RunAt = *p.RunAt
	}
	if p.IntervalSeconds != nil {
		spec.IntervalSeconds = *p.IntervalSeconds
	}
	if p.DailyTime != nil {
		spec.DailyTime = strings.TrimSpace(*p.DailyTime)
	}
	if p.Weekdays != nil {
		spec.Weekdays = append([]string(nil), (*p.Weekdays)...)
	}
	days, daysErr := normalizeWeekdays(spec.Weekdays)
	weekdaysChanged := p.Weekdays != nil && (daysErr != nil || !sameStrings(days, x.Weekdays))
	changed = kindChanged || p.RunAt != nil || weekdaysChanged ||
		(p.IntervalSeconds != nil && *p.IntervalSeconds != x.IntervalSeconds) ||
		(p.DailyTime != nil && spec.DailyTime != x.DailyTime) ||
		(p.Timezone != nil && spec.Timezone != x.Timezone)
	if !changed {
		return spec, time.Time{}, false, nil
	}
	instantOnly := !kindChanged && p.RunAt == nil && (kind == scheduleOnce || kind == scheduleInterval) &&
		(p.IntervalSeconds == nil || *p.IntervalSeconds == x.IntervalSeconds)
	if instantOnly {
		if _, e := time.LoadLocation(spec.Timezone); e != nil {
			return spec, time.Time{}, true, schedErr("schedule_timezone_invalid", fmt.Sprintf("unknown timezone %q", spec.Timezone))
		}
		next, e := parseStoredTime(x.NextAtUTC)
		return spec, next, true, e
	}
	if kind == scheduleInterval && strings.TrimSpace(spec.RunAt) == "" {
		step := time.Duration(spec.IntervalSeconds) * time.Second
		if step < minScheduleInterval || step > maxScheduleInterval {
			step = minScheduleInterval // out of range: let validation name interval_seconds
		}
		spec.RunAt = at.Add(step).UTC().Format(time.RFC3339Nano)
	}
	spec, next, err = validateScheduleSpec(spec, at)
	return spec, next, true, err
}

// PatchSchedule changes title, description and content, and optionally the
// timing (kind, run_at, interval_seconds, daily_time, weekdays, timezone).
// A timing change recomputes next_at_utc from now through the creation
// validation. It never rewrites or cancels an occurrence that is already
// claimed or running (a run in flight finishes as it was claimed), leaves a
// paused schedule paused, and is refused with schedule_timing_locked once a
// one-time schedule has been claimed or has completed.
func (s *Store) PatchSchedule(id string, patch SchedulePatch) (Schedule, error) {
	return s.patchScheduleWithSource(id, patch, nil)
}

func (s *Store) patchScheduleWithSource(id string, patch SchedulePatch, source *scheduleMutationSource) (Schedule, error) {
	if err := s.ensureSchedules(); err != nil {
		return Schedule{}, err
	}
	if patch.Title == nil && patch.Description == nil && patch.Content == nil && !patch.hasTiming() {
		return Schedule{}, errors.New("provide title, description, content or timing")
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
	x, err := scanSchedule(tx.QueryRow(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,weekdays,status,created_at,updated_at FROM schedules WHERE id=? AND status<>'deleted'`, id))
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
	contentChanged := patch.Content != nil && *patch.Content != x.Content
	var plan ScheduleSpec
	var planNext time.Time
	timingChanged := false
	if patch.hasTiming() {
		if err = checkTimingExpectation(patch.ExpectedTiming, x); err != nil {
			return Schedule{}, err
		}
		if plan, planNext, timingChanged, err = planScheduleTiming(x, patch, time.Now().UTC()); err != nil {
			return Schedule{}, err
		}
	}
	if timingChanged {
		locked := x.Status == scheduleComplete
		if x.Kind == scheduleOnce && !locked {
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM schedule_occurrences WHERE schedule_id=?)`, id).Scan(&locked); err != nil {
				return Schedule{}, err
			}
		}
		if locked {
			return Schedule{}, schedErr("schedule_timing_locked", "a one-time schedule that has run or been claimed cannot be rescheduled")
		}
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
	if timingChanged {
		previousNext := x.NextAtUTC
		x.Kind, x.Timezone, x.IntervalSeconds, x.DailyTime, x.Weekdays, x.NextAtUTC = plan.Kind, plan.Timezone, plan.IntervalSeconds, plan.DailyTime, plan.Weekdays, scheduleTime(planNext)
		// Status is untouched: a paused schedule stays paused with its new next_at.
		res, e := tx.Exec(`UPDATE schedules SET title=?,description=?,content=?,kind=?,timezone=?,next_at_utc=?,interval_seconds=?,daily_time=?,weekdays=?,updated_at=? WHERE id=? AND next_at_utc=?`, x.Title, x.Description, x.Content, x.Kind, x.Timezone, x.NextAtUTC, x.IntervalSeconds, x.DailyTime, strings.Join(x.Weekdays, ","), x.UpdatedAt, id, previousNext)
		if e != nil {
			return Schedule{}, e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return Schedule{}, fmt.Errorf("%w: timing", ErrEditConflict)
		}
	} else if _, err = tx.Exec(`UPDATE schedules SET title=?,description=?,content=?,updated_at=? WHERE id=?`, x.Title, x.Description, x.Content, x.UpdatedAt, id); err != nil {
		return Schedule{}, err
	}
	// Timing is part of the authorized execution scope, so a timing edit is
	// journaled like a content edit (same source rules, same event kind).
	if contentChanged || timingChanged {
		if err = appendScheduleAuthorizationTx(tx, x, "content_edit", x.UpdatedAt, source); err != nil {
			return Schedule{}, err
		}
	}
	if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, x.UpdatedAt); err != nil {
		return Schedule{}, err
	}
	if err = tx.Commit(); err != nil {
		return Schedule{}, err
	}
	return s.GetSchedule(id)
}
