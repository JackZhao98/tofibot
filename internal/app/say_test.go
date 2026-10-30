package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// callTool runs a request tool the way the loop does, under a provider call ID.
func callTool(ctx context.Context, req runtime.Request, name, callID string, args map[string]any) (string, error) {
	raw, _ := json.Marshal(args)
	for _, tool := range req.Tools {
		if tool.Name == name {
			return tool.Execute(runtime.WithToolCallID(ctx, callID), raw)
		}
	}
	return "", fmt.Errorf("no %s tool", name)
}

func say(ctx context.Context, req runtime.Request, callID, purpose, text string) (string, error) {
	return callTool(ctx, req, "say", callID, map[string]any{"purpose": purpose, "outcome": "done", "text": text})
}

func assistantRows(t *testing.T, s *Store, convID string) []Message {
	t.Helper()
	msgs, _, err := s.Messages(convID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []Message
	for _, m := range msgs {
		if m.Role == "assistant" && m.Kind != "notice" {
			out = append(out, m)
		}
	}
	return out
}

func kindsAndText(rows []Message) []string {
	var out []string
	for _, m := range rows {
		out = append(out, m.Kind+"|"+m.Content)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSayAnswerPublishesOnceEndsTheTurnAndTagsTheOrigin(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "call-1", "answer", "It is half past four."); err != nil {
			return runtime.Result{}, err
		}
		// What a model writes after its answer is never published.
		return runtime.Result{Content: "Anything else I can help with?"}, nil
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	rows := assistantRows(t, s.store, run.ConversationID)
	if want := []string{"|It is half past four."}; !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
}

func TestSaySecondAnswerIsRefused(t *testing.T) {
	var second, otherSecond error
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "call-1", "answer", "first answer"); err != nil {
			return runtime.Result{}, err
		}
		_, second = say(ctx, req, "call-2", "answer", "second answer")
		_, otherSecond = say(ctx, req, "call-3", "answer", "first answer")
		return runtime.Result{}, nil
	}))
	if second == nil || !strings.Contains(second.Error(), "already delivered") || otherSecond == nil {
		t.Fatalf("second answers: %v / %v", second, otherSecond)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|first answer"}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// A progress update is a real message in the chat, not a folded line.
func TestSayNoteIsAVisibleMessage(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "n1", "note", "Reading the report now."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "The report says revenue grew.")
		return runtime.Result{}, err
	}))
	want := []string{"segment|Reading the report now.", "|The report says revenue grew."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
}

func TestSayMessageIsASegmentCappedAtThree(t *testing.T) {
	var fourth error
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		for i := 1; i <= 3; i++ {
			if _, err := say(ctx, req, fmt.Sprintf("m%d", i), "message", fmt.Sprintf("beat %d", i)); err != nil {
				return runtime.Result{}, err
			}
		}
		_, fourth = say(ctx, req, "m4", "message", "beat 4")
		_, err := say(ctx, req, "a1", "answer", "the conclusion")
		return runtime.Result{}, err
	}))
	if fourth == nil || !strings.Contains(fourth.Error(), "limit") {
		t.Fatalf("fourth message error = %v", fourth)
	}
	want := []string{"segment|beat 1", "segment|beat 2", "segment|beat 3", "|the conclusion"}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
}

// The provider call ID makes a replayed call return the first publication.
func TestSayPublicationIsIdempotentPerProviderCall(t *testing.T) {
	var replay, conflict, answerReplay error
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "n1", "note", "step one done"); err != nil {
			return runtime.Result{}, err
		}
		_, replay = say(ctx, req, "n1", "note", "step one done")
		_, conflict = say(ctx, req, "n1", "note", "different words")
		if _, err := say(ctx, req, "m1", "message", "hello there"); err != nil {
			return runtime.Result{}, err
		}
		if _, err := say(ctx, req, "m1", "message", "hello there"); err != nil {
			return runtime.Result{}, err
		}
		if _, err := say(ctx, req, "a1", "answer", "final words"); err != nil {
			return runtime.Result{}, err
		}
		_, answerReplay = say(ctx, req, "a1", "answer", "final words")
		return runtime.Result{}, nil
	}))
	if replay != nil || answerReplay != nil || conflict == nil {
		t.Fatalf("replay=%v answerReplay=%v conflict=%v", replay, answerReplay, conflict)
	}
	want := []string{"segment|step one done", "segment|hello there", "|final words"}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
}

