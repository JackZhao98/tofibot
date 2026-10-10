// Package provider provides a unified LLM provider abstraction for multiple AI services.
// It supports OpenAI (Responses + Chat Completions), Anthropic Claude, Google Gemini,
// and any OpenAI-compatible provider (Ollama, OpenRouter, Groq, DeepSeek, etc.).
package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"strings"
)

// Provider is the unified interface for LLM API calls.
type Provider interface {
	// Chat sends a non-streaming request and returns the complete response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// ChatStream sends a streaming request. onDelta is called with each incremental update.
	// Returns the final aggregated response (content, tool calls, usage).
	ChatStream(ctx context.Context, req *ChatRequest, onDelta func(StreamDelta)) (*ChatResponse, error)
}

// ChatRequest represents a unified chat request across all providers.
type ChatRequest struct {
	Model string
	// ReasoningEffort is optional and ignored by providers that do not support it.
	ReasoningEffort string
	System          string    // System prompt (extracted from messages for providers that need it separate)
	Messages        []Message // Conversation history
	Tools           []Tool    // Available tools for function calling
	// PromptCacheKey groups requests that share a prefix (Responses API only).
	PromptCacheKey string
	// OmitReasoningReplay drops Message.ReasoningItems from the request after
	// the provider rejected them once in this run.
	OmitReasoningReplay bool
	// ToolChoice constrains tool use for this request. Empty means the
	// provider default (auto); ToolChoiceNone keeps Tools in the request but
	// forbids calling them.
	ToolChoice string
}

// ToolChoiceNone is ChatRequest.ToolChoice for a text-only turn.
const ToolChoiceNone = "none"

// ReasoningItem is replayable reasoning from one assistant turn.
//
// OpenAI (Provider ""/"openai"): an opaque Responses API reasoning item. With
// store=false it must be replayed with its encrypted content before the
// function calls it produced, so the model can continue its prior reasoning.
//
// Anthropic (Provider "anthropic"): Content holds the turn's complete
// content-block array as the model produced it (thinking blocks with their
// signatures, redacted_thinking, text, tool_use), replayed verbatim on the
// next request. Each adapter ignores the other's items.
type ReasoningItem struct {
	ID               string          `json:"id,omitempty"`
	EncryptedContent string          `json:"encrypted_content,omitempty"`
	Summary          []string        `json:"summary,omitempty"`
	Provider         string          `json:"provider,omitempty"`
	Content          json.RawMessage `json:"content,omitempty"`
}

// Message represents a conversation message in the unified format.
type Message struct {
	Role       string     // "user", "assistant", "tool"
	Content    string     // Text content
	ImageURLs  []string   // Public image URLs attached as visual input for the next model turn
	ToolCalls  []ToolCall // For assistant messages: tool calls made
	ToolCallID string     // For tool messages: which call this is responding to
	ToolName   string     // For tool messages: name of the tool
	// ReasoningItems belong to an assistant tool-call message (Responses API).
	ReasoningItems []ReasoningItem `json:"reasoning_items,omitempty"`
	// Backend metadata only; provider request converters never send these fields.
	ToolOutcome *tooloutcome.Outcome `json:"tool_outcome,omitempty"`
	ToolFailed  bool                 `json:"tool_failed,omitempty"`
}

// Tool represents a callable function tool.
type Tool struct {
	Name        string
	Description string
	Parameters  interface{} // JSON Schema object
}

// ToolCall represents a tool invocation by the assistant.
type ToolCall struct {
	ID        string // Provider-assigned call ID
	Name      string // Function name
	Arguments string // Raw JSON string of arguments
}

// StreamDelta represents an incremental update during streaming.
type StreamDelta struct {
	Content   string          // Text content delta
	Reasoning string          // Reasoning/thinking content delta
	ToolCalls []ToolCallDelta // Tool call deltas
}

// ToolCallDelta represents an incremental tool call update during streaming.
type ToolCallDelta struct {
	Index     int    // Tool call index (for parallel calls)
	ID        string // Call ID (present in first chunk)
	Name      string // Function name (present in first chunk)
	Arguments string // Arguments JSON delta
}

// ChatResponse represents the aggregated response from an LLM call.
type ChatResponse struct {
	Content   string     // Full text content
	Reasoning string     // Full reasoning/thinking content
	ToolCalls []ToolCall // Completed tool calls
	Usage     Usage      // Token usage statistics
	// ReasoningItems are replayable reasoning outputs (encrypted content present).
	ReasoningItems []ReasoningItem
	// ReasoningReplayRejected reports that the provider rejected replayed
	// reasoning items and this response was produced without them.
	ReasoningReplayRejected bool
}

// HasToolCalls returns true if the response contains tool calls.
func (r *ChatResponse) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}

// Usage tracks token consumption for cost calculation.
//
// InputTokens is the whole prompt the model read, cached or not (OpenAI's
// input_tokens; Anthropic's input_tokens + cache_read_input_tokens +
// cache_creation_input_tokens), so it measures context size on every
// provider. CacheReadTokens and CacheWriteTokens are the subsets of
// InputTokens served from / written to the prompt cache, when reported.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Add accumulates usage from another Usage.
func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
}

