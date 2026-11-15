package app

// Moving existing files between this server's disk and the bucket, one file at
// a time (design docs/design/attachment-object-storage.md section 6). Every step
// is repeatable: the row is the only state, so a process that died mid-file
// leaves either the old place or the new one, never a half switch. Order of one
// file: read the source, check size and sha256, write the target (write-once),
// read the target back and check again, switch the row in one transaction (the
// source goes into the deletion journal in the same transaction), then remove
// the source. A bucket source waits out the deletion grace.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
)

const (
	moveToBucket = "to_bucket"
	moveToDisk   = "to_disk"

	codeMoveMissingOnDisk  = "file_missing_on_disk"
	codeMoveSizeMismatch   = "file_size_mismatch"
	codeMoveDigestMismatch = "file_digest_mismatch"
	codeMoveVerifyFailed   = "file_verify_failed"
	codeMoveKeyConflict    = "file_key_conflict"
	codeMoveFailed         = "file_move_failed"
)

// moveCodeErr is a per-file failure with a named code: the job records it and goes on.
type moveCodeErr string

func (e moveCodeErr) Error() string { return string(e) }

// errMoveDiskFull pauses a bucket-to-disk job: the next file does not fit under the host floor.
var errMoveDiskFull = errors.New("not enough free disk space")

// Row sets, by place. A `vm:` row is copied by the legacy copier, not moved; an expired row has no bytes.
const (
	moveObjRows  = `(disk_name>='obj:' AND disk_name<'obj;' AND expired_at IS NULL)`
	moveDiskRows = `(NOT (disk_name>='obj:' AND disk_name<'obj;') AND disk_name NOT LIKE 'vm:%' AND expired_at IS NULL)`
)

func moveSourcePredicate(dir string) string {
	if dir == moveToDisk {
		return moveObjRows
	}
	return moveDiskRows
}

type moveRow struct {
	id, disk, digest, origin, mime string
	size                           int64
}

// moveResult is one step of a job. Found is false when no row is left after `after`.
type moveResult struct {
	Found bool
	ID    string
	Size  int64
	Kind  string
	// Code names a per-file failure; empty when the file moved (or the row changed under us: Skipped).
	Code    string
	Skipped bool
}

