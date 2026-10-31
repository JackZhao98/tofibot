//go:build linux

package guest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeOCU is a stdio MCP server in the shape of `open-computer-use mcp`: it
// answers every tools/call with its own pid, the bus address and display it was
// started with, the tool and the arguments, and interleaves a notification so
// a reply must be matched by id.
const fakeOCU = `#!/usr/bin/env python3
import json, os, sys
for line in sys.stdin:
    m = json.loads(line)
    if "id" not in m:
        continue
    if m["method"] == "initialize":
        print(json.dumps({"jsonrpc": "2.0", "id": m["id"], "result": {"protocolVersion": "2025-03-26", "capabilities": {"tools": {}}, "serverInfo": {"name": "fake", "version": "1"}}}), flush=True)
        continue
    if m["method"] == "tools/call":
        p = m["params"]
        print(json.dumps({"jsonrpc": "2.0", "method": "notifications/message", "params": {"x": 1}}), flush=True)
        if p["name"] == "list_apps":
            text = "pid=%d bus=%s display=%s gtk=%s" % (os.getpid(), os.environ.get("DBUS_SESSION_BUS_ADDRESS"), os.environ.get("DISPLAY"), os.environ.get("GTK_A11Y"))
        elif p["name"] == "crash":
            sys.exit(3)
        else:
            text = "%s %s" % (p["name"], json.dumps(p["arguments"], sort_keys=True))
        print(json.dumps({"jsonrpc": "2.0", "id": m["id"], "result": {"content": [{"type": "text", "text": text}, {"type": "image", "data": "AA", "mimeType": "image/png"}], "isError": p["name"] == "click" and p["arguments"].get("element_index") == "99"}}), flush=True)
        continue
    print(json.dumps({"jsonrpc": "2.0", "id": m["id"], "error": {"message": "no " + m["method"]}}), flush=True)
`

func fakeOCUBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	path := filepath.Join(t.TempDir(), "open-computer-use")
	if err := os.WriteFile(path, []byte(fakeOCU), 0o755); err != nil {
		t.Fatal(err)
	}
	old := ocuBinary
	ocuBinary = path
	t.Cleanup(func() { ocuBinary = old })
}

func ocuTestService(t *testing.T) (*Service, *desktop) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	d := &desktop{botDir: dir, display: ":77", runtimeDir: filepath.Join(root, "run"), readyClosed: true}
	s := &Service{root: root, desktops: map[string]*desktop{}, sharedDesktop: d}
	return s, d
}

const (
	ocuBotA = "11111111-1111-1111-1111-111111111111"
	ocuBotB = "22222222-2222-2222-2222-222222222222"
)

