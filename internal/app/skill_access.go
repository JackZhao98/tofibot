package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// skill_access restricts a skill to selected Bots. A skill with no rows is
// available to every Bot. Rows are kept when a Bot is archived or deleted
// (such a Bot never runs) and when a skill is reinstalled under the same name;
// they are removed only when the skill itself is deleted.
func migrateSkillAccess(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS skill_access(
skill_name TEXT NOT NULL,
bot_id TEXT NOT NULL,
PRIMARY KEY(skill_name, bot_id)
)`)
	return err
}

type skillAccessView struct {
	Mode   string   `json:"mode"`
	BotIDs []string `json:"bot_ids,omitempty"`
}

// skillAccessMap returns skill name -> sorted Bot ids for restricted skills.
func (s *Store) skillAccessMap() (map[string][]string, error) {
	rows, err := s.db.Query(`SELECT skill_name, bot_id FROM skill_access ORDER BY skill_name, bot_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var skill, bot string
		if err := rows.Scan(&skill, &bot); err != nil {
			return nil, err
		}
		out[skill] = append(out[skill], bot)
	}
	return out, rows.Err()
}

func accessViewFor(m map[string][]string, name string) skillAccessView {
	if ids := m[name]; len(ids) > 0 {
		return skillAccessView{Mode: "selected", BotIDs: ids}
	}
	return skillAccessView{Mode: "all"}
}

// skillAccessFilter is the extensions.Config.SkillAccess hook. A skill with
// no rows is open to every Bot; otherwise only listed Bots. Any error is
// returned so the caller withholds every skill rather than granting by default.
func (s *Store) skillAccessFilter(ctx context.Context, botID string, names []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	restricted, err := s.skillAccessMap()
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		ids := restricted[name]
		if len(ids) == 0 {
			allowed[name] = true
			continue
		}
		for _, id := range ids {
			if botID != "" && id == botID {
				allowed[name] = true
			}
		}
	}
	return allowed, nil
}

// setSkillAccess replaces a skill's access in one transaction and publishes a
// config workspace event. mode "all" removes every row.
func (s *Store) setSkillAccess(name, mode string, botIDs []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`DELETE FROM skill_access WHERE skill_name=?`, name); err != nil {
		return err
	}
	if mode == "selected" {
		for _, id := range botIDs {
			if _, err = tx.Exec(`INSERT INTO skill_access(skill_name, bot_id) VALUES(?,?)`, name, id); err != nil {
				return err
			}
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) deleteSkillAccess(name string) error {
	_, err := s.db.Exec(`DELETE FROM skill_access WHERE skill_name=?`, name)
	return err
}

// allActiveBots reports whether every id names an existing, non-archived Bot.
func (s *Store) allActiveBots(ids []string) (bool, error) {
	for _, id := range ids {
		var archived int
		err := s.db.QueryRow(`SELECT archived FROM bots WHERE id=?`, id).Scan(&archived)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && archived != 0) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// deleteSkill removes the skill files first and its access rows second, so a
// failure between the two can only leave a restriction behind, never lift one.
func (s *Server) deleteSkill(name string) error {
	if err := s.extensions.DeleteSkill(name); err != nil {
		return err
	}
	return s.store.deleteSkillAccess(name)
}

type skillWithAccess struct {
	extensions.SkillView
	Access skillAccessView `json:"access"`
}

func (s *Server) routeSkillAccess(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "extensions/skills/") || !strings.HasSuffix(p, "/access") || r.Method != http.MethodPut {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/skills/"), "/access")
	var in struct {
		Mode   string   `json:"mode"`
		BotIDs []string `json:"bot_ids"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil {
		writeErr(w, 400, "invalid_request", e.Error())
		return true
	}
	if in.Mode != "all" && in.Mode != "selected" {
		writeErr(w, 400, "invalid_request", "mode must be all or selected")
		return true
	}
	views, _ := s.extensions.ListSkills()
	found := false
	for _, v := range views {
		if v.Name == name {
			found = true
		}
	}
	if !found {
		writeErr(w, 404, "not_found", "skill not found")
		return true
	}
	seen := map[string]bool{}
	ids := []string{}
	if in.Mode == "selected" {
		for _, id := range in.BotIDs {
			if id = strings.TrimSpace(id); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			writeErr(w, 400, "invalid_request", "selected mode requires at least one Bot; use mode all to remove the restriction")
			return true
		}
		sort.Strings(ids)
		ok, err := s.store.allActiveBots(ids)
		if err != nil {
			writeErr(w, 500, "internal", "skill access unavailable")
			return true
		}
		if !ok {
			writeErr(w, 400, "unknown_bot", "unknown or archived Bot")
			return true
		}
	}
	if err := s.store.setSkillAccess(name, in.Mode, ids); err != nil {
		writeErr(w, 500, "internal", "skill access not saved")
		return true
	}
	view := skillAccessView{Mode: "all"}
	if in.Mode == "selected" {
		view = skillAccessView{Mode: "selected", BotIDs: ids}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next_run": true, "access": view})
	return true
}
