package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A pending answer is honest only while the run holds an open wait for the work.
func TestPendingAnswerIsAcceptedWhileTheRunHoldsAnOpenWait(t *testing.T) {
	args := `{"purpose":"answer","outcome":"pending","text":"I will look again when the timer ends."}`
	s, c, r, _ := outcomeFixture(t, "")
	if code := outcomeErrCode(sayOutcome(s, c, r, "w0", args)); code != "answer_pending_work" {
		t.Fatalf("without a wait: %q", code)
	}
	s, c, r, _ = outcomeFixture(t, "")
	w, err := s.store.registerWait(WaitSpec{BotID: r.BotID, RunID: r.ID, ConversationID: c.ID, Kind: waitTimer, Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err := sayOutcome(s, c, r, "w1", args); err != nil {
		t.Fatalf("with an open wait: %v", err)
	}
	// A wait that ended does not count.
	s, c, r, _ = outcomeFixture(t, "")
	w, _ = s.store.registerWait(WaitSpec{BotID: r.BotID, RunID: r.ID, ConversationID: c.ID, Kind: waitTimer, Timeout: time.Hour})
	if _, err := s.store.cancelWait(r.BotID, w.ID); err != nil {
		t.Fatal(err)
	}
	if code := outcomeErrCode(sayOutcome(s, c, r, "w2", args)); code != "answer_pending_work" {
		t.Fatalf("after the wait ended: %q", code)
	}
	// A delegation holds the wait for its result, so "delegated" with no wake is honest too.
	s, c, r, _ = outcomeFixture(t, "")
	helper, _ := s.store.CreateBot("helper", "", "m")
	if _, _, err := s.store.AddForwardHandoff(c.ID, r.BotID, helper.ID, r.ID, "Check the source"); err != nil {
		t.Fatal(err)
	}
	delegated := `{"purpose":"answer","outcome":"delegated","text":"The check is with my colleague."}`
	if err := sayOutcome(s, c, r, "w3", delegated); err != nil {
		t.Fatalf("delegated: %v", err)
	}
}

// An answer that says "done" while checklist items are open goes back once, and
// the send-back is the same one the pending check uses.
func TestChecklistReconciliationSendsBackOnceSharedWithPending(t *testing.T) {
	setup := func(t *testing.T) (*Server, Conversation, Run, *sayRunState, string) {
		s, c, r, st := outcomeFixture(t, "")
		giver, _ := s.store.CreateBot("giver", "", "m")
		id := newID()
		if _, err := s.store.db.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,assignee_run_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, c.ID, r.BotID, "task", "Review", "Review the chapters", "in_progress", nil, now(), now(), "", "bot", giver.ID, id, r.ID); err != nil {
			t.Fatal(err)
		}
		tx, _ := s.store.db.Begin()
		if err := addChecklistItemsTx(tx, id, []string{"Chapter one", "Chapter two"}); err != nil {
			t.Fatal(err)
		}
		tx.Commit()
		return s, c, r, st, id
	}
	done := `{"purpose":"answer","outcome":"done","text":"Everything is reviewed."}`

	s, c, r, st, id := setup(t)
	if code := outcomeErrCode(sayOutcome(s, c, r, "c1", done)); code != "answer_checklist_open" {
		t.Fatalf("first done answer: %q", code)
	}
	if _, ok := s.store.sayAnswer(r.ID); ok {
		t.Fatal("a refused answer was recorded")
	}
	if !st.pendingCheck.Load() {
		t.Fatal("the send-back did not use the run's one slot")
	}
	if err := sayOutcome(s, c, r, "c2", done); err != nil {
		t.Fatalf("the same answer sent again must pass: %v", err)
	}

	// Closing the items first lets the answer through at once.
	s, c, r, _, id = setup(t)
	items, _ := checklistTx(s.store.db, id)
	for _, it := range items {
		if _, err := s.store.updateTask(r.BotID, TaskUpdate{TaskID: id, Items: []ChecklistChange{{ID: it.ID, Status: checklistSkipped, Reason: "not needed"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sayOutcome(s, c, r, "c3", done); err != nil {
		t.Fatalf("items closed: %v", err)
	}

	// One slot per run: a pending send-back first leaves none for the checklist.
	s, c, r, _, _ = setup(t)
	if code := outcomeErrCode(sayOutcome(s, c, r, "c4", `{"purpose":"answer","outcome":"pending","text":"Will do."}`)); code != "answer_pending_work" {
		t.Fatalf("pending: %q", code)
	}
	if err := sayOutcome(s, c, r, "c5", done); err != nil {
		t.Fatalf("done after the slot was spent: %v", err)
	}

	// An answer submitted with task_id binds to that task and is checked against its list.
	s, c, r, _, id = setup(t)
	withTask := `{"purpose":"answer","outcome":"done","text":"Everything is reviewed.","task_id":"` + id + `"}`
	if code := outcomeErrCode(sayOutcome(s, c, r, "c6", withTask)); code != "answer_checklist_open" {
		t.Fatalf("task_id answer: %q", code)
	}
	if err := sayOutcome(s, c, r, "c7", withTask); err != nil {
		t.Fatal(err)
	}
	var submit string
	s.store.db.QueryRow(`SELECT submit_run_id FROM work_items WHERE id=?`, id).Scan(&submit)
	if submit != r.ID {
		t.Fatalf("submit run %q", submit)
	}
	// A task the Bot does not hold cannot be submitted for.
	s, c, r, _, id = setup(t)
	other, _ := s.store.CreateBot("other", "", "m")
	if _, err := s.store.db.Exec(`UPDATE work_items SET bot_id=? WHERE id=?`, other.ID, id); err != nil {
		t.Fatal(err)
	}
	if code := outcomeErrCode(sayOutcome(s, c, r, "c8", `{"purpose":"answer","outcome":"blocked","text":"x","task_id":"`+id+`"}`)); code != "task_not_held" {
		t.Fatalf("foreign task: %q", code)
	}
	_ = strings.TrimSpace
}

// The say tool offers task_id only to a Bot that holds a task.
func TestSayOffersTaskIDOnlyToABotThatHoldsATask(t *testing.T) {
	s, c, r, _ := outcomeFixture(t, "")
	tool := s.sayTool(c, r)
	if _, ok := tool.Parameters["properties"].(map[string]any)["task_id"]; ok || !tool.Strict {
		t.Fatalf("a Bot with no task sees task_id (strict=%v)", tool.Strict)
	}
	id := newID()
	giver, _ := s.store.CreateBot("giver", "", "m")
	if _, err := s.store.db.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, c.ID, r.BotID, "task", "Review", "", "todo", nil, now(), now(), "", "bot", giver.ID, id); err != nil {
		t.Fatal(err)
	}
	tool = s.sayTool(c, r)
	if _, ok := tool.Parameters["properties"].(map[string]any)["task_id"]; !ok || tool.Strict {
		t.Fatal("a Bot that holds a task does not see task_id")
	}
}

// What a run says while it holds a wait does not use the allowance of
// messages it had for its work (decision 0050 keeps three per run).
func TestMessagesWhileWaitingDoNotUseTheWorkAllowance(t *testing.T) {
	f := newTaskFix(t)
	dm, _ := f.s.GetConversation(f.a.DMConversationID)
	run := f.running(dm.ID, f.a)
	say := func(i int) error {
		_, err := f.s.PublishChatSegment(context.Background(), run, "call-"+itoa(i), "Beat "+itoa(i))
		return err
	}
	for i := 1; i <= maxChatSegments; i++ {
		if err := say(i); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := say(4); err == nil {
		t.Fatal("a fourth message with no wait was accepted")
	}
	if _, err := f.s.registerWait(WaitSpec{BotID: f.a.ID, RunID: run.ID, ConversationID: dm.ID, Kind: waitTimer, Timeout: time.Hour}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	for i := 4; i <= 3+maxChatSegments; i++ {
		if err := say(i); err != nil {
			t.Fatalf("message %d while waiting: %v", i, err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := say(10); err == nil {
		t.Fatal("the allowance while waiting is not bounded")
	}
}
