package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// routeSkillLibrary serves the owner's library routes (decision 0058):
//
//	GET    /api/extensions/skills                       the library, bundles, who installed what
//	POST   /api/extensions/skills                       {name, files, enable?} add one skill
//	POST   /api/extensions/skill-bundles                {files, enable?} add a plugin folder (two levels)
//
// enable is "all" (every active Bot), "none" (the default) or a list of active
// Bot ids; the rows are written with the library row, in one transaction.
//
//	DELETE /api/extensions/skills/{name}                remove one skill (owner only; not a bundle member)
//	DELETE /api/extensions/skill-bundles/{id}           remove a bundle and its skills
//	POST   /api/extensions/skills/{name}/versions       {files, note} publish a new version
//	POST   /api/extensions/skills/{name}/rollback       {version} make an archived version current
//
// A Bot never reaches these: its tool calls the same functions as itself.
func (s *Server) routeSkillLibrary(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "extensions/skills" && r.Method == http.MethodGet:
		reconciled, err := s.reconcileSkillLibrary()
		if err != nil {
			writeErr(w, 500, "internal", "skill library unavailable")
			return true
		}
		skills, bundles, diags, err := s.libraryListing()
		if err != nil {
			writeErr(w, 500, "internal", "skill library unavailable")
			return true
		}
		writeJSON(w, 200, map[string]any{"skills": skills, "bundles": bundles, "diagnostics": diags, "reconciled": reconciled})
		return true
	case p == "extensions/skills" && r.Method == http.MethodPost:
		var in struct {
			Name   string            `json:"name"`
			Files  map[string]string `json:"files"`
			Enable enableSpec        `json:"enable"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", "files must be UTF-8 strings and enable must be all, none or a list of Bot ids")
			return true
		}
		enableFor, ok := s.resolveEnableSpec(w, in.Enable)
		if !ok {
			return true
		}
		if e := s.installSkill(skillActor{Kind: skillActorOwner}, in.Name, stringFiles(in.Files), "settings", enableFor...); e != nil {
			status, code, msg := skillLibraryError(e)
			if code == "skill_exists" {
				writeErr(w, 400, "extensions", msg)
			} else {
				writeErr(w, status, code, msg)
			}
		} else {
			s.writeExtensionSaved(w, http.StatusCreated)
		}
		return true
	case p == "extensions/skill-bundles" && r.Method == http.MethodPost:
		var in struct {
			Files  map[string]string `json:"files"`
			Enable enableSpec        `json:"enable"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", "files must be UTF-8 strings and enable must be all, none or a list of Bot ids")
			return true
		}
		enableFor, ok := s.resolveEnableSpec(w, in.Enable)
		if !ok {
			return true
		}
		bundle, e := extensions.ParseSkillBundle(stringFiles(in.Files))
		if e != nil {
			status, code, msg := skillLibraryError(e)
			writeErr(w, status, code, msg)
			return true
		}
		view, e := s.installSkillBundle(skillActor{Kind: skillActorOwner}, bundle, "settings", enableFor...)
		if e != nil {
			status, code, msg := skillLibraryError(e)
			writeErr(w, status, code, msg)
			return true
		}
		_, syncErr := s.store.WorkspaceEvent(workspaceScopeConfig)
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "next_run": true, "sync_pending": syncErr != nil, "bundle": view})
		return true
	case strings.HasPrefix(p, "extensions/skill-bundles/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(p, "extensions/skill-bundles/")
		if e := s.deleteSkillBundle(id); e != nil {
			status, code, msg := skillLibraryError(e)
			writeErr(w, status, code, msg)
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	case strings.HasPrefix(p, "extensions/skills/") && strings.HasSuffix(p, "/versions") && r.Method == http.MethodPost:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/skills/"), "/versions")
		var in struct {
			Files map[string]string `json:"files"`
			Note  string            `json:"note"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil {
			writeErr(w, 400, "invalid_request", "files must be UTF-8 strings")
			return true
		}
		version, e := s.publishSkillVersion(skillActor{Kind: skillActorOwner}, name, stringFiles(in.Files), in.Note)
		if e != nil {
			status, code, msg := skillLibraryError(e)
			writeErr(w, status, code, msg)
			return true
		}
		_, syncErr := s.store.WorkspaceEvent(workspaceScopeConfig)
		writeJSON(w, 200, map[string]any{"ok": true, "next_run": true, "sync_pending": syncErr != nil, "version": version})
		return true
	case strings.HasPrefix(p, "extensions/skills/") && strings.HasSuffix(p, "/rollback") && r.Method == http.MethodPost:
		name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/skills/"), "/rollback")
		var in struct {
			Version int `json:"version"`
		}
		if e := decodeExtensionJSON(r, &in); e != nil || in.Version < 1 {
			writeErr(w, 400, "invalid_request", "version must be a positive number")
			return true
		}
		if e := s.rollbackSkill(name, in.Version); e != nil {
			status, code, msg := skillLibraryError(e)
			writeErr(w, status, code, msg)
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	case strings.HasPrefix(p, "extensions/skills/") && r.Method == http.MethodDelete:
		name := strings.TrimPrefix(p, "extensions/skills/")
		if row, found, err := s.store.libraryRow(name); err == nil && found && row.BundleID != "" {
			status, code, msg := skillLibraryError(errSkillInBundle)
			writeErr(w, status, code, msg)
			return true
		}
		if e := s.deleteSkill(name); e != nil {
			status, code, msg := skillLibraryError(e)
			if code == "extensions" {
				status = 404
			}
			writeErr(w, status, code, msg)
		} else {
			s.writeExtensionSaved(w, http.StatusOK)
		}
		return true
	}
	return false
}

// enableSpec is the owner's choice of Bots at install time: "all", "none"
// (also when omitted) or a list of Bot ids.
type enableSpec struct {
	All  bool
	IDs  []string
	Kind string // "", "all", "none", "list"
}

func (e *enableSpec) UnmarshalJSON(b []byte) error {
	var word string
	if json.Unmarshal(b, &word) == nil {
		switch word {
		case "all":
			e.Kind, e.All = "all", true
		case "none":
			e.Kind = "none"
		default:
			return errors.New("enable must be all, none or a list of Bot ids")
		}
		return nil
	}
	if err := json.Unmarshal(b, &e.IDs); err != nil {
		return errors.New("enable must be all, none or a list of Bot ids")
	}
	e.Kind = "list"
	return nil
}

// resolveEnableSpec turns the choice into active Bot ids, or answers 400.
func (s *Server) resolveEnableSpec(w http.ResponseWriter, spec enableSpec) ([]string, bool) {
	if spec.Kind == "" || spec.Kind == "none" {
		return nil, true
	}
	active, err := s.store.activeBotSet()
	if err != nil {
		writeErr(w, 500, "internal", "skill library unavailable")
		return nil, false
	}
	var ids []string
	if spec.All {
		for id := range active {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return ids, true
	}
	seen := map[string]bool{}
	for _, id := range spec.IDs {
		if !active[id] {
			writeErr(w, 400, "invalid_request", "enable lists a Bot that does not exist or is archived")
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

func stringFiles(in map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for p, text := range in {
		out[p] = []byte(text)
	}
	return out
}

// handleBotSkills serves one Bot's enabled set to the owner (decision 0058):
//
//	GET /api/bots/{id}/skills                    the whole library with this Bot's choice on each skill
//	PUT /api/bots/{id}/skills/{name}             {enabled} turn one skill on or off for this Bot
//	PUT /api/bots/{id}/skill-bundles/{bundle}    {enabled} turn every skill of a bundle on or off
//
// Each change records the owner as the one who set it. A Bot changes only its
// own column, through manage_extensions, and never through these routes.
func (s *Server) handleBotSkills(w http.ResponseWriter, r *http.Request) bool {
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/bots/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || (parts[1] != "skills" && parts[1] != "skill-bundles") {
		return false
	}
	isList := parts[1] == "skills" && len(parts) == 2 && r.Method == http.MethodGet
	isSet := len(parts) == 3 && r.Method == http.MethodPut
	if !isList && !isSet {
		writeErr(w, 405, "method_not_allowed", "unsupported method")
		return true
	}
	botID := parts[0]
	if s.extensions == nil {
		writeErr(w, 503, "extensions_unavailable", "extensions unavailable")
		return true
	}
	if _, err := s.store.GetBot(botID); err != nil {
		writeErr(w, 404, "not_found", "Bot not found")
		return true
	}
	if r.Method == http.MethodGet {
		skills, bundles, err := s.botSkillStates(botID)
		if err != nil {
			writeErr(w, 500, "internal", "skills unavailable")
			return true
		}
		writeJSON(w, 200, map[string]any{"skills": skills, "bundles": bundles})
		return true
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil || in.Enabled == nil {
		writeErr(w, 400, "invalid_request", "enabled must be true or false")
		return true
	}
	target := parts[2]
	var skills []string
	if parts[1] == "skills" {
		found := extensions.IsBuiltinSkill(target)
		if !found {
			_, ok, err := s.store.libraryRow(target)
			if err != nil {
				writeErr(w, 500, "internal", "skills unavailable")
				return true
			}
			found = ok
		}
		if !found {
			writeErr(w, 404, "not_found", "skill not found")
			return true
		}
		skills = []string{target}
	} else {
		members, err := s.store.bundleMembers(target)
		if err != nil {
			writeErr(w, 500, "internal", "skills unavailable")
			return true
		}
		if len(members) == 0 {
			writeErr(w, 404, "not_found", "bundle not found")
			return true
		}
		skills = members
	}
	if err := s.store.setSkillsEnabled(botID, skills, *in.Enabled, skillSetByOwner); err != nil {
		writeErr(w, 500, "internal", "skill setting not saved")
		return true
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next_run": true, "enabled": *in.Enabled, "skills": skills})
	return true
}
