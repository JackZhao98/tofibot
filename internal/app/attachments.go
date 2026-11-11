package app

// Attachments are deliberately stored outside SQLite. SQLite keeps only the
// stable metadata and conversation binding; files are written atomically below
// the database directory using an opaque id as their name.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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

	"github.com/JackZhao98/tofibot/internal/filestore"
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
	 FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE);
	CREATE INDEX IF NOT EXISTS attachment_messages_message ON attachment_messages(message_id);
	CREATE INDEX IF NOT EXISTS attachments_conversation_created ON attachments(conversation_id,created_at);
	CREATE TABLE IF NOT EXISTS outbox_synced(
	 path TEXT NOT NULL, size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL, sha256 TEXT NOT NULL,
	 attachment_id TEXT NOT NULL, synced_at TEXT NOT NULL,
	 PRIMARY KEY(path,size,mtime_ns));`)
	if err != nil {
		return err
	}
	// Additive columns (computer-decoupling S1). Old servers ignore them; a row
	// that predates them reads as an upload with an unknown digest.
	for _, col := range []struct{ name, ddl string }{
		{"sha256", `ALTER TABLE attachments ADD COLUMN sha256 TEXT`},
		{"origin", `ALTER TABLE attachments ADD COLUMN origin TEXT NOT NULL DEFAULT 'upload'`},
		{"bot_id", `ALTER TABLE attachments ADD COLUMN bot_id TEXT`},
		// '' = this server's disk (or a legacy `vm:` row); else the bucket generation of an `obj:` row.
		{"store", `ALTER TABLE attachments ADD COLUMN store TEXT NOT NULL DEFAULT ''`},
	} {
		if err = ensureColumn(db, "attachments", col.name, col.ddl); err != nil {
			return err
		}
	}
	return migrateStorageCounters(db)
}

// Where an attachment came from. Stored in attachments.origin.
const (
	attachmentOriginUpload    = "upload"
	attachmentOriginPublished = "published"
	attachmentOriginGenerated = "generated"
	attachmentOriginOutbox    = "outbox"
	attachmentOriginMirror    = "mirror"
	attachmentOriginImport    = "import"
	// A file a Bot made or passed along during work that is not a deliverable (a process screenshot, a draft attachment). A File, not an Artifact.
	attachmentOriginBotFile = "bot_file"
)

// attachmentOpts carries what the server records beside the bytes.
type attachmentOpts struct {
	Origin string
	BotID  string
	// MaxBytes lowers the per-file cap; zero means maxAttachmentSize.
	MaxBytes int64
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
		// Two uploads can create it at the same moment: the loser sees EEXIST and checks what the winner made.
		if err := os.Mkdir(root, 0700); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return "", err
			}
			if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", errors.New("attachment directory is not a real directory")
			}
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
	return s.addAttachmentOpts(ctx, conv, name, declaredMIME, src, attachmentOpts{})
}

// addAttachmentOpts is the one writer of class A file bytes: the account's
// attachments directory, in every mode. The bytes are fsynced before the
// rename and the row is inserted last, so a crash leaves at worst an unreferenced
// file, never a row without bytes.
func (s *Store) addAttachmentOpts(ctx context.Context, conv, name, declaredMIME string, src io.Reader, opts attachmentOpts) (Attachment, error) {
	if opts.Origin == "" {
		opts.Origin = attachmentOriginUpload
	}
	limit := maxAttachmentSize
	if opts.MaxBytes > 0 && opts.MaxBytes < limit {
		limit = opts.MaxBytes
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
	// Quota and host free space (S3). A cap below the file limit turns an
	// overflow into storage_quota_exceeded instead of "exceeds N MB".
	room, release, err := s.storageAdmit(root, limit)
	if err != nil {
		return Attachment{}, err
	}
	defer release()
	quotaCapped := room > 0 && room < limit
	if quotaCapped {
		limit = room
	}
	id := uuid.NewString()
	backend, err := s.fileBackend(ctx)
	if err != nil {
		return Attachment{}, err
	}
	toBucket := backend.Kind == fileStoreKindS3
	if toBucket && backend.Store == nil {
		return Attachment{}, newFileStoreError(codeFileStoreMisconfigured, errors.New("no bucket configured"))
	}
	// Every file is first written whole to a local spool file; size, type and
	// hash are settled there, then Put hands it to the backend.
	spoolDir := filepath.Join(root, spoolDirName)
	if err = os.MkdirAll(spoolDir, 0700); err != nil {
		return Attachment{}, err
	}
	spool := filepath.Join(spoolDir, id)
	f, err := os.OpenFile(spool, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Attachment{}, err
	}
	defer func() { f.Close(); _ = os.Remove(spool) }()
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(f, hash), io.LimitReader(src, limit+1))
	if copyErr == nil {
		copyErr = f.Sync()
	}
	if copyErr != nil || written > limit {
		if written > limit {
			if quotaCapped {
				return Attachment{}, newQuotaError()
			}
			return Attachment{}, fmt.Errorf("attachment exceeds %d MB", limit>>20)
		}
		return Attachment{}, copyErr
	}
	actual := "application/octet-stream"
	var b [512]byte
	n, _ := f.ReadAt(b[:], 0)
	actual = http.DetectContentType(b[:n])
	// A client supplied type is useful for otherwise opaque formats, but never
	// allows a browser to render active content.
	if strings.HasPrefix(actual, "text/") || strings.HasPrefix(actual, "image/") || actual == "application/pdf" || actual == "application/json" {
		declaredMIME = actual
	} else if mt, _, e := mime.ParseMediaType(declaredMIME); e == nil && mt != "" {
		declaredMIME = mt
	} else {
		declaredMIME = actual
	}
	if strings.HasPrefix(declaredMIME, "image/") && !isRasterFile(spool, declaredMIME) {
		declaredMIME = "application/octet-stream"
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	var target filestore.Store = filestore.Disk{Root: root}
	key := id + ".upload"
	disk, gen := key, ""
	if toBucket {
		target, key = backend.Store, backend.objectKey(opts.Origin, id)
		disk, gen = objPrefix+key, backend.Gen
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return Attachment{}, err
	}
	if err = target.Put(ctx, key, f, filestore.ObjectMeta{Size: written, SHA256: digest, SafeMIME: declaredMIME}); err != nil {
		if toBucket {
			return Attachment{}, fileStoreFailure(err)
		}
		return Attachment{}, err
	}
	a := Attachment{ID: id, ConversationID: conv, Name: name, MIME: declaredMIME, Size: written, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	_, err = s.db.ExecContext(ctx, `INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at,sha256,origin,bot_id,store) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.ID, a.ConversationID, a.Name, a.MIME, a.Size, disk, a.CreatedAt, digest, opts.Origin, nullString(opts.BotID), gen)
	if err != nil {
		_ = target.Delete(context.WithoutCancel(ctx), key)
		return Attachment{}, err
	}
	return a, nil
}

