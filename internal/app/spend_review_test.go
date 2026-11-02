package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// A compatible endpoint serving a model the registry prices counts; an unknown
// local model does not.
func TestSpendCompatPricedModelCounts(t *testing.T) {
	if !modelBilledPerCall("compat:claude-sonnet-5-5") {
		t.Fatal("a compat endpoint serving a priced model must be billed")
	}
	if modelBilledPerCall("compat:llama3") {
		t.Fatal("an unpriced local model must stay uncounted")
	}
	s := spendStore(t)
	const model = "compat:claude-sonnet-5-5"
	now := time.Now().UTC()
	setSpendLimit(t, s, "1")
	usd, known := usageEquivalent(model, provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	if !known || usd <= 1 {
		t.Fatalf("usd=%v known=%v", usd, known)
	}
	bookSpend(t, s, now, model, usd)
	if s.checkSpendCap(model, now) == nil {
		t.Fatal("a priced compat model is refused at the limit")
	}
}

// Background calls (follow-up sweep, grader, reviewer, tool search) are refused
// at the limit and logged once.
func TestSpendCapRefusesBackgroundCalls(t *testing.T) {
	s := spendStore(t)
	const model = "claude-sonnet-5-5"
	now := time.Now().UTC()
	setSpendLimit(t, s, "1")
	if s.refuseBackground(model, "the test") != nil {
		t.Fatal("below the limit")
	}
	bookSpend(t, s, now, model, 2)
	for i := 0; i < 3; i++ {
		if s.refuseBackground(model, "the test") == nil {
			t.Fatal("at the limit a background call is refused")
		}
	}
	n := 0
	s.spendRefusalLogged.Range(func(_, _ any) bool { n++; return true })
	if n != 1 {
		t.Fatalf("refusals logged under %d keys, want one", n)
	}
	if s.refuseBackground("codex-gpt-5.6", "the test") != nil {
		t.Fatal("a plan model is never refused")
	}
}

func TestHandoverNotForUsesThePrefixRule(t *testing.T) {
	for model, want := range map[string]bool{"claude-haiku-4-5": true, "claude-haiku-4-5-20251001": true, "compat:claude-haiku-4-5": true, "CLAUDE-HAIKU-4-5": true, "claude-sonnet-5-5": false, "": false} {
		if got := handoverModelMatches("claude-haiku-4-5", model); got != want {
			t.Errorf("model %q: got %v want %v", model, got, want)
		}
	}
}

func TestSpendAlertPrintsTheActualPercent(t *testing.T) {
	s := spendStore(t)
	bot, err := s.CreateBot("Pct Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	setSpendLimit(t, s, "10")
	now := time.Now().UTC()
	bookSpend(t, s, now, "claude-sonnet-5-5", 10.5)
	s.noteSpend(now)
	var content string
	if err = s.db.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND notice_data LIKE '%spend_alert%'`, bot.DMConversationID).Scan(&content); err != nil || !strings.Contains(content, "(105%)") {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestSpendPublishedForTheAdminView(t *testing.T) {
	s := spendStore(t)
	var got []SpendStatus
	s.spendSnapshot = func(st SpendStatus) { got = append(got, st) }
	setSpendLimit(t, s, "10")
	bookSpend(t, s, time.Now().UTC(), "claude-sonnet-5-5", 3)
	s.noteSpend(time.Now().UTC())
	if len(got) != 2 || got[0].LimitUSD != 10 || got[1].SpendUSD != 3 {
		t.Fatalf("published: %+v", got)
	}
}

func TestMCPEndpointRuleForMemberAndAdmin(t *testing.T) {
	s := &Server{accountID: "acct_member"}
	s.accountIsAdmin = func() bool { return false }
	ctx := t.Context()
	for _, u := range []string{"http://127.0.0.1:8123/api/mcp", "http://10.0.0.5/mcp", "http://169.254.169.254/mcp"} {
		if s.mcpEndpointCheck(ctx, u) == nil {
			t.Fatalf("member must be refused for %s", u)
		}
	}
	if s.mcpEndpointCheck(ctx, "https://8.8.8.8/mcp") != nil {
		t.Fatal("a public address is fine")
	}
	t.Setenv(privateEndpointsEnvFlag, "1")
	if s.mcpEndpointCheck(ctx, "http://10.0.0.5/mcp") != nil || s.mcpEndpointCheck(ctx, "http://169.254.169.254/mcp") == nil {
		t.Fatal("the flag opens private ranges only")
	}
	t.Setenv(privateEndpointsEnvFlag, "")
	s.accountIsAdmin = func() bool { return true }
	if s.mcpEndpointCheck(ctx, "http://192.168.1.4:8123/api/mcp") != nil || s.mcpEndpointCheck(ctx, "http://169.254.169.254/mcp") == nil {
		t.Fatal("an Admin reaches private, never link-local")
	}
	t.Setenv(compatPrivateEnvFlag, "1")
	if (&Server{accountID: "x"}).mcpPrivateAllowed() {
		t.Fatal("the model-only alias must not open MCP")
	}
	if !(&Server{accountID: "x"}).compatPrivateAllowed() {
		t.Fatal("the model-only alias still opens models")
	}
}
