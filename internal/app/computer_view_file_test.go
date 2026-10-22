package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type viewResult struct {
	Path         string `json:"path"`
	ImageURL     string `json:"image_url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	SourceWidth  int    `json:"source_width"`
	SourceHeight int    `json:"source_height"`
}

// decodeViewResult checks the result is a bounded data URI of an image the
// vision path accepts and returns the decoded format.
func decodeViewResult(t *testing.T, out string) (viewResult, string) {
	t.Helper()
	var res viewResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	prefix, encoded, ok := strings.Cut(res.ImageURL, ";base64,")
	if !ok {
		t.Fatalf("not a data URI: %.80s", res.ImageURL)
	}
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(pixels) > viewDeliveredMax {
		t.Fatalf("delivered %d bytes, over the %d cap", len(pixels), viewDeliveredMax)
	}
	decoded, format, err := image.Decode(bytes.NewReader(pixels))
	if err != nil || prefix != "data:image/"+format || decoded.Bounds().Dx() != res.Width || decoded.Bounds().Dy() != res.Height {
		t.Fatalf("delivered bytes are %q (header %q, reported %dx%d), err %v", format, prefix, res.Width, res.Height, err)
	}
	return res, format
}

func solidPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{200, 220, 240, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func TestViewImageResultPassesASmallImageThroughUnchanged(t *testing.T) {
	data := solidPNG(320, 200)
	out, err := viewImageResult("slide.png", data)
	if err != nil {
		t.Fatal(err)
	}
	res, format := decodeViewResult(t, out)
	if format != "png" || res.Width != 320 || res.Height != 200 || res.SourceWidth != 320 {
		t.Fatalf("%s %+v", format, res)
	}
	if !strings.Contains(res.ImageURL, base64.StdEncoding.EncodeToString(data)) {
		t.Fatal("a small image must be delivered byte for byte")
	}
}

func TestViewImageResultScalesALargeImageToTheBound(t *testing.T) {
	// Noise does not compress, so this forces both the scale and the size cap.
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.Intn(256))
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	out, err := viewImageResult("shot.jpg", b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	res, _ := decodeViewResult(t, out)
	if max(res.Width, res.Height) > viewLongSideMax || res.SourceWidth != 3000 || res.SourceHeight != 2000 {
		t.Fatalf("%+v", res)
	}
}

func TestViewImageResultRefusesWhatIsNotAViewableImage(t *testing.T) {
	for name, data := range map[string][]byte{
		"text":   []byte("not an image"),
		"gif":    []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
		"empty":  nil,
		"hugePx": solidPNG(8000, 4000),
	} {
		if _, err := viewImageResult(name, data); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// files.view reads through the real guest, as the model's tool does.
func TestComputerFilesViewReadsAnImageFromTheComputer(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "check the page", "rm-view", nil, nil)
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.png"), solidPNG(390, 600), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := s.viewComputerFile(context.Background(), run, json.RawMessage(`{"path":"page.png"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res, format := decodeViewResult(t, out); format != "png" || res.Width != 390 || res.Height != 600 {
		t.Fatalf("%s %+v", format, res)
	}
	if _, err := s.viewComputerFile(context.Background(), run, json.RawMessage(`{"path":"notes.txt"}`)); err == nil {
		t.Fatal("a text file was shown as an image")
	}
	if _, err := s.viewComputerFile(context.Background(), run, json.RawMessage(`{"path":"missing.png"}`)); err == nil {
		t.Fatal("a missing file was shown as an image")
	}
}

// The tool offers the action, treats it as an observation, and caps views.
func TestComputerFilesToolOffersViewAndCapsViewsPerRun(t *testing.T) {
	s, root, _ := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	_, run, _ := sendWithFiles(t, s, conv.ID, bot.ID, "check the page", "rm-cap", nil, nil)
	dir := filepath.Join(root, "bots", bot.ID)
	if err := os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.png"), solidPNG(10, 10), 0o600); err != nil {
		t.Fatal(err)
	}
	var tool Tool
	for _, c := range s.microVMTools(run) {
		if c.Name == "computer_files" {
			tool = c
		}
	}
	if tool.Name == "" {
		t.Fatal("computer_files is not offered")
	}
	schema, _ := json.Marshal(tool.Parameters)
	if !strings.Contains(tool.Description, "files.view") || !strings.Contains(string(schema), `"files.view"`) {
		t.Fatal("the tool does not offer files.view")
	}
	call := json.RawMessage(`{"action":"files.view","path":"a.png"}`)
	if id := tool.Identity(call); id.Risk != tooloutcome.Observation {
		t.Fatalf("files.view risk = %v", id.Risk)
	}
	for i := 0; i < viewsPerRunMax; i++ {
		if _, err := tool.Execute(context.Background(), call); err != nil {
			t.Fatalf("view %d: %v", i+1, err)
		}
	}
	if _, err := tool.Execute(context.Background(), call); err == nil {
		t.Fatal("the view beyond the per-run cap was allowed")
	}
}
