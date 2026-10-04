package app

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func webhookHTTP(method, path, body, secret string, cookie *http.Cookie, handler http.Handler, change func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:2345"
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tofi-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func webhookOwnerFixture(t *testing.T, engine bool) (*Server, string, *http.Cookie, Bot) {
	t.Helper()
	webhookSyntheticEnvironment(t)
	var s *Server
	var dir string
	if engine {
		dir = t.TempDir()
		var err error
		s, err = NewServer(Config{DataDir: dir, Environment: "acceptance", OwnerAuth: true, Engine: testEngine{}, PublicOrigin: "https://synthetic.example.invalid"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
	} else {
		s, dir = ownerTestServer(t)
		s.instance = instanceIdentity{ID: "synthetic-instance", Environment: "acceptance"}
		s.publicOrigin = "https://synthetic.example.invalid"
		s.webhookStandalone = true
		s.webhookLimits = newWebhookLimiter()
		if err := migrateWebhookRegistry(s.store.db); err != nil {
			t.Fatal(err)
		}
		if err := migrateWebhookIngress(s.store.db); err != nil {
			t.Fatal(err)
		}
	}
	cookie := ownerSetup(t, s, dir)
	b, err := s.store.CreateBot("Synthetic HTTP bot", "", "synthetic-model")
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, cookie, b
}

func webhookSyntheticEnvironment(t *testing.T) {
	t.Helper()
	// Never inherit live service sockets, extension configuration or credentials
	// from the developer shell into these disposable source fixtures.
	for _, key := range []string{"TOFI_COMPUTER_SOCKET", "TOFI_MCP_CONFIG", "TOFI_SKILLS_DIR", "TOFI_TRANSCRIPTION_API_KEY"} {
		t.Setenv(key, "")
	}
}

func webhookMetadataResponse(t *testing.T, w *httptest.ResponseRecorder, status int) webhookMetadata {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status=%d want=%d body=%s", w.Code, status, w.Body.String())
	}
	var m webhookMetadata
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestWebhookManagementOneTimeSecretLifecycleAndCAS(t *testing.T) {
	s, dir, cookie, b := webhookOwnerFixture(t, false)
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return webhookHTTP(method, path, body, "", cookie, s.Handler(), nil)
	}
	if m := webhookMetadataResponse(t, call("GET", path, ""), 200); m.Configured || m.Secret != "" || webhookCount(t, s.store, "webhook_endpoints") != 0 || webhookCount(t, s.store, "webhook_targets") != 0 {
		t.Fatal("metadata lookup generated a grant or target identity")
	}
	created := webhookMetadataResponse(t, call("POST", path, `{}`), 201)
	if !created.Enabled || len(created.Secret) != 43 || created.HookID == "" || created.Version != 1 || created.URL != "https://synthetic.example.invalid/api/webhooks/"+created.HookID || strings.Contains(created.URL, created.Secret) {
		t.Fatalf("create response %+v", created)
	}
	e, err := s.store.webhookByHook(context.Background(), created.HookID)
	if err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(created.Secret))
	if string(e.TokenHash) != string(wantHash[:]) || e.AccountID != "standalone-owner" || e.WorkspaceID != s.instance.ID || !webhookMatches(e, created.Secret) {
		t.Fatal("stored binding/hash differs from issued key")
	}
	if m := webhookMetadataResponse(t, call("GET", path, ""), 200); m.Secret != "" || m.HookID != created.HookID {
		t.Fatal("GET revealed key or changed binding")
	}
	if w := call("POST", path, `{}`); w.Code != 409 || strings.Contains(w.Body.String(), created.Secret) {
		t.Fatal("implicit reveal/recreate permitted")
	}
	rotated := webhookMetadataResponse(t, call("POST", path+"/rotate", `{"expected_version":1}`), 200)
	if rotated.Secret == created.Secret || len(rotated.Secret) != 43 || rotated.HookID != created.HookID || rotated.Version != 2 {
		t.Fatalf("rotation %+v", rotated)
	}
	for _, method := range []string{"POST", "DELETE"} {
		p := path
		if method == "POST" {
			p += "/rotate"
		}
		if w := call(method, p, `{"expected_version":1}`); w.Code != 409 {
			t.Fatalf("stale %s status %d", method, w.Code)
		}
	}
	payload := `{"event_id":"synthetic-event","content":"Synthetic event"}`
	if w := webhookHTTP("POST", "/api/webhooks/"+created.HookID, payload, created.Secret, nil, s.Handler(), nil); w.Code != 401 {
		t.Fatalf("old key valid after rotation: %d %s", w.Code, w.Body.String())
	}
	// A valid new key reaches model-unavailable admission rather than auth failure.
	if w := webhookHTTP("POST", "/api/webhooks/"+rotated.HookID, payload, rotated.Secret, nil, s.Handler(), nil); w.Code != 503 || strings.Contains(w.Body.String(), payload) {
		t.Fatalf("new key status=%d %s", w.Code, w.Body.String())
	}
	if w := call("DELETE", path, `{"expected_version":2}`); w.Code != 204 {
		t.Fatalf("revoke %d %s", w.Code, w.Body.String())
	}
	if w := webhookHTTP("POST", "/api/webhooks/"+rotated.HookID, payload, rotated.Secret, nil, s.Handler(), nil); w.Code != 401 {
		t.Fatalf("revoked key %d", w.Code)
	}
	metadata := webhookMetadataResponse(t, call("GET", path, ""), 200)
	if metadata.Enabled || metadata.Secret != "" || metadata.Version != 3 {
		t.Fatalf("revoked metadata %+v", metadata)
	}
	reenabled := webhookMetadataResponse(t, call("POST", path, `{}`), 201)
	if reenabled.HookID != created.HookID || reenabled.Version != 4 || reenabled.Secret == rotated.Secret {
		t.Fatalf("explicit re-enable %+v", reenabled)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(filepath.Join(dir, "tofi.db") + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, secret := range []string{created.Secret, rotated.Secret, reenabled.Secret} {
			if strings.Contains(string(data), secret) {
				t.Fatal("plaintext secret persisted in SQLite")
			}
		}
	}
	for _, table := range []string{"messages", "runs", "events", "webhook_deliveries"} {
		if webhookCount(t, s.store, table) != 0 {
			t.Fatalf("management/denial created %s", table)
		}
	}
}

func TestWebhookManagementOwnerAuthenticationAndCSRF(t *testing.T) {
	s, _, cookie, b := webhookOwnerFixture(t, false)
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	for _, tc := range []struct{ method, path, body string }{{"POST", path, `{}`}, {"POST", path + "/rotate", `{"expected_version":1}`}, {"DELETE", path, `{"expected_version":1}`}} {
		if w := webhookHTTP(tc.method, tc.path, tc.body, "", nil, s.Handler(), nil); w.Code != 401 {
			t.Fatalf("unauthenticated %s %d", tc.path, w.Code)
		}
		if w := webhookHTTP(tc.method, tc.path, tc.body, "", cookie, s.Handler(), func(r *http.Request) { r.Header.Set("Origin", "https://evil.example.invalid") }); w.Code != 403 {
			t.Fatalf("CSRF %s %d", tc.path, w.Code)
		}
	}
	for _, body := range []string{`{"account_id":"other"}`, `{"secret":"synthetic"}`, `{"expected_version":1}`, `{} {}`} {
		if w := webhookHTTP("POST", path, body, "", cookie, s.Handler(), nil); w.Code != 400 {
			t.Fatalf("create envelope %q status %d", body, w.Code)
		}
	}
	if webhookCount(t, s.store, "webhook_endpoints") != 0 {
		t.Fatal("denied management created grant")
	}
	s.webhookStandalone = false
	if w := webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil); w.Code != 401 {
		t.Fatalf("auth-disabled standalone management %d", w.Code)
	}
	if w := webhookHTTP("POST", "/api/webhooks/synthetic", `{"event_id":"1","content":"event"}`, strings.Repeat("a", 43), nil, s.Handler(), nil); w.Code != 401 {
		t.Fatalf("auth-disabled standalone ingress %d", w.Code)
	}
}

func TestWebhookStrictEnvelopeAndRequestBounds(t *testing.T) {
	valid := `{"event_id":"sender-1._:safe","content":"Synthetic UTF-8 event 确认"}`
	for _, tc := range []struct {
		name, body string
		want       int
		change     func(*http.Request)
	}{
		{"valid", valid, 0, nil},
		{"whitespace", ` { "content" : "Synthetic event", "event_id" : "1" } `, 0, nil},
		{"unknown target", `{"event_id":"1","content":"event","conversation_id":"other"}`, 400, nil},
		{"unknown account", `{"event_id":"1","content":"event","account_id":"other"}`, 400, nil},
		{"unknown approval", `{"event_id":"1","content":"yes","approval_id":"copied"}`, 400, nil},
		{"duplicate key", `{"event_id":"1","event_id":"2","content":"event"}`, 400, nil},
		{"duplicate escaped key", `{"event_id":"1","event\u005fid":"2","content":"event"}`, 400, nil},
		{"missing content", `{"event_id":"1"}`, 400, nil},
		{"empty content", `{"event_id":"1","content":" \n "}`, 400, nil},
		{"number content", `{"event_id":"1","content":1}`, 400, nil},
		{"null content", `{"event_id":"1","content":null}`, 400, nil},
		{"unsafe ID", `{"event_id":"one/two","content":"event"}`, 400, nil},
		{"overlong ID", fmt.Sprintf(`{"event_id":%q,"content":"event"}`, strings.Repeat("a", 129)), 400, nil},
		{"nonASCII ID", `{"event_id":"确认","content":"event"}`, 400, nil},
		{"content over 16KiB", fmt.Sprintf(`{"event_id":"1","content":%q}`, strings.Repeat("a", (16<<10)+1)), 400, nil},
		{"body over 32KiB chunked", strings.Repeat(" ", 32<<10) + valid, 413, func(r *http.Request) { r.ContentLength = -1; r.TransferEncoding = []string{"chunked"} }},
		{"invalid UTF8", "{\"event_id\":\"1\",\"content\":\"" + string([]byte{255}) + "\"}", 400, nil},
		{"multiple objects", valid + valid, 400, nil},
		{"array", `[]`, 400, nil},
		{"media type", valid, 415, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
		{"encoding", valid, 415, func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }},
		{"missing timestamp", valid, 400, func(r *http.Request) { r.Header.Del("X-Tofi-Timestamp") }},
		{"stale timestamp", valid, 400, func(r *http.Request) {
			r.Header.Set("X-Tofi-Timestamp", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10))
		}},
		{"future timestamp", valid, 400, func(r *http.Request) {
			r.Header.Set("X-Tofi-Timestamp", strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10))
		}},
		{"overflow timestamp", valid, 400, func(r *http.Request) { r.Header.Set("X-Tofi-Timestamp", "9223372036854775808") }},
		{"oversize headers", valid, 400, func(r *http.Request) { r.Header.Set("X-Synthetic", strings.Repeat("a", 8193)) }},
		{"query", valid, 400, func(r *http.Request) { r.URL.RawQuery = "secret=synthetic" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/webhooks/synthetic", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Tofi-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
			if tc.change != nil {
				tc.change(r)
			}
			_, status := webhookInput(httptest.NewRecorder(), r)
			if status != tc.want {
				t.Fatalf("status=%d want=%d", status, tc.want)
			}
		})
	}
}

