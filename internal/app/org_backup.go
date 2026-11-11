package app

// Remote backups (design backups.md, phase 1). The engine is internal/backup;
// this file is the server side: the `backup_store` organization resource (its
// own bucket and access key), the data key held in the organization vault, the
// schedule, the status an administrator reads, and the admin API. The Recovery
// Key is shown once and never stored here.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/backup"
	"github.com/JackZhao98/tofibot/internal/filestore"
)

const (
	orgBackupStoreID = "backup_store"
	orgBackupKind    = "backup_store"
	backupKeyVaultID = "backup:key"
	backupKeyKind    = "backup_key"
)

const backupStateDDL = `CREATE TABLE IF NOT EXISTS backup_state(k TEXT PRIMARY KEY, v TEXT NOT NULL)`

var backupWrapper = backup.Wrapper{
	Wrap:   func(plain []byte, key string) ([]byte, error) { return sealExportFile(plain, key) },
	Unwrap: func(slot []byte, key string) ([]byte, error) { return openExportFile(slot, key) },
}

// backupConfig is the resource's non-secret half and the backup settings, all
// visible on the Backups card. Defaults apply to every blank field.
type backupConfig struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	PathStyle       bool   `json:"path_style"`
	Remote          string `json:"remote"`   // on | off
	Schedule        string `json:"schedule"` // daily | 6h | hourly | manual
	KeepDaily       int    `json:"keep_daily"`
	KeepWeekly      int    `json:"keep_weekly"`
	KeepMonthly     int    `json:"keep_monthly"`
	Verify          string `json:"verify"` // weekly | monthly | manual
	StaleAfterHours int    `json:"stale_after_hours"`
	KeyFingerprint  string `json:"key_fingerprint,omitempty"`
	KeyConfirmedAt  string `json:"key_confirmed_at,omitempty"`
}

func (c backupConfig) withDefaults() backupConfig {
	if c.Remote == "" {
		c.Remote = "off"
	}
	if c.Schedule == "" {
		c.Schedule = "daily"
	}
	if c.KeepDaily == 0 {
		c.KeepDaily = 7
	}
	if c.KeepWeekly == 0 {
		c.KeepWeekly = 4
	}
	if c.KeepMonthly == 0 {
		c.KeepMonthly = 12
	}
	if c.Verify == "" {
		c.Verify = "weekly"
	}
	if c.StaleAfterHours == 0 {
		c.StaleAfterHours = 36
	}
	return c
}

func (c backupConfig) fileStore() fileStoreConfig {
	return fileStoreConfig{Endpoint: c.Endpoint, Region: c.Region, Bucket: c.Bucket, Prefix: c.Prefix, PathStyle: c.PathStyle}
}

func (c backupConfig) retention() backup.Retention {
	return backup.Retention{Daily: c.KeepDaily, Weekly: c.KeepWeekly, Monthly: c.KeepMonthly}
}

// backupStatus is what the last attempts left behind (table backup_state).
type backupStatus struct {
	Running        bool   `json:"running"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	LastStatus     string `json:"last_status,omitempty"` // ok | failed | blocked
	LastCode       string `json:"last_code,omitempty"`
	LastAccount    string `json:"last_account,omitempty"`
	LastSuccessAt  string `json:"last_success_at,omitempty"`
	LastSnapshot   string `json:"last_snapshot,omitempty"`
	LastMs         int64  `json:"last_duration_ms,omitempty"`
	PruneCode      string `json:"prune_code,omitempty"`
	VerifyAt       string `json:"verified_at,omitempty"`
	VerifySnapshot string `json:"verified_snapshot,omitempty"`
	VerifyStatus   string `json:"verify_status,omitempty"` // ok | failed
	VerifyCode     string `json:"verify_code,omitempty"`
	VerifyMs       int64  `json:"verify_duration_ms,omitempty"`
}

func (g *AccountGateway) loadBackupStatus() backupStatus {
	var raw string
	var st backupStatus
	if g.root.store.db.QueryRow(`SELECT v FROM backup_state WHERE k='status'`).Scan(&raw) == nil {
		_ = json.Unmarshal([]byte(raw), &st)
	}
	return st
}

func (g *AccountGateway) saveBackupStatus(st backupStatus) {
	raw, _ := json.Marshal(st)
	if _, err := g.root.store.db.Exec(`INSERT INTO backup_state(k,v) VALUES('status',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, string(raw)); err != nil {
		log.Printf("[backup] record status: %v", err)
	}
}

