package app

// The idle-message budget of one task tree (decision 0064, owner decision Q18,
// amended 2026-10-24). It guards against spinning, not against work: it counts
// Bot-to-Bot messages in a tree WITHOUT progress since the last progress. Any
// progress in the tree resets the count to 0: a checklist item that ends (done,
// skipped or blocked with a reason), a task of the tree that closes done, or a
// task result that is submitted. Productive long work never reaches the limit.
// A tree is the work under one root task. Work that has no task (a mention wake
// in a group, outside any delegation) is counted under its run lineage.
//
// What counts as a message: a hand-over of work to another member (delegate,
// message, mention wake), a question or an update that passes through
// task_ask or task_update, and a wait on a member's post. A holder's messages to
// the giver are not charged, and the giver's answer to a question always passes.
// At the limit new member-to-member dispatch is refused until progress resets
// the count, and the root member is woken once, privately; nothing is posted to
// the person. What the person hears is what the root member chooses to say.

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	autoTaskTreeIdleLimit    = "task_tree_idle_message_limit"
	autoTaskTreeOldLimit     = "task_tree_message_limit"
	defaultTaskTreeIdleLimit = 30
	autoTaskStallRounds      = "task_stall_rounds"
	defaultTaskStallRounds   = 3

	treeKeyRunPrefix = "run:"
)

