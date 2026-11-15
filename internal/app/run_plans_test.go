package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type planStep = map[string]any

func (f taskFix) plan(c Conversation, r Run, args map[string]any) (map[string]any, error) {
	f.t.Helper()
	out, err := call(f.t, f.tool(c, r, "update_plan"), args)
	if err != nil {
		return nil, err
	}
	var receipt map[string]any
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		f.t.Fatalf("receipt %q: %v", out, err)
	}
	return receipt, nil
}

func (f taskFix) planOf(runID string) RunPlan {
	f.t.Helper()
	p, ok := f.s.runPlan(runID)
	if !ok {
		f.t.Fatalf("run %s keeps no plan", runID)
	}
	return p
}

func (f taskFix) planEvents(runID string) int {
	f.t.Helper()
	return f.count(`SELECT COUNT(*) FROM events WHERE type='run_plan' AND json_extract(data,'$.run_id')=?`, runID)
}

func hasTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func stepsOf(p RunPlan) string {
	var parts []string
	for _, st := range p.Steps {
		parts = append(parts, st.ID+":"+st.Status+":"+st.Text)
	}
	return strings.Join(parts, " | ")
}

// The Bot keeps its own list with whole-list calls or one advance; each call is
// one row change and one event, and a step keeps its id while it keeps its place
// or its text.
func TestUpdatePlanKeepsTheWholeListWithStableIDs(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	receipt, err := f.plan(f.g, r, map[string]any{"steps": []planStep{
		{"text": "Read the three reports", "status": "in_progress"},
		{"text": "Compare  the totals", "status": "pending"},
		{"text": "Write the summary", "status": "pending"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if receipt["total"] != float64(3) || receipt["done"] != float64(0) || receipt["in_progress"] != "Read the three reports" {
		t.Fatalf("receipt %v", receipt)
	}
	p := f.planOf(r.ID)
	if got := stepsOf(p); got != "s1:in_progress:Read the three reports | s2:pending:Compare the totals | s3:pending:Write the summary" || p.Version != 1 || !p.Open {
		t.Fatalf("plan %s v%d open=%v", got, p.Version, p.Open)
	}
	if f.planEvents(r.ID) != 1 {
		t.Fatal("one call sends one event")
	}
	// A reworded step keeps the id of its place; a new step gets a new id.
	if _, err = f.plan(f.g, r, map[string]any{"steps": []planStep{
		{"text": "Read the three reports", "status": "done"},
		{"text": "Compare the totals by region", "status": "in_progress"},
		{"text": "Write the summary", "status": "pending"},
		{"text": "Check the summary with the person", "status": "pending"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(r.ID)); got != "s1:done:Read the three reports | s2:in_progress:Compare the totals by region | s3:pending:Write the summary | s4:pending:Check the summary with the person" {
		t.Fatalf("ids after a rewrite: %s", got)
	}
	// A moved step keeps the id of its text.
	if _, err = f.plan(f.g, r, map[string]any{"steps": []planStep{
		{"text": "Read the three reports", "status": "done"},
		{"text": "Write the summary", "status": "pending"},
		{"text": "Compare the totals by region", "status": "in_progress"},
		{"text": "Check the summary with the person", "status": "pending"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(r.ID)); got != "s1:done:Read the three reports | s3:pending:Write the summary | s2:in_progress:Compare the totals by region | s4:pending:Check the summary with the person" {
		t.Fatalf("ids after a move: %s", got)
	}
	// advance: the step in progress is done, the next pending step after it starts.
	receipt, err = f.plan(f.g, r, map[string]any{"advance": true})
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(r.ID)); got != "s1:done:Read the three reports | s3:pending:Write the summary | s2:done:Compare the totals by region | s4:in_progress:Check the summary with the person" {
		t.Fatalf("advance: %s", got)
	}
	if receipt["in_progress"] != "Check the summary with the person" || receipt["done"] != float64(2) {
		t.Fatalf("advance receipt %v", receipt)
	}
	if _, err = f.plan(f.g, r, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(r.ID)); !strings.Contains(got, "s3:in_progress") || !strings.Contains(got, "s4:done") {
		t.Fatalf("advance with nothing after the step starts the first pending one: %s", got)
	}
	if f.planOf(r.ID).Version != 5 || f.planEvents(r.ID) != 5 {
		t.Fatalf("version %d events %d", f.planOf(r.ID).Version, f.planEvents(r.ID))
	}
	// An empty list clears the plan; the event still carries the empty list.
	if _, err = f.plan(f.g, r, map[string]any{"steps": []planStep{}}); err != nil {
		t.Fatal(err)
	}
	var data string
	f.s.db.QueryRow(`SELECT data FROM events WHERE type='run_plan' ORDER BY id DESC LIMIT 1`).Scan(&data)
	if !strings.Contains(data, `"steps":[]`) {
		t.Fatalf("a cleared plan sends an explicit empty list: %s", data)
	}
	if _, err = f.plan(f.g, r, map[string]any{"advance": true}); codeOf(err) != "plan_empty" {
		t.Fatalf("advance on an empty plan: %v", err)
	}
}

func TestUpdatePlanRefusesWithCodes(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	many := make([]planStep, planMaxSteps+1)
	for i := range many {
		many[i] = planStep{"text": "Step", "status": "pending"}
	}
	for code, args := range map[string]map[string]any{
		"plan_too_many_steps":     {"steps": many},
		"plan_step_empty":         {"steps": []planStep{{"text": "  ", "status": "pending"}}},
		"plan_step_too_long":      {"steps": []planStep{{"text": strings.Repeat("x", planStepMaxRunes+1), "status": "pending"}}},
		"plan_bad_status":         {"steps": []planStep{{"text": "Do it", "status": "later"}}},
		"plan_steps_or_advance":   {"steps": []planStep{{"text": "Do it", "status": "pending"}}, "advance": true},
		"plan_nothing_to_advance": nil,
	} {
		if code == "plan_nothing_to_advance" {
			if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Do it", "status": "done"}}}); err != nil {
				t.Fatal(err)
			}
			args = map[string]any{"advance": true}
		}
		if _, err := f.plan(f.g, r, args); codeOf(err) != code {
			t.Fatalf("%s: got %v", code, err)
		}
	}
	if _, err := f.plan(f.g, r, map[string]any{}); codeOf(err) != "plan_steps_or_advance" {
		t.Fatalf("no arguments: %v", err)
	}
	// The status words of the TodoWrite convention are understood.
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Do it", "status": "completed"}}}); err != nil || f.planOf(r.ID).Steps[0].Status != planDone {
		t.Fatalf("completed: %v %+v", err, f.planOf(r.ID).Steps)
	}
	if desc := len(updatePlanToolDescription); desc > 1024 {
		t.Fatalf("description %d bytes", desc)
	}
}

// The tool is offered to a person-visible run with the setting on, never to
// triage, and not to a run whose task already carries a checklist.
func TestUpdatePlanIsOfferedWhereTheCardCanShow(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	if !hasTool(f.srv.tools(f.g, r), "update_plan") {
		t.Fatal("a person's run in a group is offered update_plan")
	}
	if tools := f.srv.tools(f.g, r); tools[len(tools)-1].Name != "update_plan" {
		t.Fatalf("update_plan is the last tool, so the cached prefix stays: %s", tools[len(tools)-1].Name)
	}
	triage := r
	triage.Kind = runKindTriage
	if hasTool(f.srv.tools(f.g, triage), "update_plan") {
		t.Fatal("triage keeps no plan")
	}
	hidden := f.g
	hidden.UserVisible = false
	if hasTool(f.srv.tools(hidden, r), "update_plan") {
		t.Fatal("nobody sees a hidden conversation's card")
	}
	setAuto(t, f.srv, autoWorkPlan, "false")
	if hasTool(f.srv.tools(f.g, r), "update_plan") {
		t.Fatal("the setting turns the tool off")
	}
	setAuto(t, f.srv, autoWorkPlan, "true")
	// A run that works on a task with a checklist shows that checklist on the card.
	_, holder, err := f.s.AddHandoff(f.g.ID, f.a.ID, f.b.ID, r.ID, "Collect the numbers of the week")
	if err != nil {
		t.Fatal(err)
	}
	task := f.taskOf(holder.ID)
	tx, _ := f.s.db.Begin()
	if err := addChecklistItemsAsTx(tx, task.ID, []string{"Collect the numbers"}, true); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if hasTool(f.srv.tools(f.g, holder), "update_plan") {
		t.Fatal("a checklist holder keeps no second list")
	}
}

// A list closes with the answer, stays open while the run waits, and ends with Stop all.
func TestPlanClosesWithTheAnswerAndStopAll(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Find the file", "status": "in_progress"}, {"text": "Answer", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.FinishRun(r.ID, f.g.ID, f.a.ID, "Here it is."); err != nil {
		t.Fatal(err)
	}
	if p := f.planOf(r.ID); p.Open || p.Closed != planClosedAnswered || p.Version != 2 || f.planEvents(r.ID) != 2 {
		t.Fatalf("after the answer %+v, %d events", p, f.planEvents(r.ID))
	}
	if _, err := f.plan(f.g, r, map[string]any{"advance": true}); codeOf(err) != "plan_run_ended" {
		t.Fatalf("an ended run's plan does not change: %v", err)
	}
	// A run that ends its turn holding a wait keeps its list open for the run that carries the work on.
	waiting := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, waiting, map[string]any{"steps": []planStep{{"text": "Wait for the parts", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	f.s.db.Exec(`INSERT INTO waits(id,bot_id,run_id,conversation_id,kind,group_id,deadline_at,created_at) VALUES('w1',?,?,?,'timer','g1',?,?)`, f.a.ID, waiting.ID, f.g.ID, stampTime(time.Now().Add(time.Hour)), now())
	if _, _, err := f.s.FinishRun(waiting.ID, f.g.ID, f.a.ID, "Waiting on the parts."); err != nil {
		t.Fatal(err)
	}
	if !f.planOf(waiting.ID).Open {
		t.Fatal("a run that waits keeps its list open")
	}
	// Stop all ends it.
	stopped := f.running(f.g.ID, f.b)
	if _, err := f.plan(f.g, stopped, map[string]any{"steps": []planStep{{"text": "Draft", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CancelRunTree(stopped.ID); err != nil {
		t.Fatal(err)
	}
	if p := f.planOf(stopped.ID); p.Open || p.Closed != planClosedStopped {
		t.Fatalf("after Stop all %+v", p)
	}
}

// Work that goes on in a later run takes the open list along; a person's new
// request starts without one.
func TestPlanCarriesOnToTheRunThatContinuesTheWork(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Fetch the data", "status": "done"}, {"text": "Build the chart", "status": "in_progress"}, {"text": "Explain it", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetRunStatus(r.ID, "failed", "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	if !f.planOf(r.ID).Open {
		t.Fatal("a failed run leaves its list open for its retry")
	}
	retry, err := f.s.RetryRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.s.runPlan(retry.ID); ok {
		t.Fatal("a queued retry has no list until it starts")
	}
	f.start(retry.ID)
	carried := f.planOf(retry.ID)
	if carried.InheritedFrom != r.ID || stepsOf(carried) != stepsOf(f.planOf(r.ID)) || !carried.Open || carried.Version != 1 {
		t.Fatalf("the retry carries the list on: %+v", carried)
	}
	if old := f.planOf(r.ID); old.Open || old.Closed != planClosedPassed {
		t.Fatalf("the failed run's list is passed on: %+v", old)
	}
	block := f.srv.currentPlanContext(retry)
	if !strings.HasPrefix(block, "[current plan]") || !strings.Contains(block, "2. [in_progress] Build the chart") || !strings.HasSuffix(block, "[/current plan]") {
		t.Fatalf("block %q", block)
	}
	if f.srv.currentPlanContext(r) != "" {
		t.Fatal("a closed list gives no block")
	}
	// The carried list keeps its ids, so the next step gets a new one.
	if _, err := f.plan(f.g, retry, map[string]any{"steps": []planStep{{"text": "Fetch the data", "status": "done"}, {"text": "Build the chart", "status": "done"}, {"text": "Explain it", "status": "done"}, {"text": "Share the file", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(retry.ID)); !strings.HasSuffix(got, "s4:in_progress:Share the file") {
		t.Fatalf("ids after the carry: %s", got)
	}
	if _, err := f.s.SetRunStatus(retry.ID, "failed", "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	// A person's new request is new work.
	fresh := f.running(f.g.ID, f.a)
	if _, ok := f.s.runPlan(fresh.ID); ok {
		t.Fatal("a person's new request starts without a list")
	}
	if f.srv.currentPlanContext(fresh) != "" {
		t.Fatal("no block without a list")
	}
	// The wake of a run that ended waiting carries its list on.
	lead := f.running(f.g.ID, f.c)
	if _, err := f.plan(f.g, lead, map[string]any{"steps": []planStep{{"text": "Give out the parts", "status": "done"}, {"text": "Join the results", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	f.s.db.Exec(`INSERT INTO waits(id,bot_id,run_id,conversation_id,kind,group_id,deadline_at,created_at) VALUES('w2',?,?,?,'timer','g2',?,?)`, f.c.ID, lead.ID, f.g.ID, stampTime(time.Now().Add(time.Hour)), now())
	if _, _, err := f.s.FinishRun(lead.ID, f.g.ID, f.c.ID, "Waiting on the parts."); err != nil {
		t.Fatal(err)
	}
	f.s.db.Exec(`UPDATE waits SET status='fulfilled' WHERE id='w2'`)
	wake := queueRun(t, f.s, f.g.ID, f.c.ID, runKindFollowup, lead.ID)
	f.start(wake.ID)
	if p := f.planOf(wake.ID); p.InheritedFrom != lead.ID || len(p.Steps) != 2 {
		t.Fatalf("the wake carries the list on: %+v", p)
	}
}

// Runs listed for the page carry their list; a cleared list is an explicit empty one.
func TestRunListsCarryThePlan(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Look", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	runs, err := f.s.Runs(f.g.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *RunPlan
	for _, run := range runs {
		if run.ID == r.ID {
			found = run.Plan
		}
	}
	if found == nil || len(found.Steps) != 1 || found.Steps[0].Text != "Look" {
		t.Fatalf("listed plan %+v", found)
	}
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{}}); err != nil {
		t.Fatal(err)
	}
	runs, _ = f.s.Runs(f.g.ID)
	for _, run := range runs {
		if run.ID == r.ID {
			data, _ := json.Marshal(run)
			if !strings.Contains(string(data), `"plan":{`) || !strings.Contains(string(data), `"steps":[]`) {
				t.Fatalf("cleared plan in the list: %s", data)
			}
		}
	}
}

func TestPlanRetentionFollowsTheTaskSetting(t *testing.T) {
	f := newTaskFix(t)
	old := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, old, map[string]any{"steps": []planStep{{"text": "Old work", "status": "done"}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.FinishRun(old.ID, f.g.ID, f.a.ID, "Done."); err != nil {
		t.Fatal(err)
	}
	live := f.running(f.g.ID, f.b)
	if _, err := f.plan(f.g, live, map[string]any{"steps": []planStep{{"text": "Live work", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	long := stampTime(time.Now().Add(-40 * 24 * time.Hour))
	f.s.db.Exec(`UPDATE run_plans SET updated_at=?`, long)
	f.srv.runTaskRetention(time.Now())
	if _, ok := f.s.runPlan(old.ID); ok {
		t.Fatal("an ended run's old list goes with the task retention")
	}
	if _, ok := f.s.runPlan(live.ID); !ok {
		t.Fatal("a working run keeps its list")
	}
	// Deleting the run deletes its list.
	f.s.db.Exec(`PRAGMA foreign_keys=ON`)
	if _, err := f.s.db.Exec(`DELETE FROM runs WHERE id=?`, live.ID); err != nil {
		t.Fatal(err)
	}
	if f.count(`SELECT COUNT(*) FROM run_plans WHERE run_id=?`, live.ID) != 0 {
		t.Fatal("the list cascades with its run")
	}
}
