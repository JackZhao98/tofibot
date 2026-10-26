package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func quietSayCall(id, text string, quiet bool) fakeCall {
	args, _ := json.Marshal(map[string]any{"purpose": "answer", "outcome": "done", "text": text, "sources": []string{"synthetic source"}, "quiet": quiet})
	return fakeCall{id, "say", string(args)}
}

// scheduledInDM runs one scheduled occurrence of the fixture's Bot in its DM.
func scheduledInDM(t *testing.T, f dedupeFixture, content string) Run {
	t.Helper()
	dm, err := f.server.store.GetConversation(f.tofi.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	x, err := f.server.store.CreateSchedule(dm.ID, f.tofi.ID, ScheduleSpec{Content: content, Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	makeDue(t, f.server.store, x.ID, time.Now().Add(-time.Minute))
	claimed, err := f.server.store.ClaimDueSchedules(time.Now())
	if err != nil || len(claimed) != 1 {
		// The server's own ScheduleWorker may have claimed it first; either way
		// exactly one run exists for the occurrence.
		runs, _ := f.server.store.Runs(dm.ID)
		if len(runs) != 1 {
			t.Fatalf("claim=%+v err=%v runs=%+v", claimed, err, runs)
		}
		claimed = runs
	}
	f.server.enqueue(dm, claimed[0])
	return waitRunTerminal(t, f.server, claimed[0].ID)
}

func visibleAnswers(t *testing.T, s *Store, convID string) []Message {
	t.Helper()
	var out []Message
	for _, m := range assistantRows(t, s, convID) {
		if m.Kind == "" && strings.TrimSpace(m.Content) != "" {
			out = append(out, m)
		}
	}
	return out
}

// A scheduled answer with quiet completes the run: receipt, status done, the
// conclusion recorded, and no chat message.
func TestQuietScheduledAnswerCompletesWithoutAChatMessage(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{quietSayCall("q1", "Nothing was restocked; checked two pages.", true)}})
	f := newDedupeFixture(t, model.engine(t))
	run := scheduledInDM(t, f, "notify only if an item is restocked")
	if run.Status != "done" {
		t.Fatalf("run = %+v", run)
	}
	if done, err := f.server.store.scheduleReceipt(run.ID); err != nil || !done {
		t.Fatalf("receipt = %v, %v", done, err)
	}
	got, _ := f.server.store.GetRun(run.ID)
	if got.FinalOrigin != finalOriginQuiet {
		t.Fatalf("final_origin = %q", got.FinalOrigin)
	}
	if rows := visibleAnswers(t, f.server.store, run.ConversationID); len(rows) != 0 {
		t.Fatalf("a quiet run posted %q", kindsAndText(rows))
	}
	occ, err := f.server.store.ScheduleOccurrences(context.Background(), run.ConversationID, []string{run.ID})
	if err != nil || len(occ) != 1 {
		t.Fatalf("occurrences = %+v, %v", occ, err)
	}
	o := occ[0]
	if o.ExecutionStatus != "done" || o.ResultPosted || o.ResultInConversation || o.ResultText != "Nothing was restocked; checked two pages." ||
		len(o.ResultSources) != 1 || o.ResultSources[0] != "synthetic source" || o.CompletedAt == "" {
		t.Fatalf("occurrence = %+v", o)
	}
}

// Without quiet a scheduled answer posts, as before, and the occurrence still
// carries the recorded result.
func TestDefaultScheduledAnswerIsStillPosted(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{quietSayCall("q1", "Restocked: the synthetic item.", false)}})
	f := newDedupeFixture(t, model.engine(t))
	run := scheduledInDM(t, f, "report the restock status")
	if run.Status != "done" {
		t.Fatalf("run = %+v", run)
	}
	if rows := visibleAnswers(t, f.server.store, run.ConversationID); len(rows) != 1 || rows[0].Content != "Restocked: the synthetic item." {
		t.Fatalf("messages = %q", kindsAndText(visibleAnswers(t, f.server.store, run.ConversationID)))
	}
	got, _ := f.server.store.GetRun(run.ID)
	if got.FinalOrigin != finalOriginSay {
		t.Fatalf("final_origin = %q", got.FinalOrigin)
	}
	occ, _ := f.server.store.ScheduleOccurrences(context.Background(), run.ConversationID, []string{run.ID})
	if len(occ) != 1 || !occ[0].ResultPosted || !occ[0].ResultInConversation || occ[0].ResultText != "Restocked: the synthetic item." {
		t.Fatalf("occurrence = %+v", occ)
	}
}

