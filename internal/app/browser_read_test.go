package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBrowserReadCommandIsBoundedAndQuoted(t *testing.T) {
	raw, err := browserReadCommand(browserReadArgs{Find: `it's "quoted"`, MaxChars: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var shell struct {
		Command    string `json:"command"`
		TimeoutSec int    `json:"timeout_sec"`
	}
	if json.Unmarshal(raw, &shell) != nil || shell.TimeoutSec != 30 || !strings.HasPrefix(shell.Command, "python3 - ") {
		t.Fatalf("shell=%+v", shell)
	}
	encoded := strings.Fields(shell.Command)[2]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var opts struct {
		Max  int    `json:"max"`
		Find string `json:"find"`
	}
	if json.Unmarshal(decoded, &opts) != nil || opts.Max != browserReadMaxChars || opts.Find != `it's "quoted"` {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestBrowserReadResultUnwrapsShell(t *testing.T) {
	page := `{"title":"T","url":"https://example.com","text":"hello","links":[]}`
	out, _ := json.Marshal(map[string]any{"stdout": page + "\n", "stderr": "", "exit_code": 0})
	got, err := browserReadResult(string(out))
	if err != nil || got != page {
		t.Fatalf("got=%q err=%v", got, err)
	}
	gone, _ := json.Marshal(map[string]any{"stdout": `{"error": "chrome_not_running"}`, "exit_code": 0})
	if _, err := browserReadResult(string(gone)); err == nil || !browserProcessGone(err) {
		t.Fatalf("chrome not running must be a restartable failure, err=%v", err)
	}
	if computerRecoveryIdentity("bot", microVMComputerID, "browser.read", json.RawMessage(`{}`)).Risk != "observation" {
		t.Fatal("browser.read must be an observation")
	}
}

func TestBrowserClickCarriesTextAndIsNotAnObservation(t *testing.T) {
	raw, _ := browserReadCommand(browserReadArgs{Click: "Your Ticket has been issued"})
	var shell struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(raw, &shell)
	decoded, _ := base64.StdEncoding.DecodeString(strings.Fields(shell.Command)[2])
	if !strings.Contains(string(decoded), `"click":"Your Ticket has been issued"`) {
		t.Fatalf("opts=%s", decoded)
	}
	missing, _ := json.Marshal(map[string]any{"stdout": `{"error": "click_target_not_found", "click": "x"}`, "exit_code": 0})
	if _, err := browserReadResult(string(missing)); err == nil || browserProcessGone(err) {
		t.Fatalf("missing click target must be a repairable argument error, err=%v", err)
	}
	if computerRecoveryIdentity("bot", microVMComputerID, "browser.click", json.RawMessage(`{"click":"x"}`)).Risk == "observation" {
		t.Fatal("a click changes the page")
	}
}

func TestOnlyDeadDevToolsTriggersDesktopRestart(t *testing.T) {
	dead := errors.New(`computer control returned 500 Internal Server Error: {"ok":false,"error":"Get \"http://127.0.0.1:38589/json/list\": dial tcp 127.0.0.1:38589: connect: connection refused"}`)
	if !browserProcessGone(dead) {
		t.Fatal("dead DevTools endpoint must restart the desktop")
	}
	if browserProcessGone(errors.New("dial unix /run/tofi/guest.sock: connect: connection refused")) {
		t.Fatal("a host-to-guest socket failure is not a dead Chrome")
	}
}
