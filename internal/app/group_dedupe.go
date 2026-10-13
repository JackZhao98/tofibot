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

// coordinatorFamilySQL lists a run and all its descendants.
const coordinatorFamilySQL = `WITH RECURSIVE family(id) AS (
	SELECT ? UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
)`

// userMentionedHelpers returns the Bots a multi-mention user message addressed
// besides the coordinator, in order of mention, with the text addressed to each.
func userMentionedHelpers(anchor Message, coordinator string, members []Bot) (ids []string, asked map[string]string) {
	parsed := parseMentions(anchor.Content, members)
	if len(parsed.IDs) < 2 || len(parsed.Unknown) != 0 || len(parsed.Ambiguous) != 0 {
		return nil, nil
	}
	found := false
	for _, id := range parsed.IDs {
		if id == coordinator {
			found = true
		} else {
			ids = append(ids, id)
		}
	}
	if !found {
		return nil, nil
	}
	spans := mentionSpans(anchor.Content, members)
	runes := []rune(anchor.Content)
	asked = map[string]string{}
	for i, sp := range spans {
		stop := len(runes)
		if i+1 < len(spans) {
			stop = spans[i+1].start
		}
		if text := strings.TrimSpace(strings.TrimLeft(string(runes[sp.end:stop]), " \t,，:：-—")); text != "" {
			asked[sp.botID] += text
		}
	}
	return ids, asked
}

// coordinationContext tells the coordinator of a multi-mention user message
// (the only Bot that got a root run) which mentioned Bots still need their part
// handed over. It serves the root run and every group_followup run in its
// chain, and returns "" for anything else, including legacy messages whose
// mentioned Bots did start their own root runs.
func (s *Server) coordinationContext(c Conversation, r Run, bot Bot) string {
	if c.Kind != "group" || (r.Kind != "" && r.Kind != runKindFollowup) {
		return ""
	}
	rootID := r.ID
	root := r
	for steps := 0; root.ParentRunID != ""; steps++ {
		if steps > 64 {
			return ""
		}
		p, err := s.store.GetRun(root.ParentRunID)
		if err != nil {
			return ""
		}
		root, rootID = p, p.ID
	}
	if root.Kind != "" || root.BotID != bot.ID || root.TriggerMessageID == "" {
		return ""
	}
	anchor, err := s.store.GetMessage(root.TriggerMessageID)
	if err != nil || anchor.Role != "user" || anchor.ConversationID != c.ID {
		return ""
	}
	members, err := s.memberBots(c)
	if err != nil {
		return ""
	}
	helpers, asked := userMentionedHelpers(anchor, bot.ID, members)
	if len(helpers) == 0 {
		return ""
	}
	var otherRoots int
	if s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE trigger_message_id=? AND COALESCE(parent_run_id,'')='' AND kind='' AND bot_id<>?`, anchor.ID, bot.ID).Scan(&otherRoots) != nil || otherRoots > 0 {
		return ""
	}
	handed := map[string]bool{}
	rows, err := s.store.db.Query(coordinatorFamilySQL+` SELECT DISTINCT bot_id FROM runs WHERE id IN (SELECT id FROM family) AND kind=? AND status<>'failed'`, rootID, runKindGroupTask)
	if err != nil {
		return ""
	}
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			handed[id] = true
		}
	}
	rows.Close()
	byID := map[string]Bot{}
	for _, m := range members {
		byID[m.ID] = m
	}
	var pending []string
	for _, id := range helpers {
		if handed[id] {
			continue
		}
		text := asked[id]
		if text == "" {
			text = "(see the user's message)"
		}
		pending = append(pending, fmt.Sprintf("- %s (id=%s) asked: %s", byID[id].Name, id, trimRunes(text, 600)))
	}
	if len(pending) == 0 {
		return "[coordination]\nEvery Bot the user mentioned has returned its part. Use their results to finish the user's request, then reply.\n[/coordination]"
	}
	if r.Kind == runKindFollowup {
		return "[coordination]\nThe user also mentioned these Bots, whose parts are still open. Hand the next one its part with handoff (one per turn) and wait for its result; when none remain, finish the user's request and reply.\n" + strings.Join(pending, "\n") + "\n[/coordination]"
	}
	return "[coordination]\nThe user addressed this message to you and also mentioned the Bots below. You coordinate: hand each its part with handoff (one at a time, in order), use their returned results, then finish the user's request and reply.\n" + strings.Join(pending, "\n") + "\n[/coordination]"
}

// delegationConflictError wraps ErrDelegationToMentionedBot with the target's
// display name for the model-facing refusal.
type delegationConflictError struct{ botName string }

func (e *delegationConflictError) Error() string { return ErrDelegationToMentionedBot.Error() }
func (e *delegationConflictError) Unwrap() error { return ErrDelegationToMentionedBot }
