package app

import (
	"database/sql"
	"fmt"
	"strings"
)

// A Bot can delegate to several colleagues at once: the children share one
// parent_run_id. The requester gets one return run for the whole fan-out, made
// by whichever child reaches a terminal state last, so the person receives one
// answer instead of one near-identical reply per colleague.
//
// Safety under concurrency: the store has a single SQLite connection
// (SetMaxOpenConns(1)), so transactions run strictly one after another. Each
// child writes its own terminal status before it looks at its siblings, so of
// any two children finishing together exactly one is second and sees the other
// terminal; the first saw the second still unfinished and skipped.

const fanOutChildKinds = `('` + runKindMessage + `','` + runKindGroupTask + `','` + runKindTeam + `')`

type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// fanOutPendingTx reports whether a sibling delegated by the same parent run
// is still unfinished, in which case the finished child must not return yet.
func fanOutPendingTx(tx *sql.Tx, done Run) (bool, error) {
	if done.ParentRunID == "" || !childFailureReturnable(done.Kind) {
		return false, nil
	}
	var pending bool
	err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE parent_run_id=? AND id<>? AND kind IN `+fanOutChildKinds+` AND status IN ('queued','running','waiting'))`, done.ParentRunID, done.ID).Scan(&pending)
	if err != nil || pending {
		return pending, err
	}
	// Parts the giver handed out through the inbox (no run of their own yet, or a
	// woken run with no parent) are siblings too: their wait is still open.
	err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM work_items w WHERE w.origin_run_id=? AND w.assignee_run_id<>? AND EXISTS(
 SELECT 1 FROM waits x WHERE x.kind=? AND x.target=w.id AND x.delivery=? AND x.status='pending'))`, done.ParentRunID, done.ID, waitTaskResult, deliveryEngine).Scan(&pending)
	return pending, err
}

// fanOutRowHeld decides what the inbox does with the wait result of a hand-out
// that had parts on both paths: wait while a direct child still runs (its return
// would answer the person), and drop the row once that return exists.
func (s *Store) fanOutRowHeld(row inboxRow) (wait, drop bool) {
	if row.Kind != inboxWaitResult || row.ParentRun == "" {
		return false, false
	}
	var active, returned bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE parent_run_id=? AND kind IN `+fanOutChildKinds+` AND status IN ('queued','running','waiting'))`, row.ParentRun).Scan(&active)
	if active {
		return true, false
	}
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs r WHERE r.kind=? AND r.parent_run_id IN (SELECT id FROM runs WHERE parent_run_id=? AND kind IN `+fanOutChildKinds+`))`, runKindFollowup, row.ParentRun).Scan(&returned)
	return false, returned
}

// fanOutReturnTx finds a return run already created for this child's fan-out:
// one whose parent is a sibling that was created no later than the return. A
// sibling delegated after that return (a later round of the same run) is not
// covered by it.
func fanOutReturnTx(tx *sql.Tx, done Run, conversation string) (Run, bool, error) {
	if done.ParentRunID == "" || !childFailureReturnable(done.Kind) {
		return Run{}, false, nil
	}
	r, err := scanRun(tx.QueryRow(`SELECT r.id,r.conversation_id,r.bot_id,r.status,r.error,r.parent_run_id,r.model,r.kind,r.origin_conversation_id,r.trigger_message_id,r.queue_seq,r.created_at,r.updated_at FROM runs r WHERE r.kind=? AND r.conversation_id=? AND r.created_at>=? AND r.parent_run_id IN (SELECT id FROM runs WHERE parent_run_id=? AND id<>? AND kind IN `+fanOutChildKinds+`) ORDER BY r.created_at,r.id LIMIT 1`, runKindFollowup, conversation, done.CreatedAt, done.ParentRunID, done.ID))
	if err == sql.ErrNoRows {
		return Run{}, false, nil
	}
	return r, err == nil, err
}

type siblingOutcome struct {
	Name   string
	BotID  string
	Failed bool
	Text   string // result message, or the typed failure record
}

// siblingOutcomes lists the other finished children of done's fan-out that
// have no return of their own, oldest first, with each one's result in the
// return conversation or its failure record.
func siblingOutcomes(q rowQuerier, done Run, conversation string) []siblingOutcome {
	if done.ParentRunID == "" || !childFailureReturnable(done.Kind) {
		return nil
	}
	rows, err := q.Query(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs s WHERE parent_run_id=? AND id<>? AND kind IN `+fanOutChildKinds+` AND status IN ('done','failed') AND NOT EXISTS(SELECT 1 FROM runs r WHERE r.parent_run_id=s.id AND r.kind=?) ORDER BY created_at,id`, done.ParentRunID, done.ID, runKindFollowup)
	if err != nil {
		return nil
	}
	var siblings []Run
	for rows.Next() {
		if r, err := scanRun(rows); err == nil {
			siblings = append(siblings, r)
		}
	}
	rows.Close()
	var out []siblingOutcome
	for _, sib := range siblings {
		name := sib.BotID
		var n sql.NullString
		if q.QueryRow(`SELECT name FROM bots WHERE id=?`, sib.BotID).Scan(&n) == nil && n.String != "" {
			name = n.String
		}
		o := siblingOutcome{Name: name, BotID: sib.BotID, Failed: sib.Status == "failed"}
		if o.Failed {
			var partial sql.NullString
			_ = q.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND sender_bot_id=? AND kind NOT IN ('notice') ORDER BY seq DESC LIMIT 1`, sib.ID, sib.BotID).Scan(&partial)
			o.Text = childFailureContext(sib, name, partial.String)
		} else {
			var content sql.NullString
			if q.QueryRow(`SELECT content FROM messages WHERE conversation_id=? AND run_id=? AND role='assistant' AND sender_bot_id=? AND kind IN ('','forward_result','bot_result') ORDER BY seq DESC LIMIT 1`, conversation, sib.ID, sib.BotID).Scan(&content) != nil {
				continue
			}
			o.Text = content.String
		}
		out = append(out, o)
	}
	return out
}

// fanOutSiblingBlocks renders sibling outcomes, each trimmed to an equal share
// of budget runes, for the return run's context.
func fanOutSiblingBlocks(others []siblingOutcome, budget int) string {
	if len(others) == 0 {
		return ""
	}
	per := budget / len(others)
	if per < 200 {
		per = 200
	}
	var b strings.Builder
	for _, o := range others {
		label := "Result from"
		if o.Failed {
			label = "Failed task from"
		}
		head := fmt.Sprintf("\n%s %s (id=%s):\n", label, o.Name, o.BotID)
		room := per - len([]rune(head))
		if room < 0 {
			room = 0
		}
		b.WriteString(head + trimRunes(o.Text, room))
	}
	return b.String()
}
