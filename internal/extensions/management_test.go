package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPManagementMasksAndRetainsSecrets(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "mcp.json")
	m := NewManager(Config{MCPConfigPath: p, SkillsDir: filepath.Join(d, "skills")})
	if err := m.SaveMCP("demo", MCPServerConfig{URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": "secret"}}, false); err != nil {
		t.Fatal(err)
	}
	v, err := m.ListMCP()
	if err != nil || len(v) != 1 || v[0].Headers["Authorization"] != maskedSecret {
		t.Fatalf("view=%#v err=%v", v, err)
	}
	if err := m.SaveMCP("demo", MCPServerConfig{URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": maskedSecret, "X-Test": "ok"}}, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "secret") || strings.Contains(s, maskedSecret) {
		t.Fatalf("stored config leaked/masked: %s", s)
	}
}

func TestSkillInstallRejectsTraversalAndInvalidFrontmatter(t *testing.T) {
	d := t.TempDir()
	m := NewManager(Config{SkillsDir: d})
	if err := m.InstallSkill("bad", map[string][]byte{"SKILL.md": []byte("name: bad"), "../escape": []byte("x")}); err == nil {
		t.Fatal("accepted traversal")
	}
	if err := m.InstallSkill("bad", map[string][]byte{"SKILL.md": []byte("name: bad")}); err == nil {
		t.Fatal("accepted malformed frontmatter")
	}
	if err := m.InstallSkill("good", map[string][]byte{"SKILL.md": []byte("---\nname: good\ndescription: safe\n---\nbody")}); err != nil {
		t.Fatal(err)
	}
	if _, d := m.ListSkills(); len(d) != 0 {
		t.Fatal(d)
	}
	badManifest := []byte("---\nname: partial\ndescription: partial\n---\nbody")
	if err := m.InstallSkill("partial", map[string][]byte{"SKILL.md": badManifest, "docs": []byte("file"), "docs/note": []byte("nested")}); err == nil {
		t.Fatal("accepted conflicting staged paths")
	}
	if _, err := os.Stat(filepath.Join(d, "partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed install left catalog entry: %v", err)
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-") {
			t.Fatalf("failed install left staging directory %q", entry.Name())
		}
	}
	invalidUTF8 := string([]byte{'x', 0xff})
	if err := m.InstallSkill("utf8", map[string][]byte{"SKILL.md": []byte("---\nname: utf8\ndescription: safe\n---\nbody"), invalidUTF8: []byte("x")}); err == nil {
		t.Fatal("accepted non-UTF-8 path")
	}
	if err := m.InstallSkill("large", map[string][]byte{"SKILL.md": []byte("---\nname: large\ndescription: safe\n---\nbody"), "a": make([]byte, maxSkillFile), "b": []byte("overflow")}); err == nil {
		t.Fatal("accepted aggregate oversized skill")
	}
}

func TestOAuthStateAndRedirectValidation(t *testing.T) {
	if ValidateRedirectURI("http://evil.test/cb") == nil {
		t.Fatal("accepted insecure redirect")
	}
	f := &OAuthFlow{State: "expected"}
	if err := f.Callback(context.Background(), "code", "wrong"); err == nil {
		t.Fatal("accepted wrong state")
	}
}

func TestLegacyBotPolicyIsIgnoredAndResourcesRemainGlobal(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "must not be contacted", http.StatusInternalServerError)
	}))
	defer remote.Close()
	dir := t.TempDir()
	mgr := NewManager(Config{MCPConfigPath: filepath.Join(dir, "mcp.json")})
	if err := mgr.SaveMCP("deny-all", MCPServerConfig{URL: remote.URL, BotAllowlists: map[string][]string{}}, false); err != nil {
		t.Fatal(err)
	}
	stored, err := loadServers(mgr.cfg.MCPConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored["deny-all"].BotAllowlists == nil {
		t.Log("legacy policy was dropped on write")
	}
	prepared, err := mgr.PrepareForBot(context.Background(), "any-bot")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if requests.Load() == 0 || len(prepared.Tools) != 0 {
		t.Fatalf("global prepare requests=%d tools=%d", requests.Load(), len(prepared.Tools))
	}
	views, err := mgr.ListMCP()
	if err != nil || len(views) != 1 {
		t.Fatalf("deny-all view=%#v err=%v", views, err)
	}
}

