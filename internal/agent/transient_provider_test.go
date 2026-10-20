package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func midStreamFailure(err error) func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return func(_ context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		delta(provider.StreamDelta{Content: "partial"})
		return nil, err
	}
}

func TestRetryableProviderErrorAfterOutputIsRetriedWithBackoff(t *testing.T) {
	old := transientProviderBackoff
	transientProviderBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	defer func() { transientProviderBackoff = old }()
	for name, failure := range map[string]error{
		"overloaded":    &provider.APIError{StatusCode: 529, Body: "overloaded_error"},
		"response.fail": &provider.ResponseFailedError{Code: "server_error", Message: "You can retry your request"},
	} {
		t.Run(name, func(t *testing.T) {
			p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
				midStreamFailure(failure), midStreamFailure(failure), reply(provider.ChatResponse{Content: "done"}),
			}}
			resets, waits := 0, 0
			ctx := provider.WithRetryObserver(context.Background(), func(int, error, time.Duration) { waits++ })
			result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", Ctx: ctx, OnStreamChunk: func(string, string) {}, OnStreamReset: func() { resets++ }})
			if err != nil || result.Content != "done" || len(p.requests) != 3 || resets != 2 || waits != 2 {
				t.Fatalf("result=%+v err=%v calls=%d resets=%d waits=%d", result, err, len(p.requests), resets, waits)
			}
		})
	}
}

func TestProviderQuotaAndExhaustedRetriesStillFail(t *testing.T) {
	old := transientProviderBackoff
	transientProviderBackoff = []time.Duration{time.Millisecond}
	defer func() { transientProviderBackoff = old }()
	quota := &provider.APIError{StatusCode: 429, Body: "usage_limit_reached"}
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){midStreamFailure(quota)}}
	if _, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}}); err == nil || len(p.requests) != 1 {
		t.Fatalf("quota must not retry: err=%v calls=%d", err, len(p.requests))
	}
	overload := &provider.APIError{StatusCode: 529, Body: "overloaded_error"}
	p = &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){midStreamFailure(overload), midStreamFailure(overload)}}
	if _, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}}); err == nil || len(p.requests) != 2 {
		t.Fatalf("retries must be bounded: err=%v calls=%d", err, len(p.requests))
	}
}

func TestWrapUpIgnoredIsStillExhausted(t *testing.T) {
	call := provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "x", Name: "lookup", Arguments: `{}`}}}
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){reply(call), reply(call)}}
	result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", MaxRunLLMCalls: 1, ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
	if err != nil || !result.BudgetExhausted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestLongSayAnswerIsNotAbortedAndOverlongSayGetsShorterNote(t *testing.T) {
	long := strings.Repeat("界", 20000)
	args := `{"text":"` + long + `"}`
	stream := func(ctx context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "s", Name: "say", Arguments: args}}})
		return &provider.ChatResponse{Content: "ok"}, nil
	}
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){stream}}
	result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}})
	if err != nil || result.Content != "ok" || len(p.requests) != 1 {
		t.Fatalf("a long say answer was aborted: result=%+v err=%v calls=%d", result, err, len(p.requests))
	}
	huge := `{"text":"` + strings.Repeat("a", maxBulkToolCallArgumentChars+10) + `"}`
	over := func(ctx context.Context, _ *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		delta(provider.StreamDelta{ToolCalls: []provider.ToolCallDelta{{Index: 0, ID: "s", Name: "say", Arguments: huge}}})
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p = &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){over, reply(provider.ChatResponse{Content: "short"})}}
	result, err = runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnStreamChunk: func(string, string) {}})
	if err != nil || result.Content != "short" || len(p.requests) != 2 {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, len(p.requests))
	}
	last := p.requests[1].Messages[len(p.requests[1].Messages)-1].Content
	if strings.Contains(last, "split") || !strings.Contains(last, "shorter") {
		t.Fatalf("recovery note for an over-long answer: %q", last)
	}
}
