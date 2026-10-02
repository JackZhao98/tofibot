package app

import (
	"context"
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
