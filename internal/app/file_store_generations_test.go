package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/filestore"
	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
)

func putSecondBucket(f *moveFixture, t *testing.T, srv *fakes3.Server, bucket string, want int) string {
	t.Helper()
	return f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"s3","endpoint":"`+srv.URL+`","region":"us-east-1","bucket":"`+bucket+`","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, want)
}

// A changed bucket is a new generation: the old rows stay readable from their own bucket,
// new files go to the new one, a to_generation move copies the rest, and only then can the
// old generation be retired.
func TestBucketGenerationsMoveAndRetire(t *testing.T) {
	quietPeriodOff(t)
	f := newMoveFixture(t, 3)
	moveAll(t, f.ws.store, moveToBucket)
	f.checkRows(t, true)
	// The bucket cannot be removed while rows live in it.
	f.admin_(t, "DELETE", "/api/admin/org/resources/file_store", "", 409)

	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	var cfg fileStoreConfig
	row, _, _ := f.g.getOrgResource(t.Context(), orgFileStoreID)
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil || cfg.Gen != "g2" || len(cfg.Prev) != 1 || cfg.Prev[0].Gen != "g1" || cfg.Prev[0].Bucket != "bkt" {
		t.Fatalf("config %s %v", row.Config, err)
	}
	// Old rows are still read from the old bucket; the new one is empty.
	f.checkRows(t, true)
	if len(second.Keys()) != 0 || len(f.srv.Keys()) != 3 {
		t.Fatalf("buckets hold %v / %v", second.Keys(), f.srv.Keys())
	}
	// New files are written to g2 and named by it.
	bot, _ := f.ws.store.CreateBot("synthetic-2", "", "model")
	a, err := f.ws.store.AddAttachment(bot.DMConversationID, "new.txt", "text/plain", strings.NewReader("fresh"))
	if err != nil {
		t.Fatal(err)
	}
	_, path, _ := f.ws.store.Attachment(a.ID)
	if !strings.HasPrefix(path, "obj:g2|") || len(second.Keys()) != 1 || len(f.srv.Keys()) != 3 || string(readAll(t, f.ws.store, path)) != "fresh" {
		t.Fatalf("new file at %q, buckets %v / %v", path, second.Keys(), f.srv.Keys())
	}
	// The earlier bucket's address cannot come back as a new generation.
	if code := orgErrCode(t, putSecondBucket(f, t, f.srv, "bkt", 409)); code != "file_store_location_in_use" {
		t.Fatalf("code %s", code)
	}
	// Retiring with rows in it is refused by name.
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 409)); code != "file_store_generation_in_use" {
		t.Fatalf("code %s", code)
	}

	// Move g1 -> g2 directly, bytes and sha256 intact.
	f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_generation"}`, 202)
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 3 || v.Failures.Count != 0 {
		t.Fatalf("job %+v failures %d", v.Job, v.Failures.Count)
	}
	f.checkRows(t, true)
	for id := range f.bodies {
		_, p, _ := f.ws.store.Attachment(id)
		if !strings.HasPrefix(p, "obj:g2|") {
			t.Fatalf("row %s is at %q", id, p)
		}
	}
	if len(second.Keys()) != 4 {
		t.Fatalf("new bucket holds %v", second.Keys())
	}
	// The source objects wait out the grace, so retiring is refused until the cleanup ran.
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 409)); code != "generation_cleanup_pending" {
		t.Fatalf("code %s", code)
	}
	if _, err := f.ws.store.db.Exec(`UPDATE deleted_attachment_files SET delete_after='2000-01-01T00:00:00Z' WHERE disk_name LIKE 'obj:%'`); err != nil {
		t.Fatal(err)
	}
	f.ws.store.cleanupDeletedAttachments()
	if len(f.srv.Keys()) != 0 || len(second.Keys()) != 4 {
		t.Fatalf("after cleanup: old %v new %v", f.srv.Keys(), second.Keys())
	}
	f.checkRows(t, true)
	f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 200)
	row, _, _ = f.g.getOrgResource(t.Context(), orgFileStoreID)
	cfg = fileStoreConfig{}
	_ = json.Unmarshal([]byte(row.Config), &cfg)
	if len(cfg.Prev) != 0 || cfg.Gen != "g2" {
		t.Fatalf("config after retire %s", row.Config)
	}
	vault, verr := f.g.orgSecrets()
	if verr != nil {
		t.Fatal(verr)
	}
	if _, ok := secretSource(vault).secret(fileStoreGenVaultID("g1"), orgFileStoreKind); ok {
		t.Fatal("the retired generation's key is still stored")
	}
	// Still unretirable twice, and the current bucket is still readable.
	f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 404)
	f.checkRows(t, true)
}

