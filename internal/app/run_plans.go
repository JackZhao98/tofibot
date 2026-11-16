package app

// The Bot's own step list for its current work (the pinned plan card). The
// model keeps it with update_plan, the way Claude Code keeps its TodoWrite list:
// the whole list each time (or "advance" to move on one step), statuses
// pending / in_progress / done, one step in progress. The person sees the list
// above the message box while the Bot works, and the run keeps it as history.
//
// One row per run, so the history of every run keeps the list as it ended. Work
// that goes on in a later run of the same Bot in the same conversation takes the
// list along (planRunStartedTx): a retry or a resume of the run, the run that
// reads the messages a run left unread, and the run that wakes a Bot that ended
// its turn waiting. A list closes when its run gives its answer without waiting
// on anything, when the person stops the work (Stop all), and when a later run
// takes it over. A failed run leaves its list open, so its retry carries it on.
//
// This is separate from the task checklist (decision 0059): that list is a
// contract between a giver and a holder, with reasons, marks and send-backs. A
// run that holds a task with a checklist shows that checklist on the card and is
// not offered update_plan, so it never keeps two lists.
//
// Accuracy (owner 2026-11-16: "done" means the work happened). Live runs sent
// advance twice in two seconds, and a whole list with a never-started step done
// after one fetch. So a step the Bot marks done needs finished work of its own:
// each newly done step takes one finished tool call (or one event the run read,
// such as a member's result) since the list last moved (a step started or
// finished). A step without one is kept open (held), the write goes on, and the
// receipt says so. The answer is the work of the step in progress and finishes
// it; steps the answer never reached stay open, and an answered list shows them
// as skipped. update_plan runs after the other calls of its batch (agent
// package), so an advance sent with the step's calls counts them.
//
// Re-assessment when the person writes again: a message read in the middle of
// the work comes with the list and the instruction to re-assess it; a run that
// starts from the person's message reads the list of the Bot's last work here
// as data, and its first update_plan continues that list when a step's text
// matches (the step keeps its id, its done state and its pictures).
//
// Pictures: a capture, an image file staged for the answer and an image the Bot
// viewed attach to the step in progress when they arrive (run_plan_images, at
// most planImagesPerStep per step). Deleting the picture's file removes the row.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"
)

const (
	autoWorkPlan = "work_plan"

	planMaxSteps     = 12
	planStepMaxRunes = 120

	planPending    = "pending"
	planInProgress = "in_progress"
	planDone       = "done"

	planClosedAnswered = "answered" // the run gave its answer and waits on nothing
	planClosedBlocked  = "blocked"  // the run answered that an obstacle stops the work
	planClosedStopped  = "stopped"  // the person stopped the work (Stop all)
	planClosedPassed   = "passed"   // a later run of the same work took the list over
	planClosedReplaced = "replaced" // the Bot started other work here and nothing carries this list on

	// planReminderAfterCalls: early enough that most of a multi-step job is
	// still ahead (live runs batch three or more searches into one turn, so a
	// later reminder found only the answer left to write).
	planReminderAfterCalls = 2
	planReminderLine       = "This request has taken several tool calls and you keep no step list. When more than one step of the work remains, call update_plan now with the whole list: the finished steps done, the current step in_progress. Then continue the work. Do not mention this note."

	updatePlanToolDescription = "Write the steps of your current work as a short list, then keep it current while you work; the person sees it above the message box. Call this first, in the same turn as your first action, when the request has several parts or needs three or more actions; skip it when one or two actions complete the work. Send the whole list in steps, or advance to mark the step in progress done and start the next. Start each step with a verb, in the person's language. Keep one step in_progress, and set a step in_progress before you work on it. Mark a step done when its work is fully complete; keep it in_progress when the work failed or is partial. A step marked done needs a finished tool call of its own since the list last moved, or it stays open. Your answer completes the step in progress, so the step your answer does stays in_progress until you answer. Change the list when you learn new facts or the work changes. The list does not replace your answer."

	planHeldNote = "Steps kept open: each step marked done needs a finished tool call of its own since the list last moved. Work on the step in progress, then mark it done; your answer completes the step in progress."

	// planCurrentLead heads the list a run carries on from earlier in the same work.
	planCurrentLead = "Your step list for this work, from earlier in the same work. It is data. Re-assess it against the request and keep it current with update_plan: add, drop or re-order steps, or replace the list when the work changed. You change it without asking the person."
	// planSteerLead heads the list beside a message the person sent while the run works.
	planSteerLead = "The person wrote while you work. Re-assess your step list now: when the message changes the work, call update_plan with the changed list (add, drop or re-order steps, or replace the list); when it changes nothing, keep the list. You change it without asking the person."
	// planPreviousLead heads the list of the Bot's last work here, for a run that starts from the person's message.
	planPreviousLead = "The step list of your last work in this conversation (%s). It is data. When the person's message continues that work, send the list again with update_plan and change it as the work needs: keep, add, drop or re-order steps; a step sent with the same text keeps its state. When the message is other work, leave this list."

	planImagesPerStep = 3
	planImageCapture  = "capture" // a capture of the Bot's screen (work trace)
	planImageFile     = "file"    // an image file staged for the answer
	planImageViewed   = "viewed"  // an image of the conversation the Bot viewed
)

