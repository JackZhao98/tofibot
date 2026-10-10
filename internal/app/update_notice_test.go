package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseVersionOrderMatchesHostTool(t *testing.T) {
	ordered := []string{"v0.1.0-rc.1", "v0.1.0-rc.2", "v0.1.0-rc.10", "v0.1.0", "v0.1.1", "v0.2.0-rc.1", "v0.2.0", "v1.0.0"}
	for i := range ordered {
		for j := range ordered {
			if got, want := releaseIsNewer(ordered[i], ordered[j]), i > j; got != want {
				t.Fatalf("releaseIsNewer(%s,%s)=%v want %v", ordered[i], ordered[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "dev", "0.1.0", "v1.2", "v1.2.3-beta"} {
		if releaseIsNewer("v9.9.9", bad) || releaseIsNewer(bad, "v0.0.1") {
			t.Fatalf("unparseable %q must not compare", bad)
		}
	}
}

// manifestServer serves a mutable manifest body and counts hits.
type manifestServer struct {
	*httptest.Server
	hits   atomic.Int32
	status atomic.Int32
	body   atomic.Value
}

func newManifestServer(version string) *manifestServer {
	m := &manifestServer{}
	m.status.Store(200)
	m.set(version)
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.hits.Add(1)
		if code := int(m.status.Load()); code != 200 {
			http.Error(w, "boom", code)
			return
		}
		w.Write([]byte(m.body.Load().(string)))
	}))
	return m
}

func (m *manifestServer) set(version string) {
	body, _ := json.Marshal(map[string]string{"version": version, "notes_url": "https://example.test/notes/" + version})
	m.body.Store(string(body))
}

// settle runs one background refresh to completion.
func settle(t *testing.T, c *updateChecker) updateNotice {
	t.Helper()
	c.mu.Lock()
	done := make(chan struct{})
	c.done = done
	c.mu.Unlock()
	n := c.snapshot()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
	_ = n
	return c.snapshot()
}

func TestUpdateCheckerVersions(t *testing.T) {
	cases := []struct {
		name, current, latest string
		available             bool
	}{
		{"newer stable", "v0.1.0", "v0.1.1", true},
		{"same", "v0.1.1", "v0.1.1", false},
		{"older", "v0.2.0", "v0.1.1", false},
		{"rc current, stable latest", "v0.1.0-rc.3", "v0.1.0", true},
		{"rc current, older stable", "v0.2.0-rc.1", "v0.1.9", false},
		{"dev build", "dev", "v0.1.0", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newManifestServer(tc.latest)
			defer srv.Close()
			t.Setenv("TOFI_AUTO_UPDATE", "patch")
			n := settle(t, newUpdateChecker(srv.URL, tc.current))
			if n.Current != tc.current || n.Latest != tc.latest || n.UpdateAvailable != tc.available {
				t.Fatalf("%+v", n)
			}
			if n.NotesURL != "https://example.test/notes/"+tc.latest || n.CheckedAt == "" || n.AutoUpdate != "patch" {
				t.Fatalf("%+v", n)
			}
		})
	}
}

