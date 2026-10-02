package guest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFilesWriteExpectedSHA256ConflictAndAtomicResult(t *testing.T) {
	s := newTestService(t)
	initial := "base-content"
	if _, err := s.writeFile(context.Background(), testBot, fileArgs{Path: "shared.txt", Content: initial}); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(initial))
	expected := hex.EncodeToString(h[:])
	var wg sync.WaitGroup
	type result struct {
		value map[string]any
		err   error
	}
	results := make(chan result, 2)
	for _, value := range []string{"writer-a", "writer-b"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			got, err := s.writeFile(context.Background(), testBot, fileArgs{Path: "shared.txt", Content: value, ExpectedSHA256: expected})
			results <- result{value: got, err: err}
		}(value)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for got := range results {
		if got.err == nil {
			successes++
			if _, ok := got.value["sha256"].(string); !ok {
				t.Fatalf("successful write omitted sha256: %#v", got.value)
			}
		} else if errors.Is(got.err, errFileConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent write error: %v", got.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent writes successes=%d conflicts=%d", successes, conflicts)
	}
	status, response := postAction(t, s.Handler(), testBot, "run-http", "files.write", map[string]any{
		"path": "shared.txt", "content": "http-conflict", "expected_sha256": expected,
	})
	if status != http.StatusConflict || response.OK {
		t.Fatalf("HTTP conflict status=%d response=%#v", status, response)
	}
	read, err := s.readFile(context.Background(), testBot, fileArgs{Path: "shared.txt"})
	if err != nil {
		t.Fatal(err)
	}
	content := read["content"].(string)
	if content != "writer-a" && content != "writer-b" {
		t.Fatalf("file was partially written: %q", content)
	}
}

func TestFilesWriteCancellationCleansTemporaryFile(t *testing.T) {
	s := newTestService(t)
	path, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.writeFile(ctx, testBot, fileArgs{Path: "cancelled.txt", Content: "should not land"}); err == nil {
		t.Fatal("cancelled write succeeded")
	}
	matches, err := filepath.Glob(filepath.Join(path, ".tofi-write-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("cancelled write left temporary files: %v", matches)
	}
	if _, err := os.Stat(filepath.Join(path, "cancelled.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancelled write created destination: %v", err)
	}
}

func TestFilesWriteThroughInWorkspaceSymlinkPreservesLink(t *testing.T) {
	s := newTestService(t)
	bot, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(bot, "target.txt")
	link := filepath.Join(bot, "link.txt")
	if err := os.WriteFile(target, []byte("old"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	result, err := s.writeFile(context.Background(), testBot, fileArgs{Path: "link.txt", Content: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if result["sha256"] == nil {
		t.Fatalf("missing hash: %#v", result)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("write replaced symlink: info=%v err=%v", info, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("symlink target=%q err=%v", got, err)
	}
	if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("target permissions changed: info=%v err=%v", info, err)
	}
}

func TestExportFileChunkIsBinaryBoundedAndVersioned(t *testing.T) {
	s := newTestService(t)
	p, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0, 1, 2, 0xff, 'x'}
	if err = os.WriteFile(filepath.Join(p, "image.bin"), want, 0600); err != nil {
		t.Fatal(err)
	}
	_, out := postAction(t, s.Handler(), testBot, "run-1", "files.export_chunk", map[string]any{"path": "image.bin", "limit": 3})
	if !out.OK {
		t.Fatalf("chunk=%#v", out)
	}
	m := mustMap(t, out.Result)
	got, _ := base64.StdEncoding.DecodeString(m["data_base64"].(string))
	if string(got) != string(want[:3]) || m["eof"] != false {
		t.Fatalf("chunk=%#v", m)
	}
	version := m["version"].(string)
	if err = os.WriteFile(filepath.Join(p, "image.bin"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	status, out := postAction(t, s.Handler(), testBot, "run-1", "files.export_chunk", map[string]any{"path": "image.bin", "offset": 3, "version": version})
	if status == http.StatusOK || out.OK {
		t.Fatalf("changed file accepted: %d %#v", status, out)
	}
}

func TestExportFileChunkRejectsFIFOAndEscapingSymlink(t *testing.T) {
	s := newTestService(t)
	bot, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(bot, "pipe")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	status, out := postAction(t, s.Handler(), testBot, "run-1", "files.export_chunk", map[string]any{"path": "pipe"})
	if time.Since(started) > time.Second {
		t.Fatal("FIFO open blocked")
	}
	if status == http.StatusOK || out.OK {
		t.Fatalf("FIFO accepted: %d %#v", status, out)
	}
	outside := filepath.Join(filepath.Dir(s.root), "export-secret")
	if err = os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(bot, "escape")); err != nil {
		t.Fatal(err)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "files.export_chunk", map[string]any{"path": "escape"})
	if status == http.StatusOK || out.OK {
		t.Fatalf("escaping symlink accepted: %d %#v", status, out)
	}
}

const testBot = "11111111-1111-4111-8111-111111111111"

const testBotB = "22222222-2222-4222-8222-222222222222"

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func postAction(t *testing.T, h http.Handler, bot, run, action string, args any) (int, ActionResponse) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"bot_id": bot, "run_id": run, "action": action, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/action", strings.NewReader(string(b)))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	var out ActionResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("status=%d body=%s: %v", res.Code, res.Body.String(), err)
	}
	return res.Code, out
}

func TestGuestHTTPHealthAndActionEnvelope(t *testing.T) {
	s := newTestService(t)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))
	// Unit tests also run on development hosts without the guest image's
	// Google Chrome package. Health must report that dependency accurately;
	// the acceptance guest is expected to return 200.
	if (res.Code != http.StatusOK && res.Code != http.StatusServiceUnavailable) || !strings.Contains(res.Body.String(), "tofi-guest") {
		t.Fatalf("health=%d %s", res.Code, res.Body.String())
	}
	status, out := postAction(t, s.Handler(), testBot, "run-1", "files.write", map[string]any{"path": "notes/one.txt", "content": "hello"})
	if status != http.StatusOK || !out.OK {
		t.Fatalf("write=%d %#v", status, out)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "files.read", map[string]any{"path": "notes/one.txt"})
	if status != http.StatusOK || !out.OK || !strings.Contains(string(mustJSON(t, out.Result)), "hello") {
		t.Fatalf("read=%d %#v", status, out)
	}
	status, out = postAction(t, s.Handler(), "bad", "run-1", "files.list", map[string]any{})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("invalid bot=%d %#v", status, out)
	}
}

