package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type attachmentQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func attachmentMetadata(q attachmentQuerier, id string) ([]Attachment, error) {
	rows, err := q.Query(`SELECT a.id,a.conversation_id,a.name,a.mime,a.size,a.created_at,EXISTS(SELECT 1 FROM unavailable_guest_attachments u WHERE u.attachment_id=a.id) FROM attachments a JOIN attachment_messages am ON am.attachment_id=a.id WHERE am.message_id=? ORDER BY a.created_at,a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt, &a.Unavailable); err != nil {
			return nil, err
		}
		a.MessageID = id
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) attachmentImage(id, conv string) (string, error) {
	a, path, err := s.Attachment(id)
	if err != nil {
		return "", err
	}
	if a.Unavailable {
		return "", errors.New("cloud computer deleted; attachment contents unavailable")
	}
	if ok, e := s.attachmentAvailableInConversation(id, conv); e != nil || !ok {
		return "", errors.New("attachment belongs to another conversation")
	}
	if a.MIME != "image/png" && a.MIME != "image/jpeg" && a.MIME != "image/gif" {
		return "", errors.New("image format not supported for vision")
	}
	if a.Size > 8<<20 {
		return "", errors.New("image exceeds model input budget (8 MB)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(ctx, path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	bytes, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return "", err
	}
	if len(bytes) > 8<<20 {
		return "", errors.New("image exceeds model input budget")
	}
	return "data:" + a.MIME + ";base64," + base64.StdEncoding.EncodeToString(bytes), nil
}

func (s *Store) attachmentAvailableInConversation(id, conv string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM attachments a WHERE a.id=? AND (a.conversation_id=? OR EXISTS(
	 SELECT 1 FROM attachment_messages am JOIN messages m ON m.id=am.message_id WHERE am.attachment_id=a.id AND m.conversation_id=?))`, id, conv, conv).Scan(&n)
	return n > 0, err
}

func (s *Server) attachmentTools(c Conversation) []Tool {
	return []Tool{{Name: "read_attachment", Description: "Read a text attachment from this conversation by its attachment_id. Binary documents remain downloadable but text extraction may be unavailable.", Parameters: objectSchema(map[string]any{"attachment_id": map[string]any{"type": "string"}}, []string{"attachment_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var x struct {
			ID string `json:"attachment_id"`
		}
		if json.Unmarshal(raw, &x) != nil || x.ID == "" {
			return "", errors.New("attachment_id required")
		}
		text, a, err := s.store.ReadAttachmentText(x.ID, c.ID)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"name": a.Name, "content": text})
		return string(out), nil
	}}}
}

func (s *Server) addAttachmentContext(c Conversation, m Message, pm *runtime.Message, imagesLeft *int, imageBudget *int64) {
	attachments, err := s.store.AttachmentsForMessage(m.ID)
	if err != nil || len(attachments) == 0 {
		return
	}
	var note strings.Builder
	for _, a := range attachments {
		fmt.Fprintf(&note, "\nAttachment: %s (attachment_id=%s, type=%s, bytes=%d).", a.Name, a.ID, a.MIME, a.Size)
		if strings.HasPrefix(a.MIME, "image/") && *imagesLeft > 0 && a.Size <= *imageBudget {
			image, e := s.store.attachmentImage(a.ID, c.ID)
			if e == nil {
				pm.ImageURLs = append(pm.ImageURLs, image)
				*imagesLeft--
				*imageBudget -= a.Size
			} else {
				fmt.Fprintf(&note, " Visual input unavailable: %s.", e.Error())
			}
		}
	}
	pm.Content += note.String()
}
