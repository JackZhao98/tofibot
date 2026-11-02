package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// leadFix makes bravo the lead of a piece of work: alpha gave it a task, and
// bravo's run for that task is running. Bravo then gives parts to alpha and
// charlie, so the plan items land on the task card of bravo.
func (f taskFix) leadFix() (lead Run, leadTask WorkItem) {
	f.t.Helper()
	root := f.running(f.g.ID, f.a)
	first := f.delegateTo(f.g, root, f.b, "Plan the trip")
	leadTask = f.taskRow(first.TaskID)
	// Alpha's giving run ends, so alpha is free to hold a part later.
	if _, ok, err := f.s.FinishRun(root.ID, f.g.ID, f.a.ID, "Handed to bravo."); err != nil || !ok {
		f.t.Fatal(ok, err)
	}
	f.start(leadTask.AssigneeRunID)
	lead, _ = f.s.GetRun(leadTask.AssigneeRunID)
	return lead, leadTask
}

func (f taskFix) planItems(taskID string) []ChecklistItem {
	f.t.Helper()
	items, err := checklistTx(f.s.db, taskID)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []ChecklistItem
	for _, it := range items {
		if strings.HasPrefix(it.Mark, planMarkPrefix) {
			out = append(out, it)
		}
	}
	return out
}

// startPart starts the run of a part that began through the inbox and returns it.
func (f taskFix) startPart(task string, bot Bot) Run {
	f.t.Helper()
	f.srv.flushInbox()
	run := f.taskRow(task).AssigneeRunID
	if run == "" {
		f.t.Fatalf("part %s has no run", task)
	}
	f.start(run)
	r, _ := f.s.GetRun(run)
	return r
}

func TestPartsBecomeTheItemsOfTheLeadsTaskCard(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"},
		{"bot_id": f.a.ID, "task": "Plan days", "title": "Itinerary"},
	})
	items := f.planItems(leadTask.ID)
	if len(items) != 2 || items[0].Text != "Hotels · charlie" || items[1].Text != "Itinerary · alpha" || items[0].Status != "open" {
		t.Fatalf("plan items %+v", items)
	}
	if items[0].TaskID != r.Tasks[0].TaskID || items[0].BotID != f.c.ID || items[0].BotName != "charlie" || items[0].DeadlineAt != "" || items[0].TaskStatus != "todo" {
		t.Fatalf("plan item view %+v", items[0])
	}
	// The card is visible: the plan is what the group watches.
	if f.taskRow(leadTask.ID).Status == "" || f.count(`SELECT hidden FROM work_items WHERE id=?`, leadTask.ID) != 0 {
		t.Fatal("the lead's task must be shown")
	}
	// The lead's own final answer is held back while parts are open.
	if open := f.s.openChecklistForSubmission(lead.ID, f.b.ID, leadTask.ID); open != 2 {
		t.Fatalf("open items %d", open)
	}
}

