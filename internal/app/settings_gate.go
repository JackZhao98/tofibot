package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The confirm path. A change the registry marks confirm is executed only after a person approves a card for exactly that change. The
// card is the existing request_approval card: the guarded tool refuses with a token that holds a digest of the whole payload, the
// server remembers facts it built itself for that token, the Bot asks with the token as the target, the card shows the facts above
// the Bot's own words, and the Bot repeats the same call, which consumes the approval once (claimApproval). The Bot can neither set
// the facts nor reuse an approval for another value. The card is never standing_eligible and offers no Remember: those fields are
// set only by the MCP gate, and an AutoReview mode never answers a request_approval card.

// ApprovalSettingFacts describe a settings change on its card. Names are catalog keys or server data; nothing is model-written.
type ApprovalSettingFacts struct {
	Key        string `json:"key"`
	LabelKey   string `json:"label_key"`
	Scope      string `json:"scope"`
	TargetName string `json:"target_name,omitempty"`
	Old        string `json:"old"`
	New        string `json:"new"`
	EffectCode string `json:"effect_code"`
	Effect     string `json:"effect"`
	Link       string `json:"link"`
	Loosens    bool   `json:"loosens,omitempty"`
}

const settingTokenPrefix = "setting "

// settingChangeToken names one change. The digest covers the key, the target, the new value and, for a revert, the change reverted, so
// an approval cannot be used for a different payload.
func settingChangeToken(key string, t Target, newText, revertOf string) string {
	canonical, _ := json.Marshal([]string{key, t.BotID, t.ConversationID, t.Connection, t.Tool, newText, revertOf})
	sum := sha256.Sum256(canonical)
	where := firstNonEmpty(t.BotID, t.ConversationID, t.Connection)
	if where != "" {
		where = " " + where
		if t.Tool != "" {
			where += "/" + t.Tool
		}
	}
	return fmt.Sprintf("%s%s%s #%s", settingTokenPrefix, key, where, hex.EncodeToString(sum[:])[:12])
}

var settingEffects = map[string]string{
	"approval_mode_auto":  "The reviewer decides most tool calls without asking you, so Bots act with fewer questions.",
	"approval_mode_ask":   "Tool calls ask you first.",
	"delete_bot":          "The Bot is deleted with its chat, memory, tasks and schedules. This cannot be undone.",
	"delete_conversation": "The conversation is deleted with its history, memory, tasks and schedules. This cannot be undone.",
	"archive":             "It leaves your list. Its chats and memory are kept.",
	"restore":             "It returns to your list.",
	"disconnect":          "Bots lose access to this connection and the tools you always allowed on it stop being allowed.",
	"tier_looser":         "This tool will run with less asking.",
	"connection_access":   "The connections this Bot may use change.",
	"members":             "The group's members change. A member that leaves ends its open tasks in the group.",
	"model":               "The model or its reasoning effort changes from the next message. This changes cost and quality.",
	"primary":             "This Bot takes your general chores first.",
	"bot_identity":        "Another Bot's name, instructions or skills change.",
	"remembered_hours":    "Approvals you remember in a chat last for a different time.",
	"revert":              "The value from before the earlier change comes back.",
	"change":              "The setting changes.",
	"bot_skills":          "Another Bot's skills change.",
	"bot_avatar":          "Another Bot's avatar changes.",
	"appearance":          "The theme changes on every device.",
	"connection_connect":  "Opens the sign-in for this connection.",
}

// settingEffectCode says what the change does, in a code the UI words in the person's language and an English sentence for the rest.
func settingEffectCode(d SettingDef, t Target, oldText, newText string, revert bool, self bool) string {
	switch {
	case revert:
		return "revert"
	case d.Key == "permissions.approval_mode" && newText == "auto":
		return "approval_mode_auto"
	case d.Key == "permissions.approval_mode":
		return "approval_mode_ask"
	case d.Key == "bot.delete":
		return "delete_bot"
	case d.Key == "conversation.delete":
		return "delete_conversation"
	case d.Key == "bot.archived" || d.Key == "conversation.archived":
		if newText == "true" {
			return "archive"
		}
		return "restore"
	case d.Key == "connection.disconnect":
		return "disconnect"
	case d.Key == "connection.tool_tier":
		return "tier_looser"
	case d.Key == "bot.connections":
		return "connection_access"
	case d.Key == "conversation.members":
		return "members"
	case d.Key == "bot.model" || d.Key == "bot.reasoning_effort" || d.Key == "models.default_model" || d.Key == "models.default_effort" || strings.HasSuffix(d.Key, "_model"):
		return "model"
	case d.Key == "account.primary_bot":
		return "primary"
	case d.Key == "automation.remembered_approval_hours":
		return "remembered_hours"
	case d.Key == "bot.skills" && !self:
		return "bot_skills"
	case d.Key == "bot.avatar" && !self:
		return "bot_avatar"
	case d.Key == "account.appearance":
		return "appearance"
	case d.Scope == ScopeBot && !self:
		return "bot_identity"
	}
	return "change"
}

// settingDisplay words a stored value for the card: Bot ids become names.
func (s *Server) settingDisplay(d SettingDef, text string) string {
	botName := func(id string) string {
		if b, err := s.store.GetBot(id); err == nil {
			return b.Name
		}
		return id
	}
	switch d.Key {
	case "account.primary_bot":
		return botName(text)
	case "conversation.members", "bot.connections", "bot.skills":
		var list []string
		if json.Unmarshal([]byte(text), &list) == nil {
			if d.Key == "conversation.members" {
				for i, id := range list {
					list[i] = botName(id)
				}
			}
			return strings.Join(list, ", ")
		}
	}
	return clipNoticeValue(text)
}

