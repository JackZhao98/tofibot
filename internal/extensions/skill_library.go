package extensions

// Skill library file operations: replacing a skill's files for a new version,
// the archive of earlier versions, and bundles in the Claude Code plugin folder
// layout. The app keeps the library table beside these files; the files stay the
// source of truth for what a skill says.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	versionsDirName = ".versions"
	maxBundleSkills = 32
	pluginManifest  = ".claude-plugin/plugin.json"
	pluginSkillsDir = "skills/"
)

// SkillFilesDigest is the content digest InstalledSkillFiles reports, computed
// from an in-memory file set.
func SkillFilesDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for p := range files {
		names = append(names, p)
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, p := range names {
		fmt.Fprintf(sum, "%s\x00%d\x00", p, len(files[p]))
		sum.Write(files[p])
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// SkillFilesMap returns the files of an installed skill as a path to bytes map
// with their digest.
func (m *Manager) SkillFilesMap(name string) (map[string][]byte, string, error) {
	files, digest, err := m.InstalledSkillFiles(name)
	if err != nil {
		return nil, "", err
	}
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out, digest, nil
}

// DiffSkillFiles lists, by path, what a new file set adds, removes and changes.
func DiffSkillFiles(old, next map[string][]byte) (added, removed, changed []string) {
	for p, b := range next {
		prev, ok := old[p]
		switch {
		case !ok:
			added = append(added, p)
		case string(prev) != string(b):
			changed = append(changed, p)
		}
	}
	for p := range old {
		if _, ok := next[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

// ReplaceSkill swaps an installed skill's files for a complete new set. The new
// files are staged first and moved into place with renames, so a reader sees
// either the old skill or the new one.
func (m *Manager) ReplaceSkill(name string, files map[string][]byte) error {
	if err := validateSkillFiles(name, files); err != nil {
		return err
	}
	root := m.cfg.SkillsDir
	if strings.TrimSpace(root) == "" {
		return tooloutcome.New(tooloutcome.Permanent, "skills_unavailable", "not_executed", "Skills directory is not configured.", "explain_blocker").Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := filepath.Join(root, name)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("skill not installed")
	}
	stage, err := os.MkdirTemp(root, ".replace-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := writeSkillDir(stage, files); err != nil {
		return err
	}
	parked, err := os.MkdirTemp(root, ".old-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(parked)
	old := filepath.Join(parked, "skill")
	if err := os.Rename(dir, old); err != nil {
		return err
	}
	if err := os.Rename(stage, dir); err != nil {
		_ = os.Rename(old, dir)
		return err
	}
	return nil
}

func (m *Manager) versionDir(name string, version int) (string, error) {
	if !skillNamePattern.MatchString(name) || version < 1 || strings.TrimSpace(m.cfg.SkillsDir) == "" {
		return "", errors.New("invalid skill version")
	}
	return filepath.Join(m.cfg.SkillsDir, versionsDirName, name, strconv.Itoa(version)), nil
}

// SaveSkillVersion archives one version's files. An existing archive of that
// version is kept: versions are immutable.
func (m *Manager) SaveSkillVersion(name string, version int, files map[string][]byte) error {
	dir, err := m.versionDir(name, version)
	if err != nil {
		return err
	}
	for p := range files {
		if !validSkillInstallPath(p) {
			return errors.New("unsafe skill file path")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Lstat(dir); err == nil {
		return nil
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := writeSkillDir(stage, files); err != nil {
		return err
	}
	return os.Rename(stage, dir)
}

// ReadSkillVersion returns the archived files of one version.
func (m *Manager) ReadSkillVersion(name string, version int) (map[string][]byte, error) {
	dir, err := m.versionDir(name, version)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("skill version not archived")
	}
	defer root.Close()
	out := map[string][]byte{}
	total := 0
	err = walkRegular(root, func(p string, data []byte) error {
		if len(out) >= maxInstallFiles {
			return errors.New("skill has too many files")
		}
		if total += len(data); total > 2*maxSkillFile {
			return errors.New("skill is too large")
		}
		out[p] = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveSkillVersions deletes the archive of a skill.
func (m *Manager) RemoveSkillVersions(name string) error {
	if !skillNamePattern.MatchString(name) || strings.TrimSpace(m.cfg.SkillsDir) == "" {
		return errors.New("invalid skill name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return os.RemoveAll(filepath.Join(m.cfg.SkillsDir, versionsDirName, name))
}

// PruneSkillVersions removes archived versions that are not in keep.
func (m *Manager) PruneSkillVersions(name string, keep map[int]bool) error {
	if !skillNamePattern.MatchString(name) || strings.TrimSpace(m.cfg.SkillsDir) == "" {
		return errors.New("invalid skill name")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	base := filepath.Join(m.cfg.SkillsDir, versionsDirName, name)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		n, convErr := strconv.Atoi(e.Name())
		if convErr == nil && !keep[n] {
			if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// SkillBundle is a group of skills in the Claude Code plugin folder layout: a
// plugin folder with .claude-plugin/plugin.json and skills/<name>/SKILL.md.
// Anything else in the folder (commands, agents, hooks) is not installed. A
// bundle holds skills only, so there are two levels and no more.
type SkillBundle struct {
	Name        string
	Description string
	Version     string
	Skills      []BundleSkill
	// Ignored counts files outside skills/ that are not part of the library.
	Ignored int
}

type BundleSkill struct {
	Name  string
	Files map[string][]byte
}

// HasPluginManifest reports whether a file set is a plugin folder.
func HasPluginManifest(files map[string][]byte) bool {
	_, ok := files[pluginManifest]
	return ok
}

// ParseSkillBundle reads a plugin folder given as relative path to bytes. It
// rejects the whole bundle if anything in it is invalid.
func ParseSkillBundle(files map[string][]byte) (SkillBundle, error) {
	raw, ok := files[pluginManifest]
	if !ok {
		return SkillBundle{}, tooloutcome.InvalidArguments("a plugin folder needs .claude-plugin/plugin.json")
	}
	var manifest struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return SkillBundle{}, tooloutcome.InvalidArguments("invalid plugin.json")
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	if len([]rune(manifest.Name)) > 64 || !skillNamePattern.MatchString(manifest.Name) || strings.Contains(manifest.Name, "--") {
		return SkillBundle{}, tooloutcome.InvalidArguments("invalid plugin name")
	}
	bundle := SkillBundle{Name: manifest.Name, Description: truncateRunes(manifest.Description, 1024), Version: truncateRunes(manifest.Version, 64)}
	groups := map[string]map[string][]byte{}
	for p, data := range files {
		if !validSkillInstallPath(p) {
			return SkillBundle{}, tooloutcome.InvalidArguments("unsafe or oversized skill file path")
		}
		if p == pluginManifest {
			continue
		}
		rest, inSkills := strings.CutPrefix(p, pluginSkillsDir)
		if !inSkills {
			bundle.Ignored++
			continue
		}
		dir, file, found := strings.Cut(rest, "/")
		if !found {
			bundle.Ignored++
			continue
		}
		if groups[dir] == nil {
			groups[dir] = map[string][]byte{}
		}
		groups[dir][file] = data
	}
	if len(groups) == 0 {
		return SkillBundle{}, tooloutcome.InvalidArguments("the plugin folder has no skills")
	}
	if len(groups) > maxBundleSkills {
		return SkillBundle{}, tooloutcome.InvalidArguments("the plugin folder has too many skills")
	}
	names := make([]string, 0, len(groups))
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := validateSkillFiles(n, groups[n]); err != nil {
			return SkillBundle{}, err
		}
		bundle.Skills = append(bundle.Skills, BundleSkill{Name: n, Files: groups[n]})
	}
	return bundle, nil
}

// InstallSkillBundle installs every skill of a bundle or none. A name that is
// already in the library refuses the whole bundle.
func (m *Manager) InstallSkillBundle(b SkillBundle) error {
	root := m.cfg.SkillsDir
	if strings.TrimSpace(root) == "" {
		return tooloutcome.New(tooloutcome.Permanent, "skills_unavailable", "not_executed", "Skills directory is not configured.", "explain_blocker").Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range b.Skills {
		if _, err := os.Lstat(filepath.Join(root, s.Name)); err == nil {
			return ErrSkillExists
		}
	}
	var done []string
	for _, s := range b.Skills {
		if err := m.installSkillLocked(root, s.Name, s.Files); err != nil {
			for _, n := range done {
				_ = os.RemoveAll(filepath.Join(root, n))
			}
			return err
		}
		done = append(done, s.Name)
	}
	return nil
}

// walkRegular calls fn with the path and bytes of every regular file under
// root; links and anything else are skipped, and each file is size-bounded.
func walkRegular(root *os.Root, fn func(path string, data []byte) error) error {
	return fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
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
		return fn(p, data)
	})
}

// ValidateSkillFiles checks a complete skill file set for the given name.
func ValidateSkillFiles(name string, files map[string][]byte) error {
	return validateSkillFiles(name, files)
}
