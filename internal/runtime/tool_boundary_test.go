package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestToolsOnlyRejectsUndeclaredLegacyWait(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{toolCall: &provider.ToolCall{ID: "wait-id", Name: "tofi_wait", Arguments: `{"seconds":0}`}}
	e := &engine{provider: p, model: "test"}
	_, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("provider calls = %d", len(p.requests))
	}
	last := p.requests[1].Messages[len(p.requests[1].Messages)-1]
	if last.ToolCallID != "wait-id" || !strings.Contains(last.Content, "not available in this run") {
		t.Fatalf("undeclared tool response = %#v", last)
	}
}

func TestToolsOnlyUsesDeclaredHandlerWithoutLegacySkillHint(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &scriptedProvider{toolCall: &provider.ToolCall{ID: "wait-id", Name: "tofi_wait", Arguments: `{}`}}
	e := &engine{provider: p, model: "test"}
	called := 0
	const output = "```text\nreference material\n```"
	_, err := e.Run(context.Background(), Request{BotID: "bot", RunID: "run", Tools: []Tool{{
		Name: "tofi_wait", Execute: func(context.Context, json.RawMessage) (string, error) {
			called++
			return output, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || len(p.requests) != 2 {
		t.Fatalf("handler calls = %d; provider calls = %d", called, len(p.requests))
	}
	last := p.requests[1].Messages[len(p.requests[1].Messages)-1]
	if last.ToolCallID != "wait-id" || last.Content != output {
		t.Fatalf("declared tool result changed = %#v", last)
	}
}
