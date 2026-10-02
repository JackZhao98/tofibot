package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/paths"
	"github.com/JackZhao98/tofibot/internal/provider"
)

type thresholdSuspendProvider struct{ calls int }

func (p *thresholdSuspendProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *thresholdSuspendProvider) ChatStream(_ context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	p.calls++
	switch {
	case p.calls < 15:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("tick-%d", p.calls), Name: "tick", Arguments: `{}`}}}, nil
	case p.calls == 15:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "wait", Name: "ask", Arguments: `{}`}}}, nil
	case p.calls == 16:
		foundReminder := false
		for _, message := range req.Messages {
			if strings.Contains(message.Content, "user-visible progress update") {
				foundReminder = true
			}
		}
		if !foundReminder {
			return nil, errors.New("missing report reminder after resume")
		}
		return &provider.ChatResponse{Content: "I reached the waiting point and will continue.", ToolCalls: []provider.ToolCall{{ID: "after-report", Name: "tick", Arguments: `{}`}}}, nil
	default:
		return &provider.ChatResponse{Content: "Done."}, nil
	}
}

func TestRunResumesAtReportingThreshold(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &thresholdSuspendProvider{}
	e := &engine{provider: p, model: "test-model"}
	var checkpoint json.RawMessage
	toolCalls, reports := 0, 0
	req := Request{
		RunID: "threshold-suspend", BotID: "bot", Messages: []Message{{Role: "user", Content: "task"}},
		Tools: []Tool{
			{Name: "tick", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) { toolCalls++; return "ok", nil }},
			{Name: "ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return "", SuspendForUserInput(ctx, "question-1")
			}},
		},
		OnAssistantTurn: func(int, string) error { reports++; return nil },
		OnSuspend: func(_ string, raw json.RawMessage) error {
			checkpoint = append(json.RawMessage(nil), raw...)
			return nil
		},
	}
	paused, err := e.Run(context.Background(), req)
	if err != nil || !paused.Suspended || toolCalls != 14 || len(checkpoint) == 0 {
		t.Fatalf("paused=%+v err=%v calls=%d checkpoint=%q", paused, err, toolCalls, checkpoint)
	}
	c, err := decodeContinuation(checkpoint, req, "test-model")
	if err != nil || c.ToolCallsSinceReport != 15 || !c.ReportRequired {
		t.Fatalf("checkpoint=%+v err=%v", c, err)
	}
	req.Continuation, req.ResumeResult = checkpoint, "continue"
	finished, err := e.Run(context.Background(), req)
	if err != nil || finished.Content != "Done." || toolCalls != 15 || reports != 1 {
		t.Fatalf("finished=%+v err=%v calls=%d reports=%d", finished, err, toolCalls, reports)
	}
}

// suspensionBatchProvider is deliberately in-process. Its second response is
// a fresh tool decision, proving the resumed loop did not execute a stale
// call merely because it was present in the suspended assistant batch.
type suspensionBatchProvider struct {
	mu       sync.Mutex
	calls    int
	requests []provider.ChatRequest
}

func (p *suspensionBatchProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *suspensionBatchProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	copyReq := *req
	copyReq.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, copyReq)
	p.calls++
	switch p.calls {
	case 1:
		return &provider.ChatResponse{
			Content: "first public turn",
			ToolCalls: []provider.ToolCall{
				{ID: "before-id", Name: "before", Arguments: `{"record":"before"}`},
				{ID: "wait-id", Name: "ask", Arguments: `{"question":"continue?"}`},
				{ID: "stale-id", Name: "stale", Arguments: `{"side_effect":true}`},
			},
			Usage: provider.Usage{InputTokens: 11, OutputTokens: 3},
		}, nil
	case 2:
		return &provider.ChatResponse{
			Content:   "new decision after answer",
			ToolCalls: []provider.ToolCall{{ID: "fresh-id", Name: "fresh", Arguments: `{}`}},
			Usage:     provider.Usage{InputTokens: 13, OutputTokens: 4},
		}, nil
	case 3:
		return &provider.ChatResponse{Content: "final", Usage: provider.Usage{InputTokens: 17, OutputTokens: 5}}, nil
	default:
		return nil, errors.New("unexpected provider call")
	}
}

func suspensionTools(before, stale, fresh *int) []Tool {
	return []Tool{
		{Name: "before", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			*before++
			return "before completed", nil
		}},
		{Name: "ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if !CanSuspend(ctx) {
				return "", errors.New("suspension control missing")
			}
			return "", SuspendForUserInput(ctx, "question-1")
		}},
		{Name: "stale", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			*stale++
			return "stale side effect", nil
		}},
		{Name: "fresh", Parameters: map[string]any{"type": "object"}, Execute: func(context.Context, json.RawMessage) (string, error) {
			*fresh++
			return "fresh completed", nil
		}},
	}
}

