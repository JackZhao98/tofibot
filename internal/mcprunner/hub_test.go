package mcprunner

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	accountA = "11111111-1111-4111-8111-111111111111"
	accountB = "22222222-2222-4222-8222-222222222222"
)

var testMaster = bytes.Repeat([]byte{7}, 32)

func fakeGog(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gog")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	old := gogBinary
	gogBinary = path
	t.Cleanup(func() { gogBinary = old })
}

func newTestHub(t *testing.T, mutate func(*HubConfig)) (*Hub, string) {
	t.Helper()
	fakeGog(t)
	root := t.TempDir()
	cfg := HubConfig{Root: root, Master: testMaster, MinFreeBytes: MinFreeDisabled}
	if mutate != nil {
		mutate(&cfg)
	}
	if cfg.IsolateUIDs && os.Geteuid() == 0 {
		shareTestBinary(t)
	}
	hub, err := NewHub(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hub.Close)
	return hub, root
}

func call(hub *Hub, method, path, token string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	hub.ServeHTTP(w, req)
	return w
}

func tokenOf(id string) string { return AccountToken(testMaster, id) }

func TestAccountTokensAreScopedAndDeterministic(t *testing.T) {
	if AccountToken(testMaster, accountA) != AccountToken(testMaster, accountA) {
		t.Fatal("token is not deterministic")
	}
	if AccountToken(testMaster, accountA) == AccountToken(testMaster, accountB) {
		t.Fatal("two accounts share a token")
	}
	if AccountToken(testMaster, accountA) == AccountToken(bytes.Repeat([]byte{8}, 32), accountA) {
		t.Fatal("token does not depend on the master key")
	}
	if strings.Contains(AccountToken(testMaster, accountA), accountA) {
		t.Fatal("token carries the account id")
	}
}

func TestHubRefusesAnotherAccountsTokenWithoutTouchingItsState(t *testing.T) {
	hub, root := newTestHub(t, nil)
	for _, path := range []string{"/a/" + accountB + "/v1/plugins", "/a/" + accountB + "/mcp/gog", "/a/" + accountB + "/v1/plugins/gog/gog/status", "/a/" + accountB + "/v1/import"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			if w := call(hub, method, path, tokenOf(accountA), map[string]any{}); w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s with account A's token: %d", method, path, w.Code)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountB)); !os.IsNotExist(err) {
		t.Fatalf("a refused request created the other account's state: %v", err)
	}
	if w := call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil); w.Code != http.StatusOK {
		t.Fatalf("own token refused: %d %s", w.Code, w.Body.String())
	}
}

