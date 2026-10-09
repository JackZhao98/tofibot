package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This is a local acceptance fixture for upgrading a populated single-owner
// data directory. The account gateway remains opt-in; this does not perform a
// production migration or claim that the legacy owner's data was relocated.
// Disabling a legacy account revokes App work/access; the existing external
// host VM is not stopped or transferred by this fixture. Its budget stays held.
func TestAccountLegacyOwnerUpgradeDisableRestoreAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	legacy, err := NewServer(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("synthetic-salt-16")
	hash, err := legacy.ownerAuth.hash(context.Background(), "SyntheticPassword123!", salt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.store.db.Exec(`INSERT INTO workspace_owner(id,username,email,salt,password_hash,created_at) VALUES(1,'legacy','legacy@example.test',?,?,1)`, salt, hash); err != nil {
		t.Fatal(err)
	}
	bot, err := legacy.store.CreateBot("Preserved legacy bot", "synthetic acceptance", "test-model")
	if err != nil {
		t.Fatal(err)
	}
	message, _, err := legacy.store.AddMessage(bot.DMConversationID, "user", "", "", "legacy message marker", "legacy-message-client")
	if err != nil {
		t.Fatal(err)
	}
	const fileBytes = "synthetic legacy file bytes"
	attachment, err := legacy.store.AddAttachment(bot.DMConversationID, "legacy.txt", "text/plain", strings.NewReader(fileBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err = legacy.store.BindAttachments(bot.DMConversationID, message.ID, []string{attachment.ID}); err != nil {
		t.Fatal(err)
	}
	credReq := httptest.NewRequest(http.MethodPost, "/api/computer/credentials", strings.NewReader(`{"name":"fixture","kind":"env","target":"LEGACY_FIXTURE_KEY","value":"synthetic-secret-marker"}`))
	credRec := httptest.NewRecorder()
	if !legacy.handleSecrets(credRec, credReq) || credRec.Code != http.StatusCreated || strings.Contains(credRec.Body.String(), "synthetic-secret-marker") {
		t.Fatalf("create legacy credential: %d %s", credRec.Code, credRec.Body.String())
	}
	var credential struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(credRec.Body.Bytes(), &credential); err != nil || credential.ID == "" {
		t.Fatalf("credential record: %s (%v)", credRec.Body.String(), err)
	}
	legacyCookieRecorder := httptest.NewRecorder()
	legacyLoginReq := httptest.NewRequest(http.MethodPost, "/", nil)
	legacyLoginReq.TLS = &tls.ConnectionState{}
	if err = legacy.ownerAuth.issue(legacyCookieRecorder, legacyLoginReq); err != nil {
		t.Fatal(err)
	}
	legacyCookie := legacyCookieRecorder.Result().Cookies()[0]
	if err = legacy.Close(); err != nil {
		t.Fatal(err)
	}

	g, err := NewAccountGateway(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	if got := accountRequest(g, http.MethodGet, "/api/auth/session", "", legacyCookie); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"role":"admin"`) {
		t.Fatalf("imported owner session: %d %s", got.Code, got.Body.String())
	}
	legacyAccount := Account{ID: "legacy-owner", Username: "legacy", Role: "admin", Legacy: true}
	oldRuntime, err := g.workspace(legacyAccount)
	if err != nil {
		t.Fatal(err)
	}
	ownerCookie := accountCookie(t, g, legacyAccount)

	// Invite a second account, let it set its own password, then promote it so
	// another administrator can exercise the legacy owner's disable guard.
	invited, err := g.create(context.Background(), "second-admin", "second@example.test", "InitialPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	inviteCookie := accountCookie(t, g, invited)
	changed := accountRequest(g, http.MethodPost, "/api/auth/password", `{"current_password":"InitialPassword123!","password":"UpdatedPassword123!"}`, inviteCookie)
	if changed.Code != http.StatusOK {
		t.Fatalf("set invited password: %d %s", changed.Code, changed.Body.String())
	}
	adminCookie := changed.Result().Cookies()[0]
	promoted := accountRequest(g, http.MethodPatch, "/api/admin/accounts/"+invited.ID, `{"role":"admin"}`, ownerCookie)
	if promoted.Code != http.StatusOK {
		t.Fatalf("promote invited account: %d %s", promoted.Code, promoted.Body.String())
	}
	adminCookie = accountCookie(t, g, invited)
	newTenant, err := g.create(context.Background(), "new-tenant", "tenant@example.test", "Orchard-Lantern-9051", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, newTenant.ID); err != nil {
		t.Fatal(err)
	}
	newTenantCookie := accountCookie(t, g, newTenant)
	newTenantBots := accountRequest(g, http.MethodGet, "/api/bots", "", newTenantCookie)
	if newTenantBots.Code != http.StatusOK || strings.Contains(newTenantBots.Body.String(), bot.ID) {
		t.Fatalf("new tenant listed legacy bot: %d %s", newTenantBots.Code, newTenantBots.Body.String())
	}
	for _, path := range []string{"/api/conversations/" + bot.DMConversationID + "/messages", "/api/attachments/" + attachment.ID} {
		if got := accountRequest(g, http.MethodGet, path, "", newTenantCookie); got.Code != http.StatusNotFound {
			t.Errorf("new tenant read legacy ID at %s: %d %s", path, got.Code, got.Body.String())
		}
	}

	// Register an in-flight authenticated request using the owner's imported
	// session; disabling must revoke both its token and its active context.
	activeReq := httptest.NewRequest(http.MethodGet, "/api/conversations/events", nil)
	activeReq.TLS = &tls.ConnectionState{}
	activeReq.AddCookie(ownerCookie)
	registered, _, cleanup, ok := g.register(activeReq)
	if !ok {
		t.Fatal("could not register legacy in-flight request")
	}
	defer cleanup()
	if disabled := accountRequest(g, http.MethodPatch, "/api/admin/accounts/legacy-owner", `{"disabled":true}`, adminCookie); disabled.Code != http.StatusOK {
		t.Fatalf("disable legacy owner: %d %s", disabled.Code, disabled.Body.String())
	}
	if registered.Context().Err() == nil {
		t.Fatal("legacy owner's in-flight request remained active")
	}
	if _, exists := g.workspaces[legacyAccount.ID]; exists {
		t.Fatal("disabled legacy runtime remained registered")
	}
	if err = oldRuntime.store.db.Ping(); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatal("disabled legacy runtime database remained open")
	}
	if got := accountRequest(g, http.MethodGet, "/api/auth/session", "", ownerCookie); got.Code != http.StatusOK || strings.Contains(got.Body.String(), `"authenticated":true`) {
		t.Fatalf("disabled owner session accepted: %d %s", got.Code, got.Body.String())
	}

	// A gateway restart must not eagerly recreate a runtime for a disabled
	// legacy owner, and must leave their shared legacy records intact.
	if err = g.Close(); err != nil {
		t.Fatal(err)
	}
	g, err = NewAccountGateway(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = g.Close() }()
	if _, exists := g.workspaces[legacyAccount.ID]; exists {
		t.Fatal("restart scheduled a disabled legacy runtime")
	}
	if got := accountRequest(g, http.MethodPost, "/api/auth/login", `{"identifier":"legacy","password":"SyntheticPassword123!"}`, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("disabled owner login accepted after restart: %d %s", got.Code, got.Body.String())
	}

	// The separate administrator restores the same account; a fresh login must
	// recover the original bot, message, credential metadata and file bytes.
	if restored := accountRequest(g, http.MethodPatch, "/api/admin/accounts/legacy-owner", `{"disabled":false}`, adminCookie); restored.Code != http.StatusOK {
		t.Fatalf("restore legacy owner: %d %s", restored.Code, restored.Body.String())
	}
	login := accountRequest(g, http.MethodPost, "/api/auth/login", `{"identifier":"legacy","password":"SyntheticPassword123!"}`, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("legacy owner login after restore: %d %s", login.Code, login.Body.String())
	}
	freshCookie := login.Result().Cookies()[0]
	bots := accountRequest(g, http.MethodGet, "/api/bots", "", freshCookie)
	if bots.Code != http.StatusOK || !strings.Contains(bots.Body.String(), bot.ID) || !strings.Contains(bots.Body.String(), "Preserved legacy bot") {
		t.Fatalf("legacy bot not preserved: %d %s", bots.Code, bots.Body.String())
	}
	messages := accountRequest(g, http.MethodGet, "/api/conversations/"+bot.DMConversationID+"/messages", "", freshCookie)
	if messages.Code != http.StatusOK || !strings.Contains(messages.Body.String(), message.ID) || !strings.Contains(messages.Body.String(), "legacy message marker") || !strings.Contains(messages.Body.String(), attachment.ID) {
		t.Fatalf("legacy message or file reference not preserved: %d %s", messages.Code, messages.Body.String())
	}
	credentials := accountRequest(g, http.MethodGet, "/api/computer/credentials", "", freshCookie)
	if credentials.Code != http.StatusOK || !strings.Contains(credentials.Body.String(), credential.ID) || strings.Contains(credentials.Body.String(), "synthetic-secret-marker") {
		t.Fatalf("legacy credential metadata not preserved/redacted: %d %s", credentials.Code, credentials.Body.String())
	}
	file := accountRequest(g, http.MethodGet, "/api/attachments/"+attachment.ID, "", freshCookie)
	if file.Code != http.StatusOK || file.Body.String() != fileBytes {
		t.Fatalf("legacy file bytes not preserved: %d %q", file.Code, file.Body.String())
	}
	path := filepath.Join(dir, "attachments", attachment.ID+".upload")
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != fileBytes {
		t.Fatalf("persisted legacy file changed: %q (%v)", got, readErr)
	}
}
