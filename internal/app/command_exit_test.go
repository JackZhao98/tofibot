package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/JackZhao98/tofibot/internal/computer"
)

// scriptedGuest answers terminal.list and terminal.read from a table the test
// edits between polls, and records every action it was sent.
type scriptedGuest struct {
	mu        sync.Mutex
	terminals []map[string]any
	output    map[string][]byte
	actions   []computer.Action
	opened    string
}

func (g *scriptedGuest) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(v any) (*http.Response, error) {
		b, _ := json.Marshal(map[string]any{"ok": true, "result": v})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	}
	if r.Method == http.MethodGet {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"state":"ready"}`))}, nil
	}
	var a computer.Action
	_ = json.NewDecoder(r.Body).Decode(&a)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.actions = append(g.actions, a)
	switch a.Name {
	case "terminal.list":
		return reply(map[string]any{"terminals": g.terminals})
	case "terminal.read":
		var in struct {
			TerminalID string `json:"terminal_id"`
			Cursor     uint64 `json:"cursor"`
		}
		_ = json.Unmarshal(a.Args, &in)
		data := g.output[in.TerminalID]
		from := min(int(in.Cursor), len(data))
		return reply(map[string]any{"data_base64": base64.StdEncoding.EncodeToString(data[from:]), "next_cursor": len(data)})
	case "terminal.open":
		return reply(map[string]any{"id": g.opened, "terminal_id": g.opened})
	}
	return reply(map[string]any{})
}

func (g *scriptedGuest) set(id string, fields map[string]any, output string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.output == nil {
		g.output = map[string][]byte{}
	}
	g.output[id] = []byte(output)
	row := map[string]any{"id": id, "terminal_id": id, "command": "sleep 90", "closed": false, "exited": false, "exit_code": -1, "cursor": len(output)}
	for k, v := range fields {
		row[k] = v
	}
	for i, t := range g.terminals {
		if t["id"] == id {
			g.terminals[i] = row
			return
		}
	}
	g.terminals = append(g.terminals, row)
}

func (g *scriptedGuest) drop(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	kept := g.terminals[:0]
	for _, t := range g.terminals {
		if t["id"] != id {
			kept = append(kept, t)
		}
	}
	g.terminals = kept
}

func (g *scriptedGuest) calls(name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, a := range g.actions {
		if a.Name == name {
			n++
		}
	}
	return n
}

const shellCommand = "# tofi-default-terminal\nexec bash -i"

func (f taskFix) withGuest() *scriptedGuest {
	f.t.Helper()
	g := &scriptedGuest{}
	f.srv.microVM, _ = computer.New(computer.Config{Socket: "/test.sock", Client: &http.Client{Transport: g}})
	return g
}

type cmdReceipt struct {
	WaitID   string `json:"wait_id"`
	GroupID  string `json:"group_id"`
	Mode     string `json:"mode"`
	Watching string `json:"watching"`
	Already  bool   `json:"already_registered"`
	Check    struct {
		WaitID string `json:"wait_id"`
		EndsAt string `json:"ends_at"`
	} `json:"check"`
}

func (f taskFix) waitOnCommand(g *scriptedGuest, r Run, terminal string) cmdReceipt {
	f.t.Helper()
	out, err := call(f.t, f.tool(f.g, r, "wait_for"), map[string]any{"kind": "command_exit", "target": terminal})
	if err != nil {
		f.t.Fatalf("wait_for command_exit: %v", err)
	}
	var rc cmdReceipt
	if json.Unmarshal([]byte(out), &rc) != nil || rc.WaitID == "" {
		f.t.Fatalf("receipt %q", out)
	}
	return rc
}

func (f taskFix) resultBodies() []string {
	f.t.Helper()
	rows, err := f.s.db.Query(`SELECT body FROM bot_inbox WHERE kind='wait_result' ORDER BY created_at`)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		_ = rows.Scan(&b)
		out = append(out, b)
	}
	return out
}

func (f taskFix) poll() { f.srv.pollCommandExits(context.Background()) }

func TestCommandMonitorFulfilsExactlyOnceOnProcessExit(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "building...\n")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "term-1")
	if rc.Mode != waitModeAny || rc.Watching != commandWatchProcess || rc.Check.WaitID == "" {
		t.Fatalf("receipt %+v", rc)
	}
	f.poll()
	if got := f.count(`SELECT COUNT(*) FROM waits WHERE id=? AND status='pending'`, rc.WaitID); got != 1 {
		t.Fatal("a running command ended its wait")
	}
	if g.calls("terminal.read") == 0 {
		t.Fatal("the poll did not cache the output")
	}
	g.set("term-1", map[string]any{"exited": true, "closed": true, "exit_code": 3, "close_reason": "process_exit"}, "building...\nFAILED at step 4\n")
	f.poll()
	f.poll() // the second pass has nothing to do
	bodies := f.resultBodies()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "exit code 3") || !strings.Contains(bodies[0], "FAILED at step 4") {
		t.Fatalf("result bodies %q", bodies)
	}
	if got := f.count(`SELECT COUNT(*) FROM waits WHERE id=? AND status='fulfilled'`, rc.WaitID); got != 1 {
		t.Fatal("the command wait is not fulfilled")
	}
	if got := f.count(`SELECT COUNT(*) FROM waits WHERE id=? AND status='cancelled'`, rc.Check.WaitID); got != 1 {
		t.Fatal("the paired timer was not stood down")
	}
	if got := f.count(`SELECT COUNT(*) FROM messages WHERE kind='notice' AND content LIKE 'Command finished with exit code 3%'`); got != 1 {
		t.Fatalf("notice lines: %d", got)
	}
	if got := f.count(`SELECT COUNT(*) FROM command_waits`); got != 0 {
		t.Fatal("cached state outlived the wait")
	}
	// A second finisher that raced the first (a stale watch) adds nothing.
	w := commandWatch{waitID: rc.WaitID, botID: f.a.ID, conversationID: f.g.ID, terminalID: "term-1"}
	if f.srv.finishCommandWait(w, commandResult{TerminalID: "term-1", Outcome: commandExited}) {
		t.Fatal("a finished wait ended twice")
	}
	if len(f.resultBodies()) != 1 || f.count(`SELECT COUNT(*) FROM messages WHERE kind='notice' AND content LIKE 'Command finished%'`) != 1 {
		t.Fatal("a second result or notice was written")
	}
}

func TestCommandMonitorAsksOnlyBotsThatWait(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "x")
	f.poll()
	if len(g.actions) != 0 {
		t.Fatalf("a Bot with no command wait was polled: %+v", g.actions)
	}
}

func TestCommandMonitorEndsAShellJobWhenTheForegroundReturns(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	shell := map[string]any{"command": shellCommand, "foreground_job": false}
	g.set("sh-1", shell, "$ make\n")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "sh-1")
	if rc.Watching != commandWatchJob {
		t.Fatalf("watching %q", rc.Watching)
	}
	f.poll() // the job has not started yet: the shell at its prompt is not an end
	if len(f.resultBodies()) != 0 {
		t.Fatal("a shell at its prompt before the job started ended the wait")
	}
	g.set("sh-1", map[string]any{"command": shellCommand, "foreground_job": true}, "$ make\ncompiling\n")
	f.poll()
	if len(f.resultBodies()) != 0 {
		t.Fatal("a running job ended the wait")
	}
	g.set("sh-1", shell, "$ make\ncompiling\ndone\n$ ")
	f.poll()
	bodies := f.resultBodies()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "exit code is unknown") || !strings.Contains(bodies[0], "compiling") {
		t.Fatalf("result %q", bodies)
	}
}

func TestOldGuestWithoutForegroundFieldReportsProcessExitOnly(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("sh-1", map[string]any{"command": shellCommand}, "$ ")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "sh-1")
	if rc.Watching != commandWatchProcess {
		t.Fatalf("watching %q", rc.Watching)
	}
	f.poll()
	f.poll()
	if len(f.resultBodies()) != 0 {
		t.Fatal("an old guest's shell ended without the process ending")
	}
	g.set("sh-1", map[string]any{"command": shellCommand, "exited": true, "closed": true, "exit_code": 0}, "$ exit\n")
	f.poll()
	if bodies := f.resultBodies(); len(bodies) != 1 || !strings.Contains(bodies[0], "exit code 0") {
		t.Fatalf("result %q", bodies)
	}
}

func TestRestartDeliversInterruptedWithTheLastOutput(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "step 1 ok\nstep 2 ok\n")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "term-1")
	f.poll() // caches the tail
	g.drop("term-1")
	f.poll()
	bodies := f.resultBodies()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "interrupted by a restart") || !strings.Contains(bodies[0], "step 2 ok") {
		t.Fatalf("result %q", bodies)
	}
	if got := f.count(`SELECT COUNT(*) FROM waits WHERE id=? AND status='fulfilled'`, rc.WaitID); got != 1 {
		t.Fatal("the wait did not end")
	}
	if f.count(`SELECT COUNT(*) FROM messages WHERE kind='notice' AND content LIKE 'Command interrupted%'`) != 1 {
		t.Fatal("no notice line")
	}
}

func TestTheTailCachedByEachPollIsBounded(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, strings.Repeat("a", 5000))
	r := f.running(f.g.ID, f.a)
	f.waitOnCommand(g, r, "term-1")
	f.poll()
	g.set("term-1", nil, strings.Repeat("a", 5000)+strings.Repeat("b", 20000)+"END")
	f.poll()
	var tail string
	if err := f.s.db.QueryRow(`SELECT tail FROM command_waits`).Scan(&tail); err != nil {
		t.Fatal(err)
	}
	if len(tail) > commandTailBytes || !strings.HasSuffix(tail, "bbbEND") || strings.Contains(tail, "a") {
		t.Fatalf("tail len %d", len(tail))
	}
}

func TestACommandWaitAlwaysHasATimerCheck(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "")
	g.set("term-2", nil, "")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "term-1")
	if f.count(`SELECT COUNT(*) FROM waits WHERE group_id=? AND kind='timer' AND status='pending' AND note LIKE 'check on%'`, rc.GroupID) != 1 {
		t.Fatal("the system did not add a timer")
	}
	var deadline, created string
	_ = f.s.db.QueryRow(`SELECT deadline_at,created_at FROM waits WHERE id=?`, rc.Check.WaitID).Scan(&deadline, &created)
	if deadline == "" || deadline <= created {
		t.Fatal("the check has no later deadline")
	}
	// The same terminal twice is one watch.
	if again := f.waitOnCommand(g, r, "term-1"); !again.Already || again.WaitID != rc.WaitID {
		t.Fatalf("second registration %+v", again)
	}
	// The Bot's own timer in the group counts: no second one is added.
	own, err := call(t, f.tool(f.g, r, "wait_for"), map[string]any{"kind": "timer", "seconds": 600, "mode": "any"})
	if err != nil {
		t.Fatal(err)
	}
	var ot cmdReceipt
	_ = json.Unmarshal([]byte(own), &ot)
	out, err := call(t, f.tool(f.g, r, "wait_for"), map[string]any{"kind": "command_exit", "target": "term-2", "group": ot.GroupID})
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	if n := f.count(`SELECT COUNT(*) FROM waits WHERE group_id=? AND kind='timer'`, ot.GroupID); n != 1 {
		t.Fatalf("timers in the Bot's group: %d", n)
	}
	// A group of mode all would make the timer useless.
	allTimer, _ := call(t, f.tool(f.g, r, "wait_for"), map[string]any{"kind": "timer", "seconds": 600, "mode": "all"})
	var at cmdReceipt
	_ = json.Unmarshal([]byte(allTimer), &at)
	g.set("term-3", nil, "")
	if _, err := call(t, f.tool(f.g, r, "wait_for"), map[string]any{"kind": "command_exit", "target": "term-3", "group": at.GroupID}); codeOf(err) != "wait_mode_conflict" {
		t.Fatalf("mode all group: %v", err)
	}
	// The check interval is a setting.
	if f.srv.commandCheckInterval().Minutes() != defaultCommandCheckMinutes {
		t.Fatal("default interval")
	}
}

func TestCommandWaitRefusesWhatIsNotAWatchableTerminal(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("done-1", map[string]any{"exited": true, "closed": true, "exit_code": 2}, "")
	r := f.running(f.g.ID, f.a)
	tool := f.tool(f.g, r, "wait_for")
	if _, err := call(t, tool, map[string]any{"kind": "command_exit", "target": "missing"}); codeOf(err) != "terminal_not_found" {
		t.Fatalf("missing terminal: %v", err)
	}
	if _, err := call(t, tool, map[string]any{"kind": "command_exit", "target": "done-1"}); codeOf(err) != "command_already_ended" {
		t.Fatalf("ended terminal: %v", err)
	}
	if _, err := call(t, tool, map[string]any{"kind": "command_exit"}); codeOf(err) != "wait_target_invalid" {
		t.Fatalf("no target: %v", err)
	}
}

func TestNotifyOnExitRegistersTheWaitAfterTheTerminalOpens(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.opened = "term-9"
	g.set("term-9", map[string]any{"command": "sleep 900"}, "")
	r := f.running(f.g.ID, f.a)
	out, err := call(t, f.srv.terminalTool(r), map[string]any{"action": "open", "command": "sleep 900", "notify_on_exit": true})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Notify struct {
			Registered bool   `json:"registered"`
			WaitID     string `json:"wait_id"`
			Mode       string `json:"mode"`
		} `json:"notify_on_exit"`
	}
	if json.Unmarshal([]byte(out), &res) != nil || !res.Notify.Registered || res.Notify.Mode != waitModeAny {
		t.Fatalf("result %q", out)
	}
	if f.count(`SELECT COUNT(*) FROM waits WHERE kind='command_exit' AND target='term-9' AND status='pending'`) != 1 || f.count(`SELECT COUNT(*) FROM waits WHERE kind='timer' AND status='pending'`) != 1 {
		t.Fatal("the wait and its timer are not both open")
	}
	// The flag never reaches the guest.
	g.mu.Lock()
	for _, a := range g.actions {
		if a.Name == "terminal.open" && strings.Contains(string(a.Args), "notify_on_exit") {
			t.Errorf("guest got %s", a.Args)
		}
	}
	g.mu.Unlock()
	if _, err := call(t, f.srv.terminalTool(r), map[string]any{"action": "read", "terminal_id": "term-9", "notify_on_exit": true}); err == nil {
		t.Fatal("notify_on_exit accepted on read")
	}
}

func TestTerminalWithAPendingCommandWaitIsNotCleanedUp(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "")
	r := f.running(f.g.ID, f.a)
	rc := f.waitOnCommand(g, r, "term-1")
	if _, err := f.s.db.Exec(`UPDATE runs SET status='failed' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.db.Exec(`INSERT INTO run_terminal_cleanup VALUES(?,?,?,?)`, r.ID, r.BotID, "/test.sock", now()); err != nil {
		t.Fatal(err)
	}
	f.srv.reconcileTerminalCleanup(context.Background())
	if g.calls("terminal.cancel_run") != 0 || cleanupCount(t, f.s) != 1 {
		t.Fatal("a terminal with a pending command wait was killed")
	}
	// Cancelling the wait (the Bot, or a Stop) releases it.
	if _, err := call(t, f.tool(f.g, r, "cancel_wait"), map[string]any{"wait_id": rc.WaitID}); err != nil {
		t.Fatal(err)
	}
	f.srv.reconcileTerminalCleanup(context.Background())
	if g.calls("terminal.cancel_run") != 1 || cleanupCount(t, f.s) != 0 {
		t.Fatalf("cleanup after cancel_wait: calls=%d", g.calls("terminal.cancel_run"))
	}
}

func TestBusyEndpointCountsPendingCommandWaits(t *testing.T) {
	f := newTaskFix(t)
	g := f.withGuest()
	g.set("term-1", nil, "")
	r := f.running(f.g.ID, f.a)
	get := func() (int, int) {
		rec := httptest.NewRecorder()
		if !f.srv.routeCommandWaits(rec, httptest.NewRequest("GET", "/api/command-waits", nil), "command-waits") {
			t.Fatal("route not served")
		}
		var out struct {
			Count int `json:"count"`
			Bots  []struct {
				Name string `json:"name"`
			} `json:"bots"`
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("response %d %s", rec.Code, rec.Body.String())
		}
		return out.Count, len(out.Bots)
	}
	if n, _ := get(); n != 0 {
		t.Fatalf("idle count %d", n)
	}
	f.waitOnCommand(g, r, "term-1")
	if n, bots := get(); n != 1 || bots != 1 {
		t.Fatalf("count %d bots %d", n, bots)
	}
}
