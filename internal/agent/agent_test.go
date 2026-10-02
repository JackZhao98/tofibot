package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type emptyResponseProvider struct {
	calls atomic.Int32
}

type budgetWrapProvider struct {
	calls int
}

type usageCallbackProvider struct{}

func (usageCallbackProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ready", Usage: provider.Usage{InputTokens: 42, OutputTokens: 5}}, nil
}
func (p usageCallbackProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func TestAgentContextUsageCallbacks(t *testing.T) {
	execCtx := models.NewExecutionContext("usage-callback", "bot", t.TempDir())
	defer execCtx.Cancel()
	estimated, actual, output := 0, int64(0), int64(0)
	_, err := RunAgentLoop(AgentConfig{Ctx: context.Background(), Provider: usageCallbackProvider{}, Model: "codex-gpt-6-sol", Prompt: "hello", ToolsOnly: true,
		OnContextEstimate: func(value int) { estimated = value }, OnUsage: func(input, out int64) { actual = input; output = out }}, execCtx)
	if err != nil || estimated <= 0 || actual != 42 || output != 5 {
		t.Fatalf("estimate=%d actual=%d output=%d err=%v", estimated, actual, output, err)
	}
}

func (p *budgetWrapProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	if p.calls == 1 {
		return &provider.ChatResponse{
			Content: "<think>private reasoning</think>agenda",
			ToolCalls: []provider.ToolCall{{
				ID: "discarded-call", Name: "publish", Arguments: `{}`,
			}},
		}, nil
	}
	return &provider.ChatResponse{Content: "final"}, nil
}

func (p *budgetWrapProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func (p *emptyResponseProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls.Add(1)
	return &provider.ChatResponse{}, nil
}

func (p *emptyResponseProvider) ChatStream(ctx context.Context, _ *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return p.Chat(ctx, nil)
}

func TestRunAgentLoopDoesNotUseUserMessageAfterEmptyResponses(t *testing.T) {
	p := &emptyResponseProvider{}
	execCtx := models.NewExecutionContext("run", "user", t.TempDir())
	defer execCtx.Cancel()

	result, err := RunAgentLoop(AgentConfig{
		Ctx:       context.Background(),
		Provider:  p,
		Model:     "test-model",
		Prompt:    "user input that must not become an answer",
		ToolsOnly: true,
	}, execCtx)
	if err == nil {
		t.Fatal("RunAgentLoop returned success after repeated empty responses")
	}
	if !strings.Contains(err.Error(), "maximum agent steps exceeded without a final response") {
		t.Fatalf("error = %v", err)
	}
	if result != nil {
		t.Fatalf("result = %#v, want nil on missing final response", result)
	}
	if got := p.calls.Load(); got != 30 {
		t.Fatalf("provider calls = %d, want 30 before max-step error", got)
	}
}

func TestAssistantTurnCallbackCoversBudgetWrapUp(t *testing.T) {
	p := &budgetWrapProvider{}
	execCtx := models.NewExecutionContext("run-budget", "user", t.TempDir())
	defer execCtx.Cancel()
	var gotIndex int
	var gotContent string
	called := 0
	result, err := RunAgentLoop(AgentConfig{
		Ctx:            context.Background(),
		Provider:       p,
		Model:          "test-model",
		Prompt:         "prepare",
		ToolsOnly:      true,
		MaxRunLLMCalls: 1,
		OnAssistantTurn: func(index int, content string) {
			called++
			gotIndex, gotContent = index, content
		},
	}, execCtx)
	if err != nil {
		t.Fatalf("RunAgentLoop error = %v", err)
	}
	if result.Content != "final" || p.calls != 2 {
		t.Fatalf("result=%q provider calls=%d, want final/2", result.Content, p.calls)
	}
	if called != 1 || gotIndex != 1 || gotContent != "agenda" {
		t.Fatalf("callback=(%d,%d,%q), want (1,1,agenda)", called, gotIndex, gotContent)
	}
}