func TestWebhookExactPublicRouteTransportAndGenericRejections(t *testing.T) {
	s, _, _, _ := webhookOwnerFixture(t, false)
	body := `{"event_id":"1","content":"synthetic payload never echoed"}`
	for _, path := range []string{"/api/webhooks", "/api/webhooks/", "/api/webhooks/a/extra", "/api/webhooks/a/", "/api/webhooks/a/rotate"} {
		if _, ok := webhookPublicPath(path); ok {
			t.Fatalf("broad public exception %s", path)
		}
		if w := webhookHTTP("POST", path, body, strings.Repeat("a", 43), nil, s.Handler(), nil); w.Code != 401 {
			t.Fatalf("nonexact route auth %s %d", path, w.Code)
		}
	}
	for _, tc := range []struct {
		name, method, secret string
		want                 int
		change               func(*http.Request)
	}{
		{"unknown hook", "POST", strings.Repeat("a", 43), 401, nil},
		{"missing bearer", "POST", "", 401, nil},
		{"short bearer", "POST", "a", 401, nil},
		{"duplicate auth", "POST", strings.Repeat("a", 43), 401, func(r *http.Request) { r.Header.Add("Authorization", "Bearer "+strings.Repeat("b", 43)) }},
		{"wrong method", "GET", strings.Repeat("a", 43), 405, nil},
		{"Origin rejected", "POST", strings.Repeat("a", 43), 403, func(r *http.Request) { r.Header.Set("Origin", "https://synthetic.example.invalid") }},
		{"crosssite rejected", "POST", strings.Repeat("a", 43), 403, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
		{"forwarded transport rejected", "POST", strings.Repeat("a", 43), 401, func(r *http.Request) {
			r.TLS = nil
			r.RemoteAddr = "192.0.2.1:4000"
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := webhookHTTP(tc.method, "/api/webhooks/synthetic-hook", body, tc.secret, nil, s.Handler(), tc.change)
			if w.Code != tc.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "synthetic payload") || strings.Contains(w.Body.String(), "synthetic-hook") || strings.Contains(w.Body.String(), tc.secret) && len(tc.secret) >= 32 {
				t.Fatal("rejection leaked request material")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("ingress security response headers missing")
			}
			if tc.want == 405 && w.Header().Get("Allow") != "POST" {
				t.Fatal("Allow missing")
			}
		})
	}
	if webhookCount(t, s.store, "messages") != 0 || webhookCount(t, s.store, "webhook_deliveries") != 0 {
		t.Fatal("rejected ingress had effects")
	}
}

