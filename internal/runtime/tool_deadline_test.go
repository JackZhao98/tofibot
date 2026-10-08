package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func shortDeadlineGrace(t *testing.T) {
	t.Helper()
	grace, cancelGrace := toolDeadlineGrace, toolCancelGrace
	toolDeadlineGrace, toolCancelGrace = 20*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { toolDeadlineGrace, toolCancelGrace = grace, cancelGrace })
}

func TestToolDeadlineReturnsTypedTimeoutForExecutorThatIgnoresContext(t *testing.T) {
	shortDeadlineGrace(t)
	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	_, err := executeWithDeadline(context.Background(), "computer_browser", 50*time.Millisecond, true, func(context.Context) (string, error) {
		<-release // a hung transport that never looks at its context
		return "late", nil
	})
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("deadline did not bound the call: %s", elapsed)
	}
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != ToolTimeoutCode || o.Status != tooloutcome.Transient || o.Certainty != "no_side_effects" {
		t.Fatalf("observation timeout outcome = %#v, %v", o, err)
	}
}

func TestToolDeadlineMarksSideEffectingTimeoutUncertain(t *testing.T) {
	shortDeadlineGrace(t)
	_, err := executeWithDeadline(context.Background(), "computer_desktop", 30*time.Millisecond, false, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	o, ok := tooloutcome.FromError(err)
	if !ok || o.Code != ToolTimeoutCode || o.Status != tooloutcome.Uncertain || o.Certainty != "unknown" || o.NextAction != "verify_effect" {
		t.Fatalf("side-effect timeout outcome = %#v, %v", o, err)
	}
}

func TestToolDeadlineExcludesPausedWaits(t *testing.T) {
	shortDeadlineGrace(t)
	out, err := executeWithDeadline(context.Background(), "computer_browser", 80*time.Millisecond, true, func(ctx context.Context) (string, error) {
		resume := PauseToolDeadline(ctx) // queued behind another run's desktop lease
		time.Sleep(250 * time.Millisecond)
		resume()
		return "read", nil
	})
	if err != nil || out != "read" {
		t.Fatalf("paused wait counted against the deadline: %q, %v", out, err)
	}
	out, err = executeWithDeadline(context.Background(), "ask_user_question", 80*time.Millisecond, false, func(ctx context.Context) (string, error) {
		resume := PauseForUserInput(ctx)
		time.Sleep(250 * time.Millisecond)
		resume()
		return "answered", nil
	})
	if err != nil || out != "answered" {
		t.Fatalf("human wait counted against the deadline: %q, %v", out, err)
	}
}

func TestToolDeadlineKeepsResultsAndCallerCancellation(t *testing.T) {
	shortDeadlineGrace(t)
	out, err := executeWithDeadline(context.Background(), "x", time.Second, false, func(context.Context) (string, error) { return "ok", nil })
	if err != nil || out != "ok" {
		t.Fatalf("fast call = %q, %v", out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = executeWithDeadline(ctx, "x", time.Second, false, func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation = %v", err)
	}
	_, err = executeWithDeadline(context.Background(), "x", time.Second, false, func(context.Context) (string, error) { panic("boom") })
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic = %v", err)
	}
}

func TestEffectiveToolTimeoutDefaultsAndCaps(t *testing.T) {
	if EffectiveToolTimeout(Tool{}) != DefaultToolTimeout || EffectiveToolTimeout(Tool{Timeout: time.Hour}) != MaxToolTimeout || EffectiveToolTimeout(Tool{Timeout: 90 * time.Second}) != 90*time.Second {
		t.Fatal("tool timeout bounds changed")
	}
}

// A hung tool fails as a normal tool step and the model gets another turn.
func TestRunContinuesAfterToolTimeout(t *testing.T) {
	shortDeadlineGrace(t)
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{toolCall: &provider.ToolCall{ID: "snap", Name: "computer_browser", Arguments: `{"action":"browser.snapshot"}`}}
	e := &engine{provider: p, model: "test-model"}
	release := make(chan struct{})
	defer close(release)
	var events []ToolEvent
	result, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "run", Messages: []Message{{Role: "user", Content: "look"}},
		Tools: []Tool{{Name: "computer_browser", Parameters: map[string]any{"type": "object"}, Timeout: 50 * time.Millisecond,
			Identity: func(raw json.RawMessage) tooloutcome.Identity {
				i := tooloutcome.DefaultIdentity("computer_browser", raw)
				i.Risk = tooloutcome.Observation
				return i
			},
			Execute: func(context.Context, json.RawMessage) (string, error) { <-release; return "", nil }}},
		OnToolEvent: func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil || result.Content != "done" {
		t.Fatalf("run = %#v, %v", result, err)
	}
	last := events[len(events)-1]
	if last.Status != "failed" || last.Outcome == nil || last.Outcome.Code != ToolTimeoutCode || last.Outcome.Certainty != "no_side_effects" {
		t.Fatalf("timeout tool event = %#v", last)
	}
	msgs := p.requests[1].Messages
	if !strings.Contains(msgs[len(msgs)-1].Content, ToolTimeoutCode) {
		t.Fatalf("model did not see the timeout: %#v", msgs[len(msgs)-1])
	}
}