func TestHubRefusesMissingAndForgedTokens(t *testing.T) {
	hub, _ := newTestHub(t, nil)
	path := "/a/" + accountA + "/v1/plugins"
	for name, token := range map[string]string{
		"none":      "",
		"garbage":   "garbage",
		"prefix":    tokenOf(accountA)[:20],
		"extended":  tokenOf(accountA) + "00",
		"wrong key": AccountToken(bytes.Repeat([]byte{9}, 32), accountA),
	} {
		if w := call(hub, http.MethodGet, path, token, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	// A bare "Authorization: <token>" without the scheme is not accepted either.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", tokenOf(accountA))
	w := httptest.NewRecorder()
	hub.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("scheme-less token: %d", w.Code)
	}
}

func TestHubRefusesPathTricks(t *testing.T) {
	hub, root := newTestHub(t, nil)
	for _, path := range []string{
		"/a/../etc/v1/plugins",
		"/a/" + accountA + "/../" + accountB + "/v1/plugins",
		"/a/" + accountA + "%2f..%2f" + accountB + "/v1/plugins",
		"/a/ACCOUNT-A/v1/plugins",
		"/a//v1/plugins",
		"/a/-x/v1/plugins",
		"/v1/plugins",
		"/a/" + accountA + "//" + accountB + "/v1/plugins",
	} {
		req := httptest.NewRequest(http.MethodGet, "http://runner"+path, nil)
		req.Header.Set("Authorization", "Bearer "+tokenOf(accountA))
		w := httptest.NewRecorder()
		hub.ServeHTTP(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("%q was served", path)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, "accounts"))
	for _, e := range entries {
		if e.Name() != accountA {
			t.Fatalf("path trick created %q", e.Name())
		}
	}
}

func TestHubKeepsPluginsAndStateSeparatePerAccount(t *testing.T) {
	hub, root := newTestHub(t, nil)
	install := map[string]any{"id": "gog", "kind": "builtin_gog"}
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), install); w.Code != http.StatusCreated {
		t.Fatalf("install A: %d %s", w.Code, w.Body.String())
	}
	w := call(hub, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "gog") {
		t.Fatalf("B sees A's plugin: %d %s", w.Code, w.Body.String())
	}
	if w := call(hub, http.MethodPost, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), install); w.Code != http.StatusCreated {
		t.Fatalf("B installs its own: %d %s", w.Code, w.Body.String())
	}
	keyA, _ := os.ReadFile(filepath.Join(root, "accounts", accountA, "state", "plugins", "gog", "keyring-password"))
	keyB, _ := os.ReadFile(filepath.Join(root, "accounts", accountB, "state", "plugins", "gog", "keyring-password"))
	if len(keyA) == 0 || len(keyB) == 0 || bytes.Equal(keyA, keyB) {
		t.Fatal("accounts share a keyring password")
	}
	if w := call(hub, http.MethodDelete, "/a/"+accountB+"/v1/plugins/gog", tokenOf(accountB), nil); w.Code != 200 {
		t.Fatalf("remove B: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA, "state", "plugins", "gog")); err != nil {
		t.Fatalf("removing B's plugin touched A: %v", err)
	}
	// The manifest is outside the directory plugin code can write.
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA, "manifest.json")); err != nil {
		t.Fatalf("manifest not beside the state directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA, "state", "manifest.json")); !os.IsNotExist(err) {
		t.Fatal("manifest is inside the plugin-writable state directory")
	}
	// Every child gets a private TMPDIR, not the shared /tmp.
	if p, _ := hub.tenant(accountA); p.runner.opts.Tmp != filepath.Join(root, "accounts", accountA, "tmp") {
		t.Fatalf("tmp = %q", p.runner.opts.Tmp)
	}
}

func TestHubPurgeRemovesOnlyThatAccount(t *testing.T) {
	hub, root := newTestHub(t, nil)
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodPost, "/a/"+id+"/v1/plugins", tokenOf(id), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
			t.Fatalf("install %s: %d", id, w.Code)
		}
	}
	if w := call(hub, http.MethodDelete, "/a/"+accountA, tokenOf(accountB), nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("B purged A: %d", w.Code)
	}
	if w := call(hub, http.MethodDelete, "/a/"+accountA, tokenOf(accountA), nil); w.Code != 200 {
		t.Fatalf("purge: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA)); !os.IsNotExist(err) {
		t.Fatalf("state survived the purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountB, "state", "plugins", "gog")); err != nil {
		t.Fatalf("purge of A removed B's data: %v", err)
	}
}

func TestHubRefusesManifestThatPointsIntoAnotherAccount(t *testing.T) {
	hub, root := newTestHub(t, nil)
	if w := call(hub, http.MethodPost, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
		t.Fatalf("install B: %d", w.Code)
	}
	victim := filepath.Join(root, "accounts", accountB, "state", "plugins", "gog")
	dir := filepath.Join(root, "accounts", accountA)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, spec := range map[string]Spec{
		"work dir":   {ID: "evil", Kind: "npm", Command: filepath.Join(dir, "state", "plugins", "evil", "bin"), WorkDir: victim},
		"command":    {ID: "evil", Kind: "npm", Command: filepath.Join(victim, "bin"), WorkDir: filepath.Join(dir, "state", "plugins", "evil")},
		"secret env": {ID: "evil", Kind: "npm", Command: filepath.Join(dir, "state", "plugins", "evil", "bin"), WorkDir: filepath.Join(dir, "state", "plugins", "evil"), SecretEnv: map[string]string{"K": filepath.Join(victim, "keyring-password")}},
		"fake gog":   {ID: "evil", Kind: "builtin_gog", Command: "/bin/sh", WorkDir: filepath.Join(dir, "state", "plugins", "evil")},
	} {
		data, _ := json.Marshal([]InstalledRecord{{Spec: spec}})
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		delete(hub.tenants, accountA)
		hub.mu.Unlock()
		if w := call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: tenant opened with a manifest pointing outside its state: %d", name, w.Code)
		}
	}
}

