package app

// The contract of a delegated part (divide and conquer, line A): the form of the
// result, the limits and the deadline are stored columns of the task, so the card
// shows them, the holder's blocks carry them as written and the server closes the
// giver's wait at the deadline instead of relying on the model to remember.

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxContractTextRunes = 400
	minDeadlineSeconds   = 60
	maxDeadlineSeconds   = 7 * 24 * 3600
)

// taskContract is what a giver writes beside the brief. Every field is optional.
type taskContract struct {
	ResultFormat    string
	Limits          string
	DeadlineSeconds int
}

// cleanTaskContract validates and trims a contract. A refusal carries a code the
// model repairs.
func cleanTaskContract(c taskContract) (taskContract, error) {
	c.ResultFormat = strings.TrimSpace(c.ResultFormat)
	c.Limits = strings.TrimSpace(c.Limits)
	if utf8.RuneCountInString(c.ResultFormat) > maxContractTextRunes {
		return c, taskRefusal("result_format_too_long", fmt.Sprintf("result_format holds at most %d characters.", maxContractTextRunes))
	}
	if utf8.RuneCountInString(c.Limits) > maxContractTextRunes {
		return c, taskRefusal("limits_too_long", fmt.Sprintf("limits holds at most %d characters.", maxContractTextRunes))
	}
	if c.DeadlineSeconds != 0 && (c.DeadlineSeconds < minDeadlineSeconds || c.DeadlineSeconds > maxDeadlineSeconds) {
		return c, taskRefusal("deadline_range", fmt.Sprintf("deadline_seconds is %d seconds to 7 days.", minDeadlineSeconds))
	}
	return c, nil
}

// deadlineFrom is the stored deadline of a part that starts at from.
func deadlineFrom(from time.Time, seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return stampTime(from.Add(time.Duration(seconds) * time.Second))
}

// storeTaskContractTx writes the contract of a task. The deadline starts to run
// when the task starts: unlockTaskTx sets deadline_at then, so a task that waits
// on other tasks does not spend its time while it waits.
func storeTaskContractTx(tx *sql.Tx, taskID string, c taskContract) error {
	_, err := tx.Exec(`UPDATE work_items SET result_format=?,limits=?,deadline_seconds=? WHERE id=?`, c.ResultFormat, c.Limits, c.DeadlineSeconds, taskID)
	return err
}

// storedContract is a task's contract as the columns hold it.
type storedContract struct {
	item            WorkItem
	deadlineSeconds int
}

// loadContractTx reads the contract columns of a task.
func loadContractTx(q queryer, taskID string) storedContract {
	var c storedContract
	_ = q.QueryRow(`SELECT result_format,limits,deadline_at,deadline_seconds FROM work_items WHERE id=?`, taskID).Scan(&c.item.ResultFormat, &c.item.Limits, &c.item.DeadlineAt, &c.deadlineSeconds)
	return c
}

// fillContractTx adds what the receipt of a task reports: its deadline, and that
// a task a Bot gave to itself runs after the run that gave it.
func (rc *delegatedTaskReceipt) fillContractTx(q queryer, giver string) {
	rc.DeadlineAt = loadContractTx(q, rc.TaskID).item.DeadlineAt
	if rc.BotID == giver {
		rc.Runs = "serial"
	}
}

const (
	maxProgressRunes = 140
	// progressEmitSeconds is how often a status line reaches the card: a line written
	// sooner after the last one is stored (the next card update shows it) and sends no event.
	progressEmitSeconds = 20
)

// setProgressTx stores the holder's one-line status. It overwrites the earlier line
// and makes no message. The card event goes out at most once in progressEmitSeconds.
func setProgressTx(tx *sql.Tx, taskID, line string) error {
	var last string
	if err := tx.QueryRow(`SELECT progress_at FROM work_items WHERE id=?`, taskID).Scan(&last); err != nil {
		return err
	}
	at := time.Now()
	if prev, err := time.Parse(storedTimeLayout, last); err == nil && at.Sub(prev) < progressEmitSeconds*time.Second {
		_, err = tx.Exec(`UPDATE work_items SET progress_line=? WHERE id=?`, line, taskID)
		return err
	}
	if _, err := tx.Exec(`UPDATE work_items SET progress_line=?,progress_at=?,updated_at=? WHERE id=?`, line, stampTime(at), now(), taskID); err != nil {
		return err
	}
	return taskEventTx(tx, taskID)
}

// taskContractLines is the text of the contract for the blocks the holder reads.
// The deadline is the time left at the moment of reading.
func taskContractLines(item WorkItem, at time.Time) string {
	var b strings.Builder
	if item.ResultFormat != "" {
		fmt.Fprintf(&b, "Result format: %s\n", item.ResultFormat)
	}
	if item.Limits != "" {
		fmt.Fprintf(&b, "Limits: %s\n", item.Limits)
	}
	if item.DeadlineAt != "" {
		if t, err := time.Parse(storedTimeLayout, item.DeadlineAt); err == nil {
			left := int(t.Sub(at).Round(time.Minute) / time.Minute)
			if left < 0 {
				left = 0
			}
			fmt.Fprintf(&b, "Deadline: %d minutes left\n", left)
		}
	}
	return b.String()
}

// taskContractForRun reads the contract lines of the open task a run holds.
func (s *Store) taskContractForRun(runID string) string {
	item, ok, err := taskForRunTx(s.db, runID)
	if err != nil || !ok {
		return ""
	}
	if err := decorateTaskTx(s.db, &item); err != nil {
		return ""
	}
	return taskContractLines(item, time.Now())
}