func quietPeriodOff(t *testing.T) {
	t.Helper()
	old := generationQuietPeriod
	generationQuietPeriod = 0
	t.Cleanup(func() { generationQuietPeriod = old })
}

func readConfig(t *testing.T, f *moveFixture) fileStoreConfig {
	t.Helper()
	row, _, _ := f.g.getOrgResource(context.Background(), orgFileStoreID)
	var cfg fileStoreConfig
	if err := json.Unmarshal([]byte(row.Config), &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// 1: the same physical bucket under another region or alias is an edit of that generation, never a new one.
func TestSameBucketUnderAnotherAddressIsNotANewGeneration(t *testing.T) {
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", `{"kind":"s3","endpoint":"`+f.srv.URL+`","region":"wnam","bucket":"bkt","path_style":true,"access_key_id":"`+testAccessKeyID+`","key":"`+testSecretKey+`"}`, 200)
	if cfg := readConfig(t, f); cfg.Gen != "g1" || len(cfg.Prev) != 0 || cfg.Region != "wnam" {
		t.Fatalf("config %+v", cfg)
	}
	f.checkRows(t, true)
}

// 1: the sweep never takes an object a row of ANY generation, or a pending removal, names.
func TestSweepKeepsObjectsNamedByAnyGeneration(t *testing.T) {
	s, conv, f := bucketStore(t)
	if _, err := s.addAttachmentOpts(context.Background(), conv, "a.txt", "text/plain", strings.NewReader("only copy"), attachmentOpts{Origin: attachmentOriginUpload}); err != nil {
		t.Fatal(err)
	}
	b, _ := s.fileBackend(context.Background())
	g2 := b
	g2.Gen = "g2"
	g2.Old = func(string) (filestore.Store, error) { return b.Store, nil }
	s.files = func(context.Context) (FileBackend, error) { return g2, nil }
	f.Set(b.Prefix+"uploads/journaled", []byte("x"))
	if _, err := s.db.Exec(`INSERT INTO deleted_attachment_files(disk_name,delete_after) VALUES(?,?)`, "obj:"+b.Prefix+"uploads/journaled", "2999-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scanFiles(context.Background(), scanOpts{Sweep: true, Now: time.Now().Add(60 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if len(f.Keys()) != 2 {
		t.Fatalf("the sweep removed a named object: %v", f.Keys())
	}
}

// 1: a leftover destination object of an interrupted attempt is adopted when its content matches.
func TestMoveAdoptsALeftoverDestinationObject(t *testing.T) {
	s, conv, _ := bucketStore(t)
	a, err := s.addAttachmentOpts(context.Background(), conv, "a.txt", "text/plain", strings.NewReader("leftover body"), attachmentOpts{Origin: attachmentOriginUpload})
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := s.fileBackend(context.Background())
	st2, f2 := fakeBucket(t)
	g2 := b1
	g2.Gen, g2.Store = "g2", st2
	g2.Old = func(string) (filestore.Store, error) { return b1.Store, nil }
	s.files = func(context.Context) (FileBackend, error) { return g2, nil }
	f2.Set(g2.objectKey(attachmentOriginUpload, a.ID), []byte("leftover body"))
	res, err := s.moveNextFile(context.Background(), moveToGeneration, "")
	if err != nil || res.Code != "" || !res.Found {
		t.Fatalf("move: %+v %v", res, err)
	}
	if _, p, _ := s.Attachment(a.ID); !strings.HasPrefix(p, "obj:g2|") || string(readAll(t, s, p)) != "leftover body" {
		t.Fatalf("row at %q", p)
	}
	// A leftover with other content is a named per-file failure, not adopted.
	s.files = func(context.Context) (FileBackend, error) { return b1, nil }
	a2, _ := s.addAttachmentOpts(context.Background(), conv, "b.txt", "text/plain", strings.NewReader("second"), attachmentOpts{Origin: attachmentOriginUpload})
	s.files = func(context.Context) (FileBackend, error) { return g2, nil }
	f2.Set(g2.objectKey(attachmentOriginUpload, a2.ID), []byte("different"))
	res, err = s.moveNextFile(context.Background(), moveToGeneration, "")
	if err != nil || res.Code != codeMoveKeyConflict {
		t.Fatalf("conflict: %+v %v", res, err)
	}
}

// 1: a real alias is refused when the job starts.
func TestMoveJobRefusesAnAliasedGenerationAtStart(t *testing.T) {
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	// Point the current generation at the first bucket's server too: both generations are now one bucket.
	cfg := readConfig(t, f)
	cfg.Endpoint, cfg.Bucket = f.srv.URL, "bkt"
	raw, _ := json.Marshal(cfg)
	if _, err := f.g.root.store.db.Exec(`UPDATE org_resources SET config=?,updated_at=? WHERE id='file_store'`, string(raw), now()); err != nil {
		t.Fatal(err)
	}
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_generation"}`, 409)); code != codeSamePhysicalBucket {
		t.Fatalf("code %s", code)
	}
	for _, k := range f.srv.Keys() {
		if strings.Contains(k, "_probe") {
			t.Fatalf("probe left behind: %s", k)
		}
	}
}

// 2: an earlier bucket that cannot be reached needs an explicit confirmation to change the bucket.
func TestUnreachableEarlierBucketNeedsConfirmation(t *testing.T) {
	f := newMoveFixture(t, 1)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	third := fakes3.New("bkt3", testAccessKeyID)
	t.Cleanup(third.Close)
	f.srv.Down = true
	body := func(extra string) string {
		return `{"kind":"s3","endpoint":"` + third.URL + `","region":"us-east-1","bucket":"bkt3","path_style":true,"access_key_id":"` + testAccessKeyID + `","key":"` + testSecretKey + `"` + extra + `}`
	}
	if code := orgErrCode(t, f.admin_(t, "PUT", "/api/admin/org/resources/file_store", body(""), 409)); code != "file_store_cannot_compare" {
		t.Fatalf("code %s", code)
	}
	f.admin_(t, "PUT", "/api/admin/org/resources/file_store", body(`,"confirm_unverified":true`), 200)
	if cfg := readConfig(t, f); cfg.Gen != "g3" || len(cfg.Prev) != 2 {
		t.Fatalf("config %+v", cfg)
	}
}

// 5: a save aborts by name when the resource changed between the checks and the write.
func TestSaveAbortsWhenTheResourceChangedMeanwhile(t *testing.T) {
	f := newMoveFixture(t, 0)
	other := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(other.Close)
	fileStoreBeforeLock = func() {
		f.g.root.store.db.Exec(`UPDATE org_resources SET updated_at=? WHERE id='file_store'`, now())
	}
	t.Cleanup(func() { fileStoreBeforeLock = nil })
	if code := orgErrCode(t, putSecondBucket(f, t, other, "bkt2", 409)); code != "file_store_changed" {
		t.Fatalf("code %s", code)
	}
	if cfg := readConfig(t, f); cfg.Gen != "g1" || cfg.Bucket != "bkt" {
		t.Fatalf("config changed: %+v", cfg)
	}
}

// 4: a probe left behind by a failed delete is removed by the sweep once it is a day old.
func TestSweepRemovesStaleProbes(t *testing.T) {
	f := newMoveFixture(t, 0)
	dir := readConfig(t, f).Prefix + "v1/" + f.g.root.instance.ID + "/_probe/"
	f.srv.SetAt(dir+"old", []byte("p"), time.Now().Add(-48*time.Hour))
	f.srv.SetAt(dir+"fresh", []byte("p"), time.Now())
	if n := f.g.sweepProbes(context.Background(), time.Now()); n != 1 {
		t.Fatalf("removed %d", n)
	}
	keys := f.srv.Keys()
	if len(keys) != 1 || !strings.HasSuffix(keys[0], "/fresh") {
		t.Fatalf("keys %v", keys)
	}
}

// 2: a real location change always makes a generation; retire waits for the quiet hour and staged imports.
func TestLocationChangeAlwaysMintsAGenerationAndRetireWaits(t *testing.T) {
	f := newMoveFixture(t, 0)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	if cfg := readConfig(t, f); cfg.Gen != "g2" || len(cfg.Prev) != 1 {
		t.Fatalf("config %+v", cfg)
	}
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 409)); code != "generation_busy" {
		t.Fatalf("inside the quiet hour: %s", code)
	}
	quietPeriodOff(t)
	if _, err := f.ws.store.db.Exec(`INSERT INTO portability_asset_staging(import_id,target_id,sha256,size,created_at,disk_name,store) VALUES('i','t','x',1,'now','obj:k/accounts/a/imports/t','g1')`); err != nil {
		t.Fatal(err)
	}
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 409)); code != "generation_busy" {
		t.Fatalf("with a staged import: %s", code)
	}
	f.ws.store.db.Exec(`DELETE FROM portability_asset_staging`)
	f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 200)
}

