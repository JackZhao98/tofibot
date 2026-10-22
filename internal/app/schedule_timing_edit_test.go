package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func sp[T any](v T) *T { return &v }

func timingTestSchedule(t *testing.T, spec ScheduleSpec) (*Store, Schedule) {
	t.Helper()
	store, bot, c := scheduleTestStore(t)
	t.Cleanup(func() { store.Close() })
	if spec.Title == "" {
		spec.Title, spec.Description = "Report", "Daily report"
	}
	if spec.Content == "" {
		spec.Content = "Write the synthetic report."
	}
	if spec.Timezone == "" {
		spec.Timezone = "UTC"
	}
	x, err := store.CreateSchedule(c.ID, bot.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	return store, x
}

func dailyTimingSpec(clock string) ScheduleSpec {
	return ScheduleSpec{Kind: scheduleDaily, DailyTime: clock, Timezone: "UTC"}
}

func mustNext(t *testing.T, x Schedule) time.Time {
	t.Helper()
	at, err := parseStoredTime(x.NextAtUTC)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestPatchScheduleDailyTimeRecomputesNext(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("02:30"))
	before := time.Now().UTC()
	got, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00")})
	if err != nil {
		t.Fatal(err)
	}
	next := mustNext(t, got)
	if next.Format("15:04") != "06:00" || !next.After(before) || next.Sub(before) > 24*time.Hour+time.Minute {
		t.Fatalf("next_at %s not the coming 06:00 UTC (now %s)", next, before)
	}
	if got.Kind != scheduleDaily || got.DailyTime != "06:00" || got.Status != scheduleActive || got.Content != x.Content || got.Title != x.Title {
		t.Fatalf("edit changed more than timing: %+v", got)
	}
	// Restating the stored timing is not a change: no recompute, no new journal row.
	token := scheduleAuthorizationToken(t, store, x.ID)
	again, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00"), Kind: sp("daily")})
	if err != nil || again.NextAtUTC != got.NextAtUTC || scheduleAuthorizationToken(t, store, x.ID) != token {
		t.Fatalf("unchanged timing recomputed: %+v %v", again, err)
	}
}

func TestPatchScheduleTimezoneMovesWallClock(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("06:00"))
	got, err := store.PatchSchedule(x.ID, SchedulePatch{Timezone: sp("America/Los_Angeles")})
	if err != nil {
		t.Fatal(err)
	}
	loc := weeklyLoc(t)
	if next := mustNext(t, got).In(loc); next.Format("15:04") != "06:00" || got.Timezone != "America/Los_Angeles" {
		t.Fatalf("wall clock not kept in the new zone: %s %+v", next, got)
	}
}

func TestPatchScheduleWeeklyWeekdays(t *testing.T) {
	spec := weeklySpec("sun")
	store, x := timingTestSchedule(t, spec)
	loc := weeklyLoc(t)
	got, err := store.PatchSchedule(x.ID, SchedulePatch{Weekdays: &[]string{"WED", "mon"}, DailyTime: sp("08:15")})
	if err != nil {
		t.Fatal(err)
	}
	next := mustNext(t, got).In(loc)
	if !reflect.DeepEqual(got.Weekdays, []string{"mon", "wed"}) || got.DailyTime != "08:15" || (next.Weekday() != time.Monday && next.Weekday() != time.Wednesday) || next.Format("15:04") != "08:15" || !next.After(time.Now()) {
		t.Fatalf("weekly edit: %+v next=%s", got, next)
	}
	// daily -> weekly needs the weekdays; weekly -> daily drops them.
	if _, err := store.PatchSchedule(x.ID, SchedulePatch{Kind: sp("daily")}); err != nil {
		t.Fatalf("weekly to daily: %v", err)
	}
	d, _ := store.GetSchedule(x.ID)
	if d.Kind != scheduleDaily || len(d.Weekdays) != 0 || d.DailyTime != "08:15" {
		t.Fatalf("weekly->daily kept weekdays: %+v", d)
	}
	_, err = store.PatchSchedule(x.ID, SchedulePatch{Kind: sp("weekly")})
	if scheduleErrorCode(err, "") != "schedule_weekdays_invalid" {
		t.Fatalf("daily->weekly without weekdays: %v", err)
	}
	if w, err := store.PatchSchedule(x.ID, SchedulePatch{Kind: sp("weekly"), Weekdays: &[]string{"fri"}, DailyTime: sp("07:00")}); err != nil || w.Kind != scheduleWeekly || !reflect.DeepEqual(w.Weekdays, []string{"fri"}) {
		t.Fatalf("daily->weekly: %+v %v", w, err)
	}
}

