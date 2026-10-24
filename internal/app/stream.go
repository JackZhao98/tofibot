package app

// Durable assistant streaming. Only completed assistant turns enter messages;
// unfinished drafts remain outside the conversation and model context.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/JackZhao98/tofibot/internal/agent"
	"github.com/google/uuid"
)

const (
	streamDraftActive    = "active"
	streamDraftDone      = "done"
	streamDraftCancelled = "cancelled"
	maxStreamDraftRunes  = 128000
	maxStreamDeltaRunes  = 4096
	maxAssistantTurns    = agent.MaxAssistantTurnIndex
	streamFlushInterval  = 50 * time.Millisecond
)

type StreamDraft struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id"`
	BotID          string `json:"bot_id"`
	MessageID      string `json:"message_id"`
	Seq            int64  `json:"seq"`
	Content        string `json:"content"`
	Status         string `json:"status"`
	Revision       int64  `json:"revision"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

// EnsureStreamSchema is safe to call during application startup. It is kept
// separate from the hot delta path so streaming never performs migrations.
func (s *Store) EnsureStreamSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS stream_drafts(
	 run_id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, bot_id TEXT NOT NULL,
	 message_id TEXT NOT NULL UNIQUE, content TEXT NOT NULL DEFAULT '',
	 seq INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0,
	 created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
	 FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS stream_assistant_turns(
	 run_id TEXT NOT NULL, turn_index INTEGER NOT NULL,
	 message_id TEXT NOT NULL UNIQUE,
	 created_at TEXT NOT NULL,
	 PRIMARY KEY(run_id, turn_index),
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE)`)
	if err != nil {
		return err
	}
	if err = ensureColumn(s.db, "stream_drafts", "seq", `ALTER TABLE stream_drafts ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS stream_drafts_conversation ON stream_drafts(conversation_id,status,seq)`); err != nil {
		return err
	}
	// Delta events of drafts that are no longer live were never pruned before:
	// the message or the draft row carries their text.
	_, err = s.db.Exec(`DELETE FROM events WHERE type='delta' AND json_extract(data,'$.message_id') NOT IN (SELECT message_id FROM stream_drafts WHERE status='active')`)
	return err
}

// Two index-backed MAX probes; a UNION ALL over both tables materialised every
// message of the conversation on each insert.
const nextMessageSeqSQL = `SELECT MAX(
	COALESCE((SELECT MAX(seq) FROM messages WHERE conversation_id=?),0),
	COALESCE((SELECT MAX(seq) FROM stream_drafts WHERE conversation_id=? AND status=? AND seq>0),0))+1`

