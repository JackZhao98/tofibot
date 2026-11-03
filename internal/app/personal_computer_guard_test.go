package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
)

// allowReviewer approves every reviewed action. Test servers install it so that
// tests of the computer plumbing are not about the approval gate.
type allowReviewer struct{}

func (allowReviewer) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: `{"decision":"allow","reason":"test"}`}, nil
}

func (a allowReviewer) ChatStream(ctx context.Context, req *provider.ChatRequest, _ func(provider.StreamDelta)) (*provider.ChatResponse, error) {
	return a.Chat(ctx, req)
}

func personalGuardServer(t *testing.T, decision string) (*Server, *testMac, Run, *reviewStub) {
	t.Helper()
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Studio Mac", "files.read", "files.write", "sandbox.exec", "desktop.click", "desktop.type", "desktop.key", "screen.capture", "desktop.capture", "desktop.move", "desktop.drag", "desktop.scroll", "desktop.double_click", "desktop.triple_click")
	run := activeComputerRun(t, s)
	stub := &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return &provider.ChatResponse{Content: `{"decision":"` + decision + `","reason":"stub"}`}, nil
	}}
	s.autoReviewProvider = stub
	return s, mac, run, stub
}

// A consequential desktop action or write on a Mac raises the approval, and
// nothing is queued for the Mac.
func TestPersonalGuardBlocksConsequentialActions(t *testing.T) {
	s, mac, run, stub := personalGuardServer(t, "confirm")
	tool := computerActionTool(s, run)
	for _, c := range []struct{ action, args string }{
		{"desktop.click", `{"x":10,"y":10,"effect":"purchase"}`},
		{"desktop.type", `{"text":"hello","effect":"send"}`},
		{"desktop.key", `{"key":"Return","effect":"submit"}`},
		{"files.write", `{"path":"a","content":"x"}`},
		{"shell.exec", `{"command":"rm -rf x"}`},
	} {
		_, err := runCall(tool, mac.id, c.action, c.args)
		if toolCode(err) != "user_confirmation_required" {
			t.Fatalf("%s: want approval, got %v", c.action, err)
		}
	}
	if stub.calls.Load() != 5 {
		t.Fatalf("reviewer calls=%d", stub.calls.Load())
	}
	if w := mac.call("GET", "/api/computers/"+mac.id+"/jobs?wait=0", "", mac.token); strings.Contains(w.Body.String(), `"job":{`) {
		t.Fatalf("a refused action was queued: %s", w.Body.String())
	}
}

// Fail closed on the person's own computer: input that declares no effect is
// reviewed as consequential, in every input action except move.
func TestPersonalGuardUndeclaredInputIsConsequential(t *testing.T) {
	s, mac, run, stub := personalGuardServer(t, "confirm")
	tool := computerActionTool(s, run)
	cases := []struct{ action, args string }{
		{"desktop.click", `{"x":10,"y":10}`},
		{"desktop.double_click", `{"x":10,"y":10}`},
		{"desktop.triple_click", `{"x":10,"y":10}`},
		{"desktop.drag", `{"from":{"x":1,"y":1},"to":{"x":9,"y":9}}`},
		{"desktop.scroll", `{"direction":"down","amount":3}`},
		{"desktop.type", `{"text":"hello"}`},
		{"desktop.key", `{"key":"ctrl+shift+t"}`},
		{"desktop.click", `{"x":10,"y":10,"effect":""}`},
	}
	for _, c := range cases {
		_, err := runCall(tool, mac.id, c.action, c.args)
		if toolCode(err) != "user_confirmation_required" {
			t.Fatalf("%s %s: want approval, got %v", c.action, c.args, err)
		}
	}
	if int(stub.calls.Load()) != len(cases) {
		t.Fatalf("reviewer calls=%d want %d", stub.calls.Load(), len(cases))
	}
	if w := mac.call("GET", "/api/computers/"+mac.id+"/jobs?wait=0", "", mac.token); strings.Contains(w.Body.String(), `"job":{`) {
		t.Fatalf("a refused action was queued: %s", w.Body.String())
	}
}

