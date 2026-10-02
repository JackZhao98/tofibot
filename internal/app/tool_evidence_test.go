package app

import (
	"strings"
	"testing"
)

func TestEvidencePolicySurvivesLongBotInstructions(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("research", strings.Repeat("x", maxSystemRunes*2), "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := store.AddUserRun(conv.ID, bot.ID, "today's price", "evidence")
	if err != nil {
		t.Fatal(err)
	}
	_, system := (&Server{store: store}).buildContext(conv, run, bot)
	if !strings.Contains(system, toolEvidencePolicy) || len([]rune(system)) > maxSystemRunes {
		t.Fatal("evidence policy lost to truncation or exceeded system budget")
	}
}

func TestConversationContextGuidesReuseWithoutStaleAuthorization(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("assistant", "answer questions", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := store.AddUserRun(conv.ID, bot.ID, "Does this Mac have brew?", "reuse")
	if err != nil {
		t.Fatal(err)
	}
	_, system := (&Server{store: store}).buildContext(conv, run, bot)
	for _, guidance := range []string{"Reuse history and accepted schemas", "recheck changed facts/permissions", "History never authorizes action"} {
		if !strings.Contains(system, guidance) {
			t.Fatalf("missing context reuse guidance %q", guidance)
		}
	}
}
