package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/provider"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const tinyPNGURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

// fail posts a refusal for a claimed job, as the desktop app does.
func (m *testMac) fail(job *ComputerJob, text string) {
	m.t.Helper()
	body, _ := json.Marshal(map[string]any{"error": text})
	if w := m.call("POST", "/api/computers/"+m.id+"/jobs/"+job.ID+"/result", string(body), m.token); w.Code != 200 {
		m.t.Fatalf("fail: %d %s", w.Code, w.Body.String())
	}
}

func TestPersonalDesktopArgsAndResults(t *testing.T) {
	ok := []struct{ action, args string }{
		{"desktop.capture", `{}`},
		{"desktop.capture", `{"display":1,"detail":"full"}`},
		{"screen.capture", `{}`},
		{"desktop.click", `{"x":3,"y":4,"button":"right","clicks":2,"screenshot_width":1280,"screenshot_height":800}`},
		{"desktop.click", `{"x":3,"y":4,"button":2}`},
		{"desktop.click", `{"x":3,"y":4,"button":"middle","effect":"none"}`},
		{"desktop.double_click", `{"x":3,"y":4}`},
		{"desktop.triple_click", `{"x":3,"y":4}`},
		{"desktop.move", `{"x":3,"y":4}`},
		{"desktop.drag", `{"from":{"x":1,"y":2},"to":{"x":3,"y":4},"button":"left"}`},
		{"desktop.scroll", `{"direction":"down","amount":5,"x":10,"y":10}`},
		{"desktop.scroll", `{"direction":"left"}`},
		{"desktop.type", `{"text":"hi"}`},
		{"desktop.key", `{"key":"ctrl+shift+t"}`},
		{"desktop.key", `{"key":"Return","modifiers":["cmd"]}`},
	}
	for _, c := range ok {
		if err := validateComputerArgs(c.action, json.RawMessage(c.args)); err != nil {
			t.Errorf("%s %s: %v", c.action, c.args, err)
		}
	}
	bad := []struct{ action, args string }{
		{"desktop.capture", `{"display":99}`},
		{"desktop.capture", `{"x":1}`},
		{"desktop.click", `{"x":3}`},
		{"desktop.click", `{"x":3,"y":4,"button":"back"}`},
		{"desktop.click", `{"x":3,"y":4,"button":4}`},
		{"desktop.click", `{"x":3,"y":4,"clicks":4}`},
		{"desktop.click", `{"x":3,"y":4,"screenshot_width":100}`},
		{"desktop.double_click", `{"x":-1,"y":4}`},
		{"desktop.move", `{"x":3,"y":4,"button":"left"}`},
		{"desktop.drag", `{"from":{"x":1,"y":2}}`},
		{"desktop.scroll", `{"direction":"sideways"}`},
		{"desktop.scroll", `{"direction":"up","amount":21}`},
		{"desktop.scroll", `{"direction":"up","x":1}`},
	}
	for _, c := range bad {
		if err := validateComputerArgs(c.action, json.RawMessage(c.args)); err == nil {
			t.Errorf("%s %s: accepted", c.action, c.args)
		}
	}

	// A desktop.capture result gets the same image checks as screen.capture.
	for _, action := range []string{"screen.capture", "desktop.capture"} {
		if err := validateComputerResult(action, json.RawMessage(`{"image_url":"`+tinyPNGURL+`","width":1,"height":1,"screen_width":1,"screen_height":1,"display":{"id":1},"displays":[],"cursor":{"x":0,"y":0}}`)); err != nil {
			t.Errorf("%s: %v", action, err)
		}
		for _, raw := range []string{`{"ok":true}`, `{"image_url":"data:text/plain;base64,AAAA"}`, `{"image_url":"data:image/png;base64,AAAA"}`} {
			if err := validateComputerResult(action, json.RawMessage(raw)); err == nil {
				t.Errorf("%s accepted %s", action, raw)
			}
		}
	}
	// The limits stay: 1 MiB of result, 768 KiB of picture.
	big := `{"pad":"` + strings.Repeat("a", maxComputerResult) + `"}`
	if validateComputerResult("desktop.click", json.RawMessage(big)) == nil {
		t.Error("an oversized result was accepted")
	}
	huge := `{"image_url":"data:image/png;base64,` + strings.Repeat("A", 800<<10) + `"}`
	if validateComputerResult("desktop.capture", json.RawMessage(huge)) == nil {
		t.Error("an oversized capture was accepted")
	}
	if validateComputerArgs("desktop.type", json.RawMessage(`{"text":"`+strings.Repeat("a", maxComputerResult)+`"}`)) == nil {
		t.Error("oversized args were accepted")
	}
}