func TestRunSuspendsAndResumesWithoutReplayingBatch(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &suspensionBatchProvider{}
	e := &engine{provider: p, model: "test-model"}
	before, stale, fresh := 0, 0, 0
	var checkpoint json.RawMessage
	var events []ToolEvent
	var turns []string
	request := Request{
		RunID:    "run-suspend",
		BotID:    "bot-suspend",
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "initial request"}},
		Tools:    suspensionTools(&before, &stale, &fresh),
		OnToolEvent: func(event ToolEvent) error {
			events = append(events, event)
			return nil
		},
		OnAssistantTurn: func(index int, content string) error {
			turns = append(turns, string(rune('0'+index))+":"+content)
			return nil
		},
		OnSuspend: func(questionID string, raw json.RawMessage) error {
			if questionID != "question-1" {
				t.Fatalf("question id = %q", questionID)
			}
			checkpoint = append(json.RawMessage(nil), raw...)
			return nil
		},
	}

	paused, err := e.Run(context.Background(), request)
	if err != nil || !paused.Suspended || paused.Content != "" || paused.InputTokens != 11 || paused.OutputTokens != 3 {
		t.Fatalf("paused result=%+v err=%v", paused, err)
	}
	if before != 1 || stale != 0 || fresh != 0 || len(checkpoint) == 0 {
		t.Fatalf("execution before=%d stale=%d fresh=%d checkpoint=%q", before, stale, fresh, checkpoint)
	}
	continuation, err := decodeContinuation(checkpoint, request, "test-model")
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	if continuation.LLMCalls != 1 || continuation.TotalUsage != (provider.Usage{InputTokens: 11, OutputTokens: 3}) || continuation.AssistantTurnIndex != 1 || continuation.ToolCallsSinceReport != 2 || continuation.ReportRequired {
		t.Fatalf("checkpoint counters = %+v", continuation)
	}
	if len(continuation.Messages) != 3 || len(continuation.Messages[1].ToolCalls) != 3 || continuation.Messages[2].ToolCallID != "before-id" {
		t.Fatalf("checkpoint transcript = %#v", continuation.Messages)
	}

	request.Continuation = checkpoint
	request.ResumeResult = "the user answered yes"
	request.Messages = []Message{{Role: "user", Content: "must not replace checkpoint history"}}
	completed, err := e.Run(context.Background(), request)
	if err != nil || completed.Suspended || completed.Content != "final" || completed.InputTokens != 41 || completed.OutputTokens != 12 {
		t.Fatalf("resumed result=%+v err=%v", completed, err)
	}
	if before != 1 || stale != 0 || fresh != 1 {
		t.Fatalf("side effects before=%d stale=%d fresh=%d", before, stale, fresh)
	}
	if !reflect.DeepEqual(turns, []string{"1:first public turn", "2:new decision after answer"}) {
		t.Fatalf("assistant turns=%#v", turns)
	}
	p.mu.Lock()
	secondRequest := p.requests[1]
	p.mu.Unlock()
	if len(secondRequest.Messages) != 5 || secondRequest.Messages[0].Content != "initial request" || secondRequest.Messages[3].ToolCallID != "wait-id" || secondRequest.Messages[3].Content != "the user answered yes" || secondRequest.Messages[4].ToolCallID != "stale-id" || !strings.Contains(secondRequest.Messages[4].Content, "execution skipped") {
		t.Fatalf("resume transcript = %#v", secondRequest.Messages)
	}

	gotLifecycle := make([]string, 0, len(events))
	for _, event := range events {
		gotLifecycle = append(gotLifecycle, event.CallID+":"+event.Status)
	}
	wantLifecycle := []string{
		"before-id:queued", "wait-id:queued", "stale-id:queued",
		"before-id:running", "before-id:completed", "wait-id:running",
		"wait-id:completed", "stale-id:failed",
		"fresh-id:queued", "fresh-id:running", "fresh-id:completed",
	}
	if !reflect.DeepEqual(gotLifecycle, wantLifecycle) {
		t.Fatalf("tool lifecycle=%#v, want %#v", gotLifecycle, wantLifecycle)
	}
}

func TestSuspendCallbackFailureAbortsWithoutReplayingTools(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &suspensionBatchProvider{}
	e := &engine{provider: p, model: "test-model"}
	before, stale, fresh := 0, 0, 0
	callbackErr := errors.New("checkpoint write failed")
	_, err := e.Run(context.Background(), Request{
		RunID:     "run-callback-failure",
		BotID:     "bot",
		Model:     "test-model",
		Messages:  []Message{{Role: "user", Content: "initial"}},
		Tools:     suspensionTools(&before, &stale, &fresh),
		OnSuspend: func(string, json.RawMessage) error { return callbackErr },
	})
	if !errors.Is(err, callbackErr) || before != 1 || stale != 0 || fresh != 0 || p.calls != 1 {
		t.Fatalf("error=%v before=%d stale=%d fresh=%d calls=%d", err, before, stale, fresh, p.calls)
	}
}

