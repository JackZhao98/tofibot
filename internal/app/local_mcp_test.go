package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalMCPInstallAttachesPrivateRunnerWithoutLeakingToken(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	if err := os.WriteFile(tokenPath, []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Errorf("missing private auth")
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/v1/plugins" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"gog","state":"sleeping"}`))
		case r.URL.Path == "/v1/plugins" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"plugins":[{"id":"gog","state":"sleeping"}]}`))
		case r.URL.Path == "/v1/plugins/gog" && r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenPath)
	s, err := NewServer(Config{DataDir: root, MCPConfigPath: filepath.Join(root, "mcp.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/install", strings.NewReader(`{"id":"gog","kind":"builtin_gog"}`))
	if !s.routeLocalMCP(w, request, "extensions/local-mcp/install") || w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 1 || views[0].Name != "local_gog" {
		t.Fatalf("views=%v err=%v", views, err)
	}
	if strings.Contains(views[0].Headers["Authorization"], "private-token") {
		t.Fatal("token leaked through settings")
	}
	w = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/extensions/local-mcp", nil)
	s.routeLocalMCP(w, request, "extensions/local-mcp")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodDelete, "/api/extensions/local-mcp/gog", nil)
	s.routeLocalMCP(w, request, "extensions/local-mcp/gog")
	if w.Code != 200 {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	views, err = s.extensions.ListMCP()
	if err != nil || len(views) != 0 {
		t.Fatalf("remaining views=%v err=%v", views, err)
	}
}

func TestLocalGogOAuthUsesConfiguredHTTPSCallback(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	if err := os.WriteFile(tokenPath, []byte("private-token"), 0600); err != nil {
		t.Fatal(err)
	}
	callback := "https://tofi.example/api/extensions/local-mcp/gog/oauth/callback"
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing Runner token")
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/plugins/gog/gog/start":
			var input struct {
				RedirectURI string `json:"redirect_uri"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.RedirectURI != callback {
				t.Errorf("redirect=%q err=%v", input.RedirectURI, err)
			}
			_, _ = w.Write([]byte(`{"authorization_url":"https://accounts.google.com/test"}`))
		case "/v1/plugins/gog/gog/finish":
			var input struct {
				RedirectedURL string `json:"redirected_url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.RedirectedURL != callback+"?code=c&state=s" {
				t.Errorf("callback=%q err=%v", input.RedirectedURL, err)
			}
			_, _ = w.Write([]byte(`{"connected":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer runner.Close()
	t.Setenv("TOFI_MCP_RUNNER_URL", runner.URL)
	t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenPath)
	s, err := NewServer(Config{DataDir: root, PublicOrigin: "https://tofi.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/start", strings.NewReader(`{"email":"person@gmail.com","credentials_json":{"web":{"client_id":"id"}}}`))
	request.Header.Set("Origin", "https://tofi.example")
	s.routeLocalMCP(w, request, "extensions/local-mcp/gog/oauth/start")
	if w.Code != 200 {
		t.Fatalf("start=%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/extensions/local-mcp/gog/oauth/callback?code=c&state=s", nil)
	s.routeLocalMCP(w, request, "extensions/local-mcp/gog/oauth/callback")
	if w.Code != 200 {
		t.Fatalf("callback=%d %s", w.Code, w.Body.String())
	}
}
