package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// browserJar keeps one host's cookies the way a browser does: a Secure cookie is never sent to a
// plain-http page, and a plain-http page cannot set or delete a cookie whose name an existing Secure
// cookie holds (RFC 6265bis "Leave Secure Cookies Alone"; Safari, Chrome and Firefox all apply it).
// Cookies are not port-specific, so https and http on the same port share one jar.
type browserJar map[string]*http.Cookie

func (j browserJar) take(w *httptest.ResponseRecorder, secure bool) {
	for _, c := range w.Result().Cookies() {
		if old, ok := j[c.Name]; ok && old.Secure && !secure {
			continue
		}
		if c.Secure && !secure {
			continue
		}
		if c.MaxAge < 0 {
			delete(j, c.Name)
			continue
		}
		j[c.Name] = c
	}
}

func (j browserJar) give(r *http.Request, secure bool) {
	for _, c := range j {
		if !c.Secure || secure {
			r.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
		}
	}
}

// browse sends one request over https (secure) or plain http from a LAN peer, through the jar.
func browse(h http.Handler, jar browserJar, secure bool, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "10.0.10.20:52000"
	if secure {
		r.TLS = &tls.ConnectionState{}
	}
	jar.give(r, secure)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	jar.take(w, secure)
	return w
}

func signedIn(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	var state struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatalf("session state %d %s", w.Code, w.Body.String())
	}
	return state.Authenticated
}

// The same port serves https and, with LAN HTTP allowed, plain http. A browser that already holds an
// https session and then signs in over http must end up signed in over http (it looped back to the
// sign-in page: the http Set-Cookie had the Secure cookie's name and the browser dropped it), keep its
// https session, and a sign-out over http must not end the https one.
func checkSignInOverBothTransports(t *testing.T, h http.Handler, login string) {
	t.Helper()
	jar := browserJar{}
	if w := browse(h, jar, true, "POST", "/api/auth/login", login); w.Code != 200 {
		t.Fatalf("https sign-in %d %s", w.Code, w.Body.String())
	}
	if w := browse(h, jar, false, "POST", "/api/auth/login", login); w.Code != 200 {
		t.Fatalf("http sign-in %d %s", w.Code, w.Body.String())
	}
	if !signedIn(t, browse(h, jar, false, "GET", "/api/auth/session", "")) {
		t.Fatalf("signed in over http, then the http page is signed out again (cookies %v)", jar)
	}
	if !signedIn(t, browse(h, jar, true, "GET", "/api/auth/session", "")) {
		t.Fatal("the https session was lost")
	}
	if w := browse(h, jar, false, "POST", "/api/auth/logout", `{}`); w.Code != 200 {
		t.Fatalf("http sign-out %d", w.Code)
	}
	if signedIn(t, browse(h, jar, false, "GET", "/api/auth/session", "")) {
		t.Fatal("still signed in over http after signing out there")
	}
	if !signedIn(t, browse(h, jar, true, "GET", "/api/auth/session", "")) {
		t.Fatal("signing out over http ended the https session")
	}
	if w := browse(h, jar, true, "POST", "/api/auth/logout", `{}`); w.Code != 200 {
		t.Fatalf("https sign-out %d", w.Code)
	}
	if signedIn(t, browse(h, jar, true, "GET", "/api/auth/session", "")) || len(jar) != 0 {
		t.Fatalf("after signing out over https: cookies %v", jar)
	}
}

func TestGatewaySignInOverHTTPWithAnHTTPSSessionInTheBrowser(t *testing.T) {
	g := accountFixture(t)
	g.auth.allowLAN = true
	if _, err := g.create(context.Background(), "lan-admin", "lan-admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true)); err != nil {
		t.Fatal(err)
	}
	checkSignInOverBothTransports(t, g.Handler(), `{"identifier":"lan-admin","password":"SyntheticPassword123!"}`)
}

func TestOwnerSignInOverHTTPWithAnHTTPSSessionInTheBrowser(t *testing.T) {
	s, dir := ownerTestServer(t)
	s.ownerAuth.allowLAN = true
	secret, err := readOwnerBootstrap(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	setup, _ := json.Marshal(map[string]string{"bootstrap_secret": secret, "username": "lan-owner", "email": "lan@example.invalid", "password": testOwnerPassword})
	if w := browse(s.Handler(), browserJar{}, true, "POST", "/api/auth/setup", string(setup)); w.Code != 200 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	login, _ := json.Marshal(map[string]string{"identifier": "lan-owner", "password": testOwnerPassword})
	checkSignInOverBothTransports(t, s.Handler(), string(login))
}
