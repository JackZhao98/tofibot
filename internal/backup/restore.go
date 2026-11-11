package backup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
)

// RestoreOpts bounds a restore or a drill.
type RestoreOpts struct {
	// MaxSchema is the newest database schema the installed program opens; a
	// newer snapshot is refused (backup_schema_too_new), never downgraded.
	MaxSchema int
	// Existing is a directory holding the account's current files: an
	// attachment already there is copied, not downloaded (single-account restore).
	Existing string
	// Sample limits attachment downloads to this many per database (the drill); 0 is all.
	Sample int
	// Full runs integrity_check instead of quick_check (the drill).
	Full bool
}

// EntryReport is what one restored database came back with.
type EntryReport struct {
	ID          string         `json:"id"`
	Rows        map[string]int `json:"rows"`
	DiskFiles   int            `json:"disk_files"`
	MissingObjs int            `json:"missing_files"` // attachments the bucket no longer has
	VM          int            `json:"vm_refs"`
	Obj         int            `json:"bucket_refs"`
	VaultKeys   int            `json:"vault_records"`
}

// Report is the restore report (design 9.1 step 6).
type Report struct {
	Snapshot string        `json:"snapshot"`
	At       string        `json:"at"`
	Entries  []EntryReport `json:"entries"`
	Elapsed  time.Duration `json:"elapsed_ns"`
}