func TestOAuthMCPBotPolicySkillScopeRefreshAndDisconnect(t *testing.T) {
	var mcpRequests, refreshes, revocations atomic.Int32
	mcpServer := fixtureServer("oauth-fixture", "1")
	mcpServer.AddTool(fixtureTool("ping"), func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return fixtureText("authenticated pong"), nil
	})
	mcpHandler := fixtureHTTPHandler(mcpServer)

	var fixture *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, map[string]any{
			"issuer": fixture.URL, "authorization_endpoint": fixture.URL + "/authorize",
			"token_endpoint": fixture.URL + "/token", "revocation_endpoint": fixture.URL + "/revoke",
			"response_types_supported": []string{"code"}, "code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "fixture-code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "bad exchange", http.StatusUnauthorized)
				return
			}
			writeFixtureJSON(w, map[string]any{"access_token": "stale-token", "refresh_token": "fixture-refresh", "token_type": "bearer", "expires_in": 3600})
		case "refresh_token":
			refreshes.Add(1)
			if r.Form.Get("refresh_token") != "fixture-refresh" {
				http.Error(w, "bad refresh", http.StatusUnauthorized)
				return
			}
			writeFixtureJSON(w, map[string]any{"access_token": "fresh-token", "refresh_token": "fresh-refresh", "token_type": "Bearer", "expires_in": 3600})
		default:
			http.Error(w, "unsupported grant", http.StatusBadRequest)
		}
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("token") != "fresh-refresh" {
			http.Error(w, "wrong token", http.StatusBadRequest)
			return
		}
		revocations.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpRequests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fresh-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="fixture"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	fixture = httptest.NewServer(mux)
	defer fixture.Close()

	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	mgr := NewManager(Config{MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: skillsDir})
	config := MCPServerConfig{
		URL: fixture.URL + "/mcp", BotAllowlists: map[string][]string{"allowed": {"ping"}},
		OAuth: &OAuthConfig{ClientID: "fixture-client", ClientSecret: "fixture-secret", Scopes: []string{"tools"}, AuthServerMetadataURL: fixture.URL + "/.well-known/oauth-authorization-server"},
	}
	if err := mgr.SaveMCP("fixture", config, false); err != nil {
		t.Fatal(err)
	}
	if err := mgr.InstallSkill("alpha", map[string][]byte{"SKILL.md": []byte("---\nname: alpha\ndescription: scoped skill\n---\nprivate instructions")}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetSkillEnabled("allowed", "alpha", true); err != nil {
		t.Fatal(err)
	}

	sid, authorizationURL, err := mgr.OAuthStart(context.Background(), "fixture", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(authorizationURL)
	state := authorization.Query().Get("state")
	if sid == "" || state == "" || authorization.Query().Get("code_challenge") == "" {
		t.Fatalf("incomplete OAuth start: sid=%q url=%q", sid, authorizationURL)
	}
	if err := mgr.OAuthCallback(context.Background(), sid, "fixture-code", "wrong-state"); err == nil {
		t.Fatal("accepted wrong OAuth state")
	}
	if err := mgr.OAuthCallback(context.Background(), "", "fixture-code", state); err != nil {
		t.Fatal(err)
	}
	if err := mgr.OAuthCallback(context.Background(), sid, "fixture-code", state); err == nil {
		t.Fatal("OAuth session was reusable")
	}
	store := mgr.tokenStore("fixture", config)
	token, err := store.GetToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	token.ExpiresAt = time.Now().Add(-time.Minute)
	if err := store.SaveToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := NewOAuthHandler(OAuthFlowConfig{ClientID: config.OAuth.ClientID, ClientSecret: config.OAuth.ClientSecret, AuthServerMetadataURL: config.OAuth.AuthServerMetadataURL, TokenStore: store})
			h.SetBaseURL(config.URL)
			_, err := h.GetAuthorizationHeader(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("concurrent clients refreshed %d times", refreshes.Load())
	}

	prepared, err := mgr.PrepareForBot(context.Background(), "allowed")
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if refreshes.Load() != 1 {
		t.Fatalf("refreshes=%d", refreshes.Load())
	}
	var ping, readSkill bool
	for _, tool := range prepared.Tools {
		switch {
		case strings.Contains(tool.Name, "__ping"):
			out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
			if err != nil || out != "authenticated pong" {
				t.Fatalf("MCP call out=%q err=%v", out, err)
			}
			ping = true
		case tool.Name == "read_skill":
			out, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`))
			if err != nil || out != "private instructions" {
				t.Fatalf("skill read out=%q err=%v", out, err)
			}
			readSkill = true
		}
	}
	if !ping || !readSkill {
		t.Fatalf("missing scoped tools: %#v", prepared.Tools)
	}

	beforeGlobal := mcpRequests.Load()
	denied, err := mgr.PrepareForBot(context.Background(), "denied")
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	if mcpRequests.Load() <= beforeGlobal {
		t.Fatalf("global resource was not prepared: before=%d after=%d", beforeGlobal, mcpRequests.Load())
	}
	var deniedPing, deniedSkill bool
	for _, tool := range denied.Tools {
		if strings.Contains(tool.Name, "__ping") {
			deniedPing = true
		}
		if tool.Name == "read_skill" {
			if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`)); err != nil {
				t.Fatal(err)
			}
			deniedSkill = true
		}
	}
	if !deniedPing || !deniedSkill {
		t.Fatalf("global resources missing: %#v", denied.Tools)
	}

	views, err := mgr.ListMCP()
	if err != nil || len(views) != 1 || views[0].OAuth == nil || !views[0].OAuth.Connected || views[0].OAuth.ClientSecret != maskedSecret {
		t.Fatalf("masked status=%#v err=%v", views, err)
	}
	encoded, _ := json.Marshal(views)
	for _, secret := range []string{"fixture-secret", "fresh-token", "fresh-refresh"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("status leaked %q: %s", secret, encoded)
		}
	}
	if err := mgr.SaveMCP("fixture", MCPServerConfig{URL: fixture.URL + "/mcp", OAuth: &OAuthConfig{ClientID: "fixture-client", ClientSecret: maskedSecret, Scopes: []string{"tools"}, AuthServerMetadataURL: fixture.URL + "/.well-known/oauth-authorization-server"}}, true); err != nil {
		t.Fatal(err)
	}
	stored, err := loadServers(mgr.cfg.MCPConfigPath)
	if err != nil || stored["fixture"].OAuth.ClientSecret != "fixture-secret" {
		t.Fatalf("OAuth secret was not retained: %#v err=%v", stored, err)
	}
	if err := mgr.OAuthDisconnect("fixture"); err != nil {
		t.Fatal(err)
	}
	if revocations.Load() != 1 {
		t.Fatalf("revocations=%d", revocations.Load())
	}
	if _, err := store.GetToken(context.Background()); !errors.Is(err, errCredentialInvalidated) {
		t.Fatalf("token remains after disconnect: %v", err)
	}
}

