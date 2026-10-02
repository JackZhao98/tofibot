package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminPurgeClearsWorkspaceAndReturnsToAdminSetup(t *testing.T) {
	s, dir := ownerTestServer(t)
	if _, err := s.store.CreateBot("Disposable Bot", "instructions", "model"); err != nil {
		t.Fatal(err)
	}
	cookie := ownerSetup(t, s, dir)
	w := ownerCall(s, "POST", "/api/admin/purge", map[string]string{"confirm": purgeConfirmation}, cookie, true)
	if w.Code != 200 {
		t.Fatalf("purge status=%d body=%s", w.Code, w.Body.String())
	}
	var bots int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM bots`).Scan(&bots); err != nil || bots != 0 {
		t.Fatalf("bots after purge=%d err=%v", bots, err)
	}
	w = ownerCall(s, "GET", "/api/auth/session", nil, cookie, true)
	var state ownerSessionState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.SetupRequired || state.Authenticated || state.Owner != nil {
		t.Fatalf("unexpected post-purge auth state: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(dir, "owner-bootstrap.secret")); err != nil {
		t.Fatalf("new setup secret missing: %v", err)
	}
}
