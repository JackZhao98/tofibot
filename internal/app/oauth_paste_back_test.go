package app

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func TestParsePastedRedirectIsStrict(t *testing.T) {
	const base = desktopOAuthRedirect
	code, state, iss, denied, err := parsePastedRedirect("  "+base+"?code=c1&state=s1&iss=https%3A%2F%2Fissuer.example.test \n", base)
	if err != nil || code != "c1" || state != "s1" || iss != "https://issuer.example.test" || denied {
		t.Fatalf("valid paste refused: %q %q %q %v %v", code, state, iss, denied, err)
	}
	if _, state, _, denied, err := parsePastedRedirect(base+"?error=access_denied&state=s1", base); err != nil || !denied || state != "s1" {
		t.Fatalf("provider refusal not recognised: %v %v", denied, err)
	}
	bad := map[string]string{
		"empty":            "",
		"other host":       "http://127.0.0.1:9999/oauth/callback?code=c&state=s",
		"localhost":        "http://localhost:43821/oauth/callback?code=c&state=s",
		"lookalike host":   "http://127.0.0.1.evil.example:43821/oauth/callback?code=c&state=s",
		"userinfo":         "http://a@127.0.0.1:43821/oauth/callback?code=c&state=s",
		"https":            "https://127.0.0.1:43821/oauth/callback?code=c&state=s",
		"other path":       "http://127.0.0.1:43821/oauth/callback/x?code=c&state=s",
		"path prefix":      "http://127.0.0.1:43821/oauth/callbackx?code=c&state=s",
		"public origin":    "https://tofi.example/api/extensions/mcp/x/oauth/callback?code=c&state=s",
		"fragment":         base + "?code=c&state=s#f",
		"no query":         base,
		"no state":         base + "?code=c",
		"no code":          base + "?state=s",
		"repeated code":    base + "?code=a&code=b&state=s",
		"repeated state":   base + "?code=a&state=s&state=t",
		"inner whitespace": base + "?code=a b&state=s",
		"bare code":        "c1",
		"too long":         base + "?code=" + strings.Repeat("a", pasteBackMaxURL) + "&state=s",
		"long code":        base + "?code=" + strings.Repeat("a", pasteBackMaxCode+1) + "&state=s",
		"long state":       base + "?code=a&state=" + strings.Repeat("s", pasteBackMaxState+1),
		"bad escape":       base + "?code=%zz&state=s",
		"repeated iss":     base + "?code=a&state=s&iss=a&iss=b",
		"long iss":         base + "?code=a&state=s&iss=" + strings.Repeat("i", pasteBackMaxIss+1),
	}
	for name, raw := range bad {
		if _, _, _, _, err := parsePastedRedirect(raw, base); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// A synthetic provider with dynamic registration that records the redirects it
// is asked to register and the redirect sent with the code exchange.
type loopbackProvider struct {
	srv                 *httptest.Server
	mu                  sync.Mutex
	registered, exchged []string
	hits                map[string]int
	// refusePublic: registration answers invalid_redirect_uri for any non-loopback redirect (RFC 7591 error code).
	refusePublic bool
	// advertiseIss: the metadata says authorization responses carry `iss` (RFC 9207).
	advertiseIss bool
}

func newLoopbackProvider(t *testing.T) *loopbackProvider {
	t.Helper()
	p := &loopbackProvider{hits: map[string]int{}}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits[r.URL.Path]++
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token", "registration_endpoint": p.srv.URL + "/register", "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": p.advertiseIss})
		case "/register":
			var in struct {
				RedirectURIs []string `json:"redirect_uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			p.mu.Lock()
			p.registered = append(p.registered, in.RedirectURIs...)
			p.mu.Unlock()
			for _, uri := range in.RedirectURIs {
				if p.refusePublic && !strings.HasPrefix(uri, "http://127.0.0.1") {
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_redirect_uri", "error_description": "wording that is never read"})
					return
				}
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "synthetic-client"})
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "synthetic-code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "bad exchange", 400)
				return
			}
			p.mu.Lock()
			p.exchged = append(p.exchged, r.Form.Get("redirect_uri"))
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-token", "token_type": "bearer", "expires_in": 3600})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func TestLoopbackOnlyConnectionRoutes(t *testing.T) {
	p := newLoopbackProvider(t)
	s, err := NewServer(Config{DataDir: t.TempDir(), PublicOrigin: "https://tofi.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	oauth := func(policy string) *extensions.OAuthConfig {
		return &extensions.OAuthConfig{AuthServerMetadataURL: p.srv.URL + "/metadata", RedirectPolicy: policy}
	}
	if err := s.extensions.SaveMCP("broker", extensions.MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: oauth(extensions.RedirectPolicyLoopback)}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.extensions.SaveMCP("plain", extensions.MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: oauth("")}, false); err != nil {
		t.Fatal(err)
	}
	call := func(path, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		if !s.routeExtensions(w, r, strings.TrimPrefix(r.URL.Path, "/api/")) {
			t.Fatalf("unhandled %s", path)
		}
		return w
	}
	// The https popup is never taken for a loopback-only connection, and nothing is registered.
	w := call("/api/extensions/mcp/broker/oauth/start", "https://tofi.example", "{}")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_loopback_only") {
		t.Fatalf("https popup start: %d %s", w.Code, w.Body)
	}
	p.mu.Lock()
	registered := len(p.registered)
	p.mu.Unlock()
	if registered != 0 {
		t.Fatal("the public origin reached registration")
	}
	// Other providers are unchanged: the popup still starts with the public callback.
	w = call("/api/extensions/mcp/plain/oauth/start", "https://tofi.example", "{}")
	if w.Code != 200 {
		t.Fatalf("plain popup start: %d %s", w.Code, w.Body)
	}
	// Paste-back is not offered to them.
	if w = call("/api/extensions/mcp/plain/oauth/paste/start", "", "{}"); w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_paste_not_needed") {
		t.Fatalf("plain paste start: %d %s", w.Code, w.Body)
	}

	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)

	start := func() (sid, state string) {
		w := call("/api/extensions/mcp/broker/oauth/paste/start", "", "{}")
		if w.Code != 200 {
			t.Fatalf("paste start: %d %s", w.Code, w.Body)
		}
		var out struct {
			SessionID        string `json:"session_id"`
			AuthorizationURL string `json:"authorization_url"`
			RedirectURI      string `json:"redirect_uri"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.RedirectURI != desktopOAuthRedirect {
			t.Fatalf("paste start body: %v %s", err, w.Body)
		}
		u, _ := url.Parse(out.AuthorizationURL)
		if u.Query().Get("redirect_uri") != desktopOAuthRedirect || u.Query().Get("code_challenge_method") != "S256" {
			t.Fatalf("authorization URL: %s", out.AuthorizationURL)
		}
		return out.SessionID, u.Query().Get("state")
	}
	complete := func(sid, pasted string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"session_id": sid, "url": pasted})
		return call("/api/extensions/mcp/broker/oauth/paste/complete", "", string(body))
	}
	sid, state := start()
	p.mu.Lock()
	var first []string
	for _, uri := range p.registered {
		if !strings.Contains(uri, "/plain/") { // the unchanged provider's own public callback
			first = append(first, uri)
		}
	}
	p.mu.Unlock()
	if len(first) != 1 || first[0] != desktopOAuthRedirect {
		t.Fatalf("registered redirects: %v", first)
	}
	secret := "secret-code-value-1234"
	for name, pasted := range map[string]string{
		"wrong host":  "http://127.0.0.1:9/oauth/callback?code=" + secret + "&state=" + state,
		"wrong path":  "http://127.0.0.1:43821/other?code=" + secret + "&state=" + state,
		"wrong state": desktopOAuthRedirect + "?code=" + secret + "&state=not-the-state",
		"public":      "https://tofi.example/api/extensions/mcp/broker/oauth/callback?code=" + secret + "&state=" + state,
	} {
		w := complete(sid, pasted)
		if w.Code != 400 || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "not-the-state") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	p.mu.Lock()
	exchanged := len(p.exchged)
	p.mu.Unlock()
	if exchanged != 0 {
		t.Fatal("a rejected paste reached the token endpoint")
	}
	// The session survived the failed pastes; the right address completes it once, with PKCE and the registered redirect.
	if w := complete(sid, desktopOAuthRedirect+"?code=synthetic-code&state="+state); w.Code != 200 {
		t.Fatalf("complete: %d %s", w.Code, w.Body)
	}
	p.mu.Lock()
	ex := append([]string(nil), p.exchged...)
	p.mu.Unlock()
	if len(ex) != 1 || ex[0] != desktopOAuthRedirect {
		t.Fatalf("exchange redirect: %v", ex)
	}
	if w := complete(sid, desktopOAuthRedirect+"?code=synthetic-code&state="+state); w.Code != 400 {
		t.Fatalf("replay accepted: %d", w.Code)
	}
	// A provider refusal ends the session without an exchange.
	sid, state = start()
	if w := complete(sid, desktopOAuthRedirect+"?error=access_denied&state="+state); w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_denied") {
		t.Fatalf("denied: %d %s", w.Code, w.Body)
	}
	// The pasted URL, its code and its state are in no log line.
	for _, leak := range []string{secret, "not-the-state", state, "synthetic-code"} {
		if strings.Contains(logs.String(), leak) {
			t.Fatalf("log contains pasted material %q: %s", leak, logs.String())
		}
	}
}

