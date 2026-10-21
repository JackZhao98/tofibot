package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Page reading is offered with search, to the same models, behind the same
// switches.
func TestWebFetchFollowsWebSearchEligibility(t *testing.T) {
	if webFetchMaxUses("claude-sonnet-5-5", nil) <= 0 || webFetchMaxUses("claude-haiku-4-5", nil) <= 0 {
		t.Fatal("an Anthropic model gets no server-side page reader")
	}
	if webFetchMaxUses("gpt-5.5", nil) != 0 {
		t.Fatal("fetch offered to a model whose provider has no server-side fetch")
	}
	if webFetchMaxUses("claude-sonnet-5-5", []Tool{{Name: "web_fetch"}}) != 0 {
		t.Fatal("fetch declared beside a tool of the same name")
	}
	if webFetchMaxUses("claude-sonnet-5-5", []Tool{{Name: "web_search"}}) <= 0 {
		t.Fatal("a Bot tool named like search must not remove page reading")
	}
	t.Setenv("TOFI_WEB_SEARCH", "off")
	if webFetchMaxUses("claude-sonnet-5-5", nil) != 0 {
		t.Fatal("the owner's off switch is ignored")
	}
}

func TestWebFetchSitsBehindTheStoredWebSearchSwitch(t *testing.T) {
	s := providerServer(t, newFakeOpenAI(t, "ok"), nil)
	if s.fetchUses("claude-sonnet-5-5", nil) <= 0 {
		t.Fatal("fetch is off by default although search is on")
	}
	if code := autoPutCode(t, s, `{"web_search":false}`); code != 200 {
		t.Fatalf("put=%d", code)
	}
	if s.searchUses("claude-sonnet-5-5", nil) != 0 || s.fetchUses("claude-sonnet-5-5", nil) != 0 {
		t.Fatal("the stored switch does not turn page reading off with search")
	}
}

func TestWebFetchIsCountedWithoutMoneyOrARequest(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Fetch Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "what is it", "wf-count", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	if err = s.recordContextEstimate(runs[0].ID, bot.Model, 1000); err != nil {
		t.Fatal(err)
	}
	if err = s.recordModelUsage(runs[0].ID, 1000, 100); err != nil {
		t.Fatal(err)
	}
	base, _ := s.usagePeriod(time.Hour, usageKindChat)
	if err = s.recordWebFetchUsage(runs[0].ID, 2); err != nil {
		t.Fatal(err)
	}
	var rows int
	var usd float64
	if err = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(equivalent_usd),0) FROM usage_calls WHERE run_id=? AND kind=?`, runs[0].ID, usageKindFetch).Scan(&rows, &usd); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || usd != 0 {
		t.Fatalf("rows=%d usd=%v", rows, usd)
	}
	after, err := s.usagePeriod(time.Hour, usageKindChat)
	if err != nil || len(after) != 1 || len(base) != 1 || after[0].Requests != base[0].Requests || after[0].EquivalentUSD != base[0].EquivalentUSD {
		t.Fatalf("a page read changed the chat totals: base=%+v after=%+v err=%v", base, after, err)
	}
	calls, err := s.listUsageCalls()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if c.Kind == usageKindFetch {
			t.Fatal("a zero-fee page read is listed as a call")
		}
	}
}

func TestWebFetchEventsLandInTheRunsEvidence(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.CreateBot("Evidence Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	_, runs, _, err := s.AddUserRuns(bot.DMConversationID, "what is it", "wf-evidence", []runSpec{{BotID: bot.ID, Model: bot.Model}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%v err=%v", runs, err)
	}
	queued, finished := runtime.WebFetchToolEventForTest(provider.WebFetch{ID: "f1", URL: "https://example.com/t", Title: "T"})
	for _, ev := range []runtime.ToolEvent{queued, finished} {
		if err := s.RecordToolEvent(bot.DMConversationID, bot.ID, runs[0].ID, ev); err != nil {
			t.Fatalf("%s: %v", ev.Status, err)
		}
	}
	var name, status, result string
	if err := s.db.QueryRow(`SELECT name,status,result FROM tool_activities WHERE run_id=?`, runs[0].ID).Scan(&name, &status, &result); err != nil {
		t.Fatal(err)
	}
	if name != "web_fetch" || status != "completed" || !strings.Contains(result, "https://example.com/t") {
		t.Fatalf("%s %s %q", name, status, result)
	}
}

// The point-of-decision principle lives in the say contract as well as the
// shared guidance, and carries no sample sentences, contrasts or keyword lists.
func TestSayContractCarriesTheLookupPrinciple(t *testing.T) {
	for _, want := range []string{
		"confirmed by a search in this run",
		"presented as sourced comes from a page you opened in this run",
		"otherwise the answer says it is from memory",
	} {
		if !strings.Contains(sayToolDescription, want) {
			t.Fatalf("say description lost %q", want)
		}
	}
	for name, text := range map[string]string{"say": sayToolDescription, "group line": groupLatestMessageLine} {
		lower := strings.ToLower(text)
		for _, banned := range []string{"for example", "e.g.", "such as", "not just", "rather than"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("%s carries contrast or example wording %q", name, banned)
			}
		}
	}
}
