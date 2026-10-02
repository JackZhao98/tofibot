package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/models"
	"github.com/JackZhao98/tofibot/internal/provider"
)

// All responses are scripted in-process: no provider transport or credentials.
type finalResponseProvider struct {
	responses []provider.ChatResponse
	requests  []provider.ChatRequest
	onReply   func(int)
}

func (p *finalResponseProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *finalResponseProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, delta func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copy := *req
	copy.Messages = append([]provider.Message(nil), req.Messages...)
	p.requests = append(p.requests, copy)
	i := len(p.requests) - 1
	if i >= len(p.responses) {
		return nil, errors.New("unexpected extra provider call")
	}
	response := p.responses[i]
	if delta != nil {
		delta(provider.StreamDelta{Content: response.Content})
	}
	if p.onReply != nil {
		p.onReply(i + 1)
	}
	return &response, nil
}

func runFinalResponseTest(t *testing.T, p provider.Provider, cfg AgentConfig) (*AgentResult, error) {
	t.Helper()
	cfg.Provider, cfg.ToolsOnly = p, true
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	execCtx := models.NewExecutionContext("final-review", "synthetic", t.TempDir())
	defer execCtx.Cancel()
	return RunAgentLoop(cfg, execCtx)
}

func TestBeforeFinalResponseRepairPreservesHistoryAndEvents(t *testing.T) {
	prior := []provider.Message{
		{Role: "user", Content: "verify"},
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "prior", Name: "lookup", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "prior", Content: "prior evidence"},
	}
	const draft = "<think>private</think>draft"
	p := &finalResponseProvider{responses: []provider.ChatResponse{
		{Content: draft},
		{Content: "checking", ToolCalls: []provider.ToolCall{{ID: "repair", Name: "lookup", Arguments: `{}`}}},
		{Content: "final"},
	}}
	var order []string
	var emitted []provider.Message
	reviews := 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		Messages: prior,
		ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) {
			order = append(order, "execute")
			return "new evidence", nil
		}}},
		BeforeFinalResponse: func(content string) (string, error) {
			reviews++
			if content != "draft" {
				t.Fatalf("review received unclean content %q", content)
			}
			order = append(order, "review")
			return "Review the remaining requirement.", nil
		},
		OnStreamChunk: func(_, delta string) { order = append(order, "delta:"+delta) },
		OnAssistantTurn: func(index int, content string) {
			order = append(order, fmt.Sprintf("turn:%d:%s", index, content))
		},
		OnMessage: func(message provider.Message) {
			emitted = append(emitted, message)
			order = append(order, "message:"+message.Role)
		},
	})
	if err != nil || result.Content != "final" || result.LLMCalls != 3 || reviews != 1 {
		t.Fatalf("result=%+v err=%v reviews=%d", result, err, reviews)
	}
	wantHistory := append(append([]provider.Message(nil), prior...), provider.Message{Role: "assistant", Content: draft}, provider.Message{Role: "user", Content: "Review the remaining requirement."})
	if !reflect.DeepEqual(p.requests[1].Messages, wantHistory) {
		t.Fatalf("repair history = %#v, want %#v", p.requests[1].Messages, wantHistory)
	}
	lastHistory := p.requests[2].Messages
	if len(lastHistory) != 7 || lastHistory[6].ToolCallID != "repair" || lastHistory[6].Content != "new evidence" {
		t.Fatalf("post-tool history = %#v", lastHistory)
	}
	wantOrder := []string{"delta:draft", "message:assistant", "review", "turn:1:draft", "delta:checking", "turn:2:checking", "message:assistant", "execute", "message:tool", "delta:final", "message:assistant"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("event order = %#v, want %#v", order, wantOrder)
	}
	if len(emitted) != 4 {
		t.Fatalf("emitted messages = %#v; reminder or draft duplicated", emitted)
	}
	for _, message := range emitted {
		if message.Role == "user" {
			t.Fatal("internal reminder leaked to OnMessage")
		}
	}
}

