package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Rehearses the weekly-kind table rebuild against a production schema dump.
// Set TOFI_PROD_SCHEDULE_SCHEMA to a schema-only SQL file; all rows are synthetic.

func masterObjects(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_master WHERE name LIKE 'schedule%' OR tbl_name LIKE 'schedule%' ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var typ, name, tbl, ddl string
		if err := rows.Scan(&typ, &name, &tbl, &ddl); err != nil {
			t.Fatal(err)
		}
		out[typ+" "+name+" on "+tbl] = ddl
	}
	return out
}

func dumpRows(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, v := range vals {
			parts = append(parts, fmt.Sprintf("%v", v))
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

const (
	prodSchedulesCols = `id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at,authorization_revision`
	prodOccCols       = `schedule_id,scheduled_for_utc,run_id,title,description,created_by,kind,timezone,interval_seconds,daily_time,occurrence_number,created_at`
)

func TestProductionScheduleSchemaRebuildRehearsal(t *testing.T) {
	path := os.Getenv("TOFI_PROD_SCHEDULE_SCHEMA")
	if path == "" {
		t.Skip("TOFI_PROD_SCHEDULE_SCHEMA not set")
	}
	prodSQL, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	daily := ScheduleSpec{Title: "Daily", Description: "d", Content: "Read synthetic alpha and report its public value.", Kind: scheduleDaily, DailyTime: "08:00", Timezone: "America/Los_Angeles"}
	f := newScheduledMCPFixtureSpec(t, false, scheduleSourceChat, &daily)
	store := f.s.store
	// Bring the schedule to authorization revision 3 (two chat content edits)
	// and claim its next occurrence at that revision.
	f.setStatus(f.root.ID, "done", "")
	f.chatContentEdit("Read synthetic alpha and report the public value twice.")
	f.chatContentEdit("Read synthetic alpha and report the public value three times.")
	f.nextOccurrence()
	if scheduleAuthorizationToken(t, store, f.schedule.ID) != 3 {
		t.Fatal("fixture schedule is not at revision 3")
	}
	server := f.s
	// Other shapes: DM daily by form (rev 1), group daily (rev 1), deleted daily (rev 2), once, interval.
	form, err := store.createScheduleWithSource(f.c.ID, f.bot.ID, ScheduleSpec{Title: "Form", Description: "d", Content: "form daily", Kind: scheduleDaily, DailyTime: "09:15", Timezone: "UTC"}, server.scheduleFormSource("create", map[string]string{"k": "v"}))
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("rehearsal group", []string{f.bot.ID, f.colleague.ID})
	if err != nil {
		t.Fatal(err)
	}
	gsched, err := store.createScheduleWithSource(group.ID, f.bot.ID, ScheduleSpec{Title: "Group", Description: "d", Content: "group daily", Kind: scheduleDaily, DailyTime: "10:30", Timezone: "America/Los_Angeles"}, server.scheduleFormSource("create", map[string]string{"k": "g"}))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.createScheduleWithSource(f.c.ID, f.bot.ID, ScheduleSpec{Title: "Gone", Description: "d", Content: "to delete", Kind: scheduleDaily, DailyTime: "11:00", Timezone: "UTC"}, server.scheduleFormSource("create", map[string]string{"k": "d"}))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.deleteScheduleWithSource(deleted.ID, server.scheduleFormSource("delete", map[string]string{"k": "d"})); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateSchedule(f.c.ID, f.bot.ID, ScheduleSpec{Title: "Once", Description: "d", Content: "once", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateSchedule(f.c.ID, f.bot.ID, ScheduleSpec{Title: "Interval", Description: "d", Content: "interval", Kind: scheduleInterval, IntervalSeconds: 3600, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	makeDue(t, store, gsched.ID, time.Now().Add(-time.Minute))
	if runs, err := store.ClaimDueSchedules(time.Now()); err != nil || len(runs) != 1 {
		t.Fatalf("group claim %v %v", runs, err)
	}
	_ = form
	// A legacy (format 0) occurrence authorization carrying complete revision rows.
	var fmt1 string
	if err := store.db.QueryRow(`SELECT snapshot_json FROM schedule_occurrence_authorizations WHERE schedule_id=?`, f.schedule.ID).Scan(&fmt1); err != nil {
		t.Fatal(err)
	}
	record, err := readScheduleOccurrenceAuthorization(store.db, f.root.ID)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := readScheduleAuthorizationChain(store.db, record)
	if err != nil {
		t.Fatal(err)
	}
	legacy := record.Snapshot
	legacy.Format, legacy.RevisionReferences, legacy.Revisions = 0, nil, chain
	legacy.RootRunID = f.source.ID
	legacyRaw, _ := json.Marshal(legacy)
	if _, err := store.db.Exec(`INSERT INTO schedule_occurrence_authorizations(root_run_id,schedule_id,revision,account_id,scheduled_for_utc,trigger_message_id,snapshot_json,snapshot_digest,captured_at) VALUES(?,?,?,?,?,?,?,?,?)`, f.source.ID, f.schedule.ID, record.Revision, record.AccountID, "2020-01-01T00:00:00.000000000Z", record.TriggerMessageID, string(legacyRaw), digestBytes(legacyRaw), record.CapturedAt); err != nil {
		t.Fatal(err)
	}

	// Downgrade the four schedule tables to the exact production DDL, keeping rows.
	db := store.db
	for _, statement := range []string{
		`PRAGMA foreign_keys=OFF`,
		`CREATE TABLE keep_sched AS SELECT ` + prodSchedulesCols + ` FROM schedules`,
		`CREATE TABLE keep_occ AS SELECT ` + prodOccCols + ` FROM schedule_occurrences`,
		`CREATE TABLE keep_rev AS SELECT * FROM schedule_authorization_revisions`,
		`CREATE TABLE keep_auth AS SELECT * FROM schedule_occurrence_authorizations`,
		`DROP TABLE schedule_occurrence_authorizations`,
		`DROP TABLE schedule_authorization_revisions`,
		`DROP TABLE schedule_occurrences`,
		`DROP TABLE schedules`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if _, err := db.Exec(string(prodSQL)); err != nil {
		t.Fatalf("prod schema: %v", err)
	}
	for _, statement := range []string{
		`INSERT INTO schedules(` + prodSchedulesCols + `) SELECT ` + prodSchedulesCols + ` FROM keep_sched`,
		`INSERT INTO schedule_occurrences(` + prodOccCols + `) SELECT ` + prodOccCols + ` FROM keep_occ`,
		`INSERT INTO schedule_authorization_revisions SELECT * FROM keep_rev`,
		`INSERT INTO schedule_occurrence_authorizations SELECT * FROM keep_auth`,
		`DROP TABLE keep_sched`, `DROP TABLE keep_occ`, `DROP TABLE keep_rev`, `DROP TABLE keep_auth`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	snapshot := func(db *sql.DB) map[string][]string {
		return map[string][]string{
			"schedules":   dumpRows(t, db, `SELECT `+prodSchedulesCols+` FROM schedules ORDER BY id`),
			"occurrences": dumpRows(t, db, `SELECT `+prodOccCols+` FROM schedule_occurrences ORDER BY schedule_id,scheduled_for_utc`),
			"revisions":   dumpRows(t, db, `SELECT * FROM schedule_authorization_revisions ORDER BY schedule_id,revision`),
			"auths":       dumpRows(t, db, `SELECT * FROM schedule_occurrence_authorizations ORDER BY root_run_id`),
			"runs":        dumpRows(t, db, `SELECT id,conversation_id,bot_id,kind FROM runs ORDER BY id`),
		}
	}
	beforeRows := snapshot(db)
	beforeMaster := masterObjects(t, db)
	if len(beforeRows["schedules"]) != 6 || len(beforeRows["revisions"]) < 8 || len(beforeRows["auths"]) < 3 || len(beforeRows["occurrences"]) < 3 {
		t.Fatalf("seed too small: %d schedules %d revisions %d auths %d occurrences", len(beforeRows["schedules"]), len(beforeRows["revisions"]), len(beforeRows["auths"]), len(beforeRows["occurrences"]))
	}
	// The downgraded schema must equal the production dump.
	for _, want := range []string{"CHECK(kind IN ('once','interval','daily'))", "authorization_revision INTEGER NOT NULL DEFAULT 0,\n\tFOREIGN KEY"} {
		if !strings.Contains(beforeMaster["table schedules on schedules"], want) {
			t.Fatalf("rehearsal schema is not the production shape: %s", beforeMaster["table schedules on schedules"])
		}
	}

	// Reopen with this branch's code and let the lazy migration run.
	dir := f.dir
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	f.s.store = reopened // closed by the fixture cleanup
	if err := reopened.ensureSchedules(); err != nil {
		t.Fatal(err)
	}
	db = reopened.db
	afterRows := snapshot(db)
	for k := range beforeRows {
		if !reflect.DeepEqual(beforeRows[k], afterRows[k]) {
			t.Errorf("%s changed:\nbefore %v\nafter  %v", k, beforeRows[k], afterRows[k])
		}
	}
	if n := dumpRows(t, db, `SELECT COUNT(*) FROM schedules WHERE weekdays<>''`); n[0] != "0" {
		t.Errorf("weekdays backfilled: %v", n)
	}
	afterMaster := masterObjects(t, db)
	var keys []string
	for k := range beforeMaster {
		keys = append(keys, k)
	}
	for k := range afterMaster {
		if _, ok := beforeMaster[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		b, a := beforeMaster[k], afterMaster[k]
		_, inBefore := beforeMaster[k]
		_, inAfter := afterMaster[k]
		switch {
		case !inAfter:
			t.Errorf("LOST %s", k)
		case !inBefore:
			t.Errorf("NEW %s", k)
		case b != a:
			t.Logf("sqlite_master DIFF %s\n--- before\n%s\n--- after\n%s", k, b, a)
			if k != "table schedules on schedules" && k != "table schedule_occurrences on schedule_occurrences" {
				t.Errorf("unexpected change to %s", k)
			}
		default:
			t.Logf("sqlite_master identical: %s", k)
		}
	}
	for _, name := range []string{"schedule_authorization_revisions_immutable", "schedule_occurrence_authorizations_immutable", "schedules_due", "schedules_conversation", "schedule_occurrences_run"} {
		found := false
		for k := range afterMaster {
			found = found || strings.Contains(k, " "+name+" on ")
		}
		if !found {
			t.Errorf("object %s missing after migration", name)
		}
	}
	if fk := dumpRows(t, db, `PRAGMA foreign_key_check`); len(fk) != 0 {
		t.Errorf("foreign_key_check: %v", fk)
	}
	if ic := dumpRows(t, db, `PRAGMA integrity_check`); len(ic) != 1 || ic[0] != "ok" {
		t.Errorf("integrity_check: %v", ic)
	}
	if v := dumpRows(t, db, `PRAGMA foreign_keys`); v[0] != "1" {
		t.Errorf("foreign_keys off: %v", v)
	}
	// Immutability triggers still stop UPDATE (the production trigger is BEFORE UPDATE only).
	if _, err := db.Exec(`UPDATE schedule_authorization_revisions SET request_id='x'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Errorf("revision UPDATE not blocked: %v", err)
	}
	if _, err := db.Exec(`UPDATE schedule_occurrence_authorizations SET snapshot_json='x'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Errorf("occurrence authorization UPDATE not blocked: %v", err)
	}
	// ON DELETE CASCADE from schedules still works (hard delete on a throwaway row).
	tmp, err := reopened.CreateSchedule(f.c.ID, f.bot.ID, weeklySpec("sat"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schedules WHERE id=?`, tmp.ID); err != nil {
		t.Fatal(err)
	}
	if n := dumpRows(t, db, `SELECT COUNT(*) FROM schedule_authorization_revisions WHERE schedule_id='`+tmp.ID+`'`); n[0] != "0" {
		t.Errorf("cascade lost: %v", n)
	}

	// A scheduled MCP write on the migrated revision-3 schedule still verifies.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The restart interrupted the pre-migration run, as it would in production;
	// claim a fresh occurrence through the migrated table at revision 3.
	f.exec(`UPDATE runs SET status='done' WHERE id=?`, f.root.ID)
	f.nextOccurrence()
	if rec, err := readScheduleOccurrenceAuthorization(reopened.db, f.root.ID); err != nil || rec.Revision != 3 {
		t.Fatalf("post-migration occurrence authorization: %+v %v", rec.Revision, err)
	}
	f.proposeTool("publish_note", false)
	if err := f.s.approveMCPCall(ctx, f.c, f.r, f.call); err != nil {
		t.Fatalf("MCP write on migrated revision-3 schedule refused: %v", err)
	}
	if f.p.calls.Load() != 1 || f.claimCount() != 1 {
		t.Fatalf("reviewer=%d claims=%d", f.p.calls.Load(), f.claimCount())
	}
}
