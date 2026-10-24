package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// triageTaskFor makes the task of a routed person message for a running run of
// bot, the way AddHandoff does for triage.
func (f taskFix) triageTask(bot Bot) (Run, WorkItem) {
	f.t.Helper()
	run := f.running(f.g.ID, bot)
	tx, err := f.s.db.Begin()
	if err != nil {
		f.t.Fatal(err)
	}
	if err = recordTriageTaskTx(tx, f.g.ID, bot.ID, run.ID, run.TriggerMessageID); err != nil {
		f.t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		f.t.Fatal(err)
	}
	return run, f.taskOf(run.ID)
}

func (f taskFix) messageCount() int {
	return f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, f.g.ID)
}

func TestOnlyTheHolderClaimsAndClaimPostsNoMessage(t *testing.T) {
	f := newTaskFix(t)
	run, task := f.triageTask(f.a)
	if task.AssignerKind != "user" || task.AssignerBotID != "" || task.BotID != f.a.ID || task.ClaimedAt != "" {
		t.Fatalf("task %+v", task)
	}
	if _, err := call(t, f.tool(f.g, run, "claim_task"), map[string]any{"task_id": "nope"}); codeOf(err) != "task_not_found" {
		t.Fatalf("unknown task: %v", err)
	}
	other := f.running(f.g.ID, f.b)
	if _, err := call(t, f.tool(f.g, other, "claim_task"), map[string]any{"task_id": task.ID}); codeOf(err) != "task_not_held" {
		t.Fatalf("a member that does not hold the task claimed it: %v", err)
	}
	before, events := f.messageCount(), f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='message'`, f.g.ID)
	if _, err := call(t, f.tool(f.g, run, "claim_task"), map[string]any{"task_id": task.ID}); err != nil {
		t.Fatal(err)
	}
	got := f.taskOf(run.ID)
	if got.ClaimedAt == "" || got.Status != "in_progress" {
		t.Fatalf("claimed task %+v", got)
	}
	first := got.ClaimedAt
	if _, err := call(t, f.tool(f.g, run, "claim_task"), map[string]any{"task_id": task.ID}); err != nil {
		t.Fatal(err)
	}
	if again := f.taskOf(run.ID); again.ClaimedAt != first {
		t.Fatalf("a second claim moved the time: %s -> %s", first, again.ClaimedAt)
	}
	if f.messageCount() != before || f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='message'`, f.g.ID) != events {
		t.Fatal("a claim posted a message or a message event")
	}
	if f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='work_item'`, f.g.ID) == 0 {
		t.Fatal("a claim sent no task event")
	}
}

func TestOnlyTheHolderPassesAndTheGiverStays(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, err := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if err != nil {
		t.Fatal(err)
	}
	f.start(child.ID)
	task := f.taskOf(child.ID)
	if _, err := call(t, f.tool(f.g, root, "pass_task"), map[string]any{"task_id": task.ID, "to_bot_id": f.c.ID, "reason": "it fits"}); codeOf(err) != "task_not_held" {
		t.Fatalf("the giver passed a task it does not hold: %v", err)
	}
	pass := f.tool(f.g, child, "pass_task")
	outsider, _ := f.s.CreateBot("delta", "", "m")
	for name, args := range map[string]map[string]any{
		"pass_target_not_member": {"task_id": task.ID, "to_bot_id": outsider.ID, "reason": "r"},
		"pass_target_invalid":    {"task_id": task.ID, "to_bot_id": f.b.ID, "reason": "r"},
		"pass_reason_required":   {"task_id": task.ID, "to_bot_id": f.c.ID, "reason": "  "},
	} {
		if _, err := call(t, pass, args); codeOf(err) != name {
			t.Fatalf("want %s, got %v", name, err)
		}
	}
	if f.taskOf(child.ID).BotID != f.b.ID {
		t.Fatal("a refused pass moved the task")
	}
	if _, err := call(t, f.tool(f.g, child, "claim_task"), map[string]any{"task_id": task.ID}); err != nil {
		t.Fatal(err)
	}
	messages := f.messageCount()
	if _, err := call(t, pass, map[string]any{"task_id": task.ID, "to_bot_id": f.c.ID, "reason": "Charlie owns this area"}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE id=?`, task.ID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, _ := f.s.GetWorkItem(id)
	if got.BotID != f.c.ID || got.AssignerBotID != f.a.ID || got.AssignerKind != "bot" || got.Status != "todo" || got.ClaimedAt != "" {
		t.Fatalf("passed task %+v: the holder changes, the giver and the claim state do not carry over", got)
	}
	if got.AssigneeRunID == child.ID {
		t.Fatal("the task still follows the old run")
	}
	next, _ := f.s.GetRun(got.AssigneeRunID)
	if next.BotID != f.c.ID || next.Status != "queued" || next.ParentRunID != child.ID {
		t.Fatalf("new holder run %+v", next)
	}
	if f.messageCount() != messages {
		t.Fatal("a pass posted a message")
	}
	// The giver's wait still targets the same task.
	if f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND kind=? AND target=? AND status='pending'`, f.a.ID, waitTaskResult, task.ID) != 1 {
		t.Fatal("the giver's wait for the task changed")
	}
	if f.count(`SELECT COUNT(*) FROM bot_inbox WHERE bot_id=? AND task_id=? AND consumed_at=''`, f.c.ID, task.ID) != 1 {
		t.Fatal("the new holder was not told why")
	}
	// The old holder no longer holds it.
	if _, err := call(t, pass, map[string]any{"task_id": task.ID, "to_bot_id": f.a.ID, "reason": "back"}); codeOf(err) != "task_not_held" {
		t.Fatalf("the old holder passed again: %v", err)
	}
}

func TestAPassCannotCircleForever(t *testing.T) {
	f := newTaskFix(t)
	run, task := f.triageTask(f.a)
	bots := []Bot{f.a, f.b}
	holder := 0
	for i := 0; i < maxTaskPasses; i++ {
		to := 1 - holder
		if _, _, err := f.s.passTask(bots[holder].ID, run.ID, task.ID, bots[to].ID, "other fits"); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
		holder = to
	}
	if _, _, err := f.s.passTask(bots[holder].ID, run.ID, task.ID, bots[1-holder].ID, "again"); codeOf(err) != "pass_limit" {
		t.Fatalf("want pass_limit, got %v", err)
	}
}

func TestClaimAndPassAreOnDemandGroupTools(t *testing.T) {
	f := newTaskFix(t)
	run := f.running(f.g.ID, f.a)
	for _, name := range []string{"claim_task", "pass_task"} {
		if !onDemandTools[name] {
			t.Fatalf("%s is not on demand", name)
		}
		f.tool(f.g, run, name)
	}
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	dmRun := f.running(dm.ID, f.a)
	for _, tool := range f.srv.tools(dm, dmRun) {
		if tool.Name == "claim_task" || tool.Name == "pass_task" {
			t.Fatalf("%s is offered in a DM", tool.Name)
		}
	}
}

func TestTaskCardsEndpointGivesHolderClaimAndChecklist(t *testing.T) {
	f := newTaskFix(t)
	run, task := f.triageTask(f.a)
	if err := f.s.updateTaskAddItems(t, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, f.tool(f.g, run, "claim_task"), map[string]any{"task_id": task.ID}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+f.g.ID+"/task-cards", nil)
	res := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("status %d %s", res.Code, res.Body.String())
	}
	var body struct{ Tasks []WorkItem }
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tasks) != 1 {
		t.Fatalf("tasks %+v", body.Tasks)
	}
	got := body.Tasks[0]
	if got.BotID != f.a.ID || got.ClaimedAt == "" || len(got.Checklist) != 2 || got.Status != "in_progress" || !strings.Contains(got.Title, "synthetic") {
		t.Fatalf("card %+v", got)
	}
	if nf := httptest.NewRecorder(); func() int {
		f.srv.Handler().ServeHTTP(nf, httptest.NewRequest(http.MethodGet, "/api/conversations/nope/task-cards", nil))
		return nf.Code
	}() != 404 {
		t.Fatal("an unknown conversation is not 404")
	}
}

func (s *Store) updateTaskAddItems(t *testing.T, id string) error {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = addChecklistItemsTx(tx, id, []string{"one", "two"}); err != nil {
		return err
	}
	return tx.Commit()
}

func TestTaskEventsReachTheDelegatedChatToo(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, child, err := f.s.AddForwardHandoff(f.g.ID, f.a.ID, f.b.ID, root.ID, "Write the page")
	if err != nil {
		t.Fatal(err)
	}
	task := f.taskOf(child.ID)
	if task.WorkConversationID != f.b.DMConversationID || task.ConversationID != f.g.ID {
		t.Fatalf("task %+v", task)
	}
	for _, conv := range []string{f.g.ID, f.b.DMConversationID} {
		if f.count(`SELECT COUNT(*) FROM events WHERE conversation_id=? AND type='work_item'`, conv) == 0 {
			t.Fatalf("no task event in %s", conv)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+f.b.DMConversationID+"/task-cards", nil)
	res := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(res, req)
	if res.Code != 200 || !strings.Contains(res.Body.String(), task.ID) {
		t.Fatalf("the delegated chat lists no card: %d %s", res.Code, res.Body.String())
	}
}

func TestTriageMakesATaskTheChosenMemberHoldsAndAnOneTurnAnswerClosesIt(t *testing.T) {
	f := newTriageFixture(t)
	f.post(t, f.group.ID, "What do the earnings tell us this quarter?", "tri-task")
	runs := settleTriageRuns(t, f.s, f.group.ID, 2)
	answer := answeringRun(runs)
	var id string
	if err := f.s.store.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, answer.ID).Scan(&id); err != nil {
		t.Fatalf("the routed message made no task: %v", err)
	}
	task, _ := f.s.store.GetWorkItem(id)
	if task.BotID != f.stocks.ID || task.AssignerKind != "user" || task.AssignerBotID != "" || task.ConversationID != f.group.ID || !strings.Contains(task.Description, "earnings") {
		t.Fatalf("task %+v", task)
	}
	// The model answered in one turn and called no tool: the run closed the task.
	if task.Status != "done" || task.ClosedAt == "" {
		t.Fatalf("a one-turn answer left the task %s", task.Status)
	}
	var n int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM work_items`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d tasks for one message", n)
	}
	var waits, inbox int
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM waits`).Scan(&waits)
	_ = f.s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_inbox`).Scan(&inbox)
	if waits != 0 || inbox != 0 {
		t.Fatalf("a routed message left %d waits and %d inbox rows", waits, inbox)
	}
}