func TestCredentialInvalidationBlocksLateRefreshCallbackAndTargetReuse(t *testing.T) {
	refreshEntered := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var exchanges atomic.Int32
	var fixture *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata", func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, map[string]any{
			"issuer": fixture.URL, "authorization_endpoint": fixture.URL + "/authorize",
			"token_endpoint": fixture.URL + "/token", "response_types_supported": []string{"code"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "refresh_token" {
			close(refreshEntered)
			<-releaseRefresh
			writeFixtureJSON(w, map[string]any{"access_token": "late", "refresh_token": "late-refresh", "token_type": "Bearer", "expires_in": 3600})
			return
		}
		exchanges.Add(1)
		writeFixtureJSON(w, map[string]any{"access_token": "callback-token", "token_type": "Bearer"})
	})
	fixture = httptest.NewServer(mux)
	defer fixture.Close()

	dir := t.TempDir()
	mgr := NewManager(Config{MCPConfigPath: filepath.Join(dir, "mcp.json")})
	cfg := MCPServerConfig{URL: fixture.URL + "/mcp", OAuth: &OAuthConfig{ClientID: "client", AuthServerMetadataURL: fixture.URL + "/metadata"}}
	if err := mgr.SaveMCP("blocked", cfg, false); err != nil {
		t.Fatal(err)
	}
	store := mgr.tokenStore("blocked", cfg)
	if err := store.SaveToken(context.Background(), &Token{AccessToken: "expired", RefreshToken: "refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := store.GetToken(context.Background())
		refreshDone <- err
	}()
	<-refreshEntered
	disconnectDone := make(chan error, 1)
	go func() { disconnectDone <- mgr.OAuthDisconnect("blocked") }()
	select {
	case err := <-disconnectDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect waited for in-flight refresh")
	}
	close(releaseRefresh)
	if err := <-refreshDone; !errors.Is(err, errCredentialInvalidated) {
		t.Fatalf("late refresh error=%v", err)
	}
	if _, err := os.Stat(mgr.tokenPath("blocked")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late refresh recreated token: %v", err)
	}

	_, authorizationURL, err := mgr.OAuthStart(context.Background(), "blocked", "http://localhost/callback")
	if err != nil {
		t.Fatal(err)
	}
	authorization, _ := url.Parse(authorizationURL)
	state := authorization.Query().Get("state")
	if err := mgr.DeleteMCP("blocked"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.OAuthCallback(context.Background(), "", "code", state); !errors.Is(err, errCredentialInvalidated) && !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stale callback error=%v", err)
	}
	if exchanges.Load() != 0 {
		t.Fatalf("stale callback exchanged code %d times", exchanges.Load())
	}

	if err := mgr.SaveMCP("moved", cfg, false); err != nil {
		t.Fatal(err)
	}
	oldStore := mgr.tokenStore("moved", cfg)
	if err := oldStore.SaveToken(context.Background(), &Token{AccessToken: "old", TokenType: "Bearer"}); err != nil {
		t.Fatal(err)
	}
	moved := cfg
	moved.URL = fixture.URL + "/other-mcp"
	if err := mgr.SaveMCP("moved", moved, true); err != nil {
		t.Fatal(err)
	}
	if _, err := oldStore.GetToken(context.Background()); !errors.Is(err, errCredentialInvalidated) {
		t.Fatalf("old target store remained active: %v", err)
	}
	if _, err := os.Stat(mgr.tokenPath("moved")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target change retained token: %v", err)
	}
}

