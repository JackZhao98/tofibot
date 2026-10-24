package app

import (
	"strings"
	"testing"
	"time"
)

// Finding 1: a copied run_waits row must not come back after retention.
func TestMigratedRunWaitIsMarkedAndDoesNotReturnAfterRetention(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	f.s.db.Exec(`DELETE FROM waits`)
	deadline := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := f.s.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, root.ID, f.g.ID, f.a.ID, f.c.ID, "l", deadline, now()); err != nil {
		t.Fatal(err)
	}
	if err := migrateTasksAndWaits(f.s.db); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM run_waits WHERE status='pending'`); n != 0 {
		t.Fatalf("copied run_waits row still pending: %d", n)
	}
	// End the copy, let retention delete it, start again.
	if _, err := f.s.db.Exec(`UPDATE waits SET status='timed_out',resolved_at=? WHERE id=?`, now(), "legacy:"+root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.cleanupFinishedTasks(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := migrateTasksAndWaits(f.s.db); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE id=?`, "legacy:"+root.ID); n != 0 {
		t.Fatalf("the wait came back after retention: %d", n)
	}
}

// Finding 1: one failing wait must not stop the sweep, and a repeated
// wait_result for a group is not an error.
func TestExpireDueWaitsContinuesPastAFailingWaitAndResultInsertIsIdempotent(t *testing.T) {
	f := newTaskFix(t)
	r1 := f.running(f.g.ID, f.a)
	r2 := f.running(f.g.ID, f.b)
	r3 := f.running(f.g.ID, f.c)
	past := time.Now().Add(-time.Minute)
	w1 := insertBotPostWait(t, f.s.db, r1.ID, f.g.ID, f.a.ID, f.c.ID, past)
	w2 := insertBotPostWait(t, f.s.db, r2.ID, f.g.ID, f.b.ID, f.c.ID, past.Add(time.Second))
	w3 := insertBotPostWait(t, f.s.db, r3.ID, f.g.ID, f.c.ID, f.a.ID, past.Add(2*time.Second))
	// The first group already has its result row; the unique index would refuse a second.
	if _, err := f.s.db.Exec(`INSERT INTO bot_inbox(id,bot_id,conversation_id,kind,wait_group,body,created_at) VALUES('pre',?,?,'wait_result',?,'x',?)`, f.a.ID, f.g.ID, w1, now()); err != nil {
		t.Fatal(err)
	}
	// The second group's delivery fails for another reason.
	if _, err := f.s.db.Exec(`CREATE TRIGGER boom BEFORE INSERT ON bot_inbox WHEN NEW.bot_id='` + f.b.ID + `' AND NEW.kind='wait_result' BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatal(err)
	}
	f.s.expireDueWaits(time.Now())
	status := func(id string) string {
		var st string
		f.s.db.QueryRow(`SELECT status FROM waits WHERE id=?`, id).Scan(&st)
		return st
	}
	if status(w1) != waitTimedOut {
		t.Fatalf("w1 is %s", status(w1))
	}
	if status(w3) != waitTimedOut {
		t.Fatalf("a later wait never timed out: %s", status(w3))
	}
	if status(w2) != waitPending {
		t.Fatalf("the failing wait changed: %s", status(w2))
	}
}

// Finding 2: a row is read only when it is rendered into the context.
func TestInboxRowsNotShownStayUnreadForTheWake(t *testing.T) {
	f := newTaskFix(t)
	body := strings.Repeat("question text ", 40)
	for i := 0; i < 10; i++ {
		tx, _ := f.s.db.Begin()
		if err := addInboxTx(tx, InboxItem{BotID: f.a.ID, ConversationID: f.g.ID, Kind: inboxTaskQuestion, Body: body, Wakes: true}); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
	}
	// A block that is built but dropped by the history budget reads nothing.
	if block, ids := f.s.taskUpdatesBlock(f.a.ID, f.g.ID, "run-1"); block == "" || len(ids) == 0 {
		t.Fatal("no block")
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE consumed_at=''`); n != 10 {
		t.Fatalf("building the block read %d rows", 10-n)
	}
	block := f.s.taskUpdatesContext(f.a.ID, f.g.ID, "run-1")
	shown := strings.Count(block, "- task_question:")
	if shown == 0 || shown >= 10 {
		t.Fatalf("shown %d", shown)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE consumed_at<>''`); n != shown {
		t.Fatalf("%d shown but %d marked read", shown, n)
	}
	if due, _ := f.s.dueInboxRows(f.a.ID); len(due) != 10-shown {
		t.Fatalf("%d rows left for the wake, want %d", len(due), 10-shown)
	}
}
