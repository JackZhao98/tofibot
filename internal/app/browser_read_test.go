package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
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

func TestBrowserReadCarriesGuestTabLimitNote(t *testing.T) {
	page := `{"title":"T","url":"https://example.com","text":"hello"}`
	note := `Closed least-recently-used tab "News (news.example)" to stay within the 3-tab limit.`
	out, _ := json.Marshal(map[string]any{"stdout": page, "exit_code": 0, "tab_limit_note": note})
	got, err := browserReadResult(string(out))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(got), &decoded) != nil || decoded["tab_limit_note"] != note || decoded["text"] != "hello" {
		t.Fatalf("got=%s", got)
	}
	quoted, _ := json.Marshal(note)
	if withTabLimitNote("{}", note) != `{"tab_limit_note": `+string(quoted)+`}` {
		t.Fatalf("empty page: %s", withTabLimitNote("{}", note))
	}
	// Reads carry the guest's notes; locate probes for action review do not
	// consume them because they use another heredoc tag.
	read, _ := browserReadCommand(browserReadArgs{})
	dry, _ := browserReadCommand(browserReadArgs{Click: "Send", Dry: true})
	probe, _ := browserReadCommand(browserReadArgs{Probe: []float64{1, 2, 3, 4}})
	if !strings.Contains(string(read), "TOFI_BROWSER_READ") || strings.Contains(string(dry), "TOFI_BROWSER_READ") || strings.Contains(string(probe), "TOFI_BROWSER_READ") {
		t.Fatal("only page reads may use the TOFI_BROWSER_READ tag")
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

func TestActionReviewFollowsDeclaredEffect(t *testing.T) {
	s, bot, conv := contextSurfaceServer(t)
	_, run, _, err := s.store.AddUserRun(conv.ID, bot.ID, "fill the httpbin form and submit it", "effect-review")
	if err != nil {
		t.Fatal(err)
	}
	decision := "allow"
	var seen actionReview
	stub := &reviewStub{t: t, reply: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &seen)
		return &provider.ChatResponse{Content: `{"decision":"` + decision + `","reason":"stub"}`}, nil
	}}
	s.autoReviewProvider = stub
	if err := s.guardAction(context.Background(), run, actionReview{Kind: "click", Effect: "none", Element: "Next page"}); err != nil || stub.calls.Load() != 0 {
		t.Fatalf("a no-effect action must not be reviewed: err=%v calls=%d", err, stub.calls.Load())
	}
	if err := s.guardAction(context.Background(), run, actionReview{Kind: "click", Effect: "submit", Element: "Submit order"}); err != nil {
		t.Fatalf("reviewer allowed: %v", err)
	}
	if seen.Request != "fill the httpbin form and submit it" || seen.Effect != "submit" || seen.Element != "Submit order" {
		t.Fatalf("reviewer input=%+v", seen)
	}
	decision = "confirm"
	err = s.guardAction(context.Background(), run, actionReview{Kind: "click", Effect: "purchase", Element: "Place order"})
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "user_confirmation_required" {
		t.Fatalf("confirm must ask the user: %v", err)
	}
	s.autoReviewProvider = &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return nil, errors.New("down")
	}}
	if err := s.guardAction(context.Background(), run, actionReview{Kind: "click", Effect: "delete", Element: "Delete"}); err == nil {
		t.Fatal("an unavailable reviewer must ask the user")
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

func TestBrowserPageNavigationIsAnObservation(t *testing.T) {
	for _, action := range []string{"browser.navigate", "browser.new", "browser.switch", "browser.snapshot", "browser.read"} {
		if computerRecoveryIdentity("bot", microVMComputerID, action, json.RawMessage(`{"url":"https://example.com"}`)).Risk != "observation" {
			t.Fatalf("%s must be an observation", action)
		}
	}
	if computerRecoveryIdentity("bot", microVMComputerID, "browser.click", json.RawMessage(`{"click":"x"}`)).Risk == "observation" {
		t.Fatal("browser.click is not an observation")
	}
}
