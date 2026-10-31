package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

var errUnsupportedDesktopMCP = errors.New("unsupported action: desktop.mcp")

const dsStateReply = `{"tool":"get_app_state","text":"App=pcmanfm\n0 window Files\n 1 button Home\n 2 text Name: notes.txt","is_error":false}`

func dsServer(t *testing.T, engine string) (*Server, *pwRecorder, Run) {
	t.Helper()
	s, rec, run := pwServer(t, "")
	rec.reply = dsStateReply
	if engine != "" {
		v := engine
		if err := s.saveAutomationValue(autoDesktopEngine, &v); err != nil {
			t.Fatal(err)
		}
	}
	return s, rec, run
}

func TestDesktopEngineSettingChoosesTheTools(t *testing.T) {
	s, _, run := dsServer(t, "")
	classic := pwToolMap(s.microVMTools(run))
	for _, name := range desktopEngineToolNames() {
		if _, ok := classic[name]; ok {
			t.Fatalf("classic registered %s", name)
		}
	}
	for _, name := range []string{"computer_shell", "computer_files", "computer_desktop", "computer_browser", "computer_help"} {
		if _, ok := classic[name]; !ok {
			t.Fatalf("classic lacks %s", name)
		}
	}
	v := desktopEngineA11y
	if err := s.saveAutomationValue(autoDesktopEngine, &v); err != nil {
		t.Fatal(err)
	}
	a11y := pwToolMap(s.microVMTools(run))
	// The screenshot tool and the classic browser stay; the engine adds to them.
	for _, name := range append(desktopEngineToolNames(), "computer_shell", "computer_files", "computer_desktop", "computer_browser", "computer_help") {
		if _, ok := a11y[name]; !ok {
			t.Fatalf("a11y lacks %s", name)
		}
	}
	// Both engines together.
	pw := browserEnginePlaywright
	if err := s.saveAutomationValue(autoBrowserEngine, &pw); err != nil {
		t.Fatal(err)
	}
	both := pwToolMap(s.microVMTools(run))
	if !strings.HasPrefix(both["computer_browser"].Description, computerBrowserFallbackNote) {
		t.Fatal("with the browser engine on, computer_browser is only the fallback")
	}
	for _, name := range append(desktopEngineToolNames(), browserEngineToolNames()...) {
		if _, ok := both[name]; !ok {
			t.Fatalf("both engines lack %s", name)
		}
	}
}

func TestDesktopEngineDefaultsToClassicAndEnvOverrides(t *testing.T) {
	s := &Server{}
	if s.desktopEngineA11y() {
		t.Fatal("default must be classic")
	}
	t.Setenv("TOFI_DESKTOP_ENGINE", "a11y")
	if !s.desktopEngineA11y() {
		t.Fatal("environment override ignored")
	}
	t.Setenv("TOFI_DESKTOP_ENGINE", "bogus")
	if s.desktopEngineA11y() {
		t.Fatal("an invalid environment value must fall back")
	}
}

func TestDesktopEngineToolTextFollowsTheRules(t *testing.T) {
	for _, sp := range desktopEngineSpecs {
		for _, banned := range []string{"e.g.", "for example", "such as", "i.e."} {
			if strings.Contains(strings.ToLower(sp.Description), banned) {
				t.Errorf("%s description holds an example marker %q", sp.Name, banned)
			}
		}
		if !strings.HasPrefix(sp.Name, "desktop_") {
			t.Errorf("%s breaks the naming", sp.Name)
		}
		for key, prop := range sp.Props {
			if text, _ := prop.(map[string]any)["description"].(string); strings.Contains(strings.ToLower(text), "for example") {
				t.Errorf("%s.%s holds an example", sp.Name, key)
			}
		}
	}
	if strings.Contains(computerDesktopGuideA11y, "desktop.click") || strings.Contains(computerDesktopEssentialsA11y, "desktop.click") {
		t.Fatal("a11y guidance names classic actions")
	}
}