// Reads and no-effect input stay ungated and reach the Mac.
func TestPersonalGuardLeavesReadsAndNoEffectInputAlone(t *testing.T) {
	s, mac, run, stub := personalGuardServer(t, "confirm")
	tool := computerActionTool(s, run)
	for _, c := range []struct{ action, args, result string }{
		{"screen.capture", `{}`, `{"image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}`},
		{"files.read", `{"path":"a"}`, `{"ok":true}`},
		{"desktop.click", `{"x":1,"y":1,"button":"left","effect":"none"}`, `{"ok":true}`},
		{"desktop.capture", `{}`, `{"image_url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="}`},
		{"desktop.move", `{"x":5,"y":5}`, `{"ok":true}`},
		{"desktop.double_click", `{"x":1,"y":1,"effect":"none"}`, `{"ok":true}`},
		{"desktop.drag", `{"from":{"x":1,"y":1},"to":{"x":2,"y":2},"effect":"none"}`, `{"ok":true}`},
		{"desktop.scroll", `{"direction":"up","effect":"none"}`, `{"ok":true}`},
		{"desktop.type", `{"text":"x","effect":"none"}`, `{"ok":true}`},
		{"desktop.key", `{"key":"Return","effect":"none"}`, `{"ok":true}`},
	} {
		done := make(chan error, 1)
		go func() { _, err := runCall(tool, mac.id, c.action, c.args); done <- err }()
		job := mac.poll(5)
		want := c.action
		if want == "screen.capture" {
			want = "desktop.capture" // the old name is an alias of the new one
		}
		if job == nil || job.Action != want {
			t.Fatalf("%s: job %+v", c.action, job)
		}
		mac.answer(job, c.result)
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", c.action, err)
		}
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("reviewer called %d times for ungated actions", stub.calls.Load())
	}
}

// When the reviewer allows, the action runs.
func TestPersonalGuardAllowedActionRuns(t *testing.T) {
	s, mac, run, stub := personalGuardServer(t, "allow")
	tool := computerActionTool(s, run)
	done := make(chan error, 1)
	go func() {
		_, err := runCall(tool, mac.id, "desktop.click", `{"x":1,"y":1,"button":"left","effect":"submit"}`)
		done <- err
	}()
	job := mac.poll(5)
	if job == nil || job.Action != "desktop.click" {
		select {
		case err := <-done:
			t.Fatalf("no job; call returned %v", err)
		default:
		}
		t.Fatalf("job %+v", job)
	}
	mac.answer(job, `{"ok":true}`)
	if err := <-done; err != nil || stub.calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, stub.calls.Load())
	}
}

// A device that has not polled lately is offline in both lists, sorts after
// online ones, and a call aimed at it names the online alternative.
func TestPersonalStaleDeviceIsOffline(t *testing.T) {
	s := computerTestServer(t)
	stale := enrollMac(t, s, "Old Mac", "files.read")
	fresh := enrollMac(t, s, "New Mac", "files.read")
	old := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	if _, err := s.store.db.Exec(`UPDATE computers SET last_seen=? WHERE id=?`, old, stale.id); err != nil {
		t.Fatal(err)
	}
	run := activeComputerRun(t, s)
	var list Tool
	for _, tool := range s.computerTools(run) {
		if tool.Name == "list_computers" {
			list = tool
		}
	}
	out, err := list.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Computers []Computer `json:"computers"`
	}
	_ = json.Unmarshal([]byte(out), &parsed)
	pos := map[string]int{}
	by := map[string]Computer{}
	for i, c := range parsed.Computers {
		pos[c.ID], by[c.ID] = i, c
	}
	if by[stale.id].Online || !by[fresh.id].Online || pos[fresh.id] > pos[stale.id] {
		t.Fatalf("list: %s", out)
	}
	for _, c := range s.botComputers(context.Background()) {
		if c.ID == stale.id && c.Online {
			t.Fatal("botComputers reports a silent device online")
		}
	}
	_, err = runCall(computerActionTool(s, run), stale.id, "files.read", `{"path":"a"}`)
	if toolCode(err) != "computer_offline" || !strings.Contains(err.Error(), "offline") || !strings.Contains(err.Error(), fresh.id) {
		t.Fatalf("offline error: %v", err)
	}
}