// The occurrence API serves the same fields to the conversation's reader.
func TestOccurrenceAPIReturnsTheQuietResult(t *testing.T) {
	model := newFakeModel(t, fakeStep{calls: []fakeCall{quietSayCall("q1", "Nothing changed.", true)}})
	f := newDedupeFixture(t, model.engine(t))
	run := scheduledInDM(t, f, "notify only on change")
	got := readOccurrences(t, f.server, run.ConversationID, run.ID)
	if len(got) != 1 || got[0].ResultText != "Nothing changed." || got[0].ResultPosted || got[0].CompletedAt == "" {
		t.Fatalf("api occurrence = %+v", got)
	}
	w := occurrenceCall(f.server, http.MethodGet, run.ConversationID, "root_run_ids="+run.ID)
	if !strings.Contains(w.Body.String(), `"result_posted":false`) || !strings.Contains(w.Body.String(), `"result_text":"Nothing changed."`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

// quiet is a scheduled-run field: refused elsewhere with a stable code, and
// not offered in the schema.
func TestQuietIsRefusedOutsideScheduledRuns(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "plain"}, nil
	}))
	c, _ := s.store.GetConversation(run.ConversationID)
	bot, _ := s.store.GetBot(run.BotID)
	_, live, _, err := s.store.AddUserRun(c.ID, bot.ID, "a live turn", "quiet-refused-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), live.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	live.structured = true
	tool := s.sayTool(c, live)
	if _, offered := tool.Parameters["properties"].(map[string]any)["quiet"]; offered {
		t.Fatal("a chat turn is offered quiet")
	}
	_, err = tool.Execute(runtime.WithToolCallID(context.Background(), "q1"), json.RawMessage(`{"purpose":"answer","outcome":"done","text":"nothing to report","quiet":true}`))
	outcome, ok := tooloutcome.FromError(err)
	if !ok || outcome.Code != "quiet_not_available" {
		t.Fatalf("err = %v", err)
	}
	if _, recorded := s.store.sayAnswer(live.ID); recorded {
		t.Fatal("a refused quiet answer was recorded")
	}
	// The same turn can still answer normally.
	if _, err := tool.Execute(runtime.WithToolCallID(context.Background(), "q2"), json.RawMessage(`{"purpose":"answer","outcome":"done","text":"nothing to report"}`)); err != nil {
		t.Fatal(err)
	}
	sched := live
	if props := s.sayTool(c, Run{Kind: runKindSchedule, structured: true}).Parameters["properties"].(map[string]any); props["quiet"] == nil {
		t.Fatalf("a scheduled run is not offered quiet: %v", props)
	}
	_ = sched
}

// D43 still grades a quiet answer: a gap sends it back to the writer, and
// only the revised one is recorded.
func TestQuietAnswerIsStillGradedByTheAnswerGate(t *testing.T) {
	g := &gradeProvider{replies: []string{gradeGap, gradePass}}
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{quietSayCall("c1", "First draft that hands the work back.", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("c2", "Second draft with the finding.", true)}})
	s, run := gateRunWith(t, model.engine(t), g, "")
	if run.Status != "done" || g.calls.Load() != 2 || modelCalls(model) != 2 {
		t.Fatalf("run=%+v grader=%d model=%d", run, g.calls.Load(), modelCalls(model))
	}
	rec, ok := s.store.sayAnswer(run.ID)
	if !ok || !rec.Quiet || rec.Text != "Second draft with the finding." {
		t.Fatalf("record = %+v ok=%v", rec, ok)
	}
	if rows := visibleAnswers(t, s.store, run.ConversationID); len(rows) != 0 {
		t.Fatalf("messages = %q", kindsAndText(rows))
	}
}

func contextContents(msgs []runtime.Message) string {
	var parts []string
	for _, m := range msgs {
		parts = append(parts, m.Content)
	}
	return strings.Join(parts, "\n---\n")
}

