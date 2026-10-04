package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// DisplayCard is presentation data, never an instruction, a sent email, or an
// authorization to perform an external action. The text remains in messages;
// structured fields are stored in the existing per-message metadata column.
type DisplayCard struct {
	Type       string            `json:"type"`
	Mail       *MailPresentation `json:"mail,omitempty"`
	Title      string            `json:"title,omitempty"`
	Body       string            `json:"body"`
	From       string            `json:"from,omitempty"`
	To         string            `json:"to,omitempty"`
	Subject    string            `json:"subject,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	ReceivedAt string            `json:"received_at,omitempty"`
	Source     string            `json:"source,omitempty"`
}

func (c *DisplayCard) normalize() error {
	c.Type = strings.TrimSpace(c.Type)
	c.Title = strings.TrimSpace(c.Title)
	c.Body = strings.TrimSpace(c.Body)
	c.From = strings.TrimSpace(c.From)
	c.To = strings.TrimSpace(c.To)
	c.Subject = strings.TrimSpace(c.Subject)
	c.Summary = strings.TrimSpace(c.Summary)
	c.ReceivedAt = strings.TrimSpace(c.ReceivedAt)
	c.Source = strings.TrimSpace(c.Source)
	if c.Type == "mail_list" {
		if utf8.RuneCountInString(c.Title) > 512 {
			return errors.New("mail title exceeds 512 characters")
		}
		return validateMailPresentation(c.Mail)
	}
	if c.Type != "text" && c.Type != "mail" {
		return errors.New("type must be text or mail")
	}
	if c.Body == "" || utf8.RuneCountInString(c.Body) > 32000 {
		return errors.New("body is required and must be at most 32000 characters")
	}
	for _, value := range []string{c.Title, c.From, c.To, c.Subject, c.ReceivedAt, c.Source} {
		if utf8.RuneCountInString(value) > 512 {
			return errors.New("display card metadata exceeds 512 characters")
		}
	}
	if utf8.RuneCountInString(c.Summary) > 2000 {
		return errors.New("summary exceeds 2000 characters")
	}
	if c.Type == "mail" && (c.From == "" || c.Subject == "" || c.Source == "") {
		return errors.New("mail requires from, subject and source")
	}
	return nil
}

func (s *Server) displayTools(c Conversation, r Run) []Tool {
	return append(s.mailPresentationTools(c, r), Tool{Name: "display_content", Description: "Present a document, report or substantial text to the human in a structured text card. This tool does not verify its content or perform an external action. Use display_emails with stored mail_sources for actual mail presentation, and open_email to select a presented message. Preserve source text and do not fabricate citations. Ordinary short replies should remain normal chat messages.", Parameters: objectSchema(map[string]any{
		"type":        map[string]any{"type": "string", "enum": []string{"text"}},
		"title":       map[string]any{"type": "string"},
		"body":        map[string]any{"type": "string"},
		"from":        map[string]any{"type": "string"},
		"to":          map[string]any{"type": "string"},
		"subject":     map[string]any{"type": "string"},
		"summary":     map[string]any{"type": "string"},
		"received_at": map[string]any{"type": "string"},
		"source":      map[string]any{"type": "string", "description": "Where this content came from; use a real source, not a fabricated citation"},
	}, []string{"type", "body"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var card DisplayCard
		if err := json.Unmarshal(raw, &card); err != nil {
			return "", err
		}
		if strings.TrimSpace(card.Type) != "text" {
			return "", errors.New("display_content supports text only; use display_emails with stored mail_sources")
		}
		if err := card.normalize(); err != nil {
			return "", err
		}
		message, err := s.store.AddDisplayCard(ctx, c.ID, r, card)
		if err != nil {
			return "", err
		}
		return "Displayed card " + message.ID + " to the user. It did not send an email or perform any other action.", nil
	}})
}

// Message and replay event commit together: a failed publication cannot leave
// a card visible only after a hard refresh.
func (s *Store) AddDisplayCard(ctx context.Context, conv string, run Run, card DisplayCard) (Message, error) {
	if err := ctx.Err(); err != nil {
		return Message{}, err
	}
	if err := card.normalize(); err != nil {
		return Message{}, err
	}
	if card.Type == "mail_list" {
		for i, email := range card.Mail.Emails {
			detail, err := s.resolvePresentedEmail(ctx, conv, run.BotID, email.Source)
			if err != nil {
				return Message{}, err
			}
			canonical := detail.Email
			canonical.Summary = email.Summary
			canonical.Tag = email.Tag
			canonical.Priority = email.Priority
			card.Mail.Emails[i] = canonical
		}
		if err := validateMailPresentation(card.Mail); err != nil {
			return Message{}, err
		}
	}
	metadata, err := json.Marshal(card)
	if err != nil {
		return Message{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id=? AND conversation_id=? AND bot_id=?`, run.ID, conv, run.BotID).Scan(&status); err != nil {
		return Message{}, err
	}
	if status != "running" {
		return Message{}, errors.New("run is no longer active")
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return Message{}, err
	}
	content := card.Title + "\n" + card.Body
	if card.Type == "mail_list" {
		content = card.Title
		for _, email := range card.Mail.Emails {
			content += "\n邮件 · " + email.Subject + " · " + email.From + " · " + email.Summary
		}
	} else if card.Type == "mail" {
		content = "邮件 · " + card.Subject + "\n发件人: " + card.From
		if card.To != "" {
			content += "\n收件人: " + card.To
		}
		content += "\n正文:\n" + card.Body
		if card.Summary != "" {
			content += "\nBot 解读: " + card.Summary
		}
	} else if card.Title == "" {
		content = "文本\n" + card.Body
	}
	message := Message{ID: uuid.NewString(), ConversationID: conv, Seq: seq, Role: "assistant", Kind: "ui_card", SenderBotID: run.BotID, RunID: run.ID, Content: content, Card: &card, CreatedAt: now()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, message.ID, conv, seq, message.Role, message.Kind, run.BotID, run.ID, content, string(metadata), message.CreatedAt); err != nil {
		return Message{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, message.CreatedAt, conv); err != nil {
		return Message{}, err
	}
	if err = insertEventTx(tx, conv, "message", message, message.CreatedAt); err != nil {
		return Message{}, err
	}
	if err = tx.Commit(); err != nil {
		return Message{}, err
	}
	return message, nil
}
