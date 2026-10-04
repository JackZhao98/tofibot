package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"
)

const messageKindWebhookEvent = "webhook_event"

var (
	errWebhookTarget      = errors.New("webhook target unavailable")
	errWebhookConflict    = errors.New("webhook event conflict")
	errWebhookCapacity    = errors.New("webhook capacity exceeded")
	errWebhookUnavailable = errors.New("webhook service unavailable")
)

type webhookEnvelope struct {
	EventID string `json:"event_id"`
	Content string `json:"content"`
}
type webhookReceipt struct {
	Accepted   bool   `json:"accepted"`
	DeliveryID string `json:"delivery_id"`
	Duplicate  bool   `json:"duplicate"`
}

func migrateWebhookIngress(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS webhook_targets(conversation_id TEXT PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE, identity TEXT NOT NULL UNIQUE);
	CREATE TABLE IF NOT EXISTS webhook_deliveries(hook_id TEXT NOT NULL,event_id TEXT NOT NULL,payload_digest BLOB NOT NULL,delivery_id TEXT NOT NULL UNIQUE,conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,message_id TEXT NOT NULL,run_id TEXT NOT NULL,accepted_at INTEGER NOT NULL,PRIMARY KEY(hook_id,event_id));
	CREATE INDEX IF NOT EXISTS webhook_delivery_time ON webhook_deliveries(accepted_at,hook_id);`)
	return err
}

// The target identity dies with the conversation. Recreating the same public
// ID cannot reactivate a grant stored in the separate account control plane.
func webhookTargetTx(tx *sql.Tx, conversationID string, create bool) (Conversation, string, runSpec, error) {
	c, err := scanConv(tx.QueryRow(`SELECT id,kind,name,bot_id,updated_at,archived,user_visible FROM conversations WHERE id=?`, conversationID))
	if err != nil || c.Archived || !c.UserVisible {
		return Conversation{}, "", runSpec{}, errWebhookTarget
	}
	var spec runSpec
	if c.Kind == "dm" {
		err = tx.QueryRow(`SELECT id,model FROM bots WHERE id=? AND dm_conversation_id=? AND archived=0`, c.BotID, c.ID).Scan(&spec.BotID, &spec.Model)
	} else if c.Kind == "group" {
		spec, err = (&Store{}).defaultGroupRunSpecTx(tx, c.ID)
		spec.Kind = runKindGroupChat
	} else {
		err = errWebhookTarget
	}
	if err != nil || requireCurrentMemberTx(tx, c.ID, spec.BotID) != nil {
		return Conversation{}, "", runSpec{}, errWebhookTarget
	}
	if create {
		if _, err = tx.Exec(`INSERT OR IGNORE INTO webhook_targets(conversation_id,identity) VALUES(?,?)`, c.ID, newID()); err != nil {
			return Conversation{}, "", runSpec{}, err
		}
	}
	var identity string
	if err = tx.QueryRow(`SELECT identity FROM webhook_targets WHERE conversation_id=?`, c.ID).Scan(&identity); err != nil {
		if err == sql.ErrNoRows && !create {
			return c, "", spec, nil
		}
		return Conversation{}, "", runSpec{}, errWebhookTarget
	}
	return c, identity, spec, nil
}

func (s *Store) webhookTarget(ctx context.Context, id string, create bool) (Conversation, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, "", err
	}
	defer tx.Rollback()
	c, identity, _, err := webhookTargetTx(tx, id, create)
	if err != nil {
		return Conversation{}, "", err
	}
	return c, identity, tx.Commit()
}

// AdmitWebhook has no human steering, question-answer, approval or input-wait
// transitions. The receipt, message, queue root and events share one commit.
func (s *Store) AdmitWebhook(ctx context.Context, endpoint webhookEndpoint, in webhookEnvelope, modelReady bool) (webhookReceipt, Run, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return webhookReceipt{}, Run{}, err
	}
	defer tx.Rollback()
	c, identity, spec, err := webhookTargetTx(tx, endpoint.ConversationID, false)
	if err != nil || identity == "" || identity != endpoint.TargetIdentity {
		return webhookReceipt{}, Run{}, errWebhookTarget
	}
	digest := sha256.Sum256([]byte("tofi-webhook-v1\x00" + in.Content))
	var saved []byte
	var receipt webhookReceipt
	var runID string
	err = tx.QueryRowContext(ctx, `SELECT payload_digest,delivery_id,run_id FROM webhook_deliveries WHERE hook_id=? AND event_id=?`, endpoint.HookID, in.EventID).Scan(&saved, &receipt.DeliveryID, &runID)
	if err == nil {
		if string(saved) != string(digest[:]) {
			return webhookReceipt{}, Run{}, errWebhookConflict
		}
		r, e := scanRun(tx.QueryRowContext(ctx, `SELECT id,conversation_id,bot_id,status,error,parent_run_id,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at FROM runs WHERE id=?`, runID))
		if e != nil {
			return webhookReceipt{}, Run{}, e
		}
		receipt.Accepted, receipt.Duplicate = true, true
		return receipt, r, tx.Commit()
	}
	if err != sql.ErrNoRows {
		return webhookReceipt{}, Run{}, err
	}
	if !modelReady {
		return webhookReceipt{}, Run{}, errWebhookUnavailable
	}
	var endpointCount, workspaceCount, pendingConversation, pendingWorkspace int
	cutoff := time.Now().Add(-time.Minute).Unix()
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN hook_id=? THEN 1 ELSE 0 END),0) FROM webhook_deliveries WHERE accepted_at>?`, endpoint.HookID, cutoff).Scan(&workspaceCount, &endpointCount); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	// Count live descendants as well as roots: a waiting child keeps its root
	// inside the budget. Finished/failed/interrupted work is never retried here.
	if err = tx.QueryRowContext(ctx, `WITH RECURSIVE family(root,id,conversation_id) AS (
	 SELECT d.run_id,d.run_id,d.conversation_id FROM webhook_deliveries d
	 UNION SELECT f.root,r.id,f.conversation_id FROM runs r JOIN family f ON r.parent_run_id=f.id
	) SELECT COUNT(DISTINCT f.root),COUNT(DISTINCT CASE WHEN f.conversation_id=? THEN f.root END)
	FROM family f JOIN runs r ON r.id=f.id WHERE r.status IN ('queued','running','waiting')`, c.ID).Scan(&pendingWorkspace, &pendingConversation); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	if endpointCount >= 6 || workspaceCount >= 30 || pendingConversation >= 20 || pendingWorkspace >= 100 {
		return webhookReceipt{}, Run{}, errWebhookCapacity
	}
	t := now()
	r := Run{ID: newID(), ConversationID: c.ID, BotID: spec.BotID, Model: spec.Model, Kind: spec.Kind, Status: "queued", OriginConversationID: c.ID, CreatedAt: t, UpdatedAt: t}
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(queue_seq),0)+1 FROM runs WHERE conversation_id=?`, c.ID).Scan(&r.QueueSeq); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	m := Message{ID: newID(), ConversationID: c.ID, Role: "user", Kind: messageKindWebhookEvent, RunID: r.ID, Content: in.Content, CreatedAt: t}
	if err = tx.QueryRowContext(ctx, nextMessageSeqSQL, c.ID, c.ID, streamDraftActive).Scan(&m.Seq); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	r.TriggerMessageID = m.ID
	if _, err = tx.ExecContext(ctx, `INSERT INTO messages(id,conversation_id,seq,role,kind,run_id,content,created_at) VALUES(?,?,?,?,?,?,?,?)`, m.ID, c.ID, m.Seq, m.Role, m.Kind, m.RunID, m.Content, t); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO runs(id,conversation_id,bot_id,status,model,kind,origin_conversation_id,trigger_message_id,queue_seq,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, r.ID, c.ID, r.BotID, r.Status, r.Model, r.Kind, c.ID, m.ID, r.QueueSeq, t, t); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	receipt = webhookReceipt{Accepted: true, DeliveryID: newID()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO webhook_deliveries(hook_id,event_id,payload_digest,delivery_id,conversation_id,message_id,run_id,accepted_at) VALUES(?,?,?,?,?,?,?,?)`, endpoint.HookID, in.EventID, digest[:], receipt.DeliveryID, c.ID, m.ID, r.ID, time.Now().Unix()); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	if err = insertEventTx(tx, c.ID, "message", m, t); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	if err = insertEventTx(tx, c.ID, "run", r, t); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conversations SET updated_at=? WHERE id=?`, t, c.ID); err != nil {
		return webhookReceipt{}, Run{}, err
	}
	return receipt, r, tx.Commit()
}
