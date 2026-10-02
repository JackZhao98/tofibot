package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type twoCallProvider struct {
	called bool
}

func (p *twoCallProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	if !p.called {
		p.called = true
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{
			{ID: "invalid-first", Name: "write", Arguments: `{invalid`},
			{ID: "valid-second", Name: "write", Arguments: `{}`},
		}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}

func (p *twoCallProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestToolEventTrackerLifecycleKeepsProviderIDs(t *testing.T) {
	var events []ToolEvent
	tracker := newToolEventTracker(func(event ToolEvent) error {
		events = append(events, event)
		return nil
	})
	tracker.queue([]provider.ToolCall{{ID: "provider-call/1", Name: "bash", Arguments: `{"command":"printf hi"}`}})
	if _, err := tracker.start("bash"); err != nil {
		t.Fatal(err)
	}
	tracker.finish(provider.Message{Role: "tool", ToolCallID: "provider-call/1", ToolName: "bash", Content: "hi"})
	if len(events) != 3 {
		t.Fatalf("events=%d, want lifecycle of 3", len(events))
	}
	for i, status := range []string{"queued", "running", "completed"} {
		if events[i].Status != status || events[i].CallID != "provider-call/1" {
			t.Fatalf("event %d=%+v", i, events[i])
		}
	}
}

func TestToolEventTrackerRejectsDuplicateCallID(t *testing.T) {
	tracker := newToolEventTracker(nil)
	tracker.queue([]provider.ToolCall{{ID: "same", Name: "one", Arguments: `{}`}})
	tracker.queue([]provider.ToolCall{{ID: "same", Name: "two", Arguments: `{}`}})
	if tracker.Err() == nil || !strings.Contains(tracker.Err().Error(), "duplicate tool call id") {
		t.Fatalf("duplicate error=%v", tracker.Err())
	}
}

func TestRunToolEventCallbackFailurePreventsExecution(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{toolCall: &provider.ToolCall{ID: "call-fail", Name: "write", Arguments: `{}`}}
	e := &engine{provider: p, model: "test-model"}
	var executed bool
	callbackErr := errors.New("record tool event: disk full")
	_, err := e.Run(context.Background(), Request{
		RunID:    "run-fail",
		BotID:    "bot-fail",
		Messages: []Message{{Role: "user", Content: "write"}},
		Tools: []Tool{{Name: "write", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			executed = true
			return "side effect", nil
		}}},
		OnToolEvent: func(event ToolEvent) error {
			if event.Status == "queued" {
				return callbackErr
			}
			return nil
		},
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Run() error=%v, want callback failure", err)
	}
	if executed {
		t.Fatal("tool executed after queued event persistence failed")
	}
}

func TestRunToolEventRecordsFailedToolResult(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{toolCall: &provider.ToolCall{ID: "provider-error", Name: "write", Arguments: `{}`}}
	e := &engine{provider: p, model: "test-model"}
	var events []ToolEvent
	_, err := e.Run(context.Background(), Request{
		RunID:    "run-error",
		BotID:    "bot-error",
		Messages: []Message{{Role: "user", Content: "write"}},
		Tools: []Tool{{Name: "write", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New("permission denied")
		}}},
		OnToolEvent: func(event ToolEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error=%v", err)
	}
	if len(events) != 3 || events[2].Status != "failed" || events[2].CallID != "provider-error" {
		t.Fatalf("events=%+v", events)
	}
	if !strings.Contains(events[2].Result, "permission denied") {
		t.Fatalf("failed result=%q", events[2].Result)
	}
}

func TestRunSameNameInvalidCallDoesNotShiftNextCall(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &twoCallProvider{}
	e := &engine{provider: p, model: "test-model"}
	var events []ToolEvent
	executed := 0
	_, err := e.Run(context.Background(), Request{
		RunID:    "run-two-calls",
		BotID:    "bot-two-calls",
		Messages: []Message{{Role: "user", Content: "write twice"}},
		Tools: []Tool{{Name: "write", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			executed++
			return "second succeeded", nil
		}}},
		OnToolEvent: func(event ToolEvent) error {
			events = append(events, event)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error=%v", err)
	}
	if executed != 1 {
		t.Fatalf("executed=%d, want only valid second call", executed)
	}
	if len(events) != 5 {
		t.Fatalf("events=%+v", events)
	}
	if events[0].CallID != "invalid-first" || events[1].CallID != "valid-second" || events[2].Status != "failed" || events[2].CallID != "invalid-first" || events[3].Status != "running" || events[3].CallID != "valid-second" || events[4].Status != "completed" || events[4].CallID != "valid-second" {
		t.Fatalf("call lifecycle shifted: %+v", events)
	}
}
