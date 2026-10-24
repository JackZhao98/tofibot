package app

// Tables of the task and wait model (docs/decisions/0059-tasks-and-waits.md).
// A task is a board item (work_items) with its columns; its checklist, its
// revisions, the waits Bots register and the Bot inbox live in the tables
// below. Everything is additive: CREATE ... IF NOT EXISTS, ensureColumn, and
// data steps that can run again without a second effect.

import (
	"database/sql"
	"errors"
	"time"
)

func migrateTasksAndWaits(db *sql.DB) error {
	if db == nil {
		return errors.New("task database is required")
	}
	// The answer's outcome label is kept with it: a run that said "blocked"
	// leaves its task blocked, not done.
	if err := ensureColumn(db, "run_say_answers", "outcome", `ALTER TABLE run_say_answers ADD COLUMN outcome TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS task_checklist(
 task_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
 item_id TEXT NOT NULL,
 ord INTEGER NOT NULL,
 text TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','done','skipped','blocked')),
 reason TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(task_id,item_id)
);
CREATE TABLE IF NOT EXISTS task_revisions(
 task_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
 version INTEGER NOT NULL,
 source TEXT NOT NULL,
 actor TEXT NOT NULL DEFAULT '',
 text TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 PRIMARY KEY(task_id,version)
);
CREATE TABLE IF NOT EXISTS waits(
 id TEXT PRIMARY KEY,
 bot_id TEXT NOT NULL,
 run_id TEXT NOT NULL DEFAULT '',
 conversation_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('bot_post','task_result','human_answer','command_exit','timer')),
 target TEXT NOT NULL DEFAULT '',
 group_id TEXT NOT NULL,
 group_mode TEXT NOT NULL DEFAULT 'all' CHECK(group_mode IN ('any','all')),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','fulfilled','timed_out','cancelled')),
 delivery TEXT NOT NULL DEFAULT 'engine' CHECK(delivery IN ('engine','legacy')),
 deadline_at TEXT NOT NULL,
 root_task_id TEXT NOT NULL DEFAULT '',
 lineage TEXT NOT NULL DEFAULT '',
 note TEXT NOT NULL DEFAULT '',
 result_ref TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 resolved_at TEXT NOT NULL DEFAULT '',
 delivered_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS waits_pending_deadline ON waits(status,deadline_at);
CREATE INDEX IF NOT EXISTS waits_bot ON waits(bot_id,status);
CREATE INDEX IF NOT EXISTS waits_group ON waits(group_id);
CREATE INDEX IF NOT EXISTS waits_run ON waits(run_id);
CREATE INDEX IF NOT EXISTS waits_root ON waits(root_task_id,status);
CREATE INDEX IF NOT EXISTS waits_target ON waits(kind,target,status);
CREATE TABLE IF NOT EXISTS bot_inbox(
 id TEXT PRIMARY KEY,
 bot_id TEXT NOT NULL,
 conversation_id TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL CHECK(kind IN ('user_message','task_update','task_question','task_result','wait_result','progress','reaction')),
 task_id TEXT NOT NULL DEFAULT '',
 root_task_id TEXT NOT NULL DEFAULT '',
 wait_group TEXT NOT NULL DEFAULT '',
 body TEXT NOT NULL DEFAULT '',
 wakes INTEGER NOT NULL DEFAULT 0,
 needs_reply INTEGER NOT NULL DEFAULT 0,
 priority INTEGER NOT NULL DEFAULT 0,
 parent_run TEXT NOT NULL DEFAULT '',
 after_run TEXT NOT NULL DEFAULT '',
 via TEXT NOT NULL DEFAULT '',
 anchor TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 consumed_by_run TEXT NOT NULL DEFAULT '',
 consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS bot_inbox_bot_open ON bot_inbox(bot_id,consumed_at,created_at);
CREATE INDEX IF NOT EXISTS bot_inbox_root ON bot_inbox(root_task_id);
CREATE UNIQUE INDEX IF NOT EXISTS bot_inbox_one_wait_result ON bot_inbox(wait_group) WHERE kind='wait_result' AND wait_group<>'';
CREATE TABLE IF NOT EXISTS command_waits(
 wait_id TEXT PRIMARY KEY,
 bot_id TEXT NOT NULL,
 terminal_id TEXT NOT NULL,
 interactive INTEGER NOT NULL DEFAULT 0,
 seen_job INTEGER NOT NULL DEFAULT 0,
 cursor INTEGER NOT NULL DEFAULT 0,
 tail TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS command_waits_bot ON command_waits(bot_id);`); err != nil {
		return err
	}
	// drained marks a row a running Bot read in place at a boundary (decision
	// 0062), as opposed to one a run's context or a wake carried. A restart that
	// lost the boundary before it was checkpointed gives these rows back.
	if err := ensureColumn(db, "bot_inbox", "drained", `ALTER TABLE bot_inbox ADD COLUMN drained INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := ensureTaskClaimTables(db); err != nil {
		return err
	}
	// stall_sig is the progress signature the stall counter compares (D64); the
	// budget table counts the messages of one task tree.
	if err := ensureColumn(db, "work_items", "stall_sig", `ALTER TABLE work_items ADD COLUMN stall_sig TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS task_tree_budget(
 tree_key TEXT PRIMARY KEY,
 messages INTEGER NOT NULL DEFAULT 0,
 reported INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
)`); err != nil {
		return err
	}
	// tree_key names the task tree a run or an inbox row belongs to (decision 0064):
	// a woken run inherits the key of the rows that woke it.
	if err := ensureColumn(db, "bot_inbox", "tree_key", `ALTER TABLE bot_inbox ADD COLUMN tree_key TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// A side table, not a column of runs: a wider runs row changes the plans of the
	// conversation list queries (TestBackendPerfPlansAvoidFullScans).
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_tree(run_id TEXT PRIMARY KEY, tree_key TEXT NOT NULL)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS work_items_origin_run ON work_items(origin_run_id) WHERE origin_run_id<>''`); err != nil {
		return err
	}
	if err := migrateWaitsFromRunWaits(db); err != nil {
		return err
	}
	return migrateLiveDelegations(db)
}

// migrateWaitsFromRunWaits copies the pending follow_up waits of the old table
// into waits. The old table stays as it is (it is not written any more), and
// the copy is keyed by the old run id, so a second start adds nothing.
func migrateWaitsFromRunWaits(db *sql.DB) error {
	var table int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='run_waits'`).Scan(&table); err != nil || table == 0 {
		return err
	}
	rows, err := db.Query(`SELECT run_id,conversation_id,bot_id,wait_for,lineage,deadline_at,created_at FROM run_waits WHERE status='pending'`)
	if err != nil {
		return err
	}
	type legacyWait struct{ run, conv, bot, waitFor, lineage, deadline, created string }
	var legacy []legacyWait
	for rows.Next() {
		var w legacyWait
		if err := rows.Scan(&w.run, &w.conv, &w.bot, &w.waitFor, &w.lineage, &w.deadline, &w.created); err != nil {
			rows.Close()
			return err
		}
		legacy = append(legacy, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, w := range legacy {
		deadline := w.deadline
		if parsed, perr := time.Parse(time.RFC3339Nano, w.deadline); perr == nil {
			deadline = stampTime(parsed)
		} else {
			deadline = stampTime(time.Now())
		}
		id := "legacy:" + w.run
		if _, err = tx.Exec(`INSERT OR IGNORE INTO waits(id,bot_id,run_id,conversation_id,kind,target,group_id,group_mode,status,delivery,deadline_at,lineage,created_at)
 VALUES(?,?,?,?,?,?,?,'all','pending','engine',?,?,?)`, id, w.bot, w.run, w.conv, waitBotPost, w.waitFor, id, deadline, w.lineage, w.created); err != nil {
			return err
		}
		// The copy is the wait from now on. Mark the old row so retention cannot
		// make the next start copy it again, and an older binary does not fire it.
		if _, err = tx.Exec(`UPDATE run_waits SET status='expired' WHERE run_id=? AND status='pending'`, w.run); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// migrateLiveDelegations gives every delegation that is still in flight (a
// child run that is queued, running or waiting, assigned by another Bot) a task
// row and the wait its assigner holds for the result. A delegation that already
// has a task is left alone, so the step is safe on every start.
func migrateLiveDelegations(db *sql.DB) error {
	var table int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='runs'`).Scan(&table); err != nil || table == 0 {
		return err
	}
	rows, err := db.Query(delegationChildSQL + ` AND r.status IN ('queued','running','waiting') AND NOT EXISTS(SELECT 1 FROM work_items w WHERE w.assignee_run_id=r.id)`)
	if err != nil {
		return err
	}
	var children []delegationChild
	for rows.Next() {
		child, err := scanDelegationChild(rows)
		if err != nil {
			rows.Close()
			return err
		}
		children = append(children, child)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, child := range children {
		if _, err = recordTaskForChildTx(tx, child); err != nil {
			return err
		}
	}
	return tx.Commit()
}
