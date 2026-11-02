package app

// Montage: the one piece of code that stitches stored captures of a run into a
// labelled grid. The model's desktop.trace action (at most nine tiles) and the
// strip made for the person when a run ends (at most twelve) both call
// renderMontage, so the two pictures can never disagree about a layout or a
// label. Text is drawn with the built-in 7x13 bitmap face: no system font is
// needed and the output is the same on every host.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	montageTileW      = 424
	montageTileH      = 265
	montageLabelH     = 18
	montageGap        = 2
	montageQuality    = 75
	montageQualityLow = 55
	montageBytesMax   = viewDeliveredMax // 640 KiB, the same bound as an image the model views

	// MontageModelTilesMax and MontagePersonTilesMax are the owner's limits: at
	// most nine tiles for the model, twelve for the person.
	montageModelTilesMax  = 9
	montagePersonTilesMax = 12
)

var (
	montageBackground = color.RGBA{0x1d, 0x1d, 0x1f, 0xff}
	montageGray       = color.RGBA{0x3a, 0x3a, 0x3e, 0xff}
	montageInk        = color.RGBA{0xf2, 0xf2, 0xf4, 0xff}
	montageMuted      = color.RGBA{0xa8, 0xa8, 0xb0, 0xff}
)

// montageTile is one cell. Img is nil for a redacted or not-kept tile.
type montageTile struct {
	Seq      int
	Offset   time.Duration // time since the first capture of the run
	Label    string
	Redacted bool
	Reason   string
	Img      image.Image
}

// montageOptions chooses the grid. Earlier counts steps left out before the
// first tile; when it is above zero the first cell is a summary cell.
type montageOptions struct {
	Columns int
	Earlier int
}

// montageResult is the picture and what each cell shows.
type montageResult struct {
	JPEG          []byte
	Width, Height int
	Tiles         []montageTileInfo
}

type montageTileInfo struct {
	Seq      int    `json:"seq"`
	TakenAt  string `json:"taken_at"`
	Label    string `json:"label"`
	Redacted bool   `json:"redacted"`
	Kept     bool   `json:"kept"`
}

// montageText draws a line of ASCII text; other characters become '?'.
func montageText(dst draw.Image, x, y int, text string, c color.Color, maxW int) {
	var b strings.Builder
	for _, r := range text {
		if r < 0x20 || r > 0x7e {
			r = '?'
		}
		b.WriteRune(r)
	}
	line := b.String()
	const advance = 7
	if limit := maxW / advance; limit > 0 && len(line) > limit {
		if limit > 1 {
			line = line[:limit-1] + "~"
		} else {
			line = line[:limit]
		}
	}
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: basicfont.Face7x13, Dot: fixed.P(x, y)}
	d.DrawString(line)
}

// montageStepLabel is the line under a tile: "#3 +0:42 click 412,300".
func montageStepLabel(t montageTile) string {
	secs := int(t.Offset.Round(time.Second) / time.Second)
	if secs < 0 {
		secs = 0
	}
	label := fmt.Sprintf("#%d +%d:%02d", t.Seq, secs/60, secs%60)
	if t.Label != "" {
		label += " " + t.Label
	}
	return label
}

func drawCenteredText(dst draw.Image, r image.Rectangle, text string, c color.Color) {
	w := utf8.RuneCountInString(text) * 7
	x := r.Min.X + (r.Dx()-w)/2
	y := r.Min.Y + r.Dy()/2 + 4
	montageText(dst, x, y, text, c, r.Dx())
}

