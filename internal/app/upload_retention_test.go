package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// retentionFixture is a server with one conversation holding five files, all
// older than the cutoff: an upload, a Bot file, an artifact, a trace and a kept upload.
func retentionFixture(t *testing.T) (s *Server, c Conversation, ids map[string]string) {
	t.Helper()
	s = providerServer(t, nil, nil)
	b, err := s.store.CreateBot("keeper", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if c, err = s.store.GetConversation(b.DMConversationID); err != nil {
		t.Fatal(err)
	}
	ids = map[string]string{}
	for name, origin := range map[string]string{"upload": attachmentOriginUpload, "bot_file": attachmentOriginBotFile, "artifact": attachmentOriginPublished, "trace": attachmentOriginTrace, "kept": attachmentOriginUpload} {
		a, err := s.store.addAttachmentOpts(context.Background(), c.ID, name+".txt", "text/plain", strings.NewReader("data of "+name), attachmentOpts{Origin: origin})
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = a.ID
	}
	if _, err = s.store.db.Exec(`UPDATE attachments SET created_at=? `, stampTime(time.Now().Add(-40*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE attachments SET kept_by_bot_id=? WHERE id=?`, b.ID, ids["kept"]); err != nil {
		t.Fatal(err)
	}
	return s, c, ids
}

func expiredAt(t *testing.T, s *Server, id string) bool {
	t.Helper()
	var at string
	if err := s.store.db.QueryRow(`SELECT COALESCE(expired_at,'') FROM attachments WHERE id=?`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != ""
}

func TestUploadRetentionExpiresOnlyOldUnkeptUploadsAndBotFiles(t *testing.T) {
	s, _, ids := retentionFixture(t)
	setAuto(t, s, autoUploadRetentionDays, "0")
	s.runUploadRetention(time.Now())
	for name, id := range ids {
		if expiredAt(t, s, id) {
			t.Fatalf("%s expired with the setting at 0", name)
		}
	}
	setAuto(t, s, autoUploadRetentionDays, "30")
	s.runUploadRetention(time.Now())
	for name, want := range map[string]bool{"upload": true, "bot_file": true, "artifact": false, "trace": false, "kept": false} {
		if got := expiredAt(t, s, ids[name]); got != want {
			t.Fatalf("%s expired=%v, want %v", name, got, want)
		}
	}
	// A file younger than the period stays.
	a, err := s.store.addAttachmentOpts(context.Background(), mustConv(t, s, ids["upload"]), "new.txt", "text/plain", strings.NewReader("new"), attachmentOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.runUploadRetention(time.Now())
	if expiredAt(t, s, a.ID) {
		t.Fatal("a new upload expired")
	}
}

func uploadsBytes(t *testing.T, s *Server) int64 {
	t.Helper()
	var n int64
	if err := s.store.db.QueryRow(`SELECT bytes FROM storage_counters WHERE class='uploads'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustConv(t *testing.T, s *Server, attachmentID string) string {
	t.Helper()
	var conv string
	if err := s.store.db.QueryRow(`SELECT conversation_id FROM attachments WHERE id=?`, attachmentID).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	return conv
}

func TestExpiredAttachmentIsATombstoneThatNamesItself(t *testing.T) {
	s, c, ids := retentionFixture(t)
	setAuto(t, s, autoUploadRetentionDays, "30")
	before := uploadsBytes(t, s)
	s.runUploadRetention(time.Now())
	if after := uploadsBytes(t, s); after >= before {
		t.Fatalf("uploads counter %d did not fall from %d", after, before)
	}
	if _, _, err := s.store.Attachment(ids["upload"]); !errors.Is(err, errAttachmentExpired) || !strings.Contains(err.Error(), codeAttachmentExpired) {
		t.Fatalf("read error = %v", err)
	}
	if _, _, err := s.store.ReadAttachmentText(ids["upload"], c.ID); !errors.Is(err, errAttachmentExpired) {
		t.Fatalf("ReadAttachmentText error = %v", err)
	}
	rec := httptest.NewRecorder()
	s.routeAttachments(rec, httptest.NewRequest(http.MethodGet, "/api/attachments/"+ids["upload"], nil), "attachments/"+ids["upload"])
	if rec.Code != 410 || !bytes.Contains(rec.Body.Bytes(), []byte(codeAttachmentExpired)) {
		t.Fatalf("download = %d %s", rec.Code, rec.Body.String())
	}
	m, _, err := s.store.AddMessage(c.ID, "user", "", "", "look", "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.store.BindAttachments(c.ID, m.ID, []string{ids["upload"]}); err != nil {
		t.Fatal(err)
	}
	list, err := s.store.AttachmentsForMessage(m.ID)
	if err != nil || len(list) != 1 || list[0].ExpiredAt == "" {
		t.Fatalf("card data = %+v %v", list, err)
	}
	// The Bot tool answers with the same named error.
	tool, _ := toolNamed(s.attachmentTools(c, Run{}), "read_attachment")
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"attachment_id":"`+ids["upload"]+`"}`)); err == nil || !strings.Contains(err.Error(), codeAttachmentExpired) {
		t.Fatalf("read_attachment error = %v", err)
	}
}

func TestKeepFileMarksTheRowAndNamesTheBot(t *testing.T) {
	s, c, ids := retentionFixture(t)
	if _, err := s.store.db.Exec(`UPDATE attachments SET kept_by_bot_id=NULL`); err != nil {
		t.Fatal(err)
	}
	run := Run{BotID: c.BotID}
	keep := func(id string) (string, error) {
		tool, ok := toolNamed(s.keepFileTools(c, run), "keep_file")
		if !ok {
			t.Fatal("keep_file missing")
		}
		return tool.Execute(context.Background(), json.RawMessage(`{"attachment_id":"`+id+`"}`))
	}
	// A server that never deletes says so and changes nothing.
	setAuto(t, s, autoUploadRetentionDays, "0")
	out, err := keep(ids["upload"])
	if err != nil || !strings.Contains(out, "does not delete uploaded files automatically") {
		t.Fatalf("%s %v", out, err)
	}
	var keeper string
	s.store.db.QueryRow(`SELECT COALESCE(kept_by_bot_id,'') FROM attachments WHERE id=?`, ids["upload"]).Scan(&keeper)
	if keeper != "" {
		t.Fatal("keep_file changed a row on a server that never deletes")
	}
	setAuto(t, s, autoUploadRetentionDays, "30")
	if out, err = keep(ids["upload"]); err != nil || !strings.Contains(out, `"kept":true`) {
		t.Fatalf("%s %v", out, err)
	}
	// An artifact is not auto-deleted: nothing to keep, nothing changed.
	if out, err = keep(ids["artifact"]); err != nil || !strings.Contains(out, `"kept":false`) {
		t.Fatalf("%s %v", out, err)
	}
	s.runUploadRetention(time.Now())
	if expiredAt(t, s, ids["upload"]) || !expiredAt(t, s, ids["bot_file"]) {
		t.Fatal("the sweep ignored the keep mark or skipped the unkept file")
	}
	m, _, _ := s.store.AddMessage(c.ID, "user", "", "", "x", "c2")
	if err = s.store.BindAttachments(c.ID, m.ID, []string{ids["upload"]}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.store.AttachmentsForMessage(m.ID); len(list) != 1 || list[0].KeptBy != "keeper" {
		t.Fatalf("card data = %+v", list)
	}
	// An expired file cannot be kept.
	if _, err = keep(ids["bot_file"]); err == nil || !strings.Contains(err.Error(), codeAttachmentExpired) {
		t.Fatalf("keep expired: %v", err)
	}
	// The setting removes the tool.
	setAuto(t, s, autoBotsKeepFiles, "false")
	if _, ok := toolNamed(s.keepFileTools(c, run), "keep_file"); ok || s.uploadRetentionPrompt() != "" {
		t.Fatal("keep_file offered with the setting off")
	}
}

func TestUploadRetentionDefaultFollowsTheAccountServer(t *testing.T) {
	s := providerServer(t, nil, nil)
	if got := s.autoInt(autoUploadRetentionDays); got != 0 {
		t.Fatalf("single-account default = %d, want 0", got)
	}
	s.accountID = "acct"
	if got := s.autoInt(autoUploadRetentionDays); got != 30 {
		t.Fatalf("account default = %d, want 30", got)
	}
	if p := s.uploadRetentionPrompt(); !strings.Contains(p, "30 days") || !strings.Contains(p, "keep_file") {
		t.Fatalf("prompt = %q", p)
	}
}
