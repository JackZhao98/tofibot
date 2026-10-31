//go:build linux

package guest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestDesktopEngineThroughTheGuestActionOnTheRealDesktop sends desktop.mcp through the guest's action path on the
// real desktop: Xvfb, fluxbox, the one session bus, the file manager from the dock launcher and a second GTK app
// started on the same bus. It shows both apps in one list_apps, reads each tree, clicks an element of the first and
// sets the value of the second through the relay. It needs the computer image run as uid 1000 with the pinned
// open-computer-use installed (TOFI_OCU_BINARY names it), a GTK editor (TOFI_OCU_EDITOR, default mousepad) and
// TOFI_OCU_E2E=1.
func TestDesktopEngineThroughTheGuestActionOnTheRealDesktop(t *testing.T) {
	if os.Getenv("TOFI_OCU_E2E") != "1" {
		t.Skip("set TOFI_OCU_E2E=1 inside the computer image")
	}
	if bin := os.Getenv("TOFI_OCU_BINARY"); bin != "" {
		ocuBinary = bin
	}
	editor := os.Getenv("TOFI_OCU_EDITOR")
	if editor == "" {
		editor = "mousepad"
	}
	root, err := os.MkdirTemp("", "tofi-ocu-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	s, err := NewWithIdleTimeout(root, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	bot := "11111111-1111-4111-8111-111111111111"
	do := func(action, args string) map[string]any {
		t.Helper()
		begin := time.Now()
		out, err := s.action(ctx, ActionRequest{BotID: bot, RunID: "run-1", Action: action, Args: json.RawMessage(args), Source: ActionSourceModel})
		if err != nil {
			t.Fatalf("%s %s: %v", action, args, err)
		}
		t.Logf("%s took %s", action, time.Since(begin).Round(time.Millisecond))
		m, _ := out.(map[string]any)
		return m
	}
	tool := func(name, args string) string {
		t.Helper()
		res := do("desktop.mcp", `{"tool":"`+name+`","arguments":`+args+`}`)
		text, _ := res["text"].(string)
		t.Logf("%s %s -> is_error=%v\n%s", name, args, res["is_error"], text)
		if res["is_error"] == true {
			t.Fatalf("%s failed: %s", name, text)
		}
		return text
	}

	do("desktop.start", `{}`)
	s.mu.Lock()
	d := s.sharedDesktop
	s.mu.Unlock()
	if d == nil {
		t.Fatal("no desktop")
	}
	// The dock launcher starts the file manager on the desktop's bus.
	launcher := d.botDir + "/.fluxbox/launch-files.sh"
	files := exec.Command(launcher)
	if err := files.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = files.Process.Kill(); _ = files.Wait() }()
	// A second app, started the way the terminal does: the desktop's bus and display.
	app := exec.Command(editor)
	app.Env = withDesktopA11yEnv(append(cleanEnv(root, d.botDir), "DISPLAY="+d.display), d.runtimeDir)
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Process.Kill(); _ = app.Wait() }()

	var apps string
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Second) {
		apps = tool("list_apps", `{}`)
		if strings.Contains(apps, "pcmanfm") && strings.Contains(strings.ToLower(apps), editor) {
			break
		}
	}
	if !strings.Contains(apps, "pcmanfm") || !strings.Contains(strings.ToLower(apps), editor) {
		t.Fatalf("both apps must show in one session: %s", apps)
	}
	filesTree := tool("get_app_state", `{"app":"pcmanfm"}`)
	text := tool("get_app_state", `{"app":"`+editor+`"}`)
	index := func(tree string, re string) string {
		m := regexp.MustCompile(re).FindStringSubmatch(tree)
		if m == nil {
			t.Fatalf("no element matches %s in:\n%s", re, tree)
		}
		return m[1]
	}
	// The classic screenshot tool works on the same desktop.
	if shot := do("desktop.capture", `{}`); shot == nil {
		t.Fatal("desktop.capture returned nothing")
	}
	// Click in the first app, then set the value of the second: element indexes of each app stay valid.
	fileMenu := index(filesTree, `(?m)^\s*(\d+)\s+menu File\b`)
	tool("click", `{"app":"pcmanfm","element_index":"`+fileMenu+`"}`)
	edit := index(text, `(?m)^\s*(\d+)\s+(?:text|editor|document text|text entry)\b`)
	tool("set_value", `{"app":"`+editor+`","element_index":"`+edit+`","value":"hello from the desktop engine"}`)
	again := tool("get_app_state", `{"app":"`+editor+`"}`)
	if !strings.Contains(again, "hello from the desktop engine") {
		t.Fatalf("the value is not in the editor's tree:\n%s", again)
	}
	// A stale index is refused by the engine, not acted on.
	res := do("desktop.mcp", `{"tool":"click","arguments":{"app":"pcmanfm","element_index":"99999"}}`)
	if res["is_error"] != true || !strings.Contains(res["text"].(string), "unknown element_index") {
		t.Fatalf("a stale index must be an engine error: %v", res)
	}
	// Stopping the desktop ends the engine process.
	do("desktop.stop", `{}`)
	s.ocu.mu.Lock()
	n := len(s.ocu.sessions)
	s.ocu.mu.Unlock()
	if n != 0 {
		t.Fatalf("the engine outlived the desktop: %d", n)
	}
}