// Option configures provider creation.
type Option func(*providerConfig)

type providerConfig struct {
	BaseURL      string            // Custom endpoint URL (for OpenAI-compatible providers)
	RetryConfig  *RetryConfig      // Retry configuration (nil = no retry)
	ExtraHeaders map[string]string // Additional HTTP headers (e.g., OpenRouter attribution)
}

// WithBaseURL sets a custom base URL for the provider.
// Used for OpenAI-compatible providers like Ollama, vLLM, etc.
func WithBaseURL(url string) Option {
	return func(c *providerConfig) {
		c.BaseURL = strings.TrimRight(url, "/")
	}
}

// WithRetry enables automatic retry with exponential backoff for transient errors.
// Rate-limited requests (429/529) are retried with backoff. After MaxRateLimitRetries
// consecutive rate limits, falls back to FallbackModel if configured.
func WithRetry(config RetryConfig) Option {
	return func(c *providerConfig) {
		c.RetryConfig = &config
	}
}

// WithExtraHeaders sets additional HTTP headers on every request.
// Used for provider-specific headers like OpenRouter attribution.
func WithExtraHeaders(headers map[string]string) Option {
	return func(c *providerConfig) {
		c.ExtraHeaders = headers
	}
}

// WithDefaultRetry enables retry with sensible defaults:
// 5 retries, 1s base delay, 30s max delay, 3 rate limit retries before fallback.
func WithDefaultRetry() Option {
	return func(c *providerConfig) {
		c.RetryConfig = &RetryConfig{}
	}
}

// New creates a Provider instance for the given provider name.
// For OpenAI models, it automatically selects Responses API or Chat Completions
// based on the model name.
//
// Supported provider names:
//   - "openai" — OpenAI Responses API (primary)
//   - "openai_completions" — OpenAI Chat Completions API (explicit)
//   - "anthropic", "claude" — Anthropic Claude Messages API
//   - "gemini" — Google Gemini API
//   - "deepseek", "groq", "openrouter", "together", "ollama" — OpenAI-compatible (Chat Completions)
func New(providerName, apiKey string, opts ...Option) (Provider, error) {
	cfg := &providerConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	var p Provider
	var err error

	switch strings.ToLower(providerName) {
	case "openai":
		p, err = newOpenAIResponses(apiKey, cfg)
	case "openai_codex":
		p, err = newOpenAICodex(apiKey, cfg)
	case "openai_completions", "openai_legacy":
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	case "anthropic", "claude":
		p, err = newAnthropic(apiKey, cfg)
	case "gemini":
		p, err = newGemini(apiKey, cfg)
	case "deepseek":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.deepseek.com/v1"
		}
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	case "groq":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.groq.com/openai/v1"
		}
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	case "openrouter":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://openrouter.ai/api/v1"
		}
		if cfg.ExtraHeaders == nil {
			cfg.ExtraHeaders = map[string]string{}
		}
		if cfg.ExtraHeaders["HTTP-Referer"] == "" {
			cfg.ExtraHeaders["HTTP-Referer"] = "https://github.com/JackZhao98/tofibot"
		}
		if cfg.ExtraHeaders["X-Title"] == "" {
			cfg.ExtraHeaders["X-Title"] = "Tofi"
		}
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	case "together":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "https://api.together.xyz/v1"
		}
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	case "ollama":
		if cfg.BaseURL == "" {
			cfg.BaseURL = "http://localhost:11434/v1"
		}
		p, err = newOpenAIChatCompletions(apiKey, cfg)
	default:
		// Assume OpenAI-compatible for unknown providers
		if cfg.BaseURL != "" {
			p, err = newOpenAIChatCompletions(apiKey, cfg)
		} else {
			return nil, fmt.Errorf("unknown provider: %s (set a custom endpoint with WithBaseURL)", providerName)
		}
	}

	if err != nil {
		return nil, err
	}

	// Wrap with retry if configured
	if cfg.RetryConfig != nil {
		p = NewRetryProvider(p, *cfg.RetryConfig)
	}

	return p, nil
}

// NewForModel creates a Provider that's optimized for the given model.
// It auto-detects the provider and API type from the model name.
func NewForModel(model, apiKey string, opts ...Option) (Provider, error) {
	info, ok := GetModelInfo(model)
	if ok {
		// Known model — check if it needs a specific API type
		if info.Provider == "openai" && info.APIType == "responses" {
			return New("openai", apiKey, opts...)
		}
		if info.Provider == "openai" {
			// For known OpenAI models that aren't marked as "responses",
			// still use Responses API (it supports all OpenAI models)
			return New("openai", apiKey, opts...)
		}
		return New(info.Provider, apiKey, opts...)
	}

	// Unknown model — detect provider from name prefix
	prov := DetectProvider(model)
	return New(prov, apiKey, opts...)
}
