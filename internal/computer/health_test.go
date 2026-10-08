package computer

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func healthTestClient(t *testing.T, handler http.HandlerFunc, ensure func(context.Context) error) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable in this test environment: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	client, err := New(Config{Socket: socket, Client: NewUnixHTTPClient(socket, 5*time.Second), Ensure: ensure})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestActionWithinBoundsOnlyTheGuestAnswer(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	client := healthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" {
			_ = json.NewEncoder(w).Encode(Info{State: "ready"})
			return
		}
		select { // a guest that never answers
		case <-release:
		case <-r.Context().Done():
		}
	}, func(context.Context) error { return nil })
	started := time.Now()
	_, err := client.ActionWithin(context.Background(), Action{BotID: "bot", Name: "browser.snapshot"}, 80*time.Millisecond)
	var deadline *ActionDeadlineError
	if !errors.As(err, &deadline) || deadline.Limit != 80*time.Millisecond {
		t.Fatalf("err = %v", err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("action deadline did not bound the call")
	}
	// A caller cancellation is not reported as the action's own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err = client.ActionWithin(ctx, Action{BotID: "bot", Name: "browser.snapshot"}, time.Second)
	if errors.As(err, &deadline) {
		t.Fatalf("caller deadline misreported: %v", err)
	}
}

func TestHealthAndRecoverNeverAdmitAndClassifyReplies(t *testing.T) {
	var recoverStatus = http.StatusConflict
	client := healthTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/health":
			_ = json.NewEncoder(w).Encode(GuestHealth{State: "ready", Guest: "unresponsive", Error: "timeout"})
		case "/v1/recover":
			w.WriteHeader(recoverStatus)
			_, _ = w.Write([]byte(`{"error":"guest is responsive; not restarting"}`))
		default:
			http.NotFound(w, r)
		}
	}, func(context.Context) error { t.Error("health probes must not admit (start) the computer"); return nil })
	health, err := client.Health(context.Background())
	if err != nil || health.Guest != "unresponsive" {
		t.Fatalf("health = %+v, %v", health, err)
	}
	if err := client.Recover(context.Background()); !errors.Is(err, ErrGuestResponsive) {
		t.Fatalf("recover on responsive guest = %v", err)
	}
	recoverStatus = http.StatusAccepted
	if err := client.Recover(context.Background()); err != nil {
		t.Fatalf("accepted recover = %v", err)
	}

	old := healthTestClient(t, http.NotFound, nil)
	if _, err := old.Health(context.Background()); !errors.Is(err, ErrHealthUnsupported) {
		t.Fatalf("old manager health = %v", err)
	}
}
