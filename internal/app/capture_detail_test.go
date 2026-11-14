package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// captureComputer answers the capture actions like the guest does and records
// every action it receives.
type captureComputer struct {
	*fakeComputer
	cmu     sync.Mutex
	actions []computer.Action
	// snapshotURL is added to a browser.snapshot result.
	snapshotURL string
	// shade changes the picture of the next captures.
	shade uint8
	// failSnapshot makes browser.snapshot fail, as when Chrome is not up.
	failSnapshot bool
}

func (c *captureComputer) Action(ctx context.Context, a computer.Action) (computer.ActionResult, error) {
	return c.ActionWithin(ctx, a, 0)
}

func (c *captureComputer) ActionWithin(_ context.Context, a computer.Action, _ time.Duration) (computer.ActionResult, error) {
	c.cmu.Lock()
	c.actions = append(c.actions, a)
	shade, pageURL, fail := c.shade, c.snapshotURL, c.failSnapshot
	c.cmu.Unlock()
	switch a.Name {
	case "desktop.capture":
		return computer.ActionResult{OK: true, Result: captureJSON(1280, 800, "", shade)}, nil
	case "browser.snapshot":
		if fail {
			return computer.ActionResult{}, errors.New("synthetic: Chrome is not reachable")
		}
		return computer.ActionResult{OK: true, Result: captureJSON(1280, 800, pageURL, shade)}, nil
	}
	return computer.ActionResult{OK: true, Result: json.RawMessage(`{}`)}, nil
}

// patternPNG draws gradients so a scaled copy is visibly not a flat colour.
func patternPNG(w, h int, shade uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), shade, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func captureJSON(w, h int, pageURL string, shade uint8) json.RawMessage {
	fields := map[string]any{"image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(patternPNG(w, h, shade)), "width": w, "height": h}
	if pageURL != "" {
		fields["url"] = pageURL
	}
	raw, _ := json.Marshal(fields)
	return raw
}

func (c *captureComputer) count(name string) int {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	n := 0
	for _, a := range c.actions {
		if a.Name == name {
			n++
		}
	}
	return n
}

func captureServer(t *testing.T) (*Server, *captureComputer, Run) {
	t.Helper()
	s, bot, conv := contextSurfaceServer(t)
	c := &captureComputer{fakeComputer: newFakeComputer("firecracker"), shade: 90}
	s.microVM = c
	classic := browserEngineClassic
	if err := s.saveAutomationValue(autoBrowserEngine, &classic); err != nil {
		t.Fatal(err)
	}
	return s, c, contextSurfaceRun(t, s, conv, bot, "check the page")
}

type captureOut struct {
	ImageURL     string `json:"image_url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	SourceWidth  int    `json:"source_width"`
	SourceHeight int    `json:"source_height"`
	Detail       string `json:"detail"`
	CaptureSeq   int    `json:"capture_seq"`
	URL          string `json:"url"`
}

func runCapture(t *testing.T, s *Server, run Run, tool, args string) captureOut {
	t.Helper()
	tl, ok := toolNamed(s.microVMTools(run), tool)
	if !ok {
		t.Fatalf("no tool %s", tool)
	}
	out, err := tl.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatal(err)
	}
	var res captureOut
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %.200s", err, out)
	}
	return res
}

func decodeDataURL(t *testing.T, uri string) (image.Image, string) {
	t.Helper()
	prefix, encoded, ok := strings.Cut(uri, ";base64,")
	if !ok {
		t.Fatalf("not a data URL: %.60s", uri)
	}
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(pixels))
	if err != nil {
		t.Fatal(err)
	}
	return img, prefix
}

func TestOverviewCaptureIsScaledOnTheHostToTheLongSide(t *testing.T) {
	s, c, run := captureServer(t)
	res := runCapture(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview"}`)
	img, prefix := decodeDataURL(t, res.ImageURL)
	if prefix != "data:image/jpeg" || img.Bounds().Dx() != 640 || img.Bounds().Dy() != 400 {
		t.Fatalf("overview is %s %v", prefix, img.Bounds())
	}
	if res.Width != 640 || res.Height != 400 || res.SourceWidth != 1280 || res.SourceHeight != 800 || res.Detail != "overview" {
		t.Fatalf("%+v", res)
	}
	// The guest is asked for the same capture whatever the detail: no Bot computer rebuild.
	for _, a := range c.actions {
		if a.Name == "desktop.capture" && string(a.Args) != "{}" {
			t.Fatalf("guest args changed: %s", a.Args)
		}
	}
}

