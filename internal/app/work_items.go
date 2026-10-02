package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrWorkItemInvalid = errors.New("invalid work item")
var ErrWorkItemScope = errors.New("work item is outside the current conversation")

const workItemColumns = `id,conversation_id,bot_id,kind,title,description,status,parent_goal_id,created_at,updated_at,completed_at`

type WorkItem struct {
	Execution            *WorkItemExecution `json:"execution,omitempty"`
	ConversationArchived bool               `json:"conversation_archived"`
	BotArchived          bool               `json:"bot_archived"`
	ConversationName     string             `json:"conversation_name"`
	BotName              string             `json:"bot_name"`
	ID                   string             `json:"id"`
	ConversationID       string             `json:"conversation_id"`
	BotID                string             `json:"bot_id"`
	Kind                 string             `json:"kind"`
	Title                string             `json:"title"`
	Description          string             `json:"description"`
	Status               string             `json:"status"`
	ParentGoalID         string             `json:"parent_goal_id,omitempty"`
	CreatedAt            string             `json:"created_at"`
	UpdatedAt            string             `json:"updated_at"`
	CompletedAt          string             `json:"completed_at,omitempty"`
}

type WorkItemInput struct {
	Kind         string `json:"kind"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	BotID        string `json:"bot_id,omitempty"`
	ParentGoalID string `json:"parent_goal_id,omitempty"`
}

type WorkItemPatch struct {
	Status      *string `json:"status,omitempty"`
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	BotID       *string `json:"bot_id,omitempty"`
}

func migrateWorkItems(db *sql.DB) error {
	if db == nil {
		return errors.New("work item database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS work_items (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES bots(id),
 kind TEXT NOT NULL CHECK(kind IN ('goal','task')),
 title TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('todo','in_progress','blocked','review','done','cancelled')),
 parent_goal_id TEXT REFERENCES work_items(id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 completed_at TEXT NOT NULL DEFAULT ''
 );
 CREATE INDEX IF NOT EXISTS work_items_conversation ON work_items(conversation_id,status,updated_at);
 CREATE INDEX IF NOT EXISTS work_items_bot ON work_items(bot_id,status,updated_at);
 CREATE INDEX IF NOT EXISTS work_items_parent ON work_items(parent_goal_id);`)
	if err != nil {
		return err
	}
	var schema string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='work_items'`).Scan(&schema); err != nil {
		return err
	}
	if strings.Contains(schema, "'review'") {
		return nil
	}
	// The existing CHECK constraint cannot be expanded with ALTER TABLE. Rebuild
	// transactionally so production work items and parent links survive upgrade.
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE work_items_next (
 id TEXT PRIMARY KEY,
 conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
 bot_id TEXT NOT NULL REFERENCES bots(id),
 kind TEXT NOT NULL CHECK(kind IN ('goal','task')),
 title TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL CHECK(status IN ('todo','in_progress','blocked','review','done','cancelled')),
 parent_goal_id TEXT REFERENCES work_items_next(id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 completed_at TEXT NOT NULL DEFAULT ''
 );
 INSERT INTO work_items_next (` + workItemColumns + `) SELECT ` + workItemColumns + ` FROM work_items;
 DROP TABLE work_items;
 ALTER TABLE work_items_next RENAME TO work_items;
 CREATE INDEX work_items_conversation ON work_items(conversation_id,status,updated_at);
 CREATE INDEX work_items_bot ON work_items(bot_id,status,updated_at);
 CREATE INDEX work_items_parent ON work_items(parent_goal_id);`); err != nil {
		return err
	}
	return tx.Commit()
}

func scanWorkItem(row interface{ Scan(...any) error }, names ...bool) (WorkItem, error) {
	var item WorkItem
	var parent sql.NullString
	dest := []any{&item.ID, &item.ConversationID, &item.BotID, &item.Kind, &item.Title, &item.Description, &item.Status, &parent, &item.CreatedAt, &item.UpdatedAt, &item.CompletedAt}
	if len(names) > 0 && names[0] {
		dest = append(dest, &item.ConversationName, &item.BotName, &item.ConversationArchived, &item.BotArchived)
	}
	err := row.Scan(dest...)
	item.ParentGoalID = parent.String
	return item, err
}

