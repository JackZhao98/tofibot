package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

const (
	defaultToolActivityLimit       = 100
	maxToolActivityLimit           = 200
	maxToolActivitySummaryRuns     = 50
	maxToolActivityRunIDLength     = 128
	defaultToolActivityDetailLimit = 25
	maxToolActivityDetailLimit     = 25
	maxToolActivityDetailOffset    = 1000
	maxToolActivityArguments       = 16 * 1024
	maxToolActivityResult          = 32 * 1024
)

// ToolActivity is the durable snapshot of one provider tool call.
type ToolActivity struct {
	ConversationID string `json:"conversation_id"`
	BotID          string `json:"bot_id"`
	RunID          string `json:"run_id"`
	CallID         string `json:"call_id"`
	Name           string `json:"name"`
	Arguments      string `json:"arguments"`
	Result         string `json:"result"`
	Status         string `json:"status"`
	Truncated      bool   `json:"truncated"`
	StartedAt      string `json:"started_at"`
	UpdatedAt      string `json:"updated_at"`
}

// ToolActivityRunSummary is the small, exact count shown before a user asks
// to load a run's potentially large arguments and results.
type ToolActivityRunSummary struct {
	RunID            string `json:"run_id"`
	BotID            string `json:"bot_id"`
	ToolCount        int    `json:"tool_count"`
	CompletedCount   int    `json:"completed_count"`
	FailedCount      int    `json:"failed_count"`
	InterruptedCount int    `json:"interrupted_count"`
	PendingCount     int    `json:"pending_count"`
	StartedAt        string `json:"started_at"`
	UpdatedAt        string `json:"updated_at"`
}

var toolActivityStatuses = map[string]bool{
	"queued":      true,
	"running":     true,
	"completed":   true,
	"failed":      true,
	"interrupted": true,
}

