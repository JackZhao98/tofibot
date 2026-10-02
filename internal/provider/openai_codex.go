package provider

import (
	"context"
	"fmt"
	"strings"
)

// openAICodex uses the ChatGPT Codex Responses backend. It deliberately has
// no Chat Completions fallback because OAuth credentials are not API keys.
type openAICodex struct{ responses *openaiResponses }

func newOpenAICodex(accessToken string, cfg *providerConfig) (Provider, error) {
	parts := strings.SplitN(accessToken, "\x00", 2)
	accessToken = parts[0]
	if accessToken == "" {
		return nil, fmt.Errorf("Codex OAuth access token is required")
	}
	headers := map[string]string{"originator": "tofi", "User-Agent": "tofi/1.0"}
	for key, value := range cfg.ExtraHeaders {
		headers[key] = value
	}
	if len(parts) == 2 && parts[1] != "" {
		headers["ChatGPT-Account-Id"] = parts[1]
	}
	return &openAICodex{responses: &openaiResponses{
		apiKey:  accessToken,
		baseURL: "https://chatgpt.com/backend-api/codex",
		headers: headers,
		noStore: true,
	}}, nil
}

func (o *openAICodex) request(req *ChatRequest) *ChatRequest {
	copy := *req
	copy.Model = strings.TrimPrefix(req.Model, "codex-")
	return &copy
}

func (o *openAICodex) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// The Codex backend requires streaming requests, including callers using
	// the unified non-streaming Chat method. The underlying parser safely
	// accepts a nil callback and still aggregates the complete response.
	return o.responses.ChatStream(ctx, o.request(req), nil)
}

func (o *openAICodex) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	return o.responses.ChatStream(ctx, o.request(req), onDelta)
}
