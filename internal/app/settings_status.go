package app

import (
	"context"

	"github.com/JackZhao98/tofibot/internal/manual"
)

// settingsUpdateNotice is the cached update state; tests replace it so no test reaches the network.
var settingsUpdateNotice = func() updateNotice { return sharedUpdates().snapshot() }

type statusConnection struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Link   string `json:"link"`
}

// status answers "what is my current state" from stored state only. Nothing here probes a service.
func (sr settingRun) status(ctx context.Context) (string, error) {
	s := sr.s
	lang := s.settingsLanguage()
	label := func(key string) string { return manual.Term(lang, key) }
	out := map[string]any{}

	if bot, err := s.store.GetBot(sr.r.BotID); err == nil {
		bot = s.withEffectiveModel(ctx, bot)
		item := map[string]any{"id": bot.ID, "name": bot.Name, "model": bot.Model, "model_effective": bot.EffectiveModel, "reasoning_effort_effective": bot.EffectiveReasoningEffort, "follows_default": isFollowGlobalModel(bot.Model)}
		if views, _, err := s.botSkillStates(bot.ID); err == nil {
			on := []string{}
			for _, v := range views {
				if v.Enabled {
					on = append(on, v.Name)
				}
			}
			item["skills_on"] = on
		}
		if s.extensions != nil {
			if rows, err := s.botConnectionAccess(bot.ID); err == nil {
				closed := []string{}
				for _, row := range rows {
					if !row.Enabled {
						closed = append(closed, row.Server)
					}
				}
				if len(closed) == 0 {
					item["connections_open"] = "all"
				} else {
					item["connections_closed"] = closed
				}
			}
		}
		out["bot"] = item
	}

	account := map[string]any{"default_model": s.effectiveDefaultModel(ctx), "provider": s.activeProvider(), "provider_configured": s.anyProviderConfigured()}
	if p, err := s.store.userPreferences(); err == nil {
		account["timezone"], account["language"] = p.Timezone, p.Language
	}
	if x, err := s.store.getAutoReviewSettings(); err == nil {
		account["approval_mode"] = approvalModeName(x.Mode)
	}
	account["link"] = "/settings/general"
	account["link_label"] = label("settings:shell.title")
	out["account"] = account

	if s.extensions != nil {
		conns := []statusConnection{}
		if servers, err := s.extensions.ListMCP(); err == nil {
			for _, v := range servers {
				conns = append(conns, statusConnection{Name: v.Name, Status: connectionStatusWord(v), Link: settingLinkPath(SettingDef{Link: linkConnection}, Target{Connection: v.Name})})
			}
		}
		out["connections"] = conns
	}

	caps := s.computerCapabilities()
	out["computer"] = map[string]any{"backend": caps.Backend, "available": caps.Available, "link": "/settings/computer"}

	notice := settingsUpdateNotice()
	out["version"] = map[string]any{"installed": notice.Current, "latest_stable": notice.Latest, "update_available": notice.UpdateAvailable, "link": "/settings/advanced#version"}

	pending := 0
	_ = s.store.db.QueryRow(`SELECT COUNT(*) FROM questions WHERE status=?`, questionPending).Scan(&pending)
	out["pending"] = map[string]any{"questions": pending}
	return settingsOutput(out)
}
