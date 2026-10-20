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

// ErrRecipientAddressed reports that a hand-off or wake was refused because
// the target Bot is one of several the person addressed in the user message
// this run descends from. Each addressed Bot answers that message in a run of
// its own, so no other member starts or briefs it for the same message.
var ErrRecipientAddressed = errors.New("target bot was addressed by the same user message")

type recipientAddressedError struct{ botName string }

func (e *recipientAddressedError) Error() string { return ErrRecipientAddressed.Error() }
func (e *recipientAddressedError) Unwrap() error { return ErrRecipientAddressed }

// recipientAddressedRefusal is the classified outcome for that refusal. It is
// Transient with next action retry, like answer_review: nothing was executed,
// and the model's next call (without that member) is not fenced.
func recipientAddressedRefusal(botName string) error {
	return tooloutcome.New(tooloutcome.Transient, "recipient_already_addressed", "not_executed",
		fmt.Sprintf("%s was addressed by the same message from the person and answers it directly in its own turn, so it is not handed work or woken for this message. Do your own part and leave %s out.", botName, botName),
		"retry").Err()
}

// coAddressedRootTx reports whether target has its own root run for the user
// message that runID descends from, in a message that addressed several Bots
// (two or more root runs share that trigger). It reads only run rows: the
// person's message is never parsed here. Callers decide whether the
// structured-output mode applies before opening a transaction.
func coAddressedRootTx(q interface {
	QueryRow(string, ...any) *sql.Row
}, runID, target string) (bool, error) {
	const roots = `WITH RECURSIVE ancestors(id,parent_run_id,trigger_message_id) AS (
		SELECT id,parent_run_id,trigger_message_id FROM runs WHERE id=?
		UNION SELECT r.id,r.parent_run_id,r.trigger_message_id FROM runs r JOIN ancestors a ON a.parent_run_id=r.id
	), userTriggers(id) AS (
		SELECT DISTINCT a.trigger_message_id FROM ancestors a JOIN messages m ON m.id=a.trigger_message_id WHERE m.role='user'
	), rootRuns(bot_id) AS (
		SELECT bot_id FROM runs WHERE kind='' AND COALESCE(parent_run_id,'')='' AND trigger_message_id IN (SELECT id FROM userTriggers)
	)`
	var total, mine int
	if err := q.QueryRow(roots+` SELECT COUNT(*), COALESCE(SUM(bot_id=?),0) FROM rootRuns`, runID, target).Scan(&total, &mine); err != nil {
		return false, err
	}
	return total >= 2 && mine > 0, nil
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
			if !mentionNameEndsAt(nr, runes, end) {
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
	if len(parsed.IDs) < 2 || len(parsed.Ambiguous) != 0 {
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

// coAddressedRoots returns the root runs of the user message r belongs to, in
// queue order, when that message addressed several Bots and r is one of the
// roots. Under structured output each addressed Bot has such a root.
func (s *Store) coAddressedRoots(r Run) []Run {
	if r.Kind != "" || r.ParentRunID != "" || r.TriggerMessageID == "" {
		return nil
	}
	rows, err := s.db.Query(`SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE trigger_message_id=? AND kind='' AND COALESCE(parent_run_id,'')='' AND EXISTS(SELECT 1 FROM messages m WHERE m.id=runs.trigger_message_id AND m.role='user') ORDER BY queue_seq,created_at,id`, r.TriggerMessageID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []Run
	found := false
	for rows.Next() {
		x, err := scanRun(rows)
		if err != nil {
			return nil
		}
		found = found || x.ID == r.ID
		out = append(out, x)
	}
	if !found || len(out) < 2 {
		return nil
	}
	return out
}

// coAddressedReplies returns what the other addressed Bots posted for the same
// user message (their runs and everything those spawned), up to the next
// person message, so a Bot that answers later reads the earlier replies. Their
// say notes count too: a member whose part is an input to a later member
// leaves it as a folded note, which the later member reads and the person does
// not see unfolded.
func (s *Store) coAddressedReplies(r Run, conversationID string) []Message {
	anchor, err := s.GetMessage(r.TriggerMessageID)
	if err != nil || anchor.ConversationID != conversationID {
		return nil
	}
	rows, err := s.db.Query(`WITH RECURSIVE family(id) AS (
		SELECT id FROM runs WHERE trigger_message_id=? AND kind='' AND COALESCE(parent_run_id,'')='' AND id<>?
		UNION SELECT r.id FROM runs r JOIN family f ON r.parent_run_id=f.id
	) SELECT id FROM messages WHERE conversation_id=? AND seq>? AND role='assistant' AND (kind IN ('','segment') OR (kind='progress' AND client_message_id LIKE 'say-note:%'))
		AND run_id IN (SELECT id FROM family)
		AND seq<COALESCE((SELECT MIN(seq) FROM messages WHERE conversation_id=? AND role='user' AND seq>?),9223372036854775807)
		ORDER BY seq LIMIT 200`, r.TriggerMessageID, r.ID, conversationID, anchor.Seq, conversationID, anchor.Seq)
	if err != nil {
		return nil
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	var out []Message
	for _, id := range ids {
		if m, e := s.GetMessage(id); e == nil {
			out = append(out, m)
		}
	}
	return out
}

// coAddressedContext tells a Bot that the person's message also went to other
// Bots, which answer it in runs of their own in mention order. It names who
// replied before and who answers after, so the Bot builds on earlier replies
// and neither briefs nor waits for anyone.
func (s *Server) coAddressedContext(c Conversation, r Run, bot Bot) string {
	if c.Kind != "group" || !c.UserVisible {
		return ""
	}
	roots := s.store.coAddressedRoots(r)
	if len(roots) == 0 {
		return ""
	}
	anchor, err := s.store.GetMessage(r.TriggerMessageID)
	if err != nil {
		return ""
	}
	members, err := s.memberBots(c)
	if err != nil {
		return ""
	}
	_, asked := userMentionedHelpers(anchor, bot.ID, members)
	var lines []string
	before := true
	for _, root := range roots {
		if root.ID == r.ID {
			before = false
			continue
		}
		name := root.BotID
		if b, e := s.store.GetBot(root.BotID); e == nil {
			name = b.Name
		}
		place := "answers after you"
		if before {
			place = "answered before you"
		}
		line := fmt.Sprintf("- %s (id=%s), %s", name, root.BotID, place)
		if text := asked[root.BotID]; text != "" {
			line += "; asked: " + trimRunes(text, 600)
		}
		lines = append(lines, line)
	}
	return "[co-addressed]\nThe person addressed this message to you and to the members below. Each answers it directly in a run of its own, one after another in mention order. Answer the part meant for you. Replies from members who answered before you are in the transcript: build on them and leave out what they cover. No member waits on a brief from you, and handing work to these members or waking them for this message is refused.\nWhen the person asked for one combined or final result from several members, only the member that completes it publishes it. If your part is an input to a member who answers after you, put that work in a note: later members read it, and it stays folded away from the person. Then end your turn with respond, disposition silent: the person gets the one result from the member that completes it, with no line from you about notes or hand-offs. Answer visibly only when the person asked each member for its own answer. If members before you left notes as inputs, you hold the final: fold them into the single result the person asked for, publishing no account of their drafts, and leave it as the one visible result.\n" + strings.Join(lines, "\n") + "\n[/co-addressed]"
}

// coAddressedInputMayEndSilent reports whether a co-addressed root run may end
// with no visible answer. Three things must hold: the run left a say note this
// run (its part exists, folded, for the next member), it is not the last
// member, and a later member of the same message is still to run. The last
// member always owes the visible answer, so a combined result cannot end with
// every member silent; a later member that already failed or was cancelled
// leaves the earlier one answering visibly.
func (s *Store) coAddressedInputMayEndSilent(r Run) bool {
	roots := s.coAddressedRoots(r)
	if len(roots) < 2 {
		return false
	}
	later, seen := false, false
	for _, x := range roots {
		if x.ID == r.ID {
			seen = true
			continue
		}
		if seen && (x.Status == "queued" || x.Status == "running") {
			later = true
		}
	}
	if !later {
		return false
	}
	var notes int
	if s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant' AND kind='progress' AND client_message_id LIKE 'say-note:%' AND TRIM(content)<>''`, r.ID).Scan(&notes) != nil {
		return false
	}
	return notes > 0
}

// promoteCoAddressedFallback keeps a combined group result from ending in
// silence. When the last member of a co-addressed message has ended without a
// visible answer (its run failed) and every member is finished, the person has
// seen nothing from any of them; the latest part an earlier member left as a
// folded note then becomes the visible reply, deterministically, so the work
// the person asked for still reaches them.
func (s *Store) promoteCoAddressedFallback(r Run) {
	roots := s.coAddressedRoots(r)
	if len(roots) == 0 {
		return
	}
	var ids []any
	holders := ""
	for _, x := range roots {
		cur, err := s.GetRun(x.ID)
		if err != nil || cur.Status == "queued" || cur.Status == "running" || cur.Status == "waiting" {
			return
		}
		ids = append(ids, x.ID)
		holders += ",?"
	}
	var visible int
	if s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE role='assistant' AND kind IN ('','segment') AND TRIM(content)<>'' AND run_id IN (`+holders[1:]+`)`, ids...).Scan(&visible) != nil || visible > 0 {
		return
	}
	for i := len(roots) - 1; i >= 0; i-- {
		var origin string
		if s.db.QueryRow(`SELECT final_origin FROM runs WHERE id=?`, roots[i].ID).Scan(&origin) != nil || origin != finalOriginNone {
			continue
		}
		if beat, ok := s.lastSayBeat(roots[i].ID); ok {
			if _, err := s.promoteSayBeat(beat); err == nil {
				s.noteFinalOrigin(roots[i].ID, finalOriginPromoted)
			}
			return
		}
	}
}
