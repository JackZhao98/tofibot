package app

// Claim and pass (docs/decisions/0060-group-triage-and-reactions.md). A task
// made by group triage is held by the member that triage chose. That member
// confirms it with claim_task (no chat message: the member list shows the
// claim), or gives it to a better-fitting member with pass_task. A pass changes
// the holder and never the giver. Both tools load on demand.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	claimTaskToolDescription = "Claim a task you hold: you have checked that it fits your role and you will do it. Claiming posts nothing in the chat; the member list shows what each member claimed. A one-turn answer needs no claim."

	passTaskToolDescription = "Pass a task you hold to another member of the group whose role fits it better. Give the member id and a short reason. The task keeps the member that gave it; only the holder changes, and the new holder is woken. Passing posts nothing in the chat. After you pass, end your turn."

	// maxTaskPasses bounds the hops of one task, so a task cannot circle.
	maxTaskPasses = 4
)

// groupTriageGuidance is the second-pass triage principle of a group member.
const groupTriageGuidance = `[task triage]
Work can reach you first and not fit your role. Compare it with the role of each member. If it fits and you will not answer at once, claim it. If another member fits better, pass it with a reason. Claiming and passing post nothing in the chat. Answer a simple request in one turn. The tools load on demand with workspace_capabilities: claim_task, pass_task.
[/task triage]
`

