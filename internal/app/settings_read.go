package app

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// The read side of the registry: one reader per key. A reader returns the stored value from the same source the Settings page reads, so
// a result never repeats what the model asked for. No reader returns a secret, because the registry has no entry that holds one.

func init() {
	prefs := func(pick func(userPreferences) any, def any) settingReader {
		return func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
			p, err := sr.s.store.userPreferences()
			if err != nil {
				return SettingValue{}, err
			}
			return SettingValue{Value: pick(p), Default: def}, nil
		}
	}
	bindSettingRead("account.timezone", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		p, err := sr.s.store.userPreferences()
		if err != nil {
			return SettingValue{}, err
		}
		v := SettingValue{Value: p.Timezone}
		if p.Timezone == "" {
			v.Note = "not set"
		}
		return v, nil
	})
	bindSettingRead("account.language", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		p, err := sr.s.store.userPreferences()
		if err != nil {
			return SettingValue{}, err
		}
		v := SettingValue{Value: p.Language, Default: ""}
		if p.Language == "" {
			v.Note = "automatic: follows the browser language"
		}
		return v, nil
	})
	bindSettingRead("account.show_activity", prefs(func(p userPreferences) any { return p.ShowActivity }, false))
	bindSettingRead("account.notify_chats", prefs(func(p userPreferences) any { return p.NotifyChats }, true))
	bindSettingRead("account.notify_tasks", prefs(func(p userPreferences) any { return p.NotifyTasks }, true))
	bindSettingRead("account.primary_bot", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		id, err := sr.s.store.PrimaryBotID()
		if err != nil {
			return SettingValue{}, err
		}
		return SettingValue{Value: id, Options: sr.s.activeBotIDs()}, nil
	})

	botRead := func(pick func(ctx context.Context, sr settingRun, b Bot) SettingValue) settingReader {
		return func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
			b, err := sr.s.store.GetBot(t.BotID)
			if err != nil {
				return SettingValue{}, err
			}
			return pick(ctx, sr, sr.s.withEffectiveModel(ctx, b)), nil
		}
	}
	bindSettingRead("bot.name", botRead(func(_ context.Context, _ settingRun, b Bot) SettingValue { return SettingValue{Value: b.Name} }))
	bindSettingRead("bot.instructions", botRead(func(_ context.Context, _ settingRun, b Bot) SettingValue {
		return SettingValue{Value: b.Instructions}
	}))
	bindSettingRead("bot.model", botRead(func(ctx context.Context, sr settingRun, b Bot) SettingValue {
		return SettingValue{Value: b.Model, Effective: b.EffectiveModel, Default: followGlobalModel, Options: sr.s.configuredModelIDs(ctx)}
	}))
	bindSettingRead("bot.reasoning_effort", botRead(func(_ context.Context, _ settingRun, b Bot) SettingValue {
		return SettingValue{Value: firstNonEmpty(b.ReasoningEffort, followGlobalModel), Effective: b.EffectiveReasoningEffort, Default: followGlobalModel}
	}))
	bindSettingRead("bot.archived", botRead(func(_ context.Context, _ settingRun, b Bot) SettingValue {
		return SettingValue{Value: b.Archived, Default: false}
	}))
	bindSettingRead("bot.skills", func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
		views, _, err := sr.s.botSkillStates(t.BotID)
		if err != nil {
			return SettingValue{}, err
		}
		on, all := []string{}, []string{}
		for _, v := range views {
			all = append(all, v.Name)
			if v.Enabled {
				on = append(on, v.Name)
			}
		}
		return SettingValue{Value: on, Options: all}, nil
	})
	bindSettingRead("bot.connections", func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
		if sr.s.extensions == nil {
			return SettingValue{Value: []string{}}, nil
		}
		rows, err := sr.s.botConnectionAccess(t.BotID)
		if err != nil {
			return SettingValue{}, err
		}
		open, all, closed := []string{}, []string{}, 0
		for _, row := range rows {
			all = append(all, row.Server)
			if row.Enabled {
				open = append(open, row.Server)
			} else {
				closed++
			}
		}
		v := SettingValue{Value: open, Options: all}
		if closed == 0 {
			v.Note = "every connection is open to this Bot"
		}
		return v, nil
	})

	convRead := func(pick func(c Conversation, pinned bool) SettingValue) settingReader {
		return func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
			c, err := sr.s.store.GetConversation(t.ConversationID)
			if err != nil {
				return SettingValue{}, err
			}
			var pin int
			if err := sr.s.store.db.QueryRow(`SELECT pin_order FROM conversations WHERE id=?`, t.ConversationID).Scan(&pin); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return SettingValue{}, err
			}
			return pick(c, pin > 0), nil
		}
	}
	bindSettingRead("conversation.name", convRead(func(c Conversation, _ bool) SettingValue {
		v := SettingValue{Value: c.Name}
		if c.Kind != "group" {
			v.Note = "a direct message is named after its Bot"
		}
		return v
	}))
	bindSettingRead("conversation.pinned", convRead(func(_ Conversation, pinned bool) SettingValue { return SettingValue{Value: pinned, Default: false} }))
	bindSettingRead("conversation.archived", convRead(func(c Conversation, _ bool) SettingValue { return SettingValue{Value: c.Archived, Default: false} }))
	bindSettingRead("conversation.members", func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
		c, err := sr.s.store.GetConversation(t.ConversationID)
		if err != nil {
			return SettingValue{}, err
		}
		ids := append([]string{}, c.BotIDs...)
		sort.Strings(ids)
		return SettingValue{Value: ids, Options: sr.s.activeBotIDs()}, nil
	})

	bindSettingRead("permissions.approval_mode", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		x, err := sr.s.store.getAutoReviewSettings()
		if err != nil {
			return SettingValue{}, err
		}
		return SettingValue{Value: approvalModeName(x.Mode), Default: "ask", Options: []string{"ask", "auto"}}, nil
	})
	bindSettingRead("models.default_model", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		model, _ := sr.s.modelDefaults()
		return SettingValue{Value: model, Options: sr.s.configuredModelIDs(ctx)}, nil
	})
	bindSettingRead("models.default_effort", func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
		_, effort := sr.s.modelDefaults()
		return SettingValue{Value: effort}, nil
	})

	for _, item := range automationItems {
		if item.Key == autoMCPReview {
			continue
		}
		item := item
		bindSettingRead("automation."+item.Key, func(ctx context.Context, sr settingRun, _ Target) (SettingValue, error) {
			v := sr.s.automationView(ctx, item)
			out := SettingValue{Value: v.Value, Default: v.Default, Effective: v.Effective, Options: v.Options}
			if v.Locked {
				out.Note = "set by the server environment; the page cannot change it"
			}
			return out, nil
		})
	}

	bindSettingRead("connection.enabled", func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
		servers, err := sr.s.extensions.ListMCP()
		if err != nil {
			return SettingValue{}, err
		}
		for _, v := range servers {
			if v.Name == t.Connection {
				return SettingValue{Value: v.Enabled == nil || *v.Enabled, Default: true}, nil
			}
		}
		return SettingValue{}, sql.ErrNoRows
	})
	bindSettingRead("connection.tool_tier", func(ctx context.Context, sr settingRun, t Target) (SettingValue, error) {
		list := sr.s.extensions.ListToolTiers(t.Connection)
		if t.Tool == "" {
			names := make([]string, 0, len(list.Tools))
			for _, tool := range list.Tools {
				names = append(names, tool.RemoteName+": "+string(tool.Tier))
			}
			return SettingValue{Value: names, Options: []string{"read_only", "write", "high_risk"}, Note: "pass tool to read or change one tool"}, nil
		}
		for _, tool := range list.Tools {
			if tool.RemoteName == t.Tool {
				return SettingValue{Value: string(tool.Tier), Options: []string{"read_only", "write", "high_risk"}}, nil
			}
		}
		return SettingValue{}, sql.ErrNoRows
	})
}