func TestFailedRefreshIsNotRetriedForSamePersistedToken(t *testing.T) {
	var requests atomic.Int32
	var fixture *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata", func(w http.ResponseWriter, _ *http.Request) {
		writeFixtureJSON(w, map[string]any{
			"issuer": fixture.URL, "authorization_endpoint": fixture.URL + "/authorize",
			"token_endpoint": fixture.URL + "/token", "response_types_supported": []string{"code"},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "rejected", http.StatusUnauthorized)
	})
	fixture = httptest.NewServer(mux)
	defer fixture.Close()

	dir := t.TempDir()
	mgr := NewManager(Config{MCPConfigPath: filepath.Join(dir, "mcp.json")})
	cfg := MCPServerConfig{URL: fixture.URL + "/mcp", OAuth: &OAuthConfig{ClientID: "client", AuthServerMetadataURL: fixture.URL + "/metadata"}}
	if err := mgr.SaveMCP("failed", cfg, false); err != nil {
		t.Fatal(err)
	}
	store := mgr.tokenStore("failed", cfg)
	if err := store.SaveToken(context.Background(), &Token{AccessToken: "expired", RefreshToken: "rejected-refresh", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := NewOAuthHandler(OAuthFlowConfig{ClientID: "client", AuthServerMetadataURL: fixture.URL + "/metadata", TokenStore: store})
			h.SetBaseURL(cfg.URL)
			if _, err := h.GetAuthorizationHeader(context.Background()); err == nil {
				t.Error("failed refresh unexpectedly authorized")
			}
		}()
	}
	wg.Wait()
	if requests.Load() != 1 {
		t.Fatalf("same failed refresh token was requested %d times", requests.Load())
	}
}

func writeFixtureJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(fmt.Sprintf("encode fixture: %v", err))
	}
}