func TestDesktopEngineRelaysRefsAsElementIndexes(t *testing.T) {
	s, rec, run := dsServer(t, desktopEngineA11y)
	tools := pwToolMap(s.microVMTools(run))
	out, err := tools["desktop_click"].Execute(context.Background(), json.RawMessage(`{"app":"pcmanfm","ref":"1","element":"Home","click_count":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "button Home") {
		t.Fatalf("result lacks the state: %q", out)
	}
	a := rec.last()
	if a.Name != "desktop.mcp" {
		t.Fatalf("action %q", a.Name)
	}
	var sent struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(a.Args, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Tool != "click" || sent.Args["element_index"] != "1" || sent.Args["app"] != "pcmanfm" || sent.Args["click_count"] != float64(2) {
		t.Fatalf("relayed %+v", sent)
	}
	for _, leaked := range []string{"ref", "element", "effect"} {
		if _, has := sent.Args[leaked]; has {
			t.Fatalf("the model's %s argument leaked to the engine", leaked)
		}
	}
}

func TestDesktopEngineRejectsBadArguments(t *testing.T) {
	s, rec, run := dsServer(t, desktopEngineA11y)
	tools := pwToolMap(s.microVMTools(run))
	for name, raw := range map[string]string{
		"desktop_click":            `{"app":"a","element":"x"}`,
		"desktop_get_app_state":    `{}`,
		"desktop_set_value":        `{"app":"a","ref":"2","element":"f"}`,
		"desktop_type_text":        `{"app":"a","text":"hi","script":"x"}`,
		"desktop_press_key":        `{"app":"a"}`,
		"desktop_scroll":           `{"app":"a","ref":"2","direction":"down","x":1}`,
		"desktop_list_apps":        `{"app":"a"}`,
		"desktop_secondary_action": `{"app":"a","ref":"2","element":"x"}`,
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
	// An empty value is how a field is cleared.
	if _, err := tools["desktop_set_value"].Execute(context.Background(), json.RawMessage(`{"app":"a","ref":"2","element":"Name","value":"","effect":"none"}`)); err != nil {
		t.Fatalf("clearing a field: %v", err)
	}
}

// A consequential action goes through the same review as computer_desktop; a
// no-effect action never reaches the reviewer.
func TestDesktopEngineActionsGoThroughActionReview(t *testing.T) {
	s, rec, run := dsServer(t, desktopEngineA11y)
	decision := "confirm"
	var seen actionReview
	stub := &reviewStub{t: t, reply: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		_ = json.Unmarshal([]byte(req.Messages[0].Content), &seen)
		return &provider.ChatResponse{Content: `{"decision":"` + decision + `","reason":"stub"}`}, nil
	}}
	s.autoReviewProvider = stub
	tools := pwToolMap(s.microVMTools(run))
	ctx := context.Background()
	if _, err := tools["desktop_get_app_state"].Execute(ctx, json.RawMessage(`{"app":"pcmanfm"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := tools["desktop_click"].Execute(ctx, json.RawMessage(`{"app":"pcmanfm","ref":"1","element":"Home","effect":"none"}`)); err != nil || stub.calls.Load() != 0 {
		t.Fatalf("a no-effect click must not be reviewed: %v calls=%d", err, stub.calls.Load())
	}
	sent := len(rec.actions)
	_, err := tools["desktop_set_value"].Execute(ctx, json.RawMessage(`{"app":"pcmanfm","ref":"2","element":"Name","value":"rm all","effect":"delete"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Code != "user_confirmation_required" {
		t.Fatalf("a refused review must ask the user: %v", err)
	}
	if len(rec.actions) != sent {
		t.Fatal("an action the reviewer refused reached the computer")
	}
	if seen.Kind != "set_value" || seen.Effect != "delete" || seen.Element != "Name" || seen.Title != "pcmanfm" || seen.Text != "rm all" || seen.Request != "pay for the cart" {
		t.Fatalf("reviewer input %+v", seen)
	}
	decision = "allow"
	if _, err := tools["desktop_set_value"].Execute(ctx, json.RawMessage(`{"app":"pcmanfm","ref":"2","element":"Name","value":"x","effect":"delete"}`)); err != nil {
		t.Fatalf("an allowed action: %v", err)
	}
	seen = actionReview{}
	if _, err := tools["desktop_press_key"].Execute(ctx, json.RawMessage(`{"app":"pcmanfm","key":"Delete","effect":"delete"}`)); err != nil {
		t.Fatal(err)
	}
	if seen.Kind != "key" || seen.Text != "key Delete" {
		t.Fatalf("key review %+v", seen)
	}
}

func TestDesktopEngineStaleRefIsARepairableRefusal(t *testing.T) {
	s, rec, run := dsServer(t, desktopEngineA11y)
	rec.reply = `{"tool":"click","text":"unknown element_index \"9\"","is_error":true}`
	tools := pwToolMap(s.microVMTools(run))
	_, err := tools["desktop_click"].Execute(context.Background(), json.RawMessage(`{"app":"pcmanfm","ref":"9","element":"Old row"}`))
	if o, ok := tooloutcome.FromError(err); !ok || o.Certainty != "not_executed" || o.NextAction != "repair_arguments" {
		t.Fatalf("stale ref: %v", err)
	}
	// Any other engine error stays uncertain.
	rec.reply = `{"tool":"click","text":"Linux runtime timed out after 30s","is_error":true}`
	_, err = tools["desktop_click"].Execute(context.Background(), json.RawMessage(`{"app":"pcmanfm","ref":"1","element":"Home"}`))
	if o, ok := tooloutcome.FromError(err); ok && o.Certainty == "not_executed" {
		t.Fatalf("a timeout was reported as not executed: %v", err)
	}
}

func TestDesktopEngineToolsAreObservationsOrEffects(t *testing.T) {
	for _, name := range []string{"desktop_list_apps", "desktop_get_app_state", "desktop_scroll"} {
		if !desktopEngineObservation(name) {
			t.Errorf("%s must be an observation", name)
		}
		if mcpUncertainEffectActivity(ToolActivity{Name: name, Arguments: `{}`}) {
			t.Errorf("%s must not count as an uncertain effect", name)
		}
	}
	for _, name := range []string{"desktop_click", "desktop_set_value", "desktop_type_text", "desktop_press_key", "desktop_secondary_action"} {
		if desktopEngineObservation(name) {
			t.Errorf("%s must be an effect", name)
		}
		if !mcpUncertainEffectActivity(ToolActivity{Name: name, Arguments: `{}`}) {
			t.Errorf("%s must count as an uncertain effect", name)
		}
		if completionReviewTool(name) {
			t.Errorf("%s must not arm the completion review", name)
		}
	}
}

func TestDesktopEngineGuestWithoutTheActionIsNamed(t *testing.T) {
	s, rec, run := dsServer(t, desktopEngineA11y)
	rec.fail = errUnsupportedDesktopMCP
	tools := pwToolMap(s.microVMTools(run))
	_, err := tools["desktop_list_apps"].Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "newer Bot computer image") {
		t.Fatalf("an old guest must be named: %v", err)
	}
}

func TestDesktopEngineSettingIsWritableThroughTheSettingsAPI(t *testing.T) {
	s := providerServer(t, nil, nil)
	if code := autoPutCode(t, s, `{"desktop_engine":"bogus"}`); code != 400 {
		t.Fatalf("an unknown engine was accepted: %d", code)
	}
	if code := autoPutCode(t, s, `{"desktop_engine":"a11y"}`); code != 200 {
		t.Fatalf("PUT: %d", code)
	}
	item := autoGet(t, s).find(t, autoDesktopEngine)
	if item.Value != "a11y" || item.Source != "stored" || item.Default != "classic" {
		t.Fatalf("setting after PUT: %+v", item)
	}
	if !s.desktopEngineA11y() {
		t.Fatal("the stored value does not reach the tool registry")
	}
	if code := autoPutCode(t, s, `{"desktop_engine":null}`); code != 200 || s.desktopEngineA11y() {
		t.Fatalf("reset to default failed: %d", code)
	}
}

// Hybrid mode: the a11y engine adds to the classic screenshot and coordinate
// tool; its text says the tree is preferred and the coordinates are the fallback.
func TestDesktopEngineA11yIsHybridWithTheCoordinateTools(t *testing.T) {
	s, _, run := dsServer(t, desktopEngineA11y)
	tools := pwToolMap(s.microVMTools(run))
	desk, ok := tools["computer_desktop"]
	if !ok {
		t.Fatal("a11y mode lost computer_desktop")
	}
	if !strings.HasPrefix(desk.Description, computerDesktopHybridNote) || !strings.Contains(desk.Description, "fallback") {
		t.Fatalf("computer_desktop does not say it is the fallback: %.120s", desk.Description)
	}
	schema, _ := json.Marshal(desk.Parameters)
	for _, action := range []string{"desktop.capture", "desktop.click", "desktop.type", "desktop.key", "desktop.scroll"} {
		if !strings.Contains(string(schema), action) {
			t.Errorf("computer_desktop lost %s", action)
		}
	}
	for _, name := range desktopEngineToolNames() {
		if _, ok := tools[name]; !ok {
			t.Errorf("a11y mode lacks %s", name)
		}
	}
	if !strings.Contains(tools["desktop_get_app_state"].Description, "Preferred") || !strings.Contains(computerDesktopEssentialsA11y, "coordinate") {
		t.Fatal("the desktop_ text must say the tree is preferred and coordinates are the fallback")
	}
	// Classic mode keeps the plain description.
	s2, _, run2 := dsServer(t, "")
	if strings.Contains(pwToolMap(s2.microVMTools(run2))["computer_desktop"].Description, computerDesktopHybridNote) {
		t.Fatal("classic mode must not carry the hybrid note")
	}
}
