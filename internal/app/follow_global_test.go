package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func followGlobalBotRequest(t *testing.T, s *Server, method, path, body string, want int) Bot {
	t.Helper()
	rec := httptest.NewRecorder()
	s.bots(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	if rec.Code != want {
		t.Fatalf("%s %s status=%d body=%s", method, path, rec.Code, rec.Body.String())
	}
	var b Bot
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBotsFollowGlobalModelByDefault(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "global-model", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := httptest.NewRecorder()
	s.modelSettings(rec, httptest.NewRequest(http.MethodPut, "/api/model-settings", strings.NewReader(`{"model":"global-model","reasoning_effort":"high"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	// The global setting itself must name a concrete model.
	rec = httptest.NewRecorder()
	s.modelSettings(rec, httptest.NewRequest(http.MethodPut, "/api/model-settings", strings.NewReader(`{"model":"default"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("global model accepted the follow sentinel: %d", rec.Code)
	}

	created := followGlobalBotRequest(t, s, http.MethodPost, "/api/bots", `{"name":"follower"}`, http.StatusCreated)
	if created.Model != followGlobalModel || created.ReasoningEffort != followGlobalModel {
		t.Fatalf("new Bot copied the defaults instead of following: %q/%q", created.Model, created.ReasoningEffort)
	}
	if created.EffectiveModel != "global-model" || created.EffectiveReasoningEffort != "high" {
		t.Fatalf("effective=%q/%q", created.EffectiveModel, created.EffectiveReasoningEffort)
	}
	stored, err := s.store.GetBot(created.ID)
	if err != nil || stored.Model != followGlobalModel || stored.ReasoningEffort != followGlobalModel {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}

	pinned := followGlobalBotRequest(t, s, http.MethodPost, "/api/bots", `{"name":"pinned","model":"pinned-model","reasoning_effort":"low"}`, http.StatusCreated)
	if pinned.Model != "pinned-model" || pinned.ReasoningEffort != "low" || pinned.EffectiveModel != "pinned-model" || pinned.EffectiveReasoningEffort != "low" {
		t.Fatalf("explicit model did not pin: %+v", pinned)
	}
	// An explicit model with no effort pins the model and uses its default.
	pinnedDefault := followGlobalBotRequest(t, s, http.MethodPost, "/api/bots", `{"name":"pinned2","model":"pinned-model"}`, http.StatusCreated)
	if pinnedDefault.Model != "pinned-model" || pinnedDefault.ReasoningEffort != followGlobalModel {
		t.Fatalf("pinned Bot effort=%+v", pinnedDefault)
	}

	onboarding := followGlobalBotRequest(t, s, http.MethodPost, "/api/bots", `{"onboarding":true,"client_creation_id":"`+uuid.NewString()+`"}`, http.StatusCreated)
	if onboarding.Model != followGlobalModel || onboarding.ReasoningEffort != followGlobalModel {
		t.Fatalf("onboarding Bot does not follow: %+v", onboarding)
	}

	// Choosing "follow global" on a pinned Bot follows the effort too.
	back := followGlobalBotRequest(t, s, http.MethodPatch, "/api/bots/"+pinned.ID, `{"model":"default"}`, http.StatusOK)
	if back.Model != followGlobalModel || back.ReasoningEffort != followGlobalModel || back.EffectiveModel != "global-model" || back.EffectiveReasoningEffort != "high" {
		t.Fatalf("PATCH follow=%+v", back)
	}
	// A pinned model with effort "default" is valid.
	repin := followGlobalBotRequest(t, s, http.MethodPatch, "/api/bots/"+pinned.ID, `{"model":"pinned-model","reasoning_effort":"default"}`, http.StatusOK)
	if repin.Model != "pinned-model" || repin.ReasoningEffort != followGlobalModel {
		t.Fatalf("PATCH pin=%+v", repin)
	}

	// The list keeps raw values and adds the effective pair.
	rec = httptest.NewRecorder()
	s.bots(rec, httptest.NewRequest(http.MethodGet, "/api/bots", nil))
	var list struct {
		Bots []Bot `json:"bots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range list.Bots {
		if b.ID == created.ID {
			found = b.Model == followGlobalModel && b.EffectiveModel == "global-model" && b.EffectiveReasoningEffort == "high"
		}
	}
	if !found {
		t.Fatalf("list=%s", rec.Body.String())
	}
}

func TestFollowGlobalValidationWithCatalog(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "codex-a", Provider: "openai_codex"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.modelCatalog = []ModelOption{
		{ID: "codex-a", Name: "A", ReasoningEfforts: []string{"low", "medium"}, DefaultReasoning: "low"},
		{ID: "codex-b", Name: "B", ReasoningEfforts: []string{"medium", "high"}, DefaultReasoning: "high"},
	}
	s.modelCatalogAt, s.modelCatalogSource = time.Now(), "live"
	ctx := context.Background()
	for _, tc := range []struct {
		model, effort string
		ok            bool
	}{
		{followGlobalModel, followGlobalModel, true},
		{followGlobalModel, "low", true},
		{followGlobalModel, "high", false}, // checked against the current global model
		{"codex-b", followGlobalModel, true},
		{"codex-b", "high", true},
		{"codex-b", "low", false},
		{"invented", followGlobalModel, false},
	} {
		if err := s.validateModelChoice(ctx, tc.model, tc.effort); (err == nil) != tc.ok {
			t.Errorf("validateModelChoice(%q,%q) err=%v want ok=%v", tc.model, tc.effort, err, tc.ok)
		}
	}
	if err := s.validateModelID(ctx, followGlobalModel); err != nil {
		t.Fatalf("validateModelID(default)=%v", err)
	}
}

func TestFollowGlobalResolution(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "codex-a", Provider: "openai_codex"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.modelCatalog = []ModelOption{
		{ID: "codex-a", Name: "A", ReasoningEfforts: []string{"low", "medium", "high"}, DefaultReasoning: "low"},
		{ID: "codex-b", Name: "B", ReasoningEfforts: []string{"medium", "high"}, DefaultReasoning: "high"},
	}
	s.modelCatalogAt, s.modelCatalogSource = time.Now(), "live"
	s.mu.Lock()
	s.defaultReasoning = "medium"
	s.mu.Unlock()
	ctx := context.Background()
	for _, tc := range []struct {
		name               string
		runModel           string
		bot                Bot
		wantModel, wantEff string
	}{
		{"follow global", "", Bot{Model: followGlobalModel, ReasoningEffort: followGlobalModel}, "codex-a", "medium"},
		{"follow global via run sentinel", followGlobalModel, Bot{Model: followGlobalModel, ReasoningEffort: followGlobalModel}, "codex-a", "medium"},
		{"follow global explicit effort", "", Bot{Model: followGlobalModel, ReasoningEffort: "high"}, "codex-a", "high"},
		{"pinned model default effort", "", Bot{Model: "codex-b", ReasoningEffort: followGlobalModel}, "codex-b", "high"},
		{"pinned equal to global uses model default", "", Bot{Model: "codex-a", ReasoningEffort: followGlobalModel}, "codex-a", "low"},
		{"legacy empty effort", "", Bot{Model: "codex-b", ReasoningEffort: ""}, "codex-b", "medium"},
		{"legacy empty model", "", Bot{Model: "", ReasoningEffort: "high"}, "codex-a", "high"},
		{"persisted run of a follower keeps global effort", "codex-a", Bot{Model: followGlobalModel, ReasoningEffort: followGlobalModel}, "codex-a", "medium"},
		{"run pin wins", "codex-b", Bot{Model: followGlobalModel, ReasoningEffort: followGlobalModel}, "codex-b", "high"},
	} {
		model, effort := s.resolveBotModel(ctx, tc.runModel, tc.bot)
		if model != tc.wantModel || effort != tc.wantEff {
			t.Errorf("%s: got %q/%q want %q/%q", tc.name, model, effort, tc.wantModel, tc.wantEff)
		}
	}
	// Changing the global setting moves every follower at once.
	s.mu.Lock()
	s.defaultModel, s.defaultReasoning = "codex-b", "medium"
	s.mu.Unlock()
	if model, effort := s.resolveBotModel(ctx, "", Bot{Model: followGlobalModel, ReasoningEffort: followGlobalModel}); model != "codex-b" || effort != "medium" {
		t.Fatalf("after global change: %q/%q", model, effort)
	}
}

func TestFollowGlobalRunPersistsResolvedModel(t *testing.T) {
	e := &captureEngine{}
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: e, DefaultModel: "global-model", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.mu.Lock()
	s.defaultReasoning = "high"
	s.mu.Unlock()

	follower, err := s.store.CreateBotWithReasoning("follower", "", followGlobalModel, followGlobalModel)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(follower.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, follower.ID, "hello", "follow-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != followGlobalModel {
		t.Fatalf("queued run model=%q", r.Model)
	}
	s.execute(c, r)
	if e.req.Model != "global-model" || e.req.ReasoningEffort != "high" {
		t.Fatalf("engine request=%q/%q", e.req.Model, e.req.ReasoningEffort)
	}
	done, err := s.store.GetRun(r.ID)
	if err != nil || done.Model != "global-model" {
		t.Fatalf("run row model=%q err=%v", done.Model, err)
	}
	var usageModel string
	if err := s.store.db.QueryRow(`SELECT model FROM run_usage WHERE run_id=?`, r.ID).Scan(&usageModel); err == nil && usageModel != "global-model" {
		t.Fatalf("usage model=%q", usageModel)
	}

	legacy, err := s.store.CreateBot("legacy", "", "pinned-model")
	if err != nil {
		t.Fatal(err)
	}
	lc, _ := s.store.GetConversation(legacy.DMConversationID)
	_, lr, _, err := s.store.AddUserRun(lc.ID, legacy.ID, "hello", "legacy-1")
	if err != nil {
		t.Fatal(err)
	}
	s.execute(lc, lr)
	if e.req.Model != "pinned-model" || e.req.ReasoningEffort != "medium" {
		t.Fatalf("legacy request=%q/%q", e.req.Model, e.req.ReasoningEffort)
	}
	// A run that already names its model is never rewritten.
	if err := s.store.setRunModelIfFollowing(lr.ID, "other-model"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.store.GetRun(lr.ID); got.Model != "pinned-model" {
		t.Fatalf("pinned run model rewritten to %q", got.Model)
	}
	s.summaryWG.Wait()
}

func TestWorkspaceUpdateBotFollowGlobal(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "model", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.store.CreateBotWithReasoning("alpha", "", "model", "low")
	if err != nil {
		t.Fatal(err)
	}
	r := workspaceTestRun(t, s, a)
	update := workspaceTestTool(t, s, r, "workspace_update_bot")
	// A pinned model with effort "default" uses the model default.
	got := workspaceJSON(t, update, `{"bot_id":"`+a.ID+`","reasoning_effort":"default"}`)
	if got["model"] != "model" || got["reasoning_effort"] != followGlobalModel {
		t.Fatalf("pinned+default=%v", got)
	}
	got = workspaceJSON(t, update, `{"bot_id":"`+a.ID+`","model":"inherit"}`)
	if got["model"] != followGlobalModel || got["reasoning_effort"] != followGlobalModel || got["effective_model"] != "model" {
		t.Fatalf("follow=%v", got)
	}
	list := workspaceJSON(t, workspaceTestTool(t, s, r, "workspace_list"), `{}`)
	if containsJSONString(t, list["configured_models"], followGlobalModel) {
		t.Fatalf("sentinel listed as a configured model: %v", list["configured_models"])
	}
}

func TestFollowGlobalMigrationRunsOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, DefaultModel: "codex-gpt-5.6-luna", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.putModelSettings(modelSettings{Model: "codex-gpt-5.6-luna", ReasoningEffort: "medium"}); err != nil {
		t.Fatal(err)
	}
	mk := func(name, model, effort string) string {
		b, err := s.store.CreateBotWithReasoning(name, "", model, effort)
		if err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	copied := mk("copied", "codex-gpt-5.6-luna", "medium")
	bare := mk("bare", "gpt-5.6-luna", "medium") // Codex equivalence without an OpenAI key
	legacy := mk("legacy", "codex-gpt-5.6-luna", "")
	otherEffort := mk("other-effort", "codex-gpt-5.6-luna", "high")
	otherModel := mk("other-model", "codex-gpt-6-sol", "medium")
	// Simulate a workspace from before the migration existed.
	if _, err := s.store.db.Exec(`DELETE FROM model_follow_global_migration`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = NewServer(Config{DataDir: dir, DefaultModel: "codex-gpt-5.6-luna", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{copied: true, bare: true, legacy: true, otherEffort: false, otherModel: false}
	for id, follows := range want {
		b, err := s.store.GetBot(id)
		if err != nil {
			t.Fatal(err)
		}
		got := b.Model == followGlobalModel && b.ReasoningEffort == followGlobalModel
		if got != follows {
			t.Errorf("bot %s model=%q effort=%q follows=%v want %v", b.Name, b.Model, b.ReasoningEffort, got, follows)
		}
	}
	var switched int
	if err := s.store.db.QueryRow(`SELECT switched FROM model_follow_global_migration WHERE id=1`).Scan(&switched); err != nil || switched != 3 {
		t.Fatalf("marker switched=%d err=%v", switched, err)
	}
	// A later explicit pin equal to the global setting is a user choice.
	pin, effort := "codex-gpt-5.6-luna", "medium"
	if _, err := s.store.UpdateBot(copied, nil, nil, &pin, &effort); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = NewServer(Config{DataDir: dir, DefaultModel: "codex-gpt-5.6-luna", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.store.GetBot(copied)
	if err != nil || b.Model != pin || b.ReasoningEffort != effort {
		t.Fatalf("migration re-ran over a user pin: %+v err=%v", b, err)
	}
}
