package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type autoItemBody struct {
	Key       string `json:"key"`
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Default   any    `json:"default"`
	Value     any    `json:"value"`
	Effective any    `json:"effective"`
	Source    string `json:"source"`
	Env       string `json:"env"`
	Locked    bool   `json:"locked"`
	ReadOnly  bool   `json:"read_only"`
}

type autoSettingsBody struct {
	Items []autoItemBody `json:"items"`
}

func autoGet(t *testing.T, s *Server) autoSettingsBody {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/automation-settings", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET automation-settings=%d %s", rec.Code, rec.Body.String())
	}
	var body autoSettingsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func (b autoSettingsBody) find(t *testing.T, key string) autoItemBody {
	t.Helper()
	for _, item := range b.Items {
		if item.Key == key {
			return item
		}
	}
	t.Fatalf("setting %s missing", key)
	return autoItemBody{}
}

func autoPutCode(t *testing.T, s *Server, values string) int {
	t.Helper()
	return providerRequest(t, s, http.MethodPut, "/api/automation-settings", `{"values":`+values+`}`).Code
}

// --- model resolution -------------------------------------------------------

func TestConversationRunsUseTheConversationDefault(t *testing.T) {
	anthropic := newFakeAnthropic(t, "ok")
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	ctx := context.Background()
	if s.defaultModel != defaultConversationModel || defaultConversationModel != "claude-sonnet-5-5" {
		t.Fatalf("a fresh workspace defaults to Sonnet, got %q", s.defaultModel)
	}
	if got := s.effectiveDefaultModel(ctx); got != "claude-sonnet-5-5" {
		t.Fatalf("effective default=%s", got)
	}
	// DM, group and scheduled runs all resolve through the Bot: a following Bot
	// gets the conversation default whatever the run kind.
	for _, kind := range []string{"", runKindSchedule, runKindGroupChat, runKindTeam} {
		model, _ := s.resolveBotModel(ctx, followGlobalModel, Bot{Model: followGlobalModel})
		if model != "claude-sonnet-5-5" {
			t.Fatalf("run kind %q runs on %s", kind, model)
		}
	}
}

// End to end: the request a following Bot's run sends names the conversation
// default, and changing the default applies to the next run with no restart.
func TestFollowingBotRunSendsTheConversationModel(t *testing.T) {
	var got string
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
		got = req.Model
		return runtime.Result{Content: "ok"}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, _ := s.store.CreateBot("follower", "", followGlobalModel)
	c, _ := s.store.GetConversation(b.DMConversationID)
	send := func(id string) {
		_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", id)
		if err != nil {
			t.Fatal(err)
		}
		s.enqueue(c, run)
		waitRunTerminal(t, s, run.ID)
	}
	send("conv-1")
	if got != defaultConversationModel {
		t.Fatalf("run used %q, want the conversation default %q", got, defaultConversationModel)
	}
	if err := s.saveModelDefaults(modelSettings{Model: "synthetic-next-model", ReasoningEffort: "medium"}); err != nil {
		t.Fatal(err)
	}
	send("conv-2")
	if got != "synthetic-next-model" {
		t.Fatalf("a changed default did not reach the next run: %q", got)
	}
}

