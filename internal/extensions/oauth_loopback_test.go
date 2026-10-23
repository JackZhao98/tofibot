package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

// A synthetic provider that records every dynamic client registration it sees.
type registrationProbe struct {
	mu   sync.Mutex
	uris [][]string
	srv  *httptest.Server
}

func newRegistrationProbe(t *testing.T) *registrationProbe {
	t.Helper()
	p := &registrationProbe{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token", "registration_endpoint": p.srv.URL + "/register"})
		case "/register":
			var in struct {
				RedirectURIs []string `json:"redirect_uris"`
			}
			_ = json.NewDecoder(r.Body).Decode(&in)
			p.mu.Lock()
			p.uris = append(p.uris, in.RedirectURIs)
			p.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "synthetic-client"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *registrationProbe) registrations() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]string(nil), p.uris...)
}

func TestLoopbackOnlyConnectionRegistersAndUsesOnlyLoopbackRedirects(t *testing.T) {
	p := newRegistrationProbe(t)
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("broker", MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: p.srv.URL + "/metadata", RedirectPolicy: RedirectPolicyLoopback}}, false); err != nil {
		t.Fatal(err)
	}
	if !m.OAuthLoopbackOnly("broker") || m.OAuthLoopbackOnly("missing") {
		t.Fatal("the catalog property was not read back")
	}
	// None of these may reach the provider's registration endpoint.
	for _, redirect := range []string{
		"https://tofi.example/api/extensions/mcp/broker/oauth/callback",
		"http://localhost:43821/oauth/callback",
		"http://[::1]:43821/oauth/callback",
		"http://127.0.0.1.evil.example/oauth/callback",
		"http://user@127.0.0.1:43821/oauth/callback",
		"http://127.0.0.1:43821/oauth/callback?x=1",
	} {
		if _, _, err := m.OAuthStart(context.Background(), "broker", redirect); err == nil {
			t.Fatalf("%s: want a refusal, got %v", redirect, err)
		}
	}
	if got := p.registrations(); len(got) != 0 {
		t.Fatalf("a non-loopback redirect reached registration: %v", got)
	}
	for _, redirect := range []string{"http://127.0.0.1:43821/oauth/callback", "http://127.0.0.1:41999/oauth/callback/token"} {
		if _, _, err := m.OAuthStart(context.Background(), "broker", redirect); err != nil {
			t.Fatalf("%s: %v", redirect, err)
		}
	}
	got := p.registrations()
	if len(got) != 2 || len(got[0]) != 1 || got[0][0] != "http://127.0.0.1:43821/oauth/callback" || len(got[1]) != 1 || got[1][0] != "http://127.0.0.1:41999/oauth/callback/token" {
		t.Fatalf("registered redirects: %v", got)
	}
}

func TestLoopbackHandlerRefusesAnyOtherRedirectEvenWithoutManager(t *testing.T) {
	p := newRegistrationProbe(t)
	_, err := StartOAuth(context.Background(), OAuthFlowConfig{RedirectURI: "https://tofi.example/cb", AuthServerMetadataURL: p.srv.URL + "/metadata", LoopbackOnly: true, TokenStore: NewMemoryTokenStore()}, p.srv.URL+"/mcp")
	if !errors.Is(err, ErrOAuthLoopbackOnly) || len(p.registrations()) != 0 {
		t.Fatalf("flow accepted a public redirect: %v %v", err, p.registrations())
	}
	h := NewOAuthHandler(OAuthFlowConfig{RedirectURI: "https://tofi.example/cb", AuthServerMetadataURL: p.srv.URL + "/metadata", LoopbackOnly: true})
	if err := h.RegisterClient(context.Background(), "Tofi"); !errors.Is(err, ErrOAuthLoopbackOnly) || len(p.registrations()) != 0 {
		t.Fatalf("handler registered a public redirect: %v", err)
	}
}

func TestOtherProvidersKeepTheirRedirects(t *testing.T) {
	p := newRegistrationProbe(t)
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("plain", MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: p.srv.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	if m.OAuthLoopbackOnly("plain") {
		t.Fatal("an unmarked connection was treated as loopback-only")
	}
	const public = "https://tofi.example/api/extensions/mcp/plain/oauth/callback"
	if _, _, err := m.OAuthStart(context.Background(), "plain", public); err != nil {
		t.Fatal(err)
	}
	if got := p.registrations(); len(got) != 1 || got[0][0] != public {
		t.Fatalf("registered: %v", got)
	}
}

func TestRedirectPolicyIsValidatedAndSurvivesAnEditWithoutIt(t *testing.T) {
	p := newRegistrationProbe(t)
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("x", MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &OAuthConfig{RedirectPolicy: "anything"}}, false); err == nil {
		t.Fatal("an unknown policy was accepted")
	}
	if err := m.SaveMCP("x", MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &OAuthConfig{RedirectPolicy: RedirectPolicyLoopback}}, false); err != nil {
		t.Fatal(err)
	}
	// An edit that omits the property keeps it while the address is the same.
	if err := m.SaveMCP("x", MCPServerConfig{URL: p.srv.URL + "/mcp", OAuth: &OAuthConfig{Scopes: []string{"a"}}}, true); err != nil {
		t.Fatal(err)
	}
	if !m.OAuthLoopbackOnly("x") {
		t.Fatal("an edit dropped the loopback restriction")
	}
	views, _ := m.ListMCP()
	if len(views) != 1 || views[0].OAuth == nil || views[0].OAuth.RedirectPolicy != RedirectPolicyLoopback {
		t.Fatalf("the list does not report the policy: %#v", views)
	}
}
