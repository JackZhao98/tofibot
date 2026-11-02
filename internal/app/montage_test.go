package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func solidImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 255
	}
	return img
}

func noiseImage(w, h int, seed int64) image.Image {
	rng := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(rng.Intn(256))
	}
	return img
}

func tilesOf(n int, mk func(i int) image.Image) []montageTile {
	tiles := make([]montageTile, n)
	for i := range tiles {
		tiles[i] = montageTile{Seq: i + 1, Offset: time.Duration(i) * 7 * time.Second, Label: "click 10,20", Img: mk(i)}
	}
	return tiles
}

func decodeJPEG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func sameColor(a, b color.Color, tol int) bool {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) int { return absInt(int(x>>8) - int(y>>8)) }
	return d(ar, br) <= tol && d(ag, bg) <= tol && d(ab, bb) <= tol
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestMontageLayoutForEveryTileCount(t *testing.T) {
	for n := 1; n <= montagePersonTilesMax; n++ {
		res, err := renderMontage(tilesOf(n, func(int) image.Image { return solidImage(640, 400, color.RGBA{200, 30, 30, 255}) }), montageOptions{Columns: 3})
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		cols := min(n, 3)
		rows := (n + cols - 1) / cols
		wantW := cols*montageTileW + (cols+1)*montageGap
		wantH := rows*(montageTileH+montageLabelH) + (rows+1)*montageGap
		if res.Width != wantW || res.Height != wantH || len(res.Tiles) != n {
			t.Fatalf("n=%d: %dx%d, want %dx%d, %d tiles", n, res.Width, res.Height, wantW, wantH, len(res.Tiles))
		}
		if len(res.JPEG) > montageBytesMax {
			t.Fatalf("n=%d: %d bytes", n, len(res.JPEG))
		}
		if cols == 3 && res.Width > 1568 {
			t.Fatalf("n=%d: wider than the vision limit", n)
		}
		img := decodeJPEG(t, res.JPEG)
		if img.Bounds().Dx() != res.Width || img.Bounds().Dy() != res.Height {
			t.Fatalf("n=%d: decoded %v", n, img.Bounds())
		}
		// The first tile shows the picture: a pixel inside the first cell is red.
		if !sameColor(img.At(montageGap+montageTileW/2, montageGap+montageTileH/2), color.RGBA{200, 30, 30, 255}, 24) {
			t.Fatalf("n=%d: the first cell does not show its picture", n)
		}
	}
}

func TestMontageColumnsAreTwoOrThree(t *testing.T) {
	two, _ := renderMontage(tilesOf(4, func(int) image.Image { return solidImage(640, 400, color.RGBA{10, 10, 10, 255}) }), montageOptions{Columns: 2})
	if two.Width != 2*montageTileW+3*montageGap {
		t.Fatalf("two columns: %d", two.Width)
	}
	def, _ := renderMontage(tilesOf(4, func(int) image.Image { return solidImage(640, 400, color.RGBA{10, 10, 10, 255}) }), montageOptions{})
	if def.Width != 3*montageTileW+4*montageGap {
		t.Fatalf("default columns: %d", def.Width)
	}
	if _, err := renderMontage(nil, montageOptions{}); err == nil {
		t.Fatal("an empty montage was rendered")
	}
}

func TestRedactedAndNotKeptTilesAreGreyCells(t *testing.T) {
	tiles := []montageTile{
		{Seq: 1, Img: solidImage(640, 400, color.RGBA{250, 250, 250, 255})},
		{Seq: 2, Redacted: true, Reason: traceReasonSecretInput, Label: "type 8 characters"},
		{Seq: 3, Label: "capture"},
	}
	res, err := renderMontage(tiles, montageOptions{Columns: 3})
	if err != nil {
		t.Fatal(err)
	}
	img := decodeJPEG(t, res.JPEG)
	corner := func(i int) color.Color {
		x := montageGap + i*(montageTileW+montageGap) + 6
		return img.At(x, montageGap+6)
	}
	if !sameColor(corner(0), color.RGBA{250, 250, 250, 255}, 12) {
		t.Fatal("kept tile is not shown")
	}
	for i := 1; i <= 2; i++ {
		if !sameColor(corner(i), montageGray, 12) {
			t.Fatalf("tile %d is not a grey cell", i)
		}
	}
	if res.Tiles[1].Redacted != true || res.Tiles[1].Kept || res.Tiles[2].Kept || !res.Tiles[0].Kept {
		t.Fatalf("%+v", res.Tiles)
	}
}

