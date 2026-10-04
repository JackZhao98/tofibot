package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
)

func (s *Store) applyPortable(ctx context.Context, b portableBundle, previewID string) (portableResult, error) {
	result, _, err := s.applyPortableWithState(ctx, b, previewID)
	return result, err
}

// The replay bit is internal, so the immutable receipt remains byte-identical.
func (s *Store) applyPortableWithState(ctx context.Context, b portableBundle, previewID string) (portableResult, bool, error) {
	return s.applyPortableWithStateGuard(ctx, b, previewID, nil)
}
func (s *Store) applyPortableSQL(ctx context.Context, b portableBundle, previewID string, assetTargets map[string]string) (portableResult, bool, error) {
	if err := b.validate(); err != nil {
		return portableResult{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return portableResult{}, false, err
	}
	defer tx.Rollback()
	previous, replayed, err := s.portablePreviewState(ctx, tx, b, previewID)
	if err != nil || replayed {
		return previous, replayed, err
	}
	result := portableResult{ImportID: previewID, Counts: b.counts(), IDMap: map[string]string{}}
	newID := func(id string) { result.IDMap[id] = uuid.NewString() }
	if err = portableRecoveryCapacity(ctx, tx, b); err != nil {
		return result, false, err
	}
	for _, x := range b.VaultEnvironment {
		newID(x.ID)
	}
	for _, x := range b.Bots {
		newID(x.ID)
	}
	for _, x := range b.Conversations {
		newID(x.ID)
	}
	for _, x := range b.Messages {
		newID(x.ID)
	}
	for _, x := range b.Memories {
		newID(x.ID)
	}
	for _, x := range b.Schedules {
		newID(x.ID)
	}
	for _, x := range b.Attachments {
		target := assetTargets[x.ID]
		if !portableID(target) {
			return result, false, errPortableStorage
		}
		result.IDMap[x.ID] = target
	}
	for _, x := range b.MissingAttachments {
		newID(x.ID)
	}
	id := func(old string) string { return result.IDMap[old] }
	provenance := func(kind, old string, o portableOrigin) error {
		if o.RecordID == "" {
			o.RecordID = old
		}
		if o.InstanceID == "" {
			o.InstanceID = b.SourceInstance
		}
		raw, _ := json.Marshal(o)
		_, e := tx.ExecContext(ctx, `INSERT INTO portability_provenance(kind,target_id,source_json) VALUES(?,?,?)`, kind, id(old), string(raw))
		return e
	}
	for _, x := range b.Bots {
		if _, err = tx.ExecContext(ctx, `INSERT INTO bots(id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived) VALUES(?,?,?,?,?,?,?,?)`, id(x.ID), x.Name, x.Instructions, x.Model, x.ReasoningEffort, id(x.DMConversationID), x.CreatedAt, x.Archived); err != nil {
			return result, false, err
		}
		if err = provenance("bot", x.ID, x.Origin); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.Conversations {
		if _, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,kind,name,bot_id,updated_at,archived,user_visible) VALUES(?,?,?,?,?,?,?)`, id(x.ID), x.Kind, x.Name, nullString(id(x.BotID)), x.UpdatedAt, x.Archived, x.UserVisible); err != nil {
			return result, false, err
		}
		for _, member := range x.BotIDs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, id(x.ID), id(member)); err != nil {
				return result, false, err
			}
		}
	}
	for _, x := range b.Messages {
		kind := x.Kind
		if kind == messageKindWebhookEvent || x.Origin.Kind == messageKindWebhookEvent {
			// This discriminator only restricts imported history. The durable
			// import marker and NULL run link below remain mandatory.
			kind = messageKindWebhookEvent
		} else if kind != "" && kind != "progress" && kind != "segment" {
			kind = "imported_history"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,created_at) VALUES(?,?,?,?,?,?,NULL,?,?)`, id(x.ID), id(x.ConversationID), x.Seq, x.Role, kind, nullString(id(x.SenderBotID)), x.Content, x.CreatedAt); err != nil {
			return result, false, err
		}
		if err = provenance("message", x.ID, x.Origin); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.Memories {
		if _, err = tx.ExecContext(ctx, `INSERT INTO memories(id,conversation_id,bot_id,content,title,description,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, id(x.ID), id(x.ConversationID), nullString(id(x.BotID)), x.Content, x.Title, x.Description, x.Revision, x.CreatedAt, x.UpdatedAt); err != nil {
			return result, false, err
		}
		if err = provenance("memory", x.ID, x.Origin); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.Schedules {
		if _, err = tx.ExecContext(ctx, `INSERT INTO schedules(id,conversation_id,bot_id,content,title,description,created_by,kind,timezone,next_at_utc,interval_seconds,daily_time,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'paused',?,?)`, id(x.ID), id(x.ConversationID), id(x.BotID), x.Content, x.Title, x.Description, x.CreatedBy, x.Kind, x.Timezone, x.NextAtUTC, x.IntervalSeconds, x.DailyTime, x.CreatedAt, x.UpdatedAt); err != nil {
			return result, false, err
		}
		origin := x.Origin
		if origin.Status == "" {
			origin.Status = x.Status
		}
		if err = provenance("schedule", x.ID, origin); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.Attachments {
		var digest string
		var size int64
		if err = tx.QueryRowContext(ctx, `SELECT sha256,size FROM portability_asset_staging WHERE import_id=? AND target_id=?`, previewID, id(x.ID)).Scan(&digest, &size); err != nil || digest != x.SHA256 || size != x.Size {
			return result, false, errPortableStorage
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO attachments(id,conversation_id,name,mime,size,disk_name,created_at) VALUES(?,?,?,?,?,?,?)`, id(x.ID), id(x.ConversationID), x.Name, x.MIME, x.Size, guestAttachmentPrefix+id(x.ID), x.CreatedAt); err != nil {
			return result, false, err
		}
		if err = provenance("attachment", x.ID, x.Origin); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.MissingAttachments {
		old := x.ID
		x.ID, x.ConversationID = id(x.ID), id(x.ConversationID)
		if x.Origin.RecordID == "" {
			x.Origin.RecordID = old
		}
		if x.Origin.InstanceID == "" {
			x.Origin.InstanceID = b.SourceInstance
		}
		mappedMessages := []string{}
		for _, oldMessage := range x.MessageIDs {
			mappedMessages = append(mappedMessages, id(oldMessage))
		}
		x.MessageIDs = mappedMessages
		raw, _ := json.Marshal(x)
		if _, err = tx.ExecContext(ctx, `INSERT INTO portability_missing_assets(id,conversation_id,metadata_json) VALUES(?,?,?)`, x.ID, x.ConversationID, string(raw)); err != nil {
			return result, false, err
		}
	}
	for _, x := range b.AttachmentBindings {
		if _, err = tx.ExecContext(ctx, `INSERT INTO attachment_messages(attachment_id,message_id) VALUES(?,?)`, id(x.AttachmentID), id(x.MessageID)); err != nil {
			return result, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM portability_asset_staging WHERE import_id=?`, previewID); err != nil {
		return result, false, err
	}
	if x := b.Settings; x != nil {
		for _, op := range []struct {
			q    string
			args []any
		}{
			{`INSERT INTO user_preferences(id,timezone,updated_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET timezone=excluded.timezone,updated_at=excluded.updated_at`, []any{x.Timezone, now()}},
			{`INSERT INTO model_settings(id,model,reasoning_effort,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET model=excluded.model,reasoning_effort=excluded.reasoning_effort,updated_at=excluded.updated_at`, []any{x.Model, x.ReasoningEffort, now()}},
			{`INSERT INTO dictation_settings(id,model,updated_at) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET model=excluded.model,updated_at=excluded.updated_at`, []any{x.DictationModel, now()}},
		} {
			if _, err = tx.ExecContext(ctx, op.q, op.args...); err != nil {
				return result, false, err
			}
		}
	}
	for _, x := range b.VaultEnvironment {
		sourceID := x.ID
		x.ID = id(sourceID)
		clear, e := json.Marshal(x)
		if e != nil {
			return result, false, errPortableSecret
		}
		capsule, e := s.portabilitySecrets.seal("recovery", x.ID, clear)
		if e != nil {
			return result, false, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO portability_secret_recovery(id,import_id,source_id,capsule,size,created_at) VALUES(?,?,?,?,?,?)`, x.ID, previewID, sourceID, capsule, len(x.Value), now()); err != nil {
			return result, false, err
		}
	}
	for _, scope := range []string{workspaceScopeBots, workspaceScopeGroups, workspaceScopeConfig} {
		if err = insertWorkspaceEventTx(tx, scope, now()); err != nil {
			return result, false, err
		}
	}
	raw, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, `UPDATE portability_imports SET status='applied',result_json=? WHERE id=? AND status='preview'`, string(raw), previewID); err != nil {
		return result, false, err
	}
	return result, false, tx.Commit()
}
