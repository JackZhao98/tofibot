package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOAuthEntryRouting(t *testing.T) {
	redirects := make(chan string, 2)
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "code_challenge_methods_supported": []string{"S256"}})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "fixture-code" {
				http.Error(w, "invalid exchange", 400)
				return
			}
			redirects <- r.Form.Get("redirect_uri")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-token", "token_type": "bearer", "expires_in": 3600})
		}
	}))
	defer provider.Close()
	s, err := NewServer(Config{DataDir: t.TempDir(), PublicOrigin: "https://tofi.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.extensions.SaveMCP("notion", extensionsFixture(provider.URL), false); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		if !s.routeExtensions(w, r, strings.TrimPrefix(r.URL.Path, "/api/")) {
			t.Fatalf("unhandled %s", path)
		}
		return w
	}
	for _, origin := range []string{"", "http://tofi.example", "https://other.example"} {
		w := call("POST", "/api/extensions/mcp/notion/oauth/start", origin, "{}")
		if w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_https_required") {
			t.Fatalf("unsafe Web start %q: %d %s", origin, w.Code, w.Body)
		}
	}
	// HTTPS browser behind a reverse proxy: configured public origin is used,
	// never the internal Host or an arbitrary X-Forwarded-Host.
	for _, mode := range []string{"web", "desktop"} {
		path := "/api/extensions/mcp/notion/oauth/start"
		origin := "https://tofi.example"
		want := origin + "/api/extensions/mcp/notion/oauth/callback"
		if mode == "desktop" {
			path = "/api/extensions/mcp/notion/oauth/desktop/start"
			origin = ""
			want = desktopOAuthRedirect
		}
		w := call("POST", path, origin, "{}")
		if w.Code != 200 {
			t.Fatalf("%s start: %d %s", mode, w.Code, w.Body)
		}
		var start struct {
			SessionID        string `json:"session_id"`
			AuthorizationURL string `json:"authorization_url"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil {
			t.Fatal(err)
		}
		authorization, _ := url.Parse(start.AuthorizationURL)
		q := authorization.Query()
		if q.Get("redirect_uri") != want || q.Get("code_challenge_method") != "S256" {
			t.Fatalf("wrong authorization: %s", start.AuthorizationURL)
		}
		body, _ := json.Marshal(map[string]string{"session_id": start.SessionID, "state": q.Get("state"), "code": "fixture-code"})
		completion := "/api/extensions/oauth/desktop/complete"
		if mode == "web" {
			// Desktop completion cannot steal a Web flow, even with its state.
			if w := call("POST", completion, "", string(body)); w.Code != 400 {
				t.Fatalf("cross-channel completion: %d", w.Code)
			}
			w = call("GET", want+"?state="+url.QueryEscape(q.Get("state"))+"&code=fixture-code", "", "")
		} else {
			w = call("POST", completion, "", string(body))
		}
		if w.Code != 200 {
			t.Fatalf("%s complete: %d %s", mode, w.Code, w.Body)
		}
		if got := <-redirects; got != want {
			t.Fatalf("exchange redirect differs: %s", got)
		}
		if w := call("POST", completion, "", string(body)); w.Code != 400 {
			t.Fatalf("replay accepted: %d", w.Code)
		}
	}
	w := call("GET", "/api/extensions/oauth-options", "", "")
	if !strings.Contains(w.Body.String(), desktopOAuthRedirect) || !strings.Contains(w.Body.String(), "https://tofi.example") {
		t.Fatalf("incomplete options: %s", w.Body)
	}
}

func TestDesktopOAuthRoutesAreNotPublicCallbacks(t *testing.T) {
	for _, path := range []string{"/api/extensions/oauth/desktop/complete", "/api/extensions/oauth/desktop/cancel", "/api/extensions/mcp/notion/oauth/desktop/start"} {
		// Owner auth's sole callback exemption remains GET .../oauth/callback.
		r := httptest.NewRequest("POST", path, nil)
		if ownerIndependentRoute(r) {
			t.Fatalf("native route must require owner authentication: %s", path)
		}
	}
}
