package guest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDesktopStreamReservationDoesNotStartOrRenewDesktop(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(nil)
	if _, status := s.reserveDesktopStream(testBot); status != 410 {
		t.Fatalf("missing desktop: %d", status)
	}
	if len(s.desktops) != 0 {
		t.Fatal("stream allocated a desktop")
	}
	old := time.Now().Add(-time.Hour)
	d := &desktop{botID: testBot, readyClosed: true, lastActivity: old}
	s.desktops[testBot] = d
	if got, status := s.reserveDesktopStream(testBot); status != 200 || got != d {
		t.Fatalf("reserve %v/%d", got, status)
	}
	if d.inFlight != 0 || !d.lastActivity.Equal(old) || len(s.holds) != 0 {
		t.Fatal("viewer renewed activity or held desktop")
	}
	if _, status := s.reserveDesktopStream(testBot); status != 409 {
		t.Fatalf("duplicate viewer=%d", status)
	}
	d.streamActive = false
	if _, status := s.reserveDesktopStream(testBot); status != 200 {
		t.Fatalf("reconnect=%d", status)
	}
	d.stopping = true
	if _, status := s.reserveDesktopStream(testBot); status != 410 {
		t.Fatalf("stopping=%d", status)
	}
	delete(s.desktops, testBot)
}

func TestDesktopStreamRejectsInvalidRoutingAndMethods(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(nil)
	for _, target := range []string{"/v1/desktop/stream", "/v1/desktop/stream?bot_id=other", "/v1/desktop/stream?bot_id=" + testBot + "&display=:0", "/v1/desktop/stream?bot_id=" + testBot + "&cursor=bad", "/v1/desktop/stream?bot_id=" + testBot + "&bot_id=" + testBot} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	// A hidden cursor is a valid observer policy; it reaches the normal
	// no-desktop response rather than being rejected as an unknown query.
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/desktop/stream?bot_id="+testBot+"&cursor=hidden", nil))
	if w.Code != http.StatusGone {
		t.Fatalf("hidden cursor query=%d", w.Code)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/v1/desktop/stream?bot_id="+testBot, nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatal(w.Code)
	}
}
