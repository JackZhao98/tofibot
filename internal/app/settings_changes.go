package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

// The audit trail of settings the tool changed. A row is written before the change and marked applied after it, so a crash between
// the two leaves a row that is never shown and never reverted. Values are plain text, at most maxChangeValueBytes; a longer value is
// cut and the change cannot be reverted from this table.

const (
	maxChangeValueBytes   = 4 << 10
	maxNoticeValueRunes   = 160
	settingsChangesLimit  = 50
	settingsChangeNotice  = "settings_change"
	changeActorBot        = "bot"
	changeActorUser       = "user"
	changeActorRevert     = "revert"
	defaultSettingsChange = 8
)

func migrateSettingsChanges(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS settings_changes(
id TEXT PRIMARY KEY,
at TEXT NOT NULL,
actor_kind TEXT NOT NULL,
bot_id TEXT NOT NULL DEFAULT '',
run_id TEXT NOT NULL DEFAULT '',
conversation_id TEXT NOT NULL DEFAULT '',
key TEXT NOT NULL,
target_bot_id TEXT NOT NULL DEFAULT '',
target_conversation_id TEXT NOT NULL DEFAULT '',
target_connection TEXT NOT NULL DEFAULT '',
target_tool TEXT NOT NULL DEFAULT '',
old_value TEXT NOT NULL DEFAULT '',
new_value TEXT NOT NULL DEFAULT '',
risk TEXT NOT NULL DEFAULT '',
approval_question_id TEXT NOT NULL DEFAULT '',
reverted_by TEXT NOT NULL DEFAULT '',
applied INTEGER NOT NULL DEFAULT 0,
revertable INTEGER NOT NULL DEFAULT 1,
notice_message_id TEXT NOT NULL DEFAULT '')`); err != nil {
		return err
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS settings_changes_at ON settings_changes(at)`)
	return err
}

// SettingsChange is one row of the audit trail.
type SettingsChange struct {
	ID                   string `json:"id"`
	At                   string `json:"at"`
	ActorKind            string `json:"actor_kind"`
	BotID                string `json:"bot_id,omitempty"`
	RunID                string `json:"run_id,omitempty"`
	ConversationID       string `json:"conversation_id,omitempty"`
	Key                  string `json:"key"`
	TargetBotID          string `json:"target_bot_id,omitempty"`
	TargetConversationID string `json:"target_conversation_id,omitempty"`
	TargetConnection     string `json:"target_connection,omitempty"`
	TargetTool           string `json:"target_tool,omitempty"`
	Old                  string `json:"old_value"`
	New                  string `json:"new_value"`
	Risk                 string `json:"risk"`
	ApprovalQuestionID   string `json:"approval_question_id,omitempty"`
	RevertedBy           string `json:"reverted_by,omitempty"`
	Revertable           bool   `json:"revertable"`
	NoticeMessageID      string `json:"notice_message_id,omitempty"`
	// Names for the Maintenance list; filled by the HTTP list only.
	LabelKey   string `json:"label_key,omitempty"`
	BotName    string `json:"bot_name,omitempty"`
	TargetName string `json:"target_name,omitempty"`
}

func (c SettingsChange) target() Target {
	return Target{BotID: c.TargetBotID, ConversationID: c.TargetConversationID, Connection: c.TargetConnection, Tool: c.TargetTool}
}

