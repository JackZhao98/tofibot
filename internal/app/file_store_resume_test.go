package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

func (f *moveFixture) waitReport(t *testing.T) *fileReport {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if v := f.view(t); v.Report != nil && v.Report.Status != "running" {
			f.waitIdle(t)
			return v.Report
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the report never finished")
	return nil
}

// The sweep report's `deleted` counts orphaned files only. A stale alias probe it removes on the
// way is TOFI's own 16-byte object, counted apart, so an administrator is never told a file of
// theirs was deleted when none was.
func TestSweepReportCountsProbesApartFromFiles(t *testing.T) {
	f := newMoveFixture(t, 0)
	f.srv.SetAt(readConfig(t, f).Prefix+"v1/"+f.g.root.instance.ID+"/_probe/old", []byte("p"), time.Now().Add(-48*time.Hour))
	f.admin_(t, "POST", "/api/admin/file-move/sweep", `{"dry_run":false}`, 202)
	rep := f.waitReport(t)
	if rep.Status != "done" || rep.Deleted != 0 || rep.ProbesDeleted != 1 || len(f.srv.Keys()) != 0 {
		t.Fatalf("report %+v, bucket %v", rep, f.srv.Keys())
	}
}

// A to_generation move between two buckets with the same prefix writes every file under the same
// key it had. When the process dies after the put and before the row switches, the job resumes at
// start, adopts the object already there, and the cleanup later removes the old bucket's copy only:
// the journal names the generation, so the same key in the new bucket is never touched.
func TestInterruptedSamePrefixGenerationMoveResumes(t *testing.T) {
	quietPeriodOff(t)
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	if cfg := readConfig(t, f); cfg.Prefix != cfg.Prev[0].Prefix {
		t.Fatalf("prefixes differ: %q / %q", cfg.Prefix, cfg.Prev[0].Prefix)
	}
	var first string
	f.ws.store.db.QueryRow(`SELECT id FROM attachments ORDER BY id LIMIT 1`).Scan(&first)
	f.ws.store.moveStepHook = func(step string) error {
		if step == "after_put" {
			return context.Canceled
		}
		return nil
	}
	if _, err := f.ws.store.moveNextFile(context.Background(), moveToGeneration, ""); err == nil {
		t.Fatal("the step was not interrupted")
	}
	f.ws.store.moveStepHook = nil
	var disk string
	f.ws.store.db.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, first).Scan(&disk)
	_, key := objectRef(disk)
	if strings.Contains(disk, "|") || len(second.Keys()) != 1 || second.Keys()[0] != key {
		t.Fatalf("row %q, new bucket %v: the copy must exist under the same key and the row must not have switched", disk, second.Keys())
	}
	f.g.saveMoveJob(&fileMoveJob{Direction: moveToGeneration, Status: moveRunning, FilesTotal: 2, StartedAt: now()})
	f.g.startFileMove()
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 2 || v.Failures.Count != 0 {
		t.Fatalf("job %+v failures %+v", v.Job, v.Failures)
	}
	for id := range f.bodies {
		if _, p, _ := f.ws.store.Attachment(id); !strings.HasPrefix(p, "obj:g2|") {
			t.Fatalf("row %s is at %q", id, p)
		}
	}
	oldKeys := f.srv.Keys()
	if _, err := f.ws.store.db.Exec(`UPDATE deleted_attachment_files SET delete_after='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	f.ws.store.cleanupDeletedAttachments()
	if len(f.srv.Keys()) != 0 || len(second.Keys()) != 2 {
		t.Fatalf("after cleanup: old %v (was %v), new %v", f.srv.Keys(), oldKeys, second.Keys())
	}
	f.checkRows(t, true)
}