func TestTriagePromptListsUnfinishedTasksBounded(t *testing.T) {
	f := newTriageFixture(t)
	for i := 0; i < maxTriageTasks+8; i++ {
		title := "Open job " + strings.Repeat("x", 200)
		if i == 0 {
			title = "Lisbon trip plan"
		}
		if _, err := f.s.store.db.Exec(`INSERT INTO work_items(`+workItemColumns+`,root_task_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			newID(), f.group.ID, f.travel.ID, "task", title, "", "in_progress", nil, now(), stampFor(i), "", "t"+newID()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.store.db.Exec(`INSERT INTO work_items(`+workItemColumns+`,root_task_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), f.group.ID, f.chef.ID, "task", "Finished job", "", "done", nil, now(), now(), now(), "d"+newID()); err != nil {
		t.Fatal(err)
	}
	f.post(t, f.group.ID, "Earnings season is here", "tri-open")
	settleTriageRuns(t, f.s, f.group.ID, 2)
	system := f.e.triage[0].System
	if !strings.Contains(system, "Unfinished tasks of the group") || !strings.Contains(system, "held by Atlas ("+f.travel.ID+")") {
		t.Fatalf("triage prompt lacks the open tasks: %s", system)
	}
	if strings.Contains(system, "Finished job") {
		t.Fatal("a finished task is in the triage prompt")
	}
	if got := strings.Count(system, " held by "); got != maxTriageTasks {
		t.Fatalf("%d task lines, want the bound %d", got, maxTriageTasks)
	}
	if strings.Contains(system, strings.Repeat("x", maxTriageTaskTitles+1)) {
		t.Fatal("a task title is not bounded")
	}
}

func stampFor(i int) string {
	return "2026-10-23T00:00:" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + ".000000000Z"
}

func TestGroupPromptCarriesTheSecondPassTriagePrinciple(t *testing.T) {
	for _, want := range []string{"claim it", "pass it with a reason", "post nothing in the chat", "claim_task", "pass_task"} {
		if !strings.Contains(groupTriageGuidance, want) {
			t.Fatalf("the principle lacks %q", want)
		}
	}
}
