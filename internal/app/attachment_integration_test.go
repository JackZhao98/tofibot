package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestAttachmentMessageRollbackAndVisionContext(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	c, err := s.GetConversation(conv)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateBot("other", "", "")
	if err != nil {
		t.Fatal(err)
	}
	alien, err := s.AddAttachment(other.DMConversationID, "secret.txt", "text/plain", strings.NewReader("private"))
	if err != nil {
		t.Fatal(err)
	}
	var before int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = s.AddUserRunsWithAttachments(conv, "invalid", "bad-bind", []runSpec{{BotID: c.BotID}}, []string{alien.ID})
	if err == nil {
		t.Fatal("cross-conversation attachment accepted")
	}
	var after, runs int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if before != after || runs != 0 {
		t.Fatalf("failed ingress left message/run: %d -> %d, runs=%d", before, after, runs)
	}
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err = png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAttachment(conv, "image.png", "image/png", bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	m, rr, dup, err := s.AddUserRunsWithAttachments(conv, "Describe image", "vision-1", []runSpec{{BotID: c.BotID}}, []string{a.ID})
	if err != nil || dup || len(m.Attachments) != 1 {
		t.Fatalf("ingress: %v duplicate=%v attachments=%v", err, dup, m.Attachments)
	}
	repeated, repeatedRuns, dup, err := s.AddUserRunsWithAttachments(conv, "Describe image", "vision-1", []runSpec{{BotID: c.BotID}}, []string{a.ID})
	if err != nil || !dup || repeated.ID != m.ID || repeatedRuns[0].ID != rr[0].ID {
		t.Fatal("attachment send retry duplicated ingress", err)
	}
	server := &Server{store: s}
	pm := runtime.Message{Role: "user", Content: m.Content}
	left := 3
	budget := int64(8 << 20)
	server.addAttachmentContext(c, m, &pm, &left, &budget)
	if len(pm.ImageURLs) != 1 || !strings.HasPrefix(pm.ImageURLs[0], "data:image/png;base64,") || !strings.Contains(pm.Content, a.ID) {
		t.Fatal("image missing from provider context")
	}
	if _, err = server.attachmentTools(c)[0].Execute(context.Background(), []byte(`{"attachment_id":"`+alien.ID+`"}`)); err == nil {
		t.Fatal("attachment tool leaked another DM")
	}
}

func TestAttachmentUploadDownloadAndEscape(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("file", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte("hello upload"))
	form.Close()
	req := httptest.NewRequest("POST", "/api/conversations/"+conv+"/attachments", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	server := &Server{store: s}
	server.routeAttachments(w, req, "conversations/"+conv+"/attachments")
	if w.Code != 201 {
		t.Fatalf("multipart upload: %d %s", w.Code, w.Body.String())
	}
	a, err := s.AddAttachment(conv, "escape.txt", "text/plain", strings.NewReader("safe"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, err := s.Attachment(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReadAttachmentText(a.ID, conv); err == nil {
		t.Fatal("text extraction followed escaping symlink")
	}
	w = httptest.NewRecorder()
	server.routeAttachments(w, httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil), "attachments/"+a.ID)
	if w.Code != 404 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal("download followed escaping symlink")
	}
}

func TestMessageHTTPReturnsAttachmentMetadataReadErrors(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	server := &Server{store: s, engine: testEngine{}}
	if _, err := s.db.Exec(`DROP TABLE attachment_messages`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	server.conversation(w, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conv+"/messages", nil), conv)
	if w.Code != 500 {
		t.Fatalf("message list status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDuplicateSendHTTPReturnsAttachmentMetadataReadErrors(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	c, err := s.GetConversation(conv)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.AddUserRun(conv, c.BotID, "hello", "duplicate-metadata"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`DROP TABLE attachment_messages`); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: s, engine: testEngine{}}
	request := httptest.NewRequest(http.MethodPost, "/api/conversations/"+conv+"/messages", strings.NewReader(`{"content":"hello","client_message_id":"duplicate-metadata"}`))
	response := httptest.NewRecorder()
	server.conversation(response, request, conv)
	if response.Code != 500 {
		t.Fatalf("duplicate send status=%d body=%s", response.Code, response.Body.String())
	}
}
