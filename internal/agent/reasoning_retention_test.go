package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

type scriptStep = func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error)

func anthropicThinking(text string) []provider.ReasoningItem {
	blocks, _ := json.Marshal([]map[string]string{{"type": "thinking", "thinking": text, "signature": "sig"}, {"type": "text", "text": "visible"}})
	return []provider.ReasoningItem{{Provider: "anthropic", Content: blocks}}
}

func longTranscript(steps int, item func(i int) []provider.ReasoningItem) []provider.Message {
	messages := []provider.Message{{Role: "user", Content: "goal"}}
	for i := 0; i < steps; i++ {
		id := fmt.Sprintf("c%d", i)
		messages = append(messages,
			provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "lookup", Arguments: `{}`}}, ReasoningItems: item(i)},
			provider.Message{Role: "tool", ToolCallID: id, ToolName: "lookup", Content: strings.Repeat("row ", 400)})
	}
	return messages
}

func TestMicroCompactKeepsReasoningAcrossFortySteps(t *testing.T) {
	messages := longTranscript(40, func(i int) []provider.ReasoningItem {
		return []provider.ReasoningItem{{ID: fmt.Sprintf("rs_%d", i), EncryptedContent: "enc"}}
	})
	got := microCompact(messages, 6)
	trimmed := 0
	for i, msg := range got {
		if msg.Role == "assistant" && (len(msg.ReasoningItems) != 1 || msg.ReasoningItems[0].ID != messages[i].ReasoningItems[0].ID) {
			t.Fatalf("message %d lost its reasoning: %+v", i, msg.ReasoningItems)
		}
		if strings.Contains(msg.Content, microCompactMarker) {
			trimmed++
		}
	}
	if trimmed == 0 {
		t.Fatal("old tool results were not trimmed")
	}
}

// runToolSteps drives n tool rounds, each with a large result, then a final answer.
func runToolSteps(t *testing.T, model string, n int, item func(i int) []provider.ReasoningItem) *scriptedProvider {
	t.Helper()
	var steps []scriptStep
	for i := 0; i < n; i++ {
		i := i
		steps = append(steps, reply(provider.ChatResponse{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("c%d", i), Name: "lookup", Arguments: `{}`}}, ReasoningItems: item(i)}))
	}
	steps = append(steps, reply(provider.ChatResponse{Content: "done"}))
	p := &scriptedProvider{steps: steps}
	_, err := runLoop(t, AgentConfig{Provider: p, Model: model, Prompt: "go",
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) {
			return strings.Repeat("row ", 8000), nil
		})}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAnthropicReplayHistoryIsNotRewritten(t *testing.T) {
	p := runToolSteps(t, "claude-opus-5", 12, func(i int) []provider.ReasoningItem { return anthropicThinking(fmt.Sprintf("thought %d", i)) })
	last := p.requests[len(p.requests)-1].Messages
	thinking := 0
	for i, msg := range last {
		if strings.Contains(msg.Content, microCompactMarker) || strings.Contains(msg.Content, omittedToolImageNote) {
			t.Fatalf("message %d (%s) was rewritten under live Anthropic replay", i, msg.Role)
		}
		if len(msg.ReasoningItems) > 0 {
			thinking++
		}
	}
	if thinking != 12 {
		t.Fatalf("replayed thinking turns = %d, want 12", thinking)
	}
}

func TestOtherProvidersStillTrimToolResultsAndKeepReasoning(t *testing.T) {
	p := runToolSteps(t, "test-model", 12, func(i int) []provider.ReasoningItem {
		return []provider.ReasoningItem{{ID: fmt.Sprintf("rs_%d", i), EncryptedContent: "enc"}}
	})
	last := p.requests[len(p.requests)-1].Messages
	trimmed, reasoning := 0, 0
	for _, msg := range last {
		if strings.Contains(msg.Content, microCompactMarker) {
			trimmed++
		}
		if len(msg.ReasoningItems) > 0 {
			reasoning++
		}
	}
	if trimmed == 0 || reasoning != 12 {
		t.Fatalf("trimmed=%d reasoning=%d, want >0 and 12", trimmed, reasoning)
	}
}

func TestCompactionContinuesTranscriptWithReasoning(t *testing.T) {
	messages := longTranscript(3, func(i int) []provider.ReasoningItem { return anthropicThinking("why") })
	tools := []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}
	p := &scriptedProvider{steps: []scriptStep{reply(provider.ChatResponse{Content: "handoff"})}}
	cfg := &AgentConfig{Provider: p, Model: "claude-opus-5"}
	summary, err := compactTranscript(context.Background(), cfg, "SYS", tools, messages, false)
	if err != nil || summary.Summary != "handoff" || len(p.requests) != 1 || len(summary.Calls) != 1 {
		t.Fatalf("summary=%+v err=%v calls=%d", summary, err, len(p.requests))
	}
	req := p.requests[0]
	if req.System != "SYS" || len(req.Tools) != 1 || len(req.Messages) != len(messages)+1 {
		t.Fatalf("not a continuation of the real transcript: system=%q tools=%d messages=%d", req.System, len(req.Tools), len(req.Messages))
	}
	for i, msg := range messages {
		if len(req.Messages[i].ReasoningItems) != len(msg.ReasoningItems) {
			t.Fatalf("message %d reasoning not replayed", i)
		}
	}
	if ask := req.Messages[len(req.Messages)-1]; ask.Role != "user" || !strings.Contains(ask.Content, "Current line of thinking") {
		t.Fatalf("last message = %+v", ask)
	}
}

