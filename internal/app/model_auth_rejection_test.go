package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFailRunMarksRejectedCodexSignInForReconnect(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.codex == nil || !s.codexManaged {
		t.Fatal("fixture requires the managed Codex account")
	}
	if err = s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	a, err := s.store.CreateBot("synthetic", "synthetic instructions", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(a.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if !s.modelConfigured() {
		t.Fatal("model should start configured")
	}

	// Unrelated failures never touch the account connection.
	_, unrelated, _, err := s.store.AddUserRun(c.ID, a.ID, "first", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	s.failRun(c, unrelated, errors.New("tool returned 401 for synthetic fixture"))
	if status := s.codex.Status(); !status.Connected || status.NeedsReconnect {
		t.Fatalf("unrelated failure changed status: %+v", status)
	}

	_, r, _, err := s.store.AddUserRun(c.ID, a.ID, "second", "client-2")
	if err != nil {
		t.Fatal(err)
	}
	s.failRun(c, r, errors.New(`LLM call failed: openai API error (HTTP 401): {"error":{"code":"token_invalidated"}}`))
	if status := s.codex.Status(); status.Connected || !status.NeedsReconnect {
		t.Fatalf("status after provider rejection = %+v", status)
	}
	if s.modelConfigured() {
		t.Fatal("a rejected sign-in must not report a configured model")
	}
	failed, err := s.store.GetRun(r.ID)
	if err != nil || failed.failure() == nil || failed.failure().Code != "model_auth_invalid" {
		t.Fatalf("run failure = %+v err=%v", failed.failure(), err)
	}
}

func TestVerifyCodexSignInReportsProviderVerdict(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	original := probeCodexCredential
	defer func() { probeCodexCredential = original }()
	var probes int
	verdict := func(status int, err error) {
		probeCodexCredential = func(context.Context, string) (int, error) { probes++; return status, err }
	}
	if got := s.verifyCodexSignIn(context.Background()); got != codexCheckNotConnected {
		t.Fatalf("no credential check=%s", got)
	}
	if err = s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Unreachable provider: not a verdict, connection untouched.
	verdict(0, errors.New("synthetic network failure"))
	if got := s.verifyCodexSignIn(context.Background()); got != codexCheckUnverified || !s.codex.Status().Connected {
		t.Fatalf("network check=%s status=%+v", got, s.codex.Status())
	}
	s.forgetCodexVerification()

	// Accepted: verified, and a fresh result is served without probing again.
	verdict(200, nil)
	if got := s.verifyCodexSignIn(context.Background()); got != codexCheckOK {
		t.Fatalf("accepted check=%s", got)
	}
	before := probes
	if got := s.verifyCodexSignIn(context.Background()); got != codexCheckOK || probes != before {
		t.Fatalf("cached check=%s probes=%d->%d", got, before, probes)
	}
	s.forgetCodexVerification()

	// Rejected and not refreshable: reconnect is reported and the model is unconfigured.
	verdict(401, nil)
	if got := s.verifyCodexSignIn(context.Background()); got != codexCheckRejected {
		t.Fatalf("rejected check=%s", got)
	}
	if status := s.codex.Status(); status.Connected || !status.NeedsReconnect || s.modelConfigured() {
		t.Fatalf("status after rejected check=%+v configured=%v", status, s.modelConfigured())
	}
}
