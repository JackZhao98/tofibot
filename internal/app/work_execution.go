package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

var ErrWorkExecutionConflict = errors.New("work execution unavailable")

// An attempt is durable; its read model follows the entire causal run family.
// Finishing a run is not proof of completing the user's task. The board only
// proposes review, leaving completion to an explicit subsequent action.
type WorkItemExecution struct {
	ID              string `json:"id"`
	WorkItemID      string `json:"work_item_id"`
	RootRunID       string `json:"root_run_id"`
	StatusRunID     string `json:"status_run_id"`
	Status          string `json:"status"`
	Active          bool   `json:"active"`
	Error           string `json:"error,omitempty"`
	Result          string `json:"result,omitempty"`
	ResultMessageID string `json:"result_message_id,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

func migrateWorkExecutions(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS work_item_executions (
 id TEXT PRIMARY KEY,
 work_item_id TEXT NOT NULL REFERENCES work_items(id) ON DELETE CASCADE,
 root_run_id TEXT NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
 request_id TEXT NOT NULL,
 created_at TEXT NOT NULL,
 UNIQUE(work_item_id,request_id)
 );
 CREATE INDEX IF NOT EXISTS work_item_executions_item ON work_item_executions(work_item_id,created_at);`)
	return err
}

const workExecutionFamilySQL = `WITH RECURSIVE family(id) AS (
 SELECT ? UNION SELECT child.id FROM runs child JOIN family parent ON child.parent_run_id=parent.id
) `

