package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// giveRunObservableWork records a finished tool call for the run, the kind of
// visible work after which a restart must not silently re-run it.
func giveRunObservableWork(t *testing.T, s *Store, r Run) {
	t.Helper()
	for _, status := range []string{"queued", "running", "completed"} {
		if err := s.RecordToolEvent(r.ConversationID, r.BotID, r.ID, runtime.ToolEvent{CallID: "work-" + r.ID, Name: "search_history", Arguments: `{}`, Result: "ok", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
}

// ---- Boundary checkpoints and crash resume (Phase 2) ----

type fakeCall struct{ id, name, args string }
type fakeStep struct {
	content string
	calls   []fakeCall
}

// fakeModel is a synthetic OpenAI-compatible endpoint that answers each
// request with the next scripted step and records every request body.
type fakeModel struct {
	srv    *httptest.Server
	mu     sync.Mutex
	steps  []fakeStep
	bodies []string
}

func newFakeModel(t *testing.T, steps ...fakeStep) *fakeModel {
	t.Helper()
	m := &fakeModel{steps: steps}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		n := len(m.bodies)
		m.bodies = append(m.bodies, string(raw))
		step := fakeStep{content: "done"}
		if n < len(m.steps) {
			step = m.steps[n]
		}
		m.mu.Unlock()
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(raw, &req)
		calls := []any{}
		for i, c := range step.calls {
			calls = append(calls, map[string]any{"index": i, "id": c.id, "type": "function", "function": map[string]any{"name": c.name, "arguments": c.args}})
		}
		usage := map[string]int{"prompt_tokens": 10, "completion_tokens": 5}
		if req.Stream {
			delta := map[string]any{"content": step.content}
			if len(calls) > 0 {
				delta["tool_calls"] = calls
			}
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}, "usage": usage})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
			return
		}
		message := map[string]any{"content": step.content}
		if len(calls) > 0 {
			message["tool_calls"] = calls
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message}}, "usage": usage})
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *fakeModel) engine(t *testing.T) runtime.Engine {
	t.Helper()
	e, err := runtime.New(runtime.Config{Provider: "openai_completions", APIKey: "synthetic", BaseURL: m.srv.URL + "/v1", Model: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (m *fakeModel) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

type resumeFixture struct {
	dir  string
	bot  Bot
	conv Conversation
	run  Run
}

// crashedRun leaves dir looking exactly as a killed process would: the run is
// running, its last durable checkpoint is the boundary after the first tool of
// a three-call batch, the second call is mid-execution (its activity row is
// "running") and the third is still queued. The real runtime writes every row
// through the real hooks; the "crash" is that nothing is recorded after the
// blocking tool starts.
func crashedRun(t *testing.T, inflightTool string) resumeFixture {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("resume bot", "", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	conv, _ := s.GetConversation(bot.DMConversationID)
	_, run, _, err := s.AddUserRun(conv.ID, bot.ID, "synthetic task", "resume-client")
	if err != nil {
		t.Fatal(err)
	}
	claim, ok, err := s.claimRun(context.Background(), run.ID)
	if err != nil || !ok || claim.Attempt != 1 {
		t.Fatalf("claim=%+v ok=%v err=%v", claim, ok, err)
	}
	model := newFakeModel(t, fakeStep{calls: []fakeCall{
		{"call-a", "search_history", `{"query":"first"}`},
		{"call-b", inflightTool, `{"id":"x"}`},
		{"call-c", "search_history", `{"query":"third"}`},
	}})
	var crashed atomic.Bool
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	block := func(ctx context.Context, _ json.RawMessage) (string, error) {
		crashed.Store(true)
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	tools := []Tool{
		{Name: "search_history", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "found-first", nil }},
		{Name: inflightTool, Parameters: map[string]any{"type": "object"}, Execute: block},
	}
	done := make(chan error, 1)
	go func() {
		_, err := model.engine(t).Run(ctx, runtime.Request{
			BotID: bot.ID, RunID: run.ID, ConversationID: conv.ID, Attempt: claim.Attempt,
			Messages: []runtime.Message{{Role: "user", Content: "synthetic task"}}, Tools: tools,
			OnCheckpoint: func(kind string, cp json.RawMessage) error {
				if crashed.Load() {
					return nil
				}
				return s.SaveRunCheckpoint(ctx, run.ID, claim.Attempt, kind, cp)
			},
			OnToolEvent: func(ev runtime.ToolEvent) error {
				if crashed.Load() {
					return nil
				}
				return s.RecordToolEvent(conv.ID, bot.ID, run.ID, ev)
			},
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight tool never started")
	}
	cancel()
	<-done
	return resumeFixture{dir: dir, bot: bot, conv: conv, run: run}
}

func runColumns(t *testing.T, s *Store, runID string) (status string, attempt, resumes int, notice, errText string) {
	t.Helper()
	var e sql.NullString
	if err := s.db.QueryRow(`SELECT status,attempt,resumes,resume_notice,error FROM runs WHERE id=?`, runID).Scan(&status, &attempt, &resumes, &notice, &e); err != nil {
		t.Fatal(err)
	}
	return status, attempt, resumes, notice, e.String
}

func activityByCall(t *testing.T, s *Store, runID string) map[string]ToolActivity {
	t.Helper()
	rows, err := s.db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at,outcome_json FROM tool_activities WHERE run_id=?`, runID)
	if err != nil {
		t.Fatal(err)
	}
	list, err := scanToolActivities(rows)
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]ToolActivity{}
	for _, a := range list {
		out[a.CallID] = a
	}
	return out
}

func checkpointCount(t *testing.T, s *Store, runID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_checkpoints WHERE run_id=?`, runID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func waitForStatus(t *testing.T, s *Server, id, status string) Run {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r, e := s.store.GetRun(id)
		if e == nil && r.Status == status {
			return r
		}
		time.Sleep(10 * time.Millisecond)
	}
	r, _ := s.store.GetRun(id)
	t.Fatalf("run %s did not reach %s (got %s, error %q)", id, status, r.Status, r.Error)
	return r
}

func TestCrashedRunRecoversAndResumesWithInterruptedEffect(t *testing.T) {
	f := crashedRun(t, "write_effect")

	// Startup recovery: queued, one resume, the in-flight effect settled uncertain.
	s, err := OpenStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	status, attempt, resumes, notice, _ := runColumns(t, s, f.run.ID)
	if status != "queued" || attempt != 1 || resumes != 1 || notice != "restart" {
		t.Fatalf("recovered run status=%s attempt=%d resumes=%d notice=%q", status, attempt, resumes, notice)
	}
	if checkpointCount(t, s, f.run.ID) != 1 {
		t.Fatal("the checkpoint must survive recovery for the resume to read")
	}
	acts := activityByCall(t, s, f.run.ID)
	if a := acts["call-a"]; a.Status != "completed" {
		t.Fatalf("finished call changed: %+v", a)
	}
	b := acts["call-b"]
	if b.Status != "failed" || b.Outcome == nil || b.Outcome.Code != "restart_interrupted_effect" || b.Outcome.Status != "uncertain_effect" || b.Outcome.Certainty != "unknown" || b.Outcome.NextAction != "verify_effect" {
		t.Fatalf("in-flight effect not settled uncertain: %+v", b)
	}
	c := acts["call-c"]
	if c.Status != "failed" || c.Outcome == nil || c.Outcome.Code != "batch_skipped" || c.Outcome.Certainty != "not_executed" {
		t.Fatalf("queued call not skipped: %+v", c)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Resume: the model receives the settled outcomes and finishes the run.
	model := newFakeModel(t, fakeStep{content: "Verified the write; nothing else to do."})
	srv, err := NewServer(Config{DataDir: f.dir, Engine: model.engine(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	waitForStatus(t, srv, f.run.ID, "done")
	requests := model.requests()
	if len(requests) == 0 {
		t.Fatal("the resumed run never called the model")
	}
	// A second request, if any, is the post-run long-term memory pass.
	body := requests[0]
	for _, want := range []string{"synthetic task", "found-first", "call-b", "restart_interrupted_effect", "verify_effect", "call-c", "batch_skipped"} {
		if !strings.Contains(body, want) {
			t.Fatalf("resumed model request lacks %q:\n%s", want, body)
		}
	}
	status, attempt, resumes, notice, _ = runColumns(t, srv.store, f.run.ID)
	if status != "done" || attempt != 2 || resumes != 1 || notice != "" {
		t.Fatalf("finished run status=%s attempt=%d resumes=%d notice=%q", status, attempt, resumes, notice)
	}
	if checkpointCount(t, srv.store, f.run.ID) != 0 {
		t.Fatal("a finished run kept its checkpoint")
	}
	msgs, _, err := srv.store.Messages(f.conv.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	notices := 0
	for _, m := range msgs {
		if m.Kind == "notice" && m.Notice != nil && m.Notice.Type == "resume" && m.Notice.Reason == "restart" && m.RunID == f.run.ID {
			notices++
		}
	}
	if notices != 1 {
		t.Fatalf("resume notices=%d in %+v", notices, msgs)
	}
}

func TestCrashedObservationToolIsRetryableAfterRestart(t *testing.T) {
	f := crashedRun(t, "list_bots")
	s, err := OpenStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := activityByCall(t, s, f.run.ID)["call-b"]
	if b.Status != "failed" || b.Outcome == nil || b.Outcome.Code != "restart_interrupted" || b.Outcome.Status != "transient_failure" || b.Outcome.Certainty != "no_side_effects" || b.Outcome.NextAction != "retry" {
		t.Fatalf("in-flight observation not retryable: %+v", b)
	}
}

func TestRecoveryWithoutCheckpoint(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.CreateBot("plain", "", "synthetic")
	conv, _ := s.GetConversation(bot.DMConversationID)

	_, idle, _, _ := s.AddUserRun(conv.ID, bot.ID, "nothing happened", "idle")
	if _, ok, err := s.claimRun(context.Background(), idle.ID); err != nil || !ok {
		t.Fatalf("claim idle: %v %v", ok, err)
	}
	// A second conversation: one active user run per conversation.
	other, _ := s.CreateBot("worked", "", "synthetic")
	otherConv, _ := s.GetConversation(other.DMConversationID)
	_, worked, _, _ := s.AddUserRun(otherConv.ID, other.ID, "did something", "worked")
	if _, ok, err := s.claimRun(context.Background(), worked.ID); err != nil || !ok {
		t.Fatalf("claim worked: %v %v", ok, err)
	}
	giveRunObservableWork(t, s, worked)

	if err := recoverInterruptedRuns(s.db); err != nil {
		t.Fatal(err)
	}
	status, _, resumes, notice, _ := runColumns(t, s, idle.ID)
	if status != "queued" || resumes != 1 || notice != "" {
		t.Fatalf("idle run status=%s resumes=%d notice=%q", status, resumes, notice)
	}
	status, _, _, _, errText := runColumns(t, s, worked.ID)
	if status != "interrupted" || errText != "service restarted after claim" {
		t.Fatalf("run with tool work but no checkpoint: status=%s error=%q", status, errText)
	}
}

func TestResumeCapFailsTheFourthInterruption(t *testing.T) {
	f := crashedRun(t, "write_effect")
	s, err := OpenStore(f.dir) // interruption 1 (open runs recovery)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for want := 1; want <= maxAutomaticResumes; want++ {
		status, _, resumes, _, _ := runColumns(t, s, f.run.ID)
		if status != "queued" || resumes != want {
			t.Fatalf("interruption %d: status=%s resumes=%d", want, status, resumes)
		}
		claim, ok, err := s.claimRun(context.Background(), f.run.ID)
		if err != nil || !ok || !claim.Boundary || claim.Attempt != want+1 {
			t.Fatalf("claim %d: %+v ok=%v err=%v", want, claim, ok, err)
		}
		if err := recoverInterruptedRuns(s.db); err != nil {
			t.Fatal(err)
		}
	}
	status, _, resumes, _, errText := runColumns(t, s, f.run.ID)
	if status != "failed" || resumes != maxAutomaticResumes+1 || errText != fmt.Sprintf("%s:%d", resumedRepeatedlyCode, maxAutomaticResumes+1) {
		t.Fatalf("4th interruption: status=%s resumes=%d error=%q", status, resumes, errText)
	}
	run, err := s.GetRun(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fail := run.failure(); fail == nil || fail.Code != resumedRepeatedlyCode || fail.Count != maxAutomaticResumes+1 {
		t.Fatalf("failure=%+v", fail)
	}
	if checkpointCount(t, s, f.run.ID) != 0 {
		t.Fatal("a failed run kept its checkpoint")
	}
}

func TestConcurrentClaimsOfOneQueuedRunSucceedOnce(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.CreateBot("racer", "", "synthetic")
	conv, _ := s.GetConversation(bot.DMConversationID)
	_, run, _, _ := s.AddUserRun(conv.ID, bot.ID, "claim me", "race")
	var wins, errs atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ok, err := s.claimRun(context.Background(), run.ID)
			if err != nil {
				errs.Add(1)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || errs.Load() != 0 {
		t.Fatalf("winners=%d errors=%d", wins.Load(), errs.Load())
	}
	if _, attempt, _, _, _ := runColumns(t, s, run.ID); attempt != 1 {
		t.Fatalf("attempt=%d", attempt)
	}
}

func TestStaleAttemptCheckpointIsRejected(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.CreateBot("stale", "", "synthetic")
	conv, _ := s.GetConversation(bot.DMConversationID)
	_, run, _, _ := s.AddUserRun(conv.ID, bot.ID, "stale writer", "stale")
	ctx := context.Background()
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "turn", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a checkpoint for a run that is not running was stored")
	}
	if _, ok, err := s.claimRun(ctx, run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, attempt := range []int{0, 2} {
		if err := s.SaveRunCheckpoint(ctx, run.ID, attempt, "turn", json.RawMessage(`{}`)); !errors.Is(err, errStaleCheckpoint) {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "turn", json.RawMessage(`{"n":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "tool", json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}
	var seq int
	var kind string
	if err := s.db.QueryRow(`SELECT seq,kind FROM run_checkpoints WHERE run_id=?`, run.ID).Scan(&seq, &kind); err != nil || seq != 2 || kind != "tool" {
		t.Fatalf("seq=%d kind=%s err=%v", seq, kind, err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "bogus", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown checkpoint kind accepted")
	}
	// An empty checkpoint means "could not snapshot this boundary": drop the old one.
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "tool", nil); err != nil || checkpointCount(t, s, run.ID) != 0 {
		t.Fatalf("empty checkpoint did not discard the stored one: %v", err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "tool", json.RawMessage(`{"n":2}`)); err != nil {
		t.Fatal(err)
	}

	// A newer claim fences out the older writer.
	if _, err := s.db.Exec(`UPDATE runs SET status='queued' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.claimRun(ctx, run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "tool", json.RawMessage(`{"n":3}`)); !errors.Is(err, errStaleCheckpoint) {
		t.Fatalf("old generation write: %v", err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 2, "tool", json.RawMessage(`{"n":4}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT seq FROM run_checkpoints WHERE run_id=?`, run.ID).Scan(&seq); err != nil || seq != 1 {
		t.Fatalf("seq restarts per attempt: %d %v", seq, err)
	}
}

func TestClaimRejectsCheckpointNotOlderThanTheClaim(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, _ := s.CreateBot("future", "", "synthetic")
	conv, _ := s.GetConversation(bot.DMConversationID)
	_, run, _, _ := s.AddUserRun(conv.ID, bot.ID, "future checkpoint", "future")
	ctx := context.Background()
	if _, ok, err := s.claimRun(ctx, run.ID); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := s.SaveRunCheckpoint(ctx, run.ID, 1, "tool", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET status='queued' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE run_checkpoints SET attempt=9 WHERE run_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.claimRun(ctx, run.ID); err == nil || ok {
		t.Fatalf("claimed over a checkpoint from a later attempt: ok=%v err=%v", ok, err)
	}
	if status, attempt, _, _, _ := runColumns(t, s, run.ID); status != "queued" || attempt != 1 {
		t.Fatalf("a refused claim must roll back: status=%s attempt=%d", status, attempt)
	}
}

// A human-input wait supersedes the boundary checkpoint taken before it: after
// the answer is claimed, a crash must not resume from the older snapshot.
func TestInputWaitSupersedesBoundaryCheckpoint(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	ctx := context.Background()
	if err := s.SaveRunCheckpoint(ctx, r.ID, 0, "tool", json.RawMessage(`{"before":"wait"}`)); err != nil {
		t.Fatal(err)
	}
	in, err := normalizeQuestionInput(askQuestionInput{Question: "Which value?", Type: questionText})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, r, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running"} {
		if err = s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: "wait-call", Name: "ask_user_question", Status: status}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SaveInputContinuation(ctx, r.ID, q.ID, json.RawMessage(`{"version":1,"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	if checkpointCount(t, s, r.ID) != 0 {
		t.Fatal("parking the run kept the older boundary checkpoint")
	}
	if _, _, err = s.AnswerQuestion(q.ID, "human", "answer"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.nextQueuedRun(c.ID); err != nil || !ok || got.ID != r.ID {
		t.Fatalf("answered run not queued: %+v %v %v", got, ok, err)
	}
	claim, ok, err := s.claimRun(ctx, r.ID)
	if err != nil || !ok || claim.Boundary || len(claim.Continuation) == 0 || string(claim.Question.Answer) != `"answer"` {
		t.Fatalf("claim=%+v ok=%v err=%v", claim, ok, err)
	}
}

func TestExecuteWiresCheckpointHookWithTheClaimAttempt(t *testing.T) {
	var seen struct {
		attempt, seq int
		kind         string
		count        int
	}
	var s *Server
	engine := checkpointProbeEngine(func(req runtime.Request) error {
		if err := req.OnCheckpoint("tool", json.RawMessage(`{"synthetic":true}`)); err != nil {
			return err
		}
		return s.store.db.QueryRow(`SELECT attempt,seq,kind,(SELECT COUNT(*) FROM run_checkpoints WHERE run_id=?) FROM run_checkpoints WHERE run_id=?`, req.RunID, req.RunID).Scan(&seen.attempt, &seen.seq, &seen.kind, &seen.count)
	})
	var err error
	s, err = NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("hook bot", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, _ := s.store.AddUserRun(c.ID, b.ID, "hook", "hook")
	s.execute(c, r)
	if seen.attempt != 1 || seen.seq != 1 || seen.kind != "tool" || seen.count != 1 {
		t.Fatalf("checkpoint seen by the engine: %+v", seen)
	}
	if checkpointCount(t, s.store, r.ID) != 0 {
		t.Fatal("a finished run kept its checkpoint")
	}
}

type checkpointProbeEngine func(runtime.Request) error

func (e checkpointProbeEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if strings.HasSuffix(req.RunID, ":memory") {
		return runtime.Result{Content: "ok"}, nil
	}
	if err := e(req); err != nil {
		return runtime.Result{}, err
	}
	return runtime.Result{Content: "ok"}, nil
}
