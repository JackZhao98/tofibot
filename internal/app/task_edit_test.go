package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Owner decisions 2026-10-24: whoever holds a task edits it; the giver keeps the
// same rights; every edit has a reason, is recorded and told to the other side.
// Small talk is a hidden task until it shows itself to be a long task.

type editFix struct {
	taskFix
	root   Run
	holder Run
	task   string
}

// newEditFix gives a task that a (root) gave to b, with three items and b's run.
func newEditFix(t *testing.T) editFix {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	out, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "Review the report", "title": "Review", "checklist": []string{"one", "two", "three"}})
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Tasks []delegatedTaskReceipt `json:"tasks"`
	}
	if json.Unmarshal([]byte(out), &receipt) != nil || len(receipt.Tasks) != 1 {
		t.Fatalf("receipt %q", out)
	}
	id := receipt.Tasks[0].TaskID
	holder, _ := f.s.GetRun(f.taskRow(id).AssigneeRunID)
	f.start(holder.ID)
	holder, _ = f.s.GetRun(holder.ID)
	return editFix{f, root, holder, id}
}

func (f editFix) update(run Run, args map[string]any) (string, error) {
	args["task_id"] = f.task
	return call(f.t, f.tool(f.g, run, "task_update"), args)
}

func (f editFix) inbox(bot Bot, like string) int {
	return f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=? AND body LIKE ?`, bot.ID, f.task, like)
}

func TestHolderEditsRequestItemsOrderAndDependenciesWithReasonsAndTheGiverIsTold(t *testing.T) {
	f := newEditFix(t)
	items := f.taskRow(f.task).Checklist
	// No reason, no edit, for every kind of edit.
	for name, args := range map[string]map[string]any{
		"brief":   {"brief": "Review the report and the appendix"},
		"add":     {"add_items": []string{"four"}},
		"remove":  {"remove_items": []string{items[0].ID}},
		"rewrite": {"rewrite_items": []map[string]any{{"id": items[0].ID, "text": "uno"}}},
		"order":   {"order": []string{items[2].ID}},
		"deps":    {"add_depends_on": []string{f.task}},
	} {
		if _, err := f.update(f.holder, args); codeOf(err) != "edit_reason_required" {
			t.Errorf("%s without a reason: %v", name, err)
		}
	}
	if got := f.taskRow(f.task); got.SpecVersion != 1 || len(got.Checklist) != 3 {
		t.Fatalf("a refused edit changed the task: %+v", got)
	}
	// The holder changes the request: the version goes up, the reason is stored, the giver is told.
	if _, err := f.update(f.holder, map[string]any{"brief": "Review the report and the appendix", "reason": "the person added the appendix in a later message"}); err != nil {
		t.Fatal(err)
	}
	got := f.taskRow(f.task)
	if got.SpecVersion != 2 || got.Description != "Review the report and the appendix" {
		t.Fatalf("request %+v", got)
	}
	var revision, actor string
	if err := f.s.db.QueryRow(`SELECT reason,actor FROM task_revisions WHERE task_id=? AND version=2`, f.task).Scan(&revision, &actor); err != nil || !strings.Contains(revision, "appendix") || actor != f.b.ID {
		t.Fatalf("revision %q %q %v", revision, actor, err)
	}
	if f.inbox(f.a, "%version 2%") != 1 {
		t.Fatal("the giver was not told of the new version")
	}
	// Add, rewrite, reorder and remove, each with its reason.
	if _, err := f.update(f.holder, map[string]any{"add_items": []string{"four"}, "reason": "the appendix needs a pass"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.update(f.holder, map[string]any{"rewrite_items": []map[string]any{{"id": items[0].ID, "text": "one, with the figures"}}, "reason": "sharper scope"}); err != nil {
		t.Fatal(err)
	}
	got = f.taskRow(f.task)
	if len(got.Checklist) != 4 || got.Checklist[0].Text != "one, with the figures" || got.Checklist[3].Text != "four" {
		t.Fatalf("list %+v", got.Checklist)
	}
	if _, err := f.update(f.holder, map[string]any{"order": []string{got.Checklist[3].ID, got.Checklist[2].ID}, "reason": "the appendix goes first"}); err != nil {
		t.Fatal(err)
	}
	got = f.taskRow(f.task)
	if got.Checklist[0].Text != "four" || got.Checklist[1].Text != "three" || got.Checklist[2].Text != "one, with the figures" || got.Checklist[3].Text != "two" {
		t.Fatalf("order %+v", got.Checklist)
	}
	if _, err := f.update(f.holder, map[string]any{"remove_items": []string{got.Checklist[3].ID}, "reason": "covered by the first item"}); err != nil {
		t.Fatal(err)
	}
	if left := f.taskRow(f.task).Checklist; len(left) != 3 {
		t.Fatalf("remove %+v", left)
	}
	// Every edit is in the log with its actor, role and reason; the giver heard of each.
	if n := f.count(`SELECT COUNT(*) FROM task_edits WHERE task_id=? AND actor=? AND role='holder' AND reason<>''`, f.task, f.b.ID); n != 5 {
		t.Fatalf("%d logged edits", n)
	}
	if f.inbox(f.a, "%Removed items%covered by the first item%") != 1 {
		t.Fatal("the giver was not told of the removal and its reason")
	}
	// A holder's edit notice does not wake the giver.
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=? AND wakes=1 AND consumed_at=''`, f.a.ID, f.task); n != 0 {
		t.Fatalf("%d waking notices to the giver", n)
	}
}

