package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveCodexCreatesTeamAndReturnsDiscussion(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Skip("opt-in isolated live team acceptance")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &token) != nil || token.AccessToken == "" {
		t.Fatal("credential unavailable")
	}
	if token.ExpiresAt < time.Now().Add(3*time.Minute).UnixMilli() {
		t.Fatal("access snapshot expires too soon")
	}
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.codex.SaveAccessOnlyCredential(token.AccessToken, token.AccountID, token.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	coordinator, err := s.store.CreateBot("Coordinator", "You organize a small team using the actual available team tools. Do the requested creation and dispatch. Never invent tool success. Specialists should collaborate sequentially through handoff, not repeated fan-out. Keep responses short.", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(coordinator.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	prompt := `Create two specialist Bots: Planner and Reviewer, and a group called Kickoff team containing both plus yourself. Give Planner instructions to propose a concise 30-minute kickoff agenda, then hand off to Reviewer using the actual handoff tool. Give Reviewer instructions to critique the agenda, make one concrete improvement, deliver a final agenda, and then stop without another handoff. Use send_group_message to start Planner working in the group. This is a fictional planning task, no external research needed. Do not merely describe your plan: create the team and dispatch the work now.`
	_, run, _, err := s.store.AddUserRun(c.ID, coordinator.ID, prompt, "real-team")
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, run)
	deadline := time.Now().Add(140 * time.Second)
	for time.Now().Before(deadline) {
		var active int
		if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE status IN ('queued','running')`).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active == 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	var failed, active int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE status IN ('failed','interrupted','cancelled')`).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE status IN ('queued','running')`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if failed != 0 || active != 0 {
		t.Fatalf("team did not finish cleanly: failed=%d active=%d", failed, active)
	}
	bots, err := s.store.ListBots()
	if err != nil {
		t.Fatal(err)
	}
	if len(bots) != 3 {
		t.Fatalf("created Bots=%d want3", len(bots))
	}
	groups, err := s.store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	var group Conversation
	for _, g := range groups {
		if g.Kind == "group" {
			group = g
			break
		}
	}
	if group.ID == "" || len(group.BotIDs) != 3 {
		t.Fatal("team group missing or wrong membership")
	}
	messages, _, err := s.store.Messages(group.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	var final string
	for _, m := range messages {
		if m.Role == "assistant" && m.Kind != "notice" {
			authors[m.SenderBotID] = true
			final = m.Content
		}
	}
	for _, bot := range bots {
		if (bot.Name == "Planner" || bot.Name == "Reviewer") && !authors[bot.ID] {
			t.Fatalf("specialist %s never contributed to discussion", bot.Name)
		}
	}
	if len(authors) < 2 || strings.TrimSpace(final) == "" {
		t.Fatalf("no multi-Bot discussion: %d authors", len(authors))
	}
	dm, _, err := s.store.Messages(c.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := false
	for _, m := range dm {
		if m.Content == final {
			forwarded = true
		}
	}
	if !forwarded {
		t.Fatal("discussion result was not returned to original permanent DM")
	}
	t.Log("Real Codex created two specialists, assembled a group, dispatched a sequential discussion, and returned the result to the originating DM. All fixtures isolated and removed.")
}
