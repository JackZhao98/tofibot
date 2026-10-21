package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

func migratePublishedAttachments(s *Store) error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS run_attachments(
	 run_id TEXT NOT NULL, attachment_id TEXT NOT NULL UNIQUE, request_key TEXT NOT NULL,
	 content_sha256 TEXT NOT NULL, ordinal INTEGER NOT NULL, created_at TEXT NOT NULL,
	 PRIMARY KEY(run_id,request_key,content_sha256),
	 FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE,
	 FOREIGN KEY(attachment_id) REFERENCES attachments(id) ON DELETE CASCADE);`)
	return err
}

// stageRunAttachment durably queues an attachment for the run's final assistant
// message. A retry with identical key and bytes returns the first attachment.
func (s *Store) stageRunAttachment(run Run, c Conversation, a Attachment, requestKey string) (Attachment, error) {
	if run.ID == "" || run.ConversationID != c.ID || a.ConversationID != c.ID {
		return Attachment{}, errors.New("attachment run scope mismatch")
	}
	_, path, err := s.Attachment(a.ID)
	if err != nil {
		return Attachment{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(ctx, path)
	if err != nil {
		return Attachment{}, err
	}
	h := sha256.New()
	_, err = io.Copy(h, f)
	_ = f.Close()
	if err != nil {
		return Attachment{}, err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if strings.TrimSpace(requestKey) == "" {
		requestKey = a.Name
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Attachment{}, err
	}
	defer tx.Rollback()
	var status, conv, bot string
	if err = tx.QueryRow(`SELECT status,conversation_id,bot_id FROM runs WHERE id=?`, run.ID).Scan(&status, &conv, &bot); err != nil || status != "running" || conv != c.ID || bot != run.BotID {
		return Attachment{}, errors.New("run is no longer active for this conversation")
	}
	var existing string
	err = tx.QueryRow(`SELECT attachment_id FROM run_attachments WHERE run_id=? AND request_key=? AND content_sha256=?`, run.ID, requestKey, digest).Scan(&existing)
	if err == nil {
		_ = tx.Rollback()
		_ = s.deleteUnboundAttachment(a.ID)
		xa, _, getErr := s.Attachment(existing)
		return xa, getErr
	}
	// A newer publish of the same source (request key) or the same file name in
	// this run supersedes the older one: only the latest is bound to the final
	// message. The older attachment stays stored in the conversation, unlisted.
	if _, err = tx.Exec(`DELETE FROM run_attachments WHERE run_id=? AND (request_key=? OR attachment_id IN (SELECT id FROM attachments WHERE name=? AND id<>?))`, run.ID, requestKey, a.Name, a.ID); err != nil {
		return Attachment{}, err
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, run.ID).Scan(&count); err != nil {
		return Attachment{}, err
	}
	if count >= 8 {
		return Attachment{}, errors.New("at most 8 published files are allowed per run")
	}
	if _, err = tx.Exec(`INSERT INTO run_attachments(run_id,attachment_id,request_key,content_sha256,ordinal,created_at)
	 VALUES(?,?,?,?,(SELECT COALESCE(MAX(ordinal),0)+1 FROM run_attachments WHERE run_id=?),?)`, run.ID, a.ID, requestKey, digest, run.ID, now()); err != nil {
		return Attachment{}, err
	}
	if err = tx.Commit(); err != nil {
		return Attachment{}, err
	}
	return a, nil
}

func (s *Store) deleteUnboundAttachment(id string) error {
	a, path, err := s.Attachment(id)
	_ = a
	if err != nil {
		return err
	}
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM attachment_messages WHERE attachment_id=?`, id).Scan(&n); err != nil || n != 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	disk := filepath.Base(path)
	if strings.HasPrefix(path, guestAttachmentPrefix) {
		disk = path
	}
	if _, err = tx.Exec(`INSERT OR IGNORE INTO deleted_attachment_files VALUES(?)`, disk); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM attachments WHERE id=?`, id); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.cleanupDeletedAttachments()
	return nil
}

func bindRunAttachmentsTx(tx *sql.Tx, runID, messageID string) error {
	if _, err := tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id)
	 SELECT attachment_id,? FROM run_attachments WHERE run_id=? ORDER BY ordinal`, messageID, runID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM run_attachments WHERE run_id=?`, runID)
	return err
}

// keepFailedRunFilesTx gives files a run published before it failed a home in
// the conversation: an attachments-only assistant message, so the failure the
// person sees carries the work already done and every link the model handed
// out keeps resolving. Cancelled and interrupted runs still discard theirs.
func keepFailedRunFilesTx(tx *sql.Tx, runID, t string) error {
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, runID).Scan(&pending); err != nil || pending == 0 {
		return err
	}
	var conv, bot string
	if err := tx.QueryRow(`SELECT conversation_id,bot_id FROM runs WHERE id=?`, runID).Scan(&conv, &bot); err != nil {
		return err
	}
	var seq int64
	if err := tx.QueryRow(nextMessageSeqSQL, conv, conv, streamDraftActive).Scan(&seq); err != nil {
		return err
	}
	m := Message{ID: uuid.NewString(), ConversationID: conv, Seq: seq, Role: "assistant", SenderBotID: bot, RunID: runID, CreatedAt: t}
	if _, err := tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, m.ID, conv, seq, m.Role, m.Kind, bot, runID, "", t); err != nil {
		return err
	}
	if err := bindRunAttachmentsTx(tx, runID, m.ID); err != nil {
		return err
	}
	var err error
	if m.Attachments, err = attachmentMetadata(tx, m.ID); err != nil {
		return err
	}
	b, _ := json.Marshal(m)
	_, err = tx.Exec(`INSERT INTO events(conversation_id,type,data,created_at) VALUES(?,?,?,?)`, conv, "message", string(b), t)
	return err
}

func (s *Store) cleanupRunAttachments(runID string) {
	rows, err := s.db.Query(`SELECT attachment_id FROM run_attachments WHERE run_id=?`, runID)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_ = s.deleteUnboundAttachment(id)
	}
}

func (s *Store) cleanupRunTreeAttachments(root string) {
	rows, err := s.db.Query(`WITH RECURSIVE affected(id) AS (SELECT id FROM runs WHERE id=? UNION SELECT r.id FROM runs r JOIN affected a ON r.parent_run_id=a.id) SELECT id FROM affected`, root)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		s.cleanupRunAttachments(id)
	}
}

func (s *Server) publishAttachmentTools(c Conversation, r Run) []Tool {
	if s.microVM == nil {
		return nil
	}
	return []Tool{{Name: "publish_file", Description: "Publish a regular file from the shared Linux VM as a persistent attachment in this conversation. Images are previewable; other files are downloadable after the VM stops. The result carries a url that opens the file for the signed-in person; when the person asks for the file or its link, give them that url, as a markdown link, instead of saying a link is unavailable.", Parameters: objectSchema(map[string]any{
		"path": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "request_key": map[string]any{"type": "string", "description": "Optional stable retry key"},
	}, []string{"path"}), Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
		var in struct {
			Path       string `json:"path"`
			Name       string `json:"name"`
			RequestKey string `json:"request_key"`
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || strings.TrimSpace(in.Path) == "" {
			return "", errors.New("publish_file requires path")
		}
		if in.RequestKey == "" {
			in.RequestKey = in.Path
		}
		type wireChunk struct {
			Data    string `json:"data_base64"`
			Next    int64  `json:"next_offset"`
			Size    int64  `json:"size"`
			EOF     bool   `json:"eof"`
			Version string `json:"version"`
			Name    string `json:"name"`
		}
		fetch := func(offset int64, version string) (wireChunk, []byte, error) {
			args, _ := json.Marshal(map[string]any{"path": in.Path, "offset": offset, "limit": 512 << 10, "version": version})
			out, err := s.microVMAction(ctx, r, "files.export_chunk", args)
			if err != nil {
				return wireChunk{}, nil, err
			}
			var w wireChunk
			if json.Unmarshal([]byte(out), &w) != nil || w.Next < offset || w.Size > maxAttachmentSize {
				return w, nil, errors.New("invalid or oversized VM file")
			}
			b, err := base64.StdEncoding.DecodeString(w.Data)
			if err != nil || int64(len(b)) != w.Next-offset {
				return w, nil, errors.New("invalid VM file chunk")
			}
			return w, b, nil
		}
		first, firstBytes, err := fetch(0, "")
		if err != nil {
			return "", err
		}
		version, sourceName, size := first.Version, first.Name, first.Size
		var offset int64
		reader, writer := io.Pipe()
		errCh := make(chan error, 1)
		go func() {
			defer writer.Close()
			fail := func(err error) { _ = writer.CloseWithError(err); errCh <- err }
			if _, err := writer.Write(firstBytes); err != nil {
				errCh <- err
				return
			}
			offset = first.Next
			if first.EOF {
				errCh <- nil
				return
			}
			for {
				chunk, b, e := fetch(offset, version)
				if e != nil {
					fail(e)
					return
				}
				if version != chunk.Version || size != chunk.Size {
					fail(errors.New("file changed while publishing"))
					return
				}
				if _, e = writer.Write(b); e != nil {
					errCh <- e
					return
				}
				offset = chunk.Next
				if chunk.EOF {
					errCh <- nil
					return
				}
			}
		}()
		name := filepath.Base(strings.TrimSpace(in.Name))
		if name == "." || name == "" {
			name = sourceName
		}
		a, err := s.store.AddAttachment(c.ID, name, "application/octet-stream", reader)
		if err != nil {
			_ = reader.CloseWithError(err)
		}
		copyErr := <-errCh
		if err == nil {
			err = copyErr
		}
		if err != nil {
			return "", err
		}
		created := a
		a, err = s.store.stageRunAttachment(r, c, a, in.RequestKey)
		if err != nil {
			_ = s.store.deleteUnboundAttachment(created.ID)
			return "", err
		}
		return s.publishedFileResult(a), nil
	}}}
}

// attachmentLink is the in-app address of an attachment: absolute when the
// server knows its public origin, app-relative otherwise. Opening it needs the
// owner's normal session; no unauthenticated link exists.
func (s *Server) attachmentLink(id string) string {
	return s.publicOrigin + "/api/attachments/" + url.PathEscape(id)
}

func (s *Server) publishedFileResult(a Attachment) string {
	result, _ := json.Marshal(map[string]any{"attachment_id": a.ID, "name": a.Name, "mime": a.MIME, "size": a.Size, "url": s.attachmentLink(a.ID), "link_access": "opens for the signed-in owner only"})
	return string(result)
}