func validateWorkItem(item WorkItem) error {
	if item.Kind != "goal" && item.Kind != "task" {
		return fmt.Errorf("%w: kind must be goal or task", ErrWorkItemInvalid)
	}
	if strings.TrimSpace(item.Title) == "" || utf8.RuneCountInString(item.Title) > 240 {
		return fmt.Errorf("%w: title must contain 1-240 characters", ErrWorkItemInvalid)
	}
	if utf8.RuneCountInString(item.Description) > 8000 {
		return fmt.Errorf("%w: description exceeds 8000 characters", ErrWorkItemInvalid)
	}
	for _, id := range []string{item.ConversationID, item.BotID} {
		if strings.TrimSpace(id) == "" || len(id) > 128 {
			return fmt.Errorf("%w: conversation and owner IDs are required", ErrWorkItemInvalid)
		}
	}
	if len(item.ParentGoalID) > 128 || item.ParentGoalID != "" && item.Kind != "task" {
		return fmt.Errorf("%w: only tasks may reference a parent goal", ErrWorkItemInvalid)
	}
	switch item.Status {
	case "todo", "in_progress", "blocked", "review", "done", "cancelled":
	default:
		return fmt.Errorf("%w: invalid status", ErrWorkItemInvalid)
	}
	return nil
}

func requireWorkItemMemberTx(tx *sql.Tx, conversationID, botID string) error {
	if err := requireCurrentMemberTx(tx, conversationID, botID); err != nil {
		return fmt.Errorf("%w: %w", ErrWorkItemInvalid, err)
	}
	return nil
}

func (s *Store) CreateWorkItem(conversationID, botID string, in WorkItemInput) (WorkItem, error) {
	return s.createWorkItem(conversationID, botID, in, "")
}

