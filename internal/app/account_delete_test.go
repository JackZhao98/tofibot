package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeBroker answers the account provisioner protocol for deletion tests.
type fakeBroker struct {
	mu      sync.Mutex
	calls   []map[string]string
	failOps map[string]bool
}

func (f *fakeBroker) ops(op string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, c := range f.calls {
		if c["op"] == op {
			ids = append(ids, c["account_id"])
		}
	}
	return ids
}

func (f *fakeBroker) fail(op string, on bool) {
	f.mu.Lock()
	f.failOps[op] = on
	f.mu.Unlock()
}

func deleteFixture(t *testing.T) (*AccountGateway, *fakeBroker, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "tofi-del-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	socket := filepath.Join(root, "broker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	fb := &fakeBroker{failOps: map[string]bool{}}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		call := map[string]string{}
		for k, v := range body {
			if s, ok := v.(string); ok {
				call[k] = s
			}
		}
		fb.mu.Lock()
		fb.calls = append(fb.calls, call)
		fail := fb.failOps[call["op"]]
		fb.mu.Unlock()
		if fail {
			w.WriteHeader(409)
			return
		}
		out := map[string]any{"account_id": call["account_id"]}
		if call["op"] == "delete" {
			out["deleted"] = true
		}
		json.NewEncoder(w).Encode(out)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	data := filepath.Join(root, "data")
	g, err := NewAccountGateway(Config{DataDir: data, Environment: "acceptance", OwnerAuth: true,
		AccountProvisionerSocket: socket, AccountComputerSocketRoot: filepath.Join(root, "sockets"), AccountComputerDiskGiB: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g, fb, data
}

func seedAccountData(t *testing.T, g *AccountGateway, data string, a Account) string {
	t.Helper()
	id := a.ID
	if _, err := g.workspace(a); err != nil { // creates the account's real store first
		t.Fatal(err)
	}
	dir := filepath.Join(data, "accounts", id, "files")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(marker, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return marker
}

func accountExists(g *AccountGateway, id string) bool {
	var n int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE id=?`, id).Scan(&n)
	return n == 1
}

func sessionCount(g *AccountGateway, id string) int {
	var n int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM account_sessions WHERE account_id=?`, id).Scan(&n)
	return n
}

func deleteBody(name string) string {
	b, _ := json.Marshal(map[string]string{"confirm_username": name})
	return string(b)
}

const uuidZero = "00000000-0000-4000-8000-000000000000"

func TestAccountDeletePreconditions(t *testing.T) {
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	user, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
	other, _ := g.create(ctx, "bob", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	seedAccountData(t, g, data, user)
	url := "/api/admin/accounts/" + user.ID

	if w := accountRequest(g, "DELETE", url, deleteBody("alice"), uc); w.Code != 403 {
		t.Fatalf("ordinary user: %d", w.Code)
	}
	if w := accountRequest(g, "DELETE", url, deleteBody("alice"), ac); w.Code != 409 || !strings.Contains(w.Body.String(), "account_not_deactivated") {
		t.Fatalf("active account: %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+admin.ID, deleteBody("admin"), ac); w.Code != 409 || !strings.Contains(w.Body.String(), "self_lockout") {
		t.Fatalf("self: %d %s", w.Code, w.Body.String())
	}
	if w := accountRequest(g, "PATCH", url, `{"disabled":true}`, ac); w.Code != 200 {
		t.Fatalf("deactivate: %d", w.Code)
	}
	for _, body := range []string{"", `{}`, deleteBody(""), deleteBody("Alice"), deleteBody("bob"), `{"confirm_username":"alice","x":1}`} {
		if w := accountRequest(g, "DELETE", url, body, ac); w.Code != 400 {
			t.Fatalf("body %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+uuidZero, deleteBody("x"), ac); w.Code != 404 {
		t.Fatalf("unknown: %d", w.Code)
	}
	if len(fb.ops("delete")) != 0 || !accountExists(g, user.ID) {
		t.Fatal("a rejected request changed state")
	}
	// The legacy owner is refused before any broker call.
	g.root.store.db.Exec(`INSERT INTO accounts(id,username,email,role,salt,password_hash,disabled,legacy,created_at) VALUES('legacy-owner','owner','o@x.test','admin',x'00',x'00',1,1,1)`)
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/legacy-owner", deleteBody("owner"), ac); w.Code != 409 || !strings.Contains(w.Body.String(), "legacy_account_not_deletable") {
		t.Fatalf("legacy: %d %s", w.Code, w.Body.String())
	}
	// Last admin: with no other active admin the target cannot go.
	g.root.store.db.Exec(`UPDATE accounts SET role='admin',disabled=1 WHERE id=?`, other.ID)
	g.root.store.db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, admin.ID)
	if _, derr := g.beginAccountDelete(ctx, "nobody", other.ID, "bob"); derr == nil || derr.code != "last_admin" {
		t.Fatalf("last admin: %+v", derr)
	}
	if len(fb.ops("delete")) != 0 {
		t.Fatal("broker delete called by a refused request")
	}
}

func TestAccountDeleteRemovesOnlyTheTargetAndIsExact(t *testing.T) {
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	victim, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
	keeper, _ := g.create(ctx, "bob", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	ac := accountCookie(t, g, admin)
	accountCookie(t, g, victim)
	keeperCookie := accountCookie(t, g, keeper)
	victimMarker := seedAccountData(t, g, data, victim)
	keeperMarker := seedAccountData(t, g, data, keeper)
	accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if accountExists(g, victim.ID) || sessionCount(g, victim.ID) != 0 {
		t.Fatal("account row or sessions survived")
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(victimMarker))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("account data dir survived: %v", err)
	}
	if _, err := os.Stat(keeperMarker); err != nil {
		t.Fatal("another account's data was touched")
	}
	if !accountExists(g, keeper.ID) || sessionCount(g, keeper.ID) != 1 || !accountExists(g, admin.ID) {
		t.Fatal("another account's rows were touched")
	}
	if got := fb.ops("delete"); len(got) != 1 || got[0] != victim.ID {
		t.Fatalf("broker delete calls: %v", got)
	}
	if w := accountRequest(g, "GET", "/api/auth/session", "", keeperCookie); !strings.Contains(w.Body.String(), `"authenticated":true`) {
		t.Fatal("other account lost its session")
	}
	// An account that is already gone is a plain 404.
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 404 {
		t.Fatalf("repeat: %d", w.Code)
	}
}

func TestAccountDeleteResumesAfterFailureAtEveryStep(t *testing.T) {
	for _, failing := range accountDeleteSteps {
		t.Run(failing, func(t *testing.T) {
			g, fb, data := deleteFixture(t)
			ctx := context.Background()
			admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
			victim, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
			g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
			ac := accountCookie(t, g, admin)
			accountCookie(t, g, victim)
			marker := seedAccountData(t, g, data, victim)
			accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
			url := "/api/admin/accounts/" + victim.ID
			failures := 1
			g.deleteHook = func(step string) error {
				if step == failing && failures > 0 {
					failures--
					return errors.New("injected")
				}
				return nil
			}
			w := accountRequest(g, "DELETE", url, deleteBody("alice"), ac)
			if w.Code != 503 || !strings.Contains(w.Body.String(), "account_delete_failed") {
				t.Fatalf("first attempt: %d %s", w.Code, w.Body.String())
			}
			var deleting bool
			var step, code string
			g.root.store.db.QueryRow(`SELECT deleting,delete_step,delete_error FROM accounts WHERE id=?`, victim.ID).Scan(&deleting, &step, &code)
			if !deleting || step != failing || code == "" {
				t.Fatalf("persisted state deleting=%v step=%q error=%q", deleting, step, code)
			}
			list := accountRequest(g, "GET", "/api/admin/accounts", "", ac).Body.String()
			if !strings.Contains(list, `"deleting":true`) || !strings.Contains(list, `"delete_step":"`+failing+`"`) {
				t.Fatalf("list does not expose the stopped deletion: %s", list)
			}
			if w := accountRequest(g, "PATCH", url, `{"disabled":false}`, ac); w.Code != 409 || !strings.Contains(w.Body.String(), "account_deleting") {
				t.Fatalf("reactivating a deleting account: %d %s", w.Code, w.Body.String())
			}
			if w := accountRequest(g, "POST", "/api/auth/login", `{"identifier":"alice","password":"SyntheticPassword123!"}`, nil); w.Code != 401 {
				t.Fatalf("deleting account signed in: %d", w.Code)
			}
			if w := accountRequest(g, "DELETE", url, deleteBody("alice"), ac); w.Code != 200 {
				t.Fatalf("retry: %d %s", w.Code, w.Body.String())
			}
			if accountExists(g, victim.ID) {
				t.Fatal("account survived retry")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("data survived retry")
			}
			if len(fb.ops("delete")) == 0 {
				t.Fatal("broker never asked to delete")
			}
		})
	}
}

func TestAccountDeleteBrokerRefusalKeepsAccountDeleting(t *testing.T) {
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	victim, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	ac := accountCookie(t, g, admin)
	marker := seedAccountData(t, g, data, victim)
	accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	fb.fail("delete", true)
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 503 {
		t.Fatalf("broker refusal: %d", w.Code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("data removed before the computer was released")
	}
	var code string
	g.root.store.db.QueryRow(`SELECT delete_error FROM accounts WHERE id=?`, victim.ID).Scan(&code)
	if code != "computer_unavailable" {
		t.Fatalf("code %q", code)
	}
	fb.fail("delete", false)
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 200 {
		t.Fatalf("retry: %d", w.Code)
	}
}

func TestRemoveAccountDataDirPathSafety(t *testing.T) {
	data := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	outside := filepath.Join(t.TempDir(), "precious")
	os.MkdirAll(outside, 0o700)
	os.WriteFile(filepath.Join(outside, "keep"), []byte("x"), 0o600)
	if err := removeAccountDataDir(data, id); err != nil {
		t.Fatalf("missing root is a no-op: %v", err)
	}
	for _, bad := range []string{"", "..", "../x", "legacy-owner", "11111111-1111-4111-8111-11111111111", id + "/.."} {
		if removeAccountDataDir(data, bad) == nil {
			t.Fatalf("accepted id %q", bad)
		}
	}
	os.MkdirAll(filepath.Join(data, "accounts"), 0o700)
	if err := os.Symlink(outside, filepath.Join(data, "accounts", id)); err != nil {
		t.Fatal(err)
	}
	if removeAccountDataDir(data, id) == nil {
		t.Fatal("followed a symlinked account directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("symlink target was touched")
	}
	os.Remove(filepath.Join(data, "accounts", id))
	// A symlink inside the tree is unlinked, never followed.
	dir := filepath.Join(data, "accounts", id)
	os.MkdirAll(dir, 0o700)
	os.Symlink(outside, filepath.Join(dir, "link"))
	if err := removeAccountDataDir(data, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("inner symlink was followed")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("directory survived")
	}
	// A symlinked accounts root is refused outright.
	data2 := t.TempDir()
	os.Symlink(outside, filepath.Join(data2, "accounts"))
	if removeAccountDataDir(data2, id) == nil {
		t.Fatal("followed a symlinked accounts root")
	}
}