func workExecutionTx(tx *sql.Tx, item WorkItem) (*WorkItemExecution, error) {
	var execution WorkItemExecution
	err := tx.QueryRow(`SELECT id,work_item_id,root_run_id,created_at FROM work_item_executions WHERE work_item_id=? ORDER BY rowid DESC LIMIT 1`, item.ID).Scan(&execution.ID, &execution.WorkItemID, &execution.RootRunID, &execution.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return projectWorkExecutionAttemptTx(tx, item, execution)
}

// projectWorkExecutionAttemptTx derives the public read model for one durable
// attempt. All results remain confined to the work item's visible conversation.
func projectWorkExecutionAttemptTx(tx *sql.Tx, item WorkItem, execution WorkItemExecution) (*WorkItemExecution, error) {
	rows, err := tx.Query(workExecutionFamilySQL+`SELECT r.id,r.conversation_id,r.bot_id,r.status,r.error,r.parent_run_id,r.model,r.kind,r.origin_conversation_id,r.trigger_message_id,r.queue_seq,r.created_at,r.updated_at FROM runs r JOIN family f ON f.id=r.id ORDER BY r.created_at,r.id`, execution.RootRunID)
	if err != nil {
		return nil, err
	}
	family := []scheduleFamilyRun{}
	runs := map[string]Run{}
	execution.UpdatedAt = execution.CreatedAt
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		runs[run.ID] = run
		family = append(family, scheduleFamilyRun{id: run.ID, parent: run.ParentRunID, bot: run.BotID, kind: run.Kind, trigger: run.TriggerMessageID, created: run.CreatedAt, status: run.Status})
		if run.Status == "queued" || run.Status == "running" || run.Status == runWaiting {
			execution.Active = true
		}
		if run.UpdatedAt > execution.UpdatedAt {
			execution.UpdatedAt = run.UpdatedAt
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	execution.StatusRunID, execution.Status = scheduleFamilyOutcome(family)
	if execution.Status == "" {
		return nil, errors.New("work execution has no root run")
	}
	selected := runs[execution.StatusRunID]
	// A hidden delegate's private diagnostic is not a board receipt. Its
	// visible requester must publish any result in the original conversation.
	if execution.Status == "failed" || execution.Status == "interrupted" {
		if selected.ConversationID == item.ConversationID {
			execution.Error = boundedWorkResult(selected.Error, 2000)
		} else if selected.Error != "" {
			execution.Error = "协作执行未完成，请在原对话查看进度或停止本轮后重试。"
		}
	}
	if !execution.Active && execution.Status == "done" {
		err = tx.QueryRow(workExecutionFamilySQL+`SELECT m.id,substr(m.content,1,4001) FROM messages m
 JOIN runs r ON r.id=m.run_id JOIN family f ON f.id=r.id
 WHERE m.conversation_id=? AND m.role='assistant' AND COALESCE(m.kind,'') IN ('','forward_result','user_message') AND r.status='done' AND r.handoff_count=0
 AND (trim(m.content)<>'' OR EXISTS (SELECT 1 FROM attachment_messages a WHERE a.message_id=m.id))
 ORDER BY m.seq DESC LIMIT 1`, execution.RootRunID, item.ConversationID).Scan(&execution.ResultMessageID, &execution.Result)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		execution.Result = boundedWorkResult(execution.Result, 4000)
		if execution.ResultMessageID == "" {
			execution.Error = "本轮执行已结束，但没有在原对话发布可验收的结果。"
		} else if strings.TrimSpace(execution.Result) == "" {
			execution.Result = "本轮结果包含附件，请在原对话查看。"
		}
	}
	return &execution, nil
}

// ListWorkItemExecutions returns the bounded execution history for a work
// item, newest first. It is a read-only projection; the current execution
// remains available through GetWorkItem and ListWorkItems.
func (s *Store) ListWorkItemExecutions(workItemID string, limit int) ([]WorkItemExecution, error) {
	return s.listWorkItemExecutions(workItemID, limit, "", "")
}

// listWorkItemExecutions reads a work item's history within an optional
// conversation and member scope. The checks and projections share one read
// transaction so a tool caller cannot observe a stale authorization decision.
func (s *Store) listWorkItemExecutions(workItemID string, limit int, conversationID, actorBotID string) ([]WorkItemExecution, error) {
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	item, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemColumns+` FROM work_items WHERE id=?`, workItemID))
	if err != nil {
		return nil, err
	}
	if conversationID != "" && item.ConversationID != conversationID {
		return nil, ErrWorkItemScope
	}
	if actorBotID != "" {
		if err := requireCurrentMemberTx(tx, item.ConversationID, actorBotID); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(`SELECT id,work_item_id,root_run_id,created_at FROM work_item_executions WHERE work_item_id=? ORDER BY rowid DESC LIMIT ?`, workItemID, limit)
	if err != nil {
		return nil, err
	}
	attempts := make([]WorkItemExecution, 0, limit)
	for rows.Next() {
		var execution WorkItemExecution
		if err := rows.Scan(&execution.ID, &execution.WorkItemID, &execution.RootRunID, &execution.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		attempts = append(attempts, execution)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range attempts {
		execution, err := projectWorkExecutionAttemptTx(tx, item, attempts[i])
		if err != nil {
			return nil, err
		}
		attempts[i] = *execution
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return attempts, nil
}

func boundedWorkResult(value string, limit int) string {
	chars := []rune(value)
	if len(chars) > limit {
		return string(chars[:limit]) + "…"
	}
	return value
}

// A retry in a hidden conversation must not revive a superseded board attempt.
// Keep this check in RetryRun's transaction so start, close and retry serialize.
func requireCurrentWorkExecutionRetryTx(tx *sql.Tx, runID string) error {
	var attemptID, latestID, status, conversationID, botID, rootBotID string
	err := tx.QueryRow(`WITH RECURSIVE ancestors(id) AS (
 SELECT ? UNION SELECT r.parent_run_id FROM runs r JOIN ancestors a ON r.id=a.id WHERE r.parent_run_id IS NOT NULL
)
 SELECT e.id,(SELECT latest.id FROM work_item_executions latest WHERE latest.work_item_id=e.work_item_id ORDER BY latest.rowid DESC LIMIT 1),w.status,w.conversation_id,w.bot_id,root.bot_id
 FROM work_item_executions e JOIN ancestors a ON a.id=e.root_run_id JOIN work_items w ON w.id=e.work_item_id JOIN runs root ON root.id=e.root_run_id`, runID).Scan(&attemptID, &latestID, &status, &conversationID, &botID, &rootBotID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if attemptID != latestID || status == "done" || status == "cancelled" || botID != rootBotID {
		return fmt.Errorf("%w: 待办已重新执行、结束或更换负责人，请从看板查看当前任务。", ErrWorkExecutionConflict)
	}
	return requireCurrentMemberTx(tx, conversationID, botID)
}

func projectWorkExecutionTx(tx *sql.Tx, item WorkItem) (WorkItem, error) {
	execution, err := workExecutionTx(tx, item)
	if err != nil {
		return WorkItem{}, err
	}
	item.Execution = execution
	if execution != nil && item.Status == "in_progress" {
		if execution.Active {
			item.Status = "in_progress"
		} else if execution.Status == "done" && execution.ResultMessageID != "" {
			item.Status = "review"
		} else {
			item.Status = "blocked"
		}
	}
	return item, nil
}

// StartWorkItem snapshots the chosen task as one user request and queues only
// its assigned Bot. No automatic execution on create, and no superseding work
// already running in this conversation. Request replay never creates a run.
func (s *Store) StartWorkItem(id, requestID string) (Run, bool, error) {
	parsed, err := uuid.Parse(requestID)
	if err != nil || parsed.String() != requestID {
		return Run{}, false, fmt.Errorf("%w: request_id must be a canonical UUID", ErrWorkItemInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	item, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemColumns+` FROM work_items WHERE id=?`, id))
	if err != nil {
		return Run{}, false, err
	}
	if err = requireCurrentMemberTx(tx, item.ConversationID, item.BotID); err != nil {
		return Run{}, false, err
	}
	var visible bool
	if err = tx.QueryRow(`SELECT user_visible FROM conversations WHERE id=?`, item.ConversationID).Scan(&visible); err != nil {
		return Run{}, false, err
	}
	if !visible {
		return Run{}, false, fmt.Errorf("%w: 请在用户可见的原对话中执行待办。", ErrWorkExecutionConflict)
	}
	var previousRun string
	err = tx.QueryRow(`SELECT root_run_id FROM work_item_executions WHERE work_item_id=? AND request_id=?`, id, requestID).Scan(&previousRun)
	if err == nil {
		run, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, previousRun))
		return run, true, err
	} else if err != sql.ErrNoRows {
		return Run{}, false, err
	}
	if item.Kind != "task" {
		return Run{}, false, fmt.Errorf("%w: 目标用于规划，请选择一项待办开始执行。", ErrWorkExecutionConflict)
	}
	if item.Status == "done" || item.Status == "cancelled" {
		return Run{}, false, fmt.Errorf("%w: 请先重新打开这项已结束的待办。", ErrWorkExecutionConflict)
	}
	execution, err := workExecutionTx(tx, item)
	if err != nil {
		return Run{}, false, err
	}
	if execution != nil && execution.Active {
		return Run{}, false, fmt.Errorf("%w: 这项待办已有执行中的任务，请等待结束或先停止执行。", ErrWorkExecutionConflict)
	}
	var busy int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE (conversation_id=? OR origin_conversation_id=?) AND status IN ('queued','running','waiting')`, item.ConversationID, item.ConversationID).Scan(&busy); err != nil {
		return Run{}, false, err
	}
	if busy > 0 {
		return Run{}, false, fmt.Errorf("%w: 当前对话仍有任务在执行或等待协作，请等待结束或先停止本轮。", ErrWorkExecutionConflict)
	}
	var model string
	if err = tx.QueryRow(`SELECT model FROM bots WHERE id=?`, item.BotID).Scan(&model); err != nil {
		return Run{}, false, err
	}
	stamp := now()
	var seq, queue int64
	if err = tx.QueryRow(nextMessageSeqSQL, item.ConversationID, item.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return Run{}, false, err
	}
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, item.ConversationID).Scan(&queue); err != nil {
		return Run{}, false, err
	}
	content := "Execute this board task within the user's existing authorization. Return the actual result and any remaining limitations in this conversation. Execution does not itself confirm task completion.\n\nTask ID: " + item.ID + "\nTitle: " + item.Title
	if item.Description != "" {
		content += "\nDescription: " + item.Description
	}
	run := Run{ID: newID(), ConversationID: item.ConversationID, BotID: item.BotID, Status: "queued", Model: model, OriginConversationID: item.ConversationID, QueueSeq: queue, CreatedAt: stamp, UpdatedAt: stamp}
	message := Message{ID: newID(), ConversationID: item.ConversationID, Seq: seq, Role: "user", RunID: run.ID, Content: content, CreatedAt: stamp}
	run.TriggerMessageID = message.ID
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,run_id,content,created_at) VALUES(?,?,?,?,?,?,?)`, message.ID, message.ConversationID, seq, message.Role, run.ID, content, stamp); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.ConversationID, run.BotID, run.Status, run.Model, run.Kind, run.OriginConversationID, run.TriggerMessageID, queue, stamp, stamp); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO work_item_executions(id,work_item_id,root_run_id,request_id,created_at) VALUES(?,?,?,?,?)`, newID(), id, run.ID, requestID, stamp); err != nil {
		return Run{}, false, err
	}
	item.Status, item.UpdatedAt, item.CompletedAt = "in_progress", stamp, ""
	if _, err = tx.Exec(`UPDATE work_items SET status=?,updated_at=?,completed_at='' WHERE id=?`, item.Status, stamp, id); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, stamp, item.ConversationID); err != nil {
		return Run{}, false, err
	}
	for _, event := range []struct {
		name  string
		value any
	}{{"message", message}, {"run", run}} {
		data, err := json.Marshal(event.value)
		if err != nil {
			return Run{}, false, err
		}
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, item.ConversationID, event.name, string(data), stamp); err != nil {
			return Run{}, false, err
		}
	}
	if err = workItemNamesTx(tx, &item); err != nil {
		return Run{}, false, err
	}
	if item, err = projectWorkExecutionTx(tx, item); err != nil {
		return Run{}, false, err
	}
	if err = workItemEventTx(tx, item); err != nil {
		return Run{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return run, false, nil
}

func (s *Server) startWorkItem(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method", "method not allowed")
		return
	}
	var input struct {
		RequestID string `json:"request_id"`
	}
	if decode(r, &input) != nil {
		writeErr(w, 400, "invalid_request", "invalid execution request")
		return
	}
	if !s.modelConfigured() {
		writeErr(w, 503, "model_unavailable", "请先连接模型，再执行待办。")
		return
	}
	run, duplicate, err := s.store.StartWorkItem(id, input.RequestID)
	if err != nil {
		s.writeWorkItemError(w, err)
		return
	}
	conversation, err := s.store.GetConversation(run.ConversationID)
	if err != nil {
		s.writeWorkItemError(w, err)
		return
	}
	s.enqueue(conversation, run)
	item, err := s.store.GetWorkItem(id)
	if err != nil {
		s.writeWorkItemError(w, err)
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"work_item": item, "run": run, "duplicate": duplicate})
}
