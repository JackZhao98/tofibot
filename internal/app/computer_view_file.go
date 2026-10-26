package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"

	_ "golang.org/x/image/webp"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// D54: a model cannot judge layout from a file path. files.view reads a PNG or
// JPEG on the computer and hands it back as visual input, the way a screenshot
// is. The image is bounded: the file, the decoded pixels, the delivered size and
// the number of views per run.
const (
	viewSourceBytesMax   = 8 << 20   // largest file read from the computer
	viewSourcePixelsMax  = 24 << 20  // largest decoded image
	viewLongSideMax      = 1568      // longer sides are scaled down to this
	viewDeliveredMax     = 640 << 10 // encoded size handed to the model
	viewsPerRunMax       = 16
	viewChunkBytes       = 512 << 10
	viewSourceDimsMax    = 16384
	viewJPEGQualityFirst = 85
)

func viewFileError(format string, args ...any) error {
	return tooloutcome.InvalidArguments(fmt.Sprintf(format, args...))
}

// viewComputerFile fetches the file with the same bounded chunk export that
// publish_file uses, then returns a JSON result whose image_url the agent loop
// lifts into the model's vision input.
func (s *Server) viewComputerFile(ctx context.Context, r Run, raw json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Path == "" {
		return "", viewFileError("files.view requires the path of a PNG, JPEG, GIF or WebP file")
	}
	data, err := s.readComputerFile(ctx, r, in.Path, viewSourceBytesMax)
	var tooLarge *fileTooLargeError
	if errors.As(err, &tooLarge) {
		return "", viewFileError("%s is %d bytes; files.view reads images up to %d bytes. Save a smaller copy and view that", in.Path, tooLarge.size, viewSourceBytesMax)
	}
	if err != nil {
		return "", err
	}
	return viewImageResult(in.Path, data)
}

// viewImageResult validates and, when needed, scales an image into a result the
// vision path accepts. The returned width and height describe the delivered
// pixels; source_width and source_height describe the file.
func viewImageResult(path string, data []byte) (string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg" && format != "gif" && format != "webp") {
		return "", viewFileError("%s is not a PNG, JPEG, GIF or WebP image", path)
	}
	if config.Width < 1 || config.Height < 1 || config.Width > viewSourceDimsMax || config.Height > viewSourceDimsMax || config.Width*config.Height > viewSourcePixelsMax {
		return "", viewFileError("%s is %dx%d pixels, too large to view; save a smaller copy and view that", path, config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", viewFileError("%s cannot be decoded as an image", path)
	}
	longSide := max(config.Width, config.Height)
	encoded, mime := data, "image/"+format
	if format == "gif" || format == "webp" {
		// The vision input takes PNG and JPEG; a GIF shows its first frame.
		var b bytes.Buffer
		if png.Encode(&b, img) != nil {
			return "", viewFileError("%s cannot be converted for viewing", path)
		}
		encoded, mime = b.Bytes(), "image/png"
	}
	if longSide > viewLongSideMax || len(encoded) > viewDeliveredMax {
		for target := min(longSide, viewLongSideMax); ; target = target * 3 / 4 {
			if target < 240 {
				return "", viewFileError("%s cannot be reduced to a viewable size", path)
			}
			resized := resizeToLongSide(img, target)
			var b bytes.Buffer
			if png.Encode(&b, resized) == nil && b.Len() <= viewDeliveredMax {
				img, encoded, mime = resized, b.Bytes(), "image/png"
				break
			}
			done := false
			for _, quality := range []int{viewJPEGQualityFirst, 70, 55} {
				b.Reset()
				if jpeg.Encode(&b, flattenOnWhite(resized), &jpeg.Options{Quality: quality}) == nil && b.Len() <= viewDeliveredMax {
					img, encoded, mime, done = resized, b.Bytes(), "image/jpeg", true
					break
				}
			}
			if done {
				break
			}
		}
	}
	bounds := img.Bounds()
	out, err := json.Marshal(map[string]any{
		"path": path, "width": bounds.Dx(), "height": bounds.Dy(),
		"source_width": config.Width, "source_height": config.Height,
		"image_url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(encoded),
	})
	return string(out), err
}

// resizeToLongSide scales by area averaging; the standard library has no
// resampler and the images are small enough for the direct loop.
func resizeToLongSide(src image.Image, longSide int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if max(w, h) <= longSide {
		return src
	}
	nw, nh := w, h
	if w >= h {
		nw, nh = longSide, max(1, h*longSide/w)
	} else {
		nw, nh = max(1, w*longSide/h), longSide
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := b.Min.Y+y*h/nh, b.Min.Y+max(y*h/nh+1, (y+1)*h/nh)
		for x := 0; x < nw; x++ {
			x0, x1 := b.Min.X+x*w/nw, b.Min.X+max(x*w/nw+1, (x+1)*w/nw)
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, pa := src.At(xx, yy).RGBA()
					r, g, bl, a, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), a+uint64(pa), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{uint16(r / n), uint16(g / n), uint16(bl / n), uint16(a / n)})
		}
	}
	return dst
}

func flattenOnWhite(src image.Image) image.Image {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := src.At(x, y).RGBA()
			// Premultiplied channels over a white background.
			dst.Set(x, y, color.RGBA64{uint16(r + (0xffff - a)), uint16(g + (0xffff - a)), uint16(bl + (0xffff - a)), 0xffff})
		}
	}
	return dst
}
