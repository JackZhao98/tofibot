package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer"
	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// pwRecorder is a computer whose guest answers browser.mcp like the Playwright
// relay does, and records every action it receives.
type pwRecorder struct {
	*fakeComputer
	rmu     sync.Mutex
	actions []computer.Action
	reply   string
}

func (p *pwRecorder) Action(ctx context.Context, a computer.Action) (computer.ActionResult, error) {
	return p.ActionWithin(ctx, a, 0)
}

func (p *pwRecorder) ActionWithin(_ context.Context, a computer.Action, _ time.Duration) (computer.ActionResult, error) {
	p.rmu.Lock()
	p.actions = append(p.actions, a)
	p.rmu.Unlock()
	if a.Name == "browser.mcp" {
		return computer.ActionResult{OK: true, Result: json.RawMessage(p.reply)}, nil
	}
	return computer.ActionResult{OK: true, Result: json.RawMessage(`{}`)}, nil
}

func (p *pwRecorder) last() computer.Action {
	p.rmu.Lock()
	defer p.rmu.Unlock()
	return p.actions[len(p.actions)-1]
}

func pwServer(t *testing.T, engine string) (*Server, *pwRecorder, Run) {
	t.Helper()
	s, bot, conv := contextSurfaceServer(t)
	rec := &pwRecorder{fakeComputer: newFakeComputer("firecracker"), reply: `{"tool":"x","text":"### Page\n- Page URL: https://shop.example/cart\n- Page Title: Cart\n### Snapshot\n- button \"Pay\" [ref=e7]","is_error":false}`}
	s.microVM = rec
	if engine != "" {
		v := engine
		if err := s.saveAutomationValue(autoBrowserEngine, &v); err != nil {
			t.Fatal(err)
		}
	}
	run := contextSurfaceRun(t, s, conv, bot, "pay for the cart")
	return s, rec, run
}

func pwToolMap(tools []Tool) map[string]Tool {
	m := map[string]Tool{}
	for _, tool := range tools {
		m[tool.Name] = tool
	}
	return m
}

func TestBrowserEngineSettingChoosesTheTools(t *testing.T) {
	s, _, run := pwServer(t, "")
	classic := pwToolMap(s.microVMTools(run))
	if _, ok := classic["computer_browser"]; !ok {
		t.Fatal("classic must keep computer_browser")
	}
	for _, name := range browserEngineToolNames() {
		if _, ok := classic[name]; ok {
			t.Fatalf("classic registered %s", name)
		}
	}
	v := browserEnginePlaywright
	if err := s.saveAutomationValue(autoBrowserEngine, &v); err != nil {
		t.Fatal(err)
	}
	pw := pwToolMap(s.microVMTools(run))
	if _, ok := pw["computer_browser"]; ok {
		t.Fatal("playwright must replace computer_browser")
	}
	for _, name := range append(browserEngineToolNames(), "computer_shell", "computer_files", "computer_desktop", "computer_help") {
		if _, ok := pw[name]; !ok {
			t.Fatalf("playwright lacks %s", name)
		}
	}
}

func TestBrowserEngineDefaultsToClassicAndEnvOverrides(t *testing.T) {
	s := &Server{}
	if s.browserEnginePlaywright() {
		t.Fatal("default must be classic")
	}
	t.Setenv("TOFI_BROWSER_ENGINE", "playwright")
	if !s.browserEnginePlaywright() {
		t.Fatal("environment override ignored")
	}
	t.Setenv("TOFI_BROWSER_ENGINE", "bogus")
	if s.browserEnginePlaywright() {
		t.Fatal("an invalid environment value must fall back")
	}
}

func TestBrowserEngineToolTextFollowsTheRules(t *testing.T) {
	for _, sp := range browserEngineSpecs {
		text := sp.Description
		for _, banned := range []string{"e.g.", "for example", "such as", "i.e."} {
			if strings.Contains(strings.ToLower(text), banned) {
				t.Errorf("%s description holds an example marker %q", sp.Name, banned)
			}
		}
		if !strings.HasPrefix(sp.Name, "browser_") {
			t.Errorf("%s breaks the naming", sp.Name)
		}
	}
	if strings.Contains(computerBrowserGuidePlaywright, "browser.read") || strings.Contains(computerBrowserEssentialsPlaywright, "browser.click") {
		t.Fatal("playwright guidance names classic actions")
	}
}

