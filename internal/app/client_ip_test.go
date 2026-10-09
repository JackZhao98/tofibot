package app

import (
	"crypto/tls"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientIPTrustedProxies(t *testing.T) {
	trusted, err := parseTrustedProxies("10.0.10.0/24, 172.16.0.5 ,fd00::/8")
	if err != nil || len(trusted) != 3 {
		t.Fatalf("parse: %v %v", trusted, err)
	}
	cases := []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"untrusted peer with spoofed header keys by RemoteAddr", "203.0.113.9:4000", []string{"198.51.100.1"}, "203.0.113.9"},
		{"trusted peer, single hop", "10.0.10.7:5000", []string{"198.51.100.23"}, "198.51.100.23"},
		{"trusted peer, client-supplied prefix is ignored", "10.0.10.7:5000", []string{"1.2.3.4, 198.51.100.23"}, "198.51.100.23"},
		{"chain of trusted proxies", "10.0.10.7:5000", []string{"198.51.100.23, 172.16.0.5, 10.0.10.8"}, "198.51.100.23"},
		{"split header lines", "10.0.10.7:5000", []string{"1.2.3.4", "198.51.100.23, 10.0.10.8"}, "198.51.100.23"},
		{"missing header", "10.0.10.7:5000", nil, "10.0.10.7"},
		{"malformed header", "10.0.10.7:5000", []string{"not-an-ip"}, "10.0.10.7"},
		{"malformed hop to the right of a good one", "10.0.10.7:5000", []string{"198.51.100.23, garbage"}, "10.0.10.7"},
		{"all hops trusted", "10.0.10.7:5000", []string{"10.0.10.8, 172.16.0.5"}, "10.0.10.7"},
		{"ipv6 client", "10.0.10.7:5000", []string{"2001:db8::1"}, "2001:db8::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = c.remote
		for _, line := range c.xff {
			r.Header.Add("X-Forwarded-For", line)
		}
		if got := clientIP(r, trusted); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	// Default (no trusted proxies) never reads the header.
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "10.0.10.7:5000"
	r.Header.Set("X-Forwarded-For", "198.51.100.23")
	if got := clientIP(r, nil); got != "10.0.10.7" {
		t.Fatalf("default: %s", got)
	}
	if _, err := parseTrustedProxies("10.0.0.0/33"); err == nil {
		t.Fatal("bad CIDR accepted")
	}
	if _, err := parseTrustedProxies("nope"); err == nil {
		t.Fatal("bad IP accepted")
	}
}

func TestLimiterIsPerClientBehindTrustedProxy(t *testing.T) {
	g := accountFixture(t)
	var err error
	if g.auth.trusted, err = parseTrustedProxies("192.0.2.0/24"); err != nil { // httptest peers are 192.0.2.1
		t.Fatal(err)
	}
	post := func(client string) int {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"identifier":"x","password":"y"}`))
		r.TLS = &tls.ConnectionState{}
		r.Header.Set("X-Forwarded-For", client)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		return w.Code
	}
	for i := 0; i < 8; i++ {
		if c := post("198.51.100.1"); c != 401 {
			t.Fatalf("attempt %d: %d", i, c)
		}
	}
	if c := post("198.51.100.1"); c != 429 {
		t.Fatalf("attacker not limited: %d", c)
	}
	if c := post("198.51.100.2"); c != 401 {
		t.Fatalf("a different client was locked out: %d", c)
	}
}

func TestClosedSetupAndFieldErrorsDoNotSpendAttempts(t *testing.T) {
	g := accountFixture(t)
	secret := accountCreationSecret(t, g, true)
	// Field validation (400) is refunded: far more than 8 of them still pass.
	for i := 0; i < 12; i++ {
		w := accountRequest(g, "POST", "/api/auth/setup", `{"bootstrap_secret":"`+secret+`","username":"a!","email":"x@example.test","password":"Zq7!mK2$vXpL"}`, nil)
		if w.Code != 400 {
			t.Fatalf("field error %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if w := accountRequest(g, "POST", "/api/auth/setup", bootstrapBody(secret, "synthetic-admin"), nil); w.Code != 200 {
		t.Fatalf("setup after refunded errors: %d %s", w.Code, w.Body.String())
	}
	// Setup is closed now: 409 from a cheap read, never 429, however often it is hit.
	for i := 0; i < 20; i++ {
		for _, path := range []string{"/api/auth/setup", "/api/auth/setup/verify"} {
			if w := accountRequest(g, "POST", path, `{"bootstrap_secret":"`+secret+`"}`, nil); w.Code != 409 {
				t.Fatalf("%s closed: %d", path, w.Code)
			}
		}
	}
	if w := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"synthetic-admin","password":"SyntheticPassword123!"}`, nil); w.Code != 200 {
		t.Fatalf("login budget was burned: %d", w.Code)
	}
}

func TestLegacyOwnerVerifyClosedAndOrdering(t *testing.T) {
	s, dir := ownerTestServer(t)
	secret, err := readOwnerBootstrap(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string, body map[string]string) int {
		return ownerCall(s, "POST", path, body, nil, true).Code
	}
	// Wrong secret outranks a field error.
	if c := call("/api/auth/setup", map[string]string{"bootstrap_secret": "wrong", "username": "a!", "email": "bad", "password": "x"}); c != 401 {
		t.Fatalf("secret first: %d", c)
	}
	if c := call("/api/auth/setup", map[string]string{"bootstrap_secret": secret, "username": "a!", "email": "bad", "password": "x"}); c != 400 {
		t.Fatalf("field error: %d", c)
	}
	if c := call("/api/auth/setup/verify", map[string]string{"bootstrap_secret": secret}); c != 200 {
		t.Fatalf("verify: %d", c)
	}
	if c := call("/api/auth/setup", map[string]string{"bootstrap_secret": secret, "username": "legacy-owner", "email": "founder@example.invalid", "password": testOwnerPassword}); c != 200 {
		t.Fatalf("setup: %d", c)
	}
	if c := call("/api/auth/setup/verify", map[string]string{"bootstrap_secret": secret}); c != 409 {
		t.Fatalf("closed verify: %d", c)
	}
}
