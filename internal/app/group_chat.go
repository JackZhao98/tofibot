package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// The starter and invited members share their immutable user trigger. Descendants retain
// normal handoff parent links; no schema or in-memory round state is needed.
const roundFamilySQL = `WITH RECURSIVE ancestors(id,parent_run_id,kind,trigger_message_id) AS (
 SELECT id,parent_run_id,kind,trigger_message_id FROM runs WHERE id=?
 UNION SELECT r.id,r.parent_run_id,r.kind,r.trigger_message_id FROM runs r JOIN ancestors a ON a.parent_run_id=r.id
), roots(id) AS (
 SELECT r.id FROM runs r JOIN ancestors a ON a.kind='group_chat' AND r.kind='group_chat' AND r.trigger_message_id=a.trigger_message_id
), family(id) AS (
 SELECT id FROM roots UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
)`

// Use the original user boundary, plus only messages from this finite round.
// A later user input may already be persisted while this round is executing.
func (s *Store) groupRoundContext(r Run, conversationID string) ([]Message, int64, bool) {
	var trigger string
	err := s.db.QueryRow(roundFamilySQL+` SELECT trigger_message_id FROM runs WHERE id IN (SELECT id FROM roots) LIMIT 1`, r.ID).Scan(&trigger)
	if err != nil {
		return nil, 0, false
	}
	anchor, err := s.GetMessage(trigger)
	if err != nil || anchor.ConversationID != conversationID {
		return nil, 0, false
	}
	msgs, _, _ := s.Messages(conversationID, anchor.Seq+1, 200)
	rows, err := s.db.Query(roundFamilySQL+` SELECT id FROM messages WHERE conversation_id=? AND seq>? AND run_id IN (SELECT id FROM family) ORDER BY seq DESC LIMIT 200`, r.ID, conversationID, anchor.Seq)
	if err != nil {
		return msgs, anchor.Seq + 1, true
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if m, e := s.GetMessage(id); e == nil {
			msgs = append(msgs, m)
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].Seq < msgs[j].Seq })
	return msgs, anchor.Seq + 1, true
}

func (s *Server) groupChatTools(c Conversation, r Run) []runtime.Tool {
	if c.Kind != "group" || r.Kind != runKindGroupChat {
		return nil
	}
	return []runtime.Tool{{Name: "stay_silent", Description: "End your participation in this group round without posting a message when you have no useful contribution. Call before any public assistant message or delegation; do not announce that you are staying silent.", Parameters: objectSchema(map[string]any{}, nil), Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if _, done, err := s.store.finishRun(r.ID, c.ID, r.BotID, "", true); err != nil {
			return "", err
		} else if !done {
			return "", errors.New("run is no longer active")
		}
		s.mu.Lock()
		cancel := s.runs[r.ID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return "Participation completed without a message.", nil
	}}, {Name: "invite_group_members", Description: "Invite selected active group members to contribute naturally to this conversation after your turn. Choose relevant members from the roster according to the user's request; invite every other member when everyone is asked to contribute. This creates no public assignment or automatic return. Repeated invitations, yourself, and members already asked for a task are no-ops. Use handoff for a concrete delegated task.", Parameters: objectSchema(map[string]any{"bot_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, []string{"bot_ids"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var args struct {
			BotIDs []string `json:"bot_ids"`
		}
		if err := json.Unmarshal(data, &args); err != nil || args.BotIDs == nil {
			return "", errors.New("bot_ids must be an array of group member IDs")
		}
		invited, err := s.store.InviteGroupMembers(r.ID, args.BotIDs)
		if err != nil {
			return "", err
		}
		if len(invited) > 0 {
			s.startConversationWorker(c.ID)
		}
		ids := make([]string, 0, len(invited))
		for _, run := range invited {
			ids = append(ids, run.BotID)
		}
		result, _ := json.Marshal(map[string]any{"invited_bot_ids": ids})
		return string(result), nil
	}}}
}

// InviteGroupMembers atomically validates the entire request and reserves each
// member's single conversational opportunity. Parent links provide cancellation
// and the shared trigger provides bounded context and restart-safe deduplication.
func (s *Store) InviteGroupMembers(parentID string, botIDs []string) ([]Run, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	parent, err := scanRun(tx.QueryRow(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, parentID))
	if err != nil {
		return nil, err
	}
	if parent.Status != "running" || parent.Kind != runKindGroupChat {
		return nil, errors.New("invitations require an active conversational group turn")
	}
	var kind string
	if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, parent.ConversationID).Scan(&kind); err != nil {
		return nil, err
	}
	if kind != "group" {
		return nil, errors.New("invitations require a group")
	}
	if err = requireCurrentMemberTx(tx, parent.ConversationID, parent.BotID); err != nil {
		return nil, err
	}
	var anchorRole, anchorConversation string
	if err = tx.QueryRow(`SELECT role,conversation_id FROM messages WHERE id=?`, parent.TriggerMessageID).Scan(&anchorRole, &anchorConversation); err != nil {
		return nil, err
	}
	if anchorRole != "user" || anchorConversation != parent.ConversationID {
		return nil, errors.New("invitations require a user-triggered group round")
	}
	// Validate even duplicates/self before inserting anything: no partial success.
	selected := make([]runSpec, 0, len(botIDs))
	seen := map[string]bool{}
	for _, id := range botIDs {
		if err = requireCurrentMemberTx(tx, parent.ConversationID, id); err != nil {
			return nil, fmt.Errorf("invalid invited member %q: %w", id, err)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		var model string
		if err = tx.QueryRow(`SELECT model FROM bots WHERE id=?`, id).Scan(&model); err != nil {
			return nil, err
		}
		selected = append(selected, runSpec{BotID: id, Model: model})
	}
	var next int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0) FROM runs WHERE conversation_id=?`, parent.ConversationID).Scan(&next); err != nil {
		return nil, err
	}
	invited := make([]Run, 0, len(selected))
	t := now()
	for _, spec := range selected {
		var represented int
		if err = tx.QueryRow(roundFamilySQL+` SELECT COUNT(*) FROM runs WHERE id IN (SELECT id FROM family) AND bot_id=?`, parent.ID, spec.BotID).Scan(&represented); err != nil {
			return nil, err
		}
		if represented > 0 {
			continue
		}
		next++
		run := Run{ID: newID(), ConversationID: parent.ConversationID, BotID: spec.BotID, Status: "queued", ParentRunID: parent.ID, Model: spec.Model, Kind: runKindGroupChat, OriginConversationID: parent.ConversationID, TriggerMessageID: parent.TriggerMessageID, QueueSeq: next, CreatedAt: t, UpdatedAt: t}
		if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.ConversationID, run.BotID, run.Status, run.ParentRunID, 0, run.Model, run.Kind, run.OriginConversationID, run.TriggerMessageID, run.QueueSeq, t, t); err != nil {
			return nil, err
		}
		data, _ := json.Marshal(run)
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, run.ConversationID, "run", string(data), t); err != nil {
			return nil, err
		}
		invited = append(invited, run)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return invited, nil
}
