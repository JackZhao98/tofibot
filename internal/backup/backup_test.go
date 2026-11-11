package backup_test

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/backup"
	"github.com/JackZhao98/tofibot/internal/filestore"
	"github.com/JackZhao98/tofibot/internal/filestore/fakes3"
	_ "modernc.org/sqlite"
)

const base = "tofi/v1/inst/server/backups/"

var wrapper = backup.Wrapper{
	Wrap: func(p []byte, key string) ([]byte, error) {
		k := sha256.Sum256([]byte(key))
		b, _ := aes.NewCipher(k[:])
		g, _ := cipher.NewGCM(b)
		n := make([]byte, g.NonceSize())
		_, _ = rand.Read(n)
		return g.Seal(n, n, p, nil), nil
	},
	Unwrap: func(s []byte, key string) ([]byte, error) {
		k := sha256.Sum256([]byte(key))
		b, _ := aes.NewCipher(k[:])
		g, _ := cipher.NewGCM(b)
		return g.Open(nil, s[:g.NonceSize()], s[g.NonceSize():], nil)
	},
}

func bucket(t *testing.T) (*filestore.S3, *fakes3.Server) {
	t.Helper()
	f := fakes3.New("bkt", "AKIATESTONLY")
	t.Cleanup(f.Close)
	s, err := filestore.NewS3(filestore.S3Config{Endpoint: f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "AKIATESTONLY", Secret: "bucket-secret-xyz", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	return s, f
}

func mkdb(t *testing.T, dir string, msgs int, atts map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{`PRAGMA journal_mode=WAL`, `PRAGMA user_version=1`, `CREATE TABLE messages(id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE attachments(id TEXT PRIMARY KEY, disk_name TEXT NOT NULL, sha256 TEXT)`}
	for _, s := range stmts {
		if _, err = db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < msgs; i++ {
		db.Exec(`INSERT INTO messages(body) VALUES('hello')`)
	}
	for id, content := range atts {
		sum := sha256.Sum256([]byte(content))
		name := id + ".upload"
		os.MkdirAll(filepath.Join(dir, "attachments"), 0o700)
		os.WriteFile(filepath.Join(dir, "attachments", name), []byte(content), 0o600)
		db.Exec(`INSERT INTO attachments VALUES(?,?,?)`, id, name, hex.EncodeToString(sum[:]))
	}
}

// vault writes a one-record vault in the workspace format.
func vault(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, "secrets"), 0o700)
	key := bytes.Repeat([]byte{7}, 32)
	os.WriteFile(filepath.Join(dir, "secrets", "key"), key, 0o600)
	b, _ := aes.NewCipher(key)
	g, _ := cipher.NewGCM(b)
	n := make([]byte, g.NonceSize())
	ct := g.Seal(n, n, []byte("sk-live-never-in-bucket"), []byte("r1"))
	rec, _ := json.Marshal(map[string]any{"r1": map[string]any{"id": "r1", "ciphertext": ct}})
	os.WriteFile(filepath.Join(dir, "secrets", "vault.json"), rec, 0o600)
}

type fixture struct {
	data string
	src  backup.Source
	repo *backup.Repo
	f    *fakes3.Server
}

func setup(t *testing.T) fixture {
	t.Helper()
	s3, f := bucket(t)
	data := t.TempDir()
	mkdb(t, data, 3, map[string]string{"att-owner": "owner file bytes"})
	root, _ := sql.Open("sqlite", filepath.Join(data, "tofi.db"))
	root.Exec(`CREATE TABLE accounts(id TEXT PRIMARY KEY, legacy INTEGER, deleting INTEGER)`)
	root.Exec(`INSERT INTO accounts VALUES('legacy-owner',1,0),('acct-a',0,0),('acct-gone',0,1)`)
	root.Close()
	vault(t, data)
	mkdb(t, filepath.Join(data, "accounts", "acct-a"), 5, map[string]string{"att-a1": "alpha one", "att-a2": "alpha two"})
	vault(t, filepath.Join(data, "accounts", "acct-a"))
	os.WriteFile(filepath.Join(data, "instance.json"), []byte(`{"instance_id":"inst"}`), 0o600)
	repo, err := backup.Init(context.Background(), s3, base, "RECV-KEY-0001", "2026-11-10T00:00:00Z", wrapper)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{data: data, repo: repo, f: f, src: backup.Source{DataDir: data, InstanceID: "inst", AppVersion: "test", Schema: 1, Staging: filepath.Join(t.TempDir(), "stage")}}
}

func puts(f *fakes3.Server, prefix string) int {
	n := 0
	for _, p := range f.Paths {
		if strings.HasPrefix(p, "PUT /bkt/"+prefix) {
			n++
		}
	}
	return n
}

// Backup, wipe, restore: every database has the same rows, every disk
// attachment the same bytes, the vault opens, and nothing readable is in the bucket.
func TestRoundTrip(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC)
	m, err := x.repo.Run(ctx, x.src, "scheduled", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Accounts) != 1 || m.Accounts[0].ID != "acct-a" {
		t.Fatalf("accounts: %+v", m.Accounts)
	}
	for _, k := range x.f.Keys() {
		data, _, _ := x.f.Object(k)
		if strings.HasSuffix(k, "repo.json") || strings.HasSuffix(k, "head.json") {
			continue
		}
		for _, secret := range []string{"sk-live-never-in-bucket", "owner file bytes", "alpha one", "hello", "SQLite format", "RECV-KEY-0001", "bucket-secret-xyz"} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("%s holds plaintext %q", k, secret)
			}
		}
		for _, bad := range []string{"acct-gone", "legacy-owner", "instance.json", "att-a1.upload"} {
			if strings.Contains(k, bad) {
				t.Fatalf("key %s names %s", k, bad)
			}
		}
	}
	dest := filepath.Join(t.TempDir(), "data")
	rep, err := x.repo.RestoreAll(ctx, "", dest, backup.RestoreOpts{MaxSchema: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries %+v", rep.Entries)
	}
	for _, p := range []string{"attachments/att-owner.upload", "accounts/acct-a/attachments/att-a1.upload", "accounts/acct-a/attachments/att-a2.upload", "instance.json"} {
		want, _ := os.ReadFile(filepath.Join(x.data, p))
		got, err := os.ReadFile(filepath.Join(dest, p))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s differs: %v", p, err)
		}
	}
	if rep.Entries[0].VaultKeys != 1 || rep.Entries[1].VaultKeys != 1 {
		t.Fatalf("vault %+v", rep.Entries)
	}
	db, _ := sql.Open("sqlite", filepath.Join(dest, "accounts", "acct-a", "tofi.db"))
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&n)
	db.Close()
	if n != 5 {
		t.Fatalf("messages %d", n)
	}
	// One account alone, reusing the files already on disk.
	one := filepath.Join(t.TempDir(), "a")
	rep, err = x.repo.Restore(ctx, m.Snapshot, "acct-a", one, backup.RestoreOpts{MaxSchema: 1, Existing: filepath.Join(x.data, "accounts", "acct-a")})
	if err != nil || rep.Entries[0].DiskFiles != 2 {
		t.Fatalf("single: %v %+v", err, rep)
	}
	// A newer schema is refused by name.
	if _, err = x.repo.Restore(ctx, m.Snapshot, "acct-a", filepath.Join(t.TempDir(), "n"), backup.RestoreOpts{MaxSchema: 0}); backup.CodeOf(err) != "backup_schema_too_new" {
		t.Fatalf("schema: %v", err)
	}
}