func TestBackgroundHelpersUseTheBackgroundDefault(t *testing.T) {
	anthropic := newFakeAnthropic(t, "ok")
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	ctx := context.Background()
	for _, purpose := range []string{backgroundTriage, backgroundReview} {
		if got := s.backgroundModel(purpose); got != "claude-haiku-5-5" {
			t.Fatalf("%s runs on %s", purpose, got)
		}
	}
	// Summaries, follow-up sweeps and group routing share the triage helper.
	if s.triageModelName() != "claude-haiku-5-5" {
		t.Fatalf("triage helper runs on %s", s.triageModelName())
	}
	// A stored background model replaces the default; the conversation model is untouched.
	if code := autoPutCode(t, s, `{"background_model":"claude-opus-5-5"}`); code != http.StatusOK {
		t.Fatalf("put background model=%d", code)
	}
	if got := s.backgroundModel(backgroundTriage); got != "claude-opus-5-5" {
		t.Fatalf("stored background model not used: %s", got)
	}
	if got := s.effectiveDefaultModel(ctx); got != "claude-sonnet-5-5" {
		t.Fatalf("background choice moved the conversation model to %s", got)
	}
	// Null returns to automatic.
	if code := autoPutCode(t, s, `{"background_model":null}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if got := s.backgroundModel(backgroundTriage); got != "claude-haiku-5-5" {
		t.Fatalf("reset background model=%s", got)
	}
}

func TestBotModelOverrideBeatsBothDefaults(t *testing.T) {
	anthropic := newFakeAnthropic(t, "ok")
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	ctx := context.Background()
	if model, _ := s.resolveBotModel(ctx, followGlobalModel, Bot{Model: "claude-opus-5-5"}); model != "claude-opus-5-5" {
		t.Fatalf("the Bot's own model lost to the default: %s", model)
	}
	if got := s.backgroundModel(backgroundTriage); got != "claude-haiku-5-5" {
		t.Fatalf("a Bot's model must not move background work: %s", got)
	}
}

func TestBackgroundDefaultIsTheConversationModelOffAnthropic(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	if rec := providerRequest(t, s, http.MethodPut, "/api/model-settings", `{"model":"gpt-6-luna"}`); rec.Code != http.StatusOK {
		t.Fatalf("model settings=%d %s", rec.Code, rec.Body.String())
	}
	for _, purpose := range []string{backgroundTriage, backgroundReview} {
		if got := s.backgroundModel(purpose); got != "gpt-6-luna" {
			t.Fatalf("%s runs on %s, want the conversation model", purpose, got)
		}
	}
	// The page shows what automatic means right now.
	if got := autoGet(t, s).find(t, autoBackgroundModel).Default; got != "gpt-6-luna" {
		t.Fatalf("background default shown as %v", got)
	}
}

func TestBackgroundModelEnvironmentWins(t *testing.T) {
	anthropic := newFakeAnthropic(t, "ok")
	s := providerServer(t, nil, anthropic)
	putProviderKeyOK(t, s, "anthropic", syntheticAnthropicKey)
	if code := autoPutCode(t, s, `{"background_model":"claude-opus-5-5"}`); code != http.StatusOK {
		t.Fatal(code)
	}
	t.Setenv("TOFI_BACKGROUND_MODEL", "claude-haiku-4-5")
	if got := s.backgroundModel(backgroundReview); got != "claude-haiku-4-5" {
		t.Fatalf("env override lost: %s", got)
	}
	item := autoGet(t, s).find(t, autoBackgroundModel)
	if !item.Locked || item.Source != "env" || item.Env != "TOFI_BACKGROUND_MODEL" || item.Value != "claude-haiku-4-5" {
		t.Fatalf("env override not reported: %+v", item)
	}
	if code := autoPutCode(t, s, `{"background_model":null}`); code != http.StatusConflict {
		t.Fatalf("writing an env-locked setting=%d, want 409", code)
	}
}

func TestCodexOnlyWorkspaceFallsBackToCodexModel(t *testing.T) {
	s := providerServer(t, nil, nil)
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", 4102444800000); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.modelDefaults(); got != codexConversationFallback {
		t.Fatalf("Sonnet default is unusable without an Anthropic key; got %s", got)
	}
	if got := s.backgroundModel(backgroundTriage); got != codexDefaultModel {
		t.Fatalf("background=%s", got)
	}
	// With a live catalog the newest Sol wins; background work stays on the small model.
	s.rememberProviderCatalog(providerCodex, []ModelOption{{ID: "codex-gpt-5.6-sol"}, {ID: "codex-gpt-6.1-sol"}, {ID: "codex-gpt-6-sol"}, {ID: "codex-gpt-6.2-luna"}, {ID: "codex-gpt-6-astra"}})
	if got, _ := s.modelDefaults(); got != "codex-gpt-6.1-sol" {
		t.Fatalf("conversation default with catalog = %s", got)
	}
	if got := s.backgroundModel(backgroundTriage); got != codexDefaultModel {
		t.Fatalf("background with catalog = %s", got)
	}
}

// --- settings API -----------------------------------------------------------

func TestAutomationSettingsInventoryAndDefaults(t *testing.T) {
	s := providerServer(t, nil, nil)
	body := autoGet(t, s)
	want := []string{autoConversationModel, autoBackgroundModel, autoStructuredOutput, autoForceSayHaikuDM, autoWebSearch, autoWebFetchLimit, autoGroupExchangeGists, autoOwnRepliesVerbatim, autoTaskRetentionDays, autoCompletionReview, autoAnswerGate, autoMCPReview, autoRememberedApprovalHrs, autoProviderRetries, autoScheduleReceiptReview, autoDesktopIdle, autoBrowserEngine, autoGroupTriage, autoGroupTriageModel, autoGroupFollowUp, autoCommandCheckMinutes, autoTaskStallRounds, autoTaskTreeIdleLimit, autoWidgetArtifacts, autoWidgetComputer, autoSettingsChangeLimit}
	if len(body.Items) != len(want) {
		t.Fatalf("%d items, want %d", len(body.Items), len(want))
	}
	for i, key := range want {
		if body.Items[i].Key != key {
			t.Fatalf("item %d is %s, want %s", i, body.Items[i].Key, key)
		}
	}
	defaults := map[string]any{autoConversationModel: "claude-sonnet-5-5", autoStructuredOutput: true, autoForceSayHaikuDM: true, autoWebSearch: true, autoGroupExchangeGists: true, autoGroupTriage: true, autoGroupFollowUp: true, autoWidgetArtifacts: true, autoWidgetComputer: true, autoOwnRepliesVerbatim: float64(3), autoTaskRetentionDays: float64(30), autoCommandCheckMinutes: float64(60), autoTaskStallRounds: float64(3), autoTaskTreeIdleLimit: float64(30), autoCompletionReview: true, autoAnswerGate: float64(2), autoMCPReview: "off", autoBrowserEngine: "classic", autoRememberedApprovalHrs: float64(24), autoProviderRetries: float64(8), autoScheduleReceiptReview: true}
	for key, def := range defaults {
		item := body.find(t, key)
		if item.Default != def || item.Value != def || item.Source != "default" {
			t.Fatalf("%s: default=%v value=%v source=%s, want %v", key, item.Default, item.Value, item.Source, def)
		}
	}
	if item := body.find(t, autoDesktopIdle); !item.ReadOnly {
		t.Fatalf("desktop idle is owned by the computer service: %+v", item)
	}
}

func TestAutomationSettingsReadWriteEachItem(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	changes := []struct {
		key, json string
		value     any
		live      func() bool
	}{
		{autoStructuredOutput, "false", false, func() bool { return !s.store.structuredOutputOn() }},
		{autoForceSayHaikuDM, "false", false, func() bool { return !s.forceSayHaikuDMEnabled() }},
		{autoWebSearch, "false", false, func() bool { return !s.webSearchEnabled() && s.searchUses("claude-sonnet-5-5", nil) == 0 }},
		{autoGroupExchangeGists, "false", false, func() bool { return !s.groupExchangeGistsEnabled() }},
		{autoOwnRepliesVerbatim, "7", float64(7), func() bool { return s.ownRepliesVerbatim() == 7 }},
		{autoCompletionReview, "false", false, func() bool { return !s.completionReviewEnabled() }},
		{autoAnswerGate, "4", float64(4), func() bool { return s.answerGateRounds() == 4 }},
		{autoMCPReview, `"shadow"`, "shadow", func() bool { x, _ := s.store.getAutoReviewSettings(); return x.Mode == "shadow" }},
		{autoRememberedApprovalHrs, "48", float64(48), func() bool { return s.rememberedApprovalWindow().Hours() == 48 }},
		{autoProviderRetries, "2", float64(2), func() bool { return s.providerRetries() == 2 }},
		{autoScheduleReceiptReview, "false", false, func() bool { return !s.scheduleReceiptReviewEnabled() }},
		{autoGroupTriage, "false", false, func() bool { return !s.groupTriageEnabled() }},
		{autoGroupFollowUp, "false", false, func() bool { return !s.groupFollowUpEnabled() }},
		{autoWidgetArtifacts, "false", false, func() bool { return !s.autoBool(autoWidgetArtifacts) }},
		{autoWidgetComputer, "false", false, func() bool { return !s.autoBool(autoWidgetComputer) }},
		{autoTaskStallRounds, "5", float64(5), func() bool { return autoIntTx(s.store.db, autoTaskStallRounds) == 5 }},
		{autoTaskTreeIdleLimit, "120", float64(120), func() bool { return autoIntTx(s.store.db, autoTaskTreeIdleLimit) == 120 }},
		{autoGroupTriageModel, `"gpt-5-mini"`, "gpt-5-mini", func() bool { return s.groupTriageModel() == "gpt-5-mini" }},
		{autoBackgroundModel, `"gpt-5-mini"`, "gpt-5-mini", func() bool { return s.backgroundModel(backgroundTriage) == "gpt-5-mini" }},
		{autoConversationModel, `"gpt-6-luna"`, "gpt-6-luna", func() bool { m, _ := s.modelDefaults(); return m == "gpt-6-luna" }},
	}
	for _, c := range changes {
		if code := autoPutCode(t, s, `{"`+c.key+`":`+c.json+`}`); code != http.StatusOK {
			t.Fatalf("put %s=%d", c.key, code)
		}
		item := autoGet(t, s).find(t, c.key)
		if item.Value != c.value || item.Source != "stored" {
			t.Fatalf("%s reads back %v (%s), want %v stored", c.key, item.Value, item.Source, c.value)
		}
		if !c.live() {
			t.Fatalf("%s did not take effect without a restart", c.key)
		}
	}
	// Out-of-range values are refused and leave the stored value alone.
	if code := autoPutCode(t, s, `{"`+autoTaskStallRounds+`":0}`); code == http.StatusOK {
		t.Fatalf("a stall threshold of 0 was accepted")
	}
	if code := autoPutCode(t, s, `{"`+autoTaskTreeIdleLimit+`":5}`); code == http.StatusOK {
		t.Fatalf("a tree limit of 5 was accepted")
	}
	if autoIntTx(s.store.db, autoTaskStallRounds) != 5 || autoIntTx(s.store.db, autoTaskTreeIdleLimit) != 120 {
		t.Fatalf("a refused change altered the stored value")
	}
	// Linked items agree with the older surfaces.
	if rec := providerRequest(t, s, http.MethodGet, "/api/preferences", ""); !strings.Contains(rec.Body.String(), `"structured_output":false`) {
		t.Fatalf("preferences disagree: %s", rec.Body.String())
	}
	if rec := providerRequest(t, s, http.MethodGet, "/api/auto-review-settings", ""); !strings.Contains(rec.Body.String(), `"mode":"shadow"`) {
		t.Fatalf("auto-review settings disagree: %s", rec.Body.String())
	}
	if rec := providerRequest(t, s, http.MethodGet, "/api/model-settings", ""); !strings.Contains(rec.Body.String(), `"model":"gpt-6-luna"`) {
		t.Fatalf("model settings disagree: %s", rec.Body.String())
	}
	// Everything resets with null.
	for _, c := range changes {
		if c.key == autoConversationModel {
			continue // would need the unavailable Sonnet default
		}
		if code := autoPutCode(t, s, `{"`+c.key+`":null}`); code != http.StatusOK {
			t.Fatalf("reset %s=%d", c.key, code)
		}
		if item := autoGet(t, s).find(t, c.key); item.Source != "default" {
			t.Fatalf("%s still %s after reset", c.key, item.Source)
		}
	}
}

func TestAutomationSettingsSurviveRestartOfTheCache(t *testing.T) {
	s := providerServer(t, nil, nil)
	if code := autoPutCode(t, s, `{"provider_retries":1}`); code != http.StatusOK {
		t.Fatal(code)
	}
	s.automation.mu.Lock()
	s.automation.loaded, s.automation.values = false, nil // a new process reads the table again
	s.automation.mu.Unlock()
	if s.providerRetries() != 1 {
		t.Fatalf("stored value lost, retries=%d", s.providerRetries())
	}
}

func TestAutomationSettingsValidation(t *testing.T) {
	s := providerServer(t, nil, nil)
	for name, body := range map[string]string{
		"unknown key":           `{"nope":true}`,
		"bool given a string":   `{"web_search":"yes"}`,
		"int given a string":    `{"provider_retries":"3"}`,
		"int above the range":   `{"provider_retries":11}`,
		"int below the range":   `{"own_replies_verbatim":0}`,
		"int with a fraction":   `{"provider_retries":2.5}`,
		"enum outside options":  `{"mcp_auto_review":"always"}`,
		"unknown model":         `{"background_model":"gpt-nonexistent"}`,
		"empty conversation":    `{"conversation_model":""}`,
		"read-only desktop key": `{"desktop_idle_seconds":60}`,
	} {
		if code := autoPutCode(t, s, body); code != http.StatusBadRequest {
			t.Errorf("%s: %s -> %d, want 400", name, body, code)
		}
	}
	if rec := providerRequest(t, s, http.MethodPut, "/api/automation-settings", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body=%d", rec.Code)
	}
	if rec := providerRequest(t, s, http.MethodPost, "/api/automation-settings", `{}`); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST=%d", rec.Code)
	}
	// A rejected request stores nothing, even next to a valid change.
	if code := autoPutCode(t, s, `{"web_search":false,"provider_retries":99}`); code != http.StatusBadRequest {
		t.Fatal(code)
	}
	if !s.webSearchEnabled() {
		t.Fatal("a partly invalid request was partly applied")
	}
}

func TestAutomationSettingsEnvironmentOverridesWin(t *testing.T) {
	s := providerServer(t, nil, nil)
	if code := autoPutCode(t, s, `{"web_search":true,"force_say_haiku_dm":true,"provider_retries":4}`); code != http.StatusOK {
		t.Fatal(code)
	}
	t.Setenv("TOFI_WEB_SEARCH", "off")
	t.Setenv("TOFI_FORCE_SAY", "off")
	t.Setenv("TOFI_PROVIDER_RETRIES", "1")
	if s.webSearchEnabled() || s.forceSayHaikuDMEnabled() || s.providerRetries() != 1 {
		t.Fatal("stored values beat the environment")
	}
	body := autoGet(t, s)
	for key, env := range map[string]string{autoWebSearch: "TOFI_WEB_SEARCH", autoForceSayHaikuDM: "TOFI_FORCE_SAY", autoProviderRetries: "TOFI_PROVIDER_RETRIES"} {
		item := body.find(t, key)
		if !item.Locked || item.Source != "env" || item.Env != env {
			t.Errorf("%s: %+v", key, item)
		}
		if code := autoPutCode(t, s, `{"`+key+`":null}`); code != http.StatusConflict {
			t.Errorf("writing %s under an env override=%d, want 409", key, code)
		}
	}
	if body.find(t, autoProviderRetries).Value != float64(1) {
		t.Fatal("the page must show the value in force")
	}
	// An env value that is not valid is ignored rather than guessed at.
	t.Setenv("TOFI_PROVIDER_RETRIES", "lots")
	if s.providerRetries() != 4 {
		t.Fatalf("invalid env value was used: %d", s.providerRetries())
	}
	// "on" wins over a stored off.
	t.Setenv("TOFI_WEB_SEARCH", "on")
	if code := autoPutCode(t, s, `{"web_search":false}`); code != http.StatusConflict {
		t.Fatal(code)
	}
	if !s.webSearchEnabled() {
		t.Fatal("env on lost")
	}
}

// --- behaviours that now read the settings ------------------------------------

func TestGroupExchangeGistsFollowTheSetting(t *testing.T) {
	s := providerServer(t, nil, nil)
	c := Conversation{Kind: "group", UserVisible: true}
	r := Run{Kind: runKindGroupChat, TriggerMessageID: "m1"}
	if !s.groupExchangeGistsApply(c, r) {
		t.Fatal("closed-exchange gists are on by default")
	}
	if code := autoPutCode(t, s, `{"group_exchange_gists":false}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if s.groupExchangeGistsApply(c, r) {
		t.Fatal("gists still applied after switching them off")
	}
}

func TestProviderRetriesZeroStaysZero(t *testing.T) {
	s := providerServer(t, nil, nil)
	if got := s.providerRetries(); got != 8 {
		t.Fatalf("default retries=%d", got)
	}
	if code := autoPutCode(t, s, `{"provider_retries":0}`); code != http.StatusOK {
		t.Fatal(code)
	}
	if got := s.providerRetries(); got != 0 {
		t.Fatalf("zero retries must stay zero, got %d", got)
	}
}

// The settings reach the request a run sends: forced tool use, server-side web
// search and the completion review are all decided per run from the stored
// values, so a change applies to the next run.
func TestRunRequestFollowsAutomationSettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		put   string
		force bool
		web   bool
		final bool
	}{
		{"defaults", ``, true, true, true},
		{"everything off", `{"force_say_haiku_dm":false,"web_search":false,"completion_review":false}`, false, false, false},
		{"only web search off", `{"web_search":false}`, true, false, true},
		{"only completion review off", `{"completion_review":false}`, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got runtime.Request
			s, err := NewServer(Config{DataDir: t.TempDir(), Engine: dmFuncEngine(func(ctx context.Context, req runtime.Request) (runtime.Result, error) {
				got = req
				return runtime.Result{Content: "ok"}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if tc.put != "" {
				if code := autoPutCode(t, s, tc.put); code != http.StatusOK {
					t.Fatalf("put=%d", code)
				}
			}
			b, _ := s.store.CreateBot("settings fixture", "", "claude-haiku-5-5")
			c, _ := s.store.GetConversation(b.DMConversationID)
			_, run, _, err := s.store.AddUserRun(c.ID, b.ID, "hello", "auto-"+tc.name)
			if err != nil {
				t.Fatal(err)
			}
			s.enqueue(c, run)
			waitRunTerminal(t, s, run.ID)
			if got.ForceToolUse != tc.force || (got.WebSearchMaxUses > 0) != tc.web || got.BeforeFinalResponse == nil {
				// A structured run always carries the final hook (say arguments written
				// as text are recovered whatever the review setting); whether the
				// completion review itself runs is tested through completionReviewEnabled.
				t.Fatalf("force=%v web=%d final=%v, want force=%v web=%v and a final hook", got.ForceToolUse, got.WebSearchMaxUses, got.BeforeFinalResponse != nil, tc.force, tc.web)
			}
		})
	}
}

func TestScheduledReceiptReviewFollowsTheSetting(t *testing.T) {
	for _, on := range []bool{true, false} {
		s, bot, conversation := scheduleTestStore(t)
		schedule, err := s.CreateSchedule(conversation.ID, bot.ID, ScheduleSpec{Content: "synthetic review fixture", Kind: scheduleOnce, RunAt: time.Now().Add(time.Hour).Format(time.RFC3339), Timezone: "UTC"})
		if err != nil {
			t.Fatal(err)
		}
		makeDue(t, s, schedule.ID, time.Now().Add(-time.Minute))
		runs, err := s.ClaimDueSchedules(time.Now())
		if err != nil || len(runs) != 1 {
			t.Fatalf("claim=%+v err=%v", runs, err)
		}
		var hasReview bool
		engine := scheduleResultEngine(func(ctx context.Context, req Request) (Result, error) {
			hasReview = req.BeforeFinalResponse != nil
			return (scheduleTestEngine{}).Run(ctx, req)
		})
		server := &Server{store: s, engine: engine, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
		if !on {
			off := "false"
			if err := server.saveAutomationValue(autoScheduleReceiptReview, &off); err != nil {
				t.Fatal(err)
			}
		}
		server.execute(conversation, runs[0])
		if hasReview != on {
			t.Fatalf("setting on=%v but the scheduled run's receipt review present=%v", on, hasReview)
		}
		s.Close()
	}
}

func TestOwnReplyGistWindowFollowsTheSetting(t *testing.T) {
	for _, tc := range []struct{ keep, gisted int }{{3, 3}, {5, 1}, {1, 5}} {
		s, err := OpenStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		b, _ := s.CreateBot("A", "", "m")
		c, _ := s.GetConversation(b.DMConversationID)
		for i := 1; i <= 6; i++ {
			if _, _, err := s.AddMessage(c.ID, "user", "", "", fmt.Sprintf("synthetic question %d", i), ""); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.AddMessage(c.ID, "assistant", b.ID, "", fmt.Sprintf("Synthetic answer number %d is here. It ends with a routine closing offer.", i), ""); err != nil {
				t.Fatal(err)
			}
		}
		server := &Server{store: s, convMu: map[string]*sync.Mutex{}, runs: map[string]context.CancelFunc{}}
		v := strconv.Itoa(tc.keep)
		if err := server.saveAutomationValue(autoOwnRepliesVerbatim, &v); err != nil {
			t.Fatal(err)
		}
		msgs, _ := server.buildContextPartsWith(c, Run{ID: "r", ConversationID: c.ID, BotID: b.ID}, b, nil)
		gisted := 0
		for _, m := range msgs {
			if m.Role == "assistant" && strings.HasPrefix(m.Content, "(earlier reply, gist) ") {
				gisted++
			}
		}
		if gisted != tc.gisted {
			t.Fatalf("keep %d replies verbatim: %d gisted, want %d", tc.keep, gisted, tc.gisted)
		}
		s.Close()
	}
}

func TestRememberedApprovalWindowFollowsTheSetting(t *testing.T) {
	f := newAutoReviewFixture(t)
	two := "2"
	if err := f.s.saveAutomationValue(autoRememberedApprovalHrs, &two); err != nil {
		t.Fatal(err)
	}
	approveAndRemember(t, f)
	grants, err := f.s.store.listRememberedApprovals(f.c.ID)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants=%v err=%v", grants, err)
	}
	expires, err := time.Parse(time.RFC3339Nano, grants[0].ExpiresAt)
	if err != nil || expires.After(time.Now().Add(2*time.Hour+time.Minute)) || expires.Before(time.Now().Add(2*time.Hour-time.Minute)) {
		t.Fatalf("a grant made under a 2 hour setting expires at %s", grants[0].ExpiresAt)
	}
}

// The server's own engine reads the retry setting on every request.
func TestServerEngineRetriesFollowTheSetting(t *testing.T) {
	var hits atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	s := providerServer(t, nil, nil)
	s.providerEndpoints[providerOpenAI] = failing.URL + "/v1"
	s.envProviderKeys[providerOpenAI] = syntheticOpenAIKey
	if code := autoPutCode(t, s, `{"provider_retries":0}`); code != http.StatusOK {
		t.Fatal(code)
	}
	_, err := s.engine.Run(context.Background(), runtime.Request{RunID: "retry-setting", BotID: "bot", Model: "gpt-6-luna", Messages: []runtime.Message{{Role: "user", Content: "hi"}}})
	if err == nil {
		t.Fatal("a failing provider must fail the run")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("retries=0 made %d requests, want 1", got)
	}
}