func TestBeforeFinalResponseAtMostOneRepair(t *testing.T) {
	p := &finalResponseProvider{responses: []provider.ChatResponse{{Content: "first"}, {Content: "still incomplete"}}}
	reviews, turns := 0, 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		BeforeFinalResponse: func(string) (string, error) { reviews++; return "Reassess.", nil },
		OnAssistantTurn:     func(int, string) { turns++ },
	})
	if err != nil || result.Content != "still incomplete" || reviews != 1 || turns != 1 || len(p.requests) != 2 {
		t.Fatalf("result=%+v err=%v reviews=%d turns=%d calls=%d", result, err, reviews, turns, len(p.requests))
	}
}

func TestBeforeFinalResponseCompletenessRepairContinuesMissingCategory(t *testing.T) {
	p := &finalResponseProvider{responses: []provider.ChatResponse{
		{Content: "Category A is complete."},
		{Content: "Checking category B.", ToolCalls: []provider.ToolCall{{ID: "category-b", Name: "research_category", Arguments: `{}`}}},
		{Content: "Category A and category B are complete."},
	}}
	reviews, researched := 0, 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "research_category", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) {
			researched++
			return "Category B evidence", nil
		}}},
		BeforeFinalResponse: func(content string) (string, error) {
			reviews++
			if content != "Category A is complete." {
				t.Fatalf("unexpected reviewed draft %q", content)
			}
			return "Category B is still requested. Continue the authorized work and inspect evidence for it before finalizing.", nil
		},
	})
	if err != nil || result.Content != "Category A and category B are complete." || reviews != 1 || researched != 1 || len(p.requests) != 3 {
		t.Fatalf("result=%+v err=%v reviews=%d researched=%d calls=%d", result, err, reviews, researched, len(p.requests))
	}
	if got := p.requests[1].Messages[len(p.requests[1].Messages)-1].Content; !strings.Contains(got, "Category B is still requested") {
		t.Fatalf("missing-category reminder was not supplied to the model: %q", got)
	}
}

type reservedFinalRepairProvider struct {
	requests   []provider.ChatRequest
	emptyFinal bool
}

func (p *reservedFinalRepairProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *reservedFinalRepairProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copy := *req
	copy.Messages = append([]provider.Message(nil), req.Messages...)
	copy.Tools = append([]provider.Tool(nil), req.Tools...)
	p.requests = append(p.requests, copy)
	switch len(p.requests) {
	case 1:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "initial-lookup", Name: "lookup", Arguments: `{}`}}}, nil
	case 2:
		return &provider.ChatResponse{Content: "Evidence supports the scheduled result, but the receipt is missing."}, nil
	case 3:
		if len(req.Tools) != 1 || req.Tools[0].Name != "complete_scheduled_task" {
			return nil, fmt.Errorf("reserved repair exposed tools %#v", req.Tools)
		}
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{
			{ID: "receipt", Name: "complete_scheduled_task", Arguments: `{"content":"verified result"}`},
			{ID: "side-effect", Name: "external_action", Arguments: `{}`},
		}}, nil
	case 4:
		if len(req.Tools) != 0 {
			return nil, fmt.Errorf("final-only receipt follow-up exposed tools %#v", req.Tools)
		}
		if p.emptyFinal {
			return &provider.ChatResponse{}, nil
		}
		return &provider.ChatResponse{Content: "Verified result."}, nil
	default:
		return nil, errors.New("unexpected provider call")
	}
}

