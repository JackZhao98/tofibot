package app

// send_message attachments: one way to give the person files. An item names
// exactly one source: attachment_id (a file of this conversation, or the
// capture_id of a screen capture of this run), path (a file on the Bot
// computer) or url (a public address the server downloads). The call is
// atomic: every item is resolved to a stored, still unbound attachment before
// the answer gates run, and when any item or any gate fails, everything this
// call created is deleted. Staging into run_attachments happens only after
// the gates pass.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/JackZhao98/tofibot/internal/webtools"
)

const (
	maxSayAttachments = 8
	// maxSayAttachmentBytes bounds the bytes one call newly fetches (downloads,
	// computer files, captures).
	maxSayAttachmentBytes int64 = 40 << 20
	// sayDownloadBudget is the time one call may spend downloading.
	sayDownloadBudget = 60 * time.Second

	sayAttachmentsDescription = "Files that reach the person with this message; pictures show inline. Each item names exactly one source: attachment_id (a file of this conversation: an upload, a generated image or a file sent earlier; a capture_id of a screen capture of this run goes here too), path (an absolute path on the Bot computer, under /workspace), or url (the public web address of a file, which the server downloads). name sets the file name shown; replaces_attachment_id names an image generate_image made in this run, which then leaves the reply. Every regular file under /workspace/home/outbox, sub-folders included, is attached to the answer when the run ends, so keep only deliverables there and make working files in your own directory. At most 8 files per reply; a refused item refuses the whole call and nothing is attached."
)

// sayAttachmentRoot is the Bot computer's workspace; a path item must lie under it. Tests point it at a fake guest's root.
var sayAttachmentRoot = "/workspace"

type sayAttachmentItem struct {
	AttachmentID string `json:"attachment_id"`
	Path         string `json:"path"`
	ComputerID   string `json:"computer_id"`
	URL          string `json:"url"`
	Name         string `json:"name"`
	Replaces     string `json:"replaces_attachment_id"`
}

func sayAttachmentsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": maxSayAttachments, "description": sayAttachmentsDescription, "items": objectSchema(map[string]any{
		"attachment_id":          map[string]any{"type": "string"},
		"path":                   map[string]any{"type": "string"},
		"computer_id":            map[string]any{"type": "string", "description": "With path only: the Bot computer, which is the default."},
		"url":                    map[string]any{"type": "string"},
		"name":                   map[string]any{"type": "string"},
		"replaces_attachment_id": map[string]any{"type": "string"},
	}, nil)}
}

// sayAttachmentRefusal is a validation outcome naming the item and the reason.
func sayAttachmentRefusal(code string, index int, reason string) error {
	msg := fmt.Sprintf("attachments[%d]: %s. Nothing was attached or sent; repair the item and call send_message again.", index, reason)
	return tooloutcome.New(tooloutcome.Validation, code, "not_executed", msg, "repair_arguments").Err()
}

// sayResolved is one attachment ready to stage.
type sayResolved struct {
	Att      Attachment
	Key      string
	Created  bool
	Replaces string
	Index    int
}

// sayResolution is the result of resolving a call's items.
type sayResolution struct {
	Items []sayResolved
	// settles return an owner approval a failed call did not use.
	settles []func(error)
}

// names lists the file names of the resolved items, in order.
func (res sayResolution) names() []string {
	var out []string
	for _, it := range res.Items {
		out = append(out, it.Att.Name)
	}
	return out
}

// settle reports the call's end to every approval it claimed.
func (res sayResolution) settle(err error) {
	for _, f := range res.settles {
		f(err)
	}
}

// discard deletes every attachment this call created. Files that were already
// part of the conversation stay.
func (s *Server) discardSayAttachments(res sayResolution) {
	for _, it := range res.Items {
		if it.Created {
			_ = s.store.deleteUnboundAttachment(it.Att.ID)
		}
	}
}

