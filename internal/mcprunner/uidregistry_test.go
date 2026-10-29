package mcprunner

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeOwnership replaces chown and the owner lookup with an in-memory table, so
// the uid bookkeeping can be tested without root. The root-only test in
// isolation_root_test.go repeats the important cases with real uids.
func fakeOwnership(t *testing.T) (owner map[string]int, mu *sync.Mutex) {
	t.Helper()
	owner, mu = map[string]int{}, &sync.Mutex{}
	oldChown, oldOwner := chownAt, ownerOf
	chownAt = func(r *os.Root, name string, uid, gid int) error {
		mu.Lock()
		defer mu.Unlock()
		owner[filepath.Join(r.Name(), name)] = uid
		return nil
	}
	ownerOf = func(path string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		uid, ok := owner[path]
		return uid, ok
	}
	t.Cleanup(func() { chownAt, ownerOf = oldChown, oldOwner })
	return owner, mu
}

func openAccount(t *testing.T, hub *Hub, id string) int {
	t.Helper()
	w := call(hub, http.MethodGet, "/a/"+id+"/v1/plugins", tokenOf(id), nil)
	return w.Code
}

func uidOf(t *testing.T, hub *Hub, id string) int {
	t.Helper()
	tenant, err := hub.tenant(id)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	return int(tenant.runner.opts.Cred.Uid)
}

func TestLostRegistryRefusesExistingAccountsAndNeverReusesTheirUIDs(t *testing.T) {
	fakeOwnership(t)
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	uidA, uidB := uidOf(t, hub, accountA), uidOf(t, hub, accountB)
	hub.Close()
	if err := os.Remove(filepath.Join(root, "uids.json")); err != nil {
		t.Fatal(err)
	}
	hub2, err := NewHub(HubConfig{Root: root, Master: testMaster, IsolateUIDs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer hub2.Close()
	for _, id := range []string{accountA, accountB} {
		if code := openAccount(t, hub2, id); code != http.StatusServiceUnavailable {
			t.Fatalf("account %s opened with a lost registry: %d", id, code)
		}
	}
	// A new account still works and gets a uid no existing directory carries.
	third := "33333333-3333-4333-8333-333333333333"
	uidC := uidOf(t, hub2, third)
	if uidC == uidA || uidC == uidB || uidC <= uidA || uidC <= uidB {
		t.Fatalf("new uid %d collides with or precedes %d/%d", uidC, uidA, uidB)
	}
}

func TestCorruptRegistryIsTreatedAsLostAndKept(t *testing.T) {
	fakeOwnership(t)
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	uidOf(t, hub, accountA)
	hub.Close()
	if err := os.WriteFile(filepath.Join(root, "uids.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	hub2, err := NewHub(HubConfig{Root: root, Master: testMaster, IsolateUIDs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer hub2.Close()
	if code := openAccount(t, hub2, accountA); code != http.StatusServiceUnavailable {
		t.Fatalf("opened with a corrupt registry: %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "uids.json.unusable")); err != nil {
		t.Fatalf("the unusable registry was not kept: %v", err)
	}
}

func TestRegistryThatDisagreesWithDirectoryOwnersRefusesBothAccounts(t *testing.T) {
	fakeOwnership(t)
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	uidA, uidB := uidOf(t, hub, accountA), uidOf(t, hub, accountB)
	hub.Close()
	swapped, _ := json.Marshal(uidFile{Next: uidB + 1, IDs: map[string]int{accountA: uidB, accountB: uidA}})
	if err := os.WriteFile(filepath.Join(root, "uids.json"), swapped, 0600); err != nil {
		t.Fatal(err)
	}
	hub2, err := NewHub(HubConfig{Root: root, Master: testMaster, IsolateUIDs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer hub2.Close()
	for _, id := range []string{accountA, accountB} {
		if code := openAccount(t, hub2, id); code != http.StatusServiceUnavailable {
			t.Fatalf("account %s opened although the registry disagrees with its directory: %d", id, code)
		}
	}
}

func TestRegistryAgreementIsCheckedOnEveryOpen(t *testing.T) {
	owner, mu := fakeOwnership(t)
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	uidOf(t, hub, accountA)
	// The account is evicted (or the Runner restarted) and, meanwhile, the
	// directory changes hands: the next open must notice.
	hub.mu.Lock()
	for _, tn := range hub.tenants {
		tn.runner.Close()
	}
	hub.tenants = map[string]*tenant{}
	hub.mu.Unlock()
	mu.Lock()
	owner[filepath.Join(root, "accounts", accountA, "tmp")] = 31337
	mu.Unlock()
	if code := openAccount(t, hub, accountA); code != http.StatusServiceUnavailable {
		t.Fatalf("reopened with a foreign owner: %d", code)
	}
}

func TestAllocationSkipsUIDsThatOwnADirectory(t *testing.T) {
	owner, mu := fakeOwnership(t)
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	// A leftover directory (not in the registry) already carries the first uid.
	stray := "44444444-4444-4444-8444-444444444444"
	if err := os.MkdirAll(filepath.Join(root, "accounts", stray, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	owner[filepath.Join(root, "accounts", stray, "tmp")] = hub.cfg.UIDBase
	mu.Unlock()
	if got := uidOf(t, hub, accountA); got == hub.cfg.UIDBase {
		t.Fatalf("uid %d is already the owner of another account's directory", got)
	}
}

func TestUIDRangeFitsAUserNamespace(t *testing.T) {
	if _, err := NewHub(HubConfig{Root: t.TempDir(), Master: testMaster, UIDBase: 65000, MaxAccounts: 1000}); err == nil {
		t.Fatal("a uid range beyond 65534 was accepted")
	}
	reg, err := loadUIDRegistry(filepath.Join(t.TempDir(), "uids.json"), MaxUID-1, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.uid(accountA); err != nil {
		t.Fatal(err)
	}
	if uid, err := reg.uid(accountB); err == nil {
		t.Fatalf("allocated uid %d at or above MaxUID", uid)
	}
}
