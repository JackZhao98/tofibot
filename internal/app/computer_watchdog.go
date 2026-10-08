package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// Deadlines for one computer action. A guest that answers nothing within
// these is treated as stuck: the call fails as a normal tool step and the
// watchdog probes the guest. Boot and admission are not counted (see
// computer.Client.ActionWithin); neither is waiting for the shared desktop.
const (
	// computerActionTimeout covers navigate/read/snapshot/click/key/type. The
	// guest's own page operations finish in seconds; 90 s leaves room for a
	// slow page load without letting a hung guest hold the run.
	computerActionTimeout = 90 * time.Second
	// computerLifecycleTimeout covers desktop start/stop (Chrome launch) and
	// file transfers, which legitimately take longer.
	computerLifecycleTimeout = 120 * time.Second
	// computerShellMargin is added to a shell command's own timeout_sec.
	computerShellMargin = 30 * time.Second
	// computerToolTimeout is the runtime backstop for one computer tool call,
	// which may chain desktop.start, the action and one desktop restart.
	computerToolTimeout = 5 * time.Minute
)

// computerDispatchTimeoutFor is replaced only by tests.
var computerDispatchTimeoutFor = computerDispatchTimeout

// computerDispatchTimeout is the guest-answer deadline of one action.
func computerDispatchTimeout(name string, args json.RawMessage) time.Duration {
	switch name {
	case "shell.exec":
		var in struct {
			TimeoutSec int `json:"timeout_sec"`
		}
		_ = json.Unmarshal(args, &in)
		if in.TimeoutSec <= 0 {
			in.TimeoutSec = 60 // the guest default
		}
		if in.TimeoutSec > 120 {
			in.TimeoutSec = 120
		}
		return max(computerActionTimeout, time.Duration(in.TimeoutSec)*time.Second+computerShellMargin)
	case "desktop.start", "desktop.stop", "files.read", "files.write", "files.list", "files.export_chunk":
		return computerLifecycleTimeout
	}
	return computerActionTimeout
}

const (
	computerHealthy       = "healthy"
	computerSuspect       = "suspect"
	computerRestarting    = "restarting"
	computerUnresponsive  = "unresponsive"
	computerHealthUnknown = "unknown"
)

// Watchdog policy for one account's computer.
const (
	watchdogInterval = 30 * time.Second
	// watchdogFastInterval re-probes sooner while a failure or restart is open.
	watchdogFastInterval = 20 * time.Second
	// watchdogFailureThreshold consecutive unanswered liveness probes (5 s
	// each, roughly a minute in all) before a restart; the manager then
	// re-probes twice itself. A busy guest still answers its static info.
	watchdogFailureThreshold = 3
	// watchdogRestartWait is how long a restart may take before it counts as failed.
	watchdogRestartWait = 4 * time.Minute
	// watchdogBackoffBase doubles per restart within the window: 2, 4, 8 min.
	watchdogBackoffBase = 2 * time.Minute
	// watchdogRestartCap restarts per window; then the computer is reported
	// unresponsive and only a person (Retry) restarts it again.
	watchdogRestartCap    = 3
	watchdogRestartWindow = time.Hour
)

// ComputerHealth is the watchdog's view, surfaced by the computer info API.
type ComputerHealth struct {
	State         string `json:"state"`
	Failures      int    `json:"failures,omitempty"`
	Restarts      int    `json:"restarts,omitempty"`
	RestartedAt   string `json:"restarted_at,omitempty"`
	NextRestartAt string `json:"next_restart_at,omitempty"`
}

// computerWatchdog restarts only this account's computer when its guest
// stops answering while the VM process still looks ready.
type computerWatchdog struct {
	mu              sync.Mutex
	now             func() time.Time
	probe           func(context.Context) (computer.GuestHealth, error)
	recover         func(context.Context) error
	state           string
	failures        int
	restarts        []time.Time
	restartingSince time.Time
	nextRestart     time.Time
	wake            chan struct{}
	cancel          context.CancelFunc
	done            chan struct{}
}

func newComputerWatchdog(client *computer.Client) *computerWatchdog {
	return &computerWatchdog{now: time.Now, probe: client.Health, recover: client.Recover, state: computerHealthUnknown, wake: make(chan struct{}, 1)}
}

func (w *computerWatchdog) start() {
	if w == nil || w.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.done = cancel, make(chan struct{})
	go func() {
		defer close(w.done)
		for {
			w.check(ctx)
			timer := time.NewTimer(w.interval())
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-w.wake:
			case <-timer.C:
			}
			timer.Stop()
		}
	}()
}

func (w *computerWatchdog) stop() {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (w *computerWatchdog) interval() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == computerSuspect || w.state == computerRestarting {
		return watchdogFastInterval
	}
	return watchdogInterval
}

