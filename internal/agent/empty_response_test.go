package agent

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func TestEmptyResponseRecoveryPreservesHistory(t *testing.T) {
	for _, empty := range []string{"", " \n", "<think>still considering</think>"} {
		t.Run(empty, func(t *testing.T) {
			prior := []provider.Message{{Role: "user", Content: "Look up the answer."}}
			p := &finalResponseProvider{responses: []provider.ChatResponse{
				{Content: empty},
				{ToolCalls: []provider.ToolCall{{ID: "lookup-1", Name: "lookup", Arguments: `{}`}}},
				{Content: "Answer from evidence."},
			}}
			executed := 0
			var emitted []provider.Message
			result, err := runFinalResponseTest(t, p, AgentConfig{
				Messages: prior,
				ExtraTools: []ExtraBuiltinTool{{Schema: provider.Tool{Name: "lookup", Parameters: map[string]any{"type": "object"}}, Handler: func(map[string]interface{}) (string, error) {
					executed++
					return "evidence", nil
				}}},
				OnMessage: func(message provider.Message) { emitted = append(emitted, message) },
			})
			if err != nil || result.Content != "Answer from evidence." || executed != 1 || result.LLMCalls != 3 {
				t.Fatalf("result=%+v err=%v executed=%d", result, err, executed)
			}
			history := p.requests[1].Messages
			if len(history) != 3 || !reflect.DeepEqual(history[0], prior[0]) || history[1].Role != "assistant" || history[1].Content != empty || history[2].Role != "user" || !strings.Contains(history[2].Content, "Please continue") {
				t.Fatalf("recovery lost history or reminder: %#v", history)
			}
			history = p.requests[2].Messages
			if len(history) != 5 || history[4].ToolCallID != "lookup-1" || history[4].Content != "evidence" {
				t.Fatalf("recovery lost tool result: %#v", history)
			}
			for _, message := range emitted {
				if message.Role == "user" {
					t.Fatal("internal recovery reminder leaked to OnMessage")
				}
			}
		})
	}
}

func TestEmptyResponseHonorsRunBudgets(t *testing.T) {
	for _, budget := range []string{"calls", "duration", "cost"} {
		for _, final := range []string{"", "<think>still considering</think>", "Recovered final answer."} {
			t.Run(budget+"/"+final, func(t *testing.T) {
				cfg := AgentConfig{}
				switch budget {
				case "calls":
					cfg.MaxRunLLMCalls = 1
				case "duration":
					cfg.MaxRunDuration = time.Nanosecond
				case "cost":
					cfg.Model, cfg.MaxRunCost = "gpt-4o", 0.000001
				}
				p := &finalResponseProvider{responses: []provider.ChatResponse{
					{Usage: provider.Usage{InputTokens: 1000, OutputTokens: 4}},
					{Content: final},
				}}
				result, err := runFinalResponseTest(t, p, cfg)
				if err != nil || result == nil || result.LLMCalls != 2 || len(p.requests) != 2 {
					t.Fatalf("empty responses bypassed budget: result=%+v err=%v calls=%d", result, err, len(p.requests))
				}
				history := p.requests[1].Messages
				if !strings.Contains(history[len(history)-1].Content, "Do not call any more tools") {
					t.Fatalf("missing budget wrap-up directive: %#v", history)
				}
				if final == "Recovered final answer." {
					if result.Content != final {
						t.Fatalf("lost final answer: %q", result.Content)
					}
				} else if !strings.Contains(result.Content, "Run stopped") {
					t.Fatalf("missing explicit budget stop: %q", result.Content)
				}
			})
		}
	}
}