func TestDesktopEngineRunsOnTheDesktopBusAndKeepsOneProcessPerBot(t *testing.T) {
	fakeOCUBinary(t)
	s, d := ocuTestService(t)
	defer s.stopOCU()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := s.desktopMCP(ctx, ocuBotA, ocuCall("list_apps", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	text := first.(map[string]any)["text"].(string)
	for _, want := range []string{"bus=unix:path=" + d.runtimeDir + "/bus", "display=:77", "gtk=atspi"} {
		if !strings.Contains(text, want) {
			t.Fatalf("engine environment lacks %q: %s", want, text)
		}
	}
	if first.(map[string]any)["images_omitted"] != 1 {
		t.Fatalf("images must be left out: %v", first)
	}
	again, _ := s.desktopMCP(ctx, ocuBotA, ocuCall("list_apps", `{}`))
	if again.(map[string]any)["text"] != text {
		t.Fatal("one Bot must keep one engine process between calls")
	}
	other, _ := s.desktopMCP(ctx, ocuBotB, ocuCall("list_apps", `{}`))
	if other.(map[string]any)["text"] == text {
		t.Fatal("two Bots shared one engine process and its element indexes")
	}
	// A tool error stays a result; the notifications between never displace a reply.
	res, err := s.desktopMCP(ctx, ocuBotA, ocuCall("click", `{"app":"pcmanfm","element_index":"99"}`))
	if err != nil || res.(map[string]any)["is_error"] != true {
		t.Fatalf("a tool error must come back as a result: %v %v", res, err)
	}
	// An engine that dies mid-call is an error and the next call starts a new one.
	if _, err := s.desktopMCP(ctx, ocuBotA, ocuCall("get_app_state", `{"app":"crash"}`)); err != nil {
		t.Fatal(err)
	}
	s.ocu.mu.Lock()
	sess := s.ocu.sessions[ocuBotA]
	s.ocu.mu.Unlock()
	if _, err := sess.call(ctx, "tools/call", map[string]any{"name": "crash", "arguments": map[string]any{}}); err == nil {
		t.Fatal("a dead engine answered")
	}
	s.dropOCU(sess)
	third, err := s.desktopMCP(ctx, ocuBotA, ocuCall("list_apps", `{}`))
	if err != nil || third.(map[string]any)["text"] == text {
		t.Fatalf("no new engine process after a crash: %v", err)
	}
}

func TestDesktopEngineStopsWhenIdleAndOnDesktopStop(t *testing.T) {
	fakeOCUBinary(t)
	old := ocuIdle
	ocuIdle = 400 * time.Millisecond
	defer func() { ocuIdle = old }()
	s, _ := ocuTestService(t)
	defer s.stopOCU()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.desktopMCP(ctx, ocuBotA, ocuCall("list_apps", `{}`)); err != nil {
		t.Fatal(err)
	}
	s.ocu.mu.Lock()
	a := s.ocu.sessions[ocuBotA]
	s.ocu.mu.Unlock()
	select {
	case <-a.dead:
	case <-time.After(10 * time.Second):
		t.Fatal("an idle engine was never stopped")
	}
	s.ocu.mu.Lock()
	n := len(s.ocu.sessions)
	s.ocu.mu.Unlock()
	if n != 0 {
		t.Fatalf("a stopped engine stays listed: %d", n)
	}
	ocuIdle = time.Hour
	if _, err := s.desktopMCP(ctx, ocuBotA, ocuCall("list_apps", `{}`)); err != nil {
		t.Fatal(err)
	}
	s.ocu.mu.Lock()
	b := s.ocu.sessions[ocuBotA]
	s.ocu.mu.Unlock()
	s.stopOCU()
	select {
	case <-b.dead:
	case <-time.After(10 * time.Second):
		t.Fatal("a desktop stop left the engine running")
	}
}

func TestDesktopEngineMissingBinaryIsNamed(t *testing.T) {
	old := ocuBinary
	ocuBinary = filepath.Join(t.TempDir(), "absent")
	defer func() { ocuBinary = old }()
	s, _ := ocuTestService(t)
	_, err := s.desktopMCP(context.Background(), ocuBotA, ocuCall("list_apps", `{}`))
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("want a clear missing-engine error, got %v", err)
	}
}

func TestDesktopSessionBusEnvironment(t *testing.T) {
	env := withDesktopA11yEnv([]string{"HOME=/h", "GTK_A11Y=none"}, "/run/x")
	joined := strings.Join(env, "\n")
	for _, want := range []string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/x/bus", "GTK_A11Y=atspi", "XDG_RUNTIME_DIR=/run/x", "NO_AT_BRIDGE=0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %v", want, env)
		}
	}
	if strings.Contains(joined, "GTK_A11Y=none") {
		t.Fatal("an earlier GTK_A11Y value was kept")
	}
}

func TestDesktopSessionBusAcceptsConnections(t *testing.T) {
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon is not installed")
	}
	dir, err := os.MkdirTemp("/tmp", "tofi-bus-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	log := &limitedBuffer{limit: 4096}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, err := startSessionBus(ctx, dir, os.Environ(), log)
	if err != nil {
		t.Fatalf("%v %s", err, log.String())
	}
	if err := stopProcess(cmd); err != nil {
		t.Fatalf("the bus did not stop cleanly: %v", err)
	}
}