// boundChangeValue cuts a value to the table's bound; the second result says whether anything was cut.
func boundChangeValue(v string) (string, bool) {
	if len(v) <= maxChangeValueBytes {
		return v, false
	}
	cut := v[:maxChangeValueBytes]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

func (s *Store) beginSettingsChange(c SettingsChange) (SettingsChange, error) {
	c.ID, c.At = newID(), now()
	old, oldCut := boundChangeValue(c.Old)
	nw, newCut := boundChangeValue(c.New)
	c.Old, c.New, c.Revertable = old, nw, !(oldCut || newCut)
	_, err := s.db.Exec(`INSERT INTO settings_changes(id,at,actor_kind,bot_id,run_id,conversation_id,key,target_bot_id,target_conversation_id,target_connection,target_tool,old_value,new_value,risk,approval_question_id,applied,revertable) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)`,
		c.ID, c.At, c.ActorKind, c.BotID, c.RunID, c.ConversationID, c.Key, c.TargetBotID, c.TargetConversationID, c.TargetConnection, c.TargetTool, c.Old, c.New, c.Risk, c.ApprovalQuestionID, c.Revertable)
	return c, err
}

func (s *Store) finishSettingsChange(id string, applied bool, noticeMessageID string) error {
	if !applied {
		_, err := s.db.Exec(`DELETE FROM settings_changes WHERE id=? AND applied=0`, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE settings_changes SET applied=1,notice_message_id=? WHERE id=?`, noticeMessageID, id)
	return err
}

const settingsChangeColumns = `id,at,actor_kind,bot_id,run_id,conversation_id,key,target_bot_id,target_conversation_id,target_connection,target_tool,old_value,new_value,risk,approval_question_id,reverted_by,revertable,notice_message_id`

func scanSettingsChange(row interface{ Scan(...any) error }) (SettingsChange, error) {
	var c SettingsChange
	var revertable int
	err := row.Scan(&c.ID, &c.At, &c.ActorKind, &c.BotID, &c.RunID, &c.ConversationID, &c.Key, &c.TargetBotID, &c.TargetConversationID, &c.TargetConnection, &c.TargetTool, &c.Old, &c.New, &c.Risk, &c.ApprovalQuestionID, &c.RevertedBy, &revertable, &c.NoticeMessageID)
	// A change that was undone is not offered for undo again.
	c.Revertable = revertable != 0 && c.RevertedBy == ""
	return c, err
}

// settingsChange reads one applied change.
func (s *Store) settingsChange(id string) (SettingsChange, error) {
	return scanSettingsChange(s.db.QueryRow(`SELECT `+settingsChangeColumns+` FROM settings_changes WHERE id=? AND applied=1`, id))
}

// recentSettingsChanges lists the newest applied changes, for the Maintenance page.
func (s *Store) recentSettingsChanges(limit int) ([]SettingsChange, error) {
	rows, err := s.db.Query(`SELECT `+settingsChangeColumns+` FROM settings_changes WHERE applied=1 AND actor_kind<>? ORDER BY at DESC,id DESC LIMIT ?`, changeActorRevert, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SettingsChange{}
	for rows.Next() {
		c, err := scanSettingsChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// runSettingsChangeCount counts the direct changes one run made.
func (s *Store) runSettingsChangeCount(runID string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM settings_changes WHERE run_id=? AND actor_kind=? AND applied=1 AND risk=?`, runID, changeActorBot, string(RiskDirect)).Scan(&n)
	return n
}

func (s *Store) markSettingsReverted(id, by string) error {
	res, err := s.db.Exec(`UPDATE settings_changes SET reverted_by=? WHERE id=? AND reverted_by=''`, by, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("the change was already reverted")
	}
	return nil
}

// addSettingsChangeNotice posts the chat line "Timezone: A -> B" with its Undo in the conversation of the run. It is sent by "system"
// with kind notice, so it never enters a Bot's history. The UI words it from the notice fields in the person's language.
func (s *Store) addSettingsChangeNotice(c SettingsChange, labelKey, labelEnglish string) (string, error) {
	if c.ConversationID == "" {
		return "", nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	t := now()
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, c.ConversationID, c.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return "", err
	}
	notice := &HandoffNotice{Type: settingsChangeNotice, FromBotID: c.BotID, ToBotID: c.BotID, TargetConversationID: c.ConversationID,
		ChangeID: c.ID, SettingKey: c.Key, LabelKey: labelKey, OldValue: clipNoticeValue(c.Old), NewValue: clipNoticeValue(c.New), Revertable: c.Revertable}
	data, _ := json.Marshal(notice)
	text := labelEnglish + ": " + notice.OldValue + " → " + notice.NewValue
	m := Message{ID: newID(), ConversationID: c.ConversationID, Seq: seq, Role: "assistant", Kind: "notice", SenderBotID: "system", Content: text, Notice: notice, CreatedAt: t}
	if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, nil, m.Content, string(data), t); err != nil {
		return "", err
	}
	if err := insertRecoveryEvent(tx, m.ConversationID, "message", m, t); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, m.ConversationID); err != nil {
		return "", err
	}
	return m.ID, tx.Commit()
}

