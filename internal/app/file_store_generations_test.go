package app

import (
	"encoding/json"
	"strings"
	"testing"

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
	if v, _ := f.g.orgSecrets(); v != nil {
		if _, ok := secretSource(v).secret(fileStoreGenVaultID("g1"), orgFileStoreKind); ok {
			t.Fatal("the retired generation's key is still stored")
		}
	}
	// Still unretirable twice, and the current bucket is still readable.
	f.admin_(t, "POST", "/api/admin/file-move/retire", `{"gen":"g1"}`, 404)
	f.checkRows(t, true)
}
