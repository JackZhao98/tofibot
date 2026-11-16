package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"strings"
	"testing"
)

func keptOpen(receipt map[string]any) []string {
	var out []string
	if list, ok := receipt["kept_open"].([]any); ok {
		for _, v := range list {
			out = append(out, v.(string))
		}
	}
	return out
}

// A step the Bot marks done needs finished work of its own (owner 2026-11-16).
// The two live runs of that night, replayed with synthetic calls.
func TestPlanDoneNeedsWorkOfItsOwn(t *testing.T) {
	f := newTaskFix(t)

	// Advance twice in two seconds with no call between: the second is held,
	// changes nothing and sends no event; the answer then finishes the step.
	r := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Confirm the set", "status": "in_progress"}, {"text": "Find user examples", "status": "pending"}, {"text": "Write the uses", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	f.did(r, 4)
	if _, err := f.plan(f.g, r, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	f.did(r, 4)
	if _, err := f.plan(f.g, r, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	version, events := f.planOf(r.ID).Version, f.planEvents(r.ID)
	receipt, err := f.plan(f.g, r, map[string]any{"advance": true})
	if err != nil {
		t.Fatal(err)
	}
	if got := keptOpen(receipt); len(got) != 1 || got[0] != "Write the uses" || receipt["note"] != planHeldNote || receipt["in_progress"] != "Write the uses" {
		t.Fatalf("held advance receipt %v", receipt)
	}
	if p := f.planOf(r.ID); stepsOf(p) != "s1:done:Confirm the set | s2:done:Find user examples | s3:in_progress:Write the uses" || p.Version != version || f.planEvents(r.ID) != events {
		t.Fatalf("a held advance changes nothing: %s v%d (was v%d), %d events", stepsOf(p), p.Version, version, f.planEvents(r.ID))
	}
	tx, _ := f.s.db.Begin()
	if err := planRunFinishedTx(tx, r.ID, sayOutcomeDone); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	if p := f.planOf(r.ID); p.Steps[2].Status != planDone || p.Open {
		t.Fatalf("the answer finishes the step it is: %+v", p)
	}

	// One fetch, then a whole list with a never-started step done: the started
	// step is done, the other is held in progress.
	d := f.running(f.g.ID, f.b)
	f.did(d, 1) // a search before the list: the first step's work
	if _, err := f.plan(f.g, d, map[string]any{"steps": []planStep{{"text": "Find the pictures", "status": "in_progress"}, {"text": "Collect the links", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	f.did(d, 1)
	receipt, err = f.plan(f.g, d, map[string]any{"steps": []planStep{{"text": "Find the pictures", "status": "done"}, {"text": "Collect the links", "status": "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(d.ID)); got != "s1:done:Find the pictures | s2:in_progress:Collect the links" || len(keptOpen(receipt)) != 1 {
		t.Fatalf("one call backs one step: %s %v", got, receipt)
	}

	// A batch that did two steps' work backs both; a reworded list is new
	// steps and needs work of its own; an event the run read is work.
	b := f.running(f.g.ID, f.c)
	if _, err := f.plan(f.g, b, map[string]any{"steps": []planStep{{"text": "Read A", "status": "in_progress"}, {"text": "Read B", "status": "pending"}, {"text": "Compare", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	f.did(b, 2)
	if _, err := f.plan(f.g, b, map[string]any{"steps": []planStep{{"text": "Read A", "status": "done"}, {"text": "Read B", "status": "done"}, {"text": "Compare", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(b.ID)); got != "s1:done:Read A | s2:done:Read B | s3:in_progress:Compare" {
		t.Fatalf("a batch backs its steps: %s", got)
	}
	receipt, err = f.plan(f.g, b, map[string]any{"steps": []planStep{{"text": "Read A again", "status": "done"}, {"text": "Read B", "status": "done"}, {"text": "Compare", "status": "in_progress"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(b.ID)); got != "s1:pending:Read A again | s2:done:Read B | s3:in_progress:Compare" || len(keptOpen(receipt)) != 1 {
		t.Fatalf("a reworded step is not done by the old step's work: %s", got)
	}
	f.s.db.Exec(`INSERT INTO bot_inbox(id,bot_id,conversation_id,kind,body,consumed_by_run,consumed_at,created_at) VALUES('ib_result',?,?,'task_result','synthetic result',?,?,?)`, f.c.ID, f.g.ID, b.ID, now(), now())
	if _, err := f.plan(f.g, b, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(b.ID)); !strings.Contains(got, "s3:done:Compare") {
		t.Fatalf("a result the run read backs a step: %s", got)
	}

	// The reminder path: work before the list backs the steps sent as done.
	late := f.running(f.g.ID, f.a)
	f.did(late, 2)
	if _, err := f.plan(f.g, late, map[string]any{"steps": []planStep{{"text": "Search", "status": "done"}, {"text": "Open the result", "status": "done"}, {"text": "Summarize", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	if got := stepsOf(f.planOf(late.ID)); got != "s1:done:Search | s2:done:Open the result | s3:in_progress:Summarize" {
		t.Fatalf("earlier work backs the first list: %s", got)
	}
	if desc := len(updatePlanToolDescription); desc > 1024 {
		t.Fatalf("description %d bytes", desc)
	}
}

// The gate on its own: in list order, a held step that goes back in progress
// takes back the step the same write started.
func TestGateDoneSteps(t *testing.T) {
	st := func(text, status string) PlanStep { return PlanStep{ID: text, Text: text, Status: status} }
	prev := []PlanStep{st("A", planInProgress), st("B", planPending), st("C", planPending)}
	out, held, moved := gateDoneSteps(prev, []PlanStep{st("A", planDone), st("B", planInProgress), st("C", planPending)}, 0)
	if !sameSteps(out, prev) || len(held) != 1 || moved {
		t.Fatalf("a held advance is no change: %+v %v %v", out, held, moved)
	}
	out, held, moved = gateDoneSteps(prev, []PlanStep{st("A", planDone), st("B", planDone), st("C", planDone)}, 1)
	if got := out[0].Status + out[1].Status + out[2].Status; got != planDone+planInProgress+planPending || len(held) != 2 || !moved {
		t.Fatalf("one call, three steps: %s %v", got, held)
	}
	if _, _, moved = gateDoneSteps(prev, []PlanStep{st("C", planPending), st("A", planInProgress), st("B", planPending)}, 0); moved {
		t.Fatal("re-ordering does not move the list")
	}
}

// When the person writes again the Bot re-assesses its list: in the middle of
// the work beside the message, and in the next run from the list of its last
// work, which its first update_plan continues when a step's text matches.
func TestPlanReassessedOnNewMessages(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	if f.srv.steerPlanContext(f.g, r) != "" {
		t.Fatal("no list, no block")
	}
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Find the set", "status": "in_progress"}, {"text": "List the uses", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	if block := f.srv.steerPlanContext(f.g, r); !strings.HasPrefix(block, "[current plan]\n"+planSteerLead) || !strings.Contains(block, "1. [in_progress] Find the set") {
		t.Fatalf("steer block %q", block)
	}
	att := f.picture("set.png")
	f.s.notePlanImage(r.ID, "", att.ID, planImageViewed)
	f.did(r, 1)
	if _, err := f.plan(f.g, r, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.FinishRun(r.ID, f.g.ID, f.a.ID, "Here are the uses."); err != nil {
		t.Fatal(err)
	}

	// The follow-up reads the closed list as data and shows no card.
	next := f.running(f.g.ID, f.a)
	if _, ok := f.s.runPlan(next.ID); ok {
		t.Fatal("the follow-up starts without a card")
	}
	block := f.srv.currentPlanContext(f.g, next)
	if !strings.HasPrefix(block, "[previous plan]") || !strings.Contains(block, "(answered)") || !strings.Contains(block, "2. [done] List the uses") {
		t.Fatalf("previous block %q", block)
	}
	// It continues the list: matched steps keep their ids, state and pictures;
	// the done steps need no new work; the new step gets the next id.
	if _, err := f.plan(f.g, next, map[string]any{"steps": []planStep{{"text": "Find the set", "status": "done"}, {"text": "List the uses", "status": "done"}, {"text": "Find pictures of the set", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	p := f.planOf(next.ID)
	if stepsOf(p) != "s1:done:Find the set | s2:done:List the uses | s3:in_progress:Find pictures of the set" || p.InheritedFrom != r.ID || !p.continued {
		t.Fatalf("continued list %s from %q", stepsOf(p), p.InheritedFrom)
	}
	if len(p.Steps[0].Images) != 1 || p.Steps[0].Images[0].ID != att.ID {
		t.Fatalf("a continued step keeps its picture: %+v", p.Steps[0])
	}
	// The earlier run's work backs no new step of the continued list.
	if receipt, _ := f.plan(f.g, next, map[string]any{"advance": true}); len(keptOpen(receipt)) != 1 {
		t.Fatalf("the new step needs this run's work: %v", receipt)
	}
	if _, _, err := f.s.FinishRun(next.ID, f.g.ID, f.a.ID, "Pictures."); err != nil {
		t.Fatal(err)
	}

	// Other work: no step text matches, so it is a new list with its own ids.
	other := f.running(f.g.ID, f.a)
	if _, err := f.plan(f.g, other, map[string]any{"steps": []planStep{{"text": "Book the table", "status": "in_progress"}}}); err != nil {
		t.Fatal(err)
	}
	if p := f.planOf(other.ID); stepsOf(p) != "s1:in_progress:Book the table" || p.InheritedFrom != "" || p.continued {
		t.Fatalf("new work is a new list: %s from %q", stepsOf(p), p.InheritedFrom)
	}
	if _, _, err := f.s.FinishRun(other.ID, f.g.ID, f.a.ID, "Booked."); err != nil {
		t.Fatal(err)
	}
	// Only the last work's list is offered; a run of the Bot without a list in
	// between ends the offer, and runs that do not start from the person's
	// message get none.
	plain := f.running(f.g.ID, f.a)
	if _, _, err := f.s.FinishRun(plain.ID, f.g.ID, f.a.ID, "Hi."); err != nil {
		t.Fatal(err)
	}
	if block := f.srv.currentPlanContext(f.g, f.running(f.g.ID, f.a)); block != "" {
		t.Fatalf("an older list is not offered: %q", block)
	}
	wake := queueRun(t, f.s, f.g.ID, f.a.ID, runKindFollowup, other.ID)
	f.start(wake.ID)
	if _, ok := f.s.previousPlan(wake.ID); ok {
		t.Fatal("a wake is not a person's message")
	}
}

// picture stores a synthetic image attachment in the group.
func (f taskFix) picture(name string) Attachment {
	f.t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		f.t.Fatal(err)
	}
	a, err := f.s.addAttachmentOpts(context.Background(), f.g.ID, name, "image/png", &buf, attachmentOpts{BotID: f.a.ID})
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

// A picture attaches to the step in progress when it arrives, at most three a
// step, the newest; the card gets the list again; a deleted file drops out; a
// retry keeps the pictures; the page's run list carries them.
func TestPlanStepPictures(t *testing.T) {
	f := newTaskFix(t)
	r := f.running(f.g.ID, f.a)
	before := f.picture("before.png")
	f.s.notePlanImage(r.ID, "", before.ID, planImageCapture)
	if _, ok := f.s.runPlan(r.ID); ok {
		t.Fatal("a picture without a list makes no list")
	}
	if _, err := f.plan(f.g, r, map[string]any{"steps": []planStep{{"text": "Open the page", "status": "in_progress"}, {"text": "Fill the form", "status": "pending"}}}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 4; i++ {
		a := f.picture("shot.png")
		ids = append(ids, a.ID)
		f.s.notePlanImage(r.ID, "", a.ID, planImageCapture)
	}
	p := f.planOf(r.ID)
	if got := p.Steps[0].Images; len(got) != 3 || got[0].ID != ids[1] || got[2].ID != ids[3] || got[2].Kind != planImageCapture {
		t.Fatalf("the newest three: %+v", got)
	}
	if p.Version != 5 || f.planEvents(r.ID) != 5 {
		t.Fatalf("each picture sends the list: v%d, %d events", p.Version, f.planEvents(r.ID))
	}
	f.s.notePlanImage(r.ID, "", ids[3], planImageCapture)
	if f.planOf(r.ID).Version != 5 {
		t.Fatal("the same picture twice changes nothing")
	}
	// The stored steps never hold pictures.
	var stored string
	f.s.db.QueryRow(`SELECT steps FROM run_plans WHERE run_id=?`, r.ID).Scan(&stored)
	if strings.Contains(stored, "images") {
		t.Fatalf("stored steps %s", stored)
	}
	f.did(r, 1)
	if _, err := f.plan(f.g, r, map[string]any{"advance": true}); err != nil {
		t.Fatal(err)
	}
	// A background capture belongs to the step of the call that ended, even
	// when the list moved on before it was stored.
	late := f.picture("late.png")
	f.s.notePlanImage(r.ID, "s1", late.ID, planImageCapture)
	// An image file of the answer goes to the step in progress.
	file := f.picture("chart.png")
	if _, err := f.s.stageRunAttachment(r, f.g, file, "chart"); err != nil {
		t.Fatal(err)
	}
	p = f.planOf(r.ID)
	if got := p.Steps[0].Images; got[len(got)-1].ID != late.ID {
		t.Fatalf("the late capture: %+v", got)
	}
	if got := p.Steps[1].Images; len(got) != 1 || got[0].ID != file.ID || got[0].Kind != planImageFile {
		t.Fatalf("the answer's image: %+v", got)
	}
	// An image the Bot views goes to the step in progress.
	viewed := f.picture("photo.png")
	if _, err := call(t, f.tool(f.g, r, "read_attachment"), map[string]any{"attachment_id": viewed.ID}); err != nil {
		t.Fatal(err)
	}
	if got := f.planOf(r.ID).Steps[1].Images; len(got) != 2 || got[1].ID != viewed.ID || got[1].Kind != planImageViewed {
		t.Fatalf("the viewed image: %+v", got)
	}
	// Deleting the file removes the picture.
	if err := f.s.deleteUnboundAttachment(late.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.planOf(r.ID).Steps[0].Images; len(got) != 2 {
		t.Fatalf("a deleted picture drops out: %+v", got)
	}
	// The page's run list carries the pictures; a retry keeps them.
	runs, _ := f.s.Runs(f.g.ID)
	for _, run := range runs {
		if run.ID == r.ID && (run.Plan == nil || len(run.Plan.Steps[1].Images) != 2) {
			t.Fatalf("listed plan %+v", run.Plan)
		}
	}
	f.s.SetRunStatus(r.ID, "failed", "synthetic failure")
	retry, err := f.s.RetryRun(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.start(retry.ID)
	if got := f.planOf(retry.ID).Steps; len(got[0].Images) != 2 || len(got[1].Images) != 2 {
		t.Fatalf("the retry keeps the pictures: %+v", got)
	}
	// A closed list takes no picture.
	tx, _ := f.s.db.Begin()
	closeRunPlansTx(tx, []string{retry.ID}, planClosedStopped)
	tx.Commit()
	version := f.planOf(retry.ID).Version
	f.s.notePlanImage(retry.ID, "", f.picture("after.png").ID, planImageCapture)
	if f.planOf(retry.ID).Version != version {
		t.Fatal("a closed list takes no picture")
	}
}
