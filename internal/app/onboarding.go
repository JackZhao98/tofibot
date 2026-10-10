package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
	"github.com/google/uuid"
)

const onboardingBotName = "New Bot"

// onboardingSystemPrompt is model-facing and stays English.
const onboardingSystemPrompt = "\nYou are onboarding a newly created self-hosted Bot in its persistent direct message. Continue as this same Bot across future runs. Use only capabilities actually connected and exposed in this run; never claim an unavailable capability. Reply in the user's language. Your opening message asked the user two things: what to call you and what you should help with. Settle both in this chat. If the user gives a name, use it exactly. If they say you should pick, or they do not care, choose a short friendly name yourself and tell them what you chose. Then confirm or ask for the role you will take on; derive a durable role/persona from their assignment. Persist both with set_bot_profile (name and instructions together) before completing setup. Until you call set_bot_profile your display name is a placeholder, so do not introduce yourself by it. Ask at most one clarification question at a time, and keep each reply short and warm. Keep the role and persona durable for future work; do not change model or permissions."

var errBotsExist = errors.New("a Bot already exists")

// onboardingWelcomes is the new Bot's opening message in each UI language. It
// asks two things in one breath: what to call the Bot (which it may pick for
// itself) and what it should help with.
var onboardingWelcomes = map[string]string{
	"en":    "Hi! First things first: what should I be called? If you'd rather not decide, just say \"you pick\" and I'll choose a name. And what would you like me to help with?",
	"zh-CN": "你好！先问一句：你想叫我什么？不想费心的话说「你来取」，我自己挑一个。另外，你希望我帮你做什么？",
	"zh-TW": "你好！先問一句：你想叫我什麼？不想費心的話說「你來取」，我自己挑一個。另外，你希望我幫你做什麼？",
	"ja":    "こんにちは！まず、私のことは何と呼びますか？決めるのが面倒なら「おまかせ」と言ってください、自分で名前を選びます。それと、どんなことを手伝いましょうか？",
	"ko":    "안녕하세요! 먼저, 저를 뭐라고 부를까요? 고르기 귀찮으면 \"알아서 정해\"라고 말해 주세요, 제가 이름을 고를게요. 그리고 어떤 일을 도와드릴까요?",
	"de":    "Hallo! Zuerst: Wie soll ich heißen? Wenn du dich nicht entscheiden magst, sag einfach \"such du aus\", dann wähle ich einen Namen. Und wobei soll ich dir helfen?",
	"fr":    "Bonjour ! D'abord, comment dois-je m'appeler ? Si tu préfères ne pas choisir, dis \"choisis toi-même\" et je prendrai un nom. Et sur quoi veux-tu que je t'aide ?",
}

// onboardingWelcome returns the opening message for a UI language, English
// when the language is unknown.
func onboardingWelcome(language string) string {
	if text, ok := onboardingWelcomes[language]; ok {
		return text
	}
	return onboardingWelcomes["en"]
}

// onboardingLocale picks the language for the opening message: the request's
// UI language, else the saved preference, else English.
func (s *Server) onboardingLocale(requested string) string {
	if requested = strings.TrimSpace(requested); uiLanguages[requested] {
		return requested
	}
	if prefs, err := s.store.userPreferences(); err == nil && uiLanguages[prefs.Language] {
		return prefs.Language
	}
	return "en"
}

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
	return s.createOnboardingBot(clientCreationID, model, reasoningEffort, "en", false)
}

// createOnboardingBot is the shared creation transaction. With onlyIfEmpty it
// refuses (errBotsExist) when the account already has any Bot, checked inside
// the same transaction so two devices cannot each create a first Bot.
func (s *Store) createOnboardingBot(clientCreationID, model, reasoningEffort, language string, onlyIfEmpty bool) (Bot, bool, error) {
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

	if onlyIfEmpty {
		var existing int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM bots`).Scan(&existing); err != nil {
			return Bot{}, false, err
		}
		if existing > 0 {
			return Bot{}, false, errBotsExist
		}
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
	message := Message{ID: uuid.NewString(), ConversationID: b.DMConversationID, Seq: 1, Role: "assistant", SenderBotID: b.ID, Content: onboardingWelcome(language), CreatedAt: t}
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
		Description: "Set or update this Bot's concise name and durable role/persona. Use only when the user asks to set or change the profile, or during initial setup. While initial setup is pending, BOTH name and instructions are required in the same call; afterwards omitted fields are preserved. Model and permissions are unchanged.",
		Local:       true,
		Parameters: objectSchema(map[string]any{
			"name":         map[string]any{"type": "string", "description": "A concise name for this Bot (required during initial setup; afterwards omit to preserve the current name)"},
			"instructions": map[string]any{"type": "string", "description": "Durable role and persona instructions (required during initial setup; afterwards omit to preserve the current instructions)"},
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
				return "", tooloutcome.InvalidArguments("name or instructions is required")
			}
			if x.Name == nil && x.Instructions == nil {
				return "", tooloutcome.InvalidArguments("name or instructions is required")
			}
			var err error
			if x.Name != nil {
				value, e := normalizeTeamText(*x.Name, "name", maxTeamNameRunes)
				if e != nil {
					return "", tooloutcome.InvalidArguments(e.Error())
				}
				x.Name = &value
			}
			if x.Instructions != nil {
				value, e := normalizeTeamText(*x.Instructions, "instructions", maxSystemRunes)
				if e != nil {
					return "", tooloutcome.InvalidArguments(e.Error())
				}
				x.Instructions = &value
			}
			updated, err := s.store.setBotProfile(ctx, r, c, x.Name, x.Instructions)
			if err != nil {
				var missing *profileMissingError
				if errors.As(err, &missing) {
					return "", tooloutcome.InvalidArguments(missing.Error())
				}
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
// A retry retains the original user trigger and is eligible; handoffs and
// scheduled runs have assistant/system triggers or a non-ordinary kind.
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
	var role string
	var sender sql.NullString
	err = s.store.db.QueryRow(`SELECT role,sender_bot_id FROM messages WHERE id=? AND conversation_id=?`, trigger, c.ID).Scan(&role, &sender)
	return err == nil && role == "user" && !sender.Valid
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
	var triggerRole string
	var triggerSender sql.NullString
	if err = tx.QueryRow(`SELECT role,sender_bot_id FROM messages WHERE id=? AND conversation_id=?`, trigger, c.ID).Scan(&triggerRole, &triggerSender); err != nil {
		return Bot{}, err
	}
	if triggerRole != "user" || triggerSender.Valid {
		return Bot{}, errors.New("Bot profile requires a user trigger")
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
		var missing []string
		if name == nil || b.Name == "" {
			missing = append(missing, "name")
		}
		if instructions == nil || b.Instructions == "" {
			missing = append(missing, "instructions")
		}
		if len(missing) > 0 {
			return Bot{}, &profileMissingError{fields: missing}
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

// profileMissingError is a pre-write refusal: initial setup needs both fields in one call.
type profileMissingError struct{ fields []string }

func (e *profileMissingError) Error() string {
	return "Initial Bot setup requires both name and instructions in one set_bot_profile call; missing: " + strings.Join(e.fields, ", ") + ". Nothing was saved. Call again with both fields."
}
