package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/google/uuid"
)

// legacyBlobs is a synthetic guest blob store for `vm:` rows.
type legacyBlobs struct {
	mu      sync.Mutex
	data    map[string][]byte
	getErr  error
	deleted []string
	gets    int
}

func newLegacyBlobs() *legacyBlobs { return &legacyBlobs{data: map[string][]byte{}} }
func (l *legacyBlobs) PutBlob(_ context.Context, id string, b []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.data[id] = append([]byte(nil), b...)
	return nil
}
func (l *legacyBlobs) GetBlob(_ context.Context, id string) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gets++
	if l.getErr != nil {
		return nil, l.getErr
	}
	b, ok := l.data[id]
	if !ok {
		return nil, computer.ErrBlobNotFound
	}
	return append([]byte(nil), b...), nil
}
func (l *legacyBlobs) DeleteBlob(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.data, id)
	l.deleted = append(l.deleted, id)
	return nil
}
func (l *legacyBlobs) has(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.data[id]
	return ok
}

// legacyRow makes a row exactly as the old guest-only store did.
// legacyRow is a vm: row that recorded its sha256.
func legacyRow(t *testing.T, s *Store, conv string, blobs *legacyBlobs, name string, data []byte) Attachment {
	t.Helper()
	a := legacyRowUnhashed(t, s, conv, blobs, name, data)
	sum := sha256.Sum256(data)
	if _, err := s.db.Exec(`UPDATE attachments SET sha256=? WHERE id=?`, hex.EncodeToString(sum[:]), a.ID); err != nil {
		t.Fatal(err)
	}
	return a
}

// legacyRowUnhashed is a vm: row from before sha256 was recorded.
func legacyRowUnhashed(t *testing.T, s *Store, conv string, blobs *legacyBlobs, name string, data []byte) Attachment {
	t.Helper()
	id := uuid.NewString()
	blobs.data[id] = append([]byte(nil), data...)
	a := Attachment{ID: id, ConversationID: conv, Name: name, MIME: "text/plain", Size: int64(len(data)), CreatedAt: now()}
	if _, err := s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, id, conv, name, a.MIME, a.Size, guestAttachmentPrefix+id, a.CreatedAt); err != nil {
		t.Fatal(err)
	}
	return a
}

