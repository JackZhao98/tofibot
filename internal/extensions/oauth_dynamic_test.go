package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthPublicErrorDoesNotExposeProviderDetails(t *testing.T) {
	secret := "fixture-client-secret"
	got := OAuthPublicError(errors.New("registration failed at https://provider.invalid/register?client_secret=" + secret))
	if got == "" || strings.Contains(got, secret) || strings.Contains(got, "provider.invalid") {
		t.Fatalf("public OAuth error leaked provider details: %q", got)
	}
}

func TestOAuthAuthorizationRequiredClassification(t *testing.T) {
	if !oauthAuthorizationRequired(ErrOAuthAuthorizationRequired) || !oauthAuthorizationRequired(ErrAuthorizationRequired) || !oauthAuthorizationRequired(ErrNoToken) {
		t.Fatal("OAuth sentinel errors were not classified as authorization required")
	}
	if oauthAuthorizationRequired(errors.New("connection refused")) {
		t.Fatal("generic connection failure was classified as authorization required")
	}
}

func TestOAuthDiscoveryFailsClosedForAdvertisedIssuer(t *testing.T) {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{"https://issuer.invalid"}})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	_, err := StartOAuth(context.Background(), OAuthFlowConfig{ClientID: "client", RedirectURI: "http://localhost/callback", TokenStore: NewMemoryTokenStore()}, s.URL+"/mcp")
	if !errors.Is(err, errOAuthAdvertisedIssuerUnavailable) {
		t.Fatalf("expected advertised issuer failure, got %v", err)
	}
}

func TestStartOAuthRejectsUnavailableOrIncompleteMetadata(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusOK} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				if status == http.StatusOK {
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer s.Close()
			_, err := StartOAuth(context.Background(), OAuthFlowConfig{ClientID: "client", RedirectURI: "http://localhost/callback", AuthServerMetadataURL: s.URL + "/metadata", TokenStore: NewMemoryTokenStore()}, s.URL+"/mcp")
			if err == nil {
				t.Fatal("expected metadata validation error")
			}
		})
	}
}

// This exercises the provider-side DCR contract locally. No provider account
// or credential is used; the returned client secret is synthetic.
func TestStartOAuthDynamicallyRegistersAndCarriesCredentials(t *testing.T) {
	var registrations atomic.Int32
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": s.URL + "/authorize",
				"token_endpoint":         s.URL + "/token",
				"registration_endpoint":  s.URL + "/register",
			})
		case "/register":
			registrations.Add(1)
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "dynamic-client", "client_secret": "dynamic-secret"})
		case "/authorize":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()

	redirect := s.URL + "/callback"
	flow, err := StartOAuth(context.Background(), OAuthFlowConfig{
		RedirectURI:           redirect,
		AuthServerMetadataURL: s.URL + "/metadata",
		TokenStore:            NewMemoryTokenStore(),
		PKCEEnabled:           true,
	}, s.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !flow.RegisteredClient || flow.ClientID != "dynamic-client" || flow.ClientSecret != "dynamic-secret" {
		t.Fatalf("unexpected dynamic client: %#v", flow)
	}
	if registrations.Load() != 1 {
		t.Fatalf("registration calls=%d", registrations.Load())
	}
	if _, err := url.Parse(flow.Handler.GetClientID()); err != nil {
		t.Fatal(err)
	}
}

