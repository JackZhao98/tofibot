package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func failWith(err error) func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, err
	}
}

// The test machine's flag (2026-10-23 and 2026-11-16): an in-stream
// response.failed before any output. A person's retry passed both times.
var codexFlag = &provider.ResponseFailedError{Code: "invalid_prompt", Message: "Invalid prompt: your prompt was flagged as potentially violating our usage policy. Please try again with a different prompt"}

func TestFlaggedPromptIsResentOncePerRun(t *testing.T) {
	old := promptFlagRetryDelay
	promptFlagRetryDelay = time.Millisecond
	defer func() { promptFlagRetryDelay = old }()

	http400 := &provider.APIError{StatusCode: 400, Provider: "openai", Body: `{"error":{"message":"Invalid prompt: your prompt was flagged as potentially violating our usage policy.","code":"invalid_prompt"}}`}
	for name, flag := range map[string]error{"codex in-stream": codexFlag, "openai http 400": http400} {
		t.Run(name+" passes on the re-send", func(t *testing.T) {
			p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){failWith(flag), reply(provider.ChatResponse{Content: "done"})}}
			waits := 0
			ctx := provider.WithRetryObserver(context.Background(), func(int, error, time.Duration) { waits++ })
			result, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", Ctx: ctx})
			if err != nil || result.Content != "done" || len(p.requests) != 2 || waits != 1 {
				t.Fatalf("result=%+v err=%v calls=%d waits=%d", result, err, len(p.requests), waits)
			}
			if len(p.requests[1].Messages) != len(p.requests[0].Messages) {
				t.Fatalf("the re-send added to the transcript: %d then %d messages", len(p.requests[0].Messages), len(p.requests[1].Messages))
			}
		})
	}

	t.Run("a second flag ends the run and says it was retried", func(t *testing.T) {
		p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){failWith(codexFlag), failWith(codexFlag)}}
		_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go"})
		if err == nil || len(p.requests) != 2 || !strings.Contains(err.Error(), provider.PromptFlaggedAgain) || !provider.IsContentFlagged(err.Error()) {
			t.Fatalf("err=%v calls=%d", err, len(p.requests))
		}
	})

	t.Run("once per run, not once per call", func(t *testing.T) {
		call := provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "x", Name: "lookup", Arguments: `{}`}}}
		p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){failWith(codexFlag), reply(call), failWith(codexFlag)}}
		_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
		if err == nil || len(p.requests) != 3 || !strings.Contains(err.Error(), provider.PromptFlaggedAgain) {
			t.Fatalf("err=%v calls=%d", err, len(p.requests))
		}
	})

	// Anthropic blocks can come after streamed text, and the same context
	// tends to be blocked again: the person retries from the card instead.
	for name, block := range map[string]error{
		"anthropic refusal":      &provider.RefusalError{Provider: "anthropic", Category: "cyber"},
		"anthropic output block": &provider.APIError{StatusCode: 400, Provider: "anthropic", Body: `{"type":"error","error":{"type":"invalid_request_error","message":"Output blocked by content filtering policy"}}`},
	} {
		t.Run(name+" is not re-sent", func(t *testing.T) {
			p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){failWith(block), reply(provider.ChatResponse{Content: "done"})}}
			_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go"})
			if err == nil || len(p.requests) != 1 || !provider.IsContentFlagged(err.Error()) {
				t.Fatalf("err=%v calls=%d", err, len(p.requests))
			}
		})
	}
}