// Both capture names work against either kind of client, and a click grant
// covers the other pointer actions.
func TestPersonalCapabilityAliasesBothWays(t *testing.T) {
	s := computerTestServer(t)
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)
	expect := func(mac *testMac, action, args, wantJob string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := runCall(tool, mac.id, action, args); done <- err }()
		job := mac.poll(5)
		if job == nil || job.Action != wantJob {
			t.Fatalf("%s: job %+v want %s", action, job, wantJob)
		}
		mac.answer(job, `{"ok":true}`)
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	old := enrollMac(t, s, "Old client", "screen.capture", "desktop.click")
	cur := enrollMac(t, s, "New client", "desktop.capture")
	capture := func(mac *testMac, action, wantJob string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := runCall(tool, mac.id, action, `{}`); done <- err }()
		job := mac.poll(5)
		if job == nil || job.Action != wantJob {
			t.Fatalf("%s: job %+v want %s", action, job, wantJob)
		}
		mac.answer(job, `{"image_url":"`+tinyPNGURL+`"}`)
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	capture(old, "desktop.capture", "screen.capture")
	capture(old, "screen.capture", "screen.capture")
	capture(cur, "screen.capture", "desktop.capture")
	capture(cur, "desktop.capture", "desktop.capture")
	for _, a := range []string{"desktop.double_click", "desktop.triple_click", "desktop.move", "desktop.scroll"} {
		args := `{"x":1,"y":1,"effect":"none"}`
		if a == "desktop.scroll" {
			args = `{"direction":"down","effect":"none"}`
		}
		expect(old, a, args, a)
	}
	// Neither grant covers typing.
	if _, err := runCall(tool, old.id, "desktop.type", `{"text":"x","effect":"none"}`); toolCode(err) != "capability_not_granted" {
		t.Fatalf("type without a grant: %v", err)
	}
	// A capture whose picture is not an image is refused by the server.
	done := make(chan error, 1)
	go func() { _, err := runCall(tool, cur.id, "desktop.capture", `{}`); done <- err }()
	job := cur.poll(5)
	body, _ := json.Marshal(map[string]any{"result": map[string]any{"image_url": "data:image/png;base64,AAAA"}})
	if w := cur.call("POST", "/api/computers/"+cur.id+"/jobs/"+job.ID+"/result", string(body), cur.token); w.Code == 200 {
		t.Fatal("a broken desktop.capture result was accepted")
	}
	cur.fail(job, "capture failed")
	<-done
}

