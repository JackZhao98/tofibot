package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestPreferencesPersistValidateAndInitializeOnly(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	get := httptest.NewRecorder()
	s.Handler().ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/preferences", nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"timezone_configured":false`) {
		t.Fatalf("initial preferences: %d %s", get.Code, get.Body.String())
	}
	bad := httptest.NewRecorder()
	s.Handler().ServeHTTP(bad, httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"timezone":"Not/IANA"}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid timezone status=%d body=%s", bad.Code, bad.Body.String())
	}
	init := httptest.NewRecorder()
	s.Handler().ServeHTTP(init, httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"timezone":"America/Los_Angeles","initialize_only":true}`)))
	if init.Code != http.StatusOK || !strings.Contains(init.Body.String(), `"timezone_configured":true`) {
		t.Fatalf("initialize status=%d body=%s", init.Code, init.Body.String())
	}
	keep := httptest.NewRecorder()
	s.Handler().ServeHTTP(keep, httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"timezone":"UTC","initialize_only":true}`)))
	if keep.Code != http.StatusOK || !strings.Contains(keep.Body.String(), `America/Los_Angeles`) {
		t.Fatalf("initialize-only overwrote value: %d %s", keep.Code, keep.Body.String())
	}
	clear := httptest.NewRecorder()
	s.Handler().ServeHTTP(clear, httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(`{"timezone":""}`)))
	if clear.Code != http.StatusOK || !strings.Contains(clear.Body.String(), `"timezone_configured":false`) {
		t.Fatalf("clear status=%d body=%s", clear.Code, clear.Body.String())
	}
}

func TestPreferencesLanguageIsIndependentOfTimezone(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	put := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/preferences", strings.NewReader(body)))
		return rec
	}
	get := func() string {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/preferences", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("get status=%d body=%s", rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}
	if body := get(); !strings.Contains(body, `"language":""`) {
		t.Fatalf("default language must be automatic: %s", body)
	}
	if rec := put(`{"language":"zh-TW"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"language":"zh-TW"`) || !strings.Contains(rec.Body.String(), `"timezone_configured":false`) {
		t.Fatalf("language-only put: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(`{"timezone":"UTC"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"language":"zh-TW"`) {
		t.Fatalf("timezone put must keep language: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(`{"language":"xx"}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_language") {
		t.Fatalf("unsupported language: %d %s", rec.Code, rec.Body.String())
	}
	if rec := put(`{"language":""}`); rec.Code != http.StatusOK {
		t.Fatalf("reset to automatic: %d %s", rec.Code, rec.Body.String())
	}
	if body := get(); !strings.Contains(body, `"language":""`) || !strings.Contains(body, `"timezone":"UTC"`) {
		t.Fatalf("language reset must keep timezone: %s", body)
	}
	if rec := put(`{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty put must be rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPreferenceInitializeOnlyIsAtomic(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, zone := range []string{"UTC", "America/Los_Angeles"} {
		wg.Add(1)
		go func(zone string) {
			defer wg.Done()
			winner, err := s.putUserTimezone(zone, true)
			if err != nil {
				t.Errorf("initialize %s: %v", zone, err)
				return
			}
			results <- winner
		}(zone)
	}
	wg.Wait()
	close(results)
	zone, err := s.userTimezone()
	if err != nil || (zone != "UTC" && zone != "America/Los_Angeles") {
		t.Fatalf("stored winner=%q err=%v", zone, err)
	}
	for winner := range results {
		if winner != zone {
			t.Fatalf("caller returned non-winning timezone %q; stored %q", winner, zone)
		}
	}
}

func TestScheduleRequiresConfiguredTimezoneWhenOmitted(t *testing.T) {
	s, b, c := scheduleTestStore(t)
	_, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "x", Kind: scheduleOnce, RunAt: "2099-01-01T00:00:00Z"})
	if err == nil || !strings.Contains(err.Error(), "timezone is required") {
		t.Fatalf("expected explicit timezone requirement, got %v", err)
	}
	if _, err := s.putUserTimezone("UTC", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSchedule(c.ID, b.ID, ScheduleSpec{Content: "x", Kind: scheduleOnce, RunAt: "2099-01-01T00:00:00Z"}); err != nil {
		t.Fatalf("configured timezone fallback failed: %v", err)
	}
}
