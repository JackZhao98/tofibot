package app

import (
	"context"
	"testing"
)

// A profile change made through set_bot_profile after setup is a settings change like any other: it is recorded
// and the chat gets the notice with its Undo.
func TestSetBotProfileAfterSetupIsAnAuditedSettingsChange(t *testing.T) {
	e := newSettingsEnv(t)
	tool, ok := onboardingProfileTool(e.s, e.conv, e.run)
	if !ok {
		t.Fatal("set_bot_profile is not offered")
	}
	if _, err := tool.Execute(context.Background(), []byte(`{"name":"Beta"}`)); err != nil {
		t.Fatal(err)
	}
	b, err := e.s.store.GetBot(e.bot.ID)
	if err != nil || b.Name != "Beta" {
		t.Fatalf("bot = %+v %v", b, err)
	}
	var key, notice string
	if err := e.s.store.db.QueryRow(`SELECT key,notice_message_id FROM settings_changes WHERE applied=1`).Scan(&key, &notice); err != nil || key != "bot.name" || notice == "" {
		t.Fatalf("change row: key=%q notice=%q err=%v", key, notice, err)
	}
	msg, err := e.s.store.GetMessage(notice)
	if err != nil || msg.Notice == nil || !msg.Notice.Revertable || msg.Notice.NewValue != "Beta" {
		t.Fatalf("notice = %+v %v", msg.Notice, err)
	}
	// The DM carries the new name too.
	if c, _ := e.s.store.GetConversation(e.conv.ID); c.Name != "Beta" {
		t.Fatalf("DM name = %q", c.Name)
	}
	// Instructions go the same way, and an unchanged value records nothing.
	if _, err := tool.Execute(context.Background(), []byte(`{"instructions":"Keep notes tidy."}`)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.s.store.db.QueryRow(`SELECT COUNT(*) FROM settings_changes WHERE applied=1 AND key='bot.instructions'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("instruction changes = %d %v", n, err)
	}
}
