package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func twoAccounts(t *testing.T) (*AccountGateway, *fakeBroker, string, Account, Account) {
	t.Helper()
	g, fb, data := deleteFixture(t)
	ctx := context.Background()
	admin, _ := g.create(ctx, "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	victim, _ := g.create(ctx, "alice", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0`)
	return g, fb, data, admin, victim
}

// M1: a deactivation whose broker call failed must not strand the deletion.
func TestDeleteAfterFailedBrokerDisableStillCompletes(t *testing.T) {
	g, fb, data, admin, victim := twoAccounts(t)
	fb.mu.Lock()
	fb.strict = true
	fb.mu.Unlock()
	marker := seedAccountData(t, g, data, victim)
	ac := accountCookie(t, g, admin)
	fb.fail("disable", true)
	w := accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "computer_transition_pending") {
		t.Fatalf("expected a pending transition, got %d %s", w.Code, w.Body.String())
	}
	var disabled bool
	g.root.store.db.QueryRow(`SELECT disabled FROM accounts WHERE id=?`, victim.ID).Scan(&disabled)
	if !disabled {
		t.Fatal("account should be disabled in the control DB")
	}
	fb.fail("disable", false)
	w = accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac)
	if w.Code != 200 {
		t.Fatalf("delete after a failed disable: %d %s", w.Code, w.Body.String())
	}
	if got := fb.ops("delete"); len(got) != 1 || got[0] != victim.ID {
		t.Fatalf("broker delete calls %v", got)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) || accountExists(g, victim.ID) {
		t.Fatal("deletion did not finish")
	}
	// The disable must have preceded the delete.
	fb.mu.Lock()
	defer fb.mu.Unlock()
	order := []string{}
	for _, c := range fb.calls {
		if c["account_id"] == victim.ID && (c["op"] == "disable" || c["op"] == "delete") {
			order = append(order, c["op"])
		}
	}
	if len(order) < 2 || order[len(order)-2] != "disable" || order[len(order)-1] != "delete" {
		t.Fatalf("order %v", order)
	}
}

// m1: guessing is bounded per peer; only misses count toward the global cap.
func TestDownloadLimiterCannotLockOutLegitimateDownloads(t *testing.T) {
	var l downloadLimiter
	now := time.Now()
	for i := 0; i < 500; i++ {
		if !l.allow("peer-"+string(rune('a'+i%26))+string(rune('a'+i/26)), now) {
			t.Fatalf("hit %d refused although none missed", i)
		}
	}
	for i := 0; i < 21; i++ {
		l.allow("single", now)
	}
	if l.allow("single", now) {
		t.Fatal("per-peer cap not enforced")
	}
	for i := 0; i < 201; i++ {
		l.miss(now)
	}
	if l.allow("fresh-peer", now) {
		t.Fatal("global miss cap not enforced")
	}
	if !l.allow("fresh-peer", now.Add(61*time.Second)) {
		t.Fatal("window did not reset")
	}
}

// m2: a missing, malformed or replaced link key degrades rows, never the list.
func TestExportListSurvivesLinkKeyLoss(t *testing.T) {
	g, _, data, admin, out := closeAccount(t)
	id := out["export"].(map[string]any)["id"].(string)
	ac := accountCookie(t, g, admin)
	keyPath := filepath.Join(data, deletedExportDirName, ".link-key")
	good, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	check := func(label string, wantUnavailable bool) {
		t.Helper()
		w := accountRequest(g, "GET", "/api/admin/deleted-exports", "", ac)
		if w.Code != 200 {
			t.Fatalf("%s: list %d %s", label, w.Code, w.Body.String())
		}
		var rows []deletedExport
		json.Unmarshal(w.Body.Bytes(), &rows)
		if len(rows) != 1 || rows[0].ID != id || rows[0].LinkUnavailable != wantUnavailable {
			t.Fatalf("%s: %+v", label, rows)
		}
		if wantUnavailable && (rows[0].LinkPath != "" || !rows[0].PassphraseUnavailable) {
			t.Fatalf("%s: link or passphrase still offered: %+v", label, rows[0])
		}
		if !wantUnavailable && rows[0].LinkPath == "" {
			t.Fatalf("%s: link missing", label)
		}
	}
	check("intact", false)
	os.Remove(keyPath)
	check("missing", true)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a reader must not mint a new key")
	}
	os.WriteFile(keyPath, []byte("short"), 0o600)
	check("malformed", true)
	os.WriteFile(keyPath, make([]byte, 32), 0o600)
	check("replaced", true)
	if w := accountRequest(g, "GET", "/api/admin/deleted-exports/"+id+"/passphrase", "", ac); w.Code != 409 {
		t.Fatalf("passphrase with a lost key: %d", w.Code)
	}
	os.WriteFile(keyPath, good, 0o600)
	check("restored", false)
}

