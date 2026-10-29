package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func storageStore(t *testing.T) (*Store, Conversation, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Tests must not depend on the free space of the machine running them.
	s.storage.minFree = -1
	bot, err := s.CreateBot("synthetic", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	return s, conv, dir
}

func addOrigin(t *testing.T, s *Store, conv, name, origin, body string) Attachment {
	t.Helper()
	a, err := s.addAttachmentOpts(context.Background(), conv, name, "text/plain", strings.NewReader(body), attachmentOpts{Origin: origin})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func counter(t *testing.T, s *Store, class string) (int64, int64) {
	t.Helper()
	var b, o int64
	err := s.db.QueryRow(`SELECT bytes,objects FROM storage_counters WHERE class=?`, class).Scan(&b, &o)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return b, o
}

func TestCountersFollowInsertDelete(t *testing.T) {
	s, conv, _ := storageStore(t)
	a1 := addOrigin(t, s, conv.ID, "a.txt", attachmentOriginUpload, "12345")
	addOrigin(t, s, conv.ID, "b.txt", attachmentOriginPublished, "1234567")
	addOrigin(t, s, conv.ID, "c.txt", attachmentOriginGenerated, "123")
	// Dedup: identical bytes are two references, counted twice logically, and
	// stored as two files (there is no content-addressed store yet).
	d1 := addOrigin(t, s, conv.ID, "d1.txt", attachmentOriginOutbox, "same bytes")
	d2 := addOrigin(t, s, conv.ID, "d2.txt", attachmentOriginOutbox, "same bytes")
	if s.attachmentDigest(d1.ID) != s.attachmentDigest(d2.ID) {
		t.Fatal("digests differ for identical content")
	}
	for class, want := range map[string][2]int64{"uploads": {5, 1}, "published": {7, 1}, "generated": {3, 1}, "outbox": {20, 2}, "mirror": {0, 0}} {
		if b, o := counter(t, s, class); b != want[0] || o != want[1] {
			t.Fatalf("%s = %d bytes / %d files, want %v", class, b, o, want)
		}
	}
	// Deleting one reference removes exactly its bytes.
	if _, err := s.db.Exec(`DELETE FROM attachments WHERE id=?`, d1.ID); err != nil {
		t.Fatal(err)
	}
	if b, o := counter(t, s, "outbox"); b != 10 || o != 1 {
		t.Fatalf("outbox after delete = %d / %d", b, o)
	}
	// A changed row moves between classes in one step.
	if _, err := s.db.Exec(`UPDATE attachments SET origin='mirror', size=40 WHERE id=?`, a1.ID); err != nil {
		t.Fatal(err)
	}
	if b, o := counter(t, s, "uploads"); b != 0 || o != 0 {
		t.Fatalf("uploads after move = %d / %d", b, o)
	}
	if b, o := counter(t, s, "mirror"); b != 40 || o != 1 {
		t.Fatalf("mirror after move = %d / %d", b, o)
	}
	// Deleting the Bot cascades through conversation and attachments.
	var botID string
	if err := s.db.QueryRow(`SELECT bot_id FROM conversations WHERE id=?`, conv.ID).Scan(&botID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBot(botID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM attachments`).Scan(&n)
	for _, class := range storageAttachmentClasses {
		if b, o := counter(t, s, class); b != 0 || o != 0 || n != 0 {
			t.Fatalf("%s after cascade = %d / %d (rows %d)", class, b, o, n)
		}
	}
}

func TestReconcileRepairsDrift(t *testing.T) {
	s, conv, _ := storageStore(t)
	addOrigin(t, s, conv.ID, "a.txt", attachmentOriginUpload, "12345")
	addOrigin(t, s, conv.ID, "b.txt", attachmentOriginPublished, "1234567")
	// A build with no triggers wrote a row, and a counter was edited.
	for _, q := range []string{`DROP TRIGGER storage_attachments_ai`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at,origin) VALUES('x1',?,'x','text/plain',100,'x1.upload',?,'generated')`, conv.ID, now()); err != nil {
		t.Fatal(err)
	}
	if err := migrateStorageCounters(s.db); err != nil { // puts the trigger back
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE storage_counters SET bytes=9999 WHERE class='uploads'`); err != nil {
		t.Fatal(err)
	}
	drift, err := reconcileAttachmentCounters(s.db)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, d := range drift {
		got[d.Class] = d.ActualBytes
	}
	if len(got) != 2 || got["uploads"] != 5 || got["generated"] != 100 {
		t.Fatalf("drift = %+v", drift)
	}
	if b, _ := counter(t, s, "generated"); b != 100 {
		t.Fatalf("generated = %d", b)
	}
	// A second pass finds nothing: the repair is stable.
	if drift, err = reconcileAttachmentCounters(s.db); err != nil || len(drift) != 0 {
		t.Fatalf("second pass drift=%+v err=%v", drift, err)
	}
}

func TestCountersSeedOnUpgrade(t *testing.T) {
	s, conv, _ := storageStore(t)
	addOrigin(t, s, conv.ID, "a.txt", attachmentOriginUpload, "12345")
	if _, err := s.db.Exec(`DELETE FROM storage_counters`); err != nil {
		t.Fatal(err)
	}
	if err := migrateStorageCounters(s.db); err != nil {
		t.Fatal(err)
	}
	if b, o := counter(t, s, "uploads"); b != 5 || o != 1 {
		t.Fatalf("seeded uploads = %d / %d", b, o)
	}
}

func TestReconcileMeasuresConversationsAndBreakdown(t *testing.T) {
	s, conv, dir := storageStore(t)
	addOrigin(t, s, conv.ID, "a.txt", attachmentOriginUpload, "12345")
	if _, err := s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at,origin,bot_id) SELECT 'bb',?, 'p.txt','text/plain',9,'bb.upload',?,'published',bot_id FROM conversations WHERE id=?`, conv.ID, now(), conv.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "bb.upload"), []byte("123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddMessage(conv.ID, "user", "", "", "hello there", ""); err != nil {
		t.Fatal(err)
	}
	rep, err := s.reconcileStorage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rep.FilesBytes != 14 || rep.PhysicalFilesBytes != 5+9 {
		t.Fatalf("files=%d physical=%d", rep.FilesBytes, rep.PhysicalFilesBytes)
	}
	if rep.DatabaseBytes <= 0 || rep.ServerBytes != rep.FilesBytes+rep.DatabaseBytes {
		t.Fatalf("database=%d server=%d", rep.DatabaseBytes, rep.ServerBytes)
	}
	var conversations StorageClass
	for _, c := range rep.Classes {
		if c.Class == storageClassConversations {
			conversations = c
		}
	}
	if conversations.Bytes < int64(len("hello there")) || conversations.Billable {
		t.Fatalf("conversations class = %+v", conversations)
	}
	if len(rep.Detail.ByConversation) != 1 || rep.Detail.ByConversation[0].ID != conv.ID || rep.Detail.ByConversation[0].Bytes < 14+int64(len("hello there")) {
		t.Fatalf("by conversation = %+v", rep.Detail.ByConversation)
	}
	var botBytes, userBytes int64
	for _, b := range rep.Detail.ByBot {
		if b.ID == "" {
			userBytes = b.Bytes
		} else if b.Name == "synthetic" {
			botBytes = b.Bytes
		}
	}
	if botBytes != 9 || userBytes < 5+int64(len("hello there")) {
		t.Fatalf("by bot = %+v", rep.Detail.ByBot)
	}
}

func TestQuotaRefusesUpload(t *testing.T) {
	s, conv, dir := storageStore(t)
	var limit *int64
	s.storage.limit = func() *int64 { return limit }
	// NULL = unlimited.
	addOrigin(t, s, conv.ID, "free.txt", attachmentOriginUpload, strings.Repeat("x", 1000))
	// The limit leaves room for the database and 100 more bytes.
	used, err := s.serverBytes()
	if err != nil {
		t.Fatal(err)
	}
	room := used + 100
	limit = &room
	addOrigin(t, s, conv.ID, "fits.txt", attachmentOriginUpload, strings.Repeat("y", 60))
	// A file that crosses the line is refused with the code, and leaves no bytes.
	before, _ := counter(t, s, "uploads")
	_, err = s.addAttachmentOpts(context.Background(), conv.ID, "big.txt", "text/plain", strings.NewReader(strings.Repeat("z", 500)), attachmentOpts{})
	if storageErrorCode(err) != StorageQuotaExceededCode {
		t.Fatalf("crossing the limit: %v", err)
	}
	if after, _ := counter(t, s, "uploads"); after != before {
		t.Fatalf("counter moved from %d to %d", before, after)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "attachments"))
	if len(entries) != 2 {
		t.Fatalf("%d files on disk, want 2", len(entries))
	}
	// A full account refuses any new file, including a model-side import.
	zero := int64(0)
	limit = &zero
	if _, err = s.addAttachmentOpts(context.Background(), conv.ID, "p.txt", "text/plain", strings.NewReader("p"), attachmentOpts{Origin: attachmentOriginPublished}); storageErrorCode(err) != StorageQuotaExceededCode {
		t.Fatalf("full account: %v", err)
	}
	if !strings.HasPrefix(err.Error(), StorageQuotaExceededCode+":") {
		t.Fatalf("a tool result must start with the code: %q", err.Error())
	}
	// Raising the limit, or setting NULL, lets files in again.
	limit = nil
	addOrigin(t, s, conv.ID, "again.txt", attachmentOriginUpload, "ok")
}

func TestQuotaRefusesUploadOverHTTP(t *testing.T) {
	dir := t.TempDir()
	var limit *int64
	srv, err := NewServer(Config{IsolatedWorkspace: true, ComputerBackend: ComputerBackendNone, DataDir: dir, HostMinFreeBytes: -1, StorageLimit: func() *int64 { return limit }})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	bot, _ := srv.store.CreateBot("synthetic", "", "model")
	zero := int64(0)
	limit = &zero
	upload := func() *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := newMultipart(&body, "file", "n.txt", "hello")
		r := httptest.NewRequest("POST", "/api/conversations/"+bot.DMConversationID+"/attachments", &body)
		r.Header.Set("Content-Type", mw)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, r)
		return w
	}
	w := upload()
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
		Code string `json:"code"`
	}
	json.Unmarshal(w.Body.Bytes(), &errBody)
	if w.Code != http.StatusInsufficientStorage || !strings.Contains(w.Body.String(), StorageQuotaExceededCode) {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	limit = nil
	if w = upload(); w.Code != 201 {
		t.Fatalf("unlimited upload: %d %s", w.Code, w.Body.String())
	}
}

