package app

import (
	"encoding/json"
	"testing"
	"time"
)

// A lead that fails after it gave out two parts does not take the parts with
// it: the root stays open with no run, the parts finish and link, and the run
// their results wake carries the root and merges.
func TestALeadThatFailsAfterDelegatingKeepsItsPartsRunning(t *testing.T) {
	f := newTaskFix(t)
	lead := f.running(f.g.ID, f.b)
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Flights", "title": "Flights"},
		{"bot_id": f.a.ID, "task": "Lodging", "title": "Lodging"},
	})
	var rootID string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, lead.ID).Scan(&rootID); err != nil {
		t.Fatalf("no root task: %v", err)
	}
	flights := f.startPart(r.Tasks[0].TaskID, f.c)
	lodging := f.startPart(r.Tasks[1].TaskID, f.a)
	if ok, err := f.s.SetRunStatus(lead.ID, "failed", "provider error"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	root := f.taskRow(rootID)
	if root.Status != "in_progress" || root.AssigneeRunID != "" {
		t.Fatalf("the root stays open with no run: %s %q", root.Status, root.AssigneeRunID)
	}
	for _, part := range r.Tasks {
		if got := f.taskRow(part.TaskID); got.Status == "cancelled" {
			t.Fatalf("part %s was cancelled with the lead", part.TaskID)
		}
	}
	for _, run := range []Run{flights, lodging} {
		if got, _ := f.s.GetRun(run.ID); got.Status != "running" {
			t.Fatalf("the holder run stopped: %s", got.Status)
		}
	}
	m1, _, err := f.s.FinishRun(flights.ID, f.g.ID, f.c.ID, "Flights found.")
	if err != nil {
		t.Fatal(err)
	}
	m2, _, err := f.s.FinishRun(lodging.ID, f.g.ID, f.a.ID, "Lodging found.")
	if err != nil {
		t.Fatal(err)
	}
	history, _, err := f.s.Messages(f.g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(history)
	var rows []map[string]any
	if err = json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	linked := map[string]any{}
	for _, row := range rows {
		linked[row["id"].(string)] = row["task_id"]
	}
	for i, m := range []Message{m1, m2} {
		if linked[m.ID] != r.Tasks[i].TaskID {
			t.Fatalf("part %d result is not linked to its part: %v", i, linked[m.ID])
		}
	}
	f.srv.flushInbox()
	woken := f.wokenRuns(f.b)
	if len(woken) != 1 {
		t.Fatalf("the lead wakes once: %d", len(woken))
	}
	if got := f.taskRow(rootID); got.AssigneeRunID != woken[0].ID {
		t.Fatalf("the woken run takes the root: %q", got.AssigneeRunID)
	}
	f.start(woken[0].ID)
	if _, _, err = f.s.FinishRun(woken[0].ID, f.g.ID, f.b.ID, "Here is the plan."); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(rootID); got.Status != "done" {
		t.Fatalf("the root closes with the merged answer: %s", got.Status)
	}
}

// A root made for a lead that nothing works on any more does not stay open
// forever and does not make the lead look busy in [members].
func TestAStaleLeadRootIsNotBusyAndIsClosedByTheCleanup(t *testing.T) {
	f := newTaskFix(t)
	lead := f.running(f.g.ID, f.b)
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Flights", "title": "Flights"},
	})
	var rootID string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, lead.ID).Scan(&rootID); err != nil {
		t.Fatalf("no root task: %v", err)
	}
	f.startPart(r.Tasks[0].TaskID, f.c)
	if ok, err := f.s.SetRunStatus(lead.ID, "failed", "provider error"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// The part is still open: the lead's plan is live work.
	if busy, _ := f.s.memberActivity(f.b.ID); !busy {
		t.Fatal("a plan with an open part is busy")
	}
	// Everything under it ends without a wake: the part is cancelled by hand.
	for _, q := range []string{
		`UPDATE runs SET status='cancelled' WHERE conversation_id='` + f.g.ID + `' AND status IN ('queued','running','waiting')`,
		`UPDATE work_items SET status='cancelled' WHERE parent_task_id='` + rootID + `'`,
		`UPDATE waits SET status='cancelled' WHERE status='pending'`,
		`DELETE FROM bot_inbox`,
	} {
		if _, err := f.s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if busy, title := f.s.memberActivity(f.b.ID); busy {
		t.Fatalf("a stale root makes the lead busy: %q", title)
	}
	if got := f.taskRow(rootID); got.Status != "in_progress" {
		t.Fatalf("setup: %s", got.Status)
	}
	// Too fresh: left alone. After the idle period: closed.
	if err := f.s.closeIdleLeadRoots(time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(rootID); got.Status != "in_progress" {
		t.Fatalf("a fresh root is left alone: %s", got.Status)
	}
	if err := f.s.closeIdleLeadRoots(time.Now().Add(2 * leadRootIdleAfter)); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(rootID); got.Status != "done" {
		t.Fatalf("the idle root closes: %s", got.Status)
	}
}

// Stop ends the plan: the tree is cancelled and so are the runs that held the
// parts, so nothing keeps working.
func TestStoppingALeadStopsTheHolderRunsOfItsParts(t *testing.T) {
	f := newTaskFix(t)
	lead := f.running(f.g.ID, f.b)
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Flights", "title": "Flights"},
		{"bot_id": f.a.ID, "task": "Lodging", "title": "Lodging"},
	})
	flights := f.startPart(r.Tasks[0].TaskID, f.c)
	lodging := f.startPart(r.Tasks[1].TaskID, f.a)
	if err := f.s.CancelRunTree(lead.ID); err != nil {
		t.Fatal(err)
	}
	for _, run := range []Run{lead, flights, lodging} {
		if got, _ := f.s.GetRun(run.ID); got.Status != "cancelled" {
			t.Fatalf("run %s is %s after Stop", run.ID, got.Status)
		}
	}
	for _, part := range r.Tasks {
		if got := f.taskRow(part.TaskID); got.Status != "cancelled" {
			t.Fatalf("part %s is %s", part.TaskID, got.Status)
		}
	}
	var active int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND status IN ('queued','running','waiting')`, f.g.ID).Scan(&active); err != nil || active != 0 {
		t.Fatalf("runs still active: %d %v", active, err)
	}
}
