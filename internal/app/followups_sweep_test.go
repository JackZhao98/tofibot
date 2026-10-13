package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type followupStubEngine struct {
	mu    sync.Mutex
	calls []Request
	reply func(call int, req Request) (Result, error)
}

func (e *followupStubEngine) Run(_ context.Context, req Request) (Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, req)
	n := len(e.calls)
	e.mu.Unlock()
	return e.reply(n, req)
}

func (e *followupStubEngine) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// shownItems decodes the messages JSON the sweep put in the model request.
func shownItems(t *testing.T, req Request) []map[string]any {
	t.Helper()
	body := req.Messages[0].Content
	var items []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(body, "Messages (JSON):\n")), &items); err != nil {
		t.Fatalf("request payload: %v\n%s", err, body)
	}
	return items
}

func followupServer(t *testing.T, engine *followupStubEngine) (*Server, Bot) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	b := mustBot(t, s.store, "Secretary")
	if err = s.store.putFollowupBotSettings(followupBotSettings{BotID: b.ID, Mode: followupModeShadow, SourceChat: true, SourceEmail: true}); err != nil {
		t.Fatal(err)
	}
	return s, b
}

// ownerSays records an owner message the way the host's ingress does.
func ownerSays(t *testing.T, s *Server, b Bot, text string) Message {
	t.Helper()
	m, _, err := s.store.AddMessage(b.DMConversationID, "user", "", "", text, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`INSERT INTO user_message_ingress(message_id,created_at) VALUES(?,?)`, m.ID, now()); err != nil {
		t.Fatal(err)
	}
	return m
}

func jsonLoops(loops ...map[string]any) string {
	raw, _ := json.Marshal(loops)
	return string(raw)
}

