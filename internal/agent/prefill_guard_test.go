package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// Current Claude models reject a request whose last turn is the assistant's
// (assistant prefill is gone). Every path in the loop that builds a request is
// run here against a recording provider, and each recorded request is turned
// into Messages API turns exactly as the Anthropic provider does: the last
// turn must be a user turn (user text or a tool result).

type recordingStep struct {
	response provider.ChatResponse
	err      error
}

type recordingProvider struct {
	steps    []recordingStep
	requests [][]provider.Message
}

func (p *recordingProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *recordingProvider) ChatStream(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, append([]provider.Message(nil), req.Messages...))
	i := len(p.requests) - 1
	if i >= len(p.steps) {
		return nil, errors.New("unexpected extra provider call")
	}
	step := p.steps[i]
	if step.err != nil {
		return nil, step.err
	}
	response := step.response
	return &response, nil
}

func guardLookupTools() []ExtraBuiltinTool {
	return []ExtraBuiltinTool{{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) {
		return "evidence", nil
	}}}
}

func assertRequestsEndWithUser(t *testing.T, requests [][]provider.Message) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("no request was built")
	}
	for i, messages := range requests {
		for _, replay := range []bool{false, true} {
			roles := provider.AnthropicTurnRoles(messages, replay)
			if len(roles) == 0 || roles[len(roles)-1] != "user" {
				t.Fatalf("request %d (replay=%v) turns = %v, want the last to be a user turn", i, replay, roles)
			}
		}
	}
}

func TestEveryRequestPathEndsWithAUserTurn(t *testing.T) {
	tool := func(id string) provider.ToolCall { return provider.ToolCall{ID: id, Name: "lookup", Arguments: `{}`} }
	longHistory := []provider.Message{
		{Role: "user", Content: "one"}, {Role: "assistant", Content: "reply one"},
		{Role: "user", Content: "two"}, {Role: "assistant", Content: "reply two"},
		{Role: "user", Content: "three"}, {Role: "assistant", Content: "reply three"},
	}
	cases := []struct {
		name string
		cfg  AgentConfig
		step []recordingStep
	}{
		{
			name: "history ends with the Bot's own reply",
			cfg:  AgentConfig{Messages: []provider.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "earlier answer"}}},
			step: []recordingStep{{response: provider.ChatResponse{Content: "ok"}}},
		},
		{
			name: "history ends with a user turn that renders empty",
			cfg:  AgentConfig{Messages: []provider.Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "  "}}},
			step: []recordingStep{{response: provider.ChatResponse{Content: "ok"}}},
		},
		{
			name: "continuation after a demoted draft",
			cfg: AgentConfig{Prompt: "q", BeforeFinalResponse: func(string) (string, error) {
				return "Review the remaining requirement.", nil
			}},
			step: []recordingStep{{response: provider.ChatResponse{Content: "draft"}}, {response: provider.ChatResponse{Content: "final"}}},
		},
		{
			name: "budget wrap-up",
			cfg:  AgentConfig{Prompt: "q", MaxRunLLMCalls: 1, ExtraTools: guardLookupTools()},
			step: []recordingStep{{response: provider.ChatResponse{Content: "agenda", ToolCalls: []provider.ToolCall{tool("c1")}}}, {response: provider.ChatResponse{Content: "final"}}},
		},
		{
			name: "empty response prompts a continuation",
			cfg:  AgentConfig{Prompt: "q"},
			step: []recordingStep{{response: provider.ChatResponse{}}, {response: provider.ChatResponse{Content: "done"}}},
		},
		{
			name: "tool round",
			cfg:  AgentConfig{Prompt: "q", ExtraTools: guardLookupTools()},
			step: []recordingStep{{response: provider.ChatResponse{ToolCalls: []provider.ToolCall{tool("c1")}}}, {response: provider.ChatResponse{Content: "final"}}},
		},
		{
			name: "context overflow recovery compacts and retries",
			cfg:  AgentConfig{Messages: longHistory[:5]},
			step: []recordingStep{
				{err: provider.NewAPIError("anthropic", 400, "prompt is too long: 999999 tokens")},
				{response: provider.ChatResponse{Content: "summary of the work"}},
				{response: provider.ChatResponse{Content: "final"}},
			},
		},
		{
			name: "resume from a tool boundary checkpoint",
			cfg: AgentConfig{Continuation: boundaryBase([]provider.Message{
				{Role: "user", Content: "synthetic task"},
				{Role: "assistant", ToolCalls: []provider.ToolCall{tool("a")}},
				{Role: "tool", ToolCallID: "a", ToolName: "lookup", Content: "ok"},
			}, nil), ExtraTools: guardLookupTools()},
			step: []recordingStep{{response: provider.ChatResponse{Content: "final"}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &recordingProvider{steps: c.step}
			cfg := c.cfg
			cfg.Ctx, cfg.Provider, cfg.ToolsOnly, cfg.Model = context.Background(), p, true, "test-model"
			execCtx := models.NewExecutionContext("prefill-guard", "synthetic", t.TempDir())
			defer execCtx.Cancel()
			if _, err := RunAgentLoop(cfg, execCtx); err != nil {
				t.Fatalf("run: %v", err)
			}
			assertRequestsEndWithUser(t, p.requests)
		})
	}
}

func TestResumedInputWaitEndsWithAToolResult(t *testing.T) {
	waiting := continuationWithUsage(provider.Usage{InputTokens: 5, OutputTokens: 1}, ModelUsage{InputTokens: 5, OutputTokens: 1, APICallCount: 1}, 1)
	messages, err := resumeContinuation(waiting, "the answer")
	if err != nil {
		t.Fatal(err)
	}
	assertRequestsEndWithUser(t, [][]provider.Message{messages})
}
