package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

const (
	testAccessKeyID = "AKIATESTONLYNOTREAL1"
	testSecretKey   = "test-only-secret-not-a-real-key-0123"
	testAccount     = "acct-1"
)

func fakeBucket(t *testing.T) (*filestore.S3, *fakes3.Server) {
	t.Helper()
	f := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(f.Close)
	s, err := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: testAccessKeyID, Secret: testSecretKey, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

// bucketStore is a workspace Store whose new files go to a fake bucket.
func bucketStore(t *testing.T) (*Store, string, *fakes3.Server) {
	t.Helper()
	s, conv, _ := attachmentFixture(t)
	t.Cleanup(func() { s.Close() })
	b, f := fakeBucket(t)
	backend := FileBackend{Kind: fileStoreKindS3, Store: b, Gen: "g1", Prefix: FileStoreKeyPrefix("tofi/", "inst-1", testAccount), Account: testAccount, GraceDays: 14}
	s.files = func(context.Context) (FileBackend, error) { return backend, nil }
	return s, conv, f
}

func readAll(t *testing.T, s *Store, path string) []byte {
	t.Helper()
	r, err := s.openAttachment(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return b
}

func attachmentDiskFiles(t *testing.T, s *Store) []string {
	t.Helper()
	root, _ := s.attachmentRoot()
	entries, _ := os.ReadDir(root)
	var out []string
	for _, e := range entries {
		if e.Name() == spoolDirName {
			if sp, _ := os.ReadDir(filepath.Join(root, spoolDirName)); len(sp) != 0 {
				t.Fatalf("spool not empty: %d", len(sp))
			}
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

func TestBucketHoldsNewFilesUnderTheKeyLayout(t *testing.T) {
	s, conv, f := bucketStore(t)
	cases := map[string]string{
		attachmentOriginUpload: "uploads", attachmentOriginPublished: "artifacts", attachmentOriginGenerated: "artifacts",
		attachmentOriginOutbox: "artifacts", attachmentOriginMirror: "uploads", attachmentOriginBotFile: "files", attachmentOriginTrace: "traces", attachmentOriginImport: "imports",
	}
	for origin, kind := range cases {
		body := []byte("body of " + origin)
		a, err := s.addAttachmentOpts(context.Background(), conv, "my secret plan.txt", "text/plain", bytes.NewReader(body), attachmentOpts{Origin: origin})
		if err != nil {
			t.Fatalf("%s: %v", origin, err)
		}
		_, path, _ := s.Attachment(a.ID)
		key := strings.TrimPrefix(path, objPrefix)
		want := "tofi/v1/inst-1/accounts/" + testAccount + "/" + kind + "/" + a.ID
		if key != want {
			t.Fatalf("%s: key %q want %q", origin, key, want)
		}
		var store string
		s.db.QueryRow(`SELECT store FROM attachments WHERE id=?`, a.ID).Scan(&store)
		if store != "g1" {
			t.Fatalf("store column %q", store)
		}
		data, meta, ok := f.Object(key)
		if !ok || !bytes.Equal(data, body) || !bytes.Equal(readAll(t, s, path), body) {
			t.Fatalf("%s: bytes not in the bucket", origin)
		}
		// Object metadata: the safe type, attachment disposition, the hash; never the file name.
		if meta.Get("Content-Type") != "text/plain; charset=utf-8" && meta.Get("Content-Type") != "text/plain" || meta.Get("Content-Disposition") != "attachment" || len(meta.Get("X-Amz-Meta-Sha256")) != 64 {
			t.Fatalf("metadata %v", meta)
		}
		for k, v := range meta {
			if strings.Contains(strings.Join(v, ""), "secret plan") || strings.Contains(k, "secret") {
				t.Fatalf("file name leaked into metadata: %v", meta)
			}
		}
		if strings.Contains(key, "secret") || strings.Contains(key, ".txt") {
			t.Fatalf("file name in key %q", key)
		}
	}
	if d := attachmentDiskFiles(t, s); len(d) != 0 {
		t.Fatalf("bucket mode wrote to disk: %v", d)
	}
}

func TestBucketDownWritesNothingToDisk(t *testing.T) {
	s, conv, f := bucketStore(t)
	f.Down = true
	_, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("hello"))
	fe, ok := asFileStoreError(err)
	if !ok || fe.code != codeFileStoreUnavailable || fe.status != 503 {
		t.Fatalf("err = %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM attachments`).Scan(&n)
	if n != 0 || len(attachmentDiskFiles(t, s)) != 0 {
		t.Fatalf("rows=%d disk=%v", n, attachmentDiskFiles(t, s))
	}
	// The HTTP route answers the named code.
	srv := &Server{store: s}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "a.txt")
	part.Write([]byte("hello"))
	mw.Close()
	r := httptest.NewRequest("POST", "/api/conversations/"+conv+"/attachments", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.routeAttachments(w, r, "conversations/"+conv+"/attachments")
	if w.Code != 503 || !strings.Contains(w.Body.String(), codeFileStoreUnavailable) {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "127.0.0.1") || strings.Contains(w.Body.String(), "bkt") {
		t.Fatalf("the response names the bucket host: %s", w.Body.String())
	}
}

func TestBucketRefusedKeyIsMisconfiguredNotDisk(t *testing.T) {
	s, conv, f := bucketStore(t)
	f.AccessKey = "someone-else"
	_, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("hello"))
	if fe, ok := asFileStoreError(err); !ok || fe.code != codeFileStoreMisconfigured {
		t.Fatalf("err = %v", err)
	}
	if d := attachmentDiskFiles(t, s); len(d) != 0 {
		t.Fatalf("disk: %v", d)
	}
}

func TestObjRowNeverReadsDisk(t *testing.T) {
	s, conv, f := bucketStore(t)
	a, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("in the bucket"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := s.Attachment(a.ID)
	root, _ := s.attachmentRoot()
	for _, name := range []string{a.ID + ".upload", filepath.Base(path), a.ID} {
		os.WriteFile(filepath.Join(root, name), []byte("decoy on disk"), 0600)
	}
	if got := readAll(t, s, path); string(got) != "in the bucket" {
		t.Fatalf("read %q", got)
	}
	for _, k := range f.Keys() {
		f.Delete(k)
	}
	_, err = s.openAttachment(context.Background(), path)
	if fe, ok := asFileStoreError(err); !ok || fe.code != codeFileMissingInStore || fe.status != 404 {
		t.Fatalf("missing object: %v", err)
	}
	// And through HTTP.
	srv := &Server{store: s}
	w := httptest.NewRecorder()
	srv.routeAttachments(w, httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil), "attachments/"+a.ID)
	if w.Code != 404 || !strings.Contains(w.Body.String(), codeFileMissingInStore) || strings.Contains(w.Body.String(), "decoy") {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	f.Down = true
	w = httptest.NewRecorder()
	srv.routeAttachments(w, httptest.NewRequest("GET", "/api/attachments/"+a.ID, nil), "attachments/"+a.ID)
	if w.Code != 503 || !strings.Contains(w.Body.String(), codeFileStoreUnavailable) {
		t.Fatalf("download while down: %d %s", w.Code, w.Body.String())
	}
}

func TestDiskRowNeverReadsBucket(t *testing.T) {
	s, conv, f := bucketStore(t)
	// New files go to the disk while a bucket is configured for reading.
	b, _ := s.fileBackend(context.Background())
	b.Kind = fileStoreKindDisk
	s.files = func(context.Context) (FileBackend, error) { return b, nil }
	a, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("on disk"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := s.Attachment(a.ID)
	if strings.HasPrefix(path, objPrefix) || len(f.Keys()) != 0 {
		t.Fatalf("kind disk wrote %q, bucket %v", path, f.Keys())
	}
	f.Set("tofi/v1/inst-1/accounts/"+testAccount+"/uploads/"+a.ID, []byte("decoy in bucket"))
	f.Set(a.ID+".upload", []byte("decoy in bucket"))
	if got := readAll(t, s, path); string(got) != "on disk" {
		t.Fatalf("read %q", got)
	}
	os.Remove(path)
	before := len(f.Paths)
	if _, err = s.openAttachment(context.Background(), path); err == nil {
		t.Fatal("a missing disk file must not be found in the bucket")
	}
	if len(f.Paths) != before {
		t.Fatalf("a disk row asked the bucket: %v", f.Paths[before:])
	}
}

func TestBucketQuotaRefusalMakesNoBucketRequest(t *testing.T) {
	s, conv, f := bucketStore(t)
	limit := int64(1)
	s.storage = storageGate{limit: func() *int64 { return &limit }, minFree: -1}
	_, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("more than one byte"))
	if code := storageErrorCode(err); code != "storage_quota_exceeded" {
		t.Fatalf("err = %v", err)
	}
	if len(f.Paths) != 0 {
		t.Fatalf("bucket was asked: %v", f.Paths)
	}
}

func TestBucketPutThenInsertFailureRemovesTheObject(t *testing.T) {
	s, conv, f := bucketStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_attachment BEFORE INSERT ON attachments BEGIN SELECT RAISE(ABORT,'refused'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("hello")); err == nil {
		t.Fatal("insert should fail")
	}
	if k := f.Keys(); len(k) != 0 {
		t.Fatalf("orphan object: %v", k)
	}
}

func TestBucketDeletionWaitsOutTheGrace(t *testing.T) {
	s, conv, f := bucketStore(t)
	a, err := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := s.Attachment(a.ID)
	if err = s.deleteUnboundAttachment(a.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.Keys()) != 1 {
		t.Fatal("the object went at once; it must wait out the grace")
	}
	var after string
	s.db.QueryRow(`SELECT delete_after FROM deleted_attachment_files WHERE disk_name=?`, path).Scan(&after)
	due, _ := time.Parse(time.RFC3339, after)
	if d := time.Until(due); d < 13*24*time.Hour || d > 14*24*time.Hour+time.Minute {
		t.Fatalf("delete_after %q is %v away", after, d)
	}
	s.cleanupDeletedAttachments()
	if len(f.Keys()) != 1 {
		t.Fatal("cleanup before the deadline removed the object")
	}
	s.db.Exec(`UPDATE deleted_attachment_files SET delete_after=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339))
	f.Down = true
	s.cleanupDeletedAttachments()
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files`).Scan(&n)
	if n != 1 || len(f.Keys()) != 1 {
		t.Fatal("a failed delete must stay journaled")
	}
	f.Down = false
	s.cleanupDeletedAttachments()
	s.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files`).Scan(&n)
	if n != 0 || len(f.Keys()) != 0 {
		t.Fatalf("after the grace: journal=%d objects=%v", n, f.Keys())
	}
}

func TestBucketConversationDeleteJournalsWithGrace(t *testing.T) {
	s, _, f := bucketStore(t)
	b, _ := s.CreateBot("doomed", "", "model")
	c, _ := s.GetConversation(b.DMConversationID)
	if _, err := s.AddAttachment(c.ID, "a.txt", "text/plain", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteBot(b.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files WHERE delete_after IS NOT NULL`).Scan(&n)
	if n != 1 || len(f.Keys()) != 1 {
		t.Fatalf("journal with grace=%d objects=%v", n, f.Keys())
	}
}

func TestPortableExportImportAcrossBackends(t *testing.T) {
	src, conv, _ := bucketStore(t)
	m, _, _ := src.AddMessage(conv, "user", "", "", "hi", "c-1")
	a, err := src.AddAttachment(conv, "note.txt", "text/plain", strings.NewReader("Synthetic bytes 中文"))
	if err != nil {
		t.Fatal(err)
	}
	if err = src.BindAttachments(conv, m.ID, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	b, err := src.exportPortable(context.Background(), "src-instance", portableSelection{}, "account")
	if err != nil || len(b.Attachments) != 1 || b.AttachmentCount != 0 {
		t.Fatalf("export: %+v %v", b.MissingAttachments, err)
	}
	// Import into a bucket store and into a disk store: same bytes, each in its own place.
	for _, toBucket := range []bool{false, true} {
		var dst *Store
		var f *fakes3.Server
		if toBucket {
			dst, _, f = bucketStore(t)
		} else {
			dst, err = OpenStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer dst.Close()
		}
		p, err := dst.previewPortable(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		res, err := dst.applyPortable(context.Background(), b, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		_, path, err := dst.Attachment(res.IDMap[a.ID])
		if err != nil || strings.HasPrefix(path, objPrefix) != toBucket {
			t.Fatalf("toBucket=%v path=%q %v", toBucket, path, err)
		}
		if got := readAll(t, dst, path); string(got) != "Synthetic bytes 中文" {
			t.Fatalf("imported bytes %q", got)
		}
		if toBucket {
			if !strings.Contains(path, "/imports/") || len(attachmentDiskFiles(t, dst)) != 0 || len(f.Keys()) != 1 {
				t.Fatalf("path=%q disk=%v keys=%v", path, attachmentDiskFiles(t, dst), f.Keys())
			}
		}
	}
}

func TestPortableExportBucketDownListsUnavailable(t *testing.T) {
	s, conv, f := bucketStore(t)
	m, _, _ := s.AddMessage(conv, "user", "", "", "hi", "c-1")
	a, _ := s.AddAttachment(conv, "note.txt", "text/plain", strings.NewReader("hello"))
	s.BindAttachments(conv, m.ID, []string{a.ID})
	f.Down = true
	b, err := s.exportPortable(context.Background(), "src-instance", portableSelection{}, "account")
	if err != nil || len(b.Attachments) != 0 || len(b.MissingAttachments) != 1 || b.MissingAttachments[0].Reason != "unavailable" {
		t.Fatalf("export: %+v %v", b.MissingAttachments, err)
	}
}

// ---- the administrator's resource ----

func putFileStore(f *orgFixture, t *testing.T, srv *fakes3.Server, extra string, want int) string {
	t.Helper()
	body := `{"endpoint":"` + srv.URL + `","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"` + testAccessKeyID + `","key":"` + testSecretKey + `"` + extra + `}`
	return f.admin_(t, "PUT", "/api/admin/org/resources/file_store", body, want)
}

func TestFileStoreResourceTestsBeforeSavingAndNeverEchoesKeys(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f := newOrgFixture(t)
	srv := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(srv.Close)
	// A bad key is refused by name and nothing is saved.
	bad := `{"endpoint":"` + srv.URL + `","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"WRONGKEYWRONGKEY","key":"` + testSecretKey + `"}`
	out := f.admin_(t, "PUT", "/api/admin/org/resources/file_store", bad, 400)
	if orgErrCode(t, out) != "credentials_rejected" {
		t.Fatalf("got %s", out)
	}
	f.admin_(t, "GET", "/api/admin/org/resources/file_store", "", 404)
	all := out
	// A public bucket needs a confirmation.
	srv.Public = true
	out = putFileStore(f, t, srv, "", 400)
	if orgErrCode(t, out) != "bucket_is_public" {
		t.Fatalf("got %s", out)
	}
	srv.Public = false
	all += out
	all += putFileStore(f, t, srv, "", 200)
	all += f.admin_(t, "GET", "/api/admin/org/resources/file_store", "", 200)
	all += f.admin_(t, "GET", "/api/admin/org/resources", "", 200)
	all += f.admin_(t, "POST", "/api/admin/org/resources/file_store/test", "", 200)
	if len(srv.Keys()) != 0 {
		t.Fatalf("connection test left objects: %v", srv.Keys())
	}
	// The member side: a workspace upload goes to the bucket under the member's own prefix.
	ws, err := f.g.workspace(f.member)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := ws.store.CreateBot("b", "", "model")
	conv, _ := ws.store.GetConversation(bot.DMConversationID)
	a, err := ws.store.AddAttachment(conv.ID, "x.txt", "text/plain", strings.NewReader("member file"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := ws.store.Attachment(a.ID)
	if !strings.Contains(path, "/accounts/"+f.member.ID+"/uploads/") || !strings.Contains(path, "/"+f.g.root.instance.ID+"/") {
		t.Fatalf("key %q", path)
	}
	// A bucket failure now marks the resource on the card.
	srv.Down = true
	if _, err = ws.store.AddAttachment(conv.ID, "y.txt", "text/plain", strings.NewReader("more")); err == nil {
		t.Fatal("expected an error")
	}
	var status, code string
	for i := 0; i < 50; i++ {
		f.g.root.store.db.QueryRow(`SELECT status,error_code FROM org_resources WHERE id='file_store'`).Scan(&status, &code)
		if status == orgStatusRejected {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status != orgStatusRejected || code != "endpoint_unreachable" {
		t.Fatalf("resource status %q %q", status, code)
	}
	srv.Down = false
	all += f.admin_(t, "POST", "/api/admin/org/resources/file_store/test", "", 200)
	all += logs.String()
	for _, secret := range []string{testSecretKey, testAccessKeyID, "X-Amz-Signature", "X-Amz-Credential", "Signature="} {
		if strings.Contains(all, secret) {
			t.Fatalf("%q reached a response or the log", secret)
		}
	}
	// Members cannot see the resource at all.
	if code, _ := f.call(t, "GET", "/api/admin/org/resources/file_store", "", f.mc); code != 403 && code != 404 {
		t.Fatalf("member read: %d", code)
	}
	// While rows live in the bucket it cannot be removed or moved.
	f.admin_(t, "DELETE", "/api/admin/org/resources/file_store", "", 409)
	other := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(other.Close)
	if out := putFileStore(f, t, other, "", 409); orgErrCode(t, out) != "file_store_in_use" {
		t.Fatalf("got %s", out)
	}
	// Rotating the key at the same address is fine; so is sending new files back to the disk.
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"key":"`+testSecretKey+`","access_key_id":"`+testAccessKeyID+`"}`, 200)
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"disk"}`, 200)
	a2, err := ws.store.AddAttachment(conv.ID, "z.txt", "text/plain", strings.NewReader("on disk now"))
	if err != nil {
		t.Fatal(err)
	}
	if _, p2, _ := ws.store.Attachment(a2.ID); strings.HasPrefix(p2, objPrefix) {
		t.Fatalf("kind disk still wrote to the bucket: %q", p2)
	}
	if got := readAll(t, ws.store, path); string(got) != "member file" {
		t.Fatalf("old object unreadable after the switch: %q", got)
	}
}

func TestAccountDeleteFilesStepRemovesOnlyThatAccountsObjects(t *testing.T) {
	f := newOrgFixture(t)
	srv := fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(srv.Close)
	putFileStore(f, t, srv, "", 200)
	for _, a := range []Account{f.admin, f.member} {
		ws, err := f.g.workspace(a)
		if err != nil {
			t.Fatal(err)
		}
		bot, _ := ws.store.CreateBot("b", "", "model")
		conv, _ := ws.store.GetConversation(bot.DMConversationID)
		if _, err = ws.store.AddAttachment(conv.ID, "x.txt", "text/plain", strings.NewReader("data of "+a.Username)); err != nil {
			t.Fatal(err)
		}
	}
	if len(srv.Keys()) != 2 {
		t.Fatalf("keys %v", srv.Keys())
	}
	srv.Down = true
	if err := f.g.deleteStep(context.Background(), "files", f.member); err == nil {
		t.Fatal("a bucket that is down must fail the step so it can be retried")
	}
	srv.Down = false
	if err := f.g.deleteStep(context.Background(), "files", f.member); err != nil {
		t.Fatal(err)
	}
	keys := srv.Keys()
	if len(keys) != 1 || !strings.Contains(keys[0], "/accounts/"+f.admin.ID+"/") {
		t.Fatalf("after the step: %v", keys)
	}
	if err := f.g.deleteStep(context.Background(), "files", f.member); err != nil {
		t.Fatalf("the step is repeatable: %v", err)
	}
}

func TestFileStoreCloudKeyOutOfScopeIsRefused(t *testing.T) {
	s, conv, _ := bucketStore(t)
	a, _ := s.AddAttachment(conv, "a.txt", "text/plain", strings.NewReader("x"))
	s.db.Exec(`UPDATE attachments SET disk_name=? WHERE id=?`, objPrefix+"tofi/v1/inst-1/accounts/someone-else/uploads/"+a.ID, a.ID)
	_, path, _ := s.Attachment(a.ID)
	_, err := s.openAttachment(context.Background(), path)
	if fe, ok := asFileStoreError(err); !ok || fe.code != codeFileStoreOutOfScope || errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMigrationAddsStoreColumnsToAnOldDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`ALTER TABLE attachments DROP COLUMN store`)
	s.db.Exec(`ALTER TABLE deleted_attachment_files DROP COLUMN delete_after`)
	s.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, q := range []string{`SELECT store FROM attachments LIMIT 1`, `SELECT delete_after FROM deleted_attachment_files LIMIT 1`} {
		if rows, err := s.db.Query(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		} else {
			rows.Close()
		}
	}
}
