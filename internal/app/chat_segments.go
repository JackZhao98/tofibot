package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/JackZhao98/tofibot/internal/runtime"
)

const maxChatSegments = 3

const chatSegmentCadence = 420 * time.Millisecond

func (s *Store) paceChatSegment(ctx context.Context, runID string) error {
	var sentAt string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM messages WHERE run_id=? AND kind='segment' AND client_message_id LIKE 'chat-segment:%' ORDER BY seq DESC LIMIT 1`, runID).Scan(&sentAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	previous, err := time.Parse(time.RFC3339Nano, sentAt)
	if err != nil {
		return err
	}
	remaining := time.Until(previous.Add(chatSegmentCadence))
	if remaining <= 0 {
		return nil
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// PublishChatSegment delivers one independent conversational beat while a run
// continues. The provider call ID makes recovery/retries safe, and the steering
// check shares the same transaction as publication so an interjection wins.
func (s *Store) PublishChatSegment(ctx context.Context, run Run, callID, content string) (Message, error) {
	return s.publishChatMessage(ctx, run, callID, content, chatPurposeAnswer)
}

// Purposes a Bot can label a mid-run message with. Anything but "status" is an
// answer: an unknown or missing value must never hide a message.
const (
	chatPurposeAnswer = "answer"
	chatPurposeStatus = "status"
	maxStatusMessages = 20
)

func normalizeChatPurpose(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), chatPurposeStatus) {
		return chatPurposeStatus
	}
	return chatPurposeAnswer
}

// publishChatMessage stores an answer as kind "segment" (always visible) and a
// status note as kind "progress" (folded under the final answer once done).
func (s *Store) publishChatMessage(ctx context.Context, run Run, callID, content, purpose string) (Message, error) {
	kind, limit := "segment", maxChatSegments
	if purpose == chatPurposeStatus {
		kind, limit = "progress", maxStatusMessages
	}
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > 1200 {
		return Message{}, errors.New("message must contain 1 to 1200 characters")
	}
	if callID == "" {
		return Message{}, errors.New("tool call identity is required")
	}
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	clientID := "chat-segment:" + run.ID + ":" + callID
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
	err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, run.ID, run.ConversationID, run.BotID).Scan(&status)
	if err != nil || status != "running" {
		return Message{}, errors.New("run is no longer active")
	}
	var successors, count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_steering WHERE old_run_id=?`, run.ID).Scan(&successors); err != nil {
		return Message{}, err
	}
	if successors > 0 {
		return Message{}, errors.New("run has been interrupted by a newer message")
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE run_id=? AND kind=? AND client_message_id LIKE 'chat-segment:%'`, run.ID, kind).Scan(&count); err != nil {
		return Message{}, err
	}
	if count >= limit {
		return Message{}, errors.New("chat segment limit reached; finish the reply")
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, nextMessageSeqSQL, run.ConversationID, run.ConversationID, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	m := Message{ID: uuid.NewString(), ConversationID: run.ConversationID, Seq: seq, Role: "assistant", Kind: kind, SenderBotID: run.BotID, RunID: run.ID, Content: content, CreatedAt: now()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at,client_message_id) VALUES(?,?,?,?,?,?,?,?,?,?)`, m.ID, m.ConversationID, m.Seq, m.Role, m.Kind, m.SenderBotID, m.RunID, m.Content, m.CreatedAt, clientID); err != nil {
		return Message{}, err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Message{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, run.ConversationID, "message", string(b), m.CreatedAt); err != nil {
		return Message{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, m.CreatedAt, run.ConversationID); err != nil {
		return Message{}, err
	}
	// A prior text delta may have reserved this run's draft sequence. Its
	// eventual final answer belongs after the newly delivered message.
	if _, err = tx.ExecContext(ctx, `UPDATE stream_drafts SET seq=0 WHERE run_id=? AND status=?`, run.ID, streamDraftActive); err != nil {
		return Message{}, err
	}
	return m, tx.Commit()
}

func (s *Server) chatSegmentTool(_ Conversation, r Run) Tool {
	return Tool{Name: "send_chat_message", Description: "Send one concise, self-contained message to the user while you continue this run. Set purpose to label it. purpose=answer: it answers or confirms something for the user and must stay visible, e.g. \"Done, the report now runs daily at 2:30.\" (at most three per run; leave the remaining conclusion for your final answer). purpose=status: a transient note on what you are doing, folded away when the run finishes, e.g. \"Checking the page now.\" Do not use for private reasoning, partial code, or filler. A newer user message interrupts further delivery.", Parameters: objectSchema(map[string]any{"content": map[string]any{"type": "string", "description": "One complete conversational message, at most 1200 characters"}, "purpose": map[string]any{"type": "string", "enum": []string{chatPurposeAnswer, chatPurposeStatus}, "description": "answer: answers or confirms something to the user, stays visible. status: transient note about what you are doing."}}, []string{"content", "purpose"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			Content string `json:"content"`
			Purpose string `json:"purpose"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return "", errors.New("invalid message")
		}
		purpose := normalizeChatPurpose(in.Purpose)
		if purpose == chatPurposeAnswer {
			if err := s.store.paceChatSegment(ctx, r.ID); err != nil {
				return "", err
			}
		}
		m, err := s.store.publishChatMessage(ctx, r, runtime.ToolCallID(ctx), in.Content, purpose)
		if err != nil {
			return "", err
		}
		return "Delivered message " + m.ID, nil
	}}
}
