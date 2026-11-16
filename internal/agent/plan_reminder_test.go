package agent

import (
	"context"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func planReminderRun(t *testing.T, names ...string) *scriptedProvider {
	t.Helper()
	var steps []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error)
	for i, name := range names {
		steps = append(steps, reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: string(rune('a' + i)), Name: name, Arguments: `{}`}}}))
	}
	steps = append(steps, reply(provider.ChatResponse{Content: "done"}))
	p := &scriptedProvider{steps: steps}
	handler := func(context.Context, map[string]interface{}) (string, error) { return "ok", nil }
	tool := func(name string) ExtraBuiltinTool {
		return ExtraBuiltinTool{Schema: provider.Tool{Name: name, Parameters: map[string]any{"type": "object"}}, HandlerCtx: handler}
	}
	if _, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", PlanReminder: "PLAN-REMINDER", PlanReminderAfter: 3, ToolsOnly: true, StructuredOutput: true,
		OnAssistantTurn: func(int, string) {}, ExtraTools: []ExtraBuiltinTool{tool("lookup"), tool("send_message"), tool("update_plan")}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func reminderCount(p *scriptedProvider) (requests int, first int) {
	first = -1
	for i, req := range p.requests {
		for _, m := range req.Messages {
			if m.Role == "user" && m.Content == "PLAN-REMINDER" {
				requests++
				if first < 0 {
					first = i
				}
				break
			}
		}
	}
	return requests, first
}

// The harness says the plan reminder once, after the third tool call that is
// work (say is not), and never to a run that already keeps a list.
func TestPlanReminderComesOnceAfterThreeCalls(t *testing.T) {
	p := planReminderRun(t, "lookup", "send_message", "lookup", "lookup", "lookup")
	n, first := reminderCount(p)
	if first != 4 {
		t.Fatalf("the reminder reaches the request after the third work call: first at request %d", first)
	}
	if last := p.requests[len(p.requests)-1]; countUser(last, "PLAN-REMINDER") != 1 {
		t.Fatalf("the reminder is said once: %d requests carry it, the last %d times", n, countUser(last, "PLAN-REMINDER"))
	}
	if n, _ := reminderCount(planReminderRun(t, "update_plan", "lookup", "lookup", "lookup", "lookup")); n != 0 {
		t.Fatal("a run that keeps a list gets no reminder")
	}
	if n, _ := reminderCount(planReminderRun(t, "lookup", "lookup")); n != 0 {
		t.Fatal("two calls are no reason for a list")
	}
	// Searches the provider ran itself count: one say turn that searched three times is enough.
	searched := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "a", Name: "send_message", Arguments: `{}`}}, WebSearches: []provider.WebSearch{{}, {}, {}}}),
		reply(provider.ChatResponse{Content: "done"}),
	}}
	handler := func(context.Context, map[string]interface{}) (string, error) { return "ok", nil }
	if _, err := runLoop(t, AgentConfig{Provider: searched, Prompt: "go", PlanReminder: "PLAN-REMINDER", PlanReminderAfter: 3, ToolsOnly: true, StructuredOutput: true, OnAssistantTurn: func(int, string) {},
		ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "send_message", Parameters: map[string]any{"type": "object"}}, HandlerCtx: handler}}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := reminderCount(searched); n != 1 {
		t.Fatalf("provider-side searches count toward the reminder: %d", n)
	}
}

func countUser(req provider.ChatRequest, text string) int {
	n := 0
	for _, m := range req.Messages {
		if m.Role == "user" && m.Content == text {
			n++
		}
	}
	return n
}
