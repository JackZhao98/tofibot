package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestOAuthWebCallbackBindsIdentityAndChannel(t *testing.T) {
	var exchanges atomic.Int32
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"authorization_endpoint": provider.URL + "/authorize", "token_endpoint": provider.URL + "/token", "code_challenge_methods_supported": []string{"S256"}})
		case "/token":
			exchanges.Add(1)
			_ = r.ParseForm()
			if r.Form.Get("code") != "synthetic-code" || r.Form.Get("code_verifier") == "" {
				t.Error("code/PKCE missing")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-token", "token_type": "bearer", "expires_in": 3600})
		}
	}))
	defer provider.Close()
	dir := t.TempDir()
	m := NewManager(Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: filepath.Join(dir, "skills")})
	if err := m.SaveMCP("service", MCPServerConfig{URL: provider.URL, OAuth: &OAuthConfig{ClientID: "fixture", AuthServerMetadataURL: provider.URL + "/.well-known/oauth-authorization-server"}}, false); err != nil {
		t.Fatal(err)
	}
	const redirect = "https://tofi.example/api/extensions/mcp/service/oauth/callback"
	start := func() (string, string) {
		t.Helper()
		sid, auth, err := m.OAuthStart(context.Background(), "service", redirect)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(auth)
		return sid, u.Query().Get("state")
	}
	sid, state := start()
	for _, tc := range []struct {
		name, sid, state, redirect string
		denied                     bool
	}{
		{"other", sid, state, redirect, false},
		{"service", sid, "wrong", redirect, true},
		{"service", "wrong", state, redirect, true},
		{"service", sid, state, "http://127.0.0.1:43821/oauth/callback", false},
		{"service", sid, state, "", false},
	} {
		identity, err := m.OAuthWebCallback(context.Background(), tc.name, tc.sid, "synthetic-code", tc.state, tc.redirect, tc.denied)
		if err == nil || identity.Name != "" || identity.URL != "" {
			t.Fatalf("unverified identity: %+v %v", identity, err)
		}
	}
	identity, err := m.OAuthWebCallback(context.Background(), "service", "", "synthetic-code", state, redirect, false)
	if err != nil || identity.Name != "service" || identity.URL != provider.URL || exchanges.Load() != 1 {
		t.Fatalf("completion %+v %v", identity, err)
	}
	if identity, err = m.OAuthWebCallback(context.Background(), "service", sid, "synthetic-code", state, redirect, false); err == nil || identity.Name != "" || exchanges.Load() != 1 {
		t.Fatal("callback replay accepted")
	}

	sid, state = start()
	if identity, err = m.OAuthWebCallback(context.Background(), "service", sid, "", state, redirect, true); !errors.Is(err, ErrOAuthDenied) || identity.Name != "service" || exchanges.Load() != 1 {
		t.Fatalf("denied: %+v %v", identity, err)
	}
	if _, err = m.OAuthWebCallback(context.Background(), "service", sid, "synthetic-code", state, redirect, false); err == nil {
		t.Fatal("denied flow reused")
	}

	sid, state = start()
	m.mu.Lock()
	session := m.oauth[sid]
	session.Expires = time.Now().Add(-time.Minute)
	m.oauth[sid] = session
	m.mu.Unlock()
	if identity, err = m.OAuthWebCallback(context.Background(), "service", sid, "synthetic-code", state, redirect, false); err == nil || identity.Name != "" {
		t.Fatal("expired identity leaked")
	}
}
