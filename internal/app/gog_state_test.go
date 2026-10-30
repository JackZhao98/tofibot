package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A rejected grant becomes a stable reauth_required error, and a disconnect drops the cached account status so Bots
// stop being offered the Google tools at once.
func TestGogReauthAndDisconnectStateReachSettings(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	_ = os.WriteFile(tokenPath, []byte("private-token"), 0600)
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/plugins/gog/gog/check":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"reauth_required"}`))
		case "/v1/plugins/gog/gog/disconnect":
			_, _ = w.Write([]byte(`{"connected":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	s := googleTestServer(t, runner.URL, tokenPath)
	w := httptest.NewRecorder()
	s.routeLocalMCP(w, httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/check", strings.NewReader("{}")), "extensions/local-mcp/gog/oauth/check")
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"reauth_required"`) {
		t.Fatalf("check: %d %s", w.Code, w.Body.String())
	}
	s.gogStatus.mu.Lock()
	s.gogStatus.connected, s.gogStatus.checked = true, time.Now()
	s.gogStatus.mu.Unlock()
	w = httptest.NewRecorder()
	s.routeLocalMCP(w, httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/disconnect", strings.NewReader("{}")), "extensions/local-mcp/gog/oauth/disconnect")
	if w.Code != 200 || !s.gogStatus.checked.IsZero() {
		t.Fatalf("disconnect: %d %s cache=%v", w.Code, w.Body.String(), s.gogStatus.checked)
	}
}
