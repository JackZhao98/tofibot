package extensions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var builtinNames = []string{"code-delivery", "data-chart", "document-report", "docx-edit", "email", "file-convert", "image-edit", "install-software", "pdf", "slides", "spreadsheet", "video", "video-edit", "web-page", "web-research", "web-tasks", "web-to-sheet"}

func prepareBuiltin(t *testing.T, cfg Config, bot string) *Prepared {
	t.Helper()
	p, err := NewManager(cfg).PrepareDiscoverableForBot(context.Background(), bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestBuiltinSkillsShipWithTheBinary(t *testing.T) {
	got := BuiltinSkills()
	if len(got) != len(builtinNames) {
		t.Fatalf("built-in skills=%d want %d", len(got), len(builtinNames))
	}
	for i, s := range got {
		if s.Name != builtinNames[i] || !s.Builtin || s.Body == "" || strings.Contains(s.Description, "\n") || len([]rune(s.Description)) > 100 {
			t.Fatalf("skill %d = %+v", i, s)
		}
		if !IsBuiltinSkill(s.Name) {
			t.Fatalf("%s not reported as built-in", s.Name)
		}
		files, digest, err := BuiltinSkillFiles(s.Name)
		if err != nil || len(digest) != 12 {
			t.Fatalf("%s files err=%v digest=%q", s.Name, err, digest)
		}
		hasManifest, hasScript := false, false
		for _, f := range files {
			hasManifest = hasManifest || f.Path == "SKILL.md"
			hasScript = hasScript || (strings.HasPrefix(f.Path, "scripts/") && strings.HasSuffix(f.Path, ".py") && f.Exec && len(f.Data) > 200)
			if strings.Contains(f.Path, "test_") || strings.Contains(f.Path, "__pycache__") {
				t.Fatalf("%s ships %s", s.Name, f.Path)
			}
		}
		// Only deliverable Skills ship a script.
		if !hasManifest || (!hasScript && s.Kind == "deliverable") {
			t.Fatalf("%s manifest=%v script=%v", s.Name, hasManifest, hasScript)
		}
		_, again, _ := BuiltinSkillFiles(s.Name)
		if again != digest {
			t.Fatal("digest is not stable")
		}
	}
	if IsBuiltinSkill("nonexistent") {
		t.Fatal("unknown name reported built-in")
	}
	if _, _, err := BuiltinSkillFiles("nonexistent"); err == nil {
		t.Fatal("files for an unknown skill")
	}
}

func TestBuiltinSkillsAreOfferedToEveryBotAsOneLineDescriptions(t *testing.T) {
	p := prepareBuiltin(t, Config{}, "any-bot")
	for _, name := range builtinNames {
		if !strings.Contains(p.Instructions, `"name":"`+name+`"`) {
			t.Fatalf("directory lacks %s: %q", name, p.Instructions)
		}
	}
	// Progressive disclosure: no skill body is in the prompt text.
	for _, s := range BuiltinSkills() {
		if strings.Contains(p.Instructions, "## Principles") || strings.Contains(p.Instructions, s.Body[:60]) {
			t.Fatalf("body of %s leaked into the prompt", s.Name)
		}
	}
	list, err := discoveryTool(t, p, "list_skills").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(list, "spreadsheet") {
		t.Fatalf("list=%q err=%v", list, err)
	}
	if views, _ := NewManager(Config{}).SkillsForBot(context.Background(), "any-bot"); len(views) != len(builtinNames) || !views[0].Builtin {
		t.Fatalf("SkillsForBot=%+v", views)
	}
}

func TestBuiltinSkillsLoadOnDemand(t *testing.T) {
	p := prepareBuiltin(t, Config{}, "bot")
	read := discoveryTool(t, p, "read_skill")
	for _, s := range BuiltinSkills() {
		body, err := read.Execute(context.Background(), json.RawMessage(`{"name":"`+s.Name+`"}`))
		if err != nil || body != s.Body {
			t.Fatalf("%s body err=%v len=%d want %d", s.Name, err, len(body), len(s.Body))
		}
	}
	file := discoveryTool(t, p, "read_skill_file")
	script, err := file.Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet","path":"scripts/xlsx_summary.py"}`))
	if err != nil || !strings.Contains(script, "Summarise an XLSX workbook") {
		t.Fatalf("script err=%v", err)
	}
	for _, bad := range []string{"../spreadsheet/SKILL.md", "/etc/passwd", "scripts/../../slides/SKILL.md", "scripts/missing.py"} {
		raw, _ := json.Marshal(map[string]string{"name": "spreadsheet", "path": bad})
		if got, err := file.Execute(context.Background(), raw); err == nil {
			t.Fatalf("read %q returned %d bytes", bad, len(got))
		}
	}
}

func TestBuiltinSkillsCanBeTurnedOffForABot(t *testing.T) {
	off := map[string]bool{"slides": true}
	cfg := Config{SkillAccess: func(_ context.Context, _ string, names []string) (map[string]bool, error) {
		allowed := map[string]bool{}
		for _, n := range names {
			allowed[n] = !off[n]
		}
		return allowed, nil
	}}
	p := prepareBuiltin(t, cfg, "bot")
	if strings.Contains(p.Instructions, `"name":"slides"`) || !strings.Contains(p.Instructions, `"name":"spreadsheet"`) {
		t.Fatalf("directory after turning slides off: %q", p.Instructions)
	}
	if _, err := discoveryTool(t, p, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"slides"}`)); err == nil {
		t.Fatal("a disabled built-in skill was readable")
	}
	if views, _ := NewManager(cfg).SkillsForBot(context.Background(), "bot"); len(views) != len(builtinNames)-1 {
		t.Fatalf("SkillsForBot lists %d", len(views))
	}
	// The setting store failing withholds every built-in skill rather than granting by default.
	failing := Config{SkillAccess: func(context.Context, string, []string) (map[string]bool, error) { return nil, errors.New("db down") }}
	q := prepareBuiltin(t, failing, "bot")
	for _, name := range builtinNames {
		if strings.Contains(q.Instructions, `"name":"`+name+`"`) {
			t.Fatalf("%s offered while the setting was unreadable", name)
		}
	}
}

func TestBuiltinSkillsAreNotInstallableOrDeletableAsUserSkills(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	m := NewManager(Config{SkillsDir: dir})
	manifest := "---\nname: spreadsheet\ndescription: mine\n---\nbody"
	if err := m.InstallSkill("spreadsheet", map[string][]byte{"SKILL.md": []byte(manifest)}); err == nil || !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("install over a built-in name: %v", err)
	}
	if err := m.DeleteSkill("spreadsheet"); !errors.Is(err, ErrBuiltinSkill) {
		t.Fatalf("delete built-in: %v", err)
	}
	if !m.SkillExists("spreadsheet") {
		t.Fatal("built-in skill should exist for access settings")
	}
	views, _ := m.ListSkills()
	builtin := 0
	for _, v := range views {
		if v.Builtin {
			builtin++
		}
	}
	if builtin != len(builtinNames) {
		t.Fatalf("ListSkills reports %d built-in", builtin)
	}
	// A same-named skill that already sits in the directory is ignored in favour of the shipped one.
	if err := writeSkillForTest(dir, "spreadsheet", manifest); err != nil {
		t.Fatal(err)
	}
	p := prepareBuiltin(t, Config{SkillsDir: dir}, "bot")
	body, err := discoveryTool(t, p, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet"}`))
	if err != nil || body == "body" {
		t.Fatalf("installed skill shadowed the built-in one: err=%v", err)
	}
	found := false
	for _, d := range p.Diagnostics {
		found = found || strings.Contains(d.Message, "shares a built-in skill's name")
	}
	if !found {
		t.Fatalf("no diagnostic for the shadowed skill: %+v", p.Diagnostics)
	}
}