// approvalModeName maps the stored AutoReview mode onto the two words the Permissions page shows.
func approvalModeName(mode string) string {
	if mode == "auto" {
		return "auto"
	}
	return "ask"
}

// activeBotIDs lists the Bots that are not archived.
func (s *Server) activeBotIDs() []string {
	bots, err := s.store.ListBots()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(bots))
	for _, b := range bots {
		if !b.Archived {
			ids = append(ids, b.ID)
		}
	}
	return ids
}

// configuredModelIDs lists the models the workspace can run: the default and, when the server checks models, the catalog.
func (s *Server) configuredModelIDs(ctx context.Context) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || isFollowGlobalModel(id) || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(s.effectiveDefaultModel(ctx))
	if s.strictModelValidation() {
		catalog, _, _ := s.loadModels(ctx)
		for _, option := range catalog {
			add(option.ID)
		}
	}
	sort.Strings(out)
	return out
}

// connectionStatusWord is the state the Connections page shows, from stored facts only; a call here never reaches the service.
func connectionStatusWord(v extensions.MCPServerView) string {
	switch {
	case v.Enabled != nil && !*v.Enabled:
		return "paused"
	case v.Status != nil && v.Status.ErrorCode != "":
		return "setup_needed"
	case v.OAuth != nil && !v.OAuth.Connected:
		return "needs_sign_in"
	case v.Status == nil:
		return "not_checked"
	case !v.Status.OK:
		return "cannot_connect"
	}
	return "connected"
}
