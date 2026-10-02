package app

import (
	"database/sql"
	"net/http"
	"strings"
)

func migrateInbox(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS conversation_reads(conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE, read_seq INTEGER NOT NULL DEFAULT 0)`)
	return err
}

func (s *Store) MarkRead(conv string, seq int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var maxSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE conversation_id=?`, conv).Scan(&maxSeq); err != nil {
		return 0, err
	}
	if seq < 0 {
		seq = 0
	}
	if seq > maxSeq {
		seq = maxSeq
	}
	if _, err = tx.Exec(`INSERT INTO conversation_reads(conversation_id,read_seq) VALUES(?,?) ON CONFLICT(conversation_id) DO UPDATE SET read_seq=MAX(read_seq,excluded.read_seq)`, conv, seq); err != nil {
		return 0, err
	}
	if err = tx.QueryRow(`SELECT read_seq FROM conversation_reads WHERE conversation_id=?`, conv).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

func (s *Store) MarkUnread(conv string, seq int64) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var maxSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE conversation_id=?`, conv).Scan(&maxSeq); err != nil {
		return 0, err
	}
	if seq < 0 {
		seq = 0
	}
	if seq > maxSeq {
		seq = maxSeq
	}
	if _, err = tx.Exec(`INSERT INTO conversation_reads(conversation_id,read_seq) VALUES(?,?) ON CONFLICT(conversation_id) DO UPDATE SET read_seq=MIN(read_seq,excluded.read_seq)`, conv, seq); err != nil {
		return 0, err
	}
	if err = tx.QueryRow(`SELECT read_seq FROM conversation_reads WHERE conversation_id=?`, conv).Scan(&seq); err != nil {
		return 0, err
	}
	return seq, tx.Commit()
}

func (s *Server) routeInbox(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "conversations/") || !strings.HasSuffix(p, "/read") {
		return false
	}
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method", "method not allowed")
		return true
	}
	id := strings.TrimSuffix(strings.TrimPrefix(p, "conversations/"), "/read")
	if _, err := s.store.GetConversation(id); err != nil {
		writeErr(w, 404, "not_found", "conversation not found")
		return true
	}
	var x struct {
		Seq    int64 `json:"seq"`
		Unread bool  `json:"unread"`
	}
	if decode(r, &x) != nil {
		writeErr(w, 400, "invalid_request", "seq required")
		return true
	}
	seq, err := s.store.MarkRead(id, x.Seq)
	if x.Unread {
		seq, err = s.store.MarkUnread(id, x.Seq)
	}
	if err != nil {
		writeErr(w, 500, "storage", err.Error())
		return true
	}
	writeJSON(w, 200, map[string]any{"conversation_id": id, "read_seq": seq})
	return true
}
