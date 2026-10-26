package app

// Task dependencies (decision 0064). A task can depend on other tasks of the
// same tree. While one of them is open the task is blocked: it has no run and
// no holder activity. When the last one ends done, the task goes to todo in the
// same transaction and its holder is woken through the inbox. When one ends
// failed or cancelled, the member that gave the task is told, and the task
// stays blocked. The list is the `depends_on` column (task ids, comma
// separated); an unlocked task keeps it as the record.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	maxTaskDeps  = 12
	maxDepsSteps = 256

	// blockedWaitCeiling is the hard deadline of the giver's wait on a task that
	// still waits on dependencies; the usual task-result deadline starts when it unlocks.
	blockedWaitCeiling = 24 * time.Hour
)

func parseDeps(list string) []string {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cleanDepIDs(ids []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// dependencyCycleTx reports whether making taskID depend on deps would close a
// loop: whether taskID is reachable from a dependency through the dependencies
// already stored. The walk is bounded; a graph too large to walk is refused,
// because a loop cannot be ruled out (the rule of the wait cycle check).
func dependencyCycleTx(q queryer, taskID string, deps []string) (bool, error) {
	seen := map[string]bool{}
	queue := append([]string(nil), deps...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == taskID {
			return true, nil
		}
		if seen[id] {
			continue
		}
		// Only a node not seen before costs a step, so a wide graph that shares
		// nodes is walked in full.
		if len(seen) >= maxDepsSteps {
			return true, nil
		}
		seen[id] = true
		var list string
		if err := q.QueryRow(`SELECT depends_on FROM work_items WHERE id=?`, id).Scan(&list); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return false, err
		}
		queue = append(queue, parseDeps(list)...)
	}
	return false, nil
}

// checkDepsTx validates the dependencies a member names for a new or changed
// task: each exists, is visible to the member, has not failed or been cancelled,
// and all of them are in one tree. It returns that tree's root and the parent
// task a new sibling takes.
func checkDepsTx(tx *sql.Tx, giver, conv string, deps []string) (root, parent string, err error) {
	if len(deps) == 0 {
		return "", "", nil
	}
	if len(deps) > maxTaskDeps {
		return "", "", taskRefusal("dependency_too_many", fmt.Sprintf("A task depends on at most %d tasks.", maxTaskDeps))
	}
	for i, id := range deps {
		dep, e := getTaskTx(tx, id)
		if e == sql.ErrNoRows {
			return "", "", taskRefusal("dependency_not_found", "No task has the id "+id+".")
		}
		if e != nil {
			return "", "", e
		}
		if dep.AssignerBotID != giver && dep.ConversationID != conv && dep.WorkConversationID != conv {
			return "", "", taskRefusal("dependency_not_visible", "You can depend only on a task you gave or one in this conversation.")
		}
		if dep.Status == "failed" || dep.Status == "cancelled" {
			return "", "", taskRefusal("dependency_ended", "The task "+id+" ended as "+dep.Status+". Depend on work that can still finish.")
		}
		if i == 0 {
			root, parent = dep.RootTaskID, dep.ParentTaskID
		} else if dep.RootTaskID != root {
			return "", "", taskRefusal("dependency_other_tree", "The tasks a task depends on must belong to one tree.")
		}
	}
	return root, parent, nil
}

// depsOpenTx counts the dependencies of a list that are not done.
func depsOpenTx(q queryer, deps []string) (int, error) {
	open := 0
	for _, id := range deps {
		var status string
		err := q.QueryRow(`SELECT status FROM work_items WHERE id=?`, id).Scan(&status)
		if err == sql.ErrNoRows {
			open++ // a dependency that is gone is not done
			continue
		}
		if err != nil {
			return 0, err
		}
		if status != "done" {
			open++
		}
	}
	return open, nil
}

// dependentTasksTx lists the unstarted blocked tasks of a tree that name taskID.
func dependentTasksTx(tx *sql.Tx, root, taskID string) ([]WorkItem, error) {
	ids, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE root_task_id=? AND status='blocked' AND assignee_run_id='' AND depends_on<>'' AND (','||depends_on||',') LIKE ? ORDER BY created_at,id`, root, "%,"+taskID+",%")
	if err != nil {
		return nil, err
	}
	var out []WorkItem
	for _, id := range ids {
		item, err := getTaskTx(tx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

// unlockTaskTx moves an unstarted blocked task to todo and wakes its holder
// with the brief and what the tasks it waited on returned. It reports whether
// this call unlocked it.
func unlockTaskTx(tx *sql.Tx, item WorkItem) (bool, error) {
	res, err := tx.Exec(`UPDATE work_items SET status='todo',updated_at=? WHERE id=? AND status='blocked' AND assignee_run_id=''`, now(), item.ID)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	// The giver's wait on the task runs its usual deadline from now, not from when it was made.
	if _, err = tx.Exec(`UPDATE waits SET deadline_at=? WHERE kind=? AND target=? AND status='pending'`, stampTime(time.Now().Add(waitTimeoutFor(waitTaskResult))), waitTaskResult, item.ID); err != nil {
		return false, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task %q (%s) is unblocked: every task it waited on is done. It is yours now.", item.Title, item.ID)
	if item.AssignerBotID != "" {
		var giver string
		_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, item.AssignerBotID).Scan(&giver)
		fmt.Fprintf(&b, " It was given by %s (id=%s).", giver, item.AssignerBotID)
	}
	b.WriteString("\nRequest: " + trimRunes(item.Description, 1800))
	for _, id := range parseDeps(item.DependsOn) {
		var title, summary string
		if tx.QueryRow(`SELECT title,result_summary FROM work_items WHERE id=?`, id).Scan(&title, &summary) == nil {
			fmt.Fprintf(&b, "\nResult of %q (%s): %s", title, id, trimRunes(strings.TrimSpace(summary), 300))
		}
	}
	fmt.Fprintf(&b, "\nDo the work, then deliver the result with say and task_id %s.", item.ID)
	if err = addInboxTx(tx, InboxItem{BotID: item.BotID, ConversationID: item.WorkConversationOrConversation(), Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID,
		Body: trimRunes(b.String(), 3000), Wakes: true}); err != nil {
		return false, err
	}
	return true, taskEventTx(tx, item.ID)
}

// unlockDependentsTx runs when a task ends done: each task that waited on it
// and has no other open dependency is unlocked.
func unlockDependentsTx(tx *sql.Tx, root, doneID string) error {
	items, err := dependentTasksTx(tx, root, doneID)
	if err != nil {
		return err
	}
	for _, item := range items {
		open, err := depsOpenTx(tx, parseDeps(item.DependsOn))
		if err != nil {
			return err
		}
		if open > 0 {
			continue
		}
		if _, err = unlockTaskTx(tx, item); err != nil {
			return err
		}
	}
	return nil
}

// notifyDependentsEndedTx runs when a task ends failed or cancelled: the member
// that gave each waiting task is told, and the task stays blocked.
func notifyDependentsEndedTx(tx *sql.Tx, root, endedID, status string) error {
	items, err := dependentTasksTx(tx, root, endedID)
	if err != nil {
		return err
	}
	var title string
	_ = tx.QueryRow(`SELECT title FROM work_items WHERE id=?`, endedID).Scan(&title)
	for _, item := range items {
		if item.AssignerBotID == "" {
			continue
		}
		body := fmt.Sprintf("Task %q (%s) ended as %s. Task %q (%s) waits on it and stays blocked; it did not start. Decide: cancel it, remove that dependency with task_update when the work is not needed, or give the missing work again and depend on the new task. task_update loads on demand when you name it in workspace_capabilities.",
			title, endedID, status, item.Title, item.ID)
		if err = addInboxTx(tx, InboxItem{BotID: item.AssignerBotID, ConversationID: item.ConversationID, Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID, Body: body, Wakes: true, NeedsReply: true}); err != nil {
			return err
		}
	}
	return nil
}

// changeDepsTx applies a giver's change of the dependencies of an unstarted
// blocked task, with the cycle check. It reports the new list and whether the
// task has no open dependency left.
func changeDepsTx(tx *sql.Tx, item WorkItem, giver string, add, remove []string) (string, error) {
	if item.Status != "blocked" || item.AssigneeRunID != "" {
		return "", taskRefusal("dependency_locked", "Only a blocked task that has not started can change what it waits on.")
	}
	current := parseDeps(item.DependsOn)
	drop := map[string]bool{}
	for _, id := range cleanDepIDs(remove) {
		drop[id] = true
	}
	var next []string
	have := map[string]bool{}
	for _, id := range current {
		if !drop[id] {
			next = append(next, id)
			have[id] = true
		}
	}
	add = cleanDepIDs(add)
	var fresh []string
	for _, id := range add {
		if id == item.ID {
			return "", taskRefusal("dependency_cycle", "A task cannot depend on itself.")
		}
		if !have[id] {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) > 0 {
		root, _, err := checkDepsTx(tx, giver, item.ConversationID, fresh)
		if err != nil {
			return "", err
		}
		if root != item.RootTaskID {
			return "", taskRefusal("dependency_other_tree", "A task depends only on tasks of its own tree.")
		}
		cycle, err := dependencyCycleTx(tx, item.ID, fresh)
		if err != nil {
			return "", err
		}
		if cycle {
			return "", taskRefusal("dependency_cycle", "This dependency would close a cycle: a task it waits on already waits, directly or through others, for this task. Remove that dependency first.")
		}
		next = append(next, fresh...)
	}
	if len(next) > maxTaskDeps {
		return "", taskRefusal("dependency_too_many", fmt.Sprintf("A task depends on at most %d tasks.", maxTaskDeps))
	}
	list := strings.Join(next, ",")
	if _, err := tx.Exec(`UPDATE work_items SET depends_on=?,updated_at=? WHERE id=?`, list, now(), item.ID); err != nil {
		return "", err
	}
	return list, nil
}

// unlockRowSQL selects the inbox rows that start an unlocked task. Only a woken
// run reads them (wakeBotFromInbox binds one task to it): a boundary read or a
// context read leaves them alone, so a task never binds to a run that did not
// start for it.
const unlockRowSQL = `(kind='task_update' AND task_id<>'' AND EXISTS(SELECT 1 FROM work_items w WHERE w.id=bot_inbox.task_id AND w.bot_id=bot_inbox.bot_id AND w.status='todo' AND w.assignee_run_id=''))`

// bindUnlockedTasksTx hands the tasks that rows unlocked to the run that reads
// them: the run becomes their assignee. started says the run is already
// working, so the task is in progress at once.
func bindUnlockedTasksTx(tx *sql.Tx, botID, runID string, rows []inboxRow, started bool) error {
	for _, row := range rows {
		if row.Kind != inboxTaskUpdate || row.TaskID == "" {
			continue
		}
		status := "todo"
		if started {
			status = "in_progress"
		}
		res, err := tx.Exec(`UPDATE work_items SET assignee_run_id=?,status=?,run_spec_version=spec_version,updated_at=? WHERE id=? AND bot_id=? AND assignee_run_id='' AND status='todo'`, runID, status, now(), row.TaskID, botID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			if err = deliverTaskAttachmentsTx(tx, row.TaskID, runID); err != nil {
				return err
			}
			if err = taskEventTx(tx, row.TaskID); err != nil {
				return err
			}
		}
	}
	return nil
}

// deliverTaskAttachmentsTx puts the files a waiting task carries where its run
// reads: when the run's conversation cannot see them yet, a brief message of
// the giver, bound to them, is posted there (a visible group already holds the
// "@name brief" message that carries them).
func deliverTaskAttachmentsTx(tx *sql.Tx, taskID, runID string) error {
	rows, err := tx.Query(`SELECT attachment_id FROM task_attachments WHERE task_id=? ORDER BY attachment_id`, taskID)
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
	rows.Close()
	if err = rows.Err(); err != nil || len(ids) == 0 {
		return err
	}
	var conv, giver, brief, bot string
	if err = tx.QueryRow(`SELECT assigner_bot_id,description,bot_id FROM work_items WHERE id=?`, taskID).Scan(&giver, &brief, &bot); err != nil {
		return err
	}
	if err = tx.QueryRow(`SELECT conversation_id FROM runs WHERE id=?`, runID).Scan(&conv); err != nil {
		return err
	}
	var missing []string
	for _, id := range ids {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM attachments a WHERE a.id=? AND (a.conversation_id=? OR EXISTS(
	 SELECT 1 FROM attachment_messages am JOIN messages m ON m.id=am.message_id WHERE am.attachment_id=a.id AND m.conversation_id=?))`, id, conv, conv).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return err
	}
	t := now()
	notice := &HandoffNotice{Type: "handoff", FromBotID: giver, ToBotID: bot, TargetConversationID: conv, TargetRunID: runID}
	noticeData, _ := json.Marshal(notice)
	m := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: giver, RunID: runID, Content: brief, Notice: notice, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, conv, seq, m.Role, m.Kind, giver, runID, brief, string(noticeData), t); err != nil {
		return err
	}
	if err = bindDeliveredTx(tx, &m, missing); err != nil {
		return err
	}
	return insertEventTx(tx, conv, "message", m, t)
}

