package app

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Workspace notifications are deliberately small invalidation signals. A
// client receiving one should refetch the relevant workspace snapshot; no
// conversation content or credentials are included in this stream.
const (
	workspaceScopeBots   = "bots"
	workspaceScopeGroups = "groups"
	workspaceScopeConfig = "config"
	workspaceEventLimit  = 128
)

type workspaceEvent struct {
	ID        int64
	Scope     string
	CreatedAt string
}

func migrateWorkspaceEvents(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS workspace_events(
id INTEGER PRIMARY KEY AUTOINCREMENT,
scope TEXT NOT NULL CHECK(scope IN ('bots','groups','config')),
created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS workspace_events_cursor ON workspace_events(id);`)
	return err
}

// insertWorkspaceEventTx must be called in the same transaction as the
// mutation it describes. The event contains only the invalidation scope.
func insertWorkspaceEventTx(tx *sql.Tx, scope, createdAt string) error {
	if scope != workspaceScopeBots && scope != workspaceScopeGroups && scope != workspaceScopeConfig {
		return fmt.Errorf("invalid workspace event scope")
	}
	_, err := tx.Exec(`INSERT INTO workspace_events(scope,created_at) VALUES(?,?)`, scope, createdAt)
	return err
}

// WorkspaceEvent records a state change that is persisted outside a Store
// transaction (for example, a Codex credential file update).
func (s *Store) WorkspaceEvent(scope string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = insertWorkspaceEventTx(tx, scope, now()); err != nil {
		return 0, err
	}
	var id int64
	// last_insert_rowid is connection-local and remains stable while the
	// transaction holds Store's single SQLite connection.
	if err = tx.QueryRow(`SELECT last_insert_rowid()`).Scan(&id); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) workspaceEvents(after int64) ([]workspaceEvent, error) {
	rows, err := s.db.Query(`SELECT id,scope,created_at FROM workspace_events WHERE id>? ORDER BY id LIMIT ?`, after, workspaceEventLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]workspaceEvent, 0, workspaceEventLimit)
	for rows.Next() {
		var event workspaceEvent
		if err := rows.Scan(&event.ID, &event.Scope, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) workspaceEventCursor() int64 {
	var cursor int64
	_ = s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM workspace_events`).Scan(&cursor)
	return cursor
}

func parseWorkspaceCursor(r *http.Request) int64 {
	value := r.URL.Query().Get("after")
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		value = last
	}
	cursor, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || cursor < 0 {
		return 0
	}
	return cursor
}

func writeWorkspaceReset(w io.Writer) error {
	data, _ := json.Marshal(map[string]any{"revision": 0})
	// A reset is metadata for the workspace cursor, never a conversation
	// event. Omitting id: keeps EventSource's Last-Event-ID untouched.
	_, err := fmt.Fprintf(w, "event: workspace_reset\ndata: %s\n\n", data)
	return err
}

func writeWorkspaceEvent(w io.Writer, event workspaceEvent) error {
	data, _ := json.Marshal(map[string]any{"scope": event.Scope, "revision": event.ID})
	_, err := fmt.Fprintf(w, "id: %d\nevent: workspace\ndata: %s\n\n", event.ID, data)
	return err
}

func writeWorkspaceEventWithoutID(w io.Writer, event workspaceEvent) error {
	data, _ := json.Marshal(map[string]any{"scope": event.Scope, "revision": event.ID})
	_, err := fmt.Fprintf(w, "event: workspace\ndata: %s\n\n", data)
	return err
}

func (s *Server) workspaceEvents(w http.ResponseWriter, r *http.Request) {
	after := parseWorkspaceCursor(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	// Send headers immediately so EventSource.onopen fires even when no
	// workspace mutation has happened since the client connected.
	flusher.Flush()
	reset := after > s.store.workspaceEventCursor()
	if reset {
		// A restored database can have a lower AUTOINCREMENT high-water mark
		// than a client's persisted cursor. Replay the durable invalidation log
		// from its beginning so the client cannot remain stuck above future IDs.
		after = 0
		if err := writeWorkspaceReset(w); err != nil {
			return
		}
		flusher.Flush()
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		events, err := s.store.workspaceEvents(after)
		if err != nil {
			return
		}
		for _, event := range events {
			if err = writeWorkspaceEvent(w, event); err != nil {
				return
			}
			after = event.ID
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
		}
	}
}
