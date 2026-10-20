package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func weeklyLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestNextWeeklyComputation(t *testing.T) {
	loc := weeklyLoc(t)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }
	cases := []struct {
		name  string
		after time.Time
		days  []string
		clock string
		want  time.Time
	}{
		{"single day later this week", at(2026, 10, 14, 9, 0), []string{"sun"}, "00:43", at(2026, 10, 18, 0, 43)},
		{"single day, same day but time already passed goes a week out", at(2026, 10, 18, 0, 43), []string{"sun"}, "00:43", at(2026, 10, 25, 0, 43)},
		{"single day, same day time still ahead", at(2026, 10, 18, 0, 10), []string{"sun"}, "00:43", at(2026, 10, 18, 0, 43)},
		{"multiple days picks the nearest", at(2026, 10, 14, 9, 0), []string{"mon", "wed", "fri"}, "08:00", at(2026, 10, 14, 8, 0).AddDate(0, 0, 2)},
		{"multiple days, after the last day wraps into next week", at(2026, 10, 16, 12, 0), []string{"mon", "wed", "fri"}, "08:00", at(2026, 10, 19, 8, 0)},
		{"crossing the week and month boundary", at(2026, 10, 31, 12, 0), []string{"mon"}, "09:00", at(2026, 11, 2, 9, 0)},
	}
	for _, c := range cases {
		got, err := nextWeekly(c.after.UTC(), loc, c.clock, c.days)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s: got %s want %s (%v)", c.name, got.In(loc), c.want, err)
		}
	}
	// The weekday is the schedule timezone's, not UTC's: Sunday 00:43 in
	// Los Angeles is still Saturday in UTC? No: it is 07:43Z Sunday, but
	// Saturday 23:30 local is already Sunday in UTC and must not count.
	got, err := nextWeekly(time.Date(2026, 10, 17, 23, 30, 0, 0, loc).UTC(), loc, "00:43", []string{"sun"})
	if err != nil || got.UTC().Format(time.RFC3339) != "2026-10-18T07:43:00Z" {
		t.Fatalf("local weekday: %s %v", got, err)
	}
}

func TestNextWeeklyDSTTransitionsInLosAngeles(t *testing.T) {
	loc := weeklyLoc(t)
	// Spring forward is Sunday 2026-03-08 at 02:00. The wall clock stays 01:30
	// on both sides, so the UTC gap between weeks shrinks by one hour.
	first := time.Date(2026, 3, 1, 1, 30, 0, 0, loc)
	second, err := nextWeeklyAfterOccurrence(first, loc, "01:30", []string{"sun"})
	if err != nil || second.UTC().Format(time.RFC3339) != "2026-03-08T09:30:00Z" {
		t.Fatalf("pre-transition: %s %v", second, err)
	}
	third, err := nextWeeklyAfterOccurrence(second, loc, "01:30", []string{"sun"})
	if err != nil || third.UTC().Format(time.RFC3339) != "2026-03-15T08:30:00Z" || third.Sub(second) != 7*24*time.Hour-time.Hour {
		t.Fatalf("post-transition keeps wall clock: %s %v", third, err)
	}
	// A nonexistent local time is skipped for that week, like daily.
	gap, err := nextWeekly(time.Date(2026, 3, 5, 12, 0, 0, 0, loc).UTC(), loc, "02:30", []string{"sun"})
	if err != nil || gap.In(loc).Day() != 15 || gap.In(loc).Hour() != 2 || gap.In(loc).Minute() != 30 {
		t.Fatalf("spring gap should skip to the next valid Sunday: %s %v", gap.In(loc), err)
	}
	// Fall back is Sunday 2026-11-01: 01:30 happens twice and fires once, at
	// the earlier instant; the next occurrence is the following week.
	fall, err := nextWeekly(time.Date(2026, 10, 28, 12, 0, 0, 0, loc).UTC(), loc, "01:30", []string{"sun"})
	if err != nil || fall.UTC().Format(time.RFC3339) != "2026-11-01T08:30:00Z" {
		t.Fatalf("fall fold earlier instant: %s %v", fall, err)
	}
	next, err := nextWeeklyAfterOccurrence(fall, loc, "01:30", []string{"sun"})
	if err != nil || next.UTC().Format(time.RFC3339) != "2026-11-08T09:30:00Z" {
		t.Fatalf("fall fold must not fire twice: %s %v", next, err)
	}
}