func TestBrowserEngineRelaysRefsAsTargets(t *testing.T) {
	s, rec, run := pwServer(t, browserEnginePlaywright)
	tools := pwToolMap(s.microVMTools(run))
	out, err := tools["browser_click"].Execute(context.Background(), json.RawMessage(`{"ref":"e7","element":"Pay button","double_click":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[ref=e7]") {
		t.Fatalf("result lacks the snapshot: %q", out)
	}
	a := rec.last()
	if a.Name != "browser.mcp" {
		t.Fatalf("action %q", a.Name)
	}
	var sent struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(a.Args, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Tool != "browser_click" || sent.Args["target"] != "e7" || sent.Args["doubleClick"] != true || sent.Args["element"] != "Pay button" {
		t.Fatalf("relayed %+v", sent)
	}
	if _, has := sent.Args["ref"]; has {
		t.Fatal("the model's ref name leaked to the engine")
	}
}

func TestBrowserEngineRejectsBadArguments(t *testing.T) {
	s, rec, run := pwServer(t, browserEnginePlaywright)
	tools := pwToolMap(s.microVMTools(run))
	for name, raw := range map[string]string{
		"browser_click":       `{"element":"x"}`,
		"browser_navigate":    `{}`,
		"browser_type":        `{"ref":"e1","element":"f","text":"a","script":"x"}`,
		"browser_snapshot":    `{"filename":"/etc/passwd"}`,
		"browser_file_upload": `{"paths":[]}`,
	} {
		before := len(rec.actions)
		_, err := tools[name].Execute(context.Background(), json.RawMessage(raw))
		if err == nil {
			t.Errorf("%s accepted %s", name, raw)
		}
		if o, ok := tooloutcome.FromError(err); !ok || o.Certainty != "not_executed" {
			t.Errorf("%s: want a not_executed refusal, got %v", name, err)
		}
		if len(rec.actions) != before {
			t.Errorf("%s reached the computer", name)
		}
	}
}

// A consequential action goes through the same review as computer_browser; a
// no-effect action never reaches the reviewer.
func TestBrowserEngineActionsGoThroughActionReview(t *testing.T) {
	s, rec, run := pwServer(t, browserEnginePlaywright)
	decision := "confirm"
	var seen actionReview
	stub := &reviewStub{t: t, reply: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &seen)
		return &provider.ChatResponse{Content: `{"decision":"` + decision + `","reason":"stub"}`}, nil
	}}
	s.autoReviewProvider = stub
	tools := pwToolMap(s.microVMTools(run))
	ctx := context.Background()
	// Learn the page first, as a snapshot does.
	if _, err := tools["browser_snapshot"].Execute(ctx, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools["browser_click"].Execute(ctx, json.RawMessage(`{"ref":"e7","element":"Next","effect":"none"}`)); err != nil || stub.calls.Load() != 0 {
		t.Fatalf("a no-effect click must not be reviewed: %v calls=%d", err, stub.calls.Load())
	}
	sent := len(rec.actions)
	_, err := tools["browser_click"].Execute(ctx, json.RawMessage(`{"ref":"e7","element":"Pay button","effect":"purchase"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "user_confirmation_required" {
		t.Fatalf("a refused review must ask the user: %v", err)
	}
	if len(rec.actions) != sent {
		t.Fatal("a click the reviewer refused reached the computer")
	}
	if seen.Kind != "click" || seen.Effect != "purchase" || seen.Element != "Pay button" || seen.URL != "https://shop.example/cart" || seen.Title != "Cart" || seen.Request != "pay for the cart" {
		t.Fatalf("reviewer input %+v", seen)
	}
	decision = "allow"
	if _, err := tools["browser_click"].Execute(ctx, json.RawMessage(`{"ref":"e7","element":"Pay button","effect":"purchase"}`)); err != nil {
		t.Fatalf("an allowed click: %v", err)
	}
	// An upload is outgoing data: reviewed as send even when none is declared.
	seen = actionReview{}
	if _, err := tools["browser_file_upload"].Execute(ctx, json.RawMessage(`{"paths":["report.pdf"]}`)); err != nil {
		t.Fatal(err)
	}
	if seen.Effect != "send" || seen.Kind != "upload" || seen.Text != "report.pdf" {
		t.Fatalf("upload review %+v", seen)
	}
	var args struct {
		Arguments struct {
			Paths []string `json:"paths"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(rec.last().Args, &args)
	if len(args.Arguments.Paths) != 1 || args.Arguments.Paths[0] != "/workspace/bots/"+run.BotID+"/report.pdf" {
		t.Fatalf("upload path %v", args.Arguments.Paths)
	}
}

func TestBrowserEngineStaleRefIsARepairableRefusal(t *testing.T) {
	s, rec, run := pwServer(t, browserEnginePlaywright)
	rec.reply = `{"tool":"browser_click","text":"Error: Ref e9 not found in the current page snapshot. Try capturing new snapshot.","is_error":true}`
	tools := pwToolMap(s.microVMTools(run))
	_, err := tools["browser_click"].Execute(context.Background(), json.RawMessage(`{"ref":"e9","element":"Old link"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Certainty != "not_executed" || o.NextAction != "repair_arguments" {
		t.Fatalf("stale ref: %v", err)
	}
}

func TestBrowserEngineToolsAreObservationsOrEffects(t *testing.T) {
	for _, name := range []string{"browser_snapshot", "browser_navigate", "browser_wait_for", "browser_tabs", "browser_hover", "browser_navigate_back"} {
		if !browserEngineObservation(name) {
			t.Errorf("%s must be an observation", name)
		}
		if mcpUncertainEffectActivity(ToolActivity{Name: name, Arguments: `{}`}) {
			t.Errorf("%s must not count as an uncertain effect", name)
		}
	}
	for _, name := range []string{"browser_click", "browser_type", "browser_press_key", "browser_select_option", "browser_fill_form", "browser_file_upload", "browser_handle_dialog"} {
		if browserEngineObservation(name) {
			t.Errorf("%s must be an effect", name)
		}
		if !mcpUncertainEffectActivity(ToolActivity{Name: name, Arguments: `{}`}) {
			t.Errorf("%s must count as an uncertain effect", name)
		}
		if completionReviewTool(name) {
			t.Errorf("%s must not arm the completion review (browsing tool)", name)
		}
	}
}