func TestHostFreeSpaceGuard(t *testing.T) {
	s, conv, _ := storageStore(t)
	s.storage.minFree = 0 // the default floor, 2 GiB
	free := uint64(1 << 30)
	s.storage.diskSpace = func(string) (uint64, uint64, error) { return free, 100 << 30, nil }
	_, err := s.addAttachmentOpts(context.Background(), conv.ID, "a.txt", "text/plain", strings.NewReader("x"), attachmentOpts{})
	if storageErrorCode(err) != StorageHostFullCode {
		t.Fatalf("low host space: %v", err)
	}
	free = 3 << 30
	addOrigin(t, s, conv.ID, "a.txt", attachmentOriginUpload, "x")
	// A probe that fails refuses the file: an unreadable disk is not admitted.
	s.storage.diskSpace = func(string) (uint64, uint64, error) { return 0, 0, errors.New("no statfs") }
	_, err = s.addAttachmentOpts(context.Background(), conv.ID, "b.txt", "text/plain", strings.NewReader("x"), attachmentOpts{})
	if storageErrorCode(err) != StorageHostFullCode {
		t.Fatalf("failed probe: %v", err)
	}
	// With the floor off the probe is not consulted.
	s.storage.minFree = -1
	addOrigin(t, s, conv.ID, "c.txt", attachmentOriginUpload, "x")
}

