package app

import (
	"context"
	"testing"
)

func (f taskFix) returnOf(parent, bot string) string {
	f.t.Helper()
	var id string
	if err := f.s.db.QueryRow(`SELECT id FROM runs WHERE parent_run_id=? AND kind=? AND bot_id=?`, parent, runKindFollowup, bot).Scan(&id); err != nil {
		f.t.Fatalf("no return run of %s: %v", parent, err)
	}
	return id
}

func (f taskFix) childOf(parent, bot string) string {
	f.t.Helper()
	var id string
	if err := f.s.db.QueryRow(`SELECT id FROM runs WHERE parent_run_id=? AND bot_id=? AND kind=?`, parent, bot, runKindGroupTask).Scan(&id); err != nil {
		f.t.Fatalf("no child of %s: %v", parent, err)
	}
	return id
}

// claim starts a queued run the way the worker does: claimRun is the only
// queued->running transition in production.
func (f taskFix) claim(runID string) {
	f.t.Helper()
	if _, ok, err := f.s.claimRun(context.Background(), runID); err != nil || !ok {
		f.t.Fatalf("claim %s: %v %v", runID, ok, err)
	}
}

func (f taskFix) finish(runID string, bot Bot, text string) {
	f.t.Helper()
	if _, ok, err := f.s.FinishRun(runID, f.g.ID, bot.ID, text); err != nil || !ok {
		f.t.Fatalf("finish %s: %v %v", runID, ok, err)
	}
}

// runChain drives the live shape: A holds the person's task, delegates to B and
// ends; B finishes; A's return run delegates to C and ends with no answer; C
// finishes; A's second return run answers done without task_id.
func (f taskFix) runChain() (Run, WorkItem) {
	t := f.t
	root, rootTask := f.triageTask(f.a)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "storyboard"}); err != nil {
		t.Fatal(err)
	}
	b := f.childOf(root.ID, f.b.ID)
	f.finish(root.ID, f.a, "")
	f.claim(b)
	f.finish(b, f.b, "boards")
	ret1 := f.returnOf(b, f.a.ID)
	f.claim(ret1)
	rr1, _ := f.s.GetRun(ret1)
	if _, err := call(t, f.tool(f.g, rr1, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "paint"}); err != nil {
		t.Fatal(err)
	}
	c := f.childOf(ret1, f.c.ID)
	f.finish(ret1, f.a, "")
	f.claim(c)
	f.finish(c, f.c, "paintings")
	ret2 := f.returnOf(c, f.a.ID)
	f.claim(ret2)
	f.finish(ret2, f.a, "all done")
	return root, rootTask
}

// A root task held by Bot A follows A across any number of return runs, and a
// return run that answers done closes it.
func TestRootTaskFollowsItsHolderAcrossTwoReturnRuns(t *testing.T) {
	f := newTaskFix(t)
	root, rootTask := f.runChain()
	got, _ := f.s.GetWorkItem(rootTask.ID)
	if got.Status != "done" {
		t.Fatalf("root task is %s, assignee %s (root run %s)", got.Status, got.AssigneeRunID, root.ID)
	}
}

// Rows the lost hook left behind: the sweeper settles them, so no migration.
func TestSweeperSettlesARootTaskTheHookLeftOpen(t *testing.T) {
	f := newTaskFix(t)
	root, rootTask := f.runChain()
	if _, err := f.s.db.Exec(`UPDATE work_items SET status='in_progress',assignee_run_id=?,closed_at='',completed_at='',result_message_id='',result_summary='' WHERE id=?`, root.ID, rootTask.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.reconcileTaskRuns(); err != nil {
		t.Fatal(err)
	}
	got, _ := f.s.GetWorkItem(rootTask.ID)
	if got.Status != "done" || got.ResultSummary != "all done" {
		t.Fatalf("stuck root task is %s %q", got.Status, got.ResultSummary)
	}
}