func TestPatchScheduleInterval(t *testing.T) {
	store, x := timingTestSchedule(t, ScheduleSpec{Kind: scheduleInterval, IntervalSeconds: 3600, RunAt: time.Now().Add(10 * time.Hour).Format(time.RFC3339Nano)})
	before := time.Now().UTC()
	got, err := store.PatchSchedule(x.ID, SchedulePatch{IntervalSeconds: sp(int64(7200))})
	if err != nil {
		t.Fatal(err)
	}
	next := mustNext(t, got)
	if got.IntervalSeconds != 7200 || next.Before(before.Add(2*time.Hour-time.Second)) || next.After(time.Now().Add(2*time.Hour+time.Minute)) {
		t.Fatalf("interval edit: %+v next=%s", got, next)
	}
	// An explicit run_at anchors the first run of the new cadence.
	anchor := time.Now().Add(5 * time.Hour).UTC().Truncate(time.Second)
	got, err = store.PatchSchedule(x.ID, SchedulePatch{IntervalSeconds: sp(int64(1800)), RunAt: sp(anchor.Format(time.RFC3339))})
	if err != nil || !mustNext(t, got).Equal(anchor) {
		t.Fatalf("run_at anchor: %+v %v", got, err)
	}
	// A timezone-only edit relabels the zone and keeps the instant.
	kept := got.NextAtUTC
	got, err = store.PatchSchedule(x.ID, SchedulePatch{Timezone: sp("Asia/Tokyo")})
	if err != nil || got.NextAtUTC != kept || got.Timezone != "Asia/Tokyo" {
		t.Fatalf("zone-only edit moved the instant: %+v %v", got, err)
	}
	// daily -> interval without run_at starts one interval from now.
	d, dailyX := timingTestSchedule(t, dailyTimingSpec("02:30"))
	got, err = d.PatchSchedule(dailyX.ID, SchedulePatch{Kind: sp("interval"), IntervalSeconds: sp(int64(600))})
	if err != nil || got.Kind != scheduleInterval || got.DailyTime != "" || mustNext(t, got).Sub(time.Now()) > 10*time.Minute+time.Minute {
		t.Fatalf("daily->interval: %+v %v", got, err)
	}
}

