package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func originOf(t *testing.T, s *Store, runID string) string {
	t.Helper()
	var v string
	if err := s.db.QueryRow(`SELECT final_origin FROM runs WHERE id=?`, runID).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMergeFinalOrigin(t *testing.T) {
	for _, tc := range []struct{ cur, val, want string }{
		{"", "say", "say"},
		{"", heuristicRepeat, heuristicRepeat},
		{"say", heuristicExactDup, heuristicExactDup},
		{heuristicRepeat, "implicit", heuristicRepeat},
		{heuristicRepeat, heuristicNarrationFold, heuristicRepeat + "," + heuristicNarrationFold},
		{heuristicRepeat + "," + heuristicNarrationFold, heuristicRepeat, heuristicRepeat + "," + heuristicNarrationFold},
		{"implicit", "promoted", "promoted"},
		{"say", "", "say"},
	} {
		if got := mergeFinalOrigin(tc.cur, tc.val); got != tc.want {
			t.Errorf("merge(%q,%q)=%q want %q", tc.cur, tc.val, got, tc.want)
		}
	}
}

type dmFuncEngine func(ctx context.Context, req runtime.Request) (runtime.Result, error)

func (f dmFuncEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	return f(ctx, req)
}

// dmRun drives one direct-message run through execute() with a scripted engine.
func dmRun(t *testing.T, engine runtime.Engine) (*Server, Run) {
	t.Helper()
	return dmRunMode(t, engine, true)
}

// dmRunMode is dmRun under either output protocol.
func dmRunMode(t *testing.T, engine runtime.Engine, structured bool) (*Server, Run) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.store.putUserStructuredOutput(structured); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("origin fixture", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "What time is it in the synthetic city?", "origin-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	return s, waitRunTerminal(t, s, run.ID)
}

func TestFinalOriginImplicitFromPlainFinalText(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "It is half past four."}, nil
	}))
	if run.Status != "done" || originOf(t, s.store, run.ID) != "implicit" {
		t.Fatalf("run=%+v origin=%q", run, originOf(t, s.store, run.ID))
	}
}

// Heuristic H7 (structured output off): a progress turn stands in as the answer.
func TestFinalOriginPromotedWhenOnlyProgressWasPublished(t *testing.T) {
	s, run := dmRunMode(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		if err := req.OnAssistantTurn(1, "Checking the clock service."); err != nil {
			return runtime.Result{}, err
		}
		return runtime.Result{}, nil
	}), false)
	if got := originOf(t, s.store, run.ID); run.Status != "done" || got != "promoted" {
		t.Fatalf("run=%+v origin=%q", run, got)
	}
}

func TestFinalOriginNoneWhenRunFailsWithoutAnAnswer(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{}, errors.New("synthetic provider failure")
	}))
	if got := originOf(t, s.store, run.ID); run.Status != "failed" || got != "none" {
		t.Fatalf("run=%+v origin=%q", run, got)
	}
}

func TestFinalOriginNoneWhenGroupBotStaysSilent(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.meta.ID {
			if err := respond(ctx, req, map[string]any{"disposition": "concluded"}); err != nil {
				return "ERR " + err.Error()
			}
			return ""
		}
		return reply(ctx, req, "Page written.", wakeOf(f.meta.ID, "FYI: done, written to the page"))
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 2 {
		t.Fatalf("runs=%+v", runs)
	}
	for _, r := range runs {
		want := "say"
		if r.BotID == f.meta.ID {
			want = "none"
		}
		if got := originOf(t, f.server.store, r.ID); got != want {
			t.Fatalf("bot %s origin=%q want %q", r.BotID, got, want)
		}
	}
}

