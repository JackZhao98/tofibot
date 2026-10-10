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
	return ensureColumn(s.db, "stream_drafts", "seq", `ALTER TABLE stream_drafts ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`)
}

const nextMessageSeqSQL = `SELECT COALESCE(MAX(seq),0)+1 FROM (
	SELECT seq FROM messages WHERE conversation_id=?
	UNION ALL SELECT seq FROM stream_drafts WHERE conversation_id=? AND status=? AND seq>0
)`

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
	payload := map[string]any{"conversation_id": d.ConversationID, "run_id": d.RunID, "bot_id": d.BotID, "message_id": d.MessageID, "seq": d.Seq, "created_at": d.CreatedAt, "text": text, "content": d.Content, "revision": d.Revision}
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

// An unlabeled mid-run assistant turn is stored as kind "segment": a visible
// message that is never folded away. Only a turn the Bot explicitly labels
// purpose=status (send_chat_message) is stored as foldable kind "progress".
// Legacy rows keep kind "progress" and keep folding.
func (s *Store) publishAssistantTurn(ctx context.Context, runID string, turnIndex int, content string, terminal bool) (Message, bool, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if turnIndex < 1 || turnIndex > maxAssistantTurns {
		return Message{}, false, errors.New("assistant turn index out of range")
	}
	if strings.TrimSpace(content) == "" {
		return Message{}, false, errors.New("assistant turn content is empty")
	}
	if len([]rune(content)) > maxStreamDraftRunes {
		return Message{}, false, errors.New("assistant turn content exceeds maximum size")
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
	rotateDraft := err == nil && draft.Status == streamDraftActive
	if !rotateDraft {
		if !terminal {
			return Message{}, false, nil
		}
		draft = StreamDraft{MessageID: uuid.NewString()}
	}

	// A completed publication can be retried after a transient caller failure.
	// Return the original immutable message and leave the new active draft
	// untouched. A different payload for the same turn is a protocol error.
	var priorMessageID string
	err = tx.QueryRow(`SELECT message_id FROM stream_assistant_turns WHERE run_id=? AND turn_index=?`, runID, turnIndex).
		Scan(&priorMessageID)
	if err == nil {
		var priorContent string
		if err = tx.QueryRow(`SELECT content FROM messages WHERE id=?`, priorMessageID).Scan(&priorContent); err != nil {
			return Message{}, false, err
		}
		if priorContent != content {
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

	seq := draft.Seq
	if seq == 0 {
		if err = tx.QueryRow(nextMessageSeqSQL, runConversationID, runConversationID, streamDraftActive).Scan(&seq); err != nil {
			return Message{}, false, err
		}
	}
	t := now()
	createdAt := t
	if draft.Seq > 0 && draft.CreatedAt != "" {
		createdAt = draft.CreatedAt
	}
	m := Message{ID: draft.MessageID, ConversationID: runConversationID, Seq: seq, Role: "assistant", Kind: "segment", SenderBotID: runBotID, RunID: runID, Content: content, CreatedAt: createdAt}
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
	if _, err = tx.Exec(`UPDATE conversations SET updated_at=? WHERE id=?`, t, runConversationID); err != nil {
		return Message{}, false, err
	}
	// Do not reserve the next draft sequence until its first non-empty delta;
	// an empty follow-up draft must not jump ahead of user messages.
	if rotateDraft {
		if _, err = tx.Exec(`UPDATE stream_drafts SET message_id=?,seq=0,content='',status=?,revision=0,created_at=?,updated_at=? WHERE run_id=? AND status=?`, uuid.NewString(), streamDraftActive, t, t, runID, streamDraftActive); err != nil {
			return Message{}, false, err
		}
	}
	if _, err = tx.Exec(`INSERT INTO stream_assistant_turns(run_id,turn_index,message_id,created_at) VALUES(?,?,?,?)`, runID, turnIndex, m.ID, t); err != nil {
		return Message{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return Message{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, false, err
	}
	return m, true, nil
}

func (s *Store) CancelStream(runID string) error {
	_, err := s.db.Exec(`UPDATE stream_drafts SET status=?,updated_at=? WHERE run_id=? AND status=?`, streamDraftCancelled, now(), runID, streamDraftActive)
	return err
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
	if _, err := s.BeginStream(run); err != nil {
		return nil, nil, nil, err
	}
	var mu sync.Mutex
	var buffer []rune
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
	return func(text string) {
			mu.Lock()
			defer mu.Unlock()
			if failed || ctx.Err() != nil {
				return
			}
			buffer = append(buffer, []rune(text)...)
			if persisted.IsZero() || time.Since(persisted) >= streamFlushInterval || len(buffer) >= maxStreamDeltaRunes {
				flushLocked()
			}
		}, func() { mu.Lock(); defer mu.Unlock(); flushLocked() }, func() {
			mu.Lock()
			defer mu.Unlock()
			buffer = nil
			if failed || ctx.Err() != nil {
				return
			}
			if err := s.ResetStreamDraft(ctx, run.ID); err != nil {
				failed = true
				if onError != nil {
					onError(err)
				}
			}
		}, nil
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
