package app

// Stopping, deleting and removing end the work that hangs on the one that goes
// (docs/decisions/0059-tasks-and-waits.md). Stopping a run ends the tasks it
// gave and the tasks it held, the open tasks below them, every wait that hangs
// on any of these, and the runs that worked on them; this holds in a direct
// message as it does in a group. Deleting a Bot or taking it out of a group does
// the same for what it gave, after the person was told how many tasks that is.

import (
	"database/sql"
	"net/http"
	"strings"
	"time"
)

// taskImpact is what ending a Bot's place in the workspace takes with it.
type taskImpact struct {
	// GivenTasks are the open tasks the Bot gave, with the open tasks below
	// them. HeldTasks are the open tasks other members gave to the Bot.
	GivenTasks int `json:"given_tasks"`
	HeldTasks  int `json:"held_tasks"`
	OpenWaits  int `json:"open_waits"`
}

func sqlMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func anyArgs(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// cancelTaskWorkForRunsTx ends the task work of runs that were stopped.
func cancelTaskWorkForRunsTx(tx *sql.Tx, runIDs []string, lineage string) error {
	if len(runIDs) > 0 {
		marks := sqlMarks(len(runIDs))
		args := anyArgs(runIDs)
		roots, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE status NOT IN ('done','cancelled','failed') AND (assignee_run_id IN (`+marks+`) OR origin_run_id IN (`+marks+`))`, append(args, args...)...)
		if err != nil {
			return err
		}
		if _, _, err = cancelTaskTreeTx(tx, roots, true); err != nil {
			return err
		}
		waits, err := waitIDsTx(tx, `SELECT id FROM waits WHERE status='pending' AND run_id IN (`+marks+`)`, args...)
		if err != nil {
			return err
		}
		for _, id := range waits {
			if _, err = resolveWaitTx(tx, id, waitCancelled, ""); err != nil {
				return err
			}
		}
	}
	if lineage != "" {
		waits, err := waitIDsTx(tx, `SELECT id FROM waits WHERE status='pending' AND lineage=?`, lineage)
		if err != nil {
			return err
		}
		for _, id := range waits {
			if _, err = resolveWaitTx(tx, id, waitCancelled, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

const openTaskStatuses = `'todo','in_progress','blocked','review','input_required'`

// taskImpactForBot counts what deleting the Bot, or taking it out of one group,
// ends. groupID is empty for deletion.
func (s *Store) taskImpactForBot(botID, groupID string) (taskImpact, error) {
	var out taskImpact
	scope, args := "", []any{botID}
	if groupID != "" {
		scope = ` AND (conversation_id=? OR work_conversation_id=?)`
		args = append(args, groupID, groupID)
	}
	roots, err := waitIDsTx(s.db, `SELECT id FROM work_items WHERE assigner_bot_id=? AND status IN (`+openTaskStatuses+`)`+scope, args...)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	queue := roots
	for len(queue) > 0 && len(seen) < 4096 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		kids, err := waitIDsTx(s.db, `SELECT id FROM work_items WHERE parent_task_id=? AND status IN (`+openTaskStatuses+`)`, id)
		if err != nil {
			return out, err
		}
		queue = append(queue, kids...)
	}
	out.GivenTasks = len(seen)
	heldArgs := []any{botID}
	if groupID != "" {
		heldArgs = append(heldArgs, groupID, groupID)
	}
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM work_items WHERE bot_id=? AND assigner_kind='bot' AND status IN (`+openTaskStatuses+`)`+scope, heldArgs...).Scan(&out.HeldTasks); err != nil {
		return out, err
	}
	waitArgs := []any{botID}
	waitScope := ""
	if groupID != "" {
		waitScope = ` AND conversation_id=?`
		waitArgs = append(waitArgs, groupID)
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM waits WHERE bot_id=? AND status='pending' AND delivery='engine'`+waitScope, waitArgs...).Scan(&out.OpenWaits)
	return out, err
}

// cascadeEndBot ends what hangs on a Bot that is going: the tasks it gave and
// held (and the open tasks below them), its waits, and the runs that worked on
// the tasks. Waits other Bots hold on its posts are brought to their deadline
// now, so each of those Bots is told by the ordinary timeout. groupID limits it
// to one group, for a member that leaves.
func (s *Store) cascadeEndBot(botID, groupID string) (taskImpact, []string, error) {
	impact, err := s.taskImpactForBot(botID, groupID)
	if err != nil {
		return impact, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return impact, nil, err
	}
	defer tx.Rollback()
	runs, err := cascadeEndBotTx(tx, botID, groupID)
	if err != nil {
		return impact, nil, err
	}
	return impact, runs, tx.Commit()
}

func cascadeEndBotTx(tx *sql.Tx, botID, groupID string) ([]string, error) {
	scope := ""
	var scopeArgs []any
	if groupID != "" {
		scope = ` AND (conversation_id=? OR work_conversation_id=?)`
		scopeArgs = []any{groupID, groupID}
	}
	given, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE assigner_bot_id=? AND status IN (`+openTaskStatuses+`)`+scope, append([]any{botID}, scopeArgs...)...)
	if err != nil {
		return nil, err
	}
	// The runs that worked on what the Bot gave stop with it. The runs of the
	// tasks it holds are its own work: they block a deletion as they always did.
	_, runs, err := cancelTaskTreeTx(tx, given, true)
	if err != nil {
		return nil, err
	}
	held, err := waitIDsTx(tx, `SELECT id FROM work_items WHERE bot_id=? AND assigner_bot_id<>? AND status IN (`+openTaskStatuses+`)`+scope, append([]any{botID, botID}, scopeArgs...)...)
	if err != nil {
		return nil, err
	}
	if _, _, err = cancelTaskTreeTx(tx, held, true); err != nil {
		return nil, err
	}
	waitScope, waitArgs := "", []any{botID}
	if groupID != "" {
		waitScope = ` AND conversation_id=?`
		waitArgs = append(waitArgs, groupID)
	}
	waits, err := waitIDsTx(tx, `SELECT id FROM waits WHERE bot_id=? AND status='pending'`+waitScope, waitArgs...)
	if err != nil {
		return nil, err
	}
	for _, id := range waits {
		if _, err = resolveWaitTx(tx, id, waitCancelled, ""); err != nil {
			return nil, err
		}
	}
	// Waiters on this Bot's posts are told by the timeout, now.
	if _, err = tx.Exec(`UPDATE waits SET deadline_at=? WHERE kind='bot_post' AND target=? AND status='pending'`+waitScope, append([]any{stampTime(time.Now()), botID}, waitArgs[1:]...)...); err != nil {
		return nil, err
	}
	return runs, nil
}

// cascadeEndBotWork is cascadeEndBot with the runs stopped: the way the
// delete route and the group editor call it.
func (s *Server) cascadeEndBotWork(botID, groupID string) (taskImpact, error) {
	impact, runs, err := s.store.cascadeEndBot(botID, groupID)
	if err != nil || len(runs) == 0 {
		return impact, err
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return impact, err
	}
	defer tx.Rollback()
	if _, err = cancelTreeCollect(tx, `SELECT id FROM runs WHERE id IN (`+sqlMarks(len(runs))+`)`, anyArgs(runs)...); err != nil {
		return impact, err
	}
	if err = tx.Commit(); err != nil {
		return impact, err
	}
	s.cancelInactiveRuns()
	return impact, nil
}

// routeTaskImpact answers GET /api/bots/{id}/task-impact?group_id=: the numbers
// the confirmation of a deletion or a removal shows.
func (s *Server) routeTaskImpact(w http.ResponseWriter, r *http.Request, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 3 || parts[0] != "bots" || parts[2] != "task-impact" {
		return false
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method", "method not allowed")
		return true
	}
	if _, err := s.store.GetBot(parts[1]); err != nil {
		writeErr(w, 404, "not_found", "bot not found")
		return true
	}
	impact, err := s.store.taskImpactForBot(parts[1], r.URL.Query().Get("group_id"))
	if err != nil {
		writeErr(w, 500, "storage", "task impact unavailable")
		return true
	}
	writeJSON(w, 200, impact)
	return true
}
