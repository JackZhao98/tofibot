package app

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestPasswordChangeBucketCannotCollideWithLoginIdentifier(t *testing.T) {
	s, _ := ownerTestServer(t)
	id := "0b9f6a3e-1111-4222-8333-444455556666"
	if accountBucket("id:"+id).key == passwordChangeBucket(id).key {
		t.Fatal("password-change bucket shares a key with the login bucket")
	}
	// An anonymous caller naming the login identifier "id:<uuid>" from many
	// addresses exhausts that login bucket; the signed-in change must be unaffected.
	for i := 0; i < accountAttemptLimit; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:1", i+1)
		if !s.ownerAuth.allowAttempt(r, "id:"+id) {
			t.Fatalf("attempt %d unexpectedly limited", i)
		}
	}
	if !s.ownerAuth.allowPasswordChange(id) {
		t.Fatal("login attempts under id:<uuid> locked the password-change bucket")
	}
	// And the reverse: exhausting password changes does not limit that login identifier.
	other := "7c1d2e3f-aaaa-4bbb-8ccc-ddddeeeeffff"
	for i := 0; i < accountAttemptLimit; i++ {
		s.ownerAuth.allowPasswordChange(other)
	}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "198.51.100.1:1"
	if !s.ownerAuth.allowAttempt(r, "id:"+other) {
		t.Fatal("password-change attempts limited the login identifier id:<uuid>")
	}
}

func TestStrangersCannotLockOutAKnownSource(t *testing.T) {
	t.Setenv("TOFI_TRUSTED_PROXIES", "127.0.0.1/32")
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	good := fmt.Sprintf(`{"identifier":"workspace-owner","password":%q}`, testOwnerPassword)
	// The owner signs in once from home.
	if w := secLogin(s, "198.51.100.7", good); w.Code != 200 {
		t.Fatalf("first sign-in: %d", w.Code)
	}
	// Strangers from many addresses burn the global budget of that account.
	limited := false
	for i := 0; i < accountAttemptLimit+5; i++ {
		if w := secLogin(s, fmt.Sprintf("203.0.113.%d", i+1), `{"identifier":"workspace-owner","password":"wrong"}`); w.Code == 429 {
			limited = true
		}
	}
	if !limited {
		t.Fatal("strangers were not bounded")
	}
	// A new, unknown device is refused (no brute-force reopening) ...
	if w := secLogin(s, "192.0.2.50", good); w.Code != 429 {
		t.Fatalf("unknown source passed an exhausted account budget: %d", w.Code)
	}
	// ... but the previously successful source still signs in with the right password.
	if w := secLogin(s, "198.51.100.7", good); w.Code != 200 {
		t.Fatalf("known source locked out by strangers: %d", w.Code)
	}
	// A known source guessing wrong is still bounded by its own address bucket.
	blocked := false
	for i := 0; i < peerAttemptLimit+2; i++ {
		if w := secLogin(s, "198.51.100.7", `{"identifier":"workspace-owner","password":"wrong"}`); w.Code == 429 {
			blocked = true
		}
	}
	if !blocked {
		t.Fatal("a known source could guess without bound")
	}
}
