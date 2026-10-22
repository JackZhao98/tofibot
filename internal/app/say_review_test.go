package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// afterSayEngine runs inner with the say tool wrapped: after a say call
// succeeds, after() runs (a message arriving right after the answer).
type afterSayEngine struct {
	inner runtime.Engine
	after func()
}

func (e afterSayEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	tools := append([]runtime.Tool(nil), req.Tools...)
	for i := range tools {
		if tools[i].Name != "say" {
			continue
		}
		orig := tools[i].Execute
		tools[i].Execute = func(c context.Context, raw json.RawMessage) (string, error) {
			out, err := orig(c, raw)
			if err == nil {
				e.after()
			}
			return out, err
		}
	}
	req.Tools = tools
	return e.inner.Run(ctx, req)
}

// Finding 1: a message that arrives right after say(answer) must not cancel an
// answer the person already watched stream. The answer is published, the run is
// done, and the newer message's run goes on after it.
func TestSteeringMessageAfterSayAnswerKeepsTheAnswer(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"answer","text":"The finished answer."}`}}},
		fakeStep{content: "the successor's reply"})
	var successor Run
	s.engine = afterSayEngine{inner: model.engine(t), after: func() { successor = steeringReviewInterject(t, s, c, old) }}
	s.enqueue(c, old)
	got := waitRunTerminal(t, s, old.ID)
	if got.Status != "done" {
		t.Fatalf("run = %+v, want done", got)
	}
	rows := assistantRows(t, s.store, c.ID)
	found := false
	for _, m := range rows {
		if m.RunID == old.ID && m.Kind == "" && m.Content == "The finished answer." {
			found = true
		}
	}
	if !found {
		t.Fatalf("the answer was not published: %q", kindsAndText(rows))
	}
	if after := waitRunTerminal(t, s, successor.ID); after.Status != "done" {
		t.Fatalf("successor = %+v", after)
	}
	if originOf(t, s.store, old.ID) != finalOriginSay {
		t.Fatalf("origin = %q", originOf(t, s.store, old.ID))
	}
}

// A message that arrived before the answer is recorded wins: the record refuses.
func TestSayAnswerIsRefusedOnceAnInterjectionIsPending(t *testing.T) {
	s, c, old := steeringReviewFixture(t, false)
	if _, ok, err := s.store.claimRun(context.Background(), old.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	steeringReviewInterject(t, s, c, old)
	if err := s.store.recordSayAnswer(old.ID, "call-1", "too late"); err == nil || !strings.Contains(err.Error(), "newer message") {
		t.Fatalf("record error = %v", err)
	}
	if _, ok := s.store.sayAnswer(old.ID); ok {
		t.Fatal("an answer was recorded for a run with a pending successor")
	}
}

// The same window in a group: say(answer) took over respond(reply)'s path, so
// a message that lands right after it must keep the reply and finish the run.
func TestSteeringMessageAfterRespondReplyKeepsTheReply(t *testing.T) {
	var f dedupeFixture
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"answer","text":"Spaces, and a formatter."}`}}},
		fakeStep{content: "a model call that must not happen"})
	byBot := engineByBot{}
	f = newDedupeFixture(t, byBot)
	var once bool
	byBot[f.tofi.ID] = afterSayEngine{inner: model.engine(t), after: func() {
		if once {
			return
		}
		once = true
		var first Run
		runs, _ := f.server.store.Runs(f.group.ID)
		for _, r := range runs {
			if r.BotID == f.tofi.ID {
				first = r
				break
			}
		}
		steeringReviewInterject(t, f.server, f.group, first)
	}}
	f.post(t, "@tofi tabs or spaces", f.tofi)
	f.start(byBot)
	f.settle(t)
	var content, status, origin string
	_ = f.server.store.db.QueryRow(`SELECT COALESCE(MAX(m.content),'') FROM messages m JOIN runs r ON r.id=m.run_id WHERE r.conversation_id=? AND r.bot_id=? AND m.role='assistant' AND m.kind='' AND m.content='Spaces, and a formatter.'`, f.group.ID, f.tofi.ID).Scan(&content)
	if content == "" {
		t.Fatal("the reply was discarded by the steering message")
	}
	_ = f.server.store.db.QueryRow(`SELECT status,final_origin FROM runs WHERE conversation_id=? AND bot_id=? ORDER BY created_at LIMIT 1`, f.group.ID, f.tofi.ID).Scan(&status, &origin)
	if status != "done" || origin != finalOriginSay {
		t.Fatalf("first run status = %q origin = %q", status, origin)
	}
}

