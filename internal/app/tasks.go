package app

// Tasks: a board item that one member gave to another (docs/decisions/0059).
// This file holds the rows and their rules: who may change what, the version of
// the request, the checklist, and the way a task follows the run that works on
// it. Waits are in waits.go; the model tools are in task_tools.go.

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

const (
	checklistOpen    = "open"
	checklistDone    = "done"
	checklistSkipped = "skipped"
	checklistBlocked = "blocked"

	maxChecklistItems     = 24
	maxChecklistTextRunes = 200
	maxChecklistReason    = 400
	maxTaskBriefRunes     = 8000
	maxTaskTreeMessages   = 60 // Q18: messages one task tree may carry
)

// queryer is what a *sql.DB and a *sql.Tx both offer.
type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

type ChecklistItem struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Reason    string `json:"reason,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type ChecklistChange struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func isClosedWorkStatus(status string) bool {
	return status == "done" || status == "cancelled" || status == "failed"
}

func validChecklistStatus(status string) bool {
	switch status {
	case checklistOpen, checklistDone, checklistSkipped, checklistBlocked:
		return true
	}
	return false
}

// taskRefusal is a tool refusal with a stable code; the model repairs its call.
func taskRefusal(code, message string) error {
	return tooloutcome.New(tooloutcome.Validation, code, "not_executed", message, "repair_arguments").Err()
}

func checklistTx(q queryer, taskID string) ([]ChecklistItem, error) {
	rows, err := q.Query(`SELECT item_id,text,status,reason,updated_at FROM task_checklist WHERE task_id=? ORDER BY ord,item_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ChecklistItem
	for rows.Next() {
		var item ChecklistItem
		if err := rows.Scan(&item.ID, &item.Text, &item.Status, &item.Reason, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func openChecklistCount(q queryer, taskID string) int {
	var n int
	_ = q.QueryRow(`SELECT COUNT(*) FROM task_checklist WHERE task_id=? AND status='open'`, taskID).Scan(&n)
	return n
}

// addChecklistItemsTx appends items. Only the task's creator calls it.
func addChecklistItemsTx(tx *sql.Tx, taskID string, texts []string) error {
	var have, ord int
	if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(MAX(ord),0) FROM task_checklist WHERE task_id=?`, taskID).Scan(&have, &ord); err != nil {
		return err
	}
	if have+len(texts) > maxChecklistItems {
		return taskRefusal("checklist_too_long", fmt.Sprintf("A checklist holds at most %d items.", maxChecklistItems))
	}
	t := now()
	for _, text := range texts {
		text = strings.TrimSpace(text)
		if text == "" || utf8.RuneCountInString(text) > maxChecklistTextRunes {
			return taskRefusal("checklist_item_invalid", fmt.Sprintf("Each item needs 1-%d characters.", maxChecklistTextRunes))
		}
		ord++
		if _, err := tx.Exec(`INSERT INTO task_checklist(task_id,item_id,ord,text,status,reason,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			taskID, strings.ReplaceAll(uuid.NewString(), "-", "")[:6], ord, text, checklistOpen, "", t, t); err != nil {
			return err
		}
	}
	return nil
}

// decorateTaskTx adds what a reader of the board needs: the checklist, the name
// of the member that gave the task, and what its owner is waiting on.
func decorateTaskTx(q queryer, item *WorkItem) error {
	if item == nil || item.ID == "" {
		return nil
	}
	checklist, err := checklistTx(q, item.ID)
	if err != nil {
		return err
	}
	item.Checklist = checklist
	if item.AssignerBotID != "" {
		_ = q.QueryRow(`SELECT name FROM bots WHERE id=?`, item.AssignerBotID).Scan(&item.AssignerBotName)
	}
	if !isClosedWorkStatus(item.Status) {
		item.WaitingOn, err = pendingWaitViews(q, item.BotID, item.RootTaskID, 6)
	} else {
		item.WaitingOn = nil
	}
	return err
}

// taskEventTx writes the board event for a task row read again from the table.
func taskEventTx(tx *sql.Tx, id string) error {
	item, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemReadColumns+workItemReadFrom+` WHERE w.id=?`, id), true)
	if err != nil {
		return err
	}
	if err = decorateTaskTx(tx, &item); err != nil {
		return err
	}
	return workItemEventTx(tx, item)
}

