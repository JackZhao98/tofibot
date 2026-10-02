package app

import (
	"context"
	"testing"
)

func streamFixture(t *testing.T) (*Store, Run, Conversation) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("stream", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	r, err := s.AddRun(c.ID, b.ID, "")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		s.Close()
		t.Fatalf("start run: %v %v", ok, err)
	}
	return s, r, c
}

func TestStreamDraftDurableReplayAndFinalReplacement(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	d, err := s.BeginStream(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "hel"); err != nil || !ok {
		t.Fatalf("first delta: %v %v", ok, err)
	}
	if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "lo"); err != nil || !ok {
		t.Fatalf("second delta: %v %v", ok, err)
	}
	events, err := s.Events(c.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range events {
		if e["type"] == "delta" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("delta events=%d", count)
	}
	drafts, err := s.StreamDrafts(c.ID)
	if err != nil || len(drafts) != 1 || drafts[0].MessageID != d.MessageID || drafts[0].Content != "hello" || drafts[0].Revision != 2 {
		t.Fatalf("draft snapshot=%+v err=%v", drafts, err)
	}
	m, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "hello")
	if err != nil || !done || m.ID != d.MessageID {
		t.Fatalf("finish=%+v done=%v err=%v", m, done, err)
	}
	msgs, _, err := s.Messages(c.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != d.MessageID || msgs[0].Content != "hello" {
		t.Fatalf("messages=%+v", msgs)
	}
	if _, done, err = s.FinishRun(r.ID, c.ID, r.BotID, "duplicate"); err != nil || done {
		t.Fatalf("duplicate finish done=%v err=%v", done, err)
	}
}

func TestStreamReservesSequenceAtFirstDeltaAndUserMessageSkipsIt(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	before, err := s.StreamDraft(r.ID)
	if err != nil || before.Seq != 0 {
		t.Fatalf("draft reserved before output: %+v err=%v", before, err)
	}
	if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "first"); err != nil || !ok {
		t.Fatalf("first delta: ok=%v err=%v", ok, err)
	}
	draft, err := s.StreamDraft(r.ID)
	if err != nil || draft.Seq == 0 || draft.CreatedAt == before.CreatedAt {
		t.Fatalf("first delta did not reserve timestamped seq: %+v err=%v", draft, err)
	}
	user, _, err := s.AddMessage(c.ID, "user", "", "", "steering", "")
	if err != nil {
		t.Fatal(err)
	}
	if user.Seq <= draft.Seq {
		t.Fatalf("user seq=%d did not skip active draft seq=%d", user.Seq, draft.Seq)
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "first"); err != nil || !done {
		t.Fatalf("finish done=%v err=%v", done, err)
	}
	msgs, _, err := s.Messages(c.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var assistant Message
	for _, m := range msgs {
		if m.RunID == r.ID {
			assistant = m
		}
	}
	if assistant.ID == "" || assistant.Seq != draft.Seq || assistant.CreatedAt != draft.CreatedAt {
		t.Fatalf("assistant did not retain reserved order/time: %+v draft=%+v", assistant, draft)
	}
}

func TestPublishAssistantTurnLeavesEmptyRotatedDraftUnreserved(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "progress"); err != nil || !ok {
		t.Fatalf("publish=%v err=%v", ok, err)
	}
	draft, err := s.StreamDraft(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Seq != 0 {
		t.Fatalf("empty rotated draft reserved seq=%d", draft.Seq)
	}
}

func TestStreamCancelRejectsLateDelta(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelStream(r.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.AppendStreamDelta(context.Background(), r.ID, "late"); err != nil || ok {
		t.Fatalf("late delta ok=%v err=%v", ok, err)
	}
	if _, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "late"); err != nil || done {
		t.Fatalf("late finish done=%v err=%v", done, err)
	}
}

func TestFinishRunReusesActiveDraftMessageID(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	d, err := s.BeginStream(r)
	if err != nil {
		t.Fatal(err)
	}
	m, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "complete")
	if err != nil || !done || m.ID != d.MessageID {
		t.Fatalf("finish=%+v done=%v err=%v", m, done, err)
	}
	got, err := s.StreamDraft(r.ID)
	if err != nil || got.Status != streamDraftDone || got.Content != "complete" {
		t.Fatalf("draft=%+v err=%v", got, err)
	}
}