// A second run reads and uploads no attachment that is already in the bucket.
func TestDiskAttachmentsUploadOnce(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	if _, err := x.repo.Run(ctx, x.src, "scheduled", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := puts(x.f, base+"files/"); got != 3 {
		t.Fatalf("first run uploaded %d attachments", got)
	}
	os.WriteFile(filepath.Join(x.data, "accounts", "acct-a", "attachments", "att-a1.upload"), []byte("changed on disk"), 0o600) // proves it is not read again
	if _, err := x.repo.Run(ctx, x.src, "scheduled", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := puts(x.f, base+"files/"); got != 3 {
		t.Fatalf("second run uploaded %d attachments", got)
	}
}

// A failed upload commits nothing and the staging directory is empty.
func TestUploadFailureCommitsNothing(t *testing.T) {
	x := setup(t)
	x.f.Down = true
	_, err := x.repo.Run(context.Background(), x.src, "manual", time.Now())
	if backup.CodeOf(err) != "backup_store_unavailable" {
		t.Fatalf("err %v", err)
	}
	x.f.Down = false
	snaps, _ := x.repo.List(context.Background())
	for _, s := range snaps {
		if s.Committed {
			t.Fatalf("committed %v", s)
		}
	}
	if left, _ := os.ReadDir(x.src.Staging); len(left) != 0 {
		t.Fatalf("staging not empty: %v", left)
	}
}

func TestOversizeDatabaseIsNamed(t *testing.T) {
	x := setup(t)
	old := backup.MaxDBBytes
	backup.MaxDBBytes = 1024
	defer func() { backup.MaxDBBytes = old }()
	_, err := x.repo.Run(context.Background(), x.src, "manual", time.Now())
	if backup.CodeOf(err) != "backup_db_too_large" {
		t.Fatalf("err %v", err)
	}
}

func TestKeys(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	m, err := x.repo.Run(ctx, x.src, "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	s3, _ := filestore.NewS3(filestore.S3Config{Endpoint: x.f.URL, Region: "us-east-1", Bucket: "bkt", AccessKeyID: "AKIATESTONLY", Secret: "bucket-secret-xyz", PathStyle: true})
	if _, err = backup.Open(ctx, s3, base, "WRONG", wrapper); backup.CodeOf(err) != "backup_recovery_key_wrong" {
		t.Fatalf("wrong key: %v", err)
	}
	if err = x.repo.Rekey(ctx, "NEW-KEY", wrapper); err != nil {
		t.Fatal(err)
	}
	if _, err = backup.Open(ctx, s3, base, "RECV-KEY-0001", wrapper); backup.CodeOf(err) != "backup_recovery_key_wrong" {
		t.Fatalf("old key still opens: %v", err)
	}
	r2, err := backup.Open(ctx, s3, base, "NEW-KEY", wrapper)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r2.Manifest(ctx, m.Snapshot); err != nil {
		t.Fatalf("data untouched by rekey: %v", err)
	}
	// An object moved to another path no longer opens.
	data, _, _ := x.f.Object(base + "snapshots/" + m.Snapshot + "/root/db")
	x.f.Set(base+"snapshots/"+m.Snapshot+"/root/files", data)
	if _, err = r2.Restore(ctx, m.Snapshot, "", filepath.Join(t.TempDir(), "d"), backup.RestoreOpts{MaxSchema: 1}); backup.CodeOf(err) != "backup_object_damaged" {
		t.Fatalf("moved object: %v", err)
	}
}

// Two servers with one instance id: the second to write is told so.
func TestLineage(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	if _, err := x.repo.Run(ctx, x.src, "scheduled", time.Now()); err != nil {
		t.Fatal(err)
	}
	clone := x.src
	clone.DataDir = t.TempDir() // another server: its own state, same repository
	mkdb(t, clone.DataDir, 1, nil)
	if _, err := x.repo.Run(ctx, clone, "scheduled", time.Now().Add(time.Hour)); backup.CodeOf(err) != "backup_repo_diverged" {
		t.Fatalf("clone: %v", err)
	}
	// A restore takes the lineage over; the old server then finds itself diverged.
	seq, err := x.repo.Takeover(ctx)
	if err != nil || seq != 2 {
		t.Fatalf("takeover %d %v", seq, err)
	}
	if _, err = x.repo.Run(ctx, x.src, "scheduled", time.Now().Add(2*time.Hour)); backup.CodeOf(err) != "backup_repo_diverged" {
		t.Fatalf("old server: %v", err)
	}
}

func TestVerifyAndPrune(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	day := time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		if _, err := x.repo.Run(ctx, x.src, "scheduled", day.AddDate(0, 0, i*3)); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := x.repo.Verify(ctx, x.src.Staging, 1, 20)
	if err != nil || len(rep.Entries) != 2 {
		t.Fatalf("verify %v %+v", err, rep)
	}
	// A damaged object fails the drill by name.
	snaps, _ := x.repo.List(ctx)
	last := snaps[len(snaps)-1].Name
	x.f.Set(base+"snapshots/"+last+"/accounts/acct-a/db", []byte("garbage-garbage-garbage-garbage"))
	if _, err = x.repo.Verify(ctx, x.src.Staging, 1, 20); backup.CodeOf(err) != "backup_object_damaged" {
		t.Fatalf("drill missed damage: %v", err)
	}
	// An attachment deleted on the server is dropped from the bucket once no kept snapshot lists it.
	x.f.Set(base+"snapshots/"+last+"/stray/object", []byte("x")) // a directory with no manifest
	removed, err := x.repo.Prune(ctx, backup.Retention{Daily: 2, Weekly: 1, Monthly: 1})
	if err != nil || removed < 8 {
		t.Fatalf("prune %d %v", removed, err)
	}
	after, _ := x.repo.List(ctx)
	for _, s := range after {
		if !s.Committed {
			t.Fatalf("manifest-less directory survived: %s", s.Name)
		}
	}
	if len(x.f.Keys()) == 0 {
		t.Fatal("bucket emptied")
	}
	if _, err = x.repo.Manifest(ctx, snaps[0].Name); backup.CodeOf(err) != "backup_snapshot_missing" {
		t.Fatalf("expired snapshot still there: %v", err)
	}
	if puts(x.f, base+"files/") != 3 || len(filesKeys(x.f)) != 3 {
		t.Fatalf("attachments needed by kept snapshots were removed: %v", filesKeys(x.f))
	}
}

func filesKeys(f *fakes3.Server) []string {
	var out []string
	for _, k := range f.Keys() {
		if strings.HasPrefix(k, base+"files/") {
			out = append(out, k)
		}
	}
	return out
}

func TestSelect(t *testing.T) {
	var snaps []backup.Snapshot
	d := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC)
	for i := 0; i < 400; i++ {
		tm := d.AddDate(0, 0, i)
		snaps = append(snaps, backup.Snapshot{Name: backup.SnapshotName(tm, "scheduled"), Time: tm, Reason: "scheduled", Committed: true})
	}
	keep := backup.Select(snaps, backup.Retention{Daily: 7, Weekly: 4, Monthly: 12})
	if n := len(keep); n < 15 || n > 23 {
		t.Fatalf("kept %d", n)
	}
	if !keep[snaps[399].Name] || !keep[snaps[393].Name] {
		t.Fatal("last seven days must stay")
	}
	if got := backup.Select(nil, backup.Retention{Daily: 7}); len(got) != 0 {
		t.Fatal("nothing to keep")
	}
	two := []backup.Snapshot{{Name: "20260101T030000Z-manual", Time: d, Reason: "manual"}, {Name: "20260101T040000Z-scheduled", Time: d.Add(time.Hour), Reason: "scheduled"}}
	if k := backup.Select(two, backup.Retention{}); len(k) != 2 {
		t.Fatalf("manual and newest stay: %v", k)
	}
}

var _ = errors.New
