package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
	"github.com/google/uuid"
)

// Imported attachment bytes go straight into the server file store (S2 of
// docs/design/computer-decoupling.md): an import needs no computer. The staging
// journal holds only generated IDs, hashes and lengths, never file bytes, and it
// is durable BEFORE any file is written. The published attachment rows and the
// journal deletion commit in one SQLite transaction, so a crash leaves at worst
// an unreferenced file that recovery removes.
//
// A journal row written by a build that staged on the computer's blob store is
// cleaned the same way; when a computer is attached its staged object is removed
// too.
type portableBlobStorage interface {
	guestBlobStorage
	PutStagedBlob(context.Context, string, []byte) error
	DeleteStagedBlob(context.Context, string, string) error
}

func (s *Store) portableBlobBackend() portableBlobStorage {
	backend, _ := s.legacyBlobs().(portableBlobStorage)
	return backend
}

func portableServerDisk(id string) string { return id + ".upload" }

// portableLocation is where an imported file goes: the bucket when one is
// configured (kind imports), else <root>/<id>.upload.
func portableLocation(b FileBackend, id string) (disk, store string) {
	if b.Kind == fileStoreKindS3 {
		return objPrefix + b.objectKey(attachmentOriginImport, id), b.Gen
	}
	return portableServerDisk(id), ""
}

// writePortableAttachmentFile stores one imported file where portableLocation
// says, through the same Put as every other new file. The hash is read back.
func (s *Store) writePortableAttachmentFile(ctx context.Context, b FileBackend, id string, data []byte, digest string) error {
	var target filestore.Store
	disk, _ := portableLocation(b, id)
	key := disk
	if b.Kind == fileStoreKindS3 {
		if b.Store == nil {
			return newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
		}
		target, key = b.Store, strings.TrimPrefix(disk, objPrefix)
	} else {
		root, err := s.attachmentRoot()
		if err != nil {
			return err
		}
		target = filestore.Disk{Root: root}
	}
	if err := target.Put(ctx, key, bytes.NewReader(data), filestore.ObjectMeta{Size: int64(len(data)), SHA256: digest, SafeMIME: "application/octet-stream"}); err != nil {
		if b.Kind == fileStoreKindS3 {
			return fileStoreFailure(err)
		}
		return err
	}
	r, _, err := target.Open(ctx, key)
	if err == nil {
		defer r.Close()
		h := sha256.New()
		if _, err = io.Copy(h, r); err == nil && hex.EncodeToString(h.Sum(nil)) == digest {
			return nil
		}
	}
	_ = target.Delete(context.WithoutCancel(ctx), key)
	return errPortableStorage
}

// removePortableAttachmentFile removes a staged file by the location journaled for it.
func (s *Store) removePortableAttachmentFile(ctx context.Context, id, disk string) error {
	if disk == "" {
		disk = portableServerDisk(id)
	}
	if strings.HasPrefix(disk, objPrefix) {
		return s.removeAttachmentFile(ctx, disk)
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(root, disk))
	_ = os.Remove(filepath.Join(root, "."+disk+".tmp"))
	return nil
}

