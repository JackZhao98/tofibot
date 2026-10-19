package app

// Phase 3 of docs/agent-plan/structured-output.md: respond's reply, the
// mid-run chat message and the scheduled receipt fold into say.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// ---- Group chat: say takes over respond's reply path ----

func TestRespondOffersOnlyTheDispositionsThatPostNothingUnderTheFlag(t *testing.T) {
	for _, structured := range []bool{true, false} {
		var f dedupeFixture
		engine := &mentionEngine{}
		f = newDedupeFixture(t, engine)
		if err := f.server.store.putUserStructuredOutput(structured); err != nil {
			t.Fatal(err)
		}
		var enum []string
		var props map[string]any
		engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
			tool, ok := toolNamed(req.Tools, "respond")
			if !ok {
				return "ERR no respond tool"
			}
			props = tool.Parameters["properties"].(map[string]any)
			enum, _ = props["disposition"].(map[string]any)["enum"].([]string)
			return reply(ctx, req, "done", nil)
		}
		f.post(t, "@tofi go", f.tofi)
		f.start(engine)
		f.settle(t)
		hasReply := false
		for _, d := range enum {
			hasReply = hasReply || d == dispReply
		}
		if structured && (hasReply || props["wake"] != nil || props["text"] != nil || len(enum) != 3) {
			t.Fatalf("flag-on respond = %v %v", enum, props)
		}
		if !structured && (!hasReply || props["wake"] == nil || props["text"] == nil) {
			t.Fatalf("flag-off respond = %v %v", enum, props)
		}
	}
}

