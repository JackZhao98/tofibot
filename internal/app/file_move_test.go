package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	Failures     struct {
		Count  int64
		ByCode map[string]int64 `json:"by_code"`
	}
	Report *fileReport
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
	root, _ := f.ws.store.attachmentRoot()
	var first string
	f.ws.store.db.QueryRow(`SELECT id FROM attachments ORDER BY id LIMIT 1`).Scan(&first)
	// The process "dies" after the first file is written and read back, before its row switches.
	f.ws.store.moveStepHook = func(step string) error {
		if step == "after_put" {
			return context.Canceled
		}
		return nil
	}
	if _, err := f.ws.store.moveNextFile(context.Background(), moveToBucket, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted step: %v", err)
	}
	f.ws.store.moveStepHook = nil
	var disk string
	f.ws.store.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, first).Scan(&disk)
	if disk != first+".upload" || len(f.srv.Keys()) != 1 {
		t.Fatalf("row %q, bucket %v: the object must exist and the row must not have switched", disk, f.srv.Keys())
	}
	if _, err := os.Stat(filepath.Join(root, first+".upload")); err != nil {
		t.Fatal(err)
	}
	f.g.saveMoveJob(&fileMoveJob{Direction: moveToBucket, Status: moveRunning, FilesTotal: 3, StartedAt: now()})
	f.g.startFileMove() // resume adopts the object that is already there
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 3 || len(f.srv.Keys()) != 3 {
		t.Fatalf("job %+v, bucket %v", v.Job, f.srv.Keys())
	}
	f.checkRows(t, true)
}

func moveAll(t *testing.T, s *Store, dir string) {
	t.Helper()
	after := ""
	for {
		res, err := s.moveNextFile(context.Background(), dir, after)
		if err != nil || res.Code != "" {
			t.Fatalf("move %s: %+v %v", dir, res, err)
		}
		if !res.Found {
			return
		}
		after = res.ID
	}
}

// 1: a backup and a file step never overlap.
func TestBackupAndFileStepExcludeEachOther(t *testing.T) {
	f := newMoveFixture(t, 1)
	old := fileStepPoll
	fileStepPoll = 10 * time.Millisecond
	t.Cleanup(func() { fileStepPoll = old })
	// A running backup holds the step back.
	f.g.backupBusy.Store(true)
	ran := make(chan struct{})
	go func() { _ = f.g.withFileStep(context.Background(), func() error { close(ran); return nil }) }()
	select {
	case <-ran:
		t.Fatal("a file step started while a backup was running")
	case <-time.After(150 * time.Millisecond):
	}
	f.g.backupBusy.Store(false)
	select {
	case <-ran:
	case <-time.After(3 * time.Second):
		t.Fatal("the step never ran after the backup ended")
	}
	// A starting backup waits for a step in flight.
	f.g.fileStepMu.Lock()
	got := make(chan bool, 1)
	go func() { got <- f.g.beginBackupJob() }()
	select {
	case <-got:
		t.Fatal("a backup captured while a file step was in flight")
	case <-time.After(150 * time.Millisecond):
	}
	f.g.fileStepMu.Unlock()
	select {
	case ok := <-got:
		if !ok {
			t.Fatal("slot busy")
		}
		f.g.endBackupJob()
	case <-time.After(3 * time.Second):
		t.Fatal("the backup never started after the step ended")
	}
}

