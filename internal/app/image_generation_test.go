package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func generatedPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func runningImageFixture(t *testing.T) (*Server, Conversation, Run, Bot) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b, err := s.store.CreateBot("image bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "generate", "image-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running=%v %v", ok, err)
	}
	return s, c, r, b
}

func TestImageGenerationStagesIdempotentlyAndBindsRealSender(t *testing.T) {
	s, c, r, b := runningImageFixture(t)
	var calls atomic.Int32
	data := generatedPNG(t)
	tool := s.imageGenerationTool(c, r, func(context.Context, provider.ImageRequest) ([]byte, error) { calls.Add(1); return data, nil })
	raw := json.RawMessage(`{"prompt":"red square","name":"result"}`)
	first, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || calls.Load() != 1 {
		t.Fatalf("idempotency calls=%d first=%s second=%s", calls.Load(), first, second)
	}
	m, ok, err := s.store.FinishRun(r.ID, c.ID, b.ID, "Here is the image.")
	if err != nil || !ok {
		t.Fatalf("finish=%v %v", ok, err)
	}
	if m.SenderBotID != b.ID || len(m.Attachments) != 1 || m.Attachments[0].MIME != "image/png" {
		t.Fatalf("message=%#v", m)
	}
	_, path, err := s.store.Attachment(m.Attachments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("persisted PNG mismatch", err)
	}
}

func TestImageGenerationRejectsForeignReferenceAndCancellationLeavesNothing(t *testing.T) {
	s, c, r, _ := runningImageFixture(t)
	other, _ := s.store.CreateBot("other", "", "model")
	alien, _ := s.store.GetConversation(other.DMConversationID)
	foreign, _ := s.store.AddAttachment(alien.ID, "foreign.png", "image/png", bytes.NewReader(generatedPNG(t)))
	var calls atomic.Int32
	tool := s.imageGenerationTool(c, r, func(ctx context.Context, _ provider.ImageRequest) ([]byte, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"edit","reference_attachment_id":"`+foreign.ID+`"}`)); err == nil || !strings.Contains(err.Error(), "another conversation") {
		t.Fatalf("foreign err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := tool.Execute(ctx, json.RawMessage(`{"prompt":"cancel me"}`)); done <- err }()
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled generation succeeded")
	}
	var n int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, r.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("pending=%d err=%v", n, err)
	}
}

func TestImageGenerationRejectsTruncatedPNGWithoutStaging(t *testing.T) {
	valid := generatedPNG(t)
	for name, data := range map[string][]byte{
		"header_only":  valid[:33],
		"missing_iend": valid[:len(valid)-12],
	} {
		t.Run(name, func(t *testing.T) {
			s, c, r, _ := runningImageFixture(t)
			if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err != nil || format != "png" {
				t.Fatalf("fixture must pass header validation: format=%s err=%v", format, err)
			}
			tool := s.imageGenerationTool(c, r, func(context.Context, provider.ImageRequest) ([]byte, error) { return data, nil })
			if result, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"broken image"}`)); err == nil || result != "" {
				t.Fatalf("truncated PNG accepted: result=%s err=%v", result, err)
			}
			for _, table := range []string{"attachments", "run_attachments"} {
				var n int
				if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("%s count=%d err=%v", table, n, err)
				}
			}
			root, err := s.store.attachmentRoot()
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("orphan files=%v err=%v", entries, err)
			}
		})
	}
}

