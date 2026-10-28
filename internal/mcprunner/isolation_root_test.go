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
