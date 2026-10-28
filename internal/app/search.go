package app

import (
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// Search palette backend: GET /api/search?q=&kind=all|chats|messages&limit=
//
// Messages are found through an FTS5 external-content index over messages.content
// with the trigram tokenizer. Trigram is case-insensitive substring matching with no
// word segmentation, so Chinese, Japanese and Korean text matches like any other.
// Its one limit is that a query shorter than three characters has no trigram; those
// run as a bounded LIKE, newest rows first, and stop at the limit.
// The index holds every message; what a person may see is decided at query time
// (visible, unarchived conversations, no internal kinds), so a conversation that is
// archived later needs no index change.

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
	searchChatLimit    = 8
	searchMaxQueryLen  = 200
	searchSnippetLead  = 40
	searchSnippetLen   = 140
)

const searchHiddenKinds = `'bot_result','scheduled_task','internal','progress','notice','message_ref'`

// migrateSearch creates the index, its triggers and, only when the index is new, fills it
// from existing messages. Every statement is idempotent.
func migrateSearch(db *sql.DB) error {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='messages_fts'`).Scan(&exists); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(content, content='messages', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER IF NOT EXISTS messages_fts_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
END;
CREATE TRIGGER IF NOT EXISTS messages_fts_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
END;
CREATE TRIGGER IF NOT EXISTS messages_fts_au AFTER UPDATE OF content ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
  INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
END;`); err != nil {
		return err
	}
	if exists == 0 {
		if _, err = tx.Exec(`INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SearchChat is a conversation whose name matches.
type SearchChat struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	BotID     string `json:"bot_id,omitempty"`
	UpdatedAt string `json:"updated_at"`
	// MatchStart and MatchLen locate the hit inside Name, in UTF-16 units.
	MatchStart int `json:"match_start"`
	MatchLen   int `json:"match_len"`
}

// SearchMessage is one message hit. MatchStart and MatchLen locate the hit inside
// Snippet, in UTF-16 code units so a browser can slice the string directly.
type SearchMessage struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversation_id"`
	ConversationName string `json:"conversation_name"`
	ConversationKind string `json:"conversation_kind"`
	Seq              int64  `json:"seq"`
	Role             string `json:"role"`
	SenderBotID      string `json:"sender_bot_id,omitempty"`
	CreatedAt        string `json:"created_at"`
	Snippet          string `json:"snippet"`
	MatchStart       int    `json:"match_start"`
	MatchLen         int    `json:"match_len"`
}

type SearchResult struct {
	Query    string          `json:"query"`
	Chats    []SearchChat    `json:"chats"`
	Messages []SearchMessage `json:"messages"`
}

func ftsPhrase(q string) string { return `"` + strings.ReplaceAll(q, `"`, `""`) + `"` }

func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(q)
}

// SearchAll finds conversations by name and messages by body. kind is all, chats or messages.
func (s *Store) SearchAll(q, kind string, limit int) (SearchResult, error) {
	q = strings.TrimSpace(q)
	out := SearchResult{Query: q, Chats: []SearchChat{}, Messages: []SearchMessage{}}
	if q == "" {
		return out, nil
	}
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	if kind != "chats" {
		msgs, err := s.searchMessages(q, limit)
		if err != nil {
			return out, err
		}
		if kind == "messages" || kind == "all" || kind == "" {
			out.Messages = msgs
		}
	}
	if kind != "messages" {
		chats, err := s.searchChats(q, searchChatLimit)
		if err != nil {
			return out, err
		}
		out.Chats = chats
	}
	return out, nil
}

