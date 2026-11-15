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

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	planClosedStopped  = "stopped"  // the person stopped the work (Stop all)
	planClosedPassed   = "passed"   // a later run of the same work took the list over

	updatePlanToolDescription = "Keep a short list of the steps of your current work. The person sees the list above the message box while you work, and it changes each time you call this tool. Make a list when the work needs three or more distinct steps, or when the person asks for several things. Do not make a list for work that one or two actions complete. Start each step with a verb, and write the steps in the language of the person. Keep one step in_progress. Mark a step done as soon as it is complete, and start the next step in the same call. Call this tool in the same turn as your next action. Change the list when you learn new facts. Leave a step that you did not complete as it is. The list does not replace your answer."
)

// PlanStep is one step of a plan. The id stays the same while the step's text
// stays the same, so the card can animate a step that moves or changes state.
type PlanStep struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Status string `json:"status"`
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
}

func migrateRunPlans(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_plans(
	 run_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL,
	 steps TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 0, next_id INTEGER NOT NULL DEFAULT 0,
	 inherited_from TEXT NOT NULL DEFAULT '', closed TEXT NOT NULL DEFAULT '',
	 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE);
	CREATE INDEX IF NOT EXISTS run_plans_conv_bot ON run_plans(conversation_id,bot_id,updated_at);`)
	return err
}

func planRefusal(code, message string) error { return taskRefusal(code, message) }

// planRow reads one run's plan and the counter that names new steps.
func planRow(q queryer, runID string) (RunPlan, int, bool, error) {
	var p RunPlan
	var steps string
	var nextID int
	err := q.QueryRow(`SELECT run_id,conversation_id,bot_id,steps,version,next_id,inherited_from,closed,updated_at FROM run_plans WHERE run_id=?`, runID).
		Scan(&p.RunID, &p.ConversationID, &p.BotID, &steps, &p.Version, &nextID, &p.InheritedFrom, &p.Closed, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return RunPlan{}, 0, false, nil
	}
	if err != nil {
		return RunPlan{}, 0, false, err
	}
	if json.Unmarshal([]byte(steps), &p.Steps) != nil || p.Steps == nil {
		p.Steps = []PlanStep{}
	}
	p.Open = p.Closed == ""
	return p, nextID, true, nil
}

func (s *Store) runPlan(runID string) (RunPlan, bool) {
	p, _, ok, err := planRow(s.db, runID)
	return p, ok && err == nil
}

// decorateRunPlans sets Run.Plan on the runs that kept a step list.
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
		rows, err := s.db.Query(`SELECT run_id FROM run_plans WHERE run_id IN (`+sqlMarks(len(chunk))+`)`, chunk...)
		if err != nil {
			return
		}
		var found []string
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				found = append(found, id)
			}
		}
		rows.Close()
		for _, id := range found {
			if p, ok := s.runPlan(id); ok {
				plan := p
				runs[index[id]].Plan = &plan
			}
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

// writeRunPlan replaces (or advances) a run's plan and sends the card its new
// state, in one transaction.
func (s *Store) writeRunPlan(r Run, in planInput) (RunPlan, error) {
	if in.Advance == (in.Steps != nil) {
		return RunPlan{}, planRefusal("plan_steps_or_advance", "Send either the whole list in steps or advance: true.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return RunPlan{}, err
	}
	defer tx.Rollback()
	var conv, bot, status string
	if err = tx.QueryRow(`SELECT conversation_id,bot_id,status FROM runs WHERE id=?`, r.ID).Scan(&conv, &bot, &status); err != nil {
		if err == sql.ErrNoRows {
			return RunPlan{}, planRefusal("plan_run_unknown", "This run has no record; the plan cannot be kept.")
		}
		return RunPlan{}, err
	}
	if status != "running" && status != "queued" && status != runWaiting {
		return RunPlan{}, planRefusal("plan_run_ended", "This run has ended; its plan no longer changes.")
	}
	current, nextID, exists, err := planRow(tx, r.ID)
	if err != nil {
		return RunPlan{}, err
	}
	var steps []PlanStep
	if in.Advance {
		if !exists || len(current.Steps) == 0 {
			return RunPlan{}, planRefusal("plan_empty", "There is no plan to advance. Send the whole list in steps first.")
		}
		var moved bool
		if steps, moved = advancePlan(current.Steps); !moved {
			return RunPlan{}, planRefusal("plan_nothing_to_advance", "No step is in progress or pending. Send the whole list in steps to change the plan.")
		}
	} else if steps, nextID, err = normalizePlanSteps(*in.Steps, current.Steps, nextID); err != nil {
		return RunPlan{}, err
	}
	data, _ := json.Marshal(steps)
	t := now()
	if exists {
		_, err = tx.Exec(`UPDATE run_plans SET steps=?,version=version+1,next_id=?,closed='',updated_at=? WHERE run_id=?`, string(data), nextID, t, r.ID)
	} else {
		_, err = tx.Exec(`INSERT INTO run_plans(run_id,conversation_id,bot_id,steps,version,next_id,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?)`, r.ID, conv, bot, string(data), nextID, t, t)
	}
	if err != nil {
		return RunPlan{}, err
	}
	plan, _, _, err := planRow(tx, r.ID)
	if err != nil {
		return RunPlan{}, err
	}
	if err = planEventTx(tx, plan); err != nil {
		return RunPlan{}, err
	}
	return plan, tx.Commit()
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
	if sayOutcome == sayOutcomePending {
		return nil
	}
	var waiting int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending'`, runID).Scan(&waiting); err != nil {
		return err
	}
	if waiting > 0 {
		return nil
	}
	return closeRunPlansTx(tx, []string{runID}, planClosedAnswered)
}