func TestContinuationRejectsMalformedAndCrossRunCheckpoint(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &suspensionBatchProvider{}
	e := &engine{provider: p, model: "test-model"}
	before, stale, fresh := 0, 0, 0
	var checkpoint json.RawMessage
	request := Request{
		RunID:    "run-original",
		BotID:    "bot",
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "initial"}},
		Tools:    suspensionTools(&before, &stale, &fresh),
		OnSuspend: func(_ string, raw json.RawMessage) error {
			checkpoint = append(json.RawMessage(nil), raw...)
			return nil
		},
	}
	if result, err := e.Run(context.Background(), request); err != nil || !result.Suspended {
		t.Fatalf("initial suspension result=%+v err=%v", result, err)
	}
	callsBeforeInvalid := p.calls
	request.Continuation = json.RawMessage(`{`)
	if _, err := e.Run(context.Background(), request); err == nil || p.calls != callsBeforeInvalid {
		t.Fatalf("malformed continuation error=%v calls=%d", err, p.calls)
	}
	request.Continuation = checkpoint
	request.RunID = "run-other"
	if _, err := e.Run(context.Background(), request); err == nil || !strings.Contains(err.Error(), "run identity") || p.calls != callsBeforeInvalid {
		t.Fatalf("cross-run continuation error=%v calls=%d", err, p.calls)
	}
}

func TestSuspendPreservesCallerCancellation(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &suspensionBatchProvider{}
	e := &engine{provider: p, model: "test-model"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before, stale, fresh := 0, 0, 0
	called := false
	_, err := e.Run(ctx, Request{
		RunID:    "run-cancel",
		BotID:    "bot",
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "initial"}},
		Tools:    suspensionTools(&before, &stale, &fresh),
		OnSuspend: func(string, json.RawMessage) error {
			called = true
			cancel()
			return nil
		},
	})
	if !called || !errors.Is(err, context.Canceled) || before != 1 || stale != 0 || fresh != 0 || p.calls != 1 {
		t.Fatalf("called=%v err=%v before=%d stale=%d fresh=%d calls=%d", called, err, before, stale, fresh, p.calls)
	}
}

type multipleWaitProvider struct{ calls int }

func (p *multipleWaitProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *multipleWaitProvider) ChatStream(ctx context.Context, _ *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.calls++
	switch p.calls {
	case 1:
		return &provider.ChatResponse{Content: "first wait", ToolCalls: []provider.ToolCall{{ID: "wait-one", Name: "ask", Arguments: `{"question_id":"q1"}`}}, Usage: provider.Usage{InputTokens: 2, OutputTokens: 1}}, nil
	case 2:
		return &provider.ChatResponse{Content: "second wait", ToolCalls: []provider.ToolCall{{ID: "wait-two", Name: "ask", Arguments: `{"question_id":"q2"}`}}, Usage: provider.Usage{InputTokens: 3, OutputTokens: 1}}, nil
	case 3:
		return &provider.ChatResponse{Content: "final", Usage: provider.Usage{InputTokens: 4, OutputTokens: 1}}, nil
	default:
		return nil, errors.New("unexpected provider call")
	}
}

func TestMultipleSuspensionsPreserveCountersTurnsAndActiveBudget(t *testing.T) {
	paths.SetTofiHome(t.TempDir())
	p := &multipleWaitProvider{}
	// The human interval is intentionally longer than the full active budget.
	// A restored wall-clock start would wrap up before the second suspension.
	e := &engine{provider: p, model: "test-model", config: Config{MaxDuration: 100 * time.Millisecond}}
	var checkpoint json.RawMessage
	var turns []string
	request := Request{
		RunID:    "run-multiple-waits",
		BotID:    "bot",
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "ask twice"}},
		Tools: []Tool{{Name: "ask", Parameters: map[string]any{"type": "object"}, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				QuestionID string `json:"question_id"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", err
			}
			return "", SuspendForUserInput(ctx, args.QuestionID)
		}}},
		OnAssistantTurn: func(index int, content string) error {
			turns = append(turns, string(rune('0'+index))+":"+content)
			return nil
		},
		OnSuspend: func(_ string, raw json.RawMessage) error {
			checkpoint = append(json.RawMessage(nil), raw...)
			return nil
		},
	}
	first, err := e.Run(context.Background(), request)
	if err != nil || !first.Suspended || first.InputTokens != 2 || first.OutputTokens != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	// This is human waiting time, not active agent execution time.
	time.Sleep(150 * time.Millisecond)
	request.Continuation, request.ResumeResult = checkpoint, "answer one"
	second, err := e.Run(context.Background(), request)
	if err != nil || !second.Suspended || second.InputTokens != 5 || second.OutputTokens != 2 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	secondContinuation, err := decodeContinuation(checkpoint, request, "test-model")
	if err != nil {
		t.Fatal(err)
	}
	if secondContinuation.LLMCalls != 2 || secondContinuation.AssistantTurnIndex != 2 || secondContinuation.ModelUsage["test-model"].APICallCount != 2 {
		t.Fatalf("second checkpoint counters=%+v", secondContinuation)
	}
	request.Continuation, request.ResumeResult = checkpoint, "answer two"
	completed, err := e.Run(context.Background(), request)
	if err != nil || completed.Suspended || completed.Content != "final" || completed.InputTokens != 9 || completed.OutputTokens != 3 {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if !reflect.DeepEqual(turns, []string{"1:first wait", "2:second wait"}) || p.calls != 3 {
		t.Fatalf("turns=%#v calls=%d", turns, p.calls)
	}
}
