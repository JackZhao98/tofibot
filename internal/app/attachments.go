package app

// Attachments are deliberately stored outside SQLite. SQLite keeps only the
// stable metadata and conversation binding; files are written atomically below
// the database directory using an opaque id as their name.

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxAttachmentSize int64 = 20 << 20
const maxAttachmentText = 128 << 10

type Attachment struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id,omitempty"`
	Name           string `json:"name"`
	MIME           string `json:"mime"`
	Size           int64  `json:"size"`
	CreatedAt      string `json:"created_at"`
}

func migrateAttachments(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS attachments(
	 id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, name TEXT NOT NULL,
	 mime TEXT NOT NULL, size INTEGER NOT NULL, disk_name TEXT NOT NULL UNIQUE,
	 created_at TEXT NOT NULL, FOREIGN KEY(conversation_id) REFERENCES conversations(id) ON DELETE CASCADE);
	CREATE TABLE IF NOT EXISTS attachment_messages(
	 attachment_id TEXT NOT NULL, message_id TEXT NOT NULL,
	 PRIMARY KEY(attachment_id,message_id),
	 FOREIGN KEY(attachment_id) REFERENCES attachments(id) ON DELETE CASCADE,
	 FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE);`)
	return err
}

func (s *Store) attachmentRoot() (string, error) {
	var file string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(new(any), new(any), &file); err != nil {
		return "", err
	}
	if file == "" {
		return "", errors.New("database path unavailable")
	}
	root := filepath.Join(filepath.Dir(file), "attachments")
	if info, err := os.Lstat(root); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("attachment directory is not a real directory")
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(root, 0700); err != nil {
			return "", err
		}
	} else {
		return "", err
	}
	return root, nil
}

func (s *Store) AddAttachment(conv, name, declaredMIME string, src io.Reader) (Attachment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	return s.addAttachment(ctx, conv, name, declaredMIME, src)
}
func (s *Store) addAttachment(ctx context.Context, conv, name, declaredMIME string, src io.Reader) (Attachment, error) {
	if s.requireGuestAttachments {
		return s.addGuestAttachment(ctx, conv, name, declaredMIME, src)
	}
	if src == nil {
		return Attachment{}, errors.New("attachment content is required")
	}
	if _, err := s.GetConversation(conv); err != nil {
		return Attachment{}, errors.New("conversation not found")
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		name = "attachment"
	}
	if len(name) > 255 {
		name = name[:255]
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return Attachment{}, err
	}
	id := uuid.NewString()
	disk := id + ".upload"
	tmp := filepath.Join(root, "."+disk+".tmp")
	final := filepath.Join(root, disk)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Attachment{}, err
	}
	written, copyErr := io.Copy(f, io.LimitReader(src, maxAttachmentSize+1))
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || written > maxAttachmentSize {
		_ = os.Remove(tmp)
		if written > maxAttachmentSize {
			return Attachment{}, fmt.Errorf("attachment exceeds %d MB", maxAttachmentSize>>20)
		}
		if copyErr != nil {
			return Attachment{}, copyErr
		}
		return Attachment{}, closeErr
	}
	if err = os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return Attachment{}, err
	}
	actual := "application/octet-stream"
	if f, e := os.Open(final); e == nil {
		var b [512]byte
		n, _ := f.Read(b[:])
		_ = f.Close()
		actual = http.DetectContentType(b[:n])
	}
	// A client supplied type is useful for otherwise opaque formats, but never
	// allows a browser to render active content.
	if strings.HasPrefix(actual, "text/") || strings.HasPrefix(actual, "image/") || actual == "application/pdf" || actual == "application/json" {
		declaredMIME = actual
	} else if mt, _, e := mime.ParseMediaType(declaredMIME); e == nil && mt != "" {
		declaredMIME = mt
	} else {
		declaredMIME = actual
	}
	if strings.HasPrefix(declaredMIME, "image/") && !isRasterFile(final, declaredMIME) {
		declaredMIME = "application/octet-stream"
	}
	a := Attachment{ID: id, ConversationID: conv, Name: name, MIME: declaredMIME, Size: written, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err = s.db.Exec(`INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, a.ID, a.ConversationID, a.Name, a.MIME, a.Size, disk, a.CreatedAt)
	if err != nil {
		_ = os.Remove(final)
		return Attachment{}, err
	}
	return a, nil
}

func isRasterFile(path, typ string) bool {
	if typ == "image/svg+xml" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, format, err := image.DecodeConfig(io.LimitReader(f, 1<<20))
	if err != nil {
		return false
	}
	return format == "png" || format == "jpeg" || format == "gif" || (format == "webp" && (typ == "image/webp"))
}

func (s *Store) Attachment(id string) (Attachment, string, error) {
	var a Attachment
	var disk string
	err := s.db.QueryRow(`SELECT id,conversation_id,name,mime,size,created_at,disk_name FROM attachments WHERE id=?`, id).Scan(&a.ID, &a.ConversationID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt, &disk)
	if err != nil {
		return Attachment{}, "", err
	}
	if strings.HasPrefix(disk, guestAttachmentPrefix) {
		id, err := guestAttachmentID(disk)
		if err != nil || id != a.ID {
			return Attachment{}, "", errors.New("invalid guest attachment metadata")
		}
		return a, disk, nil
	}
	root, err := s.attachmentRoot()
	if err != nil {
		return Attachment{}, "", err
	}
	p := filepath.Join(root, filepath.Base(disk))
	if filepath.Dir(p) != root {
		return Attachment{}, "", errors.New("invalid attachment path")
	}
	return a, p, nil
}

