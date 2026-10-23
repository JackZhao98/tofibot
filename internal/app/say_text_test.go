package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// textFinalEngine ends the run the way the loop does on a text-only final: it
// asks the host's final review first and, when sent back, runs the repair
// closure.
type textFinalEngine struct {
	final  string
	repair func(ctx context.Context, req runtime.Request) error
	sent   *string // reminder the review returned
}

func (e textFinalEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.BeforeFinalResponse != nil {
		reminder, err := req.BeforeFinalResponse(e.final)
		if err != nil {
			return runtime.Result{}, err
		}
		if e.sent != nil {
			*e.sent = reminder
		}
		if reminder != "" && e.repair != nil {
			return runtime.Result{}, e.repair(ctx, req)
		}
	}
	return runtime.Result{Content: e.final}, nil
}

func visibleTexts(t *testing.T, s *Server, runID, convID string) []string {
	t.Helper()
	var out []string
	for _, m := range assistantRows(t, s.store, convID) {
		if m.RunID == runID && (m.Kind == "" || m.Kind == "segment") {
			out = append(out, m.Content)
		}
	}
	return out
}

func noRawSayJSON(t *testing.T, s *Server, convID string) {
	t.Helper()
	for _, m := range assistantRows(t, s.store, convID) {
		if strings.Contains(m.Content, `"purpose"`) {
			t.Fatalf("raw say arguments reached the chat: %q", m.Content)
		}
	}
}

func TestSayArgumentsWrittenAsTextAreDeliveredAsTheAnswer(t *testing.T) {
	for name, final := range map[string]string{
		"plain":  `{"purpose":"answer","outcome":"done","text":"It is half past four."}`,
		"fenced": "```json\n{\"purpose\":\"answer\",\"outcome\":\"done\",\"text\":\"It is half past four.\"}\n```",
		"padded": "\n  {\"purpose\":\"answer\",\"outcome\":\"done\",\"text\":\"It is half past four.\"}  \n",
	} {
		t.Run(name, func(t *testing.T) {
			s, run := dmRun(t, textFinalEngine{final: final})
			if run.Status != "done" {
				t.Fatalf("run=%+v", run)
			}
			if got := visibleTexts(t, s, run.ID, run.ConversationID); !equalStrings(got, []string{"It is half past four."}) {
				t.Fatalf("visible=%q", got)
			}
			noRawSayJSON(t, s, run.ConversationID)
			if origin := originOf(t, s.store, run.ID); origin != finalOriginSayTextRecovered {
				t.Fatalf("origin=%q", origin)
			}
		})
	}
}

func TestSayTextRecoveryIsDecidedBySchemaOnly(t *testing.T) {
	for name, final := range map[string]string{
		"wording around":   `Here you go: {"purpose":"answer","outcome":"done","text":"It is half past four."}`,
		"trailing wording": `{"purpose":"answer","outcome":"done","text":"It is half past four."} Done.`,
		"two objects":      `{"purpose":"answer","outcome":"done","text":"a"}{"purpose":"answer","outcome":"done","text":"b"}`,
		"other fence":      "```python\n{\"purpose\":\"answer\",\"outcome\":\"done\",\"text\":\"x\"}\n```",
		"no purpose key":   `{"text":"It is half past four."}`,
		"other object":     `{"city":"Synthetic","time":"16:30"}`,
	} {
		t.Run(name, func(t *testing.T) {
			var reminder string
			s, run := dmRun(t, textFinalEngine{final: final, sent: &reminder})
			if run.Status != "done" || reminder != "" {
				t.Fatalf("run=%+v reminder=%q", run, reminder)
			}
			if got := visibleTexts(t, s, run.ID, run.ConversationID); !equalStrings(got, []string{final}) {
				t.Fatalf("visible=%q", got)
			}
			if origin := originOf(t, s.store, run.ID); origin != finalOriginImplicit {
				t.Fatalf("origin=%q", origin)
			}
		})
	}
}