// Uploads that start together must not all be admitted against the same free room.
func TestConcurrentUploadsCannotOvershootTheQuota(t *testing.T) {
	s, conv, _ := storageStore(t)
	used, err := s.serverBytes()
	if err != nil {
		t.Fatal(err)
	}
	room := used + 1000
	baseFiles, _ := counter(t, s, "uploads")
	s.storage.limit = func() *int64 { return &room }
	const workers, size = 16, 400
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, results[i] = s.addAttachmentOpts(context.Background(), conv.ID, "c.txt", "text/plain", strings.NewReader(strings.Repeat("a", size)), attachmentOpts{})
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, e := range results {
		if e == nil {
			ok++
		} else if storageErrorCode(e) != StorageQuotaExceededCode {
			t.Fatalf("unexpected error: %v", e)
		}
	}
	// The database grows by whole pages, so judge the file bytes.
	if got, _ := counter(t, s, "uploads"); got-baseFiles > 1000 {
		t.Fatalf("%d uploads of %d bytes added %d file bytes to a room of 1000", ok, size, got-baseFiles)
	}
	if ok == 0 {
		t.Fatal("no upload fit")
	}
	if s.storageReserved != 0 {
		t.Fatalf("reservation leaked: %d", s.storageReserved)
	}
}

func TestStorageAPIShape(t *testing.T) {
	dir := t.TempDir()
	limit := int64(1 << 30)
	srv, err := NewServer(Config{IsolatedWorkspace: true, ComputerBackend: ComputerBackendNone, DataDir: dir, HostMinFreeBytes: -1,
		StorageLimit: func() *int64 { return &limit },
		ComputerUsage: func(context.Context) *ComputerUsage {
			return &ComputerUsage{LogicalBytes: 5, AllocatedBytes: 7, QuotaBytes: 8 << 30}
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	bot, _ := srv.store.CreateBot("synthetic", "", "model")
	if _, err = srv.store.addAttachmentOpts(context.Background(), bot.DMConversationID, "a.txt", "text/plain", strings.NewReader("12345"), attachmentOpts{BotID: bot.ID, Origin: attachmentOriginPublished}); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.store.reconcileStorage(context.Background()); err != nil {
		t.Fatal(err)
	}
	get := func() map[string]any {
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/storage", nil))
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := get()
	for _, k := range []string{"version", "measured_at", "server_bytes", "files_bytes", "database_bytes", "physical_files_bytes", "pending_copy_count", "classes", "limit_bytes", "remaining_bytes", "over_limit", "computer", "by_bot", "by_conversation"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing field %q in %v", k, out)
		}
	}
	if out["version"].(float64) != 1 || out["files_bytes"].(float64) != 5 || out["limit_bytes"].(float64) != float64(limit) || out["over_limit"].(bool) {
		t.Fatalf("unexpected values: %v", out)
	}
	if c := out["computer"].(map[string]any); c["logical_bytes"].(float64) != 5 || c["allocated_bytes"].(float64) != 7 {
		t.Fatalf("computer: %v", c)
	}
	if classes := out["classes"].([]any); len(classes) != 7 {
		t.Fatalf("classes: %v", classes)
	}
	// The counters are live: a new file shows without another reconcile.
	srv.store.addAttachmentOpts(context.Background(), bot.DMConversationID, "b.txt", "text/plain", strings.NewReader("123"), attachmentOpts{})
	if get()["files_bytes"].(float64) != 8 {
		t.Fatalf("not live: %v", get())
	}
	// No limit: null fields, never zero.
	srv.store.storage.limit = nil
	out = get()
	if out["limit_bytes"] != nil || out["remaining_bytes"] != nil || out["over_limit"].(bool) {
		t.Fatalf("unlimited: %v", out)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/storage", nil))
	if w.Code != 405 {
		t.Fatalf("POST = %d", w.Code)
	}
}

func TestAdminStorageListsPendingCopies(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "storage-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "storage-user", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID); err != nil {
		t.Fatal(err)
	}
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	ws, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	bot, err := ws.store.CreateBot("synthetic", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	blobs := newLegacyBlobs()
	legacyRow(t, ws.store, bot.DMConversationID, blobs, "one.txt", []byte("first"))
	legacyRow(t, ws.store, bot.DMConversationID, blobs, "two.txt", []byte("second"))

	type list struct {
		Version int `json:"version"`
		Host    struct {
			MinFreeBytes int64 `json:"min_free_bytes"`
		} `json:"host"`
		Totals   adminStorageTotals    `json:"totals"`
		Accounts []adminStorageAccount `json:"accounts"`
	}
	w := accountRequest(g, "GET", "/api/admin/storage", "", ac)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var out list
	if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var found *adminStorageAccount
	for i := range out.Accounts {
		if out.Accounts[i].AccountID == user.ID {
			found = &out.Accounts[i]
		}
	}
	if found == nil || found.PendingCopyCount != 2 || out.Totals.PendingCopyCount != 2 || found.FilesBytes != 11 || found.Source != "live" {
		t.Fatalf("user row = %+v, totals %+v", found, out.Totals)
	}
	if out.Version != 1 || out.Host.MinFreeBytes <= 0 || out.Totals.Accounts != 2 {
		t.Fatalf("envelope = %+v", out)
	}
	// Only an admin may read it, and a user reads only their own usage.
	if w = accountRequest(g, "GET", "/api/admin/storage", "", uc); w.Code != 403 {
		t.Fatalf("user on admin route = %d", w.Code)
	}
	if w = accountRequest(g, "GET", "/api/storage", "", uc); w.Code != 200 || !strings.Contains(w.Body.String(), `"pending_copy_count":2`) {
		t.Fatalf("own usage = %d %s", w.Code, w.Body.String())
	}
	if w = accountRequest(g, "GET", "/api/admin/accounts/"+user.ID+"/storage", "", ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"series"`) {
		t.Fatalf("account detail = %d %s", w.Code, w.Body.String())
	}
}

func TestAdminQuotaPatchSetsAndClearsServerLimit(t *testing.T) {
	g := accountFixture(t)
	admin, _ := g.create(context.Background(), "limit-admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	user, _ := g.create(context.Background(), "limit-user", "", "SyntheticPassword123!", false, "")
	g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID)
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	ws, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	ws.store.storage.minFree = -1
	bot, _ := ws.store.CreateBot("synthetic", "", "model")
	path := "/api/admin/accounts/" + user.ID + "/quota"
	for _, bad := range []string{`{}`, `{"server_bytes_limit":-1}`, `{"server_bytes_limit":"big"}`, `{"server_bytes_limit":1.5}`, `{"quota_gib":4}`, `{"other":1}`} {
		if w := accountRequest(g, "PATCH", path, bad, ac); w.Code != 400 {
			t.Fatalf("%s = %d", bad, w.Code)
		}
	}
	if w := accountRequest(g, "PATCH", path, `{"server_bytes_limit":0}`, uc); w.Code != 403 {
		t.Fatalf("user patch = %d", w.Code)
	}
	if w := accountRequest(g, "PATCH", path, `{"server_bytes_limit":0}`, ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"server_bytes_limit":0`) {
		t.Fatalf("set limit = %d %s", w.Code, w.Body.String())
	}
	if _, err = ws.store.AddAttachment(bot.DMConversationID, "a.txt", "text/plain", strings.NewReader("x")); storageErrorCode(err) != StorageQuotaExceededCode {
		t.Fatalf("full account accepted a file: %v", err)
	}
	// Another account is not affected.
	if lim := g.storageLimitFor(admin.ID); lim != nil {
		t.Fatalf("admin limit = %v", *lim)
	}
	if w := accountRequest(g, "PATCH", path, `{"server_bytes_limit":null}`, ac); w.Code != 200 || !strings.Contains(w.Body.String(), `"server_bytes_limit":null`) {
		t.Fatalf("clear limit = %d %s", w.Code, w.Body.String())
	}
	if _, err = ws.store.AddAttachment(bot.DMConversationID, "a.txt", "text/plain", strings.NewReader("x")); err != nil {
		t.Fatalf("unlimited account refused a file: %v", err)
	}
}

func TestSnapshotKeepsOnePerDay(t *testing.T) {
	g := accountFixture(t)
	user, err := g.create(context.Background(), "snap-user", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	ws, err := g.workspace(user)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		ws.reconcileStorageOnce(context.Background())
	}
	var n int
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM account_storage WHERE account_id=?`, user.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("%d snapshots for one day", n)
	}
	// An old row is pruned by the next write.
	g.root.store.db.Exec(`INSERT INTO account_storage(account_id,measured_at,server_bytes,server_physical_bytes,detail) VALUES(?,?,1,1,'{}')`, user.ID, "2020-01-01T00:00:00Z")
	ws.reconcileStorageOnce(context.Background())
	g.root.store.db.QueryRow(`SELECT COUNT(*) FROM account_storage WHERE account_id=?`, user.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("%d snapshots after pruning", n)
	}
}

func newMultipart(body *bytes.Buffer, field, filename, content string) string {
	form := multipart.NewWriter(body)
	part, _ := form.CreateFormFile(field, filename)
	part.Write([]byte(content))
	form.Close()
	return form.FormDataContentType()
}
