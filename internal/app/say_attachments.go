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
	"database/sql"
	"fmt"
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

	sayAttachmentsDescription = "Files that reach the person with this message; pictures show inline. Each item names exactly one source: attachment_id (a file of this conversation, such as an upload or a generated image; a capture_id of a screen capture of this run goes here too), path (an absolute path on the Bot computer, under /workspace), or url (a public web page or file). name sets the file name shown; replaces_attachment_id names an image generate_image made in this run, which then leaves the reply. Every regular file under /workspace/home/outbox, sub-folders included, is attached to the answer when the run ends, so keep only deliverables there and make working files in your own directory. At most 8 files per reply; a refused item refuses the whole call and nothing is attached."
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
	seen := map[string]bool{}
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

// stageSayAttachments queues the resolved files for the run's reply and
// applies replaces_attachment_id. It returns the staged attachments.
func (s *Server) stageSayAttachments(r Run, c Conversation, res sayResolution) ([]Attachment, error) {
	var out []Attachment
	for i := range res.Items {
		it := &res.Items[i]
		var staged int
		_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM run_attachments WHERE run_id=? AND attachment_id=?`, r.ID, it.Att.ID).Scan(&staged)
		a := it.Att
		if staged == 0 {
			var err error
			if a, err = s.store.stageRunAttachment(r, c, it.Att, it.Key); err != nil {
				return nil, err
			}
			it.Att = a
		}
		if it.Replaces != "" && it.Replaces != a.ID {
			if _, err := s.store.db.Exec(`DELETE FROM run_attachments WHERE run_id=? AND attachment_id=? AND request_key LIKE 'image:%'`, r.ID, it.Replaces); err != nil {
				return nil, err
			}
		}
		out = append(out, a)
	}
	return out, nil
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
