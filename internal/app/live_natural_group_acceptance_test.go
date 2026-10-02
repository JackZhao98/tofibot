package app

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Opt-in acceptance only. A read-only access snapshot is copied into an
// isolated store; no production conversation or browser profile is accessed.
func TestLiveNaturalGroupConversation(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" || os.Getenv("TOFI_LIVE_GROUP_CONVERSATION") != "1" {
		t.Skip("opt-in synthetic group conversation")
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(raw, &credential) != nil || credential.AccessToken == "" || credential.ExpiresAt < time.Now().Add(8*time.Minute).UnixMilli() {
		t.Fatal("access snapshot unavailable or expires too soon")
	}
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance", DefaultModel: "codex-gpt-5.6-luna"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, spec := range []struct{ name, role string }{{"Host", "You organize community events."}, {"Editor", "You select and discuss books."}, {"Designer", "You design welcoming spaces."}} {
		b, err := s.store.CreateBot(spec.name, spec.role+" Keep group replies concise and conversational. This is a fictional discussion; no external services or research are needed.", "codex-gpt-5.6-luna")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	group, err := s.store.CreateGroup("Synthetic reading circle", ids)
	if err != nil {
		t.Fatal(err)
	}
	runRound := func(content, client string) []Message {
		t.Helper()
		trigger, runs, _, err := s.store.AddUserRuns(group.ID, content, client, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range runs {
			s.enqueue(group, r)
		}
		if len(runs) != 1 {
			t.Fatalf("unaddressed input woke %d members before a participant made a decision", len(runs))
		}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			all, err := s.store.Runs(group.ID)
			if err != nil {
				t.Fatal(err)
			}
			active := false
			for _, r := range all {
				if r.Status == "queued" || r.Status == "running" {
					active = true
				}
				if r.Status == "failed" || r.Status == "interrupted" {
					t.Fatalf("synthetic run %s: %s", r.Status, r.Error)
				}
			}
			if !active {
				messages, _, err := s.store.Messages(group.ID, 0, 100)
				if err != nil {
					t.Fatal(err)
				}
				var out []Message
				for _, m := range messages {
					if m.Seq > trigger.Seq && m.Role == "assistant" {
						out = append(out, m)
					}
				}
				return out
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("group round did not finish")
		return nil
	}
	replies := runRound("We are planning a fictional neighborhood reading evening. I'd like each of you to offer one different, practical suggestion from your own perspective. Keep it to one or two sentences and build on what the others say.", "natural-round")
	authors := map[string]bool{}
	for _, m := range replies {
		if m.Kind == "notice" {
			t.Fatal("natural conversation unexpectedly required a handoff")
		}
		authors[m.SenderBotID] = true
		t.Logf("synthetic reply: %s", m.Content)
	}
	if len(authors) != 3 {
		t.Fatalf("expected three actual participants, got %d", len(authors))
	}
	one := runRound("Just one of you please: suggest a short title for this evening. One title is enough; no need for the rest of the group to weigh in.", "one-member")
	oneAuthor := map[string]bool{}
	for _, m := range one {
		oneAuthor[m.SenderBotID] = true
	}
	if len(oneAuthor) != 1 {
		t.Fatalf("single-reply request produced %d authors", len(oneAuthor))
	}
	silent := runRound("The discussion is finished. Please do not post any further replies to this message.", "silent-round")
	if len(silent) != 0 {
		t.Fatalf("explicitly finished conversation produced %d unnecessary replies", len(silent))
	}
	t.Log("one random starter invited three actual participants when requested; single-reply request stayed scoped; finished conversation stayed silent")
}