func TestHolderChangesTheDependenciesOfABlockedTask(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base work")
	waiting := f.delegateTo(f.g, root, f.c, "Part that waits", base.TaskID)
	if row := f.taskRow(waiting.TaskID); row.Status != "blocked" {
		t.Fatalf("status %s", row.Status)
	}
	rc := f.running(f.g.ID, f.c)
	call(t, f.tool(f.g, rc, "task_update"), map[string]any{"task_id": waiting.TaskID, "remove_depends_on": []string{base.TaskID}})
	if _, err := call(t, f.tool(f.g, rc, "task_update"), map[string]any{"task_id": waiting.TaskID, "remove_depends_on": []string{base.TaskID}, "reason": "the base work is not needed for this part"}); err != nil {
		t.Fatal(err)
	}
	if row := f.taskRow(waiting.TaskID); row.Status != "todo" || row.DependsOn != "" {
		t.Fatalf("after the holder removed the dependency: %+v", row)
	}
	if f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=? AND body LIKE '%waits on%'`, f.a.ID, waiting.TaskID) != 1 {
		t.Fatal("the giver was not told")
	}
}

func TestGiverEditsAreRecordedAndTheHolderIsTold(t *testing.T) {
	f := newEditFix(t)
	if _, err := f.update(f.root, map[string]any{"add_items": []string{"four"}}); codeOf(err) != "edit_reason_required" {
		t.Fatalf("giver edit without a reason: %v", err)
	}
	if _, err := f.update(f.root, map[string]any{"add_items": []string{"four"}, "reason": "one more check"}); err != nil {
		t.Fatal(err)
	}
	if f.inbox(f.b, "%Added items%one more check%") != 1 {
		t.Fatal("the holder was not told of the added item")
	}
	if _, err := f.update(f.root, map[string]any{"brief": "Review the report twice", "reason": "stricter review"}); err != nil {
		t.Fatal(err)
	}
	var wakes int
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=? AND wakes=1 AND body LIKE '%version 2%stricter review%'`, f.b.ID, f.task).Scan(&wakes); err != nil || wakes != 1 {
		t.Fatalf("the changed request does not wake the holder with its reason: %d %v", wakes, err)
	}
	if n := f.count(`SELECT COUNT(*) FROM task_edits WHERE task_id=? AND actor=? AND role='giver'`, f.task, f.a.ID); n != 2 {
		t.Fatalf("%d giver edits logged", n)
	}
	// The holder may not answer, accept or cancel.
	if _, err := f.update(f.holder, map[string]any{"status": "done"}); codeOf(err) != "task_creator_only" {
		t.Fatalf("holder accepts: %v", err)
	}
	if _, err := f.update(f.holder, map[string]any{"answer": "yes"}); codeOf(err) != "task_answer_giver_only" {
		t.Fatalf("holder answers: %v", err)
	}
}

