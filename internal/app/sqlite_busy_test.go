package app

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// Another process holding a write lock briefly must not make startup fail.
func TestOpenStoreWaitsForABriefExternalLock(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	other, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetMaxOpenConns(1)
	tx, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`CREATE TABLE synthetic_lock(x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(700 * time.Millisecond)
		tx.Rollback()
		close(released)
	}()
	started := time.Now()
	reopened, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("startup failed while another connection held the lock: %v", err)
	}
	defer reopened.Close()
	<-released
	if time.Since(started) < 500*time.Millisecond {
		t.Fatal("startup did not wait for the external lock")
	}
}