// Dropping a grant cancels the jobs it covered, including through the alias.
func TestPersonalCapabilityUpdateKeepsAliasedJobs(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Mac", "screen.capture", "desktop.click")
	run := activeComputerRun(t, s)
	job, err := s.store.queueComputerJob(run, mac.id, "desktop.capture", json.RawMessage(`{}`))
	if err != nil || job.Action != "screen.capture" {
		t.Fatalf("job %+v err %v", job, err)
	}
	click, err := s.store.queueComputerJob(run, mac.id, "desktop.double_click", json.RawMessage(`{"x":1,"y":1}`))
	if err != nil {
		t.Fatal(err)
	}
	// Re-declare with the new capture name only: the capture job survives, the click is cancelled.
	if _, err = s.store.updateComputerCapabilities(mac.id, mac.token, []string{"desktop.capture"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.store.computerJob(job.ID, mac.id); got.Status != "pending" {
		t.Fatalf("capture job: %s", got.Status)
	}
	if got, _ := s.store.computerJob(click.ID, mac.id); got.Status != "cancelled" {
		t.Fatalf("click job: %s", got.Status)
	}
}

// The Mac's refusals reach the Bot as named outcomes it must not retry.
func TestPersonalDeviceErrorsBecomeClearOutcomes(t *testing.T) {
	s := computerTestServer(t)
	mac := enrollMac(t, s, "Studio Mac", "desktop.capture", "desktop.click", "desktop.type", "files.read")
	run := activeComputerRun(t, s)
	tool := computerActionTool(s, run)
	cases := []struct {
		action, args, device, code, certainty string
		words                                 []string
	}{
		{"desktop.click", `{"x":1,"y":1,"effect":"none"}`, "bot_not_allowed", "bot_not_allowed", "not_executed", []string{"Bots denied on this Mac", "Do not retry"}},
		{"desktop.click", `{"x":1,"y":1,"effect":"none"}`, "user_stop", "user_stop", "unknown", []string{"pressed Stop", "Do not retry automatically"}},
		{"desktop.click", `{"x":1,"y":1,"effect":"none"}`, "user_paused", "user_paused", "not_executed", []string{"paused", "Do not retry automatically"}},
		{"desktop.click", `{"x":1,"y":1,"effect":"none"}`, "user_takeover: mouse moved", "user_takeover", "not_executed", []string{"took over", "Do not retry automatically"}},
		{"desktop.type", `{"text":"hunter2","effect":"none"}`, "secure_input", "secure_input", "not_executed", []string{"secure text field", "Do not type secrets"}},
		{"desktop.click", `{"x":1,"y":1,"effect":"none"}`, "capability_not_granted", "capability_not_granted", "not_executed", []string{"desktop.click", "Accessibility", "Mac Device Settings"}},
		{"desktop.capture", `{}`, "capability_not_granted", "capability_not_granted", "not_executed", []string{"desktop.capture", "Screen Recording"}},
	}
	for _, c := range cases {
		done := make(chan error, 1)
		go func() { _, err := runCall(tool, mac.id, c.action, c.args); done <- err }()
		job := mac.poll(5)
		if job == nil {
			t.Fatalf("%s/%s: no job", c.action, c.device)
		}
		mac.fail(job, c.device)
		err := <-done
		o, isOutcome := tooloutcome.FromError(err)
		if !isOutcome || o.Code != c.code || o.Certainty != c.certainty || o.Status != tooloutcome.Denied || o.NextAction != "explain_blocker" {
			t.Fatalf("%s: outcome %+v err %v", c.device, o, err)
		}
		for _, w := range c.words {
			if !strings.Contains(o.Message, w) {
				t.Errorf("%s: message %q lacks %q", c.device, o.Message, w)
			}
		}
		if strings.Contains(o.Message, "mouse moved") {
			t.Errorf("%s: the Mac's own text leaked: %q", c.device, o.Message)
		}
	}
	// The server's own check names the capability too.
	_, err := runCall(tool, mac.id, "desktop.key", `{"key":"Return","effect":"none"}`)
	if o, _ := tooloutcome.FromError(err); o.Code != "capability_not_granted" || !strings.Contains(o.Message, "desktop.key") {
		t.Fatalf("server-side grant check: %v", err)
	}
}

// computerDesktopServer is a server with a Bot computer attached and a paired Mac.
func computerDesktopServer(t *testing.T, decision string) (*Server, *captureComputer, *testMac, Run, *reviewStub) {
	t.Helper()
	s, vm, run := captureServer(t)
	if err := migrateComputers(s.store.db); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate run ok=%v err=%v", ok, err)
	}
	run.Status = "running"
	mac := enrollMac(t, s, "Studio Mac", "files.read", "desktop.capture", "desktop.click", "desktop.type", "desktop.key")
	stub := &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return &provider.ChatResponse{Content: `{"decision":"` + decision + `","reason":"stub"}`}, nil
	}}
	s.autoReviewProvider = stub
	return s, vm, mac, run, stub
}

func desktopTool(t *testing.T, s *Server, run Run) Tool {
	t.Helper()
	tool, ok := toolNamed(s.microVMTools(run), "computer_desktop")
	if !ok {
		t.Fatal("no computer_desktop")
	}
	return tool
}

func runDesktop(tool Tool, args string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tool.Execute(ctx, json.RawMessage(args))
}