func TestUpdateCheckerNeverBlocksAndKeepsLastGood(t *testing.T) {
	srv := newManifestServer("v0.1.1")
	defer srv.Close()
	t.Setenv("TOFI_AUTO_UPDATE", "")
	c := newUpdateChecker(srv.URL, "v0.1.0")
	now := time.Now()
	c.now = func() time.Time { return now }

	// First answer is the empty cache, not a network wait.
	release := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer slow.Close()
	defer close(release)
	blocked := newUpdateChecker(slow.URL, "v0.1.0")
	start := time.Now()
	if n := blocked.snapshot(); n.Latest != "" || n.UpdateAvailable || time.Since(start) > time.Second {
		t.Fatalf("snapshot blocked or invented data: %+v", n)
	}

	n := settle(t, c)
	if n.Latest != "v0.1.1" || !n.UpdateAvailable || n.AutoUpdate != "off" {
		t.Fatalf("%+v", n)
	}
	good := n.CheckedAt

	// Within 6h there is no refetch.
	hits := srv.hits.Load()
	now = now.Add(5 * time.Hour)
	c.snapshot()
	time.Sleep(50 * time.Millisecond)
	if srv.hits.Load() != hits {
		t.Fatal("refetched inside the 6h cache window")
	}

	// After 6h a failing fetch keeps the last good value and retries after 10 min.
	srv.status.Store(500)
	now = now.Add(2 * time.Hour)
	n = settle(t, c)
	if n.Latest != "v0.1.1" || !n.UpdateAvailable || n.CheckedAt != good {
		t.Fatalf("failure dropped last good value: %+v", n)
	}
	failed := srv.hits.Load()
	now = now.Add(5 * time.Minute)
	c.snapshot()
	time.Sleep(50 * time.Millisecond)
	if srv.hits.Load() != failed {
		t.Fatal("retried before 10 minutes")
	}
	srv.status.Store(200)
	srv.set("v0.1.2")
	now = now.Add(6 * time.Minute)
	if n = settle(t, c); n.Latest != "v0.1.2" {
		t.Fatalf("no recovery after retry window: %+v", n)
	}
}

func TestUpdateCheckerRejectsBadManifest(t *testing.T) {
	srv := newManifestServer("latest")
	defer srv.Close()
	if n := settle(t, newUpdateChecker(srv.URL, "v0.1.0")); n.Latest != "" || n.UpdateAvailable || n.CheckedAt != "" {
		t.Fatalf("%+v", n)
	}
}

func useUpdateChecker(t *testing.T, c *updateChecker) {
	t.Helper()
	sharedUpdateMu.Lock()
	prev := sharedUpdateChecker
	sharedUpdateChecker = c
	sharedUpdateMu.Unlock()
	t.Cleanup(func() {
		sharedUpdateMu.Lock()
		sharedUpdateChecker = prev
		sharedUpdateMu.Unlock()
	})
}

func TestSystemUpdateRouteSingleOwner(t *testing.T) {
	srv := newManifestServer("v0.1.1")
	defer srv.Close()
	c := newUpdateChecker(srv.URL, "v0.1.0")
	useUpdateChecker(t, c)
	s, err := NewServer(Config{DataDir: t.TempDir(), Environment: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	get := func(method string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, "/api/system/update", nil))
		return w
	}
	if w := get("POST"); w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST %d", w.Code)
	}
	settle(t, c)
	w := get("GET")
	var n updateNotice
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &n) != nil || !n.UpdateAvailable || n.Latest != "v0.1.1" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestSystemUpdateRouteAccountsAdminOnly(t *testing.T) {
	srv := newManifestServer("v0.1.1")
	defer srv.Close()
	c := newUpdateChecker(srv.URL, "v0.1.0")
	useUpdateChecker(t, c)
	settle(t, c)

	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	adminCookie := accountCookie(t, g, admin)
	created := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"alice","password":"SyntheticPassword123!"}`, adminCookie)
	var user Account
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &user) != nil {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, accountCookie(t, g, user))
	if changed.Code != 200 {
		t.Fatalf("change %d %s", changed.Code, changed.Body.String())
	}
	userCookie := changed.Result().Cookies()[0]

	if w := accountRequest(g, "GET", "/api/system/update", "", nil); w.Code != 401 {
		t.Fatalf("anonymous %d", w.Code)
	}
	if w := accountRequest(g, "GET", "/api/system/update", "", userCookie); w.Code != 403 {
		t.Fatalf("non-admin %d %s", w.Code, w.Body.String())
	}
	w := accountRequest(g, "GET", "/api/system/update", "", adminCookie)
	var n updateNotice
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &n) != nil || !n.UpdateAvailable || n.Current != "v0.1.0" {
		t.Fatalf("admin %d %s", w.Code, w.Body.String())
	}
}