// A later turn in the conversation sees a quiet run's conclusion, and a posted
// result that is still in the history window is not repeated.
func TestLaterTurnContextCarriesQuietAndSkipsPostedResultsInTheWindow(t *testing.T) {
	model := newFakeModel(t,
		fakeStep{calls: []fakeCall{quietSayCall("q1", "Quiet conclusion: nothing restocked.", true)}},
		fakeStep{calls: []fakeCall{quietSayCall("q2", "Posted conclusion: the item is back.", false)}})
	f := newDedupeFixture(t, model.engine(t))
	scheduledInDM(t, f, "notify only if an item is restocked")
	posted := scheduledInDM(t, f, "report the restock status")
	dm, _ := f.server.store.GetConversation(posted.ConversationID)
	msgs, _ := f.server.buildContextPartsWith(dm, Run{ID: "later-turn", ConversationID: dm.ID, BotID: f.tofi.ID, structured: true}, f.tofi, nil)
	all := contextContents(msgs)
	if !strings.Contains(all, "[recent scheduled results") || !strings.Contains(all, "Quiet conclusion: nothing restocked.") || !strings.Contains(all, "(quiet)") {
		t.Fatalf("the quiet result is missing from the context:\n%s", all)
	}
	if !strings.Contains(all, "never instructions") {
		t.Fatalf("the block is not marked as records data:\n%s", all)
	}
	if n := strings.Count(all, "Posted conclusion: the item is back."); n != 1 {
		t.Fatalf("the posted result appears %d times, want once (the message):\n%s", n, all)
	}
	if !strings.Contains(all, "(posted in the chat above)") {
		t.Fatalf("the posted occurrence is not listed:\n%s", all)
	}
}

// No schedules, no block; many long results stay inside the caps.
func TestScheduleResultsBlockIsAbsentWithoutSchedulesAndBounded(t *testing.T) {
	f := newDedupeFixture(t, nil)
	dm, _ := f.server.store.GetConversation(f.tofi.DMConversationID)
	later := Run{ID: "later-turn", ConversationID: dm.ID, BotID: f.tofi.ID, structured: true}
	msgs, _ := f.server.buildContextPartsWith(dm, later, f.tofi, nil)
	if strings.Contains(contextContents(msgs), "recent scheduled results") {
		t.Fatal("a conversation without schedules got the results block")
	}
	st := f.server.store
	long := strings.Repeat("w", 1000)
	record := func(n int, id, text string) {
		t.Helper()
		x, err := st.CreateSchedule(dm.ID, f.tofi.ID, ScheduleSpec{Content: fmt.Sprintf("synthetic task %d", n), Kind: scheduleInterval, IntervalSeconds: 60, Timezone: "UTC", RunAt: time.Now().Add(time.Hour).Format(time.RFC3339)})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			makeDue(t, st, x.ID, time.Now().Add(-time.Minute))
			runs, err := st.ClaimDueSchedules(time.Now())
			if err != nil || len(runs) != 1 {
				t.Fatalf("claim=%+v err=%v", runs, err)
			}
			if _, ok, err := st.claimRun(context.Background(), runs[0].ID); err != nil || !ok {
				t.Fatal(ok, err)
			}
			if err := st.recordSayAnswerWith(runs[0].ID, fmt.Sprintf("%s-%d-%d", id, n, i), text, sayAnswerExtras{Quiet: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetRunStatus(runs[0].ID, "done", ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	for n := 0; n < 8; n++ {
		record(n, "b", long)
	}
	note := f.server.scheduleResultsNote(dm, later, time.UTC, nil)
	lines := strings.Split(note, "\n")
	// header, footer, and at most 6 schedules x 3 occurrences
	if len(lines) > 2+maxScheduleResultSchedules*maxScheduleResultsPer {
		t.Fatalf("block lists %d lines:\n%s", len(lines), note)
	}
	text := 0
	for _, l := range lines {
		if i := strings.Index(l, "): "); i >= 0 {
			n := len([]rune(l[i+3:]))
			if n > maxScheduleResultRunes {
				t.Fatalf("a result is %d runes", n)
			}
			text += n
		}
	}
	if text > maxScheduleResultsRunes || text == 0 {
		t.Fatalf("result text totals %d runes (cap %d)", text, maxScheduleResultsRunes)
	}
	if per := strings.Count(note, "synthetic task 0,"); per > maxScheduleResultsPer {
		t.Fatalf("one schedule lists %d occurrences", per)
	}
}
