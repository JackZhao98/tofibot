package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func primaryTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, s.Handler()
}

func primaryCall(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func errCode(out map[string]any) string {
	if e, ok := out["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func mustBot(t *testing.T, s *Store, name string) Bot {
	t.Helper()
	b, err := s.CreateBot(name, "", "model")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustPrimary(t *testing.T, s *Store) string {
	t.Helper()
	id, err := s.PrimaryBotID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPrimaryDefaultsToFirstBotAndStaysPut(t *testing.T) {
	s, h := primaryTestServer(t)
	if got := mustPrimary(t, s.store); got != "" {
		t.Fatalf("empty account has Primary %q", got)
	}
	a := mustBot(t, s.store, "A")
	b := mustBot(t, s.store, "B")
	if got := mustPrimary(t, s.store); got != a.ID {
		t.Fatalf("first Bot must be Primary: got %q want %q", got, a.ID)
	}
	status, out := primaryCall(t, h, http.MethodGet, "/api/primary-assistant", nil)
	if status != 200 || out["bot_id"] != a.ID {
		t.Fatalf("GET primary %d %v", status, out)
	}
	status, out = primaryCall(t, h, http.MethodGet, "/api/bots", nil)
	if status != 200 {
		t.Fatalf("list %d", status)
	}
	flags := map[string]bool{}
	for _, raw := range out["bots"].([]any) {
		m := raw.(map[string]any)
		flags[m["id"].(string)], _ = m["is_primary"].(bool)
	}
	if !flags[a.ID] || flags[b.ID] {
		t.Fatalf("is_primary flags wrong: %v", flags)
	}
}

func TestPrimaryOnboardingFirstBotBecomesPrimary(t *testing.T) {
	s, _ := primaryTestServer(t)
	b, _, err := s.store.createOnboardingBot(uuid.NewString(), followGlobalModel, followGlobalModel, "en", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustPrimary(t, s.store); got != b.ID {
		t.Fatalf("onboarding Bot is not Primary: %q", got)
	}
}

func TestPrimaryMigrationBackfillsExistingAccountsByCreationOrder(t *testing.T) {
	s, _ := primaryTestServer(t)
	a := mustBot(t, s.store, "A")
	mustBot(t, s.store, "B")
	// An account from before this feature: no stored Primary.
	if _, err := s.store.db.Exec(`UPDATE user_preferences SET primary_bot_id=''`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := migratePrimaryBot(s.store.db); err != nil {
			t.Fatal(err)
		}
		var stored string
		if err := s.store.db.QueryRow(`SELECT primary_bot_id FROM user_preferences WHERE id=1`).Scan(&stored); err != nil || stored != a.ID {
			t.Fatalf("pass %d stored=%q err=%v", i, stored, err)
		}
	}
}

func TestPrimaryReassignAndValidation(t *testing.T) {
	s, h := primaryTestServer(t)
	a, b := mustBot(t, s.store, "A"), mustBot(t, s.store, "B")
	if status, _ := primaryCall(t, h, http.MethodPut, "/api/primary-assistant", map[string]string{"bot_id": b.ID}); status != 200 {
		t.Fatalf("PUT %d", status)
	}
	if got := mustPrimary(t, s.store); got != b.ID {
		t.Fatalf("not reassigned: %q", got)
	}
	if status, out := primaryCall(t, h, http.MethodPut, "/api/primary-assistant", map[string]string{"bot_id": "missing"}); status != 400 || errCode(out) != "primary_invalid" {
		t.Fatalf("unknown Bot accepted: %d %v", status, out)
	}
	if _, err := s.store.SetBotArchivedReassigning(a.ID, true, ""); err != nil {
		t.Fatal(err) // A is not Primary any more, so no successor is needed
	}
	if status, out := primaryCall(t, h, http.MethodPut, "/api/primary-assistant", map[string]string{"bot_id": a.ID}); status != 400 || errCode(out) != "primary_invalid" {
		t.Fatalf("archived Bot accepted: %d %v", status, out)
	}
}

func TestDeletingPrimaryForcesReassignment(t *testing.T) {
	s, h := primaryTestServer(t)
	a, b, c := mustBot(t, s.store, "A"), mustBot(t, s.store, "B"), mustBot(t, s.store, "C")
	// Without a successor the delete is refused and nothing changes.
	status, out := primaryCall(t, h, http.MethodDelete, "/api/bots/"+a.ID, nil)
	if status != http.StatusConflict || errCode(out) != "primary_reassign_required" {
		t.Fatalf("delete without successor: %d %v", status, out)
	}
	if _, err := s.store.GetBot(a.ID); err != nil || mustPrimary(t, s.store) != a.ID {
		t.Fatalf("refused delete changed state: %v", err)
	}
	// An unusable successor (the Bot itself, or a missing one) is refused too.
	for _, bad := range []string{a.ID, "nope"} {
		status, out = primaryCall(t, h, http.MethodDelete, "/api/bots/"+a.ID+"?new_primary_id="+bad, nil)
		if status != http.StatusBadRequest || errCode(out) != "primary_invalid" {
			t.Fatalf("bad successor %q: %d %v", bad, status, out)
		}
	}
	if _, err := s.store.GetBot(a.ID); err != nil {
		t.Fatalf("invalid successor still deleted the Bot: %v", err)
	}
	status, _ = primaryCall(t, h, http.MethodDelete, "/api/bots/"+a.ID+"?new_primary_id="+c.ID, nil)
	if status != 200 {
		t.Fatalf("delete with successor: %d", status)
	}
	if got := mustPrimary(t, s.store); got != c.ID {
		t.Fatalf("successor not applied: %q (b=%s c=%s)", got, b.ID, c.ID)
	}
	// Deleting a non-Primary Bot needs no successor.
	if status, _ = primaryCall(t, h, http.MethodDelete, "/api/bots/"+b.ID, nil); status != 200 {
		t.Fatalf("delete non-primary: %d", status)
	}
	if mustPrimary(t, s.store) != c.ID {
		t.Fatal("non-primary delete moved the Primary")
	}
}

func TestDeletingLastBotEmptiesRoleAndNextBotClaimsIt(t *testing.T) {
	s, h := primaryTestServer(t)
	a := mustBot(t, s.store, "A")
	if status, out := primaryCall(t, h, http.MethodDelete, "/api/bots/"+a.ID, nil); status != 200 {
		t.Fatalf("delete last Bot must not need a successor: %d %v", status, out)
	}
	if got := mustPrimary(t, s.store); got != "" {
		t.Fatalf("role not empty after the last Bot: %q", got)
	}
	b := mustBot(t, s.store, "B")
	if got := mustPrimary(t, s.store); got != b.ID {
		t.Fatalf("next Bot did not become Primary: %q", got)
	}
}

func TestArchivingPrimaryForcesReassignment(t *testing.T) {
	s, h := primaryTestServer(t)
	a, b := mustBot(t, s.store, "A"), mustBot(t, s.store, "B")
	status, out := primaryCall(t, h, http.MethodPatch, "/api/bots/"+a.ID, map[string]any{"archived": true})
	if status != http.StatusConflict || errCode(out) != "primary_reassign_required" {
		t.Fatalf("archive without successor: %d %v", status, out)
	}
	status, _ = primaryCall(t, h, http.MethodPatch, "/api/bots/"+a.ID, map[string]any{"archived": true, "new_primary_id": b.ID})
	if status != 200 || mustPrimary(t, s.store) != b.ID {
		t.Fatalf("archive with successor: %d primary=%q", status, mustPrimary(t, s.store))
	}
}

func TestBotRunDeleteCannotRemovePrimary(t *testing.T) {
	s, _ := primaryTestServer(t)
	a, _ := mustBot(t, s.store, "A"), mustBot(t, s.store, "B")
	_, err := s.store.deleteWorkspaceTargetWith(a.ID, true, nil, "", true)
	if !errors.Is(err, ErrPrimaryReassignRequired) {
		t.Fatalf("strict delete without successor: %v", err)
	}
}

func TestStoredPrimaryThatVanishedIsRepaired(t *testing.T) {
	s, _ := primaryTestServer(t)
	a, b := mustBot(t, s.store, "A"), mustBot(t, s.store, "B")
	if _, err := s.store.DeleteBot(a.ID); err != nil { // legacy path, no successor named
		t.Fatal(err)
	}
	if got := mustPrimary(t, s.store); got != b.ID {
		t.Fatalf("repair picked %q want %q", got, b.ID)
	}
}