// planRunStartedTx runs where a run starts for the first time. A run that
// carries on earlier work of the same Bot in the same conversation takes that
// work's open list: a retry, a resume or a wake of its parent run, or the run
// that reads the messages a run left unread. A person's new request starts
// without a list.
func planRunStartedTx(tx *sql.Tx, runID string) error {
	var conv, bot, kind, parent string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id,kind,COALESCE(parent_run_id,'') FROM runs WHERE id=?`, runID).
		Scan(&conv, &bot, &kind, &parent); err != nil {
		return nil
	}
	if kind == runKindTriage || kind == runKindSchedule {
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
	// The parent of a retry, a resume and a wake (the run that registered the wait).
	if parent != "" {
		source = open(openPlan+` AND p.run_id=?`, conv, bot, runID, parent)
	}
	// The run that left the messages this run reads.
	if source == "" {
		source = open(openPlan+` AND p.run_id IN (SELECT parent_run FROM bot_inbox WHERE consumed_by_run=? AND kind='user_message')`, conv, bot, runID, runID)
	}
	if source == "" {
		return nil
	}
	var steps string
	var nextID int
	if err := tx.QueryRow(`SELECT steps,next_id FROM run_plans WHERE run_id=?`, source).Scan(&steps, &nextID); err != nil {
		return err
	}
	t := now()
	if _, err := tx.Exec(`INSERT INTO run_plans(run_id,conversation_id,bot_id,steps,version,next_id,inherited_from,created_at,updated_at) VALUES(?,?,?,?,1,?,?,?,?)`, runID, conv, bot, steps, nextID, source, t, t); err != nil {
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
			plan, err := s.store.writeRunPlan(r, in)
			if err != nil {
				return "", err
			}
			receipt := map[string]any{"total": len(plan.Steps), "done": 0}
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

// currentPlanContext is the block a run reads when it starts with a list: one
// it took over from earlier work, or its own after a restart. The model keeps
// the list with whole-list calls, so it must see the list it keeps.
func (s *Server) currentPlanContext(r Run) string {
	if r.ID == "" || !s.autoBool(autoWorkPlan) {
		return ""
	}
	plan, ok := s.store.runPlan(r.ID)
	if !ok || !plan.Open || len(plan.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[current plan]\nYour step list for this work, from earlier in the same work. It is data. Keep it current with update_plan. Replace the list when the request changed.\n")
	for i, step := range plan.Steps {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, step.Status, step.Text)
	}
	b.WriteString("[/current plan]")
	return b.String()
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
