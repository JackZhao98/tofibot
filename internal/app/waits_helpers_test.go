package app

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// insertBotPostWait stores a pending wait of bot on waitFor's next post, as
// registering one in a run would, with a chosen deadline.
func insertBotPostWait(t testing.TB, db *sql.DB, runID, conv, bot, waitFor string, deadline time.Time) string {
	t.Helper()
	id := newID()
	if _, err := db.Exec(`INSERT INTO waits(id,bot_id,run_id,conversation_id,kind,target,group_id,group_mode,status,delivery,deadline_at,lineage,created_at)
 VALUES(?,?,?,?,?,?,?,'all','pending','engine',?,?,?)`, id, bot, runID, conv, waitBotPost, waitFor, id, stampTime(deadline), lineageKey(db, runID), now()); err != nil {
		t.Fatal(err)
	}
	return id
}

// timeoutWaitsOf brings the pending waits of a run to their deadline and runs
// one sweep, the way the clock would.
func (s *Server) timeoutWaitsOf(t testing.TB, runID string) {
	t.Helper()
	if _, err := s.store.db.Exec(`UPDATE waits SET deadline_at=? WHERE run_id=? AND status='pending'`, stampTime(time.Now().Add(-time.Second)), runID); err != nil {
		t.Fatal(err)
	}
	s.sweepWaits(time.Now())
}

func waitStatusOf(t testing.TB, db *sql.DB, runID string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM waits WHERE run_id=? ORDER BY created_at DESC LIMIT 1`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func httptestGet(s *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", path, nil)
	s.routeTaskImpact(rec, req, strings.TrimPrefix(path, "/api/"))
	return rec
}