// One Bot tool set: computer_desktop with computer_id drives a paired Mac in its
// own vocabulary, and without computer_id it is the Bot computer as before.
func TestComputerDesktopRoutesToAPersonalComputer(t *testing.T) {
	s, vm, mac, run, stub := computerDesktopServer(t, "confirm")
	tool := desktopTool(t, s, run)

	// The description and the schema say what each kind does.
	for _, want := range []string{"computer_id", "Mac"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	props := tool.Parameters["properties"].(map[string]any)
	for _, k := range []string{"computer_id", "button", "to_x", "to_y", "display"} {
		if _, ok := props[k]; !ok {
			t.Errorf("schema lacks %s", k)
		}
	}
	if d := props["computer_id"].(map[string]any)["description"].(string); !strings.Contains(d, "capture") || !strings.Contains(d, "no start, stop or trace") || !strings.Contains(d, "move, drag") || !strings.Contains(d, "needs the person's approval") {
		t.Errorf("computer_id description: %s", d)
	}

	send := func(args string, answer string) (string, *ComputerJob, error) {
		t.Helper()
		type res struct {
			out string
			err error
		}
		done := make(chan res, 1)
		go func() { out, err := runDesktop(tool, args); done <- res{out, err} }()
		job := mac.poll(2)
		if job == nil {
			r := <-done
			return r.out, nil, r.err
		}
		mac.answer(job, answer)
		r := <-done
		return r.out, job, r.err
	}

	// capture needs no review and keeps the Mac's fields.
	out, job, err := send(`{"computer_id":"`+mac.id+`","action":"capture"}`, `{"image_url":"`+tinyPNGURL+`","width":1,"height":1,"screen_width":1,"screen_height":1,"display":1}`)
	if err != nil || job == nil || job.Action != "desktop.capture" || !strings.Contains(out, "image_url") || !strings.Contains(out, "screen_width") {
		t.Fatalf("capture: %v job=%+v out=%s", err, job, out)
	}
	if stub.calls.Load() != 0 {
		t.Fatal("capture was reviewed")
	}

	// Input without an effect is gated and never reaches the Mac.
	for _, a := range []string{
		`{"computer_id":"` + mac.id + `","action":"click","x":5,"y":6}`,
		`{"computer_id":"` + mac.id + `","action":"double_click","x":5,"y":6}`,
		`{"computer_id":"` + mac.id + `","action":"drag","x":1,"y":2,"to_x":3,"to_y":4}`,
		`{"computer_id":"` + mac.id + `","action":"type","text":"hello"}`,
		`{"computer_id":"` + mac.id + `","action":"key","key":"Return"}`,
	} {
		_, job, err = send(a, `{"ok":true}`)
		if job != nil || toolCode(err) != "user_confirmation_required" {
			t.Fatalf("%s: job=%+v err=%v", a, job, err)
		}
	}

	// A declared effect of none skips the review; the Mac gets its own vocabulary and no effect field.
	_, job, err = send(`{"computer_id":"`+mac.id+`","action":"click","x":5,"y":6,"button":"right","effect":"none","screenshot_width":1280,"screenshot_height":800}`, `{"ok":true}`)
	if err != nil || job == nil || job.Action != "desktop.click" {
		t.Fatalf("click: %v %+v", err, job)
	}
	var got map[string]any
	_ = json.Unmarshal(job.Args, &got)
	if got["x"] != float64(5) || got["y"] != float64(6) || got["button"] != "right" || got["screenshot_width"] != float64(1280) {
		t.Fatalf("click args: %s", job.Args)
	}
	if _, has := got["effect"]; has {
		t.Fatalf("effect reached the Mac: %s", job.Args)
	}

	// move is never reviewed, even with no effect; a click grant covers it.
	_, job, err = send(`{"computer_id":"`+mac.id+`","action":"move","x":1,"y":1}`, `{"ok":true}`)
	if err != nil || job == nil || job.Action != "desktop.move" {
		t.Fatalf("move: %v %+v", err, job)
	}

	// Actions of the Bot computer only.
	for _, a := range []string{"start", "stop", "trace"} {
		if _, err = runDesktop(tool, `{"computer_id":"`+mac.id+`","action":"`+a+`"}`); toolCode(err) != "invalid_arguments" {
			t.Errorf("%s on a Mac: %v", a, err)
		}
	}
	if _, err = runDesktop(tool, `{"computer_id":"`+mac.id+`","action":"drag","x":1,"y":2}`); toolCode(err) != "invalid_arguments" {
		t.Errorf("drag without an end: %v", err)
	}
	if _, err = runDesktop(tool, `{"computer_id":"nonexistent","action":"capture"}`); toolCode(err) != "invalid_arguments" {
		t.Errorf("unknown computer: %v", err)
	}
	if stub.calls.Load() != 5 {
		t.Fatalf("reviewer calls=%d", stub.calls.Load())
	}

	// Without computer_id nothing changed: the call goes to the Bot computer.
	before := vm.count("desktop.capture")
	if _, err = runDesktop(tool, `{"action":"desktop.capture"}`); err != nil {
		t.Fatal(err)
	}
	if vm.count("desktop.capture") != before+1 {
		t.Fatal("the default call did not reach the Bot computer")
	}
	// Mac-only options and actions are refused on the Bot computer instead of being dropped.
	for _, a := range []string{`{"action":"move","x":1,"y":1}`, `{"action":"drag","x":1,"y":1,"to_x":2,"to_y":2}`, `{"action":"click","x":1,"y":1,"button":"right"}`} {
		if _, err = runDesktop(tool, a); toolCode(err) != "invalid_arguments" {
			t.Errorf("%s on the Bot computer: %v", a, err)
		}
	}
	// Naming the Bot computer's own id is the default.
	if _, err = runDesktop(tool, `{"computer_id":"`+vm.ID()+`","action":"desktop.capture"}`); err != nil {
		t.Fatalf("explicit Bot computer: %v", err)
	}
	// computer_action still serves files on the Mac.
	files := make(chan error, 1)
	go func() { _, e := runCall(computerActionTool(s, run), mac.id, "files.read", `{"path":"a"}`); files <- e }()
	j := mac.poll(5)
	if j == nil || j.Action != "files.read" {
		t.Fatalf("files.read job %+v", j)
	}
	mac.answer(j, `{"ok":true}`)
	if e := <-files; e != nil {
		t.Fatal(e)
	}
}

// A person's approval of a gated desktop action lets exactly that action through.
func TestComputerDesktopAllowedByReviewerRuns(t *testing.T) {
	s, _, mac, run, stub := computerDesktopServer(t, "allow")
	tool := desktopTool(t, s, run)
	done := make(chan error, 1)
	go func() {
		_, err := runDesktop(tool, `{"computer_id":"`+mac.id+`","action":"type","text":"hello","effect":"send"}`)
		done <- err
	}()
	job := mac.poll(5)
	if job == nil || job.Action != "desktop.type" {
		t.Fatalf("job %+v", job)
	}
	mac.answer(job, `{"ok":true}`)
	if err := <-done; err != nil || stub.calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, stub.calls.Load())
	}
}

