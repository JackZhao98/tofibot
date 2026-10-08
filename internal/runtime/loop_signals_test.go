package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// signalProvider streams reasoning, then a draft that is reviewed, then a tool
// call, then the final answer. It also reports one backoff through the
// request context the way RetryProvider does.
type signalProvider struct {
	requests []provider.ChatRequest
}

func (p *signalProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *signalProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, onDelta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	p.requests = append(p.requests, *req)
	switch len(p.requests) {
	case 1:
		retry := provider.NewRetryProvider(&flakyOnce{}, provider.RetryConfig{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond, OnRetry: func(int, error, time.Duration) {}})
		if _, err := retry.ChatStream(ctx, req, nil); err != nil {
			return nil, err
		}
		onDelta(provider.StreamDelta{Reasoning: "weighing sources"})
		onDelta(provider.StreamDelta{Content: "draft"})
		return &provider.ChatResponse{Content: "draft"}, nil
	case 2:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "read_mail", Arguments: `{"q":"unread"}`}}}, nil
	case 3:
		onDelta(provider.StreamDelta{Content: "final"})
		return &provider.ChatResponse{Content: "final"}, nil
	}
	return nil, errors.New("unexpected extra provider call")
}

type flakyOnce struct{ failed bool }

func (f *flakyOnce) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return f.ChatStream(ctx, req, nil)
}

func (f *flakyOnce) ChatStream(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if !f.failed {
		f.failed = true
		return nil, provider.NewAPIError("openai", 503, "busy")
	}
	return &provider.ChatResponse{}, nil
}

func TestRuntimeForwardsThinkingRetryReviewDraftAndRisk(t *testing.T) {
	p := &signalProvider{}
	e := &engine{provider: p, model: "test-model"}
	var thinking []string
	var retries []int
	var reviewDrafts, turns []string
	var events []ToolEvent
	reviews := 0
	result, err := e.Run(context.Background(), Request{
		RunID: "synthetic-signals", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "check mail"}},
		Tools: []Tool{{Name: "read_mail", Execute: func(context.Context, json.RawMessage) (string, error) { return "2 unread", nil },
			Identity: func(raw json.RawMessage) tooloutcome.Identity {
				i := tooloutcome.DefaultIdentity("read_mail", raw)
				i.Risk = tooloutcome.Observation
				return i
			}}},
		OnDelta:    func(string) {},
		OnThinking: func(delta string) { thinking = append(thinking, delta) },
		OnRetry:    func(attempt int, _ time.Duration) { retries = append(retries, attempt) },
		BeforeFinalResponse: func(string) (string, error) {
			reviews++
			if reviews == 1 {
				return "Review the draft.", nil
			}
			return "", nil
		},
		OnReviewDraft:   func(_ int, content string) error { reviewDrafts = append(reviewDrafts, content); return nil },
		OnAssistantTurn: func(_ int, content string) error { turns = append(turns, content); return nil },
		OnToolEvent:     func(ev ToolEvent) error { events = append(events, ev); return nil },
	})
	if err != nil || result.Content != "final" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(thinking) != 1 || thinking[0] != "weighing sources" || len(retries) != 1 || retries[0] != 1 {
		t.Fatalf("thinking=%v retries=%v", thinking, retries)
	}
	if len(reviewDrafts) != 1 || reviewDrafts[0] != "draft" || len(turns) != 0 {
		t.Fatalf("review drafts=%v turns=%v", reviewDrafts, turns)
	}
	if len(events) == 0 {
		t.Fatal("no tool events")
	}
	for _, ev := range events {
		if ev.Risk != tooloutcome.Observation {
			t.Fatalf("event risk = %q", ev.Risk)
		}
	}
	for _, req := range p.requests {
		if req.PromptCacheKey != "synthetic-signals" {
			t.Fatalf("prompt cache key = %q", req.PromptCacheKey)
		}
	}
}

func TestPromptCacheKeyPrefersConversation(t *testing.T) {
	if got := promptCacheKey(Request{RunID: "run-1", ConversationID: "conv-1"}); got != "conv-1" {
		t.Fatalf("key = %q", got)
	}
	if got := promptCacheKey(Request{RunID: "run-1"}); got != "run-1" {
		t.Fatalf("fallback key = %q", got)
	}
}
