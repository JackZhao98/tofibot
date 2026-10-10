package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// closeAccount deactivates and deletes "alice" (who owns one Bot and a memory)
// and returns the parsed delete response.
func closeAccount(t *testing.T) (*AccountGateway, *fakeBroker, string, Account, map[string]any) {
	t.Helper()
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	victim, _ := g.create(ctx, "alice", "alice@example.test", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	ws, err := g.workspace(victim)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := ws.store.CreateBot("Synthetic Helper", "Be brief about SYNTHETIC-INSTRUCTIONS.", "gpt-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.store.AddMemory(bot.DMConversationID, bot.ID, "SYNTHETIC-MEMORY"); err != nil {
		t.Fatal(err)
	}
	// A computer-side file and a credential-looking file inside the account root.
	os.WriteFile(filepath.Join(data, "accounts", victim.ID, "guest-secret.txt"), []byte("SYNTHETIC-COMPUTER-FILE"), 0o600)
	ac := accountCookie(t, g, admin)
	if w := accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac); w.Code != 200 {
		t.Fatalf("deactivate %d", w.Code)
	}
	w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac)
	if w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return g, fb, data, admin, out
}

func TestDeletedAccountExportIsEncryptedVerifiedAndExcludesSecrets(t *testing.T) {
	g, fb, data, _, out := closeAccount(t)
	x := out["export"].(map[string]any)
	pass, link := x["passphrase"].(string), x["link_path"].(string)
	if len(strings.ReplaceAll(pass, "-", "")) != 32 || !strings.HasPrefix(link, "/exports/") || len(link) != len("/exports/")+43 {
		t.Fatalf("unexpected passphrase/link shape: %q %q", pass, link)
	}
	// Anonymous download; the passphrase is not part of the link.
	w := accountRequest(g, "GET", link, "", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("download %d %v", w.Code, w.Header())
	}
	file := w.Body.Bytes()
	if strings.Contains(string(file), "Synthetic Helper") || strings.Contains(string(file), "SYNTHETIC-MEMORY") {
		t.Fatal("export file is not encrypted")
	}
	if strings.Contains(link, strings.ReplaceAll(pass, "-", "")) {
		t.Fatal("passphrase leaked into the link")
	}
	if _, err := openExportFile(file, "WRONG-WRONG"); err == nil {
		t.Fatal("wrong passphrase opened the export")
	}
	plain, err := openExportFile(file, pass)
	if err != nil {
		t.Fatal(err)
	}
	b, err := parsePortableBundle(plain)
	if err != nil {
		t.Fatal(err)
	}
	text := string(plain)
	if b.Kind != "account" || len(b.Bots) != 1 || b.Bots[0].Name != "Synthetic Helper" || !strings.Contains(text, "SYNTHETIC-INSTRUCTIONS") || !strings.Contains(text, "SYNTHETIC-MEMORY") {
		t.Fatalf("bundle missing Bot setup: %+v", b.Counts)
	}
	for _, forbidden := range []string{"SYNTHETIC-COMPUTER-FILE", "vault_environment"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("export contains %q", forbidden)
		}
	}
	if !containsPortable(b.Excluded, "credentials") || !containsPortable(b.Excluded, "guest_files_and_disk") {
		t.Fatal("bundle does not declare credentials and computer files as excluded")
	}
	if containsPortable(b.Included, portableEnvironmentCategory) {
		t.Fatal("environment credentials were selected")
	}
	// On disk: outside accounts/, 0600, with a sidecar and a recorded digest.
	entries, _ := os.ReadDir(filepath.Join(data, deletedExportDirName))
	var sawTofi, sawMeta bool
	for _, e := range entries {
		info, _ := e.Info()
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v", e.Name(), info.Mode().Perm())
		}
		sawTofi = sawTofi || strings.HasSuffix(e.Name(), ".tofi")
		sawMeta = sawMeta || strings.HasSuffix(e.Name(), ".json")
	}
	if !sawTofi || !sawMeta {
		t.Fatal("export file or sidecar missing")
	}
	if len(fb.ops("delete")) != 1 {
		t.Fatal("computer was not released after the export")
	}
	// The database keeps only a hash of the token.
	var hash []byte
	g.root.store.db.QueryRow(`SELECT token_hash FROM deleted_account_exports`).Scan(&hash)
	if len(hash) != 32 || strings.Contains(string(hash), link[len("/exports/"):]) {
		t.Fatal("token not stored hashed")
	}
}

