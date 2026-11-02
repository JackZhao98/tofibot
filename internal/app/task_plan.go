package app

// The task card of a lead is its plan (divide and conquer, line A). Each task the
// lead gives becomes an item of the checklist of the task it holds, marked
// "p:<child task id>". The system keeps the item true: it ends when the child
// ends, and it is blocked when the child fails, is cancelled or misses its
// deadline. The lead does not report these items; the card shows them.

import (
	"database/sql"
	"strings"
	"time"
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

// planPartViews adds to the plan items what the card shows of the tasks behind
// them, in one query for all of them.
func planPartViews(q queryer, items []ChecklistItem) {
	byTask := map[string][]int{}
	var args []any
	for i := range items {
		id := planChildID(items[i].Mark)
		if id == "" {
			continue
		}
		items[i].TaskID = id
		if _, seen := byTask[id]; !seen {
			args = append(args, id)
		}
		byTask[id] = append(byTask[id], i)
	}
	if len(args) == 0 {
		return
	}
	rows, err := q.Query(`SELECT w.id,w.bot_id,b.name,w.status,w.deadline_at,w.progress_line FROM work_items w JOIN bots b ON b.id=w.bot_id WHERE w.id IN (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id, botID, botName, status, deadline, progress string
		if rows.Scan(&id, &botID, &botName, &status, &deadline, &progress) != nil {
			continue
		}
		for _, i := range byTask[id] {
			items[i].BotID, items[i].BotName, items[i].TaskStatus, items[i].DeadlineAt, items[i].ProgressLine = botID, botName, status, deadline, progress
		}
	}
}

// ensureLeadTaskTx gives a run that delegates while it holds no task a root
// task, so the parts become the checklist of one plan card. The Bot the person
// addressed is the lead: the task is held by the run's Bot, given by the
// person, in the run's conversation, titled from the request that started the
// run (the title of the first part when the run has no such request). It is
// idempotent per run: once the run holds the task, the ancestor walk of the
// callers finds it. The task closes by the settle rules of any task: when the
// lead delivers its answer.
func ensureLeadTaskTx(tx *sql.Tx, conv, bot, runID, fallbackTitle string) (string, error) {
	var trigger string
	if err := tx.QueryRow(`SELECT COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, runID).Scan(&trigger); err != nil {
		return "", err
	}
	var content string
	if trigger != "" {
		_ = tx.QueryRow(`SELECT content FROM messages WHERE id=?`, trigger).Scan(&content)
	}
	content = strings.TrimSpace(content)
	title := firstLine(content, 80)
	if title == "" {
		title = firstLine(fallbackTitle, 80)
	}
	if title == "" {
		title = "Task"
	}
	id, t := newID(), now()
	if _, err := tx.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,parent_task_id,origin_run_id,assignee_run_id,work_conversation_id,spec_version)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, conv, bot, "task", title, trimRunes(content, maxTaskBriefRunes), "in_progress", nil, t, t, "",
		"user", "", id, "", "", runID, conv, 1); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, id, 1, "person", "", trimRunes(content, maxTaskBriefRunes), t); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE work_items SET run_spec_version=spec_version,source=? WHERE id=?`, taskSourceLead, id); err != nil {
		return "", err
	}
	if err := baselineStallTx(tx, id); err != nil {
		return "", err
	}
	return id, nil
}

// taskSourceLead marks the root task ensureLeadTaskTx made: no person or Bot
// gave it, the run's own delegation did.
const taskSourceLead = "lead_plan"

// leadRootIdleAfter is how long a lead's root task may sit with nothing active
// before the cleanup closes it.
const leadRootIdleAfter = 10 * time.Minute

// closeIdleLeadRoots closes the root tasks made for leads that nothing works on
// any more: no active run of the lead, no open part below, no pending wait or
// wake of the lead in the conversation. The plan ends done; the parts carry
// their own results. It is bounded to 100 roots per call.
func (s *Store) closeIdleLeadRoots(at time.Time) error {
	ids, err := waitIDsTx(s.db, `SELECT w.id FROM work_items w WHERE w.source=? AND w.parent_task_id='' AND w.status IN (`+openTaskStatuses+`) AND w.updated_at<?
 AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.id=w.assignee_run_id AND r.status IN ('queued','running','waiting'))
 AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.bot_id=w.bot_id AND r.conversation_id=w.conversation_id AND r.status IN ('queued','running','waiting'))
 AND NOT EXISTS(SELECT 1 FROM work_items c WHERE c.parent_task_id=w.id AND c.status NOT IN ('done','cancelled','failed'))
 AND NOT EXISTS(SELECT 1 FROM waits p WHERE p.bot_id=w.bot_id AND p.conversation_id=w.conversation_id AND p.status='pending')
 AND NOT EXISTS(SELECT 1 FROM bot_inbox i WHERE i.bot_id=w.bot_id AND i.conversation_id=w.conversation_id AND i.wakes=1 AND i.consumed_at='')
 ORDER BY w.updated_at LIMIT 100`, taskSourceLead, stampTime(at.Add(-leadRootIdleAfter)))
	if err != nil {
		return err
	}
	for _, id := range ids {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		item, err := getTaskTx(tx, id)
		if err != nil || isClosedWorkStatus(item.Status) {
			tx.Rollback()
			continue
		}
		t := now()
		if _, err = tx.Exec(`UPDATE work_items SET status='done',updated_at=?,completed_at=?,closed_at=?,assignee_run_id='',submit_run_id='' WHERE id=?`, t, t, t, id); err != nil {
			tx.Rollback()
			return err
		}
		item.Status = "done"
		if err = settleClosedTaskTx(tx, item); err == nil {
			err = taskEventTx(tx, id)
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// isLeadRootTx is true for a root task ensureLeadTaskTx made.
func isLeadRootTx(q queryer, item WorkItem) bool {
	if item.ParentTaskID != "" {
		return false
	}
	var src string
	_ = q.QueryRow(`SELECT source FROM work_items WHERE id=?`, item.ID).Scan(&src)
	return src == taskSourceLead
}
