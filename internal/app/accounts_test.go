package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Reads only the disposable fixture's generated secret; invited accounts do
// not consume or require deployment bootstrap authority.
func accountCreationSecret(t *testing.T, g *AccountGateway, first bool) string {
	t.Helper()
	if !first {
		return ""
	}
	secret, err := readOwnerBootstrap(g.auth.bootstrapPath)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func accountFixture(t *testing.T) *AccountGateway {
	t.Helper()
	g, err := NewAccountGateway(Config{DataDir: t.TempDir(), Environment: "acceptance", OwnerAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}
func accountRequest(g *AccountGateway, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.TLS = &tls.ConnectionState{}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	return w
}
func accountCookie(t *testing.T, g *AccountGateway, a Account) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r.TLS = &tls.ConnectionState{}
	if err := g.issue(w, r, a.ID); err != nil {
		t.Fatal(err)
	}
	return w.Result().Cookies()[0]
}
func TestAccountBootstrapRaceAndClosedSignup(t *testing.T) {
	g := accountFixture(t)
	secret := accountCreationSecret(t, g, true)
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, name := range []string{"first-admin", "racing-admin"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			a, err := g.create(context.Background(), name, name+"@example.test", "SyntheticPassword123!", true, secret)
			if err == nil {
				if a.Role != "admin" || a.Legacy {
					t.Error("first account must be a non-legacy admin")
				}
				successes.Add(1)
			}
		}(name)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("bootstrap successes %d", successes.Load())
	}
	w := accountRequest(g, "POST", "/api/auth/setup", `{"username":"late-admin","email":"late@example.test","password":"SyntheticPassword123!"}`, nil)
	if w.Code != 401 {
		t.Fatalf("reopened bootstrap %d %s", w.Code, w.Body.String())
	}
	w = accountRequest(g, "POST", "/api/admin/accounts", `{}`, nil)
	if w.Code != 401 {
		t.Fatalf("public invitation %d", w.Code)
	}
}
func TestAccountAdminCreatesSeparateEmptyWorkspaces(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	own, err := g.workspace(admin)
	if err != nil {
		t.Fatal(err)
	}
	if !own.isolatedWorkspace || own.store == g.root.store {
		t.Fatal("new first Admin must own an isolated workspace")
	}
	adminCookie := accountCookie(t, g, admin)
	var users []Account
	for _, name := range []string{"alice", "bravo"} {
		w := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"`+name+`","password":"SyntheticPassword123!"}`, adminCookie)
		if w.Code != 201 {
			t.Fatalf("create %d %s", w.Code, w.Body.String())
		}
		var user Account
		if json.Unmarshal(w.Body.Bytes(), &user) != nil || user.Role != "user" || !user.MustChangePassword {
			t.Fatal("wrong invited identity")
		}
		users = append(users, user)
	}
	legacyBot, err := g.root.store.CreateBot("private-admin", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		cookie := accountCookie(t, g, user)
		changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, cookie)
		if changed.Code != 200 {
			t.Fatalf("initial change %d %s", changed.Code, changed.Body.String())
		}
		cookie = changed.Result().Cookies()[0]
		w := accountRequest(g, "GET", "/api/bots", "", cookie)
		if w.Code != 200 || strings.Contains(w.Body.String(), legacyBot.ID) {
			t.Fatalf("legacy leak %d %s", w.Code, w.Body.String())
		}
		w = accountRequest(g, "GET", "/api/admin/accounts", "", cookie)
		if w.Code != 403 {
			t.Fatal("ordinary user listed accounts")
		}
		w = accountRequest(g, "POST", "/api/admin/accounts", `{}`, cookie)
		if w.Code != 403 {
			t.Fatal("ordinary user created account")
		}
	}
	a, _ := g.workspace(users[0])
	b, _ := g.workspace(users[1])
	if a.store == b.store || a == b || a.microVM != nil || b.microVM != nil || a.ownerAuth != nil {
		t.Fatal("inherited runtime or computer")
	}
	bot, _ := a.store.CreateBot("alice-private", "", "model")
	m, _, _ := a.store.AddMessage(bot.DMConversationID, "user", "", "", "private text", "")
	cookie := accountCookie(t, g, users[1])
	for _, path := range []string{"/api/bots/" + bot.ID, "/api/conversations/" + bot.DMConversationID, "/api/conversations/" + bot.DMConversationID + "/messages"} {
		w := accountRequest(g, "GET", path, "", cookie)
		if w.Code < 400 || strings.Contains(w.Body.String(), "private text") {
			t.Fatalf("foreign access %s %d %s", path, w.Code, w.Body.String())
		}
	}
	w := accountRequest(g, "PUT", "/api/conversations/"+bot.DMConversationID+"/messages/"+m.ID+"/reactions", `{"emoji":"👍","present":true}`, cookie)
	if w.Code < 400 {
		t.Fatal("foreign reaction accepted")
	}
	if strings.Contains(accountRequest(g, "GET", "/api/admin/accounts", "", adminCookie).Body.String(), "password_hash") {
		t.Fatal("hash leaked")
	}
}
func TestAccountLegacyMigrationPreservesOwnerSessions(t *testing.T) {
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("synthetic-salt-16")
	hash, err := s.ownerAuth.hash(context.Background(), "SyntheticPassword123!", salt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.db.Exec(`INSERT INTO workspace_owner(id,username,email,salt,password_hash,created_at) VALUES(1,'legacy','legacy@example.test',?,?,1)`, salt, hash)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r.TLS = &tls.ConnectionState{}
	if err = s.ownerAuth.issue(w, r); err != nil {
		t.Fatal(err)
	}
	cookie := w.Result().Cookies()[0]
	s.Close()
	g, err := NewAccountGateway(Config{DataDir: dir, OwnerAuth: true, Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	response := accountRequest(g, "GET", "/api/auth/session", "", cookie)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"role":"admin"`) || strings.Contains(response.Body.String(), `"setup_required":true`) {
		t.Fatalf("migration %s", response.Body.String())
	}
}

