package app

import (
	"fmt"
	"testing"
)

// Owner decision 2026-10-24 (decision 0064 amendment): the tree budget counts
// Bot-to-Bot messages WITHOUT progress. These tests cover the new rules.

type idleFix struct {
	taskFix
	root  Run
	first receiptTask
	tree  string
}

// newIdleFix gives a root run, a first task held by b with a checklist, and the
// holder's started run. The default setting (30) stays untouched.
func newIdleFix(t *testing.T, items int) (idleFix, Run) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	first := f.delegateTo(f.g, root, f.b, "Root work")
	texts := make([]string, items)
	for i := range texts {
		texts[i] = fmt.Sprintf("step %d", i)
	}
	if items > 0 {
		if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": first.TaskID, "add_items": texts}); err != nil {
			t.Fatal(err)
		}
	}
	holder, _ := f.s.GetRun(f.taskRow(first.TaskID).AssigneeRunID)
	f.start(holder.ID)
	holder, _ = f.s.GetRun(holder.ID)
	return idleFix{f, root, first, first.TaskID}, holder
}

// message is one Bot-to-Bot message charged to the tree the way the tools
// charge it (a bot may hold only 16 open waits, so volume goes through the
// charge itself; the tool path is exercised by dispatch below).
func (f idleFix) message(i int) error {
	tx, err := f.s.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	if err = chargeTreeTx(tx, f.tree, 1, true); err != nil {
		tx.Rollback()
		f.s.afterTreeLimit(err)
		return err
	}
	if err = tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return nil
}

// dispatch is a real delegate call from the root member.
func (f idleFix) dispatch(i int) error {
	_, err := call(f.t, f.tool(f.g, f.root, "delegate"), map[string]any{"bot_id": f.c.ID, "task": fmt.Sprintf("Part %d", i), "depends_on": []string{f.first.TaskID}})
	return err
}

// endItem ends the next open checklist item as the holder does.
func (f idleFix) endItem(holder Run) {
	f.t.Helper()
	for _, item := range f.taskRow(f.first.TaskID).Checklist {
		if item.Status == checklistOpen {
			if _, err := call(f.t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "items": []map[string]any{{"id": item.ID, "status": "done", "reason": "finished"}}}); err != nil {
				f.t.Fatal(err)
			}
			return
		}
	}
	f.t.Fatal("no open checklist item")
}

