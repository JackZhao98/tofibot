package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type cleanupTransport struct{ *inputControlTransport }

func (f cleanupTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method == http.MethodGet {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"state":"ready"}`))}, nil
	}
	return f.inputControlTransport.RoundTrip(r)
}

func cleanupFixture(st *Store, fake *inputControlTransport) *Server {
	client, _ := computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: cleanupTransport{fake}}})
	return &Server{store: st, microVM: client, runs: map[string]context.CancelFunc{}}
}

func cleanupCount(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.db.QueryRow(`SELECT count(*) FROM run_terminal_cleanup`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTerminalCleanupPreservesWaitAndCleansStoppedParkedRun(t *testing.T) {
	for _, steer := range []bool{false, true} {
		t.Run(fmt.Sprint(steer), func(t *testing.T) {
			st, c, r, _ := parkedInputFixture(t)
			defer st.Close()
			// Represents the persisted ownership intent from before the input wait.
			_, err := st.db.Exec(`INSERT INTO run_terminal_cleanup VALUES(?,?,?,?)`, r.ID, r.BotID, "/test.sock", now())
			if err != nil {
				t.Fatal(err)
			}
			fake := &inputControlTransport{}
			s := cleanupFixture(st, fake)
			for i := 0; i < 2; i++ {
				if err = recoverInterruptedRuns(st.db); err != nil {
					t.Fatal(err)
				}
				s.reconcileTerminalCleanup(context.Background())
			}
			if len(fake.actions) != 0 || cleanupCount(t, st) != 1 {
				t.Fatal("parked/recovered wait cancelled its terminal")
			}
			if steer {
				_, _, _, err = st.AddUserRuns(c.ID, "New instruction", "steer", []runSpec{{BotID: r.BotID, Model: r.Model}})
				s.cancelInactiveRuns()
			} else {
				err = st.CancelRunTree(r.ID)
				s.cancelInactiveRuns()
			}
			if err != nil {
				t.Fatal(err)
			}
			// No active worker is needed to discover the cleanup obligation.
			s.reconcileTerminalCleanup(context.Background())
			s.reconcileTerminalCleanup(context.Background())
			if len(fake.actions) != 1 || cleanupCount(t, st) != 0 {
				t.Fatalf("actions=%+v pending=%d", fake.actions, cleanupCount(t, st))
			}
			a := fake.actions[0]
			if a.Name != "terminal.cancel_run" || a.BotID != r.BotID || a.RunID != r.ID {
				t.Fatal(a)
			}
		})
	}
}

func TestTerminalCleanupRegistrationAndTerminalStates(t *testing.T) {
	for _, status := range []string{"running", "queued", "done", "failed", "cancelled", "interrupted", "deleted"} {
		t.Run(status, func(t *testing.T) {
			st, _, r := questionFixture(t)
			defer st.Close()
			ctx := context.Background()
			if err := st.registerRunTerminals(ctx, r, "/test.sock"); err != nil {
				t.Fatal(err)
			}
			if err := st.registerRunTerminals(ctx, r, "/test.sock"); err != nil || cleanupCount(t, st) != 1 {
				t.Fatal("registration not idempotent", err)
			}
			if err := st.registerRunTerminals(ctx, r, "/other.sock"); err == nil {
				t.Fatal("silently moved ownership to another VM")
			}
			wrongBot := r
			wrongBot.BotID = "other-bot"
			if err := st.registerRunTerminals(ctx, wrongBot, "/test.sock"); err == nil {
				t.Fatal("accepted another Bot")
			}
			var err error
			if status == "deleted" {
				_, err = st.db.Exec(`DELETE FROM runs WHERE id=?`, r.ID)
			} else {
				_, err = st.db.Exec(`UPDATE runs SET status=? WHERE id=?`, status, r.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if status != "running" && st.registerRunTerminals(ctx, r, "/test.sock") == nil {
				t.Fatal("accepted mutation for inactive run")
			}
			fake := &inputControlTransport{}
			s := cleanupFixture(st, fake)
			s.reconcileTerminalCleanup(ctx)
			keep := status == "running" || status == "queued"
			wantCalls, wantRows := 1, 0
			if keep {
				wantCalls, wantRows = 0, 1
			}
			if status == "done" {
				wantCalls = 0
			}
			if len(fake.actions) != wantCalls || cleanupCount(t, st) != wantRows {
				t.Fatalf("actions=%+v pending=%d", fake.actions, cleanupCount(t, st))
			}
		})
	}
}

func TestTerminalCleanupRetriesAndWaitsForWorker(t *testing.T) {
	st, _, r := questionFixture(t)
	defer st.Close()
	ctx := context.Background()
	if err := st.registerRunTerminals(ctx, r, "/test.sock"); err != nil {
		t.Fatal(err)
	}
	if err := recoverInterruptedRuns(st.db); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, runs: map[string]context.CancelFunc{r.ID: func() {}}}
	// Unavailable guest: ownership must survive both failure and process restart.
	s.microVM, _ = computer.New(computer.Config{Socket: "/nonexistent-tofi-cleanup.sock"})
	s.reconcileTerminalCleanup(ctx)
	if cleanupCount(t, st) != 1 {
		t.Fatal("socket mismatch lost intent")
	}
	fake := &inputControlTransport{}
	s.microVM = cleanupFixture(st, fake).microVM
	s.reconcileTerminalCleanup(ctx)
	if len(fake.actions) != 0 {
		t.Fatal("cancelled before worker unwound")
	}
	delete(s.runs, r.ID)
	// Same configured identity, unavailable transport.
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock"})
	s.reconcileTerminalCleanup(ctx)
	if cleanupCount(t, st) != 1 {
		t.Fatal("lost unacknowledged cleanup")
	}
	s.microVM = cleanupFixture(st, fake).microVM
	s.reconcileTerminalCleanup(ctx)
	if cleanupCount(t, st) != 0 || len(fake.actions) != 1 {
		t.Fatal("retry failed")
	}
}

func TestExecuteTerminalSurvivesInputSuspension(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	fake := &inputControlTransport{}
	s.microVM = cleanupFixture(s.store, fake).microVM
	s.mu.Lock()
	s.engine = scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
		for _, tool := range req.Tools {
			if tool.Name == "computer_terminal" {
				if _, err := tool.Execute(ctx, json.RawMessage(`{"action":"open","command":"sleep 60"}`)); err != nil {
					return Result{}, err
				}
			}
		}
		r, err := s.store.GetRun(req.RunID)
		if err != nil {
			return Result{}, err
		}
		q, err := s.store.CreateQuestion(r.ConversationID, r, askQuestionInput{Question: "Continue?", Type: questionText})
		if err != nil {
			return Result{}, err
		}
		if err = req.OnSuspend(q.ID, json.RawMessage(`{"version":1,"synthetic":true}`)); err != nil {
			return Result{}, err
		}
		return runtime.Result{Suspended: true}, nil
	})
	s.mu.Unlock()
	b, err := s.store.CreateBot("terminal wait", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	r, err := s.store.AddRun(c.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	waitForRunStatus(t, s, r.ID, runWaiting)
	s.reconcileTerminalCleanup(context.Background())
	opened := 0
	for _, a := range fake.actions {
		if a.Name == "terminal.cancel_run" {
			t.Fatal("execute killed parked terminal")
		}
		if a.Name == "terminal.open" {
			opened++
		}
	}
	if opened != 1 || cleanupCount(t, s.store) != 1 {
		t.Fatal("terminal ownership not recorded")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewServer(Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	s.microVM = cleanupFixture(s.store, fake).microVM
	s.startTerminalCleanup()
	waitForRunStatus(t, s, r.ID, runWaiting)
	if err = s.store.CancelRunTree(r.ID); err != nil {
		t.Fatal(err)
	}
	s.cancelInactiveRuns()
	deadline := time.Now().Add(2 * time.Second)
	for cleanupCount(t, s.store) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if cleanupCount(t, s.store) != 0 {
		t.Fatal("restart/Stop did not wake durable cleanup")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	cancelled := 0
	for _, a := range fake.actions {
		if a.Name == "terminal.cancel_run" {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Fatalf("cancel calls=%d", cancelled)
	}
}
