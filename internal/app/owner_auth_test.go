package app

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testOwnerPassword = "synthetic-password-2984"

func ownerTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("TOFI_OWNER_AUTH", "")
	t.Setenv("TOFI_OWNER_ALLOW_LOOPBACK_HTTP", "")
	t.Setenv("TOFI_OWNER_ALLOW_LAN_HTTP", "")
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := initializeOwnerAuth(st, Config{DataDir: dir, Listen: "127.0.0.1:8321", OwnerAuth: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: st, ownerAuth: a}
	t.Cleanup(func() { a.close(); st.Close() })
	return s, dir
}
func ownerCall(s *Server, method, path string, body any, cookie *http.Cookie, secure bool) *httptest.ResponseRecorder {
	data, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
	r.RemoteAddr = "127.0.0.1:1234"
	if secure {
		r.TLS = &tls.ConnectionState{}
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func ownerSetup(t *testing.T, s *Server, dir string) *http.Cookie {
	t.Helper()
	secret, err := readOwnerBootstrap(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	w := ownerCall(s, "POST", "/api/auth/setup", map[string]string{"bootstrap_secret": secret, "username": "workspace-owner", "email": "owner@example.invalid", "password": testOwnerPassword}, nil, true)
	if w.Code != 200 {
		t.Fatalf("setup status=%d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("expected session cookie")
	}
	return cookies[0]
}
func TestOwnerBootstrapPersistenceAndWorkspaceRetention(t *testing.T) {
	s, dir := ownerTestServer(t)
	bot, err := s.store.CreateBot("Existing Bot", "instructions", "model")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "owner-bootstrap.secret")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("bootstrap permissions")
	}
	restarted, err := initializeOwnerAuth(s.store, Config{DataDir: dir, Listen: "127.0.0.1:8321"})
	if err != nil || restarted == nil {
		t.Fatalf("persistent setup: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("bootstrap rotated on restart")
	}
	cookie := ownerSetup(t, s, dir)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != int(ownerSessionLifetime.Seconds()) {
		t.Fatal("unsafe cookie flags")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("bootstrap file retained")
	}
	if _, err = s.store.GetBot(bot.ID); err != nil {
		t.Fatal("existing workspace lost")
	}
	var hash, salt, bootstrap []byte
	if err = s.store.db.QueryRow(`SELECT password_hash,salt FROM workspace_owner`).Scan(&hash, &salt); err != nil {
		t.Fatal(err)
	}
	if len(hash) != 32 || len(salt) != 16 || string(hash) == testOwnerPassword {
		t.Fatal("invalid password storage")
	}
	s.store.db.QueryRow(`SELECT bootstrap_hash FROM owner_auth_settings`).Scan(&bootstrap)
	if len(bootstrap) != 0 {
		t.Fatal("bootstrap not consumed")
	}
	if err = s.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := initializeOwnerAuth(st, Config{DataDir: dir, Listen: "127.0.0.1:8321"})
	if err != nil || a == nil {
		t.Fatalf("auth silently disabled: %v", err)
	}
	s2 := &Server{store: st, ownerAuth: a}
	w := ownerCall(s2, "GET", "/api/auth/session", nil, cookie, true)
	var state ownerSessionState
	json.Unmarshal(w.Body.Bytes(), &state)
	if !state.Authenticated || state.Owner.Username != "workspace-owner" {
		t.Fatal("session did not survive restart")
	}
	w = ownerCall(s2, "POST", "/api/auth/setup", map[string]string{"bootstrap_secret": strings.TrimSpace(string(before)), "username": "new-owner", "email": "new@example.invalid", "password": testOwnerPassword}, nil, true)
	if w.Code != 401 {
		t.Fatal("consumed bootstrap reused")
	}
}
func TestOwnerAuthenticationRouteBoundaries(t *testing.T) {
	s, dir := ownerTestServer(t)
	for _, path := range []string{"/api/bots", "/api/conversations", "/api/auth/codex", "/api/auth/codex/connect", "/api/workspace/events", "/api/attachments/a", "/api/bots/a/computer/stream", "/api/computers", "/api/computers/vm/info", "/api/auth/session/extra", "/api/extensions/mcp/a/oauth/callback/extra"} {
		w := ownerCall(s, "GET", path, nil, nil, true)
		if w.Code != 401 {
			t.Errorf("unguarded %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/computers/pairings", "/api/computers/vm/actions", "/api/bots/a/terminal"} {
		if w := ownerCall(s, "POST", path, map[string]any{}, nil, true); w.Code != 401 {
			t.Errorf("unguarded %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/auth/session", "/api/server-info", "/health"} {
		if w := ownerCall(s, "GET", path, nil, nil, false); w.Code != 200 {
			t.Errorf("public %s: %d", path, w.Code)
		}
	}
	if w := ownerCall(s, "GET", "/api/computers/missing/jobs", nil, nil, false); w.Code != 401 || !strings.Contains(w.Body.String(), "invalid device token") {
		t.Fatal("device token gate missing")
	}
	cookie := ownerSetup(t, s, dir)
	w := ownerCall(s, "GET", "/api/bots", nil, cookie, true)
	if w.Code != 200 {
		t.Fatalf("authenticated workspace %d", w.Code)
	}
	w = ownerCall(s, "GET", "/api/auth/session", nil, nil, true)
	if strings.Contains(w.Body.String(), "owner@example") {
		t.Fatal("identity disclosed")
	}
	w = ownerCall(s, "POST", "/api/auth/logout", nil, cookie, true)
	if w.Code != 200 {
		t.Fatal("logout")
	}
	if w = ownerCall(s, "GET", "/api/bots", nil, cookie, true); w.Code != 401 {
		t.Fatal("revoked session allowed")
	}
	for _, identifier := range []string{"workspace-owner", "OWNER@example.invalid"} {
		w = ownerCall(s, "POST", "/api/auth/login", map[string]string{"identifier": identifier, "password": testOwnerPassword}, nil, true)
		if w.Code != 200 {
			t.Errorf("login identifier %s: %d", identifier, w.Code)
		}
	}
}
func TestOwnerPasswordTransportCannotUseForwardedHeaders(t *testing.T) {
	s, _ := ownerTestServer(t)
	for _, tc := range []struct {
		allow bool
		peer  string
		tls   bool
		want  bool
	}{{false, "127.0.0.1:100", false, false}, {false, "192.0.2.1:100", true, true}, {true, "127.0.0.1:100", false, true}, {true, "[::1]:100", false, true}, {true, "172.17.0.1:100", false, false}, {true, "192.0.2.1:100", false, false}} {
		s.ownerAuth.allowLoopback = tc.allow
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`))
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("Forwarded", "for=127.0.0.1;proto=https")
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if got := s.ownerAuth.transportOK(r); got != tc.want {
			t.Errorf("transport %+v got %v", tc, got)
		}
	}
	s.ownerAuth.allowLoopback = false
	if w := ownerCall(s, "POST", "/api/auth/login", map[string]string{"identifier": "owner", "password": testOwnerPassword}, nil, false); w.Code != 400 {
		t.Fatal("HTTP accepted password")
	}
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	defer st.Close()
	a, err := initializeOwnerAuth(st, Config{DataDir: dir, Listen: "0.0.0.0:8321", OwnerAuth: true, OwnerAllowLoopbackHTTP: true})
	if err != nil || a.allowLoopback {
		t.Fatal("wildcard listener trusted")
	}
}
func TestOwnerLoginBoundsAndCSRF(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	for i := 0; i < 7; i++ {
		w := ownerCall(s, "POST", "/api/auth/login", map[string]string{"identifier": "workspace-owner", "password": "wrong"}, nil, true)
		if w.Code != 401 {
			t.Fatalf("attempt %d: %d", i, w.Code)
		}
	}
	w := ownerCall(s, "POST", "/api/auth/login", map[string]string{"identifier": "workspace-owner", "password": testOwnerPassword}, nil, true)
	if w.Code != 429 {
		t.Fatal("unbounded attempts")
	}
	r := httptest.NewRequest("POST", "/api/auth/logout", nil)
	r.TLS = &tls.ConnectionState{}
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("cross-site logout accepted")
	}
	// Restarting does not reset the attempt budget.
	a, err := initializeOwnerAuth(s.store, Config{DataDir: dir, Listen: "127.0.0.1:8321"})
	if err != nil {
		t.Fatal(err)
	}
	s.ownerAuth = a
	w = ownerCall(s, "POST", "/api/auth/login", map[string]string{}, nil, true)
	if w.Code != 429 {
		t.Fatal("restart reset limits")
	}
}
func TestOwnerSessionCancellationAndExpiry(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	request := func() (*http.Request, func()) {
		r := httptest.NewRequest("GET", "/api/workspace/events", nil)
		r.TLS = &tls.ConnectionState{}
		r.AddCookie(cookie)
		r, cleanup, ok := s.ownerAuthorized(httptest.NewRecorder(), r)
		if !ok {
			t.Fatal("unauthorized")
		}
		return r, cleanup
	}
	r, cleanup := request()
	defer cleanup()
	ownerCall(s, "POST", "/api/auth/logout", nil, cookie, true)
	select {
	case <-r.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("logout did not terminate stream context")
	}
	w := ownerCall(s, "POST", "/api/auth/login", map[string]string{"identifier": "workspace-owner", "password": testOwnerPassword}, nil, true)
	cookie = w.Result().Cookies()[0]
	hash := sha256.Sum256([]byte(cookie.Value))
	if _, err := s.store.db.Exec(`UPDATE owner_sessions SET expires_at=? WHERE token_hash=?`, time.Now().Unix()+1, hash[:]); err != nil {
		t.Fatal(err)
	}
	r, cleanup2 := request()
	defer cleanup2()
	select {
	case <-r.Context().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("expiry did not terminate stream context")
	}
	if w = ownerCall(s, "GET", "/api/bots", nil, cookie, true); w.Code != 401 {
		t.Fatal("expired session authorized")
	}
}
func TestOwnerBootstrapRejectsUnsafeFilesAndConcurrentClaim(t *testing.T) {
	s, dir := ownerTestServer(t)
	path := filepath.Join(dir, "owner-bootstrap.secret")
	secret, _ := readOwnerBootstrap(path)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeOwnerAuth(s.store, Config{DataDir: dir}); err == nil {
		t.Fatal("world-readable bootstrap accepted")
	}
	os.Chmod(path, 0600)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := ownerCall(s, "POST", "/api/auth/setup", map[string]string{"bootstrap_secret": secret, "username": "synthetic-owner", "email": "synthetic@example.invalid", "password": testOwnerPassword}, nil, true)
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	successes := 0
	for code := range codes {
		if code == 200 {
			successes++
		} else if code != 401 && code != 409 {
			t.Fatalf("unexpected status %d", code)
		}
	}
	if successes != 1 {
		t.Fatalf("owner setup winners %d", successes)
	}
}
func TestOwnerLegacyRemainsDisabledWithoutOptIn(t *testing.T) {
	t.Setenv("TOFI_OWNER_AUTH", "")
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	defer st.Close()
	a, err := initializeOwnerAuth(st, Config{DataDir: dir})
	if err != nil || a != nil {
		t.Fatalf("legacy changed %v", err)
	}
	s := &Server{store: st}
	w := ownerCall(s, "GET", "/api/auth/session", nil, nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal("legacy session")
	}
	w = ownerCall(s, "GET", "/api/bots", nil, nil, false)
	if w.Code != 200 {
		t.Fatal("legacy blocked")
	}
	if _, err = os.Stat(filepath.Join(dir, "owner-bootstrap.secret")); !os.IsNotExist(err) {
		t.Fatal("legacy created setup secret")
	}
}

func TestOwnerLogoutStopsWorkspaceSSE(t *testing.T) {
	s, dir := ownerTestServer(t)
	cookie := ownerSetup(t, s, dir)
	done := make(chan struct{})
	go func() { defer close(done); ownerCall(s, "GET", "/api/workspace/events", nil, cookie, true) }()
	deadline := time.Now().Add(time.Second)
	for {
		s.ownerAuth.mu.Lock()
		active := len(s.ownerAuth.active)
		s.ownerAuth.mu.Unlock()
		if active > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SSE did not register")
		}
		time.Sleep(time.Millisecond)
	}
	ownerCall(s, "POST", "/api/auth/logout", nil, cookie, true)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE remained open after logout")
	}
}
func TestOwnerBootstrapRejectsMissingAndSymlink(t *testing.T) {
	s, dir := ownerTestServer(t)
	path := filepath.Join(dir, "owner-bootstrap.secret")
	secret, _ := os.ReadFile(path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := initializeOwnerAuth(s.store, Config{DataDir: dir}); err == nil {
		t.Fatal("missing persisted bootstrap regenerated")
	}
	target := filepath.Join(dir, "target")
	os.WriteFile(target, secret, 0600)
	os.Symlink(target, path)
	if _, err := initializeOwnerAuth(s.store, Config{DataDir: dir}); err == nil {
		t.Fatal("symlink bootstrap accepted")
	}
}
func TestOwnerIndependentRouteExactness(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/server-info", true}, {"POST", "/api/server-info", false},
		{"POST", "/api/computers/pair", true}, {"POST", "/api/computers/pairings", false},
		{"PATCH", "/api/computers/id/capabilities", true}, {"GET", "/api/computers/id/jobs", true},
		{"POST", "/api/computers/id/jobs/job/result", true}, {"POST", "/api/computers/id/jobs/job/result/extra", false},
		{"GET", "/api/extensions/mcp/name/oauth/callback", true}, {"GET", "/api/extensions/mcp/name/extra/oauth/callback", false},
		{"POST", "/api/extensions/mcp/name/oauth/callback", false}, {"GET", "/api/extensions/mcp/name/oauth/status", false},
		{"GET", "/api/extensions/local-mcp/gog/oauth/callback", true}, {"POST", "/api/extensions/local-mcp/gog/oauth/callback", false},
	} {
		if got := ownerIndependentRoute(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.want {
			t.Errorf("%s %s = %v", tc.method, tc.path, got)
		}
	}
}

func TestOwnerDeviceAuthenticationStillWorks(t *testing.T) {
	s, _ := ownerTestServer(t)
	code, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	w := ownerCall(s, "POST", "/api/computers/pair", map[string]any{"code": code, "name": "synthetic-device", "platform": "darwin", "capabilities": []string{"files.read"}}, nil, false)
	if w.Code != 201 {
		t.Fatalf("pairing status %d", w.Code)
	}
	var device struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &device); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/computers/"+device.ID+"/jobs", nil)
	r.Header.Set("Authorization", "Bearer "+device.Token)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("independent device authentication blocked: %d", w.Code)
	}
}
func TestOwnerNewServerWiresDurableGate(t *testing.T) {
	t.Setenv("TOFI_OWNER_AUTH", "1")
	t.Setenv("TOFI_OWNER_ALLOW_LOOPBACK_HTTP", "")
	t.Setenv("TOFI_OWNER_ALLOW_LAN_HTTP", "")
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, Environment: "acceptance", Provider: "synthetic", Listen: "127.0.0.1:8321"})
	if err != nil {
		t.Fatal(err)
	}
	if w := ownerCall(s, "GET", "/api/bots", nil, nil, true); w.Code != 401 {
		t.Fatal("NewServer did not install gate")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOFI_OWNER_AUTH", "")
	s, err = NewServer(Config{DataDir: dir, Environment: "acceptance", Provider: "synthetic", Listen: "127.0.0.1:8321"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.ownerAuthMode() != "owner-password" {
		t.Fatal("NewServer silently disabled authentication")
	}
}

func TestOwnerCannotBeClaimedWithoutOperatorSecret(t *testing.T) {
	s, _ := ownerTestServer(t)
	w := ownerCall(s, "POST", "/api/auth/setup", map[string]string{"username": "visitor", "email": "visitor@example.invalid", "password": testOwnerPassword}, nil, true)
	if w.Code != 401 {
		t.Fatalf("visitor claim status %d", w.Code)
	}
	var count int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM workspace_owner`).Scan(&count); err != nil || count != 0 {
		t.Fatal("owner created without operator secret")
	}
	w = ownerCall(s, "GET", "/api/auth/session", nil, nil, true)
	var state ownerSessionState
	json.Unmarshal(w.Body.Bytes(), &state)
	if !state.SetupRequired || state.Authenticated {
		t.Fatal("unexpected setup state")
	}
}

func TestOwnerLANHTTPPeerBoundary(t *testing.T) {
	s, _ := ownerTestServer(t)
	for _, peer := range []string{"10.1.2.3:100", "172.16.0.1:100", "172.31.255.254:100", "192.168.1.2:100", "127.0.0.1:100", "[::1]:100", "[fd12::1]:100", "[fc00::1]:100", "[::ffff:192.168.1.2]:100", "[::ffff:127.0.0.1]:100"} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = peer
		s.ownerAuth.allowLAN = false
		if s.ownerAuth.transportOK(r) {
			t.Errorf("LAN permitted without opt-in: %s", peer)
		}
		s.ownerAuth.allowLAN = true
		if !s.ownerAuth.transportOK(r) {
			t.Errorf("private peer rejected: %s", peer)
		}
	}
	s.ownerAuth.allowLAN = true
	for _, peer := range []string{"8.8.8.8:100", "172.15.255.255:100", "172.32.0.1:100", "192.0.2.1:100", "100.64.0.1:100", "169.254.1.1:100", "0.0.0.0:100", "[::]:100", "[fe80::1]:100", "[2001:4860::1]:100", "[::ffff:8.8.8.8]:100", "private.example:100", "malformed"} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", "192.168.1.1")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("Forwarded", "for=10.1.1.1;proto=https")
		if s.ownerAuth.transportOK(r) {
			t.Errorf("untrusted peer accepted: %s", peer)
		}
	}
}
func TestOwnerLANHTTPConfigurationAndDiscovery(t *testing.T) {
	s, dir := ownerTestServer(t)
	for _, viaEnv := range []bool{false, true} {
		if viaEnv {
			t.Setenv("TOFI_OWNER_ALLOW_LAN_HTTP", "1")
		}
		a, err := initializeOwnerAuth(s.store, Config{DataDir: dir, Listen: "0.0.0.0:8321", OwnerAllowLANHTTP: !viaEnv})
		if err != nil {
			t.Fatal(err)
		}
		s.ownerAuth = a
		if !a.allowLAN || a.allowLoopback {
			t.Fatal("LAN configuration affected strict loopback mode")
		}
		w := ownerCall(s, "GET", "/api/server-info", nil, nil, false)
		var info struct {
			Auth struct {
				Mode string `json:"mode"`
				LAN  bool   `json:"lan_http"`
			} `json:"auth"`
		}
		json.Unmarshal(w.Body.Bytes(), &info)
		if info.Auth.Mode != "owner-password" || !info.Auth.LAN {
			t.Fatal("missing explicit LAN discovery")
		}
	}
	t.Setenv("TOFI_OWNER_ALLOW_LAN_HTTP", "")
	a, err := initializeOwnerAuth(s.store, Config{DataDir: dir, Listen: "0.0.0.0:8321"})
	if err != nil {
		t.Fatal(err)
	}
	s.ownerAuth = a
	if a.allowLAN || strings.Contains(ownerCall(s, "GET", "/api/server-info", nil, nil, false).Body.String(), `"lan_http":true`) {
		t.Fatal("LAN opt-in persisted unexpectedly")
	}
	s.ownerAuth = nil
	if strings.Contains(ownerCall(s, "GET", "/api/server-info", nil, nil, false).Body.String(), `"lan_http":true`) {
		t.Fatal("legacy advertised LAN owner auth")
	}
}
func TestOwnerLANHTTPSetupLoginSessionAndRevocation(t *testing.T) {
	s, dir := ownerTestServer(t)
	s.ownerAuth.allowLAN = true
	call := func(method, path string, body any, cookie *http.Cookie, peer string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(data)))
		r.RemoteAddr = peer
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	secret, err := readOwnerBootstrap(filepath.Join(dir, "owner-bootstrap.secret"))
	if err != nil {
		t.Fatal(err)
	}
	w := call("POST", "/api/auth/setup", map[string]string{"bootstrap_secret": secret, "username": "lan-owner", "email": "lan@example.invalid", "password": testOwnerPassword}, nil, "172.17.0.1:100")
	if w.Code != 200 {
		t.Fatalf("HTTP setup status %d", w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("invalid LAN cookie flags")
	}
	for _, peer := range []string{"192.168.1.1:100", "[::ffff:10.1.1.1]:100"} {
		w = call("GET", "/api/auth/session", nil, cookie, peer)
		var state ownerSessionState
		json.Unmarshal(w.Body.Bytes(), &state)
		if !state.Authenticated || !state.PasswordTransportAllowed {
			t.Fatal("LAN session rejected")
		}
		if call("GET", "/api/bots", nil, cookie, peer).Code != 200 {
			t.Fatal("LAN workspace blocked")
		}
	}
	if call("GET", "/api/bots", nil, cookie, "8.8.8.8:100").Code != 401 {
		t.Fatal("HTTP public session accepted")
	}
	if call("POST", "/api/auth/login", map[string]string{"identifier": "lan-owner", "password": testOwnerPassword}, nil, "8.8.8.8:100").Code != 400 {
		t.Fatal("HTTP public password accepted")
	}
	s.ownerAuth.allowLAN = false
	if call("GET", "/api/bots", nil, cookie, "192.168.1.1:100").Code != 401 {
		t.Fatal("session bypassed disabled LAN option")
	}
	s.ownerAuth.allowLAN = true
	if call("POST", "/api/auth/logout", nil, cookie, "192.168.1.1:100").Code != 200 {
		t.Fatal("HTTP logout failed")
	}
	if call("GET", "/api/bots", nil, cookie, "192.168.1.1:100").Code != 401 {
		t.Fatal("HTTP session not revoked")
	}
	w = call("POST", "/api/auth/login", map[string]string{"identifier": "lan-owner", "password": testOwnerPassword}, nil, "192.168.1.1:100")
	if w.Code != 200 || w.Result().Cookies()[0].Secure {
		t.Fatal("HTTP login failed")
	}
}
