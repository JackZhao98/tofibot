package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func maintenanceBot(t *testing.T, s *Server, name string) Bot {
	t.Helper()
	b, err := s.store.CreateBot(name, "", "model")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func patchGroupRequest(t *testing.T, s *Server, id string, payload any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("status=%d body=%s: %v", rec.Code, rec.Body.String(), err)
	}
	return rec.Code, out
}

func TestUpdateGroupRenamesAndReplacesMembersWithoutTouchingHistory(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := maintenanceBot(t, s, "alpha")
	b := maintenanceBot(t, s, "beta")
	c := maintenanceBot(t, s, "gamma")
	g, err := s.store.CreateGroup("old name", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.AddMessage(g.ID, "user", "", "", "historical message", "history-1"); err != nil {
		t.Fatal(err)
	}
	workspaceCursor := s.store.workspaceEventCursor()

	status, out := patchGroupRequest(t, s, g.ID, map[string]any{
		"name":             "new name",
		"bot_ids":          []string{b.ID, c.ID},
		"expected_name":    "old name",
		"expected_bot_ids": []string{a.ID, b.ID},
	})
	if status != http.StatusOK {
		t.Fatalf("patch status=%d response=%#v", status, out)
	}
	var updated Conversation
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &updated); err != nil {
		t.Fatal(err)
	}
	wantMembers := []string{b.ID, c.ID}
	sort.Strings(wantMembers)
	if updated.Name != "new name" || !reflect.DeepEqual(updated.BotIDs, wantMembers) {
		t.Fatalf("updated conversation=%#v", updated)
	}
	got, err := s.store.GetConversation(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new name" || !reflect.DeepEqual(got.BotIDs, wantMembers) {
		t.Fatalf("stored conversation=%#v", got)
	}
	status, _ = patchGroupRequest(t, s, g.ID, map[string]any{"name": "newer name", "expected_name": "old name"})
	if status != http.StatusConflict {
		t.Fatalf("stale name precondition status=%d", status)
	}
	messages, _, err := s.store.Messages(g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "historical message" {
		t.Fatalf("history changed=%#v", messages)
	}
	if a.DMConversationID == "" || b.DMConversationID == "" || c.DMConversationID == "" {
		t.Fatal("bot canonical DM identity was lost")
	}
	workspace, err := s.store.workspaceEvents(workspaceCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace) != 1 || workspace[0].Scope != workspaceScopeGroups {
		t.Fatalf("workspace events=%#v", workspace)
	}
	events, err := s.store.Events(g.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event["type"] == "conversation" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing conversation maintenance event: %#v", events)
	}
}

func TestUpdateGroupPreconditionBusyAndValidation(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := maintenanceBot(t, s, "alpha")
	b := maintenanceBot(t, s, "beta")
	c := maintenanceBot(t, s, "gamma")
	g, err := s.store.CreateGroup("group", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.store.AddRun(g.ID, a.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != "queued" {
		t.Fatalf("run status=%s", queued.Status)
	}
	status, out := patchGroupRequest(t, s, g.ID, map[string]any{"bot_ids": []string{b.ID, c.ID}})
	if status != http.StatusConflict || !strings.Contains(string(mustMarshalJSON(t, out)), "queued or running") {
		t.Fatalf("busy status=%d response=%#v", status, out)
	}
	got, _ := s.store.GetConversation(g.ID)
	wantOriginal := []string{a.ID, b.ID}
	sort.Strings(wantOriginal)
	if !reflect.DeepEqual(got.BotIDs, wantOriginal) {
		t.Fatalf("busy request changed members=%#v", got.BotIDs)
	}
	status, _ = patchGroupRequest(t, s, g.ID, map[string]any{"name": "renamed while queued"})
	if status != http.StatusOK {
		t.Fatalf("name-only update status=%d", status)
	}
	status, _ = patchGroupRequest(t, s, g.ID, map[string]any{"bot_ids": []string{a.ID, b.ID}, "expected_bot_ids": []string{b.ID, c.ID}})
	if status != http.StatusConflict {
		t.Fatalf("stale precondition status=%d", status)
	}
	status, _ = patchGroupRequest(t, s, g.ID, map[string]any{"bot_ids": []string{a.ID, a.ID}})
	if status != http.StatusBadRequest {
		t.Fatalf("duplicate members status=%d", status)
	}
	dm, _ := s.store.GetConversation(a.DMConversationID)
	status, _ = patchGroupRequest(t, s, dm.ID, map[string]any{"name": "not a group"})
	if status != http.StatusBadRequest {
		t.Fatalf("DM maintenance status=%d", status)
	}
	status, _ = patchGroupRequest(t, s, g.ID, map[string]any{"bot_ids": []string{a.ID, "missing"}})
	if status != http.StatusBadRequest {
		t.Fatalf("unknown member status=%d", status)
	}
}

func TestUpdateGroupEventFailureRollsBackAllChanges(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := maintenanceBot(t, s, "alpha")
	b := maintenanceBot(t, s, "beta")
	c := maintenanceBot(t, s, "gamma")
	g, err := s.store.CreateGroup("before", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	beforeWorkspace := s.store.workspaceEventCursor()
	trigger := `CREATE TRIGGER fail_group_maintenance BEFORE INSERT ON events WHEN NEW.conversation_id='` + g.ID + `' AND NEW.type='conversation' BEGIN SELECT RAISE(ABORT,'maintenance event rejected'); END`
	if _, err = s.store.db.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	status, _ := patchGroupRequest(t, s, g.ID, map[string]any{"name": "after", "bot_ids": []string{b.ID, c.ID}})
	if status != http.StatusInternalServerError {
		t.Fatalf("trigger status=%d", status)
	}
	got, err := s.store.GetConversation(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantMembers := []string{a.ID, b.ID}
	sort.Strings(wantMembers)
	if got.Name != "before" || !reflect.DeepEqual(got.BotIDs, wantMembers) {
		t.Fatalf("failed mutation committed=%#v", got)
	}
	if s.store.workspaceEventCursor() != beforeWorkspace {
		t.Fatal("workspace event escaped rolled back mutation")
	}
}

func TestSendAndHandoffRecheckMembershipAtInsert(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := maintenanceBot(t, s, "alpha")
	b := maintenanceBot(t, s, "beta")
	c := maintenanceBot(t, s, "gamma")
	g, err := s.store.CreateGroup("group", []string{a.ID, b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.UpdateGroup(g.ID, GroupUpdate{BotIDs: &[]string{b.ID, c.ID}}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.store.AddUserRuns(g.ID, "stale target", "stale-send", []runSpec{{BotID: a.ID}})
	if !errors.Is(err, ErrGroupMemberConflict) {
		t.Fatalf("stale send error=%v", err)
	}
	messages, _, err := s.store.Messages(g.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("stale send wrote history=%#v", messages)
	}
	parent, err := s.store.AddRun(g.ID, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(parent.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate parent: ok=%v err=%v", ok, err)
	}
	if _, _, err = s.store.AddHandoff(g.ID, b.ID, a.ID, parent.ID, "stale handoff"); !errors.Is(err, ErrGroupMemberConflict) {
		t.Fatalf("stale handoff error=%v", err)
	}
	gotParent, err := s.store.GetRun(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotParent.Status != "running" {
		t.Fatalf("stale handoff changed parent=%#v", gotParent)
	}
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
