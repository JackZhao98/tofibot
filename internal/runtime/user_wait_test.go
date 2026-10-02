package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestUserWaitBudgetCountsOverlappingIntervalsOnce(t *testing.T) {
	ctx, b := withUserWaitBudget(context.Background())
	now := time.Unix(0, 0)
	b.now = func() time.Time { return now }
	first := PauseForUserInput(ctx)
	now = now.Add(10 * time.Second)
	second := PauseForUserInput(ctx)
	now = now.Add(10 * time.Second)
	first()
	first() // Resuming twice must not underflow the nested wait count.
	now = now.Add(10 * time.Second)
	if got := b.duration(); got != 30*time.Second {
		t.Fatalf("active nested wait = %s", got)
	}
	second()
	now = now.Add(time.Minute)
	if got := b.duration(); got != 30*time.Second {
		t.Fatalf("running time counted as wait = %s", got)
	}
	third := PauseForUserInput(ctx)
	now = now.Add(5 * time.Second)
	third()
	if got := b.duration(); got != 35*time.Second {
		t.Fatalf("separate waits = %s", got)
	}
	_, other := withUserWaitBudget(context.Background())
	if got := other.duration(); got != 0 {
		t.Fatalf("wait leaked into another run: %s", got)
	}
	PauseForUserInput(context.Background())()
	PauseForUserInput(nil)()
}

func TestUserWaitBudgetPreservesParentDeadlineAndCancellation(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	ctx, _ := withUserWaitBudget(parent)
	resume := PauseForUserInput(ctx)
	defer resume()
	want, _ := parent.Deadline()
	got, ok := ctx.Deadline()
	if !ok || got != want {
		t.Fatal("waiting changed the parent deadline")
	}
	cancel()
	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Fatalf("cancellation changed: %v", ctx.Err())
		}
	default:
		t.Fatal("waiting detached cancellation")
	}
}

type userWaitProvider struct{ calls int }

func (p *userWaitProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *userWaitProvider) ChatStream(ctx context.Context, _ *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	switch p.calls {
	case 1:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "wait", Name: "input", Arguments: `{}`}}}, nil
	case 2:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "continue", Name: "continue_work", Arguments: `{}`}}}, nil
	default:
		return &provider.ChatResponse{Content: "finished"}, nil
	}
}

func TestUserWaitRuntimeExcludesOnlyExplicitHumanWaiting(t *testing.T) {
	for _, human := range []bool{true, false} {
		t.Run(map[bool]string{true: "human", false: "ordinary_tool"}[human], func(t *testing.T) {
			p := &userWaitProvider{}
			e := &engine{provider: p, model: "test-model", config: Config{MaxDuration: 300 * time.Millisecond}}
			continued := 0
			result, err := e.Run(context.Background(), Request{RunID: "wait-budget", Tools: []Tool{
				{Name: "input", Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
					if human {
						defer PauseForUserInput(ctx)()
					}
					time.Sleep(500 * time.Millisecond)
					return "answer", nil
				}},
				{Name: "continue_work", Execute: func(context.Context, json.RawMessage) (string, error) {
					continued++
					return "recorded", nil
				}},
			}})
			if err != nil || result.Content != "finished" || p.calls != 3 {
				t.Fatalf("result=%+v error=%v calls=%d", result, err, p.calls)
			}
			want := 0
			if human {
				want = 1
			}
			if continued != want {
				t.Fatalf("continued=%d, want %d", continued, want)
			}
		})
	}
}

func TestUserWaitRuntimeStopDoesNotContinueTools(t *testing.T) {
	p := &userWaitProvider{}
	e := &engine{provider: p, model: "test-model"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	done := make(chan struct{})
	continued := false
	go func() {
		defer close(done)
		_, _ = e.Run(ctx, Request{RunID: "wait-stop", Tools: []Tool{
			{Name: "input", Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
				defer PauseForUserInput(ctx)()
				close(started)
				<-ctx.Done()
				return "", ctx.Err()
			}},
			{Name: "continue_work", Execute: func(context.Context, json.RawMessage) (string, error) { continued = true; return "", nil }},
		}})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("wait never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop failed to release wait")
	}
	if continued || p.calls != 1 {
		t.Fatalf("continued=%v calls=%d", continued, p.calls)
	}
}
