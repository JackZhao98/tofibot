package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestAccountAdminQuotaUsesVerifiedBrokerIdentity(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "quota-admin", "", "SyntheticPassword123!", true)
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "quota-user", "", "SyntheticPassword123!", false)
	if err != nil {
		t.Fatal(err)
	}
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	// A regular user with a fully changed password remains unable to allocate.
	if _, err := g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "tofi-quota-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "broker.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var mismatch atomic.Bool
	var reject atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct {
			Op    string `json:"op"`
			ID    string `json:"account_id"`
			Quota int    `json:"quota_gib"`
		}
		if r.URL.Path != "/v1/accounts" || json.NewDecoder(r.Body).Decode(&in) != nil || in.Op != "quota" || in.ID != user.ID || in.Quota != 16 {
			t.Errorf("unexpected request %+v", in)
			w.WriteHeader(400)
			return
		}
		if reject.Load() {
			w.WriteHeader(409)
			return
		}
		id := in.ID
		if mismatch.Load() {
			id = admin.ID
		}
		json.NewEncoder(w).Encode(map[string]any{"account_id": id, "quota_bytes": int64(in.Quota) << 30, "applied": true, "provisioned": true})
	})}
	go server.Serve(ln)
	defer server.Close()
	g.config.AccountProvisionerSocket = socket
	path := "/api/admin/accounts/" + user.ID + "/quota"
	for _, test := range []struct {
		cookie *http.Cookie
		body   string
		want   int
	}{
		{nil, `{"quota_gib":16}`, 401}, {uc, `{"quota_gib":16}`, 403}, {ac, `{"quota_gib":7}`, 400}, {ac, `{"quota_gib":16,"socket":"/other"}`, 400}, {ac, `{"quota_gib":16.5}`, 400},
	} {
		if w := accountRequest(g, "PATCH", path, test.body, test.cookie); w.Code != test.want {
			t.Fatalf("request %s: %d %s", test.body, w.Code, w.Body.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected request reached broker")
	}
	if w := accountRequest(g, "PATCH", path, `{"quota_gib":16}`, ac); w.Code != 200 {
		t.Fatalf("quota %d %s", w.Code, w.Body.String())
	}
	mismatch.Store(true)
	if w := accountRequest(g, "PATCH", path, `{"quota_gib":16}`, ac); w.Code != 503 {
		t.Fatalf("unverified identity %d", w.Code)
	}
	reject.Store(true)
	if w := accountRequest(g, "PATCH", path, `{"quota_gib":16}`, ac); w.Code != 409 {
		t.Fatalf("rejected quota %d", w.Code)
	}
	if calls.Load() != 3 {
		t.Fatalf("broker calls %d", calls.Load())
	}
}
