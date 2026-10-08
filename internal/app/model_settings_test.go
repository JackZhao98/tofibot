package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestModelSettingsPersistAndBotDefaultReasoning(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "custom-model", Provider: "local"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	req := httptest.NewRequest(http.MethodPut, "/api/model-settings", strings.NewReader(`{"model":"custom-model","reasoning_effort":"high"}`))
	rec := httptest.NewRecorder()
	s.modelSettings(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("settings PUT status=%d body=%s", rec.Code, rec.Body.String())
	}
	b, err := s.store.CreateBotWithReasoning("new", "", "custom-model", "high")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.store.GetBot(b.ID)
	if err != nil || got.ReasoningEffort != "high" {
		t.Fatalf("bot reasoning=%q err=%v", got.ReasoningEffort, err)
	}

	st, err := s.store.getModelSettings()
	if err != nil || st.Model != "custom-model" || st.ReasoningEffort != "high" {
		t.Fatalf("stored settings=%+v err=%v", st, err)
	}
	legacy, err := s.store.CreateBot("legacy", "", "custom-model")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.ReasoningEffort != "" {
		t.Fatalf("legacy Bot unexpectedly received a global reasoning override: %q", legacy.ReasoningEffort)
	}
	post := httptest.NewRequest(http.MethodPost, "/api/bots", strings.NewReader(`{"name":"defaulted"}`))
	postRec := httptest.NewRecorder()
	s.bots(postRec, post)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("Bot POST status=%d body=%s", postRec.Code, postRec.Body.String())
	}
	var created Bot
	if err := json.Unmarshal(postRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Model != followGlobalModel || created.ReasoningEffort != followGlobalModel || created.EffectiveModel != "custom-model" || created.EffectiveReasoningEffort != "high" {
		t.Fatalf("new Bot does not follow the global setting: %+v", created)
	}
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/bots/"+created.ID, strings.NewReader(`{"model":"custom-model","reasoning_effort":"low"}`))
	patchRec := httptest.NewRecorder()
	s.bots(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("Bot PATCH status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	patched, err := s.store.GetBot(created.ID)
	if err != nil || patched.Model != "custom-model" || patched.ReasoningEffort != "low" {
		t.Fatalf("atomic Bot settings update=%+v err=%v", patched, err)
	}

	creationID := uuid.NewString()
	onboard := httptest.NewRequest(http.MethodPost, "/api/bots", strings.NewReader(`{"onboarding":true,"client_creation_id":"`+creationID+`","model":"custom-model","reasoning_effort":"high"}`))
	onboardRec := httptest.NewRecorder()
	s.bots(onboardRec, onboard)
	if onboardRec.Code != http.StatusCreated {
		t.Fatalf("onboarding POST status=%d body=%s", onboardRec.Code, onboardRec.Body.String())
	}
	retry := httptest.NewRequest(http.MethodPost, "/api/bots", strings.NewReader(`{"onboarding":true,"client_creation_id":"`+creationID+`","model":"custom-model","reasoning_effort":"low"}`))
	retryRec := httptest.NewRecorder()
	s.bots(retryRec, retry)
	if retryRec.Code != http.StatusOK {
		t.Fatalf("onboarding retry status=%d body=%s", retryRec.Code, retryRec.Body.String())
	}
	var retried Bot
	if err := json.Unmarshal(retryRec.Body.Bytes(), &retried); err != nil {
		t.Fatal(err)
	}
	if retried.ReasoningEffort != "high" {
		t.Fatalf("idempotent onboarding retry changed reasoning: %q", retried.ReasoningEffort)
	}
}

func TestModelSettingsRejectsUnsupportedCachedEffort(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), Provider: "openai_codex", DefaultModel: "m"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.store.cacheModels([]ModelOption{{ID: "m", Name: "M", ReasoningEfforts: []string{"low"}, DefaultReasoning: "low"}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/model-settings", strings.NewReader(`{"model":"m","reasoning_effort":"high"}`))
	rec := httptest.NewRecorder()
	s.modelSettings(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestNormalizeCodexModelsIncludesGPT6Family(t *testing.T) {
	var raw codexModelsResponse
	if err := json.Unmarshal([]byte(`{"models":[
		{"slug":"gpt-6-astra","display_name":"GPT-6 Astra","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"}]},
		{"slug":"gpt-6-sol","display_name":"GPT-6 Sol","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"none"},{"effort":"medium"}]},
		{"slug":"gpt-6-luna","display_name":"GPT-6 Luna","default_reasoning_level":"medium","supported_reasoning_levels":[{"effort":"none"},{"effort":"medium"}]}
	]}`), &raw); err != nil {
		t.Fatal(err)
	}
	models := normalizeCodexModels(raw)
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	for i, id := range []string{"codex-gpt-6-astra", "codex-gpt-6-sol", "codex-gpt-6-luna"} {
		if models[i].ID != id || models[i].DefaultReasoning != "medium" || len(models[i].ReasoningEfforts) == 0 {
			t.Errorf("model %d = %+v", i, models[i])
		}
	}
}