func getTaskTx(q queryer, id string) (WorkItem, error) {
	return scanWorkItem(q.QueryRow(`SELECT `+workItemAllColumns+` FROM work_items WHERE id=?`, id))
}

// delegationChild is a run that another Bot started as an assignment.
type delegationChild struct {
	RunID, RunConversation, RunBot, RunKind, Origin, Trigger string
	ParentRun, ParentBot, ParentConversation, Brief          string
	RunStatus                                                string
}

// delegationChildSQL selects assignments: a child run of another Bot that a
// delegation made. Retries (same Bot as the parent), wakes and team dispatches
// are not assignments.
const delegationChildSQL = `SELECT r.id,r.conversation_id,r.bot_id,COALESCE(r.kind,''),COALESCE(r.origin_conversation_id,''),COALESCE(r.trigger_message_id,''),
 COALESCE(r.parent_run_id,''),p.bot_id,p.conversation_id,COALESCE(m.content,''),r.status
 FROM runs r JOIN runs p ON p.id=r.parent_run_id LEFT JOIN messages m ON m.id=r.trigger_message_id
 WHERE COALESCE(r.parent_run_id,'')<>'' AND p.bot_id<>r.bot_id
 AND (r.kind IN ('group_task','message') OR (COALESCE(r.kind,'')='' AND COALESCE(r.origin_conversation_id,'')<>'' AND r.conversation_id<>r.origin_conversation_id))`

func scanDelegationChild(row interface{ Scan(...any) error }) (delegationChild, error) {
	var c delegationChild
	err := row.Scan(&c.RunID, &c.RunConversation, &c.RunBot, &c.RunKind, &c.Origin, &c.Trigger, &c.ParentRun, &c.ParentBot, &c.ParentConversation, &c.Brief, &c.RunStatus)
	return c, err
}

// recordTaskForChildTx makes the task row of a delegation, and the wait its
// assigner holds for the result, in the transaction that created the child run.
// It is idempotent: a child that has a task returns it.
func recordTaskForChildTx(tx *sql.Tx, child delegationChild) (WorkItem, error) {
	if existing, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemAllColumns+` FROM work_items WHERE assignee_run_id=?`, child.RunID)); err == nil {
		return existing, nil
	} else if err != sql.ErrNoRows {
		return WorkItem{}, err
	}
	var live int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM bots WHERE id IN (?,?)`, child.ParentBot, child.RunBot).Scan(&live); err != nil || live < 2 {
		return WorkItem{}, err
	}
	rootID, parentTask := "", ""
	var anchorRoot, anchorID string
	err := tx.QueryRow(`WITH RECURSIVE anc(id,depth) AS (
 SELECT ?,0 UNION ALL SELECT r.parent_run_id,a.depth+1 FROM runs r JOIN anc a ON r.id=a.id WHERE COALESCE(r.parent_run_id,'')<>'' AND a.depth<64
 ) SELECT w.id,w.root_task_id FROM work_items w JOIN anc ON anc.id=w.assignee_run_id ORDER BY anc.depth LIMIT 1`, child.ParentRun).Scan(&anchorID, &anchorRoot)
	if err != nil && err != sql.ErrNoRows {
		return WorkItem{}, err
	}
	if err == nil {
		rootID, parentTask = anchorRoot, anchorID
	}
	id := newID()
	if rootID == "" {
		rootID = id
	}
	title := firstLine(child.Brief, 80)
	if title == "" {
		title = "Task"
	}
	status := "todo"
	if child.RunStatus == "running" {
		status = "in_progress"
	}
	t := now()
	if _, err = tx.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,parent_task_id,origin_run_id,assignee_run_id,work_conversation_id,spec_version)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, child.ParentConversation, child.RunBot, "task", title, trimRunes(child.Brief, maxTaskBriefRunes), status, nil, t, t, "",
		"bot", child.ParentBot, rootID, parentTask, child.ParentRun, child.RunID, child.RunConversation, 1); err != nil {
		return WorkItem{}, err
	}
	if _, err = tx.Exec(`INSERT INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, id, 1, "assigner", child.ParentBot, trimRunes(child.Brief, maxTaskBriefRunes), t); err != nil {
		return WorkItem{}, err
	}
	// The assigner holds a wait for the result. The return run that the
	// delegation already makes delivers it, so this wait settles in silence;
	// a deadline that passes first is delivered like any timeout.
	if _, err = recordWaitTx(tx, WaitSpec{BotID: child.ParentBot, RunID: child.ParentRun, ConversationID: child.ParentConversation, Kind: waitTaskResult, Target: id,
		Timeout: waitTimeoutFor(waitTaskResult), RootTaskID: rootID, Delivery: deliveryLegacy, SkipCycle: true}); err != nil {
		return WorkItem{}, err
	}
	if err = taskEventTx(tx, id); err != nil {
		return WorkItem{}, err
	}
	return getTaskTx(tx, id)
}

