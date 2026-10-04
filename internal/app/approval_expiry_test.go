package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func expiryStoreFixture(t *testing.T, dir string) (*Store, Conversation, Run, Question) {
	t.Helper()
	s, c, r := questionFixtureDir(t, dir)
	r.Model = "test"
	if _, err := s.db.Exec(`UPDATE runs SET model=? WHERE id=?`, r.Model, r.ID); err != nil {
		t.Fatal(err)
	}
	q, err := s.CreateQuestion(c.ID, r, askQuestionInput{Type: questionApproval, Question: "Synthetic approval", ExpiresInSeconds: 1800, Approval: &ApprovalDetails{Action: "write", Target: "synthetic", Impact: "test only"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		id, name string
		running  bool
	}{{"complete", "search_history", true}, {"waiting", "write", true}, {"tail", "send", false}} {
		if err = s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call.id, Name: call.name, Status: "queued", Arguments: "{}"}); err != nil {
			t.Fatal(err)
		}
		if call.running {
			if err = s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call.id, Name: call.name, Status: "running"}); err != nil {
				t.Fatal(err)
			}
		}
		if call.id == "complete" {
			if err = s.RecordToolEvent(c.ID, r.BotID, r.ID, runtime.ToolEvent{CallID: call.id, Name: call.name, Status: "completed", Result: "preserved synthetic result"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	cp := agent.Continuation{Version: 1, QuestionID: q.ID, WaitingToolCallID: "waiting", WaitingToolName: "write", Messages: []provider.Message{{Role: "user", Content: "Synthetic task"}, {Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "waiting", Name: "write", Arguments: "{}"}, {ID: "tail", Name: "send", Arguments: "{}"}}}}, SkippedToolCalls: []provider.ToolCall{{ID: "tail", Name: "send", Arguments: "{}"}}}
	raw, err := json.Marshal(map[string]any{"version": 1, "run_id": r.ID, "bot_id": r.BotID, "model": r.Model, "agent": cp})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveInputContinuation(context.Background(), r.ID, q.ID, raw); err != nil {
		t.Fatal(err)
	}
	return s, c, r, q
}

func assertExpiryConcluded(t *testing.T, s *Store, r Run, q Question) {
	t.Helper()
	got, err := s.GetRun(r.ID)
	if err != nil || got.Status != "failed" || got.Error != "approval_expired" {
		t.Fatalf("run=%+v %v", got, err)
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"stop_reason":"approval_expired"`) {
		t.Fatal(string(raw))
	}
	var state, checkpoint, message string
	if err = s.db.QueryRow(`SELECT state,checkpoint_json,summary_message_id FROM approval_expiry_recoveries WHERE run_id=? AND question_id=?`, r.ID, q.ID).Scan(&state, &checkpoint, &message); err != nil || state != "finished" || checkpoint == "" || message == "" {
		t.Fatalf("recovery %s %v", state, err)
	}
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant' AND kind<>'progress'`, r.ID).Scan(&count)
	if count != 1 {
		t.Fatalf("conclusions=%d", count)
	}
	if _, err = s.RenewExpiredApproval(q.ID); err == nil {
		t.Fatal("renewed expired flow")
	}
	if _, err = s.RetryRun(r.ID); err == nil {
		t.Fatal("replayed expired flow")
	}
	if _, _, err = s.AnswerQuestion(q.ID, "synthetic-human", true); !errors.Is(err, ErrQuestionNotPending) {
		t.Fatalf("late answer %v", err)
	}
}

func TestApprovalExpiryAtomicBatchAndConcurrentClaim(t *testing.T) {
	s, c, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	for range 2 {
		if err := s.expireApproval(q.ID); err != nil {
			t.Fatal(err)
		}
	}
	summaries, err := s.ToolActivitySummaries(c.ID, []string{r.ID})
	if err != nil || len(summaries) != 1 || summaries[0].ExpiredCount != 1 || summaries[0].SkippedCount != 1 || summaries[0].CompletedCount != 1 || summaries[0].FailedCount != 0 || summaries[0].PendingCount != 0 {
		t.Fatalf("batch summary=%+v %v", summaries, err)
	}
	var claims atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, ok, err := s.claimApprovalExpiry(context.Background(), r.ID)
			if err != nil {
				t.Error(err)
			}
			if ok {
				claims.Add(1)
			}
		}()
	}
	wg.Wait()
	if claims.Load() != 1 {
		t.Fatal(claims.Load())
	}
	for range 2 {
		if err = s.settleApprovalExpiry(r.ID, "Synthetic incomplete summary."); err != nil {
			t.Fatal(err)
		}
	}
	assertExpiryConcluded(t, s, r, q)
	activities, err := s.ToolActivities(c.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range activities {
		if a.CallID == "complete" && (a.Status != "completed" || a.Result != "preserved synthetic result") {
			t.Fatal("changed completed result")
		}
	}
}

