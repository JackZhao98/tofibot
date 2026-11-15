package app

// The file store seam (design docs/design/attachment-object-storage.md). A
// workspace never holds a bucket credential: the gateway hands it a
// FileBackend, which is the store handle and the layout, nothing else. With no
// backend, or kind "disk", new files go to the account's attachments directory
// exactly as before. A row whose disk_name is "obj:<key>" lives in the bucket
// and is read only from there; any other row never touches the bucket. A bucket
// failure is a named error. It never becomes a disk write.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
)

const (
	objPrefix             = "obj:"
	fileStoreKindDisk     = "disk"
	fileStoreKindS3       = "s3"
	defaultDeleteGraceDay = 14
	fileStoreLayoutV1     = "v1"
	firstFileStoreGen     = "g1"
)

// FileBackend is what the gateway resolves for one workspace.
type FileBackend struct {
	// Kind is where NEW files go: "disk" or "s3".
	Kind string
	// Store reads and writes bucket objects; nil while no bucket is configured.
	Store filestore.Store
	// Gen is the configuration generation, recorded in attachments.store.
	Gen string
	// Prefix is "<prefix>v1/<instance_id>/accounts/<account>/" (ends with "/").
	Prefix string
	// Account is the account segment of Prefix, used to keep a key in its own account.
	Account string
	// GraceDays is how long a deleted bucket object stays before it is removed.
	GraceDays int
	// Old opens the store of a previous generation (read, delete and move source only).
	Old func(gen string) (filestore.Store, error)
}

// fileStoreError carries one of the named codes. Its text is the code alone.
type fileStoreError struct {
	code   string
	status int
	cause  error
}

func (e *fileStoreError) Error() string { return e.code }
func (e *fileStoreError) Unwrap() error { return e.cause }

const (
	codeFileStoreUnavailable   = "file_store_unavailable"
	codeFileStoreMisconfigured = "file_store_misconfigured"
	codeFileMissingInStore     = "file_missing_in_store"
	codeFileStoreOutOfScope    = "file_store_key_out_of_scope"
)

func newFileStoreError(code string, cause error) *fileStoreError {
	status := 503
	switch code {
	case codeFileMissingInStore:
		status = 404
	case codeFileStoreOutOfScope:
		status = 500
	}
	return &fileStoreError{code: code, status: status, cause: cause}
}

// fileStoreFailure turns a filestore error into a named one.
func fileStoreFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, filestore.ErrNotFound):
		return newFileStoreError(codeFileMissingInStore, err)
	case errors.Is(err, filestore.ErrDenied):
		return newFileStoreError(codeFileStoreMisconfigured, err)
	case errors.Is(err, context.Canceled):
		return err
	}
	return newFileStoreError(codeFileStoreUnavailable, err)
}

func asFileStoreError(err error) (*fileStoreError, bool) {
	var fe *fileStoreError
	return fe, errors.As(err, &fe)
}

// fileBackend resolves the backend; no resolver means disk.
func (s *Store) fileBackend(ctx context.Context) (FileBackend, error) {
	if s.files == nil {
		return FileBackend{Kind: fileStoreKindDisk}, nil
	}
	b, err := s.files(ctx)
	if err != nil {
		return FileBackend{}, newFileStoreError(codeFileStoreMisconfigured, err)
	}
	return b, nil
}

// objectRef splits an `obj:` disk name into its generation and key. Generation
// g1 is written without a marker (`obj:<key>`); a later one is `obj:<gen>|<key>`
// ('|' is never in a key), so a row names the bucket it lives in and the same
// key in two buckets is two different journal entries.
func objectRef(disk string) (gen, key string) {
	rest := strings.TrimPrefix(disk, objPrefix)
	if g, k, ok := strings.Cut(rest, "|"); ok {
		return g, k
	}
	return firstFileStoreGen, rest
}

// objectDisk is the disk name of a key in a generation.
func objectDisk(gen, key string) string {
	if gen == "" || gen == firstFileStoreGen {
		return objPrefix + key
	}
	return objPrefix + gen + "|" + key
}

