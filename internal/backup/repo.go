// Package backup writes and reads the encrypted off-site backup repository
// (design backups.md, phase 1): one encrypted file per database per snapshot,
// disk attachments uploaded once, a manifest written last. It knows nothing of
// HTTP or accounts; internal/app wires it to the server and cmd/tofi to the
// bare-host restore. Errors are named codes, never text from the bucket.
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/JackZhao98/tofibot/internal/filestore"
)

const (
	FormatVersion = 1
	aadPrefix     = "tofi.backup:1|"
)

// MaxDBBytes is the phase-1 ceiling for one database copy (design 5.3).
var MaxDBBytes int64 = 1 << 30

// Error is a named failure. Code is the stable machine code; Account names the
// account it concerns when there is one.
type Error struct {
	Code, Account string
	Cause         error
}

func (e *Error) Error() string { return e.Code }
func (e *Error) Unwrap() error { return e.Cause }

func fail(code string, cause error) error { return &Error{Code: code, Cause: cause} }

// CodeOf is the backup error code behind err ("" when it is none of ours).
func CodeOf(err error) string {
	var be *Error
	if errors.As(err, &be) {
		return be.Code
	}
	return ""
}

// storeErr names a bucket failure.
func storeErr(err error) error {
	if err == nil {
		return nil
	}
	if CodeOf(err) != "" || errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, filestore.ErrDenied) {
		return fail("backup_store_misconfigured", err)
	}
	if errors.Is(err, filestore.ErrNotFound) {
		return fail("backup_object_missing", err)
	}
	return fail("backup_store_unavailable", err)
}

// Bucket is the store plus the one conditional overwrite the repository needs.
type Bucket interface {
	filestore.Store
	Replace(ctx context.Context, key string, data []byte, etag string) error
}

// Wrapper seals the data key under a Recovery Key. internal/app passes the
// existing "tofi.encrypted" v1 envelope (account_export.go).
type Wrapper struct {
	Wrap   func(plain []byte, key string) ([]byte, error)
	Unwrap func(slot []byte, key string) ([]byte, error)
}

// Repo is an open repository: a bucket, the key prefix of the repository, and
// the data key.
type Repo struct {
	B    Bucket
	Base string // ends with "/"
	DEK  []byte
}

func seal(dek []byte, path string, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte(aadPrefix+path)), nil
}

func open(dek []byte, path string, ct []byte) ([]byte, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	n := aead.NonceSize()
	if len(ct) < n {
		return nil, fail("backup_object_damaged", nil)
	}
	plain, err := aead.Open(nil, ct[:n], ct[n:], []byte(aadPrefix+path))
	if err != nil {
		return nil, fail("backup_object_damaged", err)
	}
	return plain, nil
}

func gz(b []byte) ([]byte, error) {
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func gunzip(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fail("backup_object_damaged", err)
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, 4*MaxDBBytes))
	if err != nil {
		return nil, fail("backup_object_damaged", err)
	}
	return out, nil
}

// readObject returns the whole object at base+path.
func readObject(ctx context.Context, b filestore.Store, key string) ([]byte, filestore.ObjectInfo, error) {
	rc, info, err := b.Open(ctx, key)
	if err != nil {
		return nil, info, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	return data, info, err
}

// get reads and decrypts one repository object.
func (r *Repo) get(ctx context.Context, path string) ([]byte, error) {
	ct, _, err := readObject(ctx, r.B, r.Base+path)
	if err != nil {
		return nil, storeErr(err)
	}
	return open(r.DEK, path, ct)
}

// put encrypts and writes one new repository object.
func (r *Repo) put(ctx context.Context, path string, plain []byte) error {
	ct, err := seal(r.DEK, path, plain)
	if err != nil {
		return err
	}
	return storeErr(r.B.Put(ctx, r.Base+path, bytes.NewReader(ct), filestore.ObjectMeta{Size: int64(len(ct)), SafeMIME: "application/octet-stream"}))
}

type repoFile struct {
	Format  int    `json:"format"`
	Created string `json:"created_at"`
}

// Init creates the repository: repo.json and the Recovery Key slot holding a
// fresh data key. It never overwrites an existing repository.
func Init(ctx context.Context, b Bucket, base, recoveryKey, now string, w Wrapper) (*Repo, error) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, err
	}
	slot, err := w.Wrap(dek, recoveryKey)
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(repoFile{Format: FormatVersion, Created: now})
	put := func(key string, data []byte) error {
		return b.Put(ctx, base+key, bytes.NewReader(data), filestore.ObjectMeta{Size: int64(len(data)), SafeMIME: "application/json"})
	}
	if err = put("repo.json", meta); errors.Is(err, filestore.ErrExists) {
		return nil, fail("backup_repo_exists", err)
	} else if err != nil {
		return nil, storeErr(err)
	}
	if err = put("keys/recovery.json", slot); err != nil {
		return nil, storeErr(err)
	}
	return &Repo{B: b, Base: base, DEK: dek}, nil
}