func TestBlockedItemMadeNotNeededNeedsAReasonAndTellsTheGiver(t *testing.T) {
	f := newEditFix(t)
	items := f.taskRow(f.task).Checklist
	if _, err := f.update(f.holder, map[string]any{"items": []map[string]any{{"id": items[0].ID, "status": "blocked", "reason": "no access"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.update(f.holder, map[string]any{"items": []map[string]any{{"id": items[0].ID, "status": "skipped"}}}); codeOf(err) != "checklist_reason_required" {
		t.Fatalf("skip without a reason: %v", err)
	}
	if _, err := f.update(f.holder, map[string]any{"items": []map[string]any{{"id": items[0].ID, "status": "skipped", "reason": "the source is not needed any more"}}}); err != nil {
		t.Fatal(err)
	}
	if f.inbox(f.a, "%was blocked and is now skipped%not needed any more%") != 1 {
		t.Fatal("the giver was not told")
	}
	if n := f.count(`SELECT COUNT(*) FROM task_edits WHERE task_id=? AND kinds LIKE '%blocked_to_skipped%'`, f.task); n != 1 {
		t.Fatalf("%d logged", n)
	}
}

func TestHolderRemoveAndReAddDoesNotResetTheIdleBudget(t *testing.T) {
	f, holder := newIdleFix(t, 2)
	items := f.taskRow(f.first.TaskID).Checklist
	end := func(id string) {
		t.Helper()
		if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "items": []map[string]any{{"id": id, "status": "done", "reason": "finished"}}}); err != nil {
			t.Fatal(err)
		}
	}
	end(items[0].ID)
	for i := 0; i < 6; i++ {
		_ = f.message(i)
	}
	// The holder removes the item that counted and adds the same text again, and ends it.
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "reason": "restructure", "remove_items": []string{items[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "reason": "restructure", "add_items": []string{"step 0"}}); err != nil {
		t.Fatal(err)
	}
	for _, it := range f.taskRow(f.first.TaskID).Checklist {
		if it.Text == "step 0" {
			end(it.ID)
		}
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 6 {
		t.Fatalf("remove and add again restarted the count: %d", used)
	}
	// An item the holder adds is not progress, whatever its text.
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "reason": "split", "add_items": []string{"brand new step"}}); err != nil {
		t.Fatal(err)
	}
	for _, it := range f.taskRow(f.first.TaskID).Checklist {
		if it.Text == "brand new step" {
			end(it.ID)
		}
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 6 {
		t.Fatalf("an item the holder added restarted the count: %d", used)
	}
	// An item the giver adds is progress when it ends.
	if _, err := call(t, f.tool(f.g, f.root, "task_update"), map[string]any{"task_id": f.first.TaskID, "reason": "one more check", "add_items": []string{"giver step"}}); err != nil {
		t.Fatal(err)
	}
	for _, it := range f.taskRow(f.first.TaskID).Checklist {
		if it.Text == "giver step" {
			end(it.ID)
		}
	}
	if used := treeUsedTx(f.s.db, f.tree); used != 0 {
		t.Fatalf("an item the giver added is progress: %d", used)
	}
}

func TestRewritingAnEndedItemIsNotNewProgress(t *testing.T) {
	f, holder := newIdleFix(t, 2)
	item := f.taskRow(f.first.TaskID).Checklist[0].ID
	set := func(status string) {
		t.Helper()
		if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "items": []map[string]any{{"id": item, "status": status, "reason": "x"}}}); err != nil {
			t.Fatal(err)
		}
	}
	set("done")
	for i := 0; i < 5; i++ {
		_ = f.message(i)
	}
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": f.first.TaskID, "reason": "reword", "rewrite_items": []map[string]any{{"id": item, "text": "another text"}}}); err != nil {
		t.Fatal(err)
	}
	set("open")
	set("done")
	if used := treeUsedTx(f.s.db, f.tree); used != 5 {
		t.Fatalf("a rewritten item counted again: %d", used)
	}
}

