package provider

import "strings"

// ModelInfo holds metadata for a known model.
type ModelInfo struct {
	Provider        string  // Provider name (e.g., "openai", "anthropic", "gemini")
	APIType         string  // For OpenAI: "responses" or "chat_completions" (empty = auto)
	ContextWindow   int     // Maximum context window in tokens
	InputCostPer1M  float64 // Cost per 1M input tokens in USD
	OutputCostPer1M float64 // Cost per 1M output tokens in USD
	MaxOutputTokens int     // Largest max_tokens the model accepts (0 = unknown)
	// Prompt-cache prices per 1M tokens; 0 = 0.1x / 1.25x input (5-minute TTL).
	CacheReadCostPer1M  float64
	CacheWriteCostPer1M float64
}

// Registry maps model names/prefixes to their metadata.
// For prefix-matched models, we check longest match first.
var Registry = map[string]ModelInfo{
	// ─── ChatGPT Codex OAuth ───
	"codex-gpt-6-astra": {Provider: "openai_codex", APIType: "responses", ContextWindow: 272000},
	"codex-gpt-6-sol":   {Provider: "openai_codex", APIType: "responses", ContextWindow: 272000},
	"codex-gpt-6-luna":  {Provider: "openai_codex", APIType: "responses", ContextWindow: 272000},
	"codex-gpt-5.4":     {Provider: "openai_codex", APIType: "responses", ContextWindow: 400000},
	"codex-gpt-5.5":     {Provider: "openai_codex", APIType: "responses", ContextWindow: 400000},
	// ─── OpenAI — Responses API ───
	"o3":         {Provider: "openai", APIType: "responses", ContextWindow: 200000, InputCostPer1M: 2.00, OutputCostPer1M: 8.00},
	"o3-pro":     {Provider: "openai", APIType: "responses", ContextWindow: 200000, InputCostPer1M: 20.00, OutputCostPer1M: 80.00},
	"o4-mini":    {Provider: "openai", APIType: "responses", ContextWindow: 200000, InputCostPer1M: 1.10, OutputCostPer1M: 4.40},
	"gpt-5":      {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 1.25, OutputCostPer1M: 10.00},
	"gpt-5.1":    {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 1.25, OutputCostPer1M: 10.00},
	"gpt-5.2":    {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 1.75, OutputCostPer1M: 14.00},
	"gpt-5.4":    {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 2.50, OutputCostPer1M: 15.00},
	"gpt-5.5":    {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 2.50, OutputCostPer1M: 15.00},
	"gpt-5-mini": {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 0.25, OutputCostPer1M: 2.00},
	"gpt-5-nano": {Provider: "openai", APIType: "responses", ContextWindow: 1047576, InputCostPer1M: 0.05, OutputCostPer1M: 0.40},

	// ─── OpenAI — Chat Completions (also work with Responses API) ───
	"gpt-4o":       {Provider: "openai", ContextWindow: 128000, InputCostPer1M: 2.50, OutputCostPer1M: 10.00},
	"gpt-4o-mini":  {Provider: "openai", ContextWindow: 128000, InputCostPer1M: 0.15, OutputCostPer1M: 0.60},
	"gpt-4.1":      {Provider: "openai", ContextWindow: 1047576, InputCostPer1M: 2.00, OutputCostPer1M: 8.00},
	"gpt-4.1-mini": {Provider: "openai", ContextWindow: 1047576, InputCostPer1M: 0.40, OutputCostPer1M: 1.60},
	"gpt-4.1-nano": {Provider: "openai", ContextWindow: 1047576, InputCostPer1M: 0.10, OutputCostPer1M: 0.40},

	// ─── Anthropic (first-party list prices, 2026-09) ───
	"claude-fable-5-1":         {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 10.00, OutputCostPer1M: 50.00, CacheReadCostPer1M: 0.25},
	"claude-mythos-5-1":        {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 10.00, OutputCostPer1M: 50.00, CacheReadCostPer1M: 0.25},
	"claude-fable-5":           {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 10.00, OutputCostPer1M: 50.00},
	"claude-mythos-5":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 10.00, OutputCostPer1M: 50.00},
	"claude-opus-5-5":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 4.00, OutputCostPer1M: 20.00, CacheReadCostPer1M: 0.20},
	"claude-opus-5":            {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 5.00, OutputCostPer1M: 25.00},
	"claude-opus-4-8":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 5.00, OutputCostPer1M: 25.00},
	"claude-opus-4-7":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 5.00, OutputCostPer1M: 25.00},
	"claude-opus-4-6":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 5.00, OutputCostPer1M: 25.00},
	"claude-opus-4-5":          {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 5.00, OutputCostPer1M: 25.00},
	"claude-opus-4-1":          {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 32000, InputCostPer1M: 15.00, OutputCostPer1M: 75.00},
	"claude-opus-4-0":          {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 32000, InputCostPer1M: 15.00, OutputCostPer1M: 75.00},
	"claude-sonnet-5-5":        {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 2.00, OutputCostPer1M: 10.00},
	"claude-sonnet-5":          {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 2.00, OutputCostPer1M: 10.00},
	"claude-sonnet-4-6":        {Provider: "anthropic", ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 3.00, OutputCostPer1M: 15.00},
	"claude-sonnet-4-5":        {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 3.00, OutputCostPer1M: 15.00},
	"claude-sonnet-4-0":        {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 3.00, OutputCostPer1M: 15.00},
	"claude-haiku-4-5":         {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 1.00, OutputCostPer1M: 5.00},
	"claude-opus-4-20250514":   {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 32000, InputCostPer1M: 15.00, OutputCostPer1M: 75.00},
	"claude-sonnet-4-20250514": {Provider: "anthropic", ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 3.00, OutputCostPer1M: 15.00},
	"claude-haiku-4-20250514":  {Provider: "anthropic", ContextWindow: 200000, InputCostPer1M: 0.80, OutputCostPer1M: 4.00},

	// ─── Google Gemini ───
	"gemini-2.5-pro":   {Provider: "gemini", ContextWindow: 1048576, InputCostPer1M: 1.25, OutputCostPer1M: 10.00},
	"gemini-2.5-flash": {Provider: "gemini", ContextWindow: 1048576, InputCostPer1M: 0.15, OutputCostPer1M: 0.60},
	"gemini-2.0-flash": {Provider: "gemini", ContextWindow: 1048576, InputCostPer1M: 0.10, OutputCostPer1M: 0.40},

	// ─── DeepSeek ───
	"deepseek-chat":     {Provider: "deepseek", ContextWindow: 64000, InputCostPer1M: 0.27, OutputCostPer1M: 1.10},
	"deepseek-reasoner": {Provider: "deepseek", ContextWindow: 64000, InputCostPer1M: 0.55, OutputCostPer1M: 2.19},
}

