package app

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProc writes a /proc-like table: each process is an exe link and a raw cmdline.
func fakeProc(t *testing.T, procs map[string][2]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p[0], filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(p[1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// findSharedChrome runs the helper's discovery (browserFindChrome) with python3 against a fake process table.
func findSharedChrome(t *testing.T, proc string, env ...string) any {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	opts, _ := json.Marshal(map[string]string{"proc": proc})
	script := "import base64, json, sys\nopts = json.loads(base64.b64decode(sys.argv[1]))\n" + browserFindChrome + "\nprint(json.dumps({\"port\": shared_chrome_port()}))\n"
	cmd := exec.Command(python, "-c", script, base64.StdEncoding.EncodeToString(opts))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH")}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper failed: %v\n%s", err, out)
	}
	var got struct{ Port any }
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("helper output %q", out)
	}
	return got.Port
}

const sharedChromeArgs = "--user-data-dir=/workspace/browser/shared --display=:1 --remote-debugging-address=127.0.0.1 --remote-debugging-port=40111 --no-first-run"

// browser.read finds the same Chrome that desktop.* and browser.navigate use. Chrome rewrites its process title on
// Linux, so its cmdline can be one space-joined string; reading only NUL-separated arguments answered
// chrome_not_running for every browser.read on the test machine from 2026-10-31 on.
func TestBrowserHelperFindsTheSharedChrome(t *testing.T) {
	nul := "/opt/google/chrome/chrome\x00" + strings.ReplaceAll(sharedChromeArgs, " ", "\x00") + "\x00"
	title := "/opt/google/chrome/chrome " + sharedChromeArgs + "\x00\x00\x00"
	for name, cmdline := range map[string]string{"nul-separated": nul, "rewritten title": title} {
		proc := fakeProc(t, map[string][2]string{
			"100": {"/opt/google/chrome/chrome", cmdline},
			// A shell that names the same flags is not Chrome.
			"200": {"/usr/bin/bash", "bash\x00-c\x00echo " + strings.ReplaceAll(sharedChromeArgs, "40111", "1") + "\x00"},
			// A headless browser a Bot started has another profile.
			"300": {"/usr/lib/chromium/chromium", "chromium --headless --user-data-dir=/tmp/x --remote-debugging-port=2\x00"},
		})
		if got := findSharedChrome(t, proc); got != float64(40111) {
			t.Fatalf("%s: port %v", name, got)
		}
	}
	// Debian's chromium on arm64, deleted after an upgrade while it runs.
	proc := fakeProc(t, map[string][2]string{"100": {"/usr/lib/chromium/chromium (deleted)", "chromium " + sharedChromeArgs + "\x00"}})
	if got := findSharedChrome(t, proc); got != float64(40111) {
		t.Fatalf("chromium: port %v", got)
	}
	// Only a shell naming the flags: nothing.
	proc = fakeProc(t, map[string][2]string{"200": {"/usr/bin/bash", "bash " + sharedChromeArgs + "\x00"}})
	if got := findSharedChrome(t, proc); got != nil {
		t.Fatalf("a shell was taken for Chrome: %v", got)
	}
	// The guest names the port: that one wins over the table.
	proc = fakeProc(t, map[string][2]string{"100": {"/opt/google/chrome/chrome", nul}})
	if got := findSharedChrome(t, proc, "TOFI_DEVTOOLS_PORT=40999"); got != float64(40999) {
		t.Fatalf("named port: %v", got)
	}
}
