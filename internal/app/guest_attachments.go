package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type guestBlobStorage interface {
	PutBlob(context.Context, string, []byte) error
	GetBlob(context.Context, string) ([]byte, error)
	DeleteBlob(context.Context, string) error
}

const guestAttachmentPrefix = "vm:"

var guestFileSlots = make(chan struct{}, 4)

type attachmentMemoryFile struct{ *bytes.Reader }

func (attachmentMemoryFile) Close() error { return nil }

func guestAttachmentID(path string) (string, error) {
	id := strings.TrimPrefix(path, guestAttachmentPrefix)
	parsed, err := uuid.Parse(id)
	if !strings.HasPrefix(path, guestAttachmentPrefix) || err != nil || parsed.String() != id {
		return "", errors.New("invalid guest attachment identity")
	}
	return id, nil
}

func (s *Store) addGuestAttachment(ctx context.Context, conv, name, declared string, src io.Reader) (Attachment, error) {
	if s.guestBlobs == nil {
		return Attachment{}, errors.New("account cloud file storage is not ready")
	}
	if src == nil {
		return Attachment{}, errors.New("attachment content required")
	}
	if _, err := s.GetConversation(conv); err != nil {
		return Attachment{}, err
	}
	select {
	case guestFileSlots <- struct{}{}:
		defer func() { <-guestFileSlots }()
	case <-ctx.Done():
		return Attachment{}, ctx.Err()
	}
	data, err := io.ReadAll(io.LimitReader(src, maxAttachmentSize+1))
	if err != nil {
		return Attachment{}, err
	}
	if int64(len(data)) > maxAttachmentSize {
		return Attachment{}, errors.New("attachment exceeds upload limit")
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		name = "attachment"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	actual := http.DetectContentType(data)
	if strings.HasPrefix(actual, "text/") || strings.HasPrefix(actual, "image/") || actual == "application/pdf" || actual == "application/json" {
		declared = actual
	} else if mt, _, err := mime.ParseMediaType(declared); err == nil && mt != "" {
		declared = mt
	} else {
		declared = actual
	}
	if strings.HasPrefix(declared, "image/") {
		_, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || (format != "png" && format != "jpeg" && format != "gif") {
			declared = "application/octet-stream"
		}
	}
	id := uuid.NewString()
	if err = s.guestBlobs.PutBlob(ctx, id, data); err != nil {
		// A lost response may still have committed the guest alias. Keep a
		// durable cleanup intent; do not silently consume quota after retries.
		s.db.Exec(`INSERT OR IGNORE INTO deleted_attachment_files VALUES(?)`, guestAttachmentPrefix+id)
		return Attachment{}, err
	}
	a := Attachment{ID: id, ConversationID: conv, Name: name, MIME: declared, Size: int64(len(data)), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, id, conv, name, declared, a.Size, guestAttachmentPrefix+id, a.CreatedAt)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if s.guestBlobs.DeleteBlob(cleanup, id) != nil {
			s.db.Exec(`INSERT OR IGNORE INTO deleted_attachment_files VALUES(?)`, guestAttachmentPrefix+id)
		}
		return Attachment{}, err
	}
	return a, nil
}

func (s *Store) openAttachment(ctx context.Context, path string) (io.ReadCloser, error) {
	if strings.HasPrefix(path, guestAttachmentPrefix) {
		id, err := guestAttachmentID(path)
		if err != nil {
			return nil, err
		}
		if s.guestBlobs == nil {
			return nil, errors.New("guest attachment storage unavailable")
		}
		data, err := s.guestBlobs.GetBlob(ctx, id)
		if err != nil {
			return nil, err
		}
		return attachmentMemoryFile{bytes.NewReader(data)}, nil
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(filepath.Base(path))
}

func (s *Store) removeAttachmentFile(ctx context.Context, path string) error {
	if strings.HasPrefix(path, guestAttachmentPrefix) {
		id, err := guestAttachmentID(path)
		if err != nil {
			return err
		}
		if s.guestBlobs == nil {
			return errors.New("guest attachment storage unavailable")
		}
		return s.guestBlobs.DeleteBlob(ctx, id)
	}
	return os.Remove(path)
}