// BeginStream creates the stable assistant message identity used by all delta
// events. It is idempotent, which makes execution recovery safe.
func (s *Store) BeginStream(run Run) (StreamDraft, error) {
	var d StreamDraft
	err := s.db.QueryRow(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at FROM stream_drafts WHERE run_id=?`, run.ID).
		Scan(&d.RunID, &d.ConversationID, &d.BotID, &d.MessageID, &d.Seq, &d.Content, &d.Status, &d.Revision, &d.CreatedAt, &d.UpdatedAt)
	if err == nil {
		if d.Status != streamDraftActive {
			return d, errors.New("stream draft is no longer active")
		}
		return d, nil
	}
	if err != sql.ErrNoRows {
		return StreamDraft{}, err
	}
	nowAt := now()
	d = StreamDraft{RunID: run.ID, ConversationID: run.ConversationID, BotID: run.BotID, MessageID: uuid.NewString(), Status: streamDraftActive, CreatedAt: nowAt, UpdatedAt: nowAt}
	_, err = s.db.Exec(`INSERT INTO stream_drafts(run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, d.RunID, d.ConversationID, d.BotID, d.MessageID, 0, "", d.Status, 0, nowAt, nowAt)
	return d, err
}

// deltaResyncEvery is how often a delta event also carries the whole draft.
// Every other delta carries only its new text, so the bytes stored and sent per
// reply grow linearly with its length instead of quadratically. The first
// revision and every resync point let a client that missed events recover.
//
// Resync points are dense while the draft is short and cheap to repeat, then
// sparse: past deltaDenseUntil only every deltaSparseEvery-th revision repeats
// the draft, which keeps the total close to linear in the reply length.
const (
	deltaResyncEvery = 16
	deltaDenseUntil  = 64
	deltaSparseEvery = 128
)

func deltaCarriesContent(revision int64) bool {
	if revision <= 1 {
		return true
	}
	if revision <= deltaDenseUntil {
		return revision%deltaResyncEvery == 0
	}
	return revision%deltaSparseEvery == 0
}

// pruneDeltaEvents is true in production. Tests that read the delta events of
// a finished run to prove how it streamed turn it off.
var pruneDeltaEvents = true

// pruneDeltaEventsTx drops the delta events of one draft identity. Once the
// draft is published as a message, reset or discarded, those rows are dead
// weight: the message event or the draft row carries the text.
func pruneDeltaEventsTx(tx *sql.Tx, conv, messageID string) error {
	if messageID == "" || !pruneDeltaEvents {
		return nil
	}
	_, err := tx.Exec(`DELETE FROM events WHERE conversation_id=? AND type='delta' AND json_extract(data,'$.message_id')=?`, conv, messageID)
	return err
}

// AppendStreamDelta atomically persists a delta and emits the UI-compatible
// {run_id,message_id,text,revision} event. A cancelled/done run is a no-op.
func (s *Store) AppendStreamDelta(ctx context.Context, runID, text string) (StreamDraft, bool, error) {
	if err := ctx.Err(); err != nil {
		return StreamDraft{}, false, err
	}
	text = trimRunes(text, maxStreamDeltaRunes)
	if text == "" {
		return StreamDraft{}, false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return StreamDraft{}, false, err
	}
	defer tx.Rollback()
	var d StreamDraft
	err = tx.QueryRow(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at FROM stream_drafts WHERE run_id=?`, runID).Scan(&d.RunID, &d.ConversationID, &d.BotID, &d.MessageID, &d.Seq, &d.Content, &d.Status, &d.Revision, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return StreamDraft{}, false, err
	}
	var runStatus string
	if err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&runStatus); err != nil {
		return StreamDraft{}, false, err
	}
	if d.Status != streamDraftActive || runStatus != "running" {
		return d, false, nil
	}
	remaining := maxStreamDraftRunes - len([]rune(d.Content))
	if remaining <= 0 {
		return d, false, nil
	}
	text = trimRunes(text, remaining)
	if text == "" {
		return d, false, nil
	}
	if d.Seq == 0 {
		if err = tx.QueryRow(nextMessageSeqSQL, d.ConversationID, d.ConversationID, streamDraftActive).Scan(&d.Seq); err != nil {
			return StreamDraft{}, false, err
		}
		d.CreatedAt = now()
		if _, err = tx.Exec(`UPDATE stream_drafts SET seq=?,created_at=? WHERE run_id=? AND status=? AND seq=0`, d.Seq, d.CreatedAt, runID, streamDraftActive); err != nil {
			return StreamDraft{}, false, err
		}
	}
	d.Content += text
	d.Revision++
	d.UpdatedAt = now()
	if _, err = tx.Exec(`UPDATE stream_drafts SET content=?,revision=?,updated_at=? WHERE run_id=? AND status=?`, d.Content, d.Revision, d.UpdatedAt, runID, streamDraftActive); err != nil {
		return StreamDraft{}, false, err
	}
	payload := map[string]any{"conversation_id": d.ConversationID, "run_id": d.RunID, "bot_id": d.BotID, "message_id": d.MessageID, "seq": d.Seq, "created_at": d.CreatedAt, "text": text, "revision": d.Revision}
	if deltaCarriesContent(d.Revision) {
		payload["content"] = d.Content
	}
	b, _ := json.Marshal(payload)
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, d.ConversationID, "delta", string(b), d.UpdatedAt); err != nil {
		return StreamDraft{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return StreamDraft{}, false, err
	}
	return d, true, nil
}

// PublishAssistantTurn makes one completed assistant turn durable before the
// engine starts its tools. The current draft identity becomes the immutable
// message identity for this turn; the draft row is then rotated for the next
// turn. The idempotency record and all message/event state are one transaction.
func (s *Store) PublishAssistantTurn(ctx context.Context, runID string, turnIndex int, content string) (Message, bool, error) {
	return s.publishAssistantTurn(ctx, runID, turnIndex, content, false)
}

// PublishDemotedDraft publishes a final draft that was sent back for review
// when the run ends without a reviewed answer (steered, failed, cancelled,
// budget-exhausted or suspended). It runs at the terminal transition, so it
// ignores the run context and accepts those run states; a done run already
// published its answer. The demoted turn index keeps it idempotent. An
// inactive draft keeps its row; the message gets a fresh identity.
func (s *Store) PublishDemotedDraft(runID string, turnIndex int, content string) (Message, bool, error) {
	return s.publishAssistantTurn(context.Background(), runID, turnIndex, content, true)
}

// PublishDemotedDraftStructured is PublishDemotedDraft for a run that follows
// the say protocol: only literal tags are stripped and no repeat fold applies.
func (s *Store) PublishDemotedDraftStructured(runID string, turnIndex int, content string) (Message, bool, error) {
	if turnIndex < 1 || turnIndex > maxAssistantTurns {
		return Message{}, false, errors.New("assistant turn index out of range")
	}
	content = strings.TrimSpace(stripProgressTags(content))
	if content == "" {
		return Message{}, false, errors.New("assistant turn content is empty")
	}
	return s.publishAssistantPartsOpts(context.Background(), runID, turnIndex, []textPart{{Text: content}}, publishOpts{terminal: true, adopt: true})
}

// A mid-run assistant turn is split by its <progress> tags (see progress_tag.go):
// text inside a tag is stored as foldable kind "progress", text outside as kind
// "segment", a visible message that is never folded away. Tags are not stored.
// Legacy rows keep kind "progress" and keep folding.
func (s *Store) publishAssistantTurn(ctx context.Context, runID string, turnIndex int, content string, terminal bool) (Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if turnIndex < 1 || turnIndex > maxAssistantTurns {
		return Message{}, false, errors.New("assistant turn index out of range")
	}
	if len([]rune(content)) > maxStreamDraftRunes {
		return Message{}, false, errors.New("assistant turn content exceeds maximum size")
	}
	parts := splitProgress(content)
	if len(parts) == 0 {
		return Message{}, false, errors.New("assistant turn content is empty")
	}
	return s.publishAssistantParts(ctx, runID, turnIndex, parts, terminal)
}

// PublishInternalTurn stores a completed mid-run assistant turn of a structured
// run. Its plain text is private working text: the whole turn is one folded
// note (kind "internal") that "View activity" shows, never a chat message.
// Literal <progress> tags of legacy Bots are stripped. The note never takes the
// stream draft's identity (only a say call's text streams into the draft); the
// turn index replay record works as in PublishAssistantTurn.
func (s *Store) PublishInternalTurn(ctx context.Context, runID string, turnIndex int, content string) (Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if turnIndex < 1 || turnIndex > maxAssistantTurns {
		return Message{}, false, errors.New("assistant turn index out of range")
	}
	content = strings.TrimSpace(stripProgressTags(content))
	if content == "" {
		return Message{}, false, errors.New("assistant turn content is empty")
	}
	if len([]rune(content)) > maxStreamDraftRunes {
		return Message{}, false, errors.New("assistant turn content exceeds maximum size")
	}
	return s.publishAssistantPartsOpts(ctx, runID, turnIndex, []textPart{{Text: content, Progress: true, Internal: true}}, publishOpts{})
}

// publishAssistantParts stores parts as consecutive messages. The first takes
// the draft's identity and sequence; the draft then rotates. turnIndex 0 skips
// the replay record (used for notes that precede a run's final answer).
func (s *Store) publishAssistantParts(ctx context.Context, runID string, turnIndex int, parts []textPart, terminal bool) (Message, bool, error) {
	return s.publishAssistantPartsOpts(ctx, runID, turnIndex, parts, publishOpts{terminal: terminal, folds: true, adopt: true})
}

// publishOpts tunes publishAssistantPartsOpts. terminal accepts a run that
// already left "running"; folds enables the repeat fold; adopt lets the first
// part take over the stream draft's identity (false when a say call's text is
// what the draft holds).
type publishOpts struct{ terminal, folds, adopt bool }

// publishAssistantPartsOpts is publishAssistantParts; folds=false is the say
// protocol, where a repeated sentence stays what the Bot said instead of being
// folded into a note.
func (s *Store) publishAssistantPartsOpts(ctx context.Context, runID string, turnIndex int, parts []textPart, opts publishOpts) (Message, bool, error) {
	terminal, folds := opts.terminal, opts.folds
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if len(parts) == 0 {
		return Message{}, false, errors.New("assistant turn content is empty")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, false, err
	}
	defer tx.Rollback()
	if err = ctx.Err(); err != nil {
		return Message{}, false, err
	}

	// Read run identity from the database. Callers cannot redirect a published
	// message by supplying a different conversation or bot identity.
	var runConversationID, runBotID, runStatus string
	err = tx.QueryRow(`SELECT conversation_id,bot_id,status FROM runs WHERE id=?`, runID).
		Scan(&runConversationID, &runBotID, &runStatus)
	if err == sql.ErrNoRows {
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	if runStatus != "running" && (!terminal || (runStatus != "waiting" && runStatus != "cancelled" && runStatus != "failed")) {
		return Message{}, false, nil
	}

	var draft StreamDraft
	err = tx.QueryRow(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at
		FROM stream_drafts WHERE run_id=?`, runID).
		Scan(&draft.RunID, &draft.ConversationID, &draft.BotID, &draft.MessageID, &draft.Seq, &draft.Content, &draft.Status, &draft.Revision, &draft.CreatedAt, &draft.UpdatedAt)
	if err == sql.ErrNoRows && !terminal {
		return Message{}, false, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return Message{}, false, err
	}
	rotateDraft := err == nil && draft.Status == streamDraftActive && opts.adopt
	if !rotateDraft {
		if !terminal && opts.adopt {
			return Message{}, false, nil
		}
		draft = StreamDraft{MessageID: uuid.NewString()}
	}

	// A completed publication can be retried after a transient caller failure.
	// Return the original immutable message and leave the new active draft
	// untouched. A different payload for the same turn is a protocol error.
	var priorMessageID string
	err = sql.ErrNoRows
	if turnIndex > 0 {
		err = tx.QueryRow(`SELECT message_id FROM stream_assistant_turns WHERE run_id=? AND turn_index=?`, runID, turnIndex).
			Scan(&priorMessageID)
	}
	if err == nil {
		var priorContent string
		if err = tx.QueryRow(`SELECT content FROM messages WHERE id=?`, priorMessageID).Scan(&priorContent); err != nil {
			return Message{}, false, err
		}
		if priorContent != parts[0].Text {
			return Message{}, false, errors.New("assistant turn replay conflicts with prior content")
		}
		m, scanErr := scanMsg(tx.QueryRow(`SELECT id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at FROM messages WHERE id=?`, priorMessageID))
		if scanErr != nil {
			return Message{}, false, scanErr
		}
		return m, true, nil
	}
	if err != sql.ErrNoRows {
		return Message{}, false, err
	}

	t := now()
	var first Message
	for i, part := range parts {
		kind := "segment"
		if part.Internal {
			kind = messageKindInternal
		} else if part.Progress {
			kind = "progress"
		} else if !folds {
			// Structured output keeps what was said as it was said.
		} else if _, repeated, repeatErr := repeatedSegmentTx(tx, runID, part.Text); repeatErr != nil {
			return Message{}, false, repeatErr
		} else if repeated {
			// The same run already said this; a restatement folds like a note.
			kind = "progress"
			noteFinalOrigin(tx, runID, heuristicRepeat)
		}
		id, createdAt, seq := uuid.NewString(), t, int64(0)
		if i == 0 {
			id, seq = draft.MessageID, draft.Seq
			if draft.Seq > 0 && draft.CreatedAt != "" {
				createdAt = draft.CreatedAt
			}
		}
		if seq == 0 {
			if err = tx.QueryRow(nextMessageSeqSQL, runConversationID, runConversationID, streamDraftActive).Scan(&seq); err != nil {
				return Message{}, false, err
			}
		}
		m := Message{ID: id, ConversationID: runConversationID, Seq: seq, Role: "assistant", Kind: kind, SenderBotID: runBotID, RunID: runID, Content: part.Text, CreatedAt: createdAt}
		if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, m.RunID, m.Content, m.CreatedAt); err != nil {
			return Message{}, false, err
		}
		b, marshalErr := json.Marshal(m)
		if marshalErr != nil {
			return Message{}, false, marshalErr
		}
		if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, runConversationID, "message", string(b), t); err != nil {
			return Message{}, false, err
		}
		if kind == "segment" {
			// What a holder posts reaches the member that gave the task (decision 0064).
			if err = routeHolderMessageTx(tx, runID, runConversationID, runBotID, m.ID, m.Content); err != nil {
				return Message{}, false, err
			}
		}
		if i == 0 {
			first = m
			// Free the draft's reserved sequence so the next part (and the next
			// draft) number after this message, not on top of it.
			if rotateDraft {
				if _, err = tx.Exec(`UPDATE stream_drafts SET seq=0 WHERE run_id=? AND status=?`, runID, streamDraftActive); err != nil {
					return Message{}, false, err
				}
				if err = pruneDeltaEventsTx(tx, runConversationID, draft.MessageID); err != nil {
					return Message{}, false, err
				}
			}
		}
	}
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, runConversationID); err != nil {
		return Message{}, false, err
	}
	if !opts.adopt {
		// The draft keeps its text (a say call's); its reserved sequence moves
		// behind the message just stored.
		if _, err = tx.Exec(`UPDATE stream_drafts SET seq=0 WHERE run_id=? AND status=?`, runID, streamDraftActive); err != nil {
			return Message{}, false, err
		}
	}
	// Do not reserve the next draft sequence until its first non-empty delta;
	// an empty follow-up draft must not jump ahead of user messages.
	if rotateDraft {
		if _, err = tx.Exec(`UPDATE stream_drafts SET message_id=?,seq=0,content='',status=?,revision=0,created_at=?,updated_at=? WHERE run_id=? AND status=?`, uuid.NewString(), streamDraftActive, t, t, runID, streamDraftActive); err != nil {
			return Message{}, false, err
		}
	}
	if turnIndex > 0 {
		if _, err = tx.Exec(`INSERT INTO stream_assistant_turns(run_id,turn_index,message_id,created_at) VALUES(?,?,?,?)`, runID, turnIndex, first.ID, t); err != nil {
			return Message{}, false, err
		}
	}
	if err = ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return first, true, nil
}

