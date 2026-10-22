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

const testGoogleClient = `{"web":{"client_id":"622599621014-abcdef.apps.googleusercontent.com","project_id":"tofi-sample-123456","client_secret":"GOCSPX-never-shown","redirect_uris":["https://tofi.example/api/extensions/local-mcp/gog/oauth/callback"]}}`

func googleTestServer(t *testing.T, runnerURL, tokenPath string) *Server {
	s, _ := googleTestServerAt(t, runnerURL, tokenPath)
	return s
}

func googleTestServerAt(t *testing.T, runnerURL, tokenPath string) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	if runnerURL != "" {
		t.Setenv("TOFI_MCP_RUNNER_URL", runnerURL)
		t.Setenv("TOFI_MCP_RUNNER_TOKEN_FILE", tokenPath)
	}
	s, err := NewServer(Config{DataDir: root, PublicOrigin: "https://tofi.example", MCPConfigPath: filepath.Join(root, "mcp.json")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}

func TestWorkspaceGoogleClientIsSealedAndNeverReadBack(t *testing.T) {
	s, root := googleTestServerAt(t, "", "")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/extensions/google/client", strings.NewReader(`{"credentials_json":`+testGoogleClient+`}`))
	if !s.routeGoogle(w, r, "extensions/google/client") || w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "GOCSPX") || strings.Contains(body, "abcdef") {
		t.Fatalf("the secret or client id came back: %s", body)
	}
	var view struct {
		Client struct {
			Configured    bool   `json:"configured"`
			ProjectID     string `json:"project_id"`
			ProjectNumber string `json:"project_number"`
		} `json:"client"`
		APIs []googleAPIView `json:"apis"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil || !view.Client.Configured || view.Client.ProjectID != "tofi-sample-123456" || view.Client.ProjectNumber != "622599621014" {
		t.Fatalf("overview: %v %s", err, body)
	}
	// An enable link for every Google service TOFI offers, for the client's project.
	if len(view.APIs) != 6 {
		t.Fatalf("apis: %+v", view.APIs)
	}
	for _, api := range view.APIs {
		if !strings.HasSuffix(api.EnableURL, "/"+api.API+"?project=tofi-sample-123456") || !strings.HasSuffix(api.MCPURL, "/"+api.MCPAPI+"?project=tofi-sample-123456") {
			t.Errorf("link for %s: %+v", api.Service, api)
		}
	}
	raw, _, err := s.googleClient()
	if err != nil || !strings.Contains(string(raw), "GOCSPX-never-shown") {
		t.Fatalf("the saved client must be readable by the server: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "secrets.json")); strings.Contains(string(data), "GOCSPX") {
		t.Fatal("secret stored in the clear")
	}
	// Replace, then remove.
	w = httptest.NewRecorder()
	s.routeGoogle(w, httptest.NewRequest(http.MethodDelete, "/api/extensions/google/client", nil), "extensions/google/client")
	if _, _, err := s.googleClient(); err == nil {
		t.Fatal("client survived removal")
	}
	w = httptest.NewRecorder()
	s.routeGoogle(w, httptest.NewRequest(http.MethodPut, "/api/extensions/google/client", strings.NewReader(`{"credentials_json":{"web":{"client_id":"x"}}}`)), "extensions/google/client")
	if w.Code != 400 {
		t.Fatalf("an unusable client must be refused: %d", w.Code)
	}
}

// A second Google service signs in with the saved client: the browser sends no JSON and the runner still gets it.
func TestSecondGoogleServiceDoesNotAskForTheClientAgain(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	_ = os.WriteFile(tokenPath, []byte("private-token"), 0600)
	var got struct {
		CredentialsJSON json.RawMessage `json:"credentials_json"`
		Services        []string        `json:"services"`
	}
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/plugins/gog/gog/start" {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"authorization_url":"https://accounts.google.com/o/oauth2/auth?include_granted_scopes=true"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer runner.Close()
	s := googleTestServer(t, runner.URL, tokenPath)
	if _, err := s.saveGoogleClient([]byte(testGoogleClient)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/start", strings.NewReader(`{"email":"person@gmail.com","services":["calendar"]}`))
	r.Header.Set("Origin", "https://tofi.example")
	s.routeLocalMCP(w, r, "extensions/local-mcp/gog/oauth/start")
	if w.Code != 200 {
		t.Fatalf("start without JSON: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(string(got.CredentialsJSON), "GOCSPX-never-shown") || len(got.Services) != 1 || got.Services[0] != "calendar" {
		t.Fatalf("the runner did not get the saved client and the new service: %s %v", got.CredentialsJSON, got.Services)
	}
	// Without a saved client and without JSON the call names what is missing.
	if err := s.deleteGoogleClient(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/start", strings.NewReader(`{"email":"person@gmail.com"}`))
	r.Header.Set("Origin", "https://tofi.example")
	s.routeLocalMCP(w, r, "extensions/local-mcp/gog/oauth/start")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "google_client_missing") {
		t.Fatalf("missing client: %d %s", w.Code, w.Body.String())
	}
}

// An MCP entry for another Google service uses the saved client too; the secret is filled in on the server.
func TestGoogleMCPEntryUsesTheSavedClient(t *testing.T) {
	s := googleTestServer(t, "", "")
	save := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		body := `{"name":"google-calendar","url":"https://calendarmcp.googleapis.com/mcp/v1","transport":"streamable_http","google_client":true,"oauth":{"client_id":"","scopes":["https://www.googleapis.com/auth/calendar.readonly"]}}`
		s.routeExtensions(w, httptest.NewRequest(http.MethodPost, "/api/extensions/mcp", strings.NewReader(body)), "extensions/mcp")
		return w
	}
	if w := save(); w.Code != 409 || !strings.Contains(w.Body.String(), "google_client_missing") {
		t.Fatalf("without a saved client: %d %s", w.Code, w.Body.String())
	}
	if _, err := s.saveGoogleClient([]byte(testGoogleClient)); err != nil {
		t.Fatal(err)
	}
	if w := save(); w.Code != 200 {
		t.Fatalf("with a saved client: %d %s", w.Code, w.Body.String())
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 1 || views[0].OAuth == nil || views[0].OAuth.ClientID != "622599621014-abcdef.apps.googleusercontent.com" {
		t.Fatalf("entry: %+v %v", views, err)
	}
	out, _ := json.Marshal(views)
	if strings.Contains(string(out), "GOCSPX") {
		t.Fatal("the saved secret was returned in the list")
	}
}

// The runner's structured "API not enabled" becomes a Settings error with a console link TOFI builds itself, in the
// saved client's project.
func TestGogCheckReportsAPINotEnabledWithTOFIBuiltLink(t *testing.T) {
	root := t.TempDir()
	tokenPath := filepath.Join(root, "runner-token")
	_ = os.WriteFile(tokenPath, []byte("private-token"), 0600)
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/plugins/gog/gog/check" {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"api_disabled","api":"gmail.googleapis.com","project":"622599621014","activationUrl":"https://evil.example"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer runner.Close()
	s := googleTestServer(t, runner.URL, tokenPath)
	if _, err := s.saveGoogleClient([]byte(testGoogleClient)); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.routeLocalMCP(w, httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/gog/oauth/check", strings.NewReader("{}")), "extensions/local-mcp/gog/oauth/check")
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"api_disabled"`) || !strings.Contains(w.Body.String(), "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456") || strings.Contains(w.Body.String(), "evil") {
		t.Fatalf("check: %d %s", w.Code, w.Body.String())
	}
}