func TestPatchScheduleOnceRunAt(t *testing.T) {
	store, x := timingTestSchedule(t, ScheduleSpec{Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
	at := time.Now().Add(26 * time.Hour).UTC().Truncate(time.Second)
	got, err := store.PatchSchedule(x.ID, SchedulePatch{RunAt: sp(at.Format(time.RFC3339))})
	if err != nil || !mustNext(t, got).Equal(at) || got.Kind != scheduleOnce {
		t.Fatalf("once edit: %+v %v", got, err)
	}
	// once -> daily drops run_at semantics and computes the next wall clock.
	got, err = store.PatchSchedule(x.ID, SchedulePatch{Kind: sp("daily"), DailyTime: sp("09:00")})
	if err != nil || got.Kind != scheduleDaily || mustNext(t, got).Format("15:04") != "09:00" {
		t.Fatalf("once->daily: %+v %v", got, err)
	}
}

func TestPatchScheduleInvalidTimingGivesStableCodes(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("02:30"))
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	for name, c := range map[string]struct {
		patch SchedulePatch
		code  string
	}{
		"unknown kind":         {SchedulePatch{Kind: sp("monthly")}, "schedule_kind_invalid"},
		"bad clock":            {SchedulePatch{DailyTime: sp("25:61")}, "schedule_time_invalid"},
		"empty clock":          {SchedulePatch{DailyTime: sp("")}, "schedule_time_invalid"},
		"unknown timezone":     {SchedulePatch{Timezone: sp("Mars/Base")}, "schedule_timezone_invalid"},
		"empty timezone":       {SchedulePatch{Timezone: sp(" ")}, "schedule_timezone_invalid"},
		"weekdays on daily":    {SchedulePatch{Weekdays: &[]string{"mon"}}, "schedule_timing_conflict"},
		"run_at on daily":      {SchedulePatch{RunAt: sp("2099-01-01T00:00:00Z")}, "schedule_timing_conflict"},
		"interval on daily":    {SchedulePatch{IntervalSeconds: sp(int64(600))}, "schedule_timing_conflict"},
		"once without run_at":  {SchedulePatch{Kind: sp("once")}, "schedule_run_at_required"},
		"once in the past":     {SchedulePatch{Kind: sp("once"), RunAt: &past}, "schedule_run_at_past"},
		"once unreadable":      {SchedulePatch{Kind: sp("once"), RunAt: sp("tomorrow-ish")}, "schedule_run_at_invalid"},
		"interval too short":   {SchedulePatch{Kind: sp("interval"), IntervalSeconds: sp(int64(5))}, "schedule_interval_invalid"},
		"interval too long":    {SchedulePatch{Kind: sp("interval"), IntervalSeconds: sp(int64(366 * 24 * 3600))}, "schedule_interval_invalid"},
		"weekly bad weekday":   {SchedulePatch{Kind: sp("weekly"), Weekdays: &[]string{"funday"}}, "schedule_weekdays_invalid"},
		"weekly empty weekday": {SchedulePatch{Kind: sp("weekly"), Weekdays: &[]string{}}, "schedule_weekdays_invalid"},
	} {
		_, err := store.PatchSchedule(x.ID, c.patch)
		var se *ScheduleError
		if !errors.As(err, &se) || se.Code != c.code {
			t.Errorf("%s: got %v, want code %s", name, err, c.code)
		}
	}
	after, _ := store.GetSchedule(x.ID)
	if after.NextAtUTC != x.NextAtUTC || after.DailyTime != "02:30" || after.UpdatedAt != x.UpdatedAt || scheduleAuthorizationToken(t, store, x.ID) != 1 {
		t.Fatalf("rejected edits changed the schedule: %+v", after)
	}
}

func TestPatchScheduleTimingLeavesClaimedOccurrenceUntouched(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("02:30"))
	due := mustNext(t, x)
	runs, err := store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim %v %v", runs, err)
	}
	read := func() (string, string, string, string) {
		var runStatus, scheduledFor, snapshot, trigger string
		if err := store.db.QueryRow(`SELECT r.status,o.scheduled_for_utc,a.snapshot_json,r.trigger_message_id FROM runs r JOIN schedule_occurrences o ON o.run_id=r.id JOIN schedule_occurrence_authorizations a ON a.root_run_id=r.id WHERE r.id=?`, runs[0].ID).Scan(&runStatus, &scheduledFor, &snapshot, &trigger); err != nil {
			t.Fatal(err)
		}
		return runStatus, scheduledFor, snapshot, trigger
	}
	a1, a2, a3, a4 := read()
	var messageCount int
	store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=?`, runs[0].ID).Scan(&messageCount)
	if _, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00")}); err != nil {
		t.Fatal(err)
	}
	b1, b2, b3, b4 := read()
	var after int
	store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=?`, runs[0].ID).Scan(&after)
	if a1 != b1 || a2 != b2 || a3 != b3 || a4 != b4 || messageCount != after || b1 != "queued" {
		t.Fatalf("claimed occurrence rewritten: %s/%s -> %s/%s", a1, a2, b1, b2)
	}
	// The in-flight run keeps blocking a second claim, then the new time applies.
	moved, _ := store.GetSchedule(x.ID)
	if next := mustNext(t, moved); next.Format("15:04") != "06:00" {
		t.Fatalf("next_at %s", next)
	}
	if again, _ := store.ClaimDueSchedules(mustNext(t, moved).Add(time.Second)); len(again) != 0 {
		t.Fatal("claimed a second occurrence while the first was queued")
	}

	// A one-time schedule that was claimed cannot be rescheduled, nor can a
	// completed one; the in-flight occurrence is not cancelled either way.
	once, once1 := timingTestSchedule(t, ScheduleSpec{Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339Nano)})
	claimed, err := once.ClaimDueSchedules(mustNext(t, once1))
	if err != nil || len(claimed) != 1 {
		t.Fatalf("once claim %v %v", claimed, err)
	}
	_, err = once.PatchSchedule(once1.ID, SchedulePatch{RunAt: sp(time.Now().Add(5 * time.Hour).Format(time.RFC3339))})
	if scheduleErrorCode(err, "") != "schedule_timing_locked" {
		t.Fatalf("claimed once rescheduled: %v", err)
	}
	if st, _ := once.GetRun(claimed[0].ID); st.Status != "queued" {
		t.Fatalf("claimed run changed: %s", st.Status)
	}
	if _, err := once.db.Exec(`UPDATE schedules SET status='completed' WHERE id=?`, once1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := once.PatchSchedule(once1.ID, SchedulePatch{Timezone: sp("Asia/Tokyo")}); scheduleErrorCode(err, "") != "schedule_timing_locked" {
		t.Fatalf("completed schedule retimed: %v", err)
	}
	// Content edits on it still work.
	if _, err := once.PatchSchedule(once1.ID, SchedulePatch{Title: sp("Renamed"), Description: sp("Still editable")}); err != nil {
		t.Fatal(err)
	}
}

