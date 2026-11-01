package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func useRunnerStatus(t *testing.T, status string, onSend func(map[string]any)) {
	t.Helper()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/plugins/gog/gog/status":
			_, _ = w.Write([]byte(status))
		case "/v1/plugins/gog/gog/send":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if onSend != nil {
				onSend(body)
			}
			_, _ = w.Write([]byte(`{"sent":true,"result":{"id":"gmail-1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(runner.Close)
	tokenFile := filepath.Join(t.TempDir(), "runner-token")
	if err := os.WriteFile(tokenFile, []byte("test-token"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenFile)
}

func prepareEmail(t *testing.T, s *Server, conv Conversation, run Run, input string) (string, error) {
	t.Helper()
	for _, candidate := range s.mailDraftTools(conv, run) {
		if candidate.Name == "prepare_email" {
			return candidate.Execute(context.Background(), json.RawMessage(input))
		}
	}
	t.Fatal("missing prepare_email")
	return "", nil
}

func TestPrepareEmailReportsTheGmailConnectionState(t *testing.T) {
	cases := []struct {
		name, status, want string
	}{
		{"not connected", `{"connected":false}`, "no Google account is connected"},
		{"read only", `{"connected":true,"scope":"readonly"}`, "can read mail but not send it"},
		{"expired", `{"connected":true,"scope":"read-send","reauth_required":true}`, "expired or was revoked"},
		{"ready", `{"connected":true,"scope":"read-send"}`, "the connection is ready"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, conv, run := mailDraftFixture(t)
			useRunnerStatus(t, tc.status, nil)
			out, err := prepareEmail(t, s, conv, run, `{"to":"a@example.com","subject":"S","body":"B"}`)
			if err != nil || !strings.Contains(out, tc.want) || !strings.Contains(out, "Do not claim it was sent") {
				t.Fatalf("out=%q err=%v", out, err)
			}
			if tc.want != "the connection is ready" && !strings.Contains(out, "/settings/connections/local_gog") {
				t.Fatalf("no step named: %q", out)
			}
			drafts, _ := s.store.ListMailDrafts(conv.ID)
			if len(drafts) != 1 {
				t.Fatalf("draft not kept while sending is blocked: %d", len(drafts))
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		s, conv, run := mailDraftFixture(t)
		t.Setenv("TOFI_MCP_RUNNER_URL", "http://127.0.0.1:1")
		t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", filepath.Join(t.TempDir(), "missing"))
		out, err := prepareEmail(t, s, conv, run, `{"to":"a@example.com","subject":"S","body":"B"}`)
		if err != nil || !strings.Contains(out, "could not be checked") {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
	t.Run("demo never checks", func(t *testing.T) {
		s, conv, run := mailDraftFixture(t)
		out, err := prepareEmail(t, s, conv, run, `{"to":"a@example.com","subject":"S","body":"B","demo":true}`)
		if err != nil || strings.Contains(out, "Gmail sending") {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
}

func TestPrepareEmailCarriesCopiesAndThreadToTheSend(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	var sent map[string]any
	useRunnerStatus(t, `{"connected":true,"scope":"read-send"}`, func(body map[string]any) { sent = body })
	out, err := prepareEmail(t, s, conv, run, `{"to":"a@example.com","cc":"c1@example.com, c2@example.com","bcc":"b@example.com","thread_id":"18c0ffee","reply_to_message_id":"18c0ffef","subject":"Re: plan","body":"B"}`)
	if err != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
	drafts, _ := s.store.ListMailDrafts(conv.ID)
	d := drafts[0]
	if d.Cc != "c1@example.com,c2@example.com" || d.Bcc != "b@example.com" || d.ThreadID != "18c0ffee" || d.ReplyToMessageID != "18c0ffef" {
		t.Fatalf("draft=%+v", d)
	}
	// A person's edit that does not mention the copies keeps them; one that does replaces them.
	edited, err := s.store.EditMailDraft(d.ID, d.Revision, "a@example.com", "Re: plan", "B2")
	if err != nil || edited.Cc != d.Cc || edited.Bcc != d.Bcc || edited.ThreadID != d.ThreadID {
		t.Fatalf("edit lost the copies: %+v err=%v", edited, err)
	}
	empty := ""
	edited, err = s.store.EditMailDraftCopies(d.ID, edited.Revision, "a@example.com", "Re: plan", "B2", &empty, nil)
	if err != nil || edited.Cc != "" || edited.Bcc != "b@example.com" {
		t.Fatalf("copies edit: %+v err=%v", edited, err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":`+string(rune('0'+edited.Revision))+`}`))
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, request, "mail-drafts/"+d.ID+"/send")
	if w.Code != 200 {
		t.Fatalf("send=%d %s", w.Code, w.Body.String())
	}
	if sent["bcc"] != "b@example.com" || sent["thread_id"] != "18c0ffee" || sent["reply_to_message_id"] != "18c0ffef" || sent["cc"] != nil {
		t.Fatalf("send body=%v", sent)
	}
}