func equalRows(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// restoreEntry rebuilds one database directory: the database (hash, schema and
// row counts checked), its small files, and its disk attachments.
func (r *Repo) restoreEntry(ctx context.Context, snap string, e Entry, dir string, o RestoreOpts) (EntryReport, error) {
	rel, attKey := "root", OwnerKey
	if e.ID != "root" {
		rel, attKey = joinKey("accounts", e.ID), e.ID
	}
	rep := EntryReport{ID: e.ID, Rows: e.Rows, VM: e.VM, Obj: e.Obj}
	wrap := func(err error) error {
		var be *Error
		if errors.As(err, &be) && be.Account == "" && e.ID != "root" {
			be.Account = e.ID
		}
		return err
	}
	if e.Schema > o.MaxSchema {
		return rep, &Error{Code: "backup_schema_too_new", Account: e.ID}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return rep, err
	}
	packed, err := r.get(ctx, joinKey("snapshots", snap, rel, "db"))
	if err != nil {
		return rep, wrap(err)
	}
	plain, err := gunzip(packed)
	if err != nil {
		return rep, wrap(err)
	}
	if hexSum(plain) != e.DBSHA {
		return rep, &Error{Code: "backup_object_damaged", Account: e.ID}
	}
	dbPath := filepath.Join(dir, "tofi.db")
	if err = os.WriteFile(dbPath, plain, 0o600); err != nil {
		return rep, err
	}
	schema, rows, err := inspect(dbPath, o.Full)
	if err != nil {
		return rep, wrap(err)
	}
	if schema != e.Schema || !equalRows(rows, e.Rows) {
		return rep, &Error{Code: "backup_capture_corrupt", Account: e.ID}
	}
	packed, err = r.get(ctx, joinKey("snapshots", snap, rel, "files"))
	if err != nil {
		return rep, wrap(err)
	}
	tarball, err := gunzip(packed)
	if err != nil {
		return rep, wrap(err)
	}
	if hexSum(tarball) != e.FilesSHA {
		return rep, &Error{Code: "backup_object_damaged", Account: e.ID}
	}
	if err = untar(tarball, dir); err != nil {
		return rep, wrap(err)
	}
	rep.VaultKeys = vaultRecords(dir)
	names, shas, err := attachmentRows(dbPath)
	if err != nil {
		return rep, err
	}
	ids := append([]string(nil), e.Disk...)
	if o.Sample > 0 && len(ids) > o.Sample {
		rand.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		ids = ids[:o.Sample]
	}
	attDir := filepath.Join(dir, "attachments")
	for _, id := range ids {
		disk := filepath.Base(names[id])
		if names[id] == "" || disk == "." {
			continue
		}
		if err = os.MkdirAll(attDir, 0o700); err != nil {
			return rep, err
		}
		if o.Existing != "" {
			if src, oerr := os.Open(filepath.Join(o.Existing, "attachments", disk)); oerr == nil {
				err = writeFile(filepath.Join(attDir, disk), src, 0o600)
				src.Close()
				if err != nil {
					return rep, err
				}
				rep.DiskFiles++
				continue
			}
		}
		raw, gerr := r.get(ctx, joinKey("files", attKey, id))
		if CodeOf(gerr) == "backup_object_missing" {
			rep.MissingObjs++
			continue
		}
		if gerr != nil {
			return rep, wrap(gerr)
		}
		if want := shas[id]; want != "" && hexSum(raw) != want {
			return rep, &Error{Code: "backup_object_damaged", Account: e.ID}
		}
		if err = os.WriteFile(filepath.Join(attDir, disk), raw, 0o600); err != nil {
			return rep, err
		}
		rep.DiskFiles++
	}
	return rep, nil
}

func attachmentRows(dbPath string) (names, shas map[string]string, err error) {
	db, err := openDB(dbPath)
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,disk_name,sha256 FROM attachments`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	defer rows.Close()
	names, shas = map[string]string{}, map[string]string{}
	for rows.Next() {
		var id, disk string
		var sha sql.NullString
		if err = rows.Scan(&id, &disk, &sha); err != nil {
			return nil, nil, err
		}
		names[id], shas[id] = disk, sha.String
	}
	return names, shas, rows.Err()
}

// vaultRecords opens secrets/vault.json with the backed-up key and counts the
// records it can decrypt; it never returns a value.
func vaultRecords(dir string) int {
	key, err := os.ReadFile(filepath.Join(dir, "secrets", "key"))
	if err != nil || len(key) != 32 {
		return 0
	}
	data, err := os.ReadFile(filepath.Join(dir, "secrets", "vault.json"))
	if err != nil {
		return 0
	}
	var recs map[string]struct {
		ID         string `json:"id"`
		Ciphertext []byte `json:"ciphertext"`
	}
	if json.Unmarshal(data, &recs) != nil {
		return 0
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	n := 0
	for _, rec := range recs {
		ns := aead.NonceSize()
		if len(rec.Ciphertext) > ns {
			if _, err := aead.Open(nil, rec.Ciphertext[:ns], rec.Ciphertext[ns:], []byte(rec.ID)); err == nil {
				n++
			}
		}
	}
	return n
}

// Restore rebuilds snapshot snap into dest. With only == "" dest is a whole
// data directory (root database and every account under accounts/); otherwise
// dest is the one account's directory. Nothing in dest is trusted until every
// check passed: the caller swaps it into place afterwards.
func (r *Repo) Restore(ctx context.Context, snap, only, dest string, o RestoreOpts) (*Report, error) {
	start := time.Now()
	m, err := r.Manifest(ctx, snap)
	if err != nil {
		return nil, err
	}
	rep := &Report{Snapshot: snap, At: m.CreatedAt}
	if only != "" {
		for _, a := range m.Accounts {
			if a.ID == only {
				er, err := r.restoreEntry(ctx, snap, a, dest, o)
				rep.Entries = append(rep.Entries, er)
				rep.Elapsed = time.Since(start)
				return rep, err
			}
		}
		return nil, &Error{Code: "backup_account_not_in_snapshot", Account: only}
	}
	er, err := r.restoreEntry(ctx, snap, m.Root, dest, o)
	rep.Entries = append(rep.Entries, er)
	if err != nil {
		return rep, err
	}
	for _, a := range m.Accounts {
		er, err = r.restoreEntry(ctx, snap, a, filepath.Join(dest, "accounts", a.ID), o)
		rep.Entries = append(rep.Entries, er)
		if err != nil {
			return rep, err
		}
	}
	rep.Elapsed = time.Since(start)
	return rep, nil
}

// Verify is the drill (design 10): the newest snapshot is rebuilt in a
// scratch directory, every database checked in full, every attachment listed
// and a sample downloaded and hashed, the vault opened and counted. The
// scratch directory is always removed. It reads the bucket only.
func (r *Repo) Verify(ctx context.Context, scratch string, maxSchema, sample int) (*Report, error) {
	snaps, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	latest := ""
	for _, s := range snaps {
		if s.Committed {
			latest = s.Name
		}
	}
	if latest == "" {
		return nil, fail("backup_no_snapshot", nil)
	}
	if err = os.MkdirAll(scratch, 0o700); err != nil {
		return nil, fail("backup_no_staging_space", err)
	}
	dir, err := os.MkdirTemp(scratch, "verify-")
	if err != nil {
		return nil, fail("backup_no_staging_space", err)
	}
	defer os.RemoveAll(dir)
	m, err := r.Manifest(ctx, latest)
	if err != nil {
		return nil, err
	}
	for _, e := range append([]Entry{m.Root}, m.Accounts...) {
		key := e.ID
		if key == "root" {
			key = OwnerKey
		}
		have := map[string]bool{}
		prefix := r.Base + joinKey("files", key) + "/"
		if err = r.B.List(ctx, prefix, func(o filestore.ObjectInfo) error { have[strings.TrimPrefix(o.Key, prefix)] = true; return nil }); err != nil {
			return nil, storeErr(err)
		}
		for _, id := range e.Disk {
			if !have[id] {
				return nil, &Error{Code: "backup_object_missing", Account: e.ID}
			}
		}
	}
	rep, err := r.Restore(ctx, latest, "", dir, RestoreOpts{MaxSchema: maxSchema, Sample: sample, Full: true})
	if err != nil {
		return rep, err
	}
	sort.Slice(rep.Entries, func(i, j int) bool { return rep.Entries[i].ID < rep.Entries[j].ID })
	return rep, nil
}

// RestoreAll is the bare-host restore: it rebuilds the newest (or named)
// snapshot into dest, which must not exist, and returns the report.
func (r *Repo) RestoreAll(ctx context.Context, snap, dest string, o RestoreOpts) (*Report, error) {
	if _, err := os.Stat(dest); err == nil {
		return nil, errors.New("destination exists")
	}
	if snap == "" {
		snaps, err := r.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range snaps {
			if s.Committed {
				snap = s.Name
			}
		}
		if snap == "" {
			return nil, fail("backup_no_snapshot", nil)
		}
	}
	rep, err := r.Restore(ctx, snap, "", dest, o)
	if err != nil {
		_ = os.RemoveAll(dest)
	}
	return rep, err
}
