package app

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestChatSegmentsPersistInOrderAndStopAfterInterjection(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendStreamDelta(ctx, r.ID, "draft"); err != nil {
		t.Fatal(err)
	}
	first, err := s.PublishChatSegment(ctx, r, "call-one", "First complete thought.")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.PublishChatSegment(ctx, r, "call-one", "First complete thought.")
	if err != nil || retry.ID != first.ID {
		t.Fatalf("idempotent retry: %+v %v", retry, err)
	}
	second, err := s.PublishChatSegment(ctx, r, "call-two", "Second thought.")
	if err != nil || second.Seq <= first.Seq {
		t.Fatalf("second segment: %+v %v", second, err)
	}
	if _, err = s.PublishChatSegment(ctx, r, "call-one", "Different text."); err == nil {
		t.Fatal("conflicting retry accepted")
	}
	_, runs, _, err := s.AddUserRuns(c.ID, "Wait, change direction.", "interrupt-after-segments", []runSpec{{BotID: r.BotID, Model: r.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("interjection: %+v %v", runs, err)
	}
	if _, err = s.PublishChatSegment(ctx, r, "call-three", "Stale third thought."); err == nil {
		t.Fatal("old run sent after user interjection")
	}
	msgs, _, err := s.Messages(c.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[0].ID != first.ID || msgs[1].ID != second.ID || msgs[2].Role != "user" {
		t.Fatalf("conversation order: %+v", msgs)
	}
}

func TestChatSegmentsBoundedAndFinalFollows(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	for i, call := range []string{"one", "two", "three"} {
		if _, err := s.PublishChatSegment(context.Background(), r, call, "Thought."); err != nil {
			t.Fatalf("segment %d: %v", i, err)
		}
	}
	if _, err := s.PublishChatSegment(context.Background(), r, "four", "Too many."); err == nil {
		t.Fatal("unbounded segments accepted")
	}
	final, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "Conclusion.")
	if err != nil || !done || final.Kind != "" || final.Seq != 4 {
		t.Fatalf("final: %+v %v %v", final, done, err)
	}
}

func TestChatMessagePurposeLabelsStoredKind(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	for _, tc := range []struct{ call, purpose, want string }{
		{"a", "answer", "segment"},
		{"s", "status", "progress"},
		{"omitted", "", "segment"},
		{"unknown", "whatever", "segment"},
	} {
		m, err := s.publishChatMessage(ctx, r, tc.call, "Synthetic note "+tc.call, normalizeChatPurpose(tc.purpose))
		if err != nil || m.Kind != tc.want {
			t.Fatalf("purpose %q: kind=%q want %q err=%v", tc.purpose, m.Kind, tc.want, err)
		}
	}
}

func TestChatToolSchemaRequiresPurposeEnum(t *testing.T) {
	srv := &Server{}
	tool := srv.chatSegmentTool(Conversation{}, Run{})
	b, _ := json.Marshal(tool.Parameters)
	var schema struct {
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(schema.Required, "purpose") || schema.Properties["purpose"]["enum"] == nil {
		t.Fatalf("schema=%s", b)
	}
}

// Incident shape: a mid-run plain reply answers the user, the run keeps
// working, and the final message talks about something else.
func TestMidRunReplyStaysUnfoldableKind(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendStreamDelta(context.Background(), r.ID, "OK, changed to 2:30 daily."); err != nil {
		t.Fatal(err)
	}
	mid, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "OK, changed to 2:30 daily.")
	if err != nil || !ok || mid.Kind == "progress" {
		t.Fatalf("mid-run reply must not be foldable progress: %+v ok=%v err=%v", mid, ok, err)
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "Other work finished."); err != nil || !done {
		t.Fatal(err, done)
	}
}
