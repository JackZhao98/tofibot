package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type receiptTask struct {
	TaskID    string   `json:"task_id"`
	BotID     string   `json:"bot_id"`
	Status    string   `json:"status"`
	RunID     string   `json:"run_id"`
	BlockedBy []string `json:"blocked_by"`
}

// delegateTo calls the delegate tool the way a model does and reads the receipt.
func (f taskFix) delegateTo(c Conversation, run Run, bot Bot, task string, deps ...string) receiptTask {
	f.t.Helper()
	args := map[string]any{"bot_id": bot.ID, "task": task}
	if len(deps) > 0 {
		args["depends_on"] = deps
	}
	out, err := call(f.t, f.tool(c, run, "delegate"), args)
	if err != nil {
		f.t.Fatalf("delegate %q: %v", task, err)
	}
	var receipt struct {
		Tasks []receiptTask `json:"tasks"`
	}
	if err = json.Unmarshal([]byte(out), &receipt); err != nil || len(receipt.Tasks) != 1 {
		f.t.Fatalf("receipt %q: %v", out, err)
	}
	return receipt.Tasks[0]
}

func (f taskFix) taskRow(id string) WorkItem {
	f.t.Helper()
	item, err := f.s.GetWorkItem(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f taskFix) setSetting(key string, value int) {
	f.t.Helper()
	if _, err := f.s.db.Exec(`INSERT INTO automation_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, fmt.Sprint(value), now()); err != nil {
		f.t.Fatal(err)
	}
}

// wokenRun finds the inbox wake run of a Bot (kind group_followup, source inbox).
func (f taskFix) wokenRuns(bot Bot) []Run {
	f.t.Helper()
	rs, _ := f.s.Runs(f.g.ID)
	var out []Run
	for _, r := range rs {
		var source string
		if r.BotID == bot.ID && r.Kind == runKindFollowup && f.s.db.QueryRow(`SELECT source FROM run_wakes WHERE run_id=?`, r.ID).Scan(&source) == nil && source == wakeSourceInbox {
			out = append(out, r)
		}
	}
	return out
}

// Decomposition: an interface task, two parallel parts and a merge that waits on both.
func TestDependentTasksUnlockWhenTheLastDependencyIsDone(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	iface := f.delegateTo(f.g, root, f.b, "Agree the interface")
	partOne := f.delegateTo(f.g, root, f.c, "Build part one", iface.TaskID)
	partTwo := f.delegateTo(f.g, root, f.b, "Build part two", iface.TaskID)
	merge := f.delegateTo(f.g, root, f.c, "Merge the parts", partOne.TaskID, partTwo.TaskID)
	for _, task := range []receiptTask{partOne, partTwo, merge} {
		row := f.taskRow(task.TaskID)
		if task.Status != "blocked" || task.RunID != "" || row.Status != "blocked" || row.AssigneeRunID != "" || row.RootTaskID != iface.TaskID || row.AssignerBotID != f.a.ID {
			t.Fatalf("a task that waits must be blocked with no run: receipt=%+v row=%+v", task, row)
		}
	}
	if got := f.taskRow(merge.TaskID).DependsOn; got != partOne.TaskID+","+partTwo.TaskID {
		t.Fatalf("depends_on %q", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND id<>?`, f.c.ID, root.ID); n != 0 {
		t.Fatalf("a blocked task started %d runs", n)
	}
	// The giver holds an engine wait on each waiting task.
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND kind='task_result' AND delivery='engine' AND status='pending'`, f.a.ID); n != 3 {
		t.Fatalf("giver waits %d", n)
	}

	// The interface task ends done: both parts unlock in that transaction, the merge does not.
	ifaceRun := f.taskRow(iface.TaskID).AssigneeRunID
	f.start(ifaceRun)
	if _, ok, err := f.s.FinishRun(ifaceRun, f.g.ID, f.b.ID, "Interface agreed: two endpoints."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if one, two, m := f.taskRow(partOne.TaskID), f.taskRow(partTwo.TaskID), f.taskRow(merge.TaskID); one.Status != "todo" || two.Status != "todo" || m.Status != "blocked" {
		t.Fatalf("after the first dependency: %s %s %s", one.Status, two.Status, m.Status)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='task_update' AND wakes=1 AND task_id IN (?,?) AND consumed_at=''`, partOne.TaskID, partTwo.TaskID); n != 2 {
		t.Fatalf("unlock wake rows %d", n)
	}
	// Parallel: both holders wake, each with the brief and the result it needed.
	f.srv.flushInbox()
	cRuns, bRuns := f.wokenRuns(f.c), f.wokenRuns(f.b)
	if len(cRuns) != 1 || len(bRuns) != 1 {
		t.Fatalf("holders woken: charlie=%d bravo=%d", len(cRuns), len(bRuns))
	}
	var request string
	f.s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=?`, cRuns[0].ID).Scan(&request)
	if !strings.Contains(request, "Build part one") || !strings.Contains(request, "Interface agreed: two endpoints.") {
		t.Fatalf("wake request %q", request)
	}
	if row := f.taskRow(partOne.TaskID); row.AssigneeRunID != cRuns[0].ID {
		t.Fatalf("the woken run must hold the task: %+v", row)
	}
	f.start(cRuns[0].ID)
	if row := f.taskRow(partOne.TaskID); row.Status != "in_progress" {
		t.Fatalf("status %s", row.Status)
	}
	if _, ok, err := f.s.FinishRun(cRuns[0].ID, f.g.ID, f.c.ID, "Part one built."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if m := f.taskRow(merge.TaskID); m.Status != "blocked" {
		t.Fatalf("the merge started with a dependency still open: %s", m.Status)
	}
	f.start(bRuns[0].ID)
	if _, ok, err := f.s.FinishRun(bRuns[0].ID, f.g.ID, f.b.ID, "Part two built."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if m := f.taskRow(merge.TaskID); m.Status != "todo" {
		t.Fatalf("the merge did not unlock on its last dependency: %s", m.Status)
	}
	// The parts of one hand-out share a wait group: the giver hears once, after the last part.
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='wait_result'`, f.a.ID); n != 0 {
		t.Fatalf("the giver was told before the last part ended: %d", n)
	}
	f.srv.flushInbox()
	mergeRuns := f.wokenRuns(f.c)
	mergeRun := mergeRuns[len(mergeRuns)-1]
	f.start(mergeRun.ID)
	if _, ok, err := f.s.FinishRun(mergeRun.ID, f.g.ID, f.c.ID, "Merged."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='wait_result' AND wakes=1`, f.a.ID); n != 1 {
		t.Fatalf("giver wait results %d, want one for the whole hand-out", n)
	}
}

func TestADependencyThatIsDoneAlreadyStartsTheTaskAtOnce(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	first := f.delegateTo(f.g, root, f.b, "First")
	run := f.taskRow(first.TaskID).AssigneeRunID
	f.start(run)
	if _, ok, err := f.s.FinishRun(run, f.g.ID, f.b.ID, "First is done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	next := f.delegateTo(f.g, root, f.c, "Second", first.TaskID)
	if next.Status != "todo" {
		t.Fatalf("status %s", next.Status)
	}
	f.srv.flushInbox()
	if len(f.wokenRuns(f.c)) != 1 {
		t.Fatal("the holder of a task with no open dependency was not woken")
	}
}

func TestIndependentTasksInOneTurnRunInParallel(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	one := f.delegateTo(f.g, root, f.b, "Part one")
	two := f.delegateTo(f.g, root, f.c, "Part two") // the run's one direct hand-over is used; this one starts through the inbox
	if one.RunID == "" || two.RunID != "" || two.Status != "todo" {
		t.Fatalf("receipts %+v %+v", one, two)
	}
	if f.taskRow(two.TaskID).RootTaskID != one.TaskID {
		t.Fatal("the second task must join the tree of the first")
	}
	f.srv.flushInbox()
	if len(f.wokenRuns(f.c)) != 1 {
		t.Fatal("the second part did not start")
	}
}

func TestDependencyFailureAndCancelTellTheGiverAndDoNotUnlock(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base work")
	waiting := f.delegateTo(f.g, root, f.c, "Needs the base", base.TaskID)
	baseRun := f.taskRow(base.TaskID).AssigneeRunID
	f.start(baseRun)
	if ok, err := f.s.SetRunStatus(baseRun, "failed", "synthetic failure"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.taskRow(waiting.TaskID); got.Status != "blocked" || got.AssigneeRunID != "" {
		t.Fatalf("a failed dependency unlocked the task: %+v", got)
	}
	var body string
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='task_update' AND wakes=1 AND task_id=?`, f.a.ID, waiting.TaskID); n != 1 {
		t.Fatalf("giver notices %d", n)
	}
	f.s.db.QueryRow(`SELECT body FROM bot_inbox WHERE bot_id=? AND task_id=?`, f.a.ID, waiting.TaskID).Scan(&body)
	if !strings.Contains(body, "ended as failed") || !strings.Contains(body, "stays blocked") {
		t.Fatalf("body %q", body)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=?`, f.c.ID, waiting.TaskID); n != 0 {
		t.Fatalf("the holder was told to start: %d", n)
	}

	// Cancelled by the giver: the same.
	root2 := f.running(f.g.ID, f.a)
	base2 := f.delegateTo(f.g, root2, f.b, "Other base")
	waiting2 := f.delegateTo(f.g, root2, f.c, "Needs the other base", base2.TaskID)
	if _, err := call(t, f.tool(f.g, root2, "task_update"), map[string]any{"task_id": base2.TaskID, "status": "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(waiting2.TaskID); got.Status != "blocked" {
		t.Fatalf("status %s", got.Status)
	}
	f.s.db.QueryRow(`SELECT body FROM bot_inbox WHERE bot_id=? AND task_id=?`, f.a.ID, waiting2.TaskID).Scan(&body)
	if !strings.Contains(body, "ended as cancelled") {
		t.Fatalf("body %q", body)
	}
	// The giver can remove the dependency of a blocked task; with none left it starts.
	if _, err := call(t, f.tool(f.g, root2, "task_update"), map[string]any{"task_id": waiting2.TaskID, "reason": "synthetic edit", "remove_depends_on": []string{base2.TaskID}}); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRow(waiting2.TaskID); got.Status != "todo" || got.DependsOn != "" {
		t.Fatalf("after removing the dependency: %+v", got)
	}
}

func TestDependencyCycleAndBadDependenciesAreRefused(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base")
	one := f.delegateTo(f.g, root, f.c, "One", base.TaskID)
	two := f.delegateTo(f.g, root, f.b, "Two", one.TaskID)
	update := f.tool(f.g, root, "task_update")
	// base is waited on by one and two; making one wait on two closes a loop.
	if _, err := call(t, update, map[string]any{"task_id": one.TaskID, "reason": "synthetic edit", "add_depends_on": []string{two.TaskID}}); codeOf(err) != "dependency_cycle" {
		t.Fatalf("cycle: %v", err)
	}
	if got := f.taskRow(one.TaskID).DependsOn; got != base.TaskID {
		t.Fatalf("a refused change altered the list: %q", got)
	}
	if _, err := call(t, update, map[string]any{"task_id": one.TaskID, "reason": "synthetic edit", "add_depends_on": []string{one.TaskID}}); codeOf(err) != "dependency_cycle" {
		t.Fatalf("self: %v", err)
	}
	// Started tasks cannot take new dependencies.
	if _, err := call(t, update, map[string]any{"task_id": base.TaskID, "reason": "synthetic edit", "add_depends_on": []string{two.TaskID}}); codeOf(err) != "dependency_locked" {
		t.Fatalf("locked: %v", err)
	}
	// Unknown id, and tasks of two trees.
	delegate := f.tool(f.g, root, "delegate")
	if _, err := call(t, delegate, map[string]any{"bot_id": f.c.ID, "task": "x", "depends_on": []string{"no-such-task"}}); codeOf(err) != "dependency_not_found" {
		t.Fatalf("unknown: %v", err)
	}
	root2 := f.running(f.g.ID, f.a)
	otherBase := f.delegateTo(f.g, root2, f.b, "Other tree base")
	if _, err := call(t, f.tool(f.g, root2, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "x", "depends_on": []string{otherBase.TaskID, base.TaskID}}); codeOf(err) != "dependency_other_tree" {
		t.Fatalf("two trees: %v", err)
	}
	// The walk itself: a stored loop is found, and a graph too long to walk is refused.
	cycle, err := dependencyCycleTx(f.s.db, base.TaskID, []string{two.TaskID})
	if err != nil || !cycle {
		t.Fatalf("walk found no loop: %v %v", cycle, err)
	}
	if cycle, _ = dependencyCycleTx(f.s.db, "unrelated", []string{two.TaskID}); cycle {
		t.Fatal("an unrelated task is not on a loop")
	}
}

func publishAs(t *testing.T, s *Store, run Run, kind, id, text string) {
	t.Helper()
	if _, err := s.publishSay(context.Background(), run, "chat-segment:"+run.ID+":"+id, kind, text, false); err != nil {
		t.Fatal(err)
	}
}

// What the holder says goes to the giver's inbox: progress waits, a question and a result wake.
func TestHolderMessagesReachTheGiverProgressDoesNotWake(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	task := f.delegateTo(f.g, root, f.b, "Write the page")
	run, _ := f.s.GetRun(f.taskRow(task.TaskID).AssigneeRunID)
	f.start(run.ID)
	run, _ = f.s.GetRun(run.ID)
	publishAs(t, f.s, run, "segment", "m1", "I read the sources and start on the draft.")
	publishAs(t, f.s, run, "segment", "m2", "The draft is half done.")
	publishAs(t, f.s, run, "progress", "n1", "A working note stays with the holder.")
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='progress' AND task_id=? AND wakes=0 AND consumed_at=''`, f.a.ID, task.TaskID); n != 2 {
		t.Fatalf("progress rows %d (a note is not a message)", n)
	}
	if rows, _ := f.s.dueInboxRows(f.a.ID); len(rows) != 0 {
		t.Fatalf("progress asked for a wake: %+v", rows)
	}
	// They are context for the giver's next run.
	if block := f.s.taskUpdatesContext(f.a.ID, f.g.ID, "reader-run"); !strings.Contains(block, "half done") {
		t.Fatalf("giver context %q", block)
	}
	f.s.db.Exec(`UPDATE bot_inbox SET consumed_at='',consumed_by_run='' WHERE kind='progress'`)
	// A question wakes.
	if _, err := call(t, f.tool(f.g, run, "task_ask"), map[string]any{"task_id": task.TaskID, "question": "Which audience?"}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='task_question' AND wakes=1`, f.a.ID); n != 1 {
		t.Fatalf("question rows %d", n)
	}
	// A result carries the progress of its run: the progress rows are read with it.
	if _, ok, err := f.s.FinishRun(run.ID, f.g.ID, f.b.ID, "The page is written."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='progress' AND consumed_at=''`, f.a.ID); n != 0 {
		t.Fatalf("progress still unread after the result: %d", n)
	}
	// The messages count against the tree.
	if used := treeUsedTx(f.s.db, task.TaskID); used != 2 {
		t.Fatalf("tree used %d", used)
	}
}

