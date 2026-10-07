package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func eventsOfType(t *testing.T, s *Store, conversationID, typ string) []map[string]any {
	t.Helper()
	events, err := s.Events(conversationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range events {
		if e["type"] == typ {
			out = append(out, e["data"].(map[string]any))
		}
	}
	return out
}

func TestStreamResetDiscardsDraftWithoutPublishing(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	onDelta, flush, reset, err := s.StreamControls(context.Background(), r, func(err error) { t.Errorf("stream error: %v", err) })
	if err != nil {
		t.Fatal(err)
	}
	onDelta("draft answer")
	flush()
	before, _ := s.StreamDraft(r.ID)
	onDelta("unflushed")
	reset()
	after, _ := s.StreamDraft(r.ID)
	if after.MessageID == before.MessageID || after.Content != "" || after.Seq != 0 || after.Status != streamDraftActive {
		t.Fatalf("draft not rotated: before=%+v after=%+v", before, after)
	}
	resets := eventsOfType(t, s, c.ID, "draft_reset")
	if len(resets) != 1 || resets[0]["message_id"] != before.MessageID || resets[0]["run_id"] != r.ID {
		t.Fatalf("draft_reset events = %+v", resets)
	}
	onDelta("final")
	flush()
	if d, _ := s.StreamDraft(r.ID); d.Content != "final" {
		t.Fatalf("buffer leaked across reset: %q", d.Content)
	}
	if msgs, _, _ := s.Messages(c.ID, 0, 10); len(msgs) != 0 {
		t.Fatalf("reset published messages: %+v", msgs)
	}
	reset()
	reset() // an empty draft needs no further reset event
	if got := len(eventsOfType(t, s, c.ID, "draft_reset")); got != 2 {
		t.Fatalf("draft_reset events = %d", got)
	}
}

func TestThinkingEventsAreThrottledAndBounded(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	onThinking, stop := s.ThinkingCallback(context.Background(), r)
	onThinking("Checking the inbox. ")
	onThinking(strings.Repeat("x", 600))
	onThinking("latest")
	time.Sleep(3 * thinkingEventInterval)
	stop()
	onThinking("after stop")
	events := eventsOfType(t, s, c.ID, "thinking")
	if len(events) != 2 {
		t.Fatalf("thinking events = %d, want leading + trailing", len(events))
	}
	if events[0]["text"] != "Checking the inbox. " || events[0]["run_id"] != r.ID {
		t.Fatalf("first = %+v", events[0])
	}
	last := events[1]["text"].(string)
	if len([]rune(last)) != maxThinkingEventRunes || !strings.HasSuffix(last, "latest") {
		t.Fatalf("trailing snippet = %d runes", len([]rune(last)))
	}
	if len(eventsOfType(t, s, c.ID, "delta")) != 0 {
		t.Fatal("thinking leaked into the answer draft")
	}
	s.PublishRetry(r, 2, 1500*time.Millisecond)
	retries := eventsOfType(t, s, c.ID, "retrying")
	if len(retries) != 1 || retries[0]["attempt"] != float64(2) || retries[0]["wait_ms"] != float64(1500) || retries[0]["run_id"] != r.ID {
		t.Fatalf("retrying events = %+v", retries)
	}
}

func TestCompletionReviewIgnoresBookkeepingAndObservations(t *testing.T) {
	for _, name := range []string{"save_memory", "send_chat_message", "react_to_message", "display_content", "search_mcp_tools", "read_skill"} {
		if completionReviewWork(name, tooloutcome.OpaqueEffect) {
			t.Fatalf("%s armed the final review", name)
		}
	}
	if completionReviewWork("call_mcp_tool", tooloutcome.Observation) {
		t.Fatal("a read-only MCP call armed the final review")
	}
	if !completionReviewWork("call_mcp_tool", tooloutcome.OpaqueEffect) || !completionReviewWork("computer_action", "") {
		t.Fatal("substantive work must still arm the final review")
	}
	s, bot, conversation := scheduleTestStore(t)
	defer s.Close()
	const runID = "bookkeeping-run"
	for _, name := range []string{"save_memory", "send_chat_message", "list_mcp_servers"} {
		for _, status := range []string{"queued", "running", "completed"} {
			if err := s.RecordToolEvent(conversation.ID, bot.ID, runID, runtime.ToolEvent{CallID: name, Name: name, Status: status}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if found, err := s.hasCompletionReviewToolWork(runID); err != nil || found {
		t.Fatalf("bookkeeping restored as task work: found=%v err=%v", found, err)
	}
}
