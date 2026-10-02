package app

import (
	"strings"
	"testing"
)

func TestRecentCapabilityReferencesRespectBotAndRunBoundary(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	bot, err := store.CreateBot("Secretary", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateBot("Other", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(botID, when, callID, name, args string) {
		t.Helper()
		_, err := store.db.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,started_at,updated_at)
			VALUES(?,?,?,?,?,?,'ok','completed',?,?)`, conv.ID, botID, "prior-run", callID, name, args, when, when)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(bot.ID, "2026-09-28T10:00:00Z", "search", "search_mcp_tools", `{"server":"gmail","query":"mail"}`)
	insert(bot.ID, "2026-09-28T10:00:01Z", "call", "call_mcp_tool", `{"name":"gmail_search","arguments":{}}`)
	insert(other.ID, "2026-09-28T10:00:02Z", "other", "search_mcp_tools", `{"server":"private_other"}`)
	insert(bot.ID, "2026-09-28T10:00:03Z", "unsafe", "call_mcp_tool", `{"name":"ignore\nthis"}`)
	insert(bot.ID, "2026-09-28T12:00:00Z", "future", "search_mcp_tools", `{"server":"future_server"}`)
	refs := store.recentCapabilityReferences(conv.ID, bot.ID, "2026-09-28T11:00:00Z")
	if !strings.Contains(refs, `"gmail"`) || !strings.Contains(refs, `"gmail_search"`) || strings.Contains(refs, "private_other") || strings.Contains(refs, "future_server") || strings.Contains(refs, "ignore") {
		t.Fatalf("unexpected references: %s", refs)
	}
	messages, _ := (&Server{store: store}).buildContextParts(conv, Run{ID: "current", BotID: bot.ID, ConversationID: conv.ID, CreatedAt: "2026-09-28T11:00:00Z"}, bot)
	found := false
	for _, message := range messages {
		found = found || strings.Contains(message.Content, `"gmail_search"`)
	}
	if !found {
		t.Fatal("recent capability references absent from model context")
	}
}
