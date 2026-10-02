package app

import (
	"strings"
	"testing"
)

func retryFixture(t *testing.T) (*Store, Conversation, Bot, Run) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("retry bot", "instructions", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	_, r, duplicate, err := s.AddUserRun(c.ID, b.ID, "original request", "retry-fixture")
	if err != nil || duplicate {
		s.Close()
		t.Fatalf("create run duplicate=%v err=%v", duplicate, err)
	}
	if _, err = s.SetRunStatus(r.ID, "failed", "test failure"); err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, c, b, r
}

func TestRetryRunRejectsCancelledRun(t *testing.T) {
	s, _, _, r := retryFixture(t)
	defer s.Close()
	if _, err := s.db.Exec(`UPDATE runs SET status='cancelled' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryRun(r.ID); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("RetryRun(cancelled) err=%v", err)
	}
}

func TestRetryRunRejectsWhenNewerUserMessageExists(t *testing.T) {
	s, c, _, r := retryFixture(t)
	defer s.Close()
	if _, _, err := s.AddMessage(c.ID, "user", "", "", "newer request", "newer-message"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryRun(r.ID); err == nil || !strings.Contains(err.Error(), "newer user message") {
		t.Fatalf("RetryRun(stale) err=%v", err)
	}
}

func TestRetryRunRejectsDuplicateRetry(t *testing.T) {
	s, _, _, r := retryFixture(t)
	defer s.Close()
	if _, err := s.RetryRun(r.ID); err != nil {
		t.Fatalf("first RetryRun: %v", err)
	}
	if _, err := s.RetryRun(r.ID); err == nil || !strings.Contains(err.Error(), "already has a retry") {
		t.Fatalf("second RetryRun err=%v", err)
	}
}
