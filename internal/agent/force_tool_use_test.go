package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func toolChoices(p *scriptedProvider) []string {
	var out []string
	for _, r := range p.requests {
		out = append(out, r.ToolChoice)
	}
	return out
}

func forceScript() []scriptStep {
	return []scriptStep{
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}),
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c2", Name: "lookup", Arguments: `{}`}}}),
		reply(provider.ChatResponse{Content: "done"}),
	}
}

func lookupOnce() ExtraBuiltinTool {
	return lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "row", nil })
}

// A structured run that asks for forced tool use requires a tool call on every
// ordinary request; a run that does not ask leaves the choice to the model.
func TestForceToolUseRequiresAToolCallOnOrdinaryRequests(t *testing.T) {
	forced := &scriptedProvider{steps: forceScript()}
	if _, err := runLoop(t, AgentConfig{Provider: forced, Prompt: "go", ForceToolUse: true, ExtraTools: []ExtraBuiltinTool{lookupOnce()}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(toolChoices(forced), ","); got != "any,any,any" {
		t.Fatalf("forced tool choices = %q", got)
	}
	free := &scriptedProvider{steps: forceScript()}
	if _, err := runLoop(t, AgentConfig{Provider: free, Prompt: "go", ExtraTools: []ExtraBuiltinTool{lookupOnce()}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(toolChoices(free), ","); got != ",," {
		t.Fatalf("unforced tool choices = %q", got)
	}
}

// A run that handed its work off owes no reply, so it may end with no tool call.
func TestForceToolUseStopsOnceTheRunHandedOff(t *testing.T) {
	handed := false
	p := &scriptedProvider{steps: []scriptStep{
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}),
		reply(provider.ChatResponse{}),
	}}
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ForceToolUse: true, EndOnEmptyFinal: func() bool { return handed },
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { handed = true; return "handed off", nil })}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(toolChoices(p), ","); got != "any," {
		t.Fatalf("tool choices = %q, want any then free", got)
	}
}

// The reserved repair and its final-only follow-up are never forced: their
// tools are restricted by the loop itself.
func TestForceToolUseSkipsTheReservedRepairRequests(t *testing.T) {
	p := &reservedFinalRepairProvider{}
	_, err := runFinalResponseTest(t, p, AgentConfig{
		MaxRunLLMCalls:           1,
		ForceToolUse:             true,
		FinalResponseRepairTools: []string{"complete_scheduled_task"},
		ExtraTools: []ExtraBuiltinTool{
			{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "unexpected", nil }},
			{Schema: provider.Tool{Name: "complete_scheduled_task", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "receipt recorded", nil }},
			{Schema: provider.Tool{Name: "external_action", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "unexpected", nil }},
		},
		BeforeFinalResponse: func(string) (string, error) { return "Record the required completion receipt.", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 4 {
		t.Fatalf("requests = %d", len(p.requests))
	}
	for i, r := range p.requests {
		// Request 0 is ordinary; 1 is the budget wrap-up (one call allowed);
		// 2 and 3 are the reserved repair and its final-only follow-up.
		if (i == 0) != (r.ToolChoice == provider.ToolChoiceAny) {
			t.Fatalf("request %d: tool choice %q", i, r.ToolChoice)
		}
	}
}

// A round in which every call failed frees the next request: forcing a model
// that cannot change its call only repeats it until the run budget is spent.
func TestForceToolUseIsReleasedAfterAFailedRound(t *testing.T) {
	fail := true
	p := &scriptedProvider{steps: []scriptStep{
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}),
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c2", Name: "lookup", Arguments: `{"again":true}`}}}),
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c3", Name: "lookup", Arguments: `{"third":true}`}}}),
		reply(provider.ChatResponse{Content: "done"}),
	}}
	calls := 0
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ForceToolUse: true,
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) {
			calls++
			if calls == 1 && fail {
				return "", errors.New("refused")
			}
			return "row", nil
		})}})
	if err != nil {
		t.Fatal(err)
	}
	// Request 2 follows the failed round; 3 and 4 follow a successful one.
	if got := strings.Join(toolChoices(p), ","); got != "any,,any,any" {
		t.Fatalf("tool choices = %q, want any,,any,any", got)
	}
}

func TestLastToolRoundFailed(t *testing.T) {
	tool := func(failed bool) provider.Message { return provider.Message{Role: "tool", ToolFailed: failed} }
	asst := provider.Message{Role: "assistant"}
	user := provider.Message{Role: "user"}
	for name, tc := range map[string]struct {
		msgs []provider.Message
		want bool
	}{
		"no tool results":           {[]provider.Message{user}, false},
		"all failed":                {[]provider.Message{user, asst, tool(true), tool(true)}, true},
		"one succeeded":             {[]provider.Message{user, asst, tool(true), tool(false)}, false},
		"older round failed":        {[]provider.Message{user, asst, tool(true), asst, tool(false)}, false},
		"reminder after the round":  {[]provider.Message{user, asst, tool(true), user}, true},
		"failed round, text answer": {[]provider.Message{user, asst, tool(true), asst}, false},
	} {
		if got := lastToolRoundFailed(tc.msgs); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

// At the budget wrap-up the model is asked to answer without tools: the request
// is not forced, and an answer given through say is the run's answer instead of
// being dropped with the rest of the tool calls.
func TestForceToolUseStopsAtTheBudgetWrapUp(t *testing.T) {
	p := &scriptedProvider{steps: []scriptStep{
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "lookup", Arguments: `{}`}}}),
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c2", Name: "say", Arguments: `{"purpose":"answer","text":"What I found before the budget ended."}`}}}),
	}}
	res, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ForceToolUse: true, StructuredOutput: true, MaxRunLLMCalls: 1,
		ExtraTools: []ExtraBuiltinTool{lookupOnce(), {Schema: provider.Tool{Name: "say", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "unexpected", nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Content != "What I found before the budget ended." {
		t.Fatalf("content = %q", res.Content)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests = %d", len(p.requests))
	}
	if got := toolChoices(p); got[0] != provider.ToolChoiceAny || got[1] == provider.ToolChoiceAny {
		t.Fatalf("tool choices = %q: the wrap-up request must not be forced", got)
	}
}