func TestGuestWorkspaceIsPersistentAndRootBound(t *testing.T) {
	s := newTestService(t)
	root := s.root
	if _, out := postAction(t, s.Handler(), testBot, "run-1", "files.write", map[string]any{"path": "persist.txt", "content": "one"}); !out.OK {
		t.Fatalf("write=%#v", out)
	}
	status, out := postAction(t, s.Handler(), testBot, "run-1", "files.read", map[string]any{"path": "/workspace/../etc/passwd"})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("absolute escape accepted: %d %#v", status, out)
	}
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "bots", testBot, "link")); err != nil {
		t.Fatal(err)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "files.write", map[string]any{"path": "link", "content": "nope"})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("symlink escape accepted: %d %#v", status, out)
	}
}

func TestGuestShellUsesCleanBotWorkspaceAndKillsTimeoutGroup(t *testing.T) {
	s := newTestService(t)
	t.Setenv("SECRET_FROM_PARENT", "must-not-leak")
	status, out := postAction(t, s.Handler(), testBot, "run-1", "shell.exec", map[string]any{"command": "printf '%s|%s|%s' \"$HOME\" \"$PWD\" \"$SECRET_FROM_PARENT\"; test -z \"$SECRET_FROM_PARENT\""})
	if status != http.StatusOK || !out.OK {
		t.Fatalf("shell=%d %#v", status, out)
	}
	shell := mustMap(t, out.Result)
	stdout, _ := shell["stdout"].(string)
	parts := strings.Split(stdout, "|")
	if len(parts) != 3 || parts[0] != filepath.Join(s.root, "home") || parts[2] != "" {
		t.Fatalf("clean shell environment=%q", stdout)
	}
	resolved, err := filepath.EvalSymlinks(parts[1])
	if err != nil || resolved != filepath.Join(s.root, "bots", testBot) {
		t.Fatalf("shell workspace=%q resolves to %q, err=%v", parts[1], resolved, err)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "shell.exec", map[string]any{"command": "sleep 30", "timeout_sec": 1})
	if status != http.StatusOK || !out.OK {
		t.Fatalf("timeout=%d %#v", status, out)
	}
	result := mustMap(t, out.Result)
	if result["timed_out"] != true || result["exit_code"] != float64(137) {
		t.Fatalf("timeout result=%#v", result)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "shell.exec", map[string]any{"command": "true", "env": map[string]string{"BAD": "ignored"}})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("silent env field accepted: %d %#v", status, out)
	}
}