func ensureTaskClaimTables(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS task_claims(
 task_id TEXT PRIMARY KEY REFERENCES work_items(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL,
 claimed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS task_passes(
 id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
 from_bot TEXT NOT NULL,
 to_bot TEXT NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS task_passes_task ON task_passes(task_id);`)
	return err
}

// claimedAtTx is when the holder claimed the task; empty when it has not.
func claimedAtTx(q queryer, taskID, holder string) string {
	var at string
	_ = q.QueryRow(`SELECT claimed_at FROM task_claims WHERE task_id=? AND bot_id=?`, taskID, holder).Scan(&at)
	return at
}

// claimTask records that the holder took the task. It is idempotent.
func (s *Store) claimTask(actor, taskID string) (WorkItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	item, err := getTaskTx(tx, taskID)
	if err == sql.ErrNoRows || (err == nil && item.Kind != "task") {
		return WorkItem{}, taskRefusal("task_not_found", "No task has that id.")
	}
	if err != nil {
		return WorkItem{}, err
	}
	if item.BotID != actor {
		return WorkItem{}, taskRefusal("task_not_held", "Only the member that holds the task may claim it.")
	}
	if isClosedWorkStatus(item.Status) {
		return WorkItem{}, taskRefusal("task_closed", "The task is closed.")
	}
	t := now()
	if _, err = tx.Exec(`INSERT INTO task_claims(task_id,bot_id,claimed_at) VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET bot_id=excluded.bot_id,claimed_at=CASE WHEN task_claims.bot_id=excluded.bot_id THEN task_claims.claimed_at ELSE excluded.claimed_at END`, item.ID, actor, t); err != nil {
		return WorkItem{}, err
	}
	if item.Status == "todo" {
		if _, err = tx.Exec(`UPDATE work_items SET status='in_progress',updated_at=? WHERE id=?`, t, item.ID); err != nil {
			return WorkItem{}, err
		}
	} else if _, err = tx.Exec(`UPDATE work_items SET updated_at=? WHERE id=?`, t, item.ID); err != nil {
		return WorkItem{}, err
	}
	if err = taskEventTx(tx, item.ID); err != nil {
		return WorkItem{}, err
	}
	out, err := getTaskTx(tx, item.ID)
	if err != nil {
		return WorkItem{}, err
	}
	return out, tx.Commit()
}

// passTask gives the task to another member of its group and starts that
// member's run. The giver (assigner) and its waits stay as they were.
func (s *Store) passTask(actor, fromRunID, taskID, to, reason string) (WorkItem, Run, error) {
	to, reason = strings.TrimSpace(to), strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxChecklistReason {
		return WorkItem{}, Run{}, taskRefusal("pass_reason_required", "A pass carries a reason of 1-400 characters.")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, Run{}, err
	}
	defer tx.Rollback()
	item, err := getTaskTx(tx, taskID)
	if err == sql.ErrNoRows || (err == nil && item.Kind != "task") {
		return WorkItem{}, Run{}, taskRefusal("task_not_found", "No task has that id.")
	}
	if err != nil {
		return WorkItem{}, Run{}, err
	}
	if item.BotID != actor {
		return WorkItem{}, Run{}, taskRefusal("task_not_held", "Only the member that holds the task may pass it.")
	}
	if isClosedWorkStatus(item.Status) {
		return WorkItem{}, Run{}, taskRefusal("task_closed", "The task is closed.")
	}
	var kind string
	if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, item.ConversationID).Scan(&kind); err != nil {
		return WorkItem{}, Run{}, err
	}
	if kind != "group" || item.WorkConversationOrConversation() != item.ConversationID {
		return WorkItem{}, Run{}, taskRefusal("pass_not_in_group", "Only a task that lives in a group can be passed.")
	}
	if to == "" || to == actor {
		return WorkItem{}, Run{}, taskRefusal("pass_target_invalid", "Pass the task to the exact id of another member of this group.")
	}
	var member int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=? AND m.bot_id=? AND b.archived=0`, item.ConversationID, to).Scan(&member); err != nil {
		return WorkItem{}, Run{}, err
	}
	if member == 0 {
		return WorkItem{}, Run{}, taskRefusal("pass_target_not_member", "That id is not an active member of this group.")
	}
	var passes int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM task_passes WHERE task_id=?`, item.ID).Scan(&passes); err != nil {
		return WorkItem{}, Run{}, err
	}
	if passes >= maxTaskPasses {
		return WorkItem{}, Run{}, taskRefusal("pass_limit", "This task was passed too many times. Keep it and do the work.")
	}
	var model, toName, fromName string
	if err = tx.QueryRow(`SELECT model,name FROM bots WHERE id=?`, to).Scan(&model, &toName); err != nil {
		return WorkItem{}, Run{}, err
	}
	_ = tx.QueryRow(`SELECT name FROM bots WHERE id=?`, actor).Scan(&fromName)
	var trigger sql.NullString
	if item.AssigneeRunID != "" {
		_ = tx.QueryRow(`SELECT trigger_message_id FROM runs WHERE id=?`, item.AssigneeRunID).Scan(&trigger)
	}
	t := now()
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, item.ConversationID).Scan(&queueSeq); err != nil {
		return WorkItem{}, Run{}, err
	}
	run := Run{ID: newID(), ConversationID: item.ConversationID, BotID: to, Status: "queued", ParentRunID: fromRunID, Model: model, Kind: runKindGroupChat,
		OriginConversationID: item.ConversationID, TriggerMessageID: trigger.String, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.ConversationID, run.BotID, run.Status, nullString(run.ParentRunID), run.Model, run.Kind, run.OriginConversationID, nullString(run.TriggerMessageID), run.QueueSeq, t, t); err != nil {
		return WorkItem{}, Run{}, err
	}
	if _, err = tx.Exec(`INSERT INTO task_passes(id,task_id,from_bot,to_bot,reason,created_at) VALUES(?,?,?,?,?,?)`, newID(), item.ID, actor, to, reason, t); err != nil {
		return WorkItem{}, Run{}, err
	}
	if _, err = tx.Exec(`DELETE FROM task_claims WHERE task_id=?`, item.ID); err != nil {
		return WorkItem{}, Run{}, err
	}
	if _, err = tx.Exec(`UPDATE work_items SET bot_id=?,status='todo',assignee_run_id=?,submit_run_id='',updated_at=? WHERE id=?`, to, run.ID, t, item.ID); err != nil {
		return WorkItem{}, Run{}, err
	}
	// The new holder reads why the task came to it when its run starts.
	body := "Task " + quoteTitle(item.Title) + " (" + item.ID + ") was passed to you by " + fromName + ". Reason: " + trimRunes(reason, maxChecklistReason) + " Check that it fits your role, then claim it or do it."
	if err = addInboxTx(tx, InboxItem{BotID: to, ConversationID: item.ConversationID, Kind: inboxTaskUpdate, TaskID: item.ID, RootTaskID: item.RootTaskID, Body: body}); err != nil {
		return WorkItem{}, Run{}, err
	}
	if err = taskEventTx(tx, item.ID); err != nil {
		return WorkItem{}, Run{}, err
	}
	data, _ := json.Marshal(run)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, item.ConversationID, "run", string(data), t); err != nil {
		return WorkItem{}, Run{}, err
	}
	out, err := getTaskTx(tx, item.ID)
	if err != nil {
		return WorkItem{}, Run{}, err
	}
	return out, run, tx.Commit()
}

func quoteTitle(title string) string { return `"` + trimRunes(title, 120) + `"` }

func (s *Server) claimTaskTool(r Run) Tool {
	return Tool{Name: "claim_task", Description: claimTaskToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{"task_id": stringProp("The id of the task.")}, []string{"task_id"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.TaskID) == "" {
				return "", errors.New("task_id is required")
			}
			item, err := s.store.claimTask(r.BotID, strings.TrimSpace(in.TaskID))
			if err != nil {
				return "", err
			}
			return receiptJSON(map[string]any{"task_id": item.ID, "status": item.Status, "claimed": true})
		}}
}

func (s *Server) passTaskTool(c Conversation, r Run) Tool {
	return Tool{Name: "pass_task", Description: passTaskToolDescription, Local: true,
		Parameters: objectSchema(map[string]any{
			"task_id":   stringProp("The id of the task."),
			"to_bot_id": stringProp("Exact id of the member that receives the task."),
			"reason":    stringProp("Why that member fits the task better."),
		}, []string{"task_id", "to_bot_id", "reason"}),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				TaskID string `json:"task_id"`
				To     string `json:"to_bot_id"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(raw, &in) != nil || strings.TrimSpace(in.TaskID) == "" {
				return "", errors.New("task_id, to_bot_id and reason are required")
			}
			item, run, err := s.store.passTask(r.BotID, r.ID, strings.TrimSpace(in.TaskID), in.To, in.Reason)
			if err != nil {
				return "", err
			}
			s.enqueue(c, run)
			return receiptJSON(map[string]any{"task_id": item.ID, "holder": item.BotID, "status": item.Status, "note": "The task is passed. End your turn without a message about it."})
		}}
}