// spoolDirName is the local staging directory inside the attachments directory.
// Its files never outlive a request; start-up empties it.
const spoolDirName = ".spool"

func (s *Store) clearSpool() {
	if root, err := s.attachmentRoot(); err == nil {
		_ = os.RemoveAll(filepath.Join(root, spoolDirName))
	}
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
	if strings.HasPrefix(disk, objPrefix) {
		return a, disk, nil
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
			if fe, ok := asFileStoreError(err); ok {
				writeErr(w, fe.status, fe.code, fileStoreMessage(fe.code))
				return true
			}
			if code := storageErrorCode(err); code != "" {
				var se *storageError
				errors.As(err, &se)
				writeErr(w, http.StatusInsufficientStorage, code, se.Text)
				return true
			}
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
		fe, isStoreErr := asFileStoreError(err)
		switch {
		case isStoreErr:
			writeErr(w, fe.status, fe.code, fileStoreMessage(fe.code))
		case errors.Is(err, errFileOnDetachedComputer):
			writeErr(w, 409, "file_on_detached_computer", "This file is on the detached Bot computer. Attach it to recover.")
		case errors.Is(err, errFileGoneFromComputer):
			writeErr(w, 404, "file_missing_on_computer", "This file is no longer on the Bot computer and was not copied to the server.")
		default:
			writeErr(w, 404, "not_found", "attachment not found")
		}
		return true
	}
	defer f.Close()
	w.Header().Set("Content-Type", a.MIME)
	// PDFs render in the viewer's embedded browser PDF reader only on request
	// (?inline=1); plain links keep downloading.
	pdfInline := r.URL.Query().Get("inline") == "1" && strings.EqualFold(strings.TrimSpace(strings.SplitN(a.MIME, ";", 2)[0]), "application/pdf")
	if pdfInline || (strings.HasPrefix(a.MIME, "image/") && a.MIME != "image/svg+xml") {
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