// migrateToolActivity creates the independent tool snapshot. Restart recovery
// is performed by recoverInterruptedRuns so state and replay events share one
// transaction with run and stream-draft recovery.
func migrateToolActivity(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS tool_activities(
 conversation_id TEXT NOT NULL,
 bot_id TEXT NOT NULL,
 run_id TEXT NOT NULL,
 call_id TEXT NOT NULL,
 name TEXT NOT NULL,
 arguments TEXT NOT NULL DEFAULT '',
 result TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL,
 truncated INTEGER NOT NULL DEFAULT 0,
 started_at TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(run_id,call_id)
 );
CREATE INDEX IF NOT EXISTS tool_activities_conversation ON tool_activities(conversation_id,updated_at DESC);`)
	return err
}

// RecordToolEvent atomically updates the call snapshot and appends its SSE
// event. Lifecycle transitions are checked so duplicate call IDs cannot be
// hidden by an upsert.
func (s *Store) RecordToolEvent(conversationID, botID, runID string, event runtime.ToolEvent) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("store is required")
	}
	if conversationID == "" || botID == "" || runID == "" || event.CallID == "" || event.Name == "" {
		return fmt.Errorf("tool event identity is required")
	}
	if !toolActivityStatuses[event.Status] || event.Status == "interrupted" {
		return fmt.Errorf("invalid recorded tool event status %q", event.Status)
	}
	event.Arguments, event.Truncated = boundToolActivityText(event.Arguments, maxToolActivityArguments, event.Truncated)
	event.Result, event.Truncated = boundToolActivityText(event.Result, maxToolActivityResult, event.Truncated)

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// A late callback cannot reopen a terminal attempt's working indicator.
	// Historical callers without a run row retain their existing behavior.
	var runStatus string
	if err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&runStatus); err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil && (runStatus == "failed" || runStatus == "cancelled" || runStatus == "interrupted" || runStatus == "done") {
		return nil
	}

	var current ToolActivity
	var truncated int
	lookupErr := tx.QueryRow(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at
FROM tool_activities WHERE run_id=? AND call_id=?`, runID, event.CallID).Scan(
		&current.ConversationID, &current.BotID, &current.RunID, &current.CallID,
		&current.Name, &current.Arguments, &current.Result, &current.Status,
		&truncated, &current.StartedAt, &current.UpdatedAt)
	if lookupErr != nil && lookupErr != sql.ErrNoRows {
		return lookupErr
	}
	t := now()
	snapshot := ToolActivity{
		ConversationID: conversationID,
		BotID:          botID,
		RunID:          runID,
		CallID:         event.CallID,
		Name:           event.Name,
		Arguments:      event.Arguments,
		Result:         event.Result,
		Status:         event.Status,
		Truncated:      event.Truncated,
		StartedAt:      t,
		UpdatedAt:      t,
	}
	if lookupErr == sql.ErrNoRows {
		if event.Status != "queued" {
			return fmt.Errorf("tool call %q has no queued record", event.CallID)
		}
		if _, err = tx.Exec(`INSERT INTO tool_activities(conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, snapshot.ConversationID, snapshot.BotID, snapshot.RunID, snapshot.CallID, snapshot.Name, snapshot.Arguments, snapshot.Result, snapshot.Status, boolInt(snapshot.Truncated), snapshot.StartedAt, snapshot.UpdatedAt); err != nil {
			return err
		}
	} else {
		current.Truncated = truncated != 0
		if current.ConversationID != conversationID || current.BotID != botID || current.Name != event.Name {
			return fmt.Errorf("tool call %q identity changed", event.CallID)
		}
		if event.Status == "queued" {
			return fmt.Errorf("duplicate queued tool call %q", event.CallID)
		}
		if event.Status == "running" && current.Status != "queued" {
			return fmt.Errorf("tool call %q cannot transition %s to running", event.CallID, current.Status)
		}
		if (event.Status == "completed" || event.Status == "failed") && current.Status != "queued" && current.Status != "running" {
			return fmt.Errorf("tool call %q cannot transition %s to %s", event.CallID, current.Status, event.Status)
		}
		arguments := current.Arguments
		if event.Arguments != "" {
			arguments = event.Arguments
		}
		result := current.Result
		if event.Result != "" {
			result = event.Result
		}
		truncatedValue := current.Truncated || event.Truncated
		if _, err = tx.Exec(`UPDATE tool_activities SET arguments=?,result=?,status=?,truncated=?,updated_at=? WHERE run_id=? AND call_id=?`, arguments, result, event.Status, boolInt(truncatedValue), t, runID, event.CallID); err != nil {
			return err
		}
		snapshot = current
		snapshot.Arguments = arguments
		snapshot.Result = result
		snapshot.Status = event.Status
		snapshot.Truncated = truncatedValue
		snapshot.UpdatedAt = t
	}

	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conversationID, "tool", string(b), t); err != nil {
		return err
	}
	return tx.Commit()
}

// HasCompletedTool reports whether a run completed a named tool call. It is
// used by background jobs whose success contract is stronger than "the model
// turn returned".
func (s *Store) HasCompletedTool(runID, name string) (bool, error) {
	var found int
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM tool_activities WHERE run_id=? AND name=? AND status='completed')`, runID, name).Scan(&found); err != nil {
		return false, err
	}
	return found != 0, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func boundToolActivityText(value string, max int, truncated bool) (string, bool) {
	runes := []rune(value)
	if len(runes) <= max {
		return value, truncated
	}
	return string(runes[:max]) + "…", true
}

// ToolActivities returns recent snapshots with a hard server-side bound.
func (s *Store) ToolActivities(conversationID string, limit int) ([]ToolActivity, error) {
	if limit <= 0 {
		limit = defaultToolActivityLimit
	}
	if limit > maxToolActivityLimit {
		limit = maxToolActivityLimit
	}
	rows, err := s.db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at
FROM tool_activities WHERE conversation_id=? ORDER BY updated_at DESC,run_id DESC,call_id DESC LIMIT ?`, conversationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	activities := make([]ToolActivity, 0)
	for rows.Next() {
		var activity ToolActivity
		var truncated int
		if err = rows.Scan(&activity.ConversationID, &activity.BotID, &activity.RunID, &activity.CallID, &activity.Name, &activity.Arguments, &activity.Result, &activity.Status, &truncated, &activity.StartedAt, &activity.UpdatedAt); err != nil {
			return nil, err
		}
		activity.Truncated = truncated != 0
		activities = append(activities, activity)
	}
	return activities, rows.Err()
}

// ToolActivitySummaries returns exact, lightweight counts for explicitly
// loaded runs. It deliberately does not return arguments or results.
func (s *Store) ToolActivitySummaries(conversationID string, runIDs []string) ([]ToolActivityRunSummary, error) {
	if len(runIDs) == 0 || len(runIDs) > maxToolActivitySummaryRuns {
		return nil, fmt.Errorf("invalid tool activity run count")
	}
	placeholders := make([]string, 0, len(runIDs))
	args := make([]any, 0, len(runIDs)+1)
	args = append(args, conversationID)
	for _, id := range runIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := s.db.Query(`SELECT run_id,MIN(bot_id),COUNT(*),COALESCE(SUM(status='completed'),0),COALESCE(SUM(status='failed'),0),COALESCE(SUM(status='interrupted'),0),COALESCE(SUM(status IN ('queued','running')),0),COALESCE(MIN(started_at),''),COALESCE(MAX(updated_at),'')
FROM tool_activities WHERE conversation_id=? AND run_id IN (`+strings.Join(placeholders, ",")+`) GROUP BY run_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := make([]ToolActivityRunSummary, 0, len(runIDs))
	for rows.Next() {
		var summary ToolActivityRunSummary
		if err = rows.Scan(&summary.RunID, &summary.BotID, &summary.ToolCount, &summary.CompletedCount, &summary.FailedCount, &summary.InterruptedCount, &summary.PendingCount, &summary.StartedAt, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

// ToolActivitiesForRun returns one bounded detail page for a single run. The
// summary endpoint remains the source of the complete count.
func (s *Store) ToolActivitiesForRun(conversationID, runID string, offset, limit int) ([]ToolActivity, int, error) {
	if runID == "" || len(runID) > maxToolActivityRunIDLength || offset < 0 || offset > maxToolActivityDetailOffset {
		return nil, 0, fmt.Errorf("invalid tool activity page")
	}
	if limit <= 0 {
		limit = defaultToolActivityDetailLimit
	}
	if limit > maxToolActivityDetailLimit {
		limit = maxToolActivityDetailLimit
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tool_activities WHERE conversation_id=? AND run_id=?`, conversationID, runID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at
FROM tool_activities WHERE conversation_id=? AND run_id=? ORDER BY started_at ASC,call_id ASC LIMIT ? OFFSET ?`, conversationID, runID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	activities := make([]ToolActivity, 0, min(limit, max(0, total-offset)))
	for rows.Next() {
		var activity ToolActivity
		var truncated int
		if err = rows.Scan(&activity.ConversationID, &activity.BotID, &activity.RunID, &activity.CallID, &activity.Name, &activity.Arguments, &activity.Result, &activity.Status, &truncated, &activity.StartedAt, &activity.UpdatedAt); err != nil {
			return nil, 0, err
		}
		activity.Truncated = truncated != 0
		activities = append(activities, activity)
	}
	return activities, total, rows.Err()
}

// routeToolActivities handles /api/conversations/{id}/tools.
func (s *Server) routeToolActivities(w http.ResponseWriter, r *http.Request, p string) bool {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) != 3 || parts[0] != "conversations" || parts[2] != "tools" {
		return false
	}
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
		return true
	}
	conversationID, err := url.PathUnescape(parts[1])
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid conversation id")
		return true
	}
	if _, err = s.store.GetConversation(conversationID); err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "conversation not found")
		return true
	}
	query := r.URL.Query()
	runID := strings.TrimSpace(query.Get("run_id"))
	runIDs, invalidRunIDs := toolActivityRunIDs(query.Get("run_ids"))
	if invalidRunIDs || (runID != "" && len(runIDs) > 0) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid tool activity run selection")
		return true
	}
	if len(runIDs) > 0 {
		summaries, err := s.store.ToolActivitySummaries(conversationID, runIDs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"summaries": summaries})
		return true
	}
	if runID != "" {
		offset := 0
		var offsetErr error
		if raw := query.Get("offset"); raw != "" {
			offset, offsetErr = strconv.Atoi(raw)
		}
		limit := defaultToolActivityDetailLimit
		var limitErr error
		if raw := query.Get("limit"); raw != "" {
			limit, limitErr = strconv.Atoi(raw)
		}
		if offsetErr != nil || limitErr != nil || offset < 0 || limit <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid tool activity page")
			return true
		}
		activities, total, err := s.store.ToolActivitiesForRun(conversationID, runID, offset, limit)
		if err != nil {
			if strings.Contains(err.Error(), "invalid tool activity page") {
				writeErr(w, http.StatusBadRequest, "invalid_request", "invalid tool activity page")
			} else {
				writeErr(w, http.StatusInternalServerError, "storage", err.Error())
			}
			return true
		}
		hasMore := offset+len(activities) < total && offset+len(activities) <= maxToolActivityDetailOffset
		writeJSON(w, http.StatusOK, map[string]any{"activities": activities, "tool_count": total, "has_more": hasMore})
		return true
	}
	limit, _ := strconv.Atoi(query.Get("limit"))
	activities, err := s.store.ToolActivities(conversationID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", err.Error())
		return true
	}
	writeJSON(w, http.StatusOK, map[string]any{"activities": activities})
	return true
}