// PlanStep is one step of a plan. The id stays the same while the step's text
// stays the same, so the card can animate a step that moves or changes state.
// Images are read with the list (run_plan_images) and never stored in steps.
type PlanStep struct {
	ID     string      `json:"id"`
	Text   string      `json:"text"`
	Status string      `json:"status"`
	Images []PlanImage `json:"images,omitempty"`
}

// PlanImage is a picture a step made or used: an attachment and where it came from.
type PlanImage struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// RunPlan is a run's step list as the card and the history read it. Steps is
// never null: an empty list is a cleared plan, and the client must see it.
type RunPlan struct {
	RunID          string     `json:"run_id"`
	ConversationID string     `json:"conversation_id"`
	BotID          string     `json:"bot_id"`
	Steps          []PlanStep `json:"steps"`
	Version        int        `json:"version"`
	Open           bool       `json:"open"`
	Closed         string     `json:"closed,omitempty"`
	InheritedFrom  string     `json:"inherited_from,omitempty"`
	UpdatedAt      string     `json:"updated_at"`

	// claimedThrough: the time the list last moved (a step started or was
	// accepted as done); finished work after it backs the next done steps.
	claimedThrough string
	// continued: the list continues a closed list of an earlier run (the
	// person's new message); that run's work backs no step of this one.
	continued bool
}

func migrateRunPlans(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_plans(
	 run_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL,
	 steps TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 0, next_id INTEGER NOT NULL DEFAULT 0,
	 inherited_from TEXT NOT NULL DEFAULT '', closed TEXT NOT NULL DEFAULT '',
	 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE);
	CREATE INDEX IF NOT EXISTS run_plans_conv_bot ON run_plans(conversation_id,bot_id,updated_at);
	CREATE TABLE IF NOT EXISTS run_plan_images(
	 run_id TEXT NOT NULL, step_id TEXT NOT NULL, attachment_id TEXT NOT NULL, kind TEXT NOT NULL, at TEXT NOT NULL,
	 PRIMARY KEY(run_id,step_id,attachment_id),
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
	 FOREIGN KEY(attachment_id) REFERENCES attachments(id) ON DELETE CASCADE);
	CREATE INDEX IF NOT EXISTS run_plan_images_attachment ON run_plan_images(attachment_id);`)
	if err != nil {
		return err
	}
	if err = ensureColumn(db, "run_plans", "claimed_through", `ALTER TABLE run_plans ADD COLUMN claimed_through TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return ensureColumn(db, "run_plans", "continued", `ALTER TABLE run_plans ADD COLUMN continued INTEGER NOT NULL DEFAULT 0`)
}

func planRefusal(code, message string) error { return taskRefusal(code, message) }

const planColumns = `run_id,conversation_id,bot_id,steps,version,next_id,inherited_from,closed,updated_at,claimed_through,continued`

func scanPlan(row rowScanner) (RunPlan, int, error) {
	var p RunPlan
	var steps string
	var nextID, continued int
	if err := row.Scan(&p.RunID, &p.ConversationID, &p.BotID, &steps, &p.Version, &nextID, &p.InheritedFrom, &p.Closed, &p.UpdatedAt, &p.claimedThrough, &continued); err != nil {
		return RunPlan{}, 0, err
	}
	if json.Unmarshal([]byte(steps), &p.Steps) != nil || p.Steps == nil {
		p.Steps = []PlanStep{}
	}
	for i := range p.Steps {
		p.Steps[i].Images = nil
	}
	p.Open, p.continued = p.Closed == "", continued != 0
	return p, nextID, nil
}

// planRow reads one run's plan, with its pictures, and the counter that names new steps.
func planRow(q queryer, runID string) (RunPlan, int, bool, error) {
	p, nextID, err := scanPlan(q.QueryRow(`SELECT `+planColumns+` FROM run_plans WHERE run_id=?`, runID))
	if err == sql.ErrNoRows {
		return RunPlan{}, 0, false, nil
	}
	if err != nil {
		return RunPlan{}, 0, false, err
	}
	images, err := planImagesQ(q, []any{runID})
	if err != nil {
		return RunPlan{}, 0, false, err
	}
	withImages(&p, images[runID])
	return p, nextID, true, nil
}