func TestEarlierStepsTakeTheFirstCell(t *testing.T) {
	tiles := tilesOf(11, func(int) image.Image { return solidImage(640, 400, color.RGBA{30, 120, 30, 255}) })
	res, err := renderMontage(tiles, montageOptions{Columns: 3, Earlier: 7})
	if err != nil {
		t.Fatal(err)
	}
	// 11 tiles and the summary cell make twelve cells: 3 columns by 4 rows.
	wantH := 4*(montageTileH+montageLabelH) + 5*montageGap
	if res.Height != wantH || res.Width != 3*montageTileW+4*montageGap {
		t.Fatalf("%dx%d", res.Width, res.Height)
	}
	img := decodeJPEG(t, res.JPEG)
	if !sameColor(img.At(montageGap+6, montageGap+6), montageGray, 12) {
		t.Fatal("the first cell is not the summary cell")
	}
	if !sameColor(img.At(montageGap+montageTileW+montageGap+montageTileW/2, montageGap+montageTileH/2), color.RGBA{30, 120, 30, 255}, 24) {
		t.Fatal("the second cell does not show the first step")
	}
}

func TestMontageStaysUnderTheDeliveredBoundForNoisyPictures(t *testing.T) {
	res, err := renderMontage(tilesOf(montagePersonTilesMax, func(i int) image.Image { return noiseImage(640, 400, int64(i)) }), montageOptions{Columns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.JPEG) > montageBytesMax {
		t.Fatalf("%d bytes", len(res.JPEG))
	}
}

func TestMontageLabelsAreDrawnFromAnyTextWithoutFailing(t *testing.T) {
	tiles := tilesOf(2, func(int) image.Image { return solidImage(100, 60, color.RGBA{1, 2, 3, 255}) })
	tiles[0].Label = strings.Repeat("宽", 200)
	tiles[1].Label = "tab navigate \x00 end"
	if _, err := renderMontage(tiles, montageOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := montageStepLabel(montageTile{Seq: 3, Offset: 125 * time.Second, Label: "click 1,2"}); got != "#3 +2:05 click 1,2" {
		t.Fatalf("label %q", got)
	}
}

func TestTheModelAndThePersonGetTheSamePictureFromTheSameCaptures(t *testing.T) {
	s, _, run := captureServer(t)
	for i := 0; i < 4; i++ {
		runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	}
	rows := captures(t, s, run)
	a, err := s.buildRunMontage(context.Background(), rows, rows, montageOptions{Columns: 3})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.buildRunMontage(context.Background(), rows, rows, montageOptions{Columns: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.JPEG, b.JPEG) || len(a.Tiles) != 4 {
		t.Fatal("one set of captures gave two different pictures")
	}
}

type traceOut struct {
	ImageURL string            `json:"image_url"`
	Width    int               `json:"width"`
	Height   int               `json:"height"`
	Tiles    []montageTileInfo `json:"tiles"`
	Total    int               `json:"captures_in_run"`
}

func runTrace(t *testing.T, s *Server, run Run, args string) (traceOut, error) {
	t.Helper()
	out, err := execTool(t, s, run, "computer_desktop", args)
	if err != nil {
		return traceOut{}, err
	}
	var res traceOut
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v: %.200s", err, out)
	}
	return res, nil
}

func TestDesktopTraceJoinsTheLatestCapturesOfTheRun(t *testing.T) {
	s, _, run := captureServer(t)
	for i := 0; i < 5; i++ {
		runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture","detail":"overview"}`)
	}
	res, err := runTrace(t, s, run, `{"action":"desktop.trace","last":3}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tiles) != 3 || res.Tiles[0].Seq != 3 || res.Tiles[2].Seq != 5 || res.Total != 5 {
		t.Fatalf("%+v", res)
	}
	img, prefix := decodeDataURL(t, res.ImageURL)
	if prefix != "data:image/jpeg" || img.Bounds().Dx() != res.Width || res.Width != 3*montageTileW+4*montageGap {
		t.Fatalf("%s %v", prefix, img.Bounds())
	}
	// The default is six; fewer captures than asked for join what exists.
	res, err = runTrace(t, s, run, `{"action":"trace"}`)
	if err != nil || len(res.Tiles) != 5 {
		t.Fatalf("%v %+v", err, res.Tiles)
	}
	res, err = runTrace(t, s, run, `{"action":"desktop.trace","capture_seqs":[4,1,4],"columns":2}`)
	if err != nil || len(res.Tiles) != 2 || res.Tiles[0].Seq != 1 || res.Tiles[1].Seq != 4 || res.Width != 2*montageTileW+3*montageGap {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestDesktopTraceNeverCallsTheComputerOrTakesAPicture(t *testing.T) {
	s, c, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	before := c.fakeComputer.calls.Load()
	for i := 0; i < 3; i++ {
		if _, err := runTrace(t, s, run, `{"action":"desktop.trace"}`); err != nil {
			t.Fatal(err)
		}
	}
	if c.fakeComputer.calls.Load() != before {
		t.Fatal("desktop.trace reached the Bot computer")
	}
	if len(captures(t, s, run)) != 1 {
		t.Fatal("desktop.trace added a capture")
	}
}

func TestDesktopTraceRefusesBadRequestsWithTypedOutcomes(t *testing.T) {
	s, _, run := captureServer(t)
	if _, err := runTrace(t, s, run, `{"action":"desktop.trace"}`); err == nil || !strings.Contains(err.Error(), "no capture") {
		t.Fatalf("no captures: %v", err)
	}
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	for name, args := range map[string]string{
		"unknown seq":     `{"action":"desktop.trace","capture_seqs":[9]}`,
		"last too small":  `{"action":"desktop.trace","last":1}`,
		"last too large":  `{"action":"desktop.trace","last":10}`,
		"bad columns":     `{"action":"desktop.trace","columns":4}`,
		"too many seqs":   `{"action":"desktop.trace","capture_seqs":[1,2,1,2,1,2,1,2,1,2]}`,
		"unknown field x": `{"action":"desktop.trace","verbose":true}`,
	} {
		_, err := runTrace(t, s, run, args)
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if o, ok := tooloutcome.FromError(err); name != "unknown field x" && (!ok || o.Certainty != "not_executed") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDesktopTraceSaysTheTraceIsOffAndKeepsNothing(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	setAuto(t, s, autoWorkTrace, "false")
	_, err := runTrace(t, s, run, `{"action":"desktop.trace"}`)
	if err == nil || !strings.Contains(err.Error(), "trace is off") {
		t.Fatalf("%v", err)
	}
}

func TestDesktopTraceCountsTowardTheViewsOfTheRun(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	for i := 0; i < viewsPerRunMax; i++ {
		if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.trace"}`)); err != nil {
			t.Fatalf("view %d: %v", i+1, err)
		}
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"desktop.trace"}`)); err == nil {
		t.Fatal("the seventeenth view was served")
	}
	files, _ := toolNamed(s.microVMTools(run), "computer_files")
	_ = files
}

func TestDesktopTraceShowsRedactedStepsAsGreyCells(t *testing.T) {
	s, _, run := captureServer(t)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	s.store.openSecretWindow(run.ID)
	runTool(t, s, run, "computer_desktop", `{"action":"desktop.capture"}`)
	res, err := runTrace(t, s, run, `{"action":"desktop.trace"}`)
	if err != nil || len(res.Tiles) != 2 || !res.Tiles[1].Redacted || res.Tiles[1].Kept || !res.Tiles[0].Kept {
		t.Fatalf("%v %+v", err, res.Tiles)
	}
}

func TestDesktopTraceIsDescribedAndLiftedAsAnImage(t *testing.T) {
	s, _, run := captureServer(t)
	tool, _ := toolNamed(s.microVMTools(run), "computer_desktop")
	if !strings.Contains(tool.Description, traceActionSentence) {
		t.Fatal("the tool description lacks the desktop.trace sentence")
	}
	if len(tool.Description) > 1024 {
		t.Fatalf("description is %d bytes", len(tool.Description))
	}
	actions := tool.Parameters["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	found := 0
	for _, a := range actions {
		if a == "desktop.trace" || a == "trace" {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("enum %v", actions)
	}
}