// AddDependentTasks creates the tasks of a delegation that waits on other
// tasks: one per recipient, with no run until they unlock. A task with no
// dependency, or whose dependencies are all done, starts at once through the
// inbox; this is how a run gives a second task after it used its one hand-over,
// so independent parts run in parallel. The giver holds the wait for each
// result, delivered through the inbox when the task ends.
func (s *Store) AddDependentTasks(conv, giver, parentRun string, recipients []string, brief, title string, checklist, deps []string, attachmentIDs ...string) ([]delegatedTaskReceipt, error) {
	receipts, err := s.addDependentTasks(conv, giver, parentRun, recipients, brief, title, checklist, deps, attachmentIDs)
	s.afterTreeLimit(err)
	return receipts, err
}

func (s *Store) addDependentTasks(conv, giver, parentRun string, recipients []string, brief, title string, checklist, deps []string, attachmentIDs []string) ([]delegatedTaskReceipt, error) {
	deps = cleanDepIDs(deps)
	brief = strings.TrimSpace(brief)
	title = strings.TrimSpace(title)
	if brief == "" || len(recipients) == 0 {
		return nil, taskRefusal("dependency_invalid", "A task needs a brief and a recipient.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var parentConv, parentStatus string
	if err = tx.QueryRow(`SELECT conversation_id,status FROM runs WHERE id=?`, parentRun).Scan(&parentConv, &parentStatus); err != nil {
		return nil, err
	}
	if parentConv != conv || parentStatus != "running" {
		return nil, taskRefusal("delegate_run_inactive", "The run that gives the task is not active in this conversation.")
	}
	var convKind string
	if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, conv).Scan(&convKind); err != nil {
		return nil, err
	}
	root, depParent, err := checkDepsTx(tx, giver, conv, deps)
	if err != nil {
		return nil, err
	}
	// A task made inside a tree belongs to that tree; the tasks it waits on must too.
	var anchorRoot, anchorID string
	aerr := tx.QueryRow(`WITH RECURSIVE anc(id,depth) AS (
 SELECT ?,0 UNION ALL SELECT r.parent_run_id,a.depth+1 FROM runs r JOIN anc a ON r.id=a.id WHERE COALESCE(r.parent_run_id,'')<>'' AND a.depth<64
 ) SELECT w.id,w.root_task_id FROM work_items w JOIN anc ON anc.id=w.assignee_run_id ORDER BY anc.depth LIMIT 1`, parentRun).Scan(&anchorID, &anchorRoot)
	if aerr != nil && aerr != sql.ErrNoRows {
		return nil, aerr
	}
	parent := depParent
	if aerr == nil {
		if root != "" && anchorRoot != root {
			return nil, taskRefusal("dependency_other_tree", "A task you give from inside a tree can wait only on tasks of that tree.")
		}
		root, parent = anchorRoot, anchorID
	}
	if tr := treeRootForRunTx(tx, parentRun); tr != "" {
		// The run's tree, by the one resolver: the tasks it gave, or the rows that woke it.
		if root != "" && root != tr {
			return nil, taskRefusal("dependency_other_tree", "A task you give from inside a tree can wait only on tasks of that tree.")
		}
		root = tr
	}
	if root == "" {
		// A task given beside the tasks this run already gave joins their tree.
		var r2, p2 string
		if tx.QueryRow(`SELECT root_task_id,parent_task_id FROM work_items WHERE origin_run_id=? AND root_task_id<>'' ORDER BY created_at LIMIT 1`, parentRun).Scan(&r2, &p2) == nil {
			root, parent = r2, p2
		}
	}
	open, err := depsOpenTx(tx, deps)
	if err != nil {
		return nil, err
	}
	status := "blocked"
	if open == 0 {
		status = "todo"
	}
	if utf8Len(title) > 240 {
		title = string([]rune(title)[:240])
	}
	if title == "" {
		title = firstLine(brief, 80)
	}
	if title == "" {
		title = "Task"
	}
	var receipts []delegatedTaskReceipt
	seen := map[string]bool{}
	for _, bot := range recipients {
		bot = strings.TrimSpace(bot)
		if bot == "" || seen[bot] {
			continue
		}
		seen[bot] = true
		if bot == giver {
			return nil, taskRefusal("delegate_self", "A task goes to another member.")
		}
		workConv := conv
		if convKind == "group" {
			if err = requireCurrentMemberTx(tx, conv, bot); err != nil {
				return nil, taskRefusal("delegate_not_member", "That Bot is not a member of this group.")
			}
		} else {
			if err = requireActiveBotTx(tx, bot); err != nil {
				return nil, taskRefusal("delegate_bot_unknown", "No active Bot has that id.")
			}
			if err = tx.QueryRow(`SELECT dm_conversation_id FROM bots WHERE id=?`, bot).Scan(&workConv); err != nil {
				return nil, err
			}
		}
		id := newID()
		if root == "" {
			root = id // the first task of a new tree is its root
		}
		// A task with a new id cannot be on a loop, so no cycle walk here: the
		// walk guards task_update, which changes the lists of tasks that exist.
		t := now()
		if _, err = tx.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,parent_task_id,origin_run_id,assignee_run_id,work_conversation_id,spec_version,depends_on)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, conv, bot, "task", title, trimRunes(brief, maxTaskBriefRunes), status, nil, t, t, "",
			"bot", giver, root, parent, parentRun, "", workConv, 1, strings.Join(deps, ",")); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(`INSERT INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, id, 1, "assigner", giver, trimRunes(brief, maxTaskBriefRunes), t); err != nil {
			return nil, err
		}
		if len(checklist) > 0 {
			if err = addChecklistItemsTx(tx, id, checklist); err != nil {
				return nil, err
			}
		}
		for _, att := range attachmentIDs {
			if _, err = tx.Exec(`INSERT OR IGNORE INTO task_attachments(task_id,attachment_id) VALUES(?,?)`, id, att); err != nil {
				return nil, err
			}
		}
		if err = chargeTreeTx(tx, treeKeyForRunTx(tx, parentRun), 1, true); err != nil {
			return nil, err
		}
		// The parts of one hand-out share a wait group (mode all): the giver is woken
		// once, after the last of them, so the person gets one return.
		group := ""
		_ = tx.QueryRow(`SELECT group_id FROM waits WHERE bot_id=? AND run_id=? AND kind=? AND delivery=? AND status='pending' AND group_mode=? ORDER BY created_at LIMIT 1`, giver, parentRun, waitTaskResult, deliveryEngine, waitModeAll).Scan(&group)
		if _, err = recordWaitTx(tx, WaitSpec{BotID: giver, RunID: parentRun, ConversationID: conv, Kind: waitTaskResult, Target: id, Timeout: blockedWaitCeiling, RootTaskID: root, SkipCycle: true, GroupID: group, Mode: waitModeAll}); err != nil {
			return nil, err
		}
		item, err := getTaskTx(tx, id)
		if err != nil {
			return nil, err
		}
		if status == "todo" {
			// Every dependency was done already: the holder starts now. The row
			// is written as blocked first so the one unlock path wakes it.
			if _, err = tx.Exec(`UPDATE work_items SET status='blocked' WHERE id=?`, id); err != nil {
				return nil, err
			}
			item.Status = "blocked"
			if _, err = unlockTaskTx(tx, item); err != nil {
				return nil, err
			}
		} else if err = taskEventTx(tx, id); err != nil {
			return nil, err
		}
		item, err = getTaskTx(tx, id)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, delegatedTaskReceipt{TaskID: id, BotID: bot, Status: item.Status, Version: item.SpecVersion, BlockedBy: deps})
	}
	return receipts, tx.Commit()
}

func utf8Len(s string) int { return len([]rune(s)) }