// recordTriageTaskTx makes the task of a routed person message. The giver is
// the person (assigner_kind user, no giver Bot), so no wait is recorded.
func recordTriageTaskTx(tx *sql.Tx, conv, bot, runID, triggerID string) error {
	var content string
	if triggerID != "" {
		_ = tx.QueryRow(`SELECT content FROM messages WHERE id=?`, triggerID).Scan(&content)
	}
	title := firstLine(content, 80)
	if title == "" {
		title = "Task"
	}
	id, t := newID(), now()
	if _, err := tx.Exec(`INSERT INTO work_items(`+workItemColumns+`,assigner_kind,assigner_bot_id,root_task_id,parent_task_id,origin_run_id,assignee_run_id,work_conversation_id,spec_version)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, conv, bot, "task", title, trimRunes(content, maxTaskBriefRunes), "todo", nil, t, t, "",
		"user", "", id, "", "", runID, conv, 1); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO task_revisions(task_id,version,source,actor,text,created_at) VALUES(?,?,?,?,?,?)`, id, 1, "person", "", trimRunes(content, maxTaskBriefRunes), t); err != nil {
		return err
	}
	return taskEventTx(tx, id)
}

const (
	maxTaskCards       = 80
	taskCardClosedDays = 14
)

// taskCards lists the tasks a conversation draws as cards and the member list
// reads: the tasks that live in it, or that were delegated to its Bot, open
// ones and the ones closed in the last two weeks. Bounded.
func (s *Store) taskCards(conversationID string) ([]WorkItem, error) {
	cutoff := stampTime(time.Now().Add(-taskCardClosedDays * 24 * time.Hour))
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT `+workItemReadColumns+workItemReadFrom+` WHERE w.kind='task' AND (w.conversation_id=? OR w.work_conversation_id=?)
 AND (w.status IN (`+openTaskStatuses+`) OR (w.status IN ('done','failed') AND w.updated_at>?)) ORDER BY w.updated_at DESC,w.id LIMIT ?`, conversationID, conversationID, cutoff, maxTaskCards)
	if err != nil {
		return nil, err
	}
	items := []WorkItem{}
	for rows.Next() {
		item, err := scanWorkItem(rows, true)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err = decorateTaskTx(tx, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// routeTaskCards serves GET /api/conversations/{id}/task-cards.
func (s *Server) routeTaskCards(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) != 3 || parts[0] != "conversations" || parts[2] != "task-cards" {
		return false
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method", "method not allowed")
		return true
	}
	if _, err := s.store.GetConversation(parts[1]); err != nil {
		writeErr(w, 404, "not_found", "conversation not found")
		return true
	}
	items, err := s.store.taskCards(parts[1])
	if err != nil {
		s.writeWorkItemError(w, err)
		return true
	}
	writeJSON(w, 200, map[string]any{"tasks": items})
	return true
}