// One stall report after N quiet rounds; again only after progress and another stall.
func TestStallIsReportedOnceAfterThreeQuietRoundsAndAgainOnlyAfterProgress(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	task := f.delegateTo(f.g, root, f.b, "Check three things")
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": task.TaskID, "reason": "synthetic edit", "add_items": []string{"one", "two", "three"}}); err != nil {
		t.Fatal(err)
	}
	items, _ := checklistTx(f.s.db, task.TaskID)
	// A round is a run of the holder that ends with the task still open.
	round := func() {
		t.Helper()
		r := queueRun(t, f.s, f.g.ID, f.b.ID, "", "")
		if _, err := f.s.db.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, r.ID, task.TaskID); err != nil {
			t.Fatal(err)
		}
		f.start(r.ID)
		if _, ok, err := f.s.FinishRun(r.ID, f.g.ID, f.b.ID, "Still working."); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	reports := func() int {
		return f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='task_update' AND wakes=1 AND body LIKE '%no change in its checklist%'`, f.a.ID)
	}
	for i := 1; i <= 2; i++ {
		round()
		if reports() != 0 {
			t.Fatalf("report after %d rounds", i)
		}
	}
	round()
	if reports() != 1 {
		t.Fatalf("no report after 3 quiet rounds")
	}
	round()
	round()
	if reports() != 1 {
		t.Fatalf("the report repeated: %d", reports())
	}
	var body string
	f.s.db.QueryRow(`SELECT body FROM bot_inbox WHERE bot_id=? AND body LIKE '%no change in its checklist%'`, f.a.ID).Scan(&body)
	if !strings.Contains(body, "has run 3 rounds") || !strings.Contains(body, task.TaskID) {
		t.Fatalf("report %q", body)
	}
	// Progress: the holder ends one item. The counter starts again.
	if _, err := f.s.updateTask(f.b.ID, TaskUpdate{TaskID: task.TaskID, Items: []ChecklistChange{{ID: items[0].ID, Status: "done", Reason: "checked"}}}); err != nil {
		t.Fatal(err)
	}
	round() // the round that saw the change resets the counter
	round()
	round()
	if reports() != 1 {
		t.Fatalf("a report came before three quiet rounds after progress: %d", reports())
	}
	round()
	if reports() != 2 {
		t.Fatalf("no second report after progress and three quiet rounds: %d", reports())
	}
}

func TestStallThresholdIsASetting(t *testing.T) {
	f := newTaskFix(t)
	f.setSetting(autoTaskStallRounds, 2)
	root := f.running(f.g.ID, f.a)
	task := f.delegateTo(f.g, root, f.b, "Quiet work")
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": task.TaskID, "reason": "synthetic edit", "add_items": []string{"one"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r := queueRun(t, f.s, f.g.ID, f.b.ID, "", "")
		f.s.db.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, r.ID, task.TaskID)
		f.start(r.ID)
		if _, ok, err := f.s.FinishRun(r.ID, f.g.ID, f.b.ID, "Working."); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND wakes=1 AND body LIKE '%has run 2 rounds%'`, f.a.ID); n != 1 {
		t.Fatalf("with a threshold of 2 the report must come after 2 rounds: %d", n)
	}
}

