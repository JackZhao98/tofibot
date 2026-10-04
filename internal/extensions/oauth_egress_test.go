package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func trapOAuthDefault(t *testing.T) *atomic.Int32 {
	t.Helper()
	calls := new(atomic.Int32)
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: oauthDestinationTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("default client trap")
	})}
	t.Cleanup(func() {
		http.DefaultClient = old
		if calls.Load() != 0 {
			t.Errorf("hosted request reached DefaultClient %d times", calls.Load())
		}
	})
	return calls
}

func TestHostedOAuthDiscoveryDenialsAreTerminal(t *testing.T) {
	trapOAuthDefault(t)
	for _, stage := range []string{"prm-private", "prm-redirect", "issuer-private", "issuer-redirect", "origin-redirect", "explicit-private", "explicit-redirect"} {
		t.Run(stage, func(t *testing.T) {
			requests := 0
			client, _ := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
				requests++
				if stage == "prm-redirect" || stage == "explicit-redirect" || (stage == "issuer-redirect" && r.Host == "issuer.fixture.test") || (stage == "origin-redirect" && strings.Contains(r.URL.Path, "oauth-authorization-server")) {
					return 302, http.Header{"Location": []string{"https://blocked.fixture.test/metadata?secret=synthetic-secret"}}, ""
				}
				if strings.Contains(r.URL.Path, "oauth-protected-resource") {
					if stage == "issuer-private" {
						return 200, nil, `{"authorization_servers":["https://blocked.fixture.test"]}`
					}
					if stage == "issuer-redirect" {
						return 200, nil, `{"authorization_servers":["https://issuer.fixture.test"]}`
					}
				}
				return 404, nil, ""
			})
			endpoint := "https://public.fixture.test/mcp"
			cfg := OAuthFlowConfig{HTTPClient: client, ClientID: "synthetic-client", RedirectURI: "http://localhost/callback", TokenStore: NewMemoryTokenStore()}
			if stage == "prm-private" {
				endpoint = "https://blocked.fixture.test/mcp"
			}
			if stage == "explicit-private" {
				cfg.AuthServerMetadataURL = "https://blocked.fixture.test/metadata?secret=synthetic-secret"
			}
			if stage == "explicit-redirect" {
				cfg.AuthServerMetadataURL = "https://public.fixture.test/metadata"
			}
			_, err := StartOAuth(context.Background(), cfg, endpoint)
			if !outboundPolicyDenied(err) {
				t.Fatalf("policy failure swallowed: %v", err)
			}
			public := OAuthPublicError(err)
			if !strings.Contains(public, "network policy") || strings.Contains(public, "synthetic-secret") || strings.Contains(public, "fixture.test") || strings.Contains(public, "169.254") {
				t.Fatal("unsafe/misleading public error")
			}
			if (stage == "prm-private" || stage == "explicit-private") && requests != 0 {
				t.Fatal("private metadata reached fixture")
			}
			if (stage == "prm-redirect" || stage == "explicit-redirect" || stage == "issuer-private") && requests != 1 {
				t.Fatal("policy denial caused discovery fallback")
			}
		})
	}
}

