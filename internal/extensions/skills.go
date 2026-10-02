package extensions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/runtime"
	"gopkg.in/yaml.v3"
)

const (
	maxSkillFile = 1 << 20
	maxSkillRead = 100_000
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body,omitempty"`
	Path        string `json:"-"`
}

type skillManifest struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func loadSkills(root string) ([]Skill, []Diagnostic) { return loadSkillsMode(root, false) }
func loadSkillsMode(root string, metadataOnly bool) ([]Skill, []Diagnostic) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("skills root: %v", err)}}
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("skills root: %v", err)}}
	}
	entries, err := os.ReadDir(rootReal)
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("read skills root: %v", err)}}
	}
	rootHandle, err := os.OpenRoot(rootReal)
	if err != nil {
		return nil, []Diagnostic{{Message: fmt.Sprintf("open skills root: %v", err)}}
	}
	defer rootHandle.Close()
	var out []Skill
	var diags []Diagnostic
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: "skill directory symlink is not allowed"})
			continue
		}
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(rootReal, entry.Name())
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil || !within(rootReal, realDir) {
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: "skill directory escapes configured root"})
			continue
		}
		relPath := filepath.Join(entry.Name(), "SKILL.md")
		file, err := rootHandle.Open(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: err.Error()})
			continue
		}
		var data []byte
		if metadataOnly {
			data, err = readSkillHeader(file)
		} else {
			data, err = readBounded(file, maxSkillFile)
		}
		file.Close()
		if err != nil {
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: fmt.Sprintf("read SKILL.md: %v", err)})
			continue
		}
		skill, err := parseSkill(data)
		if err != nil {
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: err.Error()})
			continue
		}
		if seen[skill.Name] {
			diags = append(diags, Diagnostic{Server: entry.Name(), Message: fmt.Sprintf("duplicate skill name %q", skill.Name)})
			continue
		}
		seen[skill.Name] = true
		skill.Path = realDir
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, diags
}

func parseSkill(data []byte) (Skill, error) {
	text := strings.TrimSpace(string(data))
	if !strings.HasPrefix(text, "---") {
		return Skill{}, errors.New("SKILL.md must start with YAML frontmatter")
	}
	rest := strings.TrimPrefix(text, "---")
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		return Skill{}, errors.New("SKILL.md frontmatter is incomplete")
	}
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return Skill{}, errors.New("SKILL.md missing closing frontmatter delimiter")
	}
	var manifest skillManifest
	if err := yaml.Unmarshal([]byte(rest[:end]), &manifest); err != nil {
		return Skill{}, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Description = strings.TrimSpace(manifest.Description)
	if manifest.Name == "" {
		return Skill{}, errors.New("skill name is required")
	}
	if len([]rune(manifest.Name)) > 64 || !skillNamePattern.MatchString(manifest.Name) || strings.Contains(manifest.Name, "--") {
		return Skill{}, fmt.Errorf("invalid skill name %q", manifest.Name)
	}
	if manifest.Description == "" {
		return Skill{}, errors.New("skill description is required")
	}
	if len([]rune(manifest.Description)) > 1024 {
		return Skill{}, errors.New("skill description exceeds 1024 characters")
	}
	body := strings.TrimSpace(rest[end+4:])
	return Skill{Name: manifest.Name, Description: manifest.Description, Body: body}, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func skillTools(skills []Skill, max int) []runtime.Tool { return skillToolsMode(skills, max, false) }
func skillToolsMode(skills []Skill, max int, lazy bool) []runtime.Tool {
	byName := map[string]Skill{}
	for _, s := range skills {
		byName[s.Name] = s
	}
	list := runtime.Tool{Name: "list_skills", Description: "List configured read-only skills", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}, Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if lazy {
			if err := strictDiscoveryJSON(args, &struct{}{}); err != nil {
				return "", err
			}
		}
		type item struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		out := make([]item, 0, len(skills))
		for _, s := range skills {
			out = append(out, item{s.Name, s.Description})
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}}
	read := runtime.Tool{Name: "read_skill", Description: "Read a configured skill's instructions", Parameters: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}, "required": []string{"name"}}, Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Name string `json:"name"`
		}
		decode := json.Unmarshal
		if lazy {
			decode = strictDiscoveryJSON
		}
		if err := decode(args, &in); err != nil {
			return "", err
		}
		s, ok := byName[in.Name]
		if !ok {
			return "", fmt.Errorf("skill %q not found", in.Name)
		}
		if lazy {
			root, err := os.OpenRoot(s.Path)
			if err != nil {
				return "", err
			}
			defer root.Close()
			file, err := root.Open("SKILL.md")
			if err != nil {
				return "", err
			}
			defer file.Close()
			data, err := readBounded(file, maxSkillFile)
			if err != nil {
				return "", err
			}
			current, err := parseSkill(data)
			if err != nil {
				return "", err
			}
			if current.Name != s.Name {
				return "", errors.New("skill manifest changed; prepare a new run")
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return boundSkill(current.Body, max), nil
		}
		return boundSkill(s.Body, max), nil
	}}
	readFile := runtime.Tool{Name: "read_skill_file", Description: "Read a file within a configured skill directory", Parameters: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"}}, "required": []string{"name", "path"}}, Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var in struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		decode := json.Unmarshal
		if lazy {
			decode = strictDiscoveryJSON
		}
		if err := decode(args, &in); err != nil {
			return "", err
		}
		s, ok := byName[in.Name]
		if !ok {
			return "", fmt.Errorf("skill %q not found", in.Name)
		}
		if filepath.IsAbs(in.Path) {
			return "", errors.New("skill file path must be relative")
		}
		root, err := os.OpenRoot(s.Path)
		if err != nil {
			return "", err
		}
		defer root.Close()
		file, err := root.Open(in.Path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		b, err := readBounded(file, maxSkillRead)
		return string(b), err
	}}
	return []runtime.Tool{list, read, readFile}
}

func readBounded(file *os.File, max int64) ([]byte, error) {
	if max < 0 {
		return nil, errors.New("negative read limit")
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("skill file is not regular")
	}
	b, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("file exceeds %d byte limit", max)
	}
	return b, nil
}

func boundSkill(s string, max int) string {
	if max <= 0 || max > maxSkillRead {
		max = maxSkillRead
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "\n[truncated]"
	}
	return s
}

// Read only frontmatter for a discoverable run. Bodies stay on disk until read_skill.
func readSkillHeader(file *os.File) ([]byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSkillFile {
		return nil, errors.New("invalid or oversized skill file")
	}
	scan := bufio.NewScanner(io.LimitReader(file, 16<<10))
	scan.Buffer(make([]byte, 1024), 16<<10)
	var header strings.Builder
	started := false
	for scan.Scan() {
		line := scan.Text()
		if !started && strings.TrimSpace(line) == "" {
			continue
		}
		if !started {
			if strings.TrimSpace(line) != "---" {
				return nil, errors.New("SKILL.md must start with YAML frontmatter")
			}
			started = true
			header.WriteString("---\n")
			continue
		}
		header.WriteString(line)
		header.WriteByte('\n')
		if strings.TrimSpace(line) == "---" {
			return []byte(header.String()), nil
		}
	}
	return nil, errors.New("missing or oversized skill frontmatter")
}