// The desktop tool follows what the account has: VM only, Mac only, or both.
func TestComputerDesktopToolByComputerKind(t *testing.T) {
	// VM only: the tool is there and goes to the Bot computer.
	s, vm, run := captureServer(t)
	if _, ok := toolNamed(s.microVMTools(run), "computer_desktop"); !ok {
		t.Fatal("VM only: no computer_desktop")
	}
	before := vm.count("desktop.capture")
	if _, err := runDesktop(desktopTool(t, s, run), `{"action":"capture"}`); err != nil || vm.count("desktop.capture") != before+1 {
		t.Fatalf("VM only: %v", err)
	}

	// Both: with no computer_id the Bot computer is the default; computer_id picks the Mac.
	s, vm, mac, run, _ := computerDesktopServer(t, "allow")
	tool := desktopTool(t, s, run)
	before = vm.count("desktop.capture")
	if _, err := runDesktop(tool, `{"action":"capture"}`); err != nil || vm.count("desktop.capture") != before+1 {
		t.Fatalf("both, default: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, err := runDesktop(tool, `{"computer_id":"`+mac.id+`","action":"capture"}`); done <- err }()
	if job := mac.poll(5); job == nil || job.Action != "desktop.capture" {
		t.Fatalf("both, Mac: %+v", job)
	} else {
		mac.answer(job, `{"image_url":"`+tinyPNGURL+`"}`)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Mac only (no VM, no KVM): the account still gets computer_desktop.
	s = computerTestServer(t)
	run = activeComputerRun(t, s)
	if tools := s.microVMTools(run); len(tools) != 0 {
		t.Fatalf("no computers: %d tools", len(tools))
	}
	filesOnly := enrollMac(t, s, "Files only", "files.read")
	if tools := s.microVMTools(run); len(tools) != 0 {
		t.Fatalf("a Mac without desktop control got %d tools", len(tools))
	}
	only := enrollMac(t, s, "Studio Mac", "desktop.capture", "desktop.click", "files.read")
	tools := s.microVMTools(run)
	if len(tools) != 1 || tools[0].Name != "computer_desktop" || len(tools[0].Description) > 1024 {
		t.Fatalf("Mac only: %+v", tools)
	}
	tool = tools[0]
	// With exactly one Mac that has desktop control, no computer_id means that Mac.
	go func() { _, err := runDesktop(tool, `{"action":"capture"}`); done <- err }()
	job := only.poll(5)
	if job == nil || job.Action != "desktop.capture" {
		t.Fatalf("Mac only, default: %+v", job)
	}
	only.answer(job, `{"image_url":"`+tinyPNGURL+`"}`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Input there is still gated when it declares nothing.
	s.autoReviewProvider = &reviewStub{t: t, reply: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		return &provider.ChatResponse{Content: `{"decision":"confirm","reason":"stub"}`}, nil
	}}
	if _, err := runDesktop(tool, `{"action":"click","x":1,"y":1}`); toolCode(err) != "user_confirmation_required" {
		t.Fatalf("Mac only, undeclared click: %v", err)
	}
	// A second Mac with desktop control: the Bot must say which.
	second := enrollMac(t, s, "Laptop", "desktop.capture")
	_, err := runDesktop(tool, `{"action":"capture"}`)
	if toolCode(err) != "invalid_arguments" || !strings.Contains(err.Error(), only.id) || !strings.Contains(err.Error(), second.id) || strings.Contains(err.Error(), filesOnly.id) {
		t.Fatalf("two Macs: %v", err)
	}
	go func() { _, e := runDesktop(tool, `{"computer_id":"`+second.id+`","action":"capture"}`); done <- e }()
	if job = second.poll(5); job == nil {
		t.Fatal("named Mac got no job")
	}
	second.answer(job, `{"image_url":"`+tinyPNGURL+`"}`)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPersonalDesktopKeyModifierAliases(t *testing.T) {
	action, args, err := personalDesktopArgs(desktopToolInput{Action: "desktop.key", Key: "q", Modifiers: []string{"command", "Option", " control ", "shift"}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Modifiers []string }
	_ = json.Unmarshal(args, &got)
	if strings.Join(got.Modifiers, ",") != "cmd,alt,ctrl,shift" {
		t.Fatalf("modifiers = %v", got.Modifiers)
	}
	if err := validateComputerArgs(action, args); err != nil {
		t.Fatalf("aliased modifiers rejected: %v", err)
	}
	_, args, _ = personalDesktopArgs(desktopToolInput{Action: "desktop.key", Key: "q", Modifiers: []string{"hyper"}})
	if err := validateComputerArgs("desktop.key", args); err == nil || !strings.Contains(err.Error(), "cmd, ctrl, alt or shift") {
		t.Fatalf("unknown modifier error = %v", err)
	}
}

func TestKeyChord(t *testing.T) {
	for _, c := range []struct {
		key  string
		mods []string
		want string
	}{
		{"q", []string{"command"}, "cmd+q"},
		{"cmd+shift+t", nil, "cmd+shift+t"},
		{"Shift+Cmd+Q", []string{"cmd"}, "cmd+shift+Q"},
		{"ctrl++", nil, "ctrl++"},
		{"+", nil, "+"},
		{"a+b", nil, "a+b"},
		{"Return", []string{"shift", "Option"}, "alt+shift+Return"},
		{"t", []string{"hyper"}, "hyper+t"},
	} {
		mods, key := keyChord(c.key, c.mods)
		if got := strings.Join(append(mods, key), "+"); got != c.want {
			t.Errorf("keyChord(%q, %v) = %q, want %q", c.key, c.mods, got, c.want)
		}
	}
}

func TestSharedCaptureBecomesAPublishedAttachmentOfTheRun(t *testing.T) {
	s, _, run := captureServer(t)
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("running %v %v", ok, err)
	}
	out, err := s.shareCapture(run, `{"image_url":"`+tinyPNGURL+`","width":1,"height":1}`)
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Shared struct {
			ID    string   `json:"attachment_id"`
			Name  string   `json:"name"`
			Reply []string `json:"attached_to_reply"`
		} `json:"shared"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || res.Shared.ID == "" || len(res.Shared.Reply) != 1 || res.Shared.Reply[0] != res.Shared.Name {
		t.Fatalf("result %s", out)
	}
	a, _, err := s.store.Attachment(res.Shared.ID)
	if err != nil || a.ConversationID != run.ConversationID {
		t.Fatalf("attachment %+v %v", a, err)
	}
	if _, err := s.shareCapture(run, `{"ok":true}`); err == nil {
		t.Fatal("a capture without a picture was shared")
	}
}

func TestShareIsForCaptureOnAPersonalComputer(t *testing.T) {
	s, _, _ := captureServer(t)
	for _, raw := range []string{`{"action":"desktop.click","x":1,"y":2,"share":true}`, `{"action":"desktop.capture","share":true}`} {
		if _, _, err := s.parseDesktopTool(json.RawMessage(raw)); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if _, _, err := s.parseDesktopTool(json.RawMessage(`{"action":"desktop.capture","share":true,"computer_id":"x"}`)); err != nil && strings.Contains(err.Error(), "share applies") {
		t.Errorf("refused a personal capture: %v", err)
	}
}