// GetModelInfo returns metadata for a known model.
// It first tries exact match, then prefix match (longest prefix wins).
func GetModelInfo(model string) (ModelInfo, bool) {
	// Exact match
	if info, ok := Registry[model]; ok {
		return info, true
	}

	// Prefix match — find the longest matching prefix
	bestPrefix := ""
	var bestInfo ModelInfo
	for name, info := range Registry {
		if strings.HasPrefix(model, name) && len(name) > len(bestPrefix) {
			bestPrefix = name
			bestInfo = info
		}
	}
	if bestPrefix != "" {
		return bestInfo, true
	}

	return ModelInfo{}, false
}

// Provider routing names returned by ProviderForModel.
const (
	ProviderOpenAICodex = "openai_codex"
	ProviderOpenAI      = "openai"
	ProviderAnthropic   = "anthropic"
)

// ProviderForModel routes a persisted bot model ID to the provider that
// serves it: codex-* (including codex-auto-review) to the Codex OAuth
// backend, claude* to the Anthropic API, everything else to the OpenAI API.
func ProviderForModel(id string) string {
	m := strings.ToLower(strings.TrimSpace(id))
	switch {
	case strings.HasPrefix(m, "codex-"):
		return ProviderOpenAICodex
	case strings.HasPrefix(m, "claude"):
		return ProviderAnthropic
	default:
		return ProviderOpenAI
	}
}

