package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

var (
	ErrGroupNotFound        = errors.New("group not found")
	ErrConversationNotGroup = errors.New("conversation is not a group")
	ErrGroupMemberConflict  = errors.New("group membership changed; refresh and retry")
	ErrGroupNameConflict    = errors.New("group name changed; refresh and retry")
	ErrGroupBusy            = errors.New("group has queued or running work; wait for it to finish before changing members")
	ErrGroupInvalid         = errors.New("invalid group maintenance request")
)

const maxMaintainedGroupMembers = 8

type GroupUpdate struct {
	Name           *string
	BotIDs         *[]string
	ExpectedName   *string
	ExpectedBotIDs *[]string
}

// UpdateGroup applies a group profile change in one SQLite transaction. The
// expected member list is checked in the same transaction as the mutation so
// concurrent send/handoff and maintenance requests have a single commit order.
// Messages, runs, and sender identities are never rewritten or deleted.
func (s *Store) UpdateGroup(id string, patch GroupUpdate) (Conversation, error) {
	if patch.Name == nil && patch.BotIDs == nil {
		return Conversation{}, fmt.Errorf("%w: at least one group field is required", ErrGroupInvalid)
	}
	if patch.Name != nil {
		trimmed := strings.TrimSpace(*patch.Name)
		if trimmed == "" {
			return Conversation{}, fmt.Errorf("%w: name is required", ErrGroupInvalid)
		}
		if len([]rune(trimmed)) > maxTeamNameRunes {
			return Conversation{}, fmt.Errorf("%w: name is too long", ErrGroupInvalid)
		}
		*patch.Name = trimmed
	}
	var requested, expected []string
	var err error
	if patch.BotIDs != nil {
		requested, err = normalizeMaintainedMembers(*patch.BotIDs)
		if err != nil {
			return Conversation{}, err
		}
	}
	if patch.ExpectedBotIDs != nil {
		expected, err = normalizeMaintainedMembers(*patch.ExpectedBotIDs)
		if err != nil {
			return Conversation{}, fmt.Errorf("expected_bot_ids: %w", err)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()

	var c Conversation
	var bot sql.NullString
	var archived int
	if err = tx.QueryRow(`SELECT id,kind,name,bot_id,updated_at,archived FROM conversations WHERE id=?`, id).
		Scan(&c.ID, &c.Kind, &c.Name, &bot, &c.UpdatedAt, &archived); err == sql.ErrNoRows {
		return Conversation{}, ErrGroupNotFound
	} else if err != nil {
		return Conversation{}, err
	}
	c.BotID = bot.String
	c.Archived = archived != 0
	if c.Kind != "group" {
		return Conversation{}, ErrConversationNotGroup
	}
	current, err := maintainedGroupMembers(tx, id)
	if err != nil {
		return Conversation{}, err
	}
	if patch.ExpectedBotIDs != nil && !sameStringSet(current, expected) {
		return Conversation{}, ErrGroupMemberConflict
	}
	if patch.ExpectedName != nil && *patch.ExpectedName != c.Name {
		return Conversation{}, ErrGroupNameConflict
	}
	membersChanged := patch.BotIDs != nil && !sameStringSet(current, requested)
	if membersChanged {
		currentSet := make(map[string]struct{}, len(current))
		for _, member := range current {
			currentSet[member] = struct{}{}
		}
		for _, member := range requested {
			var found string
			var archived int
			if err = tx.QueryRow(`SELECT id,archived FROM bots WHERE id=?`, member).Scan(&found, &archived); err == sql.ErrNoRows {
				return Conversation{}, fmt.Errorf("%w: bot %q not found", ErrGroupInvalid, member)
			} else if err != nil {
				return Conversation{}, err
			}
			if archived != 0 {
				if _, retained := currentSet[member]; !retained {
					return Conversation{}, fmt.Errorf("%w: bot %q is archived and unavailable for new membership", ErrGroupInvalid, member)
				}
			}
		}
		var active int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND status IN ('queued','running','waiting')`, id).Scan(&active); err != nil {
			return Conversation{}, err
		}
		if active != 0 {
			return Conversation{}, ErrGroupBusy
		}
	}
	nameChanged := patch.Name != nil && *patch.Name != c.Name
	if !membersChanged && !nameChanged {
		c.BotIDs = current
		return c, nil
	}

	t := now()
	if nameChanged {
		c.Name = *patch.Name
	}
	if membersChanged {
		if _, err = tx.Exec(`DELETE FROM members WHERE conversation_id=?`, id); err != nil {
			return Conversation{}, err
		}
		for _, member := range requested {
			if _, err = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, id, member); err != nil {
				return Conversation{}, err
			}
		}
		c.BotIDs = requested
	} else {
		c.BotIDs = current
	}
	c.UpdatedAt = t
	if _, err = tx.Exec(`UPDATE conversations SET name=?,updated_at=? WHERE id=?`, c.Name, t, id); err != nil {
		return Conversation{}, err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return Conversation{}, err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, id, "conversation", string(b), t); err != nil {
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

func normalizeMaintainedMembers(ids []string) ([]string, error) {
	if len(ids) < 2 || len(ids) > maxMaintainedGroupMembers {
		return nil, fmt.Errorf("%w: group must have 2-8 distinct members", ErrGroupInvalid)
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("%w: group member id is required", ErrGroupInvalid)
		}
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: group members must be distinct", ErrGroupInvalid)
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func maintainedGroupMembers(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query(`SELECT bot_id FROM members WHERE conversation_id=? ORDER BY bot_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var member string
		if err = rows.Scan(&member); err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// requireCurrentMemberTx closes the read-members/build-run race in the HTTP
// sender and handoff paths. Their request handlers may have read an older
// Conversation snapshot; the final insert transaction must authorize each
// target against the membership committed immediately before it.
func requireCurrentMemberTx(tx *sql.Tx, conversationID, botID string) error {
	var kind string
	if err := tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, conversationID).Scan(&kind); err != nil {
		return err
	}
	if err := requireConversationActiveTx(tx, conversationID); err != nil {
		return err
	}
	if err := requireActiveBotTx(tx, botID); err != nil {
		return err
	}
	if kind == "group" {
		var active int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM members m JOIN bots b ON b.id=m.bot_id WHERE m.conversation_id=? AND b.archived=0`, conversationID).Scan(&active); err != nil {
			return err
		}
		if active == 0 {
			return ErrNoActiveMembers
		}
	}
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM members WHERE conversation_id=? AND bot_id=?`, conversationID, botID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if kind == "group" {
			return ErrGroupMemberConflict
		}
		return fmt.Errorf("bot %q is not a conversation member", botID)
	}
	return nil
}

func (s *Server) patchConversation(w http.ResponseWriter, r *http.Request, id string) {
	var in struct {
		Name           *string   `json:"name"`
		BotIDs         *[]string `json:"bot_ids"`
		ExpectedName   *string   `json:"expected_name"`
		ExpectedBotIDs *[]string `json:"expected_bot_ids"`
		Archived       *bool     `json:"archived"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	if in.Archived != nil {
		if in.Name != nil || in.BotIDs != nil || in.ExpectedName != nil || in.ExpectedBotIDs != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "archived must be updated separately")
			return
		}
		c, err := s.store.SetGroupArchived(id, *in.Archived)
		if err != nil {
			s.writeArchiveError(w, err, false)
			return
		}
		writeJSON(w, http.StatusOK, c)
		return
	}
	c, err := s.store.UpdateGroup(id, GroupUpdate{Name: in.Name, BotIDs: in.BotIDs, ExpectedName: in.ExpectedName, ExpectedBotIDs: in.ExpectedBotIDs})
	if err != nil {
		switch {
		case errors.Is(err, ErrGroupNotFound):
			writeErr(w, http.StatusNotFound, "not_found", "group not found")
		case errors.Is(err, ErrConversationNotGroup):
			writeErr(w, http.StatusBadRequest, "group_only", "conversation is not a group")
		case errors.Is(err, ErrGroupMemberConflict):
			writeErr(w, http.StatusConflict, "membership_conflict", err.Error())
		case errors.Is(err, ErrGroupNameConflict):
			writeErr(w, http.StatusConflict, "name_conflict", err.Error())
		case errors.Is(err, ErrGroupBusy):
			writeErr(w, http.StatusConflict, "group_busy", err.Error())
		case errors.Is(err, ErrGroupInvalid):
			writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, c)
}