// resolveSayAttachments turns every item into a stored attachment of this
// conversation. It cleans up after itself when an item fails.
func (s *Server) resolveSayAttachments(ctx context.Context, r Run, c Conversation, items []sayAttachmentItem) (res sayResolution, err error) {
	defer func() {
		if err != nil {
			s.discardSayAttachments(res)
			res.settle(err)
			res = sayResolution{}
		}
	}()
	var fetched int64
	seen, keys := map[string]bool{}, map[string]bool{}
	dctx, cancel := context.WithTimeout(ctx, sayDownloadBudget)
	defer cancel()
	for i, item := range items {
		item.AttachmentID, item.Path = strings.TrimSpace(item.AttachmentID), strings.TrimSpace(item.Path)
		item.URL, item.ComputerID = strings.TrimSpace(item.URL), strings.TrimSpace(item.ComputerID)
		item.Replaces = strings.TrimSpace(item.Replaces)
		sources := 0
		for _, v := range []string{item.AttachmentID, item.Path, item.URL} {
			if v != "" {
				sources++
			}
		}
		if sources != 1 {
			return res, sayAttachmentRefusal("attachment_source_invalid", i, "name exactly one source: attachment_id, path or url")
		}
		if item.ComputerID != "" && item.Path == "" {
			return res, sayAttachmentRefusal("attachment_source_invalid", i, "computer_id goes with path only")
		}
		if item.Replaces != "" {
			var n int
			if qerr := s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=? AND attachment_id=? AND request_key LIKE 'image:%'`, r.ID, item.Replaces).Scan(&n); qerr != nil || n == 0 {
				return res, sayAttachmentRefusal("attachment_not_found", i, "replaces_attachment_id must name an image generate_image made in this run and still attached to the reply")
			}
		}
		room := maxSayAttachmentBytes - fetched
		if room <= 0 {
			return res, sayAttachmentRefusal("attachment_too_large", i, "the files of one call may total at most 40 MiB")
		}
		limit := maxAttachmentSize
		if room < limit {
			limit = room
		}
		var got sayResolved
		switch {
		case item.AttachmentID != "" && strings.HasPrefix(item.AttachmentID, "cap-"):
			k, ok := s.keptCaptureOf(r.ID, item.AttachmentID)
			if !ok {
				return res, sayAttachmentRefusal("attachment_not_found", i, "capture_id "+item.AttachmentID+" is not a capture of this run (only the last 8 captures of a run can be sent)")
			}
			if k.Withheld {
				return res, sayAttachmentRefusal("attachment_withheld", i, "that capture showed a Secret Input window or a page whose pictures are not kept, so it cannot be sent")
			}
			if k.Personal {
				settle, gerr := s.guardShare(ctx, r, k.ComputerName)
				if gerr != nil {
					return res, gerr
				}
				res.settles = append(res.settles, settle)
			}
			reader, name := keptCaptureFile(k)
			if n := strings.TrimSpace(item.Name); n != "" {
				name = n
			}
			a, aerr := s.store.addAttachmentOpts(ctx, c.ID, name, k.Mime, reader, attachmentOpts{Origin: attachmentOriginPublished, BotID: r.BotID, MaxBytes: limit})
			if aerr != nil {
				return res, sayAttachmentRefusal(sayStoreCode(aerr), i, "the capture could not be stored: "+aerr.Error())
			}
			got = sayResolved{Att: a, Key: item.AttachmentID, Created: true}
			fetched += a.Size
		case item.AttachmentID != "":
			a, _, aerr := s.store.Attachment(item.AttachmentID)
			if aerr != nil {
				if a.ID != "" {
					return res, sayAttachmentRefusal("attachment_not_found", i, "attachment "+item.AttachmentID+" is no longer available")
				}
				return res, sayAttachmentRefusal("attachment_not_found", i, "no attachment has the id "+item.AttachmentID)
			}
			if a.ConversationID != c.ID {
				return res, sayAttachmentRefusal("attachment_other_conversation", i, "attachment "+item.AttachmentID+" belongs to another conversation")
			}
			if seen[a.ID] {
				continue
			}
			got = sayResolved{Att: a, Key: a.ID}
		case item.Path != "":
			if item.ComputerID != "" && item.ComputerID != microVMComputerID && (s.computer() == nil || s.computer().ID() != item.ComputerID) {
				return res, sayAttachmentRefusal("attachment_source_unavailable", i, "files on a person's computer are read as text only, so they cannot be attached; copy the content to the Bot computer or send its text")
			}
			if s.computer() == nil {
				return res, sayAttachmentRefusal("attachment_source_unavailable", i, "there is no Bot computer to read a path from")
			}
			clean := path.Clean(item.Path)
			if !path.IsAbs(clean) || !strings.HasPrefix(clean, sayAttachmentRoot+"/") {
				return res, sayAttachmentRefusal("attachment_path_invalid", i, "path must be an absolute path under "+sayAttachmentRoot)
			}
			a, _, ierr := s.importGuestFile(dctx, r, c, guestFileImport{Path: clean, Name: item.Name, Opts: attachmentOpts{Origin: attachmentOriginPublished, BotID: r.BotID, MaxBytes: limit}})
			if ierr != nil {
				code := "attachment_path_invalid"
				if strings.Contains(ierr.Error(), "oversized") || strings.Contains(ierr.Error(), "exceeds") || strings.Contains(ierr.Error(), "quota") {
					code = "attachment_too_large"
				}
				return res, sayAttachmentRefusal(code, i, "the file could not be read from the Bot computer: "+ierr.Error())
			}
			got = sayResolved{Att: a, Key: item.Path, Created: true}
			fetched += a.Size
		default:
			data, name, ctype, derr := webtools.Builtin{Transport: s.downloadTransport}.Download(dctx, item.URL, limit)
			if derr != nil {
				switch code := webtools.CodeOf(derr); code {
				case webtools.TooLarge:
					return res, sayAttachmentRefusal("attachment_too_large", i, "the file at that address is larger than allowed")
				case webtools.NotFound:
					return res, sayAttachmentRefusal("attachment_not_found", i, "nothing exists at that address")
				default:
					return res, sayAttachmentRefusal("attachment_download_refused", i, "the address could not be downloaded ("+string(code)+"); only public http and https addresses can be")
				}
			}
			if n := strings.TrimSpace(item.Name); n != "" {
				name = n
			}
			a, aerr := s.store.addAttachmentOpts(ctx, c.ID, name, ctype, bytes.NewReader(data), attachmentOpts{Origin: attachmentOriginPublished, BotID: r.BotID, MaxBytes: limit})
			if aerr != nil {
				return res, sayAttachmentRefusal(sayStoreCode(aerr), i, "the download could not be stored: "+aerr.Error())
			}
			got = sayResolved{Att: a, Key: item.URL, Created: true}
			fetched += a.Size
		}
		got.Replaces, got.Index = item.Replaces, i
		// Two items of one call never supersede each other.
		if keys[got.Key] {
			got.Key = fmt.Sprintf("%s#%d", got.Key, i)
		}
		keys[got.Key] = true
		seen[got.Att.ID] = true
		res.Items = append(res.Items, got)
	}
	return res, nil
}

func sayStoreCode(err error) string {
	if strings.Contains(err.Error(), "exceeds") || strings.Contains(err.Error(), "quota") {
		return "attachment_too_large"
	}
	return "attachment_source_invalid"
}

// stagedRunAttachmentCount is the number of files this run has queued for its reply.
func (s *Store) stagedRunAttachmentCount(runID string) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, runID).Scan(&n)
	return n
}

// sayStaged is what one send_message call put into, and took out of, the
// run's staged set, so a refusal after staging restores the set exactly.
type sayStaged struct {
	Atts     []Attachment
	inserted []string
	removed  []runAttachmentRow
}

type runAttachmentRow struct {
	AttachmentID, RequestKey, Digest, CreatedAt string
	Ordinal                                     int
}

// stageSayAttachments queues the resolved files for the run's reply in one
// transaction: all of them or none. A file supersedes an earlier one of the
// run only when it comes from the same source (request key), so two files
// with one name both reach the person. replaces_attachment_id takes its
// generated image out of the reply. The 8-file cap is checked on the result.
func (s *Server) stageSayAttachments(r Run, c Conversation, res sayResolution) (sayStaged, error) {
	var st sayStaged
	if r.ID == "" || r.ConversationID != c.ID {
		return st, errors.New("attachment run scope mismatch")
	}
	digests := make([]string, len(res.Items))
	for i, it := range res.Items {
		if it.Att.ConversationID != c.ID {
			return st, errors.New("attachment run scope mismatch")
		}
		d, err := s.store.attachmentSHA256(it.Att.ID)
		if err != nil {
			return st, err
		}
		digests[i] = d
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	var status, conv, bot string
	if err = tx.QueryRow(`SELECT status,conversation_id,bot_id FROM runs WHERE id=?`, r.ID).Scan(&status, &conv, &bot); err != nil || status != "running" || conv != c.ID || bot != r.BotID {
		return st, errors.New("run is no longer active for this conversation")
	}
	remove := func(where string, args ...any) error {
		rows, err := tx.Query(`SELECT attachment_id,request_key,content_sha256,ordinal,created_at FROM run_attachments WHERE run_id=? AND `+where, append([]any{r.ID}, args...)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var row runAttachmentRow
			if err := rows.Scan(&row.AttachmentID, &row.RequestKey, &row.Digest, &row.Ordinal, &row.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			st.removed = append(st.removed, row)
		}
		rows.Close()
		_, err = tx.Exec(`DELETE FROM run_attachments WHERE run_id=? AND `+where, append([]any{r.ID}, args...)...)
		return err
	}
	ids := make([]string, len(res.Items))
	var reused []string
	for i, it := range res.Items {
		ids[i] = it.Att.ID
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=? AND attachment_id=?`, r.ID, it.Att.ID).Scan(&n); err != nil {
			return st, err
		}
		if n == 0 {
			key := it.Key
			if strings.TrimSpace(key) == "" {
				key = it.Att.Name
			}
			var existing string
			e := tx.QueryRow(`SELECT attachment_id FROM run_attachments WHERE run_id=? AND request_key=? AND content_sha256=?`, r.ID, key, digests[i]).Scan(&existing)
			if e == nil {
				// The same bytes from the same source are already queued.
				ids[i] = existing
				if it.Created {
					reused = append(reused, it.Att.ID)
				}
			} else {
				if err = remove("request_key=?", key); err != nil {
					return st, err
				}
				if _, err = tx.Exec(`INSERT INTO run_attachments(run_id,attachment_id,request_key,content_sha256,ordinal,created_at)
				 VALUES(?,?,?,?,(SELECT COALESCE(MAX(ordinal),0)+1 FROM run_attachments WHERE run_id=?),?)`, r.ID, it.Att.ID, key, digests[i], r.ID, now()); err != nil {
					return st, err
				}
				st.inserted = append(st.inserted, it.Att.ID)
			}
		}
		if it.Replaces != "" && it.Replaces != ids[i] {
			if err = remove("attachment_id=? AND request_key LIKE 'image:%'", it.Replaces); err != nil {
				return st, err
			}
		}
	}
	var count int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=?`, r.ID).Scan(&count); err != nil {
		return st, err
	}
	if count > maxSayAttachments {
		return sayStaged{}, sayAttachmentRefusal("attachments_too_many", 0, "a reply carries at most 8 files, counting the files already queued for it in this run")
	}
	if err = tx.Commit(); err != nil {
		return sayStaged{}, err
	}
	for _, id := range reused {
		_ = s.store.deleteUnboundAttachment(id)
	}
	for _, id := range ids {
		a, _, err := s.store.Attachment(id)
		if err != nil {
			return st, err
		}
		st.Atts = append(st.Atts, a)
	}
	return st, nil
}

// unstageSayAttachments takes back what stageSayAttachments did: the rows it
// queued leave the run's set and the rows it removed return as they were.
func (s *Server) unstageSayAttachments(r Run, st sayStaged) {
	if len(st.inserted) == 0 && len(st.removed) == 0 {
		return
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, id := range st.inserted {
		if _, err = tx.Exec(`DELETE FROM run_attachments WHERE run_id=? AND attachment_id=?`, r.ID, id); err != nil {
			return
		}
	}
	for _, row := range st.removed {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO run_attachments(run_id,attachment_id,request_key,content_sha256,ordinal,created_at) VALUES(?,?,?,?,?,?)`, r.ID, row.AttachmentID, row.RequestKey, row.Digest, row.Ordinal, row.CreatedAt); err != nil {
			return
		}
	}
	_ = tx.Commit()
}