func TestWebhookAccountIsolationAndDeniedIngressNeverConstructsRuntime(t *testing.T) {
	webhookSyntheticEnvironment(t)
	g := accountFixture(t)
	g.root.publicOrigin = "https://synthetic.example.invalid"
	var factoryCalls atomic.Int32
	g.runtimeFactory = func(Config) (*Server, error) {
		factoryCalls.Add(1)
		return nil, errors.New("synthetic runtime must not be constructed")
	}
	var accounts []Account
	var endpoints []webhookEndpoint
	var secrets []string
	for i := 0; i < 2; i++ {
		a, err := g.create(context.Background(), fmt.Sprintf("synthetic-webhook-%d", i), "", "SyntheticPassword123!", i == 0, accountCreationSecret(t, g, i == 0))
		if err != nil {
			t.Fatal(err)
		}
		webhookExec(t, g.root.store, `UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID)
		secret, hash, err := webhookKey()
		if err != nil {
			t.Fatal(err)
		}
		e := webhookEndpoint{HookID: newID(), AccountID: a.ID, WorkspaceID: "synthetic-workspace", ConversationID: newID(), TargetIdentity: newID(), CreatedBy: a.ID, TokenHash: hash, Version: 1}
		webhookExec(t, g.root.store, `INSERT INTO webhook_endpoints(`+webhookEndpointColumns+`) VALUES(?,?,?,?,?,?,?,?,NULL,?,?)`, e.HookID, e.AccountID, e.WorkspaceID, e.ConversationID, e.TargetIdentity, e.CreatedBy, e.TokenHash, e.Version, now(), now())
		accounts = append(accounts, a)
		endpoints = append(endpoints, e)
		secrets = append(secrets, secret)
	}
	body := `{"event_id":"1","content":"Synthetic event"}`
	request := func(hook, secret string) *httptest.ResponseRecorder {
		return webhookHTTP("POST", "/api/webhooks/"+hook, body, secret, nil, g.Handler(), nil)
	}
	for _, pair := range [][2]string{{endpoints[0].HookID, secrets[1]}, {endpoints[1].HookID, secrets[0]}, {"unknown", secrets[0]}, {endpoints[0].HookID, ""}} {
		if w := request(pair[0], pair[1]); w.Code != 401 {
			t.Fatalf("mixed/unknown authentication %d %s", w.Code, w.Body.String())
		}
	}
	webhookExec(t, g.root.store, `UPDATE accounts SET disabled=1 WHERE id=?`, accounts[0].ID)
	if w := request(endpoints[0].HookID, secrets[0]); w.Code != 401 {
		t.Fatalf("disabled account %d", w.Code)
	}
	webhookExec(t, g.root.store, `UPDATE accounts SET must_change_password=1 WHERE id=?`, accounts[1].ID)
	if w := request(endpoints[1].HookID, secrets[1]); w.Code != 401 {
		t.Fatalf("forced reset account %d", w.Code)
	}
	webhookExec(t, g.root.store, `UPDATE accounts SET disabled=0,must_change_password=0 WHERE id=?`, accounts[0].ID)
	webhookExec(t, g.root.store, `UPDATE webhook_endpoints SET revoked_at=? WHERE hook_id=?`, now(), endpoints[0].HookID)
	if w := request(endpoints[0].HookID, secrets[0]); w.Code != 401 {
		t.Fatalf("revoked account hook %d", w.Code)
	}
	if factoryCalls.Load() != 0 {
		t.Fatalf("rejected requests invoked runtime factory %d times", factoryCalls.Load())
	}
}

func TestWebhookAccountManagementCannotGuessOtherWorkspace(t *testing.T) {
	webhookSyntheticEnvironment(t)
	g := accountFixture(t)
	g.root.publicOrigin = "https://synthetic.example.invalid"
	var accounts []Account
	var bots []Bot
	for i := 0; i < 2; i++ {
		a, err := g.create(context.Background(), fmt.Sprintf("synthetic-owner-%d", i), "", "SyntheticPassword123!", i == 0, accountCreationSecret(t, g, i == 0))
		if err != nil {
			t.Fatal(err)
		}
		webhookExec(t, g.root.store, `UPDATE accounts SET must_change_password=0 WHERE id=?`, a.ID)
		s, err := g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.store.CreateBot("Synthetic account bot", "", "model")
		if err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, a)
		bots = append(bots, b)
	}
	cookie := accountCookie(t, g, accounts[0])
	other := "/api/conversations/" + bots[1].DMConversationID + "/webhook"
	for _, tc := range []struct{ method, path, body string }{{"GET", other, ""}, {"POST", other, `{}`}, {"POST", other + "/rotate", `{"expected_version":1}`}, {"DELETE", other, `{"expected_version":1}`}} {
		w := webhookHTTP(tc.method, tc.path, tc.body, "", cookie, g.Handler(), nil)
		if w.Code != 409 {
			t.Fatalf("cross-workspace %s %d %s", tc.method, w.Code, w.Body.String())
		}
	}
	own := "/api/conversations/" + bots[0].DMConversationID + "/webhook"
	m := webhookMetadataResponse(t, webhookHTTP("POST", own, `{}`, "", cookie, g.Handler(), func(r *http.Request) {
		r.Host = "evil.example.invalid"
		r.Header.Set("X-Forwarded-Host", "evil.example.invalid")
	}), 201)
	e, err := g.root.store.webhookByHook(context.Background(), m.HookID)
	if err != nil || e.AccountID != accounts[0].ID || strings.Contains(m.URL, "evil") {
		t.Fatalf("stored account or trusted origin %+v %v", e, err)
	}
}

func TestWebhookLimiterBoundsActualPeerAndExpiresEntries(t *testing.T) {
	l := newWebhookLimiter()
	request := httptest.NewRequest("POST", "/api/webhooks/synthetic", nil)
	request.RemoteAddr = "192.0.2.1:1000"
	request.Header.Set("X-Forwarded-For", "198.51.100.1")
	for i := 0; i < 60; i++ {
		release, ok := l.acquire(request)
		if !ok {
			t.Fatalf("request %d rejected early", i)
		}
		release()
	}
	request.Header.Set("X-Forwarded-For", "198.51.100.2")
	if _, ok := l.acquire(request); ok {
		t.Fatal("forwarded address bypassed actual peer limit")
	}
	l.mu.Lock()
	l.peers["192.0.2.1"] = webhookBudget{start: time.Now().Add(-2 * time.Minute), count: 60}
	l.mu.Unlock()
	if release, ok := l.acquire(request); !ok {
		t.Fatal("expired peer bucket retained")
	} else {
		release()
	}
	l = newWebhookLimiter()
	var releases []func()
	for i := 0; i < 32; i++ {
		release, ok := l.acquire(request)
		if !ok {
			t.Fatal("HTTP inflight prematurely full")
		}
		releases = append(releases, release)
	}
	if _, ok := l.acquire(request); ok {
		t.Fatal("HTTP inflight exceeded 32")
	}
	for _, release := range releases {
		release()
	}
	if release, ok := l.acquire(request); !ok {
		t.Fatal("HTTP inflight permit leaked")
	} else {
		release()
	}
	l = newWebhookLimiter()
	l.global = webhookBudget{start: time.Now(), count: 300}
	if _, ok := l.acquire(request); ok {
		t.Fatal("global request cap ignored")
	}
	l = newWebhookLimiter()
	for i := 0; i < 1024; i++ {
		l.peers[fmt.Sprint(i)] = webhookBudget{start: time.Now()}
	}
	if _, ok := l.acquire(request); ok || len(l.peers) != 1024 {
		t.Fatal("peer limiter map is unbounded")
	}
}

func TestWebhookRevocationAndRotationRacesDoNotAdmitStaleGeneration(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(fmt.Sprintf("rotate=%v", rotate), func(t *testing.T) {
			s, _, cookie, b := webhookOwnerFixture(t, true)
			path := "/api/conversations/" + b.DMConversationID + "/webhook"
			created := webhookMetadataResponse(t, webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil), 201)
			// Hold the shared commit fence so ingress and owner mutation compete at
			// one release. Either admission committed first or it is unauthorized.
			s.purgeMu.Lock()
			start := make(chan struct{})
			type raceResult struct {
				mutation bool
				response *httptest.ResponseRecorder
			}
			responses := make(chan raceResult, 2)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				responses <- raceResult{false, webhookHTTP("POST", "/api/webhooks/"+created.HookID, `{"event_id":"race-1","content":"Synthetic race"}`, created.Secret, nil, s.Handler(), nil)}
			}()
			go func() {
				defer wg.Done()
				<-start
				method, p := "DELETE", path
				if rotate {
					method = "POST"
					p += "/rotate"
				}
				responses <- raceResult{true, webhookHTTP(method, p, `{"expected_version":1}`, "", cookie, s.Handler(), nil)}
			}()
			close(start)
			s.purgeMu.Unlock()
			wg.Wait()
			close(responses)
			mutationBusy := false
			for result := range responses {
				response := result.response
				if result.mutation {
					want := 204
					if rotate {
						want = 200
					}
					if response.Code != want && response.Code != 503 {
						t.Fatalf("owner mutation race %d %s", response.Code, response.Body.String())
					}
					mutationBusy = response.Code == 503
				} else if response.Code != 202 && response.Code != 401 && response.Code != 503 {
					t.Fatalf("ingress race %d %s", response.Code, response.Body.String())
				}
			}
			if mutationBusy {
				// A busy owner request did not change the key. Complete that same
				// explicit CAS operation before asserting its stale-key boundary.
				method, p, want := "DELETE", path, 204
				if rotate {
					method = "POST"
					p += "/rotate"
					want = 200
				}
				if response := webhookHTTP(method, p, `{"expected_version":1}`, "", cookie, s.Handler(), nil); response.Code != want {
					t.Fatalf("owner mutation retry %d %s", response.Code, response.Body.String())
				}
			}
			before := webhookCount(t, s.store, "webhook_deliveries")
			if before > 1 {
				t.Fatal("race admitted more than one")
			}
			for i := 0; i < 3; i++ {
				w := webhookHTTP("POST", "/api/webhooks/"+created.HookID, fmt.Sprintf(`{"event_id":"after-%d","content":"Synthetic stale event"}`, i), created.Secret, nil, s.Handler(), nil)
				if w.Code != 401 {
					t.Fatalf("stale generation committed after mutation %d", w.Code)
				}
			}
			if webhookCount(t, s.store, "webhook_deliveries") != before {
				t.Fatal("stale request changed durable receipts")
			}
		})
	}
}

func TestWebhookHTTPDurableReceiptOnlyAndSemanticRetryAcrossRotation(t *testing.T) {
	s, _, cookie, b := webhookOwnerFixture(t, true)
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	created := webhookMetadataResponse(t, webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil), 201)
	ingress := "/api/webhooks/" + created.HookID
	first := webhookHTTP("POST", ingress, `{"event_id":"semantic-1","content":"Synthetic external content"}`, created.Secret, nil, s.Handler(), nil)
	if first.Code != 202 {
		t.Fatalf("acceptance %d %s", first.Code, first.Body.String())
	}
	var receipt webhookReceipt
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(first.Body.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || !receipt.Accepted || receipt.Duplicate || receipt.DeliveryID == "" {
		t.Fatalf("receipt %+v fields %+v", receipt, fields)
	}
	for _, private := range []string{created.Secret, b.ID, b.DMConversationID, "Synthetic external content", `"content"`} {
		if strings.Contains(first.Body.String(), private) {
			t.Fatal("receipt exposed target, model output, content or credential")
		}
	}
	var storedReceipt, runID, messageID string
	if err := s.store.db.QueryRow(`SELECT delivery_id,run_id,message_id FROM webhook_deliveries WHERE hook_id=? AND event_id='semantic-1'`, created.HookID).Scan(&storedReceipt, &runID, &messageID); err != nil || storedReceipt != receipt.DeliveryID || runID == "" || messageID == "" {
		t.Fatalf("202 preceded durable commit %q %q %q %v", storedReceipt, runID, messageID, err)
	}
	rotated := webhookMetadataResponse(t, webhookHTTP("POST", path+"/rotate", `{"expected_version":1}`, "", cookie, s.Handler(), nil), 200)
	// The same semantic payload, reordered JSON and new delivery timestamp must
	// use the original ledger entry despite the new credential generation.
	again := webhookHTTP("POST", ingress, " { \n \"content\" : \"Synthetic external content\", \"event_id\" : \"semantic-1\" } ", rotated.Secret, nil, s.Handler(), func(r *http.Request) { r.Header.Set("X-Tofi-Timestamp", strconv.FormatInt(time.Now().Unix()+1, 10)) })
	var duplicate webhookReceipt
	if again.Code != 200 || json.Unmarshal(again.Body.Bytes(), &duplicate) != nil || !duplicate.Duplicate || duplicate.DeliveryID != receipt.DeliveryID {
		t.Fatalf("semantic replay %d %s", again.Code, again.Body.String())
	}
	if w := webhookHTTP("POST", ingress, `{"event_id":"semantic-1","content":"Different content"}`, rotated.Secret, nil, s.Handler(), nil); w.Code != 409 {
		t.Fatalf("HTTP content conflict %d %s", w.Code, w.Body.String())
	}
	if webhookCount(t, s.store, "webhook_deliveries") != 1 {
		t.Fatal("retries created new accepted effects")
	}
	var externalMessages int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE kind=?`, messageKindWebhookEvent).Scan(&externalMessages); err != nil || externalMessages != 1 {
		t.Fatalf("external messages=%d %v", externalMessages, err)
	}
	events, err := s.store.Events(b.DMConversationID, 0)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(serialized), created.Secret) || strings.Contains(string(serialized), rotated.Secret) || strings.Contains(string(serialized), "Authorization") {
		t.Fatal("SSE leaked ingress credentials/headers")
	}
}

