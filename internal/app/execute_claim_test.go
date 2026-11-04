package app

import (
	"context"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// A worker can read a run as queued and launch it after another caller claimed it. That execute call never owns
// the run, so its cleanup must not interrupt the owner's tool calls (2026-11-04: the v0.6.3-rc.1 release CI saw
// TestGateRefusalsAreNotCountedAsFailedCalls lose a queued call this way).
func TestExecuteWithoutTheClaimLeavesTheOwnersToolCalls(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		t.Error("the engine ran for a run this call did not claim")
		return runtime.Result{Content: "plain"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.store.CreateBot("claim fixture", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(bot.DMConversationID)
	// AddUserRun alone starts no conversation worker, so nothing else can pick this run up.
	_, stale, _, err := s.store.AddUserRun(c.ID, bot.ID, "synthetic request", "claim-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.store.claimRun(context.Background(), stale.ID); err != nil || !ok {
		t.Fatal("the owner's claim:", ok, err)
	}
	for _, status := range []string{"queued", "running"} {
		if err := s.store.RecordToolEvent(c.ID, bot.ID, stale.ID, runtime.ToolEvent{CallID: "call-owned", Name: "say", Arguments: "{}", Status: status}); err != nil {
			t.Fatal(err)
		}
	}

	s.execute(c, stale) // stale still says queued

	var status string
	if err := s.store.db.QueryRow(`SELECT status FROM tool_activities WHERE run_id=? AND call_id='call-owned'`, stale.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("the owner's tool call is %q after an execute that did not claim the run, want running", status)
	}
	if run, err := s.store.GetRun(stale.ID); err != nil || run.Status != "running" {
		t.Fatalf("run = %+v %v, want still running", run, err)
	}
}
