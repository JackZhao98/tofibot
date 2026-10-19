package agent

import (
	"context"
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
		repairOrFinal := i >= 2
		if repairOrFinal && r.ToolChoice == provider.ToolChoiceAny {
			t.Fatalf("request %d (repair or final-only) was forced", i)
		}
		if !repairOrFinal && r.ToolChoice != provider.ToolChoiceAny {
			t.Fatalf("request %d (ordinary) was not forced: %q", i, r.ToolChoice)
		}
	}
}
