package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Reaction struct {
	Emoji     string `json:"emoji"`
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

var (
	errReactionInvalid   = errors.New("choose one emoji reaction")
	errReactionNotFound  = errors.New("message not found in this conversation")
	errReactionForbidden = errors.New("actor cannot react in this conversation")
	errReactionStopped   = errors.New("run can no longer react")
)

// Accept one ordinary emoji (including modifiers, flags and joined families),
// while rejecting words, whitespace and multi-emoji strings as a reaction.
func validReactionEmoji(value string) bool {
	if !utf8.ValidString(value) || len(value) > 64 || utf8.RuneCountInString(value) > 12 {
		return false
	}
	bases, joiners, regional, keycaps := 0, 0, 0, 0
	for _, r := range value {
		switch {
		case r == 0x200d:
			joiners++
		case r == 0xfe0f || r == 0xfe0e || r >= 0x1f3fb && r <= 0x1f3ff:
		case r == 0x20e3:
			keycaps++
		case r >= 0x1f1e6 && r <= 0x1f1ff:
			bases++
			regional++
		case r >= '0' && r <= '9' || r == '#' || r == '*':
			bases++
		case unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r):
			bases++
		default:
			return false
		}
	}
	if bases == 0 || joiners > 0 && joiners != bases-1 {
		return false
	}
	if regional > 0 {
		return regional == 2 && bases == 2 && joiners == 0 && keycaps == 0
	}
	if keycaps > 0 {
		return keycaps == 1 && bases == 1 && joiners == 0
	}
	for _, r := range value {
		if r >= '0' && r <= '9' || r == '#' || r == '*' {
			return false
		}
	}
	return bases == 1 || joiners == bases-1
}

func reactionRows(rows *sql.Rows) ([]Reaction, error) {
	defer rows.Close()
	out := []Reaction{}
	for rows.Next() {
		var actor string
		var reaction Reaction
		if err := rows.Scan(&reaction.Emoji, &actor, &reaction.CreatedAt); err != nil {
			return nil, err
		}
		if actor == "user" {
			reaction.ActorType = "user"
		} else {
			reaction.ActorType = "bot"
			reaction.ActorID = strings.TrimPrefix(actor, "bot:")
		}
		out = append(out, reaction)
	}
	return out, rows.Err()
}

func (s *Store) hydrateMessageReactions(messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	args := make([]any, len(messages))
	byID := make(map[string]int, len(messages))
	for i, message := range messages {
		args[i] = message.ID
		byID[message.ID] = i
	}
	query := `SELECT message_id,emoji,actor_key,created_at FROM message_reactions WHERE message_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(messages)), ",") + `) ORDER BY created_at,actor_key,emoji`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var messageID, actor string
		var reaction Reaction
		if err := rows.Scan(&messageID, &reaction.Emoji, &actor, &reaction.CreatedAt); err != nil {
			return err
		}
		if actor == "user" {
			reaction.ActorType = "user"
		} else {
			reaction.ActorType = "bot"
			reaction.ActorID = strings.TrimPrefix(actor, "bot:")
		}
		if index, ok := byID[messageID]; ok {
			messages[index].Reactions = append(messages[index].Reactions, reaction)
		}
	}
	return rows.Err()
}

func (s *Store) SetMessageReaction(ctx context.Context, conversationID, messageID, actorKey, emoji string, present bool, run *Run) ([]Reaction, error) {
	if !validReactionEmoji(emoji) {
		return nil, errReactionInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var kind, ownerBot string
	var visible int
	if err = tx.QueryRowContext(ctx, `SELECT kind,COALESCE(bot_id,''),user_visible FROM conversations WHERE id=?`, conversationID).Scan(&kind, &ownerBot, &visible); err != nil {
		return nil, errReactionNotFound
	}
	if actorKey == "user" {
		if visible == 0 {
			return nil, errReactionForbidden
		}
	} else {
		if run == nil || actorKey != "bot:"+run.BotID || run.ConversationID != conversationID {
			return nil, errReactionForbidden
		}
		var status string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, run.ID, conversationID, run.BotID).Scan(&status); err != nil || status != "running" {
			return nil, errReactionStopped
		}
		var steered int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_steering WHERE old_run_id=?)`, run.ID).Scan(&steered); err != nil || steered != 0 {
			return nil, errReactionStopped
		}
		if kind == "dm" {
			if ownerBot != run.BotID {
				return nil, errReactionForbidden
			}
		} else {
			var member int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE conversation_id=? AND bot_id=?)`, conversationID, run.BotID).Scan(&member); err != nil || member == 0 {
				return nil, errReactionForbidden
			}
		}
	}
	var messageKind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM messages WHERE id=? AND conversation_id=?`, messageID, conversationID).Scan(&messageKind); err != nil || messageKind == "progress" || messageKind == "bot_result" {
		return nil, errReactionNotFound
	}
	var result sql.Result
	if present {
		result, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_reactions(message_id,actor_key,emoji,created_at) VALUES(?,?,?,?)`, messageID, actorKey, emoji, now())
	} else {
		result, err = tx.ExecContext(ctx, `DELETE FROM message_reactions WHERE message_id=? AND actor_key=? AND emoji=?`, messageID, actorKey, emoji)
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT emoji,actor_key,created_at FROM message_reactions WHERE message_id=? ORDER BY created_at,actor_key,emoji`, messageID)
	if err != nil {
		return nil, err
	}
	reactions, err := reactionRows(rows)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed > 0 {
		data, err := json.Marshal(map[string]any{"conversation_id": conversationID, "message_id": messageID, "reactions": reactions})
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conversationID, "reaction", string(data), now()); err != nil {
			return nil, err
		}
	}
	return reactions, tx.Commit()
}

func (s *Server) putMessageReaction(w http.ResponseWriter, r *http.Request, c Conversation, messageID string) {
	var in struct {
		Emoji   string `json:"emoji"`
		Present *bool  `json:"present"`
	}
	if decode(r, &in) != nil || in.Present == nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "emoji and present are required")
		return
	}
	reactions, err := s.store.SetMessageReaction(r.Context(), c.ID, messageID, "user", in.Emoji, *in.Present, nil)
	if err != nil {
		switch {
		case errors.Is(err, errReactionInvalid):
			writeErr(w, http.StatusBadRequest, "invalid_emoji", err.Error())
		case errors.Is(err, errReactionNotFound):
			writeErr(w, http.StatusNotFound, "not_found", err.Error())
		case errors.Is(err, errReactionForbidden):
			writeErr(w, http.StatusForbidden, "forbidden", err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "storage", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": c.ID, "message_id": messageID, "reactions": reactions})
}
