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
	b.srv, b.key = setUpBackups(t, b.orgFixture, &b.all)
	return b
}

// setUpBackups gives f a backup store on a fake bucket of its own, a confirmed Recovery Key and remote backups on.
// Every response body goes to seen when it is not nil.
func setUpBackups(t *testing.T, f *orgFixture, seen *strings.Builder) (*fakes3.Server, string) {
	t.Helper()
	srv := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(srv.Close)
	record := func(body string) {
		if seen != nil {
			seen.WriteString(body)
		}
	}
	record(f.admin_(t, "PUT", "/api/admin/org/resources/backup_store", `{"endpoint":"`+srv.URL+`","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, 200))
	out := f.admin_(t, "POST", "/api/admin/backups/recovery-key", `{}`, 200)
	var k struct {
		RecoveryKey string `json:"recovery_key"`
	}
	if json.Unmarshal([]byte(out), &k) != nil || k.RecoveryKey == "" {
		t.Fatalf("no key: %s", out)
	}
	f.admin_(t, "POST", "/api/admin/backups/recovery-key/confirm", `{}`, 200)
	record(f.admin_(t, "PUT", "/api/admin/backups/settings", `{"remote":"on"}`, 200))
	return srv, k.RecoveryKey
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

// Run, Verify and the snapshot list need the confirmed Recovery Key, and Verify a snapshot of this server. An early
// press is refused before any job starts: the card does not turn failed and no admin is alerted.
func TestBackupActionsWaitForTheRecoveryKey(t *testing.T) {
	f := newOrgFixture(t)
	refused := func(path, want string) {
		t.Helper()
		if got := orgErrCode(t, f.admin_(t, "POST", "/api/admin/backups/"+path, `{}`, 409)); got != want {
			t.Fatalf("%s: %s, want %s", path, got, want)
		}
	}
	refused("run", "backup_not_ready") // nothing configured
	srv := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(srv.Close)
	f.admin_(t, "PUT", "/api/admin/org/resources/backup_store", `{"endpoint":"`+srv.URL+`","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, 200)
	refused("run", "backup_not_ready")
	refused("verify", "backup_not_ready")
	f.admin_(t, "POST", "/api/admin/backups/recovery-key", `{}`, 200)
	refused("verify", "backup_not_ready") // created, not yet confirmed
	f.admin_(t, "POST", "/api/admin/backups/recovery-key/confirm", `{}`, 200)
	refused("verify", "backup_no_snapshot")
	if st := f.g.loadBackupStatus(); st.VerifyStatus != "" || st.VerifyAlertCode != "" || st.LastStatus != "" || f.g.backupBusy.Load() {
		t.Fatalf("a refused press touched the status: %+v", st)
	}
	f.admin_(t, "POST", "/api/admin/backups/run", `{}`, 202)
	for i := 0; i < 500 && f.g.backupBusy.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if st := f.g.loadBackupStatus(); st.LastStatus != "ok" {
		t.Fatalf("run: %+v", st)
	}
	f.admin_(t, "POST", "/api/admin/backups/verify", `{}`, 202)
	for i := 0; i < 500 && f.g.backupBusy.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if st := f.g.loadBackupStatus(); st.VerifyStatus != "ok" {
		t.Fatalf("verify: %+v", st)
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

func restoreRemote(t *testing.T, req map[string]any) (int, map[string]json.RawMessage) {
	t.Helper()
	in, _ := json.Marshal(req)
	var out bytes.Buffer
	code := RunRestoreRemote(t.Context(), bytes.NewReader(in), &out)
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("answer %q: %v", out.String(), err)
	}
	return code, got
}

// The bare-host path: list, choose, fetch with the Recovery Key alone; the
// restored tree opens with the app's own code and the old server is fenced off.
func TestRestoreRemoteRebuildsTheDataDirAndTakesOver(t *testing.T) {
	b := newBackupFixture(t)
	ws, err := b.g.workspace(b.member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ws.store.CreateBot("kept", "", "model"); err != nil {
		t.Fatal(err)
	}
	if st := b.run(t); st.LastStatus != "ok" {
		t.Fatalf("%+v", st)
	}
	base := map[string]any{"endpoint": b.srv.URL, "region": "us-east-1", "bucket": "bkt", "prefix": "tofi", "path_style": true, "access_key_id": testAccessKeyID, "secret": testSecretKey}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	code, got := restoreRemote(t, with(map[string]any{"action": "instances"}))
	if code != 0 || !strings.Contains(string(got["instances"]), b.g.root.instance.ID) {
		t.Fatalf("instances: %d %s", code, got["instances"])
	}
	id := b.g.root.instance.ID
	if code, got = restoreRemote(t, with(map[string]any{"action": "snapshots", "instance_id": id, "recovery_key": "WRONG-KEY"})); code == 0 || string(got["error"]) != `"backup_recovery_key_wrong"` {
		t.Fatalf("wrong key: %d %s", code, got["error"])
	}
	if code, got = restoreRemote(t, with(map[string]any{"action": "snapshots", "instance_id": id, "recovery_key": b.key})); code != 0 || !strings.Contains(string(got["snapshots"]), `"schema_supported":true`) {
		t.Fatalf("snapshots: %d %s", code, got["snapshots"])
	}
	out := t.TempDir()
	if code, got = restoreRemote(t, with(map[string]any{"action": "fetch", "instance_id": id, "recovery_key": b.key, "mode": "takeover", "out": out})); code != 0 {
		t.Fatalf("fetch: %s", got["error"])
	}
	restored := filepath.Join(out, "data")
	if _, err = os.Stat(filepath.Join(restored, "accounts", b.member.ID, "tofi.db")); err != nil {
		t.Fatal(err)
	}
	store, err := openStoreWithLimit(filepath.Join(restored, "accounts", b.member.ID), false, 0)
	if err != nil {
		t.Fatal(err)
	}
	bots, _ := store.ListBots()
	store.Close()
	if len(bots) != 1 || bots[0].Name != "kept" {
		t.Fatalf("restored bots %+v", bots)
	}
	// The original server is no longer the writer.
	if st := b.run(t); st.LastStatus != "blocked" || st.LastCode != "backup_repo_diverged" {
		t.Fatalf("old server: %+v", st)
	}
}

// adminAlerts returns the admin_alert details in an account's workspace, oldest first.
func adminAlerts(t *testing.T, g *AccountGateway, a Account) []string {
	t.Helper()
	ws, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ws.store.attentionAfter(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		if r.Kind != attentionAdminAlert {
			t.Fatalf("unexpected kind %q", r.Kind)
		}
		out = append(out, r.Detail)
	}
	return out
}

func TestBackupFailureEpisodesMakeOneAdminEventEach(t *testing.T) {
	b := newBackupFixture(t)
	b.srv.Down = true
	b.run(t)
	b.run(t) // the same failure again: no second event
	if got := adminAlerts(t, b.g, b.admin); len(got) != 1 || got[0] != "backup_store_unavailable" {
		t.Fatalf("after a repeated failure: %v", got)
	}
	b.srv.Down = false
	if st := b.run(t); st.LastStatus != "ok" { // success: one recovered event
		t.Fatalf("recovery: %+v", st)
	}
	time.Sleep(1100 * time.Millisecond)        // a snapshot id has one-second resolution
	if st := b.run(t); st.LastStatus != "ok" { // a plain success after that: nothing
		t.Fatalf("second success: %+v", st)
	}
	if got := adminAlerts(t, b.g, b.admin); len(got) != 2 || got[1] != backupAlertRecovered {
		t.Fatalf("after recovery: %v", got)
	}
	b.srv.Down = true
	b.run(t) // a new episode after a success: a new event
	if got := adminAlerts(t, b.g, b.admin); len(got) != 3 || got[2] != "backup_store_unavailable" {
		t.Fatalf("new episode: %v", got)
	}
	if got := adminAlerts(t, b.g, b.member); len(got) != 0 {
		t.Fatalf("a member was notified: %v", got)
	}
}

func TestBackupStaleIsOneEventPerEpisode(t *testing.T) {
	b := newBackupFixture(t)
	b.run(t)
	cfg, _, _, err := b.g.backupConfigRow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cfg = cfg.withDefaults()
	at := time.Now()
	b.g.alertIfStale(cfg, at.Add(time.Hour)) // fresh: nothing
	b.g.alertIfStale(cfg, at.Add(40*time.Hour))
	b.g.alertIfStale(cfg, at.Add(41*time.Hour)) // same episode
	if got := adminAlerts(t, b.g, b.admin); len(got) != 1 || got[0] != backupAlertStale {
		t.Fatalf("stale: %v", got)
	}
	time.Sleep(1100 * time.Millisecond)
	if st := b.run(t); st.LastStatus != "ok" { // a success closes the episode with one recovered event
		t.Fatalf("success: %+v", st)
	}
	b.g.alertIfStale(cfg, time.Now().Add(40*time.Hour))
	if got := adminAlerts(t, b.g, b.admin); len(got) != 3 || got[1] != backupAlertRecovered || got[2] != backupAlertStale {
		t.Fatalf("second episode: %v", got)
	}
}
