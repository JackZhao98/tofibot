package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func readAttachmentOut(t *testing.T, s *Server, c Conversation, r Run, raw string) map[string]any {
	t.Helper()
	out, err := s.attachmentTools(c, r)[0].Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Whatever the conversation holds, read_attachment opens it: text pages on,
// an image is shown, and any other file lands on the computer.
func TestReadAttachmentOpensTextImagesAndOtherFiles(t *testing.T) {
	s, _, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("reader", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	long := strings.Repeat("字", maxAttachmentText/3+10)
	pdf := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{0, 1, 2}, 50)...)
	_, run, files := sendWithFiles(t, s, conv.ID, bot.ID, "files", "ra-1", map[string][]byte{
		"long.txt": []byte(long), "pic.png": generatedPNG(t), "doc.pdf": pdf,
	}, []string{"long.txt", "pic.png", "doc.pdf"})
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}

	first := readAttachmentOut(t, s, conv, run, `{"attachment_id":"`+files[0].ID+`"}`)
	next, ok := first["next_offset"].(float64)
	if !ok || next <= 0 {
		t.Fatalf("long text gave no next_offset: %v", first["next_offset"])
	}
	rest := readAttachmentOut(t, s, conv, run, `{"attachment_id":"`+files[0].ID+`","offset":`+jsonNumber(next)+`}`)
	if first["content"].(string)+rest["content"].(string) != long || rest["next_offset"] != nil {
		t.Fatal("paged text does not reassemble")
	}

	img := readAttachmentOut(t, s, conv, run, `{"attachment_id":"`+files[1].ID+`"}`)
	if u, _ := img["image_url"].(string); !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("image not shown: %v", img)
	}

	doc := readAttachmentOut(t, s, conv, run, `{"attachment_id":"`+files[2].ID+`"}`)
	p, _ := doc["computer_path"].(string)
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, pdf) {
		t.Fatalf("pdf not placed at %q: %v", p, err)
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(int64(f))
	return string(b)
}

// A WebP (here a 1x1 lossless one) is viewable and converted to PNG.
func TestViewImageResultConvertsWebP(t *testing.T) {
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	out, err := viewImageResult("a.webp", webp)
	if err != nil || !strings.Contains(out, "data:image/png;base64,") {
		t.Fatalf("view: %.80s %v", out, err)
	}
	if u, err := imageReferenceDataURL("a.webp", webp); err != nil || !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Fatalf("reference: %.40s %v", u, err)
	}
}
