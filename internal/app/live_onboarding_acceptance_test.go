package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLiveCodexConversationalBotOnboarding(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Skip("opt-in isolated live Bot onboarding acceptance")
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
	if json.Unmarshal(data, &token) != nil || token.AccessToken == "" || token.ExpiresAt < time.Now().Add(3*time.Minute).UnixMilli() {
		t.Fatal("a valid owner-approved access snapshot is required")
	}
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	if err = s.codex.SaveAccessOnlyCredential(token.AccessToken, token.AccountID, token.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	client := newHTTPAcceptanceClient(t, httpServer.URL)
	headers := map[string]string{"Origin": httpServer.URL}
	waitDone := func(runID string) {
		deadline := time.Now().Add(90 * time.Second)
		for {
			run, err := s.store.GetRun(runID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Status == "done" {
				return
			}
			if run.Status != "queued" && run.Status != "running" || time.Now().After(deadline) {
				t.Fatalf("run status=%s error=%s", run.Status, run.Error)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	var saved []Bot
	for _, assignment := range []string{
		"你以后负责帮我阅读文章、提炼要点和解释术语。默认用中文，简洁但不要遗漏关键限制。请你根据这个职责自己取一个简短名字，设定好职责和风格就行，暂时不需要执行任务。",
		"Quiero que me ayudes a redactar y revisar correos profesionales. Usa español, un tono amable y directo, y no envíes nada sin que yo lo pida. Elige un nombre breve para ti y guarda tu función y estilo; por ahora solo estamos definiendo tu papel.",
	} {
		status, _, body, err := client.do(http.MethodPost, "/api/bots", map[string]any{"onboarding": true, "client_creation_id": uuid.NewString()}, headers)
		if err != nil || status != http.StatusCreated {
			t.Fatalf("new Bot HTTP status=%d err=%v", status, err)
		}
		var bot Bot
		acceptanceJSON(t, body, &bot)
		if bot.Name != "New Bot" {
			t.Fatalf("initial name=%q", bot.Name)
		}
		messages, _, err := s.store.Messages(bot.DMConversationID, 0, 10)
		if err != nil || len(messages) != 1 || messages[0].Role != "assistant" || messages[0].SenderBotID != bot.ID || !strings.Contains(messages[0].Content, "?") {
			t.Fatal("new Bot did not start with one persistent role question")
		}
		status, _, body, err = client.do(http.MethodPost, "/api/conversations/"+bot.DMConversationID+"/messages", map[string]string{"content": assignment, "client_message_id": uuid.NewString()}, headers)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("assignment HTTP status=%d err=%v", status, err)
		}
		var accepted struct {
			Run Run `json:"run"`
		}
		acceptanceJSON(t, body, &accepted)
		waitDone(accepted.Run.ID)
		updated, err := s.store.GetBot(bot.ID)
		if err != nil || updated.Name == "New Bot" || len([]rune(updated.Instructions)) < 20 || updated.Model != bot.Model || updated.DMConversationID != bot.DMConversationID {
			t.Fatal("real model did not persist a role without changing identity or model")
		}
		conversation, err := s.store.GetConversation(bot.DMConversationID)
		if err != nil || conversation.Name != updated.Name {
			t.Fatal("DM name does not match persisted Bot profile")
		}
		t.Logf("saved profile: name=%s instructions=%s", updated.Name, updated.Instructions)
		initialInstructions := updated.Instructions

		status, _, body, err = client.do(http.MethodPost, "/api/conversations/"+bot.DMConversationID+"/messages", map[string]string{"content": "Please rename yourself to Repeat Profile Guide. Change only your name, preserve your current duties and instructions, and use set_bot_profile.", "client_message_id": uuid.NewString()}, headers)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("rename HTTP status=%d err=%v", status, err)
		}
		acceptanceJSON(t, body, &accepted)
		waitDone(accepted.Run.ID)
		rename, err := s.store.GetBot(bot.ID)
		if err != nil || rename.Name != "Repeat Profile Guide" || rename.Instructions != initialInstructions {
			t.Fatalf("later rename did not preserve instructions: %+v err=%v", rename, err)
		}

		status, _, body, err = client.do(http.MethodPost, "/api/conversations/"+bot.DMConversationID+"/messages", map[string]string{"content": "Now change only your durable instructions to exactly: Review planning decisions and keep key constraints visible. Keep your current name and use set_bot_profile.", "client_message_id": uuid.NewString()}, headers)
		if err != nil || status != http.StatusAccepted {
			t.Fatalf("duties HTTP status=%d err=%v", status, err)
		}
		acceptanceJSON(t, body, &accepted)
		waitDone(accepted.Run.ID)
		updated, err = s.store.GetBot(bot.ID)
		if err != nil || updated.Name != "Repeat Profile Guide" || updated.Instructions == initialInstructions || !strings.Contains(updated.Instructions, "Review planning decisions") {
			t.Fatalf("later duties update did not preserve name: %+v err=%v", updated, err)
		}
		saved = append(saved, updated)
	}
	httpServer.Close()
	s.Close()
	s, err = NewServer(Config{DataDir: dir, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range saved {
		actual, err := s.store.GetBot(expected.ID)
		if err != nil || actual != expected {
			t.Fatal("Bot profile or identity changed after restart")
		}
	}
}
