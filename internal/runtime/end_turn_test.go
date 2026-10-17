package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// stepProvider answers each model call with the next scripted response and
// fails any call beyond the script.
type stepProvider struct {
	mu       sync.Mutex
	steps    []func(delta func(provider.StreamDelta)) *provider.ChatResponse
	requests []*provider.ChatRequest
}

func (p *stepProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *stepProvider) ChatStream(_ context.Context, req *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	p.mu.Lock()
	i := len(p.requests)
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if i >= len(p.steps) {
		return &provider.ChatResponse{Content: "an unexpected extra model call"}, nil
	}
	return p.steps[i](delta), nil
}

func finishingTool(ended *int) Tool {
	return Tool{Name: "finish", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		*ended++
		EndTurn(ctx)
		return "delivered", nil
	}}
}

// A tool that ends the turn is recorded as completed, later calls of its batch
// do not run, and the loop makes no further model call.
func TestEndTurnStopsTheLoopAfterTheToolResult(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "finish", Arguments: `{}`}, {ID: "c2", Name: "later", Arguments: `{}`}}}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	ended, later := 0, 0
	var events []ToolEvent
	result, err := e.Run(context.Background(), Request{RunID: "end-turn", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}},
		Tools: []Tool{finishingTool(&ended), {Name: "later", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			later++
			return "ran", nil
		}}},
		OnToolEvent: func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil || !result.TurnEnded || result.Content != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if ended != 1 || later != 0 || len(p.requests) != 1 {
		t.Fatalf("finish=%d later=%d model calls=%d", ended, later, len(p.requests))
	}
	completed := false
	for _, ev := range events {
		if ev.CallID == "c1" && ev.Status == "completed" && ev.Result == "delivered" {
			completed = true
		}
	}
	if !completed {
		t.Fatalf("the ending tool's result was not recorded: %+v", events)
	}
}

// Ending the turn is not a way to hide a real cancellation.
func TestEndTurnDoesNotMaskACallerCancellation(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(func(provider.StreamDelta)) *provider.ChatResponse {
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "finish", Arguments: `{}`}}}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	var ended int
	tool := finishingTool(&ended)
	inner := tool.Execute
	tool.Execute = func(c context.Context, raw json.RawMessage) (string, error) {
		cancel()
		return inner(c, raw)
	}
	result, err := e.Run(ctx, Request{RunID: "end-turn-cancel", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{tool}})
	if err == nil || result.TurnEnded {
		t.Fatalf("a cancelled run reported a clean end: result=%+v err=%v", result, err)
	}
}

func TestStructuredOutputSwitchesTheProgressGuidance(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	system := func(structured bool) string {
		p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
			func(func(provider.StreamDelta)) *provider.ChatResponse { return &provider.ChatResponse{Content: "ok"} },
		}}
		e := &engine{provider: p, model: "test-model"}
		if _, err := e.Run(context.Background(), Request{RunID: "guidance", BotID: "bot", StructuredOutput: structured, System: "base", Messages: []Message{{Role: "user", Content: "go"}},
			Tools: []Tool{{Name: "t", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "", nil }}}}); err != nil {
			t.Fatal(err)
		}
		return p.requests[0].System
	}
	legacy, structured := system(false), system(true)
	if !strings.Contains(legacy, "<progress>") || strings.Contains(structured, "progress>") {
		t.Fatalf("tag guidance: legacy=%q structured=%q", legacy, structured)
	}
	if !strings.Contains(structured, "say(note)") || !strings.Contains(structured, "private working text") {
		t.Fatalf("structured guidance lacks the protocol: %q", structured)
	}
	if len([]rune(structured)) >= len([]rune(legacy)) {
		t.Fatalf("the protocol must save prompt runes: structured=%d legacy=%d", len([]rune(structured)), len([]rune(legacy)))
	}
	if SystemPromptOverheadRunes() < len([]rune(structured))-len([]rune("base")) {
		t.Fatalf("reserved overhead %d does not cover the structured suffix", SystemPromptOverheadRunes())
	}
}

func TestToolCallDeltasReachTheCallerInOrder(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stepProvider{steps: []func(func(provider.StreamDelta)) *provider.ChatResponse{
		func(delta func(provider.StreamDelta)) *provider.ChatResponse {
			delta(provider.StreamDelta{Content: "text"})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 2, ID: "call_say", Name: "say"}}})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 2, Arguments: `{"purpose":`}}})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 2, Arguments: `"answer"}`}}})
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "call_say", Name: "finish", Arguments: `{}`}}}
		},
	}}
	e := &engine{provider: p, model: "test-model"}
	var got []ToolCallDelta
	var ended int
	_, err := e.Run(context.Background(), Request{RunID: "deltas", BotID: "bot", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{finishingTool(&ended)},
		OnDelta: func(string) {}, OnToolCallDelta: func(d ToolCallDelta) { got = append(got, d) }})
	if err != nil {
		t.Fatal(err)
	}
	want := []ToolCallDelta{{Index: 2, ID: "call_say", Name: "say"}, {Index: 2, Arguments: `{"purpose":`}, {Index: 2, Arguments: `"answer"}`}}
	if len(got) != len(want) {
		t.Fatalf("deltas = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delta %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
