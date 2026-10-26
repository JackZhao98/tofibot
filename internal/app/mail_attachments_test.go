package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareEmailTool(t *testing.T, s *Server, conv Conversation, run Run) Tool {
	t.Helper()
	tool, ok := toolNamed(s.mailDraftTools(conv, run), "prepare_email")
	if !ok {
		t.Fatal("missing prepare_email")
	}
	return tool
}

func TestMailDraftStoresAttachmentIDAndComputerFile(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("secretary", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, made := sendWithFiles(t, s, conv.ID, bot.ID, "mail it", "mail-att", map[string][]byte{"upload.pdf": []byte("%PDF-uploaded")}, []string{"upload.pdf"})
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "季度报告.pdf")
	if err := os.WriteFile(path, []byte("%PDF-made"), 0o600); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"to": "a@example.com", "subject": "报告", "body": "见附件", "attachments": []string{made[0].ID, path}})
	if _, err := prepareEmailTool(t, s, conv, run).Execute(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.store.ListMailDrafts(conv.ID)
	if err != nil || len(drafts) != 1 || len(drafts[0].Attachments) != 2 {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	got := drafts[0].Attachments
	if got[0].ID != made[0].ID || got[0].Name != "upload.pdf" || got[1].Name != "季度报告.pdf" || got[1].Size != 9 || got[1].ID == "" {
		t.Fatalf("attachments=%+v", got)
	}
	// The computer file now belongs to the conversation, so it can be sent later.
	if ok, err := s.store.attachmentAvailableInConversation(got[1].ID, conv.ID); err != nil || !ok {
		t.Fatalf("stored file not in the conversation: %v %v", ok, err)
	}
	// Editing keeps the attachments.
	edited, err := s.store.EditMailDraft(drafts[0].ID, drafts[0].Revision, "b@example.com", "报告", "改过了")
	if err != nil || len(edited.Attachments) != 2 {
		t.Fatalf("edited=%+v err=%v", edited, err)
	}
	sent, err := s.mailSendAttachments(context.Background(), edited)
	if err != nil || len(sent) != 2 || sent[1]["name"] != "季度报告.pdf" {
		t.Fatalf("send attachments=%v err=%v", sent, err)
	}
}

func TestMailDraftRefusesForeignAndOversizeAttachments(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("secretary", "", "model")
	other, _ := s.store.CreateBot("neighbour", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	otherConv, _ := s.store.GetConversation(other.DMConversationID)
	_, _, foreign := sendWithFiles(t, s, otherConv.ID, other.ID, "private", "mail-foreign", map[string][]byte{"secret.pdf": []byte("secret")}, []string{"secret.pdf"})
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "mail it", "mail-big", nil, nil)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte{7}, maxMailAttachmentBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("small"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := prepareEmailTool(t, s, conv, run)
	many := make([]string, maxMailAttachments+1)
	for i := range many {
		many[i] = "x"
	}
	for name, refs := range map[string][]string{
		"foreign ID": {foreign[0].ID},
		"unknown ID": {"no-such-id"},
		"too big":    {big},
		"too many":   many,
		"missing":    {filepath.Join(dir, "missing.txt")},
		"bad second": {small, foreign[0].ID},
	} {
		input, _ := json.Marshal(map[string]any{"subject": "s", "body": "b", "attachments": refs})
		if _, err := tool.Execute(context.Background(), input); err == nil || !strings.Contains(err.Error(), "Nothing was saved") {
			t.Errorf("%s accepted or unclear: %v", name, err)
		}
	}
	if drafts, _ := s.store.ListMailDrafts(conv.ID); len(drafts) != 0 {
		t.Fatalf("a refused draft was created: %+v", drafts)
	}
	// Nothing was stored for the refused small file either.
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE conversation_id=? AND name='small.txt'`, conv.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("leftover attachments=%d err=%v", n, err)
	}
}

func TestMailDraftSendCarriesAttachmentsToTheRunner(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	a, err := s.store.AddAttachment(conv.ID, "季度报告.pdf", "application/pdf", strings.NewReader("%PDF-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.store.AddMailDraft(context.Background(), conv.ID, run, "recipient@example.com", "报告", "见附件", false, []MailDraftAttachment{{ID: a.ID, Name: a.Name, MIME: a.MIME, Size: a.Size}})
	if err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Attachments []struct {
			Name       string `json:"name"`
			MIME       string `json:"mime"`
			DataBase64 string `json:"data_base64"`
		} `json:"attachments"`
	}
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/send") {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			_, _ = w.Write([]byte(`{"sent":true,"result":{"id":"gmail-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"connected":true,"scope":"read-send"}`))
	}))
	defer runner.Close()
	tokenFile := filepath.Join(t.TempDir(), "runner-token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenFile)
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":1}`)), "mail-drafts/"+d.ID+"/send")
	if w.Code != 200 || len(sent.Attachments) != 1 || sent.Attachments[0].Name != "季度报告.pdf" || sent.Attachments[0].MIME != "application/pdf" {
		t.Fatalf("code=%d body=%s sent=%+v", w.Code, w.Body.String(), sent)
	}
	if data, _ := base64.StdEncoding.DecodeString(sent.Attachments[0].DataBase64); string(data) != "%PDF-bytes" {
		t.Fatalf("data=%q", data)
	}
}
