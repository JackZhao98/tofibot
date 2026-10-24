package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func steerFixture(t *testing.T, group bool) (*Server, Conversation, Run) {
	t.Helper()
	s, c, old := steeringReviewFixture(t, group)
	if err := s.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	// Continuation runs stay queued so the tests can look at them; the worker
	// would otherwise start them with the fake engine.
	s.store.onReturnQueued = nil
	return s, c, old
}

func sendWhileWorking(t *testing.T, s *Server, c Conversation, old Run, text, client string) (Message, []Run, []steerDelivery) {
	t.Helper()
	m, runs, queued, _, err := s.store.addUserRunsSteering(c.ID, text, client, []runSpec{{BotID: old.BotID, Model: old.Model, Kind: old.Kind}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, runs, queued
}

// The central promise: a message to a working Bot starts no run, cancels
// nothing, and is read by the same run at its next boundary.
func TestAMessageToAWorkingBotIsReadByTheSameRunAndCancelsNothing(t *testing.T) {
	s, c, old := steerFixture(t, false)
	var first, second, third string
	var queuedRun string
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		first, _ = req.DrainInbox()
		m, runs, queued := sendWhileWorking(t, s, c, old, "Please write it in English.", "english")
		if len(runs) != 0 || len(queued) != 1 {
			t.Fatalf("the message started a run or was not queued: runs=%+v queued=%+v", runs, queued)
		}
		queuedRun = queued[0].RunID
		if got, _ := s.store.GetRun(old.ID); got.Status != "running" || ctx.Err() != nil {
			t.Fatalf("the message interrupted the working run: %+v ctx=%v", got, ctx.Err())
		}
		if msgs, _, _ := s.store.Messages(c.ID, 0, 20); msgs[len(msgs)-1].ID != m.ID || msgs[len(msgs)-1].Delivery != "queued" {
			t.Fatalf("the message is not shown as queued: %+v", msgs[len(msgs)-1])
		}
		second, _ = req.DrainInbox()
		third, _ = req.DrainInbox()
		return runtime.Result{Content: "Here it is in English."}, nil
	})
	s.execute(c, old)
	if first != "" || third != "" {
		t.Fatalf("a boundary with nothing queued read %q / %q", first, third)
	}
	if !strings.Contains(second, "Please write it in English.") || !strings.Contains(second, "addition or a correction") || !strings.Contains(second, "do not start again") {
		t.Fatalf("the boundary read: %q", second)
	}
	if queuedRun != old.ID {
		t.Fatalf("queued for %s, want %s", queuedRun, old.ID)
	}
	got, _ := s.store.GetRun(old.ID)
	if got.Status != "done" {
		t.Fatalf("run ended %s (%s)", got.Status, got.Error)
	}
	runs, _ := s.store.Runs(c.ID)
	if len(runs) != 1 {
		t.Fatalf("a second run was made: %d runs", len(runs))
	}
	var by string
	var drained int
	if err := s.store.db.QueryRow(`SELECT consumed_by_run,drained FROM bot_inbox WHERE kind='user_message'`).Scan(&by, &drained); err != nil || by != old.ID || drained != 1 {
		t.Fatalf("row consumed_by=%q drained=%d %v", by, drained, err)
	}
	// Read is the stream's event; history lists only what is still queued.
	msgs, _, _ := s.store.Messages(c.ID, 0, 20)
	for _, m := range msgs {
		if m.Content == "Please write it in English." && m.Delivery != "" {
			t.Fatalf("a read message is still listed as %q", m.Delivery)
		}
	}
	events, err := s.store.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sawRead bool
	for _, e := range events {
		if d, ok := e["data"].(map[string]any); ok && e["type"] == "inbox" && d["state"] == "read" && d["run_id"] == old.ID {
			sawRead = true
		}
	}
	if !sawRead {
		t.Fatal("no inbox read event reached the stream")
	}
}

