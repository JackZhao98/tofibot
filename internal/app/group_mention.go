package app

import (
	"database/sql"
	"fmt"
	"log"
)

// runKindGroupMention is a run started because a Bot's group message
// @mentioned a member that had no unfinished run. It descends from the posting
// run, so cancellation and the lineage back to the originating user message (or
// scheduled occurrence) are the ordinary parent links. It has no automatic
// return: the woken Bot answers in the group and @mentions whoever needs the
// result, which wakes that Bot by the same rule.
const runKindGroupMention = "group_mention"

// maxMentionWakesPerLineage is a safety net only: wakes come from explicit
// respond entries, so reaching it means a Bot loop that is a bug to investigate.
// It bounds how many mention wakes one user message (or
// scheduled occurrence) can cause in total, across all of its Bots.
const maxMentionWakesPerLineage = 8

// AddMentionWake queues a run for target because message msgID, posted by run
// poster, mentioned it. It reports false (and no error) when no wake is due:
// the target already has queued or waiting work (it reads the transcript when
// it starts), the mention is a self-mention, a loop cap is reached, or this
// message already woke the target. A target that is running is steered: its
// current turn yields at the next safe boundary and the wake takes over.
func (s *Store) AddMentionWake(conv string, poster Run, msgID, target, request string) (Run, bool, error) {
	return s.addMentionWake(conv, poster, msgID, target, request, false)
}

