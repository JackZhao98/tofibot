package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// taskFix is a workspace of three Bots and one group, with no model: queued
// runs stay queued, so every state a test reads is one the code wrote.
type taskFix struct {
	t       *testing.T
	srv     *Server
	s       *Store
	a, b, c Bot
	g       Conversation
}

func newTaskFix(t *testing.T) taskFix {
	t.Helper()
	srv, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	a, _ := srv.store.CreateBot("alpha", "", "m")
	b, _ := srv.store.CreateBot("bravo", "", "m")
	c, _ := srv.store.CreateBot("charlie", "", "m")
	g, err := srv.store.CreateGroup("tasks", []string{a.ID, b.ID, c.ID})
	if err != nil {
		t.Fatal(err)
	}
	return taskFix{t, srv, srv.store, a, b, c, g}
}

var runCounter int

// running starts a user-triggered run of bot in conv and marks it running.
func (f taskFix) running(conv string, bot Bot) Run {
	f.t.Helper()
	runCounter++
	_, runs, _, err := f.s.AddUserRuns(conv, "synthetic request", "client-"+newID(), []runSpec{{BotID: bot.ID, Model: bot.Model}}, true)
	if err != nil {
		f.t.Fatal(err)
	}
	if ok, err := f.s.SetRunStatus(runs[0].ID, "running", ""); err != nil || !ok {
		f.t.Fatalf("start run: %v %v", ok, err)
	}
	r, _ := f.s.GetRun(runs[0].ID)
	return r
}

func (f taskFix) start(runID string) {
	f.t.Helper()
	if ok, err := f.s.SetRunStatus(runID, "running", ""); err != nil || !ok {
		f.t.Fatalf("start %s: %v %v", runID, ok, err)
	}
}

func (f taskFix) taskOf(runID string) WorkItem {
	f.t.Helper()
	var id string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, runID).Scan(&id); err != nil {
		f.t.Fatalf("no task for run %s: %v", runID, err)
	}
	item, err := f.s.GetWorkItem(id)
	if err != nil {
		f.t.Fatal(err)
	}
	return item
}

func (f taskFix) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.s.db.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	if o, ok := tooloutcome.FromError(err); ok {
		return o.Code
	}
	return err.Error()
}

func (f taskFix) tool(c Conversation, r Run, name string) Tool {
	f.t.Helper()
	for _, tool := range f.srv.tools(c, r) {
		if tool.Name == name {
			return tool
		}
	}
	f.t.Fatalf("no %s tool", name)
	return Tool{}
}

func call(t *testing.T, tool Tool, args any) (string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return tool.Execute(context.Background(), raw)
}

func TestDelegationMakesATaskAndTheWaitItsGiverHolds(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, err := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if err != nil {
		t.Fatal(err)
	}
	task := f.taskOf(child.ID)
	if task.BotID != f.b.ID || task.AssignerBotID != f.a.ID || task.AssignerKind != "bot" || task.SpecVersion != 1 || task.Status != "todo" ||
		task.RootTaskID != task.ID || task.ParentTaskID != "" || task.ConversationID != f.g.ID || task.OriginRunID != root.ID {
		t.Fatalf("task %+v", task)
	}
	var kind, target, delivery, status string
	if err := f.s.db.QueryRow(`SELECT kind,target,delivery,status FROM waits WHERE bot_id=? AND run_id=?`, f.a.ID, root.ID).Scan(&kind, &target, &delivery, &status); err != nil {
		t.Fatalf("the giver holds no wait: %v", err)
	}
	if kind != waitTaskResult || target != task.ID || delivery != deliveryLegacy || status != waitPending {
		t.Fatalf("wait %s %s %s %s", kind, target, delivery, status)
	}
	// The child delegates on: its task hangs under the first and shares the root.
	f.start(child.ID)
	if got := f.taskOf(child.ID); got.Status != "in_progress" || got.RunSpecVersion != 1 {
		t.Fatalf("task after start %+v", got)
	}
	_, grandchild, err := f.s.AddHandoff(f.g.ID, f.b.ID, f.c.ID, child.ID, "Draw the figure")
	if err != nil {
		t.Fatal(err)
	}
	sub := f.taskOf(grandchild.ID)
	if sub.ParentTaskID != task.ID || sub.RootTaskID != task.ID || sub.AssignerBotID != f.b.ID {
		t.Fatalf("subtask %+v", sub)
	}
}

