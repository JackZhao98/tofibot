package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageReactionsPersistAndRespectConversation(t *testing.T) {
	s, run, conversation := streamFixture(t)
	defer s.Close()
	message, _, err := s.AddMessage(conversation.ID, "user", "", "", "Hello", "")
	if err != nil {
		t.Fatal(err)
	}
	set := func(actor, emoji string, present bool, active *Run) []Reaction {
		t.Helper()
		items, err := s.SetMessageReaction(context.Background(), conversation.ID, message.ID, actor, emoji, present, active)
		if err != nil {
			t.Fatal(err)
		}
		return items
	}
	set("user", "👍", true, nil)
	set("bot:"+run.BotID, "🐱", true, &run)
	set("bot:"+run.BotID, "🐱", true, &run)
	messages, _, err := s.Messages(conversation.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || len(messages[0].Reactions) != 2 {
		t.Fatalf("hydrated reactions: %+v", messages)
	}
	if got := set("user", "👍", false, nil); len(got) != 1 || got[0].ActorID != run.BotID {
		t.Fatalf("remaining reactions: %+v", got)
	}
	if _, err := s.SetMessageReaction(context.Background(), conversation.ID, "unknown", "user", "👍", true, nil); !errors.Is(err, errReactionNotFound) {
		t.Fatalf("unknown message: %v", err)
	}
	if _, err := s.SetMessageReaction(context.Background(), conversation.ID, message.ID, "user", "two emoji", true, nil); !errors.Is(err, errReactionInvalid) {
		t.Fatalf("invalid emoji: %v", err)
	}
	if _, err := s.SetMessageReaction(context.Background(), conversation.ID, message.ID, "bot:other", "👍", true, &run); !errors.Is(err, errReactionForbidden) {
		t.Fatalf("foreign actor: %v", err)
	}
	events, err := s.Events(conversation.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event["type"] == "reaction" {
			count++
		}
	}
	if count != 3 {
		t.Fatalf("expected add/add/remove events, got %d", count)
	}
}

func TestBotCanReactToAnotherBotInSharedGroup(t *testing.T) {
	s, run, _ := streamFixture(t)
	defer s.Close()
	other, err := s.CreateBot("colleague", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateGroup("team", []string{run.BotID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	groupRun, err := s.AddRun(group.ID, run.BotID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetRunStatus(groupRun.ID, "running", ""); err != nil || !ok {
		t.Fatalf("start group run: %v %v", ok, err)
	}
	message, _, err := s.AddMessage(group.ID, "assistant", other.ID, "", "I finished it", "")
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.SetMessageReaction(context.Background(), group.ID, message.ID, "bot:"+run.BotID, "🎉", true, &groupRun)
	if err != nil || len(items) != 1 || items[0].ActorID != run.BotID {
		t.Fatalf("group reaction: %+v %v", items, err)
	}
	if _, err := s.SetMessageReaction(context.Background(), group.ID, message.ID, "bot:"+run.BotID, "👍", true, &run); !errors.Is(err, errReactionForbidden) {
		t.Fatalf("wrong conversation run: %v", err)
	}
}

func TestMessageReactionHTTPRoute(t *testing.T) {
	store, _, conversation := streamFixture(t)
	defer store.Close()
	message, _, err := store.AddMessage(conversation.ID, "user", "", "", "Hello", "")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store}
	path := "/api/conversations/" + conversation.ID + "/messages/" + message.ID + "/reactions"
	record := httptest.NewRecorder()
	server.conversation(record, httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"emoji":"❤️","present":true}`)), conversation.ID)
	if record.Code != http.StatusOK || !strings.Contains(record.Body.String(), `"actor_type":"user"`) {
		t.Fatalf("reaction response %d: %s", record.Code, record.Body.String())
	}
	record = httptest.NewRecorder()
	server.conversation(record, httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"emoji":"❤️"}`)), conversation.ID)
	if record.Code != http.StatusBadRequest {
		t.Fatalf("missing present status %d", record.Code)
	}
}