// m3: an export is not deletable while its account's deletion is unfinished.
func TestExportCannotBeRemovedWhileDeletionIsUnfinished(t *testing.T) {
	g, _, data, admin, victim := twoAccounts(t)
	seedAccountData(t, g, data, victim)
	ac := accountCookie(t, g, admin)
	accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	g.deleteHook = func(step string) error {
		if step == "computer" {
			return errors.New("injected")
		}
		return nil
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 503 {
		t.Fatalf("first attempt %d", w.Code)
	}
	var id string
	if g.root.store.db.QueryRow(`SELECT id FROM deleted_account_exports`).Scan(&id) != nil {
		t.Fatal("export should exist after the export step")
	}
	if w := accountRequest(g, "DELETE", "/api/admin/deleted-exports/"+id, "", ac); w.Code != 409 || !strings.Contains(w.Body.String(), "export_in_use") {
		t.Fatalf("early removal: %d %s", w.Code, w.Body.String())
	}
	g.deleteHook = nil
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 200 {
		t.Fatalf("retry %d", w.Code)
	}
	var n int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM deleted_account_exports WHERE account_id=?`, victim.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("retry produced %d exports, want the original one", n)
	}
	if w := accountRequest(g, "DELETE", "/api/admin/deleted-exports/"+id, "", ac); w.Code != 200 {
		t.Fatalf("removal after completion: %d", w.Code)
	}
}

// m5: one failing row does not stop the sweep.
func TestSweepContinuesPastAFailingRow(t *testing.T) {
	g, _, data, _, _ := closeAccount(t)
	dir := filepath.Join(data, deletedExportDirName)
	now := time.Now().Unix()
	for _, id := range []string{"stuck", "fine"} {
		g.root.store.db.Exec(`INSERT INTO deleted_account_exports(id,account_id,username,email,created_at,expires_at,size,sha256,token_hash) VALUES(?,?,?,?,?,?,?,?,?)`, id, "acct-"+id, id, "", now-100, now-10, 1, "x", []byte(id))
	}
	// "stuck" cannot be removed: its file path is a non-empty directory.
	os.MkdirAll(filepath.Join(dir, "stuck.tofi", "inner"), 0o700)
	os.WriteFile(filepath.Join(dir, "fine.tofi"), []byte("x"), 0o600)
	n, err := g.sweepDeletedExports(time.Now())
	if err != nil || n != 1 {
		t.Fatalf("sweep removed %d err=%v", n, err)
	}
	var left int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM deleted_account_exports WHERE id IN ('stuck','fine')`).Scan(&left)
	if left != 1 {
		t.Fatalf("%d of the two expired rows remain, want only the stuck one", left)
	}
	if _, err := os.Stat(filepath.Join(dir, "fine.tofi")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the removable export survived")
	}
}

// Old versions wrote a plaintext sidecar; removal and the sweep still clean it up.
func TestOldSidecarsAreStillCleanedUp(t *testing.T) {
	g, _, data, _, out := closeAccount(t)
	id := out["export"].(map[string]any)["id"].(string)
	dir := filepath.Join(data, deletedExportDirName)
	os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"username":"alice"}`), 0o600)
	if err := g.removeExport(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, id+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("sidecar survived removal")
	}
}

// m7: a runtime still registered when deletion starts is closed before the first step runs.
func TestLingeringRuntimeIsClosedBeforeTheSteps(t *testing.T) {
	g, _, _, admin, victim := twoAccounts(t)
	ws, err := g.workspace(victim)
	if err != nil {
		t.Fatal(err)
	}
	ac := accountCookie(t, g, admin)
	accountRequest(g, "PATCH", "/api/admin/accounts/"+victim.ID, `{"disabled":true}`, ac)
	g.mu.Lock()
	g.workspaces[victim.ID] = ws // as if a request had raced the deactivation
	g.mu.Unlock()
	closedAtFirstStep := false
	g.deleteHook = func(step string) error {
		if step == "export" {
			closedAtFirstStep = ws.store.db.Ping() != nil
		}
		return nil
	}
	if w := accountRequest(g, "DELETE", "/api/admin/accounts/"+victim.ID, deleteBody("alice"), ac); w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body.String())
	}
	if !closedAtFirstStep {
		t.Fatal("the runtime was still open when the export step began")
	}
}