func TestWebhookLANHTTPManagementRequiresWebhookTransport(t *testing.T) {
	s, _, cookie, b := webhookOwnerFixture(t, false)
	s.ownerAuth.allowLAN = true
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	for _, tc := range []struct{ method, path, body string }{{"GET", path, ""}, {"POST", path, `{}`}, {"POST", path + "/rotate", `{"expected_version":1}`}, {"DELETE", path, `{"expected_version":1}`}} {
		w := webhookHTTP(tc.method, tc.path, tc.body, "", cookie, s.Handler(), func(r *http.Request) {
			r.TLS = nil
			r.RemoteAddr = "192.168.20.10:2345"
			r.Header.Set("X-Forwarded-Proto", "https")
		})
		if w.Code != 403 {
			t.Fatalf("LAN HTTP management %s %d %s", tc.method, w.Code, w.Body.String())
		}
	}
	if webhookCount(t, s.store, "webhook_endpoints") != 0 {
		t.Fatal("LAN HTTP denial created key")
	}
}

func TestWebhookOwnSecretInEnvelopeIsRejectedWithoutPersistence(t *testing.T) {
	s, _, cookie, b := webhookOwnerFixture(t, false)
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	created := webhookMetadataResponse(t, webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil), 201)
	for _, body := range []string{fmt.Sprintf(`{"event_id":"leak-1","content":%q}`, "Synthetic bearer "+created.Secret), fmt.Sprintf(`{"event_id":%q,"content":"Synthetic event"}`, created.Secret)} {
		w := webhookHTTP("POST", "/api/webhooks/"+created.HookID, body, created.Secret, nil, s.Handler(), nil)
		if w.Code != 400 || strings.Contains(w.Body.String(), created.Secret) {
			t.Fatalf("credential payload %d %s", w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"messages", "runs", "events", "webhook_deliveries"} {
		if webhookCount(t, s.store, table) != 0 {
			t.Fatalf("credential denial persisted %s", table)
		}
	}
}

func TestWebhookTargetRecreationKeepsVersionsMonotonicAgainstStaleUI(t *testing.T) {
	s, _, cookie, b := webhookOwnerFixture(t, false)
	path := "/api/conversations/" + b.DMConversationID + "/webhook"
	created := webhookMetadataResponse(t, webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil), 201)
	// A replaced target identity represents a deleted/recreated target, while
	// its root registry row survives in the independent control plane.
	webhookExec(t, s.store, `DELETE FROM webhook_targets WHERE conversation_id=?`, b.DMConversationID)
	if metadata := webhookMetadataResponse(t, webhookHTTP("GET", path, "", "", cookie, s.Handler(), nil), 200); metadata.Configured {
		t.Fatal("old grant attached to missing target identity")
	}
	recreated := webhookMetadataResponse(t, webhookHTTP("POST", path, `{}`, "", cookie, s.Handler(), nil), 201)
	if recreated.HookID == created.HookID || recreated.Version <= created.Version || recreated.Secret == created.Secret {
		t.Fatalf("replacement grant did not advance generation %+v", recreated)
	}
	for _, method := range []string{"POST", "DELETE"} {
		p := path
		if method == "POST" {
			p += "/rotate"
		}
		if w := webhookHTTP(method, p, fmt.Sprintf(`{"expected_version":%d}`, created.Version), "", cookie, s.Handler(), nil); w.Code != 409 {
			t.Fatalf("stale UI changed replacement grant %s %d", method, w.Code)
		}
	}
	if w := webhookHTTP("POST", "/api/webhooks/"+created.HookID, `{"event_id":"old-target","content":"Synthetic event"}`, created.Secret, nil, s.Handler(), nil); w.Code != 401 {
		t.Fatalf("old target hook valid after recreation %d", w.Code)
	}
}
