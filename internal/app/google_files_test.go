package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// googleRunner is a stand-in for the Runner's gog endpoints.
func googleRunner(t *testing.T, s *Server, files map[string]string) {
	t.Helper()
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/v1/plugins/gog/gog/status":
			_, _ = w.Write([]byte(`{"connected":true,"email":"person@gmail.com"}`))
		case "/v1/plugins/gog/gog/attachment", "/v1/plugins/gog/gog/drive-export":
			id, _ := in["attachment_id"].(string)
			if id == "" {
				id, _ = in["file_id"].(string)
			}
			switch id {
			case "missing":
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":{"code":"scope_missing","message":"Drive is not authorised on the connected Google account. Ask the person to add Drive in Settings > Connections.","scope":"drive"}}`))
				return
			case "gone":
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"Google has no such item."}}`))
				return
			}
			name := "report.pdf"
			if strings.HasSuffix(r.URL.Path, "drive-export") {
				name = "notes.txt"
				if in["format"] == "pdf" {
					name = "notes.pdf"
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "size": len(files[id]), "data_base64": base64.StdEncoding.EncodeToString([]byte(files[id]))})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(runner.Close)
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("synthetic-token"), 0600); err != nil {
		t.Fatal(err)
	}
	s.localRunnerURL, s.localRunnerTokenFile = runner.URL, token
}

func TestGoogleFileTools(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	googleRunner(t, s, map[string]string{"att1": "%PDF-attachment", "doc1": "héllo world, a long enough line", "pdf1": "%PDF-export"})
	bot, _ := s.store.CreateBot("secretary", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "fetch", "google-files", nil, nil)
	tools := s.googleFileTools(conv, run)
	save, ok := toolNamed(tools, "google_gmail_save_attachment")
	read, ok2 := toolNamed(tools, "google_drive_read")
	if !ok || !ok2 {
		t.Fatalf("tools=%d", len(tools))
	}
	for _, tool := range tools {
		if tool.Identity(json.RawMessage(`{}`)).Risk != tooloutcome.Observation {
			t.Errorf("%s is not an observation", tool.Name)
		}
		if strings.Contains(tool.Description, " not ") && strings.Contains(tool.Description, " but ") {
			t.Errorf("%s description reads as a contrast", tool.Name)
		}
	}
	out, err := save.Execute(context.Background(), json.RawMessage(`{"message_id":"m1","attachment_id":"att1"}`))
	if err != nil || !strings.Contains(out, "/google/") || !strings.Contains(out, "report.pdf") {
		t.Fatalf("save=%q err=%v", out, err)
	}
	var saved string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, "report.pdf") {
			saved = p
		}
		return nil
	})
	if data, _ := os.ReadFile(saved); string(data) != "%PDF-attachment" {
		t.Fatalf("saved file %q = %q", saved, data)
	}
	// Text comes back in pages and the second page starts where the first ended.
	first, err := read.Execute(context.Background(), json.RawMessage(`{"file_id":"doc1","format":"txt","max_bytes":3}`))
	if err != nil || !strings.Contains(first, "call again with offset") || !strings.Contains(first, "third-party data") {
		t.Fatalf("first page=%q err=%v", first, err)
	}
	if !strings.Contains(first, "offset 3") && !strings.Contains(first, "offset 2") {
		t.Fatalf("page boundary: %q", first)
	}
	// A binary export is saved, not returned.
	out, err = read.Execute(context.Background(), json.RawMessage(`{"file_id":"pdf1","format":"pdf"}`))
	if err != nil || !strings.Contains(out, "notes.pdf") || strings.Contains(out, "%PDF") {
		t.Fatalf("pdf=%q err=%v", out, err)
	}
	// Typed refusals reach the model with the fix.
	_, err = read.Execute(context.Background(), json.RawMessage(`{"file_id":"missing"}`))
	outcome, isOutcome := tooloutcome.FromError(err)
	if !isOutcome || outcome.Code != "google_scope_missing" || !strings.Contains(outcome.Message, "Settings") {
		t.Fatalf("scope refusal=%v", err)
	}
	if _, err = read.Execute(context.Background(), json.RawMessage(`{"file_id":"gone"}`)); err == nil || !strings.Contains(err.Error(), "no such item") {
		t.Fatalf("not found: %v", err)
	}
	if _, err = read.Execute(context.Background(), json.RawMessage(`{"file_id":"doc1","max_bytes":999999}`)); err == nil {
		t.Fatal("page size above the bound accepted")
	}
}

func TestGoogleTextPageStaysOnCharacters(t *testing.T) {
	data := []byte("aé日本語")
	for offset := 0; offset < len(data); offset++ {
		if !utf8.RuneStart(data[offset]) {
			continue
		}
		for limit := 1; limit < 8; limit++ {
			page := googleTextPage("x.txt", data, offset, limit)
			body := page[strings.Index(page, "\n\n")+2:]
			if body == "" || !strings.HasPrefix(string(data[offset:]), body) {
				t.Fatalf("offset %d limit %d: %q", offset, limit, body)
			}
		}
	}
}