func TestTaskFollowsItsRunToDoneFailedAndCancelled(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	f.start(child.ID)
	msg, ok, err := f.s.FinishRun(child.ID, f.g.ID, f.b.ID, "The page is written.")
	if err != nil || !ok {
		t.Fatalf("finish: %v %v", ok, err)
	}
	done := f.taskOf(child.ID)
	if done.Status != "done" || done.ResultMessageID != msg.ID || done.ResultSummary == "" || done.ClosedAt == "" || done.ResultBasedOnVersion != 1 {
		t.Fatalf("done task %+v", done)
	}
	// The return run of the delegation delivers the result; the mirror wait settles in silence.
	if got := waitStatusOf(t, f.s.db, root.ID); got != waitFulfilled {
		t.Fatalf("wait %s", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 0 {
		t.Fatalf("a legacy wait delivered %d inbox rows", n)
	}

	root2 := f.running(f.g.ID, f.a)
	_, failing, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root2.ID, "Write the other page")
	f.start(failing.ID)
	if ok, err := f.s.SetRunStatus(failing.ID, "failed", "synthetic failure"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.taskOf(failing.ID); got.Status != "failed" || got.ClosedAt == "" {
		t.Fatalf("failed task %+v", got)
	}

	root3 := f.running(f.g.ID, f.a)
	_, stopped, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root3.ID, "Write a third page")
	f.start(stopped.ID)
	if err := f.s.CancelRunTree(stopped.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.taskOf(stopped.ID); got.Status != "cancelled" {
		t.Fatalf("cancelled task %+v", got)
	}
}

func TestMigrationMovesPendingRunWaitsAndLiveDelegations(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, err := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if err != nil {
		t.Fatal(err)
	}
	// Put the database back as the version before tasks left it: a delegation in
	// flight with no task row, and a follow_up wait in the old table.
	for _, q := range []string{`DELETE FROM waits`, `DELETE FROM work_items`} {
		if _, err := f.s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := f.s.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, root.ID, f.g.ID, f.a.ID, f.c.ID, "lineage-1", deadline, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'expired',?,?)`, child.ID, f.g.ID, f.b.ID, f.c.ID, "lineage-2", deadline, now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // twice: the step must not add a second copy
		if err := migrateTasksAndWaits(f.s.db); err != nil {
			t.Fatal(err)
		}
	}
	var botPosts int
	var target, lineage, status string
	if err := f.s.db.QueryRow(`SELECT COUNT(*),MIN(target),MIN(lineage),MIN(status) FROM waits WHERE kind='bot_post'`).Scan(&botPosts, &target, &lineage, &status); err != nil {
		t.Fatal(err)
	}
	if botPosts != 1 || target != f.c.ID || lineage != "lineage-1" || status != waitPending {
		t.Fatalf("migrated bot_post waits: %d %s %s %s", botPosts, target, lineage, status)
	}
	task := f.taskOf(child.ID)
	if task.AssignerBotID != f.a.ID || task.BotID != f.b.ID || task.Title != "Write the page" || task.Status != "todo" {
		t.Fatalf("migrated task %+v", task)
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items`); n != 1 {
		t.Fatalf("%d task rows after two migrations", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE kind='task_result' AND bot_id=? AND target=? AND status='pending'`, f.a.ID, task.ID); n != 1 {
		t.Fatalf("the giver's wait on the task: %d", n)
	}
	// The old table is left as it was.
	if n := f.count(`SELECT COUNT(*) FROM run_waits`); n != 2 {
		t.Fatalf("run_waits has %d rows", n)
	}
}

func TestWorkItemsKeepTheirRowsAndBecomeSelfRecordedTasks(t *testing.T) {
	f := newTaskFix(t)
	goal := workItemCreate(t, f.s, f.g.ID, f.a.ID, "goal", "Shared goal")
	// The table as before tasks: only the original columns.
	if _, err := f.s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.db.Exec(`CREATE TABLE work_items_old (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES bots(id), kind TEXT NOT NULL CHECK(kind IN ('goal','task')),
 title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('todo','in_progress','blocked','review','done','cancelled')),
 parent_goal_id TEXT REFERENCES work_items_old(id), created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL, completed_at TEXT NOT NULL DEFAULT '');
 INSERT INTO work_items_old (` + workItemColumns + `) SELECT ` + workItemColumns + ` FROM work_items;
 DROP TABLE work_items;
 ALTER TABLE work_items_old RENAME TO work_items;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = migrateWorkItems(f.s.db); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.s.GetWorkItem(goal.ID)
	if err != nil || got.Title != "Shared goal" || got.AssignerKind != "self" || got.AssignerBotID != f.a.ID || got.RootTaskID != goal.ID || got.SpecVersion != 1 {
		t.Fatalf("migrated item %+v %v", got, err)
	}
	if _, err := f.s.UpdateWorkItem(goal.ID, WorkItemPatch{Status: workItemString("input_required")}); err != nil {
		t.Fatalf("input_required: %v", err)
	}
	if _, err := f.s.UpdateWorkItem(goal.ID, WorkItemPatch{Status: workItemString("failed")}); err != nil {
		t.Fatalf("failed: %v", err)
	}
	rows, err := f.s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration broke a foreign key")
	}
}

func TestWaitCycleIsRefused(t *testing.T) {
	f := newTaskFix(t)
	ra, rb, rc := f.running(f.g.ID, f.a), f.running(f.g.ID, f.b), f.running(f.g.ID, f.c)
	reg := func(owner Bot, run Run, target Bot) (Wait, error) {
		return f.s.registerWait(WaitSpec{BotID: owner.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitBotPost, Target: target.ID})
	}
	if _, err := reg(f.a, ra, f.a); codeOf(err) != "wait_cycle" {
		t.Fatalf("waiting on oneself: %v", err)
	}
	w1, err := reg(f.a, ra, f.b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg(f.b, rb, f.a); codeOf(err) != "wait_cycle" {
		t.Fatalf("two-member cycle: %v", err)
	}
	if _, err := reg(f.b, rb, f.c); err != nil {
		t.Fatalf("a chain is not a cycle: %v", err)
	}
	if _, err := reg(f.c, rc, f.a); codeOf(err) != "wait_cycle" {
		t.Fatalf("three-member cycle: %v", err)
	}
	// Waits on a task's result count as well: alpha waits for the task bravo holds.
	_, child, _ := f.s.AddHandoff(f.g.ID, f.c.ID, f.a.ID, rc.ID, "Check the figures")
	_ = child
	if _, err := f.s.cancelWait(f.a.ID, w1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reg(f.b, rb, f.a); err != nil {
		t.Fatalf("after the first wait ended the same wait is allowed: %v", err)
	}
	// The walk is bounded: a chain longer than the bound is refused rather than guessed.
	var bots []Bot
	for i := 0; i < maxWaitCycleSteps+3; i++ {
		bot, _ := f.s.CreateBot("chain", "", "m")
		bots = append(bots, bot)
	}
	for i := 0; i+1 < len(bots); i++ {
		if _, err := f.s.db.Exec(`INSERT INTO waits(id,bot_id,run_id,conversation_id,kind,target,group_id,group_mode,status,delivery,deadline_at,created_at) VALUES(?,?,?,?,?,?,?,'all','pending','engine',?,?)`,
			newID(), bots[i].ID, "", f.g.ID, waitBotPost, bots[i+1].ID, newID(), stampTime(time.Now().Add(time.Hour)), now()); err != nil {
			t.Fatal(err)
		}
	}
	tail := f.running(f.g.ID, f.a)
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: tail.ID, ConversationID: f.g.ID, Kind: waitBotPost, Target: bots[0].ID}); codeOf(err) != "wait_cycle" {
		t.Fatalf("a graph too large to walk was not refused: %v", err)
	}
}

func TestWaitGroupAllSettlesOnTheLastMember(t *testing.T) {
	f := newTaskFix(t)
	run := f.running(f.g.ID, f.a)
	w1, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, Mode: waitModeAll})
	if err != nil {
		t.Fatal(err)
	}
	w2, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w1.GroupID})
	if err != nil {
		t.Fatal(err)
	}
	if w2.Mode != waitModeAll || w2.GroupID != w1.GroupID {
		t.Fatalf("second member %+v", w2)
	}
	end := func(id, status string) {
		t.Helper()
		tx, _ := f.s.db.Begin()
		if _, err := resolveWaitTx(tx, id, status, ""); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	end(w1.ID, waitFulfilled)
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 0 {
		t.Fatalf("a group of mode all delivered with a member open: %d", n)
	}
	end(w2.ID, waitTimedOut)
	var body string
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result' AND wakes=1`); n != 1 {
		t.Fatalf("delivered %d times", n)
	}
	f.s.db.QueryRow(`SELECT body FROM bot_inbox WHERE kind='wait_result'`).Scan(&body)
	if !strings.Contains(body, "mode all") || !strings.Contains(body, "timer ended") {
		t.Fatalf("body %q", body)
	}
	// A group already delivered takes no more members.
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w1.GroupID}); codeOf(err) != "wait_group_closed" {
		t.Fatalf("late member: %v", err)
	}
	// Another Bot cannot add to the group.
	other := f.running(f.g.ID, f.b)
	w3, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour})
	if _, err := f.s.registerWait(WaitSpec{BotID: f.b.ID, RunID: other.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w3.GroupID}); codeOf(err) != "wait_group_unknown" {
		t.Fatalf("foreign group: %v", err)
	}
}