func TestHostedOAuthCachedMetadataGuardsEveryGrant(t *testing.T) {
	trapOAuthDefault(t)
	for _, operation := range []string{"dcr", "exchange", "refresh"} {
		for _, deny := range []string{"private", "redirect"} {
			t.Run(operation+"/"+deny, func(t *testing.T) {
				calls := 0
				client, _ := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
					calls++
					_ = r.ParseForm()
					return 307, http.Header{"Location": []string{"https://blocked.fixture.test/steal"}}, ""
				})
				store := NewMemoryTokenStore()
				endpoint := "https://public.fixture.test/grant"
				if deny == "private" {
					endpoint = "https://blocked.fixture.test/grant"
				}
				h := NewOAuthHandler(OAuthFlowConfig{HTTPClient: client, ClientID: "synthetic-client", ClientSecret: "synthetic-secret", RedirectURI: "http://localhost/callback", TokenStore: store})
				h.SetBaseURL("https://public.fixture.test/mcp")
				h.SetExpectedState("synthetic-state")
				h.metadata = &AuthServerMetadata{AuthorizationEndpoint: "https://public.fixture.test/auth", TokenEndpoint: endpoint, RegistrationEndpoint: endpoint}
				// A caller-supplied context client cannot override the hosted client.
				ctx := context.WithValue(context.Background(), oauth2.HTTPClient, http.DefaultClient)
				var err error
				switch operation {
				case "dcr":
					err = h.RegisterClient(ctx, "synthetic")
				case "exchange":
					err = h.ProcessAuthorizationResponse(ctx, "synthetic-code", "synthetic-state", "synthetic-verifier")
				case "refresh":
					_, err = h.RefreshToken(ctx, "synthetic-refresh")
				}
				if !outboundPolicyDenied(err) {
					t.Fatalf("cached grant escaped: %v", err)
				}
				want := 0
				if deny == "redirect" {
					want = 1
				}
				if calls != want {
					t.Fatalf("calls=%d want=%d", calls, want)
				}
				if _, err := store.GetToken(ctx); !errors.Is(err, ErrNoToken) {
					t.Fatal("denied grant published token")
				}
			})
		}
	}
}

func TestHostedOAuthManagerConstructorAndCredentialMatrix(t *testing.T) {
	trapOAuthDefault(t)
	for _, branch := range []string{"restored", "resolved-metadata", "dynamic-client"} {
		t.Run(branch, func(t *testing.T) {
			var deny atomic.Bool
			var refreshes, exchanges, registrations atomic.Int32
			client, _ := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
				if strings.Contains(r.URL.Path, "oauth-protected-resource") {
					return 404, nil, ""
				}
				if r.URL.Path == "/metadata" || strings.Contains(r.URL.Path, "oauth-authorization-server") {
					return 200, nil, `{"authorization_endpoint":"https://public.fixture.test/auth","token_endpoint":"https://public.fixture.test/token","registration_endpoint":"https://public.fixture.test/register","revocation_endpoint":"https://public.fixture.test/revoke"}`
				}
				switch r.URL.Path {
				case "/register":
					registrations.Add(1)
					return 201, http.Header{"Content-Type": []string{"application/json"}}, `{"client_id":"synthetic-dynamic-client","client_secret":"synthetic-dynamic-secret"}`
				case "/token":
					_ = r.ParseForm()
					if r.Form.Get("grant_type") == "refresh_token" {
						refreshes.Add(1)
						if deny.Load() {
							return 308, http.Header{"Location": []string{"https://blocked.fixture.test/token"}}, ""
						}
						if r.Form.Get("refresh_token") != "synthetic-refresh" {
							t.Error("lost refresh credential")
						}
					} else {
						exchanges.Add(1)
					}
					return 200, http.Header{"Content-Type": []string{"application/json"}}, `{"access_token":"synthetic-access","refresh_token":"synthetic-refresh","token_type":"Bearer","expires_in":3600}`
				case "/revoke":
					_ = r.ParseForm()
					return 307, http.Header{"Location": []string{"https://blocked.fixture.test/steal"}}, ""
				}
				t.Errorf("unexpected OAuth fixture path %s", r.URL.Path)
				return 404, nil, ""
			})
			m := NewManager(Config{HostedEgress: true, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			m.CloseIdleConnections()
			m.httpClient = client
			t.Cleanup(m.CloseIdleConnections)
			cfg := MCPServerConfig{URL: "https://public.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-client", ClientSecret: "synthetic-secret", AuthServerMetadataURL: "https://public.fixture.test/metadata"}}
			if branch == "resolved-metadata" {
				cfg.OAuth.AuthServerMetadataURL = ""
			}
			if branch == "dynamic-client" {
				cfg.OAuth.ClientID = ""
				cfg.OAuth.ClientSecret = ""
			}
			if err := m.SaveMCP("local_spoof", cfg, false); err != nil {
				t.Fatal(err)
			}
			if branch != "restored" {
				sid, auth, err := m.OAuthStart(context.Background(), "local_spoof", "http://localhost/callback")
				if err != nil {
					t.Fatal(err)
				}
				u, _ := url.Parse(auth)
				if err := m.OAuthCallback(context.Background(), sid, "synthetic-code", u.Query().Get("state")); err != nil {
					t.Fatal(err)
				}
				if exchanges.Load() != 1 {
					t.Fatal("exchange did not use explicit client")
				}
			}
			live, err := loadServers(m.cfg.MCPConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			if branch == "dynamic-client" && (registrations.Load() != 1 || live["local_spoof"].OAuth.ClientID != "synthetic-dynamic-client") {
				t.Fatal("dynamic client commit lost")
			}
			store := m.tokenStore("local_spoof", live["local_spoof"])
			expired := &Token{AccessToken: "synthetic-old", RefreshToken: "synthetic-refresh", ExpiresAt: time.Now().Add(-time.Hour)}
			if err := store.SaveToken(context.Background(), expired); err != nil {
				t.Fatal(err)
			}
			if _, err := store.GetToken(context.Background()); err != nil {
				t.Fatal(err)
			}
			if refreshes.Load() != 1 {
				t.Fatal("refresh client missing")
			}
			if err := store.SaveToken(context.Background(), expired); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(store.Path)
			deny.Store(true)
			if _, err := store.GetToken(context.Background()); !errors.Is(err, ErrOutboundRedirectDenied) {
				t.Fatalf("denied refresh escaped: %v", err)
			}
			after, _ := os.ReadFile(store.Path)
			if string(before) != string(after) {
				t.Fatal("denied refresh changed credential generation")
			}
			if err := m.OAuthDisconnect("local_spoof"); !errors.Is(err, ErrOutboundRedirectDenied) {
				t.Fatalf("revoke denial not surfaced: %v", err)
			}
			if _, err := os.Stat(store.Path); !errors.Is(err, os.ErrNotExist) || store.active.Load() {
				t.Fatal("denied revoke restored local credentials")
			}
		})
	}
}

