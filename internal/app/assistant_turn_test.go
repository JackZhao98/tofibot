package app

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPublishAssistantTurnThenFinishUsesRotatedDraft(t *testing.T) {
	s, r, c := streamFixture(t)
	defer s.Close()
	firstDraft, err := s.BeginStream(r)
	if err != nil {
		t.Fatal(err)
	}

	first, published, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "full agenda")
	if err != nil || !published {
		t.Fatalf("publish=%+v published=%v err=%v", first, published, err)
	}
	if first.ID != firstDraft.MessageID || first.ConversationID != c.ID || first.RunID != r.ID {
		t.Fatalf("published message=%+v draft=%+v", first, firstDraft)
	}
	if first.Kind != "progress" {
		t.Fatalf("published turn kind=%q, want progress", first.Kind)
	}
	rotated, err := s.StreamDraft(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.MessageID == firstDraft.MessageID || rotated.Content != "" || rotated.Revision != 0 || rotated.Status != streamDraftActive {
		t.Fatalf("rotated draft=%+v", rotated)
	}
	messages, _, err := s.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 1 || messages[0].ID != first.ID {
		t.Fatalf("messages before final=%+v err=%v", messages, err)
	}

	final, done, err := s.FinishRun(r.ID, c.ID, r.BotID, "final answer")
	if err != nil || !done || final.ID != rotated.MessageID {
		t.Fatalf("finish=%+v done=%v err=%v", final, done, err)
	}
	messages, _, err = s.Messages(c.ID, 0, 50)
	if err != nil || len(messages) != 2 || messages[0].ID != first.ID || messages[1].ID != final.ID {
		t.Fatalf("messages after final=%+v err=%v", messages, err)
	}
	if messages[0].Kind != "progress" || messages[1].Kind != "" {
		t.Fatalf("progress/final kinds=%q/%q", messages[0].Kind, messages[1].Kind)
	}
}

func TestPublishAssistantTurnIsIdempotentAndRejectsConflictingReplay(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}

	first, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "agenda")
	if err != nil || !ok {
		t.Fatalf("first publish=%+v ok=%v err=%v", first, ok, err)
	}
	draft, err := s.StreamDraft(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "agenda")
	if err != nil || !ok || second.ID != first.ID {
		t.Fatalf("duplicate=%+v ok=%v err=%v", second, ok, err)
	}
	after, err := s.StreamDraft(r.ID)
	if err != nil || after.MessageID != draft.MessageID {
		t.Fatalf("duplicate rotated draft=%+v before=%+v err=%v", after, draft, err)
	}
	if _, ok, err = s.PublishAssistantTurn(context.Background(), r.ID, 1, "changed agenda"); err == nil || ok {
		t.Fatalf("conflicting replay ok=%v err=%v", ok, err)
	}
	messages, _, err := s.Messages(r.ConversationID, 0, 50)
	if err != nil || len(messages) != 1 {
		t.Fatalf("conflicting replay changed messages=%+v err=%v", messages, err)
	}
}

func TestPublishAssistantTurnRejectsCancelledLateAndOversize(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.SetRunStatus(r.ID, "cancelled", "cancelled"); err != nil || !changed {
		t.Fatalf("cancel run changed=%v err=%v", changed, err)
	}
	if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "late"); err != nil || ok {
		t.Fatalf("late publish ok=%v err=%v", ok, err)
	}

	s2, r2, _ := streamFixture(t)
	defer s2.Close()
	if _, err := s2.BeginStream(r2); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s2.PublishAssistantTurn(context.Background(), r2.ID, 1, strings.Repeat("x", maxStreamDraftRunes+1)); err == nil || ok {
		t.Fatalf("oversize publish ok=%v err=%v", ok, err)
	}
	if _, ok, err := s2.PublishAssistantTurn(context.Background(), r2.ID, 2, " \n\t "); err == nil || ok {
		t.Fatalf("whitespace publish ok=%v err=%v", ok, err)
	}
	if messages, _, err := s2.Messages(r2.ConversationID, 0, 50); err != nil || len(messages) != 0 {
		t.Fatalf("oversize wrote messages=%+v err=%v", messages, err)
	}
}

func TestPublishAssistantTurnRollsBackOnMessageEventFailure(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	draft, err := s.BeginStream(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_assistant_turn_event BEFORE INSERT ON events WHEN NEW.type='message' BEGIN SELECT RAISE(ABORT,'message event rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "agenda"); err == nil || ok {
		t.Fatalf("failed publish ok=%v err=%v", ok, err)
	}
	after, err := s.StreamDraft(r.ID)
	if err != nil || after.MessageID != draft.MessageID || after.Content != "" || after.Revision != 0 || after.Status != streamDraftActive {
		t.Fatalf("rollback draft=%+v initial=%+v err=%v", after, draft, err)
	}
	if messages, _, err := s.Messages(r.ConversationID, 0, 50); err != nil || len(messages) != 0 {
		t.Fatalf("rollback wrote messages=%+v err=%v", messages, err)
	}
	var publications int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM stream_assistant_turns WHERE run_id=?`, r.ID).Scan(&publications); err != nil || publications != 0 {
		t.Fatalf("rollback idempotency rows=%d err=%v", publications, err)
	}
}

func TestPublishedAssistantTurnSurvivesRestartWhileNextDraftIsInterrupted(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("restart", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	r, err := s.AddRun(b.DMConversationID, b.ID, "")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if changed, err := s.SetRunStatus(r.ID, "running", ""); err != nil || !changed {
		s.Close()
		t.Fatalf("start changed=%v err=%v", changed, err)
	}
	draft, err := s.BeginStream(r)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	published, ok, err := s.PublishAssistantTurn(context.Background(), r.ID, 1, "agenda")
	if err != nil || !ok {
		s.Close()
		t.Fatalf("publish=%+v ok=%v err=%v", published, ok, err)
	}
	rotated, err := s.StreamDraft(r.ID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	if rotated.MessageID == draft.MessageID {
		s.Close()
		t.Fatal("draft was not rotated")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	messages, _, err := restarted.Messages(b.DMConversationID, 0, 50)
	if err != nil || len(messages) != 1 || messages[0].ID != published.ID || messages[0].Content != "agenda" {
		t.Fatalf("published message after restart=%+v err=%v", messages, err)
	}
	interrupted, err := restarted.GetRun(r.ID)
	if err != nil || interrupted.Status != "interrupted" {
		t.Fatalf("run after restart=%+v err=%v", interrupted, err)
	}
	active, err := restarted.StreamDraft(r.ID)
	if err != nil || active.Status != streamDraftCancelled || active.MessageID != rotated.MessageID {
		t.Fatalf("next draft after restart=%+v err=%v", active, err)
	}
}

func TestPublishAssistantTurnContextCancellationRollsBack(t *testing.T) {
	s, r, _ := streamFixture(t)
	defer s.Close()
	if _, err := s.BeginStream(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok, err := s.PublishAssistantTurn(ctx, r.ID, 1, "agenda"); !errors.Is(err, context.Canceled) || ok {
		t.Fatalf("cancelled publish ok=%v err=%v", ok, err)
	}
	if messages, _, err := s.Messages(r.ConversationID, 0, 50); err != nil || len(messages) != 0 {
		t.Fatalf("cancelled wrote messages=%+v err=%v", messages, err)
	}
}
