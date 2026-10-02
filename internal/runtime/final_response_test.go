package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

type finalReviewProvider struct {
	requests []provider.ChatRequest
}

func (p *finalReviewProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *finalReviewProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, onDelta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copy := *req
	copy.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, copy)
	var response provider.ChatResponse
	switch len(p.requests) {
	case 1:
		response.Content = "<think>private</think>draft"
	case 2:
		response.Content = "checking"
		response.ToolCalls = []provider.ToolCall{{ID: "receipt-call", Name: "record_result", Arguments: `{}`}}
	case 3:
		response.Content = "final"
	default:
		return nil, errors.New("unexpected extra provider call")
	}
	if onDelta != nil {
		onDelta(provider.StreamDelta{Content: response.Content})
	}
	return &response, nil
}

func TestBeforeFinalResponseRuntimeSettlesStreamBeforeRepairTools(t *testing.T) {
	p := &finalReviewProvider{}
	e := &engine{provider: p, model: "test-model"}
	var order []string
	var events []ToolEvent
	stream := ""
	reviews := 0
	result, err := e.Run(context.Background(), Request{
		RunID: "synthetic-final-review", BotID: "synthetic", Messages: []Message{{Role: "user", Content: "verify"}},
		Tools: []Tool{{Name: "record_result", Execute: func(context.Context, json.RawMessage) (string, error) {
			order = append(order, "execute")
			return "recorded", nil
		}}},
		BeforeFinalResponse: func(content string) (string, error) {
			reviews++
			if content != "draft" {
				t.Fatalf("review content = %q", content)
			}
			order = append(order, "review")
			return "Reassess the unfinished requirement.", nil
		},
		BeforeModelCall: func() error { order = append(order, "boundary"); return nil },
		OnDelta:         func(delta string) { stream += delta; order = append(order, "delta:"+delta) },
		OnAssistantTurn: func(index int, content string) error {
			if stream != content {
				t.Fatalf("turn %d stream=%q content=%q; draft not settled exactly once", index, stream, content)
			}
			stream = ""
			order = append(order, fmt.Sprintf("turn:%d:%s", index, content))
			return nil
		},
		OnToolEvent: func(event ToolEvent) error {
			events = append(events, event)
			order = append(order, "tool:"+event.Status)
			return nil
		},
	})
	if err != nil || result.Content != "final" || stream != "final" || reviews != 1 {
		t.Fatalf("result=%+v err=%v stream=%q reviews=%d", result, err, stream, reviews)
	}
	wantOrder := []string{"boundary", "delta:draft", "review", "turn:1:draft", "boundary", "delta:checking", "turn:2:checking", "tool:queued", "tool:running", "execute", "tool:completed", "boundary", "delta:final"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("order=%#v, want %#v", order, wantOrder)
	}
	if len(events) != 3 {
		t.Fatalf("events=%#v", events)
	}
	for _, event := range events {
		if event.CallID != "receipt-call" || event.Name != "record_result" {
			t.Fatalf("incorrect tool event: %+v", event)
		}
	}
	if events[2].Result != "recorded" {
		t.Fatalf("completion=%+v", events[2])
	}
	if len(p.requests) != 3 || len(p.requests[1].Messages) != 3 || p.requests[1].Messages[2].Content != "Reassess the unfinished requirement." {
		t.Fatalf("repair requests=%#v", p.requests)
	}
}

func TestBeforeFinalResponseRuntimeCallbackFailureStopsRepair(t *testing.T) {
	for _, stage := range []string{"review", "promotion", "cancelled_review"} {
		t.Run(stage, func(t *testing.T) {
			p := &finalReviewProvider{}
			e := &engine{provider: p, model: "test-model"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callbackErr := errors.New("synthetic callback failure")
			wantErr := callbackErr
			if stage == "cancelled_review" {
				wantErr = context.Canceled
			}
			turns, events := 0, 0
			_, err := e.Run(ctx, Request{
				RunID: "synthetic-final-review-error", BotID: "synthetic",
				BeforeFinalResponse: func(string) (string, error) {
					if stage == "review" {
						return "", callbackErr
					}
					if stage == "cancelled_review" {
						cancel()
						return "", context.Canceled
					}
					return "Reassess.", nil
				},
				OnAssistantTurn: func(int, string) error { turns++; return callbackErr },
				OnToolEvent:     func(ToolEvent) error { events++; return nil },
			})
			if !errors.Is(err, wantErr) || len(p.requests) != 1 || events != 0 {
				t.Fatalf("err=%v calls=%d events=%d", err, len(p.requests), events)
			}
			wantTurns := 0
			if stage == "promotion" {
				wantTurns = 1
			}
			if turns != wantTurns {
				t.Fatalf("turns=%d, want %d", turns, wantTurns)
			}
		})
	}
}
