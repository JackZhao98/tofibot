package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// guestRefusal answers every guest call with a fixed non-2xx status and body.
type guestRefusal struct {
	status int
	body   string
}

func (g guestRefusal) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: g.status, Status: http.StatusText(g.status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(g.body))}, nil
}

// A stopped guest desktop reaches the viewer as a typed 422, not a generic gateway failure,
// while every other guest failure keeps its 502.
func TestDesktopCaptureReportsTypedNotRunning(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	bot, err := s.store.CreateBot("viewer", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	capture := func(transport http.RoundTripper) (int, string) {
		s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: transport}})
		raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "action": "desktop.capture", "args": map[string]any{}})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://tofi.local/api/computers/firecracker/actions", bytes.NewReader(raw)))
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Error.Code
	}
	if code, kind := capture(guestRefusal{500, `{"ok":false,"error":"desktop is not running; call desktop.start first"}`}); code != http.StatusUnprocessableEntity || kind != "desktop_not_running" {
		t.Fatalf("not-running mapped to %d %q", code, kind)
	}
	if code, kind := capture(guestRefusal{500, `{"ok":false,"error":"screenshot tool crashed"}`}); code != http.StatusBadGateway || kind != "computer_action_failed" {
		t.Fatalf("other guest failure mapped to %d %q", code, kind)
	}
}
