package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

// Response dispositions: every Bot turn in a group ends with one explicit
// choice, decided after reading the new messages.
const (
	dispReply     = "reply"
	dispSilent    = "silent"
	dispConcluded = "concluded"
	dispFollowUp  = "follow_up"
)

const defaultWaitTimeout = 10 * time.Minute

// visibleDelegationText: the delegated text is the colleague's brief; the
// group shows only a short handoff line that can be expanded.
const visibleDelegationText = "The brief for that member: the request and the context and constraints they need. The group sees only a short handoff line; post your own view to the group separately if it matters."

const groupDispositionGuidance = `[group response]
End each group turn with respond: reply (post; wake lists Bots to start, each with a concrete request; a plain @mention starts nothing), silent (not for you), concluded (a Bot's thanks/ok/done: post nothing), follow_up (waiting on a Bot: set wait_for, do not post "I'll wait"). Default reply, no wakes. Answer a person's greeting, goodbye or small talk briefly. Don't announce delegating.
[/group response]
`

// groupDispositionGuidanceStructured is the group prompt of a run that
// follows the say protocol: say(answer) posts, and its wake list starts other
// Bots. respond keeps only the choices that post nothing.
const groupDispositionGuidanceStructured = `[group response]
Post to the group with say(answer), which speaks to the person; a request to a Bot goes only in wake. Otherwise end the turn with respond: silent (not for you), concluded (a Bot's closing: post nothing), or follow_up (waiting on a Bot: set wait_for). Reply to the latest message only: a greeting or small talk gets a short reply, and a finished topic stays closed.
[/group response]
`

type groupWakeEntry struct {
	BotID   string `json:"bot_id"`
	Request string `json:"request"`
}

type groupChoice struct {
	disposition string
	wakes       []groupWakeEntry
	text        string
}

// replyChoice returns the run's recorded respond(reply), if any.
func (s *Server) replyChoice(r Run) (groupChoice, bool) {
	v, ok := s.dispositions.Load(r.ID)
	if !ok {
		return groupChoice{}, false
	}
	choice, ok := v.(groupChoice)
	return choice, ok && choice.disposition == dispReply
}

func normalizeReply(s string) string { return strings.Join(strings.Fields(s), " ") }