// The tree budget stops dispatch, and the root member and the person hear of it once.
func TestTreeLimitStopsDelegationAndReportsOnce(t *testing.T) {
	f := newTaskFix(t)
	const limit = 10
	f.setSetting(autoTaskTreeIdleLimit, limit)
	root := f.running(f.g.ID, f.a)
	first := f.delegateTo(f.g, root, f.b, "Root work")
	for i := 1; i < limit; i++ {
		f.delegateTo(f.g, root, f.c, fmt.Sprintf("Part %d", i), first.TaskID)
	}
	if used := treeUsedTx(f.s.db, first.TaskID); used != limit {
		t.Fatalf("used %d", used)
	}
	report := func() (int, int) {
		return f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND wakes=1 AND body LIKE '%limit%' AND root_task_id=?`, f.a.ID, first.TaskID),
			f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND notice_data LIKE '%tree_limit%'`, f.g.ID) +
				f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND data LIKE '%tree_limit%'`, f.g.ID)
	}
	if rows, notices := report(); rows != 1 || notices != 0 {
		t.Fatalf("report at the limit: rows=%d notices=%d", rows, notices)
	}
	for i := 0; i < 2; i++ {
		_, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "One more", "depends_on": []string{first.TaskID}})
		if codeOf(err) != "task_message_limit" {
			t.Fatalf("past the limit: %v", err)
		}
	}
	// A question inside the tree is refused the same way, and a person-facing answer is not blocked.
	run, _ := f.s.GetRun(f.taskRow(first.TaskID).AssigneeRunID)
	f.start(run.ID)
	if _, err := call(t, f.tool(f.g, run, "task_ask"), map[string]any{"task_id": first.TaskID, "question": "Anything?"}); codeOf(err) != "task_message_limit" {
		t.Fatalf("ask past the limit: %v", err)
	}
	if rows, notices := report(); rows != 1 || notices != 0 {
		t.Fatalf("the report repeated: rows=%d notices=%d", rows, notices)
	}
	// The root member is woken with it.
	f.srv.flushInbox()
	_ = f.running // the root run is still running; the row waits for it
}

// The giver's wait on a blocked task has the long ceiling; the usual deadline starts at unlock.
func TestWaitOnABlockedTaskStartsItsDeadlineAtUnlock(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base")
	waiting := f.delegateTo(f.g, root, f.c, "Waits", base.TaskID)
	deadline := func() time.Time {
		var d string
		if err := f.s.db.QueryRow(`SELECT deadline_at FROM waits WHERE kind='task_result' AND target=? AND status='pending'`, waiting.TaskID).Scan(&d); err != nil {
			t.Fatal(err)
		}
		at, err := time.Parse(time.RFC3339Nano, d)
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	if left := time.Until(deadline()); left < 23*time.Hour {
		t.Fatalf("a blocked task's wait must have the long ceiling, has %v", left)
	}
	run := f.taskRow(base.TaskID).AssigneeRunID
	f.start(run)
	if _, ok, err := f.s.FinishRun(run, f.g.ID, f.b.ID, "Base done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	left := time.Until(deadline())
	if left > waitTimeoutFor(waitTaskResult)+time.Minute || left < waitTimeoutFor(waitTaskResult)-time.Minute {
		t.Fatalf("after unlock the deadline must be the usual %v from now, is %v", waitTimeoutFor(waitTaskResult), left)
	}
}

// ---- Review round: one tree key everywhere, one reply, bounded waits ----

// reachLimit makes the giver's tree hit its budget through dependent tasks and
// returns the root task id.
func (f taskFix) reachLimit(root Run, limit int) string {
	f.t.Helper()
	f.setSetting(autoTaskTreeIdleLimit, limit)
	first := f.delegateTo(f.g, root, f.b, "Root work")
	for i := 1; i < limit; i++ {
		f.delegateTo(f.g, root, f.c, fmt.Sprintf("Part %d", i), first.TaskID)
	}
	return first.TaskID
}

// wokenRoot ends the giver's run and returns the run the inbox starts for it.
func (f taskFix) wokenRoot(root Run) Run {
	f.t.Helper()
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Delegated."); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	runs := f.wokenRuns(f.a)
	if len(runs) == 0 {
		f.t.Fatal("the giver was not woken")
	}
	f.start(runs[len(runs)-1].ID)
	r, _ := f.s.GetRun(runs[len(runs)-1].ID)
	return r
}

func TestAWokenRootCannotEscapeTheBudgetWithAFreshTree(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	tree := f.reachLimit(root, 10)
	woken := f.wokenRoot(root)
	_, err := call(t, f.tool(f.g, woken, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Escape into a fresh tree"})
	if codeOf(err) != "task_message_limit" {
		t.Fatalf("the woken root started new dispatch after the limit: %v", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE root_task_id=title OR (root_task_id<>? AND title='Escape into a fresh tree')`, tree); n != 0 {
		t.Fatalf("a fresh tree was opened: %d", n)
	}
	if _, err = call(t, f.tool(f.g, woken, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Escape with a wait", "depends_on": []string{tree}}); codeOf(err) != "task_message_limit" {
		t.Fatalf("dependent dispatch after the limit: %v", err)
	}
}

