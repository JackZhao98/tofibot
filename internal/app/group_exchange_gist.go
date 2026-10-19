package app

import (
	"fmt"
	"strings"
)

// A group exchange is the stretch of a group transcript that starts at one
// person message and ends just before the next. When a Bot answers a new person
// message, an earlier exchange that is over and that the Bot took no part in
// reaches it as a short gist instead of the members' turns, so a closed topic
// is not on the table as live material. The test is run state, not text.

// groupExchangeGistKind marks the synthetic message that stands in for a
// gisted exchange inside the history loop.
const groupExchangeGistKind = "exchange_gist"

const (
	exchangeAskRunes     = 160
	exchangeLineRunes    = 260
	exchangeHeadLines    = 2
	exchangeTailLines    = 4
	exchangeMaxLinesFull = exchangeHeadLines + exchangeTailLines
)

// groupExchangeGistsApply says whether this run's history may fold closed
// exchanges: a user-visible group, answering a person message directly.
func (s *Server) groupExchangeGistsApply(c Conversation, r Run) bool {
	if c.Kind != "group" || !c.UserVisible || r.TriggerMessageID == "" {
		return false
	}
	return r.Kind == runKindGroupChat || (r.Kind == "" && r.ParentRunID == "")
}

// foldClosedGroupExchanges returns msgs with every finished exchange the Bot
// took no part in replaced by one gist message. Exchanges the Bot took part in,
// unfinished ones, and anything at or after the trigger are left as they are.
func (s *Server) foldClosedGroupExchanges(c Conversation, r Run, bot Bot, msgs []Message) []Message {
	if !s.groupExchangeGistsApply(c, r) {
		return msgs
	}
	trigger, err := s.store.GetMessage(r.TriggerMessageID)
	if err != nil || trigger.Role != "user" || trigger.ConversationID != c.ID {
		return msgs
	}
	var starts []int
	for i, m := range msgs {
		if m.Role == "user" && m.SenderBotID == "" && m.Kind == "" && m.Seq < trigger.Seq {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return msgs
	}
	members, _ := s.memberBots(c)
	names := map[string]string{}
	for _, b := range members {
		names[b.ID] = b.Name
	}
	out := make([]Message, 0, len(msgs))
	out = append(out, msgs[:starts[0]]...)
	for k, from := range starts {
		to := len(msgs)
		if k+1 < len(starts) {
			to = starts[k+1]
		}
		segment := msgs[from:to]
		// The segment ends at the next person message; the last one at the trigger.
		if k+1 == len(starts) {
			cut := len(segment)
			for i, m := range segment {
				if m.Seq >= trigger.Seq {
					cut = i
					break
				}
			}
			tail := segment[cut:]
			segment = segment[:cut]
			if gist, ok := s.closedExchangeGist(c, bot, members, names, segment, trigger.Seq); ok {
				out = append(out, gist)
			} else {
				out = append(out, segment...)
			}
			out = append(out, tail...)
			continue
		}
		if gist, ok := s.closedExchangeGist(c, bot, members, names, segment, msgs[to].Seq); ok {
			out = append(out, gist)
		} else {
			out = append(out, segment...)
		}
	}
	return out
}

// closedExchangeGist builds the gist when the exchange is finished and the Bot
// took no part. nextSeq is the seq of the person message that follows it.
func (s *Server) closedExchangeGist(c Conversation, bot Bot, members []Bot, names map[string]string, segment []Message, nextSeq int64) (Message, bool) {
	if len(segment) == 0 {
		return Message{}, false
	}
	ask := segment[0]
	family, err := s.store.exchangeRunFamily(ask.ID)
	if err != nil || len(family) == 0 {
		return Message{}, false
	}
	// Finished: every run the person message started, with the wakes and
	// handoffs they spawned, is settled, and none of them posted past the
	// next person message.
	for _, f := range family {
		switch f.Status {
		case "queued", "running", "waiting":
			return Message{}, false
		}
		if f.BotID == bot.ID && f.Kind != runKindGroupChat {
			return Message{}, false // the Bot was addressed, woken or handed work
		}
	}
	var lines []string
	for _, m := range segment {
		if m.Seq == ask.Seq {
			if spansMention(m.Content, members, bot.ID) {
				return Message{}, false
			}
			continue
		}
		if m.SenderBotID == bot.ID {
			return Message{}, false // the Bot posted here
		}
		if m.SenderBotID != "" && spansMention(m.Content, members, bot.ID) {
			return Message{}, false
		}
		if m.Role != "assistant" || m.SenderBotID == "" || (m.Kind != "" && m.Kind != "segment") {
			continue
		}
		text := firstSentence(m.Content, exchangeLineRunes)
		if text == "" {
			continue
		}
		name := names[m.SenderBotID]
		if name == "" {
			name = m.SenderBotID
		}
		lines = append(lines, "- "+name+": "+text)
	}
	if len(lines) == 0 {
		return Message{}, false
	}
	posted, err := s.store.familyLastSeq(family)
	if err != nil || posted >= nextSeq {
		return Message{}, false
	}
	if len(lines) > exchangeMaxLinesFull {
		omitted := len(lines) - exchangeMaxLinesFull
		kept := append([]string{}, lines[:exchangeHeadLines]...)
		kept = append(kept, fmt.Sprintf("- (%d messages in between omitted)", omitted))
		lines = append(kept, lines[len(lines)-exchangeTailLines:]...)
	}
	ask1 := strings.Join(strings.Fields(ask.Content), " ")
	body := "[message_id=" + ask.ID + "] [closed group exchange, gist]\nThe group dealt with this earlier without you, and it is over. This is only a gist; the full text stays in the conversation history and search_history finds it if the person asks about it.\nThe person wrote: " + trimRunesMarker(ask1, exchangeAskRunes, "…") + "\nWhat was said, in order:\n" + strings.Join(lines, "\n") + "\n[/closed group exchange]"
	last := segment[len(segment)-1]
	return Message{ID: "", ConversationID: c.ID, Seq: last.Seq, Role: "user", Kind: groupExchangeGistKind, Content: body, CreatedAt: last.CreatedAt}, true
}

func spansMention(content string, members []Bot, botID string) bool {
	for _, sp := range mentionSpans(content, members) {
		if sp.botID == botID {
			return true
		}
	}
	return false
}

// firstSentence keeps the opening of a post, up to two sentences, bounded.
func firstSentence(content string, limit int) string {
	text := strings.Join(strings.Fields(content), " ")
	if text == "" {
		return ""
	}
	rs := []rune(text)
	cut, ends := len(rs), 0
	for i, r := range rs {
		if i >= 12 && strings.ContainsRune("。！？.!?", r) {
			if ends++; ends == 2 {
				cut = i + 1
				break
			}
		}
	}
	return trimRunesMarker(string(rs[:cut]), limit, "…")
}

type exchangeRun struct {
	ID, BotID, Kind, Status string
}

// exchangeRunFamily lists the runs a person message started and every run
// descended from them by parent link (handoffs, wakes, invitations).
func (s *Store) exchangeRunFamily(userMessageID string) ([]exchangeRun, error) {
	rows, err := s.db.Query(`WITH RECURSIVE family(id) AS (
 SELECT id FROM runs WHERE trigger_message_id=?
 UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
) SELECT r.id,r.bot_id,r.kind,r.status FROM runs r WHERE r.id IN (SELECT id FROM family)`, userMessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exchangeRun
	for rows.Next() {
		var f exchangeRun
		if err := rows.Scan(&f.ID, &f.BotID, &f.Kind, &f.Status); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// familyLastSeq is the highest seq any run of the family posted.
func (s *Store) familyLastSeq(family []exchangeRun) (int64, error) {
	var maxSeq int64
	for _, f := range family {
		var seq int64
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE run_id=?`, f.ID).Scan(&seq); err != nil {
			return 0, err
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq, nil
}
