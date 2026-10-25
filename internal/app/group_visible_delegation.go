package app

import (
	"database/sql"
	"errors"
	"log"
	"strings"

	"github.com/google/uuid"
)

// Decision 0065: a delegation to members of the same group conversation is the
// assigning member's own message in that group, "@name brief". The task record
// keeps the brief without the mentions.

// delegationClientPrefix marks the message in client_message_id, so that the
// run bookkeeping that asks "did this run already say its part" can tell a
// delegation from a reply.
const delegationClientPrefix = "delegation:"

// notDelegationSQL excludes delegation messages from a messages query.
const notDelegationSQL = ` AND COALESCE(client_message_id,'') NOT LIKE 'delegation:%'`

func visibleDelegationMessage(conv string, seq int64, sender, runID string, names []string, brief, t string) Message {
	var b strings.Builder
	for _, n := range names {
		b.WriteString("@" + n + " ")
	}
	b.WriteString(brief)
	return Message{ID: uuid.NewString(), ConversationID: conv, Seq: seq, Role: "assistant", SenderBotID: sender, RunID: runID, Content: b.String(), CreatedAt: t}
}

// insertVisibleDelegationTx stores the message; the caller emits its event.
func insertVisibleDelegationTx(tx *sql.Tx, m Message) error {
	_, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at,client_message_id) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, m.RunID, m.Content, m.CreatedAt, delegationClientPrefix+m.ID)
	return err
}

// sameGroupRecipients reports whether conv is a visible group and every
// recipient is a current member of it.
func (s *Store) sameGroupRecipients(conv string, recipients []string) bool {
	var kind string
	var visible int
	if s.db.QueryRow(`SELECT kind,user_visible FROM conversations WHERE id=?`, conv).Scan(&kind, &visible) != nil || kind != "group" || visible == 0 {
		return false
	}
	for _, id := range recipients {
		if ok, err := s.IsMember(conv, strings.TrimSpace(id)); err != nil || !ok {
			return false
		}
	}
	return len(recipients) > 0
}

// AddGroupDelegations gives one brief to several members of the group in one
// visible message of the assigner. Each recipient gets a queued group task run
// triggered by that message, with its task and the wait the assigner holds.
func (s *Store) AddGroupDelegations(conv, sender string, targets []string, parent, brief string) (Message, []Run, error) {
	m, runs, err := s.addGroupDelegations(conv, sender, targets, parent, brief)
	s.afterTreeLimit(err)
	return m, runs, err
}

func (s *Store) addGroupDelegations(conv, sender string, targets []string, parent, brief string) (Message, []Run, error) {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return Message{}, nil, errors.New("message content is required")
	}
	var unique []string
	seen := map[string]bool{}
	for _, id := range targets {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return Message{}, nil, errors.New("at least one message recipient is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, nil, err
	}
	defer tx.Rollback()
	var parentConv, parentStatus string
	if err = tx.QueryRow(`SELECT conversation_id,status FROM runs WHERE id=?`, parent).Scan(&parentConv, &parentStatus); err != nil {
		return Message{}, nil, err
	}
	if parentConv != conv || parentStatus != "running" {
		return Message{}, nil, errors.New("message parent is not active in this conversation")
	}
	if err = requireCurrentMemberTx(tx, conv, sender); err != nil {
		return Message{}, nil, err
	}
	type target struct{ id, name, model string }
	infos := make([]target, 0, len(unique))
	names := make([]string, 0, len(unique))
	for _, id := range unique {
		if id == sender {
			return Message{}, nil, errors.New("cannot message the current bot")
		}
		if err = requireCurrentMemberTx(tx, conv, id); err != nil {
			return Message{}, nil, err
		}
		var tg target
		if err = tx.QueryRow(`SELECT id,name,model FROM bots WHERE id=?`, id).Scan(&tg.id, &tg.name, &tg.model); err != nil {
			return Message{}, nil, err
		}
		infos = append(infos, tg)
		names = append(names, tg.name)
	}
	var origin sql.NullString
	_ = tx.QueryRow(`SELECT origin_conversation_id FROM runs WHERE id=?`, parent).Scan(&origin)
	if origin.String == "" {
		origin.String = conv
	}
	t := now()
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, nil, err
	}
	m := visibleDelegationMessage(conv, seq, sender, parent, names, brief, t)
	if err = insertVisibleDelegationTx(tx, m); err != nil {
		return Message{}, nil, err
	}
	if err = insertEventTx(tx, conv, "message", m, t); err != nil {
		return Message{}, nil, err
	}
	runs := make([]Run, 0, len(infos))
	for _, tg := range infos {
		var queueSeq int64
		if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, conv).Scan(&queueSeq); err != nil {
			return Message{}, nil, err
		}
		run := Run{ID: uuid.NewString(), ConversationID: conv, BotID: tg.id, Status: "queued", ParentRunID: parent, Model: tg.model, Kind: runKindGroupTask, OriginConversationID: origin.String, TriggerMessageID: m.ID, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
		if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, conv, run.BotID, run.Status, parent, run.Model, run.Kind, origin.String, m.ID, queueSeq, t, t); err != nil {
			return Message{}, nil, err
		}
		if _, err = recordTaskForChildTx(tx, delegationChild{RunID: run.ID, RunConversation: conv, RunBot: run.BotID, RunKind: run.Kind, Origin: origin.String, Trigger: m.ID,
			ParentRun: parent, ParentBot: sender, ParentConversation: conv, Brief: brief, RunStatus: "queued"}); err != nil {
			return Message{}, nil, err
		}
		if err = chargeTreeTx(tx, treeKeyForRunTx(tx, parent), 1, true); err != nil {
			return Message{}, nil, err
		}
		if err = insertEventTx(tx, conv, "run", run, t); err != nil {
			return Message{}, nil, err
		}
		runs = append(runs, run)
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv); err != nil {
		return Message{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, nil, err
	}
	return m, runs, nil
}

// isVisibleDelegation reports whether m is a delegation message of another
// member in conv, the trigger of a run of bot.
func (s *Store) isVisibleDelegation(m Message, conv, bot string) bool {
	if m.ConversationID != conv || m.Role != "assistant" || m.Kind != "" || m.SenderBotID == "" || m.SenderBotID == bot {
		return false
	}
	var found bool
	return s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE id=? AND client_message_id=?)`, m.ID, delegationClientPrefix+m.ID).Scan(&found) == nil && found
}

// publishDelegationMessage posts the delegation message for tasks that start
// through the inbox (a task that waits, or a second task of one run). The
// message triggers nothing: the tasks carry the work. A failure is logged.
func (s *Store) publishDelegationMessage(conv, sender, runID string, recipients []string, brief string) {
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("[delegate] message: %v", err)
		return
	}
	defer tx.Rollback()
	names := make([]string, 0, len(recipients))
	for _, id := range recipients {
		var name string
		if tx.QueryRow(`SELECT name FROM bots WHERE id=?`, strings.TrimSpace(id)).Scan(&name) != nil {
			return
		}
		names = append(names, name)
	}
	var seq int64
	if err = tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		log.Printf("[delegate] message: %v", err)
		return
	}
	t := now()
	m := visibleDelegationMessage(conv, seq, sender, runID, names, strings.TrimSpace(brief), t)
	if err = insertVisibleDelegationTx(tx, m); err == nil {
		err = insertEventTx(tx, conv, "message", m, t)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, conv)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("[delegate] message: %v", err)
	}
}
