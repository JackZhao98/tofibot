package app

import (
	"context"
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/provider"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUsageRecordsRunContextAndAgentTool(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Usage Bot", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "check usage", "usage-one", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	first := runs[0]
	if err = s.recordContextEstimate(first.ID, bot.Model, 12000); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(first.ID, provider.Usage{InputTokens: 12500, OutputTokens: 750}); err != nil {
		t.Fatal(err)
	}
	if err = s.recordContextCompact(first.ID, 4500); err != nil {
		t.Fatal(err)
	}
	u, err := s.runContextUsage(first.ID)
	if err != nil || u.EstimatedInput != 4500 || u.LastInput != 12500 || u.TotalOutput != 750 || u.CompactCount != 1 || u.CompactAt != 217600 || !u.WindowKnown {
		t.Fatalf("usage=%+v err=%v", u, err)
	}
	server := &Server{store: s}
	encoded, err := server.contextUsageTool(first.ID).Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool map[string]any
	if err = json.Unmarshal([]byte(encoded), &tool); err != nil {
		t.Fatal(err)
	}
	if tool["tokens_until_compaction_estimate"] != float64(217600-4500) {
		t.Fatalf("tool=%v", tool)
	}
	_, next, _, err := s.AddUserRuns(bot.DMConversationID, "next", "usage-two", []runSpec{{BotID: bot.ID, Model: bot.Model}}, true)
	if err != nil || len(next) != 1 {
		t.Fatalf("next=%v err=%v", next, err)
	}
	if err = s.recordContextEstimate(next[0].ID, bot.Model, 7000); err != nil {
		t.Fatal(err)
	}
	contexts, err := s.listContextUsage()
	if err != nil || len(contexts) != 1 || contexts[0].RunID != next[0].ID {
		t.Fatalf("contexts=%+v err=%v", contexts, err)
	}
	totals, err := s.listAgentUsageTotals()
	if err != nil || len(totals) != 1 || totals[0].InputTokens != 12500 || totals[0].Compactions != 1 || totals[0].Runs != 2 {
		t.Fatalf("totals=%+v err=%v", totals, err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/usage", nil)
	w := httptest.NewRecorder()
	if !server.routeUsage(w, req, "usage") || w.Code != http.StatusOK {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
}

func TestUsagePeriodsPricesAndRetention(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("priced", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "private prompt", "priced-one", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.recordContextEstimate(runs[0].ID, bot.Model, 100); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 1_000_000, OutputTokens: 100_000}); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 50, OutputTokens: 25}); err != nil {
		t.Fatal(err)
	}
	calls, err := s.listUsageCalls()
	if err != nil || len(calls) != 2 || calls[0].TriggerContent != "private prompt" || calls[1].EquivalentUSD != 5.5 {
		t.Fatalf("calls=%+v err=%v", calls, err)
	}
	period, err := s.usagePeriod(24*time.Hour, usageKindChat)
	if err != nil || len(period) != 1 || period[0].Requests != 2 || period[0].InputTokens != 1_000_050 || period[0].EquivalentUSD <= 3 {
		t.Fatalf("period=%+v err=%v", period, err)
	}
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	if _, err = s.db.Exec(`UPDATE usage_calls SET occurred_at=?`, old.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = pruneUsage(s.db, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	calls, err = s.listUsageCalls()
	if err != nil || len(calls) != 0 {
		t.Fatalf("retained calls=%+v err=%v", calls, err)
	}
	period, err = s.usagePeriod(30*24*time.Hour, usageKindChat)
	if err != nil || len(period) != 1 || period[0].Requests != 2 {
		t.Fatalf("30d=%+v err=%v", period, err)
	}
	if _, ok := usageEquivalent("codex-unknown", provider.Usage{InputTokens: 100, OutputTokens: 100}); ok {
		t.Fatal("unknown model must not be priced")
	}
}

func TestReviewUsageIsSeparateFromChat(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("reviewed", "", "codex-gpt-6-sol")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "do it", "review-one", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.recordContextEstimate(runs[0].ID, bot.Model, 100); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 1000, OutputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	if err = s.recordAuxiliaryModelUsage(runs[0].ID, "gpt-5-mini", 400, 40); err != nil {
		t.Fatal(err)
	}
	if err = s.recordAuxiliaryModelUsage(runs[0].ID, "codex-auto-review", 200, 20); err != nil {
		t.Fatal(err)
	}
	for _, window := range []time.Duration{24 * time.Hour, 30 * 24 * time.Hour} {
		chat, err := s.usagePeriod(window, usageKindChat)
		if err != nil || len(chat) != 1 || chat[0].Requests != 1 || chat[0].InputTokens != 1000 || chat[0].OutputTokens != 100 {
			t.Fatalf("%v chat=%+v err=%v", window, chat, err)
		}
		review, err := s.usagePeriod(window, usageKindReview)
		if err != nil || len(review) != 1 || review[0].Requests != 2 || review[0].InputTokens != 600 || review[0].OutputTokens != 60 || review[0].UnpricedRequests != 1 {
			t.Fatalf("%v review=%+v err=%v", window, review, err)
		}
	}
	calls, err := s.listUsageCalls()
	kinds := map[string]int{}
	for _, c := range calls {
		kinds[c.Kind]++
	}
	if err != nil || kinds[usageKindChat] != 1 || kinds[usageKindReview] != 2 {
		t.Fatalf("kinds=%v err=%v", kinds, err)
	}
	// Run totals still count everything the run cost, once.
	totals, err := s.listAgentUsageTotals()
	if err != nil || totals[0].InputTokens != 1600 {
		t.Fatalf("totals=%+v err=%v", totals, err)
	}
	w := httptest.NewRecorder()
	if !(&Server{store: s}).routeUsage(w, httptest.NewRequest(http.MethodGet, "/api/usage", nil), "usage") || w.Code != http.StatusOK {
		t.Fatalf("response=%d %s", w.Code, w.Body.String())
	}
	var body struct {
		Periods       map[string][]UsagePeriod `json:"periods"`
		ReviewPeriods map[string][]UsagePeriod `json:"review_periods"`
		Calls         []map[string]any         `json:"calls"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"24h", "7d", "30d"} {
		if len(body.Periods[key]) != 1 || body.Periods[key][0].Requests != 1 || len(body.ReviewPeriods[key]) != 1 || body.ReviewPeriods[key][0].Requests != 2 {
			t.Fatalf("%s periods=%+v review=%+v", key, body.Periods[key], body.ReviewPeriods[key])
		}
	}
	if len(body.Calls) != 3 || body.Calls[0]["kind"] == nil {
		t.Fatalf("calls=%v", body.Calls)
	}
}
