package guest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInputValidationRejectsEntireMalformedBatch(t *testing.T) {
	p := func(v int) *int { return &v }
	for _, events := range [][]DesktopInputEvent{
		nil, make([]DesktopInputEvent, 65), {{Type: "move"}}, {{Type: "move", X: p(1280), Y: p(0)}},
		{{Type: "down", Button: 4}}, {{Type: "wheel", DeltaY: 21}}, {{Type: "keydown", Key: "ctrl+a"}},
		{{Type: "keydown", Key: "--window"}}, {{Type: "text", Text: "\x00"}},
		{{Type: "down", Button: 1}, {Type: "invalid"}},
	} {
		if err := validateInputEvents(events); err == nil {
			t.Fatalf("accepted %#v", events)
		}
	}
	if err := validateInputEvents([]DesktopInputEvent{{Type: "move", X: p(0), Y: p(799)}, {Type: "keydown", Key: "Control_L"}, {Type: "text", Text: "中文输入🙂"}, {Type: "reset"}}); err != nil {
		t.Fatal(err)
	}
}

func testInputSession(t *testing.T) (*Service, *inputSession) {
	t.Helper()
	s := newTestService(t)
	d := &desktop{botID: testBot, readyClosed: true, lastActivity: time.Now()}
	c := &inputSession{id: "human-control:test", desktop: d, expires: time.Now().Add(time.Minute), seq: 1, keys: map[string]bool{}, buttons: map[int]bool{}}
	s.inputs = map[string]*inputSession{testBot: c}
	s.holds[testBot] = map[string]time.Time{c.id: c.expires}
	return s, c
}

func TestInputSessionIdentitySequenceAndModelExclusion(t *testing.T) {
	s, c := testInputSession(t)
	call := func(run, action, args string) (any, error) {
		return s.action(context.Background(), ActionRequest{BotID: testBot, RunID: run, Source: "human", Action: action, Args: json.RawMessage(args)})
	}
	if _, err := call(c.id, "desktop.input", `{"seq":1,"events":[{"type":"keydown","key":"a"}]}`); err != nil {
		t.Fatal(err)
	}
	if len(c.keys) != 0 {
		t.Fatal("duplicate batch replayed input")
	}
	if _, err := call(c.id, "desktop.input", `{"seq":3,"events":[{"type":"reset"}]}`); err == nil || !strings.Contains(err.Error(), "input_sequence") {
		t.Fatalf("sequence: %v", err)
	}
	if _, err := call("human-control:wrong", "desktop.input", `{"seq":2,"events":[{"type":"reset"}]}`); err == nil || !strings.Contains(err.Error(), "control_expired") {
		t.Fatalf("identity: %v", err)
	}
	if _, err := s.action(context.Background(), ActionRequest{BotID: testBot, RunID: "model", Source: "model", Action: "desktop.key", Args: json.RawMessage(`{"key":"Return"}`)}); err == nil || !strings.Contains(err.Error(), "computer_busy") {
		t.Fatalf("model exclusion: %v", err)
	}
	if _, err := s.action(context.Background(), ActionRequest{BotID: testBot, RunID: "model", Source: "model", Action: "browser.type_private", Args: json.RawMessage(`{"origin":"https://example.test","text":"fake"}`)}); err == nil || !strings.Contains(err.Error(), "computer_busy") {
		t.Fatalf("private input bypassed human control: %v", err)
	}
	if _, err := call("human-control:wrong", "desktop.release", `{}`); err != nil || s.inputs[testBot] != c {
		t.Fatal("foreign release affected session")
	}
	if _, err := call(c.id, "desktop.release", `{}`); err != nil {
		t.Fatal(err)
	}
	if s.inputs[testBot] != nil || s.hasActiveHoldLocked(testBot, time.Now()) {
		t.Fatal("release retained ownership/hold")
	}
}

func TestTerminalObserversAndCancellationBypassInputGate(t *testing.T) {
	s := newTestService(t)
	term := &terminal{id: "terminal-test", botID: testBot, runID: "run-target", command: "sleep", createdAt: time.Now(), exitCode: -1}
	s.mu.Lock()
	s.terminals[term.id] = term
	s.mu.Unlock()

	gate := s.inputGate(testBot)
	gate.Lock()
	defer gate.Unlock()

	call := func(source, runID, action, args string) (any, error) {
		return s.action(context.Background(), ActionRequest{BotID: testBot, RunID: runID, Source: source, Action: action, Args: json.RawMessage(args)})
	}
	if _, err := call(ActionSourceViewer, "viewer", "terminal.list", `{}`); err != nil {
		t.Fatalf("terminal.list was blocked by input gate: %v", err)
	}
	if _, err := call(ActionSourceViewer, "viewer", "terminal.read", `{"terminal_id":"terminal-test"}`); err != nil {
		t.Fatalf("terminal.read was blocked by input gate: %v", err)
	}
	if _, err := call(ActionSourceModel, "run-target", "terminal.write", `{"terminal_id":"terminal-test","data":"x"}`); err == nil || strings.Contains(err.Error(), "computer_busy") {
		t.Fatalf("terminal.write should remain independent of shared graphics gate: %v", err)
	}
	result, err := call(ActionSourceModel, "run-target", "terminal.cancel_run", `{}`)
	if err != nil {
		t.Fatalf("terminal.cancel_run was blocked by input gate: %v", err)
	}
	if result.(map[string]any)["cancelled"] != 1 {
		t.Fatalf("cancel result=%v", result)
	}
	term.mu.Lock()
	closed, reason := term.closed, term.closeReason
	term.mu.Unlock()
	if !closed || reason != "run_cancelled" {
		t.Fatalf("target terminal closed=%v reason=%q", closed, reason)
	}
}

func TestInputExpirationAndStaleTimerCannotReleaseNewOwner(t *testing.T) {
	s, c := testInputSession(t)
	c.expires = time.Now().Add(-time.Second)
	s.expireInput(c)
	if s.inputs[testBot] != nil {
		t.Fatal("expired session remained")
	}
	next := &inputSession{id: "human-control:next", desktop: c.desktop, expires: time.Now().Add(time.Minute)}
	s.inputs[testBot] = next
	s.expireInput(c)
	if s.inputs[testBot] != next {
		t.Fatal("stale timer released replacement")
	}
}
