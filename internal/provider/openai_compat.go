package provider

import (
	"context"
	"errors"
	"strings"
)

var errNoCompatEndpoint = errors.New("OpenAI-compatible endpoint is not configured")

// openaiCompat is an admin-configured OpenAI-compatible endpoint (Ollama, LM
// Studio, vLLM, OpenRouter, ...). It speaks Chat Completions, the one wire
// format all of them share, with tool calling and streaming. Model IDs reach
// us as "compat:<id>"; the prefix routes the model here and is removed before
// the request is sent.
type openaiCompat struct {
	inner *openaiChatCompletions
}

func newOpenAICompat(apiKey string, cfg *providerConfig) (Provider, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errNoCompatEndpoint
	}
	inner, err := newOpenAIChatCompletions(apiKey, cfg)
	if err != nil {
		return nil, err
	}
	return &openaiCompat{inner: inner.(*openaiChatCompletions)}, nil
}

func stripCompatPrefix(req *ChatRequest) *ChatRequest {
	out := *req
	out.Model = strings.TrimPrefix(req.Model, CompatModelPrefix)
	return &out
}

func (o *openaiCompat) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	return o.inner.Chat(ctx, stripCompatPrefix(req))
}

func (o *openaiCompat) ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error) {
	return o.inner.ChatStream(ctx, stripCompatPrefix(req), onDelta)
}
