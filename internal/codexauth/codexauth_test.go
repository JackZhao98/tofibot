package codexauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDeviceFlowPersistsPrivateCredential(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "device-1", "user_code": "ABCD", "interval": "1", "expires_in": 60})
		case "/device":
			polls.Add(1)
			json.NewEncoder(w).Encode(map[string]string{"authorization_code": "auth-code", "code_verifier": "verifier"})
		case "/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access-token", "refresh_token": "refresh-token", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	manager, err := newManager(t.TempDir(), server.Client(), endpoints{userCode: server.URL + "/usercode", device: server.URL + "/device", token: server.URL + "/token", issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if session.VerificationURL != deviceURL || session.UserCode != "ABCD" || session.Interval != 1 {
		t.Fatalf("unexpected session: %#v", session)
	}
	manager.mu.Lock()
	p := manager.pending[session.SessionID]
	p.interval = 0
	manager.pending[session.SessionID] = p
	manager.mu.Unlock()
	status, err := manager.Poll(context.Background(), session.SessionID)
	if err != nil || !status.Connected || status.ExpiresAt == 0 {
		t.Fatalf("Poll() = %#v, %v", status, err)
	}
	if polls.Load() != 1 {
		t.Fatalf("poll count = %d, want 1", polls.Load())
	}
	credential, err := manager.Credential(context.Background())
	if err != nil || credential != "access-token\x00" {
		t.Fatalf("Credential() = %q, %v", credential, err)
	}
	if got := manager.Status(); !got.Connected || got.Pending {
		t.Fatalf("Status() = %#v", got)
	}
	info, err := os.Stat(filepath.Join(manager.dataDir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("credential mode = %o, want 600", info.Mode().Perm())
	}
}

func TestCredentialRefreshIsSerializedAndDoesNotExposeToken(t *testing.T) {
	var refreshes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "refresh_token": "new-refresh", "expires_in": 3600})
	}))
	defer server.Close()
	manager, err := newManager(t.TempDir(), server.Client(), endpoints{token: server.URL + "/token", issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.save(token{AccessToken: "old-access", RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	results := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			value, callErr := manager.Credential(context.Background())
			results <- value
			errs <- callErr
		}()
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if got := <-results; !strings.HasPrefix(got, "new-access\x00") {
			t.Fatalf("unexpected credential format: %q", got)
		}
	}
	if got := refreshes.Load(); got != 1 {
		t.Fatalf("refresh requests = %d, want 1", got)
	}
}

func TestAccessOnlySnapshotDoesNotRefresh(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("access-only credential must not call token endpoint")
	}))
	defer server.Close()
	manager, err := newManager(t.TempDir(), server.Client(), endpoints{token: server.URL, issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveAccessOnlyCredential("snapshot-access", "account", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	credential, err := manager.Credential(context.Background())
	if err != nil || credential != "snapshot-access\x00account" {
		t.Fatalf("valid access-only credential = %q, %v", credential, err)
	}
	if !manager.Status().Connected {
		t.Fatal("valid access-only snapshot should be connected")
	}
	if err := manager.SaveAccessOnlyCredential("expired-access", "account", time.Now().Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Credential(context.Background()); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired access-only credential error = %v", err)
	}
	if manager.Status().Connected {
		t.Fatal("expired access-only snapshot should not be connected")
	}
	if requests.Load() != 0 {
		t.Fatalf("token endpoint requests = %d, want 0", requests.Load())
	}
}

func TestPollHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()
	manager, err := newManager(t.TempDir(), server.Client(), endpoints{device: server.URL, issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	manager.pending["session"] = pendingSession{deviceAuthID: "device", userCode: "code", expiresAt: time.Now().Add(time.Minute)}
	manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := manager.Poll(ctx, "session"); err == nil || strings.Contains(err.Error(), "device") {
		t.Fatalf("Poll() error = %v; error must be cancellation-safe and must not expose credentials", err)
	}
}

func TestDisconnectInvalidatesBlockedPoll(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "device", "user_code": "code", "interval": "1", "expires_in": 60})
		case "/device":
			json.NewEncoder(w).Encode(map[string]string{"authorization_code": "auth", "code_verifier": "verify"})
		case "/token":
			close(entered)
			<-release
			json.NewEncoder(w).Encode(map[string]any{"access_token": "late-access", "refresh_token": "late-refresh", "expires_in": 3600})
		}
	}))
	defer server.Close()
	manager, err := newManager(t.TempDir(), server.Client(), endpoints{userCode: server.URL + "/usercode", device: server.URL + "/device", token: server.URL + "/token", issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	p := manager.pending[session.SessionID]
	p.interval = 0
	manager.pending[session.SessionID] = p
	manager.mu.Unlock()
	pollDone := make(chan error, 1)
	go func() {
		_, pollErr := manager.Poll(context.Background(), session.SessionID)
		pollDone <- pollErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Poll did not reach token exchange")
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-pollDone; err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("late Poll error = %v, want expired session", err)
	}
	if got := manager.Status(); got.Connected {
		t.Fatalf("late Poll resurrected credentials: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(manager.dataDir, fileName)); !os.IsNotExist(err) {
		t.Fatalf("credential file after Disconnect = %v", err)
	}
}

func TestDisconnectInvalidatesBlockedStart(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "late-device", "user_code": "LATE", "interval": "1", "expires_in": 60})
	}))
	defer server.Close()
	manager, err := newManager(t.TempDir(), server.Client(), endpoints{userCode: server.URL, issuer: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(context.Background())
		startDone <- startErr
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Start did not reach device endpoint")
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-startDone; err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("late Start error = %v, want cancelled session", err)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.pending) != 0 {
		t.Fatalf("late Start recreated pending sessions: %#v", manager.pending)
	}
}
