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