func TestStartOAuthDiscoversOriginMetadataForPathResource(t *testing.T) {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": s.URL + "/authorize",
				"token_endpoint":         s.URL + "/token",
				"registration_endpoint":  s.URL + "/register",
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "origin-client"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()

	flow, err := StartOAuth(context.Background(), OAuthFlowConfig{
		RedirectURI: s.URL + "/callback",
		TokenStore:  NewMemoryTokenStore(),
	}, s.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if !flow.RegisteredClient || flow.ClientID != "origin-client" {
		t.Fatalf("unexpected dynamically registered client: %#v", flow)
	}
}

func TestStartOAuthRejectsEmptyClientWithoutRegistration(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "https://provider.invalid/authorize",
			"token_endpoint":         "https://provider.invalid/token",
		})
	}))
	defer s.Close()
	_, err := StartOAuth(context.Background(), OAuthFlowConfig{
		RedirectURI:           s.URL + "/callback",
		AuthServerMetadataURL: s.URL + "/metadata",
		TokenStore:            NewMemoryTokenStore(),
	}, s.URL+"/mcp")
	if err == nil {
		t.Fatal("expected missing dynamic registration error")
	}
}

func TestGoogleAuthorizationURLRequestsOfflineRefresh(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "https://accounts.google.com/o/oauth2/v2/auth",
			"token_endpoint":         "https://oauth2.googleapis.com/token",
		})
	}))
	defer s.Close()
	flow, err := StartOAuth(context.Background(), OAuthFlowConfig{
		ClientID:              "google-client",
		RedirectURI:           s.URL + "/callback",
		AuthServerMetadataURL: s.URL + "/metadata",
		TokenStore:            NewMemoryTokenStore(),
		PKCEEnabled:           true,
	}, s.URL+"/mcp")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := flow.AuthorizationURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for key, want := range map[string]string{"access_type": "offline", "include_granted_scopes": "true", "prompt": "consent"} {
		if q.Get(key) != want {
			t.Fatalf("%s=%q, want %q", key, q.Get(key), want)
		}
	}
}

