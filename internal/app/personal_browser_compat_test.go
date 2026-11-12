package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestPersonalBrowserFeatureMetadata(t *testing.T) {
	s := computerTestServer(t)
	g := accountFixture(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/server-info", nil))
	var single, multi map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &single)
	_ = json.Unmarshal(accountRequest(g, "GET", "/api/server-info", "", nil).Body.Bytes(), &multi)
	if !reflect.DeepEqual(single["bot_browser"], multi["bot_browser"]) {
		t.Fatalf("feature mismatch %s %s", single["bot_browser"], multi["bot_browser"])
	}
	if string(single["bot_browser"]) != `{"upload":"websocket-json","version":1,"view":"ndjson-jpeg"}` {
		t.Fatalf("metadata %s", single["bot_browser"])
	}
	if DesktopContract != 1 || DesktopMinClient != "0.2.1" {
		t.Fatal("desktop contract changed")
	}
}
func TestPersonalBrowserOwnerBoundary(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		independent  bool
	}{
		{"GET", "/api/computers/device/browser/channel", true},
		{"POST", "/api/computers/device/browser/channel", false},
		{"GET", "/api/computers/device/browser/channel/extra", false},
		{"GET", "/api/computers/device/browser/stream", false},
		{"GET", "/api/browser-sources", false},
	} {
		r := httptest.NewRequest(tt.method, tt.path, nil)
		if ownerIndependentRoute(r) != tt.independent {
			t.Fatalf("route %s %s", tt.method, tt.path)
		}
	}
	s, m, _, _ := browserFixture(t)
	w := m.call("GET", "/api/computers/"+m.id+"/browser/stream", "", m.token)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("device viewing: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/computers/"+m.id+"/browser/channel", nil)
	r.AddCookie(&http.Cookie{Name: ownerCookie, Value: "synthetic"})
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("cookie uploading: %d", w.Code)
	}
}
func TestPersonalBrowserLegacyAndStrictHeartbeat(t *testing.T) {
	s := computerTestServer(t)
	m := enrollMac(t, s, "Legacy Mac", "files.list")
	r := activeComputerRun(t, s)
	if _, e := s.computerActionOn(context.Background(), r, m.id, "browser.read", []byte(`{}`)); toolCode(e) != "browser_client_unsupported" {
		t.Fatalf("legacy browser %v", e)
	}
	if _, e := s.store.queueComputerJob(r, m.id, "files.list", json.RawMessage(`{}`)); e != nil {
		t.Fatal("legacy files changed", e)
	}
	source := s.source(m.id)
	if source.State != "unsupported" || source.Supported || source.Connection != nil {
		t.Fatalf("legacy falsely live %+v", source)
	}
	w := m.call("POST", "/api/computers/"+m.id+"/heartbeat", `{"protocol_version":1,"bot_browser":{}}`, m.token)
	if w.Code != 400 {
		t.Fatalf("heartbeat accepted new fields %d %s", w.Code, w.Body.String())
	}
}
func TestPersonalBrowserSourcesAreMetadataOnly(t *testing.T) {
	s, m, h, _ := browserFixture(t)
	w := m.call("GET", "/api/browser-sources", "", "")
	if w.Code != 200 {
		t.Fatalf("sources %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, secret := range []string{"Synthetic", "synthetic.invalid", m.token, "jpeg", "title", "url"} {
		if strings.Contains(body, secret) {
			t.Fatalf("source leaked %s", secret)
		}
	}
	h.mu.Lock()
	h.state = nil
	h.mu.Unlock()
	if source := s.source(m.id); source.State == "working" || source.State == "idle" {
		t.Fatalf("missing state falsely live %+v", source)
	}
}
func TestPersonalBrowserAccountIsolation(t *testing.T) {
	a, m, _, _ := browserFixture(t)
	b := computerTestServer(t)
	w := httptest.NewRecorder()
	b.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/computers/"+m.id+"/browser/stream", nil))
	if w.Code != 404 {
		t.Fatalf("other account stream %d", w.Code)
	}
	req := httptest.NewRequest("GET", "/api/computers/"+m.id+"/browser/channel", nil)
	req.Header.Set("Authorization", "Bearer "+m.token)
	w = httptest.NewRecorder()
	b.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("other workspace accepted token %d", w.Code)
	}
	_ = a
}

func TestPersonalBrowserGatewayCookieAndTokenIsolation(t *testing.T) {
	g := accountFixture(t)
	first, e := g.create(context.Background(), "browser-admin", "admin@synthetic.invalid", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if e != nil {
		t.Fatal(e)
	}
	second, e := g.create(context.Background(), "browser-user", "user@synthetic.invalid", "SyntheticPassword123!", false, "")
	if e != nil {
		t.Fatal(e)
	}
	_, e = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, second.ID)
	if e != nil {
		t.Fatal(e)
	}
	second.MustChangePassword = false
	a, e := g.workspace(first)
	if e != nil {
		t.Fatal(e)
	}
	b, e := g.workspace(second)
	if e != nil {
		t.Fatal(e)
	}
	id, token := pairedComputer(t, a, "browser.read")
	if a.browserInstanceID != g.root.instance.ID || b.browserInstanceID != g.root.instance.ID {
		t.Fatal("workspace used a separate partition identity")
	}
	cookie := accountCookie(t, g, second)
	w := accountRequest(g, "GET", "/api/computers/"+id+"/browser/stream", "", cookie)
	if w.Code != 404 {
		t.Fatalf("cross-account cookie %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest("GET", "/api/computers/"+id+"/browser/channel", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	routed, ok := g.deviceWorkspace(req)
	if !ok || routed != a {
		t.Fatal("token routed to wrong account")
	}
	req = httptest.NewRequest("GET", "/api/browser-sources", nil)
	req.TLS = &tls.ConnectionState{}
	req.AddCookie(cookie)
	if !b.browserSessionOK(req) || a.browserSessionOK(req) {
		t.Fatal("workspace session validation crossed accounts")
	}
	_, e = g.root.store.db.Exec(`DELETE FROM account_sessions WHERE account_id=?`, second.ID)
	if e != nil {
		t.Fatal(e)
	}
	if b.browserSessionOK(req) {
		t.Fatal("logged out session retained preview permission")
	}
}