func TestSayRejectsBadArguments(t *testing.T) {
	var errs []error
	dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		_, e1 := say(ctx, req, "x1", "nothing", "text")
		_, e2 := say(ctx, req, "x2", "answer", "   ")
		_, e3 := say(ctx, req, "x3", "note", strings.Repeat("n", maxSayNoteRunes+1))
		_, e4 := callTool(ctx, req, "say", "x4", map[string]any{"purpose": "answer", "outcome": "done"})
		errs = []error{e1, e2, e3, e4}
		_, err := say(ctx, req, "a1", "answer", "fine")
		return runtime.Result{}, err
	}))
	for i, want := range []string{"purpose must be", "nothing visible", "at most 1200", "nothing visible"} {
		if len(errs) != 4 || errs[i] == nil || !strings.Contains(errs[i].Error(), want) {
			t.Fatalf("bad call %d: error = %v, want %q", i+1, errs, want)
		}
	}
}

// Purpose gates follow the run: a scheduled run answers only after its
// completion receipt and has no mid-run message; a group turn that respond
// already ended cannot answer.
func TestSayPurposeGatesByRunKind(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	// The run is done; reuse its identity with a running copy for the gates.
	c, _ := s.store.GetConversation(run.ConversationID)
	bot, _ := s.store.GetBot(run.BotID)
	_, live, _, err := s.store.AddUserRun(c.ID, bot.ID, "scheduled style", "gates-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), live.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live.Kind = runKindSchedule
	live.structured = true
	tool := s.sayTool(c, live)
	ctx := runtime.WithToolCallID(context.Background(), "g1")
	if _, err := tool.Execute(ctx, json.RawMessage(`{"purpose":"message","outcome":"pending","text":"hi"}`)); err == nil {
		t.Fatal("a scheduled run was offered a mid-run message")
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"purpose":"note","outcome":"pending","text":"working"}`)); err != nil {
		t.Fatalf("a scheduled run may post notes: %v", err)
	}
	if _, err := tool.Execute(ctx, json.RawMessage(`{"purpose":"answer","outcome":"done","text":"done","wake":[{"bot_id":"x","request":"y"}]}`)); err == nil {
		t.Fatal("a scheduled DM answer accepted a wake list")
	}
	if done, err := s.store.scheduleReceipt(live.ID); err != nil || done {
		t.Fatalf("receipt before the answer = %v, %v", done, err)
	}
	// The answer with its sources is the completion record; no separate receipt.
	if _, err := tool.Execute(ctx, json.RawMessage(`{"purpose":"answer","outcome":"done","text":"done","sources":["synthetic source"," "]}`)); err != nil {
		t.Fatalf("scheduled answer: %v", err)
	}
	if done, err := s.store.scheduleReceipt(live.ID); err != nil || !done {
		t.Fatalf("receipt after the answer = %v, %v", done, err)
	}
	rec, ok := s.store.sayAnswer(live.ID)
	if !ok || len(rec.Sources) != 1 || rec.Sources[0] != "synthetic source" {
		t.Fatalf("recorded sources = %+v ok=%v", rec, ok)
	}
}

// Sources belong to scheduled answers, and the tool schema offers only the
// fields a run can use.
func TestSayOffersWakeInGroupsAndSourcesInScheduledRuns(t *testing.T) {
	_, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	s := &Server{}
	dm := Conversation{Kind: "dm", UserVisible: true}
	group := Conversation{Kind: "group", UserVisible: true}
	props := func(c Conversation, r Run) map[string]any {
		return s.sayTool(c, r).Parameters["properties"].(map[string]any)
	}
	if p := props(dm, run); p["wake"] != nil || p["sources"] != nil {
		t.Fatalf("a DM says wake or sources: %v", p)
	}
	if p := props(dm, run); p["covers_original"] == nil {
		t.Fatalf("a person's run cannot state that its answer covers the original request: %v", p)
	}
	if p := props(dm, func() Run { r := run; r.Kind = runKindSchedule; return r }()); p["covers_original"] != nil {
		t.Fatalf("a scheduled run offers covers_original: %v", p)
	}
	if p := props(group, run); p["wake"] == nil || p["sources"] != nil {
		t.Fatalf("group say: %v", p)
	}
	sched := run
	sched.Kind = runKindSchedule
	if p := props(dm, sched); p["wake"] != nil || p["sources"] == nil {
		t.Fatalf("scheduled say: %v", p)
	}
	if s.sayTool(group, run).Strict {
		t.Fatal("say with optional fields cannot be strict")
	}
}

func TestSayAfterRespondEndedTheTurnIsRefusedAndViceVersa(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	var afterRespond, respondAfterSay error
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.tofi.ID {
			_ = respond(ctx, req, map[string]any{"disposition": "follow_up", "wait_for": f.meta.ID})
			_, afterRespond = say(ctx, req, "s1", "answer", "late words")
			return ""
		}
		if _, err := say(ctx, req, "s2", "answer", "posted through say"); err != nil {
			return "ERR " + err.Error()
		}
		respondAfterSay = respond(ctx, req, map[string]any{"disposition": "reply", "text": "posted twice"})
		return ""
	}
	f.post(t, "@tofi and @metaphor discuss", f.tofi, f.meta)
	f.start(engine)
	f.settle(t)
	if afterRespond == nil || respondAfterSay == nil {
		t.Fatalf("afterRespond=%v respondAfterSay=%v", afterRespond, respondAfterSay)
	}
	if publishedBy(&f, f.tofi.ID) != 0 || publishedBy(&f, f.meta.ID) != 1 {
		t.Fatalf("published: tofi=%d meta=%d", publishedBy(&f, f.tofi.ID), publishedBy(&f, f.meta.ID))
	}
}

// ---- Plain text between tool calls ----

// scriptedModel runs a real engine against synthetic model steps; internal text
// then goes through the same turn callbacks production uses.
func serverWithModel(t *testing.T, model *fakeModel, structured bool) (*Server, Run) {
	t.Helper()
	return dmRunMode(t, model.engine(t), structured)
}

func modelCalls(m *fakeModel) int {
	n := 0
	for _, body := range m.requests() {
		if strings.Contains(body, `"tools"`) {
			n++
		}
	}
	return n
}

func TestPlainTextBetweenToolsStaysInternalWhenStructured(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{content: "Looking into it.", calls: []fakeCall{{"c1", "list_memory", `{}`}}},
		fakeStep{content: "Nothing saved yet."})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	want := []string{"internal|Looking into it.", "|Nothing saved yet."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
	// D3: the last text-only turn is the answer, tagged implicit, and the
	// miss costs no extra model call.
	if got := originOf(t, s.store, run.ID); got != finalOriginImplicit {
		t.Fatalf("origin = %q", got)
	}
	if n := modelCalls(model); n != 2 {
		t.Fatalf("model calls = %d, want 2 (no reminder round trip)", n)
	}
}

func TestPlainTextBetweenToolsIsPublishedWhenTheFlagIsOff(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{content: "Looking into it.", calls: []fakeCall{{"c1", "list_memory", `{}`}}},
		fakeStep{content: "Nothing saved yet."})
	s, run := serverWithModel(t, model, false)
	want := []string{"segment|Looking into it.", "|Nothing saved yet."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
}

// A model that does speak through say: its answer ends the run on that very
// model call, and the plain text before it never reaches the chat.
func TestSayAnswerEndsTheRealLoopWithoutAnotherModelCall(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{content: "Let me answer.", calls: []fakeCall{{"c1", "say", `{"purpose":"answer","outcome":"done","text":"Half past four."}`}, {"c2", "list_memory", `{}`}}},
		fakeStep{content: "an extra model call that must not happen"})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if n := modelCalls(model); n != 1 {
		t.Fatalf("model calls = %d, want 1", n)
	}
	// The plain text of the answering turn is an internal note; the answer is the say text.
	want := []string{"internal|Let me answer.", "|Half past four."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
	acts := activityByCall(t, s.store, run.ID)
	if a := acts["c1"]; a.Status != "completed" || a.Name != "say" {
		t.Fatalf("say activity = %+v", a)
	}
	if a, ok := acts["c2"]; ok && a.Status == "completed" {
		t.Fatalf("a tool after the answer ran: %+v", a)
	}
}

// The request carries purpose before text so the host knows where a draft
// goes before the text starts, and only say asks for strict schemas.
func TestSayToolSchemaListsPurposeFirst(t *testing.T) {
	model := newFakeModel(t, fakeStep{content: "hi"})
	serverWithModel(t, model, true)
	body := model.requests()[0]
	i := strings.Index(body, `"name":"say"`)
	if i < 0 {
		t.Fatalf("say is not offered: %.300s", body)
	}
	tail := body[i:]
	if p, x := strings.Index(tail, `"purpose"`), strings.Index(tail, `"text"`); p < 0 || x < 0 || p > x {
		t.Fatalf("purpose must precede text in the schema: %.400s", tail)
	}
	for _, banned := range []string{"for example", "e.g.", "such as"} {
		if strings.Contains(strings.ToLower(sayToolDescription), banned) {
			t.Fatalf("the tool description carries example wording %q", banned)
		}
	}
}

func TestSayIsNotOfferedWhenTheFlagIsOff(t *testing.T) {
	model := newFakeModel(t, fakeStep{content: "hi"})
	serverWithModel(t, model, false)
	if strings.Contains(model.requests()[0], `"name":"say"`) {
		t.Fatal("say was offered with structured output off")
	}
}

// ---- Rule 3 and the final answer guard ----

func TestRunWithOnlyASayNoteEndsByPromotingIt(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		_, err := say(ctx, req, "n1", "note", "Two mails matter: the invoice and the contract.")
		return runtime.Result{}, err
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	want := []string{"|Two mails matter: the invoice and the contract."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginPromoted {
		t.Fatalf("origin = %q", got)
	}
	// The promoted row reaches clients as a message event with kind ''.
	var promotedEvent bool
	for _, ev := range eventsOfType(t, s.store, run.ConversationID, "message") {
		if kind, _ := ev["kind"].(string); ev["content"] == "Two mails matter: the invoice and the contract." && kind == "" {
			promotedEvent = true
		}
	}
	if !promotedEvent {
		t.Fatal("no message event announced the promotion")
	}
}

// Internal text alone is not an answer, and no fixed sentence replaces it.
func TestRunWithNoTextAndNoSayFailsWithTheTypedCode(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{}, req.OnAssistantTurn(1, "Checking the clock service.")
	}))
	if run.Status != "failed" || run.failure() == nil || run.failure().Code != noFinalAnswerCode {
		t.Fatalf("run=%+v", run)
	}
	for _, m := range assistantRows(t, s.store, run.ConversationID) {
		if m.Kind != "internal" || strings.Contains(m.Content, "本次任务") {
			t.Fatalf("unexpected message %+v", m)
		}
	}
}

// H8 stays: an answer equal to text the person already saw is not posted twice.
func TestAnswerEqualToAnEarlierMessageIsDropped(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "m1", "message", "Here is the summary."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "Here is the summary.")
		return runtime.Result{}, err
	}))
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"segment|Here is the summary."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if got := originOf(t, s.store, run.ID); !strings.Contains(got, heuristicExactDup) {
		t.Fatalf("origin = %q", got)
	}
}

// A note is visible, so an answer that only repeats it adds nothing.
func TestAnswerEqualToAnEarlierNoteIsDropped(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "n1", "note", "Revenue grew four percent."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "Revenue grew four percent.")
		return runtime.Result{}, err
	}))
	want := []string{"segment|Revenue grew four percent."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q, want %q", kindsAndText(rows), want)
	}
}

// Structured runs keep what the Bot said: the narration, repeat and
// delegation-note folds are off.
func TestStructuredRunKeepsShortAnswersAfterAHandoff(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		run, err := f.server.store.GetRun(req.RunID)
		if err != nil {
			return "ERR " + err.Error()
		}
		if run.Kind == runKindGroupTask {
			return "helper result"
		}
		if run.Kind == runKindFollowup {
			// The return run ends the exchange. The old fixed cap of eight handoff
			// rounds used to end this loop by itself; the tree budget (decision 0064)
			// is sixty, so the fixture stops its own loop.
			return ""
		}
		for _, tool := range req.Tools {
			if tool.Name == "handoff" {
				if _, err := tool.Execute(ctx, handoffArgs(f.meta.ID, "write the metaphor")); err != nil {
					return "ERR " + err.Error()
				}
			}
		}
		if _, err := say(ctx, req, "s1", "answer", "On it."); err != nil {
			return "ERR " + err.Error()
		}
		return ""
	}
	f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
	f.start(engine)
	f.settle(t)
	var kind string
	if err := f.server.store.db.QueryRow(`SELECT kind FROM messages WHERE content='On it.' AND role='assistant'`).Scan(&kind); err != nil || kind != "" {
		t.Fatalf("short answer after a handoff: kind=%q err=%v", kind, err)
	}
}

// ---- Crash and resume ----

type countingEngine struct {
	calls atomic.Int32
	runID string
}

func (e *countingEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.RunID == e.runID {
		e.calls.Add(1)
	}
	return runtime.Result{Content: "a second answer from a model that should not run"}, nil
}

// The process dies after say(answer) recorded the answer and before the run
// published it. The resumed run finishes from that record with no model call
// and publishes the answer exactly once.
func TestResumeAfterCrashMidSayPublishesTheAnswerOnceWithoutAModelCall(t *testing.T) {
	f := crashedRun(t, "say")
	s, err := openStore(f.dir, false)
	if err != nil {
		t.Fatal(err)
	}
	// The crash left the say call in flight; its answer had been recorded.
	if err := s.recordSayAnswer(f.run.ID, "call-b", "The recorded answer."); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(f.dir) // recovery requeues the run from its checkpoint
	if err != nil {
		t.Fatal(err)
	}
	if status, _, resumes, _, _ := runColumns(t, s, f.run.ID); status != "queued" || resumes != 1 {
		t.Fatalf("status=%s resumes=%d", status, resumes)
	}
	s.Close()

	engine := &countingEngine{runID: f.run.ID}
	srv, err := NewServer(Config{DataDir: f.dir, Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	waitForStatus(t, srv, f.run.ID, "done")
	if engine.calls.Load() != 0 {
		t.Fatalf("the resumed run called the model %d time(s)", engine.calls.Load())
	}
	rows := assistantRows(t, srv.store, f.conv.ID)
	var visible []string
	for _, m := range rows {
		if m.Kind == "" || m.Kind == "segment" {
			visible = append(visible, m.Content)
		}
	}
	if !equalStrings(visible, []string{"The recorded answer."}) {
		t.Fatalf("visible messages = %q (all %q)", visible, kindsAndText(rows))
	}
	if got := originOf(t, srv.store, f.run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
	// The stream draft that was live at the crash became the answer's bubble:
	// no leftover half-written draft remains.
	if drafts, _ := srv.store.StreamDrafts(f.conv.ID); len(drafts) != 0 {
		t.Fatalf("drafts after finish: %+v", drafts)
	}
}

// Recording is the commit point: a second record of the same call is a no-op,
// and a different call cannot add a second answer.
func TestRecordedAnswerIsTheOnlyAnswerAcrossRestarts(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.CreateBot("resumer", "", "synthetic")
	conv, _ := s.GetConversation(bot.DMConversationID)
	_, run, _, _ := s.AddUserRun(conv.ID, bot.ID, "answer me", "once")
	if _, ok, err := s.claimRun(context.Background(), run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.recordSayAnswer(run.ID, "call-1", "one"); err != nil {
		t.Fatal(err)
	}
	if err := s.recordSayAnswer(run.ID, "call-1", "one"); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := s.recordSayAnswer(run.ID, "call-2", "two"); err == nil {
		t.Fatal("a second answer was recorded")
	}
	if rec, ok := s.sayAnswer(run.ID); !ok || rec.Text != "one" || rec.CallID != "call-1" {
		t.Fatalf("record = %+v %v", rec, ok)
	}
}

// ---- Streaming into the draft ----

func deltaTexts(t *testing.T, s *Store, convID string) []string {
	t.Helper()
	var out []string
	for _, ev := range eventsOfType(t, s, convID, "delta") {
		out = append(out, fmt.Sprint(ev["text"]))
	}
	return out
}

// A scripted loop streams a say call the way a provider does: the answer
// appears in the draft fragment by fragment, then becomes the message.
func TestSayTextStreamsIntoTheDraftAndBecomesTheMessage(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		// Plain text of the same response comes first and is replaced.
		req.OnDelta("Sure, let me write that down.")
		time.Sleep(70 * time.Millisecond)
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 1, ID: "call-s", Name: "say"})
		for _, frag := range []string{`{"purpose":"answer","outcome":"done",`, `"text":"Revenue `, `grew four `, `percent."}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 1, Arguments: frag})
			time.Sleep(70 * time.Millisecond)
		}
		_, err := say(ctx, req, "call-s", "answer", "Revenue grew four percent.")
		return runtime.Result{}, err
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	deltas := deltaTexts(t, s.store, run.ConversationID)
	joined := strings.Join(deltas, "")
	// Only the say text ever enters the chat draft: the plain text of the same
	// response goes to the live status line.
	if joined != "Revenue grew four percent." || len(deltas) < 3 {
		t.Fatalf("deltas = %q", deltas)
	}
	if len(eventsOfType(t, s.store, run.ConversationID, "draft_reset")) != 0 {
		t.Fatal("the draft was reset although no plain text ever entered it")
	}
	rows := assistantRows(t, s.store, run.ConversationID)
	if !equalStrings(kindsAndText(rows), []string{"|Revenue grew four percent."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	// The streamed bubble is the message: one identity.
	events := eventsOfType(t, s.store, run.ConversationID, "delta")
	if last := events[len(events)-1]; last["message_id"] != rows[0].ID {
		t.Fatalf("draft id %v != message id %s", last["message_id"], rows[0].ID)
	}
}

// The loop publishes a response's plain text before its tools run. When a say
// call's text is what the draft holds, that note must not take the bubble.
func TestInternalTextOfAnAnsweringResponseDoesNotTakeTheSayBubble(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnDelta("Let me answer.")
		time.Sleep(70 * time.Millisecond)
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 1, ID: "call-s", Name: "say"})
		for _, frag := range []string{`{"purpose":"answer","outcome":"done","text":"Half `, `past four."}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 1, Arguments: frag})
			time.Sleep(70 * time.Millisecond)
		}
		if err := req.OnAssistantTurn(1, "Let me answer."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "call-s", "answer", "Half past four.")
		return runtime.Result{}, err
	}))
	rows := assistantRows(t, s.store, run.ConversationID)
	if !equalStrings(kindsAndText(rows), []string{"internal|Let me answer.", "|Half past four."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	events := eventsOfType(t, s.store, run.ConversationID, "delta")
	if last := events[len(events)-1]; last["message_id"] != rows[1].ID || last["message_id"] == rows[0].ID {
		t.Fatalf("the answer lost its streamed bubble: draft=%v note=%s answer=%s", last["message_id"], rows[0].ID, rows[1].ID)
	}
	if rows[0].Seq >= rows[1].Seq {
		t.Fatalf("the note must precede the answer: %d vs %d", rows[0].Seq, rows[1].Seq)
	}
}

func TestSayNoteStreamsIntoTheDraftThenPostsInPlace(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "call-n", Name: "say"})
		for _, frag := range []string{`{"purpose":"note","outcome":"pending","text":"Checking `, `the inbox."}`} {
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: frag})
			time.Sleep(70 * time.Millisecond)
		}
		if _, err := say(ctx, req, "call-n", "note", "Checking the inbox."); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "call-a", "answer", "Two mails.")
		return runtime.Result{}, err
	}))
	rows := assistantRows(t, s.store, run.ConversationID)
	if !equalStrings(kindsAndText(rows), []string{"segment|Checking the inbox.", "|Two mails."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	events := eventsOfType(t, s.store, run.ConversationID, "delta")
	if len(events) == 0 || events[0]["message_id"] != rows[0].ID {
		t.Fatalf("the note did not take the streamed bubble: %v vs %s", events, rows[0].ID)
	}
}

// An answer that fails a gate does not leave its text sitting in the draft.
func TestRefusedSayClearsItsStreamedDraft(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if _, err := say(ctx, req, "a1", "answer", "first answer"); err != nil {
			return runtime.Result{}, err
		}
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "a2", Name: "say"})
		req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: `{"purpose":"answer","outcome":"done","text":"second answer"}`})
		_, err := say(ctx, req, "a2", "answer", "second answer")
		if err == nil {
			return runtime.Result{}, errors.New("second answer accepted")
		}
		return runtime.Result{}, nil
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|first answer"}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// ---- Workspace flag ----

func TestStructuredOutputPreferenceDefaultsOnAndPersists(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.structuredOutputOn() {
		t.Fatal("structured output must default on")
	}
	if prefs, err := s.userPreferences(); err != nil || !prefs.StructuredOutput {
		t.Fatalf("prefs=%+v err=%v", prefs, err)
	}
	if err := s.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	if s.structuredOutputOn() {
		t.Fatal("flag did not turn off")
	}
	// Other preference writes keep it.
	if _, err := s.putUserTimezone("UTC", false); err != nil {
		t.Fatal(err)
	}
	if err := s.putUserShowActivity(true); err != nil {
		t.Fatal(err)
	}
	if s.structuredOutputOn() {
		t.Fatal("another preference write turned the flag back on")
	}
	if err := s.putUserStructuredOutput(true); err != nil || !s.structuredOutputOn() {
		t.Fatalf("flag did not turn back on: %v", err)
	}
}

func TestPreferencesStructuredOutputOverHTTP(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := func(method, body string) string {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(method, "/api/preferences", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status=%d body=%s", method, body, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if body := call(http.MethodGet, ""); !strings.Contains(body, `"structured_output":true`) {
		t.Fatalf("must default on: %s", body)
	}
	if body := call(http.MethodPut, `{"structured_output":false}`); !strings.Contains(body, `"structured_output":false`) {
		t.Fatalf("put off: %s", body)
	}
	call(http.MethodPut, `{"show_activity":true}`)
	call(http.MethodPut, `{"language":"ja"}`)
	if body := call(http.MethodGet, ""); !strings.Contains(body, `"structured_output":false`) {
		t.Fatalf("other writes must keep the flag: %s", body)
	}
	if s.store.structuredOutputOn() {
		t.Fatal("the store disagrees with the API")
	}
	if body := call(http.MethodPut, `{"structured_output":true}`); !strings.Contains(body, `"structured_output":true`) {
		t.Fatalf("put on: %s", body)
	}
}

// A run keeps the protocol it started with, even if the setting changes while
// it works.
func TestRunKeepsTheProtocolItStartedWith(t *testing.T) {
	var srv *Server
	engine := dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if !req.StructuredOutput {
			return runtime.Result{}, errors.New("run did not start structured")
		}
		if err := srv.store.putUserStructuredOutput(false); err != nil {
			return runtime.Result{}, err
		}
		_, err := say(ctx, req, "a1", "answer", "still structured")
		return runtime.Result{}, err
	})
	srv, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	b, _ := srv.store.CreateBot("keeper", "", "model")
	c, _ := srv.store.GetConversation(b.DMConversationID)
	_, run, _, _ := srv.store.AddUserRun(c.ID, b.ID, "hello", "keep-1")
	srv.enqueue(c, run)
	got := waitRunTerminal(t, srv, run.ID)
	if got.Status != "done" || originOf(t, srv.store, run.ID) != finalOriginSay {
		t.Fatalf("run=%+v origin=%q", got, originOf(t, srv.store, run.ID))
	}
	if rows := assistantRows(t, srv.store, c.ID); !equalStrings(kindsAndText(rows), []string{"|still structured"}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// ---- Ending a turn cleanly ----

type engineByBot map[string]runtime.Engine

func (m engineByBot) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if e, ok := m[req.BotID]; ok {
		return e.Run(ctx, req)
	}
	return runtime.Result{Content: "ok"}, nil
}

// say(answer) is a group turn's last act: a model that would keep going after
// it is not asked to. (respond(reply) is gone under the flag; see
// TestRespondRefusesReplyUnderTheFlag.)
func TestGroupSayAnswerEndsTheTurn(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "say", `{"purpose":"answer","outcome":"done","text":"Spaces, and let a formatter decide."}`}}},
		fakeStep{content: "a model call that must not happen"})
	byBot := engineByBot{}
	f := newDedupeFixture(t, byBot)
	byBot[f.tofi.ID] = model.engine(t)
	f.post(t, "@tofi tabs or spaces", f.tofi)
	f.start(byBot)
	runs := f.settle(t)
	if len(runs) != 1 || runs[0].Status != "done" {
		t.Fatalf("runs=%+v", runs)
	}
	if n := modelCalls(model); n != 1 {
		t.Fatalf("model calls = %d, want 1", n)
	}
	if publishedBy(&f, f.tofi.ID) != 1 {
		t.Fatalf("published = %d", publishedBy(&f, f.tofi.ID))
	}
}

// After a handoff the Bot owes no answer: an empty closing response ends the
// run instead of being nudged to continue.
func TestEmptyClosingResponseAfterAHandoffEndsTheRun(t *testing.T) {
	var f dedupeFixture
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "handoff", `{"bot_id":"META","task":"write the metaphor"}`}}},
		fakeStep{content: ""},
		fakeStep{content: "Integrated the helper result."})
	byBot := engineByBot{}
	f = newDedupeFixture(t, byBot)
	model.steps[0].calls[0].args = strings.Replace(model.steps[0].calls[0].args, "META", f.meta.ID, 1)
	byBot[f.tofi.ID] = model.engine(t)
	byBot[f.meta.ID] = dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "helper result"}, nil
	})
	f.post(t, "@tofi get the metaphor from metaphor", f.tofi)
	f.start(byBot)
	runs := f.settle(t)
	var lead Run
	for _, r := range runs {
		if r.BotID == f.tofi.ID && r.Kind != runKindFollowup {
			lead = r
		}
	}
	if lead.Status != "done" {
		t.Fatalf("lead run = %+v (all %+v)", lead, runs)
	}
	// The third call is the follow-up run that integrates the colleague's
	// result; none of the requests is the nudge an empty response used to get.
	for i, body := range model.requests() {
		if strings.Contains(body, "Please continue. Use the available tools") {
			t.Fatalf("request %d nudged the model to continue after its handoff", i+1)
		}
	}
}

// Without a handoff an empty response is still not an ending.
func TestEmptyResponseWithoutAHandoffStillAsksToContinue(t *testing.T) {
	model := newFakeModel(t, fakeStep{content: ""}, fakeStep{content: "Here is the actual answer."})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	want := []string{"|Here is the actual answer."}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), want) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// The one steering line trails the history, next to the request it answers.
func TestSayPrincipleLineSitsBesideTheRequestOnlyWhenStructured(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("liner", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "What is a hash map?", "line-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, structured := range []bool{false, true} {
		run.structured = structured
		pm, system := s.buildContextParts(c, run, b)
		if strings.Contains(system, sayPrincipleLine) {
			t.Fatal("the steering line belongs beside the request, not in the cached system text")
		}
		if len(pm) < 2 {
			t.Fatalf("context = %+v", pm)
		}
		note, last := pm[len(pm)-2], pm[len(pm)-1]
		if has := strings.Contains(note.Content, sayPrincipleLine); has != structured {
			t.Fatalf("structured=%v: line present=%v in %q", structured, has, note.Content)
		}
		if !strings.Contains(last.Content, "What is a hash map?") {
			t.Fatalf("the request must stay last: %+v", last)
		}
	}
}

// A final draft sent back for review used to reset the say streamer when the
// next response started a say call, dropping every fragment after the first.
// The answer must keep growing in the draft fragment by fragment.
func TestSayStreamAfterAReviewDraftKeepsEveryFragment(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		const draft = "A first attempt at the answer."
		req.OnDelta(draft)
		if err := req.OnReviewDraft(1, draft); err != nil {
			return runtime.Result{}, err
		}
		streamed := make(chan struct{})
		go func() {
			defer close(streamed)
			req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, ID: "call-s", Name: "say"})
			for _, frag := range []string{`{"purpose":"answer","outcome":"done",`, `"text":"The reviewed `, `answer, in `, `three parts."}`} {
				req.OnToolCallDelta(runtime.ToolCallDelta{Index: 0, Arguments: frag})
				time.Sleep(70 * time.Millisecond)
			}
		}()
		select {
		case <-streamed:
		case <-time.After(5 * time.Second):
			return runtime.Result{}, errors.New("streaming a say call after a review draft deadlocked")
		}
		_, err := say(ctx, req, "call-s", "answer", "The reviewed answer, in three parts.")
		return runtime.Result{}, err
	}))
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if deltas := deltaTexts(t, s.store, run.ConversationID); strings.Join(deltas, "") != "The reviewed answer, in three parts." || len(deltas) < 3 {
		t.Fatalf("draft deltas = %q, want the answer growing fragment by fragment", deltas)
	}
	// The reviewed answer replaces the kept draft; the draft is not also posted.
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|The reviewed answer, in three parts."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

// Finding 8: notes are bounded like messages.
func TestSayNoteIsCappedPerRun(t *testing.T) {
	var last error
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		for i := 1; i <= maxSayNotes; i++ {
			if _, err := say(ctx, req, fmt.Sprintf("n%d", i), "note", fmt.Sprintf("progress %d", i)); err != nil {
				return runtime.Result{}, err
			}
		}
		_, last = say(ctx, req, "n-over", "note", "one too many")
		_, err := say(ctx, req, "a1", "answer", "done")
		return runtime.Result{}, err
	}))
	if last == nil || !strings.Contains(last.Error(), "limit") {
		t.Fatalf("note over the cap: %v", last)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); len(rows) != maxSayNotes+1 {
		t.Fatalf("rows = %d", len(rows))
	}
}
