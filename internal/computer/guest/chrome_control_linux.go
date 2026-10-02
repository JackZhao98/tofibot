//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// chromeController owns replacement Chrome processes after the
// initial desktop start. The dock talks to it over a mode-0600 Unix socket;
// Chrome is never launched directly by a desktop entry or an untracked shell.
type chromeController struct {
	d          *desktop
	env        []string
	socketPath string
	listener   net.Listener
	server     *http.Server
	serveDone  chan struct{}

	mu       sync.Mutex
	chrome   *exec.Cmd
	stopping bool
	stopped  bool
	stopErr  error
	stopDone chan struct{}
	stopFunc func(context.Context, *desktop, *exec.Cmd) error
}

var chromeControllers sync.Map // map[*desktop]*chromeController

// The launcher is intentionally a tiny client. It never starts Chrome; it
// asks the Go controller to create a window or replace an exited process.
const chromeControlClientScript = `import socket,sys
s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM)
s.settimeout(15)
s.connect(sys.argv[1])
s.sendall(b"POST /v1/chrome/reopen HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
status=s.makefile("rb").readline(4096)
if not status.startswith(b"HTTP/1.0 200") and not status.startswith(b"HTTP/1.1 200"):
    raise SystemExit("Chrome controller rejected reopen")
while s.recv(4096):
    pass
s.close()`

func startChromeController(d *desktop, env []string) error {
	if d == nil || d.runtimeDir == "" || d.chrome == nil || d.chrome.Process == nil {
		return errors.New("Chrome controller requires a running desktop process")
	}
	socketPath := filepath.Join(d.runtimeDir, "chrome-control.sock")
	if info, err := os.Lstat(socketPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("Chrome control socket is a symlink")
		}
		if err := os.Remove(socketPath); err != nil {
			return fmt.Errorf("remove stale Chrome control socket: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect Chrome control socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen for Chrome control: %w", err)
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		return fmt.Errorf("protect Chrome control socket: %w", err)
	}
	c := &chromeController{
		d: d, env: append([]string(nil), env...), socketPath: socketPath,
		listener: listener, serveDone: make(chan struct{}), chrome: d.chrome,
	}
	c.server = &http.Server{
		Handler:           http.HandlerFunc(c.handle),
		ReadHeaderTimeout: 2 * time.Second,
		WriteTimeout:      13 * time.Second,
		IdleTimeout:       2 * time.Second,
		MaxHeaderBytes:    4096,
	}
	if _, loaded := chromeControllers.LoadOrStore(d, c); loaded {
		_ = listener.Close()
		return errors.New("Chrome controller already exists")
	}
	go func() {
		_ = c.server.Serve(listener)
		close(c.serveDone)
	}()
	return nil
}

func chromeControllerFor(d *desktop) *chromeController {
	if d == nil {
		return nil
	}
	value, ok := chromeControllers.Load(d)
	if !ok {
		return nil
	}
	c, _ := value.(*chromeController)
	return c
}