func TestCompactionFallsBackToFlattenedText(t *testing.T) {
	messages := longTranscript(3, func(i int) []provider.ReasoningItem { return anthropicThinking("why") })
	overflow := func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error) {
		return nil, provider.NewAPIError("anthropic", 400, "prompt is too long: input exceeds the context window")
	}
	p := &scriptedProvider{steps: []scriptStep{overflow, reply(provider.ChatResponse{Content: "flat"})}}
	summary, err := compactTranscript(context.Background(), &AgentConfig{Provider: p, Model: "claude-opus-5"}, "SYS", nil, messages, false)
	if err != nil || summary.Summary != "flat" || len(p.requests) != 2 {
		t.Fatalf("summary=%+v err=%v calls=%d", summary, err, len(p.requests))
	}
	if flat := p.requests[1]; len(flat.Messages) != 1 || len(flat.Messages[0].ReasoningItems) != 0 || !strings.Contains(flat.Messages[0].Content, "Conversation:") {
		t.Fatalf("fallback request = %+v", flat)
	}
	// Without reasoning, or once replay is disabled, only the flattened path runs.
	for name, disabled := range map[string]bool{"disabled": true, "none": false} {
		msgs := messages
		if name == "none" {
			msgs = longTranscript(3, func(int) []provider.ReasoningItem { return nil })
		}
		p := &scriptedProvider{steps: []scriptStep{reply(provider.ChatResponse{Content: "flat"})}}
		if _, err := compactTranscript(context.Background(), &AgentConfig{Provider: p, Model: "claude-opus-5"}, "SYS", nil, msgs, disabled); err != nil || len(p.requests) != 1 || len(p.requests[0].Messages) != 1 {
			t.Fatalf("%s: err=%v requests=%d", name, err, len(p.requests))
		}
	}
}

func TestCompactionThresholdByProvider(t *testing.T) {
	if got := compactionThreshold("claude-opus-5"); got != 0.70 {
		t.Fatalf("anthropic threshold = %v", got)
	}
	if got := compactionThreshold("test-model"); got != 0.80 {
		t.Fatalf("default threshold = %v", got)
	}
}

func TestEstimateCountsAnthropicThinkingBlocks(t *testing.T) {
	text := strings.Repeat("t", 40000)
	plain := []provider.Message{{Role: "assistant", Content: "visible", ToolCalls: []provider.ToolCall{{ID: "a", Name: "lookup", Arguments: `{}`}}}}
	replayed := []provider.Message{plain[0]}
	replayed[0].ReasoningItems = anthropicThinking(text)
	with := EstimateContextBreakdown("sys", replayed, nil)
	without := EstimateContextBreakdown("sys", plain, nil)
	if with.Reasoning < 9000 || without.Reasoning != 0 {
		t.Fatalf("reasoning with=%d without=%d", with.Reasoning, without.Reasoning)
	}
	// The turn's text/tool_use blocks inside the item are not counted twice.
	if with.Messages != without.Messages || with.System != without.System {
		t.Fatalf("messages/system changed: %+v vs %+v", with, without)
	}
	if with.Total() != EstimateContextUsage("sys", replayed, nil) {
		t.Fatal("total disagrees with EstimateContextUsage")
	}
	tools := []provider.Tool{{Name: "lookup", Parameters: map[string]any{"type": "object"}}}
	if b := EstimateContextBreakdown("system prompt", nil, tools); b.System <= estimateStringTokens("system prompt") || b.Messages != 0 || b.Reasoning != 0 {
		t.Fatalf("system/tools bucket = %+v", b)
	}
}

func TestReasoningReplayRejectionIsReported(t *testing.T) {
	rejected := 0
	p := &scriptedProvider{steps: []scriptStep{
		toolCallReply("c", "lookup", `{}`),
		reply(provider.ChatResponse{Content: "done", ReasoningReplayRejected: true}),
	}}
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnReasoningReplayRejected: func() { rejected++ },
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) { return "ok", nil })}})
	if err != nil || rejected != 1 {
		t.Fatalf("err=%v rejected=%d", err, rejected)
	}
}

func TestContextBreakdownIsReportedWithEstimate(t *testing.T) {
	var total int
	var got ContextBreakdown
	p := &scriptedProvider{steps: []scriptStep{reply(provider.ChatResponse{Content: "done"})}}
	_, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", OnContextEstimate: func(n int) { total = n }, OnContextBreakdown: func(b ContextBreakdown) { got = b }})
	if err != nil || total == 0 || got.Total() != total {
		t.Fatalf("err=%v total=%d breakdown=%+v", err, total, got)
	}
}
