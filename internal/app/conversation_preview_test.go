package app

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

func TestListConversationsIncludesLatestPreviews(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	first, err := store.CreateBot("first", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateBot("second", "", "")
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.CreateGroup("group", []string{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AddMessage(first.DMConversationID, "user", "", "", "older", "first-1"); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("中", 160) + "尾"
	if _, _, err = store.AddMessage(group.ID, "assistant", second.ID, "run-secret", long, "group-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AddMessage(group.ID, "tool", first.ID, "tool-run", "newest", "group-2"); err != nil {
		t.Fatal(err)
	}
	// Make ordering deterministic for the assertion while preserving the
	// list query's updated_at DESC, id tie-break behavior.
	if _, err = store.db.Exec(`UPDATE conversations SET updated_at=CASE id WHEN ? THEN '2026-01-01T00:00:02Z' WHEN ? THEN '2026-01-01T00:00:01Z' WHEN ? THEN '2026-01-01T00:00:00Z' ELSE updated_at END`, group.ID, first.DMConversationID, second.DMConversationID); err != nil {
		t.Fatal(err)
	}

	conversations, err := store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(conversations) != 3 {
		t.Fatalf("conversations = %d, want 3", len(conversations))
	}
	if conversations[0].ID != group.ID || conversations[1].ID != first.DMConversationID {
		t.Fatalf("order = [%s, %s], want group then first DM", conversations[0].ID, conversations[1].ID)
	}
	if conversations[2].LastMessage != nil {
		t.Fatal("second bot DM without messages has a preview")
	}
	if conversations[2].BotIDs == nil {
		t.Fatal("empty DM bot_ids is nil")
	}
	preview := conversations[0].LastMessage
	if preview == nil {
		t.Fatal("group preview is nil")
	}
	if preview.Seq != 2 || preview.Role != "tool" || preview.SenderBotID != first.ID || preview.Content != "newest" {
		t.Fatalf("group preview = %+v", preview)
	}
	wantBotIDs := []string{first.ID, second.ID}
	sort.Strings(wantBotIDs)
	if len(conversations[0].BotIDs) != 2 || conversations[0].BotIDs[0] != wantBotIDs[0] || conversations[0].BotIDs[1] != wantBotIDs[1] {
		t.Fatalf("group bot_ids = %#v", conversations[0].BotIDs)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "run_id") {
		t.Fatalf("preview exposed run information: %s", encoded)
	}
	firstPreview := conversations[1].LastMessage
	if firstPreview == nil || firstPreview.Content != "older" || firstPreview.Seq != 1 {
		t.Fatalf("first DM preview = %+v", firstPreview)
	}

	// The long assistant message is not the latest preview, so create a
	// separate conversation to verify Unicode truncation directly.
	if _, _, err = store.AddMessage(second.DMConversationID, "assistant", second.ID, "run", long, "second-1"); err != nil {
		t.Fatal(err)
	}
	conversations, err = store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	var secondPreview *MessagePreview
	for _, conversation := range conversations {
		if conversation.ID == second.DMConversationID {
			secondPreview = conversation.LastMessage
		}
	}
	if secondPreview == nil || len([]rune(secondPreview.Content)) != 160 || !strings.HasPrefix(secondPreview.Content, strings.Repeat("中", 160)) {
		t.Fatalf("Unicode preview = %+v", secondPreview)
	}
}

func TestListConversationsEmptyReturnsEmptySlice(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	conversations, err := store.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if conversations == nil || len(conversations) != 0 {
		t.Fatalf("conversations = %#v, want empty non-nil slice", conversations)
	}
}
