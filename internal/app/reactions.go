package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	reactions, _, err := s.setMessageReaction(ctx, conversationID, messageID, actorKey, emoji, present, run)
	return reactions, err
}

// setMessageReaction also reports whether the reaction row changed, so a
// repeated click does not count as new input.
func (s *Store) setMessageReaction(ctx context.Context, conversationID, messageID, actorKey, emoji string, present bool, run *Run) ([]Reaction, bool, error) {
	if !validReactionEmoji(emoji) {
		return nil, false, errReactionInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var kind, ownerBot string
	var visible int
	if err = tx.QueryRowContext(ctx, `SELECT kind,COALESCE(bot_id,''),user_visible FROM conversations WHERE id=?`, conversationID).Scan(&kind, &ownerBot, &visible); err != nil {
		return nil, false, errReactionNotFound
	}
	if actorKey == "user" {
		if visible == 0 {
			return nil, false, errReactionForbidden
		}
	} else {
		if run == nil || actorKey != "bot:"+run.BotID || run.ConversationID != conversationID {
			return nil, false, errReactionForbidden
		}
		var status string
		if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, run.ID, conversationID, run.BotID).Scan(&status); err != nil || status != "running" {
			return nil, false, errReactionStopped
		}
		var steered int
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM run_steering WHERE old_run_id=?)`, run.ID).Scan(&steered); err != nil || steered != 0 {
			return nil, false, errReactionStopped
		}
		if kind == "dm" {
			if ownerBot != run.BotID {
				return nil, false, errReactionForbidden
			}
		} else {
			var member int
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM members WHERE conversation_id=? AND bot_id=?)`, conversationID, run.BotID).Scan(&member); err != nil || member == 0 {
				return nil, false, errReactionForbidden
			}
		}
	}
	var messageKind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM messages WHERE id=? AND conversation_id=?`, messageID, conversationID).Scan(&messageKind); err != nil || messageKind == "progress" || messageKind == messageKindInternal || messageKind == "bot_result" {
		return nil, false, errReactionNotFound
	}
	var result sql.Result
	if present {
		result, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO message_reactions(message_id,actor_key,emoji,created_at) VALUES(?,?,?,?)`, messageID, actorKey, emoji, now())
	} else {
		result, err = tx.ExecContext(ctx, `DELETE FROM message_reactions WHERE message_id=? AND actor_key=? AND emoji=?`, messageID, actorKey, emoji)
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT emoji,actor_key,created_at FROM message_reactions WHERE message_id=? ORDER BY created_at,actor_key,emoji`, messageID)
	if err != nil {
		return nil, false, err
	}
	reactions, err := reactionRows(rows)
	if err != nil {
		return nil, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	if changed > 0 {
		data, err := json.Marshal(map[string]any{"conversation_id": conversationID, "message_id": messageID, "reactions": reactions})
		if err != nil {
			return nil, false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conversationID, "reaction", string(data), now()); err != nil {
			return nil, false, err
		}
	}
	return reactions, changed > 0, tx.Commit()
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
	reactions, changed, err := s.store.setMessageReaction(r.Context(), c.ID, messageID, "user", in.Emoji, *in.Present, nil)
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
	if changed && *in.Present {
		s.noteReactionWake(c, messageID, "user")
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": c.ID, "message_id": messageID, "reactions": reactions})
}

// maxReactionSummaryActors bounds one message's summary in the history window.
const maxReactionSummaryActors = 8

// reactionSummaries renders, for each message of a history window that has
// reactions, one compact line: the emoji and who placed it. The line travels
// with the message in the history the model reads; it is charged to the same
// history budget and never reaches the system prompt. A failed read yields no
// summaries rather than an error: the history is complete without them.
func (s *Store) reactionSummaries(messages []Message) map[string]string {
	if len(messages) == 0 {
		return nil
	}
	args := make([]any, 0, len(messages))
	for _, m := range messages {
		args = append(args, m.ID)
	}
	rows, err := s.db.Query(`SELECT message_id,emoji,actor_key FROM message_reactions WHERE message_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`) ORDER BY created_at,actor_key,emoji`, args...)
	if err != nil {
		return nil
	}
	type row struct{ id, emoji, actor string }
	var all []row
	for rows.Next() {
		var x row
		if rows.Scan(&x.id, &x.emoji, &x.actor) != nil {
			rows.Close()
			return nil
		}
		all = append(all, x)
	}
	failed := rows.Err() != nil
	rows.Close() // the store has one connection: read names only after this
	if failed {
		return nil
	}
	type entry struct {
		emoji  string
		actors []string
	}
	per := map[string][]*entry{}
	names := map[string]string{"user": "person"}
	total := map[string]int{}
	for _, x := range all {
		name, ok := names[x.actor]
		if !ok {
			name = strings.TrimPrefix(x.actor, "bot:")
			if b, e := s.GetBot(name); e == nil {
				name = b.Name
			}
			names[x.actor] = name
		}
		total[x.id]++
		if total[x.id] > maxReactionSummaryActors {
			continue
		}
		var found *entry
		for _, e := range per[x.id] {
			if e.emoji == x.emoji {
				found = e
			}
		}
		if found == nil {
			found = &entry{emoji: x.emoji}
			per[x.id] = append(per[x.id], found)
		}
		found.actors = append(found.actors, name)
	}
	out := make(map[string]string, len(per))
	for id, entries := range per {
		parts := make([]string, 0, len(entries))
		for _, e := range entries {
			parts = append(parts, e.emoji+" "+strings.Join(e.actors, ", "))
		}
		line := "[reactions: " + strings.Join(parts, "; ")
		if extra := total[id] - maxReactionSummaryActors; extra > 0 {
			line += fmt.Sprintf("; +%d more", extra)
		}
		out[id] = line + "]"
	}
	return out
}
