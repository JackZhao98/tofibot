package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
	"modernc.org/sqlite"
)

// OwnerKey is the repository name of the original owner account, whose data
// is the root database itself.
const OwnerKey = "owner"

// Entry describes one captured database: the root, or one account.
type Entry struct {
	ID         string         `json:"id"`
	CapturedAt string         `json:"captured_at"`
	DBSize     int64          `json:"db_size"`
	DBSHA      string         `json:"db_sha256"`
	Schema     int            `json:"schema"`
	Rows       map[string]int `json:"rows"`
	FilesSHA   string         `json:"files_sha256"`
	Disk       []string       `json:"disk_attachments"`
	DiskGone   int            `json:"disk_missing"` // rows whose file was already gone from disk
	Obj        int            `json:"bucket_refs"`
	VM         int            `json:"vm_refs"`
}

// Manifest is the encrypted list of one snapshot. Writing it commits the snapshot.
type Manifest struct {
	Format     int     `json:"format"`
	InstanceID string  `json:"instance_id"`
	Snapshot   string  `json:"snapshot"`
	Reason     string  `json:"reason"`
	AppVersion string  `json:"app_version"`
	Schema     int     `json:"schema"`
	CreatedAt  string  `json:"created_at"`
	Root       Entry   `json:"root"`
	Accounts   []Entry `json:"accounts"`
}

// Source says where the data is and who is writing.
type Source struct {
	DataDir    string
	InstanceID string
	AppVersion string
	Schema     int
	Staging    string // directory for the temporary copy (one database at a time)
}

var rowTables = []string{"bots", "conversations", "messages", "attachments"}

func hexSum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=query_only(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// copyDB takes an online copy through a connection of its own, in one step: a
// read snapshot that does not block writers (design 5.1).
func copyDB(ctx context.Context, srcPath, dst string) error {
	db, err := openDB(srcPath)
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		bk, ok := dc.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("sqlite driver has no online backup")
		}
		b, err := bk.NewBackup(dst)
		if err != nil {
			return err
		}
		if _, err = b.Step(-1); err != nil {
			_ = b.Finish()
			return err
		}
		return b.Finish()
	})
}

// inspect checks a database file and reads its schema version and row counts.
func inspect(path string, full bool) (schema int, rows map[string]int, err error) {
	db, err := openDB(path)
	if err != nil {
		return 0, nil, err
	}
	defer db.Close()
	check := "quick_check"
	if full {
		check = "integrity_check"
	}
	var verdict string
	if err = db.QueryRow(`PRAGMA ` + check).Scan(&verdict); err != nil || verdict != "ok" {
		return 0, nil, fail("backup_capture_corrupt", err)
	}
	if err = db.QueryRow(`PRAGMA user_version`).Scan(&schema); err != nil {
		return 0, nil, err
	}
	rows = map[string]int{}
	for _, t := range rowTables {
		var n int
		if e := db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n); e == nil {
			rows[t] = n
		} else if !strings.Contains(e.Error(), "no such table") {
			return 0, nil, e
		}
	}
	return schema, rows, nil
}

