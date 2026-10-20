package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// stagedProvider records which tools each model call declared and walks a
// scripted list of tool calls.
type stagedProvider struct {
	mu       sync.Mutex
	script   [][]provider.ToolCall
	declared [][]string
}

func (p *stagedProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var names []string
	for _, t := range req.Tools {
		names = append(names, t.Name)
	}
	p.declared = append(p.declared, names)
	if n := len(p.declared) - 1; n < len(p.script) {
		return &provider.ChatResponse{ToolCalls: p.script[n]}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}
func (p *stagedProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// A Deferred tool is absent from the declared list until another tool of the
// run activates it; from then on it is declared and executes.
func TestDeferredToolIsDeclaredOnlyAfterActivation(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &stagedProvider{script: [][]provider.ToolCall{
		{{ID: "c1", Name: "loader", Arguments: `{}`}},
		{{ID: "c2", Name: "rare", Arguments: `{}`}},
	}}
	obj := map[string]any{"type": "object", "properties": map[string]any{}}
	var ran int
	rare := Tool{Name: "rare", Parameters: obj, Deferred: true, Execute: func(context.Context, json.RawMessage) (string, error) { ran++; return "ok", nil }}
	loader := Tool{Name: "loader", Parameters: obj, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		got := ActivateTools(ctx, "rare", "missing", "loader")
		if len(got) != 1 || got[0] != "rare" {
			t.Errorf("activated %v, want only the deferred tool", got)
		}
		return "loaded", nil
	}}
	if _, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), Request{RunID: "deferred-1", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{loader, rare}}); err != nil {
		t.Fatal(err)
	}
	if len(p.declared) < 3 {
		t.Fatalf("model calls = %d", len(p.declared))
	}
	if has(p.declared[0], "rare") || !has(p.declared[0], "loader") {
		t.Fatalf("first call declared %v", p.declared[0])
	}
	if !has(p.declared[1], "rare") {
		t.Fatalf("second call declared %v, want the activated tool", p.declared[1])
	}
	if ran != 1 {
		t.Fatalf("deferred tool ran %d times", ran)
	}
}