func TestMergeWithinATreeCombinesChecklistsRewiresDependenciesAndPointsBack(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	one := f.delegateTo(f.g, root, f.b, "Write the intro")
	two := f.delegateTo(f.g, root, f.c, "Write the opening section")
	three := f.delegateTo(f.g, root, f.b, "Proofread both", two.TaskID)
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": one.TaskID, "reason": "items", "add_items": []string{"outline"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": two.TaskID, "reason": "items", "add_items": []string{"draft", "polish"}}); err != nil {
		t.Fatal(err)
	}
	// Refused without a reason; then the holder of the absorbing task merges.
	rb := f.running(f.g.ID, f.b)
	update := f.tool(f.g, rb, "task_update")
	if _, err := call(t, update, map[string]any{"task_id": one.TaskID, "absorb_task_id": two.TaskID}); codeOf(err) != "edit_reason_required" {
		t.Fatalf("merge without a reason: %v", err)
	}
	// The holder of one task cannot take another member's task that it neither gave nor holds.
	if _, err := call(t, update, map[string]any{"task_id": one.TaskID, "absorb_task_id": two.TaskID, "reason": "same section"}); codeOf(err) != "merge_forbidden" {
		t.Fatalf("merge of a task the actor has no part in: %v", err)
	}
	// The giver of both merges them.
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": one.TaskID, "absorb_task_id": two.TaskID, "reason": "a later message made them one section"}); err != nil {
		t.Fatal(err)
	}
	survivor, absorbed, dependent := f.taskRow(one.TaskID), f.taskRow(two.TaskID), f.taskRow(three.TaskID)
	if len(survivor.Checklist) != 3 {
		t.Fatalf("combined list %+v", survivor.Checklist)
	}
	if absorbed.Status != "cancelled" || absorbed.MergedInto != one.TaskID || !strings.Contains(absorbed.ResultSummary, one.TaskID) {
		t.Fatalf("absorbed %+v", absorbed)
	}
	if dependent.DependsOn != one.TaskID || dependent.Status != "blocked" {
		t.Fatalf("dependent %+v", dependent)
	}
	if f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND body LIKE '%was merged into%'`, f.c.ID) != 1 {
		t.Fatal("the holder of the absorbed task was not told")
	}
	if f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND body LIKE '%was merged into%'`, f.b.ID) != 1 {
		t.Fatal("the holder of the surviving task was not told")
	}
	// The absorbed task closing does not cancel the task that now waits on the survivor.
	if dependent.Status == "cancelled" {
		t.Fatal("the dependent was cancelled")
	}
	// The survivor finishing unlocks the dependent: the waits moved.
	if f.count(`SELECT COUNT(*) FROM waits WHERE target=? AND status='pending'`, two.TaskID) != 0 {
		t.Fatal("a wait still points at the absorbed task")
	}
	if _, err := f.s.updateTask(f.a.ID, TaskUpdate{TaskID: one.TaskID, Status: "done"}); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(three.TaskID); got.Status != "todo" {
		t.Fatalf("the dependent did not unlock: %s", got.Status)
	}
}

func TestMergeAcrossTreesIsRefused(t *testing.T) {
	f := newTaskFix(t)
	rootA := f.running(f.g.ID, f.a)
	one := f.delegateTo(f.g, rootA, f.c, "Tree one work")
	rootB := f.running(f.g.ID, f.b)
	two := f.delegateTo(f.g, rootB, f.c, "Tree two work")
	if f.taskRow(one.TaskID).RootTaskID == f.taskRow(two.TaskID).RootTaskID {
		t.Skip("the two delegations share a tree")
	}
	// c holds both tasks, so c has a part in both and the tree is the only reason to refuse.
	rc := f.running(f.g.ID, f.c)
	if _, err := call(t, f.tool(f.g, rc, "task_update"), map[string]any{"task_id": one.TaskID, "absorb_task_id": two.TaskID, "reason": "same work"}); codeOf(err) != "merge_other_tree" {
		t.Fatalf("cross-tree merge: %v", err)
	}
	if got := f.taskRow(two.TaskID); got.Status == "cancelled" || got.MergedInto != "" {
		t.Fatalf("the task was absorbed: %+v", got)
	}
}

func TestMergeRefusesSelfRootAndAncestor(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	first := f.delegateTo(f.g, root, f.b, "First")
	second := f.delegateTo(f.g, root, f.c, "Second")
	update := f.tool(f.g, root, "task_update")
	if _, err := call(t, update, map[string]any{"task_id": second.TaskID, "absorb_task_id": second.TaskID, "reason": "x"}); codeOf(err) != "merge_self" {
		t.Fatalf("self: %v", err)
	}
	if _, err := call(t, update, map[string]any{"task_id": second.TaskID, "absorb_task_id": first.TaskID, "reason": "x"}); codeOf(err) != "merge_root" {
		t.Fatalf("root: %v", err)
	}
}

// --- hidden small talk ---------------------------------------------------

func (f taskFix) cardIDs(conv string) []string {
	f.t.Helper()
	res := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conv+"/task-cards", nil))
	if res.Code != 200 {
		f.t.Fatalf("status %d", res.Code)
	}
	var body struct{ Tasks []WorkItem }
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	var ids []string
	for _, task := range body.Tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func (f taskFix) hiddenOf(id string) bool {
	f.t.Helper()
	return f.count(`SELECT hidden FROM work_items WHERE id=?`, id) == 1
}

func (f taskFix) taskEvents(id string) int {
	return f.count(`SELECT COUNT(*) FROM events WHERE type='work_item' AND data LIKE ?`, "%"+id+"%")
}

