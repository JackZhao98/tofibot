package app

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

// backupFixture has a bucket, a configured destination and a confirmed Recovery Key.
type backupFixture struct {
	*orgFixture
	srv  *fakes3.Server
	key  string
	logs *bytes.Buffer
	all  strings.Builder // every response body seen
}

func newBackupFixture(t *testing.T) *backupFixture {
	t.Helper()
	logs := &bytes.Buffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	b := &backupFixture{orgFixture: newOrgFixture(t), logs: logs}
	b.srv = fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(b.srv.Close)
	b.all.WriteString(b.admin_(t, "PUT", "/api/admin/org/resources/backup_store", `{"endpoint":"`+b.srv.URL+`","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, 200))
	out := b.admin_(t, "POST", "/api/admin/backups/recovery-key", `{}`, 200)
	var k struct {
		RecoveryKey string `json:"recovery_key"`
	}
	if json.Unmarshal([]byte(out), &k) != nil || k.RecoveryKey == "" {
		t.Fatalf("no key: %s", out)
	}
	b.key = k.RecoveryKey
	b.admin_(t, "POST", "/api/admin/backups/recovery-key/confirm", `{}`, 200)
	b.all.WriteString(b.admin_(t, "PUT", "/api/admin/backups/settings", `{"remote":"on"}`, 200))
	return b
}

func (b *backupFixture) run(t *testing.T) backupStatus {
	t.Helper()
	if !b.g.beginBackupJob() {
		t.Fatal("slot busy")
	}
	_ = b.g.runBackupLocked(t.Context(), "manual")
	b.g.endBackupJob()
	var v struct {
		Status backupStatus `json:"status"`
	}
	out := b.admin_(t, "GET", "/api/admin/backups", "", 200)
	b.all.WriteString(out)
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err)
	}
	return v.Status
}

func TestBackupFlowRestoresOneAccountAndLeaksNoSecret(t *testing.T) {
	b := newBackupFixture(t)
	ctx := t.Context()
	// Data: the member has one Bot and an upload; the admin has one Bot.
	var wsBots = map[string]*Store{}
	for _, a := range []Account{b.admin, b.member} {
		ws, err := b.g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		wsBots[a.ID] = ws.store
		bot, _ := ws.store.CreateBot("first of "+a.Username, "", "model")
		conv, _ := ws.store.GetConversation(bot.DMConversationID)
		if _, err = ws.store.AddAttachment(conv.ID, "x.txt", "text/plain", strings.NewReader("data of "+a.Username)); err != nil {
			t.Fatal(err)
		}
	}
	st := b.run(t)
	if st.LastStatus != "ok" || st.LastSnapshot == "" {
		t.Fatalf("status %+v", st)
	}
	snaps := b.admin_(t, "GET", "/api/admin/accounts/"+b.member.ID+"/backups", "", 200)
	if !strings.Contains(snaps, st.LastSnapshot) {
		t.Fatalf("snapshots %s", snaps)
	}
	// Later changes to both accounts.
	for _, a := range []Account{b.admin, b.member} {
		ws, _ := b.g.workspace(a)
		if _, err := ws.store.CreateBot("later", "", "model"); err != nil {
			t.Fatal(err)
		}
	}
	adminDir := filepath.Join(b.g.config.DataDir, "accounts", b.admin.ID, "attachments")
	adminFiles, _ := os.ReadDir(adminDir)
	// Wrong confirmation: refused, nothing changes.
	b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore", `{"snapshot":"`+st.LastSnapshot+`","confirm_username":"nope"}`, 400)
	out := b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore", `{"snapshot":"`+st.LastSnapshot+`","confirm_username":"org-member"}`, 200)
	b.all.WriteString(out)
	ws, err := b.g.workspace(b.member)
	if err != nil {
		t.Fatal(err)
	}
	bots, _ := ws.store.ListBots()
	if len(bots) != 1 || !strings.HasPrefix(bots[0].Name, "first of") {
		t.Fatalf("member after restore: %+v", bots)
	}
	if code, _ := b.call(t, "GET", "/api/admin/accounts/"+b.member.ID+"/backups", "", b.mc); code != 403 {
		t.Fatalf("member reached the backup API: %d", code)
	}
	ws, _ = b.g.workspace(b.admin)
	if bots, _ = ws.store.ListBots(); len(bots) != 2 {
		t.Fatalf("admin changed by the member's restore: %d bots", len(bots))
	}
	if after, _ := os.ReadDir(adminDir); len(after) != len(adminFiles) {
		t.Fatalf("admin files changed")
	}
	// Undo brings the later Bot back.
	b.admin_(t, "POST", "/api/admin/accounts/"+b.member.ID+"/restore/undo", `{}`, 200)
	ws, _ = b.g.workspace(b.member)
	if bots, _ = ws.store.ListBots(); len(bots) != 2 {
		t.Fatalf("undo: %d bots", len(bots))
	}
	// The original owner cannot be restored alone.
	if _, err = b.g.root.store.db.Exec(`UPDATE accounts SET legacy=1 WHERE id=?`, b.admin.ID); err != nil {
		t.Fatal(err)
	}
	out = b.admin_(t, "POST", "/api/admin/accounts/"+b.admin.ID+"/restore", `{"snapshot":"`+st.LastSnapshot+`","confirm_username":"org-admin"}`, 409)
	if orgErrCode(t, out) != "account_restore_unsupported_legacy" {
		t.Fatalf("legacy: %s", out)
	}
	// No secret in any response, log or bucket key.
	dek, _ := b.g.backupDEK()
	seen := b.all.String() + b.logs.String() + strings.Join(b.srv.Keys(), "\n")
	for _, secret := range []string{b.key, testSecretKey, string(dek), hexOf(dek)} {
		if strings.Contains(seen, secret) {
			t.Fatalf("a secret leaked: %.8s", secret)
		}
	}
	_ = ctx
	_ = wsBots
}

func TestBackupUploadFailureIsAVisibleStatus(t *testing.T) {
	b := newBackupFixture(t)
	b.srv.Down = true
	st := b.run(t)
	if st.LastStatus != "failed" || st.LastCode != "backup_store_unavailable" || st.LastSuccessAt != "" {
		t.Fatalf("status %+v", st)
	}
	for _, k := range b.srv.Keys() {
		if strings.Contains(k, "/manifest") {
			t.Fatalf("a manifest was committed: %s", k)
		}
	}
	b.srv.Down = false
	if st = b.run(t); st.LastStatus != "ok" {
		t.Fatalf("recovery: %+v", st)
	}
}

func TestBackupSettingsNeedAConfirmedKeyAndValidate(t *testing.T) {
	f := newOrgFixture(t)
	f.admin_(t, "PUT", "/api/admin/backups/settings", `{"remote":"on"}`, 409) // nothing configured
	srv := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(srv.Close)
	f.admin_(t, "PUT", "/api/admin/org/resources/backup_store", `{"endpoint":"`+srv.URL+`","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, 200)
	if got := orgErrCode(t, f.admin_(t, "PUT", "/api/admin/backups/settings", `{"remote":"on"}`, 409)); got != "backup_not_ready" {
		t.Fatalf("no key: %s", got)
	}
	f.admin_(t, "PUT", "/api/admin/backups/settings", `{"schedule":"never"}`, 400)
	f.admin_(t, "PUT", "/api/admin/backups/settings", `{"keep_daily":0}`, 400)
	var v backupView
	if json.Unmarshal([]byte(f.admin_(t, "GET", "/api/admin/backups", "", 200)), &v) != nil || v.Settings.Schedule != "daily" || v.Settings.KeepDaily != 7 || v.Settings.KeepWeekly != 4 || v.Settings.KeepMonthly != 12 || v.Settings.Remote != "off" || v.Settings.StaleAfterHours != 36 {
		t.Fatalf("defaults: %+v", v.Settings)
	}
}

func TestBackupSchedule(t *testing.T) {
	at := time.Date(2026, 11, 10, 3, 5, 0, 0, time.Local)
	if !dueBackup("daily", "", at) || !dueBackup("daily", at.Add(-24*time.Hour).Format(time.RFC3339), at) {
		t.Fatal("daily is due after 03:00 when not yet run today")
	}
	if dueBackup("daily", at.Add(-time.Minute).Format(time.RFC3339), at) || dueBackup("daily", "", at.Add(-time.Hour)) {
		t.Fatal("daily does not run twice a day or before 03:00")
	}
	if dueBackup("manual", "", at) || !dueBackup("hourly", at.Add(-2*time.Hour).Format(time.RFC3339), at) || dueBackup("6h", at.Add(-2*time.Hour).Format(time.RFC3339), at) {
		t.Fatal("manual / hourly / 6h")
	}
	if !dueVerify("weekly", "", at) || dueVerify("manual", "", at) {
		t.Fatal("verify")
	}
}

// A crash between the two renames leaves no account directory; startup puts it back.
func TestAccountRestoreCrashWindowIsSettled(t *testing.T) {
	f := newOrgFixture(t)
	if _, err := f.g.workspace(f.member); err != nil {
		t.Fatal(err)
	}
	f.g.fence(f.member.ID) // closes the runtime so the directory can move
	dir := filepath.Join(f.g.accountsDir(), f.member.ID)
	saved := filepath.Join(f.g.accountsDir(), beforeRestoreTag+f.member.ID+"-20261110T030000.000000000Z")
	if err := os.Rename(dir, saved); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(f.g.accountsDir(), ".restore-"+f.member.ID), 0o700)
	f.g.recoverRestores()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("account directory not put back: %v", err)
	}
}
