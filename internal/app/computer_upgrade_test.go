package app

// Upgrade over the top: a data directory written by the code of main before the
// decoupling work (testdata/upgrade_from_main: a real database with a Bot, a
// conversation, messages, a memory, a schedule, an uploaded file, a file that
// lives only on the computer, and a computer paired the old way) is opened by
// the current code. Every row must survive, the migrations must be idempotent,
// and the data must still work.
//
// The fixture was made by running the seeding test of the commit named in
// testdata/upgrade_from_main/README on that commit's code.

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func unpackUpgradeFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	src := filepath.Join("testdata", "upgrade_from_main")
	dir := t.TempDir()
	gz, err := os.Open(filepath.Join(src, "tofi.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	zr, err := gzip.NewReader(gz)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(filepath.Join(dir, "tofi.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(out, zr); err != nil {
		t.Fatal(err)
	}
	out.Close()
	if err = os.MkdirAll(filepath.Join(dir, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(src, "attachments"))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, "attachments", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "attachments", e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]string
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return dir, manifest
}

// tableCounts returns the row count of every ordinary table.
func tableCounts(t *testing.T, path string) map[string]int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' AND name NOT LIKE 'messages\_fts%' ESCAPE '\' AND sql NOT LIKE 'CREATE VIRTUAL%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err = rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	sort.Strings(names)
	out := map[string]int{}
	for _, n := range names {
		var c int
		if err = db.QueryRow(`SELECT COUNT(*) FROM "` + n + `"`).Scan(&c); err != nil {
			t.Fatalf("count %s: %v", n, err)
		}
		out[n] = c
	}
	return out
}

func TestUpgradeOverTheTopKeepsEveryRow(t *testing.T) {
	dir, manifest := unpackUpgradeFixture(t)
	before := tableCounts(t, filepath.Join(dir, "tofi.db"))
	if before["bots"] == 0 || before["messages"] == 0 || before["memories"] == 0 || before["schedules"] == 0 || before["attachments"] != 2 || before["computers"] != 1 {
		t.Fatalf("fixture is not what the test expects: %v", before)
	}
	var tokenBefore []byte
	{
		db, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`SELECT token_hash FROM computers WHERE id=?`, manifest["device_id"]).Scan(&tokenBefore); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}

	// Two starts: the second proves the migrations are idempotent.
	for round := 1; round <= 2; round++ {
		s, err := NewServer(Config{DataDir: dir})
		if err != nil {
			t.Fatalf("start %d on the old data: %v", round, err)
		}
		if round == 1 {
			// The data works through the running server.
			get := func(path string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://tofi.local"+path, nil))
				return rec
			}
			rec := get("/api/attachments/" + manifest["attachment_id"])
			if rec.Code != 200 || fileSHA(rec.Body.Bytes()) != manifest["attachment_sha256"] {
				t.Fatalf("an uploaded file after the upgrade: %d", rec.Code)
			}
			rec = get("/api/attachments/" + manifest["vm_attachment_id"])
			if rec.Code != 409 || !strings.Contains(rec.Body.String(), "file_on_detached_computer") {
				t.Fatalf("a file only on the computer, with none attached: %d %s", rec.Code, rec.Body.String())
			}
			if m, err := s.store.GetConversation(manifest["conversation_id"]); err != nil || m.ID == "" {
				t.Fatalf("conversation: %v", err)
			}
			var content string
			if s.store.db.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND role='assistant'`, manifest["conversation_id"]).Scan(&content) != nil || !strings.Contains(content, "before the upgrade") {
				t.Fatal("assistant message lost")
			}
			// The paired computer is a personal computer now, and still the same device.
			row, err := s.store.computerRow(manifest["device_id"])
			if err != nil || row.Kind != "personal" || row.Status != computerRowAttached || row.Name != "Studio Mac" || row.Platform != "darwin" {
				t.Fatalf("paired computer row: %+v %v", row, err)
			}
			if n, _ := s.store.countPersonalComputers(); n != 1 {
				t.Fatalf("personal computers: %d", n)
			}
			// The storage counters were seeded from the rows that were already there.
			var uploads int64
			s.store.db.QueryRow(`SELECT COALESCE(SUM(bytes),0) FROM storage_counters WHERE class IN ('uploads','published','generated','outbox','mirror')`).Scan(&uploads)
			var sizes int64
			s.store.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM attachments`).Scan(&sizes)
			if uploads != sizes {
				t.Fatalf("storage counters %d, attachment sizes %d", uploads, sizes)
			}
		}
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
		after := tableCounts(t, filepath.Join(dir, "tofi.db"))
		for table, n := range before {
			if got, ok := after[table]; !ok || got < n {
				t.Fatalf("round %d: table %s had %d rows, now %d (present=%v)", round, table, n, got, ok)
			}
		}
		if after["computers_pre_rows"] != 0 {
			t.Fatal("the rebuild left its scratch table behind")
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tokenAfter []byte
	if err = db.QueryRow(`SELECT token_hash FROM computers WHERE id=?`, manifest["device_id"]).Scan(&tokenAfter); err != nil || !bytes.Equal(tokenBefore, tokenAfter) {
		t.Fatalf("the paired computer's credential changed: %v", err)
	}
	for _, col := range []string{"region", "labels", "protocol_version", "status"} {
		if _, err = db.Exec(`SELECT ` + col + ` FROM computers LIMIT 1`); err != nil {
			t.Fatalf("computers.%s: %v", col, err)
		}
	}
	var metered int
	if err = db.QueryRow(`SELECT COUNT(*) FROM run_computers WHERE run_id=?`, manifest["run_id"]).Scan(&metered); err != nil || metered != 0 {
		t.Fatalf("run_computers must exist and be empty for an old run: %d %v", metered, err)
	}
}
