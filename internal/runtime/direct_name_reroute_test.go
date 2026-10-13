package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type directCallProvider struct {
	name, args string
	calls      int
	mu         sync.Mutex
	history    []provider.Message
}

func (p *directCallProvider) Chat(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.history = req.Messages
	if p.calls == 1 {
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: p.name, Arguments: p.args}}}, nil
	}
	return &provider.ChatResponse{Content: "done"}, nil
}
func (p *directCallProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func runDirect(t *testing.T, name, args string, resolve func(context.Context, string) (string, bool)) (executed []string, events []ToolEvent, p *directCallProvider) {
	t.Helper()
	paths.SetTofiHome(t.TempDir())
	p = &directCallProvider{name: name, args: args}
	dispatcher := Tool{Name: "call_mcp_tool", Parameters: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}}, "required": []string{"name", "arguments"}}, ResolveDirectName: resolve, Execute: func(_ context.Context, raw json.RawMessage) (string, error) {
		executed = append(executed, string(raw))
		return "ok", nil
	}}
	_, err := (&engine{provider: p, model: "synthetic"}).Run(context.Background(), Request{RunID: "direct-" + name, BotID: "synthetic", Messages: []Message{{Role: "user", Content: "go"}}, Tools: []Tool{dispatcher}, OnToolEvent: func(e ToolEvent) error {
		events = append(events, e)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestDirectToolNameIsRoutedThroughDispatcher(t *testing.T) {
	resolve := func(_ context.Context, name string) (string, bool) {
		return name, name == "mcp_notion__notion-update-page"
	}
	executed, events, p := runDirect(t, "mcp_notion__notion-update-page", `{"page_id":"p1"}`, resolve)
	if len(executed) != 1 || executed[0] != `{"arguments":{"page_id":"p1"},"name":"mcp_notion__notion-update-page"}` {
		t.Fatalf("dispatcher got %v", executed)
	}
	for _, e := range events {
		if e.Name != "call_mcp_tool" {
			t.Fatalf("activity recorded as %q", e.Name)
		}
	}
	if len(events) == 0 || events[len(events)-1].Status != "completed" {
		t.Fatalf("events=%+v", events)
	}
	// The transcript the model sees is a normal call_mcp_tool call and result.
	var sawCall, sawResult bool
	for _, m := range p.history {
		for _, tc := range m.ToolCalls {
			sawCall = sawCall || tc.Name == "call_mcp_tool"
			if tc.Name != "call_mcp_tool" {
				t.Fatalf("history kept direct name %q", tc.Name)
			}
		}
		if m.Role == "tool" {
			sawResult = m.Content == "ok" && !m.ToolFailed
		}
	}
	if !sawCall || !sawResult {
		t.Fatalf("history=%+v", p.history)
	}
}

func TestDirectToolNameNotRoutedWhenUnresolvedOrNotObject(t *testing.T) {
	for name, tc := range map[string]struct {
		call, args string
		resolve    func(context.Context, string) (string, bool)
	}{
		"unresolved":     {"mcp_notion__other", `{}`, func(context.Context, string) (string, bool) { return "", false }},
		"array args":     {"mcp_notion__x", `[1]`, func(_ context.Context, n string) (string, bool) { return n, true }},
		"malformed args": {"mcp_notion__x", `{`, func(_ context.Context, n string) (string, bool) { return n, true }},
		"null args":      {"mcp_notion__x", `null`, func(_ context.Context, n string) (string, bool) { return n, true }},
	} {
		t.Run(name, func(t *testing.T) {
			executed, events, _ := runDirect(t, tc.call, tc.args, tc.resolve)
			var out *tooloutcome.Outcome
			for _, e := range events {
				if e.Status == "failed" {
					out = e.Outcome
				}
			}
			if len(executed) != 0 {
				t.Fatalf("dispatcher executed %v", executed)
			}
			if name == "unresolved" && (out == nil || out.Code != "tool_unavailable" || out.Certainty != "not_executed") {
				t.Fatalf("outcome=%+v", out)
			}
		})
	}
}