func TestPatchSchedulePausedStaysPaused(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("02:30"))
	if _, err := store.PauseSchedule(x.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != schedulePaused || mustNext(t, got).Format("15:04") != "06:00" {
		t.Fatalf("paused edit: %+v", got)
	}
	if runs, _ := store.ClaimDueSchedules(mustNext(t, got).Add(time.Hour)); len(runs) != 0 {
		t.Fatal("paused schedule fired")
	}
}

func TestPatchScheduleTimingExpectedBaseline(t *testing.T) {
	store, x := timingTestSchedule(t, dailyTimingSpec("02:30"))
	stale := &ScheduleTimingBaseline{Kind: sp("daily"), DailyTime: sp("02:30"), Timezone: sp("UTC")}
	// Someone else moves it first.
	if _, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("03:00")}); err != nil {
		t.Fatal(err)
	}
	before, _ := store.GetSchedule(x.ID)
	_, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00"), ExpectedTiming: stale})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale timing baseline not a conflict: %v", err)
	}
	if after, _ := store.GetSchedule(x.ID); after.NextAtUTC != before.NextAtUTC || after.DailyTime != "03:00" {
		t.Fatalf("conflicting edit applied: %+v", after)
	}
	// The content baseline still guards a combined edit.
	_, err = store.PatchSchedule(x.ID, SchedulePatch{Content: sp("New instructions."), DailyTime: sp("06:00"), Expected: &EditBaseline{Content: sp("stale content")}})
	if !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale content baseline: %v", err)
	}
	fresh := &ScheduleTimingBaseline{Kind: sp("daily"), DailyTime: sp("03:00"), Timezone: sp("UTC")}
	if got, err := store.PatchSchedule(x.ID, SchedulePatch{DailyTime: sp("06:00"), ExpectedTiming: fresh, Expected: &EditBaseline{Content: sp(x.Content)}, Content: sp("New instructions.")}); err != nil || got.DailyTime != "06:00" || got.Content != "New instructions." {
		t.Fatalf("matching baselines: %+v %v", got, err)
	}
}

