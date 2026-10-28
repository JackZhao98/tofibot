package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// releasesServer serves a GitHub-style releases list.
func releasesServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("accept %q", r.Header.Get("Accept"))
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const releasesBody = `[
 {"tag_name":"v0.3.0-rc.2","draft":false,"prerelease":true,"html_url":"https://github.com/x/y/releases/tag/v0.3.0-rc.2"},
 {"tag_name":"v0.3.0-rc.3","draft":true,"prerelease":true,"html_url":"https://github.com/x/y/releases/tag/v0.3.0-rc.3"},
 {"tag_name":"v0.3.0-rc.1","draft":false,"prerelease":true,"html_url":"https://github.com/x/y/releases/tag/v0.3.0-rc.1"},
 {"tag_name":"v0.9.9","draft":false,"prerelease":true,"html_url":"https://github.com/x/y/releases/tag/v0.9.9"},
 {"tag_name":"v0.2.1","draft":false,"prerelease":false,"html_url":"https://github.com/x/y/releases/tag/v0.2.1"}]`

func settleRC(t *testing.T, c *updateChecker, enabled bool) (string, string) {
	t.Helper()
	c.mu.Lock()
	done := make(chan struct{})
	c.doneRC = done
	c.mu.Unlock()
	c.rcSnapshot(enabled)
	if enabled {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("rc refresh did not finish")
		}
	}
	return c.rcSnapshot(enabled)
}

func TestReleaseCandidateIsOnlyFetchedWhenEnabledAndPicksNewestPublishedRC(t *testing.T) {
	manifest := newManifestServer("v0.2.1")
	defer manifest.Close()
	rel := releasesServer(t, releasesBody)
	c := newUpdateChecker(manifest.URL, "v0.2.0")
	c.releasesURL = rel.URL
	settle(t, c)
	if v, _ := settleRC(t, c, false); v != "" {
		t.Fatalf("off must offer nothing, got %q", v)
	}
	if c.rcNextTry != (time.Time{}) {
		t.Fatal("off must not fetch")
	}
	v, notes := settleRC(t, c, true)
	if v != "v0.3.0-rc.2" || !strings.HasPrefix(notes, "https://github.com/") {
		t.Fatalf("got %q %q (draft, stable-tagged prerelease and older rcs are ignored)", v, notes)
	}
}

func TestReleaseCandidateOlderThanStableIsNotOffered(t *testing.T) {
	manifest := newManifestServer("v0.3.0")
	defer manifest.Close()
	rel := releasesServer(t, releasesBody)
	c := newUpdateChecker(manifest.URL, "v0.2.0")
	c.releasesURL = rel.URL
	settle(t, c)
	if v, _ := settleRC(t, c, true); v != "" {
		t.Fatalf("rc below the stable release must not be offered, got %q", v)
	}
}

// updateFixture wires a single-owner server, a checker and the shared directories.
type updateFixture struct {
	s       *Server
	request string
	status  string
}

func newUpdateFixture(t *testing.T, current, stable string) *updateFixture {
	t.Helper()
	manifest := newManifestServer(stable)
	t.Cleanup(manifest.Close)
	rel := releasesServer(t, releasesBody)
	c := newUpdateChecker(manifest.URL, current)
	c.releasesURL = rel.URL
	useUpdateChecker(t, c)
	settle(t, c)
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := &updateFixture{s: s, request: t.TempDir(), status: t.TempDir()}
	t.Setenv("TOFI_UPDATE_REQUEST_DIR", f.request)
	t.Setenv("TOFI_UPDATE_STATUS_DIR", f.status)
	return f
}

func (f *updateFixture) host(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.status, "host.json"), []byte(`{"schema":1,"update_request":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *updateFixture) do(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}

func updateErrCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string } `json:"error"`
		Code  string                `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.Error.Code != "" {
		return e.Error.Code
	}
	return e.Code
}