// planImagesQ reads the pictures of the given runs' lists, oldest first, by run
// and step. A picture whose file is gone is not listed.
func planImagesQ(q queryer, runIDs []any) (map[string]map[string][]PlanImage, error) {
	out := map[string]map[string][]PlanImage{}
	if len(runIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(`SELECT i.run_id,i.step_id,i.attachment_id,i.kind FROM run_plan_images i JOIN attachments a ON a.id=i.attachment_id
	 WHERE i.run_id IN (`+sqlMarks(len(runIDs))+`) ORDER BY i.at,i.attachment_id`, runIDs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var run, step string
		var img PlanImage
		if err = rows.Scan(&run, &step, &img.ID, &img.Kind); err != nil {
			return nil, err
		}
		if out[run] == nil {
			out[run] = map[string][]PlanImage{}
		}
		out[run][step] = append(out[run][step], img)
	}
	return out, rows.Err()
}

func withImages(p *RunPlan, byStep map[string][]PlanImage) {
	for i := range p.Steps {
		p.Steps[i].Images = byStep[p.Steps[i].ID]
	}
}

// storedSteps is the steps column: the steps without their pictures.
func storedSteps(steps []PlanStep) string {
	out := make([]PlanStep, len(steps))
	for i, st := range steps {
		out[i] = PlanStep{ID: st.ID, Text: st.Text, Status: st.Status}
	}
	data, _ := json.Marshal(out)
	return string(data)
}

func (s *Store) runPlan(runID string) (RunPlan, bool) {
	p, _, ok, err := planRow(s.db, runID)
	return p, ok && err == nil
}

// decorateRunPlans sets Run.Plan on the runs that kept a step list: one read
// of the lists and one of their pictures per chunk of runs.
func (s *Store) decorateRunPlans(runs []Run) {
	if len(runs) == 0 {
		return
	}
	index := map[string]int{}
	var ids []any
	for i, r := range runs {
		index[r.ID] = i
		ids = append(ids, r.ID)
	}
	for start := 0; start < len(ids); start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		rows, err := s.db.Query(`SELECT `+planColumns+` FROM run_plans WHERE run_id IN (`+sqlMarks(len(chunk))+`)`, chunk...)
		if err != nil {
			return
		}
		var found []RunPlan
		for rows.Next() {
			if p, _, err := scanPlan(rows); err == nil {
				found = append(found, p)
			}
		}
		rows.Close()
		if len(found) == 0 {
			continue
		}
		planIDs := make([]any, len(found))
		for i, p := range found {
			planIDs[i] = p.RunID
		}
		images, err := planImagesQ(s.db, planIDs)
		if err != nil {
			return
		}
		for _, p := range found {
			plan := p
			withImages(&plan, images[plan.RunID])
			runs[index[plan.RunID]].Plan = &plan
		}
	}
}

func planEventTx(tx *sql.Tx, p RunPlan) error {
	return insertEventTx(tx, p.ConversationID, "run_plan", p, p.UpdatedAt)
}

type planInput struct {
	Steps   *[]struct{ Text, Status string } `json:"steps"`
	Advance bool                             `json:"advance"`
}

// normalizePlanSteps checks the list the model sent and gives it the ids of the
// previous list: a step keeps its id while its text is the same, and a reworded
// step keeps the id of the step that stood at its place.
func normalizePlanSteps(in []struct{ Text, Status string }, previous []PlanStep, nextID int) ([]PlanStep, int, error) {
	if len(in) > planMaxSteps {
		return nil, nextID, planRefusal("plan_too_many_steps", fmt.Sprintf("A plan holds at most %d steps. Join small steps into larger ones.", planMaxSteps))
	}
	out := make([]PlanStep, len(in))
	for i, step := range in {
		text := strings.Join(strings.Fields(step.Text), " ")
		if text == "" {
			return nil, nextID, planRefusal("plan_step_empty", "Every step needs a text.")
		}
		if utf8.RuneCountInString(text) > planStepMaxRunes {
			return nil, nextID, planRefusal("plan_step_too_long", fmt.Sprintf("A step holds at most %d characters. Write a short phrase.", planStepMaxRunes))
		}
		status := strings.ToLower(strings.TrimSpace(step.Status))
		switch status {
		case "", planPending:
			status = planPending
		case planInProgress, "in-progress", "active":
			status = planInProgress
		case planDone, "completed", "complete":
			status = planDone
		default:
			return nil, nextID, planRefusal("plan_bad_status", "A step's status is pending, in_progress or done.")
		}
		out[i] = PlanStep{Text: text, Status: status}
	}
	used := map[string]bool{}
	for i := range out {
		for _, old := range previous {
			if !used[old.ID] && old.Text == out[i].Text {
				out[i].ID, used[old.ID] = old.ID, true
				break
			}
		}
	}
	for i := range out {
		if out[i].ID != "" {
			continue
		}
		if i < len(previous) && !used[previous[i].ID] {
			out[i].ID, used[previous[i].ID] = previous[i].ID, true
			continue
		}
		nextID++
		out[i].ID = fmt.Sprintf("s%d", nextID)
	}
	return out, nextID, nil
}

// advancePlan marks the step in progress as done and starts the next pending
// step after it (or the first pending step when none was in progress).
func advancePlan(steps []PlanStep) ([]PlanStep, bool) {
	out := append([]PlanStep(nil), steps...)
	current := -1
	for i, step := range out {
		if step.Status == planInProgress {
			current = i
			break
		}
	}
	if current >= 0 {
		out[current].Status = planDone
	}
	next := -1
	for i := current + 1; i < len(out); i++ {
		if out[i].Status == planPending {
			next = i
			break
		}
	}
	if next < 0 {
		for i := range out {
			if out[i].Status == planPending {
				next = i
				break
			}
		}
	}
	if next >= 0 {
		out[next].Status = planInProgress
	}
	return out, current >= 0 || next >= 0
}