func TestPatchScheduleTimingAuthorizationHTTPForm(t *testing.T) {
	server, dir := ownerTestServer(t)
	server.accountID = "synthetic-owner-account"
	bot, err := server.store.CreateBot("Synthetic timing bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	cookie := ownerSetup(t, server, dir)
	create := map[string]any{"bot_id": bot.ID, "content": "Write the synthetic report.", "title": "Report", "description": "Daily report", "kind": "daily", "daily_time": "02:30", "timezone": "UTC"}
	response := ownerCall(server, http.MethodPost, "/api/conversations/"+bot.DMConversationID+"/schedules", create, cookie, true)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var x Schedule
	if err := json.Unmarshal(response.Body.Bytes(), &x); err != nil {
		t.Fatal(err)
	}
	patch := map[string]any{"daily_time": "06:00", "expected_timing": map[string]any{"kind": "daily", "daily_time": "02:30", "timezone": "UTC"}}
	if denied := ownerCall(server, http.MethodPatch, "/api/schedules/"+x.ID, patch, nil, true); denied.Code < 400 {
		t.Fatalf("unauthenticated timing edit accepted: %d", denied.Code)
	}
	response = ownerCall(server, http.MethodPatch, "/api/schedules/"+x.ID, patch, cookie, true)
	if response.Code != http.StatusOK {
		t.Fatalf("timing edit: %d %s", response.Code, response.Body.String())
	}
	var got Schedule
	_ = json.Unmarshal(response.Body.Bytes(), &got)
	if got.DailyTime != "06:00" || mustNext(t, got).Format("15:04") != "06:00" {
		t.Fatalf("response %+v", got)
	}
	edit, err := readScheduleAuthorizationRevision(server.store.db, x.ID, 2)
	if err != nil || edit.EventKind != "content_edit" || edit.SourceKind != scheduleSourceForm || edit.ExecutionSpec.DailyTime != "06:00" || edit.ExecutionSpec.InitialAtUTC != x.NextAtUTC {
		t.Fatalf("timing edit journal %+v %v", edit, err)
	}
	if _, _, _, err := readBoundScheduleAuthorization(server.store.db, x.ID, edit.AccountID); err != nil {
		t.Fatalf("authorization no longer binds the edited schedule: %v", err)
	}
	// Stale baseline -> 409 edit_conflict; invalid timing -> 400 with its code; the
	// error body carries codes, and the schedule is unchanged by both.
	response = ownerCall(server, http.MethodPatch, "/api/schedules/"+x.ID, patch, cookie, true)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"edit_conflict"`) {
		t.Fatalf("stale baseline: %d %s", response.Code, response.Body.String())
	}
	response = ownerCall(server, http.MethodPatch, "/api/schedules/"+x.ID, map[string]any{"daily_time": "99:99"}, cookie, true)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"schedule_time_invalid"`) {
		t.Fatalf("invalid timing: %d %s", response.Code, response.Body.String())
	}
	if now, _ := server.store.GetSchedule(x.ID); now.DailyTime != "06:00" || scheduleAuthorizationToken(t, server.store, x.ID) != 2 {
		t.Fatalf("rejected edits changed state: %+v", now)
	}
}

