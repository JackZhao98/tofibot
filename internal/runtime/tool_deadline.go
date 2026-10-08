package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	// MaxToolTimeout is the hard upper bound of active time for any one tool
	// call. It matches the product run budget: a call that outlives it could
	// never be followed by a useful model turn anyway.
	MaxToolTimeout = 10 * time.Minute
	// DefaultToolTimeout applies to tools that declare no deadline of their own.
	DefaultToolTimeout = MaxToolTimeout
	// ToolTimeoutCode is the typed outcome code of a call stopped by its deadline.
	ToolTimeoutCode = "tool_timeout"
)

// EffectiveToolTimeout is the deadline the runtime enforces for a tool.
func EffectiveToolTimeout(t Tool) time.Duration {
	if t.Timeout <= 0 || t.Timeout > MaxToolTimeout {
		return DefaultToolTimeout
	}
	return t.Timeout
}

var (
	// toolDeadlineGrace lets a cancelled executor unwind and report what it
	// actually observed before the runtime substitutes a timeout outcome.
	toolDeadlineGrace = 2 * time.Second
	// toolCancelGrace bounds how long a caller cancellation waits for an
	// executor that ignores its context.
	toolCancelGrace = 5 * time.Second
)

type toolDeadlineKey struct{}

// toolDeadline measures active time of one call: time spent waiting for a
// person or for a queued shared resource is excluded, so a deadline never
// fires because someone else held the desktop or a question was pending.
type toolDeadline struct {
	mu          sync.Mutex
	now         func() time.Time
	started     time.Time
	depth       int
	pausedSince time.Time
	paused      time.Duration
}

func (d *toolDeadline) active() time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	at := d.now()
	paused := d.paused
	if d.depth > 0 {
		paused += at.Sub(d.pausedSince)
	}
	return at.Sub(d.started) - paused
}

func (d *toolDeadline) isPaused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.depth > 0
}

// PauseToolDeadline excludes a backend-owned wait (a queued shared desktop, a
// pending human answer) from the current tool call's deadline. Always defer
// the returned resume function. Outside a runtime tool call it is a no-op.
func PauseToolDeadline(ctx context.Context) func() {
	if ctx == nil {
		return func() {}
	}
	d, ok := ctx.Value(toolDeadlineKey{}).(*toolDeadline)
	if !ok {
		return func() {}
	}
	d.mu.Lock()
	if d.depth == 0 {
		d.pausedSince = d.now()
	}
	d.depth++
	d.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.depth--
			if d.depth == 0 {
				d.paused += d.now().Sub(d.pausedSince)
			}
		})
	}
}

// ToolTimeoutOutcome is the typed result of a call stopped by its deadline.
// A read is known to have no side effects; anything else may have acted.
func ToolTimeoutOutcome(name string, limit time.Duration, observation bool) tooloutcome.Outcome {
	if observation {
		return tooloutcome.New(tooloutcome.Transient, ToolTimeoutCode, "no_side_effects",
			fmt.Sprintf("%s did not finish within %s and was cancelled. It only reads, so nothing changed. The target may be busy or unresponsive: try a different approach, or tell the user it is not responding.", name, limit),
			"explain_blocker")
	}
	return tooloutcome.New(tooloutcome.Uncertain, ToolTimeoutCode, "unknown",
		fmt.Sprintf("%s did not finish within %s and was cancelled. It may or may not have taken effect. Verify the target state before repeating it, use a different approach, or tell the user it is not responding.", name, limit),
		"verify_effect")
}

type toolCallResult struct {
	out string
	err error
}

// executeWithDeadline runs one tool call under a hard deadline of active
// time. The call always returns: an executor that ignores its context is
// abandoned (its context cancelled) rather than allowed to hold the run.
func executeWithDeadline(parent context.Context, name string, limit time.Duration, observation bool, fn func(context.Context) (string, error)) (string, error) {
	return executeWithDeadlineClock(parent, name, limit, observation, time.Now, fn)
}

func executeWithDeadlineClock(parent context.Context, name string, limit time.Duration, observation bool, now func() time.Time, fn func(context.Context) (string, error)) (string, error) {
	d := &toolDeadline{now: now, started: now()}
	ctx, cancel := context.WithCancel(context.WithValue(parent, toolDeadlineKey{}, d))
	defer cancel()
	done := make(chan toolCallResult, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				done <- toolCallResult{err: fmt.Errorf("%s panicked: %v", name, p)}
			}
		}()
		out, err := fn(ctx)
		done <- toolCallResult{out: out, err: err}
	}()
	timer := time.NewTimer(limit)
	defer timer.Stop()
	for {
		select {
		case r := <-done:
			return r.out, r.err
		case <-parent.Done():
			select {
			case r := <-done:
				return r.out, r.err
			case <-time.After(toolCancelGrace):
				return "", parent.Err()
			}
		case <-timer.C:
			if d.isPaused() {
				// Re-check after the wait ends; the remaining budget is
				// computed from active time only.
				timer.Reset(time.Second)
				continue
			}
			if remaining := limit - d.active(); remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			cancel()
			select {
			case r := <-done:
				// A result that raced the deadline is real evidence; keep it.
				if r.err == nil {
					return r.out, nil
				}
				// The executor's own deadline (for example a computer dispatch)
				// already names what timed out.
				if o, typed := tooloutcome.FromError(r.err); typed && o.Code == ToolTimeoutCode {
					return r.out, r.err
				}
			case <-time.After(toolDeadlineGrace):
			case <-parent.Done():
				return "", parent.Err()
			}
			return "", ToolTimeoutOutcome(name, limit, observation).Err()
		}
	}
}