func TestAccountDisableRestoreRevokesRequestsAndRetainsWorkspace(t *testing.T) {
	g := accountFixture(t)
	admin, _ := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	user, _ := g.create(context.Background(), "alice", "alice@example.test", "SyntheticPassword123!", false, "")
	cookie := accountCookie(t, g, user)
	adminCookie := accountCookie(t, g, admin)
	if w := accountRequest(g, "GET", "/api/bots", "", cookie); w.Code != 403 {
		t.Fatal("initial password not enforced")
	}
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, cookie)
	if changed.Code != 200 {
		t.Fatalf("password %d %s", changed.Code, changed.Body.String())
	}
	newCookie := changed.Result().Cookies()[0]
	if w := accountRequest(g, "GET", "/api/bots", "", cookie); w.Code != 401 {
		t.Fatal("old password session remained")
	}
	server, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := server.store.CreateBot("retained", "", "model")
	req := httptest.NewRequest("GET", "/api/conversations/events", nil)
	req.TLS = &tls.ConnectionState{}
	req.AddCookie(newCookie)
	registered, _, cleanup, ok := g.register(req)
	if !ok {
		t.Fatal("cannot register active request")
	}
	defer cleanup()
	w := accountRequest(g, "DELETE", "/api/admin/accounts/"+user.ID, "", adminCookie)
	if w.Code != 200 {
		t.Fatalf("disable %d %s", w.Code, w.Body.String())
	}
	if registered.Context().Err() == nil {
		t.Fatal("active stream not cancelled")
	}
	if accountRequest(g, "GET", "/api/bots", "", newCookie).Code != 401 {
		t.Fatal("disabled session valid")
	}
	if accountRequest(g, "POST", "/api/auth/login", `{"identifier":"alice","password":"ChangedPassword123!"}`, nil).Code != 401 {
		t.Fatal("disabled account logged in")
	}
	w = accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID, `{"disabled":false}`, adminCookie)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	login := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"alice","password":"ChangedPassword123!"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Body.String())
	}
	restored := accountRequest(g, "GET", "/api/bots", "", login.Result().Cookies()[0])
	if restored.Code != 200 || !strings.Contains(restored.Body.String(), bot.ID) {
		t.Fatal("workspace lost on restore")
	}
}
func TestAccountRBACSelfLockoutResetAndSessionRestart(t *testing.T) {
	g := accountFixture(t)
	admin, _ := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	user, _ := g.create(context.Background(), "bravo", "bravo@example.test", "SyntheticPassword123!", false, "")
	ac := accountCookie(t, g, admin)
	uc := accountCookie(t, g, user)
	for _, body := range []string{`{"disabled":true}`, `{"role":"user"}`} {
		if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+admin.ID, body, ac); w.Code != 409 {
			t.Fatalf("self lockout %d", w.Code)
		}
	}
	if accountRequest(g, "DELETE", "/api/admin/accounts/"+admin.ID, "", uc).Code != 403 {
		t.Fatal("ordinary user managed admin")
	}
	w := accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID, `{"initial_password":"ResetPassword123!"}`, ac)
	if w.Code != 200 || strings.Contains(w.Body.String(), "ResetPassword") || strings.Contains(w.Body.String(), "password_hash") {
		t.Fatal("password reset response")
	}
	if accountRequest(g, "GET", "/api/auth/session", "", uc).Body.String() == "" {
		t.Fatal("missing session state")
	}
	if accountRequest(g, "GET", "/api/bots", "", uc).Code != 401 {
		t.Fatal("reset did not revoke sessions")
	}
	login := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"bravo","password":"ResetPassword123!"}`, nil)
	if login.Code != 200 || !strings.Contains(login.Body.String(), `"must_change_password":true`) {
		t.Fatal("reset login state")
	}
	if accountRequest(g, "POST", "/api/admin/accounts", `{"username":"bad","password":"SyntheticPassword123!","role":"admin"}`, ac).Code != 400 {
		t.Fatal("malicious create role accepted")
	}
	if accountRequest(g, "PATCH", "/api/admin/accounts/"+user.ID, `{"role":"superuser"}`, ac).Code != 400 {
		t.Fatal("invalid role accepted")
	}
	logout := accountRequest(g, "POST", "/api/auth/logout", `{}`, ac)
	if logout.Code != 200 {
		t.Fatal(logout.Body.String())
	}
	if accountRequest(g, "GET", "/api/admin/accounts", "", ac).Code != 401 {
		t.Fatal("logout retained access")
	}
}

func TestAccountPublicInfoDoesNotDisableAuthAndUsesOwnInstance(t *testing.T) {
	g := accountFixture(t)
	admin, _ := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	user, _ := g.create(context.Background(), "alice", "alice@example.test", "SyntheticPassword123!", false, "")
	public := accountRequest(g, "GET", "/api/server-info", "", nil)
	if public.Code != 200 || strings.Contains(public.Body.String(), `"mode":"none"`) {
		t.Fatal("first-login discovery bypasses auth")
	}
	cookie := accountCookie(t, g, user)
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, cookie)
	if changed.Code != 200 {
		t.Fatal(changed.Body.String())
	}
	cookie = changed.Result().Cookies()[0]
	mine := accountRequest(g, "GET", "/api/server-info", "", cookie)
	owner := accountRequest(g, "GET", "/api/server-info", "", accountCookie(t, g, admin))
	var x, y struct {
		InstanceID string `json:"instance_id"`
	}
	json.Unmarshal(mine.Body.Bytes(), &x)
	json.Unmarshal(owner.Body.Bytes(), &y)
	if mine.Code != 200 || x.InstanceID == "" || x.InstanceID == y.InstanceID {
		t.Fatal("browser/native cache workspace identity is shared")
	}
}