func TestTriageTaskForAOneTurnAnswerStaysHidden(t *testing.T) {
	f := newTriageFixture(t)
	f.post(t, f.group.ID, "What do the earnings tell us this quarter?", "tri-hidden")
	runs := settleTriageRuns(t, f.s, f.group.ID, 2)
	answer := answeringRun(runs)
	var id string
	if err := f.s.store.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, answer.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	task, _ := f.s.store.GetWorkItem(id)
	if task.Status != "done" {
		t.Fatalf("status %s", task.Status)
	}
	var hidden int
	_ = f.s.store.db.QueryRow(`SELECT hidden FROM work_items WHERE id=?`, id).Scan(&hidden)
	if hidden != 1 {
		t.Fatal("the task of a one-turn answer is visible")
	}
	cards, _ := f.s.store.taskCards(f.group.ID)
	if len(cards) != 0 {
		t.Fatalf("cards %+v", cards)
	}
	list, _ := f.s.store.ListWorkItems(f.group.ID, "", true)
	open, _ := f.s.store.ListWorkItems(f.group.ID, "", false)
	if len(list)+len(open) != 0 {
		t.Fatalf("the board lists it: %+v %+v", list, open)
	}
	var events int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM events WHERE type='work_item' AND data LIKE ?`, "%"+id+"%").Scan(&events)
	if events != 0 {
		t.Fatalf("%d card events for a hidden task", events)
	}
	// The record is kept.
	var revisions int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM task_revisions WHERE task_id=?`, id).Scan(&revisions)
	if revisions != 1 {
		t.Fatalf("revisions %d", revisions)
	}
}

func TestHiddenTaskBecomesVisibleOnEachSignOfALongTask(t *testing.T) {
	signs := map[string]func(t *testing.T, f taskFix, run Run, task WorkItem){
		"checklist": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			if _, err := call(t, f.tool(f.g, run, "task_update"), map[string]any{"task_id": task.ID, "reason": "plan", "add_items": []string{"one", "two"}}); err != nil {
				t.Fatal(err)
			}
		},
		"delegation": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			f.delegateTo(f.g, run, f.b, "Part of the work")
		},
		"wait": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			if _, err := call(t, f.tool(f.g, run, "wait_for"), map[string]any{"kind": "timer", "seconds": 60}); err != nil {
				t.Fatal(err)
			}
		},
		"claim": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			if _, err := call(t, f.tool(f.g, run, "claim_task"), map[string]any{"task_id": task.ID}); err != nil {
				t.Fatal(err)
			}
		},
		"pass": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			if _, err := call(t, f.tool(f.g, run, "pass_task"), map[string]any{"task_id": task.ID, "to_bot_id": f.b.ID, "reason": "fits better"}); err != nil {
				t.Fatal(err)
			}
		},
		"first round ends open": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			tx, err := f.s.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = taskFinishedOneTx(tx, run, task, Message{ID: "m-" + newID(), Content: "Working on it."}, sayOutcomePending); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		},
		"second run": func(t *testing.T, f taskFix, run Run, task WorkItem) {
			if ok, err := f.s.SetRunStatus(run.ID, "failed", "synthetic"); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if f.hiddenOf(task.ID) == false {
				t.Fatal("a failed first run showed the task")
			}
			retry, err := f.s.RetryRun(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.start(retry.ID)
		},
	}
	for name, sign := range signs {
		t.Run(name, func(t *testing.T) {
			f := newTaskFix(t)
			run, task := f.triageTask(f.a)
			if !f.hiddenOf(task.ID) || len(f.cardIDs(f.g.ID)) != 0 || f.taskEvents(task.ID) != 0 {
				t.Fatal("a triage task starts visible")
			}
			sign(t, f, run, task)
			if f.hiddenOf(task.ID) {
				t.Fatal("the task stayed hidden")
			}
			found := false
			for _, id := range f.cardIDs(f.g.ID) {
				found = found || id == task.ID
			}
			if !found {
				t.Fatalf("no card for the task after %s", name)
			}
			if f.taskEvents(task.ID) == 0 {
				t.Fatal("no card event when the task showed")
			}
		})
	}
}

func TestTaskCreatedBySomeoneElseIsVisibleAtOnce(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, err := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if err != nil {
		t.Fatal(err)
	}
	task := f.taskOf(child.ID)
	if f.hiddenOf(task.ID) {
		t.Fatal("a delegated task is hidden")
	}
}

func TestTaskUpdateDescriptionNamesEditRulesInSteAndFitsTheLimit(t *testing.T) {
	if len(taskUpdateToolDescription) > 1024 {
		t.Fatalf("task_update description is %d bytes", len(taskUpdateToolDescription))
	}
	for _, want := range []string{"Either side", "reason", "absorb", "recorded"} {
		if !strings.Contains(taskUpdateToolDescription, want) {
			t.Fatalf("the description lacks %q", want)
		}
	}
}