// attachmentSHA256 is the SHA-256 of a stored attachment's bytes: the stored
// digest, else read from the file.
func (s *Store) attachmentSHA256(id string) (string, error) {
	if d := s.attachmentDigest(id); d != "" {
		return d, nil
	}
	_, path, err := s.Attachment(id)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	f, err := s.openAttachment(ctx, path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sayAttachmentsToCount is how many files the call adds to the reply: items
// already queued for the run add none, and a replaced generated image leaves.
func (s *Store) sayAttachmentsToCount(runID string, items []sayAttachmentItem) int {
	n := s.stagedRunAttachmentCount(runID)
	for _, it := range items {
		if id := strings.TrimSpace(it.AttachmentID); id != "" {
			var staged int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=? AND attachment_id=?`, runID, id).Scan(&staged)
			if staged > 0 {
				continue
			}
		}
		n++
		if rep := strings.TrimSpace(it.Replaces); rep != "" {
			var staged int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=? AND attachment_id=? AND request_key LIKE 'image:%'`, runID, rep).Scan(&staged)
			n -= staged
		}
	}
	return n
}

// bindRunAttachmentIDsTx binds the named staged files of a run to a message
// and removes them from the run's staged set; other staged files stay.
func bindRunAttachmentIDsTx(tx *sql.Tx, runID, messageID string, ids []string) error {
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id)
		 SELECT attachment_id,? FROM run_attachments WHERE run_id=? AND attachment_id=?`, messageID, runID, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM run_attachments WHERE run_id=? AND attachment_id=?`, runID, id); err != nil {
			return err
		}
	}
	return nil
}