func TestBeforeFinalResponseReservedRepairRejectsEmptyFinal(t *testing.T) {
	p := &reservedFinalRepairProvider{emptyFinal: true}
	receiptCalls := 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		MaxRunLLMCalls:           1,
		FinalResponseRepairTools: []string{"complete_scheduled_task"},
		ExtraTools: []ExtraBuiltinTool{
			{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "unexpected", nil }},
			{Schema: provider.Tool{Name: "complete_scheduled_task", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { receiptCalls++; return "receipt recorded", nil }},
			{Schema: provider.Tool{Name: "external_action", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { return "unexpected", nil }},
		},
		BeforeFinalResponse: func(string) (string, error) {
			return "Record the required completion receipt, then provide the final answer.", nil
		},
	})
	if result != nil || err == nil || !strings.Contains(err.Error(), "returned no final answer") || receiptCalls != 1 || len(p.requests) != 4 {
		t.Fatalf("result=%+v err=%v receipt=%d calls=%d", result, err, receiptCalls, len(p.requests))
	}
}

type stepReservedRepairProvider struct {
	requests []provider.ChatRequest
}

func (p *stepReservedRepairProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.ChatStream(ctx, req, nil)
}

func (p *stepReservedRepairProvider) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	copy := *req
	copy.Tools = append([]provider.Tool(nil), req.Tools...)
	p.requests = append(p.requests, copy)
	switch n := len(p.requests); {
	case n <= 29:
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("tick-%d", n), Name: "tick", Arguments: `{}`}}}, nil
	case n == 30:
		return &provider.ChatResponse{Content: "The requested result is ready, but the receipt is missing."}, nil
	case n == 31:
		if len(req.Tools) != 1 || req.Tools[0].Name != "complete_scheduled_task" {
			return nil, fmt.Errorf("step-limit repair exposed tools %#v", req.Tools)
		}
		return &provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: "receipt", Name: "complete_scheduled_task", Arguments: `{"content":"verified"}`}}}, nil
	case n == 32:
		if len(req.Tools) != 0 {
			return nil, fmt.Errorf("step-limit final follow-up exposed tools %#v", req.Tools)
		}
		return &provider.ChatResponse{Content: "Verified result."}, nil
	default:
		return nil, errors.New("unexpected provider call")
	}
}

func TestBeforeFinalResponseReservedRepairSurvivesStepLimit(t *testing.T) {
	p := &stepReservedRepairProvider{}
	ticks, receipts, reviews := 0, 0, 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		FinalResponseRepairTools: []string{"complete_scheduled_task"},
		ExtraTools: []ExtraBuiltinTool{
			{Schema: provider.Tool{Name: "tick", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { ticks++; return "evidence", nil }},
			{Schema: provider.Tool{Name: "complete_scheduled_task", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { receipts++; return "receipt recorded", nil }},
		},
		BeforeFinalResponse: func(content string) (string, error) {
			reviews++
			if content != "The requested result is ready, but the receipt is missing." {
				t.Fatalf("reviewed content = %q", content)
			}
			return "Record the completion receipt using the evidence already obtained, then give the final answer.", nil
		},
	})
	if err != nil || result.Content != "Verified result." || ticks != 29 || receipts != 1 || reviews != 1 || len(p.requests) != 32 {
		t.Fatalf("result=%+v err=%v ticks=%d receipts=%d reviews=%d calls=%d", result, err, ticks, receipts, reviews, len(p.requests))
	}
}

func TestBeforeFinalResponseReservedRepairRecordsReceiptAfterBudget(t *testing.T) {
	p := &reservedFinalRepairProvider{}
	lookupCalls, receiptCalls, sideEffectCalls, reviews := 0, 0, 0, 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		MaxRunLLMCalls:           1,
		FinalResponseRepairTools: []string{"complete_scheduled_task"},
		ExtraTools: []ExtraBuiltinTool{
			{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { lookupCalls++; return "unexpected", nil }},
			{Schema: provider.Tool{Name: "complete_scheduled_task", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { receiptCalls++; return "receipt recorded", nil }},
			{Schema: provider.Tool{Name: "external_action", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) { sideEffectCalls++; return "unexpected", nil }},
		},
		BeforeFinalResponse: func(content string) (string, error) {
			reviews++
			if content != "Evidence supports the scheduled result, but the receipt is missing." {
				t.Fatalf("reviewed content = %q", content)
			}
			return "Record the required completion receipt using the already verified evidence, then provide the same final answer.", nil
		},
	})
	if err != nil || result.Content != "Verified result." || reviews != 1 || receiptCalls != 1 || lookupCalls != 0 || sideEffectCalls != 0 || len(p.requests) != 4 {
		t.Fatalf("result=%+v err=%v reviews=%d receipt=%d lookup=%d side-effect=%d calls=%d", result, err, reviews, receiptCalls, lookupCalls, sideEffectCalls, len(p.requests))
	}
	if got := p.requests[2].Messages[len(p.requests[2].Messages)-1].Content; !strings.Contains(got, "Record the required completion receipt") {
		t.Fatalf("reserved repair omitted receipt instruction: %q", got)
	}
}