func (g *AccountGateway) backupConfigRow(ctx context.Context) (backupConfig, orgResourceRow, bool, error) {
	row, found, err := g.getOrgResource(ctx, orgBackupStoreID)
	if err != nil || !found {
		return backupConfig{}.withDefaults(), row, false, err
	}
	var cfg backupConfig
	if json.Unmarshal([]byte(row.Config), &cfg) != nil {
		return backupConfig{}, row, true, errors.New("backup configuration unreadable")
	}
	return cfg.withDefaults(), row, true, nil
}

func (g *AccountGateway) backupBase(cfg backupConfig) string {
	return cfg.Prefix + "v1/" + g.root.instance.ID + "/server/backups/"
}

func (g *AccountGateway) backupBucket(ctx context.Context, cfg backupConfig) (*filestore.S3, error) {
	secret, err := g.orgCredential(ctx, orgBackupStoreID)
	if err != nil {
		return nil, &backup.Error{Code: "backup_store_misconfigured", Cause: err}
	}
	accessKey, key, _ := strings.Cut(secret.Value, "\x00")
	s3, err := filestore.NewS3(cfg.fileStore().s3(accessKey, key))
	if err != nil {
		return nil, &backup.Error{Code: "backup_store_misconfigured", Cause: err}
	}
	return s3, nil
}

func (g *AccountGateway) backupDEK() ([]byte, bool) {
	v, err := g.orgSecrets()
	if err != nil {
		return nil, false
	}
	got, ok := v.secret(backupKeyVaultID, backupKeyKind)
	if !ok {
		return nil, false
	}
	dek, err := base64.StdEncoding.DecodeString(got.Value)
	return dek, err == nil && len(dek) == 32
}

func dekFingerprint(dek []byte) string { return hexOf(dek)[:8] }

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 2*len(b))
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func (g *AccountGateway) storeBackupDEK(dek []byte) error {
	v, err := g.orgSecrets()
	if err != nil {
		return err
	}
	rec := secretRecord{ID: backupKeyVaultID, Name: "Backup data key", Kind: backupKeyKind, Target: "backup", Status: "stored", CreatedAt: now()}
	_, _, err = v.put(rec, base64.StdEncoding.EncodeToString(dek))
	return err
}

// backupRepo opens the repository with the data key from the organization vault.
func (g *AccountGateway) backupRepo(ctx context.Context) (*backup.Repo, backupConfig, error) {
	cfg, _, found, err := g.backupConfigRow(ctx)
	if err != nil {
		return nil, cfg, err
	}
	if !found || cfg.Bucket == "" {
		return nil, cfg, &backup.Error{Code: "backup_store_misconfigured"}
	}
	dek, ok := g.backupDEK()
	if !ok {
		return nil, cfg, &backup.Error{Code: "backup_key_missing"}
	}
	s3, err := g.backupBucket(ctx, cfg)
	if err != nil {
		return nil, cfg, err
	}
	return &backup.Repo{B: s3, Base: g.backupBase(cfg), DEK: dek}, cfg, nil
}

func (g *AccountGateway) backupSource() backup.Source {
	return backup.Source{DataDir: g.config.DataDir, InstanceID: g.root.instance.ID, AppVersion: Version, Schema: SchemaVersion, Staging: filepath.Join(g.config.DataDir, "tmp", "backup")}
}

// beginBackupJob claims the one backup/verify/restore slot.
func (g *AccountGateway) beginBackupJob() bool {
	if !g.backupMu.TryLock() {
		return false
	}
	g.backupBusy.Store(true)
	return true
}

func (g *AccountGateway) endBackupJob() {
	g.backupBusy.Store(false)
	g.backupMu.Unlock()
}

func errorCode(err error) (code, account string) {
	var be *backup.Error
	if errors.As(err, &be) {
		return be.Code, be.Account
	}
	return "backup_failed", ""
}