// Actor is supplied only by model tools. Its membership is checked in the
// same transaction as the owner, closing the read/check/write scoping race.
func (s *Store) createWorkItem(conversationID, botID string, in WorkItemInput, actor string) (WorkItem, error) {
	if botID == "" {
		botID = in.BotID
	} else if in.BotID != "" && in.BotID != botID {
		return WorkItem{}, fmt.Errorf("%w: conflicting owners", ErrWorkItemInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	if botID == "" {
		var kind string
		var owner sql.NullString
		if err = tx.QueryRow(`SELECT kind,bot_id FROM conversations WHERE id=?`, conversationID).Scan(&kind, &owner); err != nil {
			return WorkItem{}, err
		}
		if kind == "dm" {
			botID = owner.String
		}
	}
	t := now()
	item := WorkItem{ID: newID(), ConversationID: conversationID, BotID: botID, Kind: in.Kind, Title: strings.TrimSpace(in.Title), Description: in.Description, Status: "todo", ParentGoalID: in.ParentGoalID, CreatedAt: t, UpdatedAt: t}
	if err = validateWorkItem(item); err != nil {
		return WorkItem{}, err
	}
	if actor != "" {
		if err = requireWorkItemMemberTx(tx, conversationID, actor); err != nil {
			return WorkItem{}, err
		}
	}
	if err = requireWorkItemMemberTx(tx, conversationID, botID); err != nil {
		return WorkItem{}, err
	}
	if item.ParentGoalID != "" {
		var parentKind, parentConversation string
		if err = tx.QueryRow(`SELECT kind,conversation_id FROM work_items WHERE id=?`, item.ParentGoalID).Scan(&parentKind, &parentConversation); err == sql.ErrNoRows {
			return WorkItem{}, fmt.Errorf("%w: parent goal not found", ErrWorkItemInvalid)
		} else if err != nil {
			return WorkItem{}, err
		}
		if parentKind != "goal" || parentConversation != conversationID {
			return WorkItem{}, fmt.Errorf("%w: parent must be a goal in this conversation", ErrWorkItemInvalid)
		}
	}
	if _, err = tx.Exec(`INSERT INTO work_items(`+workItemColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.ConversationID, item.BotID, item.Kind, item.Title, item.Description, item.Status, nullString(item.ParentGoalID), t, t, ""); err != nil {
		return WorkItem{}, err
	}
	if err = workItemNamesTx(tx, &item); err != nil {
		return WorkItem{}, err
	}
	if err = workItemEventTx(tx, item); err != nil {
		return WorkItem{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

func (s *Store) UpdateWorkItem(id string, patch WorkItemPatch) (WorkItem, error) {
	return s.updateWorkItem(id, patch, "", "")
}
func (s *Store) updateWorkItem(id string, patch WorkItemPatch, conversationID, actor string) (WorkItem, error) {
	if patch.Status == nil && patch.Title == nil && patch.Description == nil && patch.BotID == nil {
		return WorkItem{}, fmt.Errorf("%w: at least one field is required", ErrWorkItemInvalid)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	item, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemColumns+` FROM work_items WHERE id=?`, id))
	if err != nil {
		return WorkItem{}, err
	}
	if conversationID != "" && item.ConversationID != conversationID {
		return WorkItem{}, ErrWorkItemScope
	}
	execution, err := workExecutionTx(tx, item)
	if err != nil {
		return WorkItem{}, err
	}
	if execution != nil && execution.Active {
		return WorkItem{}, fmt.Errorf("%w: 请先停止这项待办的执行，再修改或确认完成。", ErrWorkExecutionConflict)
	}
	if actor != "" {
		if err = requireWorkItemMemberTx(tx, item.ConversationID, actor); err != nil {
			return WorkItem{}, err
		}
	}
	if patch.Status != nil {
		item.Status = *patch.Status
	}
	if patch.Title != nil {
		item.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Description != nil {
		item.Description = *patch.Description
	}
	if patch.BotID != nil {
		item.BotID = *patch.BotID
	}
	if err = validateWorkItem(item); err != nil {
		return WorkItem{}, err
	}
	if err = requireWorkItemMemberTx(tx, item.ConversationID, item.BotID); err != nil {
		return WorkItem{}, err
	}
	item.UpdatedAt = now()
	if item.Status == "done" || item.Status == "cancelled" {
		if item.CompletedAt == "" {
			item.CompletedAt = item.UpdatedAt
		}
	} else {
		item.CompletedAt = ""
	}
	if _, err = tx.Exec(`UPDATE work_items SET bot_id=?,title=?,description=?,status=?,updated_at=?,completed_at=? WHERE id=?`, item.BotID, item.Title, item.Description, item.Status, item.UpdatedAt, item.CompletedAt, id); err != nil {
		return WorkItem{}, err
	}
	if err = workItemNamesTx(tx, &item); err != nil {
		return WorkItem{}, err
	}
	if err = workItemEventTx(tx, item); err != nil {
		return WorkItem{}, err
	}
	if err = tx.Commit(); err != nil {
		return WorkItem{}, err
	}
	return item, nil
}

func workItemEventTx(tx *sql.Tx, item WorkItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, item.ConversationID, "work_item", string(data), item.UpdatedAt); err != nil {
		return err
	}
	return insertWorkspaceEventTx(tx, workspaceScopeGroups, item.UpdatedAt)
}

const workItemReadColumns = `w.id,w.conversation_id,w.bot_id,w.kind,w.title,w.description,w.status,w.parent_goal_id,w.created_at,w.updated_at,w.completed_at,c.name,b.name,c.archived,b.archived`
const workItemReadFrom = ` FROM work_items w JOIN conversations c ON c.id=w.conversation_id JOIN bots b ON b.id=w.bot_id`

func workItemNamesTx(tx *sql.Tx, item *WorkItem) error {
	return tx.QueryRow(`SELECT c.name,b.name,c.archived,b.archived FROM conversations c JOIN bots b ON b.id=? WHERE c.id=?`, item.BotID, item.ConversationID).Scan(&item.ConversationName, &item.BotName, &item.ConversationArchived, &item.BotArchived)
}
func (s *Store) GetWorkItem(id string) (WorkItem, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return WorkItem{}, err
	}
	defer tx.Rollback()
	item, err := scanWorkItem(tx.QueryRow(`SELECT `+workItemReadColumns+workItemReadFrom+` WHERE w.id=?`, id), true)
	if err != nil {
		return WorkItem{}, err
	}
	return projectWorkExecutionTx(tx, item)
}
func (s *Store) ListWorkItems(conversationID, botID string, history bool) ([]WorkItem, error) {
	if conversationID == "" && botID == "" {
		return nil, fmt.Errorf("%w: conversation or bot filter is required", ErrWorkItemInvalid)
	}
	query := `SELECT ` + workItemReadColumns + workItemReadFrom + ` WHERE 1=1`
	args := []any{}
	if conversationID != "" {
		query += ` AND w.conversation_id=?`
		args = append(args, conversationID)
	}
	if botID != "" {
		query += ` AND w.bot_id=?`
		args = append(args, botID)
	}
	if history {
		query += ` AND w.status IN ('done','cancelled')`
	} else {
		query += ` AND w.status IN ('todo','in_progress','blocked','review')`
	}
	query += ` ORDER BY w.updated_at DESC,w.id`
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WorkItem, 0)
	for rows.Next() {
		item, err := scanWorkItem(rows, true)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		items[i], err = projectWorkExecutionTx(tx, item)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Server) routeWorkItems(w http.ResponseWriter, r *http.Request, path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "work-items" && parts[2] == "executions" {
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method", "method not allowed")
			return true
		}
		limit := 20
		if raw, exists := r.URL.Query()["limit"]; exists {
			var err error
			if len(raw) != 1 {
				writeErr(w, 400, "invalid_request", "limit must be one integer from 1 to 100")
				return true
			}
			limit, err = strconv.Atoi(raw[0])
			if err != nil || limit < 1 || limit > 100 {
				writeErr(w, 400, "invalid_request", "limit must be one integer from 1 to 100")
				return true
			}
		}
		attempts, err := s.store.ListWorkItemExecutions(parts[1], limit)
		if err != nil {
			s.writeWorkItemError(w, err)
			return true
		}
		writeJSON(w, 200, map[string]any{"executions": attempts})
		return true
	}
	if len(parts) == 3 && parts[0] == "work-items" && parts[2] == "execute" {
		s.startWorkItem(w, r, parts[1])
		return true
	}
	if len(parts) == 3 && parts[2] == "work-items" && (parts[0] == "conversations" || parts[0] == "bots") {
		conversationID, botID := "", ""
		if parts[0] == "conversations" {
			conversationID = parts[1]
			if _, err := s.store.GetConversation(conversationID); err != nil {
				writeErr(w, 404, "not_found", "conversation not found")
				return true
			}
		} else {
			botID = parts[1]
			if _, err := s.store.GetBot(botID); err != nil {
				writeErr(w, 404, "not_found", "bot not found")
				return true
			}
		}
		switch {
		case r.Method == http.MethodGet:
			history := r.URL.Query().Get("history")
			if history != "" && history != "true" && history != "false" {
				writeErr(w, 400, "invalid_request", "history must be true or false")
				return true
			}
			items, err := s.store.ListWorkItems(conversationID, botID, history == "true")
			if err != nil {
				s.writeWorkItemError(w, err)
				return true
			}
			writeJSON(w, 200, map[string]any{"work_items": items})
		case r.Method == http.MethodPost && conversationID != "":
			var in WorkItemInput
			if decode(r, &in) != nil {
				writeErr(w, 400, "invalid_request", "invalid work item request")
				return true
			}
			item, err := s.store.CreateWorkItem(conversationID, in.BotID, in)
			if err != nil {
				s.writeWorkItemError(w, err)
				return true
			}
			writeJSON(w, 201, item)
		default:
			writeErr(w, 405, "method", "method not allowed")
		}
		return true
	}
	if len(parts) == 2 && parts[0] == "work-items" {
		if r.Method != http.MethodPatch {
			writeErr(w, 405, "method", "method not allowed")
			return true
		}
		var patch WorkItemPatch
		if decode(r, &patch) != nil {
			writeErr(w, 400, "invalid_request", "invalid work item patch")
			return true
		}
		item, err := s.store.UpdateWorkItem(parts[1], patch)
		if err != nil {
			s.writeWorkItemError(w, err)
			return true
		}
		writeJSON(w, 200, item)
		return true
	}
	return false
}

func (s *Server) writeWorkItemError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "not_found", "work item or owner not found")
	case errors.Is(err, ErrArchiveBlocked), errors.Is(err, ErrGroupMemberConflict), errors.Is(err, ErrNoActiveMembers):
		writeErr(w, 409, "work_item_unavailable", err.Error())
	case errors.Is(err, ErrWorkItemScope):
		writeErr(w, 403, "work_item_scope", err.Error())
	case errors.Is(err, ErrWorkExecutionConflict):
		writeErr(w, 409, "work_execution_unavailable", strings.TrimPrefix(err.Error(), ErrWorkExecutionConflict.Error()+": "))
	case errors.Is(err, ErrWorkItemInvalid):
		writeErr(w, 400, "invalid_work_item", err.Error())
	default:
		writeErr(w, 500, "storage", "work item storage unavailable")
	}
}

