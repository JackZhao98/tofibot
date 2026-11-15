package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hostBackupFixture(t *testing.T, b *backupFixture) {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "host-control.token")
	if err := os.WriteFile(tokenFile, []byte(hostToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b.g.config.HostControlTokenFile = tokenFile
}

// The host asks for a pre-update snapshot through its own channel: the state
// says what the setting is, run refuses while remote backup is off, a run takes
// the host's /etc/tofi archive along, and a bucket failure is a named code.
func TestHostBackupChannel(t *testing.T) {
	b := newBackupFixture(t)
	hostBackupFixture(t, b)
	call := func(method, path, body string) (int, map[string]any) {
		w := hostCall(b.g, method, "/api/host/backup/"+path, body, bearer())
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := call("GET", "state", ""); code != 200 || out["before_update"] != "attempt" || out["remote"] != "on" || out["configured"] != true {
		t.Fatalf("state %d %v", code, out)
	}
	b.admin_(t, "PUT", "/api/admin/backups/settings", `{"before_update":"require"}`, 200)
	b.admin_(t, "PUT", "/api/admin/backups/settings", `{"before_update":"sometimes"}`, 400)
	if _, out := call("GET", "state", ""); out["before_update"] != "require" {
		t.Fatalf("setting not kept: %v", out)
	}
	if code, _ := call("POST", "run", `{"reason":"manual"}`); code != 400 {
		t.Fatalf("only pre-update is asked for: %d", code)
	}
	etc := filepath.Join(b.g.config.DataDir, hostEtcDir)
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, hostEtcFile), []byte("etc-archive-bytes"), 0o400); err != nil {
		t.Fatal(err)
	}
	code, out := call("POST", "run", `{"reason":"pre-update"}`)
	if code != 200 || !strings.HasSuffix(out["snapshot_id"].(string), "-pre-update") {
		t.Fatalf("run %d %v", code, out)
	}
	code, out = call("GET", "snapshots", "")
	list, _ := out["snapshots"].([]any)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["has_etc"] != true || list[0].(map[string]any)["etc_captured_at"] == "" {
		t.Fatalf("snapshots %d %v", code, out)
	}
	if _, st := call("GET", "state", ""); st["last_etc_at"] == "" {
		t.Fatalf("state must say when the etc copy was packed: %v", st)
	}
	b.srv.Down = true
	if code, out = call("POST", "run", `{"reason":"pre-update"}`); code != 502 || out["code"] != "backup_store_unavailable" {
		t.Fatalf("failed run %d %v", code, out)
	}
	b.srv.Down = false
	b.admin_(t, "PUT", "/api/admin/backups/settings", `{"remote":"off"}`, 200)
	if code, out = call("POST", "run", `{"reason":"pre-update"}`); code != 409 || out["error"].(map[string]any)["code"] != "backup_remote_off" {
		t.Fatalf("off %d %v", code, out)
	}
	if strings.Contains(b.logs.String(), "etc-archive-bytes") {
		t.Fatal("the etc archive reached the log")
	}
}

// A restore that would not fit is refused by name before anything is touched.
func TestRestoreSpaceCheck(t *testing.T) {
	old := diskSpace
	defer func() { diskSpace = old }()
	diskSpace = func(string) (uint64, uint64, error) { return 1 << 30, 10 << 30, nil }
	if err := checkRestoreSpace(t.TempDir(), 512<<20); err != nil {
		t.Fatalf("fits: %v", err)
	}
	var re *restoreError
	if err := checkRestoreSpace(t.TempDir(), 1<<30); !errors.As(err, &re) || re.code != "restore_no_disk_space" || re.status != 507 {
		t.Fatalf("too big: %v", err)
	}
	diskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs") }
	if err := checkRestoreSpace(t.TempDir(), 1<<40); !errors.As(err, &re) || re.code != "restore_space_unknown" {
		t.Fatalf("an unmeasurable disk must be refused by name: %v", err)
	}
}

// The host reaches the single-account restore by username or id: list, restore
// (a later Bot goes away), undo (it comes back); unknown and original-owner
// accounts are refused by name.
func TestHostAccountRestoreChannel(t *testing.T) {
	b := newBackupFixture(t)
	hostBackupFixture(t, b)
	call := func(path, body string) (int, map[string]any) {
		w := hostCall(b.g, "POST", "/api/host/backup/"+path, body, bearer())
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	ws, err := b.g.workspace(b.member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.store.CreateBot("first", "", "model"); err != nil {
		t.Fatal(err)
	}
	st := b.run(t)
	if _, err = ws.store.CreateBot("later", "", "model"); err != nil {
		t.Fatal(err)
	}
	code, out := call("account-snapshots", `{"account":"org-member"}`)
	list, _ := out["snapshots"].([]any)
	if code != 200 || len(list) != 1 || out["account"] != b.member.ID {
		t.Fatalf("list %d %v", code, out)
	}
	if code, _ = call("account-snapshots", `{"account":"nobody"}`); code != 404 {
		t.Fatalf("unknown account: %d", code)
	}
	if code, out = call("account-restore", `{"account":"`+b.member.ID+`","snapshot":"`+st.LastSnapshot+`"}`); code != 200 || out["restored"] != true {
		t.Fatalf("restore %d %v", code, out)
	}
	ws, _ = b.g.workspace(b.member)
	if bots, _ := ws.store.ListBots(); len(bots) != 1 {
		t.Fatalf("after restore: %d bots", len(bots))
	}
	if code, _ = call("account-undo", `{"account":"org-member"}`); code != 200 {
		t.Fatalf("undo %d", code)
	}
	ws, _ = b.g.workspace(b.member)
	if bots, _ := ws.store.ListBots(); len(bots) != 2 {
		t.Fatalf("after undo: %d bots", len(bots))
	}
	if _, err = b.g.root.store.db.Exec(`UPDATE accounts SET legacy=1 WHERE id=?`, b.admin.ID); err != nil {
		t.Fatal(err)
	}
	if code, out = call("account-snapshots", `{"account":"org-admin"}`); code != 409 || out["error"].(map[string]any)["code"] != "account_restore_unsupported_legacy" {
		t.Fatalf("legacy %d %v", code, out)
	}
}