// runBackupLocked takes one snapshot and applies retention. The caller holds the slot.
func (g *AccountGateway) runBackupLocked(ctx context.Context, reason string) error {
	st := g.loadBackupStatus()
	st.Running, st.LastAttemptAt, st.PruneCode = true, now(), ""
	g.saveBackupStatus(st)
	started := time.Now()
	repo, cfg, err := g.backupRepo(ctx)
	var m *backup.Manifest
	if err == nil {
		m, err = repo.Run(ctx, g.backupSource(), reason, started)
	}
	st.Running, st.LastMs = false, time.Since(started).Milliseconds()
	if err != nil {
		code, account := errorCode(err)
		st.LastStatus, st.LastCode, st.LastAccount = "failed", code, account
		if code == "backup_repo_diverged" {
			st.LastStatus = "blocked"
		}
		switch code {
		case "backup_store_misconfigured":
			go g.recordOrgRejection(orgBackupStoreID, "credentials_rejected")
		case "backup_store_unavailable":
			go g.recordOrgRejection(orgBackupStoreID, "endpoint_unreachable")
		}
		g.saveBackupStatus(st)
		log.Printf("[backup] run failed: %s", code)
		return err
	}
	st.LastStatus, st.LastCode, st.LastAccount, st.LastSuccessAt, st.LastSnapshot = "ok", "", "", now(), m.Snapshot
	if _, perr := repo.Prune(ctx, cfg.retention()); perr != nil {
		st.PruneCode = "backup_prune_failed"
		log.Printf("[backup] prune failed: %s", firstCode(perr))
	}
	g.saveBackupStatus(st)
	return nil
}

func firstCode(err error) string { c, _ := errorCode(err); return c }

func (g *AccountGateway) runVerifyLocked(ctx context.Context) error {
	st := g.loadBackupStatus()
	started := time.Now()
	repo, _, err := g.backupRepo(ctx)
	var rep *backup.Report
	if err == nil {
		rep, err = repo.Verify(ctx, g.backupSource().Staging, SchemaVersion, 20)
	}
	st.VerifyAt, st.VerifyMs = now(), time.Since(started).Milliseconds()
	if err != nil {
		st.VerifyStatus, st.VerifyCode = "failed", firstCode(err)
		log.Printf("[backup] verify failed: %s", st.VerifyCode)
	} else {
		st.VerifyStatus, st.VerifyCode, st.VerifySnapshot = "ok", "", rep.Snapshot
	}
	g.saveBackupStatus(st)
	return err
}

// ---- schedule ----

func dueBackup(schedule, lastAttempt string, at time.Time) bool {
	last, _ := time.Parse(time.RFC3339Nano, lastAttempt)
	switch schedule {
	case "hourly":
		return at.Sub(last) >= time.Hour
	case "6h":
		return at.Sub(last) >= 6*time.Hour
	case "daily":
		slot := time.Date(at.Year(), at.Month(), at.Day(), 3, 0, 0, 0, at.Location())
		return !at.Before(slot) && last.Before(slot)
	}
	return false
}

func dueVerify(mode, last string, at time.Time) bool {
	t, _ := time.Parse(time.RFC3339Nano, last)
	switch mode {
	case "weekly":
		return at.Sub(t) >= 7*24*time.Hour
	case "monthly":
		return at.Sub(t) >= 30*24*time.Hour
	}
	return false
}

func (g *AccountGateway) backupTick(ctx context.Context, at time.Time) {
	cfg, _, found, err := g.backupConfigRow(ctx)
	if err != nil || !found || cfg.Remote != "on" {
		return
	}
	st := g.loadBackupStatus()
	if dueBackup(cfg.Schedule, st.LastAttemptAt, at) && g.beginBackupJob() {
		_ = g.runBackupLocked(ctx, "scheduled")
		g.endBackupJob()
		st = g.loadBackupStatus()
	}
	if st.LastSuccessAt != "" && dueVerify(cfg.Verify, st.VerifyAt, at) && g.beginBackupJob() {
		_ = g.runVerifyLocked(ctx)
		g.endBackupJob()
	}
	g.sweepRestoreLeftovers(at)
}