func TestInstalledSkillsStillWorkBesideBuiltinOnes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	m := NewManager(Config{SkillsDir: dir})
	if err := m.InstallSkill("deploy", map[string][]byte{"SKILL.md": []byte("---\nname: deploy\ndescription: ship it\n---\nBODY")}); err != nil {
		t.Fatal(err)
	}
	p, err := m.PrepareDiscoverableForBot(context.Background(), "bot")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !strings.Contains(p.Instructions, `"name":"deploy"`) || !strings.Contains(p.Instructions, `"name":"web-page"`) {
		t.Fatalf("directory=%q", p.Instructions)
	}
	if got, err := discoveryTool(t, p, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"deploy"}`)); err != nil || got != "BODY" {
		t.Fatalf("installed read=%q err=%v", got, err)
	}
}

func writeSkillForTest(root, name, manifest string) error {
	if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, name, "SKILL.md"), []byte(manifest), 0o600)
}

func TestBuiltinSkillsDeclareKindAndRequires(t *testing.T) {
	want := map[string]struct {
		kind     string
		computer bool
	}{
		"code-delivery": {"deliverable", true}, "document-report": {"deliverable", true}, "slides": {"deliverable", true},
		"spreadsheet": {"deliverable", true}, "web-page": {"deliverable", true}, "video": {"read", true},
		"data-chart": {"deliverable", true}, "docx-edit": {"deliverable", true}, "email": {"procedure", false}, "file-convert": {"deliverable", true}, "image-edit": {"deliverable", true}, "install-software": {"procedure", true}, "pdf": {"deliverable", true}, "video-edit": {"deliverable", true}, "web-research": {"procedure", false}, "web-tasks": {"procedure", true}, "web-to-sheet": {"deliverable", true},
	}
	for _, s := range BuiltinSkills() {
		w, ok := want[s.Name]
		if !ok || s.Kind != w.kind || s.NeedsComputer() != w.computer {
			t.Errorf("%s kind=%q requires=%v", s.Name, s.Kind, s.Requires)
		}
	}
	files, _, err := BuiltinSkillFiles("email")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range files {
		found = found || f.Path == "references/gmail-browser.md"
	}
	if !found {
		t.Fatalf("email ships no gmail-browser reference: %+v", files)
	}
}

func TestSkillsThatNeedAComputerAreHiddenWithoutOne(t *testing.T) {
	ctx := context.Background()
	without := prepareBuiltin(t, Config{HasBotComputer: func() bool { return false }}, "bot")
	for _, hidden := range []string{"slides", "spreadsheet", "document-report", "web-page", "code-delivery", "video", "image-edit", "video-edit", "file-convert", "web-tasks", "install-software"} {
		if strings.Contains(without.Instructions, `"name":"`+hidden+`"`) {
			t.Errorf("index lists %s without a computer", hidden)
		}
		_, err := discoveryTool(t, without, "read_skill").Execute(ctx, json.RawMessage(`{"name":"`+hidden+`"}`))
		if err == nil || !strings.Contains(err.Error(), "Bot computer") || !strings.Contains(err.Error(), "Settings > Environments") {
			t.Errorf("read_skill %s refusal=%v", hidden, err)
		}
		if _, err := discoveryTool(t, without, "read_skill_file").Execute(ctx, json.RawMessage(`{"name":"`+hidden+`","path":"SKILL.md"}`)); err == nil {
			t.Errorf("read_skill_file served %s without a computer", hidden)
		}
	}
	for _, shown := range []string{"web-research", "email"} {
		if !strings.Contains(without.Instructions, `"name":"`+shown+`"`) {
			t.Errorf("index lacks %s without a computer", shown)
		}
	}
	list, err := discoveryTool(t, without, "list_skills").Execute(ctx, json.RawMessage(`{}`))
	if err != nil || strings.Contains(list, `"slides"`) || !strings.Contains(list, `"email"`) {
		t.Fatalf("list without a computer: %v %s", err, list)
	}
	if ref, err := discoveryTool(t, without, "read_skill_file").Execute(ctx, json.RawMessage(`{"name":"email","path":"references/gmail-browser.md"}`)); err != nil || !strings.Contains(ref, "mail.google.com") {
		t.Fatalf("email reference: %v %q", err, ref)
	}
	with := prepareBuiltin(t, Config{HasBotComputer: func() bool { return true }}, "bot")
	for _, name := range builtinNames {
		if !strings.Contains(with.Instructions, `"name":"`+name+`"`) {
			t.Errorf("index lacks %s with a computer", name)
		}
	}
}

func TestCollisionDiagnosticNamesBothSkills(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	if err := writeSkillForTest(dir, "email", "---\nname: email\ndescription: mine\n---\nbody"); err != nil {
		t.Fatal(err)
	}
	p := prepareBuiltin(t, Config{SkillsDir: dir}, "bot")
	for _, d := range p.Diagnostics {
		if strings.Contains(d.Message, "installed skill \"email\"") && strings.Contains(d.Message, filepath.Join(dir, "email")) && strings.Contains(d.Message, "built-in skill \"email\" is used") {
			return
		}
	}
	t.Fatalf("diagnostics=%+v", p.Diagnostics)
}

// An adapted built-in Skill ships its upstream license and its NOTICE with its
// files (Apache-2.0 attribution travels with the binary and to the computer).
func TestBuiltinSkillNoticeIsServedWithItsFiles(t *testing.T) {
	for _, name := range []string{"pdf", "docx-edit", "spreadsheet", "slides"} {
		files, _, err := BuiltinSkillFiles(name)
		if err != nil {
			t.Fatal(name, err)
		}
		got := map[string]bool{}
		for _, f := range files {
			got[f.Path] = len(f.Data) > 0
		}
		if !got["NOTICE"] || !got["LICENSE.txt"] {
			t.Errorf("%s: NOTICE=%v LICENSE.txt=%v", name, got["NOTICE"], got["LICENSE.txt"])
		}
	}
}

// Every built-in Skill has its row in the prompt directory: the catalog must
// not outgrow the Skill rows' budget (the last names were cut once).
func TestEveryBuiltinSkillIsInTheDirectory(t *testing.T) {
	skills := BuiltinSkills()
	got := discoveryInstructions(map[string]MCPServerConfig{}, skills)
	_, section, _ := strings.Cut(got, skillDirectoryHeader)
	if strings.Contains(section, "Additional entries omitted") {
		t.Fatalf("the Skill rows were cut: %d built-ins need more than %d bytes", len(skills), skillDirectoryBytes)
	}
	for _, s := range skills {
		if !strings.Contains(section, `"name":"`+s.Name+`"`) {
			t.Errorf("%s is missing from the directory", s.Name)
		}
	}
}
