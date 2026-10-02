package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type blockingStreamEngine struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

type lateFinalAfterStreamFailureEngine struct{}

func (lateFinalAfterStreamFailureEngine) Run(_ context.Context, req runtime.Request) (runtime.Result, error) {
	if req.OnDelta != nil {
		req.OnDelta("cannot persist")
	}
	return runtime.Result{Content: "late final"}, nil
}

func (e *blockingStreamEngine) Run(ctx context.Context, req runtime.Request) (runtime.Result, error) {
	if req.OnDelta != nil {
		req.OnDelta("partial")
	}
	e.once.Do(func() { close(e.started) })
	select {
	case <-e.release:
		return runtime.Result{Content: "complete"}, nil
	case <-ctx.Done():
		return runtime.Result{}, ctx.Err()
	}
}

func waitForRunStatus(t *testing.T, s *Server, id, want string) Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err := s.store.GetRun(id)
		if err == nil && r.Status == want {
			return r
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := s.store.GetRun(id)
	t.Fatalf("run %s status=%s, want %s", id, r.Status, want)
	return Run{}
}

func TestExecutePublishesDraftBeforeFinalAndReusesMessageID(t *testing.T) {
	e := &blockingStreamEngine{started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("stream", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "stream-test")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, r)
	<-e.started
	d, err := s.store.StreamDraft(r.ID)
	if err != nil || d.Content != "partial" || d.MessageID == "" {
		t.Fatalf("draft=%+v err=%v", d, err)
	}
	events, _ := s.store.Events(c.ID, 0)
	found := false
	for _, event := range events {
		if event["type"] == "delta" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("draft delta was not durable before engine release")
	}
	close(e.release)
	waitForRunStatus(t, s, r.ID, "done")
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	if messages[1].ID != d.MessageID || messages[1].Content != "complete" {
		t.Fatalf("final=%+v draft=%+v", messages[1], d)
	}
}

func TestExecuteCancellationDoesNotPublishLateDraftMessage(t *testing.T) {
	e := &blockingStreamEngine{started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("stream", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "cancel-stream-test")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, r)
	<-e.started
	if err := s.store.CancelRunTree(r.ID); err != nil {
		t.Fatal(err)
	}
	s.cancelInactiveRuns()
	close(e.release)
	waitForRunStatus(t, s, r.ID, "cancelled")
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 1 {
		t.Fatalf("late assistant message=%+v err=%v", messages, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		d, err := s.store.StreamDraft(r.ID)
		if err == nil && d.Status == streamDraftCancelled {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	d, err := s.store.StreamDraft(r.ID)
	t.Fatalf("draft=%+v err=%v", d, err)
}

func TestExecuteStreamPersistenceFailureIsFailedAndDropsLateFinal(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: lateFinalAfterStreamFailureEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("stream", "", "model")
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "stream-storage-failure")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`CREATE TRIGGER fail_delta_event BEFORE INSERT ON events WHEN NEW.type='delta' BEGIN SELECT RAISE(ABORT,'delta event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	got, err := s.store.GetRun(r.ID)
	if err != nil || got.Status != "failed" || got.Error == "" {
		t.Fatalf("run=%+v err=%v", got, err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 1 {
		t.Fatalf("late final persisted: messages=%+v err=%v", messages, err)
	}
	draft, err := s.store.StreamDraft(r.ID)
	if err != nil || draft.Status != streamDraftCancelled {
		t.Fatalf("draft=%+v err=%v", draft, err)
	}
}

func TestExecuteClaimsQueuedRunOnlyOnce(t *testing.T) {
	e := &blockingStreamEngine{started: make(chan struct{}), release: make(chan struct{})}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("once", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "claim-once")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.execute(c, r); close(done) }()
	<-e.started
	// A second dispatcher must neither start a second engine nor overwrite the
	// cancellation handle belonging to the first execution.
	duplicateDone := make(chan struct{})
	go func() { s.execute(c, r); close(duplicateDone) }()
	select {
	case <-duplicateDone:
	case <-time.After(time.Second):
		t.Fatal("duplicate execution entered the engine")
	}
	s.mu.Lock()
	cancel := s.runs[r.ID]
	s.mu.Unlock()
	if cancel == nil {
		t.Fatal("active cancellation handle lost")
	}
	close(e.release)
	<-done
	rows, _, err := s.store.Messages(c.ID, 0, 50)
	if err != nil || len(rows) != 2 {
		t.Fatalf("final message count=%d err=%v", len(rows), err)
	}
}
