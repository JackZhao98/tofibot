package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// assertUsageInvariant checks what ValidateContinuation checks: LLMCalls and
// TotalUsage must agree with the per-model breakdown, or a run suspended for
// approval cannot be resumed.
func assertUsageInvariant(t *testing.T, result *AgentResult, model string, wantCalls int, wantIn, wantOut int64) {
	t.Helper()
	var calls int
	var in, out int64
	for _, usage := range result.ModelBreakdown {
		calls += usage.APICallCount
		in += usage.InputTokens
		out += usage.OutputTokens
	}
	if result.LLMCalls != wantCalls || calls != wantCalls || in != wantIn || out != wantOut ||
		result.TotalUsage.InputTokens != wantIn || result.TotalUsage.OutputTokens != wantOut {
		t.Fatalf("LLMCalls=%d breakdown calls=%d in=%d out=%d total=%+v; want calls=%d in=%d out=%d (model %s)",
			result.LLMCalls, calls, in, out, result.TotalUsage, wantCalls, wantIn, wantOut, model)
	}
	if usage, ok := result.ModelBreakdown[model]; !ok || usage.APICallCount != wantCalls {
		t.Fatalf("breakdown for %s = %+v, want %d calls", model, usage, wantCalls)
	}
}

// The overflow-recovery summarizer call is a model call: it must reach
// LLMCalls, TotalUsage, the per-model breakdown and OnUsage together.
func TestOverflowCompactionCallIsTracked(t *testing.T) {
	overflow := func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, provider.NewAPIError("openai", 400, "Your input exceeds the context window of this model.")
	}
	p := &scriptedProvider{steps: []scriptStep{
		overflow,
		reply(provider.ChatResponse{Content: "summary of earlier work", Usage: provider.Usage{InputTokens: 1000, OutputTokens: 50}}),
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			if !strings.Contains(req.Messages[0].Content, "summary of earlier work") {
				return nil, errors.New("retry did not use compacted context")
			}
			return &provider.ChatResponse{Content: "final", Usage: provider.Usage{InputTokens: 200, OutputTokens: 10}}, nil
		},
	}}
	history := []provider.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"}, {Role: "assistant", Content: "d"}, {Role: "user", Content: "e"}}
	var reported []int64
	var live provider.Usage
	result, err := runLoop(t, AgentConfig{Provider: p, Messages: history, LiveUsage: &live,
		OnUsage: func(in, out int64) { reported = append(reported, in, out) }})
	if err != nil || result.Content != "final" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertUsageInvariant(t, result, "test-model", 2, 1200, 60)
	if len(reported) != 4 || reported[0] != 1000 || reported[1] != 50 || reported[2] != 200 || reported[3] != 10 {
		t.Fatalf("OnUsage reports = %v", reported)
	}
	if live.InputTokens != 1200 || live.OutputTokens != 60 {
		t.Fatalf("LiveUsage = %+v", live)
	}
}

// The handoff compaction continues the real transcript, so it costs a full
// request. Its usage is tracked like any other call, and a replay rejection
// it reports switches replay off before the next real request.
func TestHandoffCompactionTracksUsageAndReplayRejection(t *testing.T) {
	// claude-opus-4-5: 200k window, compaction at 0.70. Three 220k-char tool
	// results estimate well past that, and history stays pinned (no micro
	// compaction) because the turns carry Anthropic thinking.
	messages := longTranscript(3, func(int) []provider.ReasoningItem { return anthropicThinking("why") })
	for i := range messages {
		if messages[i].Role == "tool" {
			messages[i].Content = strings.Repeat("row ", 55000)
		}
	}
	rejected := 0
	compacted := 0
	p := &scriptedProvider{steps: []scriptStep{
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			if ask := req.Messages[len(req.Messages)-1]; !strings.Contains(ask.Content, "Current line of thinking") {
				return nil, errors.New("first call was not the handoff request")
			}
			return &provider.ChatResponse{Content: "handoff", Usage: provider.Usage{InputTokens: 150000, OutputTokens: 800}, ReasoningReplayRejected: true}, nil
		},
		func(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			if !req.OmitReasoningReplay {
				return nil, errors.New("replay was still on after the handoff call was rejected")
			}
			if !strings.Contains(req.Messages[0].Content, "handoff") {
				return nil, errors.New("request did not use the handoff summary")
			}
			return &provider.ChatResponse{Content: "done", Usage: provider.Usage{InputTokens: 3000, OutputTokens: 20}}, nil
		},
	}}
	result, err := runLoop(t, AgentConfig{Provider: p, Model: "claude-opus-4-5", Messages: messages,
		OnReasoningReplayRejected: func() { rejected++ },
		OnCompact:                 func(int, int) { compacted++ },
		ExtraTools:                []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
	if err != nil || result.Content != "done" || compacted != 1 || rejected != 1 {
		t.Fatalf("result=%+v err=%v compacted=%d rejected=%d", result, err, compacted, rejected)
	}
	assertUsageInvariant(t, result, "claude-opus-4-5", 2, 153000, 820)
}

// A handoff call that fails or answers with tool calls still cost a request;
// both it and the flattened fallback are reported.
func TestFallbackCompactionReportsBothCalls(t *testing.T) {
	messages := longTranscript(3, func(int) []provider.ReasoningItem { return anthropicThinking("why") })
	p := &scriptedProvider{steps: []scriptStep{
		reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "x", Name: "lookup", Arguments: `{}`}}, Usage: provider.Usage{InputTokens: 500, OutputTokens: 5}}),
		reply(provider.ChatResponse{Content: "flat", Usage: provider.Usage{InputTokens: 400, OutputTokens: 40}}),
	}}
	got, err := compactTranscript(context.Background(), &AgentConfig{Provider: p, Model: "claude-opus-5"}, "SYS", nil, messages, false)
	if err != nil || got.Summary != "flat" || len(got.Calls) != 2 || got.Calls[0].InputTokens != 500 || got.Calls[1].InputTokens != 400 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	failing := &scriptedProvider{steps: []scriptStep{
		func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
			return nil, provider.NewAPIError("anthropic", 400, "prompt is too long: input exceeds the context window")
		},
		reply(provider.ChatResponse{Content: "flat", Usage: provider.Usage{InputTokens: 400, OutputTokens: 40}}),
	}}
	got, err = compactTranscript(context.Background(), &AgentConfig{Provider: failing, Model: "claude-opus-5"}, "SYS", nil, messages, false)
	if err != nil || got.Summary != "flat" || len(got.Calls) != 1 || got.Calls[0].InputTokens != 400 {
		t.Fatalf("after failed handoff: got=%+v err=%v", got, err)
	}
}
