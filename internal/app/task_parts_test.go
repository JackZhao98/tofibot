package app

import (
	"strings"
	"testing"
	"time"
)

func (f taskFix) delegationMessages(conv string) []string {
	f.t.Helper()
	rows, err := f.s.db.Query(`SELECT content FROM messages WHERE conversation_id=? AND client_message_id LIKE 'delegation:%' ORDER BY seq`, conv)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func (f taskFix) partsReceipt(c Conversation, run Run, parts []map[string]any) (contractReceipt, string) {
	f.t.Helper()
	out, err := call(f.t, f.tool(c, run, "delegate"), map[string]any{"parts": parts})
	if err != nil {
		f.t.Fatalf("delegate parts: %v", err)
	}
	var receipt contractReceipt
	mustJSON(f.t, out, &receipt)
	return receipt, out
}

// One call with parts makes every task in one transaction, posts one message in
// the group, and puts the parts in one wait group the receipt names.
func TestDelegatePartsMakeOneMessageOneWaitGroup(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r, out := f.partsReceipt(f.g, root, []map[string]any{
		{"bot_id": f.b.ID, "task": "Find the hotels", "title": "Hotels", "result_format": "A list with price", "deadline_seconds": 900},
		{"bot_id": f.c.ID, "task": "Plan the days", "title": "Itinerary"},
	})
	if len(r.Tasks) != 2 || r.Tasks[0].RunID != "" || r.Tasks[1].RunID != "" {
		t.Fatalf("receipt %s", out)
	}
	var group struct {
		GroupID string `json:"group_id"`
	}
	mustJSON(t, out, &group)
	if group.GroupID == "" {
		t.Fatalf("the receipt names no wait group: %s", out)
	}
	msgs := f.delegationMessages(f.g.ID)
	if len(msgs) != 1 || msgs[0] != "@bravo Hotels; @charlie Itinerary" {
		t.Fatalf("visible messages %q", msgs)
	}
	for _, task := range r.Tasks {
		if n := f.count(`SELECT COUNT(*) FROM waits WHERE kind='task_result' AND target=? AND group_id=? AND group_mode='all' AND status='pending' AND bot_id=?`, task.TaskID, group.GroupID, f.a.ID); n != 1 {
			t.Fatalf("task %s is not in the group", task.TaskID)
		}
		if task.Status != "todo" || task.DeadlineAt == "" {
			t.Fatalf("part %+v", task)
		}
	}
	// The first part kept its contract; the second took the default deadline of the setting (30 min).
	if row := f.taskRow(r.Tasks[0].TaskID); row.ResultFormat != "A list with price" {
		t.Fatalf("part one %+v", row)
	}
	if left := time.Until(f.waitDeadline(r.Tasks[1].TaskID)); left < 29*time.Minute || left > 30*time.Minute+time.Second {
		t.Fatalf("default deadline %s", left)
	}
	// Both parts belong to one tree, and both holders wake.
	if f.taskRow(r.Tasks[0].TaskID).RootTaskID != f.taskRow(r.Tasks[1].TaskID).RootTaskID {
		t.Fatal("parts of one call share a tree")
	}
	f.srv.flushInbox()
	if len(f.wokenRuns(f.b)) != 1 || len(f.wokenRuns(f.c)) != 1 {
		t.Fatal("both holders must wake")
	}
}

// One refusal leaves nothing behind: no task, no message, no wait.
func TestDelegatePartsAreAllOrNothing(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"parts": []map[string]any{
		{"bot_id": f.b.ID, "task": "Good part"},
		{"bot_id": "no-such-bot", "task": "Bad part"},
	}})
	if err == nil {
		t.Fatal("a part for a non-member must be refused")
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE assigner_bot_id=?`, f.a.ID); n != 0 {
		t.Fatalf("%d tasks left behind", n)
	}
	if len(f.delegationMessages(f.g.ID)) != 0 || f.count(`SELECT COUNT(*) FROM waits WHERE bot_id=?`, f.a.ID) != 0 {
		t.Fatal("a message or wait was left behind")
	}
}

func TestDelegatePartsExcludeTheFlatFields(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	_, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"bot_id": f.b.ID, "task": "x", "parts": []map[string]any{{"bot_id": f.c.ID, "task": "y"}}})
	if codeOf(err) != "parts_exclusive" {
		t.Fatalf("got %v", err)
	}
}

func TestFanoutWidthIsASettingAndRefusesPastIt(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	var parts []map[string]any
	for i := 0; i < 5; i++ {
		bot := []Bot{f.b, f.c}[i%2]
		parts = append(parts, map[string]any{"bot_id": bot.ID, "task": "Part"})
	}
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"parts": parts}); codeOf(err) != "task_fanout_width" {
		t.Fatalf("5 parts at width 4: %v", err)
	}
	if n := f.count(`SELECT COUNT(*) FROM work_items`); n != 0 {
		t.Fatalf("%d tasks after a refusal", n)
	}
	f.setSetting(autoTaskFanoutWidth, 5)
	if r, _ := f.partsReceipt(f.g, root, parts); len(r.Tasks) != 5 {
		t.Fatalf("width 5 must allow 5 parts: %+v", r)
	}
	// The tree holds at most twice the width of open tasks: 10 now, so 6 more are refused.
	f.setSetting(autoTaskFanoutWidth, 2)
	if _, err := call(t, f.tool(f.g, root, "delegate"), map[string]any{"parts": parts[:2]}); codeOf(err) != "task_fanout_width" {
		t.Fatalf("open tasks of the tree: %v", err)
	}
}

func TestFanoutDepthRefusesTheMemberAtTheLimit(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a) // holds no task: it gives at depth 0
	one := f.delegateTo(f.g, root, f.b, "Level one")
	bRun, _ := f.s.GetRun(f.taskRow(one.TaskID).AssigneeRunID)
	f.start(bRun.ID)
	// Depth 1 holder gives: allowed at the default depth 2.
	two := f.delegateArgs(f.g, bRun, map[string]any{"bot_id": f.c.ID, "task": "Level two"}).Tasks[0]
	if two.RunID == "" {
		t.Fatalf("the first task of a run is a direct hand-over: %+v", two)
	}
	f.start(two.RunID)
	if got := f.taskRow(two.TaskID); got.AssigneeRunID != two.RunID {
		t.Fatalf("task %+v", got)
	}
	cRun, _ := f.s.GetRun(two.RunID)
	// Depth 2 is the last level: the holder of the second level can still give one more.
	three := f.delegateArgs(f.g, cRun, map[string]any{"bot_id": f.b.ID, "task": "Level three"}).Tasks[0]
	f.start(three.RunID)
	deepRun, _ := f.s.GetRun(three.RunID)
	// The holder at the limit gives again: refused.
	if _, err := call(t, f.tool(f.g, deepRun, "delegate"), map[string]any{"bot_id": f.c.ID, "task": "Level four"}); codeOf(err) != "task_fanout_depth" {
		t.Fatalf("a task below depth 2: %v", err)
	}
	if _, err := call(t, f.tool(f.g, deepRun, "delegate"), map[string]any{"parts": []map[string]any{{"bot_id": f.c.ID, "task": "Level four"}}}); codeOf(err) != "task_fanout_depth" {
		t.Fatalf("parts at the depth limit: %v", err)
	}
	// Raising the setting lets it through.
	f.setSetting(autoTaskFanoutDepth, 3)
	f.delegateArgs(f.g, deepRun, map[string]any{"bot_id": f.c.ID, "task": "Level four"})
}

func TestPartsMessageMentionsEveryHolderOnce(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	f.partsReceipt(f.g, root, []map[string]any{
		{"bot_id": f.b.ID, "task": "One", "title": "First"},
		{"bot_id": f.c.ID, "task": "Two", "title": "Second"},
	})
	msgs := f.delegationMessages(f.g.ID)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "@bravo First") || !strings.Contains(msgs[0], "@charlie Second") {
		t.Fatalf("messages %q", msgs)
	}
}

// Decision 1: a part for the giver itself starts after the giving run, in the same lane.
func TestPartForTheGiverItselfWaitsForItsLane(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r, _ := f.partsReceipt(f.g, root, []map[string]any{
		{"bot_id": f.a.ID, "task": "My own part", "title": "Own"},
		{"bot_id": f.b.ID, "task": "Bravo part", "title": "Other"},
	})
	if r.Tasks[0].Runs != "serial" || r.Tasks[1].Runs != "" {
		t.Fatalf("receipt %+v", r)
	}
	f.srv.flushInbox()
	if own := f.wokenRuns(f.a); len(own) != 0 {
		t.Fatalf("the Bot is still in its giving run, so its own part does not start yet: %+v", own)
	}
	// The giving run ends with a pending answer; its part then starts as a queued run of the same Bot.
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "The parts are out."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	f.srv.flushInbox()
	own := f.wokenRuns(f.a)
	if len(own) != 1 || own[0].Status != "queued" {
		t.Fatalf("the own part must start as a queued run once the giving run ended: %+v", own)
	}
	if got := f.taskRow(r.Tasks[0].TaskID); got.AssigneeRunID != own[0].ID || got.BotID != f.a.ID {
		t.Fatalf("own part %+v", got)
	}
}