// moveCount is what a direction has left to move.
func (s *Store) moveCount(dir string) (files, bytes int64, err error) {
	err = s.db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(size),0) FROM attachments WHERE `+moveSourcePredicate(dir)).Scan(&files, &bytes)
	return
}

// moveNextFile moves the first row after `after`. An error means the job must stop (cancelled,
// bucket down or refusing, disk full, or the row query failed); a failure of one file is in Code.
func (s *Store) moveNextFile(ctx context.Context, dir, after string) (moveResult, error) {
	var r moveRow
	err := s.db.QueryRowContext(ctx, `SELECT id,disk_name,size,COALESCE(sha256,''),origin,mime FROM attachments WHERE id>? AND `+moveSourcePredicate(dir)+` ORDER BY id LIMIT 1`, after).
		Scan(&r.id, &r.disk, &r.size, &r.digest, &r.origin, &r.mime)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return moveResult{}, nil
		}
		return moveResult{}, err
	}
	b, err := s.fileBackend(ctx)
	if err != nil {
		return moveResult{}, err
	}
	if b.Store == nil {
		return moveResult{}, newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
	}
	var switched bool
	if dir == moveToDisk {
		switched, err = s.moveToDisk(ctx, b, r)
	} else {
		switched, err = s.moveToBucket(ctx, b, r)
	}
	res := moveResult{Found: true, ID: r.id, Size: r.size, Kind: attachmentKind(r.origin), Skipped: err == nil && !switched}
	if err == nil {
		return res, nil
	}
	var coded moveCodeErr
	var fe *fileStoreError
	switch {
	case errors.As(err, &coded):
		res.Code = string(coded)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, errMoveDiskFull):
		return res, err
	case errors.As(err, &fe) && (fe.code == codeFileMissingInStore || fe.code == codeFileStoreOutOfScope):
		res.Code = fe.code
	case errors.As(err, &fe):
		return res, err // bucket down or refusing: the job pauses, no file is blamed
	default:
		res.Code = codeMoveFailed
	}
	return res, nil
}

func hashReader(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// hashObject reads a bucket object back and returns its sha256 and length.
func hashObject(ctx context.Context, st filestore.Store, key string) (string, int64, error) {
	f, _, err := st.Open(ctx, key)
	if err != nil {
		return "", 0, fileStoreFailure(err)
	}
	defer f.Close()
	sum, n, err := hashReader(f)
	if err != nil {
		return "", 0, fileStoreFailure(err)
	}
	return sum, n, nil
}

func (s *Store) moveToBucket(ctx context.Context, b FileBackend, r moveRow) (bool, error) {
	root, err := s.attachmentRoot()
	if err != nil {
		return false, err
	}
	if filepath.Base(r.disk) != r.disk {
		return false, moveCodeErr(codeMoveMissingOnDisk)
	}
	src, _, err := filestore.Disk{Root: root}.Open(ctx, r.disk)
	if err != nil {
		if errors.Is(err, filestore.ErrNotFound) {
			return false, moveCodeErr(codeMoveMissingOnDisk)
		}
		return false, err
	}
	defer src.Close()
	sum, n, err := hashReader(src)
	if err != nil {
		return false, err
	}
	if n != r.size {
		return false, moveCodeErr(codeMoveSizeMismatch)
	}
	if r.digest != "" && r.digest != sum {
		return false, moveCodeErr(codeMoveDigestMismatch)
	}
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	key := b.objectKey(r.origin, r.id)
	created := true
	if err = b.Store.Put(ctx, key, src, filestore.ObjectMeta{Size: n, SHA256: sum, SafeMIME: r.mime}); errors.Is(err, filestore.ErrExists) {
		created, err = false, nil // an earlier attempt wrote it; the read-back decides
	}
	if err != nil {
		return false, fileStoreFailure(err)
	}
	drop := func() {
		if created {
			_ = b.Store.Delete(context.WithoutCancel(ctx), key)
		}
	}
	got, gotSize, err := hashObject(ctx, b.Store, key)
	if err != nil {
		return false, err
	}
	if got != sum || gotSize != n {
		drop()
		if created {
			return false, moveCodeErr(codeMoveVerifyFailed)
		}
		return false, moveCodeErr(codeMoveKeyConflict)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		drop()
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE attachments SET disk_name=?,store=?,sha256=CASE WHEN COALESCE(sha256,'')='' THEN ? ELSE sha256 END WHERE id=? AND disk_name=?`, objPrefix+key, b.Gen, sum, r.id, r.disk)
	if err != nil {
		drop()
		return false, err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		drop() // the row was deleted or changed meanwhile
		return false, nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_attachment_files(disk_name) VALUES(?)`, r.disk); err != nil {
		drop()
		return false, err
	}
	if err = tx.Commit(); err != nil {
		drop()
		return false, err
	}
	// The row now points at the bucket. The source goes at once; a failure leaves the journal entry for the cleanup pass.
	if rmErr := os.Remove(filepath.Join(root, r.disk)); rmErr == nil || errors.Is(rmErr, os.ErrNotExist) {
		_, _ = s.db.Exec(`DELETE FROM deleted_attachment_files WHERE disk_name=?`, r.disk)
	}
	return true, nil
}

func (s *Store) moveToDisk(ctx context.Context, b FileBackend, r moveRow) (bool, error) {
	key := strings.TrimPrefix(r.disk, objPrefix)
	st, err := s.objStore(ctx, key)
	if err != nil {
		return false, err
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return false, err
	}
	if floor := s.storage.floor(); floor > 0 {
		free, _, perr := s.storage.probe(root)
		if perr != nil || free < uint64(floor)+uint64(r.size) {
			return false, errMoveDiskFull
		}
	}
	rd, _, err := st.Open(ctx, key)
	if err != nil {
		return false, fileStoreFailure(err)
	}
	defer rd.Close()
	spoolDir := filepath.Join(root, spoolDirName)
	if err = os.MkdirAll(spoolDir, 0700); err != nil {
		return false, err
	}
	spoolPath := filepath.Join(spoolDir, "move-"+r.id)
	f, err := os.OpenFile(spoolPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return false, err
	}
	defer func() { f.Close(); _ = os.Remove(spoolPath) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), rd)
	if err != nil {
		return false, fileStoreFailure(err)
	}
	if err = f.Sync(); err != nil {
		return false, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if n != r.size {
		return false, moveCodeErr(codeMoveSizeMismatch)
	}
	if r.digest != "" && r.digest != sum {
		return false, moveCodeErr(codeMoveDigestMismatch)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	name := r.id + ".upload"
	created := true
	if err = (filestore.Disk{Root: root}).Put(ctx, name, f, filestore.ObjectMeta{Size: n, SHA256: sum}); errors.Is(err, filestore.ErrExists) {
		created, err = false, nil
	}
	if err != nil {
		return false, err
	}
	drop := func() {
		if created {
			_ = os.Remove(filepath.Join(root, name))
		}
	}
	got, err := hashFile(filepath.Join(root, name))
	if err != nil {
		drop()
		return false, err
	}
	if got != sum {
		drop()
		if created {
			return false, moveCodeErr(codeMoveVerifyFailed)
		}
		return false, moveCodeErr(codeMoveKeyConflict)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		drop()
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE attachments SET disk_name=?,store='',sha256=CASE WHEN COALESCE(sha256,'')='' THEN ? ELSE sha256 END WHERE id=? AND disk_name=?`, name, sum, r.id, r.disk)
	if err != nil {
		drop()
		return false, err
	}
	if changed, _ := res.RowsAffected(); changed == 0 {
		drop()
		return false, nil
	}
	// The object stays for the deletion grace, so a restored older backup still finds it.
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_attachment_files(disk_name,delete_after) VALUES(?,?)`, r.disk, s.deleteDeadline(ctx)); err != nil {
		drop()
		return false, err
	}
	if err = tx.Commit(); err != nil {
		drop()
		return false, err
	}
	return true, nil
}

// ---- reconciliation and orphan sweep ----

const reportItemCap = 20

// reportItem is one line of the short list. It carries no file name.
type reportItem struct {
	Account string `json:"account,omitempty"`
	Issue   string `json:"issue"`
	Kind    string `json:"kind,omitempty"`
	Size    int64  `json:"size"`
	AgeDays int    `json:"age_days,omitempty"`
}

// fileScan is the counts of one account's reconciliation (or of the whole server, summed).
type fileScan struct {
	Rows           int64        `json:"rows"`
	MissingInStore int64        `json:"missing_in_store"`
	MissingOnDisk  int64        `json:"missing_on_disk"`
	SizeMismatch   int64        `json:"size_mismatch"`
	ShaMismatch    int64        `json:"sha_mismatch"`
	Orphans        int64        `json:"orphans"`
	OrphanBytes    int64        `json:"orphan_bytes"`
	OrphansYoung   int64        `json:"orphans_young"` // no row yet but younger than the grace: a write may be in flight
	Deleted        int64        `json:"deleted"`
	DeleteFailed   int64        `json:"delete_failed"`
	Items          []reportItem `json:"items"`
}

func (a *fileScan) add(o fileScan, account string) {
	a.Rows += o.Rows
	a.MissingInStore += o.MissingInStore
	a.MissingOnDisk += o.MissingOnDisk
	a.SizeMismatch += o.SizeMismatch
	a.ShaMismatch += o.ShaMismatch
	a.Orphans += o.Orphans
	a.OrphanBytes += o.OrphanBytes
	a.OrphansYoung += o.OrphansYoung
	a.Deleted += o.Deleted
	a.DeleteFailed += o.DeleteFailed
	for _, it := range o.Items {
		if len(a.Items) >= reportItemCap {
			break
		}
		it.Account = account
		a.Items = append(a.Items, it)
	}
}

type scanOpts struct {
	Deep   bool // read every file and compare sha256
	Sweep  bool // delete eligible orphans
	DryRun bool
	Now    time.Time
}

// scanFiles compares this workspace's rows with its disk and its bucket prefix. A bucket
// object counts as an orphan when no row and no deletion-journal entry names it; it is
// swept only once older than the deletion grace, so an upload whose row is not yet
// inserted is safe.
func (s *Store) scanFiles(ctx context.Context, o scanOpts) (fileScan, error) {
	var out fileScan
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	add := func(it reportItem) {
		if len(out.Items) < reportItemCap {
			out.Items = append(out.Items, it)
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT disk_name,size,COALESCE(sha256,''),origin FROM attachments WHERE expired_at IS NULL AND disk_name NOT LIKE 'vm:%'`)
	if err != nil {
		return out, err
	}
	type fileRow struct {
		name, digest, origin string
		size                 int64
	}
	objRows := map[string]fileRow{}
	var diskRows []fileRow
	for rows.Next() {
		var r fileRow
		if err = rows.Scan(&r.name, &r.size, &r.digest, &r.origin); err != nil {
			rows.Close()
			return out, err
		}
		out.Rows++
		if key, ok := strings.CutPrefix(r.name, objPrefix); ok {
			objRows[key] = r
		} else {
			diskRows = append(diskRows, r)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(diskRows) > 0 {
		root, err := s.attachmentRoot()
		if err != nil {
			return out, err
		}
		for _, d := range diskRows {
			if err = ctx.Err(); err != nil {
				return out, err
			}
			path := filepath.Join(root, filepath.Base(d.name))
			info, serr := os.Lstat(path)
			switch {
			case serr != nil:
				out.MissingOnDisk++
				add(reportItem{Issue: "missing_on_disk", Kind: attachmentKind(d.origin), Size: d.size})
			case info.Size() != d.size:
				out.SizeMismatch++
				add(reportItem{Issue: "size_mismatch", Kind: attachmentKind(d.origin), Size: d.size})
			case o.Deep && d.digest != "":
				if got, herr := hashFile(path); herr == nil && got != d.digest {
					out.ShaMismatch++
					add(reportItem{Issue: "sha_mismatch", Kind: attachmentKind(d.origin), Size: d.size})
				}
			}
		}
	}
	b, err := s.fileBackend(ctx)
	if err != nil {
		return out, err
	}
	if b.Store == nil {
		if len(objRows) > 0 {
			return out, newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
		}
		return out, nil
	}
	grace := b.GraceDays
	if grace <= 0 {
		grace = defaultDeleteGraceDay
	}
	cutoff := o.Now.Add(-time.Duration(grace) * 24 * time.Hour)
	scope := "/accounts/" + b.Account + "/"
	seen := map[string]bool{}
	var doomed []filestore.ObjectInfo
	err = b.Store.List(ctx, b.Prefix, func(obj filestore.ObjectInfo) error {
		if !strings.Contains(obj.Key, scope) {
			return nil
		}
		if row, ok := objRows[obj.Key]; ok {
			seen[obj.Key] = true
			switch {
			case row.size != obj.Size:
				out.SizeMismatch++
				add(reportItem{Issue: "size_mismatch", Kind: attachmentKind(row.origin), Size: row.size})
			case o.Deep && row.digest != "":
				got, _, herr := hashObject(ctx, b.Store, obj.Key)
				if herr != nil && ctx.Err() != nil {
					return ctx.Err()
				}
				if herr == nil && got != row.digest {
					out.ShaMismatch++
					add(reportItem{Issue: "sha_mismatch", Kind: attachmentKind(row.origin), Size: row.size})
				}
			}
			return nil
		}
		var journaled int
		if s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deleted_attachment_files WHERE disk_name=?`, objPrefix+obj.Key).Scan(&journaled) == nil && journaled > 0 {
			return nil // already queued for removal
		}
		if obj.Modified.IsZero() || obj.Modified.After(cutoff) {
			out.OrphansYoung++
			return nil
		}
		out.Orphans++
		out.OrphanBytes += obj.Size
		add(reportItem{Issue: "orphan", Kind: objectKeyKind(obj.Key, b.Prefix), Size: obj.Size, AgeDays: int(o.Now.Sub(obj.Modified).Hours() / 24)})
		doomed = append(doomed, obj)
		return nil
	})
	if err != nil {
		return out, fileStoreFailure(err)
	}
	for key, r := range objRows {
		if !seen[key] && strings.Contains(key, scope) {
			out.MissingInStore++
			add(reportItem{Issue: "missing_in_store", Kind: attachmentKind(r.origin), Size: r.size})
		}
	}
	if !o.Sweep || o.DryRun {
		return out, nil
	}
	for _, obj := range doomed {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		// Check again right before the delete: a row or journal entry may have appeared since the listing.
		var n int
		if s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM attachments WHERE disk_name=?)+(SELECT COUNT(*) FROM deleted_attachment_files WHERE disk_name=?)`, objPrefix+obj.Key, objPrefix+obj.Key).Scan(&n) != nil || n > 0 {
			continue
		}
		if derr := b.Store.Delete(ctx, obj.Key); derr != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.DeleteFailed++
			continue
		}
		out.Deleted++
	}
	return out, nil
}

// objectKeyKind is the kind segment of a key under the prefix, never user text.
func objectKeyKind(key, prefix string) string {
	rest := strings.TrimPrefix(key, prefix)
	if kind, _, ok := strings.Cut(rest, "/"); ok {
		return kind
	}
	return ""
}