func TestBeforeFinalResponseAcceptanceAndError(t *testing.T) {
	callbackErr := errors.New("review failed")
	for _, mode := range []string{"nil", "empty", "whitespace", "error"} {
		t.Run(mode, func(t *testing.T) {
			p := &finalResponseProvider{responses: []provider.ChatResponse{{Content: "<think>private</think>answer"}}}
			reviews, turns := 0, 0
			cfg := AgentConfig{OnAssistantTurn: func(int, string) { turns++ }}
			if mode != "nil" {
				cfg.BeforeFinalResponse = func(content string) (string, error) {
					reviews++
					if content != "answer" {
						t.Fatalf("content = %q", content)
					}
					if mode == "error" {
						return "ignored", callbackErr
					}
					if mode == "whitespace" {
						return " \n", nil
					}
					return "", nil
				}
			}
			result, err := runFinalResponseTest(t, p, cfg)
			if mode == "error" {
				if !errors.Is(err, callbackErr) || result != nil {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			} else if err != nil || result.Content != "answer" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if turns != 0 || len(p.requests) != 1 || (mode == "nil" && reviews != 0) || (mode != "nil" && reviews != 1) {
				t.Fatalf("turns=%d calls=%d reviews=%d", turns, len(p.requests), reviews)
			}
		})
	}
}

func TestBeforeFinalResponseCancellation(t *testing.T) {
	for _, stage := range []string{"before_run", "provider", "review", "promotion", "next_boundary"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := &finalResponseProvider{responses: []provider.ChatResponse{{Content: "draft"}}}
			reviews, turns, boundaries := 0, 0, 0
			if stage == "before_run" {
				cancel()
			}
			if stage == "provider" {
				p.onReply = func(int) { cancel() }
			}
			result, err := runFinalResponseTest(t, p, AgentConfig{
				Ctx: ctx,
				BeforeFinalResponse: func(string) (string, error) {
					reviews++
					if stage == "review" {
						cancel()
					}
					return "Reassess.", nil
				},
				OnAssistantTurn: func(int, string) {
					turns++
					if stage == "promotion" {
						cancel()
					}
				},
				BeforeModelCall: func() error {
					boundaries++
					if stage == "next_boundary" && boundaries == 2 {
						cancel()
					}
					return nil
				},
			})
			if err != nil || result.Content != "" || len(p.requests) > 1 {
				t.Fatalf("result=%+v err=%v calls=%d", result, err, len(p.requests))
			}
			if (stage == "before_run" || stage == "provider") && reviews != 0 {
				t.Fatal("review called after cancellation")
			}
			if stage == "review" && turns != 0 {
				t.Fatal("cancelled review promoted draft")
			}
		})
	}
}

