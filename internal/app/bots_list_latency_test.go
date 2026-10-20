package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// GET /api/bots resolves each Bot's effective model and effort. With a stale
// provider catalog that used to call the provider live, once per Bot, inside
// the request: a slow provider made the Bot list take seconds.
func TestBotListNeverWaitsOnProviderCatalog(t *testing.T) {
	var calls atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/v1/models" && calls.Load() > 1 {
			time.Sleep(700 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-6-luna","created":400}]}`))
	}))
	defer slow.Close()
	openai := &fakeModelAPI{Server: slow}
	s := providerServer(t, openai, nil)
	putProviderKeyOK(t, s, "openai", syntheticOpenAIKey) // first call: fast, fills the catalog
	for i := 0; i < 6; i++ {
		b, err := s.store.CreateBot("follower", "", "gpt-6-luna")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.store.db.Exec(`UPDATE bots SET reasoning_effort=? WHERE id=?`, followGlobalModel, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Age every cached catalog so the next live lookup would hit the slow provider.
	s.providerMu.Lock()
	for name, entry := range s.providerCatalogs {
		entry.at = time.Now().Add(-time.Hour)
		s.providerCatalogs[name] = entry
	}
	s.providerMu.Unlock()
	s.modelCatalogMu.Lock()
	s.modelCatalogAt = time.Now().Add(-time.Hour)
	s.modelCatalogMu.Unlock()

	started := time.Now()
	rec := providerRequest(t, s, http.MethodGet, "/api/bots", "")
	elapsed := time.Since(started)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("GET /api/bots took %v with a slow provider; it must answer from the cached catalog", elapsed)
	}
}