// 3: deleting an account removes its objects from every generation and fails by name when one is unreachable.
func TestAccountDeletionClearsEveryGeneration(t *testing.T) {
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	bot, _ := f.ws.store.CreateBot("synthetic-3", "", "model")
	if _, err := f.ws.store.AddAttachment(bot.DMConversationID, "n.txt", "text/plain", strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if len(f.srv.Keys()) != 2 || len(second.Keys()) != 1 {
		t.Fatalf("setup: %v / %v", f.srv.Keys(), second.Keys())
	}
	f.srv.Down = true
	if err := f.g.deleteAccountFiles(context.Background(), f.member.ID); err == nil {
		t.Fatal("an unreachable earlier bucket must fail the step")
	} else if _, named := asFileStoreError(err); !named {
		t.Fatalf("unnamed error %v", err)
	}
	f.srv.Down = false
	if err := f.g.deleteAccountFiles(context.Background(), f.member.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.srv.Keys()) != 0 || len(second.Keys()) != 0 {
		t.Fatalf("left behind: %v / %v", f.srv.Keys(), second.Keys())
	}
}

// 5: the resource cannot be removed while a journaled deletion is still pending in any generation.
func TestRemovingTheBucketWaitsForPendingDeletions(t *testing.T) {
	f := newMoveFixture(t, 0)
	if _, err := f.ws.store.db.Exec(`INSERT INTO deleted_attachment_files(disk_name,delete_after) VALUES('obj:tofi/v1/i/accounts/a/uploads/x','2999-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	f.admin_(t, "DELETE", "/api/admin/org/resources/file_store", "", 409)
	f.ws.store.db.Exec(`DELETE FROM deleted_attachment_files`)
	f.admin_(t, "DELETE", "/api/admin/org/resources/file_store", "", 200)
}

// An earlier bucket's key without list permission (s3:ListBucket) is answered 403, not 404, for the
// alias probe it cannot see. When that key still reads the files the generation holds, the 403 means
// the probe is not there: the job starts and moves everything.
func TestMoveToGenerationStartsWhenTheEarlierKeyCannotList(t *testing.T) {
	quietPeriodOff(t)
	f := newMoveFixture(t, 2)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	f.srv.NoList = true
	f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_generation"}`, 202)
	v := f.waitJob(t, moveDone, moveDoneWithError)
	f.waitIdle(t)
	if v.Job.Status != moveDone || v.Job.FilesDone != 2 {
		t.Fatalf("job %+v failures %+v", v.Job, v.Failures)
	}
	f.checkRows(t, true)
	for id := range f.bodies {
		if _, p, _ := f.ws.store.Attachment(id); !strings.HasPrefix(p, "obj:g2|") {
			t.Fatalf("row %s is at %q", id, p)
		}
	}
}

// A key the earlier bucket refuses outright cannot tell the buckets apart: the job does not start, and
// the refusal is named for what it is, not as a bucket that did not answer.
func TestMoveToGenerationNamesARefusedEarlierKey(t *testing.T) {
	f := newMoveFixture(t, 1)
	moveAll(t, f.ws.store, moveToBucket)
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	f.srv.AccessKey = "ANOTHERKEYID" // the stored g1 key is now refused for everything
	if code := orgErrCode(t, f.admin_(t, "POST", "/api/admin/file-move", `{"direction":"to_generation"}`, 409)); code != codePreviousBucketRefused {
		t.Fatalf("code %s", code)
	}
	if len(second.Keys()) != 0 {
		t.Fatalf("probe left in the current bucket: %v", second.Keys())
	}
}

// MinIO names a missing bucket in the x-minio-error-code header of a HEAD answer: an earlier bucket
// that is gone is another bucket, not a refusal.
func TestProbeAnswerReadsMinIONoSuchBucket(t *testing.T) {
	f := newMoveFixture(t, 0)
	f.srv.MinIOHeaders = true
	gone, err := filestore.NewS3(filestore.S3Config{Endpoint: f.srv.URL, Region: "us-east-1", Bucket: "gone", AccessKeyID: testAccessKeyID, Secret: testSecretKey, PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.g.probeAnswer(context.Background(), gone, "g1", "tofi/v1/x/_probe/p"); got != probeAbsent {
		t.Fatalf("answer %v", got)
	}
}

// The same 403 on a save: an earlier key that cannot list but reads its files tells the new bucket
// apart without asking the administrator to confirm an unverified change.
func TestChangingTheBucketNeedsNoConfirmationWhenTheEarlierKeyCannotList(t *testing.T) {
	f := newMoveFixture(t, 1)
	moveAll(t, f.ws.store, moveToBucket)
	f.srv.NoList = true
	second := fakes3.New("bkt2", testAccessKeyID)
	t.Cleanup(second.Close)
	putSecondBucket(f, t, second, "bkt2", 200)
	if cfg := readConfig(t, f); cfg.Gen != "g2" || len(cfg.Prev) != 1 {
		t.Fatalf("config %+v", cfg)
	}
}
