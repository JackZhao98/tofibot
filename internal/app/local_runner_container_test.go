package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/mcprunner"
)

const (
	runnerAccountA = "11111111-1111-4111-8111-111111111111"
	runnerAccountB = "22222222-2222-4222-8222-222222222222"
)

var runnerTestMaster = bytes.Repeat([]byte{5}, 32)

// runnerHub starts a real multi-account Runner behind HTTP.
func runnerHub(t *testing.T) (*httptest.Server, *mcprunner.Hub, string) {
	t.Helper()
	gog := filepath.Join(t.TempDir(), "gog")
	if err := os.WriteFile(gog, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mcprunner.UseGogBinary(gog))
	root := t.TempDir()
	hub, err := mcprunner.NewHub(mcprunner.HubConfig{Root: root, Master: runnerTestMaster, MinFreeBytes: mcprunner.MinFreeDisabled})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(hub)
	t.Cleanup(func() { srv.Close(); hub.Close() })
	return srv, hub, root
}

func tokenFn(account string) func() (string, error) {
	return func() (string, error) { return mcprunner.AccountToken(runnerTestMaster, account), nil }
}

type fakeGuest struct {
	mu    sync.Mutex
	files map[string][]byte
	reads int
	// onRead runs before each read with the running count (tests change the guest here).
	onRead func(n int, g *fakeGuest)
}