func TestAFailedDependencyWakesTheGiverIntoTheSameTree(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base work")
	f.delegateTo(f.g, root, f.c, "Needs the base", base.TaskID)
	baseRun := f.taskRow(base.TaskID).AssigneeRunID
	f.start(baseRun)
	f.s.SetRunStatus(baseRun, "failed", "synthetic")
	woken := f.wokenRoot(root)
	next := f.delegateTo(f.g, woken, f.c, "Another way")
	if got := f.taskRow(next.TaskID).RootTaskID; got != base.TaskID {
		t.Fatalf("the woken giver opened a new tree %q, want %q", got, base.TaskID)
	}
}

func TestParallelDelegatesGiveThePersonOneReturn(t *testing.T) {
	for _, classicFirst := range []bool{true, false} {
		f := newTaskFix(t)
		root := f.running(f.g.ID, f.a)
		one := f.delegateTo(f.g, root, f.b, "Part one") // direct hand-over
		two := f.delegateTo(f.g, root, f.c, "Part two") // through the inbox
		f.srv.flushInbox()
		cRun := f.wokenRuns(f.c)[0]
		bRun := f.taskRow(one.TaskID).AssigneeRunID
		if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Both are under way."); err != nil || !ok {
			t.Fatal(ok, err)
		}
		finish := func(run, bot, text string) {
			f.start(run)
			if _, ok, err := f.s.FinishRun(run, f.g.ID, bot, text); err != nil || !ok {
				t.Fatal(ok, err)
			}
			f.srv.flushInbox()
		}
		if classicFirst {
			finish(bRun, f.b.ID, "One done.")
			finish(cRun.ID, f.c.ID, "Two done.")
		} else {
			finish(cRun.ID, f.c.ID, "Two done.")
			finish(bRun, f.b.ID, "One done.")
		}
		_ = two
		f.srv.flushInbox()
		rs, _ := f.s.Runs(f.g.ID)
		n := 0
		for _, r := range rs {
			if r.BotID == f.a.ID && r.Kind == runKindFollowup {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("classicFirst=%v: the giver answered the person %d times, want 1", classicFirst, n)
		}
	}
}

