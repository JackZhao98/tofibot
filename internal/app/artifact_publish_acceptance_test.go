package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Run only on the isolated acceptance host after its guest contains the
// matching files.export_chunk action:
//
//	TOFI_PUBLISH_ACCEPTANCE_SOCKET=/run/tofi-computer/acceptance-a/control.sock \
//	  go test ./internal/app -run TestPublishFileRealGuestAcceptance -count=1
func TestPublishFileRealGuestAcceptance(t *testing.T) {
	socket := os.Getenv("TOFI_PUBLISH_ACCEPTANCE_SOCKET")
	if socket == "" {
		t.Skip("opt-in real guest acceptance")
	}
	if socket != "/run/tofi-computer/acceptance-a/control.sock" {
		t.Fatal("refusing non-acceptance-a socket")
	}
	s, err := NewServer(Config{Environment: "acceptance", DataDir: t.TempDir(), ComputerSocket: socket})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.CreateBot("publish file acceptance synthetic", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "publish fixtures", "publish-real-guest")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	pngBytes := generatedPNG(t)
	png64 := base64.StdEncoding.EncodeToString(pngBytes)
	command := "set -eu; printf 'persistent attachment text' > publish.txt; printf '%s' '" + png64 + "' | base64 -d > publish.png"
	if _, err = s.microVMAction(context.Background(), r, "shell.exec", json.RawMessage(`{"command":`+strconv.Quote(command)+`}`)); err != nil {
		t.Fatal(err)
	}
	tool := s.publishAttachmentTools(c, r)[0]
	textResult, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"publish.txt","request_key":"text"}`))
	if err != nil {
		t.Fatal(err)
	}
	retryResult, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"publish.txt","request_key":"text"}`))
	if err != nil || retryResult != textResult {
		t.Fatalf("retry=%s %v", retryResult, err)
	}
	if _, err = tool.Execute(context.Background(), json.RawMessage(`{"path":"publish.png","request_key":"png"}`)); err != nil {
		t.Fatal(err)
	}
	m, ok, err := s.store.FinishRun(r.ID, c.ID, b.ID, "Published text and image.")
	if err != nil || !ok || len(m.Attachments) != 2 {
		t.Fatalf("finish=%#v %v %v", m, ok, err)
	}
	// Stopping the Bot desktop proves attachment delivery has no live desktop
	// dependency; shutting down the shared acceptance VM is intentionally left
	// to coordinated infrastructure acceptance.
	_, _ = s.microVMAction(context.Background(), Run{ID: "publish-cleanup", BotID: b.ID}, "desktop.stop", json.RawMessage(`{}`))
	for _, a := range m.Attachments {
		req := httptest.NewRequest(http.MethodGet, "/api/attachments/"+a.ID, nil)
		w := httptest.NewRecorder()
		if !s.routeAttachments(w, req, "attachments/"+a.ID) || w.Code != http.StatusOK {
			t.Fatalf("download %s: %d", a.Name, w.Code)
		}
		if a.MIME == "image/png" {
			if !strings.HasPrefix(w.Header().Get("Content-Type"), "image/png") || !bytes.Equal(w.Body.Bytes(), pngBytes) {
				t.Fatal("PNG preview mismatch")
			}
		}
		if a.MIME == "text/plain; charset=utf-8" || strings.HasPrefix(a.MIME, "text/") {
			if w.Body.String() != "persistent attachment text" || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
				t.Fatal("text download mismatch")
			}
		}
	}
	_, _ = s.microVMAction(context.Background(), Run{ID: "publish-cleanup", BotID: b.ID}, "shell.exec", json.RawMessage(`{"command":"rm -f publish.txt publish.png"}`))
}
