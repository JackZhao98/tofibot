package app

import (
	"database/sql"
	"unicode/utf8"
)

// This boundary is a host fact about message bindings, not a model relevance
// judgment. File bytes are not loaded or claimed to have been reviewed.
type mcpAttachmentBoundary struct {
	CurrentMessageID string                `json:"current_native_text_message_id"`
	Omissions        []mcpUnreadAttachment `json:"unread_attachment_omissions"`
}

type mcpUnreadAttachment struct {
	ID                 string `json:"attachment_id"`
	ConversationID     string `json:"conversation_id"`
	Association        string `json:"association"`
	ContentNotProvided bool   `json:"content_not_provided"`
	Name               string `json:"untrusted_name"`
	MIME               string `json:"untrusted_mime"`
	Size               int64  `json:"size_bytes"`
	CreatedAt          string `json:"created_at"`
	MessageID          string `json:"message_id"`
	MessageSeq         int64  `json:"message_seq"`
	MessageProvenance  string `json:"host_message_provenance"`
}

func readMCPAttachmentBoundary(db reviewQuerier, conversation, trigger string, triggerSeq int64) (*mcpAttachmentBoundary, error) {
	var current int
	if err := db.QueryRow(`SELECT COUNT(*) FROM attachments a WHERE a.conversation_id=? AND EXISTS(SELECT 1 FROM attachment_messages am WHERE am.attachment_id=a.id AND am.message_id=?)`, conversation, trigger).Scan(&current); err != nil {
		return nil, mcpContextFail(mcpContextAttachmentsRead)
	}
	if current != 0 {
		return nil, mcpContextLimitFail(mcpContextNonText, current, 0, 201, false)
	}
	rows, err := db.Query(`SELECT a.id,a.name,a.mime,a.size,a.created_at,am.message_id,m.id,m.conversation_id,m.seq,`+mcpMessageProvenanceSQL+`
FROM attachments a LEFT JOIN attachment_messages am ON am.attachment_id=a.id LEFT JOIN messages m ON m.id=am.message_id
WHERE a.conversation_id=? ORDER BY a.id,m.seq,m.id LIMIT 201`, conversation)
	if err != nil {
		return nil, mcpContextFail(mcpContextAttachmentsRead)
	}
	defer rows.Close()
	boundary := &mcpAttachmentBoundary{CurrentMessageID: trigger}
	for rows.Next() {
		var a mcpUnreadAttachment
		var linked, message, owner sql.NullString
		var seq sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Name, &a.MIME, &a.Size, &a.CreatedAt, &linked, &message, &owner, &seq, &a.MessageProvenance); err != nil {
			return nil, mcpContextFail(mcpContextAttachmentsRead)
		}
		if !utf8.ValidString(a.ID) || !utf8.ValidString(a.Name) || !utf8.ValidString(a.MIME) || !utf8.ValidString(a.CreatedAt) || a.Size < 0 {
			return nil, mcpContextFail(mcpContextAttachmentsRead)
		}
		a.ConversationID, a.ContentNotProvided = conversation, true
		// An unlinked upload has unknown relevance, explicitly retained as an
		// omission. A filename or timestamp cannot make it historical evidence.
		if !linked.Valid && !message.Valid && !owner.Valid && !seq.Valid {
			a.Association = "unlinked"
		} else if !message.Valid || !owner.Valid || !seq.Valid || owner.String != conversation || seq.Int64 >= triggerSeq {
			return nil, mcpContextFail(mcpContextAttachmentScope)
		} else {
			a.Association = "earlier_message"
			a.MessageID, a.MessageSeq = message.String, seq.Int64
		}
		boundary.Omissions = append(boundary.Omissions, a)
	}
	if rows.Err() != nil {
		return nil, mcpContextFail(mcpContextAttachmentsRead)
	}
	if len(boundary.Omissions) > 200 {
		return nil, mcpContextLimitFail(mcpContextAttachmentsLimit, len(boundary.Omissions), 200, 201, true)
	}
	if len(boundary.Omissions) == 0 {
		return nil, nil // Keep packets and digests unchanged without attachments.
	}
	return boundary, nil
}
