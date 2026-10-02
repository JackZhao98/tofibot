package provider

// Image generation uses the same streaming Responses transport as Codex chat,
// but sends only the explicit image brief and optional reference image. Image
// bytes never enter the agent's text transcript or tool activity log.
import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxGeneratedImage = 20 << 20

type ImageRequest struct {
	Prompt    string
	Reference string // validated data URL owned by the current conversation
}

// GenerateCodexImage does not refresh or write credentials, retry a potentially
// billable request, or silently fall back to an API-key provider.
func GenerateCodexImage(ctx context.Context, credential string, input ImageRequest) ([]byte, error) {
	return generateCodexImage(ctx, &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, "https://chatgpt.com/backend-api/codex/responses", credential, input)
}

func generateCodexImage(ctx context.Context, client *http.Client, endpoint, credential string, input ImageRequest) ([]byte, error) {
	parts := strings.SplitN(credential, "\x00", 2)
	if parts[0] == "" {
		return nil, errors.New("connect Codex in Settings before generating images")
	}
	content := []map[string]any{{"type": "input_text", "text": input.Prompt}}
	action := "generate"
	if input.Reference != "" {
		content = append(content, map[string]any{"type": "input_image", "image_url": input.Reference})
		action = "edit"
	}
	payload := map[string]any{
		"model": "gpt-5.6-luna", "store": false, "stream": true,
		"instructions": "Use the image_generation tool exactly once to fulfill the user's image brief. Do not return code, URLs, or a textual substitute for an image.",
		"input":        []map[string]any{{"role": "user", "content": content}},
		"tools":        []map[string]any{{"type": "image_generation", "action": action, "output_format": "png"}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+parts[0])
	req.Header.Set("originator", "tofi")
	req.Header.Set("User-Agent", "tofi/1.0")
	if len(parts) == 2 && parts[1] != "" {
		req.Header.Set("ChatGPT-Account-Id", parts[1])
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("image provider connection failed; retry explicitly")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Never expose arbitrary upstream bodies, which may echo credentials or
		// input images. The HTTP status is enough to distinguish auth/limits.
		return nil, fmt.Errorf("image generation unavailable (HTTP %d); check Codex access or usage limits in Settings", resp.StatusCode)
	}
	return readGeneratedImage(resp.Body)
}

func readGeneratedImage(body io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 96<<20))
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	var result []byte
	completed := false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var event struct {
			Type     string                                `json:"type"`
			Item     struct{ Type, Status, Result string } `json:"item"`
			Response struct{ Status string }               `json:"response"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			return nil, errors.New("invalid image provider response")
		}
		switch event.Type {
		case "error", "response.failed", "response.incomplete":
			return nil, errors.New("image provider did not complete generation; retry explicitly")
		case "response.output_item.done":
			if event.Item.Type == "image_generation_call" {
				if event.Item.Status != "completed" || event.Item.Result == "" {
					return nil, errors.New("image generation returned no completed image")
				}
				if len(result) != 0 {
					return nil, errors.New("image provider returned more than one image")
				}
				if base64.StdEncoding.DecodedLen(len(event.Item.Result)) > maxGeneratedImage+2 {
					return nil, errors.New("generated image exceeds 20 MB")
				}
				var err error
				result, err = base64.StdEncoding.DecodeString(event.Item.Result)
				if err != nil || len(result) == 0 || len(result) > maxGeneratedImage {
					return nil, errors.New("invalid generated image data")
				}
			}
		case "response.completed":
			completed = event.Response.Status == "completed"
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("image response interrupted or too large; retry explicitly")
	}
	if !completed || len(result) == 0 {
		return nil, errors.New("image provider returned no completed image")
	}
	return result, nil
}
