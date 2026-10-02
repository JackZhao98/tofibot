package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func workItemTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateWorkItems(s.db); err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func workItemString(v string) *string { return &v }
func workItemCreate(t *testing.T, s *Store, conversation, bot, kind, title string) WorkItem {
	t.Helper()
	item, err := s.CreateWorkItem(conversation, bot, WorkItemInput{Kind: kind, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func TestWorkItemsPersistAcrossConversationsAndReopen(t *testing.T) {
	s, dir := workItemTestStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	first, _ := s.CreateGroup("first group", []string{a.ID, b.ID})
	second, _ := s.CreateGroup("second group", []string{a.ID, b.ID})
	goal := workItemCreate(t, s, first.ID, a.ID, "goal", "Shared goal")
	task, err := s.CreateWorkItem(first.ID, a.ID, WorkItemInput{Kind: "task", Title: "Next contribution", Description: "Durable detail", ParentGoalID: goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	other := workItemCreate(t, s, second.ID, a.ID, "task", "Other group contribution")
	workItemCreate(t, s, second.ID, b.ID, "task", "Someone else's work")
	dm, err := s.CreateWorkItem(a.DMConversationID, "", WorkItemInput{Kind: "task", Title: "Personal work"})
	if err != nil || dm.BotID != a.ID {
		t.Fatalf("DM default=%+v %v", dm, err)
	}
	own, err := s.ListWorkItems("", a.ID, false)
	if err != nil || len(own) != 4 {
		t.Fatalf("own=%+v %v", own, err)
	}
	origins := map[string]string{}
	for _, item := range own {
		origins[item.ConversationID] = item.ConversationName
		if item.BotName != "alpha" {
			t.Fatalf("missing owner name: %+v", item)
		}
	}
	if origins[first.ID] != "first group" || origins[second.ID] != "second group" {
		t.Fatalf("origins=%+v", origins)
	}
	if _, err = s.UpdateWorkItem(task.ID, WorkItemPatch{Status: workItemString("done")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateWorkItem(other.ID, WorkItemPatch{Status: workItemString("cancelled")}); err != nil {
		t.Fatal(err)
	}
	active, _ := s.ListWorkItems("", a.ID, false)
	history, _ := s.ListWorkItems("", a.ID, true)
	if len(active) != 2 || len(history) != 2 {
		t.Fatalf("active=%+v history=%+v", active, history)
	}
	for _, item := range history {
		if item.CompletedAt == "" {
			t.Fatalf("missing terminal timestamp: %+v", item)
		}
	}
	if _, err = s.SetGroupArchived(second.ID, true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = migrateWorkItems(reopened.db); err != nil {
		t.Fatal(err)
	}
	saved, err := reopened.GetWorkItem(task.ID)
	if err != nil || saved.ParentGoalID != goal.ID || saved.Description != "Durable detail" || saved.Status != "done" {
		t.Fatalf("saved=%+v %v", saved, err)
	}
	archived, err := reopened.GetWorkItem(other.ID)
	if err != nil || archived.ConversationName != "second group" {
		t.Fatalf("archived origin=%+v %v", archived, err)
	}
	open, err := reopened.UpdateWorkItem(task.ID, WorkItemPatch{Status: workItemString("todo")})
	if err != nil || open.CompletedAt != "" {
		t.Fatalf("reopened=%+v %v", open, err)
	}
	history, _ = reopened.ListWorkItems(first.ID, a.ID, true)
	if len(history) != 0 {
		t.Fatalf("reopened retained in history: %+v", history)
	}
	var runs, schedules int
	reopened.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs)
	reopened.db.QueryRow(`SELECT COUNT(*) FROM schedules`).Scan(&schedules)
	if runs != 0 || schedules != 0 {
		t.Fatalf("recording work triggered execution: runs=%d schedules=%d", runs, schedules)
	}
}

func TestWorkItemsMigrateReviewStatusWithoutLosingExistingItems(t *testing.T) {
	s, _ := workItemTestStore(t)
	bot, err := s.CreateBot("reviewer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBot("other", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	group, err := s.CreateGroup("review group", []string{bot.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	goal := workItemCreate(t, s, group.ID, bot.ID, "goal", "Shared goal")
	task, err := s.CreateWorkItem(group.ID, bot.ID, WorkItemInput{Kind: "task", Title: "Check result", ParentGoalID: goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TABLE work_items_legacy (
 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES bots(id), kind TEXT NOT NULL CHECK(kind IN ('goal','task')),
 title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('todo','in_progress','blocked','done','cancelled')),
 parent_goal_id TEXT REFERENCES work_items_legacy(id), created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL, completed_at TEXT NOT NULL DEFAULT '');
 INSERT INTO work_items_legacy (` + workItemColumns + `) SELECT ` + workItemColumns + ` FROM work_items;
 DROP TABLE work_items;
 ALTER TABLE work_items_legacy RENAME TO work_items;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if err = migrateWorkItems(s.db); err != nil {
		t.Fatal(err)
	}
	if err = migrateWorkItems(s.db); err != nil {
		t.Fatalf("migration must be idempotent: %v", err)
	}
	got, err := s.UpdateWorkItem(task.ID, WorkItemPatch{Status: workItemString("review")})
	if err != nil || got.Status != "review" || got.ParentGoalID != goal.ID {
		t.Fatalf("review item=%+v err=%v", got, err)
	}
	open, err := s.ListWorkItems(group.ID, "", false)
	if err != nil || len(open) != 2 {
		t.Fatalf("open items=%+v err=%v", open, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration broke a foreign key")
	}
}

func TestWorkItemsValidateOwnerParentAndLengths(t *testing.T) {
	s, _ := workItemTestStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	outside, _ := s.CreateBot("outside", "", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	other, _ := s.CreateGroup("other", []string{a.ID, b.ID})
	goal := workItemCreate(t, s, other.ID, a.ID, "goal", "Elsewhere")
	task := workItemCreate(t, s, group.ID, a.ID, "task", "Existing task")
	for _, tc := range []struct {
		name, owner string
		input       WorkItemInput
	}{
		{"non-member", outside.ID, WorkItemInput{Kind: "task", Title: "title"}},
		{"missing owner", "", WorkItemInput{Kind: "task", Title: "title"}},
		{"empty title", a.ID, WorkItemInput{Kind: "task", Title: "  "}},
		{"long title", a.ID, WorkItemInput{Kind: "task", Title: strings.Repeat("文", 241)}},
		{"long description", a.ID, WorkItemInput{Kind: "task", Title: "title", Description: strings.Repeat("x", 8001)}},
		{"invalid kind", a.ID, WorkItemInput{Kind: "routine", Title: "title"}},
		{"cross-conversation parent", a.ID, WorkItemInput{Kind: "task", Title: "title", ParentGoalID: goal.ID}},
		{"task parent", a.ID, WorkItemInput{Kind: "task", Title: "title", ParentGoalID: task.ID}},
		{"missing parent", a.ID, WorkItemInput{Kind: "task", Title: "title", ParentGoalID: "absent"}},
		{"goal nesting", a.ID, WorkItemInput{Kind: "goal", Title: "title", ParentGoalID: goal.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateWorkItem(group.ID, tc.owner, tc.input); err == nil {
				t.Fatal("invalid item accepted")
			}
		})
	}
	if _, err := s.ListWorkItems("", "", false); err == nil {
		t.Fatal("unscoped query accepted")
	}
	for _, patch := range []WorkItemPatch{{}, {Status: workItemString("unknown")}, {Title: workItemString("")}, {BotID: workItemString(outside.ID)}} {
		if _, err := s.UpdateWorkItem(task.ID, patch); err == nil {
			t.Fatalf("invalid patch accepted: %+v", patch)
		}
	}
	if _, err := s.SetBotArchived(b.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkItem(group.ID, b.ID, WorkItemInput{Kind: "task", Title: "title"}); err == nil {
		t.Fatal("archived owner accepted")
	}
	if _, err := s.UpdateWorkItem(task.ID, WorkItemPatch{BotID: &b.ID}); err == nil {
		t.Fatal("archived reassignment accepted")
	}
	if _, err := s.SetGroupArchived(group.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateWorkItem(task.ID, WorkItemPatch{Status: workItemString("done")}); err == nil {
		t.Fatal("archived conversation mutated")
	}
}

func TestWorkItemEventsAreAtomic(t *testing.T) {
	s, _ := workItemTestStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	var before int
	s.db.QueryRow(`SELECT COUNT(*) FROM workspace_events`).Scan(&before)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_work_item_event BEFORE INSERT ON events WHEN NEW.type='work_item' BEGIN SELECT RAISE(ABORT,'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateWorkItem(a.DMConversationID, a.ID, WorkItemInput{Kind: "task", Title: "rollback"}); err == nil {
		t.Fatal("event failure ignored")
	}
	var count, after int
	s.db.QueryRow(`SELECT COUNT(*) FROM work_items`).Scan(&count)
	s.db.QueryRow(`SELECT COUNT(*) FROM workspace_events`).Scan(&after)
	if count != 0 || after != before {
		t.Fatalf("partial create: items=%d workspace=%d before=%d", count, after, before)
	}
	s.db.Exec(`DROP TRIGGER reject_work_item_event`)
	item := workItemCreate(t, s, a.DMConversationID, a.ID, "task", "durable")
	var data, scope string
	if err := s.db.QueryRow(`SELECT data FROM events WHERE type='work_item' AND conversation_id=? ORDER BY id DESC LIMIT 1`, a.DMConversationID).Scan(&data); err != nil {
		t.Fatal(err)
	}
	var event WorkItem
	if json.Unmarshal([]byte(data), &event) != nil || event.ID != item.ID {
		t.Fatalf("event=%s", data)
	}
	if err := s.db.QueryRow(`SELECT scope FROM workspace_events ORDER BY id DESC LIMIT 1`).Scan(&scope); err != nil || scope != workspaceScopeGroups {
		t.Fatalf("scope=%s err=%v", scope, err)
	}
	s.db.Exec(`CREATE TRIGGER reject_work_item_event BEFORE INSERT ON events WHEN NEW.type='work_item' BEGIN SELECT RAISE(ABORT,'test rejection'); END`)
	if _, err := s.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("done")}); err == nil {
		t.Fatal("update event failure ignored")
	}
	saved, _ := s.GetWorkItem(item.ID)
	if saved.Status != "todo" || saved.CompletedAt != "" {
		t.Fatalf("partial update: %+v", saved)
	}
}

func TestWorkItemToolsRespectConversationAndSelfScope(t *testing.T) {
	store, _ := workItemTestStore(t)
	server := &Server{store: store}
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	c, _ := store.CreateBot("charlie", "", "model")
	group, _ := store.CreateGroup("group", []string{a.ID, b.ID, c.ID})
	other, _ := store.CreateGroup("other", []string{a.ID, b.ID})
	foreign := workItemCreate(t, store, other.ID, a.ID, "task", "Other origin")
	workItemCreate(t, store, other.ID, b.ID, "task", "Another owner")
	tools := server.workItemTools(group, Run{BotID: a.ID})
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	result, err := byName["create_work_item"].Execute(context.Background(), []byte(`{"kind":"task","title":"Current work"}`))
	if err != nil {
		t.Fatal(err)
	}
	var created WorkItem
	if json.Unmarshal([]byte(result), &created) != nil || created.BotID != a.ID || created.ConversationID != group.ID {
		t.Fatalf("created=%s", result)
	}
	update, _ := json.Marshal(map[string]any{"work_item_id": foreign.ID, "status": "done"})
	if _, err = byName["update_work_item"].Execute(context.Background(), update); !errors.Is(err, ErrWorkItemScope) {
		t.Fatalf("cross-conversation update err=%v", err)
	}
	result, err = byName["list_work_items"].Execute(context.Background(), []byte(`{"scope":"self","history":false}`))
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []WorkItem `json:"work_items"`
	}
	if json.Unmarshal([]byte(result), &list) != nil || len(list.Items) != 2 {
		t.Fatalf("self=%s", result)
	}
	for _, item := range list.Items {
		if item.BotID != a.ID {
			t.Fatal("another owner's work leaked")
		}
	}
	result, err = byName["list_work_items"].Execute(context.Background(), []byte(`{"scope":"current_conversation","history":false}`))
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(result), &list)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("current=%s", result)
	}
	members := []string{b.ID, c.ID}
	if _, err = store.UpdateGroup(group.ID, GroupUpdate{BotIDs: &members}); err != nil {
		t.Fatal(err)
	}
	if _, err = byName["create_work_item"].Execute(context.Background(), []byte(`{"kind":"task","title":"Unauthorized","bot_id":"`+b.ID+`"}`)); err == nil {
		t.Fatal("removed actor created a record")
	}
	update, _ = json.Marshal(map[string]any{"work_item_id": created.ID, "bot_id": b.ID})
	if _, err = byName["update_work_item"].Execute(context.Background(), update); err == nil {
		t.Fatal("removed actor updated a record")
	}
}

func TestWorkItemHTTPRoutesScopeAndHistory(t *testing.T) {
	store, _ := workItemTestStore(t)
	server := &Server{store: store}
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	group, _ := store.CreateGroup("group", []string{a.ID, b.ID})
	other, _ := store.CreateGroup("other", []string{a.ID, b.ID})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/api/"+path, strings.NewReader(body))
		if !server.routeWorkItems(w, r, path) {
			t.Fatalf("route not handled: %s", path)
		}
		return w
	}
	body, _ := json.Marshal(WorkItemInput{Kind: "goal", Title: "Shared objective", BotID: a.ID})
	response := request(http.MethodPost, "conversations/"+group.ID+"/work-items", string(body))
	if response.Code != 201 {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	var item WorkItem
	json.Unmarshal(response.Body.Bytes(), &item)
	workItemCreate(t, store, other.ID, a.ID, "task", "Other group")
	workItemCreate(t, store, group.ID, b.ID, "task", "Other owner")
	response = request(http.MethodGet, "bots/"+a.ID+"/work-items", "")
	var list struct {
		Items []WorkItem `json:"work_items"`
	}
	json.Unmarshal(response.Body.Bytes(), &list)
	if response.Code != 200 || len(list.Items) != 2 {
		t.Fatalf("bot view=%d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodPatch, "work-items/"+item.ID, `{"status":"done"}`)
	if response.Code != 200 {
		t.Fatalf("patch=%d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodGet, "conversations/"+group.ID+"/work-items", "")
	json.Unmarshal(response.Body.Bytes(), &list)
	if len(list.Items) != 1 || list.Items[0].BotID != b.ID {
		t.Fatalf("active=%s", response.Body.String())
	}
	// Query parameters belong to the request URL, not the route dispatcher path.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/bots/"+a.ID+"/work-items?history=true", nil)
	server.routeWorkItems(w, r, "bots/"+a.ID+"/work-items")
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Items) != 1 || list.Items[0].ID != item.ID {
		t.Fatalf("history=%s", w.Body.String())
	}
	response = request(http.MethodPost, "conversations/"+a.DMConversationID+"/work-items", `{"kind":"task","title":"DM default"}`)
	if response.Code != 201 {
		t.Fatalf("DM=%d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodPost, "conversations/"+group.ID+"/work-items", `{"kind":"task","title":"Missing owner"}`)
	if response.Code != 400 {
		t.Fatalf("missing owner code=%d", response.Code)
	}
	response = request(http.MethodGet, "bots/missing/work-items", "")
	if response.Code != 404 {
		t.Fatalf("unknown bot code=%d", response.Code)
	}
}

func TestWorkItemArchiveMetadataPreservesOpenCommitments(t *testing.T) {
	store, _ := workItemTestStore(t)
	a, _ := store.CreateBot("alpha", "", "model")
	b, _ := store.CreateBot("bravo", "", "model")
	group, _ := store.CreateGroup("source", []string{a.ID, b.ID})
	item := workItemCreate(t, store, group.ID, a.ID, "task", "Unfinished commitment")
	if _, err := store.SetGroupArchived(group.ID, true); err != nil {
		t.Fatal(err)
	}
	active, err := store.ListWorkItems("", a.ID, false)
	if err != nil || len(active) != 1 || !active[0].ConversationArchived || active[0].BotArchived || active[0].Status != "todo" {
		t.Fatalf("archived origin=%+v %v", active, err)
	}
	history, _ := store.ListWorkItems("", a.ID, true)
	if len(history) != 0 {
		t.Fatalf("archive falsely completed work: %+v", history)
	}
	if _, err := store.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("done")}); err == nil {
		t.Fatal("archived source mutated")
	}
	if _, err := store.SetGroupArchived(group.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetBotArchived(a.ID, true); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetWorkItem(item.ID)
	if err != nil || saved.ConversationArchived || !saved.BotArchived || saved.BotName != "alpha" {
		t.Fatalf("archived owner=%+v %v", saved, err)
	}
	if _, err := store.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("done")}); err == nil {
		t.Fatal("archived owner work mutated")
	}
	if _, err := store.SetBotArchived(a.ID, false); err != nil {
		t.Fatal(err)
	}
	saved, err = store.UpdateWorkItem(item.ID, WorkItemPatch{Status: workItemString("done")})
	if err != nil || saved.ConversationArchived || saved.BotArchived || saved.CompletedAt == "" {
		t.Fatalf("restored completion=%+v %v", saved, err)
	}
	history, _ = store.ListWorkItems("", a.ID, true)
	active, _ = store.ListWorkItems("", a.ID, false)
	if len(history) != 1 || len(active) != 0 {
		t.Fatalf("terminal filtering history=%+v active=%+v", history, active)
	}
}