func (c *chromeController) handle(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chrome/reopen" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.Body != nil {
		_ = r.Body.Close()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	restarted, err := c.reopen(ctx)
	if err != nil {
		writeJSON(w, http.StatusConflict, ActionResponse{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse{OK: true, Result: map[string]any{
		"reopened": true, "restarted": restarted,
	}})
}

// reopen serializes the dock request with stop. If Chrome is still alive, the
// existing debugging endpoint creates and focuses one blank target. If the
// browser process was closed by the user, the managed command is reaped and
// restarted with the original profile/display/port before returning.
func (c *chromeController) reopen(ctx context.Context) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.stopping || c.stopped {
		return false, errors.New("Chrome desktop is stopping")
	}
	// A close click can precede the dock click by milliseconds. Retry while
	// the old process drains its shutdown instead of reporting a dead window
	// as successfully restored.
	for c.chrome != nil && c.chrome.Process != nil && processIsAlive(c.chrome.Process.Pid) {
		err := openChromeWindow(ctx, c.d)
		if err == nil {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("reopen Chrome window: %w", err)
		case <-time.After(100 * time.Millisecond):
		}
	}

	if c.chrome != nil && c.chrome.Process != nil && c.chrome.ProcessState == nil {
		// The process exited without the managed stop path. Reap it before
		// replacing the command so stop never calls Wait twice on one command.
		_ = c.chrome.Wait()
	}
	// A closed browser always starts fresh; preserve cookies and login data only.
	if err := clearBrowserSessionState(c.d.profile); err != nil {
		return false, fmt.Errorf("clear previous Chrome session: %w", err)
	}
	if err := startChromeWithReadiness(ctx, c.d, c.env); err != nil {
		return false, fmt.Errorf("restart Google Chrome: %w", err)
	}
	c.chrome = c.d.chrome
	return true, nil
}

func waitForChromeReady(ctx context.Context, d *desktop) error {
	return waitForChromeReadyWithTimeout(ctx, d, chromeReadyTimeout)
}

const (
	chromeReadyTimeout = 30 * time.Second
	chromeProbeTimeout = 750 * time.Millisecond
)

type chromeReadyFailureKind string

const (
	chromeExitedBeforeReady chromeReadyFailureKind = "exited"
	chromeReadyTimedOut     chromeReadyFailureKind = "timeout"
	chromeReadyCanceled     chromeReadyFailureKind = "canceled"
)

type chromeReadyError struct {
	kind       chromeReadyFailureKind
	probeErr   error
	processErr error
}

func (e *chromeReadyError) Error() string {
	var message string
	switch e.kind {
	case chromeExitedBeforeReady:
		message = "Chrome exited before CDP became ready"
	case chromeReadyTimedOut:
		message = "Chrome CDP readiness timed out"
	default:
		message = "Chrome CDP readiness was canceled"
	}
	if e.processErr != nil {
		message += ": " + e.processErr.Error()
	}
	if e.probeErr != nil {
		message += "; last CDP probe: " + e.probeErr.Error()
	}
	return message
}

func (e *chromeReadyError) Unwrap() error {
	if e.kind == chromeReadyCanceled {
		return context.Canceled
	}
	if e.kind == chromeReadyTimedOut {
		return context.DeadlineExceeded
	}
	return e.processErr
}

func waitForChromeReadyWithTimeout(ctx context.Context, d *desktop, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastProbeErr error
	for {
		probeCtx, probeCancel := context.WithTimeout(readyCtx, chromeProbeTimeout)
		_, err := chromeHTTP(probeCtx, d, http.MethodGet, "/json")
		probeCancel()
		if err == nil {
			return nil
		}
		lastProbeErr = err
		if d != nil && d.chrome != nil && d.chrome.Process != nil && !processIsAlive(d.chrome.Process.Pid) {
			processErr := d.chrome.Wait()
			return &chromeReadyError{kind: chromeExitedBeforeReady, probeErr: lastProbeErr, processErr: processErr}
		}
		if ctx.Err() != nil {
			return &chromeReadyError{kind: chromeReadyCanceled, probeErr: lastProbeErr}
		}
		if readyCtx.Err() != nil {
			return &chromeReadyError{kind: chromeReadyTimedOut, probeErr: lastProbeErr}
		}
		select {
		case <-ctx.Done():
			return &chromeReadyError{kind: chromeReadyCanceled, probeErr: lastProbeErr}
		case <-readyCtx.Done():
			return &chromeReadyError{kind: chromeReadyTimedOut, probeErr: lastProbeErr}
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func startChromeWithReadiness(ctx context.Context, d *desktop, env []string) error {
	if d.chromeLog == nil {
		d.chromeLog = &limitedBuffer{limit: 16 * 1024, tail: true}
	}
	workarea := chromeDesktopWorkarea(ctx, env)
	for attempt := 0; attempt < 2; attempt++ {
		cmd, err := startDesktopProcess("google-chrome-stable", chromeStartArgs(d.profile, d.display, d.remotePort, workarea...), env, d.chromeLog)
		if err != nil {
			return err
		}
		d.chrome = cmd
		err = waitForChromeReady(ctx, d)
		if err == nil {
			return nil
		}
		var readyErr *chromeReadyError
		if attempt == 0 && errors.As(err, &readyErr) && readyErr.kind == chromeExitedBeforeReady && ctx.Err() == nil {
			if cmd.ProcessState == nil {
				_ = stopChromeProcessDirect(context.Background(), d, cmd)
			}
			d.chrome = nil
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if cleanupErr := clearStaleChromeLocks(d.profile); cleanupErr != nil {
				return chromeErrorWithLog(fmt.Errorf("clean up failed Chrome start: %w (readiness: %v)", cleanupErr, err), d)
			}
			if cleanupErr := clearBrowserSessionState(d.profile); cleanupErr != nil {
				return chromeErrorWithLog(fmt.Errorf("clean up failed Chrome session: %w (readiness: %v)", cleanupErr, err), d)
			}
			continue
		}
		if cmd.ProcessState == nil {
			_ = stopChromeProcessDirect(context.Background(), d, cmd)
		}
		d.chrome = nil
		return chromeErrorWithLog(err, d)
	}
	return errors.New("Chrome startup retry exhausted")
}

func chromeErrorWithLog(err error, d *desktop) error {
	if d != nil && d.chromeLog != nil {
		if detail := strings.TrimSpace(d.chromeLog.String()); detail != "" {
			return fmt.Errorf("%w: Chrome log tail: %s", err, detail)
		}
	}
	return err
}

func openChromeWindow(ctx context.Context, d *desktop) error {
	// A double click can queue two requests before the first window is painted.
	// Reuse an existing normal page first so the Dock never creates duplicate
	// tabs or mistakes an extension/background target for the visible browser.
	body, err := chromeHTTP(ctx, d, http.MethodGet, "/json/list")
	if err != nil {
		return err
	}
	var pages []browserTarget
	if err := json.Unmarshal(body, &pages); err != nil {
		return fmt.Errorf("decode Chrome targets: %w", err)
	}
	// Restore the native window first to preserve its selected tab.
	hasPage := false
	for _, page := range pages {
		if page.Type == "page" {
			hasPage = true
		}
	}
	if hasPage {
		restore := exec.CommandContext(ctx, "sh", "-c", chromeRestoreWindowScript)
		restore.Env = []string{"DISPLAY=" + d.display, "PATH=/usr/local/bin:/usr/bin:/bin"}
		if restore.Run() == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			// If close was already in flight, do not acknowledge a disappearing window.
			latest, err := chromeHTTP(ctx, d, http.MethodGet, "/json/list")
			if err != nil {
				return err
			}
			var current []browserTarget
			if err := json.Unmarshal(latest, &current); err != nil {
				return err
			}
			for _, page := range current {
				if page.Type == "page" {
					return nil
				}
			}
			return errors.New("Chrome window is closing")
		}
	}
	for i := len(pages) - 1; i >= 0; i-- {
		if pages[i].Type != "page" || strings.TrimSpace(pages[i].ID) == "" {
			continue
		}
		if _, err := chromeHTTP(ctx, d, http.MethodGet, "/json/activate/"+url.PathEscape(pages[i].ID)); err != nil {
			return fmt.Errorf("activate Chrome window: %w", err)
		}
		return nil
	}
	body, err = chromeHTTP(ctx, d, http.MethodPut, "/json/new?"+url.QueryEscape("about:blank"))
	if err != nil {
		return err
	}
	var target struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &target); err != nil || strings.TrimSpace(target.ID) == "" {
		return errors.New("Chrome returned an invalid new-window target")
	}
	if _, err := chromeHTTP(ctx, d, http.MethodGet, "/json/activate/"+url.PathEscape(target.ID)); err != nil {
		return fmt.Errorf("activate Chrome window: %w", err)
	}
	return nil
}

func processIsAlive(pid int) bool {
	if pid <= 1 {
		return false
	}
	stat, err := readProcStat(pid)
	if err == nil {
		return stat.state != 'Z'
	}
	return syscall.Kill(pid, syscall.Signal(0)) == nil
}

func (c *chromeController) stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.stopped {
		err := c.stopErr
		c.mu.Unlock()
		return err
	}
	if c.stopping {
		done := c.stopDone
		c.mu.Unlock()
		select {
		case <-done:
			c.mu.Lock()
			err := c.stopErr
			c.mu.Unlock()
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	c.stopping = true
	c.stopDone = make(chan struct{})
	listener := c.listener
	server := c.server
	cmd := c.chrome
	c.mu.Unlock()

	if listener != nil {
		_ = listener.Close()
	}
	if server != nil {
		_ = server.Close()
	}
	stopFunc := c.stopFunc
	if stopFunc == nil {
		stopFunc = stopChromeProcessDirect
	}
	err := stopFunc(ctx, c.d, cmd)

	c.mu.Lock()
	c.stopped = true
	c.stopErr = err
	c.chrome = nil
	close(c.stopDone)
	c.mu.Unlock()
	chromeControllers.Delete(c.d)
	if c.serveDone != nil {
		select {
		case <-c.serveDone:
		case <-time.After(2 * time.Second):
		}
	}
	_ = os.Remove(c.socketPath)
	return err
}

func stopManagedChromeIfPresent(ctx context.Context, d *desktop) (bool, error) {
	c := chromeControllerFor(d)
	if c == nil {
		return false, nil
	}
	return true, c.stop(ctx)
}

const chromeRestoreWindowScript = `
for class in google-chrome google-chrome-stable; do
  ids=$(xdotool search --class "$class" 2>/dev/null || true)
  for id in $ids; do
    if xdotool windowmap "$id" >/dev/null 2>&1 && xdotool windowactivate --sync "$id" >/dev/null 2>&1; then exit 0; fi
  done
done
exit 1
`
