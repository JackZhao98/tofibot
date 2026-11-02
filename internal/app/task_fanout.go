package app

// Fan-out limits and default deadlines of divided work (line A, owner 2026-11-01).
// The model decides whether to divide the work; the server only stops an
// explosion of parts and gives every part a deadline, so a stuck part ends in a
// timeout result and not in a wait of hours.

import (
	"database/sql"
	"fmt"
)

const (
	autoTaskFanoutWidth        = "task_fanout_width"
	autoTaskFanoutDepth        = "task_fanout_depth"
	autoTaskPartDeadline       = "task_part_deadline_minutes"
	autoTaskPartDeadlineSched  = "task_part_deadline_scheduled_minutes"
	defaultTaskFanoutWidth     = 4
	defaultTaskFanoutDepth     = 2
	defaultTaskPartDeadline    = 30
	defaultTaskPartDeadlineSch = 60

	// maxDelegateParts bounds the parts array of one call before the setting is read.
	maxDelegateParts = 12
)

// taskDepthTx is the depth of the task a run holds: the root task of a tree is
// 0, a task it gave is 1, and so on. A run that holds no task gives at depth 0.
func taskDepthTx(q queryer, runID string) int {
	item, ok, err := taskForRunTx(q, runID)
	if err != nil || !ok {
		return 0
	}
	depth := 0
	parent := item.ParentTaskID
	for steps := 0; parent != "" && steps < 64; steps++ {
		depth++
		var next string
		if q.QueryRow(`SELECT parent_task_id FROM work_items WHERE id=?`, parent).Scan(&next) != nil {
			break
		}
		parent = next
	}
	return depth
}

// checkFanoutTx refuses a delegation that would pass a limit: more parts in one
// call than task_fanout_width, more than 2x that many open tasks in the tree, or
// a task deeper than task_fanout_depth below the root.
func checkFanoutTx(q queryer, runID string, parts int) error {
	if parts <= 0 {
		return nil
	}
	width := autoIntTx(q, autoTaskFanoutWidth)
	depth := autoIntTx(q, autoTaskFanoutDepth)
	if parts > width {
		return taskRefusal("task_fanout_width", fmt.Sprintf("One call gives at most %d parts. Join parts that one member can do, or give the rest after these end.", width))
	}
	if taskDepthTx(q, runID)+1 > depth {
		return taskRefusal("task_fanout_depth", "This task is at the depth limit of its tree. Do this part yourself, or ask the member that gave it with task_ask.")
	}
	if root := treeRootForRunTx(q, runID); root != "" {
		var open int
		if err := q.QueryRow(`SELECT COUNT(*) FROM work_items WHERE root_task_id=? AND id<>? AND kind='task' AND status NOT IN ('done','cancelled','failed')`, root, root).Scan(&open); err != nil && err != sql.ErrNoRows {
			return err
		}
		if open+parts > 2*width {
			return taskRefusal("task_fanout_width", fmt.Sprintf("This tree already has %d open tasks, and it holds at most %d at one time. Wait for some to end before you give more.", open, 2*width))
		}
	}
	return nil
}

// runUnattended reports whether the run, or the run it descends from, is a
// scheduled run: nobody is in the conversation to wait with.
func runUnattended(q queryer, runID string) bool {
	id := runID
	for steps := 0; id != "" && steps < 64; steps++ {
		var kind, parent sql.NullString
		if q.QueryRow(`SELECT kind,parent_run_id FROM runs WHERE id=?`, id).Scan(&kind, &parent) != nil {
			return false
		}
		if kind.String == runKindSchedule {
			return true
		}
		id = parent.String
	}
	return false
}

// defaultPartDeadlineSeconds is the deadline of a part whose giver set none.
func defaultPartDeadlineSeconds(q queryer, runID string) int {
	if runUnattended(q, runID) {
		return autoIntTx(q, autoTaskPartDeadlineSched) * 60
	}
	return autoIntTx(q, autoTaskPartDeadline) * 60
}
