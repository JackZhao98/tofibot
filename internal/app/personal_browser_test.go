package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

func browserFixture(t *testing.T) (*Server, *testMac, *browserHub, Run) {
	t.Helper()
	s := computerTestServer(t)
	s.microVM = nil
	m := enrollMac(t, s, "Synthetic Mac", "browser.open", "browser.navigate", "browser.read", "browser.snapshot", "browser.click", "browser.type", "browser.key", "browser.scroll", "browser.back", "browser.close")
	h := s.browserHub(m.id)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h.upload = &browserUpload{epoch: uuid.NewString(), ctx: ctx, cancel: cancel, out: make(chan any, 32), lastPong: time.Now()}
	h.supported = true
	session, target := uuid.NewString(), uuid.NewString()
	h.state = &browserState{Type: "state", V: 1, Connection: h.upload.epoch, Seq: 1, Session: &session, Target: &target, Viewport: 1, Phase: "idle", Preview: "ready"}
	h.state.Settings.Enabled = true
	h.state.Settings.Share = true
	h.targets[target] = browserTarget{Target: target, URL: "https://synthetic.invalid/page", Title: "Synthetic"}
	return s, m, h, activeComputerRun(t, s)
}
func browserArgs(h *browserHub, more map[string]any) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	v := map[string]any{"session_epoch": h.state.Session, "target_id": h.state.Target}
	for k, x := range more {
		v[k] = x
	}
	raw, _ := json.Marshal(v)
	return raw
}
func browserAnswer(t *testing.T, m *testMac, h *browserHub, j *ComputerJob, data any) {
	t.Helper()
	h.mu.Lock()
	v := map[string]any{"v": 1, "connection_epoch": h.upload.epoch, "session_epoch": h.state.Session, "target_id": h.state.Target, "viewport_epoch": h.state.Viewport, "data": data}
	h.mu.Unlock()
	raw, _ := json.Marshal(v)
	if e := m.s.store.completeComputerJob(m.id, m.token, j.ID, raw, ""); e != nil {
		t.Fatal(e)
	}
}
func browserReadData(h *browserHub) any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{"url": "https://synthetic.invalid/page", "title": "Synthetic", "text": "hello", "links": []any{}, "targets": []browserTarget{{Target: *h.state.Target, URL: "https://synthetic.invalid/page", Title: "Synthetic"}}, "truncated": false, "untrusted": true}
}
func TestPersonalBrowserNoVMExplicitRouting(t *testing.T) {
	s, m, h, r := browserFixture(t)
	var tool Tool
	for _, v := range s.microVMTools(r) {
		if v.Name == "computer_browser" {
			tool = v
		}
	}
	if tool.Execute == nil {
		t.Fatal("browser missing without VM")
	}
	if _, e := tool.Execute(context.Background(), json.RawMessage(`{"action":"read"}`)); toolCode(e) != "computer_not_configured" {
		t.Fatalf("implicit Mac: %v", e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := tool.Execute(context.Background(), json.RawMessage(`{"computer_id":"`+m.id+`","action":"read"}`))
		done <- e
	}()
	j := m.poll(1)
	if j == nil || j.Action != "browser.read" {
		t.Fatalf("job: %+v", j)
	}
	if strings.Contains(string(j.Args), "effect") || !strings.Contains(string(j.Args), h.upload.epoch) {
		t.Fatalf("wire: %s", j.Args)
	}
	browserAnswer(t, m, h, j, browserReadData(h))
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if s.computer() != nil {
		t.Fatal("Mac request attached a VM")
	}
}

type browserReviewer struct {
	allowReviewer
	onReview func()
	calls    int
	input    string
}

func (p *browserReviewer) Chat(_ context.Context, r *provider.ChatRequest) (*provider.ChatResponse, error) {
	p.calls++
	p.input = r.Messages[0].Content
	if p.onReview != nil {
		p.onReview()
	}
	return &provider.ChatResponse{Content: `{"decision":"allow"}`}, nil
}
func TestPersonalBrowserApprovalAndGenerationRecheck(t *testing.T) {
	s, m, h, r := browserFixture(t)
	review := &browserReviewer{onReview: func() { h.mu.Lock(); session := uuid.NewString(); h.state.Session = &session; h.mu.Unlock() }}
	s.autoReviewProvider = review
	_, e := s.computerActionOn(context.Background(), r, m.id, "browser.type", browserArgs(h, map[string]any{"text": "synthetic"}))
	if toolCode(e) != "browser_session_changed" || review.calls != 1 {
		t.Fatalf("review/recheck: %v calls=%d", e, review.calls)
	}
	var count int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE device_id=?`, m.id).Scan(&count)
	if count != 0 {
		t.Fatal("queued before approval or inspected desktop")
	}
	if !strings.Contains(review.input, "https://synthetic.invalid") || strings.Contains(review.input, "desktop.inspect") {
		t.Fatalf("facts: %s", review.input)
	}
}
func TestPersonalBrowserDirectComputerActionApproval(t *testing.T) {
	s, m, h, r := browserFixture(t)
	p := &browserReviewer{}
	s.autoReviewProvider = p
	tool := computerActionTool(s, r)
	raw := browserArgs(h, map[string]any{"text": "synthetic"})
	done := make(chan error, 1)
	go func() { _, e := runCall(tool, m.id, "browser.type", string(raw)); done <- e }()
	j := m.poll(1)
	if j == nil || j.Action != "browser.type" {
		t.Fatalf("job %+v", j)
	}
	browserAnswer(t, m, h, j, map[string]any{"inserted": true})
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if p.calls != 1 {
		t.Fatal("direct computer_action bypassed approval")
	}
}
func TestPersonalBrowserArguments(t *testing.T) {
	connection, session, target := uuid.NewString(), uuid.NewString(), uuid.NewString()
	base := map[string]any{"v": 1, "connection_epoch": connection, "session_epoch": session, "target_id": target}
	cases := []struct {
		action string
		extra  map[string]any
		valid  bool
	}{
		{"browser.navigate", map[string]any{"url": "https://synthetic.invalid"}, true},
		{"browser.navigate", map[string]any{"url": "file:///tmp/secret"}, false},
		{"browser.type", map[string]any{"text": strings.Repeat("x", 16385)}, false},
		{"browser.key", map[string]any{"key": "Enter", "modifiers": []string{"meta"}}, false},
		{"browser.key", map[string]any{"key": "Enter", "modifiers": []string{"shift", "shift"}}, false},
		{"browser.key", map[string]any{"key": "Enter", "modifiers": []string{"shift"}}, true},
		{"browser.click", map[string]any{"click": "button", "x": 1, "y": 1, "viewport_epoch": 1}, false},
		{"browser.click", map[string]any{"x": 1, "y": 1, "viewport_epoch": 1}, true},
		{"browser.scroll", map[string]any{"delta_x": 0, "delta_y": 2001, "viewport_epoch": 1}, false},
		{"browser.read", map[string]any{"find": strings.Repeat("a", 1025)}, false},
		{"browser.snapshot", map[string]any{"selector": "#unsafe"}, false},
		{"browser.close", map[string]any{}, true},
	}
	for _, tt := range cases {
		t.Run(tt.action, func(t *testing.T) {
			m := map[string]any{}
			for k, v := range base {
				m[k] = v
			}
			for k, v := range tt.extra {
				m[k] = v
			}
			raw, _ := json.Marshal(m)
			if e := validateBrowserArgs(tt.action, raw); (e == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, e)
			}
		})
	}
}
func TestPersonalBrowserErrorCertainty(t *testing.T) {
	for code := range browserFailureCodes {
		for _, certainty := range []string{"not_executed", "unknown"} {
			raw, _ := json.Marshal(map[string]any{"v": 1, "failure": map[string]any{"code": code, "certainty": certainty}})
			e := browserFailure(raw, code)
			out, ok := tooloutcome.FromError(e)
			if !ok || out.Certainty == "" {
				t.Fatalf("untyped %s: %v", code, e)
			}
			if certainty == "unknown" && out.Certainty != "unknown" {
				t.Fatalf("downgraded unknown %s", code)
			}
		}
	}
	for _, raw := range []string{`null`, `{"failure":{"code":"user_stop","certainty":"maybe"}}`, `{"failure":{"code":"user_stop","certainty":"not_executed"},"injected":true}`} {
		out, _ := tooloutcome.FromError(browserFailure([]byte(raw), "user_stop"))
		if out.Code != "browser_control_lost" || out.Certainty != "unknown" {
			t.Fatalf("invalid refusal %s: %+v", raw, out)
		}
	}
}
func TestPersonalBrowserResultBindingAndLateJob(t *testing.T) {
	s, m, h, r := browserFixture(t)
	args, e := s.prepareBrowser(m.id, "browser.read", []byte(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.store.queueComputerJob(r, m.id, "browser.read", args)
	if e != nil {
		t.Fatal(e)
	}
	jptr, e := s.store.claimComputerJob(m.id, m.token)
	if e != nil || jptr == nil {
		t.Fatalf("claim %v", e)
	}
	raw, _ := json.Marshal(map[string]any{"v": 1, "connection_epoch": uuid.NewString(), "session_epoch": h.state.Session, "target_id": h.state.Target, "viewport_epoch": 1, "data": browserReadData(h)})
	if e = s.store.completeComputerJob(m.id, m.token, j.ID, raw, ""); e == nil {
		t.Fatal("forged connection accepted")
	}
	s.store.cancelJob(j.ID, "cancelled")
	raw, _ = json.Marshal(map[string]any{"v": 1, "connection_epoch": h.upload.epoch, "session_epoch": h.state.Session, "target_id": h.state.Target, "viewport_epoch": 1, "data": browserReadData(h)})
	if e = s.store.completeComputerJob(m.id, m.token, j.ID, raw, ""); e == nil {
		t.Fatal("late result accepted")
	}
}
func TestPersonalBrowserSnapshotDoesNotEnterTrace(t *testing.T) {
	s, _, h, r := browserFixture(t)
	raw, _ := json.Marshal(map[string]any{"connection_epoch": h.upload.epoch, "data": map[string]any{"image_url": "data:image/jpeg;base64,synthetic"}})
	got, e := s.finishCapture(context.Background(), r, "computer_browser", "browser.snapshot", nil, string(raw), "full")
	if e != nil || got != string(raw) {
		t.Fatalf("capture %s %v", got, e)
	}
	var count int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM run_captures WHERE run_id=?`, r.ID).Scan(&count)
	if count != 0 {
		t.Fatal("Mac image entered VM trace")
	}
}
func TestPersonalBrowserTwoBotsSerial(t *testing.T) {
	s, m, h, r := browserFixture(t)
	r2 := activeComputerRun(t, s)
	done := make(chan error, 2)
	for _, run := range []Run{r, r2} {
		go func(r Run) {
			_, e := s.computerActionOn(context.Background(), r, m.id, "browser.read", []byte(`{}`))
			done <- e
		}(run)
	}
	first := m.poll(1)
	if first == nil {
		t.Fatal("no first job")
	}
	if second := m.poll(0); second != nil {
		t.Fatal("concurrent browser jobs")
	}
	browserAnswer(t, m, h, first, browserReadData(h))
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	second := m.poll(1)
	if second == nil {
		t.Fatal("no second job")
	}
	browserAnswer(t, m, h, second, browserReadData(h))
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestPersonalBrowserSnapshotValidationAndVisionOutput(t *testing.T) {
	s, m, h, _ := browserFixture(t)
	args, e := s.prepareBrowser(m.id, "browser.snapshot", browserArgs(h, nil))
	if e != nil {
		t.Fatal(e)
	}
	data := map[string]any{"image_url": "data:image/jpeg;base64," + browserJPEG(t), "width": 16, "height": 10, "css_width": 16, "css_height": 10, "shared_with_person": false}
	envelope := map[string]any{"v": 1, "connection_epoch": h.upload.epoch, "session_epoch": h.state.Session, "target_id": h.state.Target, "viewport_epoch": 1, "data": data}
	raw, _ := json.Marshal(envelope)
	if e := validateBrowserResult("browser.snapshot", args, raw, ""); e != nil {
		t.Fatal(e)
	}
	out := browserModelResult("browser.snapshot", raw)
	var image map[string]json.RawMessage
	_ = json.Unmarshal([]byte(out), &image)
	if len(image["image_url"]) == 0 || image["data"] != nil || len(image["connection_epoch"]) == 0 {
		t.Fatal("snapshot cannot reach existing vision adapter")
	}
	for _, bad := range []any{"data:image/png;base64,AAA=", "data:image/jpeg;base64," + strings.Repeat("A", 1024*1024)} {
		data["image_url"] = bad
		raw, _ = json.Marshal(envelope)
		if e := validateBrowserResult("browser.snapshot", args, raw, ""); e == nil {
			t.Fatal("invalid or oversized snapshot accepted")
		}
	}
}
func TestPersonalBrowserMissingTargetsAndServerFields(t *testing.T) {
	s, m, h, _ := browserFixture(t)
	for _, tt := range []struct {
		action string
		args   string
		code   string
	}{
		{"browser.read", `{"v":1}`, "browser_action_unsupported"},
		{"browser.read", `{"connection_epoch":"forged"}`, "browser_action_unsupported"},
		{"browser.type", `{"text":"x"}`, "browser_action_unsupported"},
		{"browser.upload", `{}`, "browser_action_unsupported"},
	} {
		_, e := s.prepareBrowser(m.id, tt.action, []byte(tt.args))
		if toolCode(e) != tt.code {
			t.Fatalf("%s: %v", tt.action, e)
		}
	}
	args := browserArgs(h, map[string]any{"x": 1, "y": 1, "viewport_epoch": 2})
	if _, e := s.prepareBrowser(m.id, "browser.click", args); toolCode(e) != "browser_viewport_changed" {
		t.Fatalf("old viewport %v", e)
	}
	h.mu.Lock()
	h.state.Target = nil
	h.state.Viewport = 0
	h.mu.Unlock()
	if _, e := s.prepareBrowser(m.id, "browser.read", []byte(`{}`)); toolCode(e) != "browser_target_gone" {
		t.Fatalf("missing page %v", e)
	}
}
func TestPersonalBrowserStopsBeforeDeliveringPrivateResult(t *testing.T) {
	s, m, h, r := browserFixture(t)
	done := make(chan error, 1)
	go func() {
		_, e := s.computerActionOn(context.Background(), r, m.id, "browser.read", []byte(`{}`))
		done <- e
	}()
	j := m.poll(1)
	if j == nil {
		t.Fatal("no job")
	}
	data := browserReadData(h)
	h.mu.Lock()
	h.state.Phase = "human"
	h.state.Preview = "human"
	h.mu.Unlock()
	browserAnswer(t, m, h, j, data)
	e := <-done
	out, _ := tooloutcome.FromError(e)
	if out.Code != "user_takeover" || out.Certainty != "unknown" {
		t.Fatalf("delivered after takeover %v", e)
	}
}

func TestPersonalBrowserPreservesVMDispatchAndRecovery(t *testing.T) {
	s := computerTestServer(t)
	r := Run{BotID: "synthetic"}
	calls := 0
	vm := Tool{Name: "computer_browser", Description: computerBrowserFallbackNote, Parameters: objectSchema(map[string]any{}, nil), Execute: func(_ context.Context, raw json.RawMessage) (string, error) {
		calls++
		if strings.Contains(string(raw), "computer_id") {
			t.Fatal("VM parser received selection field")
		}
		return "vm-result", nil
	}, Identity: func(raw json.RawMessage) tooloutcome.Identity {
		return tooloutcome.OperationIdentity("synthetic-vm", "browser.read", raw)
	}}
	tool := s.browserTool(r, &vm)
	if len(tool.Description) > 1024 {
		t.Fatalf("browser description %d bytes", len(tool.Description))
	}
	raw := json.RawMessage(`{"action":"browser.read","computer_id":"firecracker"}`)
	out, e := tool.Execute(context.Background(), raw)
	if e != nil || out != "vm-result" || calls != 1 {
		t.Fatalf("VM changed %s %v calls %d", out, e, calls)
	}
	got := tool.Identity(raw)
	want := vm.Identity(json.RawMessage(`{"action":"browser.read"}`))
	if got != want {
		t.Fatalf("VM identity changed %+v %+v", got, want)
	}
}

func TestPersonalBrowserApprovalRejectsPageNavigation(t *testing.T) {
	s, m, h, r := browserFixture(t)
	s.autoReviewProvider = &browserReviewer{onReview: func() { h.mu.Lock(); h.state.Viewport++; h.mu.Unlock() }}
	_, e := s.computerActionOn(context.Background(), r, m.id, "browser.type", browserArgs(h, map[string]any{"text": "synthetic"}))
	if toolCode(e) != "browser_viewport_changed" {
		t.Fatalf("navigation during approval accepted %v", e)
	}
	var jobs int
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM computer_jobs WHERE device_id=?`, m.id).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("action dispatched after page changed")
	}
}

func TestBrowserToolKeepsBotComputerParamsAndDescribesMacOnParams(t *testing.T) {
	s := &Server{}
	vm := Tool{Name: "computer_browser", Description: "Bot computer browser.", Parameters: map[string]any{"properties": map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"navigate", "read"}, "description": "Bot computer action."},
	}}}
	tool := s.browserTool(Run{}, &vm)
	props := tool.Parameters["properties"].(map[string]any)
	action := props["action"].(map[string]any)
	if _, ok := action["enum"]; !ok {
		t.Fatal("the Bot computer's action enum was dropped")
	}
	if d := action["description"].(string); !strings.HasPrefix(d, "Bot computer action.") || !strings.Contains(d, "browser.open") {
		t.Fatalf("action description = %q", d)
	}
	for _, name := range []string{"computer_id", "session_epoch", "target_id", "viewport_epoch", "key", "modifiers"} {
		if d, _ := props[name].(map[string]any)["description"].(string); d == "" {
			t.Errorf("%s has no description", name)
		}
	}
	if !strings.HasPrefix(tool.Description, "Bot computer browser.") || len(tool.Description) > 1024 {
		t.Fatalf("description = %q (%d bytes)", tool.Description, len(tool.Description))
	}
}
