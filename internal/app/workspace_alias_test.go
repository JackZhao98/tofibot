package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The workspace tools that predate the settings tool make the same changes, so they meet the same rules: no card, no change.
func TestWorkspaceToolsMeetTheSettingsRules(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, _ := s.store.CreateBot("alpha", "writer", "model")
	b, _ := s.store.CreateBot("beta", "reviewer", "model")
	c, _ := s.store.CreateBot("charlie", "", "model")
	g, _ := s.store.CreateGroup("review", []string{a.ID, b.ID})
	r := workspaceTestRun(t, s, a)
	code := func(err error) string {
		o, ok := tooloutcome.FromError(err)
		if !ok {
			return "untyped: " + aliasErrText(err)
		}
		return o.Code
	}

	// Another Bot's identity, a deletion and group membership each wait for the person.
	for name, in := range map[string]string{
		"workspace_update_bot":   `{"bot_id":"` + b.ID + `","name":"Renamed By Alpha"}`,
		"workspace_delete_bot":   `{"bot_id":"` + c.ID + `"}`,
		"workspace_delete_group": `{"group_id":"` + g.ID + `"}`,
	} {
		_, err := workspaceTestTool(t, s, r, name).Execute(context.Background(), []byte(in))
		if code(err) != "settings_change_needs_approval" {
			t.Errorf("%s without a card: %v", name, code(err))
		}
	}
	_, err = workspaceTestTool(t, s, r, "workspace_update_group").Execute(context.Background(), []byte(`{"group_id":"`+g.ID+`","bot_ids":["`+a.ID+`","`+c.ID+`"]}`))
	if code(err) != "settings_change_needs_approval" {
		t.Errorf("group members without a card: %v", code(err))
	}
	if got, _ := s.store.GetBot(b.ID); got.Name != "beta" {
		t.Fatalf("another Bot was renamed without a card: %q", got.Name)
	}
	if _, err := s.store.GetBot(c.ID); err != nil {
		t.Fatal("a Bot was deleted without a card")
	}
	if got, _ := s.store.GetConversation(g.ID); len(got.BotIDs) != 2 {
		t.Fatal("members changed without a card")
	}

	// A Bot's own name is direct and recorded, like the settings tool.
	if _, err := workspaceTestTool(t, s, r, "workspace_update_bot").Execute(context.Background(), []byte(`{"bot_id":"`+a.ID+`","name":"Alpha Two"}`)); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.store.recentSettingsChanges(10); len(rows) != 1 || rows[0].Key != "bot.name" || rows[0].New != "Alpha Two" {
		t.Fatalf("audit: %+v", rows)
	}

	// A run nobody attends changes nothing through any of them.
	bare, _ := s.store.AddRun(a.DMConversationID, a.ID, "")
	_, _ = s.store.SetRunStatus(bare.ID, "running", "")
	conv, _ := s.store.GetConversation(bare.ConversationID)
	for _, tool := range s.workspaceTools(conv, bare) {
		in := map[string]string{
			"workspace_update_bot":   `{"bot_id":"` + a.ID + `","name":"Nobody Asked"}`,
			"workspace_delete_bot":   `{"bot_id":"` + c.ID + `"}`,
			"workspace_delete_group": `{"group_id":"` + g.ID + `"}`,
			"workspace_update_group": `{"group_id":"` + g.ID + `","name":"Nobody Asked"}`,
		}[tool.Name]
		if in == "" {
			continue
		}
		if _, err := tool.Execute(context.Background(), []byte(in)); code(err) != "settings_change_needs_owner" {
			t.Errorf("%s unattended: %v", tool.Name, code(err))
		}
	}
	if got, _ := s.store.GetBot(a.ID); got.Name != "Alpha Two" {
		t.Fatalf("an unattended run renamed a Bot: %q", got.Name)
	}
	if !strings.Contains(workspaceTestTool(t, s, r, "workspace_delete_bot").Description, "need_approval") {
		t.Error("the tool text says what to do when a card is needed")
	}
}

func aliasErrText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
