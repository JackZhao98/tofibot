package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// Deleting an account first writes a passphrase-encrypted copy of its Bot setup
// (the existing account portability bundle: Bots, conversations, memories,
// schedules, settings and, when readable and within limits, attachments). The
// file lives outside accounts/<id>, is served through an unguessable link and
// is removed after the retention period or when an admin deletes it.
const deletedExportRetention = 30 * 24 * time.Hour
const deletedExportDirName = "deleted-account-exports"

const exportTables = `CREATE TABLE IF NOT EXISTS deleted_account_exports(
 id TEXT PRIMARY KEY, account_id TEXT NOT NULL UNIQUE, username TEXT NOT NULL, email TEXT NOT NULL,
 created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, size INTEGER NOT NULL, sha256 TEXT NOT NULL,
 token_hash BLOB NOT NULL, passphrase_sealed BLOB, attachments INTEGER NOT NULL DEFAULT 0, counts_json TEXT NOT NULL DEFAULT '{}')`

type deletedExport struct {
	ID             string         `json:"id"`
	Username       string         `json:"username"`
	Email          string         `json:"email,omitempty"`
	CreatedAt      int64          `json:"created_at"`
	ExpiresAt      int64          `json:"expires_at"`
	Size           int64          `json:"size"`
	SHA256         string         `json:"sha256,omitempty"`
	LinkPath       string         `json:"link_path"`
	Passphrase     string         `json:"passphrase,omitempty"`
	PassphraseOpen bool           `json:"passphrase_pending"`
	Attachments    bool           `json:"attachments_included"`
	Counts         map[string]int `json:"counts,omitempty"`
}

func (g *AccountGateway) exportDir() string {
	return filepath.Join(g.config.DataDir, deletedExportDirName)
}

// exportDirReady returns the export directory, created 0700, never a symlink.
func (g *AccountGateway) exportDirReady() (string, error) {
	dir := g.exportDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", errors.New("unexpected export directory")
	}
	return dir, nil
}

// linkKey is a per-installation random key kept beside the exports. Download
// tokens and the sealed pending passphrase derive from it, so the database
// alone (which stores only token hashes) cannot reconstruct a link.
func (g *AccountGateway) linkKey() ([]byte, error) {
	dir, err := g.exportDirReady()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".link-key")
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != 32 {
			return nil, errors.New("invalid export link key")
		}
		return data, nil
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return g.linkKey()
		}
		return nil, err
	}
	if _, err = f.Write(key); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return key, err
}

func exportToken(key []byte, id string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("link:" + id))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func exportPassphrase() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	parts := make([]string, 0, 8)
	for i := 0; i < len(s); i += 4 { // 32 characters = 160 bits, in 8 groups
		parts = append(parts, s[i:i+4])
	}
	return strings.Join(parts, "-"), nil
}

