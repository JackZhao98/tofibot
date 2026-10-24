package app

// The giver inbox and the stall report (decision 0064, owner decisions Q17 and
// C3 item 5). Every message a holder posts in the conversation of a task it
// holds for another member goes to the giver's inbox as a progress row, which
// does not wake. A question, a result and a stall report do wake. A task whose
// holder ran N rounds with no change of its checklist and no change of its
// status gets one report to the root member of its tree; the counter starts
// again when progress comes.

import (
	"database/sql"
	"fmt"
	"strings"
)

// routeHolderMessageTx files a message that run posted in its conversation for
// the member that gave the task the run holds. It is called in the transaction
// that stores the message. A run that holds no task for another member, a note
// and a message outside the task's conversation are not routed.
func routeHolderMessageTx(tx *sql.Tx, runID, conv, botID, messageID, content string) error {
	item, ok, err := taskForRunTx(tx, runID)
	if err != nil || !ok {
		return err
	}
	if item.AssignerKind != "bot" || item.AssignerBotID == "" || item.AssignerBotID == botID || item.BotID != botID || item.WorkConversationOrConversation() != conv {
		return nil
	}
	body := fmt.Sprintf("Message from the holder of task %q (%s): %s", item.Title, item.ID, trimRunes(strings.TrimSpace(content), 600))
	// A holder's message is not charged to the tree budget: only hand-overs and
	// the questions and requests between members are (decision 0064).
	return addInboxTx(tx, InboxItem{BotID: item.AssignerBotID, ConversationID: item.ConversationID, Kind: inboxProgress, TaskID: item.ID, RootTaskID: item.RootTaskID,
		Body: body, ParentRun: runID, Anchor: messageID})
}

// supersedeProgressTx marks the progress rows of a run's task as read when the
// run's result is delivered: the result carries them.
func supersedeProgressTx(tx *sql.Tx, taskID, runID string) error {
	_, err := tx.Exec(`UPDATE bot_inbox SET consumed_by_run='-',consumed_at=? WHERE kind=? AND task_id=? AND parent_run=? AND consumed_at=''`, now(), inboxProgress, taskID, runID)
	return err
}

// progressSigTx is what the stall counter compares between rounds: the checklist
// with each item's state, and whether the task is blocked or waiting for input.
// A move between todo, in progress and review is the system's, not progress.
func progressSigTx(q queryer, taskID string) string {
	var status string
	if q.QueryRow(`SELECT status FROM work_items WHERE id=?`, taskID).Scan(&status) != nil {
		return ""
	}
	class := "active"
	if status == "blocked" || status == "input_required" {
		class = status
	}
	items, _ := checklistTx(q, taskID)
	var b strings.Builder
	b.WriteString(class)
	for _, c := range items {
		b.WriteString("|" + c.ID + ":" + c.Status)
	}
	// A result, and the state of the tasks the holder gave on, are progress too.
	// The result message is not in the signature: a holder that posts a result each
	// round that the giver has not accepted is the same stall. The tasks it gave count.
	if rows, err := q.Query(`SELECT id,status FROM work_items WHERE parent_task_id=? ORDER BY id`, taskID); err == nil {
		for rows.Next() {
			var id, st string
			if rows.Scan(&id, &st) == nil {
				b.WriteString("|child:" + id + ":" + st)
			}
		}
		rows.Close()
	}
	return b.String()
}

// baselineStallTx records the first signature of a task, when its run starts.
func baselineStallTx(tx *sql.Tx, taskID string) error {
	_, err := tx.Exec(`UPDATE work_items SET stall_sig=? WHERE id=? AND stall_sig=''`, progressSigTx(tx, taskID), taskID)
	return err
}

// noteRoundEndedTx counts one finished round of the task's holder. A round that
// changed the signature resets the counter; the Nth quiet round in a row reports
// once to the root member. It runs for a task that stays open.
func noteRoundEndedTx(tx *sql.Tx, item WorkItem, runID string) error {
	var status, prev string
	var count int
	if err := tx.QueryRow(`SELECT status,stall_sig,stall_count FROM work_items WHERE id=?`, item.ID).Scan(&status, &prev, &count); err != nil {
		return err
	}
	if isClosedWorkStatus(status) {
		return nil
	}
	sig := progressSigTx(tx, item.ID)
	// A round that ended holding a wait, or with tasks it gave still open, is not
	// quiet: the holder is working through others.
	var busy int
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM waits WHERE run_id=? AND status='pending')+(SELECT COUNT(*) FROM work_items WHERE (parent_task_id=? OR origin_run_id=?) AND status IN (`+openTaskStatuses+`))`, runID, item.ID, runID).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 || prev == "" || sig != prev {
		_, err := tx.Exec(`UPDATE work_items SET stall_sig=?,stall_count=0 WHERE id=?`, sig, item.ID)
		return err
	}
	count++
	if _, err := tx.Exec(`UPDATE work_items SET stall_count=? WHERE id=?`, count, item.ID); err != nil {
		return err
	}
	if count != autoIntTx(tx, autoTaskStallRounds) {
		return nil
	}
	return reportStallTx(tx, item, count)
}

// reportStallTx tells the root member of the task's tree, once per stall.
func reportStallTx(tx *sql.Tx, item WorkItem, rounds int) error {
	var rootBot, rootConv string
	root := item.RootTaskID
	if root == "" {
		root = item.ID
	}
	if tx.QueryRow(`SELECT assigner_bot_id,conversation_id FROM work_items WHERE id=?`, root).Scan(&rootBot, &rootConv) != nil || rootBot == "" || rootBot == item.BotID {
		return nil
	}
	var holder string
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, item.BotID).Scan(&holder)
	body := fmt.Sprintf("Task %q (%s), held by %s (id=%s), has run %d rounds with no change in its checklist and no change in its status. Read its record and the messages in its conversation, then decide: give it a smaller step, answer what blocks it, give the work to another member, or ask the person.",
		item.Title, item.ID, holder, item.BotID, rounds)
	return addInboxTx(tx, InboxItem{BotID: rootBot, ConversationID: rootConv, Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: root, Body: body, Wakes: true, NeedsReply: true})
}
