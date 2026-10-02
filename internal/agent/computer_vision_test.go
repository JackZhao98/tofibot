package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func TestComputerScreenshotUsesVisionWithoutBase64Text(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	raw, _ := json.Marshal(map[string]any{"image_url": uri, "width": 2, "height": 2})
	text, images := computerScreenshot(string(raw))
	if len(images) != 1 || images[0] != uri || strings.Contains(text, "base64") || !strings.Contains(text, "image_attached") {
		t.Fatal("screenshot not separated into visual input")
	}
	for _, value := range []string{`{"image_url":"https://elsewhere/image.png"}`, `{"image_url":"data:image/png;base64,aGVsbG8="}`, `{"image_url":"data:image/svg+xml;base64,PHN2Zz4="}`} {
		if _, images = computerScreenshot(value); len(images) != 0 {
			t.Fatal("invalid screenshot became visual input")
		}
	}
}