func TestFinalOriginSayForRespondReply(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		return reply(ctx, req, "the metaphor: a lighthouse", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if len(runs) != 1 || originOf(t, f.server.store, runs[0].ID) != "say" {
		t.Fatalf("runs=%+v origin=%q", runs, originOf(t, f.server.store, runs[0].ID))
	}
}

// Heuristic H8 over a published segment; with structured output a turn's plain
// text is private, so it is not "posted" and the reply is.
func TestFinalOriginExactDupWhenReplyRepeatsAPostedSegment(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	if err := f.server.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		_ = req.OnAssistantTurn(1, "the metaphor: a lighthouse")
		return reply(ctx, req, "the metaphor:  a lighthouse", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	if got := originOf(t, f.server.store, runs[0].ID); got != heuristicExactDup {
		t.Fatalf("origin=%q", got)
	}
}

func TestFinalOriginLeadingMentionDispatch(t *testing.T) {
	var f dedupeFixture
	engine := &mentionEngine{}
	f = newDedupeFixture(t, engine)
	if err := f.server.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
	engine.reply = func(ctx context.Context, req runtime.Request, text string) string {
		if req.BotID == f.tofi.ID {
			return "@metaphor draft one analogy for synthetic caching"
		}
		return reply(ctx, req, "Caching is a pantry near the kitchen.", nil)
	}
	f.post(t, "@tofi go", f.tofi)
	f.start(engine)
	runs := f.settle(t)
	found := false
	for _, r := range runs {
		if r.BotID == f.tofi.ID && r.ParentRunID == "" {
			found = true
			if got := originOf(t, f.server.store, r.ID); got != heuristicLeadingMention {
				t.Fatalf("origin=%q", got)
			}
		}
	}
	if !found {
		t.Fatalf("runs=%+v", runs)
	}
}

type shortScheduleEngine struct{}

func (shortScheduleEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	const args = `{"content":"scheduled result with the synthetic figures"}`
	for _, tool := range req.Tools {
		if tool.Name != "complete_scheduled_task" {
			continue
		}
		ev := func(status, result string) error {
			return req.OnToolEvent(runtime.ToolEvent{CallID: "origin-complete", Name: tool.Name, Arguments: args, Status: status, Result: result})
		}
		if err := ev("queued", ""); err != nil {
			return runtime.Result{}, err
		}
		if err := ev("running", ""); err != nil {
			return runtime.Result{}, err
		}
		if _, err := tool.Execute(ctx, json.RawMessage(args)); err != nil {
			return runtime.Result{}, err
		}
		if err := ev("completed", "accepted"); err != nil {
			return runtime.Result{}, err
		}
	}
	return runtime.Result{Content: "Done."}, nil
}

func TestFinalOriginSchedulePrepend(t *testing.T) {
	f := newDedupeFixture(t, shortScheduleEngine{})
	if err := f.server.store.putUserStructuredOutput(false); err != nil {
		t.Fatal(err)
	}
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
	if o := originOf(t, f.server.store, got.ID); got.Status != "done" || o != heuristicSchedulePrep {
		t.Fatalf("run=%+v origin=%q", got, o)
	}
}

func TestFinalOriginRepeatNarrationAndDelegationFolds(t *testing.T) {
	const restated = "Both schedules are set: 17:50 for the metaphor post and 17:53 for my report."
	long := strings.Repeat("The synthetic ledger has twelve entries this week and the largest is the rent share. ", 3)
	for _, tc := range []struct {
		name, segment, final, want string
		handoffs                   int
	}{
		{"repeat", restated, "Both schedules are set — 17:50 metaphor post, 17:53 my report.", heuristicRepeat, 0},
		{"narration", "Now the second page.", long, heuristicNarrationFold, 0},
		{"delegation", "", "Passed it to Sol, back when they reply.", heuristicDelegationFold, 1},
		{"plain", "", "A plain answer.", "implicit", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, c := streamFixture(t)
			defer s.Close()
			if _, err := s.BeginStream(r); err != nil {
				t.Fatal(err)
			}
			if tc.segment != "" {
				if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, tc.segment); err != nil || !ok {
					t.Fatalf("publish ok=%v err=%v", ok, err)
				}
			}
			if tc.handoffs > 0 {
				if _, err := s.db.Exec(`UPDATE runs SET handoff_count=? WHERE id=?`, tc.handoffs, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, tc.final); err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			if got := originOf(t, s, r.ID); got != tc.want {
				t.Fatalf("origin=%q want %q", got, tc.want)
			}
		})
	}
}

func TestFinalOriginMidRunRepeatAndMultipleHeuristics(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{
		"I keep the books; what to eat tonight is your call. Tell me what it cost and I will log it in dollars.",
		"What to eat tonight is your call; tell me what it cost and I will log it in dollars.",
	} {
		if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, i+1, text); err != nil || !ok {
			t.Fatalf("publish ok=%v err=%v", ok, err)
		}
	}
	if got := originOf(t, s, r.ID); got != heuristicRepeat {
		t.Fatalf("after mid-run repeat origin=%q", got)
	}
	long := strings.Repeat("The synthetic ledger has twelve entries this week and the largest is the rent share. ", 3)
	if _, err := s.db.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES('seg-short',?,9000,'assistant','segment',?,?,'Now the next page.',?)`, c.ID, r.BotID, r.ID, now()); err != nil {
		t.Fatal(err)
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, long); err != nil || !done {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if got, want := originOf(t, s, r.ID), heuristicRepeat+","+heuristicNarrationFold; got != want {
		t.Fatalf("origin=%q want %q", got, want)
	}
}

func TestRunsAPIExposesFinalOrigin(t *testing.T) {
	s, run := dmRun(t, dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		return runtime.Result{Content: "It is half past four."}, nil
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/"+run.ConversationID+"/runs", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Runs []struct {
			ID          string `json:"id"`
			FinalOrigin string `json:"final_origin"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Runs) != 1 || out.Runs[0].FinalOrigin != "implicit" {
		t.Fatalf("runs=%+v body=%s", out.Runs, rec.Body.String())
	}
}
