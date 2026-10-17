package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

type cadenceFixture struct {
	structured bool
	notes      []string // tool name per scripted tool-call step
}

func (f cadenceFixture) run(t *testing.T) *scriptedProvider {
	t.Helper()
	var steps []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error)
	for i, name := range f.notes {
		id := string(rune('a' + i))
		steps = append(steps, reply(provider.ChatResponse{Content: "plain text between tools", ToolCalls: []provider.ToolCall{{ID: id, Name: name, Arguments: `{}`}}}))
	}
	steps = append(steps, reply(provider.ChatResponse{Content: "done"}))
	p := &scriptedProvider{steps: steps}
	handler := func(context.Context, map[string]interface{}) (string, error) { return "ok", nil }
	tool := func(name string) ExtraBuiltinTool {
		return ExtraBuiltinTool{Schema: provider.Tool{Name: name, Parameters: map[string]any{"type": "object"}}, HandlerCtx: handler}
	}
	if _, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", MaxToolCallsBetweenReports: 2, StructuredOutput: f.structured,
		OnAssistantTurn: func(int, string) {}, ExtraTools: []ExtraBuiltinTool{tool("lookup"), tool("say")}}); err != nil {
		t.Fatal(err)
	}
	return p
}

func sawReminder(p *scriptedProvider, text string) bool {
	for _, req := range p.requests {
		for _, m := range req.Messages {
			if m.Role == "user" && strings.Contains(m.Content, text) {
				return true
			}
		}
	}
	return false
}

// Under the protocol a plain text turn is private, so it does not count as the
// update the cadence asks for; a legacy visible turn does.
func TestPlainTextDoesNotSatisfyTheUpdateCadenceWhenStructured(t *testing.T) {
	structured := cadenceFixture{structured: true, notes: []string{"lookup", "lookup", "lookup"}}.run(t)
	if !sawReminder(structured, "say(note)") {
		t.Fatal("structured run was never asked for a say update")
	}
	legacy := cadenceFixture{structured: false, notes: []string{"lookup", "lookup", "lookup"}}.run(t)
	if sawReminder(legacy, "say(note)") || sawReminder(legacy, "last user-visible progress update") {
		t.Fatal("a visible plain text turn should have satisfied the legacy cadence")
	}
}

func TestSayCallSatisfiesTheUpdateCadence(t *testing.T) {
	p := cadenceFixture{structured: true, notes: []string{"lookup", "say", "lookup", "say", "lookup"}}.run(t)
	if sawReminder(p, "say(note)") {
		t.Fatal("a say call every other step still triggered the reminder")
	}
}

func TestStructuredPromptDropsTheTagGuidance(t *testing.T) {
	p := cadenceFixture{structured: true}.run(t)
	if sys := p.requests[0].System; strings.Contains(sys, "progress>") || !strings.Contains(sys, "say(note)") || !strings.Contains(sys, "private working text") {
		t.Fatalf("system suffix: %q", sys)
	}
	if got := StructuredReportPrompt(0); got != "" {
		t.Fatalf("no cadence, no suffix: %q", got)
	}
	if len([]rune(StructuredReportPrompt(15))) >= len([]rune(ProgressReportPrompt(15))) {
		t.Fatal("the structured suffix must be shorter than the tag guidance it replaces")
	}
}

// Streamed tool-call fragments reach the caller; a call aborted for its
// argument size is not forwarded past the cap.
func TestToolCallFragmentsAreForwarded(t *testing.T) {
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		func(_ context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "c", Name: "lookup"}}})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, Arguments: `{"q":`}}})
			delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, Arguments: `"x"}`}}})
			return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c", Name: "lookup", Arguments: `{"q":"x"}`}}}, nil
		},
		reply(provider.ChatResponse{Content: "done"}),
	}}
	var got []provider.ToolCallDelta
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}, OnToolCallDelta: func(d provider.ToolCallDelta) { got = append(got, d) },
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "lookup" || got[1].Arguments+got[2].Arguments != `{"q":"x"}` {
		t.Fatalf("fragments = %+v", got)
	}
}

func TestEmptyFinalEndsTheRunOnlyWhenTheCallerSaysSo(t *testing.T) {
	run := func(end func() bool) (*AgentResult, *scriptedProvider, error) {
		p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
			toolCallReply("c", "lookup", `{}`),
			reply(provider.ChatResponse{}),
			reply(provider.ChatResponse{Content: "after the nudge"}),
		}}
		res, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", EndOnEmptyFinal: end,
			ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
		return res, p, err
	}
	res, p, err := run(func() bool { return true })
	if err != nil || res.Content != "" || len(p.requests) != 2 {
		t.Fatalf("ended: result=%+v err=%v calls=%d", res, err, len(p.requests))
	}
	res, p, err = run(func() bool { return false })
	if err != nil || res.Content != "after the nudge" || len(p.requests) != 3 {
		t.Fatalf("nudged: result=%+v err=%v calls=%d", res, err, len(p.requests))
	}
}
