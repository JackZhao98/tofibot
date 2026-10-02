package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func mailDraftFixture(t *testing.T) (*Server, Conversation, Run) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	bot, err := s.store.CreateBot("秘书", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "准备邮件", "mail-draft-test")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !changed {
		t.Fatalf("running: %v %v", changed, err)
	}
	return s, conv, run
}

func TestMailDraftOnlyHumanCanSendExactRevision(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	var tool Tool
	for _, candidate := range s.mailDraftTools(conv, run) {
		if candidate.Name == "prepare_email" {
			tool = candidate
		}
	}
	if tool.Name == "" {
		t.Fatal("missing prepare_email")
	}
	input := []byte(`{"to":"recipient@example.com","subject":"进度","body":"这是草稿"}`)
	if _, err := tool.Execute(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.store.ListMailDrafts(conv.ID)
	if err != nil || len(drafts) != 1 || drafts[0].Status != "pending" {
		t.Fatalf("drafts=%+v err=%v", drafts, err)
	}
	d := drafts[0]
	if _, err := s.store.EditMailDraft(d.ID, 1, "recipient@example.com", "进度更新", "用户修改的正文"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.EditMailDraft(d.ID, 1, d.To, d.Subject, d.Body); err == nil {
		t.Fatal("stale edit accepted")
	}
	var sends atomic.Int32
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing private token")
		}
		switch r.URL.Path {
		case "/v1/plugins/gog/gog/status":
			_, _ = w.Write([]byte(`{"connected":true,"scope":"read-send"}`))
		case "/v1/plugins/gog/gog/send":
			sends.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["body"] != "用户修改的正文" || body["subject"] != "进度更新" {
				t.Errorf("wrong exact draft: %v %+v", err, body)
			}
			_, _ = w.Write([]byte(`{"sent":true,"result":{"id":"gmail-1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	tokenFile := filepath.Join(t.TempDir(), "runner-token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenFile)
	request := func(revision int) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":`+string([]byte{byte('0' + revision)})+`}`))
		w := httptest.NewRecorder()
		s.routeMailDrafts(w, r, "mail-drafts/"+d.ID+"/send")
		return w
	}
	if w := request(1); w.Code != 409 {
		t.Fatalf("stale send=%d %s", w.Code, w.Body.String())
	}
	if w := request(2); w.Code != 200 {
		t.Fatalf("send=%d %s", w.Code, w.Body.String())
	}
	if w := request(2); w.Code != 409 {
		t.Fatalf("duplicate send=%d %s", w.Code, w.Body.String())
	}
	if sends.Load() != 1 {
		t.Fatalf("sent %d times", sends.Load())
	}
	got, err := s.store.GetMailDraft(d.ID)
	if err != nil || got.Status != "sent" {
		t.Fatalf("status=%+v %v", got, err)
	}
}

func TestMailDraftDoesNotSendWithoutScope(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	d, err := s.store.AddMailDraft(context.Background(), conv.ID, run, "recipient@example.com", "Subject", "Body", false)
	if err != nil {
		t.Fatal(err)
	}
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/send") {
			t.Error("send invoked without permission")
		}
		_, _ = w.Write([]byte(`{"connected":true,"scope":"readonly"}`))
	}))
	defer runner.Close()
	tokenFile := filepath.Join(t.TempDir(), "runner-token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenFile)
	r := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":1}`))
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, r, "mail-drafts/"+d.ID+"/send")
	if w.Code != 409 {
		t.Fatalf("code=%d %s", w.Code, w.Body.String())
	}
	got, _ := s.store.GetMailDraft(d.ID)
	if got.Status != "pending" {
		t.Fatalf("status=%s", got.Status)
	}
}

func TestMockMailDraftCanCompleteWithoutGoogleAndNeverSends(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	var tool Tool
	for _, candidate := range s.mailDraftTools(conv, run) {
		if candidate.Name == "prepare_email" {
			tool = candidate
		}
	}
	if _, err := tool.Execute(context.Background(), []byte(`{"to":"person@example.com","subject":"UI 演示","body":"演示正文","demo":true}`)); err != nil {
		t.Fatal(err)
	}
	drafts, err := s.store.ListMailDrafts(conv.ID)
	if err != nil || len(drafts) != 1 || !drafts[0].Demo {
		t.Fatalf("mock drafts=%+v %v", drafts, err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", "")
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", "")
	r := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+drafts[0].ID+"/send", strings.NewReader(`{"revision":1}`))
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, r, "mail-drafts/"+drafts[0].ID+"/send")
	if w.Code != 200 {
		t.Fatalf("mock complete=%d %s", w.Code, w.Body.String())
	}
	got, _ := s.store.GetMailDraft(drafts[0].ID)
	if got.Status != "sent" || !got.Demo {
		t.Fatalf("mock result=%+v", got)
	}
}
