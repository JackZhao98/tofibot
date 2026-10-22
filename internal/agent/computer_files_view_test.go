package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// D54: a computer_files result that carries an image_url reaches the model as
// vision input on the next request, not as base64 text.
func TestComputerFilesImageResultReachesTheModelAsAnImage(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
	raw, _ := json.Marshal(map[string]any{"path": "slide.png", "width": 4, "height": 4, "image_url": uri})
	p := &scriptedProvider{steps: []func(context.Context, *provider.ChatRequest, func(provider.StreamDelta)) (*provider.ChatResponse, error){
		toolCallReply("c1", "computer_files", `{"action":"files.view","path":"slide.png"}`),
		reply(provider.ChatResponse{Content: "seen"}),
	}}
	tool := ExtraBuiltinTool{Schema: provider.Tool{Name: "computer_files", Parameters: map[string]any{"type": "object"}},
		HandlerCtx: func(context.Context, map[string]interface{}) (string, error) { return string(raw), nil }}
	if _, err := runLoop(t, AgentConfig{Provider: p, Prompt: "go", ExtraTools: []ExtraBuiltinTool{tool}}); err != nil {
		t.Fatal(err)
	}
	if len(p.requests) != 2 {
		t.Fatalf("requests=%d", len(p.requests))
	}
	var found bool
	for _, m := range p.requests[1].Messages {
		if m.Role != "tool" {
			continue
		}
		found = len(m.ImageURLs) == 1 && m.ImageURLs[0] == uri && !strings.Contains(m.Content, "base64") && strings.Contains(m.Content, "image_attached")
	}
	if !found {
		t.Fatalf("the image did not arrive as vision input: %+v", p.requests[1].Messages)
	}
}
