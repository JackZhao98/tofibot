//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run in the isolated guest image. This exercises real X11 selections and the
// terminal PTY; fake xdotool processes cannot prove IME/paste or key cleanup.
func TestInputRealX11UnicodePasteAndLeaseCleanup(t *testing.T) {
	for _, tool := range []string{"Xvfb", "xdotool", "xprop", "xwininfo", "xdpyinfo", "xterm", "xclip", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("guest image required: " + tool)
		}
	}
	s := newTestService(t)
	display := fmt.Sprintf(":%d", 300+os.Getpid()%10000)
	log := &limitedBuffer{limit: 64 * 1024}
	xvfb, err := startDesktopProcess("Xvfb", []string{display, "-screen", "0", "1280x800x24", "-nolisten", "tcp", "-noreset"}, append(os.Environ(), "DISPLAY="+display), log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(xvfb)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := waitForDisplay(ctx, display, s.root, log); err != nil {
		t.Fatal(err)
	}
	d := &desktop{botID: testBot, display: display, ready: make(chan struct{}), readyClosed: true, lastActivity: time.Now()}
	close(d.ready)
	s.mu.Lock()
	s.desktops[testBot] = d
	s.mu.Unlock()
	output := filepath.Join(t.TempDir(), "typed.txt")
	xterm, err := startDesktopProcess("xterm", []string{"-display", display, "-xrm", "XTerm*VT100.translations: #override Ctrl Shift <Key>V: insert-selection(CLIPBOARD)\n Ctrl Shift <Key>C: copy-selection(CLIPBOARD)", "-e", "/bin/bash", "--noprofile", "--norc", "-c", `IFS= read -r first; IFS= read -r second; printf '%s\n%s' "$first" "$second" > "$1"; sleep 30`, "input-test", output}, append(os.Environ(), "DISPLAY="+display, "LANG=C.UTF-8"), log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(xterm)
	var window string
	for until := time.Now().Add(3 * time.Second); time.Now().Before(until); {
		cmd := exec.CommandContext(ctx, "xdotool", "search", "--onlyvisible", "--class", "XTerm")
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		if out, e := cmd.Output(); e == nil && len(strings.Fields(string(out))) > 0 {
			window = strings.Fields(string(out))[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if window == "" {
		t.Fatalf("xterm missing: %s", log.String())
	}
	if _, err := runXTool(ctx, d, "windowfocus", window); err != nil {
		t.Fatal(err)
	}
	call := func(action string, args any) (any, error) {
		raw, _ := json.Marshal(args)
		return s.action(ctx, ActionRequest{BotID: testBot, RunID: "human-control:real-test", Source: "human", Action: action, Args: raw})
	}
	if _, err := call("desktop.hold", map[string]any{"acquire": true}); err != nil {
		t.Fatal(err)
	}
	events := []DesktopInputEvent{{Type: "text", Text: "English "}, {Type: "text", Text: "中文"}, {Type: "text", Text: "🙂"}, {Type: "keydown", Key: "Return"}, {Type: "keyup", Key: "Return"}, {Type: "text", Text: "第二行"}, {Type: "text", Text: " once"}, {Type: "keydown", Key: "Return"}, {Type: "keyup", Key: "Return"}}
	if _, err := call("desktop.input", map[string]any{"seq": 1, "events": events}); err != nil {
		t.Fatalf("paste: %v", err)
	}
	var text []byte
	for until := time.Now().Add(time.Second); time.Now().Before(until); {
		text, _ = os.ReadFile(output)
		if len(text) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if string(text) != "English 中文🙂\n第二行 once" {
		t.Fatalf("paste changed/lost/repeated commits: %q", text)
	}
	// A plain terminal Ctrl+C must stay an interrupt. No selection update is
	// normal and clipboard.read must not return our previous pasted clipboard.
	if _, err := call("desktop.input", map[string]any{"seq": 2, "events": []DesktopInputEvent{{Type: "keydown", Key: "Control_L"}, {Type: "keydown", Key: "c"}, {Type: "keyup", Key: "c"}, {Type: "keyup", Key: "Control_L"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := call("desktop.clipboard.read", struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if m := result.(map[string]any); m["changed"] != false || m["text"] != "" {
		t.Fatalf("copy without selection returned stale clipboard: %#v", m)
	}
	// Typing on the desktop background has no receiver; report it without
	// dropping control or replaying later events in this batch.
	rootCmd := exec.CommandContext(ctx, "xwininfo", "-root")
	rootCmd.Env = append(os.Environ(), "DISPLAY="+display)
	rootInfo, err := rootCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var rootWindow string
	for _, field := range strings.Fields(string(rootInfo)) {
		if strings.HasPrefix(field, "0x") {
			rootWindow = field
			break
		}
	}
	if rootWindow == "" {
		t.Fatal("missing X11 root window")
	}
	if _, err := runXTool(ctx, d, "windowfocus", rootWindow); err != nil {
		t.Fatal(err)
	}
	result, err = call("desktop.input", map[string]any{"seq": 3, "events": []DesktopInputEvent{{Type: "text", Text: "not accepted"}, {Type: "keydown", Key: "Control_L"}}})
	if err != nil {
		t.Fatal(err)
	}
	if m := result.(map[string]any); m["warning"] != "text_not_accepted" || m["seq"] != uint64(3) {
		t.Fatalf("unexpected no-receiver result: %#v", m)
	}
	s.mu.Lock()
	active := s.inputs[testBot]
	s.mu.Unlock()
	if active == nil || len(active.keys) != 0 || len(active.buttons) != 0 {
		t.Fatal("no-receiver text lost control or left held input")
	}
	if active.copyPending {
		t.Fatal("copy observation remained pending after read")
	}
	if _, err := call("desktop.input", map[string]any{"seq": 4, "events": []DesktopInputEvent{{Type: "keydown", Key: "Control_L"}, {Type: "down", Button: 1}}}); err != nil {
		t.Fatal(err)
	}
	assertMask := func(wantDown bool) {
		t.Helper()
		script := `import ctypes as c,ctypes.util,sys
x=c.CDLL(ctypes.util.find_library('X11')); x.XOpenDisplay.restype=c.c_void_p; d=x.XOpenDisplay(None)
x.XDefaultRootWindow.argtypes=[c.c_void_p];x.XDefaultRootWindow.restype=c.c_ulong
x.XQueryPointer.argtypes=[c.c_void_p,c.c_ulong,c.POINTER(c.c_ulong),c.POINTER(c.c_ulong),c.POINTER(c.c_int),c.POINTER(c.c_int),c.POINTER(c.c_int),c.POINTER(c.c_int),c.POINTER(c.c_uint)]
r=c.c_ulong();child=c.c_ulong();a=c.c_int();b=c.c_int();cx=c.c_int();cy=c.c_int();mask=c.c_uint()
x.XQueryPointer(d,x.XDefaultRootWindow(d),c.byref(r),c.byref(child),c.byref(a),c.byref(b),c.byref(cx),c.byref(cy),c.byref(mask))
print(mask.value & (4|256))`
		cmd := exec.CommandContext(ctx, "python3", "-c", script)
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("mask: %v %s", e, out)
		}
		want := "0"
		if wantDown {
			want = "260"
		}
		if strings.TrimSpace(string(out)) != want {
			t.Fatalf("physical X11 mask=%s want=%s", out, want)
		}
	}
	assertMask(true)
	s.mu.Lock()
	session := s.inputs[testBot]
	s.mu.Unlock()
	gate := s.inputGate(testBot)
	gate.Lock()
	session.expires = time.Now().Add(-time.Second)
	gate.Unlock()
	s.expireInput(session)
	assertMask(false)
	s.mu.Lock()
	remaining := s.inputs[testBot]
	s.mu.Unlock()
	if remaining != nil {
		t.Fatal("expired input owner remained")
	}
	if _, err := exec.LookPath("google-chrome-stable"); err != nil {
		t.Fatal("real Chrome is required in guest image")
	}
	page := filepath.Join(t.TempDir(), "input.html")
	if err := os.WriteFile(page, []byte(`<title>Tofi Input Acceptance</title><textarea autofocus style="width:90vw;height:80vh"></textarea>`), 0600); err != nil {
		t.Fatal(err)
	}
	chrome, err := startDesktopProcess("google-chrome-stable", []string{"--no-sandbox", "--disable-dev-shm-usage", "--no-first-run", "--no-default-browser-check", "--user-data-dir=" + t.TempDir(), "--app=file://" + page}, append(os.Environ(), "DISPLAY="+display), log)
	if err != nil {
		t.Fatal(err)
	}
	defer stopProcess(chrome)
	window = ""
	for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
		cmd := exec.CommandContext(ctx, "xdotool", "search", "--onlyvisible", "--name", "Tofi Input Acceptance")
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		if out, e := cmd.Output(); e == nil && len(strings.Fields(string(out))) > 0 {
			window = strings.Fields(string(out))[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if window == "" {
		t.Fatalf("Chrome input page unavailable: %s", log.String())
	}
	if _, err := runXTool(ctx, d, "windowfocus", window); err != nil {
		t.Fatal(err)
	}
	if _, err := runXTool(ctx, d, "mousemove", "--window", window, "200", "150", "click", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := call("desktop.hold", map[string]any{"acquire": true}); err != nil {
		t.Fatal(err)
	}
	shortcut := func(key string) []DesktopInputEvent {
		return []DesktopInputEvent{{Type: "keydown", Key: "Control_L"}, {Type: "keydown", Key: key}, {Type: "keyup", Key: key}, {Type: "keyup", Key: "Control_L"}}
	}
	for attempt := 1; attempt <= 2; attempt++ {
		events := []DesktopInputEvent{{Type: "text", Text: "English "}, {Type: "text", Text: "中文"}, {Type: "text", Text: "🙂"}}
		events = append(events, shortcut("a")...)
		events = append(events, shortcut("c")...)
		if result, err := call("desktop.input", map[string]any{"seq": attempt, "events": events}); err != nil {
			t.Fatal(err)
		} else if result.(map[string]any)["warning"] != "" {
			t.Fatalf("Chrome rejected text: %#v", result)
		}
		result, err := call("desktop.clipboard.read", struct{}{})
		if err != nil {
			t.Fatal(err)
		}
		if m := result.(map[string]any); m["changed"] != true || m["text"] != "English 中文🙂" {
			t.Fatalf("Chrome copy/paste attempt %d: %#v", attempt, m)
		}
	}

}