func TestPatchScheduleBotToolEditsTimingOfBotCreatedSchedule(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	_, run, _, err := store.AddUserRun(c.ID, bot.ID, "Report daily at 02:30 UTC, and move it later if I ask.", "timing-tool-source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	run, _ = store.GetRun(run.ID)
	server := &Server{store: store, accountID: "synthetic-timing-account"}
	out, err := executeScheduleTool(t, server, c, run, "create_schedule", map[string]any{"title": "Report", "description": "Daily report", "content": "Write the synthetic report.", "kind": "daily", "daily_time": "02:30", "timezone": "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	var created Schedule
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.CreatedBy != "bot" {
		t.Fatalf("created %+v %v", created, err)
	}
	// Content-only edit keeps timing.
	if _, err := executeScheduleTool(t, server, c, run, "update_schedule", map[string]any{"schedule_id": created.ID, "title": "Report", "description": "Daily report", "content": "Write the new synthetic report."}); err != nil {
		t.Fatal(err)
	}
	if kept, _ := store.GetSchedule(created.ID); kept.NextAtUTC != created.NextAtUTC || kept.DailyTime != "02:30" {
		t.Fatalf("content edit moved timing: %+v", kept)
	}
	out, err = executeScheduleTool(t, server, c, run, "update_schedule", map[string]any{"schedule_id": created.ID, "title": "Report", "description": "Daily report", "daily_time": "06:00"})
	if err != nil {
		t.Fatal(err)
	}
	var moved Schedule
	if err := json.Unmarshal([]byte(out), &moved); err != nil || moved.DailyTime != "06:00" || mustNext(t, moved).Format("15:04") != "06:00" || !strings.Contains(out, "next_at_local") {
		t.Fatalf("tool timing edit: %s %v", out, err)
	}
	edit, err := readScheduleAuthorizationRevision(store.db, created.ID, 3)
	if err != nil || edit.SourceKind != scheduleSourceChat || edit.EventKind != "content_edit" || edit.ExecutionSpec.DailyTime != "06:00" {
		t.Fatalf("chat timing edit journal %+v %v", edit, err)
	}
	if _, err := executeScheduleTool(t, server, c, run, "update_schedule", map[string]any{"schedule_id": created.ID, "title": "Report", "description": "Daily report", "daily_time": "nope"}); scheduleErrorCode(err, "") != "schedule_time_invalid" {
		t.Fatalf("tool invalid timing: %v", err)
	}
	// A schedule in another conversation is out of reach.
	other, err := store.CreateBot("other bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateSchedule(other.DMConversationID, other.ID, dailyTimingSpec("01:00").withContent())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executeScheduleTool(t, server, c, run, "update_schedule", map[string]any{"schedule_id": foreign.ID, "title": "x", "description": "y", "daily_time": "05:00"}); err == nil {
		t.Fatal("edited a schedule outside the conversation")
	}
}

func (s ScheduleSpec) withContent() ScheduleSpec {
	s.Title, s.Description, s.Content = "Other", "Other job", "Do the other thing."
	return s
}

func TestScheduledMCPWriteSurvivesTimingEdit(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	f.setStatus(f.root.ID, "done", "")
	_, run, _, err := f.s.store.AddUserRun(f.c.ID, f.bot.ID, "Move the schedule to run every day at 06:00 UTC.", "synthetic-schedule-timing-edit")
	if err != nil {
		t.Fatal(err)
	}
	f.setStatus(run.ID, "running", "")
	if run, err = f.s.store.GetRun(run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.store.patchScheduleWithSource(f.schedule.ID, SchedulePatch{Kind: sp("daily"), DailyTime: sp("06:00"), Timezone: sp("UTC")}, f.s.scheduleChatSource(f.c, run)); err != nil {
		t.Fatal(err)
	}
	f.setStatus(run.ID, "done", "")
	f.nextOccurrence()
	record, err := readScheduleOccurrenceAuthorization(f.s.store.db, f.root.ID)
	if err != nil || record.Revision != 2 || record.Snapshot.ExecutionSpec.Kind != scheduleDaily {
		t.Fatalf("occurrence %+v %v", record, err)
	}
	if _, _, err := f.s.readMCPReviewContext(f.s.store.db, f.c, f.r); err != nil {
		t.Fatalf("occurrence after a timing edit lost automatic eligibility: %v", err)
	}
	q := f.approvedQuestion()
	if err := f.claimEffect(q.ID); err != nil {
		t.Fatal(err)
	}
	if f.effects.Load() != 1 {
		t.Fatal("automatic write did not execute once")
	}
}