func TestExportFailureStopsBeforeAnythingIsDeleted(t *testing.T) {
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	victim, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	marker := seedAccountData(t, g, data, victim)
	ac := accountCookie(t, g, admin)
	accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	// A regular file where the export directory must be makes the real export fail.
	if err := os.WriteFile(filepath.Join(data, deletedExportDirName), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac)
	if w.Code != 503 {
		t.Fatalf("expected a stopped deletion, got %d", w.Code)
	}
	var step, code string
	g.root.store.db.QueryRow(`SELECT delete_step,delete_error FROM accounts WHERE id=?`, victim.ID).Scan(&step, &code)
	if step != "export" || code != "export_failed" {
		t.Fatalf("state %q %q", step, code)
	}
	if len(fb.ops("delete")) != 0 {
		t.Fatal("computer deleted although the export failed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("account data deleted although the export failed")
	}
	if !accountExists(g, victim.ID) {
		t.Fatal("account row removed although the export failed")
	}
	// Fix the cause; the retry exports first and then finishes.
	os.Remove(filepath.Join(data, deletedExportDirName))
	if w = accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 200 {
		t.Fatalf("retry %d %s", w.Code, w.Body.String())
	}
	if len(fb.ops("delete")) != 1 || accountExists(g, victim.ID) {
		t.Fatal("retry did not complete")
	}
}

func TestExportLinkExpiryAndUniformNotFound(t *testing.T) {
	g, _, _, admin, out := closeAccount(t)
	link := out["export"].(map[string]any)["link_path"].(string)
	if accountRequest(g, "GET", link, "", nil).Code != 200 {
		t.Fatal("valid link refused")
	}
	if w := accountRequest(g, "HEAD", link, "", nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("HEAD %d", w.Code)
	}
	bad := []string{
		"/exports/" + strings.Repeat("A", 43), "/exports/short", "/exports/", "/exports/" + link[len("/exports/"):] + "x",
		"/exports/../" + link[len("/exports/"):], "/exports/" + strings.ToLower(link[len("/exports/"):]),
	}
	var reference string
	for _, path := range bad {
		w := accountRequest(g, "GET", path, "", nil)
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if reference == "" {
			reference = w.Body.String()
		}
		if w.Body.String() != reference || w.Header().Get("Content-Disposition") != "" {
			t.Fatalf("not-found answers differ for %s", path)
		}
	}
	if accountRequest(g, "GET", "/exports/", "", nil).Code != 404 {
		t.Fatal("directory listing")
	}
	// Expired exports are refused even before the sweeper removes them.
	g.root.store.db.Exec(`UPDATE deleted_account_exports SET expires_at=?`, time.Now().Add(-time.Minute).Unix())
	w := accountRequest(g, "GET", link, "", nil)
	if w.Code != 404 || w.Body.String() != reference {
		t.Fatalf("expired link: %d", w.Code)
	}
	_ = admin
}

func TestExportDownloadIsRateLimited(t *testing.T) {
	g, _, _, _, _ := closeAccount(t)
	var limited bool
	for i := 0; i < 40; i++ {
		r := httptest.NewRequest("GET", "/exports/"+strings.Repeat("B", 43), nil)
		w := httptest.NewRecorder()
		g.Handler().ServeHTTP(w, r)
		if w.Code == 429 {
			limited = true
			break
		}
		if w.Code != 404 {
			t.Fatalf("unexpected %d", w.Code)
		}
	}
	if !limited {
		t.Fatal("guessing is not rate limited")
	}
}

func TestExportRetentionSweepAndAdminControls(t *testing.T) {
	g, _, data, admin, out := closeAccount(t)
	x := out["export"].(map[string]any)
	id := x["id"].(string)
	ac := accountCookie(t, g, admin)
	dir := filepath.Join(data, deletedExportDirName)
	if exp := int64(x["expires_at"].(float64)); exp-int64(x["created_at"].(float64)) != int64(30*24*3600) {
		t.Fatalf("retention %d", exp)
	}
	// The list never carries the passphrase or the digest; the pending passphrase can be re-read.
	list := accountRequest(g, "GET", "/api/admin/deleted-exports", "", ac)
	if list.Code != 200 || strings.Contains(list.Body.String(), "passphrase\":\"") || !strings.Contains(list.Body.String(), `"passphrase_pending":true`) || !strings.Contains(list.Body.String(), `"username":"alice"`) {
		t.Fatalf("list %d %s", list.Code, list.Body.String())
	}
	if w := accountRequest(g, "GET", "/api/admin/deleted-exports/"+id+"/passphrase", "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), x["passphrase"].(string)) {
		t.Fatalf("passphrase %d", w.Code)
	}
	if w := accountRequest(g, "POST", "/api/admin/deleted-exports/"+id+"/ack", "", ac); w.Code != 200 {
		t.Fatalf("ack %d", w.Code)
	}
	if w := accountRequest(g, "GET", "/api/admin/deleted-exports/"+id+"/passphrase", "", ac); w.Code != 409 {
		t.Fatalf("passphrase after ack %d", w.Code)
	}
	if !strings.Contains(accountRequest(g, "GET", "/api/admin/deleted-exports", "", ac).Body.String(), `"passphrase_pending":false`) {
		t.Fatal("ack not reflected")
	}
	// Non-admins see nothing.
	user, _ := g.create(context.Background(), "bob", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	if w := accountRequest(g, "GET", "/api/admin/deleted-exports", "", accountCookie(t, g, user)); w.Code != 403 {
		t.Fatalf("non-admin %d", w.Code)
	}
	// A fresh export survives a sweep; an orphan is removed once old; expiry removes file and row.
	if n, _ := g.sweepDeletedExports(time.Now()); n != 0 {
		t.Fatal("swept a live export")
	}
	orphan := filepath.Join(dir, "orphan.tofi")
	os.WriteFile(orphan, []byte("x"), 0o600)
	os.Chtimes(orphan, time.Now().Add(-2*time.Hour), time.Now().Add(-2*time.Hour))
	if n, err := g.sweepDeletedExports(time.Now().Add(31 * 24 * time.Hour)); err != nil || n != 1 {
		t.Fatalf("sweep n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".tofi")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expired file survived")
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("orphan survived")
	}
	if _, err := os.Stat(filepath.Join(dir, ".link-key")); err != nil {
		t.Fatal("link key must outlive individual exports")
	}
	// Early admin delete.
	g2, _, _, admin2, out2 := closeAccount(t)
	id2 := out2["export"].(map[string]any)["id"].(string)
	link2 := out2["export"].(map[string]any)["link_path"].(string)
	if w := accountRequest(g2, "DELETE", "/api/admin/deleted-exports/"+id2, "", accountCookie(t, g2, admin2)); w.Code != 200 {
		t.Fatalf("early delete %d", w.Code)
	}
	if accountRequest(g2, "GET", link2, "", nil).Code != 404 {
		t.Fatal("deleted export still downloadable")
	}
}
