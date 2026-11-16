package guest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// browser.read runs as a shell helper and must reach the same Chrome that desktop.* and browser.navigate use. The
// guest hands it that Chrome's DevTools port in TOFI_DEVTOOLS_PORT while the shared desktop is up: finding Chrome in
// the process table broke when Chrome's command line stopped looking the way the helper expected (every
// browser.read on the test machine answered chrome_not_running from 2026-10-31 on, while navigate kept working).
func TestShellGetsTheSharedChromeDevToolsPort(t *testing.T) {
	s := newTestService(t)
	run := func() string {
		t.Helper()
		result, err := s.action(context.Background(), ActionRequest{
			BotID: testBot, RunID: "devtools-port", Action: "shell.exec", Source: ActionSourceModel,
			Args: json.RawMessage(`{"command":"printf '%s' \"${TOFI_DEVTOOLS_PORT-unset}\""}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return result.(map[string]any)["stdout"].(string)
	}
	if got := run(); got != "unset" {
		t.Fatalf("no desktop: %q", got)
	}
	d := &desktop{botID: sharedDesktopKey, shared: true, remotePort: 40123, ready: make(chan struct{})}
	s.mu.Lock()
	s.sharedDesktop = d
	s.mu.Unlock()
	if got := run(); got != "unset" {
		t.Fatalf("a desktop still starting: %q", got)
	}
	s.mu.Lock()
	close(d.ready)
	d.readyClosed = true
	s.mu.Unlock()
	if got := run(); got != "40123" {
		t.Fatalf("a ready shared desktop: %q", got)
	}
	s.mu.Lock()
	d.startErr = errors.New("chrome did not start")
	s.mu.Unlock()
	if got := run(); got != "unset" {
		t.Fatalf("a desktop whose start failed: %q", got)
	}
	s.mu.Lock()
	s.sharedDesktop = nil
	s.mu.Unlock()
}
