package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in real-provider verification. Only an access snapshot is copied into an
// isolated instance; the source credential and its refresh token are untouched.
func TestLiveCodexPermanentMemoryAfterRestart(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Skip("set TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE to run the isolated live check")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("read credential snapshot:", err)
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.AccessToken == "" {
		t.Fatal("credential snapshot unavailable")
	}
	if credential.ExpiresAt < time.Now().Add(3*time.Minute).UnixMilli() {
		t.Fatal("access snapshot needs at least 3 minutes remaining")
	}
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			s.Close()
		}
	}()
	if err = s.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("Memory acceptance", "Follow current facts and corrections. Answer the exact question concisely.", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"Project identifier is SILVER-22. The delivery owner is Ada.", "Correction: project identifier is CORAL-73, replacing SILVER-22. Ada owns delivery. Pending work is the accessibility review. Keep this as an ongoing commitment."} {
		if _, _, err = s.store.AddMessage(c.ID, "user", "", "", content, "fact-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 85; i++ {
		if _, _, err = s.store.AddMessage(c.ID, "user", "", "", "Unrelated historical discussion. "+strings.Repeat("We discussed routine housekeeping without changing project decisions. ", 5), ""); err != nil {
			t.Fatal(err)
		}
	}
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "What is the latest project identifier, who owns delivery, and what remains pending?", "live-memory")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	if _, err = s.prepareLongTermContext(ctx, s.engine, c, r, b); err != nil {
		t.Fatal("real semantic summary:", err)
	}
	var covered int64
	var summary string
	if err = s.store.db.QueryRow(`SELECT covered_seq,content FROM summaries WHERE conversation_id=? ORDER BY version DESC LIMIT 1`, c.ID).Scan(&covered, &summary); err != nil {
		t.Fatal("summary not persisted:", err)
	}
	if covered < 2 || !strings.Contains(summary, "CORAL-73") {
		t.Fatal("summary omitted corrected project identifier")
	}
	// Prevent recovery workers from executing the fixture's queued task before
	// assertions inspect the preserved conversation and checkpoint.
	if _, err = s.store.SetRunStatus(r.ID, "cancelled", ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true
	s, err = NewServer(Config{DataDir: dir, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	closed = false
	persisted, err := s.store.GetBot(b.ID)
	if err != nil || persisted.DMConversationID != c.ID {
		t.Fatal("permanent DM identity changed")
	}
	_, next, _, err := s.store.AddUserRun(c.ID, b.ID, "Return only three short fields: project identifier, delivery owner, pending work. Use the latest corrected facts.", "after-restart")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, next)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		run, e := s.store.GetRun(next.ID)
		if e != nil {
			t.Fatal(e)
		}
		if run.Status != "queued" && run.Status != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	run, err := s.store.GetRun(next.ID)
	if err != nil || run.Status != "done" {
		t.Fatalf("live response status=%s error=%s read=%v", run.Status, run.Error, err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	answer := messages[len(messages)-1].Content
	if !strings.Contains(answer, "CORAL-73") || !strings.Contains(answer, "Ada") || !strings.Contains(strings.ToLower(answer), "accessibility") || strings.Contains(answer, "SILVER-22") {
		t.Fatalf("facts not recovered from permanent DM: %s", answer)
	}
	t.Log("Real Codex semantic summary and corrected facts survived restart in the same permanent DM; access-only credential snapshot and fixture removed with temp directory.")
}