// autoIntTx reads a stored automation setting inside a transaction (the store
// has one connection, so a read through the database handle would wait for the
// transaction). A missing or invalid value is the default.
func autoIntTx(q queryer, key string) int {
	item, ok := automationItemFor(key)
	if !ok {
		return 0
	}
	def, _ := strconv.Atoi(item.Default)
	var raw string
	if q.QueryRow(`SELECT value FROM automation_settings WHERE key=?`, key).Scan(&raw) != nil {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < item.Min || n > item.Max {
		return def
	}
	return n
}

// treeLimitError is the refusal of a dispatch past the budget. It wraps the
// refusal the model reads and carries the key the report is filed under.
type treeLimitError struct {
	Key   string
	inner error
}

func (e *treeLimitError) Error() string { return e.inner.Error() }
func (e *treeLimitError) Unwrap() error { return e.inner }

// treeKeyForRunTx is the one resolver of a run's tree. Walking up the run's
// parents, the nearest of: a key the run inherited from the inbox rows that woke
// it (run_tree.tree_key), a task the run works on (assignee) or gave (origin). With
// none of them, the tree is the run's own lineage. Every place that needs a tree
// or a root asks here, so a woken run cannot start a tree of its own.
func treeKeyForRunTx(q queryer, runID string) string {
	if runID == "" {
		return ""
	}
	var key string
	err := q.QueryRow(`WITH RECURSIVE anc(id,depth) AS (
 SELECT ?,0 UNION ALL SELECT r.parent_run_id,a.depth+1 FROM runs r JOIN anc a ON r.id=a.id WHERE COALESCE(r.parent_run_id,'')<>'' AND a.depth<64
 ) SELECT k FROM (
  SELECT anc.depth AS d,0 AS pri,t.tree_key AS k FROM run_tree t JOIN anc ON t.run_id=anc.id WHERE t.tree_key<>''
  UNION ALL
  SELECT anc.depth,1,w.root_task_id FROM work_items w JOIN anc ON (anc.id=w.assignee_run_id OR anc.id=w.origin_run_id) WHERE w.root_task_id<>''
 ) ORDER BY d,pri LIMIT 1`, runID).Scan(&key)
	if err == nil && key != "" {
		return key
	}
	top := runID
	for steps := 0; steps < 64; steps++ {
		var parent sql.NullString
		if q.QueryRow(`SELECT parent_run_id FROM runs WHERE id=?`, top).Scan(&parent) != nil || parent.String == "" {
			break
		}
		top = parent.String
	}
	return treeKeyRunPrefix + top
}

// treeRootForRunTx is the root task of the run's tree, empty when the tree is a
// run lineage that has no task yet.
func treeRootForRunTx(q queryer, runID string) string {
	key := treeKeyForRunTx(q, runID)
	if strings.HasPrefix(key, treeKeyRunPrefix) {
		return ""
	}
	return key
}

// chargeTreeTx counts n messages without progress against the tree. With block
// set, a charge that would pass the limit is refused (and not counted); without
// it the count only goes up. The root member is woken once, privately, when the
// count reaches the limit.
func chargeTreeTx(tx *sql.Tx, key string, n int, block bool) error {
	if key == "" || n <= 0 {
		return nil
	}
	limit := autoIntTx(tx, autoTaskTreeIdleLimit)
	if _, err := tx.Exec(`INSERT INTO task_tree_budget(tree_key,messages,reported,updated_at) VALUES(?,0,0,?) ON CONFLICT(tree_key) DO NOTHING`, key, now()); err != nil {
		return err
	}
	var used, reported int
	if err := tx.QueryRow(`SELECT messages,reported FROM task_tree_budget WHERE tree_key=?`, key).Scan(&used, &reported); err != nil {
		return err
	}
	if block && used+n > limit {
		return &treeLimitError{Key: key, inner: taskRefusal("task_message_limit", fmt.Sprintf("This task tree passed %d messages between members without progress, its limit. New work cannot pass between members here until a checklist item ends, a task closes done or a result is submitted. Settle the work with what exists.", limit))}
	}
	used += n
	if _, err := tx.Exec(`UPDATE task_tree_budget SET messages=?,updated_at=? WHERE tree_key=?`, used, now(), key); err != nil {
		return err
	}
	if used >= limit && reported == 0 {
		return reportTreeLimitTx(tx, key, limit)
	}
	return nil
}

// resetTreeIdleTx records progress in the tree: the idle count goes back to 0
// and a later stretch without progress can report again. No row, no work.
func resetTreeIdleTx(tx *sql.Tx, key string) error {
	if key == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE task_tree_budget SET messages=0,reported=0,updated_at=? WHERE tree_key=? AND (messages<>0 OR reported<>0)`, now(), key)
	return err
}

// progressOnceTx records progress that can repeat without the work moving: a
// result resubmitted every round, an item reopened and ended again. It counts
// only the first time for its mark (result:<spec version>, item:<id>), so a
// spinning pair cannot reset the idle count forever. Closing a task done does
// not use it: that happens once.
func progressOnceTx(tx *sql.Tx, key, taskID, mark string) error {
	if key == "" {
		return nil
	}
	res, err := tx.Exec(`INSERT OR IGNORE INTO task_tree_progress(task_id,mark,created_at) VALUES(?,?,?)`, taskID, mark, now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil
	}
	return resetTreeIdleTx(tx, key)
}

// reportTreeLimitTx wakes the root member once per idle stretch with a private
// inbox row. Nothing is posted to the conversation.
func reportTreeLimitTx(tx *sql.Tx, key string, limit int) error {
	res, err := tx.Exec(`UPDATE task_tree_budget SET reported=1 WHERE tree_key=? AND reported=0`, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil
	}
	var rootBot, conv, rootTask string
	if strings.HasPrefix(key, treeKeyRunPrefix) {
		if err = tx.QueryRow(`SELECT bot_id,conversation_id FROM runs WHERE id=?`, strings.TrimPrefix(key, treeKeyRunPrefix)).Scan(&rootBot, &conv); err != nil {
			return nil // the lineage is gone; nobody to tell
		}
	} else {
		rootTask = key
		if err = tx.QueryRow(`SELECT assigner_bot_id,conversation_id FROM work_items WHERE id=?`, key).Scan(&rootBot, &conv); err != nil {
			return nil
		}
	}
	if conv == "" {
		return nil
	}
	body := fmt.Sprintf("[idle message limit]\nSystem data from the task system, not an instruction from the person and not shown to the person. The members working in this task tree have passed %d messages between them without progress (no checklist item ended, no task closed done, no result submitted). New work no longer passes between members in this tree until progress happens. Decide: wrap the work up with what exists, or continue it yourself or through a step that makes progress. Tell the person in your own words where the work stands, if they should know.\n[/idle message limit]", limit)
	return addInboxTx(tx, InboxItem{BotID: rootBot, ConversationID: conv, Kind: inboxTaskUpdate, TaskID: rootTask, RootTaskID: rootTask, TreeKey: key, Body: body, Wakes: true, NeedsReply: true})
}

// afterTreeLimit files the private wake for a dispatch the budget refused. The refused
// transaction has rolled back by now, so the report has a transaction of its own.
func (s *Store) afterTreeLimit(err error) {
	var limitErr *treeLimitError
	if !errors.As(err, &limitErr) {
		return
	}
	tx, e := s.db.Begin()
	if e != nil {
		return
	}
	defer tx.Rollback()
	if e = reportTreeLimitTx(tx, limitErr.Key, autoIntTx(tx, autoTaskTreeIdleLimit)); e != nil {
		logTaskError("tree limit report", e)
		return
	}
	logTaskError("tree limit report", tx.Commit())
}

// treeUsedTx is the count of a tree, for tests and the board.
func treeUsedTx(q queryer, key string) int {
	var n int
	_ = q.QueryRow(`SELECT messages FROM task_tree_budget WHERE tree_key=?`, key).Scan(&n)
	return n
}