// diskAttachments lists the attachment rows whose bytes sit on this disk.
func diskAttachments(path string) (names map[string]string, obj, vm int, err error) {
	db, err := openDB(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id,disk_name FROM attachments`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return map[string]string{}, 0, 0, nil
		}
		return nil, 0, 0, err
	}
	defer rows.Close()
	names = map[string]string{}
	for rows.Next() {
		var id, disk string
		if err = rows.Scan(&id, &disk); err != nil {
			return nil, 0, 0, err
		}
		switch {
		case strings.HasPrefix(disk, "obj:"):
			obj++
		case strings.HasPrefix(disk, "vm:"):
			vm++
		default:
			names[id] = disk
		}
	}
	return names, obj, vm, rows.Err()
}

// skipTop are the top-level names of a data directory the small-files tar
// leaves out: databases and attachments have their own objects, the rest is
// scratch or lives elsewhere (design 4.1).
var skipTop = map[string]bool{"tofi.db": true, "tofi.db-wal": true, "tofi.db-shm": true, "attachments": true, "accounts": true,
	"tmp": true, ".spool": true, "backups": true, "backup": true, "host-control.token": true}

func tarDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		top := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
		if skipTop[top] || strings.HasPrefix(top, ".restore-") || strings.HasPrefix(top, ".before-restore-") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return nil
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err = tw.WriteHeader(h); err != nil || info.IsDir() {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err == nil {
		err = tw.Close()
	}
	return buf.Bytes(), err
}

func untar(data []byte, dir string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fail("backup_object_damaged", err)
		}
		name := filepath.FromSlash(h.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(filepath.Clean(name), "..") {
			return fail("backup_object_damaged", nil)
		}
		target := filepath.Join(dir, name)
		if h.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err = writeFile(target, tr, 0o600); err != nil {
			return err
		}
	}
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if ce := f.Close(); err == nil {
		err = ce
	}
	return err
}

func (s Source) dirOf(id string) string {
	if id == "root" {
		return s.DataDir
	}
	return filepath.Join(s.DataDir, "accounts", id)
}

// accountIDs reads the accounts to capture from the root copy: every account
// with its own database directory, but not the original owner (whose data is
// the root database) and not one being deleted.
func accountIDs(rootCopy string) ([]string, error) {
	db, err := openDB(rootCopy)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id FROM accounts WHERE legacy=0 AND deleting=0 ORDER BY id`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// capture copies, checks, encrypts and uploads one database, its small files
// and (once each) its disk attachments.
func (r *Repo) capture(ctx context.Context, src Source, snap, id string, now time.Time, accounts *[]string) (Entry, error) {
	rel := "root"
	attKey := OwnerKey
	if id != "root" {
		rel, attKey = joinKey("accounts", id), id
	}
	e := Entry{ID: id, CapturedAt: now.UTC().Format(time.RFC3339)}
	dir := src.dirOf(id)
	live := filepath.Join(dir, "tofi.db")
	if _, err := os.Stat(live); err != nil {
		return e, err
	}
	tmp, err := os.MkdirTemp(src.Staging, "db-")
	if err != nil {
		return e, fail("backup_no_staging_space", err)
	}
	defer os.RemoveAll(tmp)
	copyPath := filepath.Join(tmp, "tofi.db")
	if err = copyDB(ctx, live, copyPath); err != nil {
		var ee *Error
		if errors.As(err, &ee) {
			return e, err
		}
		return e, &Error{Code: "backup_capture_corrupt", Account: id, Cause: err}
	}
	info, err := os.Stat(copyPath)
	if err != nil {
		return e, err
	}
	if info.Size() > MaxDBBytes {
		return e, &Error{Code: "backup_db_too_large", Account: id}
	}
	if e.Schema, e.Rows, err = inspect(copyPath, false); err != nil {
		var ee *Error
		if errors.As(err, &ee) {
			ee.Account = id
		}
		return e, err
	}
	plain, err := os.ReadFile(copyPath)
	if err != nil {
		return e, err
	}
	e.DBSize, e.DBSHA = int64(len(plain)), hexSum(plain)
	packed, err := gz(plain)
	if err != nil {
		return e, err
	}
	plain = nil
	if err = r.put(ctx, joinKey("snapshots", snap, rel, "db"), packed); err != nil {
		return e, err
	}
	// Small files go after the database, so a key deleted in between only
	// shows as a connection that needs reconnecting.
	files, err := tarDir(dir)
	if err != nil {
		return e, err
	}
	e.FilesSHA = hexSum(files)
	if files, err = gz(files); err != nil {
		return e, err
	}
	if err = r.put(ctx, joinKey("snapshots", snap, rel, "files"), files); err != nil {
		return e, err
	}
	if accounts != nil {
		if *accounts, err = accountIDs(copyPath); err != nil {
			return e, err
		}
	}
	names, obj, vm, err := diskAttachments(copyPath)
	if err != nil {
		return e, err
	}
	e.Obj, e.VM = obj, vm
	have := map[string]bool{}
	prefix := r.Base + joinKey("files", attKey) + "/"
	if err = r.B.List(ctx, prefix, func(o filestore.ObjectInfo) error { have[strings.TrimPrefix(o.Key, prefix)] = true; return nil }); err != nil {
		return e, storeErr(err)
	}
	ids := make([]string, 0, len(names))
	for aid := range names {
		ids = append(ids, aid)
	}
	sort.Strings(ids)
	for _, aid := range ids {
		if !have[aid] {
			raw, rerr := os.ReadFile(filepath.Join(dir, "attachments", filepath.Base(names[aid])))
			if errors.Is(rerr, os.ErrNotExist) {
				e.DiskGone++
				continue
			}
			if rerr != nil {
				return e, rerr
			}
			if perr := r.putOnce(ctx, joinKey("files", attKey, aid), raw); perr != nil {
				return e, perr
			}
		}
		e.Disk = append(e.Disk, aid)
	}
	return e, nil
}

// putOnce uploads an attachment that never changes; an object that is already
// there is fine.
func (r *Repo) putOnce(ctx context.Context, path string, plain []byte) error {
	err := r.put(ctx, path, plain)
	if errors.Is(err, filestore.ErrExists) {
		return nil
	}
	return err
}

type lineage struct {
	WriterSeq int64 `json:"writer_seq"`
}

func statePath(dataDir string) string { return filepath.Join(dataDir, "backup", "state.json") }

func readState(dataDir string) lineage {
	var l lineage
	if b, err := os.ReadFile(statePath(dataDir)); err == nil {
		_ = json.Unmarshal(b, &l)
	}
	return l
}

// WriteState records the writer sequence this server last wrote (used by the
// restore tool after a takeover).
func WriteState(dataDir string, seq int64) error {
	if err := os.MkdirAll(filepath.Dir(statePath(dataDir)), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(lineage{WriterSeq: seq})
	return os.WriteFile(statePath(dataDir), b, 0o600)
}

// SnapshotName is the directory name of a snapshot.
func SnapshotName(t time.Time, reason string) string {
	return t.UTC().Format("20060102T150405Z") + "-" + reason
}

// Run takes one snapshot: every database, the small files, the disk
// attachments not yet in the bucket, then the manifest (the commit) and the
// lineage record. Any failure leaves no manifest, so nothing half-written is
// ever a snapshot.
func (r *Repo) Run(ctx context.Context, src Source, reason string, now time.Time) (*Manifest, error) {
	head, etag, found, err := r.readHead(ctx)
	if err != nil {
		return nil, err
	}
	if head.WriterSeq != readState(src.DataDir).WriterSeq {
		return nil, fail("backup_repo_diverged", nil)
	}
	if err = os.MkdirAll(src.Staging, 0o700); err != nil {
		return nil, fail("backup_no_staging_space", err)
	}
	snap := SnapshotName(now, reason)
	m := &Manifest{Format: FormatVersion, InstanceID: src.InstanceID, Snapshot: snap, Reason: reason, AppVersion: src.AppVersion, Schema: src.Schema, CreatedAt: now.UTC().Format(time.RFC3339)}
	var ids []string
	if m.Root, err = r.capture(ctx, src, snap, "root", now, &ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, serr := os.Stat(filepath.Join(src.dirOf(id), "tofi.db")); errors.Is(serr, os.ErrNotExist) {
			continue // the account never opened its workspace
		}
		e, cerr := r.capture(ctx, src, snap, id, now, nil)
		if cerr != nil {
			var be *Error
			if errors.As(cerr, &be) && be.Account == "" {
				be.Account = id
			}
			return nil, cerr
		}
		m.Accounts = append(m.Accounts, e)
	}
	raw, _ := json.Marshal(m)
	if err = r.put(ctx, joinKey("snapshots", snap, "manifest"), raw); err != nil {
		return nil, err
	}
	next := Head{WriterSeq: head.WriterSeq + 1, Latest: snap, At: m.CreatedAt}
	if err = r.writeHead(ctx, next, etag, found); err != nil {
		return nil, err
	}
	return m, WriteState(src.DataDir, next.WriterSeq)
}

// Snapshot is one directory under snapshots/.
type Snapshot struct {
	Name      string
	Time      time.Time
	Reason    string
	Committed bool
}

// List returns every snapshot directory, oldest first; Committed is false for
// one with no manifest.
func (r *Repo) List(ctx context.Context) ([]Snapshot, error) {
	prefix := r.Base + "snapshots/"
	seen := map[string]*Snapshot{}
	err := r.B.List(ctx, prefix, func(o filestore.ObjectInfo) error {
		name, rest, _ := strings.Cut(strings.TrimPrefix(o.Key, prefix), "/")
		s := seen[name]
		if s == nil {
			s = &Snapshot{Name: name}
			if t, perr := time.Parse("20060102T150405Z", strings.SplitN(name, "-", 2)[0]); perr == nil {
				s.Time = t
			}
			s.Reason = name[strings.LastIndex(name, "-")+1:]
			seen[name] = s
		}
		if rest == "manifest" {
			s.Committed = true
		}
		return nil
	})
	if err != nil {
		return nil, storeErr(err)
	}
	out := make([]Snapshot, 0, len(seen))
	for _, s := range seen {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Manifest reads and decrypts one snapshot's manifest.
func (r *Repo) Manifest(ctx context.Context, snap string) (*Manifest, error) {
	raw, err := r.get(ctx, joinKey("snapshots", snap, "manifest"))
	if err != nil {
		if CodeOf(err) == "backup_object_missing" {
			return nil, fail("backup_snapshot_missing", err)
		}
		return nil, err
	}
	var m Manifest
	if json.Unmarshal(raw, &m) != nil || m.Format != FormatVersion {
		return nil, fail("backup_object_damaged", nil)
	}
	return &m, nil
}

// Retention is how many snapshots GFS keeps.
type Retention struct{ Daily, Weekly, Monthly int }

// Select is the pure retention rule: the newest snapshot of each of the last
// Daily days, Weekly ISO weeks and Monthly months that have one, the newest
// three each of manual and pre-update, and always the newest of all.
func Select(snaps []Snapshot, ret Retention) map[string]bool {
	keep := map[string]bool{}
	s := append([]Snapshot(nil), snaps...)
	sort.Slice(s, func(i, j int) bool { return s[i].Name > s[j].Name }) // newest first
	if len(s) > 0 {
		keep[s[0].Name] = true
	}
	bucket := func(n int, key func(time.Time) string) {
		seen := map[string]bool{}
		for _, x := range s {
			k := key(x.Time)
			if !seen[k] {
				if len(seen) >= n {
					return
				}
				seen[k] = true
				keep[x.Name] = true
			}
		}
	}
	bucket(ret.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	bucket(ret.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-%d", y, w) })
	bucket(ret.Monthly, func(t time.Time) string { return t.Format("2006-01") })
	count := map[string]int{}
	for _, x := range s {
		if (x.Reason == "manual" || x.Reason == "pre-update") && count[x.Reason] < 3 {
			count[x.Reason]++
			keep[x.Name] = true
		}
	}
	return keep
}

// Prune deletes the snapshots Select does not keep (manifest first, so they
// stop being snapshots at once), every directory with no manifest, and then
// the disk attachments no kept snapshot lists. It returns how many snapshot
// directories went. Any doubt about a manifest stops the attachment pass.
func (r *Repo) Prune(ctx context.Context, ret Retention) (int, error) {
	snaps, err := r.List(ctx)
	if err != nil {
		return 0, err
	}
	var committed []Snapshot
	for _, s := range snaps {
		if s.Committed {
			committed = append(committed, s)
		}
	}
	keep := Select(committed, ret)
	removed := 0
	for _, s := range snaps {
		if keep[s.Name] {
			continue
		}
		dir := r.Base + joinKey("snapshots", s.Name) + "/"
		if s.Committed {
			if err = r.B.Delete(ctx, dir+"manifest"); err != nil {
				return removed, storeErr(err)
			}
		}
		var keys []string
		if err = r.B.List(ctx, dir, func(o filestore.ObjectInfo) error { keys = append(keys, o.Key); return nil }); err != nil {
			return removed, storeErr(err)
		}
		for _, k := range keys {
			if err = r.B.Delete(ctx, k); err != nil {
				return removed, storeErr(err)
			}
		}
		removed++
	}
	used := map[string]bool{}
	for _, s := range committed {
		if !keep[s.Name] {
			continue
		}
		m, merr := r.Manifest(ctx, s.Name)
		if merr != nil {
			return removed, merr
		}
		add := func(e Entry) {
			key := e.ID
			if e.ID == "root" {
				key = OwnerKey
			}
			for _, id := range e.Disk {
				used[joinKey("files", key, id)] = true
			}
		}
		add(m.Root)
		for _, a := range m.Accounts {
			add(a)
		}
	}
	prefix := r.Base + "files/"
	var stale []string
	if err = r.B.List(ctx, prefix, func(o filestore.ObjectInfo) error {
		if !used[strings.TrimPrefix(o.Key, r.Base)] {
			stale = append(stale, o.Key)
		}
		return nil
	}); err != nil {
		return removed, storeErr(err)
	}
	for _, k := range stale {
		if err = r.B.Delete(ctx, k); err != nil {
			return removed, storeErr(err)
		}
	}
	return removed, nil
}
