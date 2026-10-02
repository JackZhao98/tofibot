package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveCodexSeesUploadedImage(t *testing.T) {
	source := os.Getenv("TOFI_ACCEPTANCE_CODEX_CREDENTIAL_FILE")
	if source == "" {
		t.Skip("opt-in isolated image acceptance")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var credential struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
		ExpiresAt   int64  `json:"expires_at"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.AccessToken == "" || credential.ExpiresAt < time.Now().Add(3*time.Minute).UnixMilli() {
		t.Fatal("fresh access snapshot unavailable")
	}
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.codex.SaveAccessOnlyCredential(credential.AccessToken, credential.AccountID, credential.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	b, err := s.store.CreateBot("Image acceptance", "Describe supplied images accurately and briefly in English. Do not infer image contents from filenames.", "")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.store.GetConversation(b.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 400, 200))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(25, 40, 155, 160), image.NewUniform(color.RGBA{R: 240, A: 255}), image.Point{}, draw.Src)
	for y := 30; y < 170; y++ {
		for x := 210; x < 350; x++ {
			if (x-280)*(x-280)+(y-100)*(y-100) <= 60*60 {
				img.Set(x, y, color.RGBA{B: 240, A: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	attachment, err := s.store.AddAttachment(c.ID, "sample.png", "image/png", bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.store.AddUserRunsWithAttachments(c.ID, "Describe the color and shape on the left, then on the right. Answer in one sentence based on the image, without using tools.", "live-vision", []runSpec{{BotID: b.ID}}, []string{attachment.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.enqueue(c, runs[0])
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		run, e := s.store.GetRun(runs[0].ID)
		if e != nil {
			t.Fatal(e)
		}
		if run.Status != "queued" && run.Status != "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	run, err := s.store.GetRun(runs[0].ID)
	if err != nil || run.Status != "done" {
		t.Fatalf("image run status=%s error=%s read=%v", run.Status, run.Error, err)
	}
	messages, _, err := s.store.Messages(c.ID, 0, 5)
	if err != nil {
		t.Fatal(err)
	}
	answer := strings.ToLower(messages[len(messages)-1].Content)
	if !strings.Contains(answer, "red") || !strings.Contains(answer, "blue") || (!strings.Contains(answer, "rectangle") && !strings.Contains(answer, "square")) || !strings.Contains(answer, "circle") {
		t.Fatalf("image did not reach real model: %s", answer)
	}
	t.Log("Real Codex identified colors and shapes from a synthetic uploaded image; no contents were disclosed in its prompt or filename.")
}
