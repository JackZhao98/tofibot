package mcprunner

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This test needs the real thing: Linux, root (or CAP_SETUID/SETGID/CHOWN/DAC_OVERRIDE/FOWNER).
// It is skipped everywhere else; run it in the Runner image or any root Linux container:
//
//	go test ./internal/mcprunner -run TestRealUIDSeparation -v
func TestRealUIDSeparation(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodPost, "/a/"+id+"/v1/plugins", tokenOf(id), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
			t.Fatalf("install %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	tenantA, err := hub.tenant(accountA)
	if err != nil {
		t.Fatal(err)
	}
	specA, err := tenantA.runner.gogSpec("gog")
	if err != nil {
		t.Fatal(err)
	}
	specA.Command = "/bin/sh"
	uidA, _ := hub.reg.uid(accountA)
	uidB, _ := hub.reg.uid(accountB)
	if uidA == uidB {
		t.Fatal("same uid")
	}
	run := func(script string) (string, error) {
		out, err := runGog(context.Background(), specA, nil, "-c", script)
		return strings.TrimSpace(string(out)), err
	}
	// Children of A run as A's uid, with no supplementary groups and no capabilities.
	if out, err := run("id -u; id -G"); err != nil || out != strconv.Itoa(uidA)+"\n"+strconv.Itoa(uidA) {
		t.Fatalf("A's child ran as %q (%v), want uid/gid %d", out, err, uidA)
	}
	if out, err := run("grep CapEff /proc/self/status"); err != nil || !strings.HasSuffix(out, "0000000000000000") {
		t.Fatalf("A's child kept capabilities: %q %v", out, err)
	}
	// It reads and writes its own state ...
	own := filepath.Join(root, "accounts", accountA, "state", "plugins", "gog", "keyring-password")
	if out, err := run("cat " + own); err != nil || out == "" {
		t.Fatalf("A cannot read its own state: %q %v", out, err)
	}
	// ... and nothing of B's, nor the manifests, the uid registry, or /tmp.
	for _, path := range []string{
		filepath.Join(root, "accounts", accountB, "state", "plugins", "gog", "keyring-password"),
		filepath.Join(root, "accounts", accountB, "manifest.json"),
		filepath.Join(root, "accounts", accountA, "manifest.json"),
		filepath.Join(root, "uids.json"),
	} {
		if out, err := run("cat " + path); err == nil {
			t.Fatalf("A's child read %s: %q", path, out)
		}
	}
	if _, err := run("ls " + filepath.Join(root, "accounts", accountB, "state")); err == nil {
		t.Fatal("A's child listed B's state")
	}
	if _, err := run("ls " + filepath.Join(root, "accounts")); err == nil {
		t.Fatal("A's child listed the accounts")
	}
	if _, err := run("touch " + filepath.Join(root, "accounts", accountB, "state", "planted")); err == nil {
		t.Fatal("A's child wrote into B's state")
	}
	// The master key is outside every account's reach by construction; the uid registry is root-only.
	if info, err := os.Stat(filepath.Join(root, "uids.json")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("uid registry mode: %v %v", info, err)
	}
}

// Account A's own uid plants links to B's tree and tries to move the directories
// the Runner walks. The moves must be refused by the kernel (root owns state/ and
// plugins/), and the links must make every root-side operation fail.
//
//	docker run --rm -v $PWD:/src -w /src golang:1.25.11 go test ./internal/mcprunner -run Root -v
func TestRootAccountCannotRedirectRootSideOperations(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodPost, "/a/"+id+"/v1/plugins", tokenOf(id), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
			t.Fatalf("install %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	tenantA, err := hub.tenant(accountA)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := tenantA.runner.gogSpec("gog")
	if err != nil {
		t.Fatal(err)
	}
	// The shell runs as the account uid without the plugin's secrets, so every
	// refusal below comes from file permissions, not from a missing secret.
	shell := spec
	shell.Command = "/bin/sh"
	shell.SecretEnv = nil
	run := func(script string) error {
		_, err := runGog(context.Background(), shell, nil, "-c", script)
		return err
	}
	dirA, dirB := filepath.Join(root, "accounts", accountA), filepath.Join(root, "accounts", accountB)
	if err := run("touch " + filepath.Join(dirA, "state", "plugins", "gog", "probe")); err != nil {
		t.Fatalf("the account cannot write its own plugin directory: %v", err)
	}
	// The account cannot rename or replace the directories above its plugin.
	for _, script := range []string{
		"mv " + filepath.Join(dirA, "state", "plugins") + " " + filepath.Join(dirA, "state", "plugins.x"),
		"mv " + filepath.Join(dirA, "state") + " " + filepath.Join(dirA, "state.x"),
		"ln -s " + filepath.Join(dirB, "state") + " " + filepath.Join(dirA, "state", "plugins", "other"),
		"rm -rf " + filepath.Join(dirA, "state", "plugins", "gog") + " && ln -s " + filepath.Join(dirB, "state", "plugins", "gog") + " " + filepath.Join(dirA, "state", "plugins", "gog"),
	} {
		if err := run(script); err == nil {
			t.Fatalf("the account's uid could run %q", script)
		}
	}
	before := snapshotTree(t, dirB)
	// It can replace what it owns: the files inside its own plugin directory.
	own := filepath.Join(dirA, "state", "plugins", "gog")
	if err := run("rm -f " + filepath.Join(own, "keyring-password") + " && ln -s " + filepath.Join(dirB, "state", "plugins", "gog", "keyring-password") + " " + filepath.Join(own, "keyring-password") +
		" && ln -s " + filepath.Join(dirB, "manifest.json") + " " + filepath.Join(own, "gog-account.json")); err != nil {
		t.Fatalf("the account could not plant its own links: %v", err)
	}
	if data, err := spec.readInPlugin(spec.SecretEnv["GOG_KEYRING_PASSWORD"]); err == nil {
		t.Fatalf("root read the other account's secret through a link: %q", data)
	}
	if account, err := tenantA.runner.GogStatus("gog"); err == nil && account.Email != "" {
		t.Fatalf("status followed a link: %+v", account)
	}
	if err := tenantA.runner.Remove("gog"); err != nil {
		t.Fatalf("remove: %v", err) // links inside the plugin are deleted, never followed
	}
	sameSnapshot(t, before, snapshotTree(t, dirB), "root-side operations")
}

// A lost uids.json must not let a new account inherit the files of an old one.
func TestRootLostRegistryNeverReusesAUID(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	hub, root := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	uids := map[string]int{}
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodGet, "/a/"+id+"/v1/plugins", tokenOf(id), nil); w.Code != 200 {
			t.Fatal(w.Code)
		}
		uids[id], _ = hub.reg.uid(id)
	}
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
		if w := call(hub2, http.MethodGet, "/a/"+id+"/v1/plugins", tokenOf(id), nil); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s served with a lost registry: %d", id, w.Code)
		}
	}
	third := "33333333-3333-4333-8333-333333333333"
	if w := call(hub2, http.MethodGet, "/a/"+third+"/v1/plugins", tokenOf(third), nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	got, _ := hub2.reg.uid(third)
	if got == uids[accountA] || got == uids[accountB] {
		t.Fatalf("uid %d was reused", got)
	}
}

