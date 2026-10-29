package app

// Legacy `vm:` attachments. Before the server file store (computer-decoupling
// S1) the bytes of an upload lived in the guest blob store and the row said
// `disk_name = "vm:<uuid>"`. Nothing writes that form any more. This file only
// reads those rows, copies them into the account's attachments directory, and
// deletes the guest object after the server copy is proven by sha256.
//
// The copier is idempotent and resumable by construction: the row is the only
// state. `disk_name` stays `vm:<uuid>` until the very last step, and every step
// before it can be repeated.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/google/uuid"
)

type guestBlobStorage interface {
	PutBlob(context.Context, string, []byte) error
	GetBlob(context.Context, string) ([]byte, error)
	DeleteBlob(context.Context, string) error
}

const guestAttachmentPrefix = "vm:"

// errFileOnDetachedComputer is the stable code for a `vm:` file whose bytes
// cannot be reached now: no computer is attached, or it does not answer. The
// bytes may still be on its disk; attaching the computer recovers them.
var errFileOnDetachedComputer = errors.New("file_on_detached_computer")

// errFileGoneFromComputer: the computer answered and has no such object.
var errFileGoneFromComputer = errors.New("file_missing_on_computer")

// guestSizeMismatch: the guest object does not match the recorded size. It is
// served as before but never migrated, so nothing unverified is trusted as the
// record.
type guestSizeMismatch struct {
	data []byte
	want int64
}

func (e *guestSizeMismatch) Error() string {
	return fmt.Sprintf("guest copy is %d bytes, recorded %d; not migrated", len(e.data), e.want)
}

type attachmentMemoryFile struct{ *bytes.Reader }

func (attachmentMemoryFile) Close() error { return nil }

func guestAttachmentID(path string) (string, error) {
	id := strings.TrimPrefix(path, guestAttachmentPrefix)
	parsed, err := uuid.Parse(id)
	if !strings.HasPrefix(path, guestAttachmentPrefix) || err != nil || parsed.String() != id {
		return "", errors.New("invalid guest attachment identity")
	}
	return id, nil
}

// vmCopyLocks serialises copies of one attachment inside this process.
var vmCopyLocks sync.Map