func TestWithinIsNotAPrefixMatch(t *testing.T) {
	if within("/r/accounts/abc", "/r/accounts/abcd/state") || !within("/r/accounts/abc", "/r/accounts/abc/state") || within("/r/accounts/abc", "/r/accounts/abc/../x") {
		t.Fatal("within is wrong")
	}
}

func TestUIDsAreDistinctPersistentAndNeverReused(t *testing.T) {
	var mu sync.Mutex
	owner := map[string]int{}
	old := chownAt
	chownAt = func(r *os.Root, name string, uid, gid int) error {
		path := filepath.Join(r.Name(), name)
		mu.Lock()
		defer mu.Unlock()
		if uid != gid {
			t.Errorf("uid %d and gid %d differ", uid, gid)
		}
		owner[path] = uid
		return nil
	}
	defer func() { chownAt = old }()
	// chown is faked, so the directories' real owner is the test user: read the owner the fake recorded.
	oldOwner := ownerOf
	ownerOf = func(path string) (int, bool) {
		mu.Lock()
		defer mu.Unlock()
		uid, ok := owner[path]
		return uid, ok
	}
	defer func() { ownerOf = oldOwner }()
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodGet, "/a/"+id+"/v1/plugins", tokenOf(id), nil); w.Code != 200 {
			t.Fatalf("%s: %d", id, w.Code)
		}
	}
	a, b := owner[filepath.Join(root, "accounts", accountA, "tmp")], owner[filepath.Join(root, "accounts", accountB, "tmp")]
	if a == 0 || b == 0 || a == b {
		t.Fatalf("uids a=%d b=%d", a, b)
	}
	// state/ and state/plugins/ belong to root: the account may pass, never rename.
	for _, id := range []string{accountA, accountB} {
		for _, d := range []string{"state", filepath.Join("state", "plugins")} {
			if uid, ok := owner[filepath.Join(root, "accounts", id, d)]; !ok || uid != 0 {
				t.Fatalf("%s/%s owner = %d (set=%v), want root", id, d, uid, ok)
			}
		}
	}
	if owner[filepath.Join(root, "accounts", accountA, "tmp")] != a {
		t.Fatal("tmp does not belong to the account uid")
	}
	if _, ok := owner[filepath.Join(root, "accounts", accountA)]; ok {
		t.Fatal("the account directory itself must stay root-owned")
	}
	if _, ok := owner[filepath.Join(root, "accounts", accountA, "manifest.json")]; ok {
		t.Fatal("the manifest must stay root-owned")
	}
	tenantA, _ := hub.tenant(accountA)
	if tenantA.runner.opts.Cred == nil || int(tenantA.runner.opts.Cred.Uid) != a {
		t.Fatalf("children of A would not run as uid %d", a)
	}
	// Installing a plugin hands its whole directory to the account.
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	if owner[filepath.Join(root, "accounts", accountA, "state", "plugins", "gog", "keyring-password")] != a {
		t.Fatal("plugin files were not given to the account uid")
	}
	// Deleting an account keeps its uid reserved; a new account never inherits it.
	if err := hub.Purge(accountA); err != nil {
		t.Fatal(err)
	}
	third := "33333333-3333-4333-8333-333333333333"
	if w := call(hub, http.MethodGet, "/a/"+third+"/v1/plugins", tokenOf(third), nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	c := owner[filepath.Join(root, "accounts", third, "tmp")]
	if c == a || c == b {
		t.Fatalf("uid %d was reused", c)
	}
	// A new Hub on the same root hands out the same uids.
	hub.Close()
	hub2, err := NewHub(HubConfig{Root: root, Master: testMaster, IsolateUIDs: true})
	if err != nil {
		t.Fatal(err)
	}
	defer hub2.Close()
	if got, _ := hub2.reg.uid(accountB); got != b {
		t.Fatalf("uid of B after restart = %d, want %d", got, b)
	}
}

