package app

import (
	"testing"
	"time"
)

// A part whose result arrives after its deadline passed, when the lead already
// delivered its answer with the gap stated, does not wake the lead into a second
// answer: the card records the late result.
func TestALateResultDoesNotWakeTheLeadAfterItsAnswer(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"}})
	part := f.startPart(r.Tasks[0].TaskID, f.c)
	// The lead ends its turn pending; the deadline passes and the lead is woken with the timeout.
	if _, ok, err := f.s.FinishRun(lead.ID, f.g.ID, f.b.ID, "Waiting for the hotels."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.s.db.Exec(`UPDATE waits SET deadline_at=? WHERE kind='task_result' AND target=?`, stampTime(time.Now().Add(-time.Minute)), r.Tasks[0].TaskID)
	f.srv.sweepWaits(time.Now())
	f.srv.flushInbox()
	woken := f.wokenRuns(f.b)
	if len(woken) != 1 {
		t.Fatalf("the timeout must wake the lead once: %d", len(woken))
	}
	f.start(woken[0].ID)
	if _, ok, err := f.s.FinishRun(woken[0].ID, f.g.ID, f.b.ID, "Here is the plan; the hotels are missing."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	runs := f.count(`SELECT COUNT(*) FROM runs WHERE bot_id=?`, f.b.ID)
	inbox := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=?`, f.b.ID)
	if _, ok, err := f.s.FinishRun(part.ID, f.g.ID, f.c.ID, "Three hotels, late."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	if after := f.count(`SELECT COUNT(*) FROM runs WHERE bot_id=?`, f.b.ID); after != runs {
		t.Fatalf("a late result woke the lead into a second answer: %d runs, had %d", after, runs)
	}
	if after := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=?`, f.b.ID); after != inbox {
		t.Fatalf("a late result left a row for the lead: %d rows, had %d", after, inbox)
	}
	items := f.planItems(leadTask.ID)
	if len(items) != 1 || items[0].Status != "done" || items[0].Reason != "late" {
		t.Fatalf("the card must record the late result: %+v", items)
	}
}