func lockVMCopy(id string) func() {
	m, _ := vmCopyLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// copyGuestAttachment moves one `vm:` attachment into the server store. It
// returns nil when the row is (now) local. On any error the row is untouched
// and still `vm:`, and the guest object is untouched.
//
// Order, each step safe to repeat after a crash:
//  1. read the guest object and check its length against the row
//  2. write it to a temp file, fsync, and hash the file from disk
//  3. compare that hash with the hash of the guest bytes; only then rename
//  4. in ONE transaction repoint the row (guarded by `AND disk_name=vm:<id>`)
//     and journal the guest object for deletion
//
// The guest object is deleted later, by cleanupDeletedAttachments, which checks
// the server file against the recorded sha256 once more.
func (s *Store) copyGuestAttachment(ctx context.Context, id string) error {
	unlock := lockVMCopy(id)
	defer unlock()
	var disk string
	var size int64
	var recorded sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT disk_name,size,sha256 FROM attachments WHERE id=?`, id).Scan(&disk, &size, &recorded)
	if err != nil {
		return err
	}
	if disk != guestAttachmentPrefix+id {
		return nil // already local, copied by someone else
	}
	if s.legacyBlobs() == nil {
		return errFileOnDetachedComputer
	}
	data, err := s.legacyBlobs().GetBlob(ctx, id)
	if err != nil {
		if errors.Is(err, computer.ErrBlobNotFound) {
			return fmt.Errorf("%w: %v", errFileGoneFromComputer, err)
		}
		return fmt.Errorf("%w: %v", errFileOnDetachedComputer, err)
	}
	if int64(len(data)) != size {
		return &guestSizeMismatch{data: data, want: size}
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	// A row that recorded a hash must still match it. A legacy row has none: its
	// bytes are only as good as the computer's word, so the server copy is an
	// unverified mirror and the guest object stays the authoritative one.
	verified := recorded.Valid && recorded.String != ""
	if verified && recorded.String != digest {
		return fmt.Errorf("attachment not migrated: the computer's copy does not match the recorded sha256")
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return err
	}
	local := id + ".upload"
	tmp := filepath.Join(root, "."+local+".tmp")
	final := filepath.Join(root, local)
	if err = writeFileSynced(tmp, data); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if got, e := hashFile(tmp); e != nil || got != digest {
		_ = os.Remove(tmp)
		if e == nil {
			e = errors.New("server copy does not match the computer copy")
		}
		return e
	}
	if err = os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	syncDir(root)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	update, args := `UPDATE attachments SET disk_name=?,sha256=? WHERE id=? AND disk_name=?`, []any{local, digest, id, guestAttachmentPrefix + id}
	if !verified {
		update, args = `UPDATE attachments SET disk_name=?,sha256=?,origin=? WHERE id=? AND disk_name=?`, []any{local, digest, attachmentOriginMirror, id, guestAttachmentPrefix + id}
	}
	res, err := tx.ExecContext(ctx, update, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 1 && verified {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO deleted_attachment_files(disk_name) VALUES(?)`, guestAttachmentPrefix+id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if n == 0 {
		// Someone else finished first (same content, same name: leave the file),
		// or the row was deleted meanwhile (the file is an orphan: remove it).
		var owned int
		if s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE disk_name=? OR id=?`, local, id).Scan(&owned) == nil && owned == 0 {
			_ = os.Remove(final)
		}
	}
	return nil
}

func writeFileSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
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

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// pendingGuestCopies counts rows still waiting for their copy. The range form
// (not LIKE) lets the unique index on disk_name answer without a table scan.
func (s *Store) pendingGuestCopies() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE disk_name>='vm:' AND disk_name<'vm;'`).Scan(&n)
	return n, err
}

// copyGuestRows runs one pass of the migration over at most `limit` rows,
// oldest first. A failure on one row is logged and does not stop the pass.
func (s *Store) copyGuestRows(ctx context.Context, limit int) (copied, failed int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM attachments WHERE disk_name LIKE 'vm:%' ORDER BY created_at,id LIMIT ?`, limit)
	if err != nil {
		return 0, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return copied, failed, ctx.Err()
		}
		cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
		e := s.copyGuestAttachment(cctx, id)
		cancel()
		if e != nil {
			failed++
			log.Printf("[files] copy of attachment %s to the server store failed (kept on the computer): %v", id, e)
			if errors.Is(e, errFileOnDetachedComputer) {
				break // the computer does not answer; the rest would fail the same way
			}
			continue
		}
		copied++
	}
	return copied, failed, nil
}

func (s *Store) openAttachment(ctx context.Context, path string) (io.ReadCloser, error) {
	if strings.HasPrefix(path, guestAttachmentPrefix) {
		id, err := guestAttachmentID(path)
		if err != nil {
			return nil, err
		}
		// Lazy copy: a file a person opens is migrated at once.
		if err = s.copyGuestAttachment(ctx, id); err != nil {
			var mismatch *guestSizeMismatch
			if errors.As(err, &mismatch) {
				log.Printf("[files] attachment %s: %v", id, err)
				return attachmentMemoryFile{bytes.NewReader(mismatch.data)}, nil
			}
			return nil, err
		}
		root, err := s.attachmentRoot()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(root, id+".upload")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(filepath.Base(path))
}

func (s *Store) removeAttachmentFile(ctx context.Context, path string) error {
	if strings.HasPrefix(path, guestAttachmentPrefix) {
		id, err := guestAttachmentID(path)
		if err != nil {
			return err
		}
		if s.legacyBlobs() == nil {
			return errors.New("guest attachment storage unavailable")
		}
		return s.legacyBlobs().DeleteBlob(ctx, id)
	}
	return os.Remove(path)
}

// verifiedServerCopy reports whether the attachment row `id`, if it still
// exists, has a server file whose sha256 equals the recorded one. A deleted row
// (the conversation went away) returns true: nothing is left to protect.
func (s *Store) verifiedServerCopy(id string) bool {
	var disk, digest sql.NullString
	var size int64
	err := s.db.QueryRow(`SELECT disk_name,sha256,size FROM attachments WHERE id=?`, id).Scan(&disk, &digest, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	if err != nil || !digest.Valid || digest.String == "" || strings.HasPrefix(disk.String, guestAttachmentPrefix) || filepath.Base(disk.String) != disk.String {
		return false
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return false
	}
	info, err := os.Stat(filepath.Join(root, disk.String))
	if err != nil || info.Size() != size {
		return false
	}
	got, err := hashFile(filepath.Join(root, disk.String))
	return err == nil && got == digest.String
}