// hasPostedMessage reports whether the run already published a visible message.
func (s *Store) hasPostedMessage(runID string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE run_id=? AND role='assistant' AND kind IN ('','segment')`, runID).Scan(&n)
	return n > 0
}

// postedVisibleText reports whether the run already published a visible
// message (any kind a person sees) whose normalized text equals text.
func (s *Store) postedVisibleText(runID, text string) bool {
	return s.postedVisibleTextFor(runID, text, false)
}

// postedVisibleTextFor is postedVisibleText for either protocol. A structured
// run's folded notes are private working text, so only what a person was shown
// (a plain message or a segment) counts as already said.
func (s *Store) postedVisibleTextFor(runID, text string, structured bool) bool {
	text = normalizeReply(text)
	if text == "" {
		return false
	}
	kinds := `'','segment','progress'`
	if structured {
		kinds = `'','segment'`
	}
	rows, err := s.db.Query(`SELECT content FROM messages WHERE run_id=? AND role='assistant' AND kind IN (`+kinds+`)`, runID)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		if rows.Scan(&content) == nil && normalizeReply(content) == text {
			return true
		}
	}
	return false
}

// replyFinalText is the run's one visible final message when respond(reply)
// was called: its text, or nothing when that text was already posted this run.
// Whatever the model writes after respond is not posted again.
func (s *Server) replyFinalText(r Run, choice groupChoice) string {
	text := normalizeReply(choice.text)
	if text == "" {
		return ""
	}
	if s.store.postedVisibleTextFor(r.ID, text, r.structured) {
		return ""
	}
	return choice.text
}

// lineageKey identifies the originating user message (or scheduled occurrence)
// of a run: the trigger of its root run, else the root run itself.
func lineageKey(q interface {
	QueryRow(string, ...any) *sql.Row
}, runID string) string {
	key := runID
	for id, steps := runID, 0; id != "" && steps <= 64; steps++ {
		var parent, trigger sql.NullString
		if q.QueryRow(`SELECT parent_run_id,trigger_message_id FROM runs WHERE id=?`, id).Scan(&parent, &trigger) != nil {
			break
		}
		key = id
		if parent.String == "" && trigger.String != "" {
			key = trigger.String
		}
		id = parent.String
	}
	return key
}

func (s *Server) groupRespondTools(c Conversation, r Run) []runtime.Tool {
	if c.Kind != "group" || !c.UserVisible || r.Kind == runKindTriage {
		return nil
	}
	if r.structured {
		return []runtime.Tool{s.respondStructuredTool(c, r)}
	}
	return []runtime.Tool{{Name: "respond", Description: "End your group turn with one explicit response disposition: reply (text is the message to post and your final message; list Bots to start in wake with a concrete request), silent (nothing to add; post nothing), concluded (acknowledgement or closing such as thanks/ok/done; post nothing), or follow_up (waiting on another Bot's output: set wait_for). Call it once, before your final message for reply, or instead of a message for the others.", Parameters: objectSchema(map[string]any{
		"disposition": map[string]any{"type": "string", "enum": []string{dispReply, dispSilent, dispConcluded, dispFollowUp}},
		"wake":        map[string]any{"type": "array", "items": objectSchema(map[string]any{"bot_id": map[string]any{"type": "string"}, "request": map[string]any{"type": "string", "description": visibleDelegationText}}, []string{"bot_id", "request"})},
		"text":        map[string]any{"type": "string", "description": "reply only: the message to post. It is your final message; write nothing after calling respond."},
		"wait_for":    map[string]any{"type": "string", "description": "Bot id you are waiting on (follow_up only)"},
	}, []string{"disposition"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Disposition string           `json:"disposition"`
			Wake        []groupWakeEntry `json:"wake"`
			WaitFor     string           `json:"wait_for"`
			Text        string           `json:"text"`
		}
		if json.Unmarshal(data, &in) != nil {
			return "", errors.New("invalid respond arguments")
		}
		members := map[string]bool{}
		for _, id := range c.BotIDs {
			members[id] = true
		}
		for _, w := range in.Wake {
			if !members[w.BotID] || w.BotID == r.BotID || strings.TrimSpace(w.Request) == "" {
				return "", errors.New("each wake entry needs another group member's bot_id and a concrete request")
			}
		}
		if len(in.Wake) > 3 {
			return "", errors.New("wake at most 3 Bots")
		}
		if _, said := s.store.sayAnswer(r.ID); said {
			return "", errors.New("you already delivered your answer with say; your turn is complete, write nothing more")
		}
		if _, again := s.dispositions.Load(r.ID); again {
			return "", errors.New("respond was already called for this turn; your turn is complete, write nothing more")
		}
		switch in.Disposition {
		case dispReply:
			if strings.TrimSpace(in.Text) == "" && !s.store.hasPostedMessage(r.ID) {
				return "", errors.New("reply needs text: the message to post")
			}
			if (r.Kind == runKindSchedule || r.scheduleTask != nil) && strings.TrimSpace(in.Text) == "" {
				return "", errors.New("a scheduled run's reply needs text")
			}
			s.dispositions.Store(r.ID, groupChoice{disposition: dispReply, wakes: in.Wake, text: in.Text})
			return "Recorded. This is your final message; the listed Bots start after it is published. Write nothing more.", nil
		case dispSilent, dispConcluded, dispFollowUp:
			if len(in.Wake) > 0 {
				return "", errors.New("only a reply can wake Bots")
			}
			if r.Kind == runKindSchedule || r.scheduleTask != nil || r.Kind == runKindGroupTask || r.Kind == runKindTeam || r.Kind == runKindMessage {
				return "", errors.New("this assignment needs a posted result; use reply")
			}
			if in.Disposition == dispFollowUp {
				if !members[in.WaitFor] || in.WaitFor == r.BotID {
					return "", errors.New("follow_up needs wait_for set to another group member's bot_id")
				}
				if err := s.registerWait(c, r, in.WaitFor); err != nil {
					return "", err
				}
			} else if r.Kind != runKindGroupMention && r.Kind != runKindFollowup && r.Kind != runKindGroupChat {
				return "", errors.New("you were asked directly; reply with your result (or follow_up if you depend on another Bot)")
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
			return "Turn completed without a message.", nil
		}
		return "", errors.New("disposition must be reply, silent, concluded or follow_up")
	}}}
}

const waitTimeoutRequest = "The wait timed out: the Bot you were waiting for has not posted. Say plainly what is still missing, or do your part with what is available."

// registerWait persists the wait with its deadline so it survives restarts.
func (s *Server) registerWait(c Conversation, r Run, waitFor string) error {
	lineage := lineageKey(s.store.db, r.ID)
	timeout := s.waitTimeout
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}
	deadline := time.Now().UTC().Add(timeout)
	if _, err := s.store.db.Exec(`INSERT OR REPLACE INTO run_waits(run_id,conversation_id,bot_id,wait_for,lineage,status,deadline_at,created_at) VALUES(?,?,?,?,?,'pending',?,?)`, r.ID, c.ID, r.BotID, waitFor, lineage, deadline.Format(time.RFC3339Nano), now()); err != nil {
		return err
	}
	s.armWait(r.ID, deadline)
	return nil
}

func (s *Server) armWait(runID string, deadline time.Time) {
	time.AfterFunc(time.Until(deadline), func() { s.fireWait(runID, "", waitTimeoutRequest, "expired") })
}

// armPendingWaits re-arms every pending wait after a start. Overdue waits fire
// immediately; the conditional status update in fireWait keeps each wake single
// even if this runs more than once. A wait whose waiter left the group, or whose
// conversation was deleted, wakes nobody (wake creation checks membership, and
// deleting the conversation cascades the wait away). A newer user message does
// not cancel a wait: the wake is just another queued run behind it.
func (s *Server) armPendingWaits() {
	rows, err := s.store.db.Query(`SELECT run_id,deadline_at FROM run_waits WHERE status='pending'`)
	if err != nil {
		return
	}
	type pw struct {
		id string
		at time.Time
	}
	var all []pw
	for rows.Next() {
		var id, at string
		if rows.Scan(&id, &at) == nil {
			t, e := time.Parse(time.RFC3339Nano, at)
			if e != nil {
				t = time.Now()
			}
			all = append(all, pw{id, t})
		}
	}
	rows.Close()
	for _, w := range all {
		s.armWait(w.id, w.at)
	}
}

// fireWait wakes a registered waiter exactly once. msgID empty means timeout.
func (s *Server) fireWait(waitRunID, msgID, request, status string) {
	s.mu.Lock()
	closing := s.closing
	s.mu.Unlock()
	if closing {
		return
	}
	res, err := s.store.db.Exec(`UPDATE run_waits SET status=? WHERE run_id=? AND status='pending'`, status, waitRunID)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return
	}
	var conv, bot string
	if s.store.db.QueryRow(`SELECT conversation_id,bot_id FROM run_waits WHERE run_id=?`, waitRunID).Scan(&conv, &bot) != nil {
		return
	}
	waiter, err := s.store.GetRun(waitRunID)
	if err != nil {
		return
	}
	if msgID == "" {
		// Anchor a timeout on a message from the group, never on a person's
		// newest message: that one belongs to whoever the person is talking to.
		if s.store.db.QueryRow(`SELECT id FROM messages WHERE conversation_id=? AND role='assistant' AND kind<>'bot_result' ORDER BY seq DESC LIMIT 1`, conv).Scan(&msgID) != nil {
			msgID = waiter.TriggerMessageID
		}
		if msgID == "" {
			return
		}
	}
	c, err := s.store.GetConversation(conv)
	if err != nil {
		return
	}
	if wake, ok, err := s.store.addMentionWake(conv, waiter, msgID, bot, request, true); err == nil && ok {
		s.enqueue(c, wake)
	} else if err != nil {
		log.Printf("[run] wait wake %s: %v", waitRunID, err)
	}
}

// applyDisposition runs once a group run has published its final message:
// explicit wake entries start their Bots, and a post from a Bot someone
// registered to wait for wakes that waiter once.
func (s *Server) applyDisposition(c Conversation, r Run, msg Message) {
	v, _ := s.dispositions.LoadAndDelete(r.ID)
	if rec, said := s.store.sayAnswer(r.ID); said && r.structured && len(rec.Wake) > 0 {
		// The wake list was recorded with the answer, so it survives a restart
		// between recording and publishing.
		v = groupChoice{disposition: dispReply, wakes: rec.Wake}
	}
	if msg.ID == "" {
		// The reply was posted earlier in the run (or as a segment).
		_ = s.store.db.QueryRow(`SELECT id FROM messages WHERE run_id=? AND role='assistant' AND kind IN ('','segment') ORDER BY seq DESC LIMIT 1`, r.ID).Scan(&msg.ID)
	}
	if c.Kind != "group" || !c.UserVisible || msg.ID == "" {
		return
	}
	if choice, ok := v.(groupChoice); ok && choice.disposition == dispReply {
		for _, w := range choice.wakes {
			wake, ok, err := s.store.AddMentionWake(c.ID, r, msg.ID, w.BotID, strings.TrimSpace(w.Request))
			if err != nil {
				log.Printf("[run] mention wake %s -> %s: %v", r.ID, w.BotID, err)
				continue
			}
			if ok {
				if r.structured {
					// The request is the brief: the group sees the compact handed-over
					// line a handoff shows, with the wording behind it, not chat text.
					if err := s.store.publishWakeNotice(c.ID, r, wake, strings.TrimSpace(w.Request)); err != nil {
						log.Printf("[run] wake notice %s -> %s: %v", r.ID, w.BotID, err)
					}
				}
				s.enqueue(c, wake)
			}
		}
	}
	rows, err := s.store.db.Query(`SELECT run_id FROM run_waits WHERE conversation_id=? AND wait_for=? AND status='pending' AND bot_id<>? AND lineage=?`, c.ID, r.BotID, r.BotID, lineageKey(s.store.db, r.ID))
	if err != nil {
		return
	}
	var waiters []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			waiters = append(waiters, id)
		}
	}
	rows.Close()
	name := r.BotID
	if b, e := s.store.GetBot(r.BotID); e == nil {
		name = b.Name
	}
	for _, id := range waiters {
		s.fireWait(id, msg.ID, fmt.Sprintf("%s posted the content you were waiting for (see the message above). Continue your part with it.", name), "fulfilled")
	}
}

// respondStructuredTool is respond for a run that follows the say protocol.
// Posting, and waking other members, is say(answer); respond keeps the three
// dispositions that post nothing.
func (s *Server) respondStructuredTool(c Conversation, r Run) runtime.Tool {
	return runtime.Tool{Name: "respond", Description: "End your group turn without posting: silent (nothing to add), concluded (an acknowledgement or closing from another Bot), or follow_up (waiting on another Bot's output: set wait_for). To post to the group, deliver with say, purpose answer.", Parameters: objectSchema(map[string]any{
		"disposition": map[string]any{"type": "string", "enum": []string{dispSilent, dispConcluded, dispFollowUp}},
		"wait_for":    map[string]any{"type": "string", "description": "Bot id you are waiting on (follow_up only)"},
	}, []string{"disposition"}), Execute: func(ctx context.Context, data json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Disposition string `json:"disposition"`
			WaitFor     string `json:"wait_for"`
		}
		if json.Unmarshal(data, &in) != nil {
			return "", errors.New("invalid respond arguments")
		}
		switch in.Disposition {
		case dispSilent, dispConcluded, dispFollowUp:
		case dispReply:
			return "", errors.New("to post, deliver with say, purpose answer; respond only ends a turn that posts nothing")
		default:
			return "", errors.New("disposition must be silent, concluded or follow_up")
		}
		if _, said := s.store.sayAnswer(r.ID); said {
			return "", errors.New("you already delivered your answer with say; your turn is complete, write nothing more")
		}
		if _, again := s.dispositions.Load(r.ID); again {
			return "", errors.New("respond was already called for this turn; your turn is complete, write nothing more")
		}
		members := map[string]bool{}
		for _, id := range c.BotIDs {
			members[id] = true
		}
		coInput := false
		if r.Kind == runKindSchedule || r.scheduleTask != nil || r.Kind == runKindGroupTask || r.Kind == runKindTeam || r.Kind == runKindMessage {
			return "", errors.New("this assignment needs a posted result; deliver it with say, purpose answer")
		}
		if in.Disposition == dispFollowUp {
			if !members[in.WaitFor] || in.WaitFor == r.BotID {
				return "", errors.New("follow_up needs wait_for set to another group member's bot_id")
			}
			if err := s.registerWait(c, r, in.WaitFor); err != nil {
				return "", err
			}
		} else if r.Kind != runKindGroupMention && r.Kind != runKindFollowup && r.Kind != runKindGroupChat {
			if in.Disposition != dispSilent || !s.store.coAddressedInputMayEndSilent(r) {
				return "", errors.New("you were asked directly; deliver your result with say, purpose answer (or follow_up if you depend on another Bot). A co-addressed member whose part is an input for a later member may end after leaving that part as a note, only while a later member is still to answer")
			}
			coInput = true
		}
		if _, done, err := s.store.finishRunOpts(r.ID, c.ID, r.BotID, "", true, "", finishOpts{coAddressedInput: coInput}); err != nil {
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
		return "Turn completed without a message.", nil
	}}
}