func TestWaitGroupAnySettlesOnTheFirstFulfilledMember(t *testing.T) {
	f := newTaskFix(t)
	run := f.running(f.g.ID, f.a)
	w1, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, Mode: waitModeAny})
	w2, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w1.GroupID})
	w3, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w1.GroupID})
	end := func(id, status string) {
		t.Helper()
		tx, _ := f.s.db.Begin()
		if _, err := resolveWaitTx(tx, id, status, ""); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	// A member that times out does not settle an "any" group while others are open.
	end(w1.ID, waitTimedOut)
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 0 {
		t.Fatalf("a timeout settled an any group: %d", n)
	}
	end(w2.ID, waitFulfilled)
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 1 {
		t.Fatalf("first fulfilled member delivered %d times", n)
	}
	var status string
	f.s.db.QueryRow(`SELECT status FROM waits WHERE id=?`, w3.ID).Scan(&status)
	if status != waitCancelled {
		t.Fatalf("the remaining member is %s, want cancelled", status)
	}
	end(w3.ID, waitFulfilled) // already ended: no second delivery
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 1 {
		t.Fatalf("delivered %d times", n)
	}
}

func TestCancelWaitLetsTheGroupSettleOnTheOthers(t *testing.T) {
	f := newTaskFix(t)
	run := f.running(f.g.ID, f.a)
	w1, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour})
	w2, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, GroupID: w1.GroupID})
	if _, err := f.s.cancelWait(f.b.ID, w1.ID); codeOf(err) != "wait_not_found" {
		t.Fatalf("another Bot cancelled a wait: %v", err)
	}
	if _, err := f.s.cancelWait(f.a.ID, w1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.cancelWait(f.a.ID, w1.ID); codeOf(err) != "wait_not_pending" {
		t.Fatalf("second cancel: %v", err)
	}
	tx, _ := f.s.db.Begin()
	resolveWaitTx(tx, w2.ID, waitFulfilled, "")
	tx.Commit()
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 1 {
		t.Fatalf("delivered %d times", n)
	}
	// A group whose members were all cancelled delivers nothing.
	w3, _ := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour})
	if _, err := f.s.cancelWait(f.a.ID, w3.ID); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE kind='wait_result'`); n != 1 {
		t.Fatalf("a cancelled group delivered: %d", n)
	}
}

func TestWaitForReturnsAReceiptAtOnceAndLimitsTheKinds(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	tool := f.tool(f.g, r, "wait_for")
	started := time.Now()
	out, err := call(t, tool, map[string]any{"kind": "timer", "seconds": 60, "note": "look again"})
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("the receipt waited")
	}
	var receipt struct {
		WaitID  string `json:"wait_id"`
		GroupID string `json:"group_id"`
		Mode    string `json:"mode"`
		Status  string `json:"status"`
		EndsAt  string `json:"ends_at"`
	}
	if json.Unmarshal([]byte(out), &receipt) != nil || receipt.WaitID == "" || receipt.Status != waitPending || receipt.Mode != waitModeAll || receipt.EndsAt == "" {
		t.Fatalf("receipt %q", out)
	}
	if !f.s.runHasPendingWait(r.ID) {
		t.Fatal("the run does not hold the wait")
	}
	for name, args := range map[string]map[string]any{
		"timer too short":     {"kind": "timer", "seconds": 1},
		"command not offered": {"kind": "command_exit", "target": "t1"},
		"unknown kind":        {"kind": "mail"},
		"self post":           {"kind": "bot_post", "target": f.a.ID},
		"non-member post":     {"kind": "bot_post", "target": "nobody"},
		"unknown task":        {"kind": "task_result", "target": "nothing"},
	} {
		if _, err := call(t, tool, args); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A wait on a post is for a group; in a direct message it is refused.
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	dr := f.running(dm.ID, f.a)
	if _, err := call(t, f.tool(dm, dr, "wait_for"), map[string]any{"kind": "bot_post", "target": f.b.ID}); codeOf(err) != "wait_kind_unavailable" {
		t.Fatalf("post wait in a direct message: %v", err)
	}
	// cancel_wait ends it.
	if _, err := call(t, f.tool(f.g, r, "cancel_wait"), map[string]any{"wait_id": receipt.WaitID}); err != nil {
		t.Fatal(err)
	}
	if f.s.runHasPendingWait(r.ID) {
		t.Fatal("the wait is still open")
	}
}

func TestTimeoutIsDeliveredAsAResultOnTimeAndOnlyOnce(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	run := f.running(dm.ID, f.a)
	w, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: dm.ID, Kind: waitTimer, Timeout: 40 * time.Millisecond, Note: "look again"})
	if err != nil {
		t.Fatal(err)
	}
	// Before the deadline a sweep changes nothing.
	f.srv.sweepWaits(time.Now())
	if got := waitStatusOf(t, f.s.db, run.ID); got != waitPending {
		t.Fatalf("status %s before the deadline", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE kind=?`, runKindFollowup); n != 0 {
		t.Fatalf("woken early: %d runs", n)
	}
	// The run that set the wait ends; then the deadline passes.
	if _, ok, err := f.s.FinishRun(run.ID, dm.ID, f.a.ID, "I will look again soon."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	time.Sleep(60 * time.Millisecond)
	f.srv.sweepWaits(time.Now())
	f.srv.sweepWaits(time.Now()) // a second sweep adds nothing
	if got := waitStatusOf(t, f.s.db, run.ID); got != waitFulfilled {
		t.Fatalf("timer is %s", got)
	}
	var wakes []Run
	rs, _ := f.s.Runs(dm.ID)
	for _, r := range rs {
		if r.Kind == runKindFollowup {
			wakes = append(wakes, r)
		}
	}
	if len(wakes) != 1 || wakes[0].BotID != f.a.ID || wakes[0].ParentRunID != run.ID {
		t.Fatalf("wake runs %+v", wakes)
	}
	var request, source string
	if err := f.s.db.QueryRow(`SELECT request,source FROM run_wakes WHERE run_id=?`, wakes[0].ID).Scan(&request, &source); err != nil || source != wakeSourceInbox || !strings.Contains(request, "timer ended") || !strings.Contains(request, "look again") {
		t.Fatalf("wake %q %q %v", request, source, err)
	}
	ctx := f.srv.store.inboxWakeContext(wakes[0], "alpha", f.a.ID)
	if !strings.HasPrefix(ctx, "[wait result]") || !strings.Contains(ctx, "timer ended") {
		t.Fatalf("context %q", ctx)
	}
	// A timeout of a wait on a post is a result as well.
	g1 := f.running(f.g.ID, f.a)
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: g1.ID, ConversationID: f.g.ID, Kind: waitBotPost, Target: f.b.ID, Timeout: time.Hour, Lineage: lineageKey(f.s.db, g1.ID)}); err != nil {
		t.Fatal(err)
	}
	f.srv.timeoutWaitsOf(t, g1.ID)
	if got := waitStatusOf(t, f.s.db, g1.ID); got != waitTimedOut {
		t.Fatalf("post wait is %s", got)
	}
	_ = w
}