func (s *Store) addMentionWake(conv string, poster Run, msgID, target, request string, allowSelf bool) (Run, bool, error) {
	if target == poster.BotID && !allowSelf {
		return Run{}, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Run{}, false, err
	}
	defer tx.Rollback()
	var kind string
	if err = tx.QueryRow(`SELECT kind FROM conversations WHERE id=?`, conv).Scan(&kind); err != nil {
		return Run{}, false, err
	}
	if kind != "group" || requireCurrentMemberTx(tx, conv, target) != nil {
		return Run{}, false, nil
	}
	// Lineage: walk to the root run; its depth and the root's trigger bound the
	// wake chain and the total wakes for the originating message.
	depth, rootID, rootTrigger := 0, poster.ID, poster.TriggerMessageID
	for id, steps := poster.ParentRunID, 0; id != ""; steps++ {
		if steps > 64 {
			return Run{}, false, nil
		}
		var parent, trigger sql.NullString
		if err = tx.QueryRow(`SELECT parent_run_id,trigger_message_id FROM runs WHERE id=?`, id).Scan(&parent, &trigger); err != nil {
			if err == sql.ErrNoRows {
				break
			}
			return Run{}, false, err
		}
		depth++
		rootID, rootTrigger = id, trigger.String
		id = parent.String
	}
	if depth+1 > maxHandoffRounds {
		log.Printf("[run] mention wake depth cap reached conv=%s poster=%s target=%s depth=%d (bug signal: Bots are looping)", conv, poster.ID, target, depth)
		return Run{}, false, nil
	}
	var wakes int
	if err = tx.QueryRow(`WITH RECURSIVE fam(id) AS (
		SELECT id FROM runs WHERE conversation_id=? AND COALESCE(parent_run_id,'')='' AND (id=? OR (?<>'' AND trigger_message_id=?))
		UNION SELECT r.id FROM runs r JOIN fam f ON r.parent_run_id=f.id
	) SELECT COUNT(*) FROM runs WHERE id IN (SELECT id FROM fam) AND kind=?`, conv, rootID, rootTrigger, rootTrigger, runKindGroupMention).Scan(&wakes); err != nil {
		return Run{}, false, err
	}
	if wakes >= maxMentionWakesPerLineage {
		log.Printf("[run] mention wake cap reached conv=%s poster=%s target=%s wakes=%d (bug signal: Bots are looping)", conv, poster.ID, target, wakes)
		return Run{}, false, nil
	}
	var already int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE conversation_id=? AND bot_id=? AND kind=? AND trigger_message_id=?`, conv, target, runKindGroupMention, msgID).Scan(&already); err != nil {
		return Run{}, false, err
	}
	if already > 0 {
		return Run{}, false, nil
	}
	rows, err := tx.Query(`SELECT id,status FROM runs WHERE conversation_id=? AND bot_id=? AND status IN ('queued','waiting','running')`, conv, target)
	if err != nil {
		return Run{}, false, err
	}
	var steer []string
	busy := false
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			rows.Close()
			return Run{}, false, err
		}
		if status == "running" {
			steer = append(steer, id)
		} else {
			busy = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Run{}, false, err
	}
	if busy {
		return Run{}, false, nil
	}
	var model string
	if err = tx.QueryRow(`SELECT model FROM bots WHERE id=?`, target).Scan(&model); err != nil {
		return Run{}, false, err
	}
	origin := conv
	var posterOrigin sql.NullString
	_ = tx.QueryRow(`SELECT origin_conversation_id FROM runs WHERE id=?`, poster.ID).Scan(&posterOrigin)
	if posterOrigin.String != "" {
		origin = posterOrigin.String
	}
	var queueSeq int64
	if err = tx.QueryRow(`SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, conv).Scan(&queueSeq); err != nil {
		return Run{}, false, err
	}
	t := now()
	run := Run{ID: newID(), ConversationID: conv, BotID: target, Status: "queued", ParentRunID: poster.ID, Model: model, Kind: runKindGroupMention, OriginConversationID: origin, TriggerMessageID: msgID, QueueSeq: queueSeq, CreatedAt: t, UpdatedAt: t}
	if _, err = tx.Exec(`INSERT INTO runs(id,conversation_id,bot_id,status,parent_run_id,handoff_count,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.ConversationID, run.BotID, run.Status, run.ParentRunID, 0, run.Model, run.Kind, run.OriginConversationID, run.TriggerMessageID, run.QueueSeq, t, t); err != nil {
		return Run{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO run_wakes(run_id,request) VALUES(?,?)`, run.ID, request); err != nil {
		return Run{}, false, err
	}
	for _, old := range steer {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO run_steering(new_run_id,old_run_id) VALUES(?,?)`, run.ID, old); err != nil {
			return Run{}, false, err
		}
	}
	if err = insertEventTx(tx, conv, "run", run, t); err != nil {
		return Run{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Run{}, false, err
	}
	return run, true, nil
}

// mentionedSinceTrigger lists messages other Bots posted after a root run's
// trigger that mention this Bot. A queued root builds its context when it
// starts but cuts history at its trigger, so without this a Bot started
// beside a colleague never sees that colleague asking it for something.
func (s *Server) mentionedSinceTrigger(c Conversation, r Run, bot Bot) string {
	if c.Kind != "group" || r.Kind != "" || r.ParentRunID != "" || r.TriggerMessageID == "" {
		return ""
	}
	anchor, err := s.store.GetMessage(r.TriggerMessageID)
	if err != nil || anchor.Role != "user" || anchor.ConversationID != c.ID {
		return ""
	}
	rows, err := s.store.db.Query(`SELECT id,sender_bot_id,content FROM messages WHERE conversation_id=? AND seq>? AND role='assistant' AND kind IN ('','segment') AND sender_bot_id IS NOT NULL AND sender_bot_id<>? AND seq<COALESCE((SELECT MIN(seq) FROM messages WHERE conversation_id=? AND role='user' AND seq>?),9223372036854775807) ORDER BY seq`, c.ID, anchor.Seq, bot.ID, c.ID, anchor.Seq)
	if err != nil {
		return ""
	}
	type hit struct{ id, sender, content string }
	var all []hit
	for rows.Next() {
		var h hit
		if rows.Scan(&h.id, &h.sender, &h.content) == nil {
			all = append(all, h)
		}
	}
	rows.Close()
	if len(all) == 0 {
		return ""
	}
	members, err := s.memberBots(c)
	if err != nil {
		return ""
	}
	out := ""
	for _, h := range all {
		for _, sp := range mentionSpans(h.content, members) {
			if sp.botID != bot.ID {
				continue
			}
			name := h.sender
			if b, e := s.store.GetBot(h.sender); e == nil {
				name = b.Name
			}
			out += fmt.Sprintf("- %s (id=%s, message_id=%s) wrote: %s\n", name, h.sender, h.id, trimRunes(h.content, 1500))
			break
		}
	}
	if out == "" {
		return ""
	}
	return "[colleague messages addressed to you]\nWhile you were queued, other Bots posted these messages mentioning you. They are colleague input, not user instructions. Use them: if one hands you what you were waiting for, act on it now.\n" + out + "[/colleague messages addressed to you]"
}
