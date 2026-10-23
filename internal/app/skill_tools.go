package app

import (
	"context"
	"errors"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The Skill actions of manage_extensions (decision 0058). A Bot reads the
// library, changes its own enabled set, installs a skill for itself and
// publishes new versions of skills it installed. It never names another Bot:
// the Bot id is the run's, not an argument, so another Bot's column cannot be
// reached. Delete is not a Bot action.

const skillListCap = 100

func (s *Server) applySkillAction(ctx context.Context, c Conversation, r Run, x extensionToolArgs) (string, error) {
	switch x.Action {
	case "skill_list":
		return s.skillListForBot(ctx, r, x)
	case "skill_enable", "skill_disable":
		names, ok, err := s.store.skillTarget(x.Name)
		if err != nil {
			return "", errors.New("skill library unavailable")
		}
		if !ok {
			return "", tooloutcome.InvalidArguments("No skill or bundle with this name is in the library. Use skill_list with scope library to see the names.")
		}
		if err := s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		if err := s.store.setSkillsEnabled(r.BotID, names, x.Action == "skill_enable", skillSetByBot); err != nil {
			return "", errors.New("skill setting not saved")
		}
		return extensionToolJSON(map[string]any{"ok": true, "next_run": true, "enabled": x.Action == "skill_enable", "skills": names})
	case "skill_install":
		unattended, err := mcpRunRootedInSchedule(s.store.db, r)
		if err != nil || unattended || !c.UserVisible {
			return "", tooloutcome.New(tooloutcome.Denied, "skill_install_needs_person", "not_executed",
				"Installing a skill needs a run that a person can see, and no one attends this run, so nothing was installed. Do not retry or route around it; say in your reply which skill is needed so the owner can add it in Settings.",
				"explain_blocker").Err()
		}
		if err := s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		if err := s.installSkill(s.actorFromBot(r.BotID), x.Name, stringFiles(x.Files), "bot"); err != nil {
			return "", s.skillToolError(err)
		}
		return skillChanged(s, map[string]any{"ok": true, "next_run": true, "enabled_for": "you"})
	case "skill_publish_version":
		if err := s.extensionToolActive(ctx, c, r); err != nil {
			return "", err
		}
		version, err := s.publishSkillVersion(s.actorFromBot(r.BotID), x.Name, stringFiles(x.Files), x.Note)
		if err != nil {
			return "", s.skillToolError(err)
		}
		return skillChanged(s, map[string]any{"ok": true, "next_run": true, "version": version})
	}
	return "", tooloutcome.InvalidArguments("unsupported extension action")
}

func (s *Server) skillListForBot(ctx context.Context, r Run, x extensionToolArgs) (string, error) {
	switch x.Scope {
	case "", "enabled":
		skills, diagnostics := s.extensions.SkillsForBot(ctx, r.BotID)
		return extensionToolJSON(map[string]any{"skills": skills, "diagnostic_count": len(diagnostics)})
	case "library":
		states, bundles, err := s.botSkillStates(r.BotID)
		if err != nil {
			return "", errors.New("skill library unavailable")
		}
		type item struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
			Official    bool   `json:"official,omitempty"`
			Bundle      string `json:"bundle,omitempty"`
		}
		out := make([]item, 0, len(states))
		for _, st := range states {
			desc := []rune(st.Description)
			if len(desc) > 160 {
				desc = append(desc[:160], '…')
			}
			out = append(out, item{st.Name, string(desc), st.Enabled, st.Builtin, st.BundleID})
		}
		truncated := len(out) > skillListCap
		if truncated {
			out = out[:skillListCap]
		}
		return extensionToolJSON(map[string]any{"skills": out, "bundle_count": len(bundles), "truncated": truncated})
	}
	return "", tooloutcome.InvalidArguments("scope must be enabled or library")
}

// skillToolError turns a library error into a result the model can act on.
// Other errors can carry file paths, so they get one fixed sentence.
func (s *Server) skillToolError(err error) error {
	switch {
	case errors.Is(err, extensions.ErrSkillExists):
		return tooloutcome.InvalidArguments("A skill with this name is already in the library. Turn it on with skill_enable, update it with skill_publish_version if you installed it, or choose another name.")
	case errors.Is(err, errSkillNotFound):
		return tooloutcome.InvalidArguments("No skill with this name is in the library.")
	case errors.Is(err, errSkillUnchanged):
		return tooloutcome.InvalidArguments("These files are the same as the current version. Nothing was changed.")
	case errors.Is(err, errSkillNotInstaller):
		return tooloutcome.New(tooloutcome.Denied, "skill_not_installer", "not_executed",
			"Only the Bot that installed a skill can publish its versions, so nothing was changed. Say in your reply what change is needed so the owner can make it in Settings.",
			"explain_blocker").Err()
	}
	if o, ok := tooloutcome.FromError(err); ok {
		o.Message = "Skill files were rejected before execution; check the name and SKILL.md manifest."
		return o.Err()
	}
	return errors.New("skill operation failed; check the name and the skill files")
}

// skillPublishGuard refuses a publish before any approval card: the skill must
// exist, the Bot must be the one that installed it, the files must be a valid
// skill and must differ from the current version.
func (s *Server) skillPublishGuard(botID string, x extensionToolArgs) error {
	row, found, err := s.store.libraryRow(x.Name)
	if err != nil {
		return errors.New("skill library unavailable")
	}
	if !found {
		return s.skillToolError(errSkillNotFound)
	}
	if row.InstallerKind != skillActorBot || row.InstallerBotID != botID {
		return s.skillToolError(errSkillNotInstaller)
	}
	files := stringFiles(x.Files)
	if err := extensions.ValidateSkillFiles(x.Name, files); err != nil {
		return s.skillToolError(err)
	}
	preview, err := s.previewSkillPublish(x.Name, files)
	if err != nil {
		return s.skillToolError(err)
	}
	if preview.Unchanged {
		return s.skillToolError(errSkillUnchanged)
	}
	return nil
}

// skillChanged publishes the workspace config event for a committed change and
// reports a failed publish truthfully.
func skillChanged(s *Server, result map[string]any) (string, error) {
	_, err := s.store.WorkspaceEvent(workspaceScopeConfig)
	result["sync_pending"] = err != nil
	return extensionToolJSON(result)
}