func TestFullCaptureAndAnOmittedDetailKeepTheGuestPicture(t *testing.T) {
	s, _, run := captureServer(t)
	for _, args := range []string{`{"action":"desktop.capture"}`, `{"action":"desktop.capture","detail":"full"}`, `{"action":"capture"}`} {
		res := runCapture(t, s, run, "computer_desktop", args)
		img, prefix := decodeDataURL(t, res.ImageURL)
		if prefix != "data:image/png" || img.Bounds().Dx() != 1280 || res.Detail != "" || res.Width != 1280 {
			t.Fatalf("%s: %s %v %+v", args, prefix, img.Bounds(), res)
		}
	}
}

func TestOverviewCostsAboutAQuarterOfTheImageArea(t *testing.T) {
	s, _, run := captureServer(t)
	full := runCapture(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	over := runCapture(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview"}`)
	ratio := float64(over.Width*over.Height) / float64(full.Width*full.Height)
	if ratio < 0.24 || ratio > 0.26 {
		t.Fatalf("area ratio %.3f", ratio)
	}
}

func TestBrowserSnapshotTakesTheSameDetailAndKeepsItsOtherFields(t *testing.T) {
	s, c, run := captureServer(t)
	c.snapshotURL = "https://shop.example/cart"
	res := runCapture(t, s, run, "computer_browser", `{"action":"browser.snapshot","detail":"overview"}`)
	if res.Width != 640 || res.URL != "https://shop.example/cart" || res.Detail != "overview" {
		t.Fatalf("%+v", res)
	}
}

func TestCaptureNumbersCountUpWithinARun(t *testing.T) {
	s, _, run := captureServer(t)
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	for want := 1; want <= 3; want++ {
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","detail":"overview"}`))
		if err != nil {
			t.Fatal(err)
		}
		var res captureOut
		_ = json.Unmarshal([]byte(out), &res)
		if res.CaptureSeq != want {
			t.Fatalf("capture_seq = %d, want %d", res.CaptureSeq, want)
		}
	}
}

func TestUnknownCaptureDetailIsRefusedBeforeAnyAction(t *testing.T) {
	s, c, run := captureServer(t)
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.capture","detail":"tiny"}`)); err == nil {
		t.Fatal("an unknown detail was accepted")
	}
	if c.count("desktop.capture") != 0 {
		t.Fatal("the computer was called")
	}
}

func TestCaptureDetailIsDescribedInTheTools(t *testing.T) {
	s, _, run := captureServer(t)
	for _, name := range []string{"computer_desktop", "computer_browser"} {
		tool, _ := toolNamed(s.microVMTools(run), name)
		// The sentence is in the description or, where the description has no room left (computer_browser), on the detail parameter.
		props, _ := tool.Parameters["properties"].(map[string]any)
		detail, ok := props["detail"].(map[string]any)
		if !ok {
			t.Errorf("%s lacks the detail parameter", name)
		}
		if text, _ := detail["description"].(string); !strings.Contains(tool.Description, captureDetailSentence) && !strings.Contains(text, captureDetailSentence) {
			t.Errorf("%s lacks the detail sentence", name)
		}
	}
}

func TestApplyCaptureDetailLeavesUndecodableResultsAlone(t *testing.T) {
	for _, in := range []string{`not json`, `{"width":3}`, `{"image_url":"data:image/png;base64,AAAA"}`, `{"image_url":"data:image/webp;base64,AAAA"}`} {
		if got := applyCaptureDetail(in, captureDetailOverview); got != in {
			t.Errorf("changed %q to %q", in, got)
		}
	}
}
