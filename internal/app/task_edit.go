package app

// Dynamic task editing and hidden small tasks (owner decisions 2026-10-24,
// docs/decisions/0059 and 0060 notes). Whoever holds a task may edit it, and so
// may the member that gave it. Every edit carries a reason, is recorded in
// task_edits, and the other side is told through the inbox. A task that group
// triage made for a person's message stays hidden until it shows itself to be a
// long task (promoteTaskVisibleTx).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// holderMarkPrefix marks a checklist item that the holder added. Ending such an
	// item is not progress for the tree's idle count: the holder could remove and
	// add items to restart the count.
	holderMarkPrefix = "h:"
	maxEditReason    = 400
)

// ChecklistRewrite changes the text of one checklist item.
type ChecklistRewrite struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

func ensureTaskEditTables(db *sql.DB) error {
	if err := ensureColumn(db, "work_items", "hidden", `ALTER TABLE work_items ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureColumn(db, "work_items", "merged_into", `ALTER TABLE work_items ADD COLUMN merged_into TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(db, "task_checklist", "mark", `ALTER TABLE task_checklist ADD COLUMN mark TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := ensureColumn(db, "task_revisions", "reason", `ALTER TABLE task_revisions ADD COLUMN reason TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS task_edits(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 task_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
 version INTEGER NOT NULL,
 actor TEXT NOT NULL,
 role TEXT NOT NULL,
 kinds TEXT NOT NULL,
 reason TEXT NOT NULL,
 detail TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS task_edits_task ON task_edits(task_id,id);`)
	return err
}

// recordTaskEditTx writes one edit to the log.
func recordTaskEditTx(tx *sql.Tx, item WorkItem, actor, role string, kinds []string, reason, detail string) error {
	_, err := tx.Exec(`INSERT INTO task_edits(task_id,version,actor,role,kinds,reason,detail,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		item.ID, item.SpecVersion, actor, role, strings.Join(kinds, ","), trimRunes(reason, maxEditReason), trimRunes(detail, 2000), now())
	return err
}

// promoteTaskVisibleTx shows a hidden task. It is the one place that decides a
// task is no longer small talk; the callers are the signs of a long task: a
// checklist, a delegation or a wait, a claim or pass, a second round.
func promoteTaskVisibleTx(tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	res, err := tx.Exec(`UPDATE work_items SET hidden=0 WHERE id=? AND hidden=1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return taskEventTx(tx, id)
}

// promoteRunTasksTx shows the open tasks a run works on.
func promoteRunTasksTx(tx *sql.Tx, runID string) error {
	if runID == "" {
		return nil
	}
	items, err := tasksForRunTx(tx, runID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err = promoteTaskVisibleTx(tx, item.ID); err != nil {
			return err
		}
	}
	return nil
}

func taskHiddenTx(q queryer, id string) bool {
	var hidden int
	_ = q.QueryRow(`SELECT hidden FROM work_items WHERE id=?`, id).Scan(&hidden)
	return hidden == 1
}

// itemMarkKey is the key of the progress mark of a checklist item.
func itemMarkKey(itemID, mark string) string {
	if mark == "" {
		mark = itemID
	}
	return "item:" + mark
}

func holderOwnedMark(mark string) bool { return strings.HasPrefix(mark, holderMarkPrefix) }

func newItemID() string { return strings.ReplaceAll(newID(), "-", "")[:6] }

func shortHash(text string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(text))))
	return hex.EncodeToString(sum[:6])
}

func validItemText(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" || utf8.RuneCountInString(text) > maxChecklistTextRunes {
		return "", taskRefusal("checklist_item_invalid", fmt.Sprintf("Each item needs 1-%d characters.", maxChecklistTextRunes))
	}
	return text, nil
}

// rewriteChecklistTx changes the text of items. The item keeps its status and
// its progress mark, so a rewrite does not make a new piece of progress.
func rewriteChecklistTx(tx *sql.Tx, taskID string, rewrites []ChecklistRewrite) error {
	for _, rw := range rewrites {
		text, err := validItemText(rw.Text)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`UPDATE task_checklist SET text=?,updated_at=? WHERE task_id=? AND item_id=?`, text, now(), taskID, rw.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return taskRefusal("checklist_item_unknown", "No checklist item has that id: "+rw.ID)
		}
	}
	return nil
}

// reorderChecklistTx puts the named items first, in the order given; the other
// items follow in their old order.
func reorderChecklistTx(tx *sql.Tx, taskID string, order []string) error {
	items, err := checklistTx(tx, taskID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, it := range items {
		have[it.ID] = true
	}
	used := map[string]bool{}
	var next []string
	for _, id := range order {
		id = strings.TrimSpace(id)
		if !have[id] {
			return taskRefusal("checklist_item_unknown", "No checklist item has that id: "+id)
		}
		if !used[id] {
			used[id] = true
			next = append(next, id)
		}
	}
	for _, it := range items {
		if !used[it.ID] {
			next = append(next, it.ID)
		}
	}
	for i, id := range next {
		if _, err = tx.Exec(`UPDATE task_checklist SET ord=? WHERE task_id=? AND item_id=?`, i+1, taskID, id); err != nil {
			return err
		}
	}
	return nil
}

// editNoticeTx tells the other side of an edit. A holder's edit goes to the
// member that gave the task (none when the person did); a giver's edit goes to
// the holder. The row does not wake; a changed request wakes through its own row.
func editNoticeTx(tx *sql.Tx, item WorkItem, actor string, summary []string, reason string) error {
	var to string
	switch {
	case actor == item.BotID && item.AssignerBotID != "" && item.AssignerBotID != actor:
		to = item.AssignerBotID
	case actor != item.BotID:
		to = item.BotID
	}
	if to == "" || len(summary) == 0 {
		return nil
	}
	var name string
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, actor).Scan(&name)
	conv := item.ConversationID
	if to == item.BotID {
		conv = item.WorkConversationOrConversation()
	}
	body := fmt.Sprintf("Task %q (%s) was edited by %s. %s Reason: %s", trimRunes(item.Title, 120), item.ID, name, strings.Join(summary, " "), trimRunes(reason, maxEditReason))
	return addInboxTx(tx, InboxItem{BotID: to, ConversationID: conv, Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID, Body: trimRunes(body, 2400)})
}

// mergeTasksTx makes survivor absorb another open task of the same tree. The
// checklists combine, the dependencies are rewired, the absorbed task closes
// as cancelled with merged_into set (its row keeps the pointer), and the
// members it touched are told. A merge across trees is refused.
func mergeTasksTx(tx *sql.Tx, survivor *WorkItem, absorbedID, actor, reason string) (string, error) {
	absorbedID = strings.TrimSpace(absorbedID)
	if absorbedID == survivor.ID {
		return "", taskRefusal("merge_self", "A task cannot absorb itself.")
	}
	absorbed, err := getTaskTx(tx, absorbedID)
	if err == sql.ErrNoRows {
		return "", taskRefusal("task_not_found", "No task has the id "+absorbedID+".")
	}
	if err != nil {
		return "", err
	}
	if absorbed.RootTaskID != survivor.RootTaskID {
		return "", taskRefusal("merge_other_tree", "Tasks of different trees cannot merge.")
	}
	if isClosedWorkStatus(absorbed.Status) {
		return "", taskRefusal("task_closed", "The task "+absorbedID+" is closed.")
	}
	if actor != absorbed.BotID && (absorbed.AssignerBotID == "" || actor != absorbed.AssignerBotID) {
		return "", taskRefusal("merge_forbidden", "You can merge only tasks that you gave or hold.")
	}
	if absorbed.ID == absorbed.RootTaskID {
		return "", taskRefusal("merge_root", "The first task of a tree cannot be absorbed. Let it absorb the other task.")
	}
	// An ancestor of the survivor cannot go: its children would loop.
	for cur, steps := survivor.ParentTaskID, 0; cur != "" && steps < 64; steps++ {
		if cur == absorbed.ID {
			return "", taskRefusal("merge_ancestor", "A task cannot absorb a task above it in the tree.")
		}
		var next string
		if tx.QueryRow(`SELECT parent_task_id FROM work_items WHERE id=?`, cur).Scan(&next) != nil {
			break
		}
		cur = next
	}
	// Checklists combine.
	mine, err := checklistTx(tx, survivor.ID)
	if err != nil {
		return "", err
	}
	theirs, err := checklistTx(tx, absorbed.ID)
	if err != nil {
		return "", err
	}
	if len(mine)+len(theirs) > maxChecklistItems {
		return "", taskRefusal("checklist_too_long", fmt.Sprintf("The merged checklist would hold more than %d items. Remove or rewrite items first.", maxChecklistItems))
	}
	have := map[string]bool{}
	for _, it := range mine {
		have[it.ID] = true
	}
	ord := 0
	_ = tx.QueryRow(`SELECT COALESCE(MAX(ord),0) FROM task_checklist WHERE task_id=?`, survivor.ID).Scan(&ord)
	t := now()
	for _, it := range theirs {
		var mark string
		_ = tx.QueryRow(`SELECT mark FROM task_checklist WHERE task_id=? AND item_id=?`, absorbed.ID, it.ID).Scan(&mark)
		if mark == "" {
			mark = it.ID
		}
		id := it.ID
		if have[id] {
			id = newItemID()
		}
		have[id] = true
		ord++
		if _, err = tx.Exec(`INSERT INTO task_checklist(task_id,item_id,ord,text,status,reason,created_at,updated_at,mark) VALUES(?,?,?,?,?,?,?,?,?)`, survivor.ID, id, ord, it.Text, it.Status, it.Reason, t, t, mark); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(`DELETE FROM task_checklist WHERE task_id=?`, absorbed.ID); err != nil {
		return "", err
	}
	// Progress that the absorbed items counted already stays counted.
	if _, err = tx.Exec(`INSERT OR IGNORE INTO task_tree_progress(task_id,mark,created_at) SELECT ?,mark,created_at FROM task_tree_progress WHERE task_id=? AND mark LIKE 'item:%'`, survivor.ID, absorbed.ID); err != nil {
		return "", err
	}
	// Dependencies are rewired: whatever waited on the absorbed task waits on the survivor.
	rows, err := tx.Query(`SELECT id,depends_on FROM work_items WHERE root_task_id=? AND depends_on<>'' AND id<>?`, survivor.RootTaskID, survivor.ID)
	if err != nil {
		return "", err
	}
	type rewire struct{ id, list string }
	var changes []rewire
	for rows.Next() {
		var id, list string
		if err = rows.Scan(&id, &list); err != nil {
			rows.Close()
			return "", err
		}
		deps := parseDeps(list)
		hit := false
		var out []string
		seen := map[string]bool{}
		for _, d := range deps {
			if d == absorbed.ID {
				hit = true
				d = survivor.ID
			}
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
		if hit {
			changes = append(changes, rewire{id, strings.Join(out, ",")})
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	for _, c := range changes {
		if _, err = tx.Exec(`UPDATE work_items SET depends_on=?,updated_at=? WHERE id=?`, c.list, t, c.id); err != nil {
			return "", err
		}
	}
	// The survivor waits on both lists, without the two tasks themselves; a task
	// that started keeps its own list, because new waits cannot hold a running task.
	own := parseDeps(survivor.DependsOn)
	if survivor.Status == "blocked" && survivor.AssigneeRunID == "" {
		own = append(own, parseDeps(absorbed.DependsOn)...)
	}
	var merged []string
	seen := map[string]bool{survivor.ID: true, absorbed.ID: true}
	for _, d := range own {
		if !seen[d] {
			seen[d] = true
			merged = append(merged, d)
		}
	}
	if len(merged) > maxTaskDeps {
		return "", taskRefusal("dependency_too_many", fmt.Sprintf("A task depends on at most %d tasks.", maxTaskDeps))
	}
	if len(merged) > 0 {
		cycle, e := dependencyCycleTx(tx, survivor.ID, merged)
		if e != nil {
			return "", e
		}
		if cycle {
			return "", taskRefusal("dependency_cycle", "The merge would close a dependency cycle.")
		}
	}
	list := strings.Join(merged, ",")
	if _, err = tx.Exec(`UPDATE work_items SET depends_on=?,updated_at=? WHERE id=?`, list, t, survivor.ID); err != nil {
		return "", err
	}
	survivor.DependsOn = list
	if survivor.Status == "blocked" && survivor.AssigneeRunID == "" {
		open, e := depsOpenTx(tx, merged)
		if e != nil {
			return "", e
		}
		if open == 0 {
			if unlocked, e := unlockTaskTx(tx, *survivor); e != nil {
				return "", e
			} else if unlocked {
				survivor.Status = "todo"
			}
		}
	}
	// Children, and the waits others hold on the absorbed task, follow the survivor.
	if _, err = tx.Exec(`UPDATE work_items SET parent_task_id=?,updated_at=? WHERE parent_task_id=?`, survivor.ID, t, absorbed.ID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(`UPDATE waits SET target=? WHERE kind=? AND target=? AND status='pending'`, survivor.ID, waitTaskResult, absorbed.ID); err != nil {
		return "", err
	}
	pointer := fmt.Sprintf("Merged into task %q (%s).", trimRunes(survivor.Title, 120), survivor.ID)
	if _, err = tx.Exec(`UPDATE work_items SET status='cancelled',merged_into=?,result_summary=?,updated_at=?,completed_at=?,closed_at=? WHERE id=?`, survivor.ID, pointer, t, t, t, absorbed.ID); err != nil {
		return "", err
	}
	absorbed.Status = "cancelled"
	if err = recordTaskEditTx(tx, absorbed, actor, editRole(absorbed, actor), []string{"merged_out"}, reason, "Merged into "+survivor.ID); err != nil {
		return "", err
	}
	if err = taskEventTx(tx, absorbed.ID); err != nil {
		return "", err
	}
	// Everyone the merge touches is told, once.
	var name string
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, actor).Scan(&name)
	told := map[string]bool{actor: true}
	tell := func(bot, conv, body string) error {
		if bot == "" || told[bot] {
			return nil
		}
		told[bot] = true
		return addInboxTx(tx, InboxItem{BotID: bot, ConversationID: conv, Kind: inboxTaskUpdate, TaskID: survivor.ID, RootTaskID: survivor.RootTaskID, Body: trimRunes(body, 2400)})
	}
	both := fmt.Sprintf("Task %q (%s) was merged into task %q (%s) by %s. Reason: %s", trimRunes(absorbed.Title, 120), absorbed.ID, trimRunes(survivor.Title, 120), survivor.ID, name, trimRunes(reason, maxEditReason))
	if err = tell(survivor.BotID, survivor.WorkConversationOrConversation(), both+" The request of the absorbed task was: "+trimRunes(absorbed.Description, 1200)); err != nil {
		return "", err
	}
	if err = tell(absorbed.BotID, absorbed.WorkConversationOrConversation(), both+" Continue the work in the task that absorbed it."); err != nil {
		return "", err
	}
	if err = tell(survivor.AssignerBotID, survivor.ConversationID, both); err != nil {
		return "", err
	}
	if err = tell(absorbed.AssignerBotID, absorbed.ConversationID, both); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task %s was absorbed.", absorbed.ID), nil
}

func editRole(item WorkItem, actor string) string {
	if actor == item.BotID {
		return "holder"
	}
	return "giver"
}
