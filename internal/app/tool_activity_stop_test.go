package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestInterruptToolActivitiesScansOutcomeAndPublishesOneTerminalEvent(t *testing.T) {
	s, c, r := questionFixture(t)
	defer s.Close()
	uncertain := tooloutcome.New(tooloutcome.Uncertain, "synthetic_unknown", "unknown", "Verify the synthetic effect before retrying.", "verify_effect")
	for _, call := range []struct{ id, status string }{{"complete", "completed"}, {"failure", "failed"}, {"active", "running"}, {"tail", "queued"}} {
		if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call.id, Name: "synthetic_tool", Arguments: "{}", Status: "queued"}); err != nil {
			t.Fatal(err)
		}
		if call.status != "queued" {
			ev := runtime.ToolEvent{CallID: call.id, Name: "synthetic_tool", Status: call.status, Result: "retained synthetic result"}
			if call.status == "running" || call.status == "failed" {
				ev.Outcome = &uncertain
			}
			if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if err := s.InterruptToolActivities(r.ID); err != nil {
			t.Fatal(err)
		}
	}
	activities, err := s.ToolActivities(c.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range activities {
		want := map[string]string{"complete": "completed", "failure": "failed", "active": "interrupted", "tail": "interrupted"}[a.CallID]
		if a.Status != want {
			t.Fatalf("activity=%+v want=%s", a, want)
		}
		if a.CallID == "active" && (a.Outcome == nil || a.Outcome.Code != uncertain.Code || a.Result != "retained synthetic result") {
			t.Fatalf("lost result/outcome: %+v", a)
		}
	}
	rows, err := s.db.Query(`SELECT data FROM events WHERE conversation_id=? AND type='tool'`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	interrupted := map[string]int{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		var a ToolActivity
		if err = json.Unmarshal([]byte(data), &a); err != nil {
			t.Fatal(err)
		}
		if a.Status == "interrupted" {
			interrupted[a.CallID]++
			if a.CallID == "active" && (a.Outcome == nil || a.Outcome.Code != uncertain.Code) {
				t.Fatal("SSE terminal event lost uncertainty")
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(interrupted) != 2 || interrupted["active"] != 1 || interrupted["tail"] != 1 {
		t.Fatalf("interrupted events=%v", interrupted)
	}
}

type stopTraceEngine struct {
	started          chan struct{}
	calls            atomic.Int32
	completedEffects atomic.Int32
}

func (e *stopTraceEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.calls.Add(1)
	for _, ev := range []runtime.ToolEvent{
		{CallID: "completed-effect", Name: "synthetic_completed_write", Arguments: "{}", Status: "queued"},
		{CallID: "completed-effect", Name: "synthetic_completed_write", Status: "running"},
		{CallID: "completed-effect", Name: "synthetic_completed_write", Status: "completed", Result: "confirmed synthetic effect"},
		{CallID: "active-effect", Name: "synthetic_pending_write", Arguments: "{}", Status: "queued"},
		{CallID: "active-effect", Name: "synthetic_pending_write", Status: "running"},
		{CallID: "queued-tail", Name: "synthetic_send", Arguments: "{}", Status: "queued"},
	} {
		if err := req.OnToolEvent(ev); err != nil {
			return runtime.Result{}, err
		}
		if ev.Status == "completed" {
			e.completedEffects.Add(1)
		}
	}
	close(e.started)
	<-ctx.Done()
	return runtime.Result{}, ctx.Err()
}

func TestStopHTTPInterruptsToolTracesWithoutEffectReplay(t *testing.T) {
	e := &stopTraceEngine{started: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e, IsolatedWorkspace: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("synthetic bot", "Synthetic stop trace fixture.", "test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "Synthetic work to stop.", "synthetic-stop")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.execute(c, r); close(done) }()
	select {
	case <-e.started:
	case <-time.After(5 * time.Second):
		t.Fatal("synthetic runtime did not start")
	}
	res := httptest.NewRecorder()
	s.run(res, httptest.NewRequest(http.MethodPost, "/api/runs/"+r.ID+"/cancel", nil), r.ID)
	if res.Code != http.StatusOK {
		t.Fatalf("Stop response=%d %s", res.Code, res.Body.String())
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not release the runtime")
	}
	got, err := s.store.GetRun(r.ID)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	summaries, err := s.store.ToolActivitySummaries(c.ID, []string{r.ID})
	if err != nil || len(summaries) != 1 || summaries[0].CompletedCount != 1 || summaries[0].InterruptedCount != 2 || summaries[0].PendingCount != 0 {
		t.Fatalf("summaries=%+v err=%v", summaries, err)
	}
	if _, err = s.store.RetryRun(r.ID); err == nil {
		t.Fatal("Stop permitted effect replay")
	}
	if _, ok, err := s.store.nextQueuedRun(c.ID); err != nil || ok {
		t.Fatalf("stopped work requeued: %v %v", ok, err)
	}
	if e.calls.Load() != 1 || e.completedEffects.Load() != 1 {
		t.Fatalf("calls=%d completed effects=%d", e.calls.Load(), e.completedEffects.Load())
	}
	// This is the same feed used by the UI after reload; no tool keeps ticking.
	feed := httptest.NewRecorder()
	s.routeToolActivities(feed, httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/tools?run_id="+r.ID, nil), "conversations/"+c.ID+"/tools")
	var body struct {
		Activities []ToolActivity `json:"activities"`
	}
	if feed.Code != http.StatusOK || json.Unmarshal(feed.Body.Bytes(), &body) != nil || len(body.Activities) != 3 {
		t.Fatalf("tool feed=%d %s", feed.Code, feed.Body.String())
	}
	for _, a := range body.Activities {
		if a.Status == "running" || a.Status == "queued" {
			t.Fatalf("stale executing feed: %+v", a)
		}
	}
}
