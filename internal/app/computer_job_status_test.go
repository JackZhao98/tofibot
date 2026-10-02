package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func deviceStatusCall(s *Server, method, route, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, route, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestComputerDeviceJobStatusOwnerBoundary(t *testing.T) {
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	id, token := pairedComputer(t, s, "files.read")
	otherID, otherToken := pairedComputer(t, s, "files.read")
	j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"synthetic-private-path"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	route := "/api/computers/" + id + "/jobs/" + j.ID + "/status"
	w := deviceStatusCall(s, http.MethodGet, route, token)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var state ComputerJobStatus
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if state.ID != j.ID || state.DeviceID != id || state.Status != "running" || state.LeaseMS <= 0 || state.LeaseMS > 1000 {
		t.Fatalf("unexpected status: %+v", state)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &fields)
	if len(fields) != 5 || strings.Contains(w.Body.String(), "synthetic-private-path") {
		t.Fatalf("status must contain only the minimal DTO: %s", w.Body.String())
	}
	for _, tc := range []struct {
		name, method, route, token string
		want                       int
	}{
		{"missing-token", "GET", route, "", 401},
		{"wrong-token", "GET", route, "synthetic-wrong-token", 401},
		{"other-token", "GET", route, otherToken, 401},
		{"unknown-device", "GET", "/api/computers/missing/jobs/" + j.ID + "/status", token, 401},
		{"other-device-job", "GET", "/api/computers/" + otherID + "/jobs/" + j.ID + "/status", otherToken, 404},
		{"unknown-job", "GET", "/api/computers/" + id + "/jobs/missing/status", token, 404},
		{"owner-actions-still-private", "GET", "/api/computers/" + id + "/actions/" + j.ID, token, 401},
		{"wrong-method", "POST", route, token, 401},
		{"extra-segment", "GET", route + "/extra", token, 401},
		{"trailing-slash", "GET", route + "/", token, 401},
		{"empty-job", "GET", "/api/computers/" + id + "/jobs//status", token, 401},
		{"empty-device", "GET", "/api/computers//jobs/" + j.ID + "/status", token, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := deviceStatusCall(s, tc.method, tc.route, tc.token)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if _, err := s.store.revokeComputer(id); err != nil {
		t.Fatal(err)
	}
	if w := deviceStatusCall(s, "GET", route, token); w.Code != 401 {
		t.Fatalf("revoked token accepted: %d", w.Code)
	}
}

func TestComputerDeviceJobStatusTracksTerminalStateAndLease(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	for _, terminal := range []string{"pending", "completed", "failed", "cancelled", "expired", "interrupted"} {
		t.Run(terminal, func(t *testing.T) {
			j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.store.db.Exec(`UPDATE computer_jobs SET status=? WHERE id=?`, terminal, j.ID); err != nil {
				t.Fatal(err)
			}
			state, err := s.store.deviceJobStatus(id, token, j.ID)
			if err != nil || state.Status != terminal || state.LeaseMS != 0 {
				t.Fatalf("state=%+v error=%v", state, err)
			}
		})
	}
	j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE computer_jobs SET status='running',expires_at=? WHERE id=?`, time.Now().Add(400*time.Millisecond).UTC().Format(time.RFC3339Nano), j.ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.store.deviceJobStatus(id, token, j.ID)
	if err != nil || state.LeaseMS <= 0 || state.LeaseMS > 400 {
		t.Fatalf("short lease not bounded by expiry: %+v %v", state, err)
	}
	if _, err = s.store.db.Exec(`UPDATE computer_jobs SET expires_at=? WHERE id=?`, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano), j.ID); err != nil {
		t.Fatal(err)
	}
	state, err = s.store.deviceJobStatus(id, token, j.ID)
	if err != nil || state.Status != "expired" || state.LeaseMS != 0 {
		t.Fatalf("expired state: %+v %v", state, err)
	}
	if err := s.store.completeComputerJob(id, token, j.ID, json.RawMessage(`null`), ""); err == nil {
		t.Fatal("late completion replaced expiry")
	}
}

func TestComputerDeviceJobStatusParentCancellationAndFailedResult(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	run := activeComputerRun(t, s)
	j, err := s.store.queueComputerJob(run, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE runs SET status='cancelled' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.store.deviceJobStatus(id, token, j.ID)
	if err != nil || state.Status != "cancelled" || state.LeaseMS != 0 {
		t.Fatalf("parent cancellation: %+v %v", state, err)
	}
	if err = s.store.completeComputerJob(id, token, j.ID, json.RawMessage(`null`), "deadline exceeded"); err == nil {
		t.Fatal("late result replaced cancellation")
	}
	ownerState, err := s.store.computerJob(j.ID, id)
	if err != nil || ownerState.Status != state.Status {
		t.Fatalf("owner/device status disagree: %+v %v", ownerState, err)
	}
	failed, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	if err = s.store.completeComputerJob(id, token, failed.ID, json.RawMessage(`{"reason":"deadline_exceeded"}`), "sandbox.exec exceeded the 30 second deadline"); err != nil {
		t.Fatal(err)
	}
	state, err = s.store.deviceJobStatus(id, token, failed.ID)
	if err != nil || state.Status != "failed" || state.LeaseMS != 0 {
		t.Fatalf("failed result misreported: %+v %v", state, err)
	}
}

func TestComputerJobStatusExpiryUsesTimeNotLexicalOrdering(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "files.read")
	j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 22, 10, 0, 0, 500_000_000, time.UTC)
	// RFC3339 whole-second Z sorts after a fractional suffix, but is earlier.
	if _, err = s.store.db.Exec(`UPDATE computer_jobs SET expires_at=? WHERE id=?`, at.Truncate(time.Second).Format(time.RFC3339Nano), j.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := computerJobInTx(tx, j.ID, id, at)
	if err != nil || got.Status != "expired" {
		t.Fatalf("whole-second expiry: %+v %v", got, err)
	}
}

// Server wire contract; the actual desktop client is covered by desktop acceptance.
func TestComputerJobStatusBrokerWire(t *testing.T) {
	s, dir := ownerTestServer(t)
	ownerSetup(t, s, dir)
	id, token := pairedComputer(t, s, "files.read")
	run := activeComputerRun(t, s)
	j, err := s.store.queueComputerJob(run, id, "files.read", json.RawMessage(`{"path":"synthetic-only"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	route := "/api/computers/" + id + "/jobs/" + j.ID + "/status"
	before := deviceStatusCall(s, "GET", route, token)
	var status struct {
		Status  string `json:"status"`
		LeaseMS int    `json:"lease_ms"`
	}
	if before.Code != 200 {
		t.Fatalf("running: %d %s", before.Code, before.Body.String())
	}
	if err := json.Unmarshal(before.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "running" || status.LeaseMS <= 0 || status.LeaseMS > 1000 {
		t.Fatalf("running status: %+v", status)
	}
	if _, err = s.store.SetRunStatus(run.ID, "cancelled", "synthetic cancellation"); err != nil {
		t.Fatal(err)
	}
	after := deviceStatusCall(s, "GET", route, token)
	if err := json.Unmarshal(after.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if after.Code != 200 || status.Status != "cancelled" || status.LeaseMS != 0 {
		t.Fatalf("cancelled: %d %+v", after.Code, status)
	}
	for _, check := range []struct {
		route, token string
		code         int
	}{
		{"/api/computers/" + id + "/actions/" + j.ID, token, 401},
		{route, "synthetic-wrong", 401},
		{"/api/computers/" + id + "/jobs/missing/status", token, 404},
	} {
		if got := deviceStatusCall(s, "GET", check.route, check.token); got.Code != check.code {
			t.Fatalf("boundary %s: %d", check.route, got.Code)
		}
	}
}
