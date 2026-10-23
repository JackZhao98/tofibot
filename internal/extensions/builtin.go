package extensions

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Built-in skills ship inside the binary: one folder per skill holding
// SKILL.md and scripts/*.py. They take the same progressive-disclosure shape as
// installed skills (a one-line description in the prompt, the body on
// read_skill, supporting files on read_skill_file) and are offered to every Bot
// until the Bot's own enabled set turns one off. They are not stored in the skills
// directory, cannot be installed over, deleted or edited there, and their
// scripts are copied to the Bot's computer when the skill is read.
//
// Only SKILL.md and scripts/*.py are embedded: the Python tests next to them
// stay out of the binary.
//
//go:embed builtin_skills/*/SKILL.md builtin_skills/*/scripts/*.py
var builtinFS embed.FS

const builtinRoot = "builtin_skills"

var (
	builtinOnce   sync.Once
	builtinSkills []Skill
	builtinIssues []Diagnostic
)

func loadBuiltin() {
	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		builtinIssues = append(builtinIssues, Diagnostic{Message: "built-in skills unreadable: " + err.Error()})
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sub, err := fs.Sub(builtinFS, path.Join(builtinRoot, entry.Name()))
		if err != nil {
			continue
		}
		data, err := fs.ReadFile(sub, "SKILL.md")
		if err != nil {
			continue
		}
		skill, err := parseSkill(data)
		if err != nil || skill.Name != entry.Name() {
			builtinIssues = append(builtinIssues, Diagnostic{Server: entry.Name(), Message: "built-in skill manifest invalid"})
			continue
		}
		skill.Builtin = true
		skill.files = sub
		builtinSkills = append(builtinSkills, skill)
	}
	sort.Slice(builtinSkills, func(i, j int) bool { return builtinSkills[i].Name < builtinSkills[j].Name })
}

// BuiltinSkills lists the skills embedded in the binary, sorted by name. Bodies
// are included; callers that only need names should use IsBuiltinSkill.
func BuiltinSkills() []Skill {
	builtinOnce.Do(loadBuiltin)
	return append([]Skill(nil), builtinSkills...)
}

// IsBuiltinSkill reports whether name is a skill embedded in the binary.
func IsBuiltinSkill(name string) bool {
	builtinOnce.Do(loadBuiltin)
	for _, s := range builtinSkills {
		if s.Name == name {
			return true
		}
	}
	return false
}

// BuiltinSkillFile is one file of a built-in skill, relative to the skill folder.
type BuiltinSkillFile struct {
	Path string
	Data []byte
	Exec bool
}

// BuiltinSkillFiles returns every file of a built-in skill in a stable order
// and a digest of their names and bytes. The digest changes whenever the
// shipped content changes, so a copy on the computer can be told from an older
// one.
func BuiltinSkillFiles(name string) ([]BuiltinSkillFile, string, error) {
	builtinOnce.Do(loadBuiltin)
	for _, s := range builtinSkills {
		if s.Name != name {
			continue
		}
		var out []BuiltinSkillFile
		err := fs.WalkDir(s.files, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := fs.ReadFile(s.files, p)
			if err != nil {
				return err
			}
			out = append(out, BuiltinSkillFile{Path: p, Data: data, Exec: strings.HasPrefix(p, "scripts/")})
			return nil
		})
		if err != nil {
			return nil, "", err
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
		sum := sha256.New()
		for _, f := range out {
			fmt.Fprintf(sum, "%s\x00%d\x00", f.Path, len(f.Data))
			sum.Write(f.Data)
		}
		return out, hex.EncodeToString(sum.Sum(nil))[:12], nil
	}
	return nil, "", errors.New("not a built-in skill")
}

// readBuiltinFile reads one file of a built-in skill under the same bounds as
// installed skill files.
func readBuiltinFile(s Skill, rel string) (string, error) {
	if !validSkillInstallPath(rel) {
		return "", errors.New("skill file path must be relative and inside the skill")
	}
	data, err := fs.ReadFile(s.files, rel)
	if err != nil {
		return "", err
	}
	if len(data) > maxSkillRead {
		return "", fmt.Errorf("file exceeds %d byte limit", maxSkillRead)
	}
	return string(data), nil
}

// ErrBuiltinSkill is returned when a built-in skill is asked to be deleted.
var ErrBuiltinSkill = errors.New("built-in skills cannot be removed; turn them off per Bot in the Bot's settings")

// loadAllSkills returns the built-in skills (when withBuiltin) and the installed
// ones, sorted by name. Only the discoverable run path and the listings ask for
// built-ins: the legacy path that inlines every skill body into the prompt does
// not. An installed skill that reuses a built-in name is dropped
// with a diagnostic, so the built-in one always wins.
func (m *Manager) loadAllSkills(metadataOnly, withBuiltin bool) ([]Skill, []Diagnostic) {
	var user []Skill
	var diags []Diagnostic
	if strings.TrimSpace(m.cfg.SkillsDir) != "" {
		user, diags = loadSkillsMode(m.cfg.SkillsDir, metadataOnly)
	}
	builtinOnce.Do(loadBuiltin)
	if !withBuiltin {
		return user, diags
	}
	out := append([]Skill(nil), builtinSkills...)
	diags = append(diags, builtinIssues...)
	for _, s := range user {
		if IsBuiltinSkill(s.Name) {
			diags = append(diags, Diagnostic{Server: s.Name, Message: fmt.Sprintf("installed skill %q shares a built-in skill's name and is ignored", s.Name)})
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

// InstalledSkillFiles returns the files of a skill a person installed, in the
// same shape and with the same content digest as BuiltinSkillFiles, so both can
// be copied to a Bot's computer. Only regular files inside the skill folder are
// read; links and anything else are skipped, and the folder is bounded.
func (m *Manager) InstalledSkillFiles(name string) ([]BuiltinSkillFile, string, error) {
	if !skillNamePattern.MatchString(name) || m.cfg.SkillsDir == "" {
		return nil, "", errors.New("not an installed skill")
	}
	root, err := os.OpenRoot(filepath.Join(m.cfg.SkillsDir, name))
	if err != nil {
		return nil, "", errors.New("not an installed skill")
	}
	defer root.Close()
	var out []BuiltinSkillFile
	total := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= maxInstallFiles {
			return errors.New("skill has too many files")
		}
		file, err := root.Open(p)
		if err != nil {
			return err
		}
		defer file.Close()
		data, err := readBounded(file, maxSkillFile)
		if err != nil {
			return err
		}
		if total += len(data); total > 2*maxSkillFile {
			return errors.New("skill is too large")
		}
		out = append(out, BuiltinSkillFile{Path: p, Data: data, Exec: strings.HasPrefix(p, "scripts/")})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	sum := sha256.New()
	for _, f := range out {
		fmt.Fprintf(sum, "%s\x00%d\x00", f.Path, len(f.Data))
		sum.Write(f.Data)
	}
	return out, hex.EncodeToString(sum.Sum(nil))[:12], nil
}
