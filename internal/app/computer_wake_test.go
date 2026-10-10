package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// wakeServer fakes the host manager: /v1/info reports *state, /v1/retry counts
// resume requests the way the manager's prepare() would wake a hibernated VM.
func wakeServer(t *testing.T, state *atomic.Value) (*Server, *atomic.Int32) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tfw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := dir + "/control.sock"
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix sockets unavailable: %v", err)
	}
	retries := &atomic.Int32{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(computer.Info{Kind: "firecracker", State: state.Load().(string)})
	})
	mux.HandleFunc("/v1/retry", func(w http.ResponseWriter, _ *http.Request) {
		retries.Add(1)
		state.Store("resuming")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{}`))
	})
	server := httptest.NewUnstartedServer(mux)
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	client, err := computer.New(computer.Config{Socket: socket})
	if err != nil {
		t.Fatal(err)
	}
	return &Server{microVM: client}, retries
}

func wakeRequest(s *Server, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.routeComputers(rec, httptest.NewRequest(http.MethodPost, "/api/computers/"+id+"/wake", nil), "computers/"+id+"/wake")
	return rec
}

func TestWakeResumesAHibernatedComputerOnce(t *testing.T) {
	var state atomic.Value
	state.Store("hibernated")
	s, retries := wakeServer(t, &state)
	if rec := wakeRequest(s, "firecracker"); rec.Code != http.StatusAccepted {
		t.Fatalf("wake = %d %s", rec.Code, rec.Body.String())
	}
	// A second open while it resumes is a no-op.
	if rec := wakeRequest(s, "firecracker"); rec.Code != http.StatusOK {
		t.Fatalf("second wake = %d %s", rec.Code, rec.Body.String())
	}
	if retries.Load() != 1 {
		t.Fatalf("resume requested %d times, want 1", retries.Load())
	}
}

func TestWakeIsANoOpUnlessHibernated(t *testing.T) {
	for _, st := range []string{"ready", "resuming", "starting", "hibernating", "stopped", "error"} {
		var state atomic.Value
		state.Store(st)
		s, retries := wakeServer(t, &state)
		rec := wakeRequest(s, "firecracker")
		if rec.Code != http.StatusOK || retries.Load() != 0 {
			t.Fatalf("%s: wake = %d, resume requests %d", st, rec.Code, retries.Load())
		}
	}
}

func TestWakeOnlyReachesThisAccountsComputer(t *testing.T) {
	var state atomic.Value
	state.Store("hibernated")
	s, retries := wakeServer(t, &state)
	// Another computer id is never routed to this account's VM.
	if rec := wakeRequest(s, "someone-elses-computer"); rec.Code == http.StatusAccepted || retries.Load() != 0 {
		t.Fatalf("foreign id woke the VM: %d (%d)", rec.Code, retries.Load())
	}
	// Without a session the gateway rejects the request before any route runs.
	g := accountFixture(t)
	w := accountRequest(g, http.MethodPost, "/api/computers/firecracker/wake", "", nil)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated wake = %d %s", w.Code, w.Body.String())
	}
}
