package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	onboardingBotName = "New Bot"
	onboardingWelcome = "Hi! What would you like me to help with? Tell me the work you'd like me to take on."
)

// migrateOnboarding stores the client supplied creation identity separately
// from the stable Bot record. Keeping completed rows makes retries safe after
// the Bot has finished setup as well as while setup is still pending.
func migrateOnboarding(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS bot_onboarding(
client_creation_id TEXT PRIMARY KEY,
bot_id TEXT NOT NULL UNIQUE,
status TEXT NOT NULL CHECK(status IN ('pending','completed')),
created_at TEXT NOT NULL,
updated_at TEXT NOT NULL,
FOREIGN KEY(bot_id) REFERENCES bots(id) ON DELETE CASCADE);`)
	return err
}

func (s *Store) onboardingPending(botID string) (bool, error) {
	var status string
	err := s.db.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, botID).Scan(&status)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return status == "pending", nil
}

// CreateOnboardingBot creates the Bot, its canonical DM, the initial ordinary
// assistant message, its event, and the pending marker in one transaction.
// The returned duplicate flag is true when clientCreationID already exists.
func (s *Store) CreateOnboardingBot(clientCreationID, model string) (Bot, bool, error) {
	return s.CreateOnboardingBotWithReasoning(clientCreationID, model, "")
}

func (s *Store) CreateOnboardingBotWithReasoning(clientCreationID, model, reasoningEffort string) (Bot, bool, error) {
	clientCreationID = strings.TrimSpace(clientCreationID)
	parsed, err := uuid.Parse(clientCreationID)
	if err != nil || parsed == uuid.Nil {
		return Bot{}, false, errors.New("client_creation_id must be a valid UUID")
	}
	clientCreationID = parsed.String()
	if model == "" {
		return Bot{}, false, errors.New("model is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Bot{}, false, err
	}
	defer tx.Rollback()
	var deleted int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM deleted_bot_creations WHERE client_creation_id=?`, clientCreationID).Scan(&deleted); err != nil {
		return Bot{}, false, err
	}
	if deleted != 0 {
		return Bot{}, false, errors.New("this creation request belongs to a deleted Bot; start a new creation request")
	}
	var botID string
	if err = tx.QueryRow(`SELECT bot_id FROM bot_onboarding WHERE client_creation_id=?`, clientCreationID).Scan(&botID); err == nil {
		b, scanErr := scanBot(tx.QueryRow(`SELECT id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived FROM bots WHERE id=?`, botID))
		if scanErr != nil {
			return Bot{}, false, scanErr
		}
		return b, true, nil
	} else if err != sql.ErrNoRows {
		return Bot{}, false, err
	}

	id, dm := uuid.NewString(), uuid.NewString()
	t := now()
	b := Bot{ID: id, Name: onboardingBotName, Model: model, ReasoningEffort: reasoningEffort, DMConversationID: dm, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO bots(id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at) VALUES(?,?,?,?,?,?,?)`, b.ID, b.Name, b.Instructions, b.Model, b.ReasoningEffort, b.DMConversationID, b.CreatedAt); err != nil {
		return Bot{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO conversations(id,kind,name,bot_id,updated_at) VALUES(?,?,?,?,?)`, b.DMConversationID, "dm", b.Name, b.ID, t); err != nil {
		return Bot{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO members(conversation_id,bot_id) VALUES(?,?)`, b.DMConversationID, b.ID); err != nil {
		return Bot{}, false, err
	}
	if _, err = tx.Exec(`INSERT INTO bot_onboarding(client_creation_id,bot_id,status,created_at,updated_at) VALUES(?,?,?,?,?)`, clientCreationID, b.ID, "pending", t, t); err != nil {
		return Bot{}, false, err
	}
	message := Message{ID: uuid.NewString(), ConversationID: b.DMConversationID, Seq: 1, Role: "assistant", SenderBotID: b.ID, Content: onboardingWelcome, CreatedAt: t}
	if _, err = tx.Exec(`INSERT INTO messages(id,conversation_id,seq,role,kind,sender_bot_id,run_id,content,notice_data,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, message.ID, message.ConversationID, message.Seq, message.Role, message.Kind, message.SenderBotID, nil, message.Content, nil, message.CreatedAt); err != nil {
		return Bot{}, false, err
	}
	if err = insertEventTx(tx, b.DMConversationID, "message", message, t); err != nil {
		return Bot{}, false, err
	}
	if err = insertEventTx(tx, b.DMConversationID, "bot", map[string]any{"conversation_id": b.DMConversationID, "bot": b}, t); err != nil {
		return Bot{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Bot{}, false, err
	}
	return b, false, nil
}

func onboardingProfileTool(s *Server, c Conversation, r Run) (Tool, bool) {
	if !s.botProfileRunEligible(c, r) {
		return Tool{}, false
	}
	b, err := s.store.GetBot(r.BotID)
	if err != nil || b.DMConversationID != c.ID {
		return Tool{}, false
	}
	return Tool{
		Name:        "set_bot_profile",
		Description: "Set or update this Bot's concise name and durable role/persona. Use only when the user asks to set or change the profile. Omitted fields are preserved; model and permissions are unchanged.",
		Parameters: objectSchema(map[string]any{
			"name":         map[string]any{"type": "string", "description": "A concise name for this Bot; omit to preserve the current name"},
			"instructions": map[string]any{"type": "string", "description": "Durable role and persona instructions; omit to preserve the current instructions"},
		}, nil),
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			var x struct {
				Name         *string `json:"name"`
				Instructions *string `json:"instructions"`
			}
			if err := json.Unmarshal(raw, &x); err != nil {
				return "", errors.New("name or instructions is required")
			}
			if x.Name == nil && x.Instructions == nil {
				return "", errors.New("name or instructions is required")
			}
			var err error
			if x.Name != nil {
				value, e := normalizeTeamText(*x.Name, "name", maxTeamNameRunes)
				if e != nil {
					return "", e
				}
				x.Name = &value
			}
			if x.Instructions != nil {
				value, e := normalizeTeamText(*x.Instructions, "instructions", maxSystemRunes)
				if e != nil {
					return "", e
				}
				x.Instructions = &value
			}
			updated, err := s.store.setBotProfile(ctx, r, c, x.Name, x.Instructions)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(updated)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	}, true
}

// botProfileRunEligible is shared by tool exposure and the write transaction.
// A retry retains the original human trigger and is eligible; handoffs,
// scheduled runs, and external events never convey owner profile authority.
func (s *Server) botProfileRunEligible(c Conversation, r Run) bool {
	if c.Kind != "dm" || c.BotID != r.BotID || r.Kind != "" {
		return false
	}
	b, err := s.store.GetBot(r.BotID)
	if err != nil || b.DMConversationID != c.ID {
		return false
	}
	var status, convID, botID, runKind, trigger string
	err = s.store.db.QueryRow(`SELECT status,conversation_id,bot_id,kind,COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, r.ID).Scan(&status, &convID, &botID, &runKind, &trigger)
	if err != nil || status != "running" || convID != c.ID || botID != r.BotID || runKind != "" || trigger == "" {
		return false
	}
	var role, triggerKind string
	var sender sql.NullString
	err = s.store.db.QueryRow(`SELECT role,kind,sender_bot_id FROM messages WHERE id=? AND conversation_id=?`, trigger, c.ID).Scan(&role, &triggerKind, &sender)
	if err != nil || role != "user" || triggerKind != "" || sender.Valid {
		return false
	}
	external, err := s.store.webhookRunOrigin(r.ID)
	return err == nil && !external
}

// setBotProfile verifies the run's user-owned trigger and canonical DM in the
// same transaction as the profile, conversation, marker, and bot event. The
// marker is only relevant to the first assignment; later edits work for any
// Bot with a canonical DM, including Bots created without a marker.
func (s *Store) setBotProfile(ctx context.Context, r Run, c Conversation, name, instructions *string) (Bot, error) {
	if ctx == nil {
		return Bot{}, errors.New("context is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Bot{}, err
	}
	defer tx.Rollback()
	if err = ctx.Err(); err != nil {
		return Bot{}, err
	}
	var status, convID, botID, kind, trigger string
	if err = tx.QueryRow(`SELECT status,conversation_id,bot_id,kind,COALESCE(trigger_message_id,'') FROM runs WHERE id=?`, r.ID).Scan(&status, &convID, &botID, &kind, &trigger); err != nil {
		return Bot{}, err
	}
	if status != "running" || convID != c.ID || botID != r.BotID || c.Kind != "dm" || c.BotID != r.BotID || kind != "" || trigger == "" {
		return Bot{}, errors.New("run is not an active user profile run")
	}
	var convKind, convBotID string
	if err = tx.QueryRow(`SELECT kind,COALESCE(bot_id,'') FROM conversations WHERE id=?`, c.ID).Scan(&convKind, &convBotID); err != nil {
		return Bot{}, err
	}
	if convKind != "dm" || convBotID != r.BotID {
		return Bot{}, errors.New("profile requires the Bot's canonical DM")
	}
	var triggerRole, triggerKind string
	var triggerSender sql.NullString
	if err = tx.QueryRow(`SELECT role,kind,sender_bot_id FROM messages WHERE id=? AND conversation_id=?`, trigger, c.ID).Scan(&triggerRole, &triggerKind, &triggerSender); err != nil {
		return Bot{}, err
	}
	if triggerRole != "user" || triggerKind != "" || triggerSender.Valid {
		return Bot{}, errors.New("Bot profile requires a human user trigger")
	}
	if external, originErr := webhookRunOriginQuery(tx, r.ID); originErr != nil || external {
		return Bot{}, errors.New("Bot profile requires verified human run provenance")
	}
	var b Bot
	if b, err = scanBot(tx.QueryRow(`SELECT id,name,instructions,model,reasoning_effort,dm_conversation_id,created_at,archived FROM bots WHERE id=? AND dm_conversation_id=?`, r.BotID, c.ID)); err != nil {
		return Bot{}, err
	}
	var markerStatus string
	err = tx.QueryRow(`SELECT status FROM bot_onboarding WHERE bot_id=?`, b.ID).Scan(&markerStatus)
	if err != nil && err != sql.ErrNoRows {
		return Bot{}, err
	}
	if err = ctx.Err(); err != nil {
		return Bot{}, err
	}
	t := now()
	old := b
	if name != nil {
		b.Name = *name
	}
	if instructions != nil {
		b.Instructions = *instructions
	}
	profileChanged := old.Name != b.Name || old.Instructions != b.Instructions
	if markerStatus == "pending" {
		if b.Name == "" || b.Instructions == "" {
			return Bot{}, errors.New("initial Bot profile requires name and instructions")
		}
		markerUpdate, e := tx.Exec(`UPDATE bot_onboarding SET status='completed',updated_at=? WHERE bot_id=? AND status='pending'`, t, b.ID)
		if e != nil {
			return Bot{}, e
		}
		if n, _ := markerUpdate.RowsAffected(); n != 1 {
			return Bot{}, errors.New("Bot onboarding is already complete")
		}
	}
	if name == nil && instructions == nil {
		return Bot{}, errors.New("name or instructions is required")
	}
	if !profileChanged && markerStatus != "pending" {
		return b, nil
	}
	if _, err = tx.Exec(`UPDATE bots SET name=?,instructions=? WHERE id=?`, b.Name, b.Instructions, b.ID); err != nil {
		return Bot{}, err
	}
	if _, err = tx.Exec(`UPDATE conversations SET name=?,updated_at=? WHERE id=? AND kind='dm' AND bot_id=?`, b.Name, t, c.ID, b.ID); err != nil {
		return Bot{}, err
	}
	if err = insertEventTx(tx, c.ID, "bot", map[string]any{"conversation_id": c.ID, "bot": b}, t); err != nil {
		return Bot{}, fmt.Errorf("persist bot event: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return Bot{}, err
	}
	if err = tx.Commit(); err != nil {
		return Bot{}, err
	}
	return b, nil
}
