package app

// The message budget of one task tree (decision 0064, owner decision Q18). It
// replaces the two fixed caps the older group code carried: a run chain that
// may not be deeper than eight, and at most eight mention wakes per lineage.
// A tree is the work under one root task. Work that has no task (a mention wake
// in a group, outside any delegation) is counted under its run lineage, so a
// loop there is still bounded.
//
// What counts as a message: a hand-over of work to another member (delegate,
// message, mention wake), a question or an update that passes through
// task_ask or task_update, and a wait on a member's post. A holder's messages to
// the giver are not charged, and the giver's answer to a question always passes. The
// limit stops new member-to-member dispatch only; a member can still answer the
// person. At the limit the root member and the person are told once.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	autoTaskTreeMessageLimit    = "task_tree_message_limit"
	defaultTaskTreeMessageLimit = 60
	autoTaskStallRounds         = "task_stall_rounds"
	defaultTaskStallRounds      = 3

	treeKeyRunPrefix = "run:"
	treeLimitNotice  = "tree_limit"
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
// it (runs.tree_key), a task the run works on (assignee) or gave (origin). With
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
  SELECT anc.depth AS d,0 AS pri,r.tree_key AS k FROM runs r JOIN anc ON r.id=anc.id WHERE r.tree_key<>''
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

// chargeTreeTx counts n messages against the tree. With block set, a charge
// that would pass the limit is refused (and not counted); without it the count
// only goes up. The member that is the root of the tree and the person are told
// once, when the count reaches the limit.
func chargeTreeTx(tx *sql.Tx, key string, n int, block bool) error {
	if key == "" || n <= 0 {
		return nil
	}
	limit := autoIntTx(tx, autoTaskTreeMessageLimit)
	if _, err := tx.Exec(`INSERT INTO task_tree_budget(tree_key,messages,reported,updated_at) VALUES(?,0,0,?) ON CONFLICT(tree_key) DO NOTHING`, key, now()); err != nil {
		return err
	}
	var used, reported int
	if err := tx.QueryRow(`SELECT messages,reported FROM task_tree_budget WHERE tree_key=?`, key).Scan(&used, &reported); err != nil {
		return err
	}
	if block && used+n > limit {
		return &treeLimitError{Key: key, inner: taskRefusal("task_message_limit", fmt.Sprintf("This task tree already carried %d messages, its limit. New work cannot pass between members here. Settle the work with what exists, or ask the person.", limit))}
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

// reportTreeLimitTx tells the root member (it wakes) and posts a line for the
// person, once per tree.
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
	body := fmt.Sprintf("The members working on this task tree have passed %d messages between them, the limit. New work no longer passes between members in this tree. Do not start more. Tell the person where the work stands and what is left, and ask whether to continue.", limit)
	if err = addInboxTx(tx, InboxItem{BotID: rootBot, ConversationID: conv, Kind: inboxTaskUpdate, TaskID: rootTask, RootTaskID: rootTask, TreeKey: key, Body: body, Wakes: true, NeedsReply: true}); err != nil {
		return err
	}
	return addTreeLimitNoticeTx(tx, conv, rootBot, limit)
}

// addTreeLimitNoticeTx is the one chat line for the person. It is sent by
// "system", so it never enters a Bot's history.
func addTreeLimitNoticeTx(tx *sql.Tx, conv, rootBot string, limit int) error {
	var archived int
	if tx.QueryRow(`SELECT archived FROM conversations WHERE id=?`, conv).Scan(&archived) != nil {
		return nil
	}
	t := now()
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return err
	}
	notice := &HandoffNotice{Type: treeLimitNotice, FromBotID: rootBot, ToBotID: rootBot}
	data, _ := json.Marshal(notice)
	text := fmt.Sprintf("The members reached the limit of %d messages for this task and stopped passing work to each other. You can change the limit in Settings > Automatic behavior.", limit)
	m := Message{ID: newID(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: "system", Content: text, Notice: notice, CreatedAt: t}
	if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, nil, m.Content, string(data), t); err != nil {
		return err
	}
	if err := insertRecoveryEvent(tx, conv, "message", m, t); err != nil {
		return err
	}
	_, err := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv)
	return err
}

// afterTreeLimit files the report for a dispatch the budget refused. The refused
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
	if e = reportTreeLimitTx(tx, limitErr.Key, autoIntTx(tx, autoTaskTreeMessageLimit)); e != nil {
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