// suspect asks for an immediate probe, for example after an action timed out.
func (w *computerWatchdog) suspect() {
	if w == nil {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// check runs one probe and, when warranted, one restart request.
func (w *computerWatchdog) check(ctx context.Context) {
	health, err := w.probe(ctx)
	if ctx.Err() != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	if w.state == computerRestarting {
		if err == nil && health.Guest == "ok" {
			log.Printf("[computer] watchdog: computer answers again after restart")
			w.state, w.failures, w.restartingSince = computerHealthy, 0, time.Time{}
			return
		}
		if now.Sub(w.restartingSince) < watchdogRestartWait {
			return
		}
		// The restart did not bring the guest back in time.
		log.Printf("[computer] watchdog: restart did not recover the computer within %s", watchdogRestartWait)
		w.state, w.restartingSince, w.failures = computerSuspect, time.Time{}, watchdogFailureThreshold
		w.restartLocked(ctx, now)
		return
	}
	switch {
	case errors.Is(err, computer.ErrHealthUnsupported):
		w.state, w.failures = computerHealthUnknown, 0
	case err != nil:
		// The manager itself is unreachable (stopped account, socket gone): it
		// cannot judge or restart the guest, and that is not a hung guest.
		if w.state != computerUnresponsive {
			w.state, w.failures = computerHealthUnknown, 0
		}
	case health.Guest == "ok":
		w.state, w.failures = computerHealthy, 0
	case health.Guest == "unresponsive":
		if w.state == computerUnresponsive {
			return
		}
		w.failures++
		w.state = computerSuspect
		log.Printf("[computer] watchdog: guest did not answer (%d/%d)", w.failures, watchdogFailureThreshold)
		if w.failures >= watchdogFailureThreshold {
			w.restartLocked(ctx, now)
		}
	default:
		// not_ready: stopped, starting or failed; /v1/info reports those.
		if w.state != computerUnresponsive {
			w.state, w.failures = computerHealthUnknown, 0
		}
	}
}

// restartLocked asks the manager for one restart within backoff and cap.
func (w *computerWatchdog) restartLocked(ctx context.Context, now time.Time) {
	kept := w.restarts[:0]
	for _, at := range w.restarts {
		if now.Sub(at) < watchdogRestartWindow {
			kept = append(kept, at)
		}
	}
	w.restarts = kept
	if len(w.restarts) >= watchdogRestartCap {
		if w.state != computerUnresponsive {
			log.Printf("[computer] watchdog: %d restarts within %s; leaving the computer for a manual retry", len(w.restarts), watchdogRestartWindow)
		}
		w.state = computerUnresponsive
		return
	}
	if now.Before(w.nextRestart) {
		return
	}
	w.mu.Unlock()
	err := w.recover(ctx)
	w.mu.Lock()
	switch {
	case errors.Is(err, computer.ErrGuestResponsive):
		w.state, w.failures = computerHealthy, 0
	case err != nil:
		log.Printf("[computer] watchdog: restart request failed: %v", err)
		w.nextRestart = now.Add(watchdogBackoffBase)
	default:
		log.Printf("[computer] watchdog: restarting the unresponsive computer")
		w.restarts = append(w.restarts, now)
		w.state, w.failures, w.restartingSince = computerRestarting, 0, now
		w.nextRestart = now.Add(watchdogBackoffBase << (len(w.restarts) - 1))
	}
}

// manualRecover is a person's Retry: it clears the cap and restarts at once.
func (w *computerWatchdog) manualRecover(ctx context.Context) (bool, error) {
	if w == nil {
		return false, nil
	}
	w.mu.Lock()
	if w.state != computerUnresponsive {
		w.mu.Unlock()
		return false, nil
	}
	w.mu.Unlock()
	err := w.recover(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	w.restarts, w.nextRestart = nil, time.Time{}
	switch {
	case errors.Is(err, computer.ErrGuestResponsive):
		w.state, w.failures = computerHealthy, 0
		return true, nil
	case err != nil:
		return true, err
	}
	w.restarts = []time.Time{now}
	w.state, w.failures, w.restartingSince = computerRestarting, 0, now
	w.nextRestart = now.Add(watchdogBackoffBase)
	return true, nil
}

func (w *computerWatchdog) view() *ComputerHealth {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	v := &ComputerHealth{State: w.state, Failures: w.failures, Restarts: len(w.restarts)}
	if n := len(w.restarts); n > 0 {
		v.RestartedAt = w.restarts[n-1].UTC().Format(time.RFC3339Nano)
	}
	if (w.state == computerSuspect || w.state == computerRestarting) && w.nextRestart.After(w.now()) {
		v.NextRestartAt = w.nextRestart.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// admission fails a computer call fast while this computer is restarting or
// left unresponsive, instead of letting it hang on a dead guest.
func (w *computerWatchdog) admission() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	state := w.state
	w.mu.Unlock()
	switch state {
	case computerRestarting:
		return tooloutcome.New(tooloutcome.Transient, "computer_restarting", "not_executed",
			"The computer stopped responding and is restarting; this call was not executed. A restart usually takes one to two minutes. Continue with work that does not need the computer, or tell the user it is temporarily unavailable.",
			"explain_blocker").Err()
	case computerUnresponsive:
		return tooloutcome.New(tooloutcome.Permanent, "computer_unresponsive", "not_executed",
			"The computer is not responding and automatic restarts did not help; this call was not executed. Tell the user the computer needs a manual retry from its status panel.",
			"explain_blocker").Err()
	}
	return nil
}

func (s *Server) computerAdmission() error { return s.computerWatchdog.admission() }

// computerActionTimeoutError converts an unanswered guest action into the
// runtime's typed timeout and asks the watchdog to probe the guest.
func (s *Server) computerActionTimeoutError(ctx context.Context, name string, limit time.Duration) error {
	s.computerWatchdog.suspect()
	observation := isReadOnlyMicroVMAction(name) || name == "terminal.list" || name == "terminal.read"
	if identity, ok := tooloutcome.ExecutionIdentity(ctx); ok {
		observation = identity.Risk == tooloutcome.Observation
	}
	log.Printf("[computer] action %s did not answer within %s", name, limit)
	return runtime.ToolTimeoutOutcome(fmt.Sprintf("Computer action %s", name), limit, observation).Err()
}
