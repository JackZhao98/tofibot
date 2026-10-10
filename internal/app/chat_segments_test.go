package app

import (
	"context"
	"encoding/json"
	"strings"
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

func TestChatToolHasNoPurposeParameter(t *testing.T) {
	tool := (&Server{}).chatSegmentTool(Conversation{}, Run{})
	b, _ := json.Marshal(tool.Parameters)
	if strings.Contains(string(b), "purpose") {
		t.Fatalf("schema=%s", b)
	}
}

func TestChatSegmentCapCountsOnlyToolSentMessages(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	// Untagged mid-run turns are segments too; they must not use up the cap.
	for i := 1; i <= 4; i++ {
		if _, _, err := s.AppendStreamDelta(ctx, r.ID, "Turn text"); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := s.PublishAssistantTurn(ctx, r.ID, i, "Turn text "+string(rune('a'+i))); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.PublishChatSegment(ctx, r, id, "Tool message "+id); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := s.PublishChatSegment(ctx, r, "d", "One too many"); err == nil {
		t.Fatal("fourth tool message must hit the cap")
	}
}

func TestProgressNoteCountsAsProgressReport(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	ctx := context.Background()
	if ok, err := s.hasProgressReport(r.ID); err != nil || ok {
		t.Fatalf("empty run: ok=%v err=%v", ok, err)
	}
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AppendStreamDelta(ctx, r.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.PublishAssistantTurn(ctx, r.ID, 1, "<progress>Checking the page now.</progress>"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.hasProgressReport(r.ID); err != nil || !ok {
		t.Fatalf("progress note must count: ok=%v err=%v", ok, err)
	}
}