// gateDoneSteps keeps open the steps that the new list marks done without work
// of their own. A step is newly done when no step of the earlier list with the
// same text was done (text, not id: a reworded step is a new step). The first
// `pool` newly done steps, in list order, are accepted; each later one goes
// back to the state its text had (pending when new). When a held step goes
// back to in progress, a step this write started goes back too, so the list
// keeps one step in progress (a held advance changes nothing); when no step is
// in progress then, the first held step is. It also reports whether the list
// moved: a step accepted as done or a step newly in progress.
func gateDoneSteps(previous, next []PlanStep, pool int) (out []PlanStep, held []string, moved bool) {
	before := map[string]string{}
	for _, st := range previous {
		if before[st.Text] != planDone {
			before[st.Text] = st.Status
		}
	}
	was := func(text string) string {
		if st := before[text]; st != "" {
			return st
		}
		return planPending
	}
	out = append([]PlanStep(nil), next...)
	var heldAt []int
	heldWorking := false
	for i := range out {
		if out[i].Status != planDone || before[out[i].Text] == planDone {
			continue
		}
		if pool > 0 {
			pool--
			moved = true
			continue
		}
		out[i].Status = was(out[i].Text)
		heldWorking = heldWorking || out[i].Status == planInProgress
		held = append(held, out[i].Text)
		heldAt = append(heldAt, i)
	}
	if heldWorking {
		for i := range out {
			if out[i].Status == planInProgress && was(out[i].Text) == planPending {
				out[i].Status = planPending
			}
		}
	}
	working := false
	for _, st := range out {
		working = working || st.Status == planInProgress
	}
	if !working && len(heldAt) > 0 {
		out[heldAt[0]].Status = planInProgress
	}
	for _, st := range out {
		if st.Status == planInProgress && before[st.Text] != planInProgress {
			moved = true
		}
	}
	return out, held, moved
}

// newlyDone counts the steps the new list marks done whose text was not done before.
func newlyDone(previous, next []PlanStep) int {
	done := map[string]bool{}
	for _, st := range previous {
		done[st.Text] = done[st.Text] || st.Status == planDone
	}
	n := 0
	for _, st := range next {
		if st.Status == planDone && !done[st.Text] {
			n++
		}
	}
	return n
}

// planLineage is the run and the earlier runs whose open list it carries on
// (a retry, a resume, a wake): their work belongs to the same steps. A list
// continued from a closed one stops the walk.
func planLineage(q queryer, p RunPlan) []any {
	runs := []any{p.RunID}
	from, continued := p.InheritedFrom, p.continued
	for step := 0; from != "" && !continued && step < planLineageSteps; step++ {
		runs = append(runs, from)
		var cont int
		if q.QueryRow(`SELECT inherited_from,continued FROM run_plans WHERE run_id=?`, from).Scan(&from, &cont) != nil {
			break
		}
		continued = cont != 0
	}
	return runs
}