func weeklySpec(days ...string) ScheduleSpec {
	return ScheduleSpec{Title: "Weekly", Description: "Weekly job", Content: "Do the weekly thing.", Kind: scheduleWeekly, DailyTime: "00:43", Weekdays: days, Timezone: "America/Los_Angeles"}
}

func TestWeeklyScheduleValidation(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	got, next, err := validateScheduleSpec(weeklySpec("SUN", " mon"), now)
	if err != nil || !reflect.DeepEqual(got.Weekdays, []string{"mon", "sun"}) || next.UTC().Format(time.RFC3339) != "2026-10-18T07:43:00Z" {
		t.Fatalf("normalized spec %+v next=%s err=%v", got, next, err)
	}
	for name, spec := range map[string]ScheduleSpec{
		"bad weekday":       weeklySpec("funday"),
		"full name":         weeklySpec("sunday"),
		"empty weekdays":    weeklySpec(),
		"duplicate":         weeklySpec("sun", "sun"),
		"missing time":      {Content: "x", Kind: scheduleWeekly, Weekdays: []string{"sun"}, Timezone: "UTC"},
		"bad time":          {Content: "x", Kind: scheduleWeekly, Weekdays: []string{"sun"}, DailyTime: "25:00", Timezone: "UTC"},
		"run_at on weekly":  {Content: "x", Kind: scheduleWeekly, Weekdays: []string{"sun"}, DailyTime: "08:00", RunAt: "2030-01-01T00:00:00Z", Timezone: "UTC"},
		"weekdays on daily": {Content: "x", Kind: scheduleDaily, Weekdays: []string{"sun"}, DailyTime: "08:00", Timezone: "UTC"},
	} {
		if _, _, err := validateScheduleSpec(spec, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Daily is unchanged and stores no weekdays.
	if d, _, err := validateScheduleSpec(ScheduleSpec{Content: "x", Kind: scheduleDaily, DailyTime: "08:00", Timezone: "UTC"}, now); err != nil || len(d.Weekdays) != 0 {
		t.Fatalf("daily regressed: %+v %v", d, err)
	}
}

func executeScheduleTool(t *testing.T, server *Server, c Conversation, r Run, name string, input any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(input)
	for _, tool := range server.scheduleTools(c, r) {
		if tool.Name == name {
			return tool.Execute(context.Background(), raw)
		}
	}
	t.Fatalf("tool %s missing", name)
	return "", nil
}

func TestWeeklyToolSchemaCreateUpdateList(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	_, run, _, err := store.AddUserRun(c.ID, bot.ID, "Every Sunday at 00:43 do the weekly thing.", "weekly-tool-source")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, accountID: "synthetic-weekly-account"}
	var create Tool
	var update Tool
	for _, tool := range server.scheduleTools(c, run) {
		switch tool.Name {
		case "create_schedule":
			create = tool
		case "update_schedule":
			update = tool
		}
	}
	schema, _ := json.Marshal(create.Parameters)
	for _, want := range []string{`"weekly"`, `"weekdays"`, `"sun"`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("create_schedule schema lacks %s: %s", want, schema)
		}
	}
	if !strings.Contains(create.Description, "weekly") || !strings.Contains(create.Description, "never create a daily schedule whose instructions check the weekday") {
		t.Errorf("create_schedule description does not steer toward weekly: %s", create.Description)
	}
	if !strings.Contains(update.Description, "weekdays") {
		t.Errorf("update_schedule description should say recurrence is fixed: %s", update.Description)
	}

	body := map[string]any{"title": "Weekly", "description": "Sunday job", "content": "Do the weekly thing.", "kind": "weekly", "daily_time": "00:43", "weekdays": []string{"sun"}, "timezone": "America/Los_Angeles"}
	for _, bad := range []map[string]any{
		{"weekdays": []string{"funday"}},
		{"weekdays": []string{}},
		{"weekdays": nil},
	} {
		in := map[string]any{}
		for k, v := range body {
			in[k] = v
		}
		for k, v := range bad {
			in[k] = v
		}
		if _, err := executeScheduleTool(t, server, c, run, "create_schedule", in); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if items, _ := store.ListSchedules(c.ID); len(items) != 0 {
		t.Fatalf("rejected input created %d schedules", len(items))
	}
	out, err := executeScheduleTool(t, server, c, run, "create_schedule", body)
	if err != nil {
		t.Fatal(err)
	}
	var created Schedule
	if err := json.Unmarshal([]byte(out), &created); err != nil || created.Kind != "weekly" || !reflect.DeepEqual(created.Weekdays, []string{"sun"}) || created.DailyTime != "00:43" {
		t.Fatalf("created %+v %v", created, err)
	}
	if !strings.Contains(out, `"weekdays":["sun"]`) {
		t.Fatalf("API JSON lacks weekdays: %s", out)
	}
	next, _ := parseStoredTime(created.NextAtUTC)
	if next.In(weeklyLoc(t)).Weekday() != time.Sunday {
		t.Fatalf("first run is not a Sunday: %s", next)
	}
	listed, err := executeScheduleTool(t, server, c, run, "list_schedules", map[string]any{})
	if err != nil || !strings.Contains(listed, `"weekdays":["sun"]`) || !strings.Contains(listed, `"kind":"weekly"`) {
		t.Fatalf("list_schedules: %s %v", listed, err)
	}
	// update_schedule edits text only; timing and weekdays are preserved.
	if _, err := executeScheduleTool(t, server, c, run, "update_schedule", map[string]any{"schedule_id": created.ID, "title": "Renamed", "description": "Sunday job", "content": "Do the new thing.", "weekdays": []string{"mon"}}); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetSchedule(created.ID)
	if after.Title != "Renamed" || !reflect.DeepEqual(after.Weekdays, []string{"sun"}) || after.NextAtUTC != created.NextAtUTC || after.DailyTime != "00:43" {
		t.Fatalf("update changed recurrence: %+v", after)
	}
}

func TestWeeklyClaimFiresOnceOnTheRightDay(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	loc := weeklyLoc(t)
	x, err := store.CreateSchedule(c.ID, bot.ID, weeklySpec("sun"))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := parseStoredTime(x.NextAtUTC)
	if first.In(loc).Weekday() != time.Sunday || first.In(loc).Format("15:04") != "00:43" {
		t.Fatalf("first occurrence %s", first.In(loc))
	}
	// Nothing fires the minute before.
	if runs, err := store.ClaimDueSchedules(first.Add(-time.Minute)); err != nil || len(runs) != 0 {
		t.Fatalf("early claim %v %v", runs, err)
	}
	runs, err := store.ClaimDueSchedules(first)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim %v %v", runs, err)
	}
	if again, err := store.ClaimDueSchedules(first.Add(time.Second)); err != nil || len(again) != 0 {
		t.Fatalf("double fire %v %v", again, err)
	}
	got, _ := store.GetSchedule(x.ID)
	next, _ := parseStoredTime(got.NextAtUTC)
	if next.In(loc).Weekday() != time.Sunday || !next.After(first) || next.Sub(first) > 7*24*time.Hour+time.Hour {
		t.Fatalf("next occurrence %s", next.In(loc))
	}
	// Missed occurrences coalesce into one late fire, then resume on schedule.
	if _, err := store.db.Exec(`UPDATE runs SET status='done' WHERE id=?`, runs[0].ID); err != nil {
		t.Fatal(err)
	}
	makeDue(t, store, x.ID, first.AddDate(0, 0, -21))
	late := first.Add(36 * time.Hour)
	runs, err = store.ClaimDueSchedules(late)
	if err != nil || len(runs) != 1 {
		t.Fatalf("coalesced claim %v %v", runs, err)
	}
	if again, _ := store.ClaimDueSchedules(late); len(again) != 0 {
		t.Fatal("coalesced schedule fired twice")
	}
	got, _ = store.GetSchedule(x.ID)
	next, _ = parseStoredTime(got.NextAtUTC)
	if !next.After(late) || next.In(loc).Weekday() != time.Sunday {
		t.Fatalf("after coalescing next=%s", next.In(loc))
	}
	// The occurrence snapshot keeps the weekday set.
	occ, err := store.ScheduleOccurrences(context.Background(), c.ID, []string{runs[0].ID})
	if err != nil || len(occ) != 1 || occ[0].Kind != "weekly" || !reflect.DeepEqual(occ[0].Weekdays, []string{"sun"}) {
		t.Fatalf("occurrence %+v %v", occ, err)
	}
}

