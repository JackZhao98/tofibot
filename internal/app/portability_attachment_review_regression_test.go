package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer/guest"
	"github.com/google/uuid"
)

type reviewGuestCleanup struct {
	*portableFixtureBlobs
	handler http.Handler
}

func (f *reviewGuestCleanup) DeleteStagedBlob(ctx context.Context, id, digest string) error {
	req := httptest.NewRequest("DELETE", "/v1/blobs/"+id, nil).WithContext(ctx)
	req.Header.Set("X-Tofi-Staged-SHA256", digest)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, req)
	if w.Code != 204 || w.Header().Get("X-Tofi-Blob-Durability") != "1" {
		return fmt.Errorf("cleanup code=%d durability=%q", w.Code, w.Header().Get("X-Tofi-Blob-Durability"))
	}
	return nil
}

func TestReviewPortableCrashBeforeObjectRenameReclaimsTemporaryQuota(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	root := t.TempDir()
	service, err := guest.New(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close(context.Background())
	s.guestBlobs = &reviewGuestCleanup{newPortableFixtureBlobs(), service.Handler()}
	id := uuid.NewString()
	data := []byte("Synthetic staged bytes synced before rename")
	_, err = s.db.Exec(`INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at) VALUES(?,?,?,?,?)`, uuid.NewString(), id, portableBytesHash(data), len(data), now())
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "shared/.tofi/blobs/objects")
	if err = os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	// Same pre-rename crash state, now using the writer's journal-bound UUID name.
	pending := filepath.Join(folder, "."+id+".pending")
	f, err := os.OpenFile(pending, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0400)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.recoverPortableAssets(context.Background()); err != nil {
		t.Fatal(err)
	}
	var journal int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&journal); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("recovery reported success and remaining journal=%d, but %d pre-rename temporary object(s) still consume quota", journal, len(entries))
	}
}

type reviewExportReadGate struct {
	*portableFixtureBlobs
	once             sync.Once
	started, release chan struct{}
}

