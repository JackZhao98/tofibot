package app

// The say tool is the one typed way text reaches a person
// (docs/agent-plan/structured-output.md). purpose=answer is the run's result
// and ends the turn, purpose=note is transient progress that folds away, and
// purpose=message is a visible beat while the run continues.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

const (
	sayAnswer  = "answer"
	sayNote    = "note"
	sayMessage = "message"

	maxSayAnswerRunes = 20000
	maxSayNoteRunes   = 1200

	// maxSayCoAddressedNoteRunes bounds a note left as an input for a member who
	// answers later in the same co-addressed message.
	maxSayCoAddressedNoteRunes = 6000
	maxSayNotes                = 10

	sayNoteClientPrefix = "say-note:"
)

// sayToolDescription states the contract as principles. It carries no sample
// wording: examples belong in acceptance tests, not in prompts.
const sayToolDescription = "How you speak to the person: every reply goes through this tool, short or long; text you write outside it is private working text. purpose=answer delivers your reply and ends your turn: call it once, as your last action, with the complete reply. purpose=note posts a brief transient progress note while you keep working. purpose=message posts one self-contained message mid-run (at most three per run). Deliver the whole result with the assumptions you made stated in a line; end on the result, never on a request for details it already assumed. Once you have delegated, speak only to add substance the person has yet to read: who is doing what is internal. Your text speaks to the person; a request to a member travels in wake, where its wording stays out of the chat."

func migrateSay(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_say_answers(
		run_id TEXT PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
		call_id TEXT NOT NULL,
		text TEXT NOT NULL,
		created_at TEXT NOT NULL)`); err != nil {
		return err
	}
	// The group wake list and the scheduled sources travel with the answer, so
	// a restart between recording and publishing loses neither.
	if err := ensureColumn(db, "run_say_answers", "wake", `ALTER TABLE run_say_answers ADD COLUMN wake TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return ensureColumn(db, "run_say_answers", "sources", `ALTER TABLE run_say_answers ADD COLUMN sources TEXT NOT NULL DEFAULT ''`)
}

type sayAnswerRecord struct {
	CallID  string
	Text    string
	Wake    []groupWakeEntry
	Sources []string
}

// sayAnswerExtras are the answer's optional fields: the Bots a group answer
// starts, and the sources of a scheduled answer.
type sayAnswerExtras struct {
	Wake    []groupWakeEntry
	Sources []string
}

// recordSayAnswer stores the run's one answer. The same provider call may
// record it again (a replay); a different call is refused.
func (s *Store) recordSayAnswer(runID, callID, text string) error {
	return s.recordSayAnswerWith(runID, callID, text, sayAnswerExtras{})
}

