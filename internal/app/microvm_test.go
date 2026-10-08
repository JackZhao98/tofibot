package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

type cancelProbeEngine struct{ calls atomic.Int32 }

func TestComputerToolGuidanceReusesSuccessfulObservations(t *testing.T) {
	s := &Server{microVM: &computer.Client{}}
	found := false
	for _, tool := range s.microVMTools(Run{}) {
		if tool.Name != "computer_desktop" {
			continue
		}
		found = true
		for _, phrase := range []string{"browser.snapshot already includes", "without an extra desktop.start or desktop.capture", "inspect after acquiring"} {
			if !strings.Contains(tool.Description, phrase) {
				t.Fatalf("desktop guidance lacks %q", phrase)
			}
		}
		if strings.Contains(tool.Description, "Use desktop.capture to inspect the current screen before clicking") {
			t.Fatal("desktop schema still forces redundant observation")
		}
	}
	if !found {
		t.Fatal("desktop tool missing")
	}
	for _, tool := range s.secretTools(Run{}) {
		if tool.Name == "use_secret_input" && (!strings.Contains(tool.Description, "does not locate or focus") || !strings.Contains(tool.Description, "wrong focus is rejected")) {
			t.Fatal("private input guidance does not describe focus requirements")
		}
	}
}

func (e *cancelProbeEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	e.calls.Add(1)
	return runtime.Result{Content: "unexpected"}, nil
}

func TestComputerDriverOwnershipIsSharedAndRunScoped(t *testing.T) {
	s := &Server{computerOwners: map[string]string{}}
	if !s.claimComputerOwner("bot-a", "run-a") {
		t.Fatal("first owner was rejected")
	}
	if s.claimComputerOwner("bot-a", "run-b") {
		t.Fatal("second run took the same Bot driver")
	}
	if s.claimComputerOwner("bot-b", "run-b") {
		t.Fatal("different Bot stole the shared display")
	}
	s.releaseComputerOwner("bot-a", "run-b")
	if s.computerOwners["bot-a"] != "run-a" {
		t.Fatal("wrong run released the owner")
	}
	s.releaseComputerOwner("bot-a", "run-a")
	if s.computerOwners["bot-a"] != "" {
		t.Fatal("owner was not released")
	}
}

func TestReadOnlyComputerActionsAndInvalidDatabaseFailClosed(t *testing.T) {
	if !isReadOnlyMicroVMAction("desktop.capture") || !isReadOnlyMicroVMAction("files.read") {
		t.Fatal("read-only actions not recognized")
	}
	if isReadOnlyMicroVMAction("desktop.click") || isReadOnlyMicroVMAction("shell.exec") {
		t.Fatal("mutating actions marked read-only")
	}
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.store.db.Close()
	if !s.botHasActiveRun("missing") {
		t.Fatal("database errors must fail closed")
	}
}

func TestQueuedRunCancelledWhileHumanComputerLeaseIsHeld(t *testing.T) {
	engine := &cancelProbeEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("lease bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "go", "lease-cancel")
	if err != nil {
		t.Fatal(err)
	}
	lease := s.terminalLease(b.ID)
	lease.Lock()
	s.enqueue(c, run)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, registered := s.runs[run.ID]
		s.mu.Unlock()
		if registered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.store.CancelRunTree(run.ID); err != nil {
		lease.Unlock()
		t.Fatal(err)
	}
	s.cancelInactiveRuns()
	lease.Unlock()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, getErr := s.store.GetRun(run.ID)
		if getErr == nil && current.Status == "cancelled" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	current, err := s.store.GetRun(run.ID)
	if err != nil || current.Status != "cancelled" {
		t.Fatalf("run=%+v err=%v", current, err)
	}
	if got := engine.calls.Load(); got != 0 {
		t.Fatalf("engine invoked after cancellation: %d", got)
	}
}

func TestShutdownCancelsRunWaitingForComputerLease(t *testing.T) {
	engine := &cancelProbeEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("shutdown bot", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "wait", "shutdown-lease")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	lease := s.terminalLease(b.ID)
	lease.Lock()
	s.enqueue(c, run)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		_, registered := s.runs[run.ID]
		s.mu.Unlock()
		if registered {
			break
		}
		time.Sleep(time.Millisecond)
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			lease.Unlock()
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		lease.Unlock()
		t.Fatal("shutdown remained blocked on a queued computer lease")
	}
	lease.Unlock()
	if got := engine.calls.Load(); got != 0 {
		t.Fatalf("engine invoked after shutdown: %d", got)
	}
}

