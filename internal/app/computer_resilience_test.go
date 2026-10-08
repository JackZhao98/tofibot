package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// hungComputerServer is a manager whose guest accepts actions and never answers.
func hungComputerServer(t *testing.T, health func() computer.GuestHealth) (*Server, *atomic.Int32) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tfr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := dir + "/control.sock"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	actions := &atomic.Int32{}
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/action", func(w http.ResponseWriter, r *http.Request) {
		actions.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(computer.Info{Kind: "firecracker", State: "ready", WorkspaceRoot: "/workspace"})
	})
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(health())
	})
	server := httptest.NewUnstartedServer(mux)
	server.Listener = listener
	server.Start()
	t.Cleanup(func() { close(release); server.Close() })
	client, err := computer.New(computer.Config{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{microVM: client, computerLeases: map[string]*sync.Mutex{}, computerOwners: map[string]string{}}
	s.computerWatchdog = newComputerWatchdog(client)
	return s, actions
}

func TestComputerActionTimeoutIsTypedAndWakesWatchdog(t *testing.T) {
	old := computerDispatchTimeoutFor
	computerDispatchTimeoutFor = func(string, json.RawMessage) time.Duration { return 60 * time.Millisecond }
	t.Cleanup(func() { computerDispatchTimeoutFor = old })
	s, actions := hungComputerServer(t, func() computer.GuestHealth { return computer.GuestHealth{State: "ready", Guest: "ok"} })
	r := Run{ID: "run-timeout", BotID: "bot-timeout"}

	started := time.Now()
	_, err := s.microVMActionFromSource(context.Background(), r, "browser.snapshot", json.RawMessage(`{}`), "model")
	if time.Since(started) > 3*time.Second {
		t.Fatal("hung guest held the call")
	}
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != runtime.ToolTimeoutCode || o.Certainty != "no_side_effects" {
		t.Fatalf("snapshot timeout = %#v, %v", o, err)
	}
	select {
	case <-s.computerWatchdog.wake:
	default:
		t.Fatal("timeout did not ask the watchdog for a probe")
	}
	// A side-effecting action keeps an uncertain outcome, fenced from replay.
	_, err = s.microVMActionFromSource(context.Background(), r, "desktop.key", json.RawMessage(`{"key":"Return"}`), "model")
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != runtime.ToolTimeoutCode || o.Certainty != "unknown" || o.Status != tooloutcome.Uncertain {
		t.Fatalf("key timeout = %#v, %v", o, err)
	}
	// The tool call's own identity decides: browser.read runs as shell.exec.
	readCtx := tooloutcome.WithExecutionIdentity(context.Background(), tooloutcome.Identity{Risk: tooloutcome.Observation})
	_, err = s.microVMActionFromSource(readCtx, r, "shell.exec", json.RawMessage(`{"command":"python3 -"}`), "model")
	if o, ok := tooloutcome.FromError(err); !ok || o.Certainty != "no_side_effects" {
		t.Fatalf("read timeout = %#v, %v", o, err)
	}
	if actions.Load() != 3 {
		t.Fatalf("actions sent = %d", actions.Load())
	}
}

func TestComputerDispatchTimeouts(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       time.Duration
	}{
		{"browser.snapshot", `{}`, 90 * time.Second},
		{"browser.navigate", `{"url":"https://example.com"}`, 90 * time.Second},
		{"desktop.click", `{}`, 90 * time.Second},
		{"desktop.start", `{}`, 120 * time.Second},
		{"shell.exec", `{"command":"true"}`, 90 * time.Second},
		{"shell.exec", `{"command":"make","timeout_sec":120}`, 150 * time.Second},
		{"shell.exec", `{"command":"make","timeout_sec":9999}`, 150 * time.Second},
	} {
		if got := computerDispatchTimeout(tc.name, json.RawMessage(tc.args)); got != tc.want {
			t.Errorf("%s %s = %s, want %s", tc.name, tc.args, got, tc.want)
		}
	}
	if computerToolTimeout > runtime.MaxToolTimeout {
		t.Fatal("computer backstop exceeds the runtime cap")
	}
}

type fakeWatchdogClock struct{ at time.Time }