// Open reads the Recovery Key slot. A wrong key is backup_recovery_key_wrong,
// never "damaged".
func Open(ctx context.Context, b Bucket, base, recoveryKey string, w Wrapper) (*Repo, error) {
	slot, _, err := readObject(ctx, b, base+"keys/recovery.json")
	if errors.Is(err, filestore.ErrNotFound) {
		return nil, fail("backup_repo_missing", err)
	}
	if err != nil {
		return nil, storeErr(err)
	}
	dek, err := w.Unwrap(slot, recoveryKey)
	if err != nil || len(dek) != 32 {
		return nil, fail("backup_recovery_key_wrong", err)
	}
	return &Repo{B: b, Base: base, DEK: dek}, nil
}

// Rekey rewrites the Recovery Key slot for the same data key. Data is untouched.
func (r *Repo) Rekey(ctx context.Context, newKey string, w Wrapper) error {
	slot, err := w.Wrap(r.DEK, newKey)
	if err != nil {
		return err
	}
	info, err := r.B.Stat(ctx, r.Base+"keys/recovery.json")
	if err != nil {
		return storeErr(err)
	}
	return storeErr(r.B.Replace(ctx, r.Base+"keys/recovery.json", slot, info.ETag))
}

// Head is head.json: the lineage record. It is the only plaintext object
// besides repo.json and holds no names.
type Head struct {
	WriterSeq int64  `json:"writer_seq"`
	Latest    string `json:"latest"`
	At        string `json:"at"`
}

func (r *Repo) readHead(ctx context.Context) (h Head, etag string, found bool, err error) {
	data, info, err := readObject(ctx, r.B, r.Base+"head.json")
	if errors.Is(err, filestore.ErrNotFound) {
		return Head{}, "", false, nil
	}
	if err != nil {
		return Head{}, "", false, storeErr(err)
	}
	if json.Unmarshal(data, &h) != nil {
		return Head{}, "", false, fail("backup_object_damaged", nil)
	}
	return h, info.ETag, true, nil
}

// writeHead stores h only while head.json is still the version read (or still
// absent). Losing the race is backup_repo_diverged.
func (r *Repo) writeHead(ctx context.Context, h Head, etag string, existed bool) error {
	data, _ := json.Marshal(h)
	var err error
	if existed {
		err = r.B.Replace(ctx, r.Base+"head.json", data, etag)
	} else {
		err = r.B.Put(ctx, r.Base+"head.json", bytes.NewReader(data), filestore.ObjectMeta{Size: int64(len(data)), SafeMIME: "application/json"})
	}
	if errors.Is(err, filestore.ErrExists) {
		return fail("backup_repo_diverged", err)
	}
	return storeErr(err)
}

// Takeover raises writer_seq so a server restored from this repository becomes
// its only writer; it returns the new sequence.
func (r *Repo) Takeover(ctx context.Context) (int64, error) {
	h, etag, found, err := r.readHead(ctx)
	if err != nil {
		return 0, err
	}
	h.WriterSeq++
	return h.WriterSeq, r.writeHead(ctx, h, etag, found)
}

func joinKey(parts ...string) string { return strings.Join(parts, "/") }