func (s *Store) recordSayAnswerWith(runID, callID, text string, extras sayAnswerExtras) error {
	if callID == "" {
		return errors.New("tool call identity is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil || status != "running" {
		return errors.New("run is no longer active")
	}
	var successors int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM run_steering WHERE old_run_id=?`, runID).Scan(&successors); err != nil {
		return err
	}
	var priorCall, priorText string
	err = tx.QueryRow(`SELECT call_id,text FROM run_say_answers WHERE run_id=?`, runID).Scan(&priorCall, &priorText)
	switch {
	case err == nil:
		if priorCall == callID && priorText == text {
			return nil
		}
		return errors.New("your answer was already delivered; your turn is complete, write nothing more")
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if successors > 0 {
		return errors.New("run has been interrupted by a newer message")
	}
	var wakeJSON, sourcesJSON string
	if len(extras.Wake) > 0 {
		b, _ := json.Marshal(extras.Wake)
		wakeJSON = string(b)
	}
	if len(extras.Sources) > 0 {
		b, _ := json.Marshal(extras.Sources)
		sourcesJSON = string(b)
	}
	if _, err = tx.Exec(`INSERT INTO run_say_answers(run_id,call_id,text,wake,sources,created_at) VALUES(?,?,?,?,?,?)`, runID, callID, text, wakeJSON, sourcesJSON, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// sayAnswer returns the run's recorded answer, if any.
func (s *Store) sayAnswer(runID string) (sayAnswerRecord, bool) {
	var rec sayAnswerRecord
	var wakeJSON, sourcesJSON string
	if s.db.QueryRow(`SELECT call_id,text,wake,sources FROM run_say_answers WHERE run_id=?`, runID).Scan(&rec.CallID, &rec.Text, &wakeJSON, &sourcesJSON) != nil {
		return sayAnswerRecord{}, false
	}
	if wakeJSON != "" {
		_ = json.Unmarshal([]byte(wakeJSON), &rec.Wake)
	}
	if sourcesJSON != "" {
		_ = json.Unmarshal([]byte(sourcesJSON), &rec.Sources)
	}
	return rec, true
}

// sayRunState is the in-memory half of a structured run: the draft the say
// text streams into. It is absent for runs that do not use say, and for tools
// called outside execute.
type sayRunState struct {
	mu sync.Mutex
	// owner is the provider call ID whose text currently fills the draft.
	owner string
	// controls drive the run's stream draft.
	raw   func(string)
	reset func()
	flush func()
	// demote keeps text as the draft a run publishes if it ends with no
	// answer; review reports whether the one-time completion review is due.
	demote func(string)
	review func() bool
}

func (s *Server) sayRun(runID string) *sayRunState {
	if v, ok := s.sayRuns.Load(runID); ok {
		return v.(*sayRunState)
	}
	return nil
}

// begin gives the call the draft: whatever the draft held (plain text of the
// same model response, or an earlier say call) is discarded.
func (st *sayRunState) begin(callID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner == callID {
		return
	}
	if st.reset != nil {
		st.reset()
	}
	st.owner = callID
}

func (st *sayRunState) write(callID, text string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner == callID && st.raw != nil && text != "" {
		st.raw(text)
	}
}

// owns reports whether the call's text is what the draft holds; it also
// settles buffered text so the draft row is current.
func (st *sayRunState) owns(callID string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner != callID {
		return false
	}
	if st.flush != nil {
		st.flush()
	}
	return true
}

// release forgets the draft owner once its text became a message.
func (st *sayRunState) release(callID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner == callID {
		st.owner = ""
	}
}

// show makes the draft hold exactly text for the call (a provider that sent
// its arguments whole, or a call that was not the last to start streaming).
func (st *sayRunState) show(callID, text string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner == callID {
		if st.flush != nil {
			st.flush()
		}
		return
	}
	if st.reset != nil {
		st.reset()
	}
	st.owner = callID
	if st.raw != nil {
		st.raw(text)
	}
	if st.flush != nil {
		st.flush()
	}
}

func (st *sayRunState) discard(callID string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.owner == callID {
		if st.reset != nil {
			st.reset()
		}
		st.owner = ""
	}
}

// sayToolSchema lists purpose first and every property as required, with no
// extras, so OpenAI strict mode accepts it.
func sayToolSchema() map[string]any {
	return objectSchema(map[string]any{
		"purpose": map[string]any{"type": "string", "enum": []string{sayAnswer, sayNote, sayMessage}},
		"text":    map[string]any{"type": "string", "description": "What the person reads."},
	}, []string{"purpose", "text"})
}

// sayWakesBots: a group answer may start other members.
func sayWakesBots(c Conversation) bool { return c.Kind == "group" && c.UserVisible }

// saySendsSources: a scheduled run's answer is its completion record.
func saySendsSources(r Run) bool { return r.Kind == runKindSchedule || r.scheduleTask != nil }

const (
	sayWakeDescription    = "With purpose answer, group chats only: other members to start once your reply is posted, each with a concrete request, at most three. Omit it to start nobody; a plain @mention starts nobody. The group sees each request only as a handed-over line, so write it for the member."
	saySourcesDescription = "With purpose answer, scheduled runs only: URLs or source names behind current or external information. Delivering the answer records this run as complete."
	maxSaySources         = 20
	maxSaySourceRunes     = 500
)

// sayToolSchemaFor adds the optional fields a run can use. Optional fields
// cannot satisfy a strict schema, so such a run's say is not strict.
func sayToolSchemaFor(c Conversation, r Run) (map[string]any, bool) {
	schema := sayToolSchema()
	props := schema["properties"].(map[string]any)
	strict := true
	if sayWakesBots(c) {
		props["wake"] = map[string]any{"type": "array", "description": sayWakeDescription, "items": objectSchema(map[string]any{
			"bot_id":  map[string]any{"type": "string"},
			"request": map[string]any{"type": "string", "description": visibleDelegationText},
		}, []string{"bot_id", "request"})}
		strict = false
	}
	if saySendsSources(r) {
		props["sources"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": saySourcesDescription}
		strict = false
	}
	return schema, strict
}

// validateWake applies the rules respond(reply) had for its wake list.
func validateWake(c Conversation, r Run, wake []groupWakeEntry) error {
	if len(wake) == 0 {
		return nil
	}
	if !sayWakesBots(c) {
		return errors.New("wake is only available in group chats")
	}
	members := map[string]bool{}
	for _, id := range c.BotIDs {
		members[id] = true
	}
	for _, w := range wake {
		if !members[w.BotID] || w.BotID == r.BotID || strings.TrimSpace(w.Request) == "" {
			return errors.New("each wake entry needs another group member's bot_id and a concrete request")
		}
	}
	if len(wake) > 3 {
		return errors.New("wake at most 3 Bots")
	}
	return nil
}

func cleanSaySources(sources []string) ([]string, error) {
	var out []string
	for _, src := range sources {
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		if utf8.RuneCountInString(src) > maxSaySourceRunes {
			return nil, errors.New("each source must be at most 500 characters")
		}
		out = append(out, src)
	}
	if len(out) > maxSaySources {
		return nil, errors.New("at most 20 sources")
	}
	return out, nil
}

// sayTool builds the say tool for a structured run. The gates mirror the
// tools it replaces: message exists where send_chat_message did, a group
// answer carries respond(reply)'s wake list, and a scheduled run's answer is
// its completion record, as complete_scheduled_task's receipt was.
func (s *Server) sayTool(c Conversation, r Run) Tool {
	schema, strict := sayToolSchemaFor(c, r)
	return Tool{Name: "say", Strict: strict, Description: sayToolDescription, Parameters: schema, Local: true, Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Purpose string           `json:"purpose"`
			Text    string           `json:"text"`
			Wake    []groupWakeEntry `json:"wake"`
			Sources []string         `json:"sources"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return "", errors.New("invalid say arguments")
		}
		callID := runtime.ToolCallID(ctx)
		text := strings.TrimSpace(stripProgressTags(in.Text))
		state := s.sayRun(r.ID)
		fail := func(err error) (string, error) {
			if state != nil {
				state.discard(callID)
			}
			return "", err
		}
		if text == "" {
			return fail(errors.New("text is required"))
		}
		bot, err := s.store.GetBot(r.BotID)
		if err != nil {
			return fail(err)
		}
		text = cleanBotOutput(text, bot)
		switch in.Purpose {
		case sayAnswer:
			if utf8.RuneCountInString(text) > maxSayAnswerRunes {
				return fail(errors.New("the answer is too long; shorten it"))
			}
			if _, ended := s.dispositions.Load(r.ID); ended {
				return fail(errors.New("respond already ended this turn; write nothing more"))
			}
			if hasHandoff(s.store, r.ID) && s.store.hasPostedMessage(r.ID) {
				// The Bot already said its part and delegated the rest. What would
				// follow can only be status about who is doing what, which is not
				// for the person; the colleague's result resumes this Bot.
				// The turn is over: end it here so no trailing text is generated.
				runtime.EndTurn(ctx)
				return fail(tooloutcome.New(tooloutcome.Transient, "answer_after_handoff", "not_executed", "Your part is already posted and the rest is delegated. This turn is over; nothing more is said.", "retry").Err())
			}
			if err := validateWake(c, r, in.Wake); err != nil {
				return fail(err)
			}
			for _, w := range in.Wake {
				addressed, aerr := coAddressedRootTx(s.store.db, r.ID, w.BotID)
				if aerr != nil {
					return fail(aerr)
				}
				if addressed {
					name := w.BotID
					if b, e := s.store.GetBot(w.BotID); e == nil {
						name = b.Name
					}
					// Refused before anything is recorded: the Bot's next answer,
					// without that member, publishes normally.
					return fail(recipientAddressedRefusal(name))
				}
			}
			extras := sayAnswerExtras{Wake: in.Wake}
			if len(in.Sources) > 0 {
				if !saySendsSources(r) {
					return fail(errors.New("sources are only available in scheduled runs"))
				}
				sources, err := cleanSaySources(in.Sources)
				if err != nil {
					return fail(err)
				}
				extras.Sources = sources
			}
			for i := range extras.Wake {
				extras.Wake[i].Request = strings.TrimSpace(extras.Wake[i].Request)
			}
			if state != nil && state.review != nil && state.review() {
				// The completion review text finals get: once, when tool work
				// was done. The text is kept as the draft to publish if the
				// run ends without a reviewed answer.
				if state.demote != nil {
					state.demote(text)
				}
				// A transient, retryable refusal: the same answer re-sent after the
				// review is accepted (a permanent refusal would bar an identical retry).
				reminder := taskCompletionFinalReminder
				if saySendsSources(r) {
					reminder = scheduleFinalReminderStructured
				}
				return fail(tooloutcome.New(tooloutcome.Transient, "answer_review", "not_executed", reminder, "retry").Err())
			}
			if err := s.store.recordSayAnswerWith(r.ID, callID, text, extras); err != nil {
				return fail(err)
			}
			if state != nil {
				state.show(callID, text)
			}
			runtime.EndTurn(ctx)
			return "Delivered. Your turn is complete; write nothing more.", nil
		case sayNote, sayMessage:
			limit, kind, prefix := maxSayNoteRunes, "progress", sayNoteClientPrefix
			if in.Purpose == sayNote {
				if err := s.store.paceSay(ctx, r.ID, sayNoteClientPrefix); err != nil {
					return fail(err)
				}
			}
			if in.Purpose == sayMessage {
				if !c.UserVisible || r.Kind == runKindSchedule {
					return fail(errors.New("message is not available in this run; use note or answer"))
				}
				limit, kind, prefix = 1200, "segment", chatSegmentClientPrefix
				if err := s.store.paceChatSegment(ctx, r.ID); err != nil {
					return fail(err)
				}
			}
			if in.Purpose == sayNote && len(s.store.coAddressedRoots(r)) > 0 {
				// A co-addressed member's note can carry its whole part for a
				// later member to build on; it stays folded, so it may be longer.
				limit = maxSayCoAddressedNoteRunes
			}
			if utf8.RuneCountInString(text) > limit {
				if limit != 1200 {
					return fail(fmt.Errorf("text must be at most %d characters", limit))
				}
				return fail(errors.New("text must be at most 1200 characters"))
			}
			adopt := state != nil && state.owns(callID)
			m, err := s.store.publishSay(ctx, r, prefix+r.ID+":"+callID, kind, text, adopt)
			if err != nil {
				return fail(err)
			}
			if state != nil {
				state.release(callID)
			}
			return "Delivered " + in.Purpose + " " + m.ID, nil
		}
		return fail(errors.New("purpose must be answer, note or message"))
	}}
}