func TestHolderProgressIsNotChargedAndTheAnswerToAnAskPassesTheLimit(t *testing.T) {
	f := newTaskFix(t)
	f.setSetting(autoTaskTreeIdleLimit, 10)
	root := f.running(f.g.ID, f.a)
	task := f.delegateTo(f.g, root, f.b, "Write")
	run, _ := f.s.GetRun(f.taskRow(task.TaskID).AssigneeRunID)
	f.start(run.ID)
	run, _ = f.s.GetRun(run.ID)
	for i := 0; i < 3; i++ {
		publishAs(t, f.s, run, "segment", fmt.Sprint("m", i), "A progress message.")
	}
	if used := treeUsedTx(f.s.db, task.TaskID); used != 1 {
		t.Fatalf("progress was charged: used=%d", used)
	}
	if _, err := call(t, f.tool(f.g, run, "task_ask"), map[string]any{"task_id": task.TaskID, "question": "Which one?"}); err != nil {
		t.Fatal(err)
	}
	// The budget is spent now.
	f.s.db.Exec(`UPDATE task_tree_budget SET messages=10 WHERE tree_key=?`, task.TaskID)
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": task.TaskID, "answer": "The first one."}); err != nil {
		t.Fatalf("the answer to a question must pass the limit: %v", err)
	}
}