type exportEnvelope struct {
	Format     string `json:"format"`
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Time       uint32 `json:"kdf_time"`
	MemoryKiB  uint32 `json:"kdf_memory_kib"`
	Threads    uint8  `json:"kdf_threads"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Cipher     string `json:"cipher"`
	Ciphertext string `json:"ciphertext"`
}

const exportFormat = "tofi.bundle.encrypted"

func exportAAD(e exportEnvelope) []byte {
	return []byte(fmt.Sprintf("%s|%d|%s|%d|%d|%d|%s|%s", e.Format, e.Version, e.KDF, e.Time, e.MemoryKiB, e.Threads, e.Salt, e.Cipher))
}

// sealExportFile encrypts a portability bundle (JSON) with argon2id + AES-256-GCM.
func sealExportFile(bundle []byte, passphrase string) ([]byte, error) {
	salt := make([]byte, 16)
	nonce := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	e := exportEnvelope{Format: exportFormat, Version: 1, KDF: "argon2id", Time: 3, MemoryKiB: 64 * 1024, Threads: 1, Salt: base64.RawStdEncoding.EncodeToString(salt), Nonce: base64.RawStdEncoding.EncodeToString(nonce), Cipher: "aes-256-gcm"}
	key := argon2.IDKey([]byte(passphrase), salt, e.Time, e.MemoryKiB, e.Threads, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	e.Ciphertext = base64.RawStdEncoding.EncodeToString(gcm.Seal(nil, nonce, bundle, exportAAD(e)))
	return json.Marshal(e)
}

func openExportFile(file []byte, passphrase string) ([]byte, error) {
	var e exportEnvelope
	if json.Unmarshal(file, &e) != nil || e.Format != exportFormat || e.Version != 1 || e.KDF != "argon2id" || e.Cipher != "aes-256-gcm" || e.Time != 3 || e.MemoryKiB != 64*1024 || e.Threads != 1 {
		return nil, errors.New("unsupported export file")
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(e.Salt)
	nonce, err2 := base64.RawStdEncoding.DecodeString(e.Nonce)
	sealed, err3 := base64.RawStdEncoding.DecodeString(e.Ciphertext)
	if err1 != nil || err2 != nil || err3 != nil || len(salt) != 16 || len(nonce) != 12 {
		return nil, errors.New("malformed export file")
	}
	key := argon2.IDKey([]byte(passphrase), salt, e.Time, e.MemoryKiB, e.Threads, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, sealed, exportAAD(e))
}

func sealSmall(key []byte, plain string) ([]byte, error) {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("passphrase"))
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

func openSmall(key, sealed []byte) (string, error) {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("passphrase"))
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("short sealed value")
	}
	plain, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	return string(plain), err
}

// exportBundle opens the (deactivated) account's own store in a read-only
// control-plane runtime and produces its account bundle. Credentials, the
// environment vault and computer files are never selected.
func (g *AccountGateway) exportBundle(ctx context.Context, a Account) ([]byte, bool, error) {
	c := Config{AccountDBMaxBytes: g.config.AccountDBMaxBytes, DataDir: filepath.Join(g.config.DataDir, "accounts", a.ID), UIDir: g.config.UIDir, Listen: g.root.listen, PublicOrigin: g.root.publicOrigin, Provider: "openai_codex", IsolatedWorkspace: true, Environment: g.root.instance.Environment, OwnerAllowLoopbackHTTP: g.config.OwnerAllowLoopbackHTTP, AccountControlPlane: true, AccountID: a.ID}
	create := g.runtimeFactory
	if create == nil {
		create = NewServer
	}
	runtime, err := create(c)
	if err != nil {
		return nil, false, err
	}
	defer runtime.Close()
	try := func(categories []string) ([]byte, error) {
		b, err := runtime.store.exportPortable(ctx, runtime.instance.ID, portableSelection{Categories: categories}, "account")
		if err != nil {
			return nil, err
		}
		b.Counts = b.counts()
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		if len(raw) > portableMaxBytes {
			return nil, errors.New("export exceeds the bundle size limit")
		}
		return raw, nil
	}
	raw, err := try(append([]string(nil), portableDefaultCategories...))
	if err == nil {
		return raw, containsPortable(portableDefaultCategories, "attachments") && bytes.Contains(raw, []byte(`"attachments":[`)), nil
	}
	// Attachments are the only category bounded by the byte limits; retry
	// without them before giving up so a large file never blocks closing.
	var without []string
	for _, c := range portableDefaultCategories {
		if c != "attachments" {
			without = append(without, c)
		}
	}
	raw, err = try(without)
	return raw, false, err
}

// exportAccount is deletion step "export". It is idempotent: a verified
// export already recorded for the account is kept.
func (g *AccountGateway) exportAccount(ctx context.Context, a Account) error {
	var existing string
	if err := g.root.store.db.QueryRowContext(ctx, `SELECT id FROM deleted_account_exports WHERE account_id=?`, a.ID).Scan(&existing); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	dir, err := g.exportDirReady()
	if err != nil {
		return err
	}
	key, err := g.linkKey()
	if err != nil {
		return err
	}
	bundle, attachments, err := g.exportBundle(ctx, a)
	if err != nil {
		return err
	}
	parsed, err := parsePortableBundle(bundle)
	if err != nil {
		return fmt.Errorf("export bundle invalid: %w", err)
	}
	pass, err := exportPassphrase()
	if err != nil {
		return err
	}
	sealed, err := sealExportFile(bundle, pass)
	if err != nil {
		return err
	}
	raw := make([]byte, 16)
	if _, err = rand.Read(raw); err != nil {
		return err
	}
	id := base64.RawURLEncoding.EncodeToString(raw)
	file := filepath.Join(dir, id+".tofi")
	sidecar := filepath.Join(dir, id+".json")
	committed := false
	defer func() {
		if !committed {
			os.Remove(file)
			os.Remove(sidecar)
		}
	}()
	digest := sha256.Sum256(sealed)
	created := time.Now()
	expires := created.Add(deletedExportRetention)
	email := a.Email
	if strings.HasSuffix(email, "@account.invalid") {
		email = ""
	}
	meta, _ := json.Marshal(map[string]any{"username": a.Username, "email": email, "created_at": created.Unix(), "expires_at": expires.Unix(), "size": len(sealed), "sha256": fmt.Sprintf("%x", digest)})
	if err = writeExportFile(file, sealed); err != nil {
		return err
	}
	if err = writeExportFile(sidecar, meta); err != nil {
		return err
	}
	// Verify what is on disk, not what was in memory: hash, decrypt, parse.
	onDisk, err := os.ReadFile(file)
	if err != nil || sha256.Sum256(onDisk) != digest {
		return errors.New("export file does not match what was written")
	}
	plain, err := openExportFile(onDisk, pass)
	if err != nil {
		return errors.New("export file cannot be decrypted")
	}
	check, err := parsePortableBundle(plain)
	if err != nil || check.Kind != "account" || len(check.Bots) != len(parsed.Bots) || len(check.Messages) != len(parsed.Messages) {
		return errors.New("export file content does not verify")
	}
	hash := sha256.Sum256([]byte(exportToken(key, id)))
	pending, err := sealSmall(key, pass)
	if err != nil {
		return err
	}
	counts, _ := json.Marshal(check.counts())
	if _, err = g.root.store.db.ExecContext(ctx, `INSERT INTO deleted_account_exports(id,account_id,username,email,created_at,expires_at,size,sha256,token_hash,passphrase_sealed,attachments,counts_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, a.ID, a.Username, email, created.Unix(), expires.Unix(), len(sealed), fmt.Sprintf("%x", digest), hash[:], pending, attachments, string(counts)); err != nil {
		return err
	}
	committed = true
	return nil
}

func writeExportFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (g *AccountGateway) exportRow(ctx context.Context, where string, arg any, withPassphrase bool) (deletedExport, error) {
	var x deletedExport
	var sealed []byte
	var attachments int
	var counts string
	err := g.root.store.db.QueryRowContext(ctx, `SELECT id,username,email,created_at,expires_at,size,sha256,passphrase_sealed,attachments,counts_json FROM deleted_account_exports WHERE `+where, arg).Scan(&x.ID, &x.Username, &x.Email, &x.CreatedAt, &x.ExpiresAt, &x.Size, &x.SHA256, &sealed, &attachments, &counts)
	if err != nil {
		return x, err
	}
	x.Attachments = attachments == 1
	json.Unmarshal([]byte(counts), &x.Counts)
	x.PassphraseOpen = len(sealed) > 0
	key, err := g.linkKey()
	if err != nil {
		return x, err
	}
	x.LinkPath = "/exports/" + exportToken(key, x.ID)
	if withPassphrase && len(sealed) > 0 {
		if x.Passphrase, err = openSmall(key, sealed); err != nil {
			return x, err
		}
	}
	return x, nil
}

// sweepDeletedExports removes expired exports and unreferenced files. It is
// safe to run at any time and returns how many exports were removed.
func (g *AccountGateway) sweepDeletedExports(now time.Time) (int, error) {
	dir := g.exportDir()
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return 0, nil
	}
	rows, err := g.root.store.db.Query(`SELECT id,expires_at FROM deleted_account_exports`)
	if err != nil {
		return 0, err
	}
	live := map[string]bool{}
	var expired []string
	for rows.Next() {
		var id string
		var exp int64
		if err = rows.Scan(&id, &exp); err != nil {
			rows.Close()
			return 0, err
		}
		if exp <= now.Unix() {
			expired = append(expired, id)
		} else {
			live[id] = true
		}
	}
	rows.Close()
	for _, id := range expired {
		if err = g.removeExport(id); err != nil {
			return 0, err
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		if (ext == ".tofi" || ext == ".json") && !live[strings.TrimSuffix(name, ext)] {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > time.Hour {
				os.Remove(filepath.Join(dir, name))
			}
		}
	}
	return len(expired), nil
}

func (g *AccountGateway) removeExport(id string) error {
	if strings.ContainsAny(id, "/\\.") || id == "" {
		return errors.New("invalid export id")
	}
	dir := g.exportDir()
	os.Remove(filepath.Join(dir, id+".tofi"))
	os.Remove(filepath.Join(dir, id+".json"))
	_, err := g.root.store.db.Exec(`DELETE FROM deleted_account_exports WHERE id=?`, id)
	return err
}

func (g *AccountGateway) startExportSweeper() {
	g.sweepStop = make(chan struct{})
	go func(stop chan struct{}) {
		g.sweepDeletedExports(time.Now())
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				g.sweepDeletedExports(time.Now())
			}
		}
	}(g.sweepStop)
}