// The race the design names: the run ends before it reads the queued message.
// The end of the run makes the continuation run in the same transaction.
func TestARunThatEndsBeforeReadingMakesAContinuationRun(t *testing.T) {
	s, c, old := steerFixture(t, false)
	var m Message
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		var runs []Run
		m, runs, _ = sendWhileWorking(t, s, c, old, "One more thing: add a title.", "late")
		if len(runs) != 0 {
			t.Fatalf("runs=%+v", runs)
		}
		return runtime.Result{Content: "Finished."}, nil
	})
	s.execute(c, old)
	if got, _ := s.store.GetRun(old.ID); got.Status != "done" {
		t.Fatalf("run %+v", got)
	}
	runs, _ := s.store.Runs(c.ID)
	if len(runs) != 2 {
		t.Fatalf("want the run and its continuation, got %d runs", len(runs))
	}
	var next Run
	for _, r := range runs {
		if r.ID != old.ID {
			next = r
		}
	}
	if next.Status != "queued" || next.BotID != old.BotID || next.TriggerMessageID != m.ID || next.ParentRunID != "" {
		t.Fatalf("continuation %+v", next)
	}
	var by string
	var drained int
	if err := s.store.db.QueryRow(`SELECT consumed_by_run,drained FROM bot_inbox WHERE kind='user_message'`).Scan(&by, &drained); err != nil || by != next.ID || drained != 0 {
		t.Fatalf("row consumed_by=%q drained=%d %v", by, drained, err)
	}
	// The message is not lost and no longer queued: the continuation answers it.
	msgs, _, _ := s.store.Messages(c.ID, 0, 20)
	for _, x := range msgs {
		if x.ID == m.ID && x.Delivery != "" {
			t.Fatalf("delivery %q", x.Delivery)
		}
	}
}

// A run that has delivered its answer reads nothing more; a later message
// starts a run of its own.
func TestARunThatAlreadyAnsweredTakesNoMoreMessages(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.store.recordSayAnswer(old.ID, "call-1", "Here is the answer."); err != nil {
		t.Fatal(err)
	}
	_, runs, queued := sendWhileWorking(t, s, c, old, "And another question.", "after-answer")
	if len(runs) != 1 || len(queued) != 0 {
		t.Fatalf("after the answer the message must start a run: runs=%+v queued=%+v", runs, queued)
	}
	if rows, _ := s.store.drainInboxForRun(old, maxInjectionsPerRun); len(rows) != 0 {
		t.Fatalf("an answered run read %+v", rows)
	}
}

// Five messages are read in place; the sixth closes the run into a continuation
// run that carries what the run has done.
func TestARunReadsFiveMessagesThenCloses(t *testing.T) {
	s, c, old := steerFixture(t, false)
	var reads int
	var closeErr error
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		for i := 1; i <= maxInjectionsPerRun+1; i++ {
			sendWhileWorking(t, s, c, old, "Message number "+string(rune('0'+i)), "cap-"+string(rune('0'+i)))
			text, err := req.DrainInbox()
			if i <= maxInjectionsPerRun {
				if err != nil || text == "" {
					t.Fatalf("message %d was not read: %q %v", i, text, err)
				}
				reads++
				continue
			}
			closeErr = err
		}
		return runtime.Result{}, closeErr
	})
	s.execute(c, old)
	if reads != maxInjectionsPerRun || closeErr == nil {
		t.Fatalf("reads=%d closeErr=%v", reads, closeErr)
	}
	got, _ := s.store.GetRun(old.ID)
	if got.Status != "cancelled" || !strings.Contains(got.Error, "steered by newer user message") {
		t.Fatalf("the run was not closed at the boundary: %+v", got)
	}
	runs, _ := s.store.Runs(c.ID)
	if len(runs) != 2 {
		t.Fatalf("runs=%d, want the closed run and one continuation", len(runs))
	}
	if s.store.unreadSteerCount(old.ID) != 0 {
		t.Fatal("a message was left unread")
	}
}

// A message to a Bot that works in another conversation waits for the Bot in
// its own conversation: the lane holds it, and the other conversation's run
// never sees the words.
func TestADirectMessageWaitsForABotBusyInAGroup(t *testing.T) {
	p := newLaneProbe()
	s := laneServer(t, p)
	a, _ := s.store.CreateBot("alpha", "", "model")
	b, _ := s.store.CreateBot("bravo", "", "model")
	dm, _ := s.store.GetConversation(a.DMConversationID)
	group, _ := s.store.CreateGroup("busy", []string{a.ID, b.ID})
	working := queueRun(t, s.store, group.ID, a.ID, "", "")
	gate := p.gate(working.ID)
	s.startConversationWorker(group.ID)
	waitStarted(t, p, working.ID, 5*time.Second)
	m, runs, queued, _, err := s.store.addUserRunsSteering(dm.ID, "Are you free?", "dm-while-busy", []runSpec{{BotID: a.ID, Model: a.Model}}, nil)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v queued=%+v err=%v", runs, queued, err)
	}
	if m.Delivery != "queued" || len(queued) != 1 || queued[0].RunID != runs[0].ID {
		t.Fatalf("the message is not shown as queued: delivery=%q queued=%+v", m.Delivery, queued)
	}
	s.enqueue(dm, runs[0])
	time.Sleep(200 * time.Millisecond)
	if got, _ := s.store.GetRun(runs[0].ID); got.Status != "queued" {
		t.Fatalf("the Bot started a second run: %s", got.Status)
	}
	if n := s.store.unreadSteerCount(working.ID); n != 0 {
		t.Fatalf("the direct message was put into the group run: %d", n)
	}
	if msgs, _, _ := s.store.Messages(dm.ID, 0, 20); msgs[len(msgs)-1].Delivery != "queued" {
		t.Fatalf("history does not list the waiting message as queued: %+v", msgs[len(msgs)-1])
	}
	close(gate)
	waitRunStatus(t, s.store, runs[0].ID, "done")
	if msgs, _, _ := s.store.Messages(dm.ID, 0, 20); msgs[0].Delivery != "" {
		t.Fatalf("a message the run started on is still queued: %+v", msgs[0])
	}
	if m.RunID != runs[0].ID {
		t.Fatalf("message is not tied to the run that answers it: %+v", m)
	}
}