func TestManagerOAuthStartPersistsRegisteredClient(t *testing.T) {
	var s *httptest.Server
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"authorization_endpoint": s.URL + "/authorize",
				"token_endpoint":         s.URL + "/token",
				"registration_endpoint":  s.URL + "/register",
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "persisted-client", "client_secret": "persisted-secret"})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()

	path := filepath.Join(t.TempDir(), "mcp.json")
	m := NewManager(Config{MCPConfigPath: path})
	if err := m.SaveMCP("notion", MCPServerConfig{URL: s.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: s.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	sid, authURL, err := m.OAuthStart(context.Background(), "notion", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if err := m.OAuthCallback(context.Background(), sid, "authorization-code", u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	servers, err := loadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := servers["notion"].OAuth; got == nil || got.ClientID != "persisted-client" || got.ClientSecret != "persisted-secret" {
		t.Fatalf("registered credentials were not persisted: %#v", got)
	}
}

func TestManagerConcurrentDynamicRegistrationKeepsWinningGeneration(t *testing.T) {
	var s *httptest.Server
	var registrations atomic.Int32
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token", "registration_endpoint": s.URL + "/register"})
		case "/register":
			n := registrations.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "winner-" + strconv.Itoa(int(n)), "client_secret": "secret"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	m := NewManager(Config{MCPConfigPath: path})
	if err := m.SaveMCP("concurrent", MCPServerConfig{URL: s.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: s.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := m.OAuthStart(context.Background(), "concurrent", "http://localhost/callback")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var successes int
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes == 0 {
		t.Fatalf("successes=%d, want at least one winning generation", successes)
	}
	if store := m.tokenStores["concurrent"]; store == nil || !store.active.Load() {
		t.Fatal("winning token store was invalidated by stale contender")
	}
}

func TestDynamicRegistrationCallbackRefreshesAfterManagerReload(t *testing.T) {
	var s *httptest.Server
	var tokenCalls atomic.Int32
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token", "registration_endpoint": s.URL + "/register"})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "callback-client", "client_secret": "callback-secret"})
		case "/token":
			n := tokenCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-" + strconv.Itoa(int(n)), "token_type": "Bearer", "refresh_token": "refresh-token", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	m := NewManager(Config{MCPConfigPath: path})
	if err := m.SaveMCP("reload", MCPServerConfig{URL: s.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: s.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	sid, authURL, err := m.OAuthStart(context.Background(), "reload", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if err := m.OAuthCallback(context.Background(), sid, "authorization-code", u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	store := m.tokenStores["reload"]
	if store == nil {
		t.Fatal("missing token store")
	}
	if err := store.SaveToken(context.Background(), &Token{AccessToken: "expired", RefreshToken: "refresh-token", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	m2 := NewManager(Config{MCPConfigPath: path})
	loaded := m2.tokenStore("reload", mustLoadServer(t, path, "reload"))
	tok, err := loaded.GetToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "access-2" || tokenCalls.Load() != 2 {
		t.Fatalf("refresh after reload: token=%#v calls=%d", tok, tokenCalls.Load())
	}
}

func TestDynamicClientReregistersForChangedLoopbackRedirectWithoutOverwritingOldConfig(t *testing.T) {
	var s *httptest.Server
	var registrations atomic.Int32
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token", "registration_endpoint": s.URL + "/register"})
		case "/register":
			n := registrations.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "loopback-" + strconv.Itoa(int(n)), "client_secret": "secret"})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	m := NewManager(Config{MCPConfigPath: path})
	if err := m.SaveMCP("loopback", MCPServerConfig{URL: s.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: s.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	sid, authURL, err := m.OAuthStart(context.Background(), "loopback", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(authURL)
	if err := m.OAuthCallback(context.Background(), sid, "code", u.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	before := mustLoadServer(t, path, "loopback").OAuth
	if _, _, err := m.OAuthStart(context.Background(), "loopback", "http://localhost:4567/callback"); err != nil {
		t.Fatal(err)
	}
	after := mustLoadServer(t, path, "loopback").OAuth
	if registrations.Load() != 2 || after.ClientID != before.ClientID || after.DynamicClientRedirectURI != "http://localhost/callback" {
		t.Fatalf("unexpected re-registration state: registrations=%d before=%#v after=%#v", registrations.Load(), before, after)
	}
}

func TestConcurrentDynamicCallbacksCommitOnlyOneClientAndToken(t *testing.T) {
	var s *httptest.Server
	var registrations atomic.Int32
	s = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/metadata":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token", "registration_endpoint": s.URL + "/register"})
		case "/register":
			n := registrations.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"client_id": "callback-" + strconv.Itoa(int(n)), "client_secret": "secret"})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "refresh_token": "refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	m := NewManager(Config{MCPConfigPath: path})
	if err := m.SaveMCP("callbacks", MCPServerConfig{URL: s.URL + "/mcp", OAuth: &OAuthConfig{AuthServerMetadataURL: s.URL + "/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	sid1, url1, err := m.OAuthStart(context.Background(), "callbacks", "http://localhost/callback-1")
	if err != nil {
		t.Fatal(err)
	}
	sid2, url2, err := m.OAuthStart(context.Background(), "callbacks", "http://localhost/callback-2")
	if err != nil {
		t.Fatal(err)
	}
	u1, _ := url.Parse(url1)
	u2, _ := url.Parse(url2)
	if err := m.OAuthCallback(context.Background(), sid1, "code-1", u1.Query().Get("state")); err != nil {
		t.Fatal(err)
	}
	if err := m.OAuthCallback(context.Background(), sid2, "code-2", u2.Query().Get("state")); !errors.Is(err, errCredentialInvalidated) {
		t.Fatalf("second callback err=%v, want credential invalidation", err)
	}
	if got := mustLoadServer(t, path, "callbacks").OAuth; got == nil || got.ClientID != "callback-1" || got.DynamicClientRedirectURI != "http://localhost/callback-1" {
		t.Fatalf("winner overwritten: %#v", got)
	}
}

func mustLoadServer(t *testing.T, path, name string) MCPServerConfig {
	t.Helper()
	servers, err := loadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := servers[name]
	if !ok {
		t.Fatalf("server %q not found", name)
	}
	return c
}
