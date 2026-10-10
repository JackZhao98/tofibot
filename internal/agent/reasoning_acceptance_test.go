package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// Real-provider acceptance for run-resume Phase 1. Skipped unless
// TOFI_ACCEPT_REASONING_KEY (API key) and TOFI_ACCEPT_REASONING_MODEL are set,
// e.g. an Anthropic model to cover the append-only path or an OpenAI model to
// cover trimmed tool results with retained reasoning. Synthetic data only.
func TestReasoningRetentionLiveAcceptance(t *testing.T) {
	key, model := os.Getenv("TOFI_ACCEPT_REASONING_KEY"), os.Getenv("TOFI_ACCEPT_REASONING_MODEL")
	if key == "" || model == "" {
		t.Skip("set TOFI_ACCEPT_REASONING_KEY and TOFI_ACCEPT_REASONING_MODEL to run")
	}
	p, err := provider.NewForModel(model, key)
	if err != nil {
		t.Fatal(err)
	}
	rejected := 0
	var estimates []ContextBreakdown
	var requests int
	result, err := runLoop(t, AgentConfig{
		Provider: p, Model: model, ReasoningEffort: os.Getenv("TOFI_ACCEPT_REASONING_EFFORT"),
		Prompt:                    "Call the lookup tool 14 times, one call per turn, with a different topic each time. After each result, reason about whether the earlier results change what to look up next. Then answer with one sentence.",
		Ctx:                       context.Background(),
		OnReasoningReplayRejected: func() { rejected++ },
		OnContextBreakdown:        func(b ContextBreakdown) { estimates = append(estimates, b); requests++ },
		ExtraTools: []ExtraBuiltinTool{lookupTool(func(context.Context, map[string]interface{}) (string, error) {
			return strings.Repeat("synthetic reference row. ", 1200), nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejected != 0 {
		t.Fatalf("provider rejected replayed reasoning %d time(s)", rejected)
	}
	if strings.TrimSpace(result.Content) == "" || len(estimates) < 2 {
		t.Fatalf("content=%q estimates=%d", result.Content, len(estimates))
	}
	for i, b := range estimates {
		t.Logf("request %d: system=%d messages=%d reasoning=%d", i+1, b.System, b.Messages, b.Reasoning)
	}
}