// ---- admin API -----------------------------------------------------------

func (g *AccountGateway) adminExports(w http.ResponseWriter, r *http.Request, a Account) bool {
	const prefix = "/api/admin/deleted-exports"
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		return false
	}
	if a.Role != "admin" {
		writeErr(w, 403, "forbidden", "admin required")
		return true
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if rest == "" {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "unsupported method")
			return true
		}
		g.sweepDeletedExports(time.Now())
		rows, err := g.root.store.db.QueryContext(r.Context(), `SELECT id FROM deleted_account_exports ORDER BY created_at DESC`)
		if err != nil {
			writeErr(w, 500, "storage", "cannot list exports")
			return true
		}
		var ids []string
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		items := []deletedExport{}
		for _, id := range ids {
			x, err := g.exportRow(r.Context(), `id=?`, id, false)
			if err != nil {
				writeErr(w, 500, "storage", "cannot list exports")
				return true
			}
			x.SHA256 = ""
			items = append(items, x)
		}
		writeJSON(w, 200, items)
		return true
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	if id == "" || strings.ContainsAny(id, ".\\") || len(parts) > 2 {
		writeErr(w, 404, "not_found", "export not found")
		return true
	}
	var exists bool
	g.root.store.db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM deleted_account_exports WHERE id=?)`, id).Scan(&exists)
	if !exists {
		writeErr(w, 404, "not_found", "export not found")
		return true
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := g.removeExport(id); err != nil {
			writeErr(w, 500, "storage", "cannot delete export")
			return true
		}
		writeJSON(w, 200, map[string]any{"id": id, "deleted": true})
	case len(parts) == 2 && parts[1] == "passphrase" && r.Method == http.MethodGet:
		x, err := g.exportRow(r.Context(), `id=?`, id, true)
		if err != nil || x.Passphrase == "" {
			writeErr(w, 409, "passphrase_unavailable", "the passphrase was already acknowledged and is no longer stored")
			return true
		}
		writeJSON(w, 200, map[string]string{"passphrase": x.Passphrase})
	case len(parts) == 2 && parts[1] == "ack" && r.Method == http.MethodPost:
		if _, err := g.root.store.db.ExecContext(r.Context(), `UPDATE deleted_account_exports SET passphrase_sealed=NULL WHERE id=?`, id); err != nil {
			writeErr(w, 500, "storage", "cannot acknowledge")
			return true
		}
		writeJSON(w, 200, map[string]any{"id": id, "passphrase_pending": false})
	default:
		writeErr(w, 405, "method_not_allowed", "unsupported method")
	}
	return true
}

// ---- public download -----------------------------------------------------

type downloadLimiter struct {
	mu     sync.Mutex
	window time.Time
	peers  map[string]int
	total  int
}

func (l *downloadLimiter) allow(peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.peers == nil || now.Sub(l.window) >= time.Minute {
		l.window, l.peers, l.total = now, map[string]int{}, 0
	}
	l.peers[peer]++
	l.total++
	return l.peers[peer] <= 20 && l.total <= 200
}

func (g *AccountGateway) serveDeletedExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return
	}
	if !g.downloads.allow(g.auth.peerBucket(r), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, 429, "rate_limited", "try later")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/exports/")
	// Every request does the same work whatever the token looks like.
	sum := sha256.Sum256([]byte(token))
	var found string
	rows, err := g.root.store.db.QueryContext(r.Context(), `SELECT id,token_hash,expires_at FROM deleted_account_exports`)
	if err == nil {
		now := time.Now().Unix()
		for rows.Next() {
			var id string
			var hash []byte
			var exp int64
			if rows.Scan(&id, &hash, &exp) != nil {
				continue
			}
			if subtle.ConstantTimeCompare(hash, sum[:])&boolToInt(exp > now) == 1 {
				found = id
			}
		}
		rows.Close()
	}
	notFound := func() { writeErr(w, 404, "not_found", "not found") }
	if found == "" || len(token) != 43 {
		notFound()
		return
	}
	path := filepath.Join(g.exportDir(), found+".tofi")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		notFound()
		return
	}
	f, err := os.Open(path)
	if err != nil {
		notFound()
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="tofi-account-export-`+found[:8]+`.tofi"`)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(200)
	if r.Method == http.MethodGet {
		io.Copy(w, f)
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
