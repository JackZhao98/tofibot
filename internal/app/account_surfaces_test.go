package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Account-bound streaming, file, secret, and Runner surfaces must pass through
// the same session gates as ordinary workspace requests.
func TestAccountProtectedSurfacesRejectInvalidSessionStates(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	forced, err := g.create(context.Background(), "forced", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	active, err := g.create(context.Background(), "active", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	activeCookie := accountCookie(t, g, active)
	forcedCookie := accountCookie(t, g, forced)
	adminCookie := accountCookie(t, g, admin)
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+active.ID, "", adminCookie); w.Code != 200 {
		t.Fatalf("disable active account: %d", w.Code)
	}

	paths := []struct{ method, path, body string }{
		{http.MethodGet, "/api/conversations/00000000-0000-0000-0000-000000000000/events?after=0", ""},
		{http.MethodGet, "/api/bots/00000000-0000-0000-0000-000000000000/computer/stream", ""},
		{http.MethodGet, "/api/attachments/synthetic-file", ""},
		{http.MethodPost, "/api/conversations/synthetic-conversation/attachments", ""},
		{http.MethodGet, "/api/computer/credentials", ""},
		{http.MethodPost, "/api/computer/credentials", `{"name":"x","kind":"env","target":"X","value":"x"}`},
		{http.MethodGet, "/api/secret-inputs", ""},
		{http.MethodPost, "/api/extensions/local-mcp/install", `{"id":"x","kind":"fixture"}`},
		{http.MethodDelete, "/api/extensions/local-mcp/fixture", ""},
	}
	for _, endpoint := range paths {
		w := accountRequest(g, endpoint.method, endpoint.path, endpoint.body, forcedCookie)
		if w.Code != http.StatusForbidden {
			t.Errorf("forced-password %s %s: got %d, body %s", endpoint.method, endpoint.path, w.Code, w.Body.String())
		}
	}
	if w := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, forcedCookie); w.Code != 200 {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
	for _, cookie := range []*http.Cookie{nil, forcedCookie, activeCookie} {
		for _, endpoint := range paths {
			w := accountRequest(g, endpoint.method, endpoint.path, endpoint.body, cookie)
			if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
				t.Errorf("cookie=%v %s %s: got %d, body %s", cookie != nil, endpoint.method, endpoint.path, w.Code, w.Body.String())
			}
		}
	}
}

func TestAccountSiblingCannotReadFilesSecretsOrCallRunner(t *testing.T) {
	g := accountFixture(t)
	owner, err := g.create(context.Background(), "owner", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := g.create(context.Background(), "sibling", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Account{owner, sibling} {
		if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	secretCreate := accountRequest(g, http.MethodPost, "/api/computer/credentials", `{"name":"fixture","kind":"env","target":"PRIVATE_KEY","value":"private-secret-marker"}`, accountCookie(t, g, owner))
	if secretCreate.Code != http.StatusCreated {
		t.Fatalf("create fixture secret: %d %s", secretCreate.Code, secretCreate.Body.String())
	}
	var secret struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(secretCreate.Body.Bytes(), &secret) != nil || secret.ID == "" {
		t.Fatalf("secret response: %s", secretCreate.Body.String())
	}

	cookie := accountCookie(t, g, sibling)
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/computer/credentials"},
		{http.MethodGet, "/api/extensions/local-mcp"},
	} {
		w := accountRequest(g, endpoint.method, endpoint.path, "", cookie)
		if strings.Contains(w.Body.String(), secret.ID) || strings.Contains(w.Body.String(), "private-secret-marker") || strings.Contains(w.Body.String(), "private-key-material") {
			t.Errorf("sibling %s %s exposed secret data: %d %s", endpoint.method, endpoint.path, w.Code, w.Body.String())
		}
	}
	if got := accountRequest(g, http.MethodDelete, "/api/computer/credentials/"+secret.ID, "", cookie); got.Code != http.StatusNotFound {
		t.Fatalf("sibling secret mutation %d %s", got.Code, got.Body.String())
	}
	if got := accountRequest(g, http.MethodGet, "/api/computer/credentials", "", accountCookie(t, g, owner)); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), secret.ID) || strings.Contains(got.Body.String(), "private-secret-marker") {
		t.Fatalf("owner secret response should remain redacted: %d %s", got.Code, got.Body.String())
	}
}

func TestAccountUpgradeHeadersDoNotBypassSessionGate(t *testing.T) {
	g := accountFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/conversations/events", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	upgradeResponse := httptest.NewRecorder()
	g.Handler().ServeHTTP(upgradeResponse, req)
	if upgradeResponse.Code != http.StatusUnauthorized {
		t.Fatalf("upgrade without account session returned %d", upgradeResponse.Code)
	}
	for _, path := range []string{"/api/conversations/events", "/api/workspace/events"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Accept", "text/event-stream")
		response := httptest.NewRecorder()
		g.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "auth_required") {
			t.Errorf("%s without cookie: status=%d body=%q", path, response.Code, response.Body.String())
		}
	}
}

func TestAccountLiveSSEIsOwnConversationAndEndsOnDisable(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "stream-admin", "", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := g.create(context.Background(), "stream-owner", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := g.create(context.Background(), "stream-sibling", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []Account{owner, sibling} {
		if _, err := g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := g.workspace(owner)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := workspace.store.CreateBot("private-stream-bot", "", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/conversations/" + bot.DMConversationID + "/events?after=0"
	if response := accountRequest(g, http.MethodGet, endpoint, "", accountCookie(t, g, sibling)); response.Code != 404 {
		t.Fatalf("sibling stream %d %s", response.Code, response.Body.String())
	}
	server := httptest.NewTLSServer(g.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(accountCookie(t, g, owner))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("owner stream %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	if !scanner.Scan() || scanner.Text() != ": connected" {
		t.Fatalf("no initial frame: %v", scanner.Err())
	}
	if disabled := accountRequest(g, http.MethodDelete, "/api/admin/accounts/"+owner.ID, "", accountCookie(t, g, admin)); disabled.Code != 200 {
		t.Fatalf("disable %d %s", disabled.Code, disabled.Body.String())
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("revoked stream did not close normally: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("stream survived until client timeout")
	}
	upgrade, err := http.NewRequest(http.MethodGet, server.URL+"/api/workspace/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	upgrade.Header.Set("Sec-WebSocket-Version", "13")
	upgrade.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	rejected, err := server.Client().Do(upgrade)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Body.Close()
	if rejected.StatusCode != 401 {
		t.Fatalf("unauthenticated HTTP upgrade %d", rejected.StatusCode)
	}
}
