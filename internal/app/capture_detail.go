package app

// Capture detail (work-trace design, B1). A screenshot costs image tokens in
// proportion to its area. "overview" is a quarter-size capture for a check of
// state; "full" is the guest's capture and the default. The guest is not
// changed: the host scales the picture before the model sees it, so a Bot
// computer image built earlier keeps working.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	captureDetailFull     = "full"
	captureDetailOverview = "overview"

	// overviewLongSide is the long side of an overview capture: a quarter of the area of 1280x800.
	overviewLongSide    = 640
	overviewJPEGQuality = 75
)

// captureDetailDescription is the model-facing sentence for the detail parameter.
const captureDetailSentence = "detail overview is a quarter-size capture for a check of state; full is for reading text, numbers and for click targets."

const captureDetailDescription = "Capture detail, default full. " + captureDetailSentence + " Send x and y from an overview capture with the screenshot_width and screenshot_height of that capture."

var captureDetailParam = map[string]any{"type": "string", "enum": []string{captureDetailOverview, captureDetailFull}, "description": captureDetailDescription}

// normalizeCaptureDetail maps an empty value to full and refuses an unknown one.
func normalizeCaptureDetail(detail string) (string, error) {
	switch strings.TrimSpace(strings.ToLower(detail)) {
	case "", captureDetailFull:
		return captureDetailFull, nil
	case captureDetailOverview:
		return captureDetailOverview, nil
	}
	return "", tooloutcome.InvalidArguments(fmt.Sprintf("detail must be %q or %q", captureDetailOverview, captureDetailFull))
}

// captureDetailOf reads the detail field from the raw tool arguments.
func captureDetailOf(raw json.RawMessage) (string, error) {
	var in struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &in)
	return normalizeCaptureDetail(in.Detail)
}

// decodeCaptureImage reads the data URL of a capture result.
func decodeCaptureImage(uri string) (image.Image, bool) {
	prefix, encoded, ok := strings.Cut(uri, ";base64,")
	if !ok || (prefix != "data:image/png" && prefix != "data:image/jpeg") {
		return nil, false
	}
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, false
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(pixels))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 {
		return nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(pixels))
	if err != nil {
		return nil, false
	}
	return img, true
}

// encodeJPEGDataURL encodes an image as a JPEG data URL.
func encodeJPEGDataURL(img image.Image, quality int) (string, []byte, bool) {
	var b bytes.Buffer
	if jpeg.Encode(&b, flattenOnWhite(img), &jpeg.Options{Quality: quality}) != nil {
		return "", nil, false
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(b.Bytes()), b.Bytes(), true
}

// captureResultFields splits a capture result into its fields; ok is false for a
// result that is not a JSON object with an image.
func captureResultFields(result string) (map[string]json.RawMessage, string, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &fields) != nil {
		return nil, "", false
	}
	var uri string
	if json.Unmarshal(fields["image_url"], &uri) != nil || uri == "" {
		return nil, "", false
	}
	return fields, uri, true
}

// applyCaptureDetail scales the picture of a capture result for the requested
// detail and records the detail and the source size. A result that carries no
// decodable picture is returned unchanged. full leaves the picture as the guest
// made it.
func applyCaptureDetail(result, detail string) string {
	fields, uri, ok := captureResultFields(result)
	if !ok {
		return result
	}
	if detail != captureDetailOverview {
		return result
	}
	img, ok := decodeCaptureImage(uri)
	if !ok {
		return result
	}
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	small := resizeToLongSide(img, overviewLongSide)
	dataURL, _, ok := encodeJPEGDataURL(small, overviewJPEGQuality)
	if !ok {
		return result
	}
	sb := small.Bounds()
	set := func(key string, v any) {
		if raw, err := json.Marshal(v); err == nil {
			fields[key] = raw
		}
	}
	set("image_url", dataURL)
	set("width", sb.Dx())
	set("height", sb.Dy())
	set("source_width", srcW)
	set("source_height", srcH)
	set("detail", captureDetailOverview)
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return string(out)
}

// withCaptureSeq adds the run's capture number to a capture result.
func withCaptureSeq(result string, seq int) string {
	if seq <= 0 {
		return result
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &fields) != nil {
		return result
	}
	raw, _ := json.Marshal(seq)
	fields["capture_seq"] = raw
	out, err := json.Marshal(fields)
	if err != nil {
		return result
	}
	return string(out)
}

// afterComputerAction is what the server keeps of a computer action that
// succeeded: a capture goes through the work-trace point; any other action
// leaves a short note for the next capture, and a navigation closes the secret
// window of the run.
func (s *Server) afterComputerAction(ctx context.Context, r Run, tool, action string, raw, args json.RawMessage, out, detail string) (string, error) {
	if action == "desktop.capture" || action == "browser.snapshot" {
		return s.finishCapture(ctx, r, tool, action, args, out, detail)
	}
	if strings.HasPrefix(action, "desktop.") || strings.HasPrefix(action, "browser.") {
		s.noteTraceAction(r.ID, traceActionLabel(action, args))
	}
	if navigationAction(action, raw) {
		s.store.closeSecretWindow(r.ID)
		var nav struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(args, &nav)
		s.noteTracePage(r.ID, nav.URL)
	}
	return out, nil
}
