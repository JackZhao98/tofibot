package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func extensionHTTP(t *testing.T, handler http.Handler, method, path string, body any) (int, []byte) {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, payload)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response.Code, response.Body.Bytes()
}

func configEventsAfter(t *testing.T, s *Server, cursor int64) []workspaceEvent {
	t.Helper()
	events, err := s.store.workspaceEvents(cursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Scope != workspaceScopeConfig {
			t.Fatalf("extension mutation emitted scope %q, want %q", event.Scope, workspaceScopeConfig)
		}
	}
	return events
}

func TestExtensionMutationsPublishConfigWorkspaceEvents(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := s.Handler()

	_, err = s.store.CreateBot("extension sync bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	// Setup is intentionally direct: the test starts observing after the
	// fixture Bot exists, so each assertion belongs to exactly one HTTP action.
	cursor := s.store.workspaceEventCursor()

	status, body := extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp", nil)
	if status != http.StatusOK {
		t.Fatalf("MCP read status=%d body=%s", status, body)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("read-only MCP list emitted events=%+v", events)
	}

	status, body = extensionHTTP(t, handler, http.MethodPost, "/api/extensions/mcp/missing/test", nil)
	if status != http.StatusOK {
		t.Fatalf("MCP test status=%d body=%s", status, body)
	}
	var diagnostic struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(body, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.OK {
		t.Fatalf("missing MCP unexpectedly tested successfully: %s", body)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("failed MCP test emitted events=%+v", events)
	}

	status, _ = extensionHTTP(t, handler, http.MethodPost, "/api/extensions/mcp", map[string]any{
		"name": "fixture", "url": "http://127.0.0.1:1/mcp", "unexpected": true,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid MCP save status=%d", status)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("failed MCP save emitted events=%+v", events)
	}

	status, body = extensionHTTP(t, handler, http.MethodPost, "/api/extensions/mcp", map[string]any{
		"name": "fixture", "url": "http://127.0.0.1:1/mcp",
	})
	if status != http.StatusOK {
		t.Fatalf("MCP save status=%d body=%s", status, body)
	}
	events := configEventsAfter(t, s, cursor)
	if len(events) != 1 {
		t.Fatalf("MCP save emitted %d config events, want 1", len(events))
	}
	cursor = events[0].ID

	status, _ = extensionHTTP(t, handler, http.MethodPost, "/api/extensions/skills", map[string]any{
		"name": "broken", "files": map[string]string{"README.md": "missing manifest"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid skill install status=%d", status)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("failed skill install emitted events=%+v", events)
	}

	status, body = extensionHTTP(t, handler, http.MethodPost, "/api/extensions/skills", map[string]any{
		"name": "fixture-skill", "files": map[string]string{
			"SKILL.md": "---\nname: fixture-skill\ndescription: local sync fixture\n---\nbody",
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("skill install status=%d body=%s", status, body)
	}
	events = configEventsAfter(t, s, cursor)
	if len(events) != 1 {
		t.Fatalf("skill install emitted %d config events, want 1", len(events))
	}
	cursor = events[0].ID

	// Installed skills are global; there is no per-Bot activation request.
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("unexpected post-install events=%d", len(events))
	}
}

func TestExtensionOAuthCallbackPublishesConfigEventOnlyOnSuccess(t *testing.T) {
	var tokenRequests atomic.Int32
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint":           provider.URL + "/authorize",
				"token_endpoint":                   provider.URL + "/token",
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/token":
			tokenRequests.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("code") != "fixture-code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "invalid local fixture exchange", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "fixture-access", "refresh_token": "fixture-refresh", "token_type": "bearer", "expires_in": 3600,
			})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer provider.Close()

	s, err := NewServer(Config{DataDir: t.TempDir(), PublicOrigin: "https://tofi.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.extensions.SaveMCP("oauth-fixture", extensionsFixture(provider.URL), false); err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	cursor := s.store.workspaceEventCursor()

	status, body := extensionHTTPWithHost(t, handler, http.MethodPost, "https://tofi.example/api/extensions/mcp/oauth-fixture/oauth/start", nil, "tofi.example")
	if status != http.StatusOK {
		t.Fatalf("OAuth start status=%d body=%s", status, body)
	}
	var started struct {
		SessionID        string `json:"session_id"`
		AuthorizationURL string `json:"authorization_url"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatal(err)
	}
	authorization, err := url.Parse(started.AuthorizationURL)
	if err != nil || started.SessionID == "" || authorization.Query().Get("state") == "" {
		t.Fatalf("incomplete OAuth start response=%s err=%v", body, err)
	}
	state := authorization.Query().Get("state")

	status, body = extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp/another-service/oauth/callback?session_id="+url.QueryEscape(started.SessionID)+"&code=fixture-code&state="+url.QueryEscape(state), nil)
	if status != http.StatusBadRequest || tokenRequests.Load() != 0 || strings.Contains(string(body), "oauth-fixture") {
		t.Fatalf("wrong callback service consumed flow or exposed identity: status=%d", status)
	}

	status, _ = extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp/oauth-fixture/oauth/callback?session_id="+url.QueryEscape(started.SessionID)+"&code=bad-code&state=wrong-state", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid OAuth callback status=%d", status)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatalf("failed OAuth callback emitted events=%+v", events)
	}

	status, body = extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp/oauth-fixture/oauth/callback?session_id="+url.QueryEscape(started.SessionID)+"&code=fixture-code&state="+url.QueryEscape(state), nil)
	if status != http.StatusOK {
		t.Fatalf("successful OAuth callback status=%d body=%s", status, body)
	}
	for _, want := range []string{"Connection complete", `data-state="success"`, "/oauth/connection.css", "oauth-fixture"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("missing completion UI %q", want)
		}
	}
	for _, secret := range []string{state, started.SessionID, "fixture-code", "fixture-access", "fixture-refresh"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("OAuth material reflected into page")
		}
	}
	if tokenRequests.Load() != 1 {
		t.Fatalf("token exchange requests=%d, want 1", tokenRequests.Load())
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 1 {
		t.Fatalf("successful OAuth callback events=%d, want 1", len(events))
	}
	cursor = s.store.workspaceEventCursor()
	status, body = extensionHTTPWithHost(t, handler, http.MethodPost, "https://tofi.example/api/extensions/mcp/oauth-fixture/oauth/start", nil, "tofi.example")
	if status != http.StatusOK {
		t.Fatalf("second start=%d", status)
	}
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatal(err)
	}
	authorization, _ = url.Parse(started.AuthorizationURL)
	state = authorization.Query().Get("state")
	status, body = extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp/oauth-fixture/oauth/callback?state="+url.QueryEscape(state)+"&error=access_denied&error_description=%3Cscript%3Eprivate-provider-detail%3C/script%3E", nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), `data-state="denied"`) || !strings.Contains(string(body), "Connection failed") || strings.Contains(string(body), "private-provider-detail") || tokenRequests.Load() != 1 {
		t.Fatalf("unsafe denial response status=%d", status)
	}
	if events := configEventsAfter(t, s, cursor); len(events) != 0 {
		t.Fatal("denial emitted config event")
	}
	status, _ = extensionHTTP(t, handler, http.MethodGet, "/api/extensions/mcp/oauth-fixture/oauth/callback?state="+url.QueryEscape(state)+"&code=fixture-code", nil)
	if status != http.StatusBadRequest || tokenRequests.Load() != 1 {
		t.Fatal("denied flow replay accepted")
	}
}

// Keep the fixture config in this test package so no OAuth credential or
// provider helper reaches the real environment.
func extensionsFixture(endpoint string) extensions.MCPServerConfig {
	return extensions.MCPServerConfig{
		URL:   endpoint + "/mcp",
		OAuth: &extensions.OAuthConfig{ClientID: "fixture-client", ClientSecret: "fixture-secret", Scopes: []string{"tools"}, AuthServerMetadataURL: endpoint + "/.well-known/oauth-authorization-server"},
	}
}

func extensionHTTPWithHost(t *testing.T, handler http.Handler, method, path string, body any, host string) (int, []byte) {
	t.Helper()
	var payload *bytes.Reader
	if body == nil {
		payload = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, payload)
	req.Host = host
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response.Code, response.Body.Bytes()
}
