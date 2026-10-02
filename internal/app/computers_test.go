package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func computerTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir(), Engine: testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateComputers(s.store.db); err != nil {
		s.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func pairedComputer(t *testing.T, s *Server, caps ...string) (string, string) {
	t.Helper()
	code, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := s.store.pairComputer(code, "My Mac", "darwin", caps)
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func TestComputerPairingStatusTracksOnlyItsConsumedCode(t *testing.T) {
	s := computerTestServer(t)
	pairingID, code, _, err := s.store.createComputerPairingWithID()
	if err != nil {
		t.Fatal(err)
	}
	check := func(want string) ComputerPairingStatus {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/computers/pairings/"+pairingID, nil)
		if !s.routeComputers(w, r, "computers/pairings/"+pairingID) || w.Code != 200 {
			t.Fatalf("status response=%d %s", w.Code, w.Body.String())
		}
		var status ComputerPairingStatus
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Status != want {
			t.Fatalf("status=%+v want=%s", status, want)
		}
		return status
	}
	check("pending")
	if _, err := s.store.computerPairingStatus("another-pairing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unrelated pairing exposed: %v", err)
	}
	deviceID, _, err := s.store.pairComputer(code, "Confirmed Mac", "darwin", []string{"files.read"})
	if err != nil {
		t.Fatal(err)
	}
	status := check("paired")
	if status.DeviceID != deviceID || status.Name != "Confirmed Mac" {
		t.Fatalf("wrong device=%+v", status)
	}
	if _, err := s.store.revokeComputer(deviceID); err != nil {
		t.Fatal(err)
	}
	check("expired")
}

func TestComputerPairingIsOneUseExpiringAndHashed(t *testing.T) {
	s := computerTestServer(t)
	code, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := s.store.pairComputer(code, "Mac", "darwin", []string{"files.read"})
	if err != nil || id == "" || token == "" {
		t.Fatalf("pair id=%q token=%q err=%v", id, token, err)
	}
	if _, _, err = s.store.pairComputer(code, "Mac 2", "darwin", []string{"files.read"}); err == nil {
		t.Fatal("pairing code was reused")
	}
	var storedToken []byte
	if err = s.store.db.QueryRow(`SELECT token_hash FROM computers WHERE id=?`, id).Scan(&storedToken); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(storedToken), token) || len(storedToken) != sha256Size {
		t.Fatalf("device secret was not stored as a SHA-256 hash: %d bytes", len(storedToken))
	}

	expired, _, err := s.store.createComputerPairing()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.db.Exec(`UPDATE computer_pairings SET expires_at=? WHERE code_hash=?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), secretHash(expired)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.pairComputer(expired, "Late Mac", "darwin", []string{"files.read"}); err == nil {
		t.Fatal("expired pairing code was accepted")
	}
}

const sha256Size = 32

func TestComputerPairingBoundsPendingRequests(t *testing.T) {
	s := computerTestServer(t)
	for i := 0; i < maxPendingPairings; i++ {
		if _, _, err := s.store.createComputerPairing(); err != nil {
			t.Fatalf("pairing %d: %v", i, err)
		}
	}
	if _, _, err := s.store.createComputerPairing(); err == nil {
		t.Fatal("unbounded pairing issuance")
	}
}

func TestComputerDeviceAuthRevocationAndOutstandingCancellation(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"notes.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, "wrong"); err == nil {
		t.Fatal("invalid bearer was accepted")
	}
	if ok, err := s.store.revokeComputer(id); err != nil || !ok {
		t.Fatalf("revoke ok=%v err=%v", ok, err)
	}
	if _, err = s.store.claimComputerJob(id, token); err == nil {
		t.Fatal("revoked bearer was accepted")
	}
	got, err := s.store.computerJob(j.ID, id)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("outstanding job status=%q err=%v", got.Status, err)
	}
}

func TestComputerOfflineAndCapabilityChecksHappenBeforeQueue(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "files.read")
	if _, err := s.store.queueComputerJob(Run{}, id, "files.write", json.RawMessage(`{"path":"x","content":"y"}`)); err == nil {
		t.Fatal("ungranted capability was queued")
	}
	if _, err := s.store.db.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`)); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline queue err=%v", err)
	}
}