func fileStoreFixture(t *testing.T) (*Store, Conversation, *legacyBlobs, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	bot, err := s.CreateBot("synthetic", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	blobs := newLegacyBlobs()
	s.guestBlobs = blobs
	return s, conv, blobs, dir
}

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// The probe of design section 3.2: an isolated workspace with no computer used to
// fail with "account cloud file storage is not ready".
func TestIsolatedWorkspaceUploadWithoutComputer(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(Config{IsolatedWorkspace: true, ComputerBackend: ComputerBackendNone, DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	bot, _ := srv.store.CreateBot("synthetic", "", "model")
	body := []byte("a file with no computer attached")
	a, err := srv.store.AddAttachment(bot.DMConversationID, "note.txt", "text/plain", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("upload without a computer: %v", err)
	}
	_, path, err := srv.store.Attachment(a.ID)
	if err != nil || strings.HasPrefix(path, guestAttachmentPrefix) {
		t.Fatalf("path=%q err=%v", path, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "attachments", a.ID+".upload"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("server copy: %q %v", got, err)
	}
	var digest, origin string
	if err = srv.store.db.QueryRow(`SELECT sha256,origin FROM attachments WHERE id=?`, a.ID).Scan(&digest, &origin); err != nil || digest != sha(body) || origin != "upload" {
		t.Fatalf("digest=%q origin=%q %v", digest, origin, err)
	}
	// The model reads it, and so does the download route.
	if text, _, err := srv.store.ReadAttachmentText(a.ID, bot.DMConversationID); err != nil || text != string(body) {
		t.Fatalf("read %q %v", text, err)
	}
	req := httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil)
	w := httptest.NewRecorder()
	if !srv.routeAttachments(w, req, "attachments/"+a.ID) || w.Code != 200 || w.Body.String() != string(body) {
		t.Fatalf("download %d %q", w.Code, w.Body.String())
	}
}

func TestAttachmentMigrationIsAdditive(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := s.CreateBot("synthetic", "", "model")
	// Rebuild the table as the previous release had it, with a row in it.
	for _, q := range []string{
		`PRAGMA foreign_keys=OFF`,
		`ALTER TABLE attachments RENAME TO attachments_new`,
		`CREATE TABLE attachments(id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, name TEXT NOT NULL, mime TEXT NOT NULL, size INTEGER NOT NULL, disk_name TEXT NOT NULL UNIQUE, created_at TEXT NOT NULL, FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE)`,
		`DROP TABLE attachments_new`,
		`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES('old-1','` + bot.DMConversationID + `','a.txt','text/plain',3,'old-1.upload','2026-01-01T00:00:00Z')`,
	} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i := 0; i < 2; i++ { // twice: idempotent
		if err = migrateAttachments(s.db); err != nil {
			t.Fatal(err)
		}
	}
	var digest *string
	var origin string
	var botID *string
	if err = s.db.QueryRow(`SELECT sha256,origin,bot_id FROM attachments WHERE id='old-1'`).Scan(&digest, &origin, &botID); err != nil || digest != nil || origin != "upload" || botID != nil {
		t.Fatalf("existing row: %v %q %v %v", digest, origin, botID, err)
	}
	s.Close()
}

func TestVMRowCopierIdempotent(t *testing.T) {
	s, conv, blobs, dir := fileStoreFixture(t)
	body := bytes.Repeat([]byte("legacy bytes "), 1000)
	a := legacyRow(t, s, conv.ID, blobs, "legacy.txt", body)
	other := legacyRow(t, s, conv.ID, blobs, "other.txt", []byte("other"))
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); errs[i] = s.copyGuestAttachment(context.Background(), a.ID) }(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, _, err := s.copyGuestRows(context.Background(), 50); err != nil { // second copier over everything
		t.Fatal(err)
	}
	for _, x := range []Attachment{a, other} {
		var disk, digest string
		if err := s.db.QueryRow(`SELECT disk_name,sha256 FROM attachments WHERE id=?`, x.ID).Scan(&disk, &digest); err != nil || disk != x.ID+".upload" || digest == "" {
			t.Fatalf("row %s: disk=%q digest=%q %v", x.ID, disk, digest, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "attachments", a.ID+".upload"))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("server copy differs: %d bytes %v", len(got), err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "attachments"))
	if len(entries) != 2 {
		t.Fatalf("expected 2 files, got %d (stray temp file?)", len(entries))
	}
	// The guest object is still there until cleanup, then goes: copy verified.
	if !blobs.has(a.ID) {
		t.Fatal("guest copy deleted before cleanup ran")
	}
	s.cleanupDeletedAttachments()
	if blobs.has(a.ID) || blobs.has(other.ID) {
		t.Fatal("guest copy not removed after a verified server copy")
	}
	if n, _ := s.pendingGuestCopies(); n != 0 {
		t.Fatalf("pending=%d", n)
	}
}

func TestVMRowCopyResumesAfterACrash(t *testing.T) {
	s, conv, blobs, dir := fileStoreFixture(t)
	body := []byte("survives a crash")
	a := legacyRow(t, s, conv.ID, blobs, "c.txt", body)
	root := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Crash 1: a half-written temp file. Crash 2 (below): the final file exists
	// but the row was never repointed.
	if err := os.WriteFile(filepath.Join(root, "."+a.ID+".upload.tmp"), []byte("half"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.copyGuestAttachment(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, a.ID+".upload")); !bytes.Equal(got, body) {
		t.Fatalf("after resume: %q", got)
	}
	b := legacyRow(t, s, conv.ID, blobs, "d.txt", body)
	if err := os.WriteFile(filepath.Join(root, b.ID+".upload"), body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.copyGuestAttachment(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	var disk string
	if s.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, b.ID).Scan(&disk); disk != b.ID+".upload" {
		t.Fatalf("disk=%q", disk)
	}
}

func TestVMRowNeverDeletesTheGuestCopyUnverified(t *testing.T) {
	s, conv, blobs, dir := fileStoreFixture(t)
	body := []byte("the only good bytes")
	a := legacyRow(t, s, conv.ID, blobs, "v.txt", body)
	// A guest object whose length differs from the record is not trusted.
	blobs.data[a.ID] = []byte("short")
	if err := s.copyGuestAttachment(context.Background(), a.ID); err == nil {
		t.Fatal("size mismatch was migrated")
	} else if !strings.Contains(err.Error(), "not migrated") {
		t.Fatal(err)
	}
	blobs.data[a.ID] = body
	// Copy, then damage the server file before cleanup: the guest object stays.
	if err := s.copyGuestAttachment(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", a.ID+".upload"), []byte("the only BAD bytes!"), 0600); err != nil {
		t.Fatal(err)
	}
	s.cleanupDeletedAttachments()
	if !blobs.has(a.ID) {
		t.Fatal("guest copy deleted although the server copy no longer matches its sha256")
	}
	// Repair the server file: cleanup now proceeds.
	if err := os.WriteFile(filepath.Join(dir, "attachments", a.ID+".upload"), body, 0600); err != nil {
		t.Fatal(err)
	}
	s.cleanupDeletedAttachments()
	if blobs.has(a.ID) {
		t.Fatal("verified guest copy not removed")
	}
	// A guest object that is gone is reported as such, and the row is kept.
	c := legacyRow(t, s, conv.ID, blobs, "gone.txt", []byte("x"))
	delete(blobs.data, c.ID)
	if err := s.copyGuestAttachment(context.Background(), c.ID); !errors.Is(err, errFileGoneFromComputer) {
		t.Fatalf("err=%v", err)
	}
	var disk string
	if s.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, c.ID).Scan(&disk); disk != guestAttachmentPrefix+c.ID {
		t.Fatalf("row repointed without bytes: %q", disk)
	}
}

func TestVMRowReadWhileDetached(t *testing.T) {
	s, conv, blobs, _ := fileStoreFixture(t)
	a := legacyRow(t, s, conv.ID, blobs, "away.txt", []byte("on the detached computer"))
	// 1. no computer attached at all
	s.guestBlobs = nil
	_, path, err := s.Attachment(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.openAttachment(context.Background(), path); !errors.Is(err, errFileOnDetachedComputer) {
		t.Fatalf("detached open: %v", err)
	}
	srv := &Server{store: s}
	req := httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil)
	w := httptest.NewRecorder()
	srv.routeAttachments(w, req, "attachments/"+a.ID)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "file_on_detached_computer") {
		t.Fatalf("route: %d %s", w.Code, w.Body.String())
	}
	if _, _, err = s.ReadAttachmentText(a.ID, conv.ID); err == nil || !strings.Contains(err.Error(), "file_on_detached_computer") {
		t.Fatalf("model read: %v", err)
	}
	// 2. a computer is attached but does not answer
	s.guestBlobs = blobs
	blobs.getErr = errors.New("dial unix: connection refused")
	if _, err = s.openAttachment(context.Background(), path); !errors.Is(err, errFileOnDetachedComputer) {
		t.Fatalf("offline open: %v", err)
	}
	var disk string
	s.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, a.ID).Scan(&disk)
	if disk != guestAttachmentPrefix+a.ID || !blobs.has(a.ID) {
		t.Fatal("an offline read changed the row or the guest object")
	}
	// 3. the computer comes back: the read works and migrates the file at once.
	blobs.getErr = nil
	f, err := s.openAttachment(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	b := new(bytes.Buffer)
	b.ReadFrom(f)
	f.Close()
	if b.String() != "on the detached computer" {
		t.Fatalf("read %q", b.String())
	}
	s.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, a.ID).Scan(&disk)
	if disk != a.ID+".upload" {
		t.Fatalf("lazy copy did not migrate: %q", disk)
	}
}

func TestCopyPassStopsWhenTheComputerDoesNotAnswer(t *testing.T) {
	s, conv, blobs, _ := fileStoreFixture(t)
	for i := 0; i < 3; i++ {
		legacyRow(t, s, conv.ID, blobs, "f.txt", []byte{byte(i)})
	}
	blobs.getErr = errors.New("connection refused")
	copied, failed, err := s.copyGuestRows(context.Background(), 50)
	if err != nil || copied != 0 || failed != 1 || blobs.gets != 1 {
		t.Fatalf("copied=%d failed=%d gets=%d err=%v", copied, failed, blobs.gets, err)
	}
	if n, _ := s.pendingGuestCopies(); n != 3 {
		t.Fatalf("pending=%d", n)
	}
}

func TestExportReportsPendingCopyNotUnavailable(t *testing.T) {
	s, conv, blobs, dir := fileStoreFixture(t)
	a := legacyRow(t, s, conv.ID, blobs, "e.txt", []byte("export me"))
	s.guestBlobs = nil
	data, reason := s.readPortableAttachment(context.Background(), filepath.Join(dir, "tofi.db"), a.ID, guestAttachmentPrefix+a.ID, a.Size)
	if data != nil || reason != "pending_copy" {
		t.Fatalf("data=%q reason=%q", data, reason)
	}
	s.guestBlobs = blobs
	data, reason = s.readPortableAttachment(context.Background(), filepath.Join(dir, "tofi.db"), a.ID, guestAttachmentPrefix+a.ID, a.Size)
	if reason != "" || string(data) != "export me" {
		t.Fatalf("data=%q reason=%q", data, reason)
	}
}

func (s *Store) mustAttachmentRoot(t *testing.T) string {
	t.Helper()
	root, err := s.attachmentRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// addLegacyGuestAttachment writes an attachment the way the guest-only store
// did before S1: bytes in the guest blob backend, row `vm:<id>`. Portability
// tests use it to model rows that predate the server file store.
func (s *Store) addLegacyGuestAttachment(conv, name, declaredMIME string, src io.Reader) (Attachment, error) {
	data, err := io.ReadAll(src)
	if err != nil {
		return Attachment{}, err
	}
	id := uuid.NewString()
	if err = s.guestBlobs.PutBlob(context.Background(), id, data); err != nil {
		return Attachment{}, err
	}
	a := Attachment{ID: id, ConversationID: conv, Name: name, MIME: declaredMIME, Size: int64(len(data)), CreatedAt: now()}
	_, err = s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, a.ID, conv, name, declaredMIME, a.Size, guestAttachmentPrefix+id, a.CreatedAt)
	return a, err
}

// revertToLegacyRows puts every locally stored attachment back to the `vm:`
// form and removes the server file, as if it had never been copied.
func revertToLegacyRows(t *testing.T, s *Store) {
	t.Helper()
	root := s.mustAttachmentRoot(t)
	rows, err := s.db.Query(`SELECT id,disk_name FROM attachments WHERE disk_name NOT LIKE 'vm:%'`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ id, disk string }
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.disk); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	for _, r := range all {
		if _, err = s.db.Exec(`UPDATE attachments SET disk_name=?,sha256=NULL WHERE id=?`, guestAttachmentPrefix+r.id, r.id); err != nil {
			t.Fatal(err)
		}
		_, _ = s.db.Exec(`DELETE FROM deleted_attachment_files WHERE disk_name=?`, guestAttachmentPrefix+r.id)
		_ = os.Remove(filepath.Join(root, r.disk))
	}
}