// renderMontage lays the tiles out row by row, left to right.
func renderMontage(tiles []montageTile, opt montageOptions) (montageResult, error) {
	cols := opt.Columns
	if cols < 2 {
		cols = 3
	}
	if cols > 3 {
		cols = 3
	}
	cells := len(tiles)
	if opt.Earlier > 0 {
		cells++
	}
	if cells == 0 {
		return montageResult{}, fmt.Errorf("montage needs at least one tile")
	}
	if cells < cols {
		cols = cells
	}
	rows := (cells + cols - 1) / cols
	cellH := montageTileH + montageLabelH
	width := cols*montageTileW + (cols+1)*montageGap
	height := rows*cellH + (rows+1)*montageGap
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(montageBackground), image.Point{}, draw.Src)
	cellRect := func(i int) image.Rectangle {
		col, row := i%cols, i/cols
		x := montageGap + col*(montageTileW+montageGap)
		y := montageGap + row*(cellH+montageGap)
		return image.Rect(x, y, x+montageTileW, y+montageTileH)
	}
	index := 0
	info := make([]montageTileInfo, 0, len(tiles))
	if opt.Earlier > 0 {
		r := cellRect(0)
		draw.Draw(canvas, r, image.NewUniform(montageGray), image.Point{}, draw.Src)
		drawCenteredText(canvas, r, fmt.Sprintf("... %d earlier steps", opt.Earlier), montageInk)
		index = 1
	}
	for _, t := range tiles {
		r := cellRect(index)
		index++
		draw.Draw(canvas, r, image.NewUniform(montageGray), image.Point{}, draw.Src)
		switch {
		case t.Img != nil:
			// Fit inside the cell without distortion, centred.
			b := t.Img.Bounds()
			scale := min(float64(montageTileW)/float64(b.Dx()), float64(montageTileH)/float64(b.Dy()))
			tw, th := max(1, int(float64(b.Dx())*scale)), max(1, int(float64(b.Dy())*scale))
			scaled := resizeToFit(t.Img, tw, th)
			dx, dy := r.Min.X+(montageTileW-tw)/2, r.Min.Y+(montageTileH-th)/2
			draw.Draw(canvas, image.Rect(dx, dy, dx+tw, dy+th), scaled, scaled.Bounds().Min, draw.Src)
		case t.Redacted:
			drawCenteredText(canvas, r, "redacted", montageInk)
		default:
			drawCenteredText(canvas, r, "not kept", montageMuted)
		}
		montageText(canvas, r.Min.X+4, r.Max.Y+13, montageStepLabel(t), montageInk, montageTileW-8)
		info = append(info, montageTileInfo{Seq: t.Seq, Label: t.Label, Redacted: t.Redacted, Kept: t.Img != nil})
	}
	var out []byte
	for _, q := range []int{montageQuality, montageQualityLow, 40} {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, canvas, &jpeg.Options{Quality: q}); err != nil {
			return montageResult{}, err
		}
		out = b.Bytes()
		if len(out) <= montageBytesMax {
			return montageResult{JPEG: out, Width: width, Height: height, Tiles: info}, nil
		}
	}
	return montageResult{}, fmt.Errorf("montage exceeds %d bytes", montageBytesMax)
}

// resizeToFit scales to exactly w x h by area averaging.
func resizeToFit(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	if b.Dx() == w && b.Dy() == h {
		return src
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/h, b.Min.Y+max(y*b.Dy()/h+1, (y+1)*b.Dy()/h)
		for x := 0; x < w; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/w, b.Min.X+max(x*b.Dx()/w+1, (x+1)*b.Dx()/w)
			var r, g, bl, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, _ := src.At(xx, yy).RGBA()
					r, g, bl, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), 0xffff})
		}
	}
	return dst
}

// loadTraceTiles reads the stored pictures of the captures. A row without a
// picture (redacted or not kept) becomes a grey tile; a picture that cannot be
// read counts as not kept.
func (s *Server) loadTraceTiles(ctx context.Context, rows []RunCapture, first time.Time) []montageTile {
	tiles := make([]montageTile, 0, len(rows))
	for _, row := range rows {
		t := montageTile{Seq: row.Seq, Label: row.Label, Redacted: row.Redacted, Reason: row.Reason}
		if at, err := time.Parse(storedTimeLayout, row.TakenAt); err == nil && !first.IsZero() {
			t.Offset = at.Sub(first)
		}
		if row.AttachmentID != "" && !row.Redacted {
			t.Img = s.readTraceImage(ctx, row.AttachmentID)
		}
		tiles = append(tiles, t)
	}
	return tiles
}

func (s *Server) readTraceImage(ctx context.Context, attachmentID string) image.Image {
	_, path, err := s.store.Attachment(attachmentID)
	if err != nil {
		return nil
	}
	f, err := s.store.openAttachment(ctx, path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, traceTileBytesMax*2))
	if err != nil {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// firstCaptureTime is the zero of the step offsets of a run.
func firstCaptureTime(rows []RunCapture) time.Time {
	if len(rows) == 0 {
		return time.Time{}
	}
	t, err := time.Parse(storedTimeLayout, rows[0].TakenAt)
	if err != nil {
		return time.Time{}
	}
	return t
}

// buildRunMontage stitches the given captures of a run; allRows is the whole
// trace of the run, used for the zero of the step times.
func (s *Server) buildRunMontage(ctx context.Context, allRows, rows []RunCapture, opt montageOptions) (montageResult, error) {
	tiles := s.loadTraceTiles(ctx, rows, firstCaptureTime(allRows))
	res, err := renderMontage(tiles, opt)
	if err != nil {
		return res, err
	}
	for i := range res.Tiles {
		res.Tiles[i].TakenAt = rows[i].TakenAt
	}
	return res, nil
}