func (s *Server) workItemTools(c Conversation, r Run) []Tool {
	if c.Kind != "group" && c.Kind != "dm" {
		return nil
	}
	encode := func(value any, err error) (string, error) {
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(value)
		return string(data), err
	}
	stringField := map[string]any{"type": "string"}
	return []Tool{
		{Name: "list_work_item_executions", Description: "Read recent execution attempts for one board task in this conversation, newest first. Shows each attempt's durable run identity, live status and publicly delivered result or error. This only reads history: it does not start, retry or resume work. A run marked done is not proof that an external job succeeded or that the human accepted the result; inspect the evidence and remaining limitations.", Parameters: objectSchema(map[string]any{"work_item_id": stringField, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "Recent attempt limit, default 20"}}, []string{"work_item_id"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				ID    string `json:"work_item_id"`
				Limit *int   `json:"limit"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(data)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&in); err != nil || !json.Valid(data) || strings.TrimSpace(in.ID) == "" || len(in.ID) > 128 {
				return "", errors.New("work_item_id and an optional integer limit are required")
			}
			limit := 20
			if in.Limit != nil {
				limit = *in.Limit
			}
			if limit < 1 || limit > 100 {
				return "", errors.New("limit must be between 1 and 100")
			}
			attempts, err := s.store.listWorkItemExecutions(in.ID, limit, c.ID, r.BotID)
			return encode(map[string]any{"executions": attempts}, err)
		}},
		{Name: "create_work_item", Description: "Record a durable goal or task in this conversation with an active member as its owner (defaults to you). This records a commitment; it does not start execution or schedule a future run. Use scheduling tools for work that must actually run later. A task may reference a goal in this same conversation.", Parameters: objectSchema(map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"goal", "task"}}, "title": stringField, "description": stringField, "bot_id": stringField, "parent_goal_id": stringField}, []string{"kind", "title"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in WorkItemInput
			if err := json.Unmarshal(data, &in); err != nil {
				return "", err
			}
			owner := in.BotID
			if owner == "" {
				owner = r.BotID
			}
			item, err := s.store.createWorkItem(c.ID, owner, in, r.BotID)
			return encode(item, err)
		}},
		{Name: "update_work_item", Description: "Update a durable goal or task only in the current conversation. Change its status, title, description, or active member owner. Mark done only when the work is complete; review means checking a delivered result; blocked means unresolved work remains. Reopen completed or cancelled work with todo. This does not start or schedule execution.", Parameters: objectSchema(map[string]any{"work_item_id": stringField, "status": map[string]any{"type": "string", "enum": []string{"todo", "in_progress", "blocked", "review", "done", "cancelled"}}, "title": stringField, "description": stringField, "bot_id": stringField}, []string{"work_item_id"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				WorkItemID string `json:"work_item_id"`
				WorkItemPatch
			}
			if err := json.Unmarshal(data, &in); err != nil {
				return "", err
			}
			item, err := s.store.updateWorkItem(in.WorkItemID, in.WorkItemPatch, c.ID, r.BotID)
			return encode(item, err)
		}},
		{Name: "list_work_items", Description: "List durable goals and tasks. current_conversation shows this conversation's items; self shows only your assigned items across conversations, each retaining its origin conversation_id. history=false lists open work; history=true lists completed and cancelled work. Recorded items are commitments, not automatically executing runs.", Parameters: objectSchema(map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"current_conversation", "self"}}, "history": map[string]any{"type": "boolean", "description": "False: only open work (use before creating a task); true: only completed or cancelled records, excluding open work"}}, []string{"scope", "history"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var in struct {
				Scope   string `json:"scope"`
				History bool   `json:"history"`
			}
			if err := json.Unmarshal(data, &in); err != nil {
				return "", err
			}
			conversationID, botID := c.ID, ""
			if in.Scope == "self" {
				conversationID, botID = "", r.BotID
			} else if in.Scope != "current_conversation" {
				return "", errors.New("scope must be current_conversation or self")
			}
			items, err := s.store.ListWorkItems(conversationID, botID, in.History)
			return encode(map[string]any{"work_items": items}, err)
		}},
	}
}