// DetectProvider infers the provider name from a model name.
func DetectProvider(model string) string {
	m := strings.ToLower(model)
	// ChatGPT Codex models use the codex-* naming convention. Keep this
	// prefix rule so newly released Codex models route correctly even before
	// they have optional registry metadata.
	if strings.HasPrefix(m, "codex-") {
		return "openai_codex"
	}
	if info, ok := GetModelInfo(m); ok {
		return info.Provider
	}

	// OpenRouter format: "author/model-name" (e.g., "anthropic/claude-3.5-sonnet")
	if strings.Contains(m, "/") {
		return "openrouter"
	}

	// Check known prefixes
	switch {
	case strings.HasPrefix(m, "claude"):
		return "anthropic"
	case strings.HasPrefix(m, "gemini"):
		return "gemini"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "llama"), strings.HasPrefix(m, "mistral"), strings.HasPrefix(m, "mixtral"):
		return "ollama" // Common local models
	case strings.HasPrefix(m, "gpt-"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"):
		return "openai"
	default:
		return "openai" // Default fallback
	}
}

// GetContextWindow returns the context window size for a model.
// Falls back to 128000 for unknown models.
func GetContextWindow(model string) int {
	if info, ok := GetModelInfo(model); ok {
		if info.ContextWindow > 0 {
			return info.ContextWindow
		}
	}

	// For OpenRouter "author/model" format, try matching the model part after "/"
	if idx := strings.Index(model, "/"); idx >= 0 {
		suffix := model[idx+1:]
		if info, ok := GetModelInfo(suffix); ok {
			return info.ContextWindow
		}
	}

	// Heuristic fallbacks
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude"):
		return 200000
	case strings.Contains(m, "gemini"):
		return 1048576
	case strings.Contains(m, "deepseek"):
		return 64000
	default:
		return 128000
	}
}

// CalculateCost calculates the USD cost for given token usage on a model.
func CalculateCost(model string, usage Usage) float64 {
	info, ok := GetModelInfo(model)
	if !ok {
		return 0
	}
	readPrice, writePrice := info.CacheReadCostPer1M, info.CacheWriteCostPer1M
	if readPrice == 0 {
		readPrice = info.InputCostPer1M * 0.1
	}
	if writePrice == 0 {
		writePrice = info.InputCostPer1M * 1.25
	}
	// Cache counts are subsets of InputTokens, priced at their own rates.
	uncached := usage.InputTokens - usage.CacheReadTokens - usage.CacheWriteTokens
	if uncached < 0 {
		uncached = 0
	}
	inputCost := float64(uncached)/1_000_000*info.InputCostPer1M +
		float64(usage.CacheReadTokens)/1_000_000*readPrice +
		float64(usage.CacheWriteTokens)/1_000_000*writePrice
	outputCost := float64(usage.OutputTokens) / 1_000_000 * info.OutputCostPer1M
	return inputCost + outputCost
}

// ListModelsForProvider returns all known models for a given provider.
func ListModelsForProvider(providerName string) []string {
	var models []string
	for name, info := range Registry {
		if info.Provider == providerName {
			models = append(models, name)
		}
	}
	return models
}

// ListAllModels returns all known models with their info.
func ListAllModels() map[string]ModelInfo {
	result := make(map[string]ModelInfo, len(Registry))
	for k, v := range Registry {
		result[k] = v
	}
	return result
}