func TestAnUnlockRowIsNeverReadIntoAWorkingRunAndOneTaskBindsPerRun(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base")
	t1 := f.delegateTo(f.g, root, f.c, "Task one", base.TaskID)
	t2 := f.delegateTo(f.g, root, f.c, "Task two", base.TaskID)
	run := f.taskRow(base.TaskID).AssigneeRunID
	f.start(run)
	if _, ok, err := f.s.FinishRun(run, f.g.ID, f.b.ID, "Base done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// Charlie is working on something else: a boundary read must leave the unlock rows alone.
	busy := f.running(f.g.ID, f.c)
	rows, err := f.s.drainInboxForRun(busy, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.TaskID == t1.TaskID || r.TaskID == t2.TaskID {
			t.Fatalf("an unlock row was read into a working run: %+v", r)
		}
	}
	if f.taskRow(t1.TaskID).AssigneeRunID != "" || f.taskRow(t2.TaskID).AssigneeRunID != "" {
		t.Fatal("a working run took a task it did not start for")
	}
	if _, ok, err := f.s.FinishRun(busy.ID, f.g.ID, f.c.ID, "Done with the other thing."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	bound := f.count(`SELECT COUNT(*) FROM work_items WHERE id IN (?,?) AND assignee_run_id<>''`, t1.TaskID, t2.TaskID)
	if bound != 1 {
		t.Fatalf("one woken run must hold exactly one unlocked task, %d are bound", bound)
	}
	first := f.wokenRuns(f.c)
	f.start(first[len(first)-1].ID)
	if _, ok, err := f.s.FinishRun(first[len(first)-1].ID, f.g.ID, f.c.ID, "Task done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	if f.count(`SELECT COUNT(*) FROM work_items WHERE id IN (?,?) AND assignee_run_id<>''`, t1.TaskID, t2.TaskID) != 2 {
		t.Fatal("the second unlocked task never started")
	}
}

func TestARoundWithAPendingWaitOrOpenChildTasksIsNotQuiet(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	task := f.delegateTo(f.g, root, f.b, "Coordinate")
	call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": task.TaskID, "reason": "synthetic edit", "add_items": []string{"one"}})
	round := func(prepare func(Run)) {
		r := queueRun(t, f.s, f.g.ID, f.b.ID, "", "")
		f.s.db.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, r.ID, task.TaskID)
		f.start(r.ID)
		if prepare != nil {
			prepare(r)
		}
		if _, ok, err := f.s.FinishRun(r.ID, f.g.ID, f.b.ID, "Still going."); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	reports := func() int {
		return f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND wakes=1 AND body LIKE '%no change in its checklist%'`, f.a.ID)
	}
	for i := 0; i < 5; i++ {
		round(func(r Run) {
			if _, err := f.s.registerWait(WaitSpec{BotID: f.b.ID, RunID: r.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour}); err != nil {
				t.Fatal(err)
			}
		})
	}
	if reports() != 0 {
		t.Fatalf("rounds that waited on something were reported as stalled")
	}
}

func TestCancellingASubtreeTellsTheGiversOfDependentsOutsideIt(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base")
	dependent := f.delegateTo(f.g, root, f.c, "Needs base", base.TaskID)
	tx, _ := f.s.db.Begin()
	if _, _, err := cancelTaskTreeTx(tx, []string{base.TaskID}, true); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND wakes=1 AND task_id=? AND body LIKE '%ended as cancelled%'`, f.a.ID, dependent.TaskID); n != 1 {
		t.Fatalf("the giver of the dependent was not told: %d", n)
	}
}

func TestAWideValidDependencyGraphPassesTheCycleCheck(t *testing.T) {
	f := newTaskFix(t)
	insert := func(id, deps string) {
		if _, err := f.s.db.Exec(`INSERT INTO work_items(id,conversation_id,bot_id,kind,title,description,status,created_at,updated_at,root_task_id,depends_on) VALUES(?,?,?,'task',?, '', 'blocked', ?, ?, 'r', ?)`, id, f.g.ID, f.b.ID, id, now(), now(), deps); err != nil {
			t.Fatal(err)
		}
	}
	var tops []string
	for chain := 0; chain < 12; chain++ {
		prev := ""
		for depth := 0; depth < 6; depth++ {
			id := fmt.Sprintf("c%d-%d", chain, depth)
			insert(id, prev)
			prev = id
		}
		tops = append(tops, prev)
	}
	cycle, err := dependencyCycleTx(f.s.db, "brand-new", tops)
	if err != nil || cycle {
		t.Fatalf("a wide valid graph was refused: cycle=%v err=%v", cycle, err)
	}
	// A shared node reached many times counts once.
	insert("shared", "")
	for i := 0; i < 40; i++ {
		insert(fmt.Sprint("fan", i), "shared")
	}
	var fans []string
	for i := 0; i < 12; i++ {
		fans = append(fans, fmt.Sprint("fan", i))
	}
	if cycle, err = dependencyCycleTx(f.s.db, "brand-new", fans); err != nil || cycle {
		t.Fatalf("shared nodes were counted twice: %v %v", cycle, err)
	}
}

func TestOriginRunLookupHasAnIndex(t *testing.T) {
	f := newTaskFix(t)
	var n int
	f.s.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('work_items') WHERE name='work_items_origin_run'`).Scan(&n)
	if n != 1 {
		t.Fatal("no index on work_items.origin_run_id")
	}
}

func TestLegacyBotPostWaitsAreCountedAndBounded(t *testing.T) {
	f := newTaskFix(t)
	f.setSetting(autoTaskTreeIdleLimit, 10)
	root := f.running(f.g.ID, f.a)
	var err error
	for i := 0; i < 12 && err == nil; i++ {
		_, err = f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: root.ID, ConversationID: f.g.ID, Kind: waitBotPost, Target: f.b.ID, Timeout: time.Hour})
	}
	if codeOf(err) != "task_message_limit" {
		t.Fatalf("waits on posts were not bounded by the tree budget: %v", err)
	}
}

// Triage tasks and a passed task's queued run resolve to the same tree as any other.
func TestTriageAndPassedRunsGiveIntoTheirTaskTree(t *testing.T) {
	f := newTaskFix(t)
	run := f.running(f.g.ID, f.b)
	tx, _ := f.s.db.Begin()
	if err := recordTriageTaskTx(tx, f.g.ID, f.b.ID, run.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	triage := f.taskOf(run.ID)
	if got := treeKeyForRunTx(f.s.db, run.ID); got != triage.ID {
		t.Fatalf("triage tree key %q, want the triage task %q", got, triage.ID)
	}
	child := f.delegateTo(f.g, run, f.c, "Sub work")
	if got := f.taskRow(child.TaskID).RootTaskID; got != triage.ID {
		t.Fatalf("a delegation from a triage task opened tree %q, want %q", got, triage.ID)
	}
	// Pass the triage task: the new holder's queued run is in the same tree.
	passed, newRun, err := f.s.passTask(f.b.ID, run.ID, triage.ID, f.a.ID, "fits alpha better")
	if err != nil || passed.ID != triage.ID {
		t.Fatal(err)
	}
	if got := treeKeyForRunTx(f.s.db, newRun.ID); got != triage.ID {
		t.Fatalf("the passed run resolves to %q, want %q", got, triage.ID)
	}
	f.start(newRun.ID)
	another := f.delegateTo(f.g, newRun, f.c, "More sub work")
	if got := f.taskRow(another.TaskID).RootTaskID; got != triage.ID {
		t.Fatalf("the passed holder opened tree %q", got)
	}
}