func (f idleFix) wakes() int {
	return f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND wakes=1 AND body LIKE '%idle message limit%' AND root_task_id=?`, f.a.ID, f.tree)
}

func (f idleFix) published() int {
	return f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND (kind='notice' OR sender_bot_id='system') AND (notice_data LIKE '%tree_limit%' OR content LIKE '%limit%')`, f.g.ID) +
		f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND data LIKE '%tree_limit%'`, f.g.ID)
}

func TestIdleBudgetProductiveChainFarBeyondTheLimitIsNeverRefused(t *testing.T) {
	f, holder := newIdleFix(t, 24)
	// 100 messages, an item ends every 5: far beyond 30 in total, never 30 in a row.
	for i := 1; i <= 100; i++ {
		if err := f.message(i); err != nil {
			t.Fatalf("message %d of a productive chain was refused: %v", i, err)
		}
		if i%5 == 0 {
			f.endItem(holder)
		}
	}
	if used := treeUsedTx(f.s.db, f.tree); used >= 30 {
		t.Fatalf("idle count %d", used)
	}
	if f.wakes() != 0 || f.published() != 0 {
		t.Fatalf("the productive chain reported: wakes=%d published=%d", f.wakes(), f.published())
	}
}

func TestIdleBudgetRefusesAfterThirtyWithoutProgressAndWakesRootOnce(t *testing.T) {
	f, _ := newIdleFix(t, 0)
	// delegate of the first task was message 1.
	for i := 2; i <= 30; i++ {
		if err := f.message(i); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 30 {
		t.Fatalf("used %d", used)
	}
	for i := 0; i < 3; i++ {
		if codeOf(f.message(100+i)) != "task_message_limit" {
			t.Fatalf("message past the limit was not refused")
		}
	}
	if codeOf(f.dispatch(200)) != "task_message_limit" {
		t.Fatal("a delegate past the limit was not refused")
	}
	if f.wakes() != 1 {
		t.Fatalf("root woken %d times, want once", f.wakes())
	}
	if f.published() != 0 {
		t.Fatalf("something was published to the conversation at the limit: %d", f.published())
	}
}

func TestIdleBudgetProgressAfterRefusalResetsAndAllowsDispatch(t *testing.T) {
	cases := map[string]func(f idleFix, holder Run){
		"checklist item ends": func(f idleFix, holder Run) { f.endItem(holder) },
		"task closes done": func(f idleFix, holder Run) {
			if _, ok, err := f.s.FinishRun(holder.ID, f.g.ID, f.b.ID, "All done."); err != nil || !ok {
				f.t.Fatal(ok, err)
			}
		},
	}
	for name, progress := range cases {
		t.Run(name, func(t *testing.T) {
			f, holder := newIdleFix(t, 2)
			for i := 2; i <= 30; i++ {
				if err := f.message(i); err != nil {
					t.Fatal(err)
				}
			}
			if codeOf(f.message(31)) != "task_message_limit" {
				t.Fatal("not refused at the limit")
			}
			progress(f, holder)
			if used := treeUsedTx(f.s.db, f.tree); used != 0 {
				t.Fatalf("progress left the idle count at %d", used)
			}
			if err := f.dispatch(32); err != nil {
				t.Fatalf("dispatch after progress: %v", err)
			}
			// A second idle stretch wakes the root once more.
			for i := 33; i <= 62 && f.wakes() < 2; i++ {
				_ = f.message(i)
			}
			if f.wakes() != 2 || f.published() != 0 {
				t.Fatalf("second stretch: wakes=%d published=%d", f.wakes(), f.published())
			}
		})
	}
}

func TestIdleBudgetSettingMigratesTheOldKey(t *testing.T) {
	f := newTaskFix(t)
	db := f.s.db
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Nothing stored: the new default.
	if got := autoIntTx(db, autoTaskTreeIdleLimit); got != 30 {
		t.Fatalf("default %d", got)
	}
	exec(`INSERT INTO automation_settings(key,value,updated_at) VALUES(?,?,?)`, autoTaskTreeOldLimit, "120", now())
	if err := migrateAutomationSettings(db); err != nil {
		t.Fatal(err)
	}
	if got := autoIntTx(db, autoTaskTreeIdleLimit); got != 120 {
		t.Fatalf("an old custom value must stay: %d", got)
	}
	if f.count(`SELECT COUNT(*) FROM automation_settings WHERE key=?`, autoTaskTreeOldLimit) != 0 {
		t.Fatal("the old key stayed")
	}
	// The migration runs again without a second effect, and a new value wins.
	exec(`UPDATE automation_settings SET value='45' WHERE key=?`, autoTaskTreeIdleLimit)
	exec(`INSERT INTO automation_settings(key,value,updated_at) VALUES(?,?,?)`, autoTaskTreeOldLimit, "77", now())
	if err := migrateAutomationSettings(db); err != nil {
		t.Fatal(err)
	}
	if got := autoIntTx(db, autoTaskTreeIdleLimit); got != 45 {
		t.Fatalf("a set new value must win: %d", got)
	}
}

// A result the holder submits again and again, with the giver not accepting it,
// is not progress after the first time for a spec version.
func TestIdleBudgetResubmittedResultIsNotProgress(t *testing.T) {
	f, holder := newIdleFix(t, 1)
	f.endItem(holder) // first progress; a result below needs an open item to land in review
	if _, err := call(t, f.tool(f.g, f.root, "task_update"), map[string]any{"task_id": f.first.TaskID, "add_items": []string{"never done"}}); err != nil {
		t.Fatal(err)
	}
	submit := func(round int) {
		t.Helper()
		run := queueRun(t, f.s, f.g.ID, f.b.ID, "", "")
		f.s.db.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, run.ID, f.first.TaskID)
		f.start(run.ID)
		if _, ok, err := f.s.FinishRun(run.ID, f.g.ID, f.b.ID, fmt.Sprintf("Result %d.", round)); err != nil || !ok {
			t.Fatal(ok, err)
		}
		if st := f.taskRow(f.first.TaskID).Status; st != "review" {
			t.Fatalf("status %s, want review", st)
		}
	}
	submit(0) // the first result counts
	for i := 2; i <= 30; i++ {
		if err := f.message(i); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 40; i++ {
		submit(i)
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 29 {
		t.Fatalf("resubmitted results reset the count: used=%d", used)
	}
	if err := f.message(31); err != nil {
		t.Fatal(err) // 30th message
	}
	if codeOf(f.message(32)) != "task_message_limit" {
		t.Fatal("not refused at 30")
	}
	if f.wakes() != 1 {
		t.Fatalf("root woken %d times", f.wakes())
	}
	// A new spec version counts once again.
	f.s.db.Exec(`UPDATE task_tree_budget SET messages=5,reported=0 WHERE tree_key=?`, f.tree) // the brief is itself a charged message, so make room
	brief := "A changed request."
	if _, err := call(t, f.tool(f.g, f.root, "task_update"), map[string]any{"task_id": f.first.TaskID, "brief": brief}); err != nil {
		t.Fatal(err)
	}
	submit(100)
	if used := treeUsedTx(f.s.db, f.tree); used != 0 {
		t.Fatalf("a result for a new spec version must count: used=%d", used)
	}
	submit(101)
	f.s.db.Exec(`UPDATE task_tree_budget SET messages=5 WHERE tree_key=?`, f.tree)
	submit(102)
	if used := treeUsedTx(f.s.db, f.tree); used != 5 {
		t.Fatalf("the second result of that version reset the count: used=%d", used)
	}
}

// An item that is reopened and ended again, or ended again with another reason,
// is progress only the first time.
func TestIdleBudgetRepeatedChecklistToggleIsProgressOnce(t *testing.T) {
	f, holder := newIdleFix(t, 2)
	item := f.taskRow(f.first.TaskID).Checklist[0].ID
	set := func(status, reason string) {
		t.Helper()
		if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "items": []map[string]any{{"id": item, "status": status, "reason": reason}}}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 2; i <= 10; i++ {
		_ = f.message(i)
	}
	set("done", "finished")
	if used := treeUsedTx(f.s.db, f.tree); used != 0 {
		t.Fatalf("the first end must reset: %d", used)
	}
	for i := 2; i <= 6; i++ {
		_ = f.message(i)
	}
	for i := 0; i < 10; i++ {
		set("open", "")
		set("done", fmt.Sprintf("again %d", i))
		set("skipped", fmt.Sprintf("edited %d", i))
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 5 {
		t.Fatalf("toggling an item reset the count: %d", used)
	}
}
