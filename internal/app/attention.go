package app

// Server-side notification events (cloud-services design 4.1, decision D78, phase 1).
//
// The question "should this make the person look" used to live only in the web
// client (ui/src/App.tsx, the 5 s conversation poll). The server now owns one
// decision point: classifyAttention. Where something is posted (or a run stops
// for a person, or the monthly spend crosses a threshold) the caller asks the
// classifier and, when it names a kind, writes one pointer row into attention in
// the same transaction. A row holds ids and a sequence number, never text: the
// content is read from the conversation API. Delivery channels, per-kind
// switches, quiet hours and read-cursor suppression come with the first channel
// (phase 2) and read this table; nothing here sends anything.
//
// The message conditions copy the web client's rule exactly, so the web and a
// later channel agree: an assistant message, not internal, in a conversation
// the conversation list shows (visible and not archived), and not one of the
// kinds the list hides (bot_result, scheduled_task, internal). A delegation or
// scheduled-task result (forward_result) is a task_result, everything else a
// reply. Messages that were never posted (suppressed by an earlier rule) have no
// row to classify, so no second quiet rule exists here.

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	attentionReply      = "reply"
	attentionTaskResult = "task_result"
	attentionNeedsYou   = "needs_you"
	attentionSpendAlert = "spend_alert"

	attentionKeepDays     = 30
	attentionDefaultLimit = 50
	attentionMaxLimit     = 200
)

// The signal sources the classifier understands.
const (
	attentionFromMessage = "message"
	attentionFromRun     = "run_status"
	attentionFromSpend   = "spend"
)

// attentionSignal is the one input of the classifier. Only the fields of the
// chosen Source are read.
type attentionSignal struct {
	Source string
	// Message source: the stored message and what the conversation list knows about it.
	Role, Kind string
	Internal   bool // the message belongs to a delegated run that started in another conversation
	Listed     bool // the conversation is visible to the person and not archived
	// Run source: the status the run just entered.
	RunStatus string
	// Spend source: whole percent of the monthly limit reached.
	SpendPercent int
}

// classifyAttention returns the notification kind for a signal, or "" when the
// person need not be told.
func classifyAttention(sig attentionSignal) string {
	switch sig.Source {
	case attentionFromMessage:
		if sig.Role != "assistant" || sig.Internal || !sig.Listed {
			return ""
		}
		switch sig.Kind {
		case messageKindBotResult, "scheduled_task", messageKindInternal:
			// Private anchors and run-internal text: the conversation list never shows them.
			return ""
		case "forward_result":
			return attentionTaskResult
		}
		return attentionReply
	case attentionFromRun:
		if sig.RunStatus == runWaiting {
			return attentionNeedsYou
		}
	case attentionFromSpend:
		if sig.SpendPercent >= spendAlertPercent {
			return attentionSpendAlert
		}
	}
	return ""
}