// planWorkSince counts the work the runs finished after the stamp: completed
// tool calls other than update_plan and say, and the events they read (a
// settled wait, a member's result).
func planWorkSince(q queryer, runs []any, since string) (int, error) {
	args := append(append([]any(nil), runs...), since)
	var calls, events int
	if err := q.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE run_id IN (`+sqlMarks(len(runs))+`) AND status='completed' AND name NOT IN ('update_plan','say') AND updated_at>?`, args...).Scan(&calls); err != nil {
		return 0, err
	}
	if err := q.QueryRow(`SELECT COUNT(*) FROM bot_inbox WHERE consumed_by_run IN (`+sqlMarks(len(runs))+`) AND kind<>'user_message' AND consumed_at>?`, args...).Scan(&events); err != nil {
		return 0, err
	}
	return calls + events, nil
}

func sameSteps(a, b []PlanStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || a[i].Text != b[i].Text || a[i].Status != b[i].Status {
			return false
		}
	}
	return true
}

// writeRunPlan replaces (or advances) a run's plan and sends the card its new
// state, in one transaction. It returns the texts of the steps it kept open. A
// write that changes nothing (an advance that was held) sends no event.
func (s *Store) writeRunPlan(r Run, in planInput) (RunPlan, []string, error) {
	if in.Advance == (in.Steps != nil) {
		return RunPlan{}, nil, planRefusal("plan_steps_or_advance", "Send either the whole list in steps or advance: true.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return RunPlan{}, nil, err
	}
	defer tx.Rollback()
	var conv, bot, status string
	if err = tx.QueryRow(`SELECT conversation_id,bot_id,status FROM runs WHERE id=?`, r.ID).Scan(&conv, &bot, &status); err != nil {
		if err == sql.ErrNoRows {
			return RunPlan{}, nil, planRefusal("plan_run_unknown", "This run has no record; the plan cannot be kept.")
		}
		return RunPlan{}, nil, err
	}
	if status != "running" && status != "queued" && status != runWaiting {
		return RunPlan{}, nil, planRefusal("plan_run_ended", "This run has ended; its plan no longer changes.")
	}
	current, nextID, exists, err := planRow(tx, r.ID)
	if err != nil {
		return RunPlan{}, nil, err
	}
	// The first list of a run that starts from the person's message may continue
	// the list of the Bot's last work here.
	var base *RunPlan
	if !exists && !in.Advance {
		if prev, prevNext, ok, err := previousPlanTx(tx, r.ID); err != nil {
			return RunPlan{}, nil, err
		} else if ok {
			base, nextID = &prev, prevNext
		}
	}
	previous := current.Steps
	if base != nil {
		previous = base.Steps
	}
	var steps []PlanStep
	if in.Advance {
		if !exists || len(current.Steps) == 0 {
			return RunPlan{}, nil, planRefusal("plan_empty", "There is no plan to advance. Send the whole list in steps first.")
		}
		var moved bool
		if steps, moved = advancePlan(current.Steps); !moved {
			return RunPlan{}, nil, planRefusal("plan_nothing_to_advance", "No step is in progress or pending. Send the whole list in steps to change the plan.")
		}
	} else if steps, nextID, err = normalizePlanSteps(*in.Steps, previous, nextID); err != nil {
		return RunPlan{}, nil, err
	}
	// A continued list shares at least one step's text with the earlier one;
	// otherwise it is new work and starts its own ids.
	var carried []any
	if base != nil {
		texts := map[string]string{}
		for _, st := range base.Steps {
			texts[st.Text] = st.ID
		}
		for _, st := range steps {
			if id, ok := texts[st.Text]; ok && id == st.ID {
				carried = append(carried, st.ID)
			}
		}
		if len(carried) == 0 {
			base, previous = nil, nil
			if steps, nextID, err = normalizePlanSteps(*in.Steps, nil, 0); err != nil {
				return RunPlan{}, nil, err
			}
		}
	}
	pool := 0
	if newlyDone(previous, steps) > 0 {
		runs := []any{r.ID}
		if exists {
			runs = planLineage(tx, current)
		}
		if pool, err = planWorkSince(tx, runs, current.claimedThrough); err != nil {
			return RunPlan{}, nil, err
		}
	}
	steps, held, moved := gateDoneSteps(previous, steps, pool)
	if exists && sameSteps(steps, current.Steps) {
		return current, held, tx.Commit()
	}
	t := now()
	claimed := current.claimedThrough
	if moved {
		claimed = t
	}
	if exists {
		_, err = tx.Exec(`UPDATE run_plans SET steps=?,version=version+1,next_id=?,closed='',updated_at=?,claimed_through=? WHERE run_id=?`, storedSteps(steps), nextID, t, claimed, r.ID)
	} else {
		inherited, continued := "", 0
		if base != nil {
			inherited, continued = base.RunID, 1
		}
		_, err = tx.Exec(`INSERT INTO run_plans(run_id,conversation_id,bot_id,steps,version,next_id,inherited_from,continued,claimed_through,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?,?,?,?)`, r.ID, conv, bot, storedSteps(steps), nextID, inherited, continued, claimed, t, t)
		if err == nil && base != nil {
			// The steps carried on keep their pictures.
			_, err = tx.Exec(`INSERT OR IGNORE INTO run_plan_images(run_id,step_id,attachment_id,kind,at) SELECT ?,step_id,attachment_id,kind,at FROM run_plan_images WHERE run_id=? AND step_id IN (`+sqlMarks(len(carried))+`)`, append([]any{r.ID, base.RunID}, carried...)...)
		}
	}
	if err != nil {
		return RunPlan{}, nil, err
	}
	plan, _, _, err := planRow(tx, r.ID)
	if err != nil {
		return RunPlan{}, nil, err
	}
	if err = planEventTx(tx, plan); err != nil {
		return RunPlan{}, nil, err
	}
	return plan, held, tx.Commit()
}

// closeRunPlansTx closes the open plans of the given runs and tells the card.
func closeRunPlansTx(tx *sql.Tx, runIDs []string, reason string) error {
	t := now()
	for _, id := range runIDs {
		res, err := tx.Exec(`UPDATE run_plans SET closed=?,version=version+1,updated_at=? WHERE run_id=? AND closed=''`, reason, t, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		plan, _, ok, err := planRow(tx, id)
		if err != nil {
			return err
		}
		if ok {
			if err = planEventTx(tx, plan); err != nil {
				return err
			}
		}
	}
	return nil
}

// planRunFinishedTx runs where a run gives its answer. The list closes, unless
// the run ends its turn waiting (a pending answer or a pending wait): then the
// run that carries the work on takes the list over.
func planRunFinishedTx(tx *sql.Tx, runID, sayOutcome string) error {
	// delegated: the members the run gave parts to carry the rest, and their
	// results come back to a run of this Bot that takes the list over.
	if sayOutcome == sayOutcomePending || sayOutcome == sayOutcomeDelegated {
		return nil
	}
	var waiting int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending'`, runID).Scan(&waiting); err != nil {
		return err
	}
	if waiting > 0 {
		return nil
	}
	// The answer is the work of the step in progress and finishes it. A blocked
	// answer finishes nothing: it names what stops the work. Every other open
	// step stays open, so the history says what the answer did not reach.
	reason := planClosedAnswered
	if sayOutcome == sayOutcomeBlocked {
		reason = planClosedBlocked
	} else if p, _, ok, err := planRow(tx, runID); err != nil {
		return err
	} else if ok && p.Open && len(p.Steps) > 0 {
		for i := range p.Steps {
			if p.Steps[i].Status == planInProgress {
				p.Steps[i].Status = planDone
				if _, err = tx.Exec(`UPDATE run_plans SET steps=? WHERE run_id=?`, storedSteps(p.Steps), runID); err != nil {
					return err
				}
				break
			}
		}
	}
	return closeRunPlansTx(tx, []string{runID}, reason)
}

