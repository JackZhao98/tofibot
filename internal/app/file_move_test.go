package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

// moveFixture has a member workspace with disk files and a File storage resource on a fake bucket.
type moveFixture struct {
	*orgFixture
	srv    *fakes3.Server
	ws     *Server
	bodies map[string][]byte // attachment id -> bytes
}

func newMoveFixture(t *testing.T, files int) *moveFixture {
	t.Helper()
	f := &moveFixture{orgFixture: newOrgFixture(t), bodies: map[string][]byte{}}
	f.srv = fakes3.New("bkt", testAccessKeyID)
	t.Cleanup(f.srv.Close)
	ws, err := f.g.workspace(f.member)
	if err != nil {
		t.Fatal(err)
	}
	f.ws = ws
	bot, err := ws.store.CreateBot("synthetic", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < files; i++ {
		body := bytes.Repeat([]byte{byte('a' + i)}, 100+i)
		a, err := ws.store.AddAttachment(bot.DMConversationID, "note.txt", "text/plain", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		f.bodies[a.ID] = body
	}
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"s3","endpoint":"`+f.srv.URL+`","region":"us-east-1","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`","delete_grace_days":1}`, 200)
	return f
}

type moveView struct {
	Disk, Bucket struct{ Files, Bytes int64 }
	Job          *fileMoveJob
	Failures     struct{ Count int64 }
	Report       *fileReport
}

func (f *moveFixture) view(t *testing.T) moveView {
	t.Helper()
	var v moveView
	if err := json.Unmarshal([]byte(f.admin_(t, "GET", "/api/admin/file-move", "", 200)), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *moveFixture) waitJob(t *testing.T, statuses ...string) moveView {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		v := f.view(t)
		if v.Job != nil {
			for _, s := range statuses {
				if v.Job.Status == s {
					return v
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job never reached %v: %+v", statuses, f.view(t).Job)
	return moveView{}
}

// waitIdle waits for the job goroutine to release the slot, so the test reads settled state.
func (f *moveFixture) waitIdle(t *testing.T) {
	t.Helper()
	for i := 0; i < 500 && f.g.fileJobKind() != ""; i++ {
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *moveFixture) checkRows(t *testing.T, wantObj bool) {
	t.Helper()
	for id, body := range f.bodies {
		_, path, err := f.ws.store.Attachment(id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, objPrefix) != wantObj {
			t.Fatalf("row %s is at %q, want bucket=%v", id, path, wantObj)
		}
		if !bytes.Equal(readAll(t, f.ws.store, path), body) {
			t.Fatalf("bytes of %s changed", id)
		}
		var digest string
		f.ws.store.db.QueryRow(`SELECT COALESCE(sha256,'') FROM attachments WHERE id=?`, id).Scan(&digest)
		if digest != sha(body) {
			t.Fatalf("sha256 of %s is %q", id, digest)
		}
	}
}

func TestFileMoveRoundTripKeepsEveryByte(t *testing.T) {
	f := newMoveFixture(t, 3)
	root, _ := f.ws.store.attachmentRoot()
	f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_bucket"}`, 202)
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 3 || v.Job.FilesTotal != 3 || v.Failures.Count != 0 {
		t.Fatalf("job %+v failures %d", v.Job, v.Failures.Count)
	}
	f.checkRows(t, true)
	if left := attachmentDiskFiles(t, f.ws.store); len(left) != 0 {
		t.Fatalf("disk files left after the move: %v", left)
	}
	if len(f.srv.Keys()) != 3 {
		t.Fatalf("bucket holds %v", f.srv.Keys())
	}
	// Back: refused while new files still go to the bucket, then allowed once they go to disk.
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_disk"}`, 409)); code != "destination_is_bucket" {
		t.Fatalf("code %s", code)
	}
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"disk"}`, 200)
	f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_disk"}`, 202)
	v = f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Direction != moveToDisk || v.Job.Status != moveDone || v.Job.FilesDone != 3 {
		t.Fatalf("job %+v", v.Job)
	}
	f.checkRows(t, false)
	for id := range f.bodies {
		if _, err := os.Stat(filepath.Join(root, id+".upload")); err != nil {
			t.Fatal(err)
		}
	}
	// The objects wait out the deletion grace in the journal.
	var journaled int
	f.ws.store.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files WHERE disk_name LIKE 'obj:%' AND delete_after IS NOT NULL`).Scan(&journaled)
	if journaled != 3 || len(f.srv.Keys()) != 3 {
		t.Fatalf("journaled %d, bucket %v", journaled, f.srv.Keys())
	}
}

func TestFileMoveBucketOutagePausesThenResumes(t *testing.T) {
	f := newMoveFixture(t, 2)
	f.srv.Down = true
	f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_bucket"}`, 202)
	v := f.waitJob(t, moveStoreDown)
	f.waitIdle(t)
	if v.Failures.Count != 0 || v.Job.FilesDone != 0 {
		t.Fatalf("an outage must not blame files: %+v", v)
	}
	f.checkRows(t, false)
	f.srv.Down = false
	f.admin_(t, "POST", "/api/admin/file-move/resume", `{}`, 202)
	f.waitJob(t, moveDone)
	f.waitIdle(t)
	f.checkRows(t, true)
}

