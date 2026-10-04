package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

// The fixed account guest implements digest-aware cleanup so a crash between
// content-object creation and alias linking can also reclaim the orphan object.
type portableBlobStorage interface {
	guestBlobStorage
	PutStagedBlob(context.Context, string, []byte) error
	DeleteStagedBlob(context.Context, string, string) error
}

func (s *Store) portableBlobBackend() portableBlobStorage {
	backend, _ := s.guestBlobs.(portableBlobStorage)
	return backend
}

// Journals hold only generated IDs, hashes and lengths, never file bytes. They
// are durable BEFORE any guest write; published metadata and journal deletion
// commit in one SQLite transaction. A single workspace Store owns these jobs.
func (s *Store) recoverPortableAssets(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT import_id,target_id,sha256,size FROM portability_asset_staging ORDER BY import_id,target_id LIMIT ?`, portableMaxAttachments*32+1)
	if err != nil {
		return err
	}
	type orphan struct {
		importID, id, digest string
		size                 int64
	}
	staged := []orphan{}
	for rows.Next() {
		var x orphan
		if err = rows.Scan(&x.importID, &x.id, &x.digest, &x.size); err != nil {
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
	backend := s.portableBlobBackend()
	if backend == nil || len(staged) > portableMaxAttachments*32 {
		return errPortableStorage
	}
	for _, x := range staged {
		hash, err := hex.DecodeString(x.digest)
		if !portableID(x.id) || len(hash) != 32 || err != nil || hex.EncodeToString(hash) != x.digest || x.size < 0 || x.size > portableMaxAttachmentBytes {
			return errPortableStorage
		}
		var owned int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE id=? OR disk_name=?`, x.id, guestAttachmentPrefix+x.id).Scan(&owned); err != nil || owned != 0 {
			return errPortableStorage
		}
		if err = backend.DeleteStagedBlob(ctx, x.id, x.digest); err != nil {
			return errPortableStorage
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
	backend := s.portableBlobBackend()
	if backend == nil {
		return targets, errPortableStorage
	}
	for _, x := range b.Attachments {
		target := uuid.NewString()
		targets[x.ID] = target
		if _, err = tx.ExecContext(ctx, `INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at) VALUES(?,?,?,?,?)`, previewID, target, x.SHA256, x.Size, now()); err != nil {
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
		if err = backend.PutStagedBlob(ctx, targets[x.ID], data); err != nil {
			return targets, errPortableStorage
		}
		got, err := backend.GetBlob(ctx, targets[x.ID])
		// Verify through the same bounded account transport before publication.
		if err != nil || int64(len(got)) != x.Size || portableBytesHash(got) != x.SHA256 {
			return targets, errPortableStorage
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
	if s.portableBlobBackend() != nil && (len(b.VaultEnvironment) == 0 || len(b.Attachments) > 0) {
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