// objStore is the bucket store the row (disk name `obj:...`) lives in. A row is
// read only from its own generation, never from the current one by default.
func (s *Store) objStore(ctx context.Context, disk string) (filestore.Store, error) {
	b, err := s.fileBackend(ctx)
	if err != nil {
		return nil, err
	}
	gen, key := objectRef(disk)
	if b.Account == "" || !strings.Contains(key, "/accounts/"+b.Account+"/") {
		return nil, newFileStoreError(codeFileStoreOutOfScope, errors.New("key outside the account"))
	}
	if b.Store != nil && gen == b.Gen {
		return b.Store, nil
	}
	if b.Old == nil {
		return nil, newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
	}
	st, err := b.Old(gen)
	if err != nil || st == nil {
		return nil, newFileStoreError(codeFileStoreMisconfigured, errors.New("bucket generation not available"))
	}
	return st, nil
}

// attachmentKind groups the origins into the key kinds (artifacts, files, traces, imports, uploads).
func attachmentKind(origin string) string {
	switch origin {
	case attachmentOriginPublished, attachmentOriginGenerated, attachmentOriginOutbox:
		return "artifacts"
	case attachmentOriginBotFile:
		return "files"
	case attachmentOriginTrace:
		return "traces"
	case attachmentOriginImport:
		return "imports"
	}
	return "uploads"
}

// objectKey is the key of a new bucket object. Nothing a person typed is in it.
func (b FileBackend) objectKey(origin, id string) string {
	return b.Prefix + attachmentKind(origin) + "/" + id
}

// FileStoreKeyPrefix builds the per-account prefix.
func FileStoreKeyPrefix(prefix, instanceID, account string) string {
	return prefix + fileStoreLayoutV1 + "/" + instanceID + "/accounts/" + account + "/"
}

// deleteDeadline is when a bucket object that was just unlinked may go.
func (s *Store) deleteDeadline(ctx context.Context) string {
	days := defaultDeleteGraceDay
	if b, err := s.fileBackend(ctx); err == nil && b.GraceDays > 0 {
		days = b.GraceDays
	}
	return time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
}

// objectRows counts the rows that live in the bucket.
func (s *Store) objectRows() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE disk_name>='obj:' AND disk_name<'obj;'`).Scan(&n)
	return n, err
}

// objectUsage counts, per generation, the rows that live in a bucket and the
// journal entries still waiting to remove an object from it.
type genUsage struct{ Files, Bytes, Pending int64 }

const genOfDisk = `CASE WHEN instr(disk_name,'|')>0 THEN substr(disk_name,5,instr(disk_name,'|')-5) ELSE 'g1' END`

func (s *Store) objectUsageByGen() (map[string]genUsage, error) {
	out := map[string]genUsage{}
	rows, err := s.db.Query(`SELECT ` + genOfDisk + `,COUNT(*),COALESCE(SUM(size),0) FROM attachments WHERE disk_name>='obj:' AND disk_name<'obj;' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var g string
		var u genUsage
		if err = rows.Scan(&g, &u.Files, &u.Bytes); err != nil {
			rows.Close()
			return nil, err
		}
		out[g] = u
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT ` + genOfDisk + `,COUNT(*) FROM deleted_attachment_files WHERE disk_name>='obj:' AND disk_name<'obj;' GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var g string
		var n int64
		if err = rows.Scan(&g, &n); err != nil {
			return nil, err
		}
		u := out[g]
		u.Pending = n
		out[g] = u
	}
	return out, rows.Err()
}

func (b FileBackend) String() string { return fmt.Sprintf("file backend %s", b.Kind) }

// fileStoreMessage is the sentence a person sees. The upstream error text goes
// nowhere: not into the response, not into a log.
func fileStoreMessage(code string) string {
	switch code {
	case codeFileStoreUnavailable:
		return "The file store is not reachable right now. Try again in a moment."
	case codeFileStoreMisconfigured:
		return "The file store is not set up correctly. Ask an administrator to check File storage."
	case codeFileMissingInStore:
		return "This file is no longer in the file store."
	}
	return "The file store refused this file."
}

// deleteObjectRows removes every bucket object the workspace's rows name (a
// workspace reset). The first failure stops it, so the rows stay to retry from.
func (s *Store) deleteObjectRows() error {
	rows, err := s.db.Query(`SELECT disk_name FROM attachments WHERE disk_name>='obj:' AND disk_name<'obj;'`)
	if err != nil {
		return err
	}
	var disks []string
	for rows.Next() {
		var d string
		if err = rows.Scan(&d); err != nil {
			rows.Close()
			return err
		}
		disks = append(disks, d)
	}
	rows.Close()
	for _, d := range disks {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = s.removeAttachmentFile(ctx, d)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