func TestBeforeFinalResponseHonorsExhaustedBudgets(t *testing.T) {
	for _, budget := range []string{"calls", "time", "cost", "steps", "wrap_up"} {
		t.Run(budget, func(t *testing.T) {
			p := &finalResponseProvider{responses: []provider.ChatResponse{{Content: "final"}}}
			reviews := 0
			cfg := AgentConfig{BeforeFinalResponse: func(string) (string, error) { reviews++; return "Reassess.", nil }}
			switch budget {
			case "calls":
				cfg.MaxRunLLMCalls = 1
			case "time":
				cfg.MaxRunDuration = time.Nanosecond
			case "cost":
				cfg.Model, cfg.MaxRunCost = "gpt-4o", 0.000001
				p.responses[0].Usage.InputTokens = 1000000
			case "steps":
				p.responses = make([]provider.ChatResponse, 30)
				p.responses[29].Content = "final"
			case "wrap_up":
				cfg.MaxRunLLMCalls = 1
				p.responses = []provider.ChatResponse{{ToolCalls: []provider.ToolCall{{ID: "drop", Name: "lookup", Arguments: `{}`}}}, {Content: "final"}}
			}
			result, err := runFinalResponseTest(t, p, cfg)
			if err != nil || result.Content != "final" || reviews != 1 || len(p.requests) != len(p.responses) {
				t.Fatalf("result=%+v err=%v reviews=%d calls=%d", result, err, reviews, len(p.requests))
			}
		})
	}
}

func TestBeforeFinalResponseRepairDoesNotResetCallBudget(t *testing.T) {
	p := &finalResponseProvider{responses: []provider.ChatResponse{
		{Content: "draft"},
		{ToolCalls: []provider.ToolCall{{ID: "drop", Name: "lookup", Arguments: `{}`}}},
		{Content: "budget final"},
	}}
	reviews, executed := 0, 0
	result, err := runFinalResponseTest(t, p, AgentConfig{
		MaxRunLLMCalls:      2,
		BeforeFinalResponse: func(string) (string, error) { reviews++; return "Reassess.", nil },
		ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) {
			executed++
			return "unexpected", nil
		}}},
	})
	// The existing budget policy allows one tool-free wrap-up request. Repair
	// consumes a normal call, so its tool is discarded at call 2, not executed.
	if err != nil || result.Content != "budget final" || result.LLMCalls != 3 || reviews != 1 || executed != 0 {
		t.Fatalf("result=%+v err=%v reviews=%d executed=%d", result, err, reviews, executed)
	}
	last := p.requests[2].Messages
	if !strings.Contains(last[len(last)-1].Content, "Do not call any more tools") {
		t.Fatalf("missing existing budget directive: %#v", last)
	}
}

func TestBeforeFinalResponseCallbackTimeCannotExtendBudget(t *testing.T) {
	for _, stage := range []string{"review", "promotion", "boundary", "pre_api"} {
		t.Run(stage, func(t *testing.T) {
			const budget = 200 * time.Millisecond
			p := &finalResponseProvider{responses: []provider.ChatResponse{{Content: "draft"}}}
			reviews, turns, boundaries := 0, 0, 0
			result, err := runFinalResponseTest(t, p, AgentConfig{
				MaxRunDuration: budget,
				BeforeFinalResponse: func(string) (string, error) {
					reviews++
					if stage == "review" {
						time.Sleep(budget)
					}
					return "Reassess.", nil
				},
				OnAssistantTurn: func(int, string) {
					turns++
					if stage == "promotion" {
						time.Sleep(budget)
					}
				},
				BeforeModelCall: func() error {
					boundaries++
					if stage == "boundary" && boundaries == 2 {
						time.Sleep(budget)
					}
					return nil
				},
				Hooks: &Hooks{PreAPICall: func(step, _, _ int) error {
					if stage == "pre_api" && step == 2 {
						time.Sleep(budget)
					}
					return nil
				}},
			})
			if len(p.requests) != 1 || reviews != 1 {
				t.Fatalf("calls=%d reviews=%d", len(p.requests), reviews)
			}
			if stage == "review" {
				if err != nil || result.Content != "draft" || turns != 0 {
					t.Fatalf("result=%+v err=%v turns=%d", result, err, turns)
				}
			} else if err == nil || !strings.Contains(err.Error(), "repair budget exhausted") || turns != 1 {
				t.Fatalf("result=%+v err=%v turns=%d", result, err, turns)
			}
		})
	}
}