func TestPasteBackRoutesRequireOwnerSession(t *testing.T) {
	for _, path := range []string{"/api/extensions/mcp/broker/oauth/paste/start", "/api/extensions/mcp/broker/oauth/paste/complete"} {
		r := httptest.NewRequest("POST", path, nil)
		if ownerIndependentRoute(r) {
			t.Fatalf("paste-back route must require owner authentication: %s", path)
		}
	}
}

// A custom MCP that was not marked in the catalog: its registration refuses the public callback by error code. The connection
// is then marked loopback-only (remembered), the https start answers 409 oauth_loopback_required, and paste-back works.
func TestRegistrationRefusalOfPublicRedirectFallsBackToLoopback(t *testing.T) {
	p := newLoopbackProvider(t)
	p.refusePublic = true
	s, err := NewServer(Config{DataDir: t.TempDir(), PublicOrigin: "https://tofi.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.extensions.SaveMCP("custom", extensions.MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &extensions.OAuthConfig{AuthServerMetadataURL: p.srv.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	call := func(path, origin, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		if !s.routeExtensions(w, r, strings.TrimPrefix(r.URL.Path, "/api/")) {
			t.Fatalf("unhandled %s", path)
		}
		return w
	}
	if s.extensions.OAuthLoopbackOnly("custom") {
		t.Fatal("the connection started out marked")
	}
	w := call("/api/extensions/mcp/custom/oauth/start", "https://tofi.example", "{}")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "oauth_loopback_required") || strings.Contains(w.Body.String(), "wording that is never read") {
		t.Fatalf("fallback signal: %d %s", w.Code, w.Body)
	}
	if !s.extensions.OAuthLoopbackOnly("custom") {
		t.Fatal("the choice was not remembered")
	}
	// Remembered: the next attempt never even tries the public redirect.
	p.mu.Lock()
	before := len(p.registered)
	p.mu.Unlock()
	if w = call("/api/extensions/mcp/custom/oauth/start", "https://tofi.example", "{}"); w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_loopback_only") {
		t.Fatalf("second https start: %d %s", w.Code, w.Body)
	}
	if w = call("/api/extensions/mcp/custom/oauth/paste/start", "", "{}"); w.Code != 200 {
		t.Fatalf("paste start: %d %s", w.Code, w.Body)
	}
	p.mu.Lock()
	after := append([]string(nil), p.registered[before:]...)
	p.mu.Unlock()
	if len(after) != 1 || after[0] != desktopOAuthRedirect {
		t.Fatalf("registrations after the fallback: %v", after)
	}
	// A different error code is not a fallback trigger.
	other := newLoopbackProvider(t)
	if err := s.extensions.SaveMCP("other", extensions.MCPServerConfig{URL: other.srv.URL + "/mcp", OAuth: &extensions.OAuthConfig{AuthServerMetadataURL: other.srv.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	other.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/metadata" {
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": other.srv.URL + "/a", "token_endpoint": other.srv.URL + "/t", "registration_endpoint": other.srv.URL + "/register"})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client_metadata", "error_description": "redirect_uris is wrong"})
	})
	if w = call("/api/extensions/mcp/other/oauth/start", "https://tofi.example", "{}"); w.Code != 400 || s.extensions.OAuthLoopbackOnly("other") {
		t.Fatalf("a different registration error must not mark the connection (wording is never read): %d %s", w.Code, w.Body)
	}
}

