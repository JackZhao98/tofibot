package app

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func (f taskFix) workItemEvents(taskID string) int {
	f.t.Helper()
	return f.count(`SELECT COUNT(*) FROM events WHERE type='work_item' AND conversation_id=? AND json_extract(data,'$.id')=?`, f.g.ID, taskID)
}

// A holder keeps one line of status: it overwrites, posts no message, wakes nobody
// and reaches the card at most once in 20 seconds.
func TestProgressIsAnOverwritingLineWithNoMessage(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"},
		{"bot_id": f.a.ID, "task": "Plan days", "title": "Itinerary"},
	})
	hotels := f.startPart(r.Tasks[0].TaskID, f.c)
	id := r.Tasks[0].TaskID
	messagesBefore := f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, f.g.ID)
	inboxBefore := f.count(`SELECT COUNT(*) FROM bot_inbox`)
	eventsBefore := f.workItemEvents(id)
	update := func(line string) error {
		_, err := call(t, f.tool(f.g, hotels, "task_update"), map[string]any{"task_id": id, "progress": line})
		return err
	}
	if err := update("Reading the second price list"); err != nil {
		t.Fatal(err)
	}
	row := f.taskRow(id)
	if row.ProgressLine != "Reading the second price list" {
		t.Fatalf("progress %q", row.ProgressLine)
	}
	if f.count(`SELECT COUNT(*) FROM messages WHERE conversation_id=?`, f.g.ID) != messagesBefore || f.count(`SELECT COUNT(*) FROM bot_inbox`) != inboxBefore {
		t.Fatal("a status line makes no message and no inbox row")
	}
	if got := f.workItemEvents(id); got != eventsBefore+1 {
		t.Fatalf("the first line reaches the card: %d events, was %d", got, eventsBefore)
	}
	// A line inside the window is stored (the next card update shows it) and sends no event.
	if err := update("Comparing the three cheapest"); err != nil {
		t.Fatal(err)
	}
	if f.taskRow(id).ProgressLine != "Comparing the three cheapest" {
		t.Fatal("the newest line wins")
	}
	if got := f.workItemEvents(id); got != eventsBefore+1 {
		t.Fatalf("a second line inside 20 s sends no event: %d", got)
	}
	// After the window the next line reaches the card again.
	f.s.db.Exec(`UPDATE work_items SET progress_at=? WHERE id=?`, stampTime(time.Now().Add(-21*time.Second)), id)
	if err := update("Writing the answer"); err != nil {
		t.Fatal(err)
	}
	if got := f.workItemEvents(id); got != eventsBefore+2 {
		t.Fatalf("after the window the line reaches the card: %d", got)
	}
	// The holder reads its own line in its task record; the lead reads it beside the part's item.
	if record := f.s.taskRecordContext(hotels.ID); !strings.Contains(record, "Status line you keep: Writing the answer") {
		t.Fatalf("task record %q", record)
	}
	if record := f.s.taskRecordContext(lead.ID); !strings.Contains(record, "{task "+id+" ") || !strings.Contains(record, ": Writing the answer}") {
		t.Fatalf("lead record %q", record)
	}
	_ = leadTask
	// The card model carries it for an open part; an empty string clears it.
	items := f.planItems(leadTask.ID)
	if items[0].ProgressLine != "Writing the answer" {
		t.Fatalf("plan item %+v", items[0])
	}
	if err := update(""); err != nil || f.taskRow(id).ProgressLine != "" {
		t.Fatalf("clear: %v %q", err, f.taskRow(id).ProgressLine)
	}
}

func TestProgressIsBoundedAndTheHoldersOnly(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.c.ID, "task": "x", "title": "X"}, {"bot_id": f.a.ID, "task": "y", "title": "Y"}})
	id := r.Tasks[0].TaskID
	holder := f.startPart(id, f.c)
	long := strings.Repeat("é", maxProgressRunes+1)
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": id, "progress": long}); codeOf(err) != "progress_too_long" {
		t.Fatalf("141 characters: %v", err)
	}
	if utf8.RuneCountInString(strings.Repeat("é", maxProgressRunes)) != 140 {
		t.Fatal("test string")
	}
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": id, "progress": strings.Repeat("é", maxProgressRunes)}); err != nil {
		t.Fatalf("140 characters: %v", err)
	}
	// The giver does not keep the holder's line.
	if _, err := call(t, f.tool(f.g, lead, "task_update"), map[string]any{"task_id": id, "progress": "mine"}); codeOf(err) != "task_assignee_only" {
		t.Fatalf("giver: %v", err)
	}
	// With another field set, the line is written beside it and the edit rules still apply.
	if _, err := call(t, f.tool(f.g, holder, "task_update"), map[string]any{"task_id": id, "progress": "Stuck", "status": "blocked"}); codeOf(err) != "task_reason_required" {
		t.Fatalf("blocked without a reason: %v", err)
	}
}