func TestWeeklyAuthorizationSpecAndMCPWrite(t *testing.T) {
	override := weeklySpec("mon", "sun")
	override.Content = "Read synthetic alpha and report its public value."
	f := newScheduledMCPFixtureSpec(t, false, scheduleSourceChat, &override)
	if f.schedule.Kind != scheduleWeekly {
		t.Fatalf("fixture schedule %+v", f.schedule)
	}
	revision, err := readScheduleAuthorizationRevision(f.s.store.db, f.schedule.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if revision.ExecutionSpec.Kind != scheduleWeekly || revision.ExecutionSpec.Weekdays != "mon,sun" || revision.ExecutionSpec.DailyTime != "00:43" {
		t.Fatalf("execution spec %+v", revision.ExecutionSpec)
	}
	// Weekdays are a spec invariant, so changing them changes the digest and
	// the stored chain no longer binds the live schedule.
	changed := revision.ExecutionSpec
	changed.Weekdays = "mon"
	if scheduleExecutionSpecDigest(changed) == revision.ExecutionSpecDigest {
		t.Fatal("weekdays are not part of the execution spec digest")
	}
	f.exec(`UPDATE schedules SET weekdays='mon' WHERE id=?`, f.schedule.ID)
	if _, _, _, err := readBoundScheduleAuthorization(f.s.store.db, f.schedule.ID, revision.AccountID); err == nil {
		t.Fatal("authorization accepted changed weekdays")
	}
	f.exec(`UPDATE schedules SET weekdays='mon,sun' WHERE id=?`, f.schedule.ID)
	if _, _, _, err := readBoundScheduleAuthorization(f.s.store.db, f.schedule.ID, revision.AccountID); err != nil {
		t.Fatalf("authorization rejected unchanged weekdays: %v", err)
	}

	// An MCP write inside a weekly occurrence passes the full chain check.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.proposeTool("publish_note", false)
	if err := f.s.approveMCPCall(ctx, f.c, f.r, f.call); err != nil {
		t.Fatalf("weekly occurrence MCP write refused: %v", err)
	}
	if f.p.calls.Load() != 1 || f.claimCount() != 1 {
		t.Fatalf("reviewer=%d claims=%d", f.p.calls.Load(), f.claimCount())
	}
}

func TestWeeklyKindMigrationRebuildsLegacyTable(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	if err := store.ensureSchedules(); err != nil {
		t.Fatal(err)
	}
	daily, err := store.CreateSchedule(c.ID, bot.ID, ScheduleSpec{Title: "Daily", Description: "d", Content: "daily job", Kind: scheduleDaily, DailyTime: "08:00", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	due, _ := parseStoredTime(daily.NextAtUTC)
	runs, err := store.ClaimDueSchedules(due)
	if err != nil || len(runs) != 1 {
		t.Fatalf("claim %v %v", runs, err)
	}
	before, _ := store.GetSchedule(daily.ID)
	token := scheduleAuthorizationToken(t, store, daily.ID)

	// Rebuild into the pre-weekly shape: old CHECK, no weekdays column.
	for _, statement := range []string{
		`PRAGMA foreign_keys=OFF`,
		`CREATE TABLE schedules_old (id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL, content TEXT NOT NULL, title TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', created_by TEXT NOT NULL DEFAULT '', kind TEXT NOT NULL CHECK(kind IN ('once','interval','daily')), timezone TEXT NOT NULL, next_at_utc TEXT NOT NULL, interval_seconds INTEGER NOT NULL DEFAULT 0, daily_time TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','paused','completed','deleted')), created_at TEXT NOT NULL, updated_at TEXT NOT NULL, authorization_revision INTEGER NOT NULL DEFAULT 0, FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE, FOREIGN KEY(bot_id) REFERENCES bots(id) ON DELETE CASCADE)`,
		`INSERT INTO schedules_old SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at,authorization_revision FROM schedules`,
		`DROP TABLE schedules`,
		`ALTER TABLE schedules_old RENAME TO schedules`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := store.db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := store.db.Exec(`UPDATE schedule_occurrences SET weekdays=''`); err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if err := migrateSchedules(store.db); err != nil {
			t.Fatal(err)
		}
		if err := migrateScheduleWeeklyKind(store.db); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetSchedule(daily.ID)
		if err != nil || !reflect.DeepEqual(before, got) || scheduleAuthorizationToken(t, store, daily.ID) != token {
			t.Fatalf("pass %d: row changed %+v vs %+v %v", pass, got, before, err)
		}
		if scheduleAuthorizationCount(t, store, "schedule_authorization_revisions") != 1 || scheduleAuthorizationCount(t, store, "schedule_occurrence_authorizations") != 1 || scheduleAuthorizationCount(t, store, "schedule_occurrences") != 1 {
			t.Fatalf("pass %d: dependent rows lost", pass)
		}
	}
	w, err := store.CreateSchedule(c.ID, bot.ID, weeklySpec("fri"))
	if err != nil || !reflect.DeepEqual(w.Weekdays, []string{"fri"}) {
		t.Fatalf("weekly insert after migration: %+v %v", w, err)
	}
	var fk int
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys left off: %d %v", fk, err)
	}
	if _, err := store.db.Exec(`DELETE FROM schedules WHERE id=?`, w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestWeeklyPortabilityValidation(t *testing.T) {
	for _, tc := range []struct {
		days []string
		ok   bool
	}{{[]string{"sun"}, true}, {[]string{"mon", "fri"}, true}, {nil, false}, {[]string{"xyz"}, false}} {
		_, err := normalizeWeekdays(tc.days)
		if (err == nil) != tc.ok {
			t.Errorf("%v: %v", tc.days, err)
		}
	}
}

func TestScheduleToolResultCarriesNextRunInTheSchedulesZone(t *testing.T) {
	store, bot, c := scheduleTestStore(t)
	defer store.Close()
	_, run, _, err := store.AddUserRun(c.ID, bot.ID, "Remind me about the appointment.", "zone-tool-source")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, accountID: "synthetic-zone-account"}
	in := map[string]any{"title": "Appointment", "description": "Reminder", "content": "Remind the person.", "kind": "once", "run_at": "2030-07-01T16:00:00Z", "timezone": "America/Los_Angeles"}
	out, err := executeScheduleTool(t, server, c, run, "create_schedule", in)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		NextAtUTC   string `json:"next_at_utc"`
		NextAtLocal string `json:"next_at_local"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if want := "Mon 2030-07-01 09:00 PDT (America/Los_Angeles)"; got.NextAtLocal != want {
		t.Fatalf("next_at_local=%q want %q (utc %q)", got.NextAtLocal, want, got.NextAtUTC)
	}
	list, err := executeScheduleTool(t, server, c, run, "list_schedules", map[string]any{})
	if err != nil || !strings.Contains(list, `"next_at_local":"Mon 2030-07-01 09:00 PDT (America/Los_Angeles)"`) {
		t.Fatalf("list lacks the local time: %s %v", list, err)
	}
	for _, tool := range server.scheduleTools(c, run) {
		if tool.Name == "create_schedule" && !strings.Contains(tool.Description, "next_at_local") {
			t.Fatal("create_schedule description does not tell the confirmation to state the local time with its zone")
		}
	}
}