func loopFor(item int, kind, topic, quote string, extra map[string]any) map[string]any {
	l := map[string]any{"item": item, "kind": kind, "counterparty_name": "Elyse", "topic": topic, "category": "home",
		"signals": []string{"third_party_committed"}, "expected_by": map[string]any{"type": "relative", "days": 2},
		"resolution_phrases": []string{"pest control called"}, "suggested_next": "draft a short follow-up", "quote": quote}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func TestParseChatLoopsValidatesEverything(t *testing.T) {
	items := []chatSweepItem{
		{MessageID: "m0", Text: "Elyse said pest control will call me about the roof bees, ping me if not by Friday"},
		{MessageID: "m1", Text: "I will send Marc the draft by 2026-10-30"},
	}
	created := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	good := loopFor(0, loopKindAwaitingOther, "pest control to call about roof bees", "pest control will call me about the roof bees",
		map[string]any{"signals": []string{"owner_asked", "third_party_committed"}, "category": "home"})
	promise := loopFor(1, loopKindOwnerPromise, "send Marc the draft", "send Marc the draft by 2026-10-30",
		map[string]any{"signals": []string{}, "category": "work", "expected_by": map[string]any{"type": "explicit", "date": "2026-10-30"}})
	cases := map[string]map[string]any{
		"fabricated quote":     loopFor(0, loopKindAwaitingOther, "x", "Elyse promised a refund", nil),
		"quote from wrong msg": loopFor(1, loopKindAwaitingOther, "x", "pest control will call me", nil),
		"item out of range":    loopFor(5, loopKindAwaitingOther, "x", "pest control", nil),
		"bad kind":             loopFor(0, "assistant_task", "x", "pest control", nil),
		"empty topic":          loopFor(0, loopKindAwaitingOther, "  ", "pest control", nil),
		"empty quote":          loopFor(0, loopKindAwaitingOther, "x", "", nil),
		"unknown field":        loopFor(0, loopKindAwaitingOther, "x", "pest control", map[string]any{"run_tool": "send_email"}),
		"bad timing hint":      loopFor(0, loopKindAwaitingOther, "x", "pest control", map[string]any{"expected_by": map[string]any{"type": "whenever"}}),
		"quote over 300 runes": loopFor(0, loopKindAwaitingOther, "x", strings.Repeat("a", 301), nil),
	}
	var elements []map[string]any
	elements = append(elements, good, promise)
	for _, c := range cases {
		elements = append(elements, c)
	}
	// A reply wrapped in prose and a Markdown fence still parses.
	reply := "Here you go:\n```json\n" + jsonLoops(elements...) + "\n```\nDone."
	got, err := parseChatLoops(reply, items, "bot", created, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Loops) != 1 && len(got.Loops) != 2 {
		t.Fatalf("unexpected loop count %d", len(got.Loops))
	}
	if got.RejectedInvalid != len(cases) {
		t.Fatalf("rejected %d invalid, want %d", got.RejectedInvalid, len(cases))
	}
	byTopic := map[string]storedLoop{}
	for _, l := range got.Loops {
		byTopic[l.Topic] = l
		if !strings.Contains(items[0].Text+items[1].Text, l.EvidenceQuote) {
			t.Fatalf("stored evidence %q is not from the source", l.EvidenceQuote)
		}
	}
	wait := byTopic["pest control to call about roof bees"]
	if wait.Band != bandNudge || wait.EvidencePointer != "msg:m0" || wait.Importance != 90 {
		t.Fatalf("awaiting loop: %+v", wait)
	}
	prom := byTopic["send Marc the draft"]
	if prom.Band != bandNudge || prom.Importance < 60 || !prom.ExpectedBy.Equal(time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("dated promise must nudge at its date: %+v", prom)
	}
}

func TestParseChatLoopsBandsAndModelNumbersAreIgnored(t *testing.T) {
	items := []chatSweepItem{{MessageID: "m0", Text: "I will call my dentist sometime, and let's catch up with Sam sometime"}}
	created := time.Date(2026, 10, 13, 17, 0, 0, 0, time.UTC)
	undated := loopFor(0, loopKindOwnerPromise, "call the dentist", "I will call my dentist sometime", map[string]any{"signals": []string{}, "category": "health", "expected_by": map[string]any{"type": "none"}, "importance": 99})
	vague := loopFor(0, loopKindOwnerPromise, "catch up with Sam", "let's catch up with Sam sometime", map[string]any{"signals": []string{"vague_social"}, "category": "social", "expected_by": map[string]any{"type": "none"}})
	// "importance" is not an allowed field: the model cannot write its own score.
	got, err := parseChatLoops(jsonLoops(undated, vague), items, "bot", created, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if got.RejectedInvalid != 1 || got.RejectedBand != 1 || len(got.Loops) != 0 {
		t.Fatalf("model-authored importance must be refused: %+v", got)
	}
	delete(undated, "importance")
	got, _ = parseChatLoops(jsonLoops(undated, vague), items, "bot", created, time.UTC)
	if len(got.Loops) != 1 || got.Loops[0].Band != bandDigest || got.RejectedBand != 1 {
		t.Fatalf("undated promise is digest only and vague social is dropped: %+v", got)
	}
	if _, err = parseChatLoops("no array here", items, "bot", created, time.UTC); err == nil {
		t.Fatal("a reply with no JSON array is a failed sweep")
	}
	if _, err = parseChatLoops("[{broken", items, "bot", created, time.UTC); err == nil {
		t.Fatal("broken JSON is a failed sweep")
	}
	if got, err = parseChatLoops("[]", items, "bot", created, time.UTC); err != nil || len(got.Loops) != 0 {
		t.Fatal("empty array is a clean, empty sweep")
	}
}

func TestChatSweepReadsOnlyOwnerIngressMessagesAndExtractsLoops(t *testing.T) {
	var lastQuote string
	engine := &followupStubEngine{reply: func(_ int, req Request) (Result, error) {
		return Result{}, nil
	}}
	s, b := followupServer(t, engine)
	// Anything that is not the owner talking must never reach the model.
	if _, _, err := s.store.AddMessage(b.DMConversationID, "assistant", b.ID, "", "Bot says: I will email Marc tomorrow", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.store.AddMessage(b.DMConversationID, "user", "", "", "imported transcript line: I will pay the invoice", ""); err != nil {
		t.Fatal(err) // role=user but no host ingress record
	}
	lastQuote = "pest control will call me"
	owner := ownerSays(t, s, b, "Elyse says pest control will call me about the roof bees")

	// Capture what the model is shown.
	var shown []map[string]any
	engine.reply = func(_ int, req Request) (Result, error) {
		shown = shownItems(t, req)
		if !strings.Contains(req.System, "untrusted") || !strings.Contains(req.System, "Today is") {
			t.Fatalf("system prompt missing guardrails: %s", req.System)
		}
		return Result{Content: jsonLoops(loopFor(0, loopKindAwaitingOther, "pest control to call about roof bees", lastQuote, map[string]any{"signals": []string{"owner_asked", "third_party_committed"}})), InputTokens: 4000, OutputTokens: 300}, nil
	}
	now := time.Now().UTC()
	res, err := s.runChatSweep(context.Background(), b.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(shown) != 1 || !strings.Contains(shown[0]["user_message"].(string), "pest control will call me") {
		t.Fatalf("the model saw %v", shown)
	}
	if shown[0]["previous_assistant_message"] != "Bot says: I will email Marc tomorrow" {
		t.Fatalf("antecedent missing: %v", shown[0])
	}
	if res.LoopsCreated != 1 || res.ItemsSeen != 1 {
		t.Fatalf("result %+v", res)
	}
	views, err := s.store.followupLoopViews(10)
	if err != nil || len(views) != 1 {
		t.Fatalf("views %v %v", views, err)
	}
	v := views[0]
	if v.BotID != b.ID || v.Source != "chat" || v.Quote != lastQuote || v.State != "open" || v.Band != bandNudge || v.Importance < 60 {
		t.Fatalf("stored loop %+v", v)
	}
	var ptr string
	if err = s.store.db.QueryRow(`SELECT pointer FROM open_loop_evidence WHERE loop_id=?`, v.ID).Scan(&ptr); err != nil || ptr != "msg:"+owner.ID {
		t.Fatalf("evidence pointer %q %v", ptr, err)
	}
	// Spend is recorded under kind followup, beside (not inside) chat usage.
	var req2, chat int64
	var usd float64
	if err = s.store.db.QueryRow(`SELECT COALESCE(SUM(followup_requests),0),COALESCE(SUM(requests),0),COALESCE(SUM(followup_equivalent_usd),0) FROM usage_hourly`).Scan(&req2, &chat, &usd); err != nil || req2 != 1 || chat != 0 {
		t.Fatalf("usage rollup followup=%d chat=%d err=%v", req2, chat, err)
	}
	_ = usd
	var sweeps int
	if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM followup_sweeps WHERE source='chat' AND error='' AND input_tokens=4000`).Scan(&sweeps); err != nil || sweeps != 1 {
		t.Fatalf("sweep audit rows %d %v", sweeps, err)
	}
}

func TestChatSweepCursorIsIncremental(t *testing.T) {
	var seen [][]string
	engine := &followupStubEngine{reply: func(_ int, req Request) (Result, error) {
		var texts []string
		for _, it := range shownItems(t, req) {
			texts = append(texts, it["user_message"].(string))
		}
		seen = append(seen, texts)
		return Result{Content: "[]", InputTokens: 100, OutputTokens: 5}, nil
	}}
	s, b := followupServer(t, engine)
	now := time.Now().UTC()
	ownerSays(t, s, b, "first message")
	ownerSays(t, s, b, "second message")
	if _, err := s.runChatSweep(context.Background(), b.ID, now); err != nil {
		t.Fatal(err)
	}
	// Nothing new: no model call at all.
	res, err := s.runChatSweep(context.Background(), b.ID, now.Add(time.Hour))
	if err != nil || res.ModelCalled || res.Skipped != "no_new_messages" {
		t.Fatalf("idle sweep %+v %v", res, err)
	}
	if engine.callCount() != 1 {
		t.Fatalf("idle sweep called the model: %d", engine.callCount())
	}
	// Bot chatter alone advances the cursor without a model call.
	if _, _, err = s.store.AddMessage(b.DMConversationID, "assistant", b.ID, "", "ok", ""); err != nil {
		t.Fatal(err)
	}
	if res, err = s.runChatSweep(context.Background(), b.ID, now.Add(2*time.Hour)); err != nil || res.ModelCalled {
		t.Fatalf("assistant-only batch %+v %v", res, err)
	}
	ownerSays(t, s, b, "third message")
	if _, err = s.runChatSweep(context.Background(), b.ID, now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"first message", "second message"}, {"third message"}}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("messages processed %v, want %v", seen, want)
	}
}

func TestChatSweepFailureKeepsCursorThenGivesUpOnThePoisonBatch(t *testing.T) {
	engine := &followupStubEngine{reply: func(int, Request) (Result, error) { return Result{}, errors.New("provider down") }}
	s, b := followupServer(t, engine)
	ownerSays(t, s, b, "I will send the report")
	now := time.Now().UTC()
	for i := 1; i <= followupMaxFailures; i++ {
		res, err := s.runChatSweep(context.Background(), b.ID, now.Add(time.Duration(i)*time.Minute))
		if err == nil {
			t.Fatalf("attempt %d should fail", i)
		}
		cursor, _, _ := s.store.followupCursor(b.ID, followupSourceChat)
		if i < followupMaxFailures && cursor != 0 {
			t.Fatalf("a failed sweep moved the cursor to %d", cursor)
		}
		if i == followupMaxFailures && (cursor == 0 || res.Skipped != "failures_exceeded") {
			t.Fatalf("poison batch not skipped: cursor=%d res=%+v", cursor, res)
		}
	}
	calls := engine.callCount()
	if _, err := s.runChatSweep(context.Background(), b.ID, now.Add(time.Hour)); err != nil || engine.callCount() != calls {
		t.Fatalf("a skipped batch must not be retried: %v", err)
	}
	// Garbage output counts as a failure too.
	engine.reply = func(int, Request) (Result, error) { return Result{Content: "not json"}, nil }
	ownerSays(t, s, b, "another message")
	if _, err := s.runChatSweep(context.Background(), b.ID, now.Add(2*time.Hour)); err == nil {
		t.Fatal("unparseable reply must be a failed sweep")
	}
	if cursor, _, _ := s.store.followupCursor(b.ID, followupSourceChat); cursor == 0 {
		t.Fatal("cursor reset")
	}
}

func TestChatSweepShadowNeverPostsAnywhere(t *testing.T) {
	engine := &followupStubEngine{reply: func(_ int, req Request) (Result, error) {
		return Result{Content: jsonLoops(loopFor(0, loopKindOwnerReminder, "call the landlord", "remind me to call the landlord", map[string]any{"expected_by": map[string]any{"type": "next_week"}})), InputTokens: 10, OutputTokens: 10}, nil
	}}
	s, b := followupServer(t, engine)
	ownerSays(t, s, b, "remind me to call the landlord")
	count := func(table string) int {
		var n int
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := map[string]int{}
	for _, table := range []string{"messages", "runs", "events", "conversations", "schedules", "mail_drafts", "work_items", "questions"} {
		before[table] = count(table)
	}
	if _, err := s.runChatSweep(context.Background(), b.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for table, n := range before {
		if got := count(table); got != n {
			t.Fatalf("shadow sweep wrote to %s: %d -> %d", table, n, got)
		}
	}
	if n := count("open_loops"); n != 1 {
		t.Fatalf("loop not stored: %d", n)
	}
	var delivery string
	if err := s.store.db.QueryRow(`SELECT delivery FROM open_loops`).Scan(&delivery); err != nil || delivery != "shadow" {
		t.Fatalf("delivery %q", delivery)
	}
	// Turning delivery on is refused until it exists.
	if err := s.store.putFollowupBotSettings(followupBotSettings{BotID: b.ID, Mode: followupModeOn, SourceChat: true}); !errors.Is(err, errFollowupDeliveryUnavailable) {
		t.Fatalf("mode=on accepted: %v", err)
	}
}

func TestChatSweepDefaultsOffAndHonoursBudget(t *testing.T) {
	engine := &followupStubEngine{reply: func(int, Request) (Result, error) {
		return Result{Content: "[]", InputTokens: 1_000_000, OutputTokens: 0}, nil
	}}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: engine})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b := mustBot(t, s.store, "Coder")
	ownerSays(t, s, b, "I will fix the build")
	if cfg, _ := s.store.followupBotSettings(b.ID); cfg.Mode != followupModeOff {
		t.Fatalf("default mode %q", cfg.Mode)
	}
	if res, err := s.runChatSweep(context.Background(), b.ID, time.Now().UTC()); err != nil || res.ModelCalled || res.Skipped != "off" {
		t.Fatalf("off Bot was swept: %+v %v", res, err)
	}
	if err = s.store.putFollowupBotSettings(followupBotSettings{BotID: b.ID, Mode: followupModeShadow, SourceChat: true}); err != nil {
		t.Fatal(err)
	}
	if err = s.store.putFollowupAccount(func() followupAccountSettings {
		c := defaultFollowupAccountSettings()
		c.DailyBudgetUSD = 0.01
		return c
	}()); err != nil {
		t.Fatal(err)
	}
	// Seed spend over the cap: the model must not be called.
	if _, err = s.store.db.Exec(`INSERT INTO followup_sweeps(id,bot_id,source,started_at,items_seen,loops_created,model,input_tokens,output_tokens,equivalent_usd) VALUES('x',?,?,?,1,0,'m',1,1,0.02)`, b.ID, followupSourceChat, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.runChatSweep(context.Background(), b.ID, time.Now().UTC()); !errors.Is(err, errFollowupPaused) || engine.callCount() != 0 {
		t.Fatalf("budget not enforced: %v calls=%d", err, engine.callCount())
	}
}

func TestChatSweepMergesRepeatedLoopsAndKeepsHigherImportance(t *testing.T) {
	quote := "pest control will call me"
	engine := &followupStubEngine{reply: func(call int, req Request) (Result, error) {
		extra := map[string]any{"signals": []string{"third_party_committed"}}
		if call == 2 {
			extra = map[string]any{"signals": []string{"owner_asked", "third_party_committed"}}
		}
		return Result{Content: jsonLoops(loopFor(0, loopKindAwaitingOther, "Pest control to call about roof bees.", quote, extra)), InputTokens: 10, OutputTokens: 10}, nil
	}}
	s, b := followupServer(t, engine)
	ownerSays(t, s, b, "pest control will call me")
	now := time.Now().UTC()
	if _, err := s.runChatSweep(context.Background(), b.ID, now); err != nil {
		t.Fatal(err)
	}
	ownerSays(t, s, b, "again: pest control will call me")
	res, err := s.runChatSweep(context.Background(), b.ID, now.Add(time.Hour))
	if err != nil || res.Merged != 1 || res.LoopsCreated != 0 {
		t.Fatalf("second sweep %+v %v", res, err)
	}
	views, _ := s.store.followupLoopViews(10)
	if len(views) != 1 || views[0].Importance != 90 {
		t.Fatalf("merge must keep a single loop at the higher importance: %+v", views)
	}
	var evidence int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM open_loop_evidence`).Scan(&evidence)
	if evidence != 2 {
		t.Fatalf("evidence rows %d", evidence)
	}
}

func TestFollowupHTTPOverviewFeedbackAndSettings(t *testing.T) {
	engine := &followupStubEngine{reply: func(_ int, req Request) (Result, error) {
		return Result{Content: jsonLoops(loopFor(0, loopKindAwaitingOther, "contractor invoice", "invoice will arrive", map[string]any{"signals": []string{"owner_asked", "third_party_committed"}})), InputTokens: 10, OutputTokens: 10}, nil
	}}
	s, b := followupServer(t, engine)
	h := s.Handler()
	other := mustBot(t, s.store, "Coder")
	ownerSays(t, s, b, "the invoice will arrive Monday")
	if _, err := s.runChatSweep(context.Background(), b.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	status, out := primaryCall(t, h, http.MethodGet, "/api/followups", nil)
	if status != 200 {
		t.Fatalf("overview %d %v", status, out)
	}
	if out["primary_bot_id"] != b.ID || out["delivery_available"] != false {
		t.Fatalf("overview header %v", out)
	}
	bots := out["bots"].([]any)
	if len(bots) != 2 {
		t.Fatalf("bots %v", bots)
	}
	loops := out["loops"].([]any)
	if len(loops) != 1 {
		t.Fatalf("loops %v", loops)
	}
	id := loops[0].(map[string]any)["id"].(string)

	// Per-Bot mode: off/shadow only.
	if status, out = primaryCall(t, h, http.MethodPut, "/api/followups/bots/"+other.ID, map[string]any{"mode": "shadow"}); status != 200 {
		t.Fatalf("set shadow %d %v", status, out)
	}
	if status, out = primaryCall(t, h, http.MethodPut, "/api/followups/bots/"+other.ID, map[string]any{"mode": "on"}); status != 409 || errCode(out) != "followup_delivery_unavailable" {
		t.Fatalf("mode on: %d %v", status, out)
	}
	if status, _ = primaryCall(t, h, http.MethodPut, "/api/followups/bots/missing", map[string]any{"mode": "shadow"}); status != 404 {
		t.Fatalf("unknown Bot: %d", status)
	}
	// Account limits validate.
	if status, out = primaryCall(t, h, http.MethodPut, "/api/followups/account", map[string]any{"max_nudges_per_day": 2}); status != 200 || out["max_nudges_per_day"] != float64(2) {
		t.Fatalf("account %d %v", status, out)
	}
	for _, bad := range []map[string]any{{"max_nudges_per_day": 99}, {"quiet_start": "25:00"}, {"digest_weekday": "someday"}, {"daily_budget_usd": 0}, {"unknown": 1}} {
		if status, _ = primaryCall(t, h, http.MethodPut, "/api/followups/account", bad); status != 400 {
			t.Fatalf("bad account %v accepted: %d", bad, status)
		}
	}
	// Review buttons persist feedback; handled closes the loop silently.
	if status, _ = primaryCall(t, h, http.MethodPost, "/api/followups/loops/"+id+"/feedback", map[string]string{"verdict": "useful"}); status != 200 {
		t.Fatalf("feedback %d", status)
	}
	_, out = primaryCall(t, h, http.MethodGet, "/api/followups", nil)
	if v := out["loops"].([]any)[0].(map[string]any); v["verdict"] != "useful" || v["state"] != "open" {
		t.Fatalf("useful verdict not shown: %v", v)
	}
	if status, _ = primaryCall(t, h, http.MethodPost, "/api/followups/loops/"+id+"/feedback", map[string]string{"verdict": "handled"}); status != 200 {
		t.Fatalf("handled %d", status)
	}
	_, out = primaryCall(t, h, http.MethodGet, "/api/followups", nil)
	if v := out["loops"].([]any)[0].(map[string]any); v["verdict"] != "handled" || v["state"] != "handled_by_owner" {
		t.Fatalf("handled not applied: %v", v)
	}
	if status, _ = primaryCall(t, h, http.MethodPost, "/api/followups/loops/"+id+"/feedback", map[string]string{"verdict": "bogus"}); status != 400 {
		t.Fatalf("bogus verdict: %d", status)
	}
	if status, _ = primaryCall(t, h, http.MethodPost, "/api/followups/loops/nope/feedback", map[string]string{"verdict": "useful"}); status != 404 {
		t.Fatalf("unknown loop: %d", status)
	}
	var n int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM followup_feedback WHERE loop_id=?`, id).Scan(&n)
	if n != 2 {
		t.Fatalf("feedback rows %d", n)
	}
}

func TestDeletingABotRemovesItsFollowups(t *testing.T) {
	engine := &followupStubEngine{reply: func(_ int, req Request) (Result, error) {
		return Result{Content: jsonLoops(loopFor(0, loopKindAwaitingOther, "contractor invoice", "invoice will arrive", map[string]any{"signals": []string{"owner_asked", "third_party_committed"}})), InputTokens: 10, OutputTokens: 10}, nil
	}}
	s, b := followupServer(t, engine)
	keep := mustBot(t, s.store, "Keep")
	ownerSays(t, s, b, "the invoice will arrive Monday")
	if _, err := s.runChatSweep(context.Background(), b.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DeleteBotReassigning(b.ID, keep.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"open_loops", "open_loop_evidence", "followup_settings", "followup_cursors", "followup_sweeps", "followup_feedback"} {
		var n int
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s kept %d rows (%v)", table, n, err)
		}
	}
}

func TestFollowupMigrationIsIdempotent(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for i := 0; i < 3; i++ {
		if err = migrateFollowups(s.store.db); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"followup_account", "followup_settings", "followup_cursors", "open_loops", "open_loop_evidence", "followup_feedback", "followup_suppressions", "followup_sweeps"} {
		var n int
		if err = s.store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
}

func TestFollowupWorkerHonoursWindowAndCadence(t *testing.T) {
	engine := &followupStubEngine{reply: func(int, Request) (Result, error) { return Result{Content: "[]", InputTokens: 1, OutputTokens: 1}, nil }}
	s, b := followupServer(t, engine)
	w := &followupWorker{server: s, next: map[string]time.Time{}}
	ownerSays(t, s, b, "I will send the deck")
	night := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	w.tick(context.Background(), night)
	if engine.callCount() != 0 {
		t.Fatal("swept outside 08:00-21:00")
	}
	noon := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	w.tick(context.Background(), noon)
	if engine.callCount() != 1 {
		t.Fatalf("calls %d", engine.callCount())
	}
	ownerSays(t, s, b, "and the memo")
	w.tick(context.Background(), noon.Add(30*time.Minute))
	if engine.callCount() != 1 {
		t.Fatal("cadence: next sweep is 90 minutes out")
	}
	w.tick(context.Background(), noon.Add(91*time.Minute))
	if engine.callCount() != 2 {
		t.Fatalf("calls %d", engine.callCount())
	}
	// A Bot switched off is never swept.
	if err := s.store.putFollowupBotSettings(followupBotSettings{BotID: b.ID, Mode: followupModeOff, SourceChat: true}); err != nil {
		t.Fatal(err)
	}
	ownerSays(t, s, b, "one more")
	w.tick(context.Background(), noon.Add(4*time.Hour))
	if engine.callCount() != 2 {
		t.Fatal("off Bot swept")
	}
}
