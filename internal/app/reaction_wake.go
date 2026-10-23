package app

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// A reaction a person places on a Bot's group message is a signal to that Bot:
// it wakes the Bot at low priority, with no reply required. The Bot may end
// its turn silently or answer. A reaction placed by a Bot never wakes anyone,
// which keeps two Bots from answering each other's acknowledgements forever. A
// reaction never changes task state; it is input to the Bot's judgement only.
//
// Bursts coalesce: the first person reaction on a Bot's messages opens a short
// window, and one run starts when it closes, however many reactions arrived.
// The window is in memory. A restart inside it drops that wake; the reactions
// stay in the history the Bot reads on its next run.

const (
	// reactionWakeSource marks the run_wakes row of a reaction wake.
	reactionWakeSource = "reaction"
	// defaultReactionWakeWindow is how long a burst of reactions is gathered.
	defaultReactionWakeWindow = 10 * time.Second
	maxReactionWakeMessages   = 5
)

type reactionWakeState struct {
	mu      sync.Mutex
	pending map[string]map[string]bool // conversation|bot -> reacted message ids
}

// noteReactionWake records that actorKey placed a reaction on messageID. Only a
// person's reaction counts, and only on a message that a current, active member
// Bot wrote in a group; anything else does nothing.
func (s *Server) noteReactionWake(c Conversation, messageID, actorKey string) {
	if actorKey != "user" || c.Kind != "group" || !c.UserVisible {
		return
	}
	var sender sql.NullString
	var role, kind string
	if s.store.db.QueryRow(`SELECT sender_bot_id,role,kind FROM messages WHERE id=? AND conversation_id=?`, messageID, c.ID).Scan(&sender, &role, &kind) != nil {
		return
	}
	if !sender.Valid || sender.String == "" || role != "assistant" || kind == "notice" || kind == "progress" || kind == messageKindInternal || kind == messageKindBotResult {
		return
	}
	if !s.memberAcceptsWake(c.ID, sender.String) {
		return
	}
	key := c.ID + "|" + sender.String
	st := &s.reactionWakes
	st.mu.Lock()
	if st.pending == nil {
		st.pending = map[string]map[string]bool{}
	}
	open := st.pending[key]
	if open == nil {
		open = map[string]bool{}
		st.pending[key] = open
		window := s.reactionWakeWindow
		if window <= 0 {
			window = defaultReactionWakeWindow
		}
		time.AfterFunc(window, func() { s.flushReactionWake(c.ID, sender.String) })
	}
	open[messageID] = true
	st.mu.Unlock()
}

func (s *Server) memberAcceptsWake(conv, bot string) bool {
	var ok int
	err := s.store.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM members m JOIN bots b ON b.id=m.bot_id JOIN conversations c ON c.id=m.conversation_id WHERE m.conversation_id=? AND m.bot_id=? AND b.archived=0 AND c.archived=0)`, conv, bot).Scan(&ok)
	return err == nil && ok == 1
}

// flushReactionWake closes the window for one Bot and starts at most one run.
func (s *Server) flushReactionWake(conv, bot string) {
	st := &s.reactionWakes
	key := conv + "|" + bot
	st.mu.Lock()
	open := st.pending[key]
	delete(st.pending, key)
	st.mu.Unlock()
	if len(open) == 0 {
		return
	}
	ids := make([]string, 0, len(open))
	for id := range open {
		ids = append(ids, id)
	}
	run, ok, err := s.store.AddReactionWake(conv, bot, ids)
	if err != nil || !ok {
		return
	}
	c, err := s.store.GetConversation(conv)
	if err != nil {
		return
	}
	s.enqueue(c, run)
}

// AddReactionWake queues one low-priority run for bot because people reacted to
// its messages ids. It reports false when nothing is due: the Bot is no longer
// an active member, the reactions were taken back, or the Bot already has
// queued or waiting work (it reads the reactions in the transcript when that
// work starts). A Bot that is running keeps running; the wake queues behind it
// and a newer message to the Bot supersedes it like any other queued run.
func (s *Store) AddReactionWake(conv, bot string, messageIDs []string) (Run, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	if requireCurrentMemberTx(tx, conv, bot) != nil {
		return Run{}, false, nil
	}
	sort.Strings(messageIDs)
	if len(messageIDs) > maxReactionWakeMessages {
		messageIDs = messageIDs[len(messageIDs)-maxReactionWakeMessages:]
	}
	var lines []string
	trigger := ""
	for _, id := range messageIDs {
		var content string
		if tx.QueryRow(`SELECT content FROM messages WHERE id=? AND conversation_id=? AND sender_bot_id=?`, id, conv, bot).Scan(&content) != nil {
			continue
		}
		rows, qerr := tx.Query(`SELECT emoji FROM message_reactions WHERE message_id=? AND actor_key='user' ORDER BY created_at,emoji`, id)
		if qerr != nil {
			return Run{}, false, qerr
		}
		var emojis []string
		for rows.Next() {
			var e string
			if rows.Scan(&e) == nil {
				emojis = append(emojis, e)
			}
		}
		rows.Close()
		if len(emojis) == 0 {
			continue
		}
		trigger = id
		lines = append(lines, fmt.Sprintf("message_id=%s %s -> %q", id, strings.Join(emojis, " "), trimRunes(strings.Join(strings.Fields(content), " "), 160)))
	}
	if len(lines) == 0 {
		return Run{}, false, nil
	}
	var busy int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND bot_id=? AND status IN ('queued','waiting')`, conv, bot).Scan(&busy); err != nil {
		return Run{}, false, err
	}
	if busy > 0 {
		return Run{}, false, nil
	}
	var model string
	if err = tx.QueryRow(`SELECT model FROM bots WHERE id=?`, bot).Scan(&model); err != nil {
		return Run{}, false, err
	}
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, conv).Scan(&queueSeq); err != nil {
		return Run{}, false, err
	}
	t := now()
	run := Run{ID: newID(), ConversationID: conv, BotID: bot, Status: "queued", Model: model, Kind: runKindGroupMention, OriginConversationID: conv, TriggerMessageID: trigger, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, conv, bot, run.Status, nil, 0, run.Model, run.Kind, run.OriginConversationID, run.TriggerMessageID, run.QueueSeq, t, t); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO run_wakes(run_id,request,source,digest) VALUES(?,?,?,?)`, run.ID, strings.Join(lines, "\n"), reactionWakeSource, ""); err != nil {
		return Run{}, false, err
	}
	if err = insertEventTx(tx, conv, "run", run, t); err != nil {
		return Run{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

// reactionWakeRequest returns the reaction lines of a reaction wake run.
func (s *Store) reactionWakeRequest(runID string) (string, bool) {
	var request string
	if s.db.QueryRow(`SELECT request FROM run_wakes WHERE run_id=? AND source=?`, runID, reactionWakeSource).Scan(&request) != nil {
		return "", false
	}
	return request, true
}