type respondHookEngine struct {
	inner runtime.Engine
	after func(runtime.Request)
}

func (e respondHookEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	tools := append([]runtime.Tool(nil), req.Tools...)
	for i := range tools {
		if tools[i].Name != "respond" {
			continue
		}
		orig := tools[i].Execute
		tools[i].Execute = func(c context.Context, raw json.RawMessage) (string, error) {
			out, err := orig(c, raw)
			if err == nil {
				e.after(req)
			}
			return out, err
		}
	}
	req.Tools = tools
	return e.inner.Run(ctx, req)
}

// Finding 3: text a Bot writes after handing off, with no say, is working text.
func TestTrailingTextAfterAHandoffStaysInternal(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		run, err := f.server.store.GetRun(req.RunID)
		if err != nil {
			return "ERR " + err.Error()
		}
		switch {
		case run.Kind == runKindFollowup:
			return "Integrated the helper result."
		case run.Kind == runKindGroupTask:
			return "helper result"
		}
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				if _, err := tool.Execute(ctx, handoffArgs(f.meta.ID, "write the metaphor")); err != nil {
					return "ERR " + err.Error()
				}
			}
		}
		return "Passed it to the metaphor Bot; I will weigh in when it answers."
	}
	f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	var lead Run
	for _, r := range runs {
		if r.BotID == f.tofi.ID && r.Kind == "" {
			lead = r
		}
	}
	if lead.Status != "done" || originOf(t, f.server.store, lead.ID) != finalOriginNone {
		t.Fatalf("lead = %+v origin=%q", lead, originOf(t, f.server.store, lead.ID))
	}
	var kind string
	if err := f.server.store.db.QueryRow(`SELECT kind FROM messages WHERE run_id=? AND content LIKE 'Passed it%'`, lead.ID).Scan(&kind); err != nil || kind != "internal" {
		t.Fatalf("trailing text kind = %q err=%v", kind, err)
	}
	// The resumed lead's report is a different run and still publishes.
	var reported int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE content='Integrated the helper result.' AND kind=''`).Scan(&reported)
	if reported != 1 {
		t.Fatalf("the resumed lead's report was published %d times", reported)
	}
}

// Finding 4: with the flag on, no chat draft ever holds plain text that ends up
// internal, and a text-only final appears at once when the run finishes.
func TestPlainTextNeverEntersTheChatDraftWhenStructured(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("Looking into it now.")
		time.Sleep(70 * time.Millisecond)
		if err := req.OnAssistantTurn(1, "Looking into it now."); err != nil {
			return runtime.Result{}, err
		}
		req.OnDelta("Here is the ")
		time.Sleep(70 * time.Millisecond)
		req.OnDelta("final text.")
		return runtime.Result{Content: "Here is the final text."}, nil
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if deltas := deltaTexts(t, s.store, run.ConversationID); len(deltas) != 0 {
		t.Fatalf("plain text entered the chat draft: %q", deltas)
	}
	want := []string{"internal|Looking into it now.", "|Here is the final text."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginImplicit {
		t.Fatalf("origin = %q", got)
	}
}

// The plain text goes to the live status line instead.
func TestPlainTextFeedsTheLiveStatusWhenStructured(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("Checking the clock service.")
		time.Sleep(300 * time.Millisecond)
		return runtime.Result{Content: "Half past four."}, nil
	}))
	var seen bool
	for _, ev := range eventsOfType(t, s.store, run.ConversationID, "thinking") {
		if strings.Contains(strings.TrimSpace(ev["text"].(string)), "Checking the clock") {
			seen = true
		}
	}
	if !seen {
		t.Fatal("plain text did not reach the live status channel")
	}
}

// Finding 5: say(answer) gets the completion review text finals get, once, and
// only when tool work was done.
func TestSayAnswerAfterToolWorkGetsExactlyOneCompletionReview(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "create_work_item", `{"kind":"task","title":"Check the numbers"}`}}},
		fakeStep{calls: []fakeCall{{"c2", "say", `{"purpose":"answer","text":"First draft of the answer."}`}}},
		fakeStep{calls: []fakeCall{{"c3", "say", `{"purpose":"answer","text":"Reviewed answer."}`}}},
		fakeStep{content: "a call that must not happen"})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	reminders := 0
	for _, body := range model.requests() {
		if n := strings.Count(body, "compare the user's requested deliverable"); n > reminders {
			reminders = n
		}
	}
	if reminders != 1 {
		t.Fatalf("the review reminder appeared %d times in the longest transcript, want 1", reminders)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Reviewed answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
}

func TestSayAnswerWithoutToolWorkGetsNoReview(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"answer","text":"A plain reply."}`}}})
	s, run := serverWithModel(t, model, true)
	for _, body := range model.requests() {
		if strings.Contains(body, "compare the user's requested deliverable") {
			t.Fatal("a reply with no tool work was sent back for review")
		}
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|A plain reply."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// An answer sent back for review that is never replaced is still published.
func TestReviewedSayAnswerThatIsNeverRepeatedStillReachesThePerson(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "create_work_item", `{"kind":"task","title":"Check the numbers"}`}}},
		fakeStep{calls: []fakeCall{{"c2", "say", `{"purpose":"answer","text":"The only draft."}`}}},
		fakeStep{content: "The only draft."})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	rows := assistantRows(t, s.store, run.ConversationID)
	visible := 0
	for _, m := range rows {
		if m.Kind == "" && m.Content == "The only draft." {
			visible++
		}
	}
	if visible != 1 {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// Finding 6: the long-term summary still gets the real engine after a resume
// that finished from a recorded answer (it was handed the stand-in engine).
func TestResumedAnswerStillRunsTheLongTermSummary(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := st.CreateBot("summarizer", "", "synthetic")
	conv, _ := st.GetConversation(bot.DMConversationID)
	for i := 0; i < 80; i++ {
		role, sender := "user", ""
		if i%2 == 1 {
			role, sender = "assistant", bot.ID
		}
		if _, _, err := st.AddMessage(conv.ID, role, sender, "", fmt.Sprintf("earlier synthetic message %d with some words", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	_, run, _, err := st.AddUserRun(conv.ID, bot.ID, "the current request", "resume-summary")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.claimRun(context.Background(), run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := st.recordSayAnswer(run.ID, "call-1", "The recorded answer."); err != nil {
		t.Fatal(err)
	}
	st.Close()
	engine := &summaryProbeEngine{}
	srv, err := NewServer(Config{DataDir: dir, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	waitForStatus(t, srv, run.ID, "done")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && engine.memoryCalls() == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if engine.memoryCalls() == 0 {
		t.Fatal("the long-term summary never reached the real engine")
	}
}

type summaryProbeEngine struct{ memory atomic.Int32 }

func (e *summaryProbeEngine) memoryCalls() int { return int(e.memory.Load()) }

func (e *summaryProbeEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if strings.HasSuffix(req.RunID, ":memory") {
		e.memory.Add(1)
	}
	return runtime.Result{Content: "ok"}, nil
}

type scheduledSayEngine struct{}

func (scheduledSayEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	for _, tool := range req.Tools {
		if tool.Name == "complete_scheduled_task" {
			return runtime.Result{}, errors.New("complete_scheduled_task is offered under the say protocol")
		}
	}
	_, err := callTool(ctx, req, "say", "answer-1", map[string]any{"purpose": "answer", "text": "Short answer that does not repeat the receipt.", "sources": []string{"a"}})
	return runtime.Result{}, err
}

// Finding 7: the schedule receipt is not prepended to a say answer, and the
// origin stays say.
func TestScheduleReceiptIsNotPrependedToASayAnswer(t *testing.T) {
	f := newDedupeFixture(t, &scheduledSayEngine{})
	x, err := f.server.store.CreateSchedule(f.group.ID, f.tofi.ID, ScheduleSpec{Content: "post the daily synthetic note", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, f.server.store, x.ID, time.Now().Add(-time.Minute))
	claimed, err := f.server.store.ClaimDueSchedules(time.Now())
	if err != nil || len(claimed) != 1 {
		runs, _ := f.server.store.Runs(f.group.ID)
		if len(runs) != 1 {
			t.Fatalf("claim=%+v err=%v runs=%+v", claimed, err, runs)
		}
		claimed = runs
	}
	f.server.enqueue(f.group, claimed[0])
	got := waitRunTerminal(t, f.server, claimed[0].ID)
	if got.Status != "done" {
		t.Fatalf("run=%+v", got)
	}
	rows := assistantRows(t, f.server.store, f.group.ID)
	var visible []string
	for _, m := range rows {
		if m.RunID == got.ID && (m.Kind == "" || m.Kind == "segment") {
			visible = append(visible, m.Content)
		}
	}
	if !equalStrings(visible, []string{"Short answer that does not repeat the receipt."}) {
		t.Fatalf("visible = %q", visible)
	}
	if origin := originOf(t, f.server.store, got.ID); origin != finalOriginSay {
		t.Fatalf("origin = %q", origin)
	}
}

// The crash test above records the answer by hand. Here the real say tool runs
// inside the real engine, so record-then-EndTurn is exercised: the loop stops
// after one model call with the answer recorded, and a restart publishes that
// answer once without calling the model.
func TestResumeAfterTheRealSayToolRecordedTheAnswer(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := srv.store.CreateBot("real tool", "", "synthetic")
	conv, _ := srv.store.GetConversation(bot.DMConversationID)
	_, run, _, err := srv.store.AddUserRun(conv.ID, bot.ID, "answer me", "real-say")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := srv.store.claimRun(context.Background(), run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live := run
	live.structured = true
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"call-real", "say", `{"purpose":"answer","text":"The answer from the real tool."}`}}},
		fakeStep{content: "a second model call that must not happen"})
	res, err := model.engine(t).Run(context.Background(), runtime.Request{
		BotID: bot.ID, RunID: run.ID, ConversationID: conv.ID, Attempt: 1,
		Messages: []runtime.Message{{Role: "user", Content: "answer me"}},
		Tools:    []runtime.Tool{srv.sayTool(conv, live)},
	})
	if err != nil || !res.TurnEnded {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	if n := modelCalls(model); n != 1 {
		t.Fatalf("model calls = %d, want 1", n)
	}
	if rec, ok := srv.store.sayAnswer(run.ID); !ok || rec.Text != "The answer from the real tool." || rec.CallID != "call-real" {
		t.Fatalf("record = %+v %v", rec, ok)
	}
	srv.Close() // the process dies before the run publishes anything

	engine := &countingEngine{runID: run.ID}
	again, err := NewServer(Config{DataDir: dir, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	waitForStatus(t, again, run.ID, "done")
	if engine.calls.Load() != 0 {
		t.Fatalf("the resumed run called the model %d time(s)", engine.calls.Load())
	}
	var visible []string
	for _, m := range assistantRows(t, again.store, conv.ID) {
		if m.Kind == "" || m.Kind == "segment" {
			visible = append(visible, m.Content)
		}
	}
	if !equalStrings(visible, []string{"The answer from the real tool."}) {
		t.Fatalf("visible = %q", visible)
	}
}
