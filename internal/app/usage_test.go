package app

import (
	"context"
	"encoding/json"
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
	if err = s.recordModelUsage(first.ID, 12500, 750); err != nil {
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
	if err = s.recordModelUsage(runs[0].ID, 1_000_000, 100_000); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, 50, 25); err != nil {
		t.Fatal(err)
	}
	calls, err := s.listUsageCalls()
	if err != nil || len(calls) != 2 || calls[0].TriggerContent != "private prompt" || calls[1].EquivalentUSD != 5.5 {
		t.Fatalf("calls=%+v err=%v", calls, err)
	}
	period, err := s.usagePeriod(24 * time.Hour)
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
	period, err = s.usagePeriod(30 * 24 * time.Hour)
	if err != nil || len(period) != 1 || period[0].Requests != 2 {
		t.Fatalf("30d=%+v err=%v", period, err)
	}
	if _, ok := usageEquivalent("codex-unknown", 100, 100); ok {
		t.Fatal("unknown model must not be priced")
	}
}
