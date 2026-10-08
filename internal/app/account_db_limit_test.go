package app

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func openLimitTestDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dir+"/limit.db")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE payloads (value BLOB NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestApplyAccountDBLimitStopsGrowthAndReappliesOnReopen(t *testing.T) {
	dir := t.TempDir()
	db := openLimitTestDB(t, dir)
	var pageSize int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	limit := pageSize * 16
	if err := applyAccountDBLimit(db, limit); err != nil {
		t.Fatalf("apply limit: %v", err)
	}
	var maxPages, checkpointPages, journalLimit int64
	if err := db.QueryRow(`PRAGMA max_page_count`).Scan(&maxPages); err != nil {
		t.Fatal(err)
	}
	if maxPages != limit/pageSize {
		t.Fatalf("max_page_count = %d, want %d", maxPages, limit/pageSize)
	}
	if err := db.QueryRow(`PRAGMA wal_autocheckpoint`).Scan(&checkpointPages); err != nil {
		t.Fatal(err)
	}
	if checkpointPages <= 0 {
		t.Fatalf("wal_autocheckpoint = %d, want finite positive threshold", checkpointPages)
	}
	if err := db.QueryRow(`PRAGMA journal_size_limit`).Scan(&journalLimit); err != nil {
		t.Fatal(err)
	}
	if journalLimit != limit {
		t.Fatalf("journal_size_limit = %d, want %d", journalLimit, limit)
	}

	payload := strings.Repeat("x", int(pageSize*2))
	var inserted int
	for i := 0; i < 100; i++ {
		if _, err := db.Exec(`INSERT INTO payloads(value) VALUES (?)`, []byte(payload)); err != nil {
			if !strings.Contains(strings.ToUpper(err.Error()), "FULL") && !strings.Contains(strings.ToLower(err.Error()), "full") {
				t.Fatalf("write stopped for unexpected reason after %d rows: %v", inserted, err)
			}
			break
		}
		inserted++
	}
	if inserted == 100 {
		t.Fatal("writes did not reach SQLite page cap")
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM payloads`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != inserted || rows == 0 {
		t.Fatalf("retained row count = %d, successful writes = %d", rows, inserted)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := sql.Open("sqlite", dir+"/limit.db")
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetMaxOpenConns(1)
	defer reopened.Close()
	if err := applyAccountDBLimit(reopened, limit); err != nil {
		t.Fatalf("reapply limit after reopen: %v", err)
	}
	if err := reopened.QueryRow(`SELECT count(*) FROM payloads`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != inserted {
		t.Fatalf("reopened row count = %d, want %d", rows, inserted)
	}
	if _, err := reopened.Exec(`INSERT INTO payloads(value) VALUES (?)`, []byte(payload)); err == nil {
		t.Fatal("write after reopen unexpectedly exceeded the configured page cap")
	}
}

func TestApplyAccountDBLimitRejectsInvalidOrAlreadyOversizedDatabase(t *testing.T) {
	db := openLimitTestDB(t, t.TempDir())
	if err := applyAccountDBLimit(db, 0); err == nil {
		t.Fatal("zero limit was accepted")
	}
	var pageSize int64
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO payloads(value) VALUES (?)`, []byte(strings.Repeat("z", int(pageSize*3)))); err != nil {
		t.Fatal(err)
	}
	if err := applyAccountDBLimit(db, pageSize); err == nil {
		t.Fatal("limit below current database size was accepted")
	}
}

func TestAccountGatewayAppliesDatabaseLimitToEveryWorkspace(t *testing.T) {
	g := accountFixture(t)
	a, err := g.create(context.Background(), "dbcap", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := g.workspace(a)
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []*Store{g.root.store, workspace.store} {
		var size, pages int64
		if err := store.db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow("PRAGMA max_page_count").Scan(&pages); err != nil {
			t.Fatal(err)
		}
		if pages*size != 1<<30 {
			t.Fatalf("limit=%d", pages*size)
		}
	}
}

// A live reader can prevent checkpoint progress. This is an explicit boundary:
// max_page_count does not bound WAL bytes or aggregate service disk usage.
func TestAccountDatabaseCapDoesNotBoundPinnedWAL(t *testing.T) {
	dir := t.TempDir()
	db := openLimitTestDB(t, dir)
	const capBytes = 64 << 10
	if err := applyAccountDBLimit(db, capBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO payloads(value) VALUES(zeroblob(4096))`); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", dir+"/limit.db")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM payloads`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		if _, err := db.Exec(`UPDATE payloads SET value=?`, []byte(strings.Repeat(string(rune('a'+i%26)), 4096))); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(dir + "/limit.db-wal")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= capBytes {
		t.Fatalf("expected WAL beyond page cap, got %d", info.Size())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	info, err = os.Stat(dir + "/limit.db-wal")
	if err != nil || info.Size() != 0 {
		t.Fatalf("checkpoint WAL: %v %v", info, err)
	}
}