// JSON shaped like the say arguments that the tool's schema rejects is sent
// back once; the model's real call then delivers, and nothing raw is shown.
func TestInvalidSayArgumentsTextIsRepairedNotPublished(t *testing.T) {
	for name, final := range map[string]string{
		"unknown purpose": `{"purpose":"shout","text":"It is half past four."}`,
		"extra field":     `{"purpose":"answer","outcome":"done","text":"It is half past four.","mood":"calm"}`,
		"text not string": `{"purpose":"answer","outcome":"done","text":4}`,
	} {
		t.Run(name, func(t *testing.T) {
			var reminder string
			s, run := dmRun(t, textFinalEngine{final: final, sent: &reminder, repair: func(ctx context.Context, req runtime.Request) error {
				_, err := say(ctx, req, "call-real", "answer", "It is half past four.")
				return err
			}})
			if run.Status != "done" || reminder == "" {
				t.Fatalf("run=%+v reminder=%q", run, reminder)
			}
			if got := visibleTexts(t, s, run.ID, run.ConversationID); !equalStrings(got, []string{"It is half past four."}) {
				t.Fatalf("visible=%q", got)
			}
			noRawSayJSON(t, s, run.ConversationID)
			if origin := originOf(t, s.store, run.ID); origin != finalOriginSay {
				t.Fatalf("origin=%q", origin)
			}
		})
	}
}

// With no repair left (the loop returns the text as the final), invalid say
// arguments are still never published.
func TestInvalidSayArgumentsTextWithNoRepairLeftIsNotPublished(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: `{"purpose":"shout","text":"It is half past four."}`}, nil
	}))
	noRawSayJSON(t, s, run.ConversationID)
	if run.Status == "done" && len(visibleTexts(t, s, run.ID, run.ConversationID)) != 0 {
		t.Fatalf("published: %q", visibleTexts(t, s, run.ID, run.ConversationID))
	}
}

// A valid answer that reaches the end as the final text (no review ran, as
// when the repair budget is spent) is published as its inner text.
func TestValidSayTextAsTheFinalWithoutReviewPublishesTheInnerText(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: `{"purpose":"answer","outcome":"done","text":"It is half past four."}`}, nil
	}))
	if got := visibleTexts(t, s, run.ID, run.ConversationID); run.Status != "done" || !equalStrings(got, []string{"It is half past four."}) {
		t.Fatalf("run=%+v visible=%q", run, got)
	}
	if origin := originOf(t, s.store, run.ID); origin != finalOriginSayTextRecovered {
		t.Fatalf("origin=%q", origin)
	}
}

// Through the real loop: the model writes the say arguments as its final text.
func TestSayTextFinalThroughTheLoopIsDeliveredOnce(t *testing.T) {
	model := newFakeModel(t, fakeStep{content: `{"purpose":"answer","outcome":"done","text":"It is half past four."}`},
		fakeStep{content: "a call that must not happen"})
	s, run := serverWithModel(t, model, true)
	if run.Status != "done" {
		t.Fatalf("run=%+v", run)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|It is half past four."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSayTextRecovered {
		t.Fatalf("origin = %q", got)
	}
}

// A recovered answer is the real say call: the completion review owed after
// tool work sends it back once, exactly as for a real call.
func TestRecoveredSayAnswerGetsTheCompletionReview(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{{"c1", "create_work_item", `{"kind":"task","title":"Check the numbers"}`}}},
		fakeStep{content: `{"purpose":"answer","outcome":"done","text":"First draft of the answer."}`},
		fakeStep{calls: []fakeCall{{"c3", "say", `{"purpose":"answer","outcome":"done","text":"Reviewed answer."}`}}},
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
		t.Fatalf("the review reminder appeared %d times, want 1", reminders)
	}
	if rows := assistantRows(t, s.store, run.ConversationID); !equalStrings(kindsAndText(rows), []string{"|Reviewed answer."}) {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
	if got := originOf(t, s.store, run.ID); got != finalOriginSay {
		t.Fatalf("origin = %q", got)
	}
}

func TestScheduledRunWithSayTextGetsItsReceipt(t *testing.T) {
	engine := dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		final := `{"purpose":"answer","outcome":"done","text":"The synthetic digest is posted.","sources":["a"]}`
		reminder, err := req.BeforeFinalResponse(final)
		if err != nil || reminder != "" {
			return runtime.Result{}, errString("review: " + reminder)
		}
		return runtime.Result{Content: final}, nil
	})
	f := newDedupeFixture(t, engine)
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
	if v := visibleTexts(t, f.server, got.ID, f.group.ID); !equalStrings(v, []string{"The synthetic digest is posted."}) {
		t.Fatalf("visible=%q", v)
	}
	noRawSayJSON(t, f.server, f.group.ID)
	if origin := originOf(t, f.server.store, got.ID); origin != finalOriginSayTextRecovered {
		t.Fatalf("origin=%q", origin)
	}
	if sc, err := f.server.store.GetSchedule(x.ID); err != nil || sc.Status != scheduleComplete {
		t.Fatalf("schedule=%+v err=%v", sc, err)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
