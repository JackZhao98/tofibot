package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// toolsThenDone answers with n tool calls, one per model call, then a final.
type toolsThenDone struct {
	mu       sync.Mutex
	n        int
	calls    int
	requests []*provider.ChatRequest
}

func (p *toolsThenDone) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	p.calls++
	if p.calls <= p.n {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "call-" + string(rune('0'+p.calls)), Name: "work", Arguments: `{}`}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}

func (p *toolsThenDone) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// "Stop current step": only the call in flight ends. The model reads that the
// person interrupted the step, and the run goes on to its answer.
func TestStopCurrentStepEndsTheToolCallAndNotTheRun(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &toolsThenDone{n: 1}
	e := &engine{provider: p, model: "test-model"}
	interrupt := make(chan func(), 1)
	started := make(chan struct{})
	var events []ToolEvent
	go func() {
		stop := <-interrupt
		<-started
		stop()
	}()
	result, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "stop-step", Messages: []Message{{Role: "user", Content: "work"}},
		BeginStep: func(stop func()) func() { interrupt <- stop; return func() {} },
		Tools: []Tool{{Name: "work", Parameters: map[string]any{"type": "object"},
			Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
				close(started)
				<-ctx.Done()
				return "", ctx.Err()
			}}},
		OnToolEvent: func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil || result.Content != "done" {
		t.Fatalf("the run did not go on after the step was interrupted: %#v %v", result, err)
	}
	last := events[len(events)-1]
	if last.Status != "failed" || last.Outcome == nil || last.Outcome.Code != StepInterruptedCode || last.Outcome.Status != tooloutcome.Uncertain {
		t.Fatalf("tool event = %#v", last)
	}
	msgs := p.requests[1].Messages
	if got := msgs[len(msgs)-1].Content; !strings.Contains(got, StepInterruptedCode) || !strings.Contains(got, "The person interrupted this step") {
		t.Fatalf("the model did not read the interruption: %q", got)
	}
}

// The run's own cancellation is still a cancellation, not an interrupted step.
func TestCancellingTheRunIsNotAnInterruptedStep(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &toolsThenDone{n: 1}
	e := &engine{provider: p, model: "test-model"}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() { <-started; cancel() }()
	_, err := e.Run(ctx, Request{BotID: "bot", RunID: "stop-all", Messages: []Message{{Role: "user", Content: "work"}},
		Tools: []Tool{{Name: "work", Parameters: map[string]any{"type": "object"},
			Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
				close(started)
				<-ctx.Done()
				return "", ctx.Err()
			}}},
	})
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
}

// What the caller hands over at a boundary becomes a user turn, is checkpointed
// with the transcript, and the model sees it on its next request.
func TestDrainInboxAppendsAUserTurnThatIsCheckpointed(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &toolsThenDone{n: 2}
	e := &engine{provider: p, model: "test-model"}
	var boundary int
	var checkpoints []string
	result, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "drain", Messages: []Message{{Role: "user", Content: "work"}},
		Tools: []Tool{{Name: "work", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}},
		DrainInbox: func(int) (string, bool, error) {
			boundary++
			if boundary == 2 {
				return "[new message from the person]\nmake it shorter\n[/new message from the person]", true, nil
			}
			return "", false, nil
		},
		OnCheckpoint: func(kind string, raw json.RawMessage) error {
			checkpoints = append(checkpoints, string(raw))
			return nil
		},
	})
	if err != nil || result.Content != "done" {
		t.Fatalf("run: %#v %v", result, err)
	}
	// The second model request carries the first tool round only; the third carries the message.
	third := p.requests[2].Messages
	var found bool
	for _, m := range third {
		if m.Role == "user" && strings.Contains(m.Content, "make it shorter") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the model never saw the message: %#v", third)
	}
	saved := false
	for _, c := range checkpoints {
		if strings.Contains(c, "make it shorter") {
			saved = true
		}
	}
	if !saved {
		t.Fatal("the message was not part of any checkpoint, so a restart would lose it")
	}
	if p.calls != 3 {
		t.Fatalf("model calls = %d", p.calls)
	}
}

// Each message read in place refills the run's time budget once.
func TestAReadMessageRefillsTheTimeBudget(t *testing.T) {
	run := func(drain, refillOnRead bool) Result {
		paths.SetTofiHome(t.TempDir())
		p := &toolsThenDone{n: 4}
		e := &engine{provider: p, model: "test-model", config: Config{MaxDuration: 150 * time.Millisecond}}
		boundary := 0
		req := Request{BotID: "bot", RunID: "budget", Messages: []Message{{Role: "user", Content: "work"}},
			Tools: []Tool{{Name: "work", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
				time.Sleep(60 * time.Millisecond)
				return "ok", nil
			}}}}
		if drain {
			req.DrainInbox = func(int) (string, bool, error) {
				boundary++
				if boundary == 3 {
					return "[new message from the person]\nkeep going\n[/new message from the person]", refillOnRead, nil
				}
				return "", false, nil
			}
		}
		result, err := e.Run(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if r := run(false, false); !r.BudgetExhausted && r.BudgetReason == "" {
		t.Fatalf("without a message the 60 ms tool rounds should exhaust a 150 ms budget: %#v", r)
	}
	if r := run(true, true); r.BudgetExhausted || r.BudgetReason != "" {
		t.Fatalf("a message read in place did not refill the budget: %#v", r)
	}
}

// Review item 2: text that is not a person's message (a settled wait, a task
// update) is appended but does not refill the budget, so a stream of events
// cannot keep a run alive.
func TestOnlyAPersonsMessageRefillsTheBudget(t *testing.T) {
	run := func(refill bool) Result {
		paths.SetTofiHome(t.TempDir())
		p := &toolsThenDone{n: 4}
		e := &engine{provider: p, model: "test-model", config: Config{MaxDuration: 150 * time.Millisecond}}
		boundary := 0
		result, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "refill", Messages: []Message{{Role: "user", Content: "work"}},
			Tools: []Tool{{Name: "work", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
				time.Sleep(60 * time.Millisecond)
				return "ok", nil
			}}},
			DrainInbox: func(int) (string, bool, error) {
				boundary++
				if boundary == 3 {
					return "[events from the task system]\n- task_update: x\n[/events from the task system]", refill, nil
				}
				return "", false, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if r := run(false); !r.BudgetExhausted && r.BudgetReason == "" {
		t.Fatalf("an event refilled the budget: %#v", r)
	}
	if r := run(true); r.BudgetExhausted || r.BudgetReason != "" {
		t.Fatalf("a person's message did not refill: %#v", r)
	}
}