func (sr settingRun) targetName(t Target) string {
	switch {
	case t.BotID != "":
		if b, err := sr.s.store.GetBot(t.BotID); err == nil {
			return b.Name
		}
	case t.ConversationID != "":
		if c, err := sr.s.store.GetConversation(t.ConversationID); err == nil {
			return c.Name
		}
	case t.Connection != "":
		if t.Tool != "" {
			return t.Connection + " · " + t.Tool
		}
		return t.Connection
	}
	return ""
}

func (sr settingRun) settingFacts(d SettingDef, t Target, oldText, newText, token string, revert, loosens bool) *ApprovalFacts {
	self := t.BotID == "" || t.BotID == sr.r.BotID
	code := settingEffectCode(d, t, oldText, newText, revert, self)
	if d.Key == "connection.tool_tier" && !loosens {
		code = "change"
	}
	return &ApprovalFacts{Kind: "setting_change", Name: d.Key, ChangeID: token[strings.LastIndex(token, "#"):], Setting: &ApprovalSettingFacts{
		Key: d.Key, LabelKey: d.LabelKey, Scope: string(d.Scope), TargetName: sr.targetName(t),
		Old: sr.s.settingDisplay(d, oldText), New: sr.s.settingDisplay(d, newText),
		EffectCode: code, Effect: settingEffects[code], Link: settingLinkPath(d, t), Loosens: loosens}}
}

// settingApprovalDenied says whether the person declined a card for exactly this change in this run. The Bot may not ask again.
func (s *Server) settingApprovalDenied(r Run, token string) bool {
	questions, err := s.store.ListQuestions(r.ConversationID)
	if err != nil {
		return false
	}
	want := approvalLabel(token)
	for _, q := range questions {
		if q.RunID == r.ID && q.Approval != nil && q.Status == questionAnswered && strings.TrimSpace(string(q.Answer)) == "false" && approvalLabel(q.Approval.Target) == want {
			return true
		}
	}
	return false
}

// gate runs apply when the person has approved exactly this change, and otherwise refuses with the token to ask about. An approval is
// given back when the change failed with the certainty that nothing changed.
func (sr settingRun) gate(token string, facts *ApprovalFacts, apply func(approvalID string) (string, error)) (string, error) {
	if sr.s.settingApprovalDenied(sr.r, token) {
		return "", settingsDenied("settings_change_denied", "The person declined this exact change in this run. Do not propose it again; ask what they want instead.")
	}
	if id, ok := sr.s.claimApproval(sr.r, token); ok {
		out, err := apply(id)
		sr.s.settleApprovalClaim(id, err)
		return out, err
	}
	sr.s.rememberExtensionFacts(sr.r, token, facts)
	return "", tooloutcome.New(tooloutcome.NeedApproval, "settings_change_needs_approval", "not_executed",
		fmt.Sprintf("This change needs the person's approval of exactly this change. Ask with request_approval, put %q in target, describe the change and its effect plainly in action and impact (the card also shows the server-verified details of the change), then repeat the same call unchanged after they approve. The approval allows one execution of this exact change.", token),
		"request_approval").Err()
}

func (sr settingRun) confirmChange(ctx context.Context, d SettingDef, t Target, value any, oldText, newText, lang string) (string, error) {
	loosens := settingLoosens(d, oldText, newText)
	token := settingChangeToken(d.Key, t, newText, "")
	facts := sr.settingFacts(d, t, oldText, newText, token, false, loosens)
	return sr.gate(token, facts, func(approvalID string) (string, error) {
		change, err := sr.applyChange(ctx, d, t, value, oldText, newText, changeActorBot, RiskConfirm, approvalID)
		if err != nil {
			return "", err
		}
		return settingsOutput(map[string]any{"applied": true, "approved": true, "key": d.Key, "old": clipNoticeValue(change.Old), "new": clipNoticeValue(change.New), "change_id": change.ID, "revertable": change.Revertable, "link": settingLinkPath(d, t), "link_label": settingLinkLabel(d, lang)})
	})
}

func (sr settingRun) confirmRevert(ctx context.Context, d SettingDef, t Target, change SettingsChange, value any, lang string) (string, error) {
	token := settingChangeToken(d.Key, t, change.Old, "revert:"+change.ID)
	facts := sr.settingFacts(d, t, change.New, change.Old, token, true, settingLoosens(d, change.New, change.Old))
	return sr.gate(token, facts, func(approvalID string) (string, error) {
		reverted, err := sr.applyChange(ctx, d, t, value, change.New, change.Old, changeActorRevert, RiskConfirm, approvalID)
		if err != nil {
			return "", err
		}
		if err := sr.s.store.markSettingsReverted(change.ID, reverted.ID); err != nil {
			return "", mapSettingsWriteError(err)
		}
		sr.s.store.markSettingsNoticeReverted(change)
		return settingsOutput(map[string]any{"applied": true, "approved": true, "reverted": change.ID, "key": d.Key, "value": clipNoticeValue(change.Old), "link": settingLinkPath(d, t), "link_label": settingLinkLabel(d, lang)})
	})
}
