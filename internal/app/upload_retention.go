package app

// Retention of uploaded files (owner decisions 11-10, rounds six and seven of
// docs/design/attachment-object-storage.md). Uploads and Bot files are deleted
// after `upload_retention_days`, counted from creation; 0 never deletes. The
// row stays as a tombstone (expired_at) so the file card says "Expired" and a
// Bot reading it gets the named error attachment_expired. A Bot can exempt a
// file with keep_file. Artifacts, traces, imports and mirrors are never touched.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	autoUploadRetentionDays = "upload_retention_days"
	autoBotsKeepFiles       = "bots_can_keep_files"

	// A multi-account (cloud) server deletes after 30 days; a single-account server keeps everything unless told otherwise.
	defaultAccountUploadRetentionDays = 30

	codeAttachmentExpired = "attachment_expired"
)

var errAttachmentExpired = errors.New(codeAttachmentExpired)

// attachmentKeptByName is the SQL expression for the name of the Bot that kept a row (a deleted Bot keeps its name).
const attachmentKeptByName = `COALESCE((SELECT name FROM bots WHERE id=a.kept_by_bot_id),(SELECT name FROM deleted_bot_identities WHERE bot_id=a.kept_by_bot_id),'')`

// runUploadRetention applies the setting; 0 days deletes nothing.
func (s *Server) runUploadRetention(at time.Time) {
	days := s.autoInt(autoUploadRetentionDays)
	if days <= 0 {
		return
	}
	if _, err := s.store.expireUploads(at.Add(-time.Duration(days) * 24 * time.Hour)); err != nil {
		log.Printf("[files] upload retention: %v", err)
	}
}

// expireUploads deletes the bytes of every upload and Bot file created before
// cutoff that no Bot kept, and returns how many it expired. Bucket objects go
// through the usual delete grace.
func (s *Store) expireUploads(cutoff time.Time) (int, error) {
	rows, err := s.db.Query(`SELECT id FROM attachments WHERE origin IN (?,?) AND expired_at IS NULL AND kept_by_bot_id IS NULL AND created_at<?`, attachmentOriginUpload, attachmentOriginBotFile, stampTime(cutoff))
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	expired := 0
	defer s.cleanupDeletedAttachments()
	for _, id := range ids {
		if err = s.expireAttachment(id); err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

// expireAttachment turns one row into a tombstone and queues its bytes for deletion.
func (s *Store) expireAttachment(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var disk string
	if err = tx.QueryRow(`SELECT disk_name FROM attachments WHERE id=?`, id).Scan(&disk); err != nil {
		return err
	}
	var after any
	if strings.HasPrefix(disk, objPrefix) {
		after = s.deleteDeadline(context.Background())
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO deleted_attachment_files(disk_name,delete_after) VALUES(?,?)`, disk, after); err != nil {
		return err
	}
	// size 0 keeps the storage counters right (the trigger watches size).
	if _, err = tx.Exec(`UPDATE attachments SET expired_at=?,size=0,disk_name='expired:'||id WHERE id=?`, now(), id); err != nil {
		return err
	}
	return tx.Commit()
}

// keepFileTools is keep_file: it marks one upload or Bot file as not auto-deleted and records which Bot kept it.
func (s *Server) keepFileTools(c Conversation, r Run) []Tool {
	if !s.autoBool(autoBotsKeepFiles) {
		return nil
	}
	return []Tool{{Name: "keep_file", Description: "Mark an uploaded file or Bot file of this conversation as not automatically deleted. The file is not copied.", Parameters: objectSchema(map[string]any{
		"attachment_id": map[string]any{"type": "string"},
	}, []string{"attachment_id"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var x struct {
			ID string `json:"attachment_id"`
		}
		if json.Unmarshal(raw, &x) != nil || x.ID == "" {
			return "", errors.New("attachment_id required")
		}
		answer := func(kept bool, note string) (string, error) {
			out, _ := json.Marshal(map[string]any{"kept": kept, "note": note})
			return string(out), nil
		}
		if s.autoInt(autoUploadRetentionDays) <= 0 {
			return answer(false, "This server does not delete uploaded files automatically. Nothing was changed.")
		}
		a, _, err := s.store.Attachment(x.ID)
		if err != nil {
			return "", err
		}
		if ok, e := s.store.attachmentAvailableInConversation(x.ID, c.ID); e != nil || !ok {
			return "", errors.New("attachment belongs to another conversation")
		}
		res, err := s.store.db.Exec(`UPDATE attachments SET kept_by_bot_id=? WHERE id=? AND origin IN (?,?) AND kept_by_bot_id IS NULL`, r.BotID, x.ID, attachmentOriginUpload, attachmentOriginBotFile)
		if err != nil {
			return "", err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var origin string
			var keeper sql.NullString
			if err = s.store.db.QueryRow(`SELECT origin,kept_by_bot_id FROM attachments WHERE id=?`, x.ID).Scan(&origin, &keeper); err != nil {
				return "", err
			}
			if keeper.Valid {
				return answer(true, fmt.Sprintf("%s is already kept.", a.Name))
			}
			return answer(false, fmt.Sprintf("%s is not deleted automatically. Nothing was changed.", a.Name))
		}
		return answer(true, fmt.Sprintf("%s is kept and will not be deleted automatically.", a.Name))
	}}}
}

// uploadRetentionPrompt is the one sentence that tells a Bot about the retention; empty where nothing is deleted.
func (s *Server) uploadRetentionPrompt() string {
	days := s.autoInt(autoUploadRetentionDays)
	if days <= 0 || !s.autoBool(autoBotsKeepFiles) {
		return ""
	}
	return fmt.Sprintf("\nUploaded files are deleted after %d days; use keep_file for a file you need later.", days)
}