// After a purge nothing of the account may still run: a plugin that daemonised
// and left its process group is found by uid and killed.
func TestRootPurgeKillsEveryProcessOfTheAccountUID(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	hub, _ := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
		t.Fatalf("install: %d", w.Code)
	}
	tenant, err := hub.tenant(accountA)
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := tenant.runner.gogSpec("gog")
	spec.Command = "/bin/sh"
	// A daemon in its own session, as a plugin could start one.
	if _, err := runGog(context.Background(), spec, nil, "-c", "setsid sleep 300 >/dev/null 2>&1 </dev/null &"); err != nil {
		t.Fatal(err)
	}
	uid, _ := hub.reg.uid(accountA)
	deadline := time.Now().Add(3 * time.Second)
	for len(processesOf(uid)) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(processesOf(uid)) == 0 {
		t.Fatal("the daemon did not start")
	}
	if err := hub.Purge(accountA); err != nil {
		t.Fatal(err)
	}
	if left := processesOf(uid); len(left) != 0 {
		t.Fatalf("processes of the purged account survive: %v", left)
	}
}

// Children run with the account limits set by the launcher.
func TestRootChildrenRunWithResourceLimits(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Skip("needs Linux and root")
	}
	hub, _ := newTestHub(t, func(c *HubConfig) { c.IsolateUIDs = true })
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
		t.Fatalf("install: %d", w.Code)
	}
	tenant, _ := hub.tenant(accountA)
	spec, _ := tenant.runner.gogSpec("gog")
	spec.Command = "/bin/sh"
	out, err := runGog(context.Background(), spec, nil, "-c", "grep -E '^Max (processes|open files|file size)' /proc/self/limits")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"128", "1024", "1073741824"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("limits %q do not contain %s", out, want)
		}
	}
}
