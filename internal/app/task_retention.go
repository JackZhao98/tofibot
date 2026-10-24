package app

// Retention of finished tasks (owner decision Q1: 30 days, a setting in
// Settings > Automatic behavior). When the period has passed, what a finished
// task kept in bulk goes: its request text, its checklist, its revisions, its
// ended waits and the inbox rows already read. The task row stays with its title
// and the one-line summary of its result. Messages, attachments and runs are
// never touched here; in particular the files a failed run kept (decision D38)
// stay.

import (
	"strconv"
	"time"
)

const autoTaskRetentionDays = "task_retention_days"

const defaultTaskRetentionDays = 30

func (s *Server) taskRetentionDays() int { return s.autoInt(autoTaskRetentionDays) }

// runTaskRetention applies the setting; 0 days keeps everything.
func (s *Server) runTaskRetention(at time.Time) {
	days := s.taskRetentionDays()
	if days <= 0 {
		return
	}
	if _, err := s.store.cleanupFinishedTasks(at.Add(-time.Duration(days) * 24 * time.Hour)); err != nil {
		logTaskError("task retention", err)
	}
}

type retentionResult struct {
	Tasks, Waits, Inbox int
}

// cleanupFinishedTasks removes the bulk of what ended before cutoff.
func (s *Store) cleanupFinishedTasks(cutoff time.Time) (retentionResult, error) {
	var out retentionResult
	stamp := stampTime(cutoff)
	tx, err := s.db.Begin()
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	ids, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE assigner_kind='bot' AND closed_at<>'' AND closed_at<? AND (description<>'' OR EXISTS(SELECT 1 FROM task_checklist c WHERE c.task_id=work_items.id) OR EXISTS(SELECT 1 FROM task_revisions r WHERE r.task_id=work_items.id))`, stamp)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		if _, err = tx.Exec(`UPDATE work_items SET description='' WHERE id=?`, id); err != nil {
			return out, err
		}
		if _, err = tx.Exec(`DELETE FROM task_checklist WHERE task_id=?`, id); err != nil {
			return out, err
		}
		if _, err = tx.Exec(`DELETE FROM task_revisions WHERE task_id=?`, id); err != nil {
			return out, err
		}
		out.Tasks++
	}
	res, err := tx.Exec(`DELETE FROM waits WHERE status<>'pending' AND resolved_at<>'' AND resolved_at<?`, stamp)
	if err != nil {
		return out, err
	}
	n, _ := res.RowsAffected()
	out.Waits = int(n)
	res, err = tx.Exec(`DELETE FROM bot_inbox WHERE (consumed_at<>'' AND consumed_at<?) OR (consumed_at='' AND wakes=0 AND created_at<?)`, stamp, stamp)
	if err != nil {
		return out, err
	}
	n, _ = res.RowsAffected()
	out.Inbox = int(n)
	// A tree nobody has charged since the cutoff starts from zero if it ever comes back.
	if _, err = tx.Exec(`DELETE FROM task_tree_budget WHERE updated_at<?`, stamp); err != nil {
		return out, err
	}
	if _, err = tx.Exec(`DELETE FROM task_tree_progress WHERE created_at<?`, stamp); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func taskRetentionDefault() string { return strconv.Itoa(defaultTaskRetentionDays) }