// 2: a journal entry left by an earlier move must not follow the file back.
func TestMoveBackClearsStaleJournalEntries(t *testing.T) {
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	moveAll(t, f.ws.store, moveToDisk)
	// to_disk journaled the objects with the grace deadline; let those deadlines pass.
	if _, err := f.ws.store.db.Exec(`UPDATE deleted_attachment_files SET delete_after='2000-01-01T00:00:00Z' WHERE disk_name LIKE 'obj:%'`); err != nil {
		t.Fatal(err)
	}
	// And a stale disk-side entry for each file, as an earlier to_bucket could have left.
	for id := range f.bodies {
		f.ws.store.db.Exec(`INSERT OR IGNORE INTO deleted_attachment_files(disk_name) VALUES(?)`, id+".upload")
	}
	moveAll(t, f.ws.store, moveToBucket) // adopts the objects that are still there
	var objEntries int
	f.ws.store.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files WHERE disk_name LIKE 'obj:%'`).Scan(&objEntries)
	if objEntries != 0 {
		t.Fatalf("%d stale journal entries for live objects", objEntries)
	}
	f.ws.store.cleanupDeletedAttachments()
	if len(f.srv.Keys()) != 2 {
		t.Fatalf("cleanup removed a live object: %v", f.srv.Keys())
	}
	f.checkRows(t, true)
	// The other way: the entry for the disk file just written goes away in the switch.
	moveAll(t, f.ws.store, moveToDisk)
	var diskEntries int
	f.ws.store.db.QueryRow(`SELECT COUNT(*) FROM deleted_attachment_files WHERE disk_name LIKE '%.upload'`).Scan(&diskEntries)
	if diskEntries != 0 {
		t.Fatalf("%d stale journal entries for live disk files", diskEntries)
	}
	f.checkRows(t, false)
}

// 3: accounts the job cannot walk keep it from reporting done; disabled accounts are moved.
func TestMoveCoversDisabledAccountsAndReportsSkippedOnes(t *testing.T) {
	f := newMoveFixture(t, 2)
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"s3"}`, 200)
	run := func() moveView {
		f.g.saveMoveJob(&fileMoveJob{Direction: moveToBucket, Status: moveRunning, FilesTotal: 2, StartedAt: now()})
		f.g.root.store.db.Exec(`DELETE FROM file_move_items`)
		ctx, ok := f.g.startFileJob("move")
		if !ok {
			t.Fatal("slot busy")
		}
		f.g.runFileMove(ctx)
		return f.view(t)
	}
	f.g.mu.Lock()
	f.g.restoring = map[string]bool{f.member.ID: true}
	f.g.mu.Unlock()
	v := run()
	if v.Job.Status != moveDoneWithError || v.Failures.ByCode[codeAccountRestoring] != 1 {
		t.Fatalf("a restoring account must not end the job as done: %+v %+v", v.Job, v.Failures)
	}
	f.g.mu.Lock()
	delete(f.g.restoring, f.member.ID)
	f.g.mu.Unlock()
	if _, err := f.g.root.store.db.Exec(`UPDATE accounts SET disabled=1 WHERE id=?`, f.member.ID); err != nil {
		t.Fatal(err)
	}
	if files, _, err := f.g.moveTotals(context.Background(), moveToBucket); err != nil || files != 2 {
		t.Fatalf("totals skip a disabled account: %d %v", files, err)
	}
	v = run()
	if v.Job.Status != moveDone || v.Job.FilesDone != 2 || v.Failures.Count != 0 {
		t.Fatalf("job %+v failures %+v", v.Job, v.Failures)
	}
	f.checkRows(t, true)
}

// 4: a file the deep check cannot read is counted, not dropped.
func TestDeepReconcileCountsUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file permissions do not stop root")
	}
	s, conv, _ := attachmentFixture(t)
	a, err := s.addAttachment(context.Background(), conv, "x.txt", "text/plain", strings.NewReader("unreadable soon"))
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.attachmentRoot()
	path := filepath.Join(root, a.ID+".upload")
	if err = os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	rep, err := s.scanFiles(context.Background(), scanOpts{Deep: true})
	if err != nil || rep.Unreadable != 1 || rep.ShaMismatch != 0 {
		t.Fatalf("report %+v %v", rep, err)
	}
}

// 5: cancel is final, and resume obeys the same rules as start.
func TestMoveCancelAfterPauseIsFinal(t *testing.T) {
	f := newMoveFixture(t, 1)
	job := fileMoveJob{Direction: moveToBucket, Status: moveRunning, FilesTotal: 1, StartedAt: now()}
	f.g.saveMoveJob(&job)
	ctx, ok := f.g.startFileJob("move") // stands in for a runner that is mid-file
	if !ok {
		t.Fatal("slot busy")
	}
	f.admin_(t, "POST", "/api/admin/file-move/pause", `{}`, 202)
	f.admin_(t, "POST", "/api/admin/file-move/cancel", `{}`, 202)
	// The runner notices the pause (the first cause) and settles; it must not undo the cancel.
	f.g.settleMoveJob(ctx, &job, ctx.Err())
	f.g.saveRunnerJob(&job, nil)
	f.g.endFileJob()
	if cur, _ := f.g.loadMoveJob(); cur.Status != moveCancelled {
		t.Fatalf("status %q after pause then cancel", cur.Status)
	}
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/resume", `{}`, 409)); code != "no_paused_move" {
		t.Fatalf("resume after cancel: %s", code)
	}
	// Resume of a paused job re-checks the destination.
	f.g.saveMoveJob(&fileMoveJob{Direction: moveToBucket, Status: movePaused, FilesTotal: 1, StartedAt: now()})
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"disk"}`, 200)
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/resume", `{}`, 409)); code != "destination_is_disk" {
		t.Fatalf("resume with the wrong destination: %s", code)
	}
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
