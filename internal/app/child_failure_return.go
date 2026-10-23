package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// wakeSourceChildFailure marks a return run that reports a delegated child
// which ended failed or out of budget instead of delivering a result.
const wakeSourceChildFailure = "child_failure"

const childFailurePartialRunes = 1500

// childFailureReturnable reports whether a failed run of this kind was an
// assignment whose requester is waiting for the outcome. A return run itself is
// excluded so a failing integration step can never trigger another return.
func childFailureReturnable(kind string) bool {
	return kind == runKindMessage || kind == runKindGroupTask || kind == runKindTeam
}

// inScheduleFamilyTx reports whether the run belongs to a schedule occurrence.
// Scheduled families keep their own failed-with-Retry surface.
func inScheduleFamilyTx(tx *sql.Tx, runID string) (bool, error) {
	var found bool
	err := tx.QueryRow(`WITH RECURSIVE ancestors(id) AS (
		SELECT id FROM runs WHERE id=?
		UNION ALL
		SELECT parent.parent_run_id FROM runs parent JOIN ancestors child ON child.id=parent.id WHERE parent.parent_run_id IS NOT NULL
	)
	SELECT EXISTS(SELECT 1 FROM schedule_occurrences o JOIN ancestors a ON a.id=o.run_id)`, runID).Scan(&found)
	if err != nil && strings.Contains(err.Error(), "no such table") {
		return false, nil
	}
	return found, err
}

// scheduleChildFailureReturnTx creates exactly one return run for the
// requester of a delegated child that reached a failed terminal state, in the
// transaction that recorded the failure. The return run carries a typed failure
// context (status, reason, partial output) instead of a result message.
func scheduleChildFailureReturnTx(tx *sql.Tx, runID string) (Run, bool, error) {
	done, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
	if err == sql.ErrNoRows {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	if done.Status != "failed" || done.ParentRunID == "" || !childFailureReturnable(done.Kind) {
		return Run{}, false, nil
	}
	var handoffs int
	if err = tx.QueryRow(`SELECT handoff_count FROM runs WHERE id=?`, done.ID).Scan(&handoffs); err != nil {
		return Run{}, false, err
	}
	if handoffs != 0 {
		return Run{}, false, nil
	}
	if scheduled, err := inScheduleFamilyTx(tx, done.ID); err != nil || scheduled {
		return Run{}, false, err
	}
	target, ok, err := runReturnTargetTx(tx, done)
	if err != nil || !ok || target.BotID == "" || target.BotID == done.BotID {
		return Run{}, false, err
	}
	origin := target.ConversationID
	var kind string
	var archived int
	if err = tx.QueryRow(`SELECT kind,archived FROM conversations WHERE id=?`, origin).Scan(&kind, &archived); err != nil {
		if err == sql.ErrNoRows {
			return Run{}, false, nil
		}
		return Run{}, false, err
	}
	if archived != 0 || (kind != "dm" && kind != "group") {
		return Run{}, false, nil
	}
	var active int
	if kind == "dm" {
		err = tx.QueryRow(`SELECT COUNT(*) FROM conversations c JOIN bots b ON b.id=c.bot_id WHERE c.id=? AND c.bot_id=? AND b.archived=0`, origin, target.BotID).Scan(&active)
	} else {
		err = tx.QueryRow(`SELECT COUNT(*) FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=? AND m.bot_id=? AND b.archived=0`, origin, target.BotID).Scan(&active)
	}
	if err != nil || active != 1 {
		return Run{}, false, err
	}
	existing, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE parent_run_id=? AND kind=? AND conversation_id=? LIMIT 1`, done.ID, runKindFollowup, origin))
	if err == nil {
		return existing, false, nil
	}
	if err != sql.ErrNoRows {
		return Run{}, false, err
	}
	if sibling, found, err := fanOutReturnTx(tx, done, origin); err != nil || found {
		return sibling, false, err
	}
	if pending, err := fanOutPendingTx(tx, done); err != nil || pending {
		return Run{}, false, err
	}
	var partial sql.NullString
	_ = tx.QueryRow(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND sender_bot_id=? AND kind NOT IN ('notice') ORDER BY seq DESC LIMIT 1`, done.ID, done.BotID).Scan(&partial)
	var childName sql.NullString
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, done.BotID).Scan(&childName)
	context := childFailureContext(done, childName.String, partial.String)
	if others := siblingOutcomes(tx, done, origin); len(others) > 0 {
		context += "\nOther tasks assigned together with it:" + fanOutSiblingBlocks(others, 1800)
	}

	t := now()
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, origin).Scan(&queueSeq); err != nil {
		return Run{}, false, err
	}
	followup := Run{ID: uuid.NewString(), ConversationID: origin, BotID: target.BotID, Status: "queued", ParentRunID: done.ID, Model: target.Model, Kind: runKindFollowup, OriginConversationID: origin, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, followup.ID, followup.ConversationID, followup.BotID, followup.Status, followup.ParentRunID, 0, followup.Model, followup.Kind, followup.OriginConversationID, "", followup.QueueSeq, t, t); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO run_wakes(run_id,request,source,digest) VALUES(?,?,?,?)`, followup.ID, context, wakeSourceChildFailure, ""); err != nil {
		return Run{}, false, err
	}
	b, _ := json.Marshal(followup)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, followup.ConversationID, "run", string(b), t); err != nil {
		return Run{}, false, err
	}
	if err = settleScheduledWaitTx(tx, target, followup); err != nil {
		return Run{}, false, err
	}
	return followup, true, nil
}

// childFailureContext is the typed block a return run for a failed child
// carries: terminal status, failure code and any partial output.
func childFailureContext(child Run, name, partial string) string {
	code := "execution_failed"
	if f := child.failure(); f != nil {
		code = f.Code
	}
	if name == "" {
		name = child.BotID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "child=%s (id=%s)\nstatus=%s\nfailure_code=%s\n", name, child.BotID, child.Status, code)
	if p := strings.TrimSpace(partial); p != "" {
		b.WriteString("partial_output:\n" + trimRunes(p, childFailurePartialRunes))
	} else {
		b.WriteString("partial_output: none")
	}
	return b.String()
}