func TestFileMoveResumesAfterARestart(t *testing.T) {
	f := newMoveFixture(t, 3)
	// A process that died left the job `running` with one file already moved.
	root, _ := f.ws.store.attachmentRoot()
	var first string
	f.ws.store.db.QueryRow(`SELECT id FROM attachments ORDER BY id LIMIT 1`).Scan(&first)
	if moved, err := f.ws.store.moveNextFile(context.Background(), moveToBucket, ""); err != nil || !moved.Found || moved.ID != first {
		t.Fatalf("first move: %+v %v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(root, first+".upload")); !os.IsNotExist(err) {
		t.Fatalf("moved file still on disk: %v", err)
	}
	f.g.saveMoveJob(&fileMoveJob{Direction: moveToBucket, Status: moveRunning, FilesTotal: 3, FilesDone: 1, StartedAt: now()})
	f.g.startFileMove()
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 3 {
		t.Fatalf("job %+v", v.Job)
	}
	f.checkRows(t, true)
}

func TestOrphanSweepDeletesOnlyUnreferencedOldObjects(t *testing.T) {
	s, conv, f := bucketStore(t)
	a, err := s.addAttachment(context.Background(), conv, "kept.txt", "text/plain", strings.NewReader("referenced"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := s.Attachment(a.ID)
	prefix := "tofi/v1/inst-1/accounts/" + testAccount + "/uploads/"
	old, young, queued := prefix+"orphan-old", prefix+"orphan-young", prefix+"queued-old"
	long := time.Now().Add(-30 * 24 * time.Hour)
	f.SetAt(old, []byte("x"), long)
	f.SetAt(queued, []byte("y"), long)
	f.Set(young, []byte("z"))
	if _, err = s.db.Exec(`INSERT INTO deleted_attachment_files(disk_name,delete_after) VALUES(?,?)`, objPrefix+queued, now()); err != nil {
		t.Fatal(err)
	}
	has := func(k string) bool { _, _, ok := f.Object(k); return ok }
	ctx := context.Background()
	dry, err := s.scanFiles(ctx, scanOpts{Sweep: true, DryRun: true})
	if err != nil || dry.Orphans != 1 || dry.OrphansYoung != 1 || dry.Deleted != 0 || !has(old) {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	real, err := s.scanFiles(ctx, scanOpts{Sweep: true})
	if err != nil || real.Deleted != 1 || has(old) || !has(young) || !has(queued) || !has(strings.TrimPrefix(path, objPrefix)) {
		t.Fatalf("sweep: %+v %v", real, err)
	}
	// Reconciliation sees a missing object.
	f.Delete(strings.TrimPrefix(path, objPrefix))
	rep, err := s.scanFiles(ctx, scanOpts{})
	if err != nil || rep.MissingInStore != 1 {
		t.Fatalf("report: %+v %v", rep, err)
	}
}
