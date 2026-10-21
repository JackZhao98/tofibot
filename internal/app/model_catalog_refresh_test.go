package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func codexSnapshotOption() ModelOption {
	return ModelOption{ID: "codex-gpt-6-luna", Name: "GPT-6 Luna", ReasoningEfforts: []string{"low", "medium"}, DefaultReasoning: "medium"}
}

func modelsBody(t *testing.T, s *Server) (ids []string, provider map[string]string) {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/models", "")
	var body struct {
		Models []ModelOption `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	provider = map[string]string{}
	for _, m := range body.Models {
		ids = append(ids, m.ID)
		provider[m.ID] = m.Provider
	}
	return ids, provider
}

// A non-live snapshot must not be pinned for the live TTL: it is retried after
// the short guard, and dropped at once when the Codex connection changes.
func TestCodexSnapshotIsNotPinnedAndConnectionChangeDropsIt(t *testing.T) {
	s := providerServer(t, nil, nil)
	s.modelCatalog = []ModelOption{codexSnapshotOption()}
	s.modelCatalogSource = "cache"

	// Inside the guard the snapshot is served as a snapshot.
	s.modelCatalogAt = time.Now()
	if _, source, _ := s.loadCodexModels(context.Background()); source != "cache" {
		t.Fatalf("within guard: source=%s", source)
	}

	// Past the guard the memo is no longer trusted: the provider is asked again.
	s.modelCatalog = []ModelOption{{ID: "codex-stale-only-in-memory", Name: "Stale", ReasoningEfforts: []string{"low"}}}
	s.modelCatalogAt = time.Now().Add(-catalogRetryGuard - time.Second)
	models, _, _ := s.loadCodexModels(context.Background())
	for _, m := range models {
		if m.ID == "codex-stale-only-in-memory" {
			t.Fatal("a non-live memo older than the retry guard was served")
		}
	}

	// A live catalog keeps the full TTL.
	s.modelCatalog = []ModelOption{codexSnapshotOption()}
	s.modelCatalogAt, s.modelCatalogSource = time.Now().Add(-time.Minute), "live"
	if _, source, _ := s.loadCodexModels(context.Background()); source != "live" {
		t.Fatalf("live memo within TTL: source=%s", source)
	}

	// A connection change empties the memo outright.
	s.modelCatalogAt, s.modelCatalogSource = time.Now(), "cache"
	s.invalidateCodexCatalog()
	s.modelCatalogMu.Lock()
	empty := len(s.modelCatalog) == 0 && s.modelCatalogSource == ""
	s.modelCatalogMu.Unlock()
	if !empty {
		t.Fatal("invalidateCodexCatalog left the memo in place")
	}
}

func TestDisconnectingCodexDropsItsModels(t *testing.T) {
	openai := newFakeOpenAI(t, "ok")
	s := providerServer(t, openai, nil)
	if err := s.codex.SaveAccessOnlyCredential("synthetic-access", "", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey)
	s.modelCatalog = []ModelOption{codexSnapshotOption()}
	s.modelCatalogAt, s.modelCatalogSource = time.Now(), "live"

	ids, provider := modelsBody(t, s)
	if provider["codex-gpt-6-luna"] != "codex" {
		t.Fatalf("connected: codex model missing from %v", ids)
	}
	if rec := providerRequest(t, s, http.MethodDelete, "/api/auth/codex", ""); rec.Code != http.StatusOK {
		t.Fatalf("disconnect status=%d body=%s", rec.Code, rec.Body.String())
	}
	ids, provider = modelsBody(t, s)
	if _, ok := provider["codex-gpt-6-luna"]; ok {
		t.Fatalf("disconnected: codex model still listed: %v", ids)
	}
	if provider["gpt-6-luna"] != "openai" {
		t.Fatalf("OpenAI models must remain: %v", ids)
	}
}
