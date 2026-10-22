package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func starsAnswer(t *testing.T, s *Server, query string) map[string]int {
	t.Helper()
	w := httptest.NewRecorder()
	if !s.routeRepoStars(w, httptest.NewRequest(http.MethodGet, "/api/extensions/repo-stars?repos="+query, nil), "extensions/repo-stars") || w.Code != 200 {
		t.Fatalf("route: %d", w.Code)
	}
	var out struct {
		Stars map[string]int `json:"stars"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Stars
}

func TestRepoStarsAreCachedForADayAndFailuresShowNoNumber(t *testing.T) {
	var calls atomic.Int32
	failing := atomic.Bool{}
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("the read must be anonymous")
		}
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/repos/upstash/context7":
			_, _ = w.Write([]byte(`{"stargazers_count":63467}`))
		case "/repos/openclaw/gogcli":
			_, _ = w.Write([]byte(`{"stargazers_count":8520}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer github.Close()
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	s := &Server{}
	s.repoStars = repoStarCache{baseURL: github.URL, clock: func() time.Time { return now }}

	got := starsAnswer(t, s, "upstash/context7,openclaw/gogcli,upstash/missing,not-a-repo,../etc/passwd")
	if got["upstash/context7"] != 63467 || got["openclaw/gogcli"] != 8520 || len(got) != 2 {
		t.Fatalf("counts: %v", got)
	}
	// A repository that does not exist has no number, and malformed names never reach GitHub.
	if calls.Load() != 3 {
		t.Fatalf("fetched %d times, want 3 (two repos and the missing one, none of the malformed names)", calls.Load())
	}
	before := calls.Load()
	now = now.Add(23 * time.Hour)
	starsAnswer(t, s, "upstash/context7,openclaw/gogcli")
	starsAnswer(t, s, "upstash/context7,openclaw/gogcli")
	if calls.Load() != before {
		t.Fatalf("inside the 24 h TTL nothing is fetched again (%d -> %d)", before, calls.Load())
	}
	// After the TTL one fetch per repository, and a failure shows no number: no stale or invented value.
	now = now.Add(2 * time.Hour)
	failing.Store(true)
	got = starsAnswer(t, s, "upstash/context7")
	if _, ok := got["upstash/context7"]; ok {
		t.Fatalf("a failed refresh past the TTL must show no number: %v", got)
	}
	after := calls.Load()
	starsAnswer(t, s, "upstash/context7")
	if calls.Load() != after {
		t.Fatal("a failed repository is not hammered within the hour")
	}
	// Recovery after the failure backoff.
	failing.Store(false)
	now = now.Add(61 * time.Minute)
	if got = starsAnswer(t, s, "upstash/context7"); got["upstash/context7"] != 63467 {
		t.Fatalf("recovery: %v", got)
	}
}

func TestRepoStarsRespectTheUnauthenticatedRateLimit(t *testing.T) {
	var calls atomic.Int32
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(30*time.Minute).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer github.Close()
	s := &Server{}
	s.repoStars = repoStarCache{baseURL: github.URL, clock: func() time.Time { return now }}
	if got := starsAnswer(t, s, "upstash/context7"); len(got) != 0 {
		t.Fatalf("a rate-limited read has no number: %v", got)
	}
	first := calls.Load()
	starsAnswer(t, s, "github/github-mcp-server,openclaw/gogcli")
	if calls.Load() != first {
		t.Fatal("no request is made while GitHub says the limit is used up")
	}
	now = now.Add(31 * time.Minute)
	starsAnswer(t, s, "github/github-mcp-server")
	if calls.Load() == first {
		t.Fatal("requests resume after the reset time")
	}
}