// RFC 9207: when the server advertises `iss`, a pasted response must carry exactly that issuer.
func TestPasteBackChecksIssuerWhenAdvertised(t *testing.T) {
	for _, advertised := range []bool{true, false} {
		p := newLoopbackProvider(t)
		p.advertiseIss = advertised
		s, err := NewServer(Config{DataDir: t.TempDir(), PublicOrigin: "https://tofi.example"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.extensions.SaveMCP("broker", extensions.MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &extensions.OAuthConfig{AuthServerMetadataURL: p.srv.URL + "/metadata", RedirectPolicy: extensions.RedirectPolicyLoopback}}, false); err != nil {
			t.Fatal(err)
		}
		call := func(path, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("POST", path, strings.NewReader(body))
			w := httptest.NewRecorder()
			s.routeExtensions(w, r, strings.TrimPrefix(r.URL.Path, "/api/"))
			return w
		}
		start := func() (string, string) {
			w := call("/api/extensions/mcp/broker/oauth/paste/start", "{}")
			var out struct {
				SessionID        string `json:"session_id"`
				AuthorizationURL string `json:"authorization_url"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 {
				t.Fatalf("start: %d %s", w.Code, w.Body)
			}
			u, _ := url.Parse(out.AuthorizationURL)
			return out.SessionID, u.Query().Get("state")
		}
		complete := func(sid, query string) *httptest.ResponseRecorder {
			body, _ := json.Marshal(map[string]string{"session_id": sid, "url": desktopOAuthRedirect + "?" + query})
			return call("/api/extensions/mcp/broker/oauth/paste/complete", string(body))
		}
		sid, state := start()
		if advertised {
			for _, name := range []string{"missing iss", "wrong iss"} {
				query := "code=synthetic-code&state=" + state
				if name == "wrong iss" {
					query += "&iss=" + url.QueryEscape("https://other-issuer.example.test")
				}
				w := complete(sid, query)
				if w.Code != 400 || !strings.Contains(w.Body.String(), "oauth_issuer_mismatch") {
					t.Fatalf("%s: %d %s", name, w.Code, w.Body)
				}
				// A mismatch drops the flow: the right response can no longer be used with this session.
				if w = complete(sid, "code=synthetic-code&state="+state+"&iss="+url.QueryEscape(p.srv.URL)); w.Code != 400 {
					t.Fatalf("%s: session survived the mismatch: %d", name, w.Code)
				}
				sid, state = start()
			}
			if w := complete(sid, "code=synthetic-code&state="+state+"&iss="+url.QueryEscape(p.srv.URL)); w.Code != 200 {
				t.Fatalf("right issuer: %d %s", w.Code, w.Body)
			}
		} else if w := complete(sid, "code=synthetic-code&state="+state); w.Code != 200 {
			t.Fatalf("a server that does not advertise iss needs none: %d %s", w.Code, w.Body)
		}
		s.Close()
	}
}