// taskForRunTx finds the open task a run works on: the run is its assignee run,
// or the run submitted a result for it with say(task_id).
func taskForRunTx(q queryer, runID string) (WorkItem, bool, error) {
	item, err := scanWorkItem(q.QueryRow(`SELECT `+workItemAllColumns+` FROM work_items WHERE (assignee_run_id=? OR submit_run_id=?) AND status NOT IN ('done','cancelled','failed') ORDER BY created_at LIMIT 1`, runID, runID))
	if err == sql.ErrNoRows {
		return WorkItem{}, false, nil
	}
	return item, err == nil, err
}

// taskRunStartedTx moves the task to in_progress when its run starts, and
// remembers which version of the request that run began with. A retry run
// takes over the task of the failed run it repeats.
func taskRunStartedTx(tx *sql.Tx, runID string) error {
	var parent, bot, kind, trigger sql.NullString
	if err := tx.QueryRow(`SELECT parent_run_id,bot_id,kind,trigger_message_id FROM runs WHERE id=?`, runID).Scan(&parent, &bot, &kind, &trigger); err != nil {
		return nil
	}
	item, ok, err := taskForRunTx(tx, runID)
	if err != nil {
		return err
	}
	if !ok && parent.String != "" {
		var pBot, pKind, pTrigger sql.NullString
		if tx.QueryRow(`SELECT bot_id,kind,trigger_message_id FROM runs WHERE id=?`, parent.String).Scan(&pBot, &pKind, &pTrigger) == nil &&
			pBot.String == bot.String && pKind.String == kind.String && pTrigger.String != "" && pTrigger.String == trigger.String {
			var id string
			if tx.QueryRow(`SELECT id FROM work_items WHERE assignee_run_id=?`, parent.String).Scan(&id) == nil {
				if _, err = tx.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, runID, id); err != nil {
					return err
				}
				item, ok, err = taskForRunTx(tx, runID)
				if err != nil {
					return err
				}
				if !ok {
					// A failed task is reopened by its retry.
					item, err = getTaskTx(tx, id)
					if err != nil {
						return err
					}
					ok = item.AssigneeRunID == runID
				}
			}
		}
	}
	if !ok || item.AssigneeRunID != runID {
		return nil
	}
	status := item.Status
	if status == "todo" || status == "blocked" || status == "input_required" || status == "failed" || status == "review" {
		status = "in_progress"
	}
	if _, err = tx.Exec(`UPDATE work_items SET status=?,run_spec_version=spec_version,completed_at='',closed_at='',updated_at=? WHERE id=?`, status, now(), item.ID); err != nil {
		return err
	}
	return taskEventTx(tx, item.ID)
}

