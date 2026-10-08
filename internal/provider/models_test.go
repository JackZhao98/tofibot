package provider

import "testing"

func TestDetectProviderCodexPrefix(t *testing.T) {
	for _, model := range []string{"codex-gpt-6-astra", "codex-gpt-6-sol", "codex-gpt-6-luna", "codex-gpt-5.6-luna", "codex-future-model"} {
		if got := DetectProvider(model); got != "openai_codex" {
			t.Errorf("DetectProvider(%q) = %q, want openai_codex", model, got)
		}
	}
}

func TestGPT6CodexMetadata(t *testing.T) {
	for _, model := range []string{"codex-gpt-6-astra", "codex-gpt-6-sol", "codex-gpt-6-luna"} {
		info, ok := GetModelInfo(model)
		if !ok || info.Provider != "openai_codex" || info.APIType != "responses" || info.ContextWindow != 272000 {
			t.Errorf("GetModelInfo(%q) = %+v, %v", model, info, ok)
		}
	}
}

func TestUnknownCodexModelUsesConservativeContextFallback(t *testing.T) {
	if got := GetContextWindow("codex-gpt-5.6-luna"); got != 128000 {
		t.Fatalf("GetContextWindow() = %d, want conservative fallback", got)
	}
}

func TestProviderForModel(t *testing.T) {
	for model, want := range map[string]string{
		"codex-gpt-6-luna":          "openai_codex",
		"codex-auto-review":         "openai_codex",
		"Codex-GPT-5.5":             "openai_codex",
		"claude-opus-5-5":           "anthropic",
		"claude-haiku-4-5-20251001": "anthropic",
		"gpt-6-luna":                "openai",
		"o4-mini":                   "openai",
		"gpt-4o":                    "openai",
		"":                          "openai",
	} {
		if got := ProviderForModel(model); got != want {
			t.Errorf("ProviderForModel(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestClaudeRegistryMetadata(t *testing.T) {
	for model, want := range map[string]ModelInfo{
		"claude-opus-5-5":           {ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 4},
		"claude-sonnet-5-5":         {ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 2},
		"claude-fable-5-1":          {ContextWindow: 1000000, MaxOutputTokens: 128000, InputCostPer1M: 10},
		"claude-haiku-4-5-20251001": {ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 1},
		"claude-sonnet-4-20250514":  {ContextWindow: 200000, MaxOutputTokens: 64000, InputCostPer1M: 3},
	} {
		info, ok := GetModelInfo(model)
		if !ok || info.Provider != "anthropic" || info.ContextWindow != want.ContextWindow || info.MaxOutputTokens != want.MaxOutputTokens || info.InputCostPer1M != want.InputCostPer1M {
			t.Errorf("GetModelInfo(%q) = %+v, %v", model, info, ok)
		}
		if GetContextWindow(model) != want.ContextWindow {
			t.Errorf("GetContextWindow(%q) = %d", model, GetContextWindow(model))
		}
	}
}

func TestSupportsReasoning(t *testing.T) {
	for model, want := range map[string]bool{
		"gpt-5": true, "gpt-5.5": true, "gpt-5-mini": true, "gpt-6-luna": true, "gpt-6.1-sol": true,
		"codex-gpt-6-luna": true, "o1": true, "o3-pro": true, "o4-mini": true,
		"gpt-4o": false, "gpt-4.1": false, "codex-auto-review": false, "omni": false,
	} {
		if got := supportsReasoning(model); got != want {
			t.Errorf("supportsReasoning(%q) = %v, want %v", model, got, want)
		}
	}
}