func TestApprovalExpiryTransactionRollback(t *testing.T) {
	s, _, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	_, err := s.db.Exec(`CREATE TRIGGER fixture_expiry_reject BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'synthetic event failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.expireApproval(q.ID); err == nil {
		t.Fatal("partial expiry committed")
	}
	got, _ := s.GetQuestion(q.ID)
	run, _ := s.GetRun(r.ID)
	if got.Status != questionPending || run.Status != runWaiting {
		t.Fatal("expiry transaction partially committed")
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM approval_expiry_recoveries WHERE run_id=?`, r.ID).Scan(&n)
	if n != 0 {
		t.Fatal("orphan recovery")
	}
}

func TestApprovalExpiryRestartClaimedAndLegacyMaintenance(t *testing.T) {
	for _, kind := range []string{"ready", "claimed", "legacy", "claimed-wait", "corrupt", "mismatch"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, c, r, q := expiryStoreFixture(t, dir)
			if kind == "legacy" || kind == "claimed-wait" {
				_, _ = s.db.Exec(`UPDATE questions SET status='expired' WHERE id=?`, q.ID)
				if kind == "claimed-wait" {
					_, _ = s.db.Exec(`UPDATE run_input_waits SET state='claimed' WHERE run_id=?`, r.ID)
				}
			} else {
				if err := s.expireApproval(q.ID); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "claimed" {
				if _, _, ok, err := s.claimApprovalExpiry(context.Background(), r.ID); err != nil || !ok {
					t.Fatal(err)
				}
			}
			if kind == "corrupt" {
				_, _ = s.db.Exec(`UPDATE approval_expiry_recoveries SET checkpoint_json='{}' WHERE run_id=?`, r.ID)
			}
			if kind == "mismatch" {
				_, _ = s.db.Exec(`UPDATE questions SET bot_id='synthetic-mismatch' WHERE id=?`, q.ID)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			// Schema-only maintenance cannot expire, claim or conclude work.
			maintenance, err := NewServer(Config{DataDir: dir, AccountMaintenance: true, IsolatedWorkspace: true})
			if err != nil {
				t.Fatal(err)
			}
			var before int
			_ = maintenance.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&before)
			if before != 0 {
				t.Fatal("maintenance executed recovery")
			}
			maintenance.Close()
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			run, _ := s.GetRun(r.ID)
			if kind == "claimed" || kind == "claimed-wait" {
				if run.Status != "failed" {
					t.Fatalf("claimed restart replayable: %+v", run)
				}
			} else {
				server := &Server{store: s, runs: map[string]context.CancelFunc{}}
				server.execute(c, run)
			}
			assertExpiryConcluded(t, s, r, q)
		})
	}
}

func TestApprovalExpiryWithoutModelSettlesBeforeConfiguredGate(t *testing.T) {
	dir := t.TempDir()
	s, c, r, q := expiryStoreFixture(t, dir)
	ordinary, err := s.AddRun(c.ID, r.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE runs SET queue_seq=-1 WHERE id=?`, ordinary.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`UPDATE questions SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), q.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	server, err := NewServer(Config{DataDir: dir, IsolatedWorkspace: true})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	waitForRunStatus(t, server, r.ID, "failed")
	assertExpiryConcluded(t, server.store, r, q)
	queued, _ := server.store.GetRun(ordinary.ID)
	if queued.Status != "queued" {
		t.Fatal("unconfigured ordinary work executed")
	}
	conversations, err := server.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range conversations {
		if got.ID == c.ID && got.TaskState != nil && got.TaskState.RunID == r.ID && (got.TaskState.Status != "expired" || got.TaskState.CanRetry) {
			t.Fatalf("projection=%+v", got.TaskState)
		}
	}
}

func TestApprovalExpiryPreservesUncertainClaimAndTrueFailure(t *testing.T) {
	s, c, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	o := tooloutcome.New(tooloutcome.Uncertain, "synthetic_lost_effect", "unknown", "Verify synthetic effect", "verify_effect")
	for _, status := range []string{"queued", "running", "failed"} {
		ev := runtime.ToolEvent{CallID: "uncertain", Name: "other_write", Status: status}
		if status == "failed" {
			ev.Outcome = &o
			ev.Result = o.JSON()
		}
		if err := s.RecordToolEvent(c.ID, r.BotID, r.ID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO mcp_call_approvals(question_id,run_id,action_hash,claimed_at) VALUES(?,?,?,?)`, q.ID, r.ID, "synthetic", now()); err != nil {
		t.Fatal(err)
	}
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.settleApprovalExpiry(r.ID, ""); err != nil {
		t.Fatal(err)
	}
	var claim string
	_ = s.db.QueryRow(`SELECT claimed_at FROM mcp_call_approvals WHERE question_id=?`, q.ID).Scan(&claim)
	if claim == "" {
		t.Fatal("cleared effect claim")
	}
	activities, _ := s.ToolActivities(c.ID, 20)
	for _, a := range activities {
		if a.CallID == "uncertain" && (a.Outcome == nil || a.Outcome.Code != o.Code) {
			t.Fatal("lost original uncertainty")
		}
	}
	var conclusion string
	_ = s.db.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&conclusion)
	if !strings.Contains(conclusion, "核实结果") || strings.Contains(conclusion, "需要审批的操作未执行") {
		t.Fatal(conclusion)
	}
}
