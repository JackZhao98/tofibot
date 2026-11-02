package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, raw string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(raw), into); err != nil {
		t.Fatalf("json %q: %v", raw, err)
	}
}

type contractReceipt struct {
	Tasks []struct {
		TaskID     string `json:"task_id"`
		BotID      string `json:"bot_id"`
		Status     string `json:"status"`
		RunID      string `json:"run_id"`
		DeadlineAt string `json:"deadline_at"`
		Runs       string `json:"runs"`
	} `json:"tasks"`
}

func (f taskFix) delegateArgs(c Conversation, run Run, args map[string]any) contractReceipt {
	f.t.Helper()
	out, err := call(f.t, f.tool(c, run, "delegate"), args)
	if err != nil {
		f.t.Fatalf("delegate: %v", err)
	}
	var receipt contractReceipt
	if err = json.Unmarshal([]byte(out), &receipt); err != nil {
		f.t.Fatalf("receipt %q: %v", out, err)
	}
	return receipt
}

func (f taskFix) waitDeadline(taskID string) time.Time {
	f.t.Helper()
	var at string
	if err := f.s.db.QueryRow(`SELECT deadline_at FROM waits WHERE kind='task_result' AND target=? AND status='pending'`, taskID).Scan(&at); err != nil {
		f.t.Fatalf("wait of %s: %v", taskID, err)
	}
	parsed, err := time.Parse(storedTimeLayout, at)
	if err != nil {
		f.t.Fatal(err)
	}
	return parsed
}

// The first delegation of a run is a direct hand-over: the contract is stored on
// the task, and the giver's wait ends at the deadline instead of the 2 h default.
func TestDelegateContractIsStoredOnTheDirectHandoff(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.b.ID, "task": "Collect three prices", "result_format": "A table with name and price", "limits": "Use two sites at most", "deadline_seconds": 600})
	if len(r.Tasks) != 1 || r.Tasks[0].DeadlineAt == "" || r.Tasks[0].RunID == "" {
		t.Fatalf("receipt %+v", r)
	}
	row := f.taskRow(r.Tasks[0].TaskID)
	if row.ResultFormat != "A table with name and price" || row.Limits != "Use two sites at most" || row.DeadlineAt != r.Tasks[0].DeadlineAt {
		t.Fatalf("task %+v", row)
	}
	if left := time.Until(f.waitDeadline(row.ID)); left < 9*time.Minute || left > 10*time.Minute+time.Second {
		t.Fatalf("the giver's wait ends in %s, want about 10 minutes", left)
	}
	// The holder's blocks carry the contract as written.
	lines := f.s.taskContractForRun(r.Tasks[0].RunID)
	for _, want := range []string{"Result format: A table with name and price", "Limits: Use two sites at most", "Deadline: 10 minutes left"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("contract lines %q miss %q", lines, want)
		}
	}
	if record := f.s.taskRecordContext(r.Tasks[0].RunID); !strings.Contains(record, "Result format: A table with name and price") {
		t.Fatalf("task record %q", record)
	}
}

