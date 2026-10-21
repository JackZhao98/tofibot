package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Built-in skills (decision 0041) are embedded in the binary by
// internal/extensions. This file holds what the app adds around them:
//
//   - the Settings switch: a row in builtin_skill_off means "turned off", so a
//     fresh workspace has every built-in skill on and a missing or unreadable
//     table never silently enables a skill the person turned off;
//   - the copy of a skill's scripts onto the Bot's computer, made when the skill
//     is read (not at context build, not for skills nobody reads).
func migrateBuiltinSkillSetting(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS builtin_skill_off(
skill_name TEXT PRIMARY KEY,
updated_at TEXT NOT NULL
)`)
	return err
}

// builtinSkillsOff is the extensions.Config.BuiltinSkillsOff hook.
func (s *Store) builtinSkillsOff(ctx context.Context) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT skill_name FROM builtin_skill_off`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) setBuiltinSkillEnabled(name string, enabled bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if enabled {
		_, err = tx.Exec(`DELETE FROM builtin_skill_off WHERE skill_name=?`, name)
	} else {
		_, err = tx.Exec(`INSERT INTO builtin_skill_off(skill_name, updated_at) VALUES(?,?) ON CONFLICT(skill_name) DO UPDATE SET updated_at=excluded.updated_at`, name, now())
	}
	if err != nil {
		return err
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// routeBuiltinSkillEnabled serves PUT /api/extensions/skills/<name>/enabled
// {"enabled": bool} for built-in skills only. The change applies from the next run.
func (s *Server) routeBuiltinSkillEnabled(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "extensions/skills/") || !strings.HasSuffix(p, "/enabled") || r.Method != http.MethodPut {
		return false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(p, "extensions/skills/"), "/enabled")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if e := decodeExtensionJSON(r, &in); e != nil || in.Enabled == nil {
		writeErr(w, 400, "invalid_request", "enabled must be true or false")
		return true
	}
	if !extensions.IsBuiltinSkill(name) {
		writeErr(w, 404, "not_found", "built-in skill not found")
		return true
	}
	if err := s.store.setBuiltinSkillEnabled(name, *in.Enabled); err != nil {
		writeErr(w, 500, "internal", "built-in skill setting not saved")
		return true
	}
	writeJSON(w, 200, map[string]any{"ok": true, "next_run": true, "enabled": *in.Enabled})
	return true
}

// guestSkillsDir is where one version of a built-in skill lives on the
// computer. It sits inside the uploads folder because that is the only place the
// guest's file import accepts, and the digest in the path means a new release
// never meets the old copy (an existing file is never overwritten).
func guestSkillsDir(name, digest string) string {
	return guestUploadsRoot + "/.skills/" + name + "/" + digest
}

// stageBuiltinSkill copies a built-in skill's files to the computer and returns
// the folder. A file already there is left alone by the guest, so a repeat
// sends one small request per file and nothing else.
func (s *Server) stageBuiltinSkill(ctx context.Context, r Run, name string) (string, error) {
	files, digest, err := extensions.BuiltinSkillFiles(name)
	if err != nil {
		return "", err
	}
	dir := guestSkillsDir(name, digest)
	for _, f := range files {
		if err := s.pushGuestBytes(ctx, r, dir+"/"+f.Path, f.Data); err != nil {
			return "", fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return dir, nil
}

// pushGuestBytes sends one small in-memory file through files.import_chunk.
func (s *Server) pushGuestBytes(ctx context.Context, r Run, dst string, data []byte) error {
	var offset int
	for {
		end := offset + guestImportChunk
		if end > len(data) {
			end = len(data)
		}
		args, _ := json.Marshal(map[string]any{"path": dst, "offset": offset, "total_size": len(data), "data_base64": base64.StdEncoding.EncodeToString(data[offset:end])})
		out, err := s.microVMAction(ctx, r, "files.import_chunk", args)
		if err != nil {
			return err
		}
		var res struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal([]byte(out), &res)
		if res.Status == "exists" || end >= len(data) {
			return nil
		}
		offset = end
	}
}

// withBuiltinSkillStaging wraps read_skill so that reading a built-in skill
// also places its scripts on the Bot's computer and tells the model where they
// are. Installed skills and Bots without a computer are untouched.
func (s *Server) withBuiltinSkillStaging(r Run, tools []Tool) []Tool {
	if s.microVM == nil {
		return tools
	}
	var mu sync.Mutex
	staged := map[string]string{}
	out := make([]Tool, len(tools))
	copy(out, tools)
	for i := range out {
		if out[i].Name != "read_skill" || out[i].Execute == nil {
			continue
		}
		inner := out[i].Execute
		out[i].Execute = func(ctx context.Context, raw json.RawMessage) (string, error) {
			body, err := inner(ctx, raw)
			if err != nil {
				return body, err
			}
			var in struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &in) != nil || !extensions.IsBuiltinSkill(in.Name) {
				return body, nil
			}
			mu.Lock()
			defer mu.Unlock()
			dir, ok := staged[in.Name]
			if !ok {
				var serr error
				if dir, serr = s.stageBuiltinSkill(ctx, r, in.Name); serr != nil {
					if ctx.Err() != nil {
						return "", ctx.Err()
					}
					log.Printf("built-in skill %s: copy to the computer failed: %v", in.Name, serr)
					return "The scripts of this skill could not be copied to the computer just now; read_skill_file shows their text, and reading the skill again retries the copy.\n\n" + body, nil
				}
				staged[in.Name] = dir
			}
			return "This skill's files are on the computer in " + dir + " (scripts under " + dir + "/scripts, run with python3).\n\n" + body, nil
		}
	}
	return out
}
