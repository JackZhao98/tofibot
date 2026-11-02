package app

// The task card of a lead is its plan (divide and conquer, line A). Each task the
// lead gives becomes an item of the checklist of the task it holds, marked
// "p:<child task id>". The system keeps the item true: it ends when the child
// ends, and it is blocked when the child fails, is cancelled or misses its
// deadline. The lead does not report these items; the card shows them.

import (
	"database/sql"
	"strings"
)

const planMarkPrefix = "p:"

func planMark(childID string) string { return planMarkPrefix + childID }

// planChildID reads the child task id from the mark of a plan item.
func planChildID(mark string) string {
	if strings.HasPrefix(mark, planMarkPrefix) {
		return strings.TrimPrefix(mark, planMarkPrefix)
	}
	return ""
}

// addPlanItemTx adds the plan item of a task to the checklist of its parent. A
// task with no parent, a closed parent, a full checklist or an item already
// there changes nothing.
func addPlanItemTx(tx *sql.Tx, childID string) error {
	var title, botName, parent string
	if err := tx.QueryRow(`SELECT w.title,b.name,w.parent_task_id FROM work_items w JOIN bots b ON b.id=w.bot_id WHERE w.id=?`, childID).Scan(&title, &botName, &parent); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if parent == "" {
		return nil
	}
	var parentStatus string
	if tx.QueryRow(`SELECT status FROM work_items WHERE id=?`, parent).Scan(&parentStatus) != nil || isClosedWorkStatus(parentStatus) {
		return nil
	}
	var have, ord int
	if err := tx.QueryRow(`SELECT COUNT(*),COALESCE(MAX(ord),0) FROM task_checklist WHERE task_id=?`, parent).Scan(&have, &ord); err != nil {
		return err
	}
	if have >= maxChecklistItems {
		return nil
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM task_checklist WHERE task_id=? AND mark=?`, parent, planMark(childID)).Scan(&exists); err != nil || exists > 0 {
		return err
	}
	text := trimRunes(strings.TrimSpace(title)+" · "+botName, maxChecklistTextRunes)
	t := now()
	if _, err := tx.Exec(`INSERT INTO task_checklist(task_id,item_id,ord,text,status,reason,created_at,updated_at,mark) VALUES(?,?,?,?,?,?,?,?,?)`,
		parent, newItemID(), ord+1, text, checklistOpen, "", t, t, planMark(childID)); err != nil {
		return err
	}
	if err := promoteTaskVisibleTx(tx, parent); err != nil {
		return err
	}
	return taskEventTx(tx, parent)
}

// syncPlanItemTx makes the plan item of a task follow the task: done when the
// task is done; blocked, with the reason, when it failed or was cancelled. A
// late result still ends a timed-out item. An item the lead ended itself stays.
func syncPlanItemTx(tx *sql.Tx, item WorkItem) error {
	var parent string
	if tx.QueryRow(`SELECT parent_task_id FROM work_items WHERE id=?`, item.ID).Scan(&parent) != nil || parent == "" {
		return nil
	}
	status, reason := "", ""
	switch item.Status {
	case "done":
		status = checklistDone
		// A result that comes after the deadline is recorded as late on the card.
		var late int
		if tx.QueryRow(`SELECT COUNT(*) FROM task_checklist WHERE task_id=? AND mark=? AND status=? AND reason='timed out'`, parent, planMark(item.ID), checklistBlocked).Scan(&late) == nil && late > 0 {
			reason = "late"
		}
	case "failed":
		status, reason = checklistBlocked, "failed"
	case "cancelled":
		status, reason = checklistBlocked, "cancelled"
	default:
		return nil
	}
	return setPlanItemTx(tx, parent, item.ID, status, reason, []string{checklistOpen, checklistBlocked})
}

// planItemTimedOutTx marks the plan item of a part whose deadline passed.
func planItemTimedOutTx(tx *sql.Tx, childID string) error {
	var parent, status string
	if tx.QueryRow(`SELECT parent_task_id,status FROM work_items WHERE id=?`, childID).Scan(&parent, &status) != nil || parent == "" || isClosedWorkStatus(status) {
		return nil
	}
	return setPlanItemTx(tx, parent, childID, checklistBlocked, "timed out", []string{checklistOpen})
}

func setPlanItemTx(tx *sql.Tx, parent, childID, status, reason string, from []string) error {
	res, err := tx.Exec(`UPDATE task_checklist SET status=?,reason=?,updated_at=? WHERE task_id=? AND mark=? AND status IN (`+placeholders(len(from))+`)`,
		append([]any{status, reason, now(), parent, planMark(childID)}, stringsToAny(from)...)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return taskEventTx(tx, parent)
}

func placeholders(n int) string {
	if n <= 0 {
		return "''"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringsToAny(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

// planPartView adds to a plan item what the card shows of the task behind it.
func planPartView(q queryer, item *ChecklistItem) {
	id := planChildID(item.Mark)
	if id == "" {
		return
	}
	item.TaskID = id
	var botID, botName, status, deadline, progress string
	if q.QueryRow(`SELECT w.bot_id,b.name,w.status,w.deadline_at,w.progress_line FROM work_items w JOIN bots b ON b.id=w.bot_id WHERE w.id=?`, id).Scan(&botID, &botName, &status, &deadline, &progress) != nil {
		return
	}
	item.BotID, item.BotName, item.TaskStatus, item.DeadlineAt, item.ProgressLine = botID, botName, status, deadline, progress
}
