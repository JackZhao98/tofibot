package app

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// ErrDelegationToMentionedBot reports that a group delegation was refused
// because the target Bot already has an unfinished root run for the same user
// message. The user addressed that Bot directly, so it is already handling its
// own part; delegating again would only duplicate or contradict that work.
var ErrDelegationToMentionedBot = errors.New("target bot is already handling this user message")

// delegationConflictTx decides how delegating to target relates to the user
// message the delegating run descends from. Outcomes:
//   - no conflict: the target was not mentioned by that message, or it was and
//     already finished without having received a delegation yet (one handover);
//   - steer: the target's root run is running; the delegation is delivered to it
//     as steering input (the returned ids are the running roots);
//   - refuse: the target's root is queued or waiting (it cannot take input yet),
//     or a finished target already received its one handover.
func delegationConflictTx(tx *sql.Tx, parentID, target string) (refuse bool, steer []string, err error) {
	const triggers = `WITH RECURSIVE ancestors(id,parent_run_id,trigger_message_id) AS (
		SELECT id,parent_run_id,trigger_message_id FROM runs WHERE id=?
		UNION SELECT r.id,r.parent_run_id,r.trigger_message_id FROM runs r JOIN ancestors a ON a.parent_run_id=r.id
	), userTriggers(id) AS (
		SELECT DISTINCT a.trigger_message_id FROM ancestors a JOIN messages m ON m.id=a.trigger_message_id WHERE m.role='user'
	)`
	rows, err := tx.Query(triggers+` SELECT id,status FROM runs WHERE bot_id=? AND kind='' AND trigger_message_id IN (SELECT id FROM userTriggers)`, parentID, target)
	if err != nil {
		return false, nil, err
	}
	roots, blocked := 0, false
	for rows.Next() {
		var id, status string
		if err = rows.Scan(&id, &status); err != nil {
			rows.Close()
			return false, nil, err
		}
		roots++
		switch status {
		case "running":
			steer = append(steer, id)
		case "queued", "waiting":
			blocked = true
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, nil, err
	}
	if roots == 0 {
		return false, nil, nil
	}
	if blocked {
		return true, nil, nil
	}
	if len(steer) > 0 {
		return false, steer, nil
	}
	var delegated int
	err = tx.QueryRow(triggers+`, family(id) AS (
		SELECT id FROM runs WHERE kind='' AND trigger_message_id IN (SELECT id FROM userTriggers)
		UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
	) SELECT COUNT(*) FROM runs WHERE id IN (SELECT id FROM family) AND bot_id=? AND kind=?`, parentID, target, runKindGroupTask).Scan(&delegated)
	return delegated > 0, nil, err
}

// steersOwnRun reports whether r is a delegation delivered into the same Bot's
// still-running root run for the same user message.
func (s *Store) steersOwnRun(r Run) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_steering l JOIN runs o ON o.id=l.old_run_id WHERE l.new_run_id=? AND o.bot_id=?`, r.ID, r.BotID).Scan(&n)
	return n > 0
}

// delegationRefusal is the structured not_executed outcome returned to the
// model when delegationConflictTx fires.
func delegationRefusal(botName string) error {
	return tooloutcome.New(tooloutcome.Denied, "bot_already_handling_message", "not_executed",
		fmt.Sprintf("%s was mentioned in the same user message and is already working on its own part, so this task was not delegated. Do only your own part and post your result as a plain group reply; mention %s only to hand over a finished result.", botName, botName),
		"explain_blocker").Err()
}

type mentionSpan struct {
	botID      string
	start, end int
}

// mentionSpans lists the member mentions in order of appearance, matching
// names exactly as parseMentions does.
func mentionSpans(content string, members []Bot) []mentionSpan {
	byName := map[string]string{}
	names := []string{}
	for _, b := range members {
		k := strings.ToLower(b.Name)
		if _, dup := byName[k]; dup {
			byName[k] = ""
			continue
		}
		byName[k] = b.ID
		names = append(names, k)
	}
	sort.Slice(names, func(i, j int) bool { return len([]rune(names[i])) > len([]rune(names[j])) })
	runes := []rune(content)
	var out []mentionSpan
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' || (i > 0 && (unicode.IsLetter(runes[i-1]) || unicode.IsDigit(runes[i-1]) || runes[i-1] == '_')) {
			continue
		}
		for _, name := range names {
			nr := []rune(name)
			end := i + 1 + len(nr)
			if end > len(runes) || !strings.EqualFold(string(runes[i+1:end]), name) || byName[name] == "" {
				continue
			}
			if end < len(runes) && (unicode.IsLetter(runes[end]) || unicode.IsDigit(runes[end]) || runes[end] == '_' || runes[end] == '@') {
				continue
			}
			out = append(out, mentionSpan{botID: byName[name], start: i, end: end})
			i = end - 1
			break
		}
	}
	return out
}

// concurrentMentionContext tells a Bot started by a multi-mention user message
// which other Bots were addressed in that same message and what each was
// asked. It returns "" when this run is not one of several concurrent roots.
func (s *Server) concurrentMentionContext(c Conversation, r Run, bot Bot) string {
	if c.Kind != "group" || r.TriggerMessageID == "" || r.ParentRunID != "" || r.Kind != "" {
		return ""
	}
	anchor, err := s.store.GetMessage(r.TriggerMessageID)
	if err != nil || anchor.Role != "user" || anchor.ConversationID != c.ID {
		return ""
	}
	rows, err := s.store.db.Query(`SELECT DISTINCT bot_id FROM runs WHERE trigger_message_id=? AND COALESCE(parent_run_id,'')='' AND kind='' AND bot_id<>?`, anchor.ID, bot.ID)
	if err != nil {
		return ""
	}
	others := map[string]bool{}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			others[id] = true
		}
	}
	rows.Close()
	if len(others) == 0 {
		return ""
	}
	members, err := s.memberBots(c)
	if err != nil {
		return ""
	}
	spans := mentionSpans(anchor.Content, members)
	runes := []rune(anchor.Content)
	asked := map[string]string{}
	for i, sp := range spans {
		stop := len(runes)
		if i+1 < len(spans) {
			stop = spans[i+1].start
		}
		text := strings.TrimSpace(strings.TrimLeft(string(runes[sp.end:stop]), " \t,，:：-—"))
		if text != "" {
			asked[sp.botID] += text
		}
	}
	var b strings.Builder
	b.WriteString("[concurrent mentions]\nThe user's message mentioned several Bots, and each mentioned Bot started its own run at the same time. Other Bots working on it now:\n")
	for _, m := range members {
		if !others[m.ID] {
			continue
		}
		text := asked[m.ID]
		if text == "" {
			text = "(no separate text; see the user's message)"
		}
		fmt.Fprintf(&b, "- %s (id=%s) was asked: %s\n", m.Name, m.ID, trimRunes(text, 600))
	}
	b.WriteString("Do only your own part. Coordinate by replying in the group; mention another Bot only to hand over the result of your part. Do not delegate the user's request, or another Bot's part, to a Bot that was mentioned in this message; the system will not start a duplicate run for it.\n[/concurrent mentions]")
	return b.String()
}

// delegationConflictError wraps ErrDelegationToMentionedBot with the target's
// display name for the model-facing refusal.
type delegationConflictError struct{ botName string }

func (e *delegationConflictError) Error() string { return ErrDelegationToMentionedBot.Error() }
func (e *delegationConflictError) Unwrap() error { return ErrDelegationToMentionedBot }