// planLineageSteps bounds the walk up the parent runs: a delegation's return
// run descends from the member's run, which descends from the lead's run.
const planLineageSteps = 8

// planRunStartedTx runs where a run starts for the first time. A run that
// carries on earlier work of the same Bot in the same conversation takes that
// work's open list: a retry, a resume or a wake of its nearest ancestor run of
// this Bot (a delegation's return run descends from the member's run), or the
// run that reads the messages a run left unread. The list keeps its pictures
// and the time it last moved. A run that takes no list closes the lists nothing
// will carry on any more (closeOrphanPlansTx): a person's new request starts
// with no card, and reads the Bot's last list as data (previousPlanTx).
func planRunStartedTx(tx *sql.Tx, runID string) error {
	var conv, bot, kind, parent, trigger, resume string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id,kind,COALESCE(parent_run_id,''),COALESCE(trigger_message_id,''),COALESCE(resume_notice,'') FROM runs WHERE id=?`, runID).
		Scan(&conv, &bot, &kind, &parent, &trigger, &resume); err != nil {
		return nil
	}
	if kind == runKindTriage {
		return nil
	}
	if _, _, exists, err := planRow(tx, runID); err != nil || exists {
		return err
	}
	open := func(query string, args ...any) string {
		var id string
		if tx.QueryRow(query, args...).Scan(&id) != nil {
			return ""
		}
		return id
	}
	// A part the Bot gave itself is its own work with its own steps, even though
	// its run descends from the run that gave it.
	var ownPart int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM work_items WHERE assignee_run_id=? AND parent_task_id<>'' AND assigner_kind='bot' AND assigner_bot_id=bot_id`, runID).Scan(&ownPart); err != nil || ownPart > 0 {
		return err
	}
	const openPlan = `SELECT p.run_id FROM run_plans p JOIN runs r ON r.id=p.run_id WHERE p.closed='' AND p.conversation_id=? AND p.bot_id=? AND p.run_id<>? AND r.status NOT IN ('queued','running','waiting') AND p.steps<>'[]'`
	source := ""
	// The nearest ancestor run of this Bot here: the parent of a retry, a resume
	// and a wake (the run that registered the wait), or the lead's run above the
	// member's run a delegation's return run descends from. A scheduled
	// occurrence carries a list on only as the retry or resume of its parent.
	ancestor := parent
	if kind == runKindSchedule && resume != resumeNoticeTask {
		var parentTrigger string
		_ = tx.QueryRow(`SELECT COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, parent).Scan(&parentTrigger)
		if trigger == "" || parentTrigger != trigger {
			ancestor = ""
		}
	}
	// A run that works on a task another Bot gave it carries on only its own
	// direct parent's list (a retry or a wake); the walk past it would reach
	// the giver's work.
	walk := planLineageSteps
	var given int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM work_items WHERE assignee_run_id=? AND assigner_kind='bot'`, runID).Scan(&given); err != nil {
		return err
	}
	if given > 0 {
		walk = 1
	}
	for step := 0; ancestor != "" && step < walk; step++ {
		var aConv, aBot, aParent string
		if tx.QueryRow(`SELECT conversation_id,bot_id,COALESCE(parent_run_id,'') FROM runs WHERE id=?`, ancestor).Scan(&aConv, &aBot, &aParent) != nil {
			break
		}
		if aConv == conv && aBot == bot {
			source = open(openPlan+` AND p.run_id=?`, conv, bot, runID, ancestor)
			break
		}
		ancestor = aParent
	}
	// The run that left the messages this run reads.
	if source == "" && kind != runKindSchedule {
		source = open(openPlan+` AND p.run_id IN (SELECT parent_run FROM bot_inbox WHERE consumed_by_run=? AND kind='user_message')`, conv, bot, runID, runID)
	}
	if source == "" {
		return closeOrphanPlansTx(tx, conv, bot, runID)
	}
	var steps, claimed string
	var nextID int
	if err := tx.QueryRow(`SELECT steps,next_id,claimed_through FROM run_plans WHERE run_id=?`, source).Scan(&steps, &nextID, &claimed); err != nil {
		return err
	}
	t := now()
	if _, err := tx.Exec(`INSERT INTO run_plans(run_id,conversation_id,bot_id,steps,version,next_id,inherited_from,claimed_through,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?,?,?)`, runID, conv, bot, steps, nextID, source, claimed, t, t); err != nil {
		return err
	}
	// The steps keep their pictures.
	if _, err := tx.Exec(`INSERT OR IGNORE INTO run_plan_images(run_id,step_id,attachment_id,kind,at) SELECT ?,step_id,attachment_id,kind,at FROM run_plan_images WHERE run_id=?`, runID, source); err != nil {
		return err
	}
	if err := closeRunPlansTx(tx, []string{source}, planClosedPassed); err != nil {
		return err
	}
	plan, _, _, err := planRow(tx, runID)
	if err != nil {
		return err
	}
	return planEventTx(tx, plan)
}

