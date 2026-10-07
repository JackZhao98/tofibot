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

func TestConsequentialClickLabels(t *testing.T) {
	for label, risky := range map[string]bool{
		"Place order": true, "Buy now": true, "Send": true, "Delete": true, "提交订单": true, "立即支付": true, "发送": true,
		"Sent": false, "Posts": false, "Your reward is ready": false, "Inbox": false, "Promotions": false, "Read more": false,
	} {
		if consequentialLabel(label) != risky {
			t.Errorf("%q risky=%v", label, !risky)
		}
	}
}

func TestConsequentialClickNeedsHumanApprovalOfThatTarget(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	approve := func(key, actor string) Run {
		_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "buy it", key)
		if err != nil {
			t.Fatal(err)
		}
		if s.clickApproved(run, "Place order") {
			t.Fatal("approved without any approval")
		}
		if _, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil {
			t.Fatal(err)
		}
		q, err := s.store.CreateQuestion(conv.ID, run, askQuestionInput{Question: "Place the order?", Type: "approval", Approval: &ApprovalDetails{Action: "Click", Target: "Place order", Impact: "Charges the card"}})
		if err != nil {
			t.Fatal(err)
		}
		// The store refuses an automatic actor outright.
		if _, _, err := s.store.AnswerQuestion(q.ID, actor, true); err != nil && actor != autoReviewActor {
			t.Fatal(err)
		}
		return run
	}
	auto := approve("auto", autoReviewActor)
	if s.clickApproved(auto, "Place order") {
		t.Fatal("an automatic answer is not a person's approval")
	}
	if _, err := s.store.SetRunStatus(auto.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	run := approve("human", "user")
	if !s.clickApproved(run, "Place order") || s.clickApproved(run, "Delete account") {
		t.Fatal("approval must cover exactly the approved target")
	}
}
