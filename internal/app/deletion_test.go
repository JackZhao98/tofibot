package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deletionStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func deletionExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}
func deletionNoRows(t *testing.T, s *Store, table, field, id string) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+field+`=?`, id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("%s retains %d records for %s: %v", table, n, id, err)
	}
}
func deletionNoFK(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after deletion")
	}
}

func TestDeleteBotPreservesSharedHistoryAndRemovesOwnedData(t *testing.T) {
	s, dir := deletionStore(t)
	a, _ := s.CreateBot("Original name", "private instructions", "model")
	b, _ := s.CreateBot("Survivor", "", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	history, err := s.AddAssistant(group.ID, a.ID, "historical-run", "shared answer")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.Event(group.ID, "message", history)
	ownGoal := workItemCreate(t, s, group.ID, a.ID, "goal", "owned goal")
	survivorTask, err := s.CreateWorkItem(group.ID, b.ID, WorkItemInput{Kind: "task", Title: "surviving task", ParentGoalID: ownGoal.ID})
	if err != nil {
		t.Fatal(err)
	}
	workItemCreate(t, s, a.DMConversationID, a.ID, "task", "private task")
	s.AddMemory(a.DMConversationID, a.ID, "private memory")
	s.AddMemory(group.ID, a.ID, "owned memory")
	s.AddMemory(group.ID, b.ID, "survivor memory")
	private, _ := s.AddAttachment(a.DMConversationID, "private.txt", "text/plain", strings.NewReader("private"))
	_, privatePath, _ := s.Attachment(private.ID)
	shared, _ := s.AddAttachment(group.ID, "shared.txt", "text/plain", strings.NewReader("shared"))
	_, sharedPath, _ := s.Attachment(shared.ID)
	deletionExec(t, s, `INSERT INTO schedules(id,conversation_id,bot_id,content,kind,timezone,next_at_utc,status,created_at,updated_at) VALUES('future',?,?, 'future work','once','UTC','2099-01-01T00:00:00Z','active',?,?)`, group.ID, a.ID, now(), now())
	run, _ := s.AddRun(group.ID, a.ID, "")
	s.SetRunStatus(run.ID, "failed", "old failure")
	user, _, _ := s.AddMessage(group.ID, "user", "", "", "historical input", "")
	deletionExec(t, s, `UPDATE runs SET trigger_message_id=? WHERE id=?`, user.ID, run.ID)
	result, err := s.DeleteBot(a.ID)
	if err != nil || !result.Deleted || result.BotID != a.ID || result.ConversationID != a.DMConversationID {
		t.Fatalf("delete=%+v %v", result, err)
	}
	for _, table := range []string{"bots", "memories", "work_items", "schedules", "members"} {
		field := "bot_id"
		if table == "bots" {
			field = "id"
		}
		deletionNoRows(t, s, table, field, a.ID)
	}
	deletionNoRows(t, s, "conversations", "id", a.DMConversationID)
	if _, err = os.Stat(privatePath); !os.IsNotExist(err) {
		t.Fatalf("owned file remains: %v", err)
	}
	if _, err = os.Stat(sharedPath); err != nil {
		t.Fatalf("surviving file removed: %v", err)
	}
	survivor, err := s.GetWorkItem(survivorTask.ID)
	if err != nil || survivor.ParentGoalID != "" {
		t.Fatalf("dangling goal relation=%+v %v", survivor, err)
	}
	kept, err := s.GetMessage(history.ID)
	if err != nil || kept.SenderBotID != a.ID || kept.SenderBotName != "Original name" || kept.Content != "shared answer" {
		t.Fatalf("shared history=%+v %v", kept, err)
	}
	if _, err = s.RetryRun(run.ID); err == nil {
		t.Fatal("deleted Bot could retry historical run")
	}
	events, err := s.Events(group.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event["type"] == "message" {
			data, _ := event["data"].(map[string]any)
			if data["id"] == history.ID {
				found = data["sender_bot_name"] == "Original name"
			}
		}
	}
	if !found {
		t.Fatal("message replay lost deleted identity")
	}
	deletionNoFK(t, s)
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	list, _, err := s.Messages(group.ID, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, m := range list {
		if m.ID == history.ID {
			found = m.SenderBotName == "Original name"
		}
	}
	if !found {
		t.Fatal("deleted identity lost on restart")
	}
	current, _ := s.GetConversation(group.ID)
	if len(current.BotIDs) != 1 || current.BotIDs[0] != b.ID {
		t.Fatalf("members=%v", current.BotIDs)
	}
	if _, err = s.DeleteBot(b.ID); err != nil {
		t.Fatal(err)
	}
	empty, _ := s.GetConversation(group.ID)
	if len(empty.BotIDs) != 0 {
		t.Fatal("last membership survived")
	}
	if _, _, _, err = s.AddUserRuns(group.ID, "no members", "none", nil); !errors.Is(err, ErrNoActiveMembers) {
		t.Fatalf("empty group ingress=%v", err)
	}
}

func TestDeleteGroupCleansFKAndUnreferencedTablesOnly(t *testing.T) {
	s, _ := deletionStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	group, _ := s.CreateGroup("delete", []string{a.ID, b.ID})
	surviving, _ := s.CreateGroup("keep", []string{a.ID, b.ID})
	run, _ := s.AddRun(group.ID, a.ID, "")
	s.SetRunStatus(run.ID, "done", "")
	message, _ := s.AddAssistant(group.ID, a.ID, run.ID, "gone")
	own, _ := s.AddAttachment(group.ID, "owned.txt", "text/plain", strings.NewReader("gone"))
	_, ownPath, _ := s.Attachment(own.ID)
	foreign, _ := s.AddAttachment(surviving.ID, "foreign.txt", "text/plain", strings.NewReader("keep"))
	_, foreignPath, _ := s.Attachment(foreign.ID)
	deletionExec(t, s, `INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, foreign.ID, message.ID)
	goal := workItemCreate(t, s, group.ID, a.ID, "goal", "goal")
	_, err := s.CreateWorkItem(group.ID, b.ID, WorkItemInput{Kind: "task", Title: "child", ParentGoalID: goal.ID})
	if err != nil {
		t.Fatal(err)
	}
	deletionExec(t, s, `INSERT INTO summaries(conversation_id,version,covered_seq,content,created_at) VALUES(?,1,1,'summary',?)`, group.ID, now())
	deletionExec(t, s, `INSERT INTO team_operations(run_id,operation_key,result,created_at) VALUES(?,'op','{}',?)`, run.ID, now())
	deletionExec(t, s, `INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,status,started_at,updated_at) VALUES(?,?,?,'call','tool','completed',?,?)`, group.ID, a.ID, run.ID, now(), now())
	deletionExec(t, s, `INSERT INTO computer_jobs(id,device_id,bot_id,run_id,action,args,status,created_at,expires_at) VALUES('job','device',?,?,'noop','{}','completed',?,?)`, a.ID, run.ID, now(), now())
	deletionExec(t, s, `INSERT INTO questions(id,run_id,conversation_id,bot_id,type,prompt,status,created_at,updated_at) VALUES('question',?,?,?,'text','question','run_done',?,?)`, run.ID, group.ID, a.ID, now(), now())
	s.BeginStream(run)
	s.AddMemory(group.ID, "", "memory")
	if _, err = s.DeleteGroup(group.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"messages", "members", "memories", "runs", "events", "summaries", "attachments", "work_items", "tool_activities", "questions", "stream_drafts"} {
		deletionNoRows(t, s, table, "conversation_id", group.ID)
	}
	for _, table := range []string{"team_operations", "computer_jobs", "stream_assistant_turns"} {
		deletionNoRows(t, s, table, "run_id", run.ID)
	}
	if _, err = s.GetBot(a.ID); err != nil {
		t.Fatal("member Bot removed")
	}
	if _, err = s.GetConversation(a.DMConversationID); err != nil {
		t.Fatal("member DM removed")
	}
	if _, err = os.Stat(ownPath); !os.IsNotExist(err) {
		t.Fatal("owned file remains")
	}
	if _, err = os.Stat(foreignPath); err != nil {
		t.Fatal("borrowed attachment removed")
	}
	if _, err = s.SaveSummary(group.ID, 1, "late summary"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted conversation summary resurrected: %v", err)
	}
	deletionNoFK(t, s)
}

func TestDeletionBusyDescendantsAndTransactions(t *testing.T) {
	s, _ := deletionStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	b, _ := s.CreateBot("bravo", "", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	root, _ := s.AddRun(group.ID, a.ID, "")
	s.SetRunStatus(root.ID, "done", "")
	child, _ := s.AddRun(b.DMConversationID, b.ID, root.ID)
	if _, err := s.DeleteGroup(group.ID); !errors.Is(err, ErrDeleteBusy) {
		t.Fatalf("descendant not busy: %v", err)
	}
	if _, err := s.DeleteBot(a.ID); !errors.Is(err, ErrDeleteBusy) {
		t.Fatalf("member descendant not busy: %v", err)
	}
	s.SetRunStatus(child.ID, "done", "")
	active, _ := s.AddRun(group.ID, b.ID, "")
	if _, err := s.DeleteBot(a.ID); !errors.Is(err, ErrDeleteBusy) {
		t.Fatalf("group roster not guarded: %v", err)
	}
	s.SetRunStatus(active.ID, "done", "")
	attachment, _ := s.AddAttachment(a.DMConversationID, "keep.txt", "text/plain", strings.NewReader("keep"))
	_, path, _ := s.Attachment(attachment.ID)
	deletionExec(t, s, `CREATE TRIGGER reject_delete_event BEFORE INSERT ON workspace_events BEGIN SELECT RAISE(ABORT,'test rollback'); END`)
	if _, err := s.DeleteBot(a.ID); err == nil {
		t.Fatal("transaction failure ignored")
	}
	if _, err := s.GetBot(a.ID); err != nil {
		t.Fatal("Bot deleted despite rollback")
	}
	if _, err := s.GetConversation(a.DMConversationID); err != nil {
		t.Fatal("DM deleted despite rollback")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("file deleted before commit")
	}
	deletionNoRows(t, s, "deleted_bot_identities", "bot_id", a.ID)
	deletionNoRows(t, s, "deleted_attachment_files", "disk_name", filepath.Base(path))
	deletionNoFK(t, s)
}

func TestDeletionAttachmentGCConfinedAndRestartSafe(t *testing.T) {
	s, dir := deletionStore(t)
	a, _ := s.CreateBot("alpha", "", "model")
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := s.attachmentRoot()
	if err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "owned-link")
	if err = os.Symlink(outside, symlink); err != nil {
		t.Fatal(err)
	}
	deletionExec(t, s, `INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES('link',?,'link','text/plain',4,'owned-link',?)`, a.DMConversationID, now())
	deletionExec(t, s, `INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES('hostile',?,'hostile','text/plain',4,'../outside.txt',?)`, a.DMConversationID, now())
	if _, err = s.DeleteBot(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(outside); err != nil {
		t.Fatal("cleanup escaped attachment root")
	}
	if _, err = os.Lstat(symlink); !os.IsNotExist(err) {
		t.Fatal("owned symlink not removed")
	}
	delayed := filepath.Join(root, "delayed.upload")
	if err = os.WriteFile(delayed, []byte("remove"), 0600); err != nil {
		t.Fatal(err)
	}
	deletionExec(t, s, `INSERT INTO deleted_attachment_files(disk_name) VALUES('delayed.upload')`)
	s.Close()
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = os.Stat(delayed); !os.IsNotExist(err) {
		t.Fatal("restart did not drain file cleanup")
	}
	if _, err = os.Stat(outside); err != nil {
		t.Fatal("restart escaped root")
	}
}

func TestDeletionRoutesToolsAndStaleCreation(t *testing.T) {
	server, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	s := server.store
	a, _ := s.CreateBot("actor", "", "model")
	b, _ := s.CreateBot("target", "", "model")
	group, _ := s.CreateGroup("group", []string{a.ID, b.ID})
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
		return w
	}
	for _, path := range []string{"/api/bots/" + b.ID + "/wrong", "/api/conversations/" + group.ID + "/wrong"} {
		if response := request(path); response.Code == 200 {
			t.Fatalf("wrong subpath accepted: %s", path)
		}
	}
	if _, err = s.GetBot(b.ID); err != nil {
		t.Fatal("wrong subpath deleted Bot")
	}
	if response := request("/api/conversations/" + a.DMConversationID); response.Code != 400 {
		t.Fatalf("DM delete status=%d", response.Code)
	}
	run := workspaceTestRun(t, server, a)
	tool := workspaceTestTool(t, server, run, "workspace_delete_bot")
	if _, err = tool.Execute(context.Background(), []byte(`{"bot_id":"`+a.ID+`"}`)); !errors.Is(err, ErrDeleteBusy) {
		t.Fatalf("self deletion=%v", err)
	}
	response := request("/api/bots/" + a.ID)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "delete_busy") {
		t.Fatalf("busy response=%d %s", response.Code, response.Body.String())
	}
	response = request("/api/bots/" + b.ID)
	if response.Code != 200 {
		t.Fatalf("delete response=%d %s", response.Code, response.Body.String())
	}
	var result DeleteResult
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || !result.Deleted || result.BotID != b.ID {
		t.Fatal("wrong delete response")
	}
	if _, err = tool.Execute(context.Background(), []byte(`{"bot_id":"missing","unexpected":true}`)); err == nil {
		t.Fatal("unknown tool property accepted")
	}
	s.SetRunStatus(run.ID, "done", "")
	if _, err = tool.Execute(context.Background(), []byte(`{"bot_id":"missing"}`)); err == nil {
		t.Fatal("inactive tool run accepted")
	}
	onboarding, _, err := s.CreateOnboardingBot(newID(), "model")
	if err != nil {
		t.Fatal(err)
	}
	var client string
	s.db.QueryRow(`SELECT client_creation_id FROM bot_onboarding WHERE bot_id=?`, onboarding.ID).Scan(&client)
	if _, err = s.DeleteBot(onboarding.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateOnboardingBot(client, "model"); err == nil {
		t.Fatal("stale client creation resurrected a deleted Bot")
	}
	if _, err = s.GetBot(onboarding.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted bot exists: %v", err)
	}
}
