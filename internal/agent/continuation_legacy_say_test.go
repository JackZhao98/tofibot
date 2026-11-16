package agent

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// A continuation stored before the say tool was renamed send_message must
// validate and resume under the new name.
func TestStoredSayContinuationResumesAsSendMessage(t *testing.T) {
	calls := []provider.ToolCall{
		{ID: "a", Name: "say", Arguments: `{"purpose":"note","text":"working"}`},
		{ID: "b", Name: "ask_user_question", Arguments: `{}`},
		{ID: "c", Name: "say", Arguments: `{"purpose":"note","text":"later"}`},
	}
	c := &Continuation{
		Version: continuationVersion, Kind: ContinuationInputWait,
		QuestionID: "q1", WaitingToolCallID: "b", WaitingToolName: "ask_user_question",
		Messages: []provider.Message{
			{Role: "user", Content: "task"},
			{Role: "assistant", ToolCalls: calls},
			{Role: "tool", ToolCallID: "a", ToolName: "say", Content: "ok"},
		},
		SkippedToolCalls: []provider.ToolCall{calls[2]},
		TotalUsage:       provider.Usage{InputTokens: 10, OutputTokens: 2},
		ModelUsage:       map[string]ModelUsage{"m": {InputTokens: 10, OutputTokens: 2, APICallCount: 1}},
		LLMCalls:         1,
	}
	msgs, err := resumeContinuation(c, "answer")
	if err != nil {
		t.Fatalf("stored say continuation rejected: %v", err)
	}
	for _, m := range msgs {
		if m.ToolName == "say" {
			t.Fatalf("tool result still named say: %+v", m)
		}
		for _, tc := range m.ToolCalls {
			if tc.Name == "say" {
				t.Fatalf("tool call still named say: %+v", tc)
			}
		}
	}
	if c.SkippedToolCalls[0].Name != "send_message" || c.Messages[1].ToolCalls[0].Name != "send_message" {
		t.Fatalf("names not renamed: %+v", c.SkippedToolCalls)
	}

	w := &Continuation{
		Version: continuationVersion, Kind: ContinuationInputWait,
		QuestionID: "q2", WaitingToolCallID: "w", WaitingToolName: "say",
		Messages: []provider.Message{
			{Role: "user", Content: "task"},
			{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "w", Name: "say", Arguments: `{}`}}},
		},
		SkippedToolCalls: nil,
		TotalUsage:       provider.Usage{InputTokens: 10, OutputTokens: 2},
		ModelUsage:       map[string]ModelUsage{"m": {InputTokens: 10, OutputTokens: 2, APICallCount: 1}},
		LLMCalls:         1,
	}
	if err := ValidateContinuation(w); err != nil || w.WaitingToolName != "send_message" {
		t.Fatalf("waiting say not renamed: %v %q", err, w.WaitingToolName)
	}
}

func TestIsSendMessageTool(t *testing.T) {
	if !isSendMessageTool("send_message") || !isSendMessageTool("say") || isSendMessageTool("send_chat_message") {
		t.Fatal("isSendMessageTool wrong")
	}
}