// taskRunFinishedTx records a successful run's result on its task and settles
// the waits that were held on it. result is the message the run left.
func taskRunFinishedTx(tx *sql.Tx, done Run, result Message, sayOutcome string) error {
	item, ok, err := taskForRunTx(tx, done.ID)
	if err != nil || !ok {
		return err
	}
	status := "done"
	switch {
	case item.Status == "input_required" || item.Status == "blocked":
		// The member asked for input or reported an obstacle: the task stays so.
		status = item.Status
	case sayOutcome == sayOutcomeBlocked:
		status = "blocked"
	case sayOutcome == sayOutcomePending || sayOutcome == sayOutcomeDelegated:
		status = "in_progress"
	case openChecklistCount(tx, item.ID) > 0:
		status = "review"
	}
	t := now()
	closed := isClosedWorkStatus(status)
	completed := ""
	if closed {
		completed = t
	}
	summary := trimRunesMarker(strings.TrimSpace(result.Content), 240, "…")
	if _, err = tx.Exec(`UPDATE work_items SET status=?,updated_at=?,completed_at=?,closed_at=?,result_message_id=?,result_summary=?,result_based_on_version=?,submit_run_id='' WHERE id=?`,
		status, t, completed, completed, result.ID, summary, item.RunSpecVersion, item.ID); err != nil {
		return err
	}
	// The result is in. Whoever waited on this task has it, in whatever state
	// the task was left.
	if err = settleTaskWaitsTx(tx, item.ID, waitFulfilled, result.ID); err != nil {
		return err
	}
	if item.RunSpecVersion > 0 && item.RunSpecVersion < item.SpecVersion && item.AssignerBotID != "" {
		// The request changed while this run worked: say so to the assigner.
		body := fmt.Sprintf("The result of task %q (%s) is based on version %d of the request; the current version is %d. Decide whether the result stands or the work is repeated.", item.Title, item.ID, item.RunSpecVersion, item.SpecVersion)
		if err = addInboxTx(tx, InboxItem{BotID: item.AssignerBotID, ConversationID: item.ConversationID, Kind: inboxTaskResult, TaskID: item.ID, RootTaskID: item.RootTaskID, Body: body}); err != nil {
			return err
		}
	}
	if isClosedWorkStatus(status) {
		if err = settleClosedTaskTx(tx, WorkItem{ID: item.ID, Status: status, RootTaskID: item.RootTaskID}); err != nil {
			return err
		}
	}
	return taskEventTx(tx, item.ID)
}

// taskRunEndedTx handles a run that ended without a result.
func taskRunEndedTx(tx *sql.Tx, runID, status string) error {
	item, ok, err := taskForRunTx(tx, runID)
	if err != nil || !ok {
		return err
	}
	if item.AssigneeRunID != runID {
		// Only a run that was working for the task can fail it.
		_, err = tx.Exec(`UPDATE work_items SET submit_run_id='' WHERE id=? AND submit_run_id=?`, item.ID, runID)
		return err
	}
	var next string
	switch status {
	case "failed":
		next = "failed"
	case "cancelled":
		// A run a newer message took over from is not a stopped run: the task
		// follows the Bot into the run that carries on.
		var successor string
		err := tx.QueryRow(`SELECT l.new_run_id FROM run_steering l JOIN runs n ON n.id=l.new_run_id WHERE l.old_run_id=? AND n.bot_id=? AND n.status IN ('queued','running','waiting')
 ORDER BY n.queue_seq DESC,n.created_at DESC LIMIT 1`, runID, item.BotID).Scan(&successor)
		if err == nil {
			_, err = tx.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, successor, item.ID)
			return err
		} else if err != sql.ErrNoRows {
			return err
		}
		next = "cancelled"
	default:
		return nil
	}
	t := now()
	if _, err = tx.Exec(`UPDATE work_items SET status=?,updated_at=?,completed_at=?,closed_at=? WHERE id=?`, next, t, t, t, item.ID); err != nil {
		return err
	}
	item.Status = next
	if err = settleClosedTaskTx(tx, item); err != nil {
		return err
	}
	return taskEventTx(tx, item.ID)
}

// settleClosedTaskTx runs when a task reaches a closed status by any route.
// Waits on it settle, and a cancelled or failed task takes the open tasks below
// it along.
func settleClosedTaskTx(tx *sql.Tx, item WorkItem) error {
	switch item.Status {
	case "done", "failed":
		if err := settleTaskWaitsTx(tx, item.ID, waitFulfilled, ""); err != nil {
			return err
		}
	case "cancelled":
		if err := settleTaskWaitsTx(tx, item.ID, waitCancelled, ""); err != nil {
			return err
		}
		_, _, err := cancelTaskTreeTx(tx, []string{item.ID}, false)
		return err
	}
	return nil
}