func toolActivityRunIDs(raw string) ([]string, bool) {
	if raw == "" {
		return nil, false
	}
	seen := make(map[string]bool)
	ids := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" || len(id) > maxToolActivityRunIDLength || seen[id] {
			return nil, true
		}
		seen[id] = true
		ids = append(ids, id)
		if len(ids) > maxToolActivitySummaryRuns {
			return nil, true
		}
	}
	return ids, false
}

// InterruptToolActivities closes unfinished traces when a run exits without
// receiving every tool result. Completed effects are never replayed.
func (s *Store) InterruptToolActivities(runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT conversation_id,bot_id,run_id,call_id,name,arguments,result,status,truncated,started_at,updated_at FROM tool_activities WHERE run_id=? AND status IN ('queued','running')`, runID)
	if err != nil {
		return err
	}
	var pending []ToolActivity
	for rows.Next() {
		var a ToolActivity
		var truncated int
		if err = rows.Scan(&a.ConversationID, &a.BotID, &a.RunID, &a.CallID, &a.Name, &a.Arguments, &a.Result, &a.Status, &truncated, &a.StartedAt, &a.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		a.Truncated = truncated != 0
		a.Status = "interrupted"
		a.UpdatedAt = now()
		pending = append(pending, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range pending {
		if _, err = tx.Exec(`UPDATE tool_activities SET status='interrupted',updated_at=? WHERE run_id=? AND call_id=?`, a.UpdatedAt, a.RunID, a.CallID); err != nil {
			return err
		}
		data, _ := json.Marshal(a)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, a.ConversationID, "tool", string(data), a.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