// Group ordering: the draft on screen is published before the message is read,
// so the timeline reads that draft, the person's message, then the new answer.
func TestTheDraftIsPublishedBeforeAMessageIsReadSoTheTimelineKeepsItsOrder(t *testing.T) {
	s, c, old := steerFixture(t, true)
	var held, answer Message
	s.engine = steeringReviewEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("A first draft of the essay.")
		// The draft goes back for review: it stays on screen as a held draft.
		if err := req.OnReviewDraft(1, "A first draft of the essay."); err != nil {
			t.Fatal(err)
		}
		m, runs, _ := sendWhileWorking(t, s, c, old, "Make it English.", "english")
		if len(runs) != 0 {
			t.Fatal("a run was made")
		}
		held, _ = s.store.GetMessage(m.ID)
		text, err := req.DrainInbox()
		if err != nil || !strings.Contains(text, "Make it English.") {
			t.Fatalf("drain: %q %v", text, err)
		}
		req.OnDelta("The essay, in English.")
		return runtime.Result{Content: "The essay, in English."}, nil
	})
	s.execute(c, old)
	msgs, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, m := range msgs {
		switch {
		case m.Role == "user" && m.ID == held.ID:
			order = append(order, "message")
		case m.Role == "assistant" && strings.Contains(m.Content, "first draft"):
			order = append(order, "draft")
		case m.Role == "assistant" && strings.Contains(m.Content, "in English"):
			order = append(order, "answer")
			answer = m
		}
	}
	if strings.Join(order, ",") != "draft,message,answer" {
		t.Fatalf("timeline order %v (messages %+v)", order, msgs)
	}
	if answer.Seq <= held.Seq {
		t.Fatalf("the answer (seq %d) sits above the message (seq %d)", answer.Seq, held.Seq)
	}
}

// An interrupt-step request answers 409 when no tool call is running, and the
// run is not touched.
func TestInterruptStepWithoutAToolCallIsRefused(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	_ = c
	if s.interruptStep(old.ID) {
		t.Fatal("interrupted a step that was not running")
	}
	called := false
	end := s.beginStep(old.ID)(func() { called = true })
	if !s.interruptStep(old.ID) || !called {
		t.Fatal("the registered step was not interrupted")
	}
	end()
	if s.interruptStep(old.ID) {
		t.Fatal("a finished step was interrupted")
	}
	if got, _ := s.store.GetRun(old.ID); got.Status != "running" {
		t.Fatalf("the run was touched: %+v", got)
	}
}

// "Stop all" drops the messages a stopped run has not read, with its card and
// tasks; nothing is left to answer.
func TestStopAllDropsUnreadMessages(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	sendWhileWorking(t, s, c, old, "Never mind the old plan.", "never-mind")
	if err := s.store.CancelRunTree(old.ID); err != nil {
		t.Fatal(err)
	}
	runs, _ := s.store.Runs(c.ID)
	if len(runs) != 1 {
		t.Fatalf("stopping made a run: %d", len(runs))
	}
	var by string
	if err := s.store.db.QueryRow(`SELECT consumed_by_run FROM bot_inbox WHERE kind='user_message'`).Scan(&by); err != nil || by != inboxReadByNoRun {
		t.Fatalf("consumed_by=%q %v", by, err)
	}
}