// ReadAttachmentText is the bounded text extraction primitive used by the
// agent tool. Binary formats are intentionally returned as metadata so a
// caller cannot accidentally turn an uploaded file into an unbounded prompt.
func (s *Store) ReadAttachmentText(id, conversationID string) (string, Attachment, error) {
	a, path, err := s.Attachment(id)
	if err != nil {
		return "", Attachment{}, err
	}
	if conversationID != "" {
		ok, e := s.attachmentAvailableInConversation(id, conversationID)
		if e != nil {
			return "", Attachment{}, e
		}
		if !ok {
			return "", Attachment{}, errors.New("attachment belongs to another conversation")
		}
	}
	if !(strings.HasPrefix(a.MIME, "text/") || a.MIME == "application/json" || a.MIME == "text/csv") {
		return "", a, errors.New("attachment is not a readable text file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(ctx, path)
	if err != nil {
		return "", Attachment{}, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxAttachmentText+1))
	if err != nil {
		return "", Attachment{}, err
	}
	if len(b) > maxAttachmentText {
		b = b[:maxAttachmentText]
		return string(b) + "\n[… truncated …]", a, nil
	}
	return string(b), a, nil
}

func (s *Store) BindAttachments(conv, messageID string, ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := bindAttachmentsTx(tx, conv, messageID, ids); err != nil {
		return err
	}
	return tx.Commit()
}

func bindAttachmentsTx(tx *sql.Tx, conv, messageID string, ids []string) error {
	if len(ids) > 8 {
		return errors.New("at most 8 attachments are allowed")
	}
	var messageConv string
	if err := tx.QueryRow(`SELECT conversation_id FROM messages WHERE id=?`, messageID).Scan(&messageConv); err != nil {
		return err
	}
	if messageConv != conv {
		return errors.New("message belongs to another conversation")
	}
	for _, id := range ids {
		var owner string
		if err := tx.QueryRow(`SELECT conversation_id FROM attachments WHERE id=?`, id).Scan(&owner); err != nil {
			return err
		}
		if owner != conv {
			return errors.New("attachment belongs to another conversation")
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, id, messageID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) AttachmentsForMessage(messageID string) ([]Attachment, error) {
	return attachmentMetadata(s.db, messageID)
}

func (s *Server) routeAttachments(w http.ResponseWriter, r *http.Request, p string) bool {
	isUpload := strings.HasPrefix(p, "conversations/") && strings.HasSuffix(p, "/attachments")
	isDownload := strings.HasPrefix(p, "attachments/") && !strings.Contains(strings.TrimPrefix(p, "attachments/"), "/")
	if !isUpload && !isDownload {
		return false
	}
	if isUpload {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method", "method not allowed")
			return true
		}
		conv := strings.TrimSuffix(strings.TrimPrefix(p, "conversations/"), "/attachments")
		if _, err := s.store.GetConversation(conv); err != nil {
			writeErr(w, 404, "not_found", "conversation not found")
			return true
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentSize+1<<20)
		multipart, err := r.MultipartReader()
		if err != nil {
			writeErr(w, 400, "invalid_request", "multipart form required")
			return true
		}
		var file io.ReadCloser
		var filename, typ string
		for {
			part, e := multipart.NextPart()
			if e != nil {
				break
			}
			if part.FormName() == "file" {
				file = part
				filename = part.FileName()
				typ = part.Header.Get("Content-Type")
				break
			}
			part.Close()
		}
		if file == nil {
			writeErr(w, 400, "invalid_request", "file is required")
			return true
		}
		defer file.Close()
		a, err := s.store.addAttachment(r.Context(), conv, filename, typ, file)
		if err != nil {
			writeErr(w, 400, "invalid_attachment", err.Error())
			return true
		}
		writeJSON(w, 201, a)
		return true
	}
	id := strings.TrimPrefix(p, "attachments/")
	a, path, err := s.store.Attachment(id)
	if err != nil {
		writeErr(w, 404, "not_found", "attachment not found")
		return true
	}
	if r.Method != http.MethodGet {
		writeErr(w, 405, "method", "method not allowed")
		return true
	}
	f, err := s.store.openAttachment(r.Context(), path)
	if err != nil {
		writeErr(w, 404, "not_found", "attachment not found")
		return true
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.MIME)
	if strings.HasPrefix(a.MIME, "image/") && a.MIME != "image/svg+xml" {
		w.Header().Set("Content-Disposition", `inline; filename="`+safeAttachmentName(a.Name)+`"`)
	} else {
		w.Header().Set("Content-Disposition", `attachment; filename="`+safeAttachmentName(a.Name)+`"`)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if seeker, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, a.Name, time.Time{}, seeker)
	} else {
		data, err := io.ReadAll(io.LimitReader(f, maxAttachmentSize+1))
		if err != nil || int64(len(data)) > maxAttachmentSize {
			return true
		}
		http.ServeContent(w, r, a.Name, time.Time{}, bytes.NewReader(data))
	}
	return true
}

func safeAttachmentName(name string) string {
	name = strings.NewReplacer("\r", "", "\n", "", `"`, "'", "\\", "_").Replace(name)
	if name == "" {
		return "attachment"
	}
	return name
}