func TestPlanItemFollowsItsPart(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	r, _ := f.partsReceipt(f.g, lead, []map[string]any{
		{"bot_id": f.c.ID, "task": "Find hotels", "title": "Hotels"},
		{"bot_id": f.a.ID, "task": "Plan days", "title": "Itinerary"},
		{"bot_id": f.c.ID, "task": "Price it", "title": "Budget"},
	})
	hotels := f.startPart(r.Tasks[0].TaskID, f.c)
	days := f.startPart(r.Tasks[1].TaskID, f.a)
	if _, ok, err := f.s.FinishRun(hotels.ID, f.g.ID, f.c.ID, "Three hotels."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	budget := f.startPart(r.Tasks[2].TaskID, f.c) // charlie's second part runs after its first
	if ok, err := f.s.SetRunStatus(days.ID, "failed", "synthetic failure"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	items := f.planItems(leadTask.ID)
	if items[0].Status != "done" || items[1].Status != "blocked" || items[1].Reason != "failed" || items[2].Status != "open" {
		t.Fatalf("after one done and one failed: %+v", items)
	}
	// A part that misses its deadline shows blocked "timed out"; the late result still ends it done.
	f.s.db.Exec(`UPDATE waits SET deadline_at=? WHERE kind='task_result' AND target=?`, stampTime(time.Now().Add(-time.Minute)), r.Tasks[2].TaskID)
	f.srv.sweepWaits(time.Now())
	items = f.planItems(leadTask.ID)
	if items[2].Status != "blocked" || items[2].Reason != "timed out" {
		t.Fatalf("after the deadline: %+v", items[2])
	}
	if _, ok, err := f.s.FinishRun(budget.ID, f.g.ID, f.c.ID, "Budget is 2000."); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if items = f.planItems(leadTask.ID); items[2].Status != "done" {
		t.Fatalf("a late result ends the item: %+v", items[2])
	}
	// Failed and timed-out items are blocked, not open, so the lead can deliver with the gap stated.
	if open := f.s.openChecklistForSubmission(lead.ID, f.b.ID, leadTask.ID); open != 0 {
		t.Fatalf("open items %d", open)
	}
}

func TestADirectHandoffAlsoAddsItsPlanItem(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	one := f.delegateTo(f.g, lead, f.c, "Only part")
	items := f.planItems(leadTask.ID)
	if len(items) != 1 || items[0].TaskID != one.TaskID || items[0].Text != "Only part · charlie" {
		t.Fatalf("plan items %+v", items)
	}
	// A cancelled part is blocked with its reason.
	if _, err := call(t, f.tool(f.g, lead, "task_update"), map[string]any{"task_id": one.TaskID, "status": "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if items = f.planItems(leadTask.ID); items[0].Status != "blocked" || items[0].Reason != "cancelled" {
		t.Fatalf("after cancel: %+v", items[0])
	}
}

// A run that holds no task and gives parts becomes the lead: the system makes its
// root task (given by the person, titled from the request) and the parts are the
// items of that one card. A second call of the same run joins the same task.
func TestAGiverWithNoTaskBecomesTheLeadOfOnePlan(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	r, _ := f.partsReceipt(f.g, root, []map[string]any{{"bot_id": f.b.ID, "task": "x", "title": "X"}, {"bot_id": f.c.ID, "task": "y", "title": "Y"}})
	var leadID string
	if err := f.s.db.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, root.ID).Scan(&leadID); err != nil {
		t.Fatalf("the run holds no root task: %v", err)
	}
	lead := f.taskRow(leadID)
	if lead.AssignerKind != "user" || lead.BotID != f.a.ID || lead.ConversationID != f.g.ID || lead.Title != "synthetic request" || lead.ParentTaskID != "" || lead.RootTaskID != leadID || lead.Status != "in_progress" || f.hiddenOf(leadID) {
		t.Fatalf("lead task %+v", lead)
	}
	for _, task := range r.Tasks {
		if row := f.taskRow(task.TaskID); row.ParentTaskID != leadID || row.RootTaskID != leadID {
			t.Fatalf("part %+v", row)
		}
	}
	if items := f.planItems(leadID); len(items) != 2 {
		t.Fatalf("plan items %+v", items)
	}
	// A later call of the same run adds to the same plan, and no second root task appears.
	f.partsReceipt(f.g, root, []map[string]any{{"bot_id": f.b.ID, "task": "z", "title": "Z"}})
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE assignee_run_id=?`, root.ID); n != 1 {
		t.Fatalf("%d root tasks for one run", n)
	}
	if items := f.planItems(leadID); len(items) != 3 {
		t.Fatalf("plan items %+v", items)
	}
}

// A run that gives nothing gets no root task.
func TestARunThatDelegatesNothingGetsNoRootTask(t *testing.T) {
	f := newTaskFix(t)
	root := f.running(f.g.ID, f.a)
	if n := f.count(`SELECT COUNT(*) FROM work_items WHERE assignee_run_id=?`, root.ID); n != 0 {
		t.Fatalf("%d tasks", n)
	}
}

func TestMembersBlockShowsRoleAndState(t *testing.T) {
	f := newTaskFix(t)
	lead, leadTask := f.leadFix()
	f.s.db.Exec(`UPDATE bots SET instructions=? WHERE id=?`, "Researcher who checks prices.\nSecond line is not shown.", f.c.ID)
	f.partsReceipt(f.g, lead, []map[string]any{{"bot_id": f.a.ID, "task": "Plan days", "title": "Itinerary"}})
	bot, _ := f.s.GetBot(f.b.ID)
	g, _ := f.s.GetConversation(f.g.ID)
	block := f.srv.membersContext(g, lead, bot)
	for _, want := range []string{"[members]", "[/members]", "bravo (id=" + f.b.ID + ") — ", "— you", "charlie (id=" + f.c.ID + ") — Researcher who checks prices. — idle", "alpha (id=" + f.a.ID + ")", "busy"} {
		if !strings.Contains(block, want) {
			t.Fatalf("members block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Second line") {
		t.Fatal("only the first line of the role is shown")
	}
	// The lead's own task card item is not what makes it busy: it is "you".
	_ = leadTask
	// The kind is registered so the context view names the block.
	if kind := contextKindFor(block); kind != "members" {
		t.Fatalf("kind %q", kind)
	}
}

func TestMembersBlockIsBoundedAndSkippedForSmallGroups(t *testing.T) {
	f := newTaskFix(t)
	var ids []string
	for i := 0; i < 14; i++ {
		b, err := f.s.CreateBot(fmt.Sprintf("member%02d", i), strings.Repeat("A long role line. ", 30), "m")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, b.ID)
	}
	g, err := f.s.CreateGroup("big", ids)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := f.s.GetBot(ids[0])
	block := f.srv.membersContext(g, Run{ID: "x", BotID: bot.ID}, bot)
	lines := strings.Split(block, "\n")
	listed := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			listed++
			if utf8.RuneCountInString(l) > membersLineRunes {
				t.Fatalf("line of %d runes", utf8.RuneCountInString(l))
			}
		}
	}
	if listed != membersBlockMaxLines || !strings.Contains(block, "(2 more members not listed)") {
		t.Fatalf("listed %d:\n%s", listed, block)
	}
	if utf8.RuneCountInString(block) > 2400 {
		t.Fatalf("block of %d runes", utf8.RuneCountInString(block))
	}
	solo, _ := f.s.CreateGroup("solo", ids[:1])
	if f.srv.membersContext(solo, Run{ID: "x", BotID: bot.ID}, bot) != "" {
		t.Fatal("a group of one has no roster block")
	}
	dm, _ := f.s.GetConversation(bot.DMConversationID)
	if f.srv.membersContext(dm, Run{ID: "x", BotID: bot.ID}, bot) != "" {
		t.Fatal("a DM has no roster block")
	}
}

func TestMembersBlockIsInTheLeadsGroupContext(t *testing.T) {
	f := newTaskFix(t)
	lead, _ := f.leadFix()
	bot, _ := f.s.GetBot(f.b.ID)
	g, _ := f.s.GetConversation(f.g.ID)
	lead.Kind = runKindGroupTask
	msgs, _ := f.srv.buildContextParts(g, lead, bot)
	found := false
	for _, m := range msgs {
		found = found || strings.HasPrefix(m.Content, "[members]")
	}
	if !found {
		t.Fatal("a group run that can divide work reads the roster")
	}
}

// The group prompt grew by one sentence for dividing work; it stays principle-only.
func TestDivideSentenceInTheGroupPromptIsBounded(t *testing.T) {
	const sentence = "Work that has independent parts goes to the members whose roles fit them, also when the person named only you; you keep the whole task and deliver the one result."
	if !strings.Contains(groupTriageGuidance, sentence) {
		t.Fatal("the group guidance lacks the divide sentence")
	}
	if n := utf8.RuneCountInString(groupTriageGuidance); n > 800 {
		t.Fatalf("group triage guidance of %d runes", n)
	}
	for _, banned := range []string{"for example", "e.g.", "such as", " but "} {
		if strings.Contains(strings.ToLower(groupTriageGuidance), banned) {
			t.Fatalf("guidance holds %q", banned)
		}
	}
	// The tool text carries principles only: no example sentences and no contrast pairs.
	for _, text := range []string{delegateToolDescription} {
		low := strings.ToLower(text)
		for _, banned := range []string{"for example", "e.g.", "not x but", " but "} {
			if strings.Contains(low, banned) {
				t.Fatalf("delegate description holds %q", banned)
			}
		}
	}
}
