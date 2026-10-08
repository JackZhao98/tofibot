package extensions

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestMCPOAuthMetadataDestinationRejectsRetainedSecret(t *testing.T) {
	for _, change := range []struct{ name, old, next string }{
		{"host", "https://old.fixture.test/metadata", "https://new.fixture.test/metadata"},
		{"path", "https://old.fixture.test/metadata", "https://old.fixture.test/other"},
		{"query", "https://old.fixture.test/metadata", "https://old.fixture.test/metadata?tenant=other"},
		{"port", "https://old.fixture.test/metadata", "https://old.fixture.test:8443/metadata"},
		{"explicit-to-discovery", "https://old.fixture.test/metadata", ""},
		{"discovery-to-explicit", "", "https://new.fixture.test/metadata"},
		{"explicit-to-whitespace-discovery", "https://old.fixture.test/metadata", " "},
	} {
		t.Run(change.name, func(t *testing.T) {
			m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
			original := MCPServerConfig{URL: "https://mcp.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-client", ClientSecret: "synthetic-saved-secret", AuthServerMetadataURL: change.old}}
			if err := m.SaveMCP("fixture", original, false); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON0600(m.tokenPath("fixture"), Token{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh"}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(m.cfg.MCPConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			tokenBefore, err := os.ReadFile(m.tokenPath("fixture"))
			if err != nil {
				t.Fatal(err)
			}
			views, err := m.ListMCP()
			if err != nil {
				t.Fatal(err)
			}
			edit := original
			edit.OAuth = &OAuthConfig{ClientID: "changed-client", ClientSecret: views[0].OAuth.ClientSecret, AuthServerMetadataURL: change.next}
			if err := m.SaveMCP("fixture", edit, true); err == nil || strings.Contains(err.Error(), "synthetic-saved-secret") {
				t.Fatal("metadata destination edit implicitly retained or exposed saved secret")
			}
			after, err := os.ReadFile(m.cfg.MCPConfigPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected edit changed configuration")
			}
			tokenAfter, err := os.ReadFile(m.tokenPath("fixture"))
			if err != nil || !bytes.Equal(tokenBefore, tokenAfter) {
				t.Fatal("rejected edit invalidated saved token")
			}
		})
	}
}

func TestMCPOAuthPublicClientMetadataEdit(t *testing.T) {
	m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
	if err := m.SaveMCP("public", MCPServerConfig{URL: "https://mcp.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-public", AuthServerMetadataURL: "https://old.fixture.test/metadata"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveMCP("public", MCPServerConfig{URL: "https://mcp.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-public", ClientSecret: maskedSecret, AuthServerMetadataURL: "https://new.fixture.test/metadata"}}, true); err != nil {
		t.Fatal("public client without a saved secret should allow metadata edit")
	}
	stored, err := loadServers(m.cfg.MCPConfigPath)
	if err != nil || stored["public"].OAuth.ClientSecret != "" {
		t.Fatal("public client gained a secret")
	}
}

type oauthDestinationTransport func(*http.Request) (*http.Response, error)

func (f oauthDestinationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the production OAuthHandler sink with in-memory HTTP responses.
// No socket, live credential, provider package or MCP tool is involved.
func TestMCPOAuthMetadataDestinationActualExchange(t *testing.T) {
	for _, style := range []string{"client_secret_post", "client_secret_basic"} {
		for _, edit := range []struct {
			name, secret             string
			changed, omitted, reject bool
		}{
			{name: "changed-masked-rejected", secret: maskedSecret, changed: true, reject: true},
			{name: "same-masked-retained", secret: maskedSecret},
			{name: "same-omitted-retained", omitted: true},
			{name: "changed-explicit-replaced", secret: "synthetic-new-secret", changed: true},
			{name: "changed-explicit-cleared", changed: true},
		} {
			t.Run(style+"/"+edit.name, func(t *testing.T) {
				m := NewManager(Config{MCPConfigPath: filepath.Join(t.TempDir(), "mcp.json")})
				original := MCPServerConfig{URL: "https://mcp.fixture.test/mcp", OAuth: &OAuthConfig{ClientID: "synthetic-client", ClientSecret: "synthetic-saved-secret", AuthServerMetadataURL: "https://old.fixture.test/metadata"}}
				if err := m.SaveMCP("fixture", original, false); err != nil {
					t.Fatal(err)
				}
				next := MCPServerConfig{URL: original.URL}
				if !edit.omitted {
					next.OAuth = &OAuthConfig{ClientID: original.OAuth.ClientID, ClientSecret: edit.secret, AuthServerMetadataURL: original.OAuth.AuthServerMetadataURL}
					if edit.changed {
						next.OAuth.AuthServerMetadataURL = "https://new.fixture.test/metadata"
					}
				}
				err := m.SaveMCP("fixture", next, true)
				if (err != nil) != edit.reject {
					t.Errorf("save rejected=%v want=%v", err != nil, edit.reject)
				}
				stored, err := loadServers(m.cfg.MCPConfigPath)
				if err != nil {
					t.Fatal(err)
				}
				cfg := stored["fixture"]
				wantSecret := "synthetic-saved-secret"
				wantHost := "old.fixture.test"
				if edit.changed && !edit.reject {
					wantSecret = edit.secret
					wantHost = "new.fixture.test"
				}
				metadataCalls, tokenCalls, retainedAtNew := 0, 0, false
				client := &http.Client{Transport: oauthDestinationTransport(func(r *http.Request) (*http.Response, error) {
					body := ""
					switch r.URL.Path {
					case "/metadata":
						metadataCalls++
						body = fmt.Sprintf(`{"authorization_endpoint":"https://%s/authorize","token_endpoint":"https://%s/token","token_endpoint_auth_methods_supported":[%q],"code_challenge_methods_supported":["S256"]}`, r.URL.Host, r.URL.Host, style)
					case "/token":
						tokenCalls++
						if err := r.ParseForm(); err != nil {
							return nil, err
						}
						secret := r.Form.Get("client_secret")
						if style == "client_secret_basic" {
							_, secret, _ = r.BasicAuth()
						}
						if r.URL.Host == "new.fixture.test" && secret == "synthetic-saved-secret" {
							retainedAtNew = true
						}
						if r.URL.Host != wantHost || secret != wantSecret {
							t.Error("OAuth exchange credential destination mismatch")
						}
						body = `{"access_token":"synthetic-access-token","token_type":"Bearer","expires_in":3600}`
					default:
						return nil, fmt.Errorf("unexpected synthetic OAuth destination")
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				oldClient := http.DefaultClient
				http.DefaultClient = client
				defer func() { http.DefaultClient = oldClient }()
				h := NewOAuthHandler(OAuthFlowConfig{ClientID: cfg.OAuth.ClientID, ClientSecret: cfg.OAuth.ClientSecret, AuthServerMetadataURL: cfg.OAuth.AuthServerMetadataURL, RedirectURI: "https://tofi.fixture.test/callback", TokenStore: NewMemoryTokenStore(), PKCEEnabled: true})
				h.SetBaseURL(cfg.URL)
				h.SetExpectedState("synthetic-state")
				ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client)
				if err := h.ProcessAuthorizationResponse(ctx, "synthetic-code", "synthetic-state", "synthetic-verifier"); err != nil {
					t.Fatal(err)
				}
				if metadataCalls != 1 || tokenCalls != 1 || retainedAtNew {
					t.Fatal("unexpected OAuth calls or retained secret at new endpoint")
				}
			})
		}
	}
}
