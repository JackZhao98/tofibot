package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
)

// The writers of the keys whose change class is confirm. They run only after the person approved the card (settings_gate.go), or when
// the person reverts a change from the page.

func init() {
	bindSettingWrite("account.primary_bot", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		return sr.s.store.SetPrimaryBot(v.(string))
	})

	bindSettingWrite("bot.model", func(ctx context.Context, sr settingRun, t Target, v any) error {
		model := sr.s.normalizeBotModel(v.(string))
		if err := sr.s.validateWorkspaceModel(ctx, model); err != nil {
			return err
		}
		if err := sr.s.validateModelID(ctx, model); err != nil {
			return err
		}
		if _, err := sr.s.store.UpdateBot(t.BotID, nil, nil, &model); err != nil {
			return err
		}
		if model == followGlobalModel {
			_, err := sr.s.store.UpdateBotReasoningEffort(t.BotID, followGlobalModel) // following the default model follows its effort too
			return err
		}
		return nil
	})
	bindSettingWrite("bot.reasoning_effort", func(ctx context.Context, sr settingRun, t Target, v any) error {
		effort := strings.TrimSpace(v.(string))
		if effort == "" {
			effort = followGlobalModel
		}
		bot, err := sr.s.store.GetBot(t.BotID)
		if err != nil {
			return err
		}
		if err := sr.s.validateModelChoice(ctx, bot.Model, effort); err != nil {
			return err
		}
		_, err = sr.s.store.UpdateBotReasoningEffort(t.BotID, effort)
		return err
	})
	bindSettingWrite("bot.connections", func(ctx context.Context, sr settingRun, t Target, v any) error {
		if sr.s.extensions == nil {
			return errors.New("connections are not available in this workspace")
		}
		rows, err := sr.s.botConnectionAccess(t.BotID)
		if err != nil {
			return err
		}
		open := map[string]bool{}
		for _, name := range v.([]string) {
			open[name] = true
		}
		known := map[string]bool{}
		for _, row := range rows {
			known[row.Server] = true
		}
		for name := range open {
			if !known[name] {
				return errors.New("no connection is named " + name)
			}
		}
		for _, row := range rows {
			if row.Enabled == open[row.Server] {
				continue
			}
			if err := sr.s.store.setConnectionAccess(t.BotID, row.Server, open[row.Server]); err != nil {
				return err
			}
		}
		return nil
	})
	bindSettingWrite("bot.archived", func(ctx context.Context, sr settingRun, t Target, v any) error {
		// The strict form: the Primary Assistant is never archived without a successor, and a Bot cannot name one.
		_, err := sr.s.store.SetBotArchivedReassigning(t.BotID, v.(bool), "")
		return err
	})
	bindSettingWrite("bot.delete", func(ctx context.Context, sr settingRun, t Target, _ any) error {
		return sr.deleteTarget(t.BotID, true)
	})

	bindSettingWrite("conversation.members", func(ctx context.Context, sr settingRun, t Target, v any) error {
		ids := v.([]string)
		active := map[string]bool{}
		for _, id := range sr.s.activeBotIDs() {
			active[id] = true
		}
		for _, id := range ids {
			if !active[id] {
				return errors.New("member " + id + " is not an active Bot")
			}
		}
		if len(ids) < 2 || len(ids) > 8 {
			return errors.New("a group has 2 to 8 members")
		}
		_, err := sr.s.store.UpdateGroup(t.ConversationID, GroupUpdate{BotIDs: &ids})
		return err
	})
	bindSettingWrite("conversation.archived", func(ctx context.Context, sr settingRun, t Target, v any) error {
		_, err := sr.s.store.SetGroupArchived(t.ConversationID, v.(bool))
		return err
	})
	bindSettingWrite("conversation.delete", func(ctx context.Context, sr settingRun, t Target, _ any) error {
		return sr.deleteTarget(t.ConversationID, false)
	})

	bindSettingWrite("permissions.approval_mode", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		// The mapping of the Permissions page: Ask keeps an Observe setting made under Tool rules, otherwise it is Off.
		mode := "auto"
		if v.(string) == "ask" {
			current, err := sr.s.store.getAutoReviewSettings()
			if err != nil {
				return err
			}
			mode = "off"
			if current.Mode == "shadow" {
				mode = "shadow"
			}
		}
		return sr.s.store.putAutoReviewMode(mode)
	})
	bindSettingWrite("models.default_model", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		_, _, err := sr.s.applyConversationModel(ctx, modelSettings{Model: v.(string)})
		return err
	})
	bindSettingWrite("models.default_effort", func(ctx context.Context, sr settingRun, _ Target, v any) error {
		model, _ := sr.s.modelDefaults()
		_, _, err := sr.s.applyConversationModel(ctx, modelSettings{Model: model, ReasoningEffort: v.(string)})
		return err
	})

	bindSettingWrite("connection.disconnect", func(ctx context.Context, sr settingRun, t Target, _ any) error {
		// The Settings disconnect path: local credentials go even when the provider's revocation fails, and the tools the person
		// always allowed on the connection stop being allowed (as deleteMCPServer does).
		revokeErr := sr.s.extensions.OAuthDisconnect(t.Connection)
		_, _ = sr.s.store.WorkspaceEvent(workspaceScopeConfig)
		if err := sr.s.store.revokeStandingAllowancesForServer(t.Connection); err != nil {
			return err
		}
		if revokeErr != nil && !errors.Is(revokeErr, os.ErrNotExist) {
			return errors.New("the credentials were removed here, but the provider could not be told to revoke them")
		}
		return nil
	})
}

// deleteTarget deletes a Bot or a group the way the Settings page does for a Bot run: strict about the Primary Assistant, and the
// runs that worked on the deleted tasks stop.
func (sr settingRun) deleteTarget(id string, bot bool) error {
	actor, err := sr.s.store.GetRun(sr.r.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("workspace management requires an active run")
		}
		return err
	}
	if _, err := sr.s.store.deleteWorkspaceTarget(id, bot, &actor); err != nil {
		return err
	}
	sr.s.cancelInactiveRuns()
	return nil
}
