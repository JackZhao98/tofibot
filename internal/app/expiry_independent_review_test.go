package app

import (
	"context"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewExpirySettlementFailureRemainsRecoverableWithoutRestart(t *testing.T) {
	s, c, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER expiry_summary_transient BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'synthetic temporary storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, runs: map[string]context.CancelFunc{}}
	server.execute(c, r)
	if _, err := s.db.Exec(`DROP TRIGGER expiry_summary_transient`); err != nil {
		t.Fatal(err)
	}
	// The same worker wakes again after storage recovers. It must either find
	// safe conclusion work or observe the already persisted terminal summary.
	next, ok, err := s.nextQueuedRun(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status == "running" && !ok && !s.hasApprovalExpiry(r.ID) {
		t.Fatalf("storage recovered, but expiry remains running with no executable/settlement work: status=%s error=%s active=%d next=%+v", got.Status, got.Error, len(server.runs), next)
	}
}

type reviewExpiryEngine struct {
	calls        atomic.Int32
	observations atomic.Int32
	started      chan struct{}
	release      chan struct{}
}

func (e *reviewExpiryEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	e.calls.Add(1)
	if e.started != nil {
		close(e.started)
		select {
		case <-e.release:
		case <-ctx.Done():
			return runtime.Result{}, ctx.Err()
		}
	}
	for _, tool := range req.Tools {
		if tool.Name != "search_history" {
			continue
		}
		args := `{"query":"synthetic"}`
		for _, status := range []string{"queued", "running"} {
			if err := req.OnToolEvent(runtime.ToolEvent{CallID: "synthetic-recovery-observation", Name: tool.Name, Arguments: args, Status: status}); err != nil {
				return runtime.Result{}, err
			}
		}
		e.observations.Add(1)
		result, err := tool.Execute(ctx, []byte(args))
		if err != nil {
			return runtime.Result{}, err
		}
		if err := req.OnToolEvent(runtime.ToolEvent{CallID: "synthetic-recovery-observation", Name: tool.Name, Status: "completed", Result: result}); err != nil {
			return runtime.Result{}, err
		}
	}
	return runtime.Result{Content: "Synthetic model conclusion retained without repeating any operation."}, nil
}

func TestReviewExpiryWorkerRetriesOnlySummaryPersistence(t *testing.T) {
	s, c, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER expiry_summary_transient BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'synthetic temporary storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	engine := &reviewExpiryEngine{}
	server := &Server{store: s, engine: engine, runs: map[string]context.CancelFunc{}}
	server.execute(c, r)
	for range 2 {
		if _, err := s.db.Exec(`UPDATE approval_expiry_recoveries SET retry_after='' WHERE run_id=?`, r.ID); err != nil {
			t.Fatal(err)
		}
		next, ok, err := s.nextQueuedRun(c.ID)
		if err != nil || !ok || next.ID != r.ID {
			t.Fatalf("settlement queue=%+v ok=%v err=%v", next, ok, err)
		}
		server.execute(c, next)
	}
	var state, content, retry string
	if err := s.db.QueryRow(`SELECT state,summary_content,retry_after FROM approval_expiry_recoveries WHERE run_id=?`, r.ID).Scan(&state, &content, &retry); err != nil {
		t.Fatal(err)
	}
	if state != "claimed" || content == "" || retry == "" || engine.calls.Load() != 1 || engine.observations.Load() != 1 {
		t.Fatalf("state=%s content=%q retry=%s model=%d read=%d", state, content, retry, engine.calls.Load(), engine.observations.Load())
	}
	if _, ok, err := s.nextQueuedRun(c.ID); err != nil || ok {
		t.Fatalf("backoff ignored: queued=%v err=%v", ok, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER expiry_summary_transient`); err != nil {
		t.Fatal(err)
	}
	// The ordinary worker rechecks the durable claimed row. No restart or
	// explicit user retry is needed after the database becomes writable.
	server.startConversationWorker(c.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := s.GetRun(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.stopConversationWorkers()
	assertExpiryConcluded(t, s, r, q)
	if engine.calls.Load() != 1 || engine.observations.Load() != 1 {
		t.Fatalf("replayed recovery: model=%d read=%d", engine.calls.Load(), engine.observations.Load())
	}
	var summary string
	if err := s.db.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, content) {
		t.Fatal("lost staged conclusion")
	}
	// Duplicate completion stays a no-op once its summary and terminal state commit.
	for range 2 {
		server.execute(c, r)
	}
	assertExpiryConcluded(t, s, r, q)
}

func TestReviewExpiryStagedSummarySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, c, r, q := expiryStoreFixture(t, dir)
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER expiry_summary_transient BEFORE INSERT ON messages BEGIN SELECT RAISE(ABORT,'synthetic temporary storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	engine := &reviewExpiryEngine{}
	server := &Server{store: s, engine: engine, runs: map[string]context.CancelFunc{}}
	server.execute(c, r)
	if _, err := s.db.Exec(`DROP TRIGGER expiry_summary_transient`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertExpiryConcluded(t, s, r, q)
	if engine.calls.Load() != 1 || engine.observations.Load() != 1 {
		t.Fatal("startup replayed model/tools")
	}
	var summary string
	if err = s.db.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "Synthetic model conclusion retained") {
		t.Fatal(summary)
	}
}

func TestReviewExpiryWorkerDoesNotSettleAnActiveClaim(t *testing.T) {
	s, c, r, q := expiryStoreFixture(t, t.TempDir())
	defer s.Close()
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	engine := &reviewExpiryEngine{started: make(chan struct{}), release: make(chan struct{})}
	server := &Server{store: s, engine: engine, runs: map[string]context.CancelFunc{}}
	done := make(chan struct{})
	go func() { server.execute(c, r); close(done) }()
	<-engine.started
	defer server.stopConversationWorkers()
	server.startConversationWorker(c.ID)
	// Let the worker inspect the claimed row. It must respect the active
	// in-process owner instead of racing it to a fallback conclusion.
	time.Sleep(50 * time.Millisecond)
	got, err := s.GetRun(r.ID)
	if err != nil || got.Status != "running" {
		t.Fatalf("active claim=%+v err=%v", got, err)
	}
	var summaries int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant'`, r.ID).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 0 || engine.calls.Load() != 1 {
		t.Fatalf("premature summaries=%d model=%d", summaries, engine.calls.Load())
	}
	close(engine.release)
	<-done
	assertExpiryConcluded(t, s, r, q)
}

func TestReviewExpiryMigratesExistingRecoveryRowsWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	s, _, r, q := expiryStoreFixture(t, dir)
	if err := s.expireApproval(q.ID); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"summary_content", "retry_after"} {
		if _, err := s.db.Exec("ALTER TABLE approval_expiry_recoveries DROP COLUMN " + column); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{DataDir: dir, IsolatedWorkspace: true, AccountMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var state, content, retry string
	if err = server.store.db.QueryRow(`SELECT state,summary_content,retry_after FROM approval_expiry_recoveries WHERE run_id=?`, r.ID).Scan(&state, &content, &retry); err != nil {
		t.Fatal(err)
	}
	if state != "ready" || content != "" || retry != "" {
		t.Fatalf("maintenance executed migrated row: %q %q %q", state, content, retry)
	}
}