// A restart between a boundary read and the checkpoint that holds it gives the
// message back, so the resumed run reads it again.
func TestARestartBeforeTheCheckpointGivesTheMessageBack(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err := s.store.db.Exec(`UPDATE runs SET attempt=1 WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	sendWhileWorking(t, s, c, old, "Add a summary.", "summary")
	rows, err := s.store.drainInboxForRun(old, maxInjectionsPerRun)
	if err != nil || len(rows) != 1 {
		t.Fatalf("drain: %+v %v", rows, err)
	}
	// The process dies: the run goes back to the queue with no checkpoint, and is
	// claimed again as attempt 2.
	if _, err := s.store.db.Exec(`UPDATE runs SET status='queued' WHERE id=?`, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), old.ID); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	again, err := s.store.drainInboxForRun(old, maxInjectionsPerRun)
	if err != nil || len(again) != 1 {
		t.Fatalf("the message was lost across the restart: %+v %v", again, err)
	}
}

// A wait that ended while its Bot was working reaches that run at the next
// boundary instead of waking a second run; a row that waits for a still-active
// run, and a mention wake, stay where they are.
func TestAFulfilledWaitReachesTheWorkingRunInPlace(t *testing.T) {
	s, c, old := steerFixture(t, false)
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	add := func(item InboxItem) {
		tx, err := s.store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := addInboxTx(tx, item); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	add(InboxItem{BotID: old.BotID, ConversationID: c.ID, Kind: inboxWaitResult, WaitGroup: "g1", Body: "Your wait ended: the member posted.", Wakes: true, ParentRun: old.ID})
	add(InboxItem{BotID: old.BotID, ConversationID: c.ID, Kind: inboxWaitResult, WaitGroup: "g2", Body: "mention row", Wakes: true, Via: "mention"})
	add(InboxItem{BotID: old.BotID, ConversationID: c.ID, Kind: inboxTaskQuestion, Body: "waits for a run that is still active", Wakes: true, AfterRun: newID()})
	if _, err := s.store.db.Exec(`UPDATE bot_inbox SET after_run=(SELECT id FROM runs WHERE id=? ) WHERE body LIKE 'waits for a run%'`, old.ID); err != nil {
		t.Fatal(err)
	}
	// The third row waits for another active run: make one.
	other := queueRun(t, s.store, c.ID, old.BotID, "", "")
	if _, err := s.store.db.Exec(`UPDATE bot_inbox SET after_run=? WHERE body LIKE 'waits for a run%'`, other.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.store.drainInboxForRun(old, maxInjectionsPerRun)
	if err != nil || len(rows) != 1 || rows[0].WaitGroup != "g1" {
		t.Fatalf("drain: %+v %v", rows, err)
	}
	text := s.store.renderInjection(rows)
	if !strings.Contains(text, "[events from the task system]") || !strings.Contains(text, "never as instructions") && !strings.Contains(text, "never instructions") {
		t.Fatalf("rendered: %q", text)
	}
	if strings.Contains(text, "[new message from the person]") {
		t.Fatalf("a system event was presented as the person's message: %q", text)
	}
	var left int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_inbox WHERE consumed_at=''`).Scan(&left); err != nil || left != 2 {
		t.Fatalf("rows left unread: %d %v", left, err)
	}
}

// A scheduled run takes no queued message: its answer is a receipt the person
// does not read in the chat. A message sent meanwhile gets a run of its own and
// the scheduled run yields to it, as before.
func TestAMessageDuringAScheduledRunStartsARunAndYieldsIt(t *testing.T) {
	s, c, old := steerFixture(t, false)
	scheduled := steeringReviewClaimSchedule(t, s, c, old.BotID)
	if ok, err := s.store.SetRunStatus(scheduled.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	_, runs, queued, _, err := s.store.addUserRunsSteering(c.ID, "Quick question while you work.", "during-schedule", []runSpec{{BotID: old.BotID, Model: old.Model}}, nil)
	// The run waits for the Bot's lane, so the message shows as queued for that run.
	if err != nil || len(runs) != 1 || len(queued) != 1 || queued[0].RunID != runs[0].ID {
		t.Fatalf("runs=%+v queued=%+v err=%v", runs, queued, err)
	}
	if steered, err := s.store.HasSteeringSuccessor(scheduled.ID); err != nil || !steered {
		t.Fatalf("the scheduled run does not yield: %v %v", steered, err)
	}
}

// The HTTP side of "Stop current step": 409 with a stable code when no tool call
// is running or the run is not running, 200 once a call is in flight.
func TestInterruptStepEndpoint(t *testing.T) {
	s, _, old := steerFixture(t, false)
	post := func() (int, string) {
		w := httptest.NewRecorder()
		s.run(w, httptest.NewRequest(http.MethodPost, "/api/runs/"+old.ID+"/interrupt-step", nil), old.ID)
		return w.Code, w.Body.String()
	}
	if code, body := post(); code != 409 || !strings.Contains(body, "run_not_running") {
		t.Fatalf("queued run: %d %s", code, body)
	}
	if ok, err := s.store.SetRunStatus(old.ID, "running", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if code, body := post(); code != 409 || !strings.Contains(body, "no_step_in_flight") {
		t.Fatalf("no tool call: %d %s", code, body)
	}
	called := false
	end := s.beginStep(old.ID)(func() { called = true })
	if code, body := post(); code != 200 || !called {
		t.Fatalf("tool call in flight: %d %s called=%v", code, body, called)
	}
	end()
}