// settleTaskWaitsTx resolves the pending waits whose target is the task.
func settleTaskWaitsTx(tx *sql.Tx, taskID, status, resultRef string) error {
	ids, err := waitIDsTx(tx, `SELECT id FROM waits WHERE kind=? AND target=? AND status='pending'`, waitTaskResult, taskID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := resolveWaitTx(tx, id, status, resultRef); err != nil {
			return err
		}
	}
	return nil
}

// cancelTaskTreeTx cancels the open tasks in roots and every open task below
// them (by parent link), the waits held by or on them, and reports the runs
// that worked on them so the caller can stop those. With includeRoots false the
// roots are left as they are: the caller closed them already, and only their
// subtree goes.
func cancelTaskTreeTx(tx *sql.Tx, roots []string, includeRoots bool) (int, []string, error) {
	if len(roots) == 0 {
		return 0, nil, nil
	}
	rootSet := map[string]bool{}
	for _, id := range roots {
		rootSet[id] = true
	}
	seen := map[string]bool{}
	var ordered []string
	queue := append([]string(nil), roots...)
	for len(queue) > 0 && len(ordered) < 4096 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		ordered = append(ordered, id)
		kids, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE parent_task_id=?`, id)
		if err != nil {
			return 0, nil, err
		}
		queue = append(queue, kids...)
	}
	cancelled := 0
	var runs []string
	t := now()
	for _, id := range ordered {
		var status, run string
		if err := tx.QueryRow(`SELECT status,assignee_run_id FROM work_items WHERE id=?`, id).Scan(&status, &run); err != nil {
			continue
		}
		if run != "" {
			runs = append(runs, run)
		}
		if isClosedWorkStatus(status) || (rootSet[id] && !includeRoots) {
			continue
		}
		if _, err := tx.Exec(`UPDATE work_items SET status='cancelled',updated_at=?,completed_at=?,closed_at=? WHERE id=?`, t, t, t, id); err != nil {
			return 0, nil, err
		}
		cancelled++
		if err := settleTaskWaitsTx(tx, id, waitCancelled, ""); err != nil {
			return 0, nil, err
		}
		if err := taskEventTx(tx, id); err != nil {
			return 0, nil, err
		}
	}
	// Waits that belong to a run of the tree end with it.
	if len(runs) > 0 {
		ids, err := waitIDsTx(tx, `SELECT id FROM waits WHERE status='pending' AND run_id IN (`+sqlMarks(len(runs))+`)`, anyArgs(runs)...)
		if err != nil {
			return 0, nil, err
		}
		for _, id := range ids {
			if _, err := resolveWaitTx(tx, id, waitCancelled, ""); err != nil {
				return 0, nil, err
			}
		}
	}
	return cancelled, runs, nil
}

// TaskUpdate is one change to a task. Which fields a caller may set depends on
// its role: the creator edits the request and the checklist's membership, the
// assignee reports status and per-item results.
type TaskUpdate struct {
	TaskID      string
	Status      string
	Brief       *string
	AddItems    []string
	RemoveItems []string
	Items       []ChecklistChange
	Reason      string
	Answer      string
}

// updateTask applies a TaskUpdate for actor. It reports the change to the
// other side through the inbox.
func (s *Store) updateTask(actor string, in TaskUpdate) (WorkItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	item, err := getTaskTx(tx, in.TaskID)
	if err == sql.ErrNoRows {
		return WorkItem{}, taskRefusal("task_not_found", "No task has that id.")
	}
	if err != nil {
		return WorkItem{}, err
	}
	creator := item.AssignerBotID != "" && actor == item.AssignerBotID
	assignee := actor == item.BotID
	if !creator && !assignee {
		return WorkItem{}, taskRefusal("task_forbidden", "Only the member that gave the task or the member that holds it may change it.")
	}
	if isClosedWorkStatus(item.Status) {
		return WorkItem{}, taskRefusal("task_closed", "The task is closed. Give a new task instead.")
	}
	t := now()
	// Checklist membership and the request belong to the creator.
	if len(in.AddItems) > 0 || len(in.RemoveItems) > 0 || in.Brief != nil || in.Answer != "" {
		if !creator {
			return WorkItem{}, taskRefusal("checklist_creator_only", "Only the member that gave the task may add or remove checklist items, change the request, or answer a question about it.")
		}
	}
	// Item results and the task's own progress belong to the assignee.
	if len(in.Items) > 0 || in.Status == "in_progress" || in.Status == "blocked" {
		if !assignee {
			return WorkItem{}, taskRefusal("task_assignee_only", "Only the member that holds the task may report its progress or the result of a checklist item.")
		}
	}
	if len(in.AddItems) > 0 {
		if err = addChecklistItemsTx(tx, item.ID, in.AddItems); err != nil {
			return WorkItem{}, err
		}
	}
	for _, id := range in.RemoveItems {
		res, e := tx.Exec(`DELETE FROM task_checklist WHERE task_id=? AND item_id=?`, item.ID, id)
		if e != nil {
			return WorkItem{}, e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return WorkItem{}, taskRefusal("checklist_item_unknown", "No checklist item has that id: "+id)
		}
	}
	for _, change := range in.Items {
		if !validChecklistStatus(change.Status) {
			return WorkItem{}, taskRefusal("checklist_status_invalid", "An item is open, done, skipped or blocked.")
		}
		reason := strings.TrimSpace(change.Reason)
		if change.Status != checklistOpen && reason == "" {
			return WorkItem{}, taskRefusal("checklist_reason_required", "Every item that ends, as done, skipped or blocked, carries a reason.")
		}
		if utf8.RuneCountInString(reason) > maxChecklistReason {
			return WorkItem{}, taskRefusal("checklist_reason_too_long", fmt.Sprintf("A reason holds at most %d characters.", maxChecklistReason))
		}
		res, e := tx.Exec(`UPDATE task_checklist SET status=?,reason=?,updated_at=? WHERE task_id=? AND item_id=?`, change.Status, reason, t, item.ID, change.ID)
		if e != nil {
			return WorkItem{}, e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return WorkItem{}, taskRefusal("checklist_item_unknown", "No checklist item has that id: "+change.ID)
		}
	}
	message := false
	if in.Brief != nil {
		brief := strings.TrimSpace(*in.Brief)
		if brief == "" || utf8.RuneCountInString(brief) > maxTaskBriefRunes {
			return WorkItem{}, taskRefusal("task_brief_invalid", fmt.Sprintf("The request needs 1-%d characters.", maxTaskBriefRunes))
		}
		if brief != item.Description {
			item.SpecVersion++
			item.Description = brief
			if _, err = tx.Exec(`UPDATE work_items SET description=?,spec_version=? WHERE id=?`, brief, item.SpecVersion, item.ID); err != nil {
				return WorkItem{}, err
			}
			if err = noteTaskRevisionTx(tx, item, "assigner", actor, brief); err != nil {
				return WorkItem{}, err
			}
			// Both sides learn of the new version; the assigner's edit is
			// not a message to itself.
			if item.BotID != actor {
				if err = addInboxTx(tx, InboxItem{BotID: item.BotID, ConversationID: item.WorkConversationOrConversation(), Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID,
					Body: fmt.Sprintf("The request for task %q (%s) changed. It is now version %d: %s", item.Title, item.ID, item.SpecVersion, trimRunes(brief, 1200)), Wakes: true, AfterRun: ""}); err != nil {
					return WorkItem{}, err
				}
			}
			message = true
		}
	}
	if in.Answer != "" {
		if item.Status != "input_required" {
			return WorkItem{}, taskRefusal("task_not_waiting_for_input", "The task is not waiting for an answer.")
		}
		if err = addInboxTx(tx, InboxItem{BotID: item.BotID, ConversationID: item.WorkConversationOrConversation(), Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID,
			Body: fmt.Sprintf("Answer to your question about task %q (%s): %s", item.Title, item.ID, trimRunes(in.Answer, 2000)), Wakes: true, ParentRun: item.AssigneeRunID}); err != nil {
			return WorkItem{}, err
		}
		item.Status = "in_progress"
		message = true
	}
	switch in.Status {
	case "":
	case "in_progress", "blocked":
		if in.Status == "blocked" && strings.TrimSpace(in.Reason) == "" {
			return WorkItem{}, taskRefusal("task_reason_required", "A blocked task carries the reason.")
		}
		item.Status = in.Status
	case "cancelled", "done":
		if !creator {
			return WorkItem{}, taskRefusal("task_creator_only", "Only the member that gave the task may accept it as done or cancel it.")
		}
		item.Status = in.Status
	default:
		return WorkItem{}, taskRefusal("task_status_invalid", "A status is in_progress, blocked, done or cancelled.")
	}
	if message {
		if err = bumpTaskMessagesTx(tx, item.RootTaskID); err != nil {
			return WorkItem{}, err
		}
	}
	closed := isClosedWorkStatus(item.Status)
	completed := ""
	if closed {
		completed = t
	}
	if _, err = tx.Exec(`UPDATE work_items SET status=?,updated_at=?,completed_at=?,closed_at=? WHERE id=?`, item.Status, t, completed, completed, item.ID); err != nil {
		return WorkItem{}, err
	}
	if closed {
		if err = settleClosedTaskTx(tx, WorkItem{ID: item.ID, Status: item.Status, RootTaskID: item.RootTaskID}); err != nil {
			return WorkItem{}, err
		}
	}
	if err = taskEventTx(tx, item.ID); err != nil {
		return WorkItem{}, err
	}
	out, err := getTaskTx(tx, item.ID)
	if err != nil {
		return WorkItem{}, err
	}
	if err = decorateTaskTx(tx, &out); err != nil {
		return WorkItem{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return out, nil
}

// WorkConversationOrConversation is where the member holding the task works.
func (w WorkItem) WorkConversationOrConversation() string {
	if w.WorkConversationID != "" {
		return w.WorkConversationID
	}
	return w.ConversationID
}

func noteTaskRevisionTx(tx *sql.Tx, item WorkItem, source, actor, text string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, item.ID, item.SpecVersion, source, actor, trimRunes(text, maxTaskBriefRunes), now())
	return err
}

// bumpTaskMessagesTx counts a message against the tree and refuses past the
// limit, so two members cannot talk forever.
func bumpTaskMessagesTx(tx *sql.Tx, root string) error {
	if root == "" {
		return nil
	}
	var total int
	if err := tx.QueryRow(`SELECT COALESCE(SUM(message_count),0) FROM work_items WHERE root_task_id=?`, root).Scan(&total); err != nil {
		return err
	}
	if total >= maxTaskTreeMessages {
		return taskRefusal("task_message_limit", fmt.Sprintf("This task tree already carried %d messages. Settle the work with what exists, or ask the person.", maxTaskTreeMessages))
	}
	_, err := tx.Exec(`UPDATE work_items SET message_count=message_count+1 WHERE id=?`, root)
	return err
}

// askTask is task_ask: the member that holds the task asks the member that gave
// it. The task becomes input_required; the question reaches the assigner's
// inbox once the asking run has ended.
func (s *Store) askTask(actor, runID, taskID, question string) (WorkItem, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > 4000 {
		return WorkItem{}, taskRefusal("task_question_invalid", "The question needs 1-4000 characters.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	item, err := getTaskTx(tx, taskID)
	if err == sql.ErrNoRows {
		return WorkItem{}, taskRefusal("task_not_found", "No task has that id.")
	}
	if err != nil {
		return WorkItem{}, err
	}
	if item.BotID != actor {
		return WorkItem{}, taskRefusal("task_assignee_only", "Only the member that holds the task may ask about it.")
	}
	if isClosedWorkStatus(item.Status) {
		return WorkItem{}, taskRefusal("task_closed", "The task is closed.")
	}
	if item.AssignerKind != "bot" || item.AssignerBotID == "" || item.AssignerBotID == actor {
		return WorkItem{}, taskRefusal("task_no_assigner_bot", "No member gave this task. Ask the person with the question tool.")
	}
	if err = bumpTaskMessagesTx(tx, item.RootTaskID); err != nil {
		return WorkItem{}, err
	}
	if err = addInboxTx(tx, InboxItem{BotID: item.AssignerBotID, ConversationID: item.ConversationID, Kind: inboxTaskQuestion, TaskID: item.ID, RootTaskID: item.RootTaskID,
		Body: fmt.Sprintf("The member holding task %q (%s) asks: %s Answer with the answer field of task_update, which loads on demand when you name it in workspace_capabilities.", item.Title, item.ID, question), Wakes: true, NeedsReply: true, AfterRun: runID}); err != nil {
		return WorkItem{}, err
	}
	if _, err = tx.Exec(`UPDATE work_items SET status='input_required',updated_at=? WHERE id=?`, now(), item.ID); err != nil {
		return WorkItem{}, err
	}
	if err = taskEventTx(tx, item.ID); err != nil {
		return WorkItem{}, err
	}
	out, err := getTaskTx(tx, item.ID)
	if err != nil {
		return WorkItem{}, err
	}
	return out, tx.Commit()
}

// noteUserMessageForTasks is the version rule for a person's words. A message
// the person sends to a member that holds an open task from another member
// changes that task's request: the version goes up, the change is recorded,
// and the member that gave the task and the member that holds it both learn
// of it. Nothing is left to the Bots remembering to pass it on.
func (s *Store) noteUserMessageForTasks(conversationID string, message Message, botIDs []string) {
	if conversationID == "" || len(botIDs) == 0 {
		return
	}
	for _, botID := range botIDs {
		if err := s.noteUserMessageForBotTasks(conversationID, message, botID); err != nil {
			logTaskError("user message version", err)
		}
	}
}

func (s *Store) noteUserMessageForBotTasks(conversationID string, message Message, botID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE bot_id=? AND assigner_kind='bot' AND status NOT IN ('done','cancelled','failed') AND (conversation_id=? OR work_conversation_id=?) ORDER BY created_at`, botID, conversationID, conversationID)
	if err != nil || len(ids) == 0 {
		return err
	}
	for _, id := range ids {
		item, err := getTaskTx(tx, id)
		if err != nil {
			return err
		}
		item.SpecVersion++
		if _, err = tx.Exec(`UPDATE work_items SET spec_version=?,updated_at=? WHERE id=?`, item.SpecVersion, now(), id); err != nil {
			return err
		}
		if err = noteTaskRevisionTx(tx, item, "person", "", message.Content); err != nil {
			return err
		}
		note := fmt.Sprintf("The person changed the request for task %q (%s) with a new message. It is now version %d: %s", item.Title, item.ID, item.SpecVersion, trimRunes(message.Content, 1200))
		for _, side := range []string{item.BotID, item.AssignerBotID} {
			if side == "" {
				continue
			}
			conv := item.ConversationID
			if side == item.BotID {
				conv = item.WorkConversationOrConversation()
			}
			if err = addInboxTx(tx, InboxItem{BotID: side, ConversationID: conv, Kind: inboxTaskUpdate, TaskID: id, RootTaskID: item.RootTaskID, Body: note}); err != nil {
				return err
			}
		}
		if err = taskEventTx(tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// taskRecordContext is the block a run that holds a task reads: the request's
// version, the member that gave it and the checklist with each item's state.
func (s *Store) taskRecordContext(runID string) string {
	item, ok, err := taskForRunTx(s.db, runID)
	if err != nil || !ok {
		return ""
	}
	if err := decorateTaskTx(s.db, &item); err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("[task record]\nThe task you hold, stored by the system. The request has a version; a later version replaces the earlier one.\n")
	fmt.Fprintf(&b, "task_id=%s version=%d status=%s", item.ID, item.SpecVersion, item.Status)
	if item.AssignerBotID != "" {
		fmt.Fprintf(&b, " given_by=%s (id=%s)", item.AssignerBotName, item.AssignerBotID)
	}
	b.WriteString("\n")
	if len(item.Checklist) > 0 {
		b.WriteString("Checklist, one item at a time, each ended with a reason:\n")
		for _, c := range item.Checklist {
			fmt.Fprintf(&b, "- %s [%s] %s", c.ID, c.Status, c.Text)
			if c.Reason != "" {
				fmt.Fprintf(&b, " (%s)", trimRunes(c.Reason, 160))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("Report each item with task_update, and ask the member that gave the task with task_ask. Both load on demand when you name them in workspace_capabilities.\n[/task record]")
	return trimRunes(b.String(), 1800)
}
