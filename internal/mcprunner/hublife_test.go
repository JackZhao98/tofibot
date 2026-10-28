package mcprunner

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSlowOpenOfOneAccountDoesNotBlockAnother(t *testing.T) {
	hub, _ := newTestHub(t, nil)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	openAccountHook = func(id string) {
		if id == accountA {
			once.Do(func() { close(started) })
			<-release
		}
	}
	defer func() { openAccountHook = nil }()
	doneA := make(chan int, 1)
	go func() { doneA <- call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil).Code }()
	<-started
	doneB := make(chan int, 1)
	go func() { doneB <- call(hub, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil).Code }()
	select {
	case code := <-doneB:
		if code != http.StatusOK {
			t.Fatalf("B: %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("account B waited for account A's open: the global lock is held across slow work")
	}
	close(release)
	if code := <-doneA; code != http.StatusOK {
		t.Fatalf("A: %d", code)
	}
}

func TestIdleAccountsAreEvictedSoCapacityIsNotExhaustedForever(t *testing.T) {
	hub, root := newTestHub(t, func(c *HubConfig) { c.MaxAccounts = 1; c.AccountIdleTimeout = time.Hour })
	install := map[string]any{"id": "gog", "kind": "builtin_gog"}
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusCreated {
		t.Fatalf("install: %d", w.Code)
	}
	if code := call(hub, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("capacity not enforced: %d", code)
	}
	if n := hub.evictIdle(time.Now()); n != 0 {
		t.Fatalf("an account used a moment ago was evicted")
	}
	// An account with a request in flight is never evicted, however long ago it started.
	_, release, err := hub.acquire(accountA)
	if err != nil {
		t.Fatal(err)
	}
	if n := hub.evictIdle(time.Now().Add(48 * time.Hour)); n != 0 {
		t.Fatal("an account with a request in flight was evicted")
	}
	release()
	if n := hub.evictIdle(time.Now().Add(48 * time.Hour)); n != 1 {
		t.Fatalf("idle account not evicted: %d", n)
	}
	if code := call(hub, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil).Code; code != http.StatusOK {
		t.Fatalf("B still refused after A went idle: %d", code)
	}
	// A's state survived eviction and the next request opens it again (evicting B first).
	hub.evictIdle(time.Now().Add(48 * time.Hour))
	w := call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "gog") {
		t.Fatalf("A lost its plugin: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA, "state", "plugins", "gog")); err != nil {
		t.Fatal(err)
	}
}

func TestPurgedAccountIsNeverServedAgain(t *testing.T) {
	hub, root := newTestHub(t, nil)
	install := map[string]any{"id": "gog", "kind": "builtin_gog"}
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusCreated {
		t.Fatalf("install: %d", w.Code)
	}
	if w := call(hub, http.MethodDelete, "/a/"+accountA, tokenOf(accountA), nil); w.Code != http.StatusOK {
		t.Fatalf("purge: %d", w.Code)
	}
	// The token is still valid, but the id is dead: no recreation.
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusGone {
		t.Fatalf("a purged account was recreated: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA)); !os.IsNotExist(err) {
		t.Fatalf("recreation left a directory: %v", err)
	}
	// Deleting twice is still fine (the gateway retries).
	if w := call(hub, http.MethodDelete, "/a/"+accountA, tokenOf(accountA), nil); w.Code != http.StatusOK {
		t.Fatalf("second purge: %d", w.Code)
	}
	// The tombstone survives a restart.
	hub.Close()
	hub2, err := NewHub(HubConfig{Root: root, Master: testMaster})
	if err != nil {
		t.Fatal(err)
	}
	defer hub2.Close()
	if w := call(hub2, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil); w.Code != http.StatusGone {
		t.Fatalf("tombstone lost over a restart: %d", w.Code)
	}
	if w := call(hub2, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil); w.Code != http.StatusOK {
		t.Fatalf("another account affected: %d", w.Code)
	}
}

// Anything left running under the account's uid after its Runner closed (a
// daemonised grandchild, a process that left the process group) is killed.
func TestKillAllOfScansProcStatusForEveryUIDField(t *testing.T) {
	dir := t.TempDir()
	write := func(pid int, uids string) {
		d := filepath.Join(dir, strconv.Itoa(pid))
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "status"), []byte("Name:\tx\nUid:\t"+uids+"\nGid:\t1\t1\t1\t1\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(100, "20001\t20001\t20001\t20001") // the account's
	write(101, "0\t20001\t0\t20001")         // effective/fs uid only
	write(102, "20002\t20002\t20002\t20002") // another account's
	write(103, "0\t0\t0\t0")                 // root
	if err := os.WriteFile(filepath.Join(dir, "self"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	oldProc, oldKill := procDir, killPID
	procDir = dir
	var mu sync.Mutex
	var killed []int
	killPID = func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return os.RemoveAll(filepath.Join(dir, strconv.Itoa(pid))) // the process is gone
	}
	defer func() { procDir, killPID = oldProc, oldKill }()
	if n := killAllOf(20001); n != 2 {
		t.Fatalf("killed %d (%v), want 2", n, killed)
	}
	for _, pid := range killed {
		if pid != 100 && pid != 101 {
			t.Fatalf("killed pid %d that is not the account's", pid)
		}
	}
	if n := killAllOf(0); n != 0 {
		t.Fatal("uid 0 was targeted")
	}
	if n := killAllOf(os.Getuid()); n != 0 {
		t.Fatal("the Runner's own uid was targeted")
	}
}

func TestPurgeAndEvictionKillWhatIsLeftUnderTheAccountUID(t *testing.T) {
	fakeOwnership(t)
	dir := t.TempDir()
	oldProc, oldKill := procDir, killPID
	procDir = dir
	var mu sync.Mutex
	var killed []int
	killPID = func(pid int) error {
		mu.Lock()
		defer mu.Unlock()
		killed = append(killed, pid)
		return os.RemoveAll(filepath.Join(dir, strconv.Itoa(pid)))
	}
	defer func() { procDir, killPID = oldProc, oldKill }()
	hub, _ := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true; c.AccountIdleTimeout = time.Hour })
	uidA, uidB := uidOf(t, hub, accountA), uidOf(t, hub, accountB)
	spawn := func(pid, uid int) {
		d := filepath.Join(dir, strconv.Itoa(pid))
		_ = os.MkdirAll(d, 0700)
		_ = os.WriteFile(filepath.Join(d, "status"), []byte("Uid:\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\t"+strconv.Itoa(uid)+"\n"), 0600)
	}
	spawn(500, uidA)
	spawn(501, uidB)
	if err := hub.Purge(accountA); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := append([]int(nil), killed...)
	mu.Unlock()
	if len(got) != 1 || got[0] != 500 {
		t.Fatalf("purge of A killed %v, want only pid 500", got)
	}
	// Eviction of B does the same for B.
	if n := hub.evictIdle(time.Now().Add(48 * time.Hour)); n != 1 {
		t.Fatalf("evicted %d", n)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(killed) != 2 || killed[1] != 501 {
		t.Fatalf("killed %v", killed)
	}
}