func TestHostedOAuthDisconnectPrivateMetadataInvalidatesFirst(t *testing.T) {
	trapOAuthDefault(t)
	m := NewManager(Config{HostedEgress: true, MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	defer m.CloseIdleConnections()
	cfg := MCPServerConfig{URL: "https://public.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-client", AuthServerMetadataURL: "https://169.254.169.254/metadata"}}
	if err := m.SaveMCP("fixture", cfg, false); err != nil {
		t.Fatal(err)
	}
	store := m.tokenStore("fixture", cfg)
	if err := store.SaveToken(context.Background(), &Token{AccessToken: "synthetic-token"}); err != nil {
		t.Fatal(err)
	}
	if err := m.OAuthDisconnect("fixture"); !errors.Is(err, ErrOutboundDestinationDenied) {
		t.Fatalf("metadata denial not surfaced: %v", err)
	}
	if _, err := os.Stat(store.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disconnect left token")
	}
}

func TestHostedOAuthAdvertisedIssuerUsesGuardedClient(t *testing.T) {
	trapOAuthDefault(t)
	calls := 0
	client, _ := syntheticPublicClient(t, func(r *http.Request) (int, http.Header, string) {
		calls++
		if strings.Contains(r.URL.Path, "oauth-protected-resource") {
			return 200, nil, `{"authorization_servers":["https://issuer.fixture.test/tenant"]}`
		}
		if r.Host == "issuer.fixture.test" {
			return 200, nil, `{"authorization_endpoint":"https://issuer.fixture.test/auth","token_endpoint":"https://issuer.fixture.test/token"}`
		}
		return 404, nil, ""
	})
	md, where, err := discoverOAuthMetadata(context.Background(), "https://public.fixture.test/mcp", client)
	if err != nil || md.TokenEndpoint == "" || !strings.Contains(where, "issuer.fixture.test") || calls != 2 {
		t.Fatalf("guarded issuer discovery failed: %v", err)
	}
	// Ensure the metadata helper parses through the actual guarded HTTP sink.
	var parsed map[string]any
	_, err = fetchOAuthJSON(context.Background(), where, &parsed, client)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(parsed); !strings.Contains(string(b), "token_endpoint") {
		t.Fatal(fmt.Sprint(parsed))
	}
}