// A second task of one run starts through the inbox: the wake request carries the
// contract, and the deadline starts when the task starts.
func TestDelegateContractReachesAHolderWokenThroughTheInbox(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	f.delegateTo(f.g, root, f.b, "First")
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.c.ID, "task": "Second", "result_format": "One paragraph", "deadline_seconds": 1200})
	if len(r.Tasks) != 1 || r.Tasks[0].RunID != "" || r.Tasks[0].DeadlineAt == "" {
		t.Fatalf("receipt %+v", r)
	}
	if left := time.Until(f.waitDeadline(r.Tasks[0].TaskID)); left < 19*time.Minute || left > 20*time.Minute+time.Second {
		t.Fatalf("the giver's wait ends in %s, want about 20 minutes", left)
	}
	f.srv.flushInbox()
	runs := f.wokenRuns(f.c)
	if len(runs) != 1 {
		t.Fatalf("woken %d", len(runs))
	}
	var request string
	f.s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=?`, runs[0].ID).Scan(&request)
	if !strings.Contains(request, "Result format: One paragraph") || !strings.Contains(request, "Deadline: 20 minutes left") {
		t.Fatalf("wake request %q", request)
	}
}

// A task that waits on another task spends no time while it waits: its deadline
// is set when it unlocks.
func TestDelegateDeadlineStartsAtUnlock(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	base := f.delegateTo(f.g, root, f.b, "Base")
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.c.ID, "task": "Needs base", "depends_on": []string{base.TaskID}, "deadline_seconds": 900})
	id := r.Tasks[0].TaskID
	if r.Tasks[0].DeadlineAt != "" || f.taskRow(id).DeadlineAt != "" {
		t.Fatalf("a blocked task has no deadline yet: %+v", r.Tasks[0])
	}
	baseRun := f.taskRow(base.TaskID).AssigneeRunID
	f.start(baseRun)
	if _, ok, err := f.s.FinishRun(baseRun, f.g.ID, f.b.ID, "Base done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	row := f.taskRow(id)
	if row.Status != "todo" || row.DeadlineAt == "" {
		t.Fatalf("after unlock %+v", row)
	}
	if left := time.Until(f.waitDeadline(id)); left < 14*time.Minute || left > 15*time.Minute+time.Second {
		t.Fatalf("wait ends in %s, want about 15 minutes", left)
	}
}

func TestDelegateContractIsValidated(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	d := f.tool(f.g, root, "delegate")
	for name, args := range map[string]map[string]any{
		"deadline_range":         {"bot_id": f.b.ID, "task": "x", "deadline_seconds": 5},
		"result_format_too_long": {"bot_id": f.b.ID, "task": "x", "result_format": strings.Repeat("a", 401)},
		"limits_too_long":        {"bot_id": f.b.ID, "task": "x", "limits": strings.Repeat("a", 401)},
	} {
		if _, err := call(t, d, args); codeOf(err) != name {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE bot_id=?`, f.b.ID); n != 0 {
		t.Fatalf("a refused call made %d tasks", n)
	}
}

// When the deadline passes, the giver is woken with a timeout result; the task
// itself stays open.
func TestDelegateDeadlineTimesOutTheGiversWait(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	f.delegateTo(f.g, root, f.b, "First")
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.c.ID, "task": "Slow part", "deadline_seconds": 60})
	id := r.Tasks[0].TaskID
	f.srv.sweepWaits(time.Now().Add(2 * time.Minute))
	var status string
	if err := f.s.db.QueryRow(`SELECT status FROM waits WHERE kind='task_result' AND target=? AND bot_id=?`, id, f.a.ID).Scan(&status); err != nil || status != waitTimedOut {
		t.Fatalf("wait %q %v", status, err)
	}
	if st := f.taskRow(id).Status; isClosedWorkStatus(st) {
		t.Fatalf("a timed-out wait must not close the task: %s", st)
	}
}

// Decision 1: a Bot may give a part to itself; it starts after the run that gave it.
func TestDelegateToSelfIsSerialAndStartsAfterTheRun(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.a.ID, "task": "Own part", "deadline_seconds": 600})
	if len(r.Tasks) != 1 || r.Tasks[0].Runs != "serial" || r.Tasks[0].BotID != f.a.ID || r.Tasks[0].RunID != "" {
		t.Fatalf("receipt %+v", r)
	}
	row := f.taskRow(r.Tasks[0].TaskID)
	if row.BotID != f.a.ID || row.AssignerBotID != f.a.ID {
		t.Fatalf("task %+v", row)
	}
	// While the run goes on, the Bot's lane is taken: no second run of it is running.
	if n := f.count(`SELECT COUNT(*) FROM runs WHERE bot_id=? AND status='running'`, f.a.ID); n != 1 {
		t.Fatalf("running runs %d", n)
	}
}