// closeOrphanPlansTx closes this Bot's open lists here whose runs have ended
// and that nothing will carry on: the run holds no pending wait and left no
// paused task to resume. It runs when a run of the Bot starts without a list,
// so a list left by a wait that was cancelled, an answer that promised work, or
// a failed run whose retry is no longer possible does not stay open.
func closeOrphanPlansTx(tx *sql.Tx, conv, bot, runID string) error {
	rows, err := tx.Query(`SELECT p.run_id FROM run_plans p JOIN runs r ON r.id=p.run_id WHERE p.closed='' AND p.conversation_id=? AND p.bot_id=? AND p.run_id<>? AND r.status NOT IN ('queued','running','waiting')
	 AND NOT EXISTS(SELECT 1 FROM waits w WHERE w.run_id=p.run_id AND w.status='pending')
	 AND NOT EXISTS(SELECT 1 FROM work_items wi WHERE wi.source_run_id=p.run_id AND wi.status='todo' AND wi.interrupted_at<>'')`, conv, bot, runID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	return closeRunPlansTx(tx, ids, planClosedReplaced)
}

// previousPlanTx finds, for a run that starts from the person's message and
// keeps no list yet, the list of the Bot's last work in this conversation: the
// closed list of the Bot's latest ended run here. The run reads it as data and
// its first update_plan may continue it. Nothing for any other run: a retry, a
// wake or a schedule carries an open list on through planRunStartedTx.
func previousPlanTx(q queryer, runID string) (RunPlan, int, bool, error) {
	var conv, bot, kind, parent, trigger string
	if err := q.QueryRow(`SELECT conversation_id,bot_id,kind,COALESCE(parent_run_id,''),COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, runID).
		Scan(&conv, &bot, &kind, &parent, &trigger); err != nil {
		return RunPlan{}, 0, false, nil
	}
	if parent != "" || trigger == "" || kind == runKindTriage || kind == runKindSchedule {
		return RunPlan{}, 0, false, nil
	}
	var person int
	if err := q.QueryRow(`SELECT COUNT(*) FROM user_message_ingress WHERE message_id=?`, trigger).Scan(&person); err != nil || person == 0 {
		return RunPlan{}, 0, false, err
	}
	var last string
	err := q.QueryRow(`SELECT id FROM runs WHERE conversation_id=? AND bot_id=? AND id<>? AND kind<>'triage' AND status NOT IN ('queued','running','waiting') ORDER BY updated_at DESC,created_at DESC LIMIT 1`, conv, bot, runID).Scan(&last)
	if err == sql.ErrNoRows {
		return RunPlan{}, 0, false, nil
	}
	if err != nil {
		return RunPlan{}, 0, false, err
	}
	p, nextID, ok, err := planRow(q, last)
	if err != nil || !ok || p.Open || p.Closed == planClosedPassed || len(p.Steps) == 0 {
		return RunPlan{}, 0, false, err
	}
	return p, nextID, true, nil
}

func (s *Store) previousPlan(runID string) (RunPlan, bool) {
	p, _, ok, err := previousPlanTx(s.db, runID)
	return p, ok && err == nil
}

// planStepInProgress is the id of the step in progress of the run's open list.
func (s *Store) planStepInProgress(runID string) string {
	if p, ok := s.runPlan(runID); ok && p.Open {
		for _, st := range p.Steps {
			if st.Status == planInProgress {
				return st.ID
			}
		}
	}
	return ""
}

// notePlanImage keeps a picture on a step of the run's open list (the given
// step, else the step in progress; nothing when there is none) and sends the
// card the list again. A step keeps its newest planImagesPerStep pictures. A
// failure is logged and never fails the work that made the picture.
func (s *Store) notePlanImage(runID, stepID, attachmentID, kind string) {
	if s == nil || runID == "" || attachmentID == "" {
		return
	}
	if err := s.notePlanImageTx(runID, stepID, attachmentID, kind); err != nil {
		log.Printf("[plan] could not keep a picture on the list of run %s: %v", runID, err)
	}
}

func (s *Store) notePlanImageTx(runID, stepID, attachmentID, kind string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p, _, ok, err := planRow(tx, runID)
	if err != nil || !ok || !p.Open {
		return err
	}
	target := ""
	for _, st := range p.Steps {
		if (stepID != "" && st.ID == stepID) || (stepID == "" && st.Status == planInProgress) {
			target = st.ID
			break
		}
	}
	if target == "" {
		return nil
	}
	t := now()
	res, err := tx.Exec(`INSERT OR IGNORE INTO run_plan_images(run_id,step_id,attachment_id,kind,at) VALUES(?,?,?,?,?)`, runID, target, attachmentID, kind, t)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if _, err = tx.Exec(`DELETE FROM run_plan_images WHERE run_id=? AND step_id=? AND attachment_id NOT IN (SELECT attachment_id FROM run_plan_images WHERE run_id=? AND step_id=? ORDER BY at DESC,attachment_id DESC LIMIT ?)`, runID, target, runID, target, planImagesPerStep); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE run_plans SET version=version+1,updated_at=? WHERE run_id=? AND closed=''`, t, runID); err != nil {
		return err
	}
	if p, _, _, err = planRow(tx, runID); err != nil {
		return err
	}
	if err = planEventTx(tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

// runHoldsChecklist reports whether the run works on a task whose checklist has
// items: the card shows that checklist, so the run keeps no second list.
func (s *Store) runHoldsChecklist(runID string) bool {
	if runID == "" {
		return false
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM work_items w JOIN task_checklist c ON c.task_id=w.id WHERE w.assignee_run_id=?`, runID).Scan(&n)
	return n > 0
}

func (s *Server) planTools(c Conversation, r Run) []Tool {
	if !c.UserVisible || r.Kind == runKindTriage || !s.autoBool(autoWorkPlan) {
		return nil
	}
	if s.store.runHoldsChecklist(r.ID) {
		if _, ok := s.store.runPlan(r.ID); !ok {
			return nil
		}
	}
	return []Tool{s.updatePlanTool(r)}
}

func (s *Server) updatePlanTool(r Run) Tool {
	step := objectSchema(map[string]any{
		"text":   stringProp("What the step does, as a short phrase that starts with a verb."),
		"status": map[string]any{"type": "string", "enum": []string{planPending, planInProgress, planDone}},
	}, []string{"text", "status"})
	return Tool{Name: "update_plan", Description: updatePlanToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"steps":   map[string]any{"type": "array", "maxItems": planMaxSteps, "items": step, "description": fmt.Sprintf("The whole list, in order, at most %d steps. It replaces the earlier list. An empty list removes the plan.", planMaxSteps)},
			"advance": map[string]any{"type": "boolean", "description": "Instead of steps: mark the step in progress as done and start the next pending step."},
		}, nil),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in planInput
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", planRefusal("plan_bad_arguments", "Send steps as a list of objects with text and status, or advance: true.")
			}
			if r.ID == "" {
				return "", errors.New("update_plan needs a run")
			}
			plan, held, err := s.store.writeRunPlan(r, in)
			if err != nil {
				return "", err
			}
			receipt := map[string]any{"total": len(plan.Steps), "done": 0}
			if len(held) > 0 {
				receipt["kept_open"], receipt["note"] = held, planHeldNote
			}
			done := 0
			for _, st := range plan.Steps {
				if st.Status == planDone {
					done++
				} else if st.Status == planInProgress && receipt["in_progress"] == nil {
					receipt["in_progress"] = st.Text
				}
			}
			receipt["done"] = done
			return receiptJSON(receipt)
		}}
}

// planMidRunReminder is the one reminder a run gets after
// planReminderAfterCalls tool calls with no update_plan call: Claude Code's
// reminder that its todo list is unused. Live runs on 2026-11-15 showed that
// the tool description, a system line and a line beside the request did not
// make the model keep a list for a four-part request; in the middle of the
// work the reminder is a direct instruction about work the model can see.
// Empty when the run has no update_plan tool or already keeps a list.
func (s *Server) planMidRunReminder(r Run, tools []Tool) string {
	if r.ID == "" {
		return ""
	}
	for _, tool := range tools {
		if tool.Name == "update_plan" {
			if _, ok := s.store.runPlan(r.ID); ok {
				return ""
			}
			return planReminderLine
		}
	}
	return ""
}

func planBlock(tag, lead string, steps []PlanStep) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]\n%s\n", tag, lead)
	for i, step := range steps {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, step.Status, step.Text)
	}
	fmt.Fprintf(&b, "[/%s]", tag)
	return b.String()
}

// currentPlanContext is the block a run reads when it starts: the list it
// carries on (one it took over from earlier work, or its own after a restart),
// or, for a run that starts from the person's message, the list of the Bot's
// last work here. The model keeps the list with whole-list calls, so it must
// see the list it keeps. Empty when the run is not offered update_plan.
func (s *Server) currentPlanContext(c Conversation, r Run) string {
	if r.ID == "" || len(s.planTools(c, r)) == 0 {
		return ""
	}
	if plan, ok := s.store.runPlan(r.ID); ok {
		if !plan.Open || len(plan.Steps) == 0 {
			return ""
		}
		return planBlock("current plan", planCurrentLead, plan.Steps)
	}
	if prev, ok := s.store.previousPlan(r.ID); ok {
		return planBlock("previous plan", fmt.Sprintf(planPreviousLead, prev.Closed), prev.Steps)
	}
	return ""
}

// steerPlanContext goes with a message the person sent while the run works:
// the list the run keeps and the instruction to re-assess it.
func (s *Server) steerPlanContext(c Conversation, r Run) string {
	if r.ID == "" || len(s.planTools(c, r)) == 0 {
		return ""
	}
	plan, ok := s.store.runPlan(r.ID)
	if !ok || !plan.Open || len(plan.Steps) == 0 {
		return ""
	}
	return planBlock("current plan", planSteerLead, plan.Steps)
}

// cleanupRunPlans removes the lists of ended runs older than the task retention
// period: like a finished task's checklist, a list repeats what was asked.
func (s *Store) cleanupRunPlans(cutoff string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM run_plans WHERE updated_at<? AND run_id NOT IN (SELECT id FROM runs WHERE status IN ('queued','running','waiting'))`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
