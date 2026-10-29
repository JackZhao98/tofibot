package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Built-in skills (decision 0041) are embedded in the binary by
// internal/extensions. This file holds what the app adds around them: the copy
// of a skill's scripts onto the Bot's computer, made when the skill is read
// (not at context build, not for skills nobody reads). Whether a Bot has a
// built-in skill on is its row in skill_enablement (skill_library.go): on
// unless that Bot turned it off.

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
	return s.stageSkillFiles(ctx, r, name, files, digest)
}

func (s *Server) stageSkillFiles(ctx context.Context, r Run, name string, files []extensions.BuiltinSkillFile, digest string) (string, error) {
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

// withBuiltinSkillStaging wraps read_skill so that reading a skill that has
// files besides SKILL.md (a built-in skill, or one a person installed) also
// places them on the Bot's computer and tells the model where they are. An
// installed skill that is only SKILL.md and Bots without a computer are
// untouched. A skill's files are inert until the Bot chooses to run them.
func (s *Server) withBuiltinSkillStaging(r Run, tools []Tool) []Tool {
	if s.computer() == nil {
		return s.withNoComputerSkillNote(tools)
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
			if json.Unmarshal(raw, &in) != nil {
				return body, nil
			}
			builtin := extensions.IsBuiltinSkill(in.Name)
			var installed []extensions.BuiltinSkillFile
			var installedDigest string
			if !builtin {
				var ierr error
				installed, installedDigest, ierr = s.extensions.InstalledSkillFiles(in.Name)
				if ierr != nil || len(installed) < 2 {
					return body, nil
				}
			}
			mu.Lock()
			defer mu.Unlock()
			dir, ok := staged[in.Name]
			if !ok {
				var serr error
				if builtin {
					dir, serr = s.stageBuiltinSkill(ctx, r, in.Name)
				} else {
					dir, serr = s.stageSkillFiles(ctx, r, in.Name, installed, installedDigest)
				}
				if serr != nil {
					if ctx.Err() != nil {
						return "", ctx.Err()
					}
					log.Printf("skill %s: copy to the computer failed: %v", in.Name, serr)
					return "The scripts of this skill could not be copied to the computer just now; read_skill_file shows their text, and reading the skill again retries the copy.\n\n" + body, nil
				}
				staged[in.Name] = dir
			}
			if !builtin {
				return "This skill's files are on the computer in " + dir + ".\n\n" + body, nil
			}
			return "This skill's files are on the computer in " + dir + " (scripts under " + dir + "/scripts, run with python3).\n\n" + body, nil
		}
	}
	return out
}

// noComputerSkillNote leads the text of a skill whose scripts need a computer
// when the workspace has none.
const noComputerSkillNote = "This Bot has no computer, so this skill's scripts, rendering and verification steps cannot run. Do only what needs no computer, deliver the result as text in the chat, and say plainly which steps could not be done.\n\n"

// withNoComputerSkillNote makes read_skill state that a skill with files
// beyond SKILL.md cannot run its scripts here.
func (s *Server) withNoComputerSkillNote(tools []Tool) []Tool {
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
			if json.Unmarshal(raw, &in) != nil {
				return body, nil
			}
			if !extensions.IsBuiltinSkill(in.Name) {
				if files, _, ierr := s.extensions.InstalledSkillFiles(in.Name); ierr != nil || len(files) < 2 {
					return body, nil
				}
			}
			return noComputerSkillNote + body, nil
		}
	}
	return out
}