// A part given by a scheduled run, or by a run below one, has an hour and not half of it.
// The time counts from the start of the holder's run, and only parts have a default.
func TestScheduledRunsGiveTheirPartsAnHour(t *testing.T) {
	f := newTaskFix(t)
	lead := queueRun(t, f.s, f.g.ID, f.a.ID, runKindSchedule, "")
	f.start(lead.ID)
	lead, _ = f.s.GetRun(lead.ID)
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.b.ID, "task": "Scheduled part"}})
	if row := f.taskRow(r.Tasks[0].TaskID); row.DeadlineAt != "" {
		t.Fatalf("the clock must wait for the holder's run: %+v", row)
	}
	held := f.startPart(r.Tasks[0].TaskID, f.b)
	if left := time.Until(f.waitDeadline(r.Tasks[0].TaskID)); left < 59*time.Minute || left > 60*time.Minute+time.Second {
		t.Fatalf("a part of a scheduled run has about 60 minutes, has %s", left)
	}
	if row := f.taskRow(r.Tasks[0].TaskID); row.DeadlineAt == "" {
		t.Fatalf("the part carries its deadline: %+v", row)
	}
	// A run below the scheduled run is unattended too.
	child := queueRun(t, f.s, f.g.ID, f.c.ID, runKindFollowup, lead.ID)
	f.start(child.ID)
	child, _ = f.s.GetRun(child.ID)
	if _, ok, err := f.s.FinishRun(held.ID, f.g.ID, f.b.ID, "Done."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	r2, _ := f.partsReceipt(f.g, child, []map[string]any{{"bot_id": f.b.ID, "task": "Below the schedule"}})
	f.startPart(r2.Tasks[0].TaskID, f.b)
	if left := time.Until(f.waitDeadline(r2.Tasks[0].TaskID)); left < 59*time.Minute {
		t.Fatalf("a descendant run of a schedule has an hour, has %s", left)
	}
	// An attended run keeps the shorter default, and the setting changes it.
	if ok, err := f.s.SetRunStatus(child.ID, "cancelled", ""); err != nil || !ok {
		t.Fatal(ok, err)
	}
	attended := f.running(f.g.ID, f.c)
	f.setSetting(autoTaskPartDeadline, 10)
	r3, _ := f.partsReceipt(f.g, attended, []map[string]any{{"bot_id": f.a.ID, "task": "Attended part"}})
	if _, err := f.s.db.Exec(`UPDATE runs SET status='cancelled' WHERE id=?`, lead.ID); err != nil {
		t.Fatal(err)
	}
	f.startPart(r3.Tasks[0].TaskID, f.a)
	if left := time.Until(f.waitDeadline(r3.Tasks[0].TaskID)); left < 9*time.Minute || left > 10*time.Minute+time.Second {
		t.Fatalf("an attended part has the setting's 10 minutes, has %s", left)
	}
}

// A single delegate has no default deadline: its wait keeps the two hours it always had.
func TestAFlatDelegateKeepsTheOldWaitDefault(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.b.ID, "task": "One task"})
	f.start(r.Tasks[0].RunID)
	if row := f.taskRow(r.Tasks[0].TaskID); row.DeadlineAt != "" {
		t.Fatalf("a flat delegate must have no deadline of its own: %+v", row)
	}
	if left := time.Until(f.waitDeadline(r.Tasks[0].TaskID)); left < 119*time.Minute || left > 120*time.Minute+time.Second {
		t.Fatalf("the wait of a flat delegate has 2 hours, has %s", left)
	}
	second := f.delegateArgs(f.g, root, map[string]any{"bot_id": f.c.ID, "task": "A second task"})
	if second.Tasks[0].DeadlineAt != "" {
		t.Fatalf("a second flat delegate must have no deadline: %+v", second.Tasks[0])
	}
}

func TestProgressAndStatusLineTextIsInTheToolDescriptions(t *testing.T) {
	for _, want := range []string{"progress", "140 characters", "posts no message"} {
		if !strings.Contains(taskUpdateToolDescription, want) {
			t.Fatalf("task_update lacks %q", want)
		}
	}
	if !strings.Contains(sayToolDescription, "task card already shows needs no note") {
		t.Fatal("say lacks the task card sentence")
	}
	low := strings.ToLower(taskUpdateToolDescription + sayToolDescription)
	for _, banned := range []string{"for example", "e.g."} {
		if strings.Contains(low, banned) {
			t.Fatalf("descriptions hold %q", banned)
		}
	}
}