func newLeaseMicroVMServer(t *testing.T) (*Server, chan struct{}) {
	t.Helper()
	socketDir, err := os.MkdirTemp("", "tfb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := socketDir + "/control.sock"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	actionCalled := make(chan struct{})
	var actionOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/action", func(w http.ResponseWriter, _ *http.Request) {
		actionOnce.Do(func() { close(actionCalled) })
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"lease":"released"}}`))
	})
	httpServer := &http.Server{Handler: mux}
	go func() { _ = httpServer.Serve(listener) }()
	client, err := computer.New(computer.Config{Socket: socket})
	if err != nil {
		_ = httpServer.Close()
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerLeases: map[string]*sync.Mutex{}, computerOwners: map[string]string{}}
	t.Cleanup(func() {
		_ = httpServer.Close()
		_ = os.Remove(socket)
	})
	return s, actionCalled
}

func TestScheduledBrowserFallbackStartsDesktopBeforeBrowserAction(t *testing.T) {
	for _, tc := range []struct {
		name, kind                 string
		continuation, startupFails bool
		want                       []string
	}{
		{name: "scheduled root", kind: runKindSchedule, want: []string{"desktop.start", "browser.snapshot"}},
		{name: "scheduled continuation", kind: runKindMessage, continuation: true, want: []string{"desktop.start", "browser.snapshot"}},
		{name: "ordinary run", kind: runKindMessage, want: []string{"browser.snapshot"}},
		{name: "startup failure", kind: runKindSchedule, startupFails: true, want: []string{"desktop.start"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Unix socket paths have a small OS limit; t.TempDir includes the
			// long subtest name and would silently skip every case on macOS.
			socketDir, err := os.MkdirTemp("", "tfb-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
			socket := socketDir + "/control.sock"
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Skipf("Unix sockets unavailable: %v", err)
			}
			var mu sync.Mutex
			var actions []string
			mux := http.NewServeMux()
			mux.HandleFunc("/v1/action", func(w http.ResponseWriter, req *http.Request) {
				var action computer.Action
				if err := json.NewDecoder(req.Body).Decode(&action); err != nil {
					t.Errorf("decode action: %v", err)
					return
				}
				if action.Name != "desktop.hold" {
					mu.Lock()
					actions = append(actions, action.Name)
					mu.Unlock()
				}
				w.Header().Set("Content-Type", "application/json")
				if action.Name == "desktop.start" && tc.startupFails {
					_, _ = w.Write([]byte(`{"ok":false,"error":"desktop unavailable"}`))
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
			})
			httpServer := &http.Server{Handler: mux}
			go func() { _ = httpServer.Serve(listener) }()
			t.Cleanup(func() { _ = httpServer.Close(); _ = os.Remove(socket) })
			client, err := computer.New(computer.Config{Socket: socket})
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{microVM: client, computerLeases: map[string]*sync.Mutex{}, computerOwners: map[string]string{}}
			run := Run{ID: "scheduled-fallback-test", BotID: "bot-a", Kind: tc.kind}
			if tc.continuation {
				run.scheduleTask = &Message{Content: "scheduled assignment"}
			}
			var browser Tool
			for _, tool := range server.microVMTools(run) {
				if tool.Name == "computer_browser" {
					browser = tool
					break
				}
			}
			if browser.Execute == nil {
				t.Fatal("browser tool unavailable")
			}
			_, err = browser.Execute(context.Background(), json.RawMessage(`{"action":"browser.snapshot"}`))
			if tc.startupFails {
				if err == nil || !strings.Contains(err.Error(), "scheduled browser startup") {
					t.Fatalf("startup error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			got := append([]string(nil), actions...)
			mu.Unlock()
			if len(got) != len(tc.want) {
				t.Fatalf("actions=%v want=%v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("actions=%v want=%v", got, tc.want)
				}
			}
		})
	}
}

func waitForComputerOwner(t *testing.T, s *Server, botID, runID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s.computerOwnerMu.Lock()
		owner := s.computerOwners[botID]
		s.computerOwnerMu.Unlock()
		if owner == runID {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("owner %s was not claimed for Bot %s", runID, botID)
}

func TestModelComputerActionWaitsForViewerLease(t *testing.T) {
	s, actionCalled := newLeaseMicroVMServer(t)
	lease := s.computerLease("bot-a")
	lease.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.microVMAction(context.Background(), Run{BotID: "bot-a", ID: "run-a"}, "desktop.capture", json.RawMessage(`{}`))
		done <- err
	}()
	select {
	case <-actionCalled:
		t.Fatal("model action reached VM while viewer lease was held")
	case <-time.After(75 * time.Millisecond):
	}
	lease.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("model action did not proceed after viewer lease release")
	}
	select {
	case <-actionCalled:
	case <-time.After(time.Second):
		t.Fatal("fake VM action was not called")
	}
}

func TestModelComputerActionWaitingForViewerLeaseHonorsCancellation(t *testing.T) {
	s, actionCalled := newLeaseMicroVMServer(t)
	lease := s.computerLease("bot-a")
	lease.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.microVMAction(ctx, Run{BotID: "bot-a", ID: "run-a"}, "desktop.capture", json.RawMessage(`{}`))
		done <- err
	}()
	waitForComputerOwner(t, s, "bot-a", "run-a")
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lease wait did not honor cancellation")
	}
	lease.Unlock()
	select {
	case <-actionCalled:
		t.Fatal("cancelled model action reached VM")
	default:
	}
	s.computerOwnerMu.Lock()
	defer s.computerOwnerMu.Unlock()
	if owner := s.computerOwners["bot-a"]; owner != "" {
		t.Fatalf("cancelled lease left owner %q", owner)
	}
}

func TestDifferentRunCannotTakeOwnerWhileFirstRunWaitsForViewerLease(t *testing.T) {
	s, _ := newLeaseMicroVMServer(t)
	lease := s.computerLease("bot-a")
	lease.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.microVMAction(ctx, Run{BotID: "bot-a", ID: "run-a"}, "desktop.capture", json.RawMessage(`{}`))
		done <- err
	}()
	waitForComputerOwner(t, s, "bot-a", "run-a")
	waiting, stop := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer stop()
	if _, err := s.microVMAction(waiting, Run{BotID: "bot-a", ID: "run-b"}, "desktop.capture", json.RawMessage(`{}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second run unexpectedly took owner: %v", err)
	}
	cancel()
	lease.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first run did not exit after cancellation")
	}
}