// markSettingsNoticeReverted rewrites the notice of a reverted change, so the Undo is replaced by "Undone" on every client.
func (s *Store) markSettingsNoticeReverted(c SettingsChange) {
	if c.NoticeMessageID == "" {
		return
	}
	var data, conv string
	if err := s.db.QueryRow(`SELECT notice_data,conversation_id FROM messages WHERE id=?`, c.NoticeMessageID).Scan(&data, &conv); err != nil {
		return
	}
	var notice HandoffNotice
	if json.Unmarshal([]byte(data), &notice) != nil {
		return
	}
	notice.Reverted = true
	notice.Revertable = false
	updated, _ := json.Marshal(notice)
	if _, err := s.db.Exec(`UPDATE messages SET notice_data=? WHERE id=?`, string(updated), c.NoticeMessageID); err != nil {
		return
	}
	m, err := s.GetMessage(c.NoticeMessageID)
	if err != nil {
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if insertRecoveryEvent(tx, conv, "message", m, now()) == nil {
		_ = tx.Commit()
	}
}

func clipNoticeValue(v string) string {
	v = strings.Join(strings.Fields(v), " ")
	if r := []rune(v); len(r) > maxNoticeValueRunes {
		return string(r[:maxNoticeValueRunes-1]) + "…"
	}
	return v
}

// ---- HTTP: the Maintenance list and the person's revert

func (s *Server) routeSettingsChanges(w http.ResponseWriter, r *http.Request, p string) bool {
	rest, ok := strings.CutPrefix(p, "settings-changes")
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return false
	}
	switch {
	case rest == "" && r.Method == http.MethodGet:
		changes, err := s.store.recentSettingsChanges(settingsChangesLimit)
		if err != nil {
			writeErr(w, 500, "storage", "changes unavailable")
			return true
		}
		for i := range changes {
			s.nameSettingsChange(&changes[i])
		}
		writeJSON(w, 200, map[string]any{"changes": changes})
	case strings.HasSuffix(rest, "/revert") && r.Method == http.MethodPost:
		id := strings.Trim(strings.TrimSuffix(rest, "/revert"), "/")
		change, err := s.revertSettingsChangeByPerson(r.Context(), id)
		switch {
		case err == nil:
			s.nameSettingsChange(&change)
			writeJSON(w, 200, map[string]any{"ok": true, "change": change})
		case errors.Is(err, sql.ErrNoRows):
			writeErr(w, 404, "not_found", "change not found")
		case errors.Is(err, errChangeNotRevertable):
			writeErr(w, 409, "settings_change_not_revertable", "this change cannot be undone here")
		case errors.Is(err, errChangeAlreadyReverted):
			writeErr(w, 409, "settings_change_reverted", "this change was already undone")
		case errors.Is(err, errChangeStale):
			writeErr(w, 409, "settings_changed_since", "the setting changed again after this change")
		default:
			writeErr(w, 400, "settings_revert_failed", err.Error())
		}
	default:
		writeErr(w, 405, "method_not_allowed", "unsupported method")
	}
	return true
}

func (s *Server) nameSettingsChange(c *SettingsChange) {
	if d, ok := settingDefFor(c.Key); ok {
		c.LabelKey = d.LabelKey
	}
	if c.BotID != "" {
		if b, err := s.store.GetBot(c.BotID); err == nil {
			c.BotName = b.Name
		}
	}
	switch {
	case c.TargetBotID != "":
		if b, err := s.store.GetBot(c.TargetBotID); err == nil {
			c.TargetName = b.Name
		}
	case c.TargetConversationID != "":
		if conv, err := s.store.GetConversation(c.TargetConversationID); err == nil {
			c.TargetName = conv.Name
		}
	case c.TargetConnection != "":
		c.TargetName = c.TargetConnection
	}
}