func (s *Store) searchChats(q string, limit int) ([]SearchChat, error) {
	rows, err := s.db.Query(`SELECT id,kind,name,COALESCE(bot_id,''),updated_at FROM conversations
		WHERE archived=0 AND user_visible=1 AND name LIKE ? ESCAPE '\' ORDER BY updated_at DESC,id LIMIT ?`, "%"+likeEscape(q)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SearchChat{}
	for rows.Next() {
		var c SearchChat
		if err = rows.Scan(&c.ID, &c.Kind, &c.Name, &c.BotID, &c.UpdatedAt); err != nil {
			return nil, err
		}
		r := []rune(c.Name)
		if at, n := foldIndex(r, []rune(q)); at >= 0 {
			c.MatchStart, c.MatchLen = utf16Len(r[:at]), utf16Len(r[at:at+n])
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) searchMessages(q string, limit int) ([]SearchMessage, error) {
	const cols = `SELECT m.id,m.conversation_id,c.name,c.kind,m.seq,m.role,COALESCE(m.sender_bot_id,''),m.created_at,m.content
		FROM messages m JOIN conversations c ON c.id=m.conversation_id`
	const visible = ` c.archived=0 AND c.user_visible=1 AND m.kind NOT IN (` + searchHiddenKinds + `) AND NOT `
	var rows *sql.Rows
	var err error
	if utf8.RuneCountInString(q) >= 3 {
		rows, err = s.db.Query(cols+` WHERE m.rowid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?) AND`+visible+legacyScheduledMessageSQL("m")+
			` ORDER BY m.created_at DESC,m.seq DESC LIMIT ?`, ftsPhrase(q), limit)
	} else {
		rows, err = s.db.Query(cols+` WHERE m.content LIKE ? ESCAPE '\' AND`+visible+legacyScheduledMessageSQL("m")+
			` ORDER BY m.rowid DESC LIMIT ?`, "%"+likeEscape(q)+"%", limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	needle := []rune(q)
	out := []SearchMessage{}
	for rows.Next() {
		var m SearchMessage
		var content string
		if err = rows.Scan(&m.ID, &m.ConversationID, &m.ConversationName, &m.ConversationKind, &m.Seq, &m.Role, &m.SenderBotID, &m.CreatedAt, &content); err != nil {
			return nil, err
		}
		m.Snippet, m.MatchStart, m.MatchLen = snippetAround(content, needle)
		out = append(out, m)
	}
	return out, rows.Err()
}

func utf16Len(r []rune) int { return len(utf16.Encode(r)) }

// foldIndex finds needle in hay ignoring case, rune by rune, and returns the start and length in runes.
func foldIndex(hay, needle []rune) (int, int) {
	if len(needle) == 0 {
		return -1, 0
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		ok := true
		for j, n := range needle {
			if unicode.ToLower(hay[i+j]) != unicode.ToLower(n) {
				ok = false
				break
			}
		}
		if ok {
			return i, len(needle)
		}
	}
	return -1, 0
}

// snippetAround cuts a short window of text around the first hit: newlines become spaces,
// ellipses mark the cut ends, and the returned offsets point at the hit inside the snippet.
func snippetAround(content string, needle []rune) (string, int, int) {
	r := []rune(content)
	at, n := foldIndex(r, needle)
	if at < 0 {
		at, n = 0, 0
	}
	start := at - searchSnippetLead
	if start < 0 {
		start = 0
	}
	end := start + searchSnippetLen
	if end > len(r) {
		end = len(r)
	}
	if at+n > end {
		end = at + n
	}
	win := make([]rune, 0, end-start+2)
	hitAt := at - start
	if start > 0 {
		win = append(win, '…')
		hitAt++
	}
	for _, c := range r[start:end] {
		if c == '\n' || c == '\r' || c == '\t' {
			c = ' '
		}
		win = append(win, c)
	}
	if end < len(r) {
		win = append(win, '…')
	}
	if n == 0 {
		return string(win), 0, 0
	}
	return string(win), utf16Len(win[:hitAt]), utf16Len(win[hitAt : hitAt+n])
}

// routeSearch handles GET /api/search behind the owner session gate like the other conversation routes.
func (s *Server) routeSearch(w http.ResponseWriter, r *http.Request, p string) bool {
	if strings.Trim(p, "/") != "search" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method", "method not allowed")
		return true
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) > searchMaxQueryLen {
		writeErr(w, http.StatusBadRequest, "invalid_request", "query too long")
		return true
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "", "all", "chats", "messages":
	default:
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid kind")
		return true
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "invalid_request", "invalid limit")
			return true
		}
		limit = n
	}
	res, err := s.store.SearchAll(q, kind, limit)
	if err != nil {
		log.Printf("[search] %v", err)
		writeErr(w, http.StatusInternalServerError, "storage", "could not search")
		return true
	}
	writeJSON(w, http.StatusOK, res)
	return true
}