func (g *fakeGuest) ReadFile(_ context.Context, path string, limit int64) ([]byte, error) {
	g.mu.Lock()
	g.reads++
	n, hook := g.reads, g.onRead
	g.mu.Unlock()
	if hook != nil {
		hook(n, g)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	data, ok := g.files[path]
	if !ok {
		return nil, errGuestMissing
	}
	if int64(len(data)) > limit {
		return nil, errors.New("oversized")
	}
	return append([]byte(nil), data...), nil
}

func (g *fakeGuest) ListDir(_ context.Context, dir string) ([]guestDirEntry, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	seen := map[string]guestDirEntry{}
	for p := range g.files {
		if !strings.HasPrefix(p, dir+"/") {
			continue
		}
		name, rest, more := strings.Cut(strings.TrimPrefix(p, dir+"/"), "/")
		seen[name] = guestDirEntry{Name: name, Directory: more && rest != ""}
	}
	if len(seen) == 0 {
		return nil, errGuestMissing
	}
	out := make([]guestDirEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (g *fakeGuest) set(path, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[path] = []byte(content)
}

func guestWithGog() *fakeGuest {
	manifest := []mcprunner.InstalledRecord{{
		Request: mcprunner.InstallRequest{ID: "gog", Kind: "builtin_gog"},
		Spec:    mcprunner.Spec{ID: "gog", Kind: "builtin_gog", Command: "/usr/local/bin/gog", WorkDir: guestRunnerDir + "/plugins/gog"},
	}}
	data, _ := json.Marshal(manifest)
	g := &fakeGuest{files: map[string][]byte{guestRunnerDir + "/manifest.json": data}}
	g.set(guestRunnerDir+"/plugins/gog/keyring-password", "guest-keyring-password")
	g.set(guestRunnerDir+"/plugins/gog/gog-account.json", `{"email":"user@example.test","scope":"read-send"}`)
	g.set(guestRunnerDir+"/plugins/gog/home/.config/gogcli/keyring/user", "synthetic-encrypted-token")
	g.set(guestRunnerDir+"/plugins/gog/home/.config/gogcli/config.json", `{"keyring_backend":"file"}`)
	return g
}

// migratedServer builds an account runtime that has a computer (so it starts on the guest Runner)
// and a namespace on the container, with a saved connection that still points at the guest.
func migratedServer(t *testing.T, srvURL, account string, guest *fakeGuest) *Server {
	t.Helper()
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, MCPConfigPath: filepath.Join(dir, "mcp.json"), IsolatedWorkspace: true, ComputerSocket: "/tmp/tofi-runner-test.sock",
		LocalRunnerURL: srvURL + mcprunner.AccountPathPrefix + account, LocalRunnerTokenFunc: tokenFn(account)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.runnerMigration.guest = guest
	if err := s.extensions.SaveMCP("local_gog", extensions.MCPServerConfig{URL: computer.RunnerOrigin + "/mcp/gog", Transport: "streamable_http"}, false); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLocalRunnerConfigPrefersTheContainerAndItsTokenIsScopedToTheAccount(t *testing.T) {
	a := &Server{isolatedWorkspace: true, localRunnerURL: "http://runner:8390/a/" + runnerAccountA, localRunnerTokenFn: tokenFn(runnerAccountA)}
	base, token, err := a.localRunnerConfig()
	if err != nil || base != "http://runner:8390/a/"+runnerAccountA || token != mcprunner.AccountToken(runnerTestMaster, runnerAccountA) {
		t.Fatalf("no computer: %q %q %v", base, token, err)
	}
	b := &Server{isolatedWorkspace: true, localRunnerURL: "http://runner:8390/a/" + runnerAccountB, localRunnerTokenFn: tokenFn(runnerAccountB)}
	if _, tokenB, _ := b.localRunnerConfig(); tokenB == token {
		t.Fatal("two accounts got the same token")
	}
	// A computer keeps the account on the guest Runner until the copy is verified.
	client, err := computer.New(computer.Config{Socket: "/tmp/tofi-runner-test.sock"})
	if err != nil {
		t.Fatal(err)
	}
	a.microVM = client
	if base, token, _ = a.localRunnerConfig(); base != computer.RunnerOrigin || token != "" {
		t.Fatalf("before the switch: %q %q", base, token)
	}
	a.runnerMigration.switched.Store(true)
	if base, _, _ = a.localRunnerConfig(); base != "http://runner:8390/a/"+runnerAccountA {
		t.Fatalf("after the switch: %q", base)
	}
	// An unreadable key is "not ready", never a fallback to another account's or the guest's Runner.
	a.localRunnerTokenFn = func() (string, error) { return "", errors.New("no key") }
	if _, _, err = a.localRunnerConfig(); err == nil {
		t.Fatal("served without a token")
	}
}

func TestRunnerContainerRefusesAnotherAccountsNamespace(t *testing.T) {
	srv, _, root := runnerHub(t)
	// Account A's token against account B's namespace: a misconfigured or hostile workspace.
	hostile := &Server{isolatedWorkspace: true, localRunnerURL: srv.URL + "/a/" + runnerAccountB, localRunnerTokenFn: tokenFn(runnerAccountA)}
	for _, call := range []struct{ method, path string }{{"GET", "/v1/plugins"}, {"POST", "/v1/plugins"}, {"GET", "/mcp/gog"}, {"POST", "/v1/import"}, {"GET", "/v1/plugins/gog/gog/status"}} {
		_, status, err := hostile.runnerRequestContext(context.Background(), call.method, call.path, map[string]any{})
		if err != nil || status != http.StatusUnauthorized {
			t.Fatalf("%s %s: status %d err %v", call.method, call.path, status, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", runnerAccountB)); !os.IsNotExist(err) {
		t.Fatal("a refused request created the other account's state")
	}
	own := &Server{isolatedWorkspace: true, localRunnerURL: srv.URL + "/a/" + runnerAccountA, localRunnerTokenFn: tokenFn(runnerAccountA)}
	if _, status, err := own.runnerRequestContext(context.Background(), "GET", "/v1/plugins", nil); err != nil || status != 200 {
		t.Fatalf("own namespace: %d %v", status, err)
	}
}

func TestGatewayGivesEachAccountItsOwnNamespaceAndToken(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "master")
	if err := os.WriteFile(keyFile, append(runnerTestMaster, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	g := &AccountGateway{config: Config{RunnerGatewayURL: "http://runner:8390/", RunnerMasterKeyFile: keyFile}}
	var a, b Config
	g.configureAccountRunner(&a, runnerAccountA)
	g.configureAccountRunner(&b, runnerAccountB)
	if a.LocalRunnerURL != "http://runner:8390/a/"+runnerAccountA || b.LocalRunnerURL != "http://runner:8390/a/"+runnerAccountB {
		t.Fatalf("urls: %q %q", a.LocalRunnerURL, b.LocalRunnerURL)
	}
	ta, errA := a.LocalRunnerTokenFunc()
	tb, errB := b.LocalRunnerTokenFunc()
	if errA != nil || errB != nil || ta == tb || ta != mcprunner.AccountToken(bytes.TrimSpace(runnerTestMaster), runnerAccountA) {
		t.Fatalf("tokens: %v %v", errA, errB)
	}
	var none Config
	(&AccountGateway{}).configureAccountRunner(&none, runnerAccountA)
	if none.LocalRunnerURL != "" || none.LocalRunnerTokenFunc != nil {
		t.Fatal("configured a Runner that is not there")
	}
	var bad Config
	g.configureAccountRunner(&bad, "../"+runnerAccountB)
	if bad.LocalRunnerURL != "" {
		t.Fatal("accepted an account id that is not an id")
	}
	missing := &AccountGateway{config: Config{RunnerGatewayURL: "http://runner:8390", RunnerMasterKeyFile: filepath.Join(t.TempDir(), "absent")}}
	var c Config
	missing.configureAccountRunner(&c, runnerAccountA)
	if _, err := c.LocalRunnerTokenFunc(); err == nil {
		t.Fatal("token issued without a master key")
	}
}

func TestDeletingAnAccountRemovesOnlyItsRunnerNamespace(t *testing.T) {
	srv, _, root := runnerHub(t)
	keyFile := filepath.Join(t.TempDir(), "master")
	if err := os.WriteFile(keyFile, runnerTestMaster, 0600); err != nil {
		t.Fatal(err)
	}
	g := &AccountGateway{config: Config{RunnerGatewayURL: srv.URL, RunnerMasterKeyFile: keyFile}}
	for _, id := range []string{runnerAccountA, runnerAccountB} {
		s := &Server{isolatedWorkspace: true, localRunnerURL: srv.URL + "/a/" + id, localRunnerTokenFn: tokenFn(id)}
		if _, status, err := s.runnerRequestContext(context.Background(), "POST", "/v1/plugins", map[string]any{"id": "gog", "kind": "builtin_gog"}); err != nil || status != http.StatusCreated {
			t.Fatalf("install %s: %d %v", id, status, err)
		}
	}
	if err := g.deleteRunnerState(context.Background(), runnerAccountA); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", runnerAccountA)); !os.IsNotExist(err) {
		t.Fatal("deleted account's Runner state remains")
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", runnerAccountB, "state", "plugins", "gog")); err != nil {
		t.Fatalf("another account's state was touched: %v", err)
	}
	if err := (&AccountGateway{}).deleteRunnerState(context.Background(), runnerAccountA); err != nil {
		t.Fatalf("a deployment without a Runner container must delete cleanly: %v", err)
	}
	down := &AccountGateway{config: Config{RunnerGatewayURL: "http://127.0.0.1:1", RunnerMasterKeyFile: keyFile}}
	if err := down.deleteRunnerState(context.Background(), runnerAccountA); err == nil {
		t.Fatal("deletion succeeded while the Runner was unreachable")
	}
}

func TestMigrationCopiesVerifiesAndOnlyThenSwitches(t *testing.T) {
	srv, _, root := runnerHub(t)
	guest := guestWithGog()
	before := map[string]string{}
	for k, v := range guest.files {
		before[k] = string(v)
	}
	s := migratedServer(t, srv.URL, runnerAccountA, guest)
	if base, _, _ := s.localRunnerConfig(); base != computer.RunnerOrigin {
		t.Fatalf("not on the guest Runner before the copy: %q", base)
	}
	report, err := s.migrateGuestRunner(context.Background())
	if err != nil || len(report.Copied) != 1 || report.Copied[0] != "gog" || len(report.Kept) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	plugin := filepath.Join(root, "accounts", runnerAccountA, "state", "plugins", "gog")
	for rel, want := range map[string]string{"keyring-password": "guest-keyring-password", "gog-account.json": `{"email":"user@example.test","scope":"read-send"}`, "home/.config/gogcli/keyring/user": "synthetic-encrypted-token", "home/.config/gogcli/config.json": `{"keyring_backend":"file"}`} {
		if got, err := os.ReadFile(filepath.Join(plugin, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Fatalf("%s: %q %v", rel, got, err)
		}
	}
	base, token, err := s.localRunnerConfig()
	if err != nil || base != srv.URL+"/a/"+runnerAccountA || token != mcprunner.AccountToken(runnerTestMaster, runnerAccountA) {
		t.Fatalf("after: %q %q %v", base, token, err)
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 1 || !strings.HasPrefix(views[0].URL, srv.URL+"/a/"+runnerAccountA+"/mcp/gog") {
		t.Fatalf("connection not re-attached: %+v %v", views, err)
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", runnerAccountB)); !os.IsNotExist(err) {
		t.Fatal("migration touched another account")
	}
	// The computer's copy is untouched, and the switch is remembered across a restart.
	for k, v := range before {
		if string(guest.files[k]) != v {
			t.Fatalf("guest file %s changed", k)
		}
	}
	state, err := os.ReadFile(filepath.Join(filepath.Dir(s.runnerMigration.path), "local-runner.json"))
	if err != nil || !strings.Contains(string(state), `"state":"container"`) || !strings.Contains(string(state), `"attached":true`) {
		t.Fatalf("state: %s %v", state, err)
	}
	var again Server
	again.initLocalRunner(Config{DataDir: filepath.Dir(s.runnerMigration.path)})
	if !again.runnerMigration.switched.Load() {
		t.Fatal("the switch was not remembered")
	}
	// Running it again changes nothing.
	if report, err = s.migrateGuestRunner(context.Background()); err != nil || len(report.Copied) != 1 {
		t.Fatalf("second run: %+v %v", report, err)
	}
}

func TestMigrationDoesNotSwitchWhenTheGuestChangesWhileCopying(t *testing.T) {
	srv, _, _ := runnerHub(t)
	guest := guestWithGog()
	s := migratedServer(t, srv.URL, runnerAccountA, guest)
	total := 0
	// Read once to count a full pass, then change a file during the second pass.
	probe := guestWithGog()
	if _, _, err := snapshotGuestRunner(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	total = probe.reads
	guest.onRead = func(n int, g *fakeGuest) {
		if n == total+2 {
			g.mu.Lock()
			g.files[guestRunnerDir+"/plugins/gog/gog-account.json"] = []byte(`{"email":"changed@example.test"}`)
			g.mu.Unlock()
		}
	}
	if _, err := s.migrateGuestRunner(context.Background()); err == nil || !strings.Contains(err.Error(), "changed while it was copied") {
		t.Fatalf("err=%v", err)
	}
	if s.runnerMigration.switched.Load() {
		t.Fatal("switched on an unverified copy")
	}
	if base, _, _ := s.localRunnerConfig(); base != computer.RunnerOrigin {
		t.Fatalf("left the guest Runner: %q", base)
	}
	// The next pass sees a quiet guest and finishes.
	guest.onRead = nil
	if _, err := s.migrateGuestRunner(context.Background()); err != nil || !s.runnerMigration.switched.Load() {
		t.Fatalf("retry: %v", err)
	}
}

func TestMigrationStaysOnTheGuestWhenTheContainerFails(t *testing.T) {
	var mode string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "down":
			http.Error(w, "boom", http.StatusBadGateway)
		case "failed":
			_, _ = w.Write([]byte(`{"results":[{"id":"gog","state":"failed","error":"file changed while it was written"}]}`))
		case "short":
			_, _ = w.Write([]byte(`{"results":[]}`))
		case "missing":
			if r.URL.Path == "/a/"+runnerAccountA+"/v1/import" {
				_, _ = w.Write([]byte(`{"results":[{"id":"gog","state":"installed","identical":true,"files":4}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"plugins":[]}`))
		}
	}))
	defer stub.Close()
	s := migratedServer(t, stub.URL, runnerAccountA, guestWithGog())
	for _, m := range []string{"down", "failed", "short", "missing"} {
		mode = m
		if _, err := s.migrateGuestRunner(context.Background()); err == nil {
			t.Fatalf("%s: migration reported success", m)
		}
		if s.runnerMigration.switched.Load() {
			t.Fatalf("%s: switched", m)
		}
		if base, _, _ := s.localRunnerConfig(); base != computer.RunnerOrigin {
			t.Fatalf("%s: left the guest Runner: %q", m, base)
		}
	}
	if _, err := os.Stat(s.runnerMigration.path); !os.IsNotExist(err) {
		t.Fatal("state was recorded for a failed copy")
	}
}

func TestMigrationKeepsAnExistingContainerCopyAndNeverOverwritesIt(t *testing.T) {
	srv, _, root := runnerHub(t)
	own := &Server{isolatedWorkspace: true, localRunnerURL: srv.URL + "/a/" + runnerAccountA, localRunnerTokenFn: tokenFn(runnerAccountA)}
	if _, status, err := own.runnerRequestContext(context.Background(), "POST", "/v1/plugins", map[string]any{"id": "gog", "kind": "builtin_gog"}); err != nil || status != http.StatusCreated {
		t.Fatalf("install: %d %v", status, err)
	}
	keyring := filepath.Join(root, "accounts", runnerAccountA, "state", "plugins", "gog", "keyring-password")
	original, _ := os.ReadFile(keyring)
	s := migratedServer(t, srv.URL, runnerAccountA, guestWithGog())
	report, err := s.migrateGuestRunner(context.Background())
	if err != nil || len(report.Kept) != 1 || len(report.Copied) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if now, _ := os.ReadFile(keyring); !bytes.Equal(now, original) {
		t.Fatal("the container's own state was overwritten")
	}
}

func TestMigrationWithNothingInTheGuestSwitchesAtOnce(t *testing.T) {
	srv, _, _ := runnerHub(t)
	s := migratedServer(t, srv.URL, runnerAccountA, &fakeGuest{files: map[string][]byte{}})
	report, err := s.migrateGuestRunner(context.Background())
	if err != nil || !report.Nothing || !s.runnerMigration.switched.Load() {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestMigrationRefusesAManifestThatReadsOutsideThePluginFolder(t *testing.T) {
	srv, _, _ := runnerHub(t)
	manifest := []mcprunner.InstalledRecord{{
		Request: mcprunner.InstallRequest{ID: "tool", Kind: "npm", Package: "p", Version: "1.0.0", Binary: "b"},
		Spec:    mcprunner.Spec{ID: "tool", Kind: "npm", SecretEnv: map[string]string{"TOKEN": "/workspace/home/.ssh/id_ed25519"}},
	}}
	data, _ := json.Marshal(manifest)
	guest := &fakeGuest{files: map[string][]byte{guestRunnerDir + "/manifest.json": data, "/workspace/home/.ssh/id_ed25519": []byte("private key")}}
	s := migratedServer(t, srv.URL, runnerAccountA, guest)
	if _, err := s.migrateGuestRunner(context.Background()); err == nil || s.runnerMigration.switched.Load() {
		t.Fatalf("read a file outside the plugin folder: %v", err)
	}
}

func TestSnapshotCarriesCommunityPackageSecretsFromItsOwnFolderOnly(t *testing.T) {
	manifest := []mcprunner.InstalledRecord{{
		Request: mcprunner.InstallRequest{ID: "tool", Kind: "npm", Package: "p", Version: "1.0.0", Binary: "b", Integrity: "sha512-" + strings.Repeat("A", 86) + "=="},
		Spec:    mcprunner.Spec{ID: "tool", Kind: "npm", SecretEnv: map[string]string{"API_KEY": guestRunnerDir + "/plugins/tool/secrets/API_KEY"}},
	}}
	data, _ := json.Marshal(manifest)
	g := &fakeGuest{files: map[string][]byte{guestRunnerDir + "/manifest.json": data}}
	g.set(guestRunnerDir+"/plugins/tool/secrets/API_KEY", "synthetic-key")
	g.set(guestRunnerDir+"/plugins/tool/home/state.json", `{"n":1}`)
	g.set(guestRunnerDir+"/plugins/tool/node_modules/.bin/b", "code that is not copied")
	snap, found, err := snapshotGuestRunner(context.Background(), g)
	if err != nil || !found || len(snap.plugins) != 1 {
		t.Fatalf("%+v %v", snap, err)
	}
	p := snap.plugins[0]
	if p.Request.Env["API_KEY"] != "synthetic-key" || p.Request.Integrity == "" || len(p.Files) != 1 || p.Files[0].Path != "home/state.json" {
		t.Fatalf("plugin=%+v", p)
	}
}

func TestLocalConnectionsWorkOnTheContainerWithNoComputer(t *testing.T) {
	srv, _, root := runnerHub(t)
	dir := t.TempDir()
	s, err := NewServer(Config{DataDir: dir, MCPConfigPath: filepath.Join(dir, "mcp.json"), IsolatedWorkspace: true, ComputerBackend: ComputerBackendNone,
		LocalRunnerURL: srv.URL + mcprunner.AccountPathPrefix + runnerAccountA, LocalRunnerTokenFunc: tokenFn(runnerAccountA)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	w := httptest.NewRecorder()
	if !s.routeLocalMCP(w, httptest.NewRequest(http.MethodPost, "/api/extensions/local-mcp/install", strings.NewReader(`{"id":"gog","kind":"builtin_gog"}`)), "extensions/local-mcp/install") || w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "accounts", runnerAccountA, "state", "plugins", "gog")); err != nil {
		t.Fatalf("the plugin is not in the account's namespace: %v", err)
	}
	views, err := s.extensions.ListMCP()
	if err != nil || len(views) != 1 || !strings.HasPrefix(views[0].URL, srv.URL+"/a/"+runnerAccountA+"/mcp/gog") {
		t.Fatalf("views=%+v err=%v", views, err)
	}
	if strings.Contains(views[0].Headers["Authorization"], mcprunner.AccountToken(runnerTestMaster, runnerAccountA)) {
		t.Fatal("token shown in settings")
	}
	w = httptest.NewRecorder()
	s.routeLocalMCP(w, httptest.NewRequest(http.MethodGet, "/api/extensions/local-mcp", nil), "extensions/local-mcp")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"available":true`) || !strings.Contains(w.Body.String(), `"gog"`) {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
}

func TestGatewayFollowsARevokedTokenEpoch(t *testing.T) {
	srv, _, _ := runnerHub(t)
	keyFile := filepath.Join(t.TempDir(), "master")
	if err := os.WriteFile(keyFile, runnerTestMaster, 0600); err != nil {
		t.Fatal(err)
	}
	newGateway := func() *AccountGateway {
		return &AccountGateway{config: Config{RunnerGatewayURL: srv.URL, RunnerMasterKeyFile: keyFile}}
	}
	get := func(id, token string) int {
		req, _ := http.NewRequest("GET", srv.URL+"/a/"+id+"/v1/plugins", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	old, _ := newGateway().runnerTokenFunc(runnerAccountA)()
	if get(runnerAccountA, old) != 200 {
		t.Fatal("the original token is refused")
	}
	req, _ := http.NewRequest("POST", srv.URL+"/a/"+runnerAccountA+"/v1/token/revoke", nil)
	req.Header.Set("Authorization", "Bearer "+old)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("revoke: %v %v", resp, err)
	}
	resp.Body.Close()
	if get(runnerAccountA, old) != 401 {
		t.Fatal("a revoked token still works")
	}
	fresh, err := newGateway().runnerTokenFunc(runnerAccountA)()
	if err != nil || fresh == old || get(runnerAccountA, fresh) != 200 {
		t.Fatalf("the gateway did not pick up the new epoch: %v", err)
	}
	// Another account's token is unaffected.
	other, _ := newGateway().runnerTokenFunc(runnerAccountB)()
	if other != mcprunner.AccountToken(runnerTestMaster, runnerAccountB) || get(runnerAccountB, other) != 200 {
		t.Fatal("revoking one account changed another's token")
	}
}