func TestMicroVMToolsUseGuestActionContract(t *testing.T) {
	guestService, err := guest.New(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = guestService.Close(context.Background()) })
	socketDir, err := os.MkdirTemp("", "tfb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	socket := socketDir + "/control.sock"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"kind": "firecracker", "state": "ready", "phase": "ready", "workspace_root": "/workspace", "browser": "Google Chrome"})
	})
	mux.Handle("/", guestService.Handler())
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = os.Remove(socket)
	})
	client, err := computer.New(computer.Config{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerLeases: map[string]*sync.Mutex{}, computerOwners: map[string]string{}}
	run := Run{ID: "run-1", BotID: "11111111-1111-4111-8111-111111111111"}
	prompt := s.microVMEnvironmentPrompt(context.Background(), run.BotID)
	for _, fragment := range []string{"/workspace/bots/" + run.BotID, "/workspace/shared", "Google Chrome", "service host"} {
		if !strings.Contains(prompt, fragment) {
			t.Fatalf("environment prompt missing %q: %s", fragment, prompt)
		}
	}
	// Volatile status trails the history instead of the cache-stable system text.
	if status := s.microVMStatus(context.Background()); status != "VM status ready/ready" || strings.Contains(prompt, "ready/ready") {
		t.Fatalf("VM status=%q prompt=%s", status, prompt)
	}
	tools := s.microVMTools(run)
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	call := func(name, input string) error {
		_, callErr := byName[name].Execute(context.Background(), []byte(input))
		return callErr
	}
	if err := call("computer_shell", `{"command":"true"}`); err != nil {
		t.Fatal(err)
	}
	if err := call("computer_files", `{"action":"files.write","path":"contract.txt","content":"ok"}`); err != nil {
		t.Fatal(err)
	}
	if err := call("computer_files", `{"action":"files.read","path":"contract.txt"}`); err != nil {
		t.Fatal(err)
	}
	if err := call("computer_files", `{"action":"files.list","path":"."}`); err != nil {
		t.Fatal(err)
	}
	// These actions may report a missing Linux desktop dependency in a unit
	// environment, but the real guest handler must not reject their exact
	// action-specific argument shapes as unknown fields.
	for _, input := range []struct{ name, body string }{
		{"computer_desktop", `{"action":"desktop.start"}`},
		{"computer_desktop", `{"action":"desktop.stop"}`},
		{"computer_desktop", `{"action":"desktop.capture"}`},
		{"computer_desktop", `{"action":"desktop.click","x":10,"y":20,"screenshot_width":1280,"screenshot_height":800}`},
		{"computer_desktop", `{"action":"desktop.type","text":"ok"}`},
		{"computer_desktop", `{"action":"desktop.scroll","direction":"down","amount":3}`},
		{"computer_desktop", `{"action":"desktop.key","key":"Return","modifiers":[]}`},
		{"computer_browser", `{"action":"browser.navigate","url":"https://example.com"}`},
		{"computer_browser", `{"action":"browser.snapshot"}`},
		{"computer_browser", `{"action":"browser.action","target_action":"switch","target_id":"synthetic-target"}`},
		{"computer_browser", `{"action":"browser.action","target_action":"navigate","url":"https://example.com"}`},
	} {
		if err := call(input.name, input.body); err != nil && strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("%s contract rejected: %v", input.body, err)
		}
	}
}
