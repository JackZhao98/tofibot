package agent

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func continuationWithUsage(total provider.Usage, model ModelUsage, calls int) *Continuation {
	return &Continuation{
		Version: continuationVersion,
		Messages: []provider.Message{
			{Role: "user", Content: "create the page"},
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "call_1", Name: "call_mcp_tool", Arguments: "{}"}}},
		},
		TotalUsage:        total,
		ModelUsage:        map[string]ModelUsage{"gpt-test": model},
		LLMCalls:          calls,
		QuestionID:        "q_1",
		WaitingToolCallID: "call_1",
		WaitingToolName:   "call_mcp_tool",
	}
}

// Cached prompt tokens are part of TotalUsage but not of ModelUsage; a resume
// after an approval must not be rejected because the provider served a cache hit.
func TestValidateContinuationIgnoresCacheTokens(t *testing.T) {
	c := continuationWithUsage(
		provider.Usage{InputTokens: 1200, OutputTokens: 80, CacheReadTokens: 900, CacheWriteTokens: 40},
		ModelUsage{InputTokens: 1200, OutputTokens: 80, APICallCount: 2},
		2,
	)
	if err := ValidateContinuation(c); err != nil {
		t.Fatalf("valid continuation with cache tokens rejected: %v", err)
	}
}

func TestValidateContinuationRejectsMismatchedUsage(t *testing.T) {
	cases := map[string]*Continuation{
		"input":  continuationWithUsage(provider.Usage{InputTokens: 1201, OutputTokens: 80}, ModelUsage{InputTokens: 1200, OutputTokens: 80, APICallCount: 2}, 2),
		"output": continuationWithUsage(provider.Usage{InputTokens: 1200, OutputTokens: 81}, ModelUsage{InputTokens: 1200, OutputTokens: 80, APICallCount: 2}, 2),
		"calls":  continuationWithUsage(provider.Usage{InputTokens: 1200, OutputTokens: 80}, ModelUsage{InputTokens: 1200, OutputTokens: 80, APICallCount: 2}, 3),
	}
	for name, c := range cases {
		if err := ValidateContinuation(c); err == nil {
			t.Fatalf("%s mismatch accepted", name)
		}
	}
}
