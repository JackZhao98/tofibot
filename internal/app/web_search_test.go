package app

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestWebSearchIsOfferedToHostedSearchProviders(t *testing.T) {
	if webSearchMaxUses("claude-sonnet-5-5", nil) <= 0 || webSearchMaxUses("claude-haiku-4-5", nil) <= 0 {
		t.Fatal("an Anthropic model gets no server-side search")
	}
	for _, m := range []string{"gpt-5.5", "codex-gpt-5.6-luna"} {
		if webSearchMaxUses(m, nil) <= 0 {
			t.Fatalf("%s gets no hosted search", m)
		}
		if webSearchMaxUses(m, []Tool{{Name: "web_search"}}) != 0 {
			t.Fatalf("%s: hosted search declared beside a tool of the same name", m)
		}
		if webFetchMaxUses(m, nil) != 0 {
			t.Fatalf("%s: OpenAI has no hosted page reader", m)
		}
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

func TestOpenAIRunWithoutKeysOrComputerSearchesNativelyAndKeepsFetch(t *testing.T) {
	s := webServer(t)
	for _, model := range []string{"gpt-5.5", "codex-gpt-5.6-luna"} {
		run := Run{Model: model}
		names := toolNames(s.webTools(run))
		if names["web_search"] {
			t.Fatalf("%s: external web_search registered with no provider", model)
		}
		if !names["web_fetch"] {
			t.Fatalf("%s: the builtin page reader must stay: OpenAI has no hosted fetch", model)
		}
		tools := s.webTools(run)
		if s.searchUses(model, tools) <= 0 {
			t.Fatalf("%s: no hosted search without keys or a computer", model)
		}
		if s.fetchUses(model, tools) != 0 {
			t.Fatalf("%s: hosted fetch requested for OpenAI", model)
		}
	}
	// An owner-configured external provider keeps priority and is never doubled.
	putWebKey(t, s, "tavily", testTavilyKey)
	tools := s.webTools(Run{Model: "gpt-5.5"})
	if !toolNames(tools)["web_search"] || s.searchUses("gpt-5.5", tools) != 0 {
		t.Fatalf("search doubled: %v uses=%d", toolNames(tools), s.searchUses("gpt-5.5", tools))
	}
	// Anthropic still gets neither external tool.
	if names := toolNames(s.webTools(Run{Model: "claude-sonnet-5-5"})); names["web_search"] || names["web_fetch"] {
		t.Fatalf("anthropic: %v", names)
	}
}

func TestOpenAIWebSearchesAreCountedWithoutAFee(t *testing.T) {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bot, err := st.CreateBot("OpenAI Search Bot", "", "gpt-5.5")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := st.AddUserRuns(bot.DMConversationID, "what is it", "ws-openai", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	if err = st.recordContextEstimate(runs[0].ID, bot.Model, 1000); err != nil {
		t.Fatal(err)
	}
	if err = st.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 1000, OutputTokens: 100}); err != nil {
		t.Fatal(err)
	}
	if err = st.recordWebSearchUsageAt(runs[0].ID, 2, 0); err != nil {
		t.Fatal(err)
	}
	var rows int
	var usd float64
	if err = st.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(equivalent_usd),0) FROM usage_calls WHERE run_id=? AND kind=?`, runs[0].ID, usageKindSearch).Scan(&rows, &usd); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || usd != 0 {
		t.Fatalf("rows=%d usd=%v, want 2 rows and no invented fee", rows, usd)
	}
}

func TestHostedSearchIsNotReplacedByTheComputer(t *testing.T) {
	for _, c := range []struct {
		hosted bool
		chain  []string
		want   bool
	}{
		{false, nil, false},
		{false, []string{webComputerID}, true},
		{true, []string{webComputerID}, false},
		{true, []string{"tavily", webComputerID}, true},
		{true, []string{"searxng"}, true},
		{true, nil, false},
	} {
		if got := externalSearchFor(c.hosted, c.chain); got != c.want {
			t.Fatalf("hosted=%v chain=%v: got %v", c.hosted, c.chain, got)
		}
	}
}
