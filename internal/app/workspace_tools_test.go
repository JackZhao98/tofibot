package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func workspaceTestTool(t *testing.T, s *Server, r Run, name string) Tool {
	t.Helper()
	for _, tool := range s.workspaceTools(Conversation{}, r) {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("missing workspace tool %q", name)
	return Tool{}
}

func workspaceTestRun(t *testing.T, s *Server, bot Bot) Run {
	t.Helper()
	r, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate run: ok=%v err=%v", ok, err)
	}
	r.Status = "running"
	return r
}

func workspaceJSON(t *testing.T, tool Tool, input string) map[string]any {
	t.Helper()
	output, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("tool output %q: %v", output, err)
	}
	return result
}

func TestWorkspaceToolsManageBotsGroupsAndDeleteDirectly(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("alpha", "writer", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.CreateBot("charlie", "researcher", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.store.CreateGroup("review", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.AddMessage(g.ID, "user", "", "", "history must remain", "workspace-history"); err != nil {
		t.Fatal(err)
	}
	r := workspaceTestRun(t, s, a)

	list := workspaceTestTool(t, s, r, "workspace_list")
	initial := workspaceJSON(t, list, `{}`)
	if initial["default_model"] != "model" {
		t.Fatalf("default model=%v", initial["default_model"])
	}
	if !containsJSONString(t, initial["configured_models"], "model") {
		t.Fatalf("configured models=%v", initial["configured_models"])
	}
	if !containsID(t, initial["groups"], g.ID) {
		t.Fatalf("groups=%v", initial["groups"])
	}

	updatedBot := workspaceJSON(t, workspaceTestTool(t, s, r, "workspace_update_bot"), `{"bot_id":"`+a.ID+`","name":" Alpha renamed ","instructions":"new instructions","model":"default"}`)
	if updatedBot["name"] != "Alpha renamed" || updatedBot["instructions"] != "new instructions" || updatedBot["model"] != followGlobalModel || updatedBot["reasoning_effort"] != followGlobalModel || updatedBot["effective_model"] != "model" {
		t.Fatalf("updated bot=%v", updatedBot)
	}
	storedBot, err := s.store.GetBot(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedBot.DMConversationID != a.DMConversationID {
		t.Fatalf("canonical DM changed: %q -> %q", a.DMConversationID, storedBot.DMConversationID)
	}
	if _, err := workspaceTestTool(t, s, r, "workspace_update_bot").Execute(context.Background(), []byte(`{"bot_id":"`+a.ID+`","model":"unconfigured-model"}`)); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("invalid model err=%v", err)
	}

	memberIDs := []string{b.ID, c.ID}
	sort.Strings(memberIDs)
	groupTool := workspaceTestTool(t, s, r, "workspace_update_group")
	updatedGroup := workspaceJSON(t, groupTool, `{"group_id":"`+g.ID+`","name":"review renamed","bot_ids":["`+b.ID+`","`+c.ID+`"],"expected_name":"review","expected_bot_ids":["`+a.ID+`","`+b.ID+`"]}`)
	if updatedGroup["name"] != "review renamed" {
		t.Fatalf("updated group=%v", updatedGroup)
	}
	storedGroup, err := s.store.GetConversation(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedGroup.BotIDs, memberIDs) {
		t.Fatalf("members=%v want=%v", storedGroup.BotIDs, memberIDs)
	}
	if _, err := workspaceTestTool(t, s, r, "workspace_update_group").Execute(context.Background(), []byte(`{"group_id":"`+g.ID+`","name":"silent overwrite","expected_name":"review"}`)); !errors.Is(err, ErrGroupNameConflict) {
		t.Fatalf("stale name err=%v", err)
	}

	deletedGroup := workspaceJSON(t, workspaceTestTool(t, s, r, "workspace_delete_group"), `{"group_id":"`+g.ID+`"}`)
	if deletedGroup["deleted"] != true {
		t.Fatalf("delete result=%v", deletedGroup)
	}
	if _, err = s.store.GetConversation(g.ID); err == nil {
		t.Fatal("deleted group retained")
	}
	deletedBot := workspaceJSON(t, workspaceTestTool(t, s, r, "workspace_delete_bot"), `{"bot_id":"`+c.ID+`"}`)
	if deletedBot["deleted"] != true {
		t.Fatalf("delete result=%v", deletedBot)
	}
	if _, err = s.store.GetBot(c.ID); err == nil {
		t.Fatal("deleted Bot retained")
	}
	for _, tool := range s.workspaceTools(Conversation{}, r) {
		if strings.HasPrefix(tool.Name, "workspace_archive_") {
			t.Fatal("legacy archive tool remains exposed")
		}
	}

}

func TestWorkspaceToolsRequireActiveRunAndStoreBusyGuards(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBot("alpha", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("beta", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.CreateBot("charlie", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.store.CreateGroup("busy", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	caller := workspaceTestRun(t, s, a)
	if _, err := s.store.AddRun(g.ID, b.ID, ""); err != nil {
		t.Fatal(err)
	}
	busy := workspaceTestTool(t, s, caller, "workspace_update_group")
	_, err = busy.Execute(context.Background(), []byte(`{"group_id":"`+g.ID+`","bot_ids":["`+a.ID+`","`+c.ID+`"]}`))
	if !errors.Is(err, ErrGroupBusy) {
		t.Fatalf("busy membership update err=%v", err)
	}
	if _, err := s.store.SetRunStatus(caller.ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	_, err = workspaceTestTool(t, s, caller, "workspace_list").Execute(context.Background(), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "active run") {
		t.Fatalf("inactive caller err=%v", err)
	}
}

func containsJSONString(t *testing.T, value any, want string) bool {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func containsID(t *testing.T, value any, want string) bool {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["id"] == want {
			return true
		}
	}
	return false
}

func containsArchivedEntry(t *testing.T, value any, id string) bool {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if ok && entry["id"] == id && entry["archived"] == true {
			return true
		}
	}
	return false
}
