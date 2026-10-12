package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type unknownToolProvider struct{ calls int }

func (p *unknownToolProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "mcp_notion__notion-update-page", Arguments: `{}`}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}
func (p *unknownToolProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

// A tool name outside the run's tool set was never dispatched: the activity
// must carry a definite not_executed outcome, not an empty one.
func TestUnknownToolNameIsDefiniteNotExecuted(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	var got *tooloutcome.Outcome
	var failed bool
	_, err := (&engine{provider: &unknownToolProvider{}, model: "synthetic"}).Run(context.Background(), Request{RunID: "unknown-tool", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{{Name: "save", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}, OnToolEvent: func(e ToolEvent) error {
		if e.Status == "failed" && strings.HasPrefix(e.Name, "mcp_notion__") {
			failed, got = true, e.Outcome
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !failed || got == nil || got.Certainty != "not_executed" || got.Code != "tool_unavailable" {
		t.Fatalf("failed=%v outcome=%+v", failed, got)
	}
}