func (s *Store) CancelStream(runID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var conv, messageID string
	if err = tx.QueryRow(`SELECT conversation_id,message_id FROM stream_drafts WHERE run_id=? AND status=?`, runID, streamDraftActive).Scan(&conv, &messageID); err == nil {
		if err = pruneDeltaEventsTx(tx, conv, messageID); err != nil {
			return err
		}
	} else if err != sql.ErrNoRows {
		return err
	}
	if _, err = tx.Exec(`UPDATE stream_drafts SET status=?,updated_at=? WHERE run_id=? AND status=?`, streamDraftCancelled, now(), runID, streamDraftActive); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) StreamDraft(runID string) (StreamDraft, error) {
	var d StreamDraft
	err := s.db.QueryRow(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at FROM stream_drafts WHERE run_id=?`, runID).Scan(&d.RunID, &d.ConversationID, &d.BotID, &d.MessageID, &d.Seq, &d.Content, &d.Status, &d.Revision, &d.CreatedAt, &d.UpdatedAt)
	return d, err
}

// StreamDrafts returns active drafts for a conversation, allowing a fresh
// messages snapshot to restore in-progress assistant bubbles before SSE.
func (s *Store) StreamDrafts(conversationID string) ([]StreamDraft, error) {
	var rows *sql.Rows
	var err error
	rows, err = s.db.Query(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status,revision,created_at,updated_at FROM stream_drafts WHERE conversation_id=? AND status=? AND EXISTS(SELECT 1 FROM runs WHERE runs.id=stream_drafts.run_id AND runs.status='running') ORDER BY updated_at,run_id`, conversationID, streamDraftActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StreamDraft, 0)
	for rows.Next() {
		var d StreamDraft
		if err := rows.Scan(&d.RunID, &d.ConversationID, &d.BotID, &d.MessageID, &d.Seq, &d.Content, &d.Status, &d.Revision, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// StreamCallbacks adapts the store lifecycle to runtime.Request.OnDelta. The
// callback uses the run context and reports persistence failures to onError so
// the owner can cancel the engine rather than silently losing output.
func (s *Store) StreamCallbacks(ctx context.Context, run Run, onError func(error)) (func(string), func(), error) {
	onDelta, flush, _, err := s.StreamControls(ctx, run, onError)
	return onDelta, flush, err
}

// StreamControls is StreamCallbacks plus reset, which drops unpersisted text
// and discards the current draft (see ResetStreamDraft).
func (s *Store) StreamControls(ctx context.Context, run Run, onError func(error)) (func(string), func(), func(), error) {
	c, err := s.streamControls(ctx, run, onError)
	return c.Delta, c.Flush, c.Reset, err
}

// streamControlSet is the draft's write side: Delta takes model text (tags
// stripped), Raw takes text that is already decided (a say call's text), Flush
// settles buffered text, Reset discards the draft.
type streamControlSet struct {
	Delta func(string)
	Raw   func(string)
	Flush func()
	Reset func()
}

func (s *Store) streamControls(ctx context.Context, run Run, onError func(error)) (streamControlSet, error) {
	if _, err := s.BeginStream(run); err != nil {
		return streamControlSet{}, err
	}
	var mu sync.Mutex
	var buffer []rune
	// Live drafts never show <progress> tags: the same scanner that splits the
	// stored turn strips them here, holding back a tag cut by a chunk boundary.
	var tags progressScanner
	appendLocked := func(parts []textPart) {
		for _, part := range parts {
			buffer = append(buffer, []rune(part.Text)...)
		}
	}
	var persisted time.Time
	failed := false
	flushLocked := func() {
		for len(buffer) > 0 && !failed {
			count := min(len(buffer), maxStreamDeltaRunes)
			_, _, err := s.AppendStreamDelta(ctx, run.ID, string(buffer[:count]))
			if err != nil {
				failed = true
				if onError != nil {
					onError(err)
				}
				return
			}
			buffer = buffer[count:]
			persisted = time.Now()
		}
	}
	return streamControlSet{Delta: func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if failed || ctx.Err() != nil {
			return
		}
		appendLocked(tags.feed(text))
		if persisted.IsZero() || time.Since(persisted) >= streamFlushInterval || len(buffer) >= maxStreamDeltaRunes {
			flushLocked()
		}
	}, Raw: func(text string) {
		mu.Lock()
		defer mu.Unlock()
		if failed || ctx.Err() != nil {
			return
		}
		buffer = append(buffer, []rune(text)...)
		if persisted.IsZero() || time.Since(persisted) >= streamFlushInterval || len(buffer) >= maxStreamDeltaRunes {
			flushLocked()
		}
	}, Flush: func() {
		// A flush marks the end of a turn's text: a held-back "<pro" was
		// plain text after all, and the next turn starts outside any note.
		mu.Lock()
		defer mu.Unlock()
		appendLocked(tags.finish())
		flushLocked()
	}, Reset: func() {
		mu.Lock()
		defer mu.Unlock()
		buffer = nil
		tags.finish()
		if failed || ctx.Err() != nil {
			return
		}
		if err := s.ResetStreamDraft(ctx, run.ID); err != nil {
			failed = true
			if onError != nil {
				onError(err)
			}
		}
	}}, nil
}

// ResetStreamDraft discards the active draft without publishing it: the draft
// gets a fresh message identity and empty content, and a "draft_reset" event
// {conversation_id, run_id, bot_id, message_id} names the discarded bubble.
// It is used when a reviewed final answer or a retried model call supersedes
// text that was already streamed. Nothing enters messages.
func (s *Store) ResetStreamDraft(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var d StreamDraft
	err = tx.QueryRow(`SELECT run_id,conversation_id,bot_id,message_id,seq,content,status FROM stream_drafts WHERE run_id=?`, runID).
		Scan(&d.RunID, &d.ConversationID, &d.BotID, &d.MessageID, &d.Seq, &d.Content, &d.Status)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if d.Status != streamDraftActive || (d.Content == "" && d.Seq == 0) {
		return nil
	}
	t := now()
	if _, err = tx.Exec(`UPDATE stream_drafts SET message_id=?,seq=0,content='',revision=0,created_at=?,updated_at=? WHERE run_id=? AND status=?`, uuid.NewString(), t, t, runID, streamDraftActive); err != nil {
		return err
	}
	if err = pruneDeltaEventsTx(tx, d.ConversationID, d.MessageID); err != nil {
		return err
	}
	b, _ := json.Marshal(map[string]any{"conversation_id": d.ConversationID, "run_id": d.RunID, "bot_id": d.BotID, "message_id": d.MessageID})
	if _, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, d.ConversationID, "draft_reset", string(b), t); err != nil {
		return err
	}
	return tx.Commit()
}

const (
	thinkingEventInterval = 250 * time.Millisecond
	maxThinkingEventRunes = 400
)

// ThinkingCallback publishes reasoning-summary progress as ephemeral
// "thinking" events {conversation_id, run_id, bot_id, text}, where text is the
// latest summary snippet (at most 400 runes). Events are throttled to one per
// 250ms with a trailing update; each replaces the run's previous thinking row,
// so a long think keeps one durable row. They never enter the answer draft or
// messages. The returned stop cancels any pending update.
func (s *Store) ThinkingCallback(ctx context.Context, run Run) (func(string), func()) {
	var mu sync.Mutex
	var text []rune
	var last time.Time
	var timer *time.Timer
	var previous int64
	stopped := false
	emitLocked := func() {
		if stopped || ctx.Err() != nil || len(text) == 0 {
			return
		}
		last = time.Now()
		id, err := s.replaceEvent(previous, run.ConversationID, "thinking", map[string]any{"conversation_id": run.ConversationID, "run_id": run.ID, "bot_id": run.BotID, "text": string(text)})
		if err != nil {
			stopped = true // best effort: progress must never fail the run
			return
		}
		previous = id
	}
	return func(delta string) {
			mu.Lock()
			defer mu.Unlock()
			if stopped {
				return
			}
			text = append(text, []rune(delta)...)
			if len(text) > maxThinkingEventRunes {
				text = append([]rune(nil), text[len(text)-maxThinkingEventRunes:]...)
			}
			if wait := thinkingEventInterval - time.Since(last); wait <= 0 {
				emitLocked()
			} else if timer == nil {
				timer = time.AfterFunc(wait, func() {
					mu.Lock()
					defer mu.Unlock()
					timer = nil
					emitLocked()
				})
			}
		}, func() {
			mu.Lock()
			defer mu.Unlock()
			stopped = true
			if timer != nil {
				timer.Stop()
			}
		}
}

// replaceEvent inserts an event and deletes the superseded row previous (when
// nonzero) of the same conversation and type in one transaction.
func (s *Store) replaceEvent(previous int64, conv, typ string, v any) (int64, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if previous > 0 {
		if _, err = tx.Exec(`DELETE FROM events WHERE id=? AND conversation_id=? AND type=?`, previous, conv, typ); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conv, typ, string(b), now())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// PublishReasoningReplayOff records, as a "run" event carrying the run snapshot
// plus reasoning_replay:"off", that the provider rejected replayed reasoning
// and later turns of this run continue without it.
func (s *Store) PublishReasoningReplayOff(run Run) {
	current, err := s.GetRun(run.ID)
	if err != nil {
		current = run
	}
	b, err := json.Marshal(current) // Run has its own MarshalJSON, so extend the encoded object
	if err != nil {
		return
	}
	var snapshot map[string]any
	if json.Unmarshal(b, &snapshot) != nil {
		return
	}
	snapshot["reasoning_replay"] = "off"
	_, _ = s.Event(run.ConversationID, "run", snapshot)
}

// PublishRetry records a model-request backoff as a "retrying" event
// {conversation_id, run_id, bot_id, attempt, wait_ms}. It carries no upstream
// error text.
func (s *Store) PublishRetry(run Run, attempt int, wait time.Duration) {
	_, _ = s.Event(run.ConversationID, "retrying", map[string]any{"conversation_id": run.ConversationID, "run_id": run.ID, "bot_id": run.BotID, "attempt": attempt, "wait_ms": wait.Milliseconds()})
}