func TestSandboxExecQueueAcceptsOnlyCommandAndRejectsRemotePolicyInputs(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "sandbox.exec")
	job, err := s.store.queueComputerJob(Run{}, id, "sandbox.exec", json.RawMessage(`{"command":"printf ok"}`))
	if err != nil || job.Action != "sandbox.exec" {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	for _, raw := range []string{`{"command":"printf ok","env":{"HOME":"/tmp"}}`, `{"command":"printf ok","root":"/"}`, `{"command":"printf ok","policy":{"network":{}}}`} {
		if _, err = s.store.queueComputerJob(Run{}, id, "sandbox.exec", json.RawMessage(raw)); err == nil {
			t.Fatalf("remote policy input was accepted: %s", raw)
		}
	}
}

func TestNativeSandboxBotToolReturnsClaimedResultToOwningRun(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "sandbox.exec")
	run := activeComputerRun(t, s)
	tool := s.computerTools(run)[1]
	if !strings.Contains(tool.Description, "only args.command") || !strings.Contains(tool.Description, "no automatic rollback") {
		t.Fatal("native execution contract missing from tool metadata")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	type response struct {
		body string
		err  error
	}
	done := make(chan response, 1)
	go func() {
		body, err := tool.Execute(ctx, json.RawMessage(`{"computer_id":"`+id+`","action":"sandbox.exec","args":{"command":"printf native-fixture"}}`))
		done <- response{body, err}
	}()
	var job *ComputerJob
	for ctx.Err() == nil {
		var err error
		job, err = s.store.claimComputerJob(id, token)
		if err != nil {
			t.Fatal(err)
		}
		if job != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if job == nil || job.RunID != run.ID || job.BotID != run.BotID || job.Action != "sandbox.exec" {
		t.Fatalf("missing owning run: %#v", job)
	}
	result := json.RawMessage(`{"exitCode":0,"stdout":"native-fixture","stderr":"","containment":"unverified","cleanup":{"scope":"managed-process-group","status":"absent"}}`)
	if err := s.store.completeComputerJob(id, token, job.ID, result, ""); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil || got.body != string(result) {
		t.Fatalf("tool result: %q %v", got.body, got.err)
	}
	if err := s.store.completeComputerJob(id, token, job.ID, result, ""); err == nil {
		t.Fatal("accepted duplicate completion")
	}
}

func TestComputerJobsAreClaimedAtMostOnce(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	want, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.store.claimComputerJob(id, token)
	if err != nil || first == nil || first.ID != want.ID {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := s.store.claimComputerJob(id, token)
	if err != nil || second != nil {
		t.Fatalf("claimed twice: %#v err=%v", second, err)
	}
}

func activeComputerRun(t *testing.T, s *Server) Run {
	t.Helper()
	b, err := s.store.CreateBot("bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.store.GetConversation(b.DMConversationID)
	_, r, _, err := s.store.AddUserRun(c.ID, b.ID, "go", "computer-run")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate run ok=%v err=%v", ok, err)
	}
	r.Status = "running"
	return r
}

func TestComputerCancelledRunRejectsLateResult(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	r := activeComputerRun(t, s)
	j, err := s.store.queueComputerJob(r, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(r.ID, "cancelled", "cancelled"); err != nil || !ok {
		t.Fatalf("cancel ok=%v err=%v", ok, err)
	}
	if err = s.store.completeComputerJob(id, token, j.ID, json.RawMessage(`{"content":"too late"}`), ""); err == nil {
		t.Fatal("late result was published")
	}
	got, _ := s.store.computerJob(j.ID, id)
	if got.Status != "cancelled" || got.Result != nil {
		t.Fatalf("late job=%#v", got)
	}
}

func TestComputerRestartDoesNotReissueUncertainActions(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	r := activeComputerRun(t, s)
	claimed, _ := s.store.queueComputerJob(r, id, "files.read", json.RawMessage(`{"path":"claimed"}`))
	pending, _ := s.store.queueComputerJob(r, id, "files.read", json.RawMessage(`{"path":"pending"}`))
	if _, err := s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	if err := recoverComputerJobs(s.store.db); err != nil {
		t.Fatal(err)
	}
	gotClaimed, _ := s.store.computerJob(claimed.ID, id)
	gotPending, _ := s.store.computerJob(pending.ID, id)
	if gotClaimed.Status != "interrupted" || gotPending.Status != "pending" {
		t.Fatalf("claimed=%q pending=%q", gotClaimed.Status, gotPending.Status)
	}
	if ok, _ := s.store.SetRunStatus(r.ID, "interrupted", "restart"); !ok {
		t.Fatal("could not interrupt parent")
	}
	if err := recoverComputerJobs(s.store.db); err != nil {
		t.Fatal(err)
	}
	gotPending, _ = s.store.computerJob(pending.ID, id)
	if gotPending.Status != "cancelled" {
		t.Fatalf("inactive parent pending status=%q", gotPending.Status)
	}
}

func TestComputerToolCancellationMarksJobAndLateResultFails(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read")
	tool := s.computerTools(Run{})[1]
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := tool.Execute(ctx, json.RawMessage(`{"computer_id":"`+id+`","action":"files.read","args":{"path":"x"}}`))
		done <- err
	}()
	var jobID, status string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := s.store.db.QueryRow(`SELECT id,status FROM computer_jobs WHERE device_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&jobID, &status); err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("tool err=%v", err)
	}
	if err = s.store.db.QueryRow(`SELECT id,status FROM computer_jobs WHERE device_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&jobID, &status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("status=%q", status)
	}
	if err = s.store.completeComputerJob(id, token, jobID, json.RawMessage(`{"content":"late"}`), ""); err == nil {
		t.Fatal("cancelled job accepted late result")
	}
}

func TestComputerHTTPDevicePollRequiresBearerAndHeartbeats(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.list")
	req := httptest.NewRequest(http.MethodGet, "/api/computers/"+id+"/jobs", nil)
	w := httptest.NewRecorder()
	if !s.routeComputers(w, req, "computers/"+id+"/jobs") || w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated poll status=%d body=%s", w.Code, w.Body.String())
	}
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	_, _ = s.store.db.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, old, id)
	req = httptest.NewRequest(http.MethodGet, "/api/computers/"+id+"/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	s.routeComputers(w, req, "computers/"+id+"/jobs")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"job":null`) {
		t.Fatalf("poll status=%d body=%s", w.Code, w.Body.String())
	}
	var last string
	_ = s.store.db.QueryRow(`SELECT last_seen FROM computers WHERE id=?`, id).Scan(&last)
	if last <= old {
		t.Fatalf("heartbeat did not advance: %s", last)
	}
}