func TestRespondRefusesReplyUnderTheFlag(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	var refused error
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		refused = respond(ctx, req, map[string]any{"disposition": "reply", "text": "posted the old way"})
		return reply(ctx, req, "posted through say", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	f.settle(t)
	if refused == nil || !strings.Contains(refused.Error(), "deliver with say") {
		t.Fatalf("respond(reply) under the flag: %v", refused)
	}
	var n int
	_ = f.server.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role='assistant' AND kind='' AND content='posted the old way'`).Scan(&n)
	if n != 0 {
		t.Fatal("the refused reply was published")
	}
}

func TestGroupSayAnswerWakesMembersAndKeepsTheListInTheRecord(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	var inMemory bool
	var recorded []groupWakeEntry
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.meta.ID {
			return reply(ctx, req, "the metaphor: a lighthouse", nil)
		}
		out := reply(ctx, req, "Asked the metaphor Bot.", wakeOf(f.meta.ID, "write the metaphor"))
		_, inMemory = f.server.dispositions.Load(req.RunID)
		if rec, ok := f.server.store.sayAnswer(req.RunID); ok {
			recorded = rec.Wake
		}
		return out
	}
	f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 2 {
		t.Fatalf("runs = %+v", runs)
	}
	if publishedBy(&f, f.tofi.ID) != 1 || publishedBy(&f, f.meta.ID) != 1 {
		t.Fatalf("published: tofi=%d meta=%d", publishedBy(&f, f.tofi.ID), publishedBy(&f, f.meta.ID))
	}
	// The wake list is durable state, not an in-memory choice a restart loses.
	if inMemory || len(recorded) != 1 || recorded[0].BotID != f.meta.ID || recorded[0].Request != "write the metaphor" {
		t.Fatalf("inMemory=%v recorded=%+v", inMemory, recorded)
	}
}

func TestSayWakeFollowsRespondsRules(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	var errs []string
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		for i, wake := range []any{
			wakeOf(f.tofi.ID, "to myself"),
			wakeOf(f.meta.ID, "  "),
			wakeOf("not-a-member", "do it"),
			[]map[string]string{{"bot_id": f.meta.ID, "request": "a"}, {"bot_id": f.third.ID, "request": "b"}, {"bot_id": f.meta.ID, "request": "c"}, {"bot_id": f.third.ID, "request": "d"}},
		} {
			_, err := callTool(ctx, req, "say", "bad-wake-"+string(rune('a'+i)), map[string]any{"purpose": "answer", "text": "x", "wake": wake})
			if err == nil {
				return "accepted a bad wake list"
			}
			errs = append(errs, err.Error())
		}
		if _, ok := f.server.store.sayAnswer(req.RunID); ok {
			return "a refused wake still recorded an answer"
		}
		return reply(ctx, req, "fine", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(errs) != 4 || len(runs) != 1 || runs[0].Status != "done" || publishedBy(&f, f.tofi.ID) != 1 {
		t.Fatalf("errs=%q runs=%+v", errs, runs)
	}
}

func TestSayRefusesWakeAndSourcesWhereTheyDoNotApply(t *testing.T) {
	var gotWake, gotSources error
	_, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		_, gotWake = callTool(ctx, req, "say", "w1", map[string]any{"purpose": "answer", "text": "x", "wake": wakeOf("b", "r")})
		_, gotSources = callTool(ctx, req, "say", "w2", map[string]any{"purpose": "answer", "text": "x", "sources": []string{"s"}})
		_, err := say(ctx, req, "w3", "answer", "fine")
		return runtime.Result{}, err
	}))
	if run.Status != "done" || gotWake == nil || gotSources == nil {
		t.Fatalf("run=%+v wake=%v sources=%v", run, gotWake, gotSources)
	}
}

// ---- Group chat: a leading mention is only a mention (H6) ----

func TestLeadingMentionIsOnlyAMentionWhenStructured(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.tofi.ID {
			return reply(ctx, req, "@metaphor draft one analogy for synthetic caching", nil)
		}
		return reply(ctx, req, "must not run", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 1 || originOf(t, f.server.store, runs[0].ID) != finalOriginSay {
		t.Fatalf("a leading mention dispatched work: runs=%+v origin=%q", runs, originOf(t, f.server.store, runs[0].ID))
	}
	if publishedBy(&f, f.tofi.ID) != 1 {
		t.Fatalf("the Bot's words were not published as said")
	}
}

func TestLeadingMentionOfATextFinalIsOnlyAMentionWhenStructured(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.tofi.ID {
			return "@metaphor draft one analogy for synthetic caching"
		}
		return "must not run"
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 1 || originOf(t, f.server.store, runs[0].ID) != finalOriginImplicit {
		t.Fatalf("runs=%+v origin=%q", runs, originOf(t, f.server.store, runs[0].ID))
	}
}

// ---- send_chat_message: say(message) ----

func TestSendChatMessageIsReplacedBySayMessageUnderTheFlag(t *testing.T) {
	for _, structured := range []bool{true, false} {
		var has, hasSay bool
		s, _ := dmRunMode(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
			_, has = toolNamed(req.Tools, "send_chat_message")
			_, hasSay = toolNamed(req.Tools, "say")
			return runtime.Result{Content: "ok"}, nil
		}), structured)
		_ = s
		if has == structured || hasSay != structured {
			t.Fatalf("flag=%v: send_chat_message=%v say=%v", structured, has, hasSay)
		}
	}
}

// The beats keep send_chat_message's cap and its target: the run's own
// conversation, three at most.
func TestSayMessageKeepsTheChatMessageCapAndTarget(t *testing.T) {
	var errs []error
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		for i := 0; i < 4; i++ {
			_, err := say(ctx, req, "m"+string(rune('1'+i)), "message", "beat "+string(rune('1'+i)))
			errs = append(errs, err)
		}
		_, err := say(ctx, req, "a1", "answer", "wrap-up")
		return runtime.Result{}, err
	}))
	if run.Status != "done" || len(errs) != 4 || errs[0] != nil || errs[1] != nil || errs[2] != nil || errs[3] == nil {
		t.Fatalf("run=%+v errs=%v", run, errs)
	}
	rows := assistantRows(t, s.store, run.ConversationID)
	if len(rows) != 4 {
		t.Fatalf("rows = %q", kindsAndText(rows))
	}
	for _, m := range rows {
		if m.ConversationID != run.ConversationID {
			t.Fatalf("message landed in %s", m.ConversationID)
		}
	}
}

// ---- Scheduled runs: say(answer) is the completion record ----

func TestScheduledRunIsNotOfferedCompleteScheduledTaskUnderTheFlag(t *testing.T) {
	for _, structured := range []bool{true, false} {
		f := newDedupeFixture(t, scheduleOfferProbe{})
		if err := f.server.store.putUserStructuredOutput(structured); err != nil {
			t.Fatal(err)
		}
		offered.Store("")
		got := runScheduledOnce(t, f)
		_ = got
		v, _ := offered.Load().(string)
		if structured && strings.Contains(v, "complete_scheduled_task") || !structured && !strings.Contains(v, "complete_scheduled_task") {
			t.Fatalf("flag=%v offered tools: %s", structured, v)
		}
		if structured && !strings.Contains(v, "say") {
			t.Fatalf("say not offered: %s", v)
		}
	}
}

func runScheduledOnce(t *testing.T, f dedupeFixture) Run {
	t.Helper()
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
	return waitRunTerminal(t, f.server, claimed[0].ID)
}

type scheduleOfferProbe struct{}

var offered atomic.Value

func (scheduleOfferProbe) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	offered.Store(strings.Join(names, ","))
	return (scheduleTestEngine{}).Run(ctx, req)
}

// A scheduled run that cannot produce its result ends with plain text; with no
// say(answer) there is no completion record and the run fails with that text.
func TestScheduledBlockerAsPlainTextFailsTheRunUnderTheFlag(t *testing.T) {
	f := newDedupeFixture(t, &mentionEngine{reply: func(ctx context.Context, req runtime.Request, text string) string {
		return "The source stayed unreachable, so there is no result to report."
	}})
	got := runScheduledOnce(t, f)
	if got.Status != "failed" || !strings.Contains(got.Error, "UNCONFIRMED assistant output") || !strings.Contains(got.Error, "stayed unreachable") {
		t.Fatalf("run = %+v", got)
	}
}

// A say(answer) with sources completes the run and the occurrence counts as
// delivered, exactly as the receipt did.
func TestScheduledSayAnswerCountsAsDeliveredInTheOccurrence(t *testing.T) {
	f := newDedupeFixture(t, &scheduledSayEngine{})
	got := runScheduledOnce(t, f)
	if got.Status != "done" {
		t.Fatalf("run = %+v", got)
	}
	var delivered int
	err := f.server.store.db.QueryRow(`SELECT CASE WHEN EXISTS (SELECT 1 FROM run_say_answers WHERE run_id=?) AND EXISTS (SELECT 1 FROM messages WHERE run_id=? AND kind='') THEN 1 ELSE 0 END`, got.ID, got.ID).Scan(&delivered)
	if err != nil || delivered != 1 {
		t.Fatalf("delivered=%d err=%v", delivered, err)
	}
	occurrences, err := f.server.store.ScheduleOccurrences(context.Background(), f.group.ID, []string{got.ID})
	if err != nil || len(occurrences) != 1 || !occurrences[0].ResultInConversation || occurrences[0].ExecutionStatus != "done" {
		t.Fatalf("occurrences=%+v err=%v", occurrences, err)
	}
}

// A text-only final with no completion record gets the single review, in the
// say protocol's words, and the run completes through say(answer).
func TestScheduledFinalReviewAsksForSayAnswerUnderTheFlag(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{content: "All checks done."},
		fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"answer","text":"The synthetic report.","sources":["synthetic source"]}`}}})
	f := newDedupeFixture(t, model.engine(t))
	got := runScheduledOnce(t, f)
	if got.Status != "done" {
		t.Fatalf("run = %+v", got)
	}
	reqs := model.requests()
	if len(reqs) < 2 || !strings.Contains(reqs[1], "delivered with say") || strings.Contains(reqs[1], "call complete_scheduled_task") {
		t.Fatalf("the review reminder was not the say version: %d requests", len(reqs))
	}
	if origin := originOf(t, f.server.store, got.ID); origin != finalOriginSay {
		t.Fatalf("origin = %q", origin)
	}
}