func TestUpdateRequestStableFlow(t *testing.T) {
	f := newUpdateFixture(t, "v0.2.0", "v0.2.1")
	post := func(body string) *httptest.ResponseRecorder { return f.do("POST", "/api/system/update/request", body) }

	if w := post(`{"version":"v0.2.1","channel":"stable"}`); w.Code != 503 || updateErrCode(t, w) != "host_update_unavailable" {
		t.Fatalf("no host: %d %s", w.Code, w.Body.String())
	}
	f.host(t)
	for name, tc := range map[string]struct {
		body string
		code int
		err  string
	}{
		"bad json":         {`{`, 400, "invalid_request"},
		"unknown field":    {`{"version":"v0.2.1","channel":"stable","url":"x"}`, 400, "invalid_request"},
		"channel mismatch": {`{"version":"v0.2.1","channel":"rc"}`, 400, "invalid_request"},
		"rc disabled":      {`{"version":"v0.3.0-rc.2","channel":"rc"}`, 409, "update_channel_disabled"},
		"not on offer":     {`{"version":"v0.2.2","channel":"stable"}`, 409, "update_not_available"},
		"older":            {`{"version":"v0.1.0","channel":"stable"}`, 409, "update_not_available"},
	} {
		if w := post(tc.body); w.Code != tc.code || updateErrCode(t, w) != tc.err {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if entries, _ := os.ReadDir(f.request); len(entries) != 0 {
		t.Fatalf("refused requests must leave nothing: %v", entries)
	}
	w := post(`{"version":"v0.2.1","channel":"stable"}`)
	if w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(f.request, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var written map[string]string
	if json.Unmarshal(raw, &written) != nil || len(written) != 4 || written["version"] != "v0.2.1" || written["channel"] != "stable" ||
		!updateNonceRE.MatchString(written["nonce"]) {
		t.Fatalf("request.json %s", raw)
	}
	if _, err := time.Parse(time.RFC3339, written["requested_at"]); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(f.request); len(entries) != 1 {
		t.Fatalf("temporary files must be cleaned: %v", entries)
	}
	if w := post(`{"version":"v0.2.1","channel":"stable"}`); w.Code != 409 || updateErrCode(t, w) != "update_in_progress" {
		t.Fatalf("second request: %d %s", w.Code, w.Body.String())
	}
	var snap updateResponse
	if w := f.do("GET", "/api/system/update", ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snap) != nil || snap.Request == nil || snap.Request.Version != "v0.2.1" || !snap.HostUpdate {
		t.Fatalf("GET %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(f.do("GET", "/api/system/update", "").Body.String(), written["nonce"]) {
		t.Fatal("the nonce must not leave the server")
	}

	// The host takes the request and reports progress; an active status still blocks.
	os.Remove(filepath.Join(f.request, "request.json"))
	writeStatus := func(state string, updated time.Time) {
		body := `{"schema":1,"state":"` + state + `","version":"v0.2.1","from":"v0.2.0","channel":"stable","progress":30,"updated_at":"` + updated.UTC().Format(time.RFC3339) + `"}`
		os.WriteFile(filepath.Join(f.status, "status.json"), []byte(body), 0o644)
	}
	writeStatus("downloading", time.Now())
	if w := post(`{"version":"v0.2.1","channel":"stable"}`); w.Code != 409 || updateErrCode(t, w) != "update_in_progress" {
		t.Fatalf("active: %d %s", w.Code, w.Body.String())
	}
	writeStatus("downloading", time.Now().Add(-7*time.Hour))
	if w := post(`{"version":"v0.2.1","channel":"stable"}`); w.Code != 202 {
		t.Fatalf("a dead host's stale status must not block forever: %d %s", w.Code, w.Body.String())
	}
	os.Remove(filepath.Join(f.request, "request.json"))
	writeStatus("rolled_back", time.Now())
	if w := post(`{"version":"v0.2.1","channel":"stable"}`); w.Code != 202 {
		t.Fatalf("a finished status must not block: %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateRequestReleaseCandidateNeedsTheSetting(t *testing.T) {
	f := newUpdateFixture(t, "v0.2.0", "v0.2.0")
	f.host(t)
	if w := f.do("PUT", "/api/system/update/settings", `{"channel_rc":true}`); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// The first read starts the refresh; wait for it.
	c := sharedUpdates()
	if v, _ := settleRC(t, c, true); v != "v0.3.0-rc.2" {
		t.Fatalf("rc %q", v)
	}
	var snap updateResponse
	w := f.do("GET", "/api/system/update", "")
	if json.Unmarshal(w.Body.Bytes(), &snap) != nil || !snap.ChannelRCEnabled || snap.LatestRC != "v0.3.0-rc.2" || snap.LatestRCNotesURL == "" {
		t.Fatalf("%s", w.Body.String())
	}
	if w := f.do("POST", "/api/system/update/request", `{"version":"v0.3.0-rc.1","channel":"rc"}`); w.Code != 409 || updateErrCode(t, w) != "update_not_available" {
		t.Fatalf("an older rc is not on offer: %d %s", w.Code, w.Body.String())
	}
	if w := f.do("POST", "/api/system/update/request", `{"version":"v0.3.0-rc.2","channel":"rc"}`); w.Code != 202 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	os.Remove(filepath.Join(f.request, "request.json"))
	// Turning the channel off withdraws the offer and closes the route.
	if w := f.do("PUT", "/api/system/update/settings", `{"channel_rc":false}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = f.do("GET", "/api/system/update", "")
	snap = updateResponse{}
	if json.Unmarshal(w.Body.Bytes(), &snap) != nil || snap.ChannelRCEnabled || snap.LatestRC != "" {
		t.Fatalf("%s", w.Body.String())
	}
	if w := f.do("POST", "/api/system/update/request", `{"version":"v0.3.0-rc.2","channel":"rc"}`); w.Code != 409 || updateErrCode(t, w) != "update_channel_disabled" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if w := f.do("PUT", "/api/system/update/settings", `{}`); w.Code != 400 {
		t.Fatalf("settings need a value: %d", w.Code)
	}
}

func TestUpdateRequestMethodsAndHostGate(t *testing.T) {
	f := newUpdateFixture(t, "v0.2.0", "v0.2.1")
	if w := f.do("GET", "/api/system/update/request", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := f.do("POST", "/api/system/update", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := f.do("GET", "/api/system/update/nope", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	// No host marker: the page shows no button and the route refuses.
	var snap updateResponse
	json.Unmarshal(f.do("GET", "/api/system/update", "").Body.Bytes(), &snap)
	if snap.HostUpdate || snap.Request != nil || snap.Status != nil {
		t.Fatalf("%+v", snap)
	}
	t.Setenv("TOFI_UPDATE_REQUEST_DIR", "")
	f.host(t)
	if w := f.do("POST", "/api/system/update/request", `{"version":"v0.2.1","channel":"stable"}`); w.Code != 503 {
		t.Fatalf("unset dir: %d", w.Code)
	}
}

func TestHostUpdateStatusIsSanitized(t *testing.T) {
	f := newUpdateFixture(t, "v0.2.0", "v0.2.1")
	f.host(t)
	body := `{"state":"failed","version":"v0.2.1; rm -rf /","from":"v0.2.0","channel":"x","code":"Update Rejected!","detail":"secret path","progress":900,"rolled_back_to":"v0.2.0","updated_at":"now"}`
	os.WriteFile(filepath.Join(f.status, "status.json"), []byte(body), 0o644)
	st := hostUpdateStatus()
	if st == nil || st.Version != "" || st.Channel != "" || st.Code != "" || st.Progress != nil || st.UpdatedAt != "" || st.RolledBackTo != "v0.2.0" {
		t.Fatalf("%+v", st)
	}
	if strings.Contains(f.do("GET", "/api/system/update", "").Body.String(), "secret path") {
		t.Fatal("detail is for the host journal, not the page")
	}
	os.WriteFile(filepath.Join(f.status, "status.json"), []byte(`{"state":"exploding"}`), 0o644)
	if hostUpdateStatus() != nil {
		t.Fatal("unknown state")
	}
	os.WriteFile(filepath.Join(f.status, "status.json"), []byte(strings.Repeat("x", updateStatusLimit+1)), 0o644)
	if hostUpdateStatus() != nil {
		t.Fatal("oversize")
	}
}

func TestUpdateRequestRoutesAccountsAdminOnly(t *testing.T) {
	manifest := newManifestServer("v0.2.1")
	defer manifest.Close()
	c := newUpdateChecker(manifest.URL, "v0.2.0")
	useUpdateChecker(t, c)
	settle(t, c)
	reqDir, statusDir := t.TempDir(), t.TempDir()
	t.Setenv("TOFI_UPDATE_REQUEST_DIR", reqDir)
	t.Setenv("TOFI_UPDATE_STATUS_DIR", statusDir)
	os.WriteFile(filepath.Join(statusDir, "host.json"), []byte(`{"update_request":1}`), 0o644)

	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	adminCookie := accountCookie(t, g, admin)
	created := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"alice","password":"SyntheticPassword123!"}`, adminCookie)
	var user Account
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &user) != nil {
		t.Fatalf("create %d", created.Code)
	}
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, accountCookie(t, g, user))
	userCookie := changed.Result().Cookies()[0]

	body := `{"version":"v0.2.1","channel":"stable"}`
	if w := accountRequest(g, "POST", "/api/system/update/request", body, nil); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	if w := accountRequest(g, "POST", "/api/system/update/request", body, userCookie); w.Code != 403 {
		t.Fatalf("member %d", w.Code)
	}
	if w := accountRequest(g, "PUT", "/api/system/update/settings", `{"channel_rc":true}`, userCookie); w.Code != 403 {
		t.Fatalf("member settings %d", w.Code)
	}
	// Cross-site writes are refused by the gateway before the route.
	r := httptest.NewRequest("POST", "/api/system/update/request", strings.NewReader(body))
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("cross-origin %d", w.Code)
	}
	if entries, _ := os.ReadDir(reqDir); len(entries) != 0 {
		t.Fatal("nothing written for refused callers")
	}
	if w := accountRequest(g, "POST", "/api/system/update/request", body, adminCookie); w.Code != 202 {
		t.Fatalf("admin %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(reqDir, "request.json")); err != nil {
		t.Fatal(err)
	}
	// The channel setting lives on the root server and is shared by every admin.
	if w := accountRequest(g, "PUT", "/api/system/update/settings", `{"channel_rc":true}`, adminCookie); w.Code != 200 {
		t.Fatalf("settings %d %s", w.Code, w.Body.String())
	}
	var snap updateResponse
	w = accountRequest(g, "GET", "/api/system/update", "", adminCookie)
	if json.Unmarshal(w.Body.Bytes(), &snap) != nil || !snap.ChannelRCEnabled {
		t.Fatalf("%s", w.Body.String())
	}
}
