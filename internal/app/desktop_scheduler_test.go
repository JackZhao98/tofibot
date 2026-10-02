package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/computer"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func waitDesktopQueue(t *testing.T, s *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.computerOwnerMu.Lock()
		got := len(s.desktopWaiters)
		s.computerOwnerMu.Unlock()
		if got == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("desktop queue did not reach expected size")
}
func TestSharedDesktopFIFOAndCancelledWaiter(t *testing.T) {
	s := &Server{}
	a := Run{BotID: "a", ID: "run-a"}
	if err := s.waitComputerOwner(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	bctx, bcancel := context.WithCancel(context.Background())
	defer bcancel()
	b := make(chan error, 1)
	go func() { b <- s.waitComputerOwner(bctx, Run{BotID: "b", ID: "run-b"}) }()
	waitDesktopQueue(t, s, 1)
	c := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { c <- s.waitComputerOwner(ctx, Run{BotID: "c", ID: "run-c"}) }()
	waitDesktopQueue(t, s, 2)
	if s.claimComputerOwner("human", "human-control:one") {
		t.Fatal("human stole active screen")
	}
	bcancel()
	if err := <-b; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	waitDesktopQueue(t, s, 1)
	s.releaseComputerOwner(a.BotID, a.ID)
	if err := <-c; err != nil {
		t.Fatal(err)
	}
	if !s.ownsComputer(Run{BotID: "c", ID: "run-c"}) {
		t.Fatal("next waiter not granted")
	}
	waitDesktopQueue(t, s, 0)
}
func TestSharedDesktopKeepsRunAcrossActionsAndParallelShell(t *testing.T) {
	s, _ := secretTestServer(t)
	a := Run{BotID: "a", ID: "a"}
	if _, err := s.microVMAction(context.Background(), a, "desktop.capture", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	lease := s.computerLease("other-bot")
	lease.Lock()
	defer lease.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	for _, action := range []string{"shell.exec", "files.read", "terminal.list"} {
		if _, err := s.microVMAction(ctx, Run{BotID: "b", ID: "b"}, action, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("non-graphical action blocked: %s %v", action, err)
		}
	}
	if !s.ownsComputer(a) {
		t.Fatal("parallel action changed owner")
	}
}
func TestSharedDesktopRequiresFreshObservationAfterHandoff(t *testing.T) {
	s, _ := secretTestServer(t)
	a := Run{BotID: "a", ID: "a"}
	if _, err := s.microVMAction(context.Background(), a, "desktop.click", json.RawMessage(`{"x":1,"y":1}`)); err == nil || !strings.Contains(err.Error(), "needs_observation") {
		t.Fatal("new owner accepted stale coordinates")
	}
	if _, err := s.microVMAction(context.Background(), a, "desktop.capture", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.microVMAction(context.Background(), a, "desktop.click", json.RawMessage(`{"x":1,"y":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.yieldComputerOwner(a); err != nil {
		t.Fatal(err)
	}
	if s.ownsComputer(a) {
		t.Fatal("handoff retained owner")
	}
	if _, err := s.microVMAction(context.Background(), a, "desktop.type", json.RawMessage(`{"text":"hello"}`)); err == nil || !strings.Contains(err.Error(), "needs_observation") {
		t.Fatal("reacquired owner reused stale observation")
	}
}
func TestSharedDesktopOwnershipAPIShowsRealBotAndWaiter(t *testing.T) {
	s := &Server{}
	s.claimComputerOwner("bot-a", "run-a")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.waitComputerOwner(ctx, Run{BotID: "bot-b", ID: "run-b"}) }()
	waitDesktopQueue(t, s, 1)
	w := httptest.NewRecorder()
	if !s.handleDesktopOwnership(w, httptest.NewRequest("GET", "/api/computer/desktop-ownership", nil)) {
		t.Fatal("missing route")
	}
	for _, value := range []string{"bot-a", "run-a", "bot-b", "run-b", "shared-desktop"} {
		if !strings.Contains(w.Body.String(), value) {
			t.Fatal("owner response missing identity")
		}
	}
	cancel()
	<-done
}

func TestSharedDesktopHumanReleaseWakesAnotherBot(t *testing.T) {
	s, _ := secretTestServer(t)
	c := &computerControl{id: "fixture", botID: "a"}
	s.computerControls = map[string]*computerControl{"a": c}
	if !s.claimComputerOwner("a", "human-control:fixture") {
		t.Fatal("claim")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ready := make(chan error, 1)
	go func() { ready <- s.waitComputerOwner(ctx, Run{BotID: "b", ID: "b-run"}) }()
	waitDesktopQueue(t, s, 1)
	c.mu.Lock()
	s.releaseControl(c)
	c.mu.Unlock()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	if !s.ownsComputer(Run{BotID: "b", ID: "b-run"}) {
		t.Fatal("human release did not transfer shared screen")
	}
}
func TestSharedDesktopFailedHandoffReleaseKeepsOwner(t *testing.T) {
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-shared-desktop.sock", Client: &http.Client{Transport: secretTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":false,"error":"synthetic release failure"}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client}
	r := Run{BotID: "a", ID: "parent"}
	s.claimComputerOwner(r.BotID, r.ID)
	if err = s.yieldComputerOwner(r); err == nil {
		t.Fatal("handoff release failure swallowed")
	}
	if !s.ownsComputer(r) {
		t.Fatal("unconfirmed release allowed another owner")
	}
}

func TestHumanCanControlWhileBotRunsNonGraphicalWork(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: &inputControlTransport{}}})
	bot, err := s.store.CreateBot("shell bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetRunStatus(run.ID, "running", "")
	w := httptest.NewRecorder()
	s.handleComputerControl(w, httptest.NewRequest("POST", "/api/computers/firecracker/actions", nil), bot.ID, "desktop.control.acquire", json.RawMessage(`{}`))
	if w.Code != 200 {
		t.Fatalf("shell run blocked human: %s", w.Body.String())
	}
	var response struct {
		Result struct {
			ID string `json:"control_id"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &response)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.microVMAction(ctx, run, "desktop.capture", json.RawMessage(`{}`)); done <- err }()
	waitDesktopQueue(t, s, 1)
	w = httptest.NewRecorder()
	args, _ := json.Marshal(map[string]string{"control_id": response.Result.ID})
	s.handleComputerControl(w, httptest.NewRequest("POST", "/api/computers/firecracker/actions", nil), bot.ID, "desktop.control.release", args)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.releaseComputerOwner(run.BotID, run.ID)
}

func TestDesktopPresenceResolvesConversationAndConfirmedClick(t *testing.T) {
	store, bot, conversation := scheduleTestStore(t)
	defer store.Close()
	_, run, _, err := store.AddUserRun(conversation.ID, bot.ID, "inspect screen", "desktop-presence")
	if err != nil {
		t.Fatal(err)
	}
	s, _ := secretTestServer(t)
	s.store = store
	if _, err = s.microVMAction(context.Background(), run, "desktop.capture", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.microVMAction(context.Background(), run, "desktop.click", json.RawMessage(`{"x":448,"y":280,"screenshot_width":896,"screenshot_height":560}`)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleDesktopOwnership(w, httptest.NewRequest("GET", "/api/computer/desktop-ownership", nil))
	var response struct {
		Owner   map[string]string `json:"owner"`
		Pointer *desktopPointer   `json:"pointer"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Owner["conversation_id"] != conversation.ID || response.Owner["bot_id"] != bot.ID || response.Owner["run_id"] != run.ID {
		t.Fatalf("wrong owner: %#v", response.Owner)
	}
	p := response.Pointer
	if p == nil || p.X != 448 || p.Y != 280 || p.Width != 896 || p.Height != 560 || p.Action != "click" || p.BotID != bot.ID || p.RunID != run.ID || p.UpdatedAt == "" {
		t.Fatalf("wrong confirmed pointer: %#v", p)
	}
	if _, err = s.microVMAction(context.Background(), run, "desktop.scroll", json.RawMessage(`{"direction":"down"}`)); err != nil {
		t.Fatal(err)
	}
	if s.desktopPointer != nil {
		t.Fatal("scroll retained stale click marker")
	}
	if _, err = s.microVMAction(context.Background(), run, "desktop.click", json.RawMessage(`{"x":100,"y":200}`)); err != nil {
		t.Fatal(err)
	}
	if s.desktopPointer != nil {
		t.Fatal("dimensionless click guessed guest capture size")
	}
	if _, err = s.microVMAction(context.Background(), run, "desktop.click", json.RawMessage(`{"x":100,"y":200,"screenshot_width":1280,"screenshot_height":800}`)); err != nil {
		t.Fatal(err)
	}
	s.releaseComputerOwner(bot.ID, run.ID)
	if s.desktopPointer != nil {
		t.Fatal("released run retained its pointer")
	}
	if !s.claimComputerOwner(bot.ID, "human-control:presence") {
		t.Fatal("human claim failed")
	}
	w = httptest.NewRecorder()
	s.handleDesktopOwnership(w, httptest.NewRequest("GET", "/api/computer/desktop-ownership", nil))
	response.Pointer = nil
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Pointer != nil || response.Owner["kind"] != "human" {
		t.Fatal("human ownership exposed Bot pointer")
	}
}

func TestDesktopPresenceRejectsFailedAndUnownedClicks(t *testing.T) {
	client, err := computer.New(computer.Config{Socket: "/tmp/synthetic-click-failure.sock", Client: &http.Client{Transport: secretTransport(func(req *http.Request) (*http.Response, error) {
		var action computer.Action
		_ = json.NewDecoder(req.Body).Decode(&action)
		body := `{"ok":true,"result":{}}`
		if action.Name == "desktop.click" {
			body = `{"ok":false,"error":"fixture click failed"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client}
	run := Run{ID: "run-a", BotID: "bot-a"}
	if _, err = s.microVMAction(context.Background(), run, "desktop.capture", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.microVMAction(context.Background(), run, "desktop.click", json.RawMessage(`{"x":100,"y":100,"screenshot_width":1280,"screenshot_height":800}`)); err == nil {
		t.Fatal("failed click succeeded")
	}
	if s.desktopPointer != nil {
		t.Fatal("failed click produced marker")
	}
	s.recordDesktopAction(Run{ID: "another-run", BotID: run.BotID}, "desktop.click", json.RawMessage(`{"x":100,"y":100,"screenshot_width":1280,"screenshot_height":800}`))
	if s.desktopPointer != nil {
		t.Fatal("unowned click produced marker")
	}
	for _, args := range []string{`{}`, `{"x":-1,"y":0}`, `{"x":1280,"y":0}`, `{"x":1,"y":1,"screenshot_width":896}`} {
		s.recordDesktopAction(run, "desktop.click", json.RawMessage(args))
		if s.desktopPointer != nil {
			t.Fatalf("invalid marker args: %s", args)
		}
	}
}
