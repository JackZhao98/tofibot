package app

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPasswordPolicy(t *testing.T) {
	cases := []struct {
		name, user, email, pw, code string
	}{
		{"eleven chars rejected", "alice", "a@example.test", "Zq7!mK2$vXp", "weak_password"},
		{"twelve chars accepted", "alice", "a@example.test", "Zq7!mK2$vXpL", ""},
		{"no composition rules", "alice", "a@example.test", "horsebatterystaple", ""},
		{"common lowercase", "alice", "a@example.test", "password1234", "common_password"},
		{"common mixed case", "alice", "a@example.test", "PassWord1234", "common_password"},
		{"contains username", "synthetic-user", "a@example.test", "my-Synthetic-User-pw-9", "password_contains_identity"},
		{"contains email local part", "alice", "quartermaster@example.test", "xx-Quartermaster-77", "password_contains_identity"},
		{"equals email", "alice", "alice@example.test", "alice@example.test", "password_contains_identity"},
		{"short token only matched by equality", "ab", "ab@example.test", "zzabzzabzz-QW9", ""},
		{"max bytes accepted", "alice", "a@example.test", strings.Repeat("xK9", 341) + "q", ""},
		{"over max bytes rejected", "alice", "a@example.test", strings.Repeat("xK9", 342), "weak_password"},
		{"repeated unit", "alice", "a@example.test", "zzzzzzzzzzzz", "common_password"},
	}
	for _, c := range cases {
		got := checkPassword(c.user, c.email, c.pw)
		switch {
		case c.code == "" && got != nil:
			t.Errorf("%s: unexpected %s", c.name, got.Code)
		case c.code != "" && (got == nil || got.Code != c.code || got.Field != "password"):
			t.Errorf("%s: got %+v, want %s", c.name, got, c.code)
		}
	}
}

func TestPasswordBlocklistUIMirror(t *testing.T) {
	ui, err := os.ReadFile("../../ui/src/password-blocklist.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ui, passwordBlocklistJSON) {
		t.Fatal("ui/src/password-blocklist.json differs from internal/app/password_blocklist.json")
	}
}

func TestSetupFieldErrorShape(t *testing.T) {
	g := accountFixture(t)
	secret := accountCreationSecret(t, g, true)
	body := func(user, email, pw string) string {
		b, _ := json.Marshal(map[string]string{"bootstrap_secret": secret, "username": user, "email": email, "password": pw})
		return string(b)
	}
	for _, c := range []struct{ body, code, field string }{
		{body("a!", "ok@example.test", "Zq7!mK2$vXpL"), "invalid_username", "username"},
		{body("synthetic", "not-an-email", "Zq7!mK2$vXpL"), "invalid_email", "email"},
		{body("synthetic", "ok@example.test", "short"), "weak_password", "password"},
		{body("synthetic", "ok@example.test", "password1234"), "common_password", "password"},
	} {
		w := accountRequest(g, "POST", "/api/auth/setup", c.body, nil)
		var out struct {
			Error struct{ Code, Field, Message string }
		}
		if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Error.Code != c.code || out.Error.Field != c.field || out.Error.Message == "" {
			t.Fatalf("status=%d body=%s want %s/%s", w.Code, w.Body.String(), c.code, c.field)
		}
	}
	// A field error must not consume the setup key.
	bootstrapState(t, g, 0, 0, true)
}

func TestSetupVerifyDoesNotConsume(t *testing.T) {
	g := accountFixture(t)
	secret := accountCreationSecret(t, g, true)
	verify := func(s string) int {
		b, _ := json.Marshal(map[string]string{"bootstrap_secret": s})
		w := accountRequest(g, "POST", "/api/auth/setup/verify", string(b), nil)
		if strings.Contains(w.Body.String(), secret) || len(w.Result().Cookies()) != 0 {
			t.Fatal("verify leaked the secret or issued a cookie")
		}
		return w.Code
	}
	if c := verify("wrong-synthetic-secret"); c != 401 {
		t.Fatalf("wrong key: %d", c)
	}
	if c := verify(""); c != 401 {
		t.Fatalf("empty key: %d", c)
	}
	if c := verify(secret); c != 200 {
		t.Fatalf("right key: %d", c)
	}
	if c := verify(secret); c != 200 {
		t.Fatalf("right key again: %d", c)
	}
	bootstrapState(t, g, 0, 0, true)
	if _, err := os.Stat(g.auth.bootstrapPath); err != nil {
		t.Fatal("verify removed the secret file")
	}
	// The key is consumed only by a successful account creation.
	w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	bootstrapState(t, g, 1, 1, false)
}

func TestSetupVerifyRateLimited(t *testing.T) {
	g := accountFixture(t)
	limited := 0
	for i := 0; i < 12; i++ {
		if accountRequest(g, "POST", "/api/auth/setup/verify", `{"bootstrap_secret":"wrong"}`, nil).Code == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("verify is not rate limited")
	}
}

func TestAdminCreatedAccountUsesPolicy(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(t.Context(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	cookie := accountCookie(t, g, admin)
	w := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"bob-user","email":"bob@example.test","password":"password1234"}`, cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"common_password"`) || !strings.Contains(w.Body.String(), `"field":"password"`) {
		t.Fatalf("admin create: %d %s", w.Code, w.Body.String())
	}
	w = accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"admin-admin-admin"}`, cookie)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"password_contains_identity"`) {
		t.Fatalf("password change: %d %s", w.Code, w.Body.String())
	}
}
