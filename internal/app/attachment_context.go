package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type attachmentQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
}

func attachmentMetadata(q attachmentQuerier, id string) ([]Attachment, error) {
	rows, err := q.Query(`SELECT a.id,a.conversation_id,a.name,a.mime,a.size,a.created_at FROM attachments a JOIN attachment_messages am ON am.attachment_id=a.id WHERE am.message_id=? ORDER BY a.created_at,a.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.ConversationID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.MessageID = id
		out = append(out, a)
	}
	return out, rows.Err()
}

// attachmentsForMessages loads the attachments of a page of messages in one
// query and returns them keyed by message id.
func attachmentsForMessages(q attachmentQuerier, ids []string) (map[string][]Attachment, error) {
	out := map[string][]Attachment{}
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.Query(`SELECT am.message_id,a.id,a.conversation_id,a.name,a.mime,a.size,a.created_at FROM attachment_messages am JOIN attachments a ON a.id=am.attachment_id WHERE am.message_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) ORDER BY a.created_at,a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.MessageID, &a.ID, &a.ConversationID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		out[a.MessageID] = append(out[a.MessageID], a)
	}
	return out, rows.Err()
}

func (s *Store) attachmentImage(id, conv string) (string, error) {
	a, path, err := s.Attachment(id)
	if err != nil {
		return "", err
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

// read_attachment reads any attachment the conversation can see: text comes
// back as text, an image is shown to the model, and any other file is placed
// on the computer so the Bot opens it there with its own tools.
func (s *Server) attachmentTools(c Conversation, r Run) []Tool {
	var mu sync.Mutex
	views := 0
	return []Tool{{Name: "read_attachment", Description: "Read an attachment of this conversation by its attachment_id. Text comes back as text (use offset to continue a long one), an image is shown to you, and any other file (PDF, Office document, archive) is placed on your computer and its computer_path returned, to open there with your tools.", Parameters: objectSchema(map[string]any{
		"attachment_id": map[string]any{"type": "string"},
		"offset":        map[string]any{"type": "integer", "minimum": 0, "description": "Optional byte offset to continue a long text"},
	}, []string{"attachment_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var x struct {
			ID     string `json:"attachment_id"`
			Offset int64  `json:"offset"`
		}
		if json.Unmarshal(raw, &x) != nil || x.ID == "" || x.Offset < 0 {
			return "", errors.New("attachment_id required")
		}
		a, path, err := s.store.Attachment(x.ID)
		if err != nil {
			return "", err
		}
		if ok, e := s.store.attachmentAvailableInConversation(x.ID, c.ID); e != nil || !ok {
			return "", errors.New("attachment belongs to another conversation")
		}
		head, err := s.store.attachmentHead(ctx, path, 512)
		if err != nil {
			return "", err
		}
		if isTextAttachment(a) || strings.HasPrefix(http.DetectContentType(head), "text/") {
			text, next, err := s.store.readAttachmentTextAt(path, x.Offset)
			if err != nil {
				return "", err
			}
			m := map[string]any{"name": a.Name, "content": text}
			if next > 0 {
				m["next_offset"] = next
			}
			out, _ := json.Marshal(m)
			return string(out), nil
		}
		if _, format, e := image.DecodeConfig(bytes.NewReader(head)); e == nil && (format == "png" || format == "jpeg" || format == "gif" || format == "webp") || isWebP(head) {
			mu.Lock()
			defer mu.Unlock()
			if views >= viewsPerRunMax {
				return "", viewFileError("at most %d images can be viewed per run", viewsPerRunMax)
			}
			if a.Size > viewSourceBytesMax {
				return "", viewFileError("%s is %d bytes, too large to view; read it on the computer and view a smaller copy", a.Name, a.Size)
			}
			data, err := s.store.attachmentHead(ctx, path, viewSourceBytesMax)
			if err != nil {
				return "", err
			}
			out, err := viewImageResult(a.Name, data)
			if err != nil {
				return "", err
			}
			views++
			var m map[string]any
			if json.Unmarshal([]byte(out), &m) == nil {
				m["attachment_id"], m["name"] = a.ID, a.Name
				if b, e := json.Marshal(m); e == nil {
					out = string(b)
				}
			}
			return out, nil
		}
		if s.microVM == nil {
			return "", fmt.Errorf("%s is a %s file; without a computer only text and images can be read", a.Name, a.MIME)
		}
		if a.Size > guestImportMaxFile {
			return "", fmt.Errorf("%s is %d bytes; files over %d bytes cannot be placed on the computer", a.Name, a.Size, guestImportMaxFile)
		}
		dst := guestUploadsRoot + "/" + c.ID + "/attachments/" + guestInputName(a.ID[:8]+"-"+a.Name)
		if _, err := s.pushGuestInput(ctx, r, a, dst); err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"name": a.Name, "mime": a.MIME, "size": a.Size, "computer_path": dst})
		return string(out), nil
	}}}
}

func isTextAttachment(a Attachment) bool {
	return strings.HasPrefix(a.MIME, "text/") || a.MIME == "application/json"
}

func isWebP(b []byte) bool {
	return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP"
}

// attachmentHead reads at most limit bytes of a stored attachment.
func (s *Store) attachmentHead(ctx context.Context, path string, limit int64) ([]byte, error) {
	readCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(readCtx, path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, limit))
}

// readAttachmentTextAt returns up to maxAttachmentText bytes of text from
// offset, cut at a rune boundary, and the offset to continue from (0 at end).
func (s *Store) readAttachmentTextAt(path string, offset int64) (string, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(ctx, path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	if _, err = io.CopyN(io.Discard, f, offset); err != nil && !errors.Is(err, io.EOF) {
		return "", 0, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxAttachmentText+1))
	if err != nil {
		return "", 0, err
	}
	if len(b) <= maxAttachmentText {
		return string(b), 0, nil
	}
	b = b[:maxAttachmentText]
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return string(b), offset + int64(len(b)), nil
}

func (s *Server) addAttachmentContext(c Conversation, m Message, pm *runtime.Message, imagesLeft *int, imageBudget *int64, guestPaths map[string]string) {
	attachments, err := s.store.AttachmentsForMessage(m.ID)
	if err != nil || len(attachments) == 0 {
		return
	}
	var note strings.Builder
	for _, a := range attachments {
		fmt.Fprintf(&note, "\nAttachment: %s (attachment_id=%s, type=%s, bytes=%d).", a.Name, a.ID, a.MIME, a.Size)
		if p := guestPaths[a.ID]; p != "" {
			fmt.Fprintf(&note, " On the computer at %s.", p)
		}
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