func (s *Store) recoverPortableAssets(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT import_id,target_id,sha256,size,disk_name FROM portability_asset_staging ORDER BY import_id,target_id LIMIT ?`, portableMaxAttachments*32+1)
	if err != nil {
		return err
	}
	type orphan struct {
		importID, id, digest, disk string
		size                       int64
	}
	staged := []orphan{}
	for rows.Next() {
		var x orphan
		if err = rows.Scan(&x.importID, &x.id, &x.digest, &x.size, &x.disk); err != nil {
			break
		}
		staged = append(staged, x)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(staged) == 0 {
		return nil
	}
	if len(staged) > portableMaxAttachments*32 {
		return errPortableStorage
	}
	backend := s.portableBlobBackend()
	for _, x := range staged {
		hash, err := hex.DecodeString(x.digest)
		if !portableID(x.id) || len(hash) != 32 || err != nil || hex.EncodeToString(hash) != x.digest || x.size < 0 || x.size > portableMaxAttachmentBytes {
			return errPortableStorage
		}
		var owned int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE id=? OR disk_name=? OR disk_name=?`, x.id, guestAttachmentPrefix+x.id, portableServerDisk(x.id)).Scan(&owned); err != nil || owned != 0 {
			return errPortableStorage
		}
		if err = s.removePortableAttachmentFile(ctx, x.id, x.disk); err != nil {
			return errPortableStorage
		}
		if backend != nil {
			if err = backend.DeleteStagedBlob(ctx, x.id, x.digest); err != nil {
				return errPortableStorage
			}
		}
		if _, err = s.db.ExecContext(ctx, `DELETE FROM portability_asset_staging WHERE import_id=? AND target_id=?`, x.importID, x.id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) portablePreviewState(ctx context.Context, tx *sql.Tx, b portableBundle, previewID string) (portableResult, bool, error) {
	var digest, destination, status, resultJSON string
	var expires int64
	if err := tx.QueryRowContext(ctx, `SELECT digest,destination,status,expires_at,result_json FROM portability_imports WHERE id=?`, previewID).Scan(&digest, &destination, &status, &expires, &resultJSON); err != nil {
		return portableResult{}, false, errors.New("preview does not belong to this workspace or bundle; preview again")
	}
	if err := s.portableCheckDigest(ctx, tx, b, previewID, digest, destination, status == "applied"); err != nil {
		return portableResult{}, false, err
	}
	if status == "applied" {
		var result portableResult
		err := json.Unmarshal([]byte(resultJSON), &result)
		return result, true, err
	}
	current, _, err := portableDestination(tx)
	if err != nil {
		return portableResult{}, false, err
	}
	if expires < time.Now().Unix() || (len(b.VaultEnvironment) == 0 && current != destination) {
		return portableResult{}, false, errors.New("preview expired or destination changed; preview again")
	}
	return portableResult{}, false, nil
}
func (s *Store) preparePortableAssets(ctx context.Context, b portableBundle, previewID string) (map[string]string, error) {
	targets := map[string]string{}
	if len(b.Attachments) == 0 {
		return targets, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return targets, err
	}
	defer tx.Rollback()
	if _, replayed, err := s.portablePreviewState(ctx, tx, b, previewID); err != nil || replayed {
		return targets, err
	}
	backend, err := s.fileBackend(ctx)
	if err != nil {
		return targets, err
	}
	for _, x := range b.Attachments {
		target := uuid.NewString()
		targets[x.ID] = target
		disk, gen := portableLocation(backend, target)
		if _, err = tx.ExecContext(ctx, `INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at,disk_name,store) VALUES(?,?,?,?,?,?,?)`, previewID, target, x.SHA256, x.Size, now(), disk, gen); err != nil {
			return targets, err
		}
	}
	if err = tx.Commit(); err != nil {
		return targets, err
	}
	for _, x := range b.Attachments {
		data, err := portableAttachmentData(x)
		if err != nil {
			return targets, err
		}
		if err = s.writePortableAttachmentFile(ctx, backend, targets[x.ID], data, x.SHA256); err != nil {
			return targets, err
		}
	}
	return targets, nil
}

// The optional guard surrounds ONLY the SQLite commit/cache publication, never
// guest I/O or response writes. Staging has a separate workspace mutex.
func (s *Store) applyPortableWithStateGuard(ctx context.Context, b portableBundle, previewID string, guard func() func(portableResult, bool, error)) (result portableResult, replayed bool, err error) {
	if err = b.validate(); err != nil {
		return
	}
	s.portabilityMu.Lock()
	defer s.portabilityMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	// A credential-only recovery is independent of Guest availability. Older
	// asset journals remain durable for startup or the next attachment import.
	if len(b.VaultEnvironment) == 0 || len(b.Attachments) > 0 {
		if err = s.recoverPortableAssets(ctx); err != nil {
			return
		}
	}
	targets, err := s.preparePortableAssets(ctx, b, previewID)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.recoverPortableAssets(cleanup)
		return result, false, err
	}
	var finish func(portableResult, bool, error)
	if guard != nil {
		finish = guard()
	}
	result, replayed, err = s.applyPortableSQL(ctx, b, previewID, targets)
	if finish != nil {
		finish(result, replayed, err)
	}
	if err != nil && len(targets) > 0 {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.recoverPortableAssets(cleanup)
	}
	return
}

func portableBytesHash(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
