package app

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Scheduled occurrences in one conversation execute in due-time order, one
// lineage at a time. The per-conversation worker is serial, but a scheduled
// root that delegates parks in "waiting" and frees the worker; without this
// gate the next occurrence (typically a lead's "report what everyone did")
// started while the earlier occurrence's delegated group_task/group_followup
// children were still working. Only occurrence roots are gated: user messages,
// delegated children, follow-ups and retries are never held back by it, so a
// person talking in the group is still served promptly.
//
// scheduleOrderMaxWait bounds the wait. A blocker whose root started more than
// this long ago no longer blocks, so a wedged earlier run cannot starve the
// rest of the chain forever.
var (
	scheduleOrderMaxWait = 15 * time.Minute
	scheduleOrderClock   = func() time.Time { return time.Now().UTC() }
)

// scheduleOrderBlockedSQL is true for a queued occurrence root (table runs)
// that has an earlier-due occurrence in the same conversation whose lineage is
// still queued/running/waiting and has not exceeded the bounded wait. Direct
// conversations are not gated. It takes
// one argument: the RFC3339 cutoff (now - max wait) in scheduleTime layout.
// An unstarted blocker (started_at=”) is never overdue; it is itself ordered
// and will either run or be released by its own blockers' timeouts.
const scheduleOrderBlockedSQL = `runs.kind='schedule' AND (SELECT kind FROM conversations WHERE id=runs.conversation_id)='group' AND EXISTS (
	SELECT 1 FROM schedule_occurrences me
	JOIN schedule_occurrences prev ON prev.run_id<>me.run_id
	JOIN runs pr ON pr.id=prev.run_id
	WHERE me.run_id=runs.id AND pr.conversation_id=runs.conversation_id
	AND (prev.scheduled_for_utc<me.scheduled_for_utc OR (prev.scheduled_for_utc=me.scheduled_for_utc AND pr.queue_seq<runs.queue_seq))
	AND (prev.started_at='' OR prev.started_at>?)
	AND EXISTS (WITH RECURSIVE family(id) AS (
		SELECT prev.run_id
		UNION
		SELECT child.id FROM runs child JOIN family parent ON child.parent_run_id=parent.id
	) SELECT 1 FROM runs fr WHERE fr.id IN family AND fr.status IN ('queued','running','waiting'))
)`

// markScheduleStartedTx records the actual start of an occurrence root the
// first time it leaves the queue. scheduled_for_utc stays the planned time.
func markScheduleStartedTx(tx *sql.Tx, runID string) error {
	var present bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='schedule_occurrences')`).Scan(&present); err != nil || !present {
		return err
	}
	_, err := tx.Exec(`UPDATE schedule_occurrences SET started_at=? WHERE run_id=? AND started_at=''`, scheduleTime(scheduleOrderClock()), runID)
	return err
}

// hasScheduleOrderBlocked reports whether the conversation has a queued
// occurrence root held back by the ordering gate, so its worker keeps polling
// (a blocker finishing or timing out produces no wake-up signal of its own).
func (s *Store) hasScheduleOrderBlocked(conv string) bool {
	if s.ensureSchedules() != nil {
		return false
	}
	var found bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE conversation_id=? AND status='queued' AND (`+scheduleOrderBlockedSQL+`))`,
		conv, scheduleTime(scheduleOrderClock().Add(-scheduleOrderMaxWait))).Scan(&found)
	return found
}

// scheduleOrderNote lists earlier-due occurrences of this conversation whose
// lineage finished while r waited, so a report run knows those results are
// already in the transcript. Empty when r is not an occurrence root or nothing
// finished during its wait.
func (s *Store) scheduleOrderNote(r Run) string {
	if r.Kind != runKindSchedule || r.ParentRunID != "" || s.ensureSchedules() != nil {
		return ""
	}
	created, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return ""
	}
	var mine string
	if s.db.QueryRow(`SELECT scheduled_for_utc FROM schedule_occurrences WHERE run_id=?`, r.ID).Scan(&mine) != nil {
		return ""
	}
	rows, err := s.db.Query(`WITH RECURSIVE fam(root,id) AS (
		SELECT o.run_id,o.run_id FROM schedule_occurrences o JOIN runs pr ON pr.id=o.run_id
		WHERE pr.conversation_id=? AND o.run_id<>? AND o.scheduled_for_utc<=? AND pr.queue_seq<?
		UNION
		SELECT f.root,c.id FROM runs c JOIN fam f ON c.parent_run_id=f.id
	) SELECT o.title,COALESCE(b.name,''),o.scheduled_for_utc,MAX(x.updated_at),
		SUM(x.status IN ('queued','running','waiting')),SUM(x.status IN ('failed','interrupted','cancelled'))
	FROM fam JOIN runs x ON x.id=fam.id JOIN schedule_occurrences o ON o.run_id=fam.root
	JOIN runs rr ON rr.id=o.run_id LEFT JOIN bots b ON b.id=rr.bot_id
	GROUP BY fam.root ORDER BY o.scheduled_for_utc DESC,rr.queue_seq DESC LIMIT 12`, r.ConversationID, r.ID, mine, r.QueueSeq)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var title, bot, due, last string
		var active, failed int
		if rows.Scan(&title, &bot, &due, &last, &active, &failed) != nil || active > 0 {
			continue
		}
		finished, err := time.Parse(time.RFC3339Nano, last)
		if err != nil || finished.Before(created) {
			continue
		}
		label := strings.TrimSpace(title)
		if label == "" {
			label = "scheduled run"
		}
		outcome := "finished"
		if failed > 0 {
			outcome = "ended with a failure"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s, due %s) %s at %s", trimRunes(label, 80), trimRunes(bot, 60), due[:min(len(due), 16)]+"Z", outcome, finished.UTC().Format("15:04:05")+"Z"))
	}
	if len(lines) == 0 {
		return ""
	}
	return "[earlier scheduled runs]\nThis occurrence waited for earlier-due scheduled runs in this conversation; they just finished, so their results are already in the conversation above:\n" + strings.Join(lines, "\n") + "\n[/earlier scheduled runs]"
}
