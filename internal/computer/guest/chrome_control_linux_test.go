//go:build linux

package guest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForChromeReadyClassifiesEarlyExitAndKeepsLastProbe(t *testing.T) {
	log := &limitedBuffer{limit: 1024, tail: true}
	cmd, err := startDesktopProcess("sh", []string{"-c", "exit 7"}, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	d := &desktop{chrome: cmd, remotePort: 1}
	err = waitForChromeReadyWithTimeout(context.Background(), d, time.Second)
	var readyErr *chromeReadyError
	if !errors.As(err, &readyErr) || readyErr.kind != chromeExitedBeforeReady {
		t.Fatalf("error=%v, want early-exit readiness error", err)
	}
	if !strings.Contains(err.Error(), "last CDP probe") {
		t.Fatalf("error=%v, missing last CDP probe", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("error=%v, missing probe detail", err)
	}
}

func TestWaitForChromeReadyClassifiesRequestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForChromeReadyWithTimeout(ctx, &desktop{remotePort: 1}, time.Second)
	var readyErr *chromeReadyError
	if !errors.As(err, &readyErr) || readyErr.kind != chromeReadyCanceled || !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want canceled readiness error", err)
	}
}

func TestWaitForChromeReadyClassifiesTimeout(t *testing.T) {
	err := waitForChromeReadyWithTimeout(context.Background(), &desktop{remotePort: 1}, 20*time.Millisecond)
	var readyErr *chromeReadyError
	if !errors.As(err, &readyErr) || readyErr.kind != chromeReadyTimedOut || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v, want timeout readiness error", err)
	}
}

func TestChromeLogKeepsTail(t *testing.T) {
	log := &limitedBuffer{limit: 8, tail: true}
	_, _ = log.Write([]byte("1234567890"))
	if got := log.String(); got != "34567890" {
		t.Fatalf("log tail=%q, want %q", got, "34567890")
	}
}

func TestChromeControlHandlerRejectsUnexpectedRequests(t *testing.T) {
	c := &chromeController{}
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/v1/chrome/reopen", http.StatusMethodNotAllowed},
		{http.MethodPost, "/other", http.StatusNotFound},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		c.handle(res, req)
		if res.Code != tc.status {
			t.Fatalf("%s %s status=%d, want %d", tc.method, tc.path, res.Code, tc.status)
		}
	}
}

func TestChromeLauncherUsesManagedSocketWhenNoWindowExists(t *testing.T) {
	root, err := os.MkdirTemp("", "tofi-chrome-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	botDir := filepath.Join(root, "bots", testBot)
	runtimeDir := filepath.Join(root, "runtime", testBot)
	iconDir := filepath.Join(root, "icons")
	for _, dir := range []string{botDir, runtimeDir, iconDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := filepath.Join(root, "launch-files.sh")
	chrome := filepath.Join(root, "launch-chrome.sh")
	terminal := filepath.Join(root, "launch-terminal.sh")
	if err := writeDesktopLaunchers(files, chrome, terminal, botDir, filepath.Join(root, "home"), ":199", runtimeDir, iconDir); err != nil {
		t.Fatal(err)
	}

	// The real Linux test image has xdotool and Python. The stub makes the
	// launcher take the no-window path while the real Python client exercises
	// the generated AF_UNIX HTTP request.
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(binDir, "xdotool")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(runtimeDir, "chrome-control.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mu sync.Mutex
	var method, path string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method, path = r.Method, r.URL.Path
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()

	cmd := exec.Command(chrome)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launcher failed: %v (%s)", err, output)
	}
	mu.Lock()
	gotMethod, gotPath := method, path
	mu.Unlock()
	if gotMethod != http.MethodPost || gotPath != "/v1/chrome/reopen" {
		t.Fatalf("managed launcher request=%s %s, want POST /v1/chrome/reopen", gotMethod, gotPath)
	}
}

func TestChromeControllerConcurrentStopAndReopen(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	c := &chromeController{d: &desktop{}, stopFunc: func(context.Context, *desktop, *exec.Cmd) error {
		calls.Add(1)
		close(entered)
		<-release
		return nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 2)
	go func() { done <- c.stop(ctx) }()
	<-entered
	go func() { done <- c.stop(ctx) }()
	if _, err := c.reopen(ctx); err == nil {
		t.Error("reopened while stopping")
	}
	close(release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("stop invoked %d times", calls.Load())
	}
}