// Heuristic H5 (the receipt prepend) is gone from the flag-on path: even a run
// that holds a legacy receipt row (resumed across the protocol change) has its
// text final published exactly as written.
type legacyReceiptEngine struct{}

func (legacyReceiptEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	const args = `{"content":"The recorded result with the synthetic figures and the three sources behind them."}`
	for _, status := range []string{"queued", "running", "completed"} {
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: "legacy-receipt", Name: "complete_scheduled_task", Arguments: args, Status: status, Result: "accepted"}); err != nil {
			return runtime.Result{}, err
		}
	}
	return runtime.Result{Content: "As above."}, nil
}

func TestScheduledTextFinalIsNeverRewrittenUnderTheFlag(t *testing.T) {
	for _, structured := range []bool{true, false} {
		f := newDedupeFixture(t, legacyReceiptEngine{})
		if err := f.server.store.putUserStructuredOutput(structured); err != nil {
			t.Fatal(err)
		}
		got := runScheduledOnce(t, f)
		var content string
		_ = f.server.store.db.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND kind=''`, got.ID).Scan(&content)
		origin := originOf(t, f.server.store, got.ID)
		if got.Status != "done" {
			t.Fatalf("flag=%v run = %+v", structured, got)
		}
		if structured && (content != "As above." || origin != finalOriginImplicit) {
			t.Fatalf("flag-on text final = %q origin=%q", content, origin)
		}
		if !structured && (!strings.Contains(content, "The recorded result") || origin != heuristicSchedulePrep) {
			t.Fatalf("flag-off keeps H5: %q origin=%q", content, origin)
		}
	}
}

// ---- Budget exhaustion: no heuristic folds a structured run's partial text ----

func TestBudgetPartialTextIsNotFoldedWhenStructured(t *testing.T) {
	const beat = "Partial findings so far: the first two sources agree on the figures."
	const partial = "Partial findings so far: the first two sources agree on the figures, and I am still checking the rest."
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "b1", "message", beat); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{Content: partial, BudgetExhausted: true, BudgetReason: "synthetic budget"}, nil
	}))
	var n int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant' AND kind='' AND content=?`, run.ID, partial).Scan(&n)
	origin := originOf(t, s.store, run.ID)
	if n != 1 || strings.Contains(origin, "heuristic:") {
		t.Fatalf("budget partial text published %d times, origin=%q: a heuristic folded it", n, origin)
	}
}

// The store-level entry keeps the plain-text heuristics for the flag-off path.
func TestBudgetPartialTextKeepsItsHeuristicsWhenNotStructured(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "Partial findings so far: the first two sources agree on the figures."); err != nil || !ok {
		t.Fatalf("publish ok=%v err=%v", ok, err)
	}
	if _, done, err := s.finishRunBudget(r.ID, c.ID, r.BotID, "Partial findings so far: the first two sources agree on the figures, and I am still checking the rest.", "budget"); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if origin := originOf(t, s, r.ID); !strings.Contains(origin, "heuristic:") {
		t.Fatalf("flag-off budget text origin=%q", origin)
	}
}