func (c *fakeWatchdogClock) now() time.Time          { return c.at }
func (c *fakeWatchdogClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func TestComputerWatchdogRestartsOnlyAfterRepeatedFailuresWithBackoffAndCap(t *testing.T) {
	clock := &fakeWatchdogClock{at: time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC)}
	guest := "unresponsive"
	recovers := 0
	w := &computerWatchdog{now: clock.now, state: computerHealthUnknown, wake: make(chan struct{}, 1),
		probe: func(context.Context) (computer.GuestHealth, error) {
			return computer.GuestHealth{State: "ready", Guest: guest}, nil
		},
		recover: func(context.Context) error { recovers++; return nil }}
	ctx := context.Background()

	for i := 1; i < watchdogFailureThreshold; i++ {
		w.check(ctx)
		if recovers != 0 || w.view().State != computerSuspect || w.admission() != nil {
			t.Fatalf("restarted or blocked after %d failure(s): %+v", i, w.view())
		}
	}
	w.check(ctx)
	if recovers != 1 || w.view().State != computerRestarting {
		t.Fatalf("no restart at threshold: recovers=%d %+v", recovers, w.view())
	}
	if o, ok := tooloutcome.FromError(w.admission()); !ok || o.Code != "computer_restarting" || o.Certainty != "not_executed" {
		t.Fatalf("restarting admission = %#v", o)
	}
	guest = "ok"
	clock.advance(time.Minute)
	w.check(ctx)
	if w.view().State != computerHealthy || w.admission() != nil {
		t.Fatalf("not healthy after restart: %+v", w.view())
	}

	// Hangs again at once: the second restart waits for the 2-minute backoff.
	guest = "unresponsive"
	for i := 0; i < watchdogFailureThreshold; i++ {
		w.check(ctx)
	}
	if recovers != 1 || w.view().State != computerSuspect || w.view().NextRestartAt == "" {
		t.Fatalf("restart ignored backoff: recovers=%d %+v", recovers, w.view())
	}
	clock.advance(watchdogBackoffBase)
	w.check(ctx)
	if recovers != 2 || w.view().State != computerRestarting {
		t.Fatalf("restart after backoff: recovers=%d %+v", recovers, w.view())
	}
	// A restart that never comes back counts as failed and backs off further.
	clock.advance(watchdogRestartWait)
	w.check(ctx)
	if recovers != 3 {
		t.Fatalf("failed restart was not retried after backoff: recovers=%d %+v", recovers, w.view())
	}
	clock.advance(watchdogRestartWait + 8*time.Minute)
	w.check(ctx)
	if recovers != 3 || w.view().State != computerUnresponsive {
		t.Fatalf("cap not enforced: recovers=%d %+v", recovers, w.view())
	}
	if o, ok := tooloutcome.FromError(w.admission()); !ok || o.Code != "computer_unresponsive" {
		t.Fatalf("unresponsive admission = %#v", o)
	}
	// Only a person's Retry restarts it again, and that resets the cap.
	handled, err := w.manualRecover(ctx)
	if !handled || err != nil || recovers != 4 || w.view().State != computerRestarting {
		t.Fatalf("manual recover: %v %v recovers=%d %+v", handled, err, recovers, w.view())
	}
}

func TestComputerWatchdogIgnoresUnknownAndStoppedComputers(t *testing.T) {
	clock := &fakeWatchdogClock{at: time.Now()}
	var probeErr error
	guest := "not_ready"
	recovers := 0
	w := &computerWatchdog{now: clock.now, state: computerHealthUnknown, wake: make(chan struct{}, 1),
		probe: func(context.Context) (computer.GuestHealth, error) {
			return computer.GuestHealth{Guest: guest}, probeErr
		},
		recover: func(context.Context) error { recovers++; return nil }}
	for _, err := range []error{nil, computer.ErrHealthUnsupported, errors.New("dial unix: no such file")} {
		probeErr = err
		for i := 0; i < 2*watchdogFailureThreshold; i++ {
			w.check(context.Background())
		}
	}
	if recovers != 0 || w.view().State != computerHealthUnknown {
		t.Fatalf("restarted a computer it could not judge: %d %+v", recovers, w.view())
	}
	// The manager's own probe found the guest answering: no restart happened.
	probeErr, guest = nil, "unresponsive"
	w.recover = func(context.Context) error { recovers++; return computer.ErrGuestResponsive }
	for i := 0; i < watchdogFailureThreshold; i++ {
		w.check(context.Background())
	}
	if recovers != 1 || w.view().State != computerHealthy || w.view().Restarts != 0 {
		t.Fatalf("responsive refusal = %d %+v", recovers, w.view())
	}
}

func TestComputerRestartingFailsToolCallsFastAndInfoShowsHealth(t *testing.T) {
	s, actions := hungComputerServer(t, func() computer.GuestHealth { return computer.GuestHealth{State: "ready", Guest: "unresponsive"} })
	s.computerWatchdog.state = computerRestarting
	s.computerWatchdog.restartingSince = time.Now()
	r := Run{ID: "run-restart", BotID: "bot-restart"}
	started := time.Now()
	_, err := s.microVMAction(context.Background(), r, "browser.snapshot", json.RawMessage(`{}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "computer_restarting" || o.Status != tooloutcome.Transient {
		t.Fatalf("restarting call = %#v, %v", o, err)
	}
	if time.Since(started) > time.Second || actions.Load() != 0 {
		t.Fatalf("restarting call reached the guest (%d) or waited", actions.Load())
	}
	if status := s.microVMStatus(context.Background()); !strings.Contains(status, "health restarting") {
		t.Fatalf("model status = %q", status)
	}
	rec := httptest.NewRecorder()
	if !s.routeComputers(rec, httptest.NewRequest(http.MethodGet, "/api/computers/firecracker/info", nil), "computers/firecracker/info") {
		t.Fatal("computer info route not handled")
	}
	var body struct {
		State  string          `json:"state"`
		Health *ComputerHealth `json:"health"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.State != "ready" || body.Health == nil || body.Health.State != computerRestarting {
		t.Fatalf("info = %s, %v", rec.Body.String(), err)
	}
}

func TestComputerToolsCarryTheComputerDeadline(t *testing.T) {
	s, _ := hungComputerServer(t, func() computer.GuestHealth { return computer.GuestHealth{Guest: "ok"} })
	want := map[string]bool{"computer_shell": true, "computer_files": true, "computer_desktop": true, "computer_browser": true}
	for _, tool := range s.microVMTools(Run{ID: "r", BotID: "b"}) {
		if want[tool.Name] && tool.Timeout != computerToolTimeout {
			t.Errorf("%s timeout = %s", tool.Name, tool.Timeout)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing computer tools: %v", want)
	}
}
