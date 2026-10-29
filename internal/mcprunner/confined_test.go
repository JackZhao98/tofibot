package mcprunner

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// snapshotTree records every path below dir (links are recorded, not followed)
// with its mode and a digest of its content, so a test can prove a tree is
// byte-for-byte untouched.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			entry += " -> " + target
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(path)
			sum := sha256.Sum256(data)
			entry += " " + hex.EncodeToString(sum[:])
		}
		out[rel] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameSnapshot(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	var diff []string
	for k, v := range before {
		if after[k] != v {
			diff = append(diff, "changed/removed "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diff = append(diff, "added "+k)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("%s touched the other account's tree: %v", what, diff)
	}
}

// Account A's plugin code owns plugins/<id> and everything below it, and may try
// to point any link on the way at account B's tree. Every root-side operation
// (install, remove, import, status, secret read, attachment spool) must fail and
// leave B byte-for-byte untouched.
func TestRootSideOperationsRefuseLinksIntoAnotherAccount(t *testing.T) {
	attacks := map[string]func(t *testing.T, a, b string){
		"state": func(t *testing.T, a, b string) {
			must(t, os.Rename(filepath.Join(a, "state"), filepath.Join(a, "state.real")))
			must(t, os.Symlink(filepath.Join(b, "state"), filepath.Join(a, "state")))
		},
		"plugins": func(t *testing.T, a, b string) {
			must(t, os.Rename(filepath.Join(a, "state", "plugins"), filepath.Join(a, "state", "plugins.real")))
			must(t, os.Symlink(filepath.Join(b, "state", "plugins"), filepath.Join(a, "state", "plugins")))
		},
		"plugin dir": func(t *testing.T, a, b string) {
			must(t, os.Rename(filepath.Join(a, "state", "plugins", "gog"), filepath.Join(a, "state", "plugins", "gog.real")))
			must(t, os.Symlink(filepath.Join(b, "state", "plugins", "gog"), filepath.Join(a, "state", "plugins", "gog")))
		},
	}
	for name, attack := range attacks {
		t.Run(name, func(t *testing.T) {
			hub, root := newTestHub(t, nil)
			for _, id := range []string{accountA, accountB} {
				if w := call(hub, http.MethodPost, "/a/"+id+"/v1/plugins", tokenOf(id), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
					t.Fatalf("install %s: %d %s", id, w.Code, w.Body.String())
				}
			}
			dirA, dirB := filepath.Join(root, "accounts", accountA), filepath.Join(root, "accounts", accountB)
			bPlugin := filepath.Join(dirB, "state", "plugins", "gog")
			must(t, os.WriteFile(filepath.Join(bPlugin, "gog-account.json"), []byte(`{"email":"victim@example.test","scope":"readonly"}`), 0600))
			before := snapshotTree(t, dirB)
			attack(t, dirA, dirB)

			// install a new plugin
			// (a link at plugins/gog leaves other plugin ids alone, so a new id may install)
			if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), map[string]any{"id": "evil", "kind": "builtin_gog"}); w.Code == http.StatusCreated && name != "plugin dir" {
				t.Fatalf("install followed the link: %s", w.Body.String())
			}
			// remove the plugin that exists in both accounts
			if w := call(hub, http.MethodDelete, "/a/"+accountA+"/v1/plugins/gog", tokenOf(accountA), nil); w.Code == http.StatusOK {
				t.Fatalf("remove followed the link: %s", w.Body.String())
			}
			// import a new plugin, and import over the id that exists in both
			request := ImportRequest{Plugins: []ImportPlugin{
				{Request: InstallRequest{ID: "imp", Kind: "builtin_gog"}, Files: []ImportFile{importFile("home/x", "planted")}},
				{Request: InstallRequest{ID: "gog", Kind: "builtin_gog"}, Files: []ImportFile{importFile("gog-account.json", `{"email":"victim@example.test","scope":"readonly"}`)}},
			}}
			got := importResults(t, call(hub, http.MethodPost, "/a/"+accountA+"/v1/import", tokenOf(accountA), request))
			// (as above, a link at plugins/gog leaves the new id "imp" alone)
			if got[0].State != ImportFailed && name != "plugin dir" {
				t.Fatalf("import followed the link: %+v", got[0])
			}
			if got[1].State == ImportInstalled || got[1].Identical {
				t.Fatalf("import compared against the other account's file: %+v", got[1])
			}
			// the connection status must not report the other account's mailbox
			if w := call(hub, http.MethodGet, "/a/"+accountA+"/v1/plugins/gog/gog/status", tokenOf(accountA), nil); strings.Contains(w.Body.String(), "victim@example.test") {
				t.Fatalf("status read the other account's file: %s", w.Body.String())
			}
			sameSnapshot(t, before, snapshotTree(t, dirB), name)
		})
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// A secret file the account replaced by a link must never be read by the
// root-side Runner (it would otherwise hand the content to the account's process).
func TestSecretFileLinkedOutsideThePluginIsNotRead(t *testing.T) {
	hub, root := newTestHub(t, nil)
	if w := call(hub, http.MethodPost, "/a/"+accountA+"/v1/plugins", tokenOf(accountA), map[string]any{"id": "gog", "kind": "builtin_gog"}); w.Code != http.StatusCreated {
		t.Fatalf("install: %d", w.Code)
	}
	outside := filepath.Join(t.TempDir(), "master")
	must(t, os.WriteFile(outside, []byte("master-key-material"), 0600))
	secret := filepath.Join(root, "accounts", accountA, "state", "plugins", "gog", "keyring-password")
	must(t, os.Remove(secret))
	must(t, os.Symlink(outside, secret))
	tenant, err := hub.tenant(accountA)
	must(t, err)
	spec, err := tenant.runner.gogSpec("gog")
	must(t, err)
	if data, err := spec.readInPlugin(spec.SecretEnv["GOG_KEYRING_PASSWORD"]); err == nil {
		t.Fatalf("read through the link: %q", data)
	}
	if _, err := gogEnv(spec); err == nil {
		t.Fatal("the gogcli environment was built from a linked secret")
	}
}

func TestAttachmentSpoolRefusesALinkedTmp(t *testing.T) {
	hub, root := newTestHub(t, nil)
	for _, id := range []string{accountA, accountB} {
		if w := call(hub, http.MethodGet, "/a/"+id+"/v1/plugins", tokenOf(id), nil); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	tenant, err := hub.tenant(accountA)
	must(t, err)
	tmpA, tmpB := filepath.Join(root, "accounts", accountA, "tmp"), filepath.Join(root, "accounts", accountB, "tmp")
	before := snapshotTree(t, tmpB)
	must(t, os.Remove(tmpA))
	must(t, os.Symlink(tmpB, tmpA))
	spec := Spec{ID: "gog", WorkDir: t.TempDir(), Tmp: tenant.runner.opts.Tmp}
	if _, _, err := writeGogAttachments(spec, []GogAttachment{{Name: "a.txt", DataBase64: "eA=="}}); err == nil {
		t.Fatal("attachments were spooled through the link")
	}
	sameSnapshot(t, before, snapshotTree(t, tmpB), "attachment spool")
}