func TestHubIsolationFailsClosedWhenOwnershipCannotBeSet(t *testing.T) {
	old := chownAt
	chownAt = func(*os.Root, string, int, int) error { return os.ErrPermission }
	defer func() { chownAt = old }()
	hub, _ := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	if w := call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("served without isolation: %d", w.Code)
	}
}

func TestHubRejectsShortMasterKey(t *testing.T) {
	if _, err := NewHub(HubConfig{Root: t.TempDir(), Master: []byte("short")}); err == nil {
		t.Fatal("short master key accepted")
	}
	if _, err := NewHub(HubConfig{Root: "relative", Master: testMaster}); err == nil {
		t.Fatal("relative root accepted")
	}
}

func importFile(path, content string) ImportFile {
	sum := sha256.Sum256([]byte(content))
	return ImportFile{Path: path, SHA256: hex.EncodeToString(sum[:]), DataBase64: base64.StdEncoding.EncodeToString([]byte(content))}
}

func importResults(t *testing.T, w *httptest.ResponseRecorder) []ImportResult {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	var out struct {
		Results []ImportResult `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Results
}

func TestImportRestoresDataVerifiesItAndNeverOverwrites(t *testing.T) {
	hub, root := newTestHub(t, nil)
	files := []ImportFile{importFile("keyring-password", "guest-keyring-password"), importFile("gog-account.json", `{"email":"user@example.test"}`), importFile("home/.config/gogcli/keyring/token", "synthetic-token"), importFile("home/empty", "")}
	request := ImportRequest{Plugins: []ImportPlugin{{Request: InstallRequest{ID: "gog", Kind: "builtin_gog"}, Files: files}}}
	got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request))
	if len(got) != 1 || got[0].State != ImportInstalled || !got[0].Identical || got[0].Files != 4 {
		t.Fatalf("first import: %+v", got)
	}
	plugin := filepath.Join(root, "accounts", accountA, "state", "plugins", "gog")
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(plugin, filepath.FromSlash(f.Path)))
		want, _ := base64.StdEncoding.DecodeString(f.DataBase64)
		if err != nil || !bytes.Equal(data, want) {
			t.Fatalf("%s: %v", f.Path, err)
		}
		if info, _ := os.Stat(filepath.Join(plugin, filepath.FromSlash(f.Path))); info.Mode().Perm() != 0600 {
			t.Fatalf("%s mode %v", f.Path, info.Mode().Perm())
		}
	}
	// The plugin is recorded: a Runner restarted from the manifest has it.
	specs, err := LoadRecords(filepath.Join(root, "accounts", accountA, "manifest.json"))
	if err != nil || len(specs) != 1 || specs[0].ID != "gog" {
		t.Fatalf("manifest %v %v", specs, err)
	}
	// Same data again: present and identical, nothing rewritten.
	got = importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request))
	if got[0].State != ImportPresent || !got[0].Identical {
		t.Fatalf("second import: %+v", got)
	}
	// Different data for a plugin that exists: kept as is, reported as different.
	request.Plugins[0].Files = []ImportFile{importFile("gog-account.json", `{"email":"other@example.test"}`)}
	got = importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request))
	if got[0].State != ImportPresent || got[0].Identical {
		t.Fatalf("third import: %+v", got)
	}
	if data, _ := os.ReadFile(filepath.Join(plugin, "gog-account.json")); string(data) != `{"email":"user@example.test"}` {
		t.Fatalf("import overwrote an installed plugin: %s", data)
	}
	// Account B got nothing.
	if w := call(hub, http.MethodGet, "/a/"+accountB+"/v1/plugins", tokenOf(accountB), nil); strings.Contains(w.Body.String(), "gog") {
		t.Fatal("import leaked into another account")
	}
}

func TestImportRefusesPathsOutsidePluginData(t *testing.T) {
	hub, root := newTestHub(t, nil)
	for _, path := range []string{"../escape", "home/../../escape", "/etc/passwd", "plugins/other/x", "manifest.json", "secrets/KEY", "node_modules/x", "home/./x", "home//x", "home\\x", "", "home/..", "keyring-password/../x"} {
		request := ImportRequest{Plugins: []ImportPlugin{{Request: InstallRequest{ID: "gog", Kind: "builtin_gog"}, Files: []ImportFile{importFile(path, "x")}}}}
		got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request))
		if got[0].State != ImportFailed {
			t.Fatalf("%q imported: %+v", path, got)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "accounts", accountA, "state", "plugins")); len(entries) != 0 {
		t.Fatalf("a refused import left a plugin behind: %v", entries)
	}
	// A package's data may not carry gog-only names.
	request := ImportRequest{Plugins: []ImportPlugin{{Request: InstallRequest{ID: "x", Kind: "npm", Package: "p", Version: "1.0.0", Binary: "b"}, Files: []ImportFile{importFile("keyring-password", "x")}}}}
	if got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request)); got[0].State != ImportFailed {
		t.Fatalf("%+v", got)
	}
}

func TestImportRefusesBadDigestAndLeavesNothing(t *testing.T) {
	hub, root := newTestHub(t, nil)
	bad := importFile("home/a", "content")
	bad.SHA256 = strings.Repeat("0", 64)
	request := ImportRequest{Plugins: []ImportPlugin{{Request: InstallRequest{ID: "gog", Kind: "builtin_gog"}, Files: []ImportFile{bad}}}}
	if got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request)); got[0].State != ImportFailed {
		t.Fatalf("%+v", got)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", accountA, "state", "plugins", "gog")); !os.IsNotExist(err) {
		t.Fatal("a failed import left its directory")
	}
	if specs, _ := LoadRecords(filepath.Join(root, "accounts", accountA, "manifest.json")); len(specs) != 0 {
		t.Fatalf("a failed import was recorded: %v", specs)
	}
	big := importFile("home/big", strings.Repeat("x", importMaxFile+1))
	request.Plugins[0].Files = []ImportFile{big}
	if got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request)); got[0].State != ImportFailed {
		t.Fatalf("oversized file imported: %+v", got)
	}
}

func TestImportWriteRefusesSymlinks(t *testing.T) {
	target := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(target, "home")); err != nil {
		t.Fatal(err)
	}
	files, _, err := decodeImportFiles("builtin_gog", []ImportFile{importFile("home/stolen", "x")})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := os.OpenRoot(target)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	if err := writeImportFiles(tr, files); err == nil {
		t.Fatal("wrote through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(outside, "stolen")); !os.IsNotExist(err) {
		t.Fatal("file landed outside the plugin directory")
	}
	if err := os.Remove(filepath.Join(target, "home")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(target, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x"), filepath.Join(target, "home", "link")); err != nil {
		t.Fatal(err)
	}
	files, _, _ = decodeImportFiles("builtin_gog", []ImportFile{importFile("home/link", "y")})
	if err := writeImportFiles(tr, files); err == nil {
		t.Fatal("wrote over a symlink")
	}
}

// shareTestBinary lets account uids execute the test binary, which acts as the
// launcher children start through. `go test` builds it in a root-only 0700
// directory; in production the Runner binary is world-executable. Only the test
// harness is adjusted: the product still launches os.Executable().
func shareTestBinary(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tofi-launcher-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "launcher")
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}
	old := launcherExecutable
	launcherExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { launcherExecutable = old; os.RemoveAll(dir) })
}