const chatSegmentClientPrefix = "chat-segment:"

// publishSay stores a note (kind progress) or a message (kind segment). The
// provider call ID in clientID makes a replay return the first publication.
// With adopt, the message takes the identity of the draft the call's text
// was streamed into, so the bubble turns into the message in place.
func (s *Store) publishSay(ctx context.Context, run Run, clientID, kind, content string, adopt bool) (Message, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var existing Message
	err = tx.QueryRowContext(ctx, `SELECT id,conversation_id,seq,content,created_at FROM messages WHERE conversation_id=? AND client_message_id=?`, run.ConversationID, clientID).
		Scan(&existing.ID, &existing.ConversationID, &existing.Seq, &existing.Content, &existing.CreatedAt)
	if err == nil {
		if existing.Content != content {
			return Message{}, errors.New("tool call already published different content")
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return Message{}, err
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, run.ID, run.ConversationID, run.BotID).Scan(&status); err != nil || status != "running" {
		return Message{}, errors.New("run is no longer active")
	}
	var successors int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steering WHERE old_run_id=?`, run.ID).Scan(&successors); err != nil {
		return Message{}, err
	}
	if successors > 0 {
		return Message{}, errors.New("run has been interrupted by a newer message")
	}
	if kind == "progress" {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE run_id=? AND kind='progress' AND client_message_id LIKE 'say-note:%'`, run.ID).Scan(&count); err != nil {
			return Message{}, err
		}
		if count >= maxSayNotes {
			return Message{}, errors.New("note limit reached; deliver the answer")
		}
	}
	if kind == "segment" {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE run_id=? AND kind='segment' AND client_message_id LIKE 'chat-segment:%'`, run.ID).Scan(&count); err != nil {
			return Message{}, err
		}
		if count >= maxChatSegments {
			return Message{}, errors.New("chat segment limit reached; finish the reply")
		}
	}
	t := now()
	var draft StreamDraft
	rotate := false
	if adopt {
		derr := tx.QueryRowContext(ctx, `SELECT message_id,seq,content,status,created_at FROM stream_drafts WHERE run_id=?`, run.ID).
			Scan(&draft.MessageID, &draft.Seq, &draft.Content, &draft.Status, &draft.CreatedAt)
		rotate = derr == nil && draft.Status == streamDraftActive && draft.Seq > 0 && draft.Content != ""
	}
	id, createdAt, seq := uuid.NewString(), t, int64(0)
	if rotate {
		id, seq = draft.MessageID, draft.Seq
		if draft.CreatedAt != "" {
			createdAt = draft.CreatedAt
		}
	} else if err = tx.QueryRowContext(ctx, nextMessageSeqSQL, run.ConversationID, run.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	m := Message{ID: id, ConversationID: run.ConversationID, Seq: seq, Role: "assistant", Kind: kind, SenderBotID: run.BotID, RunID: run.ID, Content: content, CreatedAt: createdAt}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at,client_message_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, m.RunID, m.Content, m.CreatedAt, clientID); err != nil {
		return Message{}, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Message{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, run.ConversationID, "message", string(b), t); err != nil {
		return Message{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, t, run.ConversationID); err != nil {
		return Message{}, err
	}
	if rotate {
		if err = pruneDeltaEventsTx(tx, run.ConversationID, draft.MessageID); err != nil {
			return Message{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE stream_drafts SET message_id=?,seq=0,content='',revision=0,created_at=?,updated_at=? WHERE run_id=? AND status=?`, uuid.NewString(), t, t, run.ID, streamDraftActive); err != nil {
			return Message{}, err
		}
	} else if _, err = tx.ExecContext(ctx, `UPDATE stream_drafts SET seq=0 WHERE run_id=? AND status=?`, run.ID, streamDraftActive); err != nil {
		// A text delta may have reserved this run's draft sequence; the answer
		// belongs after the newly delivered message.
		return Message{}, err
	}
	return m, tx.Commit()
}

