package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

func spendStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func bookSpend(t *testing.T, s *Store, at time.Time, model string, usd float64) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = bookSpendTx(tx, at, model, usd); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func setSpendLimit(t *testing.T, s *Store, usd string) {
	t.Helper()
	if err := s.putAutomationValue(autoMonthlySpendLimit, &usd); err != nil {
		t.Fatal(err)
	}
}

func TestSpendMonthBoundaryFollowsTheAccountTimeZone(t *testing.T) {
	s := spendStore(t)
	if _, err := s.putUserTimezone("America/Los_Angeles", false); err != nil {
		t.Fatal(err)
	}
	const model = "claude-sonnet-5-5"
	// 23:30 on 31 October in Los Angeles is 06:30 UTC on 1 November.
	bookSpend(t, s, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), model, 3)
	// 00:30 on 1 November in Los Angeles is 07:30 UTC.
	bookSpend(t, s, time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), model, 5)
	bookSpend(t, s, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), model, 1)

	oct, err := s.spendStatus(time.Date(2026, 11, 1, 6, 45, 0, 0, time.UTC))
	if err != nil || oct.Month != "2026-10" || oct.SpendUSD != 3 {
		t.Fatalf("october=%+v err=%v", oct, err)
	}
	nov, err := s.spendStatus(time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC))
	if err != nil || nov.Month != "2026-11" || nov.SpendUSD != 6 || nov.Timezone != "America/Los_Angeles" {
		t.Fatalf("november=%+v err=%v", nov, err)
	}

	// With no time zone set, the same instants fall in UTC months.
	u := spendStore(t)
	bookSpend(t, u, time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), model, 3)
	utc, err := u.spendStatus(time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC))
	if err != nil || utc.SpendUSD != 3 || utc.State != "off" {
		t.Fatalf("utc=%+v err=%v", utc, err)
	}
}

func TestSpendCountsBilledModelsOnly(t *testing.T) {
	s := spendStore(t)
	bot, err := s.CreateBot("Spend Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	record := func(model string) {
		t.Helper()
		_, runs, _, e := s.AddUserRuns(bot.DMConversationID, "spend "+model, "spend-"+model, []runSpec{{BotID: bot.ID, Model: model}})
		if e != nil || len(runs) != 1 {
			t.Fatalf("runs=%v err=%v", runs, e)
		}
		if e = s.recordContextEstimate(runs[0].ID, model, 1000); e != nil {
			t.Fatal(e)
		}
		if e = s.recordModelUsage(runs[0].ID, provider.Usage{InputTokens: 100000, OutputTokens: 20000}); e != nil {
			t.Fatal(e)
		}
	}
	record("codex-gpt-5.6")
	record("compat:llama3")
	st, err := s.spendStatus(time.Now())
	if err != nil || st.SpendUSD != 0 {
		t.Fatalf("a plan model and an unpriced local model count 0: %+v err=%v", st, err)
	}
	record("claude-sonnet-5-5")
	st, err = s.spendStatus(time.Now())
	if err != nil || st.SpendUSD <= 0 {
		t.Fatalf("an API model counts: %+v err=%v", st, err)
	}
}

func TestSpendAlertAtEightyPercentIsSentOnce(t *testing.T) {
	s := spendStore(t)
	bot, err := s.CreateBot("Alert Bot", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-sonnet-5-5"
	notices := func() int {
		var n int
		if e := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conversation_id=? AND kind='notice' AND notice_data LIKE '%spend_alert%'`, bot.DMConversationID).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	setSpendLimit(t, s, "10")
	now := time.Now().UTC()
	bookSpend(t, s, now, model, 7)
	s.noteSpend(now)
	if notices() != 0 {
		t.Fatal("no notice below 80%")
	}
	bookSpend(t, s, now, model, 1.5)
	s.noteSpend(now)
	s.noteSpend(now)
	bookSpend(t, s, now, model, 0.2)
	s.noteSpend(now)
	if notices() != 1 {
		t.Fatalf("notices=%d, want one", notices())
	}
	var content string
	if err = s.db.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND notice_data LIKE '%spend_alert%'`, bot.DMConversationID).Scan(&content); err != nil || !strings.Contains(content, "$8.50") || !strings.Contains(content, "$10") {
		t.Fatalf("content=%q err=%v", content, err)
	}
	// A new limit is a new alert.
	setSpendLimit(t, s, "9")
	s.noteSpend(now)
	if notices() != 2 {
		t.Fatalf("notices=%d after a new limit", notices())
	}
}

func TestSpendCapRefusesBilledModelsAtTheLimit(t *testing.T) {
	s := spendStore(t)
	const model = "claude-sonnet-5-5"
	now := time.Now().UTC()
	if err := s.checkSpendCap(model, now); err != nil {
		t.Fatalf("no limit set: %v", err)
	}
	setSpendLimit(t, s, "5")
	bookSpend(t, s, now, model, 4.99)
	if err := s.checkSpendCap(model, now); err != nil {
		t.Fatalf("below the limit: %v", err)
	}
	bookSpend(t, s, now, model, 0.02)
	err := s.checkSpendCap(model, now)
	var capErr *spendCapError
	if !errors.As(err, &capErr) {
		t.Fatalf("at the limit: %v", err)
	}
	for _, want := range []string{"spend_cap_reached", "$5", "$5.01", "Settings > Usage", "did not call the model"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q lacks %q", err.Error(), want)
		}
	}
	if s.checkSpendCap("codex-gpt-5.6", now) != nil || s.checkSpendCap("compat:llama3", now) != nil {
		t.Fatal("a plan model and an unpriced local model are never refused")
	}
	if s.checkSpendCap("gpt-5.5", now) == nil {
		t.Fatal("an OpenAI API model is refused")
	}
	// Raising the limit lifts the refusal at once.
	setSpendLimit(t, s, "50")
	if err = s.checkSpendCap(model, now); err != nil {
		t.Fatalf("after raising the limit: %v", err)
	}
}

func TestSpendCapStopsANewRunBeforeTheModelCall(t *testing.T) {
	e := &captureEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e, DefaultModel: "claude-sonnet-5-5", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	setSpendLimit(t, s.store, "1")
	bookSpend(t, s.store, time.Now().UTC(), "claude-sonnet-5-5", 2)

	bot, err := s.store.CreateBot("capped", "", "claude-sonnet-5-5")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(bot.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, bot.ID, "hello", "cap-1")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(c, r)
	if e.req.Model != "" {
		t.Fatalf("the model was called: %q", e.req.Model)
	}
	done, err := s.store.GetRun(r.ID)
	if err != nil || done.Status != "failed" || done.failure() == nil || done.failure().Code != "spend_cap_reached" {
		t.Fatalf("run=%+v err=%v", done, err)
	}
	if f := done.failure(); !strings.Contains(f.Message, "$1") || !strings.Contains(f.Message, "$2.00") || !strings.Contains(f.Message, "Settings > Usage") {
		t.Fatalf("message=%q", f.Message)
	}

	// A plan model on the same account still runs.
	plan, err := s.store.CreateBot("plan", "", "codex-gpt-5.6")
	if err != nil {
		t.Fatal(err)
	}
	pc, _ := s.store.GetConversation(plan.DMConversationID)
	_, pr, _, err := s.store.AddUserRun(pc.ID, plan.ID, "hello", "cap-2")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(pc, pr)
	if e.req.Model != "codex-gpt-5.6" {
		t.Fatalf("the plan model run did not reach the engine: %q", e.req.Model)
	}
	s.summaryWG.Wait()
}
