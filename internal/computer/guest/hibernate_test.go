package guest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func quiesce(t *testing.T, s *Service, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/quiesce", strings.NewReader(body)))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestQuiesceReportsGuestWorkThatBlocksHibernation(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := quiesce(t, s, `{"idle_seconds":60}`); code != http.StatusOK || out["idle"] != true {
		t.Fatalf("idle guest = %d %v", code, out)
	}
	bot := "11111111-1111-4111-8111-111111111111"
	d := &desktop{botID: bot, readyClosed: true, inFlight: 1, streamViewers: 1}
	s.mu.Lock()
	s.desktops[bot] = d
	s.holds[bot] = map[string]time.Time{"run": time.Now().Add(time.Minute)}
	s.terminals["t"] = &terminal{lastOutput: time.Now()}
	s.mu.Unlock()
	code, out := quiesce(t, s, `{"idle_seconds":60}`)
	got, _ := json.Marshal(out["busy"])
	if code != http.StatusConflict || string(got) != `["desktop_operation","run_lease","terminal_output","viewer"]` {
		t.Fatalf("busy guest = %d %s", code, got)
	}
	s.mu.Lock()
	d.inFlight, d.streamViewers = 0, 0
	delete(s.holds, bot)
	s.terminals["t"].lastOutput = time.Now().Add(-2 * time.Minute)
	s.mu.Unlock()
	if code, out := quiesce(t, s, `{"idle_seconds":60}`); code != http.StatusOK {
		t.Fatalf("quiet guest = %d %v", code, out)
	}
	if code, _ := quiesce(t, s, `{"idle_seconds":-1}`); code != http.StatusBadRequest {
		t.Fatalf("invalid request = %d", code)
	}
}

func TestReaperRestartsIdleClocksAfterAResume(t *testing.T) {
	s, err := NewWithIdleTimeout(t.TempDir(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	d := &desktop{readyClosed: true, lastActivity: old}
	s.sharedDesktop = d
	now := time.Now()
	s.noteResume(now)
	if !d.lastActivity.Equal(now) {
		t.Fatalf("lastActivity = %v", d.lastActivity)
	}
}
