package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// modeGrader stands in for a grader that fails a posted reply when the task
// forbids messaging. It reads only the delivery mode the grader input carries;
// a quiet record is graded as a record and passes.
type modeGrader struct {
	mu       sync.Mutex
	requests []string
}

func (g *modeGrader) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	body := req.Messages[0].Content
	g.mu.Lock()
	g.requests = append(g.requests, body)
	g.mu.Unlock()
	reply := gradePass
	if strings.Contains(body, "## Delivery mode of the reply\nposted") && strings.Contains(body, "do not message") {
		reply = strings.Replace(gradeGap, "Hands the work back to the person.", "Messages the person although the task says not to.", 1)
	}
	return &provider.ChatResponse{Content: reply, Usage: provider.Usage{InputTokens: 100, OutputTokens: 20}}, nil
}

func (g *modeGrader) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return g.Chat(ctx, req)
}

func runQuietGrading(t *testing.T, quiet bool) (*Server, Run, *modeGrader, *fakeModel) {
	t.Helper()
	g := &modeGrader{}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{quietSayCall("q1", "Both items show Currently Unavailable; checked two pages, no restock.", quiet)}})
	retainDeltaEvents(t)
	f := newDedupeFixture(t, model.engine(t))
	f.server.answerGraderProvider = g
	run := scheduledInDM(t, f, "check stock; do not message unless an item is restocked")
	return f.server, run, g, model
}

// A quiet record that states what was checked and the outcome passes a grader
// that fails a posted reply for breaking a no-message condition, and the
// grader input carries the quiet mode.
func TestQuietRecordIsGradedAsAPrivateRecord(t *testing.T) {
	s, run, g, model := runQuietGrading(t, true)
	if run.Status != "done" || modelCalls(model) != 1 {
		t.Fatalf("run=%+v model=%d", run, modelCalls(model))
	}
	rec, ok := s.store.sayAnswer(run.ID)
	if !ok || !rec.Quiet {
		t.Fatalf("record = %+v ok=%v", rec, ok)
	}
	if len(g.requests) != 1 || !strings.Contains(g.requests[0], "## Delivery mode of the reply\nquiet: a private completion record") || strings.Contains(g.requests[0], "\nposted:") {
		t.Fatalf("grader input lacks the quiet mode: %q", g.requests)
	}
	if !strings.Contains(g.requests[0], "never shown to the person") {
		t.Fatal("the rubric does not tell the grader how to read a quiet record")
	}
}

// The same text posted to the chat is graded as a reply and is sent back.
func TestPostedReplyCarriesPostedModeToTheGrader(t *testing.T) {
	_, _, g, model := runQuietGrading(t, false)
	if len(g.requests) == 0 || !strings.Contains(g.requests[0], "## Delivery mode of the reply\nposted: the person reads this reply in the chat") {
		t.Fatalf("grader input = %q", g.requests)
	}
	if revisionRequests(model) < 1 {
		t.Fatalf("a posted reply that breaks the condition was not sent back: revisions=%d", revisionRequests(model))
	}
}

func TestSayTextVisible(t *testing.T) {
	for text, want := range map[string]bool{
		"":                       false,
		" \t\n":                  false,
		"\u200b":                 false,
		"\u200c\u200d\u2060":     false,
		"\ufeff":                 false,
		"\x00":                   false,
		" \u200b\x00\x07 \u00a0": false,
		"ok":                     true,
		"\u200b ok \x00":         true,
		"好":                      true,
	} {
		if got := sayTextVisible(text); got != want {
			t.Errorf("sayTextVisible(%q) = %v, want %v", text, got, want)
		}
	}
}

// Empty and invisible text is refused for every purpose with the stable code,
// as a validation outcome whose next action is repair.
func TestSayRefusesInvisibleTextForAnswerMessageAndNote(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	c, _ := s.store.GetConversation(run.ConversationID)
	bot, _ := s.store.GetBot(run.BotID)
	_, live, _, err := s.store.AddUserRun(c.ID, bot.ID, "a live turn", "invisible-text-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), live.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live.structured = true
	tool := s.sayTool(c, live)
	for _, purpose := range []string{"answer", "message", "note"} {
		for _, text := range []string{"", "  \n\t", "\u200b", "\u200b\u200c\u200d\u2060\ufeff", "\x00", " \u200b\x00 "} {
			args, _ := json.Marshal(map[string]string{"purpose": purpose, "text": text})
			_, err := tool.Execute(runtime.WithToolCallID(context.Background(), "inv-"+purpose), args)
			o, ok := tooloutcome.FromError(err)
			if !ok || o.Code != "say_text_empty" || o.Status != tooloutcome.Validation || o.NextAction != "repair_arguments" || o.Certainty != "not_executed" {
				t.Fatalf("%s %q: err=%v outcome=%+v", purpose, text, err, o)
			}
		}
	}
	if _, recorded := s.store.sayAnswer(live.ID); recorded {
		t.Fatal("an invisible answer was recorded")
	}
	// NUL is stripped from text that is otherwise visible.
	if _, err := tool.Execute(runtime.WithToolCallID(context.Background(), "nul-1"), json.RawMessage(`{"purpose":"answer","text":"hel\u0000lo"}`)); err != nil {
		t.Fatal(err)
	}
	if rec, ok := s.store.sayAnswer(live.ID); !ok || rec.Text != "hello" {
		t.Fatalf("record = %+v ok=%v", rec, ok)
	}
}

// With the gate's rounds spent, a later draft ships ungraded only when it has
// something to read: invisible drafts are refused until the repair budget is
// gone, and the run then fails instead of recording a blank conclusion.
func TestInvisibleDraftAfterTheRoundCapIsRefusedAndTheRunFails(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{quietSayCall("c1", "First draft that hands the work back.", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("c2", "\u200b", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("c3", "\u0000", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("c4", " ", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("c5", "\u200d", true)}})
	s, run := gateRunWith(t, model.engine(t), g, `{"answer_gate":1}`)
	if g.calls.Load() != 1 {
		t.Fatalf("graded %d times, want the single round", g.calls.Load())
	}
	if rec, ok := s.store.sayAnswer(run.ID); ok {
		t.Fatalf("a blank conclusion was recorded: %q", rec.Text)
	}
	if run.Status != "failed" || !strings.HasPrefix(run.Error, "say_text_empty:") {
		t.Fatalf("run = %+v", run)
	}
	joined := strings.Join(model.requests(), "\n")
	if !strings.Contains(joined, "say_text_empty") {
		t.Fatal("the writer never saw the refusal code")
	}
}

// A valid draft after the cap still ships, as before.
func TestValidDraftAfterTheRoundCapStillShips(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap}}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{sayAnswerCall("c1", "First.")}},
		fakeStep{calls: []fakeCall{sayAnswerCall("c2", "\u200b")}},
		fakeStep{calls: []fakeCall{sayAnswerCall("c3", "Second, with content.")}})
	s, run := gateRunWith(t, model.engine(t), g, `{"answer_gate":1}`)
	if run.Status != "done" || g.calls.Load() != 1 {
		t.Fatalf("run=%+v graded=%d", run, g.calls.Load())
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Second, with content."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}