func TestVMRowWithoutHashIsAnUnverifiedMirrorAndKeepsTheGuestCopy(t *testing.T) {
	s, conv, blobs, _ := fileStoreFixture(t)
	a := legacyRowUnhashed(t, s, conv.ID, blobs, "old.txt", []byte("no recorded hash"))
	if err := s.copyGuestAttachment(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	var origin string
	if err := s.db.QueryRow(`SELECT origin FROM attachments WHERE id=?`, a.ID).Scan(&origin); err != nil || origin != attachmentOriginMirror {
		t.Fatalf("origin=%q %v", origin, err)
	}
	s.cleanupDeletedAttachments()
	if !blobs.has(a.ID) {
		t.Fatal("the computer's copy was deleted although the server copy was never verified against a recorded hash")
	}
}

func TestVMRowWithAWrongRecordedHashIsNotMigrated(t *testing.T) {
	s, conv, blobs, _ := fileStoreFixture(t)
	a := legacyRow(t, s, conv.ID, blobs, "swapped.txt", []byte("original bytes"))
	blobs.data[a.ID] = []byte("tampered bytes") // same length
	err := s.copyGuestAttachment(context.Background(), a.ID)
	if err == nil || !strings.Contains(err.Error(), "not migrated") {
		t.Fatalf("err=%v", err)
	}
	var disk string
	if s.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, a.ID).Scan(&disk); disk != guestAttachmentPrefix+a.ID {
		t.Fatalf("row repointed: %q", disk)
	}
}
