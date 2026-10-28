package app

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestWebSearchIsOfferedToAnthropicModelsOnly(t *testing.T) {
	if webSearchMaxUses("claude-sonnet-5-5", nil) <= 0 || webSearchMaxUses("claude-haiku-4-5", nil) <= 0 {
		t.Fatal("an Anthropic model gets no server-side search")
	}
	if webSearchMaxUses("gpt-5.5", nil) != 0 {
		t.Fatal("search offered to a model whose provider has no server-side search")
	}
	if webSearchMaxUses("claude-sonnet-5-5", []Tool{{Name: "web_search"}}) != 0 {
		t.Fatal("search declared beside a tool of the same name")
	}
	t.Setenv("TOFI_WEB_SEARCH", "off")
	if webSearchMaxUses("claude-sonnet-5-5", nil) != 0 {
		t.Fatal("the owner's off switch is ignored")
	}
}

func TestWebSearchFeeIsBookedWithoutCountingARequest(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Search Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "what is it", "ws-fee", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	if err = s.recordContextEstimate(runs[0].ID, bot.Model, 1000); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 1000, OutputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	base, _ := s.usagePeriod(time.Hour, usageKindChat)
	if err = s.recordWebSearchUsage(runs[0].ID, 2); err != nil {
		t.Fatal(err)
	}
	after, err := s.usagePeriod(time.Hour, usageKindChat)
	if err != nil || len(after) != 1 || len(base) != 1 {
		t.Fatalf("after=%+v base=%+v err=%v", after, base, err)
	}
	if after[0].Requests != base[0].Requests {
		t.Fatalf("a search fee changed the request count: %d -> %d", base[0].Requests, after[0].Requests)
	}
	if got := after[0].EquivalentUSD - base[0].EquivalentUSD; math.Abs(got-0.02) > 1e-9 {
		t.Fatalf("fee=%v want 0.02", got)
	}
}

func TestWebSearchEventsLandInTheRunsEvidence(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Evidence Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "what is it", "ws-evidence", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	queued, finished := runtime.WebSearchToolEventForTest(provider.WebSearch{ID: "s1", Query: "synthetic", Sources: []provider.WebSearchSource{{Title: "T", URL: "https://example.com/t"}}})
	for _, ev := range []runtime.ToolEvent{queued, finished} {
		if err := s.RecordToolEvent(bot.DMConversationID, bot.ID, runs[0].ID, ev); err != nil {
			t.Fatalf("%s: %v", ev.Status, err)
		}
	}
	var name, status, result string
	if err := s.db.QueryRow(`SELECT name,status,result FROM tool_activities WHERE run_id=?`, runs[0].ID).Scan(&name, &status, &result); err != nil {
		t.Fatal(err)
	}
	if name != "web_search" || status != "completed" || !strings.Contains(result, "https://example.com/t") {
		t.Fatalf("%s %s %q", name, status, result)
	}
}
