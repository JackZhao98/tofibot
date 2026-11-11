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

// objStore is the bucket store a row's key lives in.
func (s *Store) objStore(ctx context.Context, key string) (filestore.Store, error) {
	b, err := s.fileBackend(ctx)
	if err != nil {
		return nil, err
	}
	if b.Store == nil {
		return nil, newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
	}
	if b.Account == "" || !strings.Contains(key, "/accounts/"+b.Account+"/") {
		return nil, newFileStoreError(codeFileStoreOutOfScope, errors.New("key outside the account"))
	}
	return b.Store, nil
}

// attachmentKind groups the origins into the four key kinds.
func attachmentKind(origin string) string {
	switch origin {
	case attachmentOriginPublished, attachmentOriginGenerated, attachmentOriginOutbox, attachmentOriginMirror:
		return "artifacts"
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
