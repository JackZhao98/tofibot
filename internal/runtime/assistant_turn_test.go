package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type assistantTurnProvider struct {
	calls int
}

func (p *assistantTurnProvider) Chat(ctx context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	if p.calls == 1 {
		return &provider.ChatResponse{
			Content: "<think>private reasoning</think>agenda",
			ToolCalls: []provider.ToolCall{{
				ID: "publish-call", Name: "publish", Arguments: `{}`,
			}},
		}, nil
	}
	return &provider.ChatResponse{Content: "final"}, nil
}

func (p *assistantTurnProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestAssistantTurnCallbackPublishesBeforeToolsAndSanitizes(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &assistantTurnProvider{}
	e := &engine{provider: p, model: "test-model"}
	var order []string
	var gotIndex int
	var gotContent string
	result, err := e.Run(context.Background(), Request{
		RunID:    "assistant-turn-order",
		BotID:    "bot",
		Messages: []Message{{Role: "user", Content: "prepare"}},
		Tools: []Tool{{Name: "publish", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			order = append(order, "tool")
			return "published", nil
		}}},
		OnAssistantTurn: func(index int, content string) error {
			order = append(order, "assistant")
			gotIndex, gotContent = index, content
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "final" {
		t.Fatalf("result content = %q, want final", result.Content)
	}
	if !reflect.DeepEqual(order, []string{"assistant", "tool"}) {
		t.Fatalf("callback/tool order = %#v", order)
	}
	if gotIndex != 1 || gotContent != "agenda" {
		t.Fatalf("assistant turn = (%d, %q), want (1, agenda)", gotIndex, gotContent)
	}
}

func TestAssistantTurnCallbackFailurePreventsToolExecution(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &assistantTurnProvider{}
	e := &engine{provider: p, model: "test-model"}
	callbackErr := errors.New("publish failed")
	executed := false
	_, err := e.Run(context.Background(), Request{
		RunID:    "assistant-turn-failure",
		BotID:    "bot",
		Messages: []Message{{Role: "user", Content: "prepare"}},
		Tools: []Tool{{Name: "publish", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			executed = true
			return "published", nil
		}}},
		OnAssistantTurn: func(int, string) error { return callbackErr },
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Run() error = %v, want callback failure", err)
	}
	if executed {
		t.Fatal("tool executed after assistant publication failed")
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1 after callback failure", p.calls)
	}
}

func TestAssistantTurnCallbackSkipsTerminalAssistantMessage(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{}
	e := &engine{provider: p, model: "test-model"}
	called := 0
	result, err := e.Run(context.Background(), Request{
		RunID:    "assistant-turn-terminal",
		BotID:    "bot",
		Messages: []Message{{Role: "user", Content: "prepare"}},
		Tools: []Tool{{Name: "save_memory", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			return "saved", nil
		}}},
		OnAssistantTurn: func(int, string) error {
			called++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Content != "done" || called != 0 {
		t.Fatalf("result=%q callback count=%d, want done/0", result.Content, called)
	}
}
