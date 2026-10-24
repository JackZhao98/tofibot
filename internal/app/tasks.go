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

// addChecklistItemsTx appends items the task's creator gave.
func addChecklistItemsTx(tx *sql.Tx, taskID string, texts []string) error {
	return addChecklistItemsAsTx(tx, taskID, texts, false)
}

// addChecklistItemsAsTx appends items. An item the holder adds carries a progress
// mark of the holder: ending it does not restart the tree's idle count, so
// removing and adding items cannot manufacture progress. Any item shows a hidden
// task: a checklist makes it a long task.
func addChecklistItemsAsTx(tx *sql.Tx, taskID string, texts []string, byHolder bool) error {
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
		id := newItemID()
		mark := ""
		if byHolder {
			mark = holderMarkPrefix + id
		}
		if _, err := tx.Exec(`INSERT INTO task_checklist(task_id,item_id,ord,text,status,reason,created_at,updated_at,mark) VALUES(?,?,?,?,?,?,?,?,?)`,
			taskID, id, ord, text, checklistOpen, "", t, t, mark); err != nil {
			return err
		}
	}
	if len(texts) > 0 {
		return promoteTaskVisibleTx(tx, taskID)
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
	item.ClaimedAt = claimedAtTx(q, item.ID, item.BotID)
	_ = q.QueryRow(`SELECT merged_into FROM work_items WHERE id=?`, item.ID).Scan(&item.MergedInto)
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
	} else if r := treeRootForRunTx(tx, child.ParentRun); r != "" {
		// A run that gave tasks before, or that an inbox row woke inside a tree,
		// gives into that tree.
		rootID = r
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
// or the run submitted a result for it with say(task_id). A task the run named
// with task_id comes first; otherwise the oldest.
func taskForRunTx(q queryer, runID string) (WorkItem, bool, error) {
	item, err := scanWorkItem(q.QueryRow(`SELECT `+workItemAllColumns+` FROM work_items WHERE (assignee_run_id=? OR submit_run_id=?) AND status NOT IN ('done','cancelled','failed') ORDER BY (submit_run_id=?) DESC,created_at LIMIT 1`, runID, runID, runID))
	if err == sql.ErrNoRows {
		return WorkItem{}, false, nil
	}
	return item, err == nil, err
}

// tasksForRunTx lists every open task the run is the assignee of or submitted
// a result for. A run that ends settles all of them.
func tasksForRunTx(q queryer, runID string) ([]WorkItem, error) {
	rows, err := q.Query(`SELECT `+workItemAllColumns+` FROM work_items WHERE (assignee_run_id=? OR submit_run_id=?) AND status NOT IN ('done','cancelled','failed') ORDER BY (submit_run_id=?) DESC,created_at,id`, runID, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkItem
	for rows.Next() {
		item, err := scanWorkItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
				// A second run works on the task.
				if err = promoteTaskVisibleTx(tx, id); err != nil {
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
	if !ok && kind.String == runKindFollowup && parent.String != "" {
		// The return run of a delegation carries on the task its Bot held when it
		// handed the work out: the run that delegated ended without closing it.
		var giver sql.NullString
		_ = tx.QueryRow(`SELECT parent_run_id FROM runs WHERE id=?`, parent.String).Scan(&giver)
		if giver.String != "" {
			res, err := tx.Exec(`UPDATE work_items SET assignee_run_id=? WHERE assignee_run_id=? AND bot_id=? AND status NOT IN ('done','cancelled','failed')
 AND (SELECT status FROM runs WHERE id=?) NOT IN ('queued','running','waiting')`, runID, giver.String, bot.String, giver.String)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				item, ok, err = taskForRunTx(tx, runID)
				if err != nil {
					return err
				}
				// A second run works on the task.
				if ok {
					if err = promoteTaskVisibleTx(tx, item.ID); err != nil {
						return err
					}
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
	if err = baselineStallTx(tx, item.ID); err != nil {
		return err
	}
	return taskEventTx(tx, item.ID)
}

// taskRunFinishedTx records a successful run's result on its task and settles
// the waits that were held on it. result is the message the run left.
func taskRunFinishedTx(tx *sql.Tx, done Run, result Message, sayOutcome string) error {
	items, err := tasksForRunTx(tx, done.ID)
	if err != nil {
		return err
	}
	// A run that named a task with say(task_id) settled that task only.
	submitted := false
	for _, item := range items {
		if item.SubmitRunID == done.ID {
			submitted = true
		}
	}
	for _, item := range items {
		if submitted && item.SubmitRunID != done.ID {
			continue
		}
		if err = taskFinishedOneTx(tx, done, item, result, sayOutcome); err != nil {
			return err
		}
	}
	return nil
}

// taskFinishedOneTx settles one task of a run that ended well.
func taskFinishedOneTx(tx *sql.Tx, done Run, item WorkItem, result Message, sayOutcome string) error {
	var err error
	// A run that handed work out and ended holds its own task open: the return
	// run carries it on and gives the real result. Naming the task with
	// say(task_id) is the only way to close it with this run's text.
	if item.SubmitRunID != done.ID {
		// Whether the run delegated at all, and whether a return run has already
		// carried the result back, decides: a child may finish before this run does.
		var given, returned int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM work_items WHERE origin_run_id=?`, done.ID).Scan(&given); err != nil {
			return err
		}
		if given > 0 {
			if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE kind=? AND status='done' AND parent_run_id IN (SELECT assignee_run_id FROM work_items WHERE origin_run_id=?)`, runKindFollowup, done.ID).Scan(&returned); err != nil {
				return err
			}
		}
		if given > 0 && returned == 0 {
			if _, err = tx.Exec(`UPDATE work_items SET submit_run_id='',updated_at=? WHERE id=? AND submit_run_id=?`, now(), item.ID, done.ID); err != nil {
				return err
			}
			return taskEventTx(tx, item.ID)
		}
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
	// A result submitted for the task (done, or in review for the giver) is
	// progress the first time for a spec version; a holder that resubmits the
	// same result each round, or a round that ends still working or blocked, is not.
	if status == "done" || status == "review" {
		if err = progressOnceTx(tx, item.RootTaskID, item.ID, fmt.Sprintf("result:%d", item.SpecVersion)); err != nil {
			return err
		}
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
	if err = supersedeProgressTx(tx, item.ID, done.ID); err != nil {
		return err
	}
	if isClosedWorkStatus(status) {
		if err = settleClosedTaskTx(tx, WorkItem{ID: item.ID, Status: status, RootTaskID: item.RootTaskID}); err != nil {
			return err
		}
	} else {
		// The first round ended with the task still open: it is a long task, not small talk.
		if err = promoteTaskVisibleTx(tx, item.ID); err != nil {
			return err
		}
		if item.AssignerBotID != "" {
			// The round ended with the task still open: it counts toward a stall.
			if err = noteRoundEndedTx(tx, item, done.ID); err != nil {
				return err
			}
		}
	}
	return taskEventTx(tx, item.ID)
}

// taskRunEndedTx handles a run that ended without a result.
func taskRunEndedTx(tx *sql.Tx, runID, status string) error {
	items, err := tasksForRunTx(tx, runID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err = taskEndedOneTx(tx, runID, status, item); err != nil {
			return err
		}
	}
	return nil
}

func taskEndedOneTx(tx *sql.Tx, runID, status string, item WorkItem) error {
	var err error
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
			if _, err = tx.Exec(`UPDATE work_items SET assignee_run_id=? WHERE id=?`, successor, item.ID); err != nil {
				return err
			}
			// A second run works on the task.
			return promoteTaskVisibleTx(tx, item.ID)
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
	if item.Status == "done" {
		// A task that closes done is progress anywhere in its tree.
		if err := resetTreeIdleTx(tx, item.RootTaskID); err != nil {
			return err
		}
	}
	switch item.Status {
	case "done", "failed":
		if err := settleTaskWaitsTx(tx, item.ID, waitFulfilled, ""); err != nil {
			return err
		}
	case "cancelled":
		if err := settleTaskWaitsTx(tx, item.ID, waitCancelled, ""); err != nil {
			return err
		}
		if _, _, err := cancelTaskTreeTx(tx, []string{item.ID}, false); err != nil {
			return err
		}
	}
	// Tasks that waited on this one (decision 0064): done unlocks them, failed or
	// cancelled tells the member that gave each of them.
	switch item.Status {
	case "done":
		return unlockDependentsTx(tx, item.RootTaskID, item.ID)
	case "failed", "cancelled":
		return notifyDependentsEndedTx(tx, item.RootTaskID, item.ID, item.Status)
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
	var cancelledIDs []string
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
		cancelledIDs = append(cancelledIDs, id)
		if err := settleTaskWaitsTx(tx, id, waitCancelled, ""); err != nil {
			return 0, nil, err
		}
		if err := taskEventTx(tx, id); err != nil {
			return 0, nil, err
		}
	}
	// Tasks outside the subtree that waited on a cancelled task stay blocked; their
	// givers are told (a dependent inside the subtree was cancelled above).
	for _, id := range cancelledIDs {
		var root string
		if err := tx.QueryRow(`SELECT root_task_id FROM work_items WHERE id=?`, id).Scan(&root); err != nil {
			continue
		}
		if err := notifyDependentsEndedTx(tx, root, id, "cancelled"); err != nil {
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

// TaskUpdate is one change to a task. Either side, the member that gave the
// task and the member that holds it, may edit it (owner decision 2026-10-24);
// every edit carries a reason. Only the giver answers a question or accepts or
// cancels the task, and only the holder reports its status and the result of an
// item.
type TaskUpdate struct {
	TaskID       string
	Status       string
	Brief        *string
	AddItems     []string
	RemoveItems  []string
	RewriteItems []ChecklistRewrite
	Order        []string
	Items        []ChecklistChange
	Reason       string
	Answer       string
	// Absorb names a task of the same tree that this task takes in.
	Absorb string
	// AddDependsOn and RemoveDependsOn change what a blocked task that has not
	// started waits on (decision 0064).
	AddDependsOn    []string
	RemoveDependsOn []string
}

func (in TaskUpdate) structural() bool {
	return len(in.AddItems) > 0 || len(in.RemoveItems) > 0 || len(in.RewriteItems) > 0 || len(in.Order) > 0 || in.Brief != nil ||
		len(in.AddDependsOn) > 0 || len(in.RemoveDependsOn) > 0 || strings.TrimSpace(in.Absorb) != ""
}

// updateTask applies a TaskUpdate for actor. It reports the change to the
// other side through the inbox and records every edit with its reason.
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
	// Answering a question is the giver's.
	if in.Answer != "" && !creator {
		return WorkItem{}, taskRefusal("task_answer_giver_only", "Only the member that gave the task may answer a question about it.")
	}
	// Item results and the task's own progress belong to the holder.
	if len(in.Items) > 0 || in.Status == "in_progress" || in.Status == "blocked" {
		if !assignee {
			return WorkItem{}, taskRefusal("task_assignee_only", "Only the member that holds the task may report its progress or the result of a checklist item.")
		}
	}
	editReason := strings.TrimSpace(in.Reason)
	if in.structural() {
		if editReason == "" {
			return WorkItem{}, taskRefusal("edit_reason_required", "Every edit of a task carries a reason: the request, the checklist, what the task waits on, or a merge.")
		}
		if utf8.RuneCountInString(editReason) > maxEditReason {
			return WorkItem{}, taskRefusal("edit_reason_too_long", fmt.Sprintf("A reason holds at most %d characters.", maxEditReason))
		}
	}
	var kinds, summary []string
	byHolder := assignee && !creator
	if len(in.AddItems) > 0 {
		if err = addChecklistItemsAsTx(tx, item.ID, in.AddItems, byHolder); err != nil {
			return WorkItem{}, err
		}
		kinds = append(kinds, "add_items")
		summary = append(summary, fmt.Sprintf("Added items: %s.", trimRunes(strings.Join(in.AddItems, "; "), 600)))
	}
	if len(in.RemoveItems) > 0 {
		var gone []string
		for _, id := range in.RemoveItems {
			var text, status string
			if e := tx.QueryRow(`SELECT text,status FROM task_checklist WHERE task_id=? AND item_id=?`, item.ID, id).Scan(&text, &status); e != nil {
				return WorkItem{}, taskRefusal("checklist_item_unknown", "No checklist item has that id: "+id)
			}
			if _, e := tx.Exec(`DELETE FROM task_checklist WHERE task_id=? AND item_id=?`, item.ID, id); e != nil {
				return WorkItem{}, e
			}
			gone = append(gone, fmt.Sprintf("%q (was %s)", trimRunes(text, 120), status))
		}
		kinds = append(kinds, "remove_items")
		summary = append(summary, "Removed items: "+strings.Join(gone, "; ")+".")
	}
	if len(in.RewriteItems) > 0 {
		if err = rewriteChecklistTx(tx, item.ID, in.RewriteItems); err != nil {
			return WorkItem{}, err
		}
		var texts []string
		for _, rw := range in.RewriteItems {
			texts = append(texts, strings.TrimSpace(rw.Text))
		}
		kinds = append(kinds, "rewrite_items")
		summary = append(summary, "Rewrote items: "+trimRunes(strings.Join(texts, "; "), 600)+".")
	}
	if len(in.Order) > 0 {
		if err = reorderChecklistTx(tx, item.ID, in.Order); err != nil {
			return WorkItem{}, err
		}
		kinds = append(kinds, "reorder_items")
		summary = append(summary, "Changed the order of the items.")
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
		var before, text, mark string
		if e := tx.QueryRow(`SELECT status,text,mark FROM task_checklist WHERE task_id=? AND item_id=?`, item.ID, change.ID).Scan(&before, &text, &mark); e != nil {
			return WorkItem{}, taskRefusal("checklist_item_unknown", "No checklist item has that id: "+change.ID)
		}
		if _, e := tx.Exec(`UPDATE task_checklist SET status=?,reason=?,updated_at=? WHERE task_id=? AND item_id=?`, change.Status, reason, t, item.ID, change.ID); e != nil {
			return WorkItem{}, e
		}
		if before == checklistBlocked && change.Status == checklistSkipped {
			// A blocked item that becomes not needed is an edit of the scope: the giver is told.
			kinds = append(kinds, "blocked_to_skipped")
			summary = append(summary, fmt.Sprintf("Item %q was blocked and is now skipped: %s.", trimRunes(text, 120), trimRunes(reason, 200)))
			if editReason == "" {
				editReason = reason
			}
		}
		if change.Status != checklistOpen && !holderOwnedMark(mark) {
			// An item that ends for the first time is progress: the tree's
			// idle-message count restarts. Reopening and ending it again is not,
			// and an item the holder added is not (it could add items to restart the count).
			if err = progressOnceTx(tx, item.RootTaskID, item.ID, itemMarkKey(change.ID, mark)); err != nil {
				return WorkItem{}, err
			}
		}
	}
	if len(in.AddDependsOn) > 0 || len(in.RemoveDependsOn) > 0 {
		list, e := changeDepsTx(tx, item, actor, in.AddDependsOn, in.RemoveDependsOn)
		if e != nil {
			return WorkItem{}, e
		}
		item.DependsOn = list
		kinds = append(kinds, "dependencies")
		summary = append(summary, "Changed what the task waits on; it now waits on: "+list+".")
		open, e := depsOpenTx(tx, parseDeps(list))
		if e != nil {
			return WorkItem{}, e
		}
		if open == 0 {
			// Nothing is left to wait on: the task starts.
			if unlocked, e := unlockTaskTx(tx, item); e != nil {
				return WorkItem{}, e
			} else if unlocked {
				item.Status = "todo"
			}
		}
	}
	if strings.TrimSpace(in.Absorb) != "" {
		if _, err = mergeTasksTx(tx, &item, in.Absorb, actor, editReason); err != nil {
			return WorkItem{}, err
		}
		kinds = append(kinds, "merged_in")
		summary = append(summary, "Absorbed task "+strings.TrimSpace(in.Absorb)+".")
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
			source := "assigner"
			if byHolder {
				source = "holder"
				// The holder wrote this version: its own run is based on it.
				if _, err = tx.Exec(`UPDATE work_items SET run_spec_version=spec_version WHERE id=?`, item.ID); err != nil {
					return WorkItem{}, err
				}
			}
			if err = noteTaskRevisionTx(tx, item, source, actor, brief, editReason); err != nil {
				return WorkItem{}, err
			}
			kinds = append(kinds, "request")
			if byHolder {
				summary = append(summary, fmt.Sprintf("The request changed. It is now version %d: %s", item.SpecVersion, trimRunes(brief, 1200)))
			} else if item.BotID != actor {
				// The holder is woken: it works on a request that changed.
				if err = addInboxTx(tx, InboxItem{BotID: item.BotID, ConversationID: item.WorkConversationOrConversation(), Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID,
					Body: fmt.Sprintf("The request for task %q (%s) changed. It is now version %d. Reason: %s The request: %s", item.Title, item.ID, item.SpecVersion, trimRunes(editReason, maxEditReason), trimRunes(brief, 1200)), Wakes: true, AfterRun: ""}); err != nil {
					return WorkItem{}, err
				}
			}
			message = creator
		}
	}
	if len(kinds) > 0 {
		if err = recordTaskEditTx(tx, item, actor, editRole(item, actor), kinds, editReason, strings.Join(summary, " ")); err != nil {
			return WorkItem{}, err
		}
		if err = editNoticeTx(tx, item, actor, summary, editReason); err != nil {
			return WorkItem{}, err
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
		// The giver's answer to a question always passes: the holder is waiting for it.
		if err = bumpTaskMessagesTx(tx, item.RootTaskID, in.Answer == "" || in.Brief != nil); err != nil {
			tx.Rollback()
			s.afterTreeLimit(err)
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

func noteTaskRevisionTx(tx *sql.Tx, item WorkItem, source, actor, text, reason string) error {
	_, err := tx.Exec(`INSERT OR REPLACE INTO task_revisions(task_id,version,source,actor,text,created_at,reason) VALUES(?,?,?,?,?,?,?)`, item.ID, item.SpecVersion, source, actor, trimRunes(text, maxTaskBriefRunes), now(), trimRunes(reason, maxEditReason))
	return err
}

// bumpTaskMessagesTx counts a message against the tree's budget (decision 0064)
// and refuses past the limit, so two members cannot talk forever. The caller
// rolls back and calls afterTreeLimit.
func bumpTaskMessagesTx(tx *sql.Tx, root string, block bool) error {
	return chargeTreeTx(tx, root, 1, block)
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
	if err = bumpTaskMessagesTx(tx, item.RootTaskID, true); err != nil {
		tx.Rollback()
		s.afterTreeLimit(err)
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
		if err = noteTaskRevisionTx(tx, item, "person", "", message.Content, ""); err != nil {
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
	if item.AssignerKind == "user" && item.Title != "" && item.Title != firstLine(item.Description, 80) {
		fmt.Fprintf(&b, "Brief of the request, resolved from the conversation: %s\nThe person's own words and the earlier turns are in the transcript.\n", item.Title)
	}
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
	if len(item.Checklist) > 0 || item.AssignerBotID != "" {
		b.WriteString("Report each item with task_update, and ask the member that gave the task with task_ask. Both load on demand when you name them in workspace_capabilities.\n")
	}
	var kind string
	if s.db.QueryRow(`SELECT kind FROM conversations WHERE id=?`, item.ConversationID).Scan(&kind) == nil && kind == "group" && item.WorkConversationOrConversation() == item.ConversationID {
		b.WriteString("If the task does not fit your role, pass it to the member that fits; if it fits and you will work on it, claim it. claim_task and pass_task load on demand the same way.\n")
	}
	b.WriteString("[/task record]")
	return trimRunes(b.String(), 1800)
}
