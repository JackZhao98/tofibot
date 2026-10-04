package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

var (
	ErrArchiveNotFound = errors.New("archive target not found")
	ErrArchiveType     = errors.New("only groups can be archived as conversations")
	ErrArchiveBusy     = errors.New("archive target has queued or running work")
	ErrArchiveBlocked  = errors.New("archived conversation or Bot cannot accept new work")
	ErrNoActiveMembers = errors.New("group has no active members")
)

// SetGroupArchived changes only the group's lifecycle bit. Members, messages,
// runs, files, and sender identities remain untouched. Active schedules are
// paused in this same transaction and are intentionally not resumed later.
func (s *Store) SetGroupArchived(id string, archived bool) (Conversation, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()

	var c Conversation
	var bot sql.NullString
	var oldArchived int
	if err = tx.QueryRow(`SELECT id,kind,name,bot_id,updated_at,archived FROM conversations WHERE id=?`, id).
		Scan(&c.ID, &c.Kind, &c.Name, &bot, &c.UpdatedAt, &oldArchived); err == sql.ErrNoRows {
		return Conversation{}, ErrArchiveNotFound
	} else if err != nil {
		return Conversation{}, err
	}
	c.BotID = bot.String
	c.Archived = oldArchived != 0
	if c.Kind != "group" {
		return Conversation{}, ErrArchiveType
	}
	if c.Archived == archived {
		tx.Rollback()
		return s.GetConversation(id)
	}
	var active int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND status IN ('queued','running','waiting')`, id).Scan(&active); err != nil {
		return Conversation{}, err
	}
	if active != 0 {
		return Conversation{}, ErrArchiveBusy
	}
	if archived {
		if err = pauseSchedulesTx(tx, "conversation_id=?", id); err != nil {
			return Conversation{}, err
		}
	}
	t := now()
	if _, err = tx.Exec(`UPDATE conversations SET archived=?,updated_at=? WHERE id=?`, boolInt(archived), t, id); err != nil {
		return Conversation{}, err
	}
	c.Archived, c.UpdatedAt = archived, t
	c.BotIDs, err = maintainedGroupMembers(tx, id)
	if err != nil {
		return Conversation{}, err
	}
	if err = archiveConversationEventTx(tx, c, t); err != nil {
		return Conversation{}, err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeGroups, t); err != nil {
		return Conversation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Conversation{}, err
	}
	return c, nil
}

// SetBotArchived also archives the Bot's canonical DM so the default
// workspace list cannot retain an orphaned private conversation. The DM ID
// remains stable and restoration reverses that paired state change.
func (s *Store) SetBotArchived(id string, archived bool) (Bot, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Bot{}, err
	}
	defer tx.Rollback()

	var b Bot
	var oldArchived int
	if err = tx.QueryRow(`SELECT id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived FROM bots WHERE id=?`, id).
		Scan(&b.ID, &b.Name, &b.Instructions, &b.Model, &b.ReasoningEffort, &b.DMConversationID, &b.CreatedAt, &oldArchived); err == sql.ErrNoRows {
		return Bot{}, ErrArchiveNotFound
	} else if err != nil {
		return Bot{}, err
	}
	b.Archived = oldArchived != 0
	if b.Archived == archived {
		tx.Rollback()
		return s.GetBot(id)
	}
	var active int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE (bot_id=? OR conversation_id=?) AND status IN ('queued','running','waiting')`, id, b.DMConversationID).Scan(&active); err != nil {
		return Bot{}, err
	}
	if active != 0 {
		return Bot{}, ErrArchiveBusy
	}
	if archived {
		if err = pauseSchedulesTx(tx, "bot_id=? OR conversation_id=?", id, b.DMConversationID); err != nil {
			return Bot{}, err
		}
	}
	t := now()
	if _, err = tx.Exec(`UPDATE bots SET archived=? WHERE id=?`, boolInt(archived), id); err != nil {
		return Bot{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET archived=?,updated_at=? WHERE id=? AND kind='dm' AND bot_id=?`, boolInt(archived), t, b.DMConversationID, id); err != nil {
		return Bot{}, err
	}
	b.Archived = archived
	if err = archiveBotEventTx(tx, b, t); err != nil {
		return Bot{}, err
	}
	dm := Conversation{ID: b.DMConversationID, Kind: "dm", Name: b.Name, BotID: b.ID, BotIDs: []string{b.ID}, UpdatedAt: t, Archived: archived}
	if err = archiveConversationEventTx(tx, dm, t); err != nil {
		return Bot{}, err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeBots, t); err != nil {
		return Bot{}, err
	}
	if err = tx.Commit(); err != nil {
		return Bot{}, err
	}
	return b, nil
}

func archiveConversationEventTx(tx *sql.Tx, c Conversation, at string) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, c.ID, "conversation", string(b), at)
	return err
}

func archiveBotEventTx(tx *sql.Tx, b Bot, at string) error {
	bts, err := json.Marshal(b)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, b.DMConversationID, "bot", string(bts), at)
	return err
}

func pauseSchedulesTx(tx *sql.Tx, where string, args ...any) error {
	rows, err := tx.Query(`SELECT id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at FROM schedules WHERE status='active' AND `+where, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var schedules []Schedule
	for rows.Next() {
		x, scanErr := scanSchedule(rows)
		if scanErr != nil {
			return scanErr
		}
		schedules = append(schedules, x)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for _, x := range schedules {
		updated := now()
		if _, err = tx.Exec(`UPDATE schedules SET status='paused',updated_at=? WHERE id=? AND status='active'`, updated, x.ID); err != nil {
			return err
		}
		x.Status = schedulePaused
		x.UpdatedAt = updated
		if err = appendScheduleAuthorizationTx(tx, x, "archive", updated, nil); err != nil {
			return err
		}
		if err = insertScheduleEvent(tx, x.ConversationID, "schedule", x, x.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) archiveBotHTTP(w http.ResponseWriter, id string, archived bool) {
	b, err := s.store.SetBotArchived(id, archived)
	if err != nil {
		s.writeArchiveError(w, err, true)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) writeArchiveError(w http.ResponseWriter, err error, bot bool) {
	switch {
	case errors.Is(err, ErrArchiveNotFound):
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrArchiveType):
		writeErr(w, http.StatusBadRequest, "group_only", err.Error())
	case errors.Is(err, ErrArchiveBusy):
		writeErr(w, http.StatusConflict, "archive_busy", "queued or running work exists; wait for it to finish or cancel it before archiving")
	case errors.Is(err, ErrArchiveBlocked):
		writeErr(w, http.StatusConflict, "archive_blocked", err.Error())
	case errors.Is(err, ErrNoActiveMembers):
		writeErr(w, http.StatusConflict, "no_active_members", err.Error())
	default:
		_ = bot
		writeErr(w, http.StatusInternalServerError, "storage", err.Error())
	}
}

func parseIncludeArchived(r *http.Request) bool {
	v := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("include_archived")))
	return v == "1" || v == "true" || v == "yes"
}

func requireConversationActiveTx(tx *sql.Tx, conv string) error {
	var archived int
	if err := tx.QueryRow(`SELECT archived FROM conversations WHERE id=?`, conv).Scan(&archived); err != nil {
		return err
	}
	if archived != 0 {
		return ErrArchiveBlocked
	}
	return nil
}

func requireActiveBotTx(tx *sql.Tx, id string) error {
	var archived int
	if err := tx.QueryRow(`SELECT archived FROM bots WHERE id=?`, id).Scan(&archived); err != nil {
		return err
	}
	if archived != 0 {
		return ErrArchiveBlocked
	}
	return nil
}

func requireActiveMemberTx(tx *sql.Tx, conversationID, botID string) error {
	if err := requireActiveBotTx(tx, botID); err != nil {
		return err
	}
	return requireCurrentMemberTx(tx, conversationID, botID)
}

func activeMemberCountTx(tx *sql.Tx, conversationID string) (int, error) {
	var n int
	err := tx.QueryRow(`SELECT COUNT(*) FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=? AND b.archived=0`, conversationID).Scan(&n)
	return n, err
}
