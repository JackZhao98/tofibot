package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestDesktopHumanHandoffPreservesRunAndWaitsForOperation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fake := &inputControlTransport{}
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: fake}})
	bot, _ := s.store.CreateBot("cat", "", "model")
	run, _ := s.store.AddRun(bot.DMConversationID, bot.ID, "")
	s.store.SetRunStatus(run.ID, "running", "")
	s.claimComputerOwner(bot.ID, run.ID)
	s.markDesktopObserved(run)
	call := func(ctx context.Context, action string, args any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(args)
		w := httptest.NewRecorder()
		s.handleComputerControl(w, httptest.NewRequest("POST", "/api/computers/firecracker/actions", nil).WithContext(ctx), bot.ID, action, raw)
		return w
	}
	// A stale owner or another human must never be displaced.
	if w := call(context.Background(), "desktop.control.acquire", map[string]any{"expected_owner": "obsolete"}); w.Code != 409 {
		t.Fatal(w.Body.String())
	}
	lease := s.computerLease(bot.ID)
	lease.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	w := call(ctx, "desktop.control.acquire", map[string]any{"expected_owner": run.ID})
	cancel()
	if w.Code != 409 || !s.ownsComputer(run) {
		t.Fatal("in-flight tool was displaced")
	}
	waited := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		waited <- call(context.Background(), "desktop.control.acquire", map[string]any{"expected_owner": run.ID})
	}()
	select {
	case <-waited:
		t.Fatal("human did not wait for in-flight operation")
	case <-time.After(20 * time.Millisecond):
	}
	fake.mu.Lock()
	before := len(fake.actions)
	fake.mu.Unlock()
	if before != 0 {
		t.Fatal("guest release preceded operation completion")
	}
	lease.Unlock()
	w = <-waited
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	stored, _ := s.store.GetRun(run.ID)
	if stored.Status != "running" {
		t.Fatal("handoff cancelled durable run")
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls[bot.ID]
	s.computerOwnerMu.Unlock()
	if c == nil || s.ownsComputer(run) || s.desktopObservedFor(run) {
		t.Fatal("ownership/observation not transferred")
	}
	if w := call(context.Background(), "desktop.control.acquire", map[string]any{"expected_owner": "human-control:" + c.id}); w.Code != 409 {
		t.Fatal("another tab displaced human")
	}
	done := make(chan error, 1)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, err := s.microVMAction(ctx, run, "desktop.capture", json.RawMessage(`{}`)); done <- err }()
	waitDesktopQueue(t, s, 1)
	c.mu.Lock()
	c.lastActivity = time.Now().Add(-20 * time.Second)
	c.mu.Unlock()
	// Composition/local pending input barrier preserves human ownership.
	w = call(context.Background(), "desktop.control.renew", map[string]any{"control_id": c.id, "interacting": true})
	if w.Code != 200 || c.released {
		t.Fatal("composition barrier lost lease")
	}
	// Backend independently protects held modifiers and pointer drags.
	for _, events := range []string{`[{"type":"keydown","key":"Shift_L"}]`, `[{"type":"down","button":1}]`} {
		c.mu.Lock()
		s.recordHumanDesktopInput(c, computerControlArgs{Events: json.RawMessage(events)})
		c.lastActivity = time.Now().Add(-20 * time.Second)
		c.mu.Unlock()
		w = call(context.Background(), "desktop.control.renew", map[string]any{"control_id": c.id})
		if w.Code != 200 || c.released {
			t.Fatal("held input lost lease")
		}
		c.mu.Lock()
		s.recordHumanDesktopInput(c, computerControlArgs{Events: json.RawMessage(`[{"type":"reset"}]`)})
		c.lastActivity = time.Now().Add(-20 * time.Second)
		c.mu.Unlock()
	}
	w = call(context.Background(), "desktop.control.renew", map[string]any{"control_id": c.id})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"reason":"bot_waiting"`) {
		t.Fatal(w.Body.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !s.ownsComputer(run) {
		t.Fatal("waiting Bot did not acquire")
	}
	if w := call(context.Background(), "desktop.control.input", map[string]any{"control_id": c.id, "seq": 9, "events": []any{}}); w.Code != 409 {
		t.Fatal("late human input accepted")
	}
	s.releaseComputerOwner(bot.ID, run.ID)
}

func TestDesktopHumanIdleRequiresRealWaiterAndIgnoresHover(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: &inputControlTransport{}}})
	bot, _ := s.store.CreateBot("human", "", "model")
	w := httptest.NewRecorder()
	s.handleComputerControl(w, httptest.NewRequest("POST", "/api/computers/firecracker/actions", nil), bot.ID, "desktop.control.acquire", json.RawMessage(`{}`))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls[bot.ID]
	s.computerOwnerMu.Unlock()
	c.mu.Lock()
	old := time.Now().Add(-time.Minute)
	c.lastActivity = old
	s.recordHumanDesktopInput(c, computerControlArgs{Events: json.RawMessage(`[{"type":"move"}]`)})
	if !c.lastActivity.Equal(old) {
		t.Fatal("hover counted as activity")
	}
	c.mu.Unlock()
	raw, _ := json.Marshal(map[string]string{"control_id": c.id})
	w = httptest.NewRecorder()
	s.handleComputerControl(w, httptest.NewRequest("POST", "/api/computers/firecracker/actions", nil), bot.ID, "desktop.control.renew", raw)
	if w.Code != 200 || c.released {
		t.Fatal("idle without Bot waiter released human")
	}
	c.mu.Lock()
	s.recordHumanDesktopInput(c, computerControlArgs{Events: json.RawMessage(`[{"type":"text"}]`)})
	if time.Since(c.lastActivity) > time.Second {
		t.Fatal("text did not record activity")
	}
	c.mu.Unlock()
}