func coloredPNG(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			im.Set(x, y, color.RGBA{R: shade, G: uint8(x * 7), B: uint8(y * 3), A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func imageResultFields(t *testing.T, out string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("result is not JSON: %v: %.200s", err, out)
	}
	return m
}

func TestImageGenerationResultCarriesBoundedImage(t *testing.T) {
	s, c, r, _ := runningImageFixture(t)
	big := coloredPNG(t, 3000, 1200, 40)
	tool := s.imageGenerationTool(c, r, func(context.Context, provider.ImageRequest) ([]byte, error) { return big, nil })
	raw := json.RawMessage(`{"prompt":"wide banner"}`)
	for i := 0; i < 2; i++ { // second pass is the same-run cache hit
		out, err := tool.Execute(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		m := imageResultFields(t, out)
		if m["attachment_id"] == nil || m["delivery"] == nil {
			t.Fatalf("pass %d lost metadata: %.200s", i, out)
		}
		uri, _ := m["image_url"].(string)
		prefix, enc, ok := strings.Cut(uri, ";base64,")
		if !ok || (prefix != "data:image/png" && prefix != "data:image/jpeg") {
			t.Fatalf("pass %d has no image content: %.120s", i, uri)
		}
		pix, err := base64.StdEncoding.DecodeString(enc)
		if err != nil || len(pix) > viewDeliveredMax {
			t.Fatalf("pass %d image bytes=%d err=%v", i, len(pix), err)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(pix))
		if err != nil || max(cfg.Width, cfg.Height) > viewLongSideMax {
			t.Fatalf("pass %d dims=%dx%d err=%v", i, cfg.Width, cfg.Height, err)
		}
	}
}

func TestImageGenerationEditsJustGeneratedImageAndReplacesBase(t *testing.T) {
	s, c, r, b := runningImageFixture(t)
	var refs []string
	n := uint8(0)
	tool := s.imageGenerationTool(c, r, func(_ context.Context, in provider.ImageRequest) ([]byte, error) {
		refs = append(refs, in.Reference)
		n += 50
		return coloredPNG(t, 16, 16, n), nil
	})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"base"}`))
	if err != nil {
		t.Fatal(err)
	}
	base := imageResultFields(t, out)["attachment_id"].(string)
	out, err = tool.Execute(context.Background(), json.RawMessage(`{"prompt":"adjust","reference_attachment_id":"`+base+`"}`))
	if err != nil {
		t.Fatalf("editing a run-bound image failed: %v", err)
	}
	m := imageResultFields(t, out)
	edit := m["attachment_id"].(string)
	if edit == base || m["image_url"] == nil {
		t.Fatalf("edit result: %.200s", out)
	}
	if len(refs) != 2 || refs[0] != "" || !strings.HasPrefix(refs[1], "data:image/png;base64,") {
		t.Fatalf("provider references: %v", refs)
	}
	msg, ok, err := s.store.FinishRun(r.ID, c.ID, b.ID, "Done.")
	if err != nil || !ok {
		t.Fatalf("finish=%v %v", ok, err)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].ID != edit {
		t.Fatalf("final reply attachments: %#v (want only %s)", msg.Attachments, edit)
	}
}

func TestImageGenerationIndependentImagesAllAttach(t *testing.T) {
	s, c, r, b := runningImageFixture(t)
	n := uint8(0)
	tool := s.imageGenerationTool(c, r, func(context.Context, provider.ImageRequest) ([]byte, error) {
		n += 50
		return coloredPNG(t, 16, 16, n), nil
	})
	for _, p := range []string{"one", "two"} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"`+p+`"}`)); err != nil {
			t.Fatal(err)
		}
	}
	msg, ok, err := s.store.FinishRun(r.ID, c.ID, b.ID, "Done.")
	if err != nil || !ok || len(msg.Attachments) != 2 {
		t.Fatalf("finish ok=%v err=%v attachments=%d", ok, err, len(msg.Attachments))
	}
}

func TestImageGenerationImageBudgetPerRun(t *testing.T) {
	s, c, r, _ := runningImageFixture(t)
	n := 0
	tool := s.imageGenerationTool(c, r, func(context.Context, provider.ImageRequest) ([]byte, error) {
		n++
		return coloredPNG(t, 8+n, 8, uint8(n)), nil
	})
	withImage := 0
	for i := 0; i < viewsPerRunMax+2; i++ {
		// each call edits nothing; same-run cache hits count as views too
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"prompt":"p`+strings.Repeat("x", i%3)+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		if imageResultFields(t, out)["image_url"] != nil {
			withImage++
		}
	}
	if withImage != viewsPerRunMax {
		t.Fatalf("images delivered=%d want %d", withImage, viewsPerRunMax)
	}
}