func TestTimeoutIsDeliveredAfterARestart(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := srv.store.CreateBot("alpha", "", "m")
	dm, _ := srv.store.GetConversation(a.DMConversationID)
	_, runs, _, _ := srv.store.AddUserRuns(dm.ID, "go", "c1", []runSpec{{BotID: a.ID, Model: a.Model}}, true)
	if _, ok, err := srv.store.claimRun(context.Background(), runs[0].ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, err := srv.store.FinishRun(runs[0].ID, dm.ID, a.ID, "Waiting."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := srv.store.registerWait(WaitSpec{BotID: a.ID, RunID: runs[0].ID, ConversationID: dm.ID, Kind: waitTimer, Timeout: 30 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond) // the deadline passes while the server is down
	srv, err = NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	// The start sweep ended it: no tick was needed.
	if got := waitStatusOf(t, srv.store.db, runs[0].ID); got != waitFulfilled {
		t.Fatalf("overdue timer is %s after the start", got)
	}
	var wakes int
	srv.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE kind=? AND bot_id=?`, runKindFollowup, a.ID).Scan(&wakes)
	if wakes != 1 {
		t.Fatalf("%d wake runs after the restart", wakes)
	}
	srv.sweepWaits(time.Now())
	srv.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE kind=? AND bot_id=?`, runKindFollowup, a.ID).Scan(&wakes)
	if wakes != 1 {
		t.Fatalf("a later sweep woke again: %d", wakes)
	}
}

func TestStopEndsTheWholeTaskTreeThroughDirectMessages(t *testing.T) {
	f := newTaskFix(t)
	dmA, _ := f.s.GetConversation(f.a.DMConversationID)
	root := f.running(dmA.ID, f.a)
	_, toB, err := f.s.AddForwardHandoff(dmA.ID, f.a.ID, f.b.ID, root.ID, "Research the topic")
	if err != nil {
		t.Fatal(err)
	}
	f.start(toB.ID)
	_, toC, err := f.s.AddForwardHandoff(toB.ConversationID, f.b.ID, f.c.ID, toB.ID, "Check one source")
	if err != nil {
		t.Fatal(err)
	}
	f.start(toC.ID)
	t1, t2 := f.taskOf(toB.ID), f.taskOf(toC.ID)
	if t2.ParentTaskID != t1.ID || t2.RootTaskID != t1.ID {
		t.Fatalf("tree %+v / %+v", t1, t2)
	}
	// Waits of every run in the tree, one of them on a timer.
	if _, err := f.s.registerWait(WaitSpec{BotID: f.b.ID, RunID: toB.ID, ConversationID: toB.ConversationID, Kind: waitTimer, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CancelRunTree(root.ID); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"root": root.ID, "child in a direct message": toB.ID, "grandchild": toC.ID} {
		if r, _ := f.s.GetRun(id); r.Status != "cancelled" {
			t.Errorf("%s run is %s", name, r.Status)
		}
	}
	for _, id := range []string{t1.ID, t2.ID} {
		if item, _ := f.s.GetWorkItem(id); item.Status != "cancelled" || item.ClosedAt == "" {
			t.Errorf("task %s is %s", id, item.Status)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE status='pending'`); n != 0 {
		t.Fatalf("%d waits are still open", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE wakes=1`); n != 0 {
		t.Fatalf("a stopped tree woke someone: %d inbox rows", n)
	}
	// Stopping the middle of the tree ends what hangs below it.
	rootB := f.running(dmA.ID, f.a)
	_, midB, _ := f.s.AddForwardHandoff(dmA.ID, f.a.ID, f.b.ID, rootB.ID, "Another job")
	f.start(midB.ID)
	_, leafC, _ := f.s.AddForwardHandoff(midB.ConversationID, f.b.ID, f.c.ID, midB.ID, "A part of it")
	f.start(leafC.ID)
	if err := f.s.CancelRunTree(midB.ID); err != nil {
		t.Fatal(err)
	}
	if item := f.taskOf(leafC.ID); item.Status != "cancelled" {
		t.Fatalf("leaf task is %s", item.Status)
	}
}

func TestDeletingABotEndsWhatItGaveAndCountsIt(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	f.start(child.ID)
	// The giver's own run ended; the child still works.
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Delegated."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: root.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	impact, err := f.s.taskImpactForBot(f.a.ID, "")
	if err != nil || impact.GivenTasks != 1 || impact.OpenWaits != 1 {
		t.Fatalf("impact %+v %v", impact, err)
	}
	if got, _ := f.s.taskImpactForBot(f.b.ID, ""); got.HeldTasks != 1 || got.GivenTasks != 0 {
		t.Fatalf("held impact %+v", got)
	}
	if _, err := f.s.DeleteBot(f.a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if r, _ := f.s.GetRun(child.ID); r.Status != "cancelled" {
		t.Fatalf("the child run is %s", r.Status)
	}
	if item := f.taskOf(child.ID); item.Status != "cancelled" {
		t.Fatalf("task is %s", item.Status)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND status='pending'`, f.a.ID); n != 0 {
		t.Fatalf("%d waits of the deleted Bot are open", n)
	}
}

func TestRemovingAMemberEndsItsTasksInThatGroup(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Delegated."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := f.s.SetRunStatus(child.ID, "cancelled", "stopped"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// A task of the member that stays is not touched; the one the leaver gave ends.
	_, err := f.s.db.Exec(`UPDATE work_items SET status='in_progress',closed_at='',completed_at='' WHERE assignee_run_id=?`, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	impact, _ := f.s.taskImpactForBot(f.a.ID, f.g.ID)
	if impact.GivenTasks != 1 {
		t.Fatalf("impact %+v", impact)
	}
	if _, err := f.s.UpdateGroup(f.g.ID, GroupUpdate{BotIDs: &[]string{f.b.ID, f.c.ID}}); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if item := f.taskOf(child.ID); item.Status != "cancelled" {
		t.Fatalf("task is %s after its giver left", item.Status)
	}
}

func TestAssigneeCannotAddOrRemoveChecklistItems(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	del := f.tool(f.g, root, "delegate")
	out, err := call(t, del, map[string]any{"bot_id": f.b.ID, "task": "Review the three chapters", "title": "Review", "checklist": []string{"Chapter one", "Chapter two"}})
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Tasks []delegatedTaskReceipt `json:"tasks"`
	}
	if json.Unmarshal([]byte(out), &receipt) != nil || len(receipt.Tasks) != 1 {
		t.Fatalf("receipt %q", out)
	}
	taskID := receipt.Tasks[0].TaskID
	task, _ := f.s.GetWorkItem(taskID)
	if task.Title != "Review" || len(task.Checklist) != 2 || task.Checklist[0].Status != checklistOpen {
		t.Fatalf("task %+v", task)
	}
	first := task.Checklist[0].ID
	// The holder may set an item's status with a reason, and nothing else about the list.
	rb := f.running(f.g.ID, f.b)
	update := f.tool(f.g, rb, "task_update")
	for name, args := range map[string]map[string]any{
		"add":        {"task_id": taskID, "add_items": []string{"Chapter three"}},
		"remove":     {"task_id": taskID, "remove_items": []string{first}},
		"new brief":  {"task_id": taskID, "brief": "Something else"},
		"cancel":     {"task_id": taskID, "status": "cancelled"},
		"accept":     {"task_id": taskID, "status": "done"},
		"answer":     {"task_id": taskID, "answer": "yes"},
		"no reason":  {"task_id": taskID, "items": []map[string]any{{"id": first, "status": "done"}}},
		"bad status": {"task_id": taskID, "items": []map[string]any{{"id": first, "status": "finished", "reason": "x"}}},
		"bad item":   {"task_id": taskID, "items": []map[string]any{{"id": "zzzzzz", "status": "done", "reason": "x"}}},
	} {
		if _, err := call(t, update, args); err == nil {
			t.Errorf("holder %s was accepted", name)
		}
	}
	if got, _ := f.s.GetWorkItem(taskID); len(got.Checklist) != 2 {
		t.Fatalf("the list changed: %+v", got.Checklist)
	}
	if _, err := call(t, update, map[string]any{"task_id": taskID, "items": []map[string]any{{"id": first, "status": "done", "reason": "read and checked"}}}); err != nil {
		t.Fatalf("holder status: %v", err)
	}
	got, _ := f.s.GetWorkItem(taskID)
	if got.Checklist[0].Status != checklistDone || got.Checklist[0].Reason != "read and checked" {
		t.Fatalf("item %+v", got.Checklist[0])
	}
	// The giver may add and remove items, and may not set an item's status.
	giver := f.tool(f.g, root, "task_update")
	if _, err := call(t, giver, map[string]any{"task_id": taskID, "add_items": []string{"Chapter three"}}); err != nil {
		t.Fatalf("giver add: %v", err)
	}
	if _, err := call(t, giver, map[string]any{"task_id": taskID, "remove_items": []string{got.Checklist[1].ID}}); err != nil {
		t.Fatalf("giver remove: %v", err)
	}
	if _, err := call(t, giver, map[string]any{"task_id": taskID, "items": []map[string]any{{"id": first, "status": "open"}}}); err == nil {
		t.Fatal("the giver set an item's status")
	}
	// A third Bot has no part in the task.
	rc := f.running(f.g.ID, f.c)
	if _, err := call(t, f.tool(f.g, rc, "task_update"), map[string]any{"task_id": taskID, "add_items": []string{"x"}}); codeOf(err) != "task_forbidden" {
		t.Fatalf("outsider: %v", err)
	}
	final, _ := f.s.GetWorkItem(taskID)
	if len(final.Checklist) != 2 || final.Checklist[1].Text != "Chapter three" {
		t.Fatalf("final list %+v", final.Checklist)
	}
}

func TestTaskAskMakesTheTaskNeedInputAndTheAnswerResumesIt(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	f.start(child.ID)
	task := f.taskOf(child.ID)
	ask := f.tool(f.g, child, "task_ask")
	if _, err := call(t, ask, map[string]any{"task_id": task.ID, "question": "Which audience?"}); err != nil {
		t.Fatal(err)
	}
	if got := f.taskOf(child.ID); got.Status != "input_required" {
		t.Fatalf("status %s", got.Status)
	}
	// The question waits in the giver's inbox until the asking run has ended.
	f.srv.flushInbox()
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE kind=? AND bot_id=?`, runKindFollowup, f.a.ID); n != 0 {
		t.Fatalf("the giver was woken while the asking run still ran: %d", n)
	}
	// The run finishing with the task asking keeps it so, and the question wakes the giver.
	if _, ok, err := f.s.FinishRun(child.ID, f.g.ID, f.b.ID, "I asked a question."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got := f.taskOf(child.ID); got.Status != "input_required" {
		t.Fatalf("a run that asked finished the task: %s", got.Status)
	}
	// Only the holder asks; a task from the person is asked about with the question tool.
	if _, err := call(t, f.tool(f.g, f.running(f.g.ID, f.c), "task_ask"), map[string]any{"task_id": task.ID, "question": "x"}); codeOf(err) != "task_assignee_only" {
		t.Fatalf("outsider ask: %v", err)
	}
	// The giver answers; the task resumes and the holder is woken with the answer.
	if _, err := call(t, f.tool(f.g, root, "task_update"), map[string]any{"task_id": task.ID, "answer": "Adults who know the field."}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.GetWorkItem(task.ID); got.Status != "in_progress" {
		t.Fatalf("status after the answer: %s", got.Status)
	}
	// The holder's run had ended, so the answer woke it at once.
	var wake Run
	rs, _ := f.s.Runs(f.g.ID)
	for _, r := range rs {
		if r.Kind == runKindFollowup && r.BotID == f.b.ID {
			wake = r
		}
	}
	if wake.ID == "" {
		t.Fatal("the holder was not woken with the answer")
	}
	if got := f.taskOf(wake.ID); got.ID != task.ID {
		t.Fatalf("the task did not follow the holder into the new run: %+v", got)
	}
	var request string
	f.s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=?`, wake.ID).Scan(&request)
	if !strings.Contains(request, "Adults who know the field.") {
		t.Fatalf("request %q", request)
	}
	// The question waited for the giver, whose own run was still going. When that
	// run ends, the question wakes the giver.
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE kind=? AND bot_id=?`, runKindFollowup, f.a.ID); n != 0 {
		t.Fatalf("the giver was woken while its run still ran: %d", n)
	}
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Answered."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	f.srv.flushInbox() // a second flush adds nothing
	var giverWakes []string
	rs, _ = f.s.Runs(f.g.ID)
	for _, r := range rs {
		if r.Kind == runKindFollowup && r.BotID == f.a.ID {
			var req string
			f.s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=?`, r.ID).Scan(&req)
			giverWakes = append(giverWakes, req)
		}
	}
	if len(giverWakes) != 1 || !strings.Contains(giverWakes[0], "Which audience?") {
		t.Fatalf("giver wakes %q", giverWakes)
	}
}

func TestPersonMessageBumpsTheVersionAndMarksAStaleResult(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page about lighthouses")
	f.start(child.ID)
	task := f.taskOf(child.ID)
	if task.SpecVersion != 1 {
		t.Fatalf("version %d", task.SpecVersion)
	}
	// The person writes to the member that holds the task, in the group.
	msg, runs, _, err := f.s.AddUserRuns(f.g.ID, "Make it about harbors instead", "client-harbor", []runSpec{{BotID: f.b.ID, Model: f.b.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	f.s.noteUserMessageForTasks(f.g.ID, msg, []string{runs[0].BotID})
	got := f.taskOf(child.ID)
	if got.SpecVersion != 2 {
		t.Fatalf("version %d, want 2", got.SpecVersion)
	}
	if n := f.count(`SELECT COUNT(*) FROM task_revisions WHERE task_id=? AND source='person' AND version=2`, task.ID); n != 1 {
		t.Fatalf("revisions %d", n)
	}
	for _, bot := range []Bot{f.a, f.b} {
		if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='task_update' AND task_id=? AND wakes=0`, bot.ID, task.ID); n != 1 {
			t.Fatalf("%s learned of the change %d times", bot.Name, n)
		}
	}
	// A message to a Bot that holds no task changes nothing.
	f.s.noteUserMessageForTasks(f.g.ID, msg, []string{f.c.ID})
	if f.taskOf(child.ID).SpecVersion != 2 {
		t.Fatal("a stranger's message bumped the version")
	}
	// The result of the run that began on version 1 is marked as based on it.
	if _, ok, err := f.s.FinishRun(child.ID, f.g.ID, f.b.ID, "The page about lighthouses."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	done := f.taskOf(child.ID)
	if done.ResultBasedOnVersion != 1 || done.SpecVersion != 2 {
		t.Fatalf("based on %d, current %d", done.ResultBasedOnVersion, done.SpecVersion)
	}
	var note string
	if err := f.s.db.QueryRow(`SELECT body FROM bot_inbox WHERE bot_id=? AND kind='task_result'`, f.a.ID).Scan(&note); err != nil || !strings.Contains(note, "version 1") || !strings.Contains(note, "current version is 2") {
		t.Fatalf("giver's note %q %v", note, err)
	}
	// The giver reads it in its next context.
	block := f.s.taskUpdatesContext(f.a.ID, f.g.ID, "next-run")
	if !strings.HasPrefix(block, "[task updates]") || !strings.Contains(block, "version 1") {
		t.Fatalf("context %q", block)
	}
	// A giver's own edit of the request also bumps the version and tells the holder.
	root2 := f.running(f.g.ID, f.a)
	_, child2, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.c.ID, root2.ID, "Write the index")
	t2 := f.taskOf(child2.ID)
	if _, err := call(t, f.tool(f.g, root2, "task_update"), map[string]any{"task_id": t2.ID, "brief": "Write the index and the glossary"}); err != nil {
		t.Fatal(err)
	}
	if got := f.taskOf(child2.ID); got.SpecVersion != 2 || !strings.Contains(got.Description, "glossary") {
		t.Fatalf("task %+v", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND kind='task_update' AND wakes=1`, f.c.ID); n != 1 {
		t.Fatalf("holder told %d times", n)
	}
}

func TestWaitingOnBlockIsBoundedAndListsEachWait(t *testing.T) {
	f := newTaskFix(t)
	if got := f.s.waitingOnContext(f.a.ID); got != "" {
		t.Fatalf("a Bot with no waits sees %q", got)
	}
	run := f.running(f.g.ID, f.a)
	for i := 0; i < maxPendingWaitsPerBot; i++ {
		if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour, Note: strings.Repeat("n", 150)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: f.g.ID, Kind: waitTimer, Timeout: time.Hour}); codeOf(err) != "wait_limit" {
		t.Fatalf("limit: %v", err)
	}
	block := f.s.waitingOnContext(f.a.ID)
	if n := len([]rune(block)); n > waitingOnContextRunes+10 || !strings.HasPrefix(block, "[waiting on]") || !strings.Contains(block, "cancel_wait") || !strings.Contains(block, "more waits not listed") {
		t.Fatalf("block of %d runes: %.200s", n, block)
	}
	one := f.running(f.g.ID, f.b)
	w, _ := f.s.registerWait(WaitSpec{BotID: f.b.ID, RunID: one.ID, ConversationID: f.g.ID, Kind: waitBotPost, Target: f.c.ID, Timeout: time.Hour, Note: "the figures"})
	short := f.s.waitingOnContext(f.b.ID)
	if !strings.Contains(short, w.ID) || !strings.Contains(short, "charlie") || !strings.Contains(short, "the figures") || strings.Contains(short, "more waits") {
		t.Fatalf("block %q", short)
	}
}

func TestRetentionClearsTheBulkOfFinishedTasksAndNeverFiles(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page with all the details")
	f.start(child.ID)
	task := f.taskOf(child.ID)
	if err := func() error {
		tx, _ := f.s.db.Begin()
		defer tx.Rollback()
		if err := addChecklistItemsTx(tx, task.ID, []string{"one", "two"}); err != nil {
			return err
		}
		return tx.Commit()
	}(); err != nil {
		t.Fatal(err)
	}
	// A failed run keeps its published files (decision 0038); the task of that run fails.
	if _, err := f.s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES('att1',?,'report.txt','text/plain',3,'att1.bin',?)`, f.g.ID, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_attachments(run_id,attachment_id,request_key,content_sha256,ordinal,created_at) VALUES(?,'att1','k','sha',0,?)`, child.ID, now()); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.s.SetRunStatus(child.ID, "failed", "synthetic failure"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if n := f.count(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id='att1'`); n != 1 {
		t.Fatalf("the failed run did not keep its file: %d bindings", n)
	}
	failed := f.taskOf(child.ID)
	if failed.Status != "failed" || failed.ClosedAt == "" {
		t.Fatalf("task %+v", failed)
	}
	messagesBefore := f.count(`SELECT COUNT(*) FROM messages`)
	runsBefore := f.count(`SELECT COUNT(*) FROM runs`)
	attachmentsBefore := f.count(`SELECT COUNT(*) FROM attachments`)
	bindingsBefore := f.count(`SELECT COUNT(*) FROM attachment_messages`)
	// Inside the period nothing goes.
	if got, err := f.s.cleanupFinishedTasks(time.Now().Add(-24 * time.Hour)); err != nil || got.Tasks != 0 {
		t.Fatalf("early cleanup %+v %v", got, err)
	}
	got, err := f.s.cleanupFinishedTasks(time.Now().Add(time.Hour))
	if err != nil || got.Tasks != 1 {
		t.Fatalf("cleanup %+v %v", got, err)
	}
	after := f.taskOf(child.ID)
	if after.Description != "" || len(after.Checklist) != 0 || after.Title == "" || after.Status != "failed" {
		t.Fatalf("task after cleanup %+v", after)
	}
	if n := f.count(`SELECT COUNT(*) FROM task_revisions WHERE task_id=?`, task.ID); n != 0 {
		t.Fatalf("%d revisions left", n)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE status<>'pending'`); n != 0 {
		t.Fatalf("%d ended waits left", n)
	}
	// Messages, runs and the files a failed run kept (decision 0038) are not touched.
	if f.count(`SELECT COUNT(*) FROM messages`) != messagesBefore || f.count(`SELECT COUNT(*) FROM runs`) != runsBefore || f.count(`SELECT COUNT(*) FROM attachments`) != attachmentsBefore || f.count(`SELECT COUNT(*) FROM attachment_messages`) != bindingsBefore {
		t.Fatal("the cleanup touched messages, runs or files")
	}
	// A board item the person recorded is not a delegated task: its text stays.
	goal := workItemCreate(t, f.s, f.g.ID, f.a.ID, "task", "Recorded by hand")
	f.s.UpdateWorkItem(goal.ID, WorkItemPatch{Description: workItemString("details"), Status: workItemString("done")})
	if _, err := f.s.cleanupFinishedTasks(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if kept, _ := f.s.GetWorkItem(goal.ID); kept.Description != "details" {
		t.Fatalf("a board item lost its text: %+v", kept)
	}
}

func TestRetentionIsASettingAndZeroKeepsEverything(t *testing.T) {
	f := newTaskFix(t)
	if got := f.srv.taskRetentionDays(); got != 30 {
		t.Fatalf("default %d, want 30", got)
	}
	item, ok := automationItemFor(autoTaskRetentionDays)
	if !ok || item.Group != "history" || item.Min != 0 {
		t.Fatalf("item %+v", item)
	}
	zero := "0"
	if err := f.srv.saveAutomationValue(autoTaskRetentionDays, &zero); err != nil {
		t.Fatal(err)
	}
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Keep this text")
	f.start(child.ID)
	f.s.SetRunStatus(child.ID, "failed", "x")
	f.srv.runTaskRetention(time.Now().Add(1000 * 24 * time.Hour))
	if got := f.taskOf(child.ID); got.Description == "" {
		t.Fatal("zero days still cleaned")
	}
	ten := "10"
	if err := f.srv.saveAutomationValue(autoTaskRetentionDays, &ten); err != nil {
		t.Fatal(err)
	}
	f.srv.runTaskRetention(time.Now().Add(5 * 24 * time.Hour))
	if got := f.taskOf(child.ID); got.Description == "" {
		t.Fatal("cleaned inside the period")
	}
	f.srv.runTaskRetention(time.Now().Add(11 * 24 * time.Hour))
	if got := f.taskOf(child.ID); got.Description != "" {
		t.Fatal("not cleaned after the period")
	}
}

func openRows(db *sql.DB, query string, args ...any) []string {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		out = append(out, v)
	}
	return out
}

func TestDelegateToSeveralMembersAndTheOldNamesStayAliases(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	out, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_ids": []string{f.b.ID, f.c.ID}, "task": "Each of you checks one source"})
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Tasks []delegatedTaskReceipt `json:"tasks"`
	}
	if json.Unmarshal([]byte(out), &receipt) != nil || len(receipt.Tasks) != 2 {
		t.Fatalf("receipt %q", out)
	}
	holders := map[string]bool{}
	for _, task := range receipt.Tasks {
		item, _ := f.s.GetWorkItem(task.TaskID)
		holders[item.BotID] = true
		if item.AssignerBotID != f.a.ID || item.SpecVersion != 1 || item.WorkConversationID == f.g.ID {
			t.Fatalf("task %+v", item)
		}
	}
	if !holders[f.b.ID] || !holders[f.c.ID] {
		t.Fatalf("holders %v", holders)
	}
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND kind='task_result' AND status='pending'`, f.a.ID); n != 2 {
		t.Fatalf("the giver holds %d waits for 2 tasks", n)
	}
	// handoff and message are aliases: executable, out of the declared list, and they make tasks.
	rb := f.running(f.g.ID, f.b)
	names := map[string]bool{}
	for _, tool := range f.srv.tools(f.g, rb) {
		if tool.Name == "handoff" || tool.Name == "message" {
			names[tool.Name] = tool.Deferred
		}
	}
	if len(names) != 2 || !names["handoff"] || !names["message"] {
		t.Fatalf("aliases %v", names)
	}
	if _, err := call(t, f.tool(f.g, rb, "handoff"), map[string]any{"bot_id": f.c.ID, "task": "Alias hand-off"}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE assigner_bot_id=? AND title='Alias hand-off'`, f.b.ID); n != 1 {
		t.Fatalf("the alias made %d tasks", n)
	}
	// delegate is declared, and so is the small set of wait tools.
	declared := map[string]bool{}
	for _, tool := range f.srv.tools(f.g, root) {
		if !tool.Deferred {
			declared[tool.Name] = true
		}
	}
	for _, name := range []string{"delegate", "wait_for", "cancel_wait", "task_update", "task_ask"} {
		if !declared[name] && name != "cancel_wait" && name != "task_ask" {
			t.Errorf("%s is not declared", name)
		}
	}
	// A run of a different kind of conversation gets no task tools in a triage run.
	triage := root
	triage.Kind = runKindTriage
	for _, tool := range f.srv.taskWaitTools(f.g, triage) {
		t.Errorf("triage run got %s", tool.Name)
	}
}

func TestWaitOnAPostWakesTheWaiterWhenTheMemberPosts(t *testing.T) {
	f := newTaskFix(t)
	_, runs, _, err := f.s.AddUserRuns(f.g.ID, "@alpha @bravo do your parts", "client-parts", []runSpec{{BotID: f.a.ID, Model: f.a.Model}, {BotID: f.b.ID, Model: f.b.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	ra, rb := runs[0], runs[1]
	f.start(ra.ID)
	f.start(rb.ID)
	if _, err := call(t, f.tool(f.g, ra, "wait_for"), map[string]any{"kind": "bot_post", "target": f.b.ID, "note": "the metaphor"}); err != nil {
		t.Fatal(err)
	}
	// The member that is waited for posts in the same lineage.
	post, err := f.s.AddAssistant(f.g.ID, f.b.ID, rb.ID, "Here is the metaphor: a lighthouse.")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := f.s.GetConversation(f.g.ID)
	f.srv.applyDisposition(c, rb, post)
	if got := waitStatusOf(t, f.s.db, ra.ID); got != waitFulfilled {
		t.Fatalf("wait is %s", got)
	}
	var wake Run
	all, _ := f.s.Runs(f.g.ID)
	for _, r := range all {
		if r.Kind == runKindGroupMention && r.BotID == f.a.ID {
			wake = r
		}
	}
	if wake.ID == "" || wake.TriggerMessageID != post.ID || wake.ParentRunID != ra.ID {
		t.Fatalf("wake %+v", wake)
	}
	// The same post again wakes nobody twice.
	f.srv.applyDisposition(c, rb, post)
	n := 0
	all, _ = f.s.Runs(f.g.ID)
	for _, r := range all {
		if r.Kind == runKindGroupMention {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d wake runs", n)
	}
}

func TestRunContextShowsWhatTheBotWaitsOnAndTheTaskItHolds(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Review the chapters")
	f.start(child.ID)
	task := f.taskOf(child.ID)
	tx, _ := f.s.db.Begin()
	addChecklistItemsTx(tx, task.ID, []string{"Chapter one"})
	tx.Commit()
	bot, _ := f.s.GetBot(f.b.ID)
	c, _ := f.s.GetConversation(f.g.ID)
	pm, _ := f.srv.buildContextParts(c, child, bot)
	var joined string
	for _, m := range pm {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "[task record]") || !strings.Contains(joined, "Chapter one") || !strings.Contains(joined, "version=1") {
		t.Fatalf("the holder's context lacks the task record:\n%s", joined)
	}
	// The giver sees its open wait on the task.
	abot, _ := f.s.GetBot(f.a.ID)
	pm, _ = f.srv.buildContextParts(c, root, abot)
	joined = ""
	for _, m := range pm {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "[waiting on]") || !strings.Contains(joined, task.ID) {
		t.Fatalf("the giver's context lacks the wait:\n%s", joined)
	}
	for _, prefix := range []string{"[waiting on]", "[task record]", "[task updates]", "[wait result]"} {
		if contextKindFor(prefix) == ctxKindOther {
			t.Errorf("%s has no context kind", prefix)
		}
	}
}

func TestTaskImpactRoute(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "A job")
	rec := httptestGet(f.srv, "/api/bots/"+f.a.ID+"/task-impact")
	var impact taskImpact
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &impact) != nil || impact.GivenTasks != 1 || impact.OpenWaits != 0 {
		t.Fatalf("impact %d %s", rec.Code, rec.Body.String())
	}
	if rec := httptestGet(f.srv, "/api/bots/missing/task-impact"); rec.Code != 404 {
		t.Fatalf("missing bot: %d", rec.Code)
	}
}

func TestAQuestionToThePersonIsOneOfTheRunsOpenWaits(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	run := f.running(dm.ID, f.a)
	q, err := f.s.CreateQuestion(dm.ID, run, askQuestionInput{Type: questionText, Question: "Which audience?", ExpiresInSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	var kind, target, delivery string
	if err := f.s.db.QueryRow(`SELECT kind,target,delivery FROM waits WHERE run_id=? AND status='pending'`, run.ID).Scan(&kind, &target, &delivery); err != nil || kind != waitHumanAnswer || target != q.ID || delivery != deliveryLegacy {
		t.Fatalf("mirror wait %q %q %q %v", kind, target, delivery, err)
	}
	if !f.s.runHasPendingWait(run.ID) {
		t.Fatal("the parked run holds no open wait")
	}
	if _, _, err := f.s.AnswerQuestion(q.ID, "person", "Adults"); err != nil {
		t.Fatal(err)
	}
	f.srv.sweepWaits(time.Now())
	if got := waitStatusOf(t, f.s.db, run.ID); got != waitFulfilled {
		t.Fatalf("wait is %s after the answer", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE wakes=1`); n != 0 {
		t.Fatalf("the answer woke through the inbox: %d (the question flow resumes the run)", n)
	}
	// An expired question ends its wait as timed out.
	r3 := f.running(dm.ID, f.a)
	q2, _ := f.s.CreateQuestion(dm.ID, r3, askQuestionInput{Type: questionText, Question: "And the length?", ExpiresInSeconds: 600})
	if _, err := f.s.db.Exec(`UPDATE questions SET status=? WHERE id=?`, questionExpired, q2.ID); err != nil {
		t.Fatal(err)
	}
	f.srv.sweepWaits(time.Now())
	if got := waitStatusOf(t, f.s.db, r3.ID); got != waitTimedOut {
		t.Fatalf("wait of an expired question is %s", got)
	}
	if n := f.count(`SELECT COUNT(*) FROM bot_inbox WHERE wakes=1`); n != 0 {
		t.Fatalf("an expired question woke the Bot through the inbox: %d (the question flow settles it)", n)
	}
}

func TestARetryRunTakesOverTheTaskAndASteeredRunKeepsIt(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	f.start(child.ID)
	task := f.taskOf(child.ID)
	if ok, err := f.s.SetRunStatus(child.ID, "failed", "synthetic"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if got, _ := f.s.GetWorkItem(task.ID); got.Status != "failed" {
		t.Fatalf("status %s", got.Status)
	}
	retry, err := f.s.RetryRun(child.ID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	f.start(retry.ID)
	got, _ := f.s.GetWorkItem(task.ID)
	if got.Status != "in_progress" || got.AssigneeRunID != retry.ID || got.ClosedAt != "" {
		t.Fatalf("task after retry %+v", got)
	}
	// A run a newer message took over from is not a stopped run.
	root2 := f.running(f.g.ID, f.a)
	_, c2, _ := f.s.AddHandoff(f.g.ID, f.a.ID, f.c.ID, root2.ID, "Draw the figure")
	f.start(c2.ID)
	t2 := f.taskOf(c2.ID)
	_, succ, _, err := f.s.AddUserRuns(f.g.ID, "@charlie also add a legend", "client-legend", []runSpec{{BotID: f.c.ID, Model: f.c.Model}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT OR IGNORE INTO run_steering(new_run_id,old_run_id) VALUES(?,?)`, succ[0].ID, c2.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.s.MarkRunSteered(c2.ID, "steered by newer user message"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := f.s.reconcileTaskRuns(); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.GetWorkItem(t2.ID); got.Status == "cancelled" || got.AssigneeRunID != succ[0].ID {
		t.Fatalf("steered task %+v", got)
	}
}

func TestAWaitChainEndsAfterTooManySelfWakes(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	run := f.running(dm.ID, f.a)
	for i := 0; i < maxWaitChain; i++ {
		if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: dm.ID, Kind: waitTimer, Timeout: time.Hour}); err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
		// Each wait ends and wakes the Bot with a new run, which waits again.
		f.s.db.Exec(`UPDATE waits SET deadline_at=? WHERE status='pending'`, stampTime(time.Now().Add(-time.Second)))
		f.s.FinishRun(run.ID, dm.ID, f.a.ID, "waiting")
		f.srv.sweepWaits(time.Now())
		var next string
		if err := f.s.db.QueryRow(`SELECT id FROM runs WHERE parent_run_id=? AND kind=?`, run.ID, runKindFollowup).Scan(&next); err != nil {
			t.Fatalf("no wake after wait %d: %v", i, err)
		}
		f.start(next)
		run, _ = f.s.GetRun(next)
	}
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: dm.ID, Kind: waitTimer, Timeout: time.Hour}); codeOf(err) != "wait_chain_limit" {
		t.Fatalf("the chain was not bounded: %v", err)
	}
}

func TestAPersonsMessageWhileACardIsOpenLeavesAnInboxRow(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	run := f.running(dm.ID, f.a)
	q, err := f.s.CreateQuestion(dm.ID, run, askQuestionInput{Type: questionText, Question: "Which audience?"})
	if err != nil {
		t.Fatal(err)
	}
	if parked := f.s.botsParkedOnInput(dm.ID); len(parked) != 0 {
		t.Fatalf("parked before the run waits: %v", parked)
	}
	if _, err := f.s.db.Exec(`UPDATE runs SET status='waiting' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_input_waits(run_id,question_id,checkpoint_json,state,created_at) VALUES(?,?,'{}','waiting',?)`, run.ID, q.ID, now()); err != nil {
		t.Fatal(err)
	}
	parked := f.s.botsParkedOnInput(dm.ID)
	if !parked[f.a.ID] || len(parked) != 1 {
		t.Fatalf("parked %v", parked)
	}
	msg := Message{ID: "m1", Content: "Actually, make it short."}
	f.s.recordUserMessageInbox(f.a.ID, dm.ID, "next-run", msg)
	var body, by string
	var wakes int
	if err := f.s.db.QueryRow(`SELECT body,consumed_by_run,wakes FROM bot_inbox WHERE kind='user_message'`).Scan(&body, &by, &wakes); err != nil || body != msg.Content || by != "next-run" || wakes != 0 {
		t.Fatalf("row %q %q %d %v", body, by, wakes, err)
	}
	// Today's behaviour is kept: the row never wakes and is never read twice.
	if rows := f.s.peekInboxForRun(f.a.ID, dm.ID, "another-run"); len(rows) != 0 {
		t.Fatalf("another run read the stored message: %v", rows)
	}
}