func TestGuestShellUsesPersistentSharedToolPathWithoutLeakingDotEnv(t *testing.T) {
	s := newTestService(t)
	botA, err := s.botDir(testBot)
	if err != nil {
		t.Fatal(err)
	}
	botB, err := s.botDir(testBotB)
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(s.root, "shared", "bin", "tofi-shared-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf shared-tool"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(botA, ".env"), []byte("TOFI_A_SECRET=from-dotenv\n"), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := s.shell(context.Background(), testBot, shellArgs{Command: "tofi-shared-tool; printf '|PWD=%s|NPM=%s|GO=%s|CARGO=%s' \"$PWD\" \"$NPM_CONFIG_PREFIX\" \"$GOBIN\" \"$CARGO_INSTALL_ROOT\""})
	if err != nil {
		t.Fatal(err)
	}
	stdout := result["stdout"].(string)
	if !strings.Contains(stdout, "shared-tool") || !strings.Contains(stdout, "PWD="+botA) ||
		!strings.Contains(stdout, "NPM="+filepath.Join(s.root, "home", ".local")) ||
		!strings.Contains(stdout, "GO="+filepath.Join(s.root, "home", ".local", "bin")) ||
		!strings.Contains(stdout, "CARGO="+filepath.Join(s.root, "home", ".local")) {
		t.Fatalf("Bot A shared tool/cwd=%q", stdout)
	}
	result, err = s.shell(context.Background(), testBotB, shellArgs{Command: "tofi-shared-tool; printf '|PWD=%s|NPM=%s|GO=%s|CARGO=%s' \"$PWD\" \"$NPM_CONFIG_PREFIX\" \"$GOBIN\" \"$CARGO_INSTALL_ROOT\""})
	if err != nil {
		t.Fatal(err)
	}
	stdout = result["stdout"].(string)
	if !strings.Contains(stdout, "shared-tool") || !strings.Contains(stdout, "PWD="+botB) || strings.Contains(stdout, botA) ||
		!strings.Contains(stdout, "NPM="+filepath.Join(s.root, "home", ".local")) ||
		!strings.Contains(stdout, "GO="+filepath.Join(s.root, "home", ".local", "bin")) ||
		!strings.Contains(stdout, "CARGO="+filepath.Join(s.root, "home", ".local")) {
		t.Fatalf("Bot B shared tool/cwd=%q", stdout)
	}

	result, err = s.shell(context.Background(), testBot, shellArgs{Command: ". \"$PWD/.env\"; printf '%s' \"$TOFI_A_SECRET\""})
	if err != nil || result["stdout"] != "from-dotenv" {
		t.Fatalf("explicit Bot A dotenv source=%#v err=%v", result, err)
	}
	result, err = s.shell(context.Background(), testBotB, shellArgs{Command: "test -z \"${TOFI_A_SECRET:-}\""})
	if err != nil || result["exit_code"] != 0 {
		t.Fatalf("dotenv leaked to Bot B=%#v err=%v", result, err)
	}
	result, err = s.shell(context.Background(), testBotB, shellArgs{Command: "cat \"" + filepath.Join(botA, ".env") + "\""})
	if err != nil || result["stdout"] != "TOFI_A_SECRET=from-dotenv\n" {
		t.Fatalf("Bot B explicit A.env read=%#v err=%v", result, err)
	}

	if err := os.Remove(tool); err != nil {
		t.Fatal(err)
	}
	result, err = s.shell(context.Background(), testBotB, shellArgs{Command: "command -v tofi-shared-tool"})
	if err != nil {
		t.Fatal(err)
	}
	if result["exit_code"] != 1 {
		t.Fatalf("removed shared tool still found=%#v", result)
	}
}

func TestGuestCancellationStopsShell(t *testing.T) {
	s := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.shell(ctx, testBot, shellArgs{Command: "sleep 30 & wait", TimeoutSec: MaxTimeout})
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shell cancellation did not return")
	}
}

func TestClickCoordinatesMapFromNaturalScreenshotToDisplay(t *testing.T) {
	if x, y := scaleClickCoordinates(448, 280, 896, 560); x != 640 || y != 400 {
		t.Fatalf("scaled click=(%d,%d), want (640,400)", x, y)
	}
	if x, y := scaleClickCoordinates(640, 400, 1280, 800); x != 640 || y != 400 {
		t.Fatalf("native click=(%d,%d), want (640,400)", x, y)
	}
}

func TestDesktopClickScreenshotDimensionsArePairedAndBounded(t *testing.T) {
	s := newTestService(t)
	status, out := postAction(t, s.Handler(), testBot, "run-1", "desktop.click", map[string]any{"x": 1, "y": 1, "screenshot_width": 896})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("unpaired screenshot dimensions accepted: %d %#v", status, out)
	}
	status, out = postAction(t, s.Handler(), testBot, "run-1", "desktop.click", map[string]any{"x": 1, "y": 1, "screenshot_width": 0, "screenshot_height": 560})
	if status != http.StatusBadRequest || out.OK {
		t.Fatalf("invalid screenshot dimensions accepted: %d %#v", status, out)
	}
}

func TestDesktopClickCoordinatesSurviveLifecyclePreflight(t *testing.T) {
	s := newTestService(t)
	for _, args := range []map[string]any{
		{"x": 528, "y": 352, "screenshot_width": 1280, "screenshot_height": 800},
		{"x": 528, "y": 352},
	} {
		status, out := postAction(t, s.Handler(), testBot, "run-click", "desktop.click", args)
		if status == http.StatusBadRequest && strings.Contains(out.Error, "unknown field") {
			t.Fatalf("desktop.click coordinates rejected by lifecycle preflight: args=%#v error=%q", args, out.Error)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("result type=%T value=%#v", v, v)
	}
	return m
}
