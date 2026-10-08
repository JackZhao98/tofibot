package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestStuckRunSweeperConcludesOnlyRunsPastTheToolDeadline(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b, err := st.CreateBot("stuck", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	runningTool := func(name string, age time.Duration) Run {
		t.Helper()
		r, err := st.AddRun(c.ID, b.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := st.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
			t.Fatalf("set running: %v %v", ok, err)
		}
		for _, status := range []string{"queued", "running"} {
			if err := st.RecordToolEvent(c.ID, b.ID, r.ID, runtime.ToolEvent{CallID: "call-" + r.ID, Name: name, Status: status}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.db.Exec(`UPDATE tool_activities SET updated_at=? WHERE run_id=?`, at.Add(-age).UTC().Format(time.RFC3339Nano), r.ID); err != nil {
			t.Fatal(err)
		}
		return r
	}
	stuck := runningTool("computer_browser", 5*time.Hour)
	recent := runningTool("computer_shell", time.Minute)
	asking := runningTool("ask_user_question", 2*time.Hour)
	if _, err := st.db.Exec(`INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,status,created_at,updated_at) VALUES('q-pending',?,?,?,'choice','?','pending',?,?)`, asking.ID, c.ID, b.ID, now(), now()); err != nil {
		t.Fatal(err)
	}
	queued := runningTool("computer_desktop", time.Hour)
	waitedThenRan := runningTool("computer_desktop", time.Hour)

	cancelled := false
	s := &Server{store: st, runs: map[string]context.CancelFunc{stuck.ID: func() { cancelled = true }}, computerOwners: map[string]string{}, computerLeases: map[string]*sync.Mutex{}}
	s.desktopWaiters = []*desktopWaiter{{BotID: b.ID, RunID: queued.ID}}
	s.desktopQueueLeft = map[string]time.Time{waitedThenRan.ID: at.Add(-time.Minute)}

	concluded := s.reconcileStuckRuns(context.Background(), at)
	if len(concluded) != 1 || concluded[0] != stuck.ID {
		t.Fatalf("concluded = %v, want only %s", concluded, stuck.ID)
	}
	if !cancelled {
		t.Fatal("live executor of the stuck run was not cancelled")
	}
	got, err := st.GetRun(stuck.ID)
	if err != nil || got.Status != "failed" || !strings.HasPrefix(got.Error, stuckRunCode+":") || !strings.Contains(got.Error, "computer_browser") {
		t.Fatalf("stuck run = %+v, %v", got, err)
	}
	if f := got.failure(); f == nil || f.Code != stuckRunCode {
		t.Fatalf("failure = %+v", f)
	}
	activities, err := st.ToolActivities(c.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range activities {
		if a.RunID == stuck.ID && a.Status != "interrupted" {
			t.Fatalf("stuck tool step still %s", a.Status)
		}
		if a.RunID != stuck.ID && a.Status != "running" {
			t.Fatalf("unrelated tool step %s changed to %s", a.Name, a.Status)
		}
	}
	for _, r := range []Run{recent, asking, queued, waitedThenRan} {
		if got, _ := st.GetRun(r.ID); got.Status != "running" {
			t.Fatalf("run %s concluded: %+v", r.ID, got)
		}
	}
	// A second sweep is idempotent.
	if again := s.reconcileStuckRuns(context.Background(), at); len(again) != 0 {
		t.Fatalf("second sweep concluded %v", again)
	}
}