func TestComputerOwnerActionCanBeClaimedCompletedAndRead(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.list")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/computers/"+id+"/actions", strings.NewReader(`{"action":"files.list","args":{"path":"."}}`))
	s.routeComputers(w, req, "computers/"+id+"/actions")
	if w.Code != http.StatusCreated {
		t.Fatalf("queue status=%d body=%s", w.Code, w.Body.String())
	}
	var queued ComputerJob
	if err := json.Unmarshal(w.Body.Bytes(), &queued); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.store.claimComputerJob(id, token)
	if err != nil || claimed == nil || claimed.ID != queued.ID {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = s.store.completeComputerJob(id, token, queued.ID, json.RawMessage(`{"entries":["a.txt"]}`), ""); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/computers/"+id+"/actions/"+queued.ID, nil)
	s.routeComputers(w, req, "computers/"+id+"/actions/"+queued.ID)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"completed"`) || !strings.Contains(w.Body.String(), `"a.txt"`) {
		t.Fatalf("read status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestComputerScreenResultRequiresValidatedImageDataURL(t *testing.T) {
	bad := []json.RawMessage{
		json.RawMessage(`{"image_url":"https://example.com/a.png"}`),
		json.RawMessage(`{"image_url":"data:text/html;base64,SGk="}`),
		json.RawMessage(`{"image_url":"data:image/png;base64,%%%"}`),
	}
	for _, raw := range bad {
		if err := validateComputerResult("screen.capture", raw); err == nil {
			t.Errorf("accepted invalid screen result %s", raw)
		}
	}
	var pngData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	valid, _ := json.Marshal(map[string]string{"image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngData.Bytes())})
	if err := validateComputerResult("screen.capture", valid); err != nil {
		t.Fatalf("valid data URL rejected: %v", err)
	}
}

func TestComputerFailureResultDoesNotRequireScreenshot(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "screen.capture")
	j, err := s.store.queueComputerJob(Run{}, id, "screen.capture", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.claimComputerJob(id, token); err != nil {
		t.Fatal(err)
	}
	if err = s.store.completeComputerJob(id, token, j.ID, json.RawMessage(`null`), "screen recording permission denied"); err != nil {
		t.Fatalf("device failure could not complete: %v", err)
	}
	got, _ := s.store.computerJob(j.ID, id)
	if got.Status != "failed" || got.Error == "" {
		t.Fatalf("failure job=%#v", got)
	}
}

func TestComputerCapabilityRefreshCanDenyAllAndCancelsPending(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read", "screen.capture")
	j, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	caps, err := s.store.updateComputerCapabilities(id, token, []string{})
	if err != nil || len(caps) != 0 {
		t.Fatalf("caps=%v err=%v", caps, err)
	}
	got, _ := s.store.computerJob(j.ID, id)
	if got.Status != "cancelled" {
		t.Fatalf("removed capability left job %q", got.Status)
	}
	if _, err = s.store.queueComputerJob(Run{}, id, "screen.capture", json.RawMessage(`{}`)); err == nil {
		t.Fatal("removed capability remained usable")
	}
	if _, err = s.store.updateComputerCapabilities(id, "wrong", []string{"files.read"}); err == nil {
		t.Fatal("unauthenticated capability update")
	}
}

func TestComputerCapabilityWithdrawalCancelsClaimedJob(t *testing.T) {
	s := computerTestServer(t)
	id, token := pairedComputer(t, s, "files.read", "screen.capture")
	job, err := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.store.claimComputerJob(id, token)
	if err != nil || claimed == nil || claimed.ID != job.ID {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if _, err := s.store.updateComputerCapabilities(id, token, []string{"screen.capture"}); err != nil {
		t.Fatal(err)
	}
	current, err := s.store.computerJob(job.ID, id)
	if err != nil || current.Status != "cancelled" {
		t.Fatalf("withdrawn job=%+v err=%v", current, err)
	}
	if err := s.store.completeComputerJob(id, token, job.ID, json.RawMessage(`{"data":"late"}`), ""); err == nil {
		t.Fatal("accepted a result after capability withdrawal")
	}
}

func TestComputerOwnerReadTerminatesExpiredAndInactiveJobs(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "files.read")
	expired, _ := s.store.queueComputerJob(Run{}, id, "files.read", json.RawMessage(`{"path":"x"}`))
	_, _ = s.store.db.Exec(`UPDATE computer_jobs SET expires_at=? WHERE id=?`, time.Now().UTC().Add(-time.Second).Format(time.RFC3339Nano), expired.ID)
	got, err := s.store.computerJob(expired.ID, id)
	if err != nil || got.Status != "expired" {
		t.Fatalf("expired job=%#v err=%v", got, err)
	}

	r := activeComputerRun(t, s)
	inactive, _ := s.store.queueComputerJob(r, id, "files.read", json.RawMessage(`{"path":"y"}`))
	_, _ = s.store.SetRunStatus(r.ID, "cancelled", "cancelled")
	got, err = s.store.computerJob(inactive.ID, id)
	if err != nil || got.Status != "cancelled" {
		t.Fatalf("inactive job=%#v err=%v", got, err)
	}
}

func TestComputerQueueIsBoundedPerDevice(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "files.list")
	for i := 0; i < maxOutstandingJobs; i++ {
		if _, err := s.store.queueComputerJob(Run{}, id, "files.list", json.RawMessage(`{}`)); err != nil {
			t.Fatalf("job %d: %v", i, err)
		}
	}
	if _, err := s.store.queueComputerJob(Run{}, id, "files.list", json.RawMessage(`{}`)); err == nil {
		t.Fatal("unbounded outstanding jobs")
	}
}

func TestComputerToolHonorsAlreadyCancelledContextBeforeQueue(t *testing.T) {
	s := computerTestServer(t)
	id, _ := pairedComputer(t, s, "files.list")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.computerTools(Run{})[1].Execute(ctx, json.RawMessage(`{"computer_id":"`+id+`","action":"files.list","args":{}}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	var count int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE device_id=?`, id).Scan(&count)
	if count != 0 {
		t.Fatalf("cancelled context queued %d jobs", count)
	}
}

func TestComputerArgumentValidationRejectsArbitraryExecutionShape(t *testing.T) {
	bad := []struct{ action, args string }{
		{"shell.exec", `{"command":"whoami"}`},
		{"files.read", `{"path":"../secret"}`},
		{"files.read", `{"path":"/etc/passwd"}`},
		{"files.read", `{"path":"x","command":"whoami"}`},
		{"desktop.key", `{"key":"x","modifiers":["unknown"]}`},
		{"desktop.click", `{"x":-1,"y":0}`},
		{"screen.capture", `null`},
	}
	for _, tc := range bad {
		if err := validateComputerArgs(tc.action, json.RawMessage(tc.args)); err == nil {
			t.Errorf("accepted %s %s", tc.action, tc.args)
		}
	}
}
