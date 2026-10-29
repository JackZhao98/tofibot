package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func usageViaRoute(t *testing.T, s *Store, conversationID string) ConversationUsage {
	t.Helper()
	srv := &Server{store: s}
	w := httptest.NewRecorder()
	path := "conversations/" + conversationID + "/usage"
	if !srv.routeConversationUsage(w, httptest.NewRequest(http.MethodGet, "/api/"+path, nil), path) {
		t.Fatal("route did not match")
	}
	var got ConversationUsage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("code=%d %s", w.Code, w.Body)
	}
	return got
}

func near(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func TestClaudeUsageIsPricedAndReportsCacheHitRate(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A dated model ID as the Anthropic model list returns it.
	const model = "claude-sonnet-4-5-20250929"
	bot, err := s.CreateBot("Claude Bot", "", model)
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "hi", "claude-cache", []runSpec{{BotID: bot.ID, Model: model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	id := runs[0].ID
	if err = s.recordContextEstimate(id, model, 900); err != nil {
		t.Fatal(err)
	}
	// Anthropic input already includes cache reads and writes.
	first := provider.Usage{InputTokens: 1_000_000, OutputTokens: 100_000, CacheWriteTokens: 600_000, CacheReported: true}
	second := provider.Usage{InputTokens: 1_000_000, OutputTokens: 0, CacheReadTokens: 800_000, CacheReported: true}
	for _, u := range []provider.Usage{first, second} {
		if err = s.recordModelUsage(id, u); err != nil {
			t.Fatal(err)
		}
	}
	got := usageViaRoute(t, s, bot.DMConversationID)
	// Sonnet 4.5: $3 in, $15 out. first = 400k*3 + 600k*3.75 + 100k*15 = 1.2+2.25+1.5; second = 200k*3 + 800k*0.3.
	want := 1.2 + 2.25 + 1.5 + 0.6 + 0.24
	if got.Totals.UnpricedRequests != 0 || !near(got.Totals.EquivalentUSD, want) {
		t.Fatalf("totals=%+v want usd %v", got.Totals, want)
	}
	if got.Totals.CacheReadTokens != 800_000 || got.Totals.CacheHitRate == nil || !near(*got.Totals.CacheHitRate, 0.4) {
		t.Fatalf("totals cache = %d %v", got.Totals.CacheReadTokens, got.Totals.CacheHitRate)
	}
	lc := got.LatestContext
	if lc == nil || lc.CacheReadTokens != 800_000 || lc.CacheHitRate == nil || !near(*lc.CacheHitRate, 0.8) || lc.UsedTokens != 1_000_000 {
		t.Fatalf("latest=%+v", lc)
	}
	if len(got.PerModel) != 1 || got.PerModel[0].UnpricedRequests != 0 || got.PerModel[0].EquivalentUSD <= 0 {
		t.Fatalf("per_model=%+v", got.PerModel)
	}
}

func TestUsageWithoutCacheFieldsHasNoHitRate(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Codex Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "hi", "no-cache", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil {
		t.Fatal(err)
	}
	id := runs[0].ID
	if err = s.recordContextEstimate(id, bot.Model, 100); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(id, provider.Usage{InputTokens: 1000, OutputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	got := usageViaRoute(t, s, bot.DMConversationID)
	if got.Totals.CacheHitRate != nil || got.LatestContext.CacheHitRate != nil {
		t.Fatalf("hit rate without cache fields: totals=%v latest=%v", got.Totals.CacheHitRate, got.LatestContext.CacheHitRate)
	}
	// A reported zero is a miss, not "unknown".
	if err = s.recordModelUsage(id, provider.Usage{InputTokens: 1000, OutputTokens: 10, CacheReported: true}); err != nil {
		t.Fatal(err)
	}
	got = usageViaRoute(t, s, bot.DMConversationID)
	if got.LatestContext.CacheHitRate == nil || *got.LatestContext.CacheHitRate != 0 {
		t.Fatalf("latest after reported miss = %v", got.LatestContext.CacheHitRate)
	}
	// Only the reporting call is in the denominator: 0 reads over 1000, not over 2000.
	if got.Totals.CacheHitRate == nil || *got.Totals.CacheHitRate != 0 {
		t.Fatalf("totals = %v", got.Totals.CacheHitRate)
	}
}

func TestUsageEquivalentCodexCacheAndRegistry(t *testing.T) {
	// codex-gpt-5.6-terra: $2 in, $12 out; cached input at 0.1x.
	usd, ok := usageEquivalent("codex-gpt-5.6-terra", provider.Usage{InputTokens: 200_000, OutputTokens: 100_000, CacheReadTokens: 100_000, CacheReported: true})
	if !ok || !near(usd, 0.1*2+0.1*0.2+0.1*12) {
		t.Fatalf("codex = %v %v", usd, ok)
	}
	// Registry models are priced with their own cache rate (gpt-4.1: $0.50 cached).
	usd, ok = usageEquivalent("gpt-4.1", provider.Usage{InputTokens: 1_000_000, CacheReadTokens: 1_000_000})
	if !ok || !near(usd, 0.5) {
		t.Fatalf("registry = %v %v", usd, ok)
	}
	if _, ok = usageEquivalent("some-unlisted-model", provider.Usage{InputTokens: 10}); ok {
		t.Fatal("unlisted model must be unpriced")
	}
}

func TestCodexCatalogCarriesContextWindow(t *testing.T) {
	t.Cleanup(func() { provider.SetCatalogContextWindows(nil) })
	provider.SetCatalogContextWindows(nil)
	var raw codexModelsResponse
	if err := json.Unmarshal([]byte(`{"models":[
	 {"slug":"gpt-5.6-terra","display_name":"GPT-5.6-Terra","default_reasoning_level":"medium","context_window":272000,"supported_reasoning_levels":[{"effort":"medium"}]},
	 {"slug":"gpt-5.6-sol","display_name":"GPT-5.6-Sol","max_context_window":400000,"supported_reasoning_levels":[{"effort":"high"}]},
	 {"slug":"gpt-5.6-luna","display_name":"GPT-5.6-Luna","supported_reasoning_levels":[{"effort":"low"}]}]}`), &raw); err != nil {
		t.Fatal(err)
	}
	models := normalizeCodexModels(raw)
	if len(models) != 3 || models[0].ContextWindow != 272000 || models[1].ContextWindow != 400000 || models[2].ContextWindow != 0 {
		t.Fatalf("models=%+v", models)
	}
	publishCatalogWindows(models)
	if provider.GetContextWindow("codex-gpt-5.6-terra") != 272000 || !provider.ContextWindowKnown("codex-gpt-5.6-terra") {
		t.Fatal("catalog window not used")
	}
	if provider.ContextWindowKnown("codex-gpt-5.6-luna") || provider.GetContextWindow("codex-gpt-5.6-luna") != 500000 {
		t.Fatal("model without a reported window must keep the estimated fallback")
	}
	// A snapshot with no windows must not wipe the set above.
	publishCatalogWindows([]ModelOption{{ID: "codex-gpt-5.6-luna"}})
	if provider.GetContextWindow("codex-gpt-5.6-terra") != 272000 {
		t.Fatal("empty snapshot wiped the catalog windows")
	}
	// A stored window is replaced by the real one once it is known.
	lc := latestContext(convUsageRun{RunID: "r", Model: "codex-gpt-5.6-terra", WindowTokens: 128000, WindowKnown: false}, nil)
	if lc.WindowTokens != 272000 || !lc.WindowKnown {
		t.Fatalf("latest window = %d known=%v", lc.WindowTokens, lc.WindowKnown)
	}
}
