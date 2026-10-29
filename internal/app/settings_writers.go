package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// The writers of the keys the tool can change. Each calls the method that already owns the change, so busy checks, archive semantics,
// validation and workspace events stay where they are.

func init() {
	bindSettingWrite("account.timezone", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		if _, err := sr.s.store.putUserTimezone(v.(string), false); err != nil {
			return err
		}
		if sr.s.microVM != nil {
			_ = sr.s.syncGuestTimezone(context.Background(), true) // a pending sync is retried when the next process starts
		}
		return nil
	})
	bindSettingWrite("account.language", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		return sr.s.store.putUserLanguage(v.(string))
	})
	bindSettingWrite("account.show_activity", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		return sr.s.store.putUserShowActivity(v.(bool))
	})
	bindSettingWrite("account.notify_chats", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		on := v.(bool)
		return sr.s.store.putUserNotify(&on, nil)
	})
	bindSettingWrite("account.notify_tasks", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		on := v.(bool)
		return sr.s.store.putUserNotify(nil, &on)
	})

	bindSettingWrite("bot.name", func(ctx context.Context, sr settingRun, t Target, v any) error {
		name, err := normalizeTeamText(v.(string), "name", maxTeamNameRunes)
		if err != nil {
			return err
		}
		_, err = sr.s.store.UpdateBot(t.BotID, &name, nil, nil)
		return err
	})
	bindSettingWrite("bot.instructions", func(ctx context.Context, sr settingRun, t Target, v any) error {
		text := strings.TrimSpace(v.(string))
		if len([]rune(text)) > maxSystemRunes {
			return errors.New("instructions is too long")
		}
		_, err := sr.s.store.UpdateBot(t.BotID, nil, &text, nil)
		return err
	})
	bindSettingWrite("bot.skills", func(ctx context.Context, sr settingRun, t Target, v any) error {
		if sr.s.extensions == nil {
			return errors.New("skills are not available in this workspace")
		}
		want := map[string]bool{}
		for _, name := range v.([]string) {
			want[name] = true
		}
		views, _, err := sr.s.botSkillStates(t.BotID)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		var enable, disable []string
		for _, view := range views {
			known[view.Name] = true
			switch {
			case want[view.Name] && !view.Enabled:
				enable = append(enable, view.Name)
			case !want[view.Name] && view.Enabled:
				disable = append(disable, view.Name)
			}
		}
		for name := range want {
			if !known[name] {
				return errors.New("no skill in the library is named " + name)
			}
		}
		setBy := skillSetByOwner
		if sr.r.ID != "" {
			setBy = skillSetByBot
		}
		if len(enable) > 0 {
			if err := sr.s.store.setSkillsEnabled(t.BotID, enable, true, setBy); err != nil {
				return err
			}
		}
		if len(disable) > 0 {
			return sr.s.store.setSkillsEnabled(t.BotID, disable, false, setBy)
		}
		return nil
	})

	bindSettingWrite("conversation.name", func(ctx context.Context, sr settingRun, t Target, v any) error {
		c, err := sr.s.store.GetConversation(t.ConversationID)
		if err != nil {
			return err
		}
		if c.Kind != "group" {
			return errors.New("only a group can be renamed; a direct message is named after its Bot")
		}
		name, err := normalizeTeamText(v.(string), "name", maxTeamNameRunes)
		if err != nil {
			return err
		}
		_, err = sr.s.store.UpdateGroup(t.ConversationID, GroupUpdate{Name: &name})
		return err
	})
	bindSettingWrite("conversation.pinned", func(ctx context.Context, sr settingRun, t Target, v any) error {
		pinned, err := sr.s.store.pinnedConversationIDs()
		if err != nil {
			return err
		}
		next := make([]string, 0, len(pinned)+1)
		for _, id := range pinned {
			if id != t.ConversationID {
				next = append(next, id)
			}
		}
		if v.(bool) {
			next = append(next, t.ConversationID)
		}
		if _, err := sr.s.store.SetSidebarLayout(next, nil); err != nil {
			if errors.Is(err, ErrLayoutInvalid) {
				return errors.New("the conversation cannot be pinned: it is archived, or too many conversations are pinned")
			}
			return err
		}
		return nil
	})

	for _, item := range automationItems {
		if item.Key == autoMCPReview || item.Kind == autoKindReadOnly {
			continue
		}
		item := item
		bindSettingWrite("automation."+item.Key, func(ctx context.Context, sr settingRun, _ Target, v any) error {
			if _, locked := autoEnv(item); locked {
				return settingsDenied("settings_locked_by_server", "The server environment sets this value, so the page and the tool cannot change it.")
			}
			raw, _ := json.Marshal(v)
			parsed, err := sr.s.parseAutomationValue(ctx, item, raw)
			if err != nil {
				return err
			}
			return sr.s.applyAutomationValue(ctx, item, parsed)
		})
	}

	bindSettingWrite("connection.enabled", func(ctx context.Context, sr settingRun, t Target, v any) error {
		if err := sr.s.extensions.SetMCPEnabled(t.Connection, v.(bool)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return sql.ErrNoRows
			}
			return err
		}
		sr.s.notifyMCPTiers()
		return nil
	})
	bindSettingWrite("connection.tool_tier", func(ctx context.Context, sr settingRun, t Target, v any) error {
		tier := v.(string)
		if t.Tool == "" {
			return errors.New("pass tool: the tier is set for one tool of the connection")
		}
		if _, err := sr.s.extensions.SetToolTierOverride(t.Connection, t.Tool, tier); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return errors.New("the connection has no tool with this name")
			}
			return err
		}
		if tier == string(extensions.MCPTierHighRisk) {
			if err := sr.s.store.revokeStandingAllowancesForTool(t.Connection, t.Tool, "tier_raised"); err != nil {
				return err
			}
		}
		sr.s.notifyMCPTiers()
		return nil
	})
}

// pinnedConversationIDs lists the pinned conversations in their order.
func (s *Store) pinnedConversationIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT id FROM conversations WHERE pin_order>0 ORDER BY pin_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
