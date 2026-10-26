package app

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// A Bot that hands work to another Bot can pass files along: the files bind to
// the message that delivers the brief, in whatever conversation the recipient
// reads it, so they show in its context, read_attachment opens them and they
// are staged onto its computer like any message's attachments.

const maxDelegateAttachments = 8

var attachmentIDsProp = map[string]any{"type": "array", "maxItems": maxDelegateAttachments, "items": map[string]any{"type": "string"}, "description": "Optional attachment_ids of this conversation to pass along (at most 8)."}

// checkDelegateAttachments trims and de-duplicates ids and refuses any that the
// sender's conversation cannot see, so a refused call changes nothing.
func (s *Store) checkDelegateAttachments(conv string, ids []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	if len(out) > maxDelegateAttachments {
		return nil, fmt.Errorf("at most %d attachments can be passed along", maxDelegateAttachments)
	}
	for _, id := range out {
		ok, err := s.attachmentAvailableInConversation(id, conv)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("attachment_id %s is not an attachment of this conversation", id)
		}
	}
	return out, nil
}

// bindDeliveredTx binds already checked attachments to the brief message m and
// fills m.Attachments for the event. The attachment keeps its own conversation:
// the binding is what makes it available where the brief is read.
func bindDeliveredTx(tx *sql.Tx, m *Message, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if len(ids) > maxDelegateAttachments {
		return errors.New("at most 8 attachments are allowed")
	}
	for _, id := range ids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, id, m.ID); err != nil {
			return err
		}
	}
	var err error
	m.Attachments, err = attachmentMetadata(tx, m.ID)
	return err
}