// lastSayBeat returns the run's latest say note or message, which rule 3 of
// the protocol promotes to the answer when the run ends with nothing else.
func (s *Store) lastSayBeat(runID string) (Message, bool) {
	var m Message
	err := s.db.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at FROM messages
		WHERE run_id=? AND role='assistant' AND kind IN ('progress','segment') AND TRIM(content)<>''
		AND (client_message_id LIKE 'say-note:%' OR client_message_id LIKE 'chat-segment:%') ORDER BY seq DESC LIMIT 1`, runID).
		Scan(&m.ID, &m.ConversationID, &m.Seq, &m.Role, &m.Kind, &m.SenderBotID, &m.RunID, &m.Content, &m.CreatedAt)
	return m, err == nil
}

// promoteSayBeat turns a say note or message into the run's visible answer:
// the stored kind becomes ” and a message event carries the change.
func (s *Store) promoteSayBeat(m Message) (Message, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE messages SET kind='' WHERE id=?`, m.ID); err != nil {
		return Message{}, err
	}
	m.Kind = ""
	b, err := json.Marshal(m)
	if err != nil {
		return Message{}, err
	}
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, m.ConversationID, "message", string(b), now()); err != nil {
		return Message{}, err
	}
	return m, tx.Commit()
}

// answeredEngine stands in for the model when a resumed run already holds its
// recorded answer: the run is finished from that record.
type answeredEngine struct{}

func (answeredEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	return runtime.Result{TurnEnded: true}, nil
}

// forceSayFor reports whether a run's model requests require a tool call, so
// that a model which tends to write its reply as plain text delivers it
// through say. Measured on Haiku 5.5, whose say adherence was 81% on tool_choice
// auto and 100% when required. It stays off where a plain-text ending is a
// legitimate outcome (a scheduled run that cannot produce its result), where the
// extra step changes how members talk (group chats, not yet measured), and for
// models with 95% or better adherence.
func forceSayFor(c Conversation, r Run, model string) bool {
	if c.Kind != "dm" || !c.UserVisible || r.Kind == runKindSchedule || r.scheduleTask != nil || r.Kind == runKindTriage {
		return false
	}
	return strings.Contains(strings.ToLower(model), "haiku")
}
