package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
)

func TestTerminalControlRequiresLeaseAndOrderedInput(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fake := &inputControlTransport{}
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: fake}})
	bot, err := s.store.CreateBot("terminal", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	call := func(action string, args any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"bot_id": bot.ID, "action": action, "args": args})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "http://tofi.local/api/computers/firecracker/actions", bytes.NewReader(raw)))
		return res
	}
	if res := call("terminal.write", map[string]any{"terminal_id": "test", "data": "bad"}); res.Code != 409 {
		t.Fatal(res.Code, res.Body.String())
	}
	acquire := call("terminal.control.acquire", map[string]any{})
	if acquire.Code != 200 {
		t.Fatal(acquire.Body.String())
	}
	var e struct {
		Result struct {
			ID string `json:"control_id"`
		}
	}
	if err = json.Unmarshal(acquire.Body.Bytes(), &e); err != nil || e.Result.ID == "" {
		t.Fatal(acquire.Body.String())
	}
	id := e.Result.ID
	if s.claimTerminalOwner(bot.ID, "model-run") {
		t.Fatal("model stole terminal control")
	}
	for _, action := range []string{"terminal.list", "terminal.read"} {
		if res := call(action, map[string]any{"terminal_id": "test"}); res.Code != 200 {
			t.Fatal(res.Body.String())
		}
	}
	if !s.claimComputerOwner(bot.ID, "parallel-desktop") {
		t.Fatal("terminal unnecessarily blocked shared desktop")
	}
	s.releaseComputerOwner(bot.ID, "parallel-desktop")
	if res := call("desktop.control.release", map[string]any{"control_id": id}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if s.claimTerminalOwner(bot.ID, "model-run") {
		t.Fatal("wrong control type released terminal")
	}
	if res := call("terminal.open", map[string]any{"control_id": id, "seq": 1}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	fake.mu.Lock()
	last := fake.actions[len(fake.actions)-1]
	fake.mu.Unlock()
	var args map[string]any
	_ = json.Unmarshal(last.Args, &args)
	if args["command"] != s.defaultTerminalCommand(bot.ID) || last.Source != "human" {
		t.Fatal(last)
	}
	if res := call("terminal.write", map[string]any{"control_id": id, "seq": 1, "terminal_id": "test", "data": "replayed"}); res.Code != 409 {
		t.Fatal("replay accepted")
	}
	if res := call("terminal.write", map[string]any{"control_id": id, "seq": 2, "terminal_id": "test", "data": "中文\r"}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	s.computerOwnerMu.Lock()
	c := s.computerControls["terminal:"+bot.ID]
	s.computerOwnerMu.Unlock()
	c.mu.Lock()
	c.expires = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if res := call("terminal.write", map[string]any{"control_id": id, "seq": 3, "terminal_id": "test", "data": "expired"}); res.Code != 409 {
		t.Fatal("expired input accepted")
	}
	if !s.claimTerminalOwner(bot.ID, "model-run") {
		t.Fatal("expiration retained owner")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, a := range fake.actions {
		if a.Name == "desktop.hold" || a.Name == "desktop.release" {
			t.Fatal("terminal control started/touched graphical desktop", a.Name)
		}
	}
}

func TestDefaultTerminalPromptRuntime(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "workspace")
	sub := filepath.Join(workspace, "sub $(touch PATH_INJECTED)")
	for _, dir := range []string{home, sub} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("export RC_LOADED=yes\nPROMPT_COMMAND='export HOOK_LOADED=yes'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	name := "Bot $(touch NAME_INJECTED) `touch OTHER_INJECTED` \\u '"
	cmd := exec.Command("bash", "-c", botTerminalCommand(name))
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "HOME="+home, "COLUMNS=1000", "BASH_SILENCE_DEPRECATION_WARNING=1")
	cmd.Stdin = strings.NewReader("printf 'STATE:%s:%s:%s:%s\\n' \"$PWD\" \"$HOME\" \"$RC_LOADED\" \"$HOOK_LOADED\"\ncd '" + sub + "'\npwd\ncd '" + root + "'\npwd\nexit\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	text := string(output)
	for _, want := range []string{name + " › " + workspace + " $ ", name + " › " + workspace + "/sub $(touch PATH_INJECTED) $ ", name + " › " + root + " $ ", "STATE:" + workspace + ":" + home + ":yes:yes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in %s", want, text)
		}
	}
	for _, file := range []string{"NAME_INJECTED", "OTHER_INJECTED", "PATH_INJECTED"} {
		if _, err := os.Stat(filepath.Join(workspace, file)); !os.IsNotExist(err) {
			t.Fatalf("injected file %s", file)
		}
	}
}

func TestModelTerminalDefaultAndCustomCommand(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fake := &inputControlTransport{}
	s.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: fake}})
	bot, err := s.store.CreateBot("Prompt bot", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.store.AddRun(bot.DMConversationID, bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.store.SetRunStatus(run.ID, "running", ""); err != nil {
		t.Fatal(err)
	}
	tool := s.terminalTool(run)
	for _, command := range []string{"", "printf custom"} {
		raw, _ := json.Marshal(map[string]string{"action": "open", "command": command})
		if _, err = tool.Execute(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		last := fake.actions[len(fake.actions)-1]
		fake.mu.Unlock()
		var args map[string]any
		_ = json.Unmarshal(last.Args, &args)
		want := command
		if want == "" {
			want = s.defaultTerminalCommand(bot.ID)
		}
		if args["command"] != want {
			t.Fatal(args)
		}
	}
	value := map[string]any{"terminals": []any{map[string]any{"command": s.defaultTerminalCommand(bot.ID)}, map[string]any{"command": "printf custom"}}}
	normalizeTerminalCommands(value)
	rows := value["terminals"].([]any)
	if rows[0].(map[string]any)["command"] != "exec bash -i" || rows[1].(map[string]any)["command"] != "printf custom" {
		t.Fatal(value)
	}
}