func (f *reviewExportReadGate) GetBlob(ctx context.Context, id string) ([]byte, error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-f.release:
		return f.portableFixtureBlobs.GetBlob(ctx, id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestReviewPortableGuestExportDoesNotOccupyOnlyDatabaseConnection(t *testing.T) {
	s, _, blobs := portableAssetFixture(t)
	gate := &reviewExportReadGate{portableFixtureBlobs: blobs, started: make(chan struct{}), release: make(chan struct{})}
	s.guestBlobs = gate
	done := make(chan error, 1)
	go func() {
		_, err := s.exportPortable(context.Background(), "synthetic", portableSelection{}, "account")
		done <- err
	}()
	defer func() { close(gate.release); <-done }()
	select {
	case <-gate.started:
	case <-time.After(time.Second):
		t.Fatal("export did not reach guest read")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&n); err != nil {
		t.Fatalf("unrelated database access blocked while attachment export waits on guest: %v", err)
	}
}

func TestReviewPortablePartialPendingCleanupAndFailureRetainsJournal(t *testing.T) {
	for _, state := range []string{"partial", "cleanup_failure"} {
		t.Run(state, func(t *testing.T) {
			s, err := OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			root := t.TempDir()
			service, err := guest.New(root, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close(context.Background())
			s.guestBlobs = &reviewGuestCleanup{newPortableFixtureBlobs(), service.Handler()}
			id := uuid.NewString()
			data := []byte("Synthetic full bytes; the crash may have written only a prefix")
			_, err = s.db.Exec(`INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at) VALUES(?,?,?,?,?)`, uuid.NewString(), id, portableBytesHash(data), len(data), now())
			if err != nil {
				t.Fatal(err)
			}
			folder := filepath.Join(root, "shared/.tofi/blobs/objects")
			if err = os.MkdirAll(folder, 0700); err != nil {
				t.Fatal(err)
			}
			pending := filepath.Join(folder, "."+id+".pending")
			if state == "partial" {
				if err = os.WriteFile(pending, data[:7], 0400); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = os.Mkdir(pending, 0700); err != nil {
					t.Fatal(err)
				}
				child := filepath.Join(pending, "synthetic-blocker")
				if err = os.WriteFile(child, []byte("synthetic"), 0600); err != nil {
					t.Fatal(err)
				}
				if err = s.recoverPortableAssets(context.Background()); err == nil {
					t.Fatal("cleanup failure acknowledged")
				}
				var journal int
				if err = s.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&journal); err != nil || journal != 1 {
					t.Fatalf("cleanup intent lost: %d %v", journal, err)
				}
				if err = os.Remove(child); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(pending); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.recoverPortableAssets(context.Background()); err != nil {
				t.Fatal(err)
			}
			var journal int
			if err = s.db.QueryRow(`SELECT COUNT(*) FROM portability_asset_staging`).Scan(&journal); err != nil || journal != 0 {
				t.Fatalf("completed cleanup journal: %d %v", journal, err)
			}
			entries, err := os.ReadDir(folder)
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary quota survived: %v %v", entries, err)
			}
		})
	}
}

func TestReviewPortableExportKeepsSnapshotAndReportsChangedFiles(t *testing.T) {
	for _, state := range []string{"metadata_changed", "file_missing", "byte_size_changed"} {
		t.Run(state, func(t *testing.T) {
			s, b, blobs := portableAssetFixture(t)
			gate := &reviewExportReadGate{portableFixtureBlobs: blobs, started: make(chan struct{}), release: make(chan struct{})}
			s.guestBlobs = gate
			type outcome struct {
				b   portableBundle
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := s.exportPortable(context.Background(), "synthetic-source-instance", portableSelection{}, "account")
				done <- outcome{result, err}
			}()
			released := false
			defer func() {
				if !released {
					close(gate.release)
					<-done
				}
			}()
			select {
			case <-gate.started:
			case <-time.After(time.Second):
				t.Fatal("export did not reach byte read")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := s.db.ExecContext(ctx, `UPDATE attachments SET name='Synthetic later name',size=size+1; UPDATE messages SET content='Synthetic later message'; UPDATE bots SET name='Synthetic later Bot';`); err != nil {
				t.Fatal(err)
			}
			changed := b.Attachments[0]
			if state == "file_missing" {
				if err := blobs.DeleteBlob(context.Background(), changed.ID); err != nil {
					t.Fatal(err)
				}
			}
			if state == "byte_size_changed" {
				blobs.mu.Lock()
				blobs.data[changed.ID] = append(blobs.data[changed.ID], byte('!'))
				blobs.mu.Unlock()
			}
			close(gate.release)
			released = true
			got := <-done
			if got.err != nil {
				t.Fatal(got.err)
			}
			if got.b.Bots[0].Name != b.Bots[0].Name || got.b.Messages[0].Content != b.Messages[0].Content {
				t.Fatal("export mixed later rows into metadata snapshot")
			}
			original := map[string]portableAttachment{}
			for _, x := range b.Attachments {
				original[x.ID] = x
			}
			for _, x := range got.b.Attachments {
				before := original[x.ID]
				if x.Name != before.Name || x.Size != before.Size || x.SHA256 != before.SHA256 || x.ConversationID != before.ConversationID {
					t.Fatal("byte read used later attachment metadata")
				}
			}
			if state == "metadata_changed" {
				if len(got.b.Attachments) != 2 || got.b.AttachmentCount != 0 {
					t.Fatal("consistent original bytes were omitted")
				}
			} else {
				reason := map[string]string{"file_missing": "missing", "byte_size_changed": "changed_metadata"}[state]
				if len(got.b.Attachments) != 1 || got.b.AttachmentCount != 1 || got.b.MissingAttachments[0].ID != changed.ID || got.b.MissingAttachments[0].Reason != reason || got.b.MissingAttachments[0].Name != changed.Name {
					t.Fatalf("changed source asset was not reported honestly: %+v", got.b.MissingAttachments)
				}
			}
		})
	}
}
