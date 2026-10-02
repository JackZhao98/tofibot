package app

import (
	"bytes"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func attachmentFixture(t *testing.T) (*Store, string, string) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBot("bot", "", "model")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	c, err := s.GetConversation(b.DMConversationID)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	m, _, err := s.AddMessage(c.ID, "user", "", "", "hello", "client-1")
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	return s, c.ID, m.ID
}

func TestAttachmentOversizedRollsBackFile(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	if _, err := s.AddAttachment(conv, "big.bin", "application/octet-stream", ioLimitReader{r: strings.NewReader(strings.Repeat("x", int(maxAttachmentSize)+1))}); err == nil {
		t.Fatal("expected size error")
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM attachments").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("metadata persisted after failed upload: %d", n)
	}
	root, _ := s.attachmentRoot()
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("temporary upload left behind: %d", len(entries))
	}
}

func TestAttachmentBindingScopesAndIsIdempotent(t *testing.T) {
	s, conv, msg := attachmentFixture(t)
	defer s.Close()
	b, _ := s.CreateBot("other", "", "model")
	other, _ := s.GetConversation(b.DMConversationID)
	a, err := s.AddAttachment(conv, "note.txt", "text/plain", bytes.NewBufferString("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BindAttachments(other.ID, msg, []string{a.ID}); err == nil {
		t.Fatal("foreign conversation binding accepted")
	}
	if err := s.BindAttachments(conv, msg, []string{a.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindAttachments(conv, msg, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := s.AttachmentsForMessage(msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("unexpected bindings: %+v", got)
	}
}

// Keeps the test independent of bytes.Reader's implementation while making
// the limit behavior explicit at the call site.
type ioLimitReader struct{ r *strings.Reader }

func (r ioLimitReader) Read(p []byte) (int, error) { return r.r.Read(p) }

func TestAttachmentRouteRequiresMultipartFile(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	r := httptest.NewRequest("POST", "/api/conversations/"+conv+"/attachments", strings.NewReader("nope"))
	w := httptest.NewRecorder()
	if !(&Server{store: s}).routeAttachments(w, r, "conversations/"+conv+"/attachments") {
		t.Fatal("route not claimed")
	}
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAttachmentRouteDoesNotClaimOtherConversationRoutes(t *testing.T) {
	s, conv, _ := attachmentFixture(t)
	defer s.Close()
	r := httptest.NewRequest("GET", "/api/conversations/"+conv+"/messages", nil)
	if (&Server{store: s}).routeAttachments(httptest.NewRecorder(), r, "conversations/"+conv+"/messages") {
		t.Fatal("attachment route claimed messages endpoint")
	}
}