func TestPrepareEmailRefusesBadCopiesAndThreadIDs(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	for _, input := range []string{
		`{"to":"a@example.com","cc":"Name <c@example.com>","subject":"S","body":"B"}`,
		`{"to":"a@example.com","bcc":"x@example.com\nBcc: y@example.com","subject":"S","body":"B"}`,
		`{"to":"a@example.com","thread_id":"-rf","subject":"S","body":"B"}`,
		`{"to":"a@example.com","reply_to_message_id":"a b","subject":"S","body":"B"}`,
	} {
		if _, err := prepareEmail(t, s, conv, run, input); err == nil || !strings.Contains(err.Error(), "Nothing was saved") {
			t.Errorf("%s accepted: %v", input, err)
		}
	}
	if drafts, _ := s.store.ListMailDrafts(conv.ID); len(drafts) != 0 {
		t.Fatalf("drafts kept: %d", len(drafts))
	}
}

func TestSendUsesTheCopiesThePersonApprovedThroughTheCard(t *testing.T) {
	s, conv, run := mailDraftFixture(t)
	var sent map[string]any
	useRunnerStatus(t, `{"connected":true,"scope":"read-send"}`, func(body map[string]any) { sent = body })
	if out, err := prepareEmail(t, s, conv, run, `{"to":"a@example.com","cc":"bot-cc@example.com","bcc":"stranger@example.com","subject":"Plan","body":"B"}`); err != nil {
		t.Fatalf("out=%q err=%v", out, err)
	}
	drafts, _ := s.store.ListMailDrafts(conv.ID)
	d := drafts[0]
	// The card clears Bcc and replaces Cc in one PATCH.
	patch := httptest.NewRequest(http.MethodPatch, "/api/mail-drafts/"+d.ID, strings.NewReader(`{"to":"a@example.com","cc":"me@example.com","bcc":"","subject":"Plan","body":"B","revision":`+strconv.Itoa(d.Revision)+`}`))
	w := httptest.NewRecorder()
	s.routeMailDrafts(w, patch, "mail-drafts/"+d.ID)
	if w.Code != 200 {
		t.Fatalf("patch=%d %s", w.Code, w.Body.String())
	}
	edited, _ := s.store.GetMailDraft(d.ID)
	send := httptest.NewRequest(http.MethodPost, "/api/mail-drafts/"+d.ID+"/send", strings.NewReader(`{"revision":`+strconv.Itoa(edited.Revision)+`}`))
	w = httptest.NewRecorder()
	s.routeMailDrafts(w, send, "mail-drafts/"+d.ID+"/send")
	if w.Code != 200 {
		t.Fatalf("send=%d %s", w.Code, w.Body.String())
	}
	if sent["cc"] != "me@example.com" {
		t.Fatalf("cc=%v want the approved value", sent["cc"])
	}
	if _, present := sent["bcc"]; present {
		t.Fatalf("bcc=%v was removed by the person and must not be sent", sent["bcc"])
	}
}
