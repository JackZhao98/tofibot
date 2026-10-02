package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/netip"
	"net/url"
	"strings"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func buildViewImageTool() *FuncTool {
	schema := provider.Tool{
		Name:        "tofi_view_image",
		Description: "Attach a public image URL as visual input so you can inspect its actual pixels. Use this whenever the user asks you to analyze, describe, compare, read, or interpret an image URL. Do not claim to have seen an image until this tool succeeds.",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"image_url": map[string]interface{}{
					"type":        "string",
					"description": "Public http or https URL of the image to inspect",
				},
			},
			"required": []string{"image_url"},
		},
	}
	return &FuncTool{
		ToolName:   schema.Name,
		ToolSchema: schema,
		ExecuteFunc: func(_ context.Context, args map[string]interface{}) (string, error) {
			imageURL, _ := args["image_url"].(string)
			if !isVisualImageURL(imageURL) {
				return "", fmt.Errorf("image_url must be a public http or https URL no longer than 2048 characters")
			}
			return "Image attached for visual analysis: " + imageURL, nil
		},
	}
}

func isVisualImageURL(rawURL string) bool {
	if len(rawURL) == 0 || len(rawURL) > 2048 {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsUnspecified() && !addr.IsMulticast()
	}
	return true
}

// Computer images originate in a paired device's bounded screenshot result.
// Keep their bytes out of the textual transcript and send them through the
// provider's existing vision input, preserving the associated tool call.
func computerScreenshot(result string) (string, []string) {
	if len(result) > 1<<20 {
		return result, nil
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(result), &payload) != nil {
		return result, nil
	}
	var uri string
	if json.Unmarshal(payload["image_url"], &uri) != nil {
		return result, nil
	}
	prefix, encoded, ok := strings.Cut(uri, ";base64,")
	if !ok || (prefix != "data:image/png" && prefix != "data:image/jpeg") {
		return result, nil
	}
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return result, nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(pixels))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || (format != "png" && format != "jpeg") {
		return result, nil
	}
	delete(payload, "image_url")
	payload["image_attached"] = json.RawMessage("true")
	cleaned, err := json.Marshal(payload)
	if err != nil {
		return result, nil
	}
	return string(cleaned), []string{uri}
}