// migrateAttention creates the pointer table. AUTOINCREMENT keeps ids rising
// after retention empties the table, so a client cursor never points past a
// reused id. The primary key serves "rows after id"; the time index serves
// retention.
func migrateAttention(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS attention(
id INTEGER PRIMARY KEY AUTOINCREMENT,
kind TEXT NOT NULL,
conversation_id TEXT,
bot_id TEXT,
message_seq INTEGER,
created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS attention_created ON attention(created_at);`)
	return err
}

// recordAttentionTx writes one row and signals the workspace stream. It must run
// in the transaction of the change it describes.
func recordAttentionTx(tx *sql.Tx, kind, conversationID, botID string, messageSeq *int64) error {
	var seq any
	if messageSeq != nil && *messageSeq > 0 {
		seq = *messageSeq
	}
	t := now()
	if _, err := tx.Exec(`INSERT INTO attention(kind,conversation_id,bot_id,message_seq,created_at) VALUES(?,?,?,?,?)`,
		kind, nullString(conversationID), nullString(botID), seq, t); err != nil {
		return err
	}
	return insertWorkspaceEventTx(tx, workspaceScopeNotifications, t)
}

// recordMessageAttentionTx classifies a message that was just inserted and
// records its row. Callers invoke it right after the INSERT INTO messages.
func recordMessageAttentionTx(tx *sql.Tx, m Message) error {
	if m.Role != "assistant" {
		return nil
	}
	var visible, archived, internal int
	// The internal test is the conversation list's own (ListConversations): a
	// message of a run that started in another conversation, except a user_message.
	err := tx.QueryRow(`SELECT c.user_visible, c.archived,
		CASE WHEN ?1='user_message' THEN 0 WHEN ?2<>'' AND EXISTS(SELECT 1 FROM runs r WHERE r.id=?2 AND r.origin_conversation_id<>c.id) THEN 1 ELSE 0 END
		FROM conversations c WHERE c.id=?3`, m.Kind, m.RunID, m.ConversationID).Scan(&visible, &archived, &internal)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	kind := classifyAttention(attentionSignal{Source: attentionFromMessage, Role: m.Role, Kind: m.Kind, Internal: internal != 0, Listed: visible != 0 && archived == 0})
	if kind == "" {
		return nil
	}
	seq := m.Seq
	return recordAttentionTx(tx, kind, m.ConversationID, m.SenderBotID, &seq)
}

// recordRunWaitingTx records that a run stopped and waits for a person (a
// question card or an approval card).
func recordRunWaitingTx(tx *sql.Tx, r Run) error {
	kind := classifyAttention(attentionSignal{Source: attentionFromRun, RunStatus: r.Status})
	if kind == "" {
		return nil
	}
	return recordAttentionTx(tx, kind, r.ConversationID, r.BotID, nil)
}

// AttentionRow is one notification event as the API serves it: pointers only.
type AttentionRow struct {
	ID             int64  `json:"id"`
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
	BotID          string `json:"bot_id,omitempty"`
	MessageSeq     *int64 `json:"message_seq"`
	CreatedAt      string `json:"created_at"`
}

// attentionAfter returns rows with id > after, oldest first, at most limit.
func (s *Store) attentionAfter(after int64, limit int) ([]AttentionRow, error) {
	rows, err := s.db.Query(`SELECT id,kind,COALESCE(conversation_id,''),COALESCE(bot_id,''),message_seq,created_at FROM attention WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AttentionRow, 0, limit)
	for rows.Next() {
		var row AttentionRow
		var seq sql.NullInt64
		if err := rows.Scan(&row.ID, &row.Kind, &row.ConversationID, &row.BotID, &seq, &row.CreatedAt); err != nil {
			return nil, err
		}
		if seq.Valid {
			v := seq.Int64
			row.MessageSeq = &v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// pruneAttention deletes rows older than the retention window.
func (s *Store) pruneAttention(at time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM attention WHERE created_at < ?`, stampTime(at.Add(-attentionKeepDays*24*time.Hour)))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// runAttentionRetention rides the daily retention pass of the wait sweeper.
func (s *Server) runAttentionRetention(at time.Time) {
	if _, err := s.store.pruneAttention(at); err != nil {
		logTaskError("attention retention", err)
	}
}

// routeNotifications serves GET /api/notifications?after=<id>&limit=<n>. The
// answer holds pointers, never content; the body of a message comes from the
// conversation API.
func (s *Server) routeNotifications(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "notifications" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	query := r.URL.Query()
	var after int64
	if v := strings.TrimSpace(query.Get("after")); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid_cursor", "after must be a non-negative integer")
			return true
		}
		after = n
	}
	limit := attentionDefaultLimit
	if v := strings.TrimSpace(query.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "invalid_limit", "limit must be a positive integer")
			return true
		}
		limit = min(n, attentionMaxLimit)
	}
	rows, err := s.store.attentionAfter(after, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "storage", "notifications unavailable")
		return true
	}
	next := after
	if len(rows) > 0 {
		next = rows[len(rows)-1].ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		Notifications []AttentionRow `json:"notifications"`
		NextAfter     int64          `json:"next_after"`
	}{rows, next})
	return true
}