// startBackupScheduler also settles a run the last process died in.
func (g *AccountGateway) startBackupScheduler() {
	ctx, cancel := context.WithCancel(context.Background())
	g.backupCancel = cancel
	if st := g.loadBackupStatus(); st.Running {
		st.Running, st.LastStatus, st.LastCode = false, "failed", "backup_interrupted"
		g.saveBackupStatus(st)
	}
	_ = os.RemoveAll(g.backupSource().Staging)
	g.recoverRestores()
	g.backupWG.Add(1)
	go func() {
		defer g.backupWG.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-t.C:
				g.backupTick(ctx, at)
			}
		}
	}()
}

func (g *AccountGateway) stopBackupScheduler() {
	if g.backupCancel != nil {
		g.backupCancel()
		g.backupWG.Wait()
	}
}

// ---- the resource: PUT backup_store ----

var backupProbeMessages = fileStoreProbeMessages

func (g *AccountGateway) putOrgBackupStore(w http.ResponseWriter, r *http.Request, sp orgResourceSpec) {
	var in fileStoreIn
	if decodeStrict(r, orgBodyLimit, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
	prev, _, found, err := g.backupConfigRow(ctx)
	if err != nil && found {
		storageFailure(w, "read resource", err)
		return
	}
	cfg := prev
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	cfg.Region = strings.TrimSpace(in.Region)
	cfg.Bucket = strings.TrimSpace(in.Bucket)
	cfg.PathStyle = in.PathStyle
	cfg.Prefix = defaultFileStorePrefix
	if in.Prefix != nil {
		cfg.Prefix = strings.Trim(strings.TrimSpace(*in.Prefix), "/")
		if cfg.Prefix != "" {
			cfg.Prefix += "/"
		}
	}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	if msg, code := validateBucketFields(cfg.fileStore()); code != "" {
		writeErr(w, 400, code, msg)
		return
	}
	accessKey, secretKey := strings.TrimSpace(in.AccessKeyID), strings.TrimSpace(in.Key)
	if accessKey == "" && secretKey == "" {
		if !found || !cfg.fileStore().sameLocation(prev.fileStore()) {
			writeErr(w, 400, "invalid_key", "Enter the access key ID and secret.")
			return
		}
		stored, cerr := g.orgCredential(ctx, sp.ID)
		if cerr != nil {
			writeErr(w, 400, "invalid_key", "Enter the access key ID and secret.")
			return
		}
		accessKey, secretKey, _ = strings.Cut(stored.Value, "\x00")
	}
	if len(accessKey) < 3 || len(accessKey) > 256 || strings.ContainsAny(accessKey, " \t\r\n\x00") || !validSecretKey(secretKey, 1024) {
		writeErr(w, 400, "invalid_key", "Enter the full access key ID and secret.")
		return
	}
	if found && !cfg.fileStore().sameLocation(prev.fileStore()) {
		// A new location starts a new repository: its Recovery Key must be made again.
		cfg.KeyFingerprint, cfg.KeyConfirmedAt, cfg.Remote = "", "", "off"
	}
	g.orgWriteMu.Lock()
	defer g.orgWriteMu.Unlock()
	// A bucket that answers reads with no credentials is refused outright.
	if res := g.verifyFileStore(ctx, cfg.fileStore(), accessKey, secretKey); !res.ok {
		writeErr(w, res.status, res.code, res.msg)
		return
	}
	cfgJSON, _ := json.Marshal(cfg)
	row := orgResourceRow{ID: sp.ID, Kind: sp.Kind, Config: string(cfgJSON), KeyHint: orgKeyHint(accessKey), KeyPrefix: orgKeyPrefix(accessKey), Status: orgStatusVerified, VerifiedAt: sql.NullString{String: now(), Valid: true}, UpdatedAt: now()}
	rec := secretRecord{ID: sp.ID, Name: sp.Name, Kind: sp.Kind, Target: sp.Target, Label: orgKeyHint(accessKey), Status: "stored", CreatedAt: now(), VerifiedAt: row.VerifiedAt.String}
	v, err := g.orgSecrets()
	if err != nil {
		storageFailure(w, "open organization vault", err)
		return
	}
	prevRec, existed, err := v.put(rec, accessKey+"\x00"+secretKey)
	if err == nil {
		if err = g.saveOrgResource(ctx, row); err != nil {
			v.restore(rec.ID, prevRec, existed)
		}
	}
	if err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	writeJSON(w, 200, row.view())
}

// validateBucketFields is the endpoint, bucket and prefix check of the File
// storage form, shared with the Backups form.
func validateBucketFields(c fileStoreConfig) (msg, code string) {
	u, err := url.Parse(c.Endpoint)
	switch {
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "":
		return "Enter the endpoint as https://host (no path).", "invalid_endpoint"
	case !fileStoreBucketPattern.MatchString(c.Bucket):
		return "Enter the bucket name.", "invalid_bucket"
	case !fileStorePrefixPattern.MatchString(c.Prefix):
		return "The prefix takes lowercase letters, digits, dashes and slashes.", "invalid_prefix"
	}
	return "", ""
}

// ---- admin API ----

type backupView struct {
	Configured  bool           `json:"configured"`
	Destination any            `json:"destination,omitempty"`
	Settings    backupConfig   `json:"settings"`
	Key         map[string]any `json:"key"`
	Status      backupStatus   `json:"status"`
	Stale       bool           `json:"stale"`
	NextRunAt   string         `json:"next_run_at,omitempty"`
	Resource    any            `json:"resource,omitempty"`
}

func (g *AccountGateway) backupView(ctx context.Context) (backupView, error) {
	cfg, row, found, err := g.backupConfigRow(ctx)
	if err != nil {
		return backupView{}, err
	}
	_, hasDEK := g.backupDEK()
	v := backupView{Configured: found && cfg.Bucket != "", Settings: cfg, Status: g.loadBackupStatus(),
		Key: map[string]any{"present": hasDEK, "fingerprint": cfg.KeyFingerprint, "confirmed_at": cfg.KeyConfirmedAt}}
	if found {
		v.Destination = map[string]string{"endpoint": cfg.Endpoint, "bucket": cfg.Bucket, "prefix": cfg.Prefix}
		v.Resource = row.view()
	}
	if cfg.Remote == "on" {
		if t, perr := time.Parse(time.RFC3339Nano, v.Status.LastSuccessAt); perr != nil || time.Since(t) > time.Duration(cfg.StaleAfterHours)*time.Hour {
			v.Stale = true
		}
	}
	return v, nil
}

func backupStatusFor(code string) int {
	switch code {
	case "backup_store_unavailable":
		return 502
	case "backup_store_misconfigured", "backup_recovery_key_wrong":
		return 400
	case "backup_in_progress", "backup_repo_diverged", "backup_repo_exists", "backup_key_missing":
		return 409
	}
	return 500
}

func (g *AccountGateway) adminBackups(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/backups"
	path := r.URL.Path
	isAccount := strings.HasPrefix(path, "/api/admin/accounts/") && (strings.HasSuffix(path, "/backups") || strings.HasSuffix(path, "/restore") || strings.HasSuffix(path, "/restore/undo"))
	if path != prefix && !strings.HasPrefix(path, prefix+"/") && !isAccount {
		return false
	}
	if a.Role != "admin" || a.MustChangePassword {
		writeErr(w, 403, "forbidden", "admin with an updated password required")
		return true
	}
	ctx := r.Context()
	if isAccount {
		g.adminAccountBackups(w, r)
		return true
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
	switch {
	case rest == "" && r.Method == http.MethodGet:
		v, err := g.backupView(ctx)
		if err != nil {
			storageFailure(w, "read backups", err)
			return true
		}
		writeJSON(w, 200, v)
	case rest == "snapshots" && r.Method == http.MethodGet:
		repo, _, err := g.backupRepo(ctx)
		if err != nil {
			code, _ := errorCode(err)
			writeErr(w, backupStatusFor(code), code, "backup storage is not ready")
			return true
		}
		snaps, err := repo.List(ctx)
		if err != nil {
			code, _ := errorCode(err)
			writeErr(w, backupStatusFor(code), code, "backup storage did not answer")
			return true
		}
		out := []map[string]any{}
		for _, s := range snaps {
			if s.Committed {
				out = append(out, map[string]any{"name": s.Name, "at": s.Time.Format(time.RFC3339), "reason": s.Reason})
			}
		}
		writeJSON(w, 200, map[string]any{"snapshots": out})
	case rest == "settings" && r.Method == http.MethodPut:
		g.putBackupSettings(w, r)
	case (rest == "run" || rest == "verify") && r.Method == http.MethodPost:
		if !g.beginBackupJob() {
			writeErr(w, 409, "backup_in_progress", "a backup job is already running")
			return true
		}
		go func() {
			defer g.endBackupJob()
			if rest == "run" {
				_ = g.runBackupLocked(g.backupJobContext(), "manual")
			} else {
				_ = g.runVerifyLocked(g.backupJobContext())
			}
		}()
		writeJSON(w, 202, map[string]any{"started": true})
	case rest == "recovery-key" && r.Method == http.MethodPost:
		g.postRecoveryKey(w, r)
	case rest == "recovery-key/confirm" && r.Method == http.MethodPost:
		g.confirmRecoveryKey(w, r)
	case rest == "recovery-card" && r.Method == http.MethodGet:
		g.recoveryCard(w, r)
	default:
		writeErr(w, 404, "not_found", "not found")
	}
	return true
}

func (g *AccountGateway) backupJobContext() context.Context {
	return context.Background()
}

func (g *AccountGateway) putBackupSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Remote          *string `json:"remote"`
		Schedule        *string `json:"schedule"`
		KeepDaily       *int    `json:"keep_daily"`
		KeepWeekly      *int    `json:"keep_weekly"`
		KeepMonthly     *int    `json:"keep_monthly"`
		Verify          *string `json:"verify"`
		StaleAfterHours *int    `json:"stale_after_hours"`
	}
	if decodeStrict(r, 4<<10, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
	cfg, row, found, err := g.backupConfigRow(ctx)
	if err != nil {
		storageFailure(w, "read backups", err)
		return
	}
	if !found {
		writeErr(w, 409, "backup_not_configured", "Set up the backup storage first.")
		return
	}
	oneOf := func(v string, ok ...string) bool {
		for _, o := range ok {
			if v == o {
				return true
			}
		}
		return false
	}
	bad := func(field string) { writeErr(w, 400, "invalid_"+field, "invalid "+field) }
	if in.Remote != nil {
		if !oneOf(*in.Remote, "on", "off") {
			bad("remote")
			return
		}
		cfg.Remote = *in.Remote
	}
	if in.Schedule != nil {
		if !oneOf(*in.Schedule, "daily", "6h", "hourly", "manual") {
			bad("schedule")
			return
		}
		cfg.Schedule = *in.Schedule
	}
	if in.Verify != nil {
		if !oneOf(*in.Verify, "weekly", "monthly", "manual") {
			bad("verify")
			return
		}
		cfg.Verify = *in.Verify
	}
	for _, f := range []struct {
		in   *int
		dst  *int
		name string
		max  int
	}{{in.KeepDaily, &cfg.KeepDaily, "keep_daily", 365}, {in.KeepWeekly, &cfg.KeepWeekly, "keep_weekly", 520}, {in.KeepMonthly, &cfg.KeepMonthly, "keep_monthly", 240}, {in.StaleAfterHours, &cfg.StaleAfterHours, "stale_after_hours", 720}} {
		if f.in != nil {
			if *f.in < 1 || *f.in > f.max {
				bad(f.name)
				return
			}
			*f.dst = *f.in
		}
	}
	if _, hasDEK := g.backupDEK(); cfg.Remote == "on" && (!hasDEK || cfg.KeyConfirmedAt == "") {
		writeErr(w, 409, "backup_not_ready", "Create and confirm the Recovery Key before turning backups on.")
		return
	}
	raw, _ := json.Marshal(cfg)
	row.Config, row.UpdatedAt = string(raw), now()
	g.orgWriteMu.Lock()
	err = g.saveOrgResource(ctx, row)
	g.orgWriteMu.Unlock()
	if err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	v, _ := g.backupView(ctx)
	writeJSON(w, 200, v)
}

// postRecoveryKey creates the Recovery Key (new repository), rotates it (same
// data key) or, with {"import": key}, adopts an existing repository's key. The
// new key is in this response only.
func (g *AccountGateway) postRecoveryKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Import string `json:"import"`
	}
	if decodeStrict(r, 4<<10, &in) != nil {
		writeErr(w, 400, "invalid_request", "invalid request")
		return
	}
	ctx := r.Context()
	if !g.beginBackupJob() {
		writeErr(w, 409, "backup_in_progress", "a backup job is already running")
		return
	}
	defer g.endBackupJob()
	cfg, row, found, err := g.backupConfigRow(ctx)
	if err != nil || !found {
		writeErr(w, 409, "backup_not_configured", "Set up the backup storage first.")
		return
	}
	s3, err := g.backupBucket(ctx, cfg)
	if err != nil {
		writeErr(w, 400, "backup_store_misconfigured", "The backup storage access key is missing.")
		return
	}
	base := g.backupBase(cfg)
	_, statErr := s3.Stat(ctx, base+"repo.json")
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, filestore.ErrNotFound) {
		code, _ := errorCode(&backup.Error{Code: "backup_store_unavailable"})
		writeErr(w, 502, code, "The backup storage did not answer.")
		return
	}
	var key string
	var dek []byte
	confirmed := ""
	switch {
	case in.Import != "":
		repo, oerr := backup.Open(ctx, s3, base, in.Import, backupWrapper)
		if oerr != nil {
			code, _ := errorCode(oerr)
			writeErr(w, backupStatusFor(code), code, "The Recovery Key does not open this repository.")
			return
		}
		dek, confirmed = repo.DEK, now()
	case exists:
		cur, ok := g.backupDEK()
		if !ok {
			writeErr(w, 409, "backup_key_missing", "This repository already exists. Import its Recovery Key.")
			return
		}
		if key, err = exportPassphrase(); err != nil {
			storageFailure(w, "generate key", err)
			return
		}
		if rerr := (&backup.Repo{B: s3, Base: base, DEK: cur}).Rekey(ctx, key, backupWrapper); rerr != nil {
			code, _ := errorCode(rerr)
			writeErr(w, backupStatusFor(code), code, "The Recovery Key could not be replaced.")
			return
		}
		dek = cur
	default:
		if key, err = exportPassphrase(); err != nil {
			storageFailure(w, "generate key", err)
			return
		}
		repo, ierr := backup.Init(ctx, s3, base, key, now(), backupWrapper)
		if ierr != nil {
			code, _ := errorCode(ierr)
			writeErr(w, backupStatusFor(code), code, "The repository could not be created.")
			return
		}
		dek = repo.DEK
	}
	if err = g.storeBackupDEK(dek); err != nil {
		storageFailure(w, "store backup key", err)
		return
	}
	cfg.KeyFingerprint, cfg.KeyConfirmedAt = dekFingerprint(dek), confirmed
	raw, _ := json.Marshal(cfg)
	row.Config, row.UpdatedAt = string(raw), now()
	g.orgWriteMu.Lock()
	err = g.saveOrgResource(ctx, row)
	g.orgWriteMu.Unlock()
	if err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	out := map[string]any{"fingerprint": cfg.KeyFingerprint, "confirmed_at": confirmed}
	if key != "" {
		out["recovery_key"] = key
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

func (g *AccountGateway) confirmRecoveryKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg, row, found, err := g.backupConfigRow(ctx)
	if err != nil || !found || cfg.KeyFingerprint == "" {
		writeErr(w, 409, "backup_key_missing", "Create the Recovery Key first.")
		return
	}
	cfg.KeyConfirmedAt = now()
	raw, _ := json.Marshal(cfg)
	row.Config, row.UpdatedAt = string(raw), now()
	g.orgWriteMu.Lock()
	err = g.saveOrgResource(ctx, row)
	g.orgWriteMu.Unlock()
	if err != nil {
		storageFailure(w, "store resource", err)
		return
	}
	v, _ := g.backupView(ctx)
	writeJSON(w, 200, v)
}

// recoveryCard is the plain-text card to keep beside the Recovery Key. It holds
// where the repository is, never a key.
func (g *AccountGateway) recoveryCard(w http.ResponseWriter, r *http.Request) {
	cfg, _, found, err := g.backupConfigRow(r.Context())
	if err != nil || !found {
		writeErr(w, 404, "not_found", "not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="tofi-recovery-card.txt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("TOFI recovery card (no keys on this card)\n" +
		"Endpoint: " + cfg.Endpoint + "\nBucket: " + cfg.Bucket + "\nPrefix: " + cfg.Prefix + "\nInstance: " + g.root.instance.ID +
		"\nKey fingerprint: " + cfg.KeyFingerprint + "\nRestore: sudo tofi restore --from-remote\n"))
}
