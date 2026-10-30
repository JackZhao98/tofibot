//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const e2ePage = `<!doctype html><title>Order form</title>
<h1>Order</h1>
<label>Full name <input id=n name=full_name></label>
<label>Plan <select id=p><option>Basic</option><option>Pro</option></select></label>
<label>Attachment <input type=file id=f></label>
<button id=go onclick="document.getElementById('out').textContent='sent '+document.getElementById('n').value+' '+(document.getElementById('f').files[0]||{name:'nofile'}).name">Send order</button>
<p id=out></p>
<table><tr><th>Item</th><th>Qty</th></tr><tr><td>Bolt</td><td>12</td></tr><tr><td>Nut</td><td>30</td></tr></table>`

// TestPlaywrightRealChrome drives the real pinned @playwright/mcp against a real
// Chrome over DevTools through the guest relay. It needs the image's packages,
// so it runs only when TOFI_PLAYWRIGHT_E2E=1 (inside the computer image).
func TestPlaywrightRealChrome(t *testing.T) {
	if os.Getenv("TOFI_PLAYWRIGHT_E2E") != "1" {
		t.Skip("set TOFI_PLAYWRIGHT_E2E=1 inside the computer image")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, e2ePage)
	}))
	defer srv.Close()

	root := t.TempDir()
	bot := filepath.Join(root, "bots", "b")
	if err := os.MkdirAll(bot, 0o755); err != nil {
		t.Fatal(err)
	}
	upload := filepath.Join(bot, "report.txt")
	if err := os.WriteFile(upload, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := os.MkdirTemp("", "tofi-e2e-profile-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { time.Sleep(500 * time.Millisecond); _ = os.RemoveAll(profile) }()
	bin := browserBinary()
	chrome := exec.Command(bin, "--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run",
		"--remote-debugging-address=127.0.0.1", fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profile, "about:blank")
	if err := chrome.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = chrome.Process.Kill(); _ = chrome.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Chrome did not open its DevTools port: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	s := &Service{root: root}
	defer s.stopPlaywright()
	started := time.Now()
	sess, err := s.playwrightFor(ctx, bot, port)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("server start: %s", time.Since(started).Round(time.Millisecond))

	run := func(tool, args string) string {
		t.Helper()
		in := call(tool, args)
		checked, err := checkPlaywrightCall(root, in)
		if err != nil {
			t.Fatalf("%s refused: %v", tool, err)
		}
		begin := time.Now()
		raw, err := sess.call(ctx, "tools/call", map[string]any{"name": tool, "arguments": checked})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		res, err := playwrightResult(root, tool, raw)
		if err != nil {
			t.Fatal(err)
		}
		text, _ := res["text"].(string)
		t.Logf("%s took %s, %d bytes", tool, time.Since(begin).Round(time.Millisecond), len(text))
		if res["is_error"] == true {
			t.Fatalf("%s failed: %s", tool, text)
		}
		return text
	}
	refOf := func(snapshot, role string) string {
		m := regexp.MustCompile(`(?m)` + role + `.*\[ref=(e\d+)\]`).FindStringSubmatch(snapshot)
		if m == nil {
			t.Fatalf("no %s ref in snapshot:\n%s", role, snapshot)
		}
		return m[1]
	}

	snap := run("browser_navigate", fmt.Sprintf(`{"url":%q}`, srv.URL))
	if !strings.Contains(snap, "Order form") || !strings.Contains(snap, "[ref=") {
		t.Fatalf("navigate returned no snapshot with refs:\n%s", snap)
	}
	name := refOf(snap, `textbox "Full name"`)
	run("browser_type", fmt.Sprintf(`{"target":%q,"element":"Full name","text":"Ada Lovelace"}`, name))
	run("browser_select_option", fmt.Sprintf(`{"target":%q,"element":"Plan","values":["Pro"]}`, refOf(snap, `combobox "Plan"`)))
	// The table reads as text.
	if !strings.Contains(snap, "Bolt") || !strings.Contains(snap, "30") {
		t.Fatalf("table cells missing from the snapshot:\n%s", snap)
	}
	// File chooser: click the file input's button, then hand over the file.
	chooser := run("browser_click", fmt.Sprintf(`{"target":%q,"element":"Attachment"}`, refOf(snap, `button "Attachment"`)))
	if !strings.Contains(strings.ToLower(chooser), "file chooser") {
		t.Fatalf("no file chooser state after the click:\n%s", chooser)
	}
	up := run("browser_file_upload", fmt.Sprintf(`{"paths":[%q]}`, upload))
	_ = up
	after := run("browser_snapshot", `{}`)
	send := refOf(after, `button "Send order"`)
	done := run("browser_click", fmt.Sprintf(`{"target":%q,"element":"Send order"}`, send))
	if !strings.Contains(done, "sent Ada Lovelace report.txt") {
		t.Fatalf("the form did not send the typed name and the uploaded file:\n%s", done)
	}
	// A stale ref is an error result, not a crash.
	raw, err := sess.call(ctx, "tools/call", map[string]any{"name": "browser_click", "arguments": map[string]any{"target": "e9999", "element": "gone"}})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := playwrightResult(root, "browser_click", raw)
	b, _ := json.Marshal(res)
	t.Logf("stale ref answer: %s", b)
	if res["is_error"] != true {
		t.Fatal("a stale ref must be reported as an error")
	}
}
