package extensions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillSet(name, body string) map[string][]byte {
	return map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: d\n---\n" + body)}
}

func TestReplaceSkillSwapsFilesAndVersionsRoundTrip(t *testing.T) {
	m := NewManager(Config{SkillsDir: filepath.Join(t.TempDir(), "skills")})
	if err := m.InstallSkill("alpha", skillSet("alpha", "ONE")); err != nil {
		t.Fatal(err)
	}
	one, digestOne, err := m.SkillFilesMap("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if digestOne != SkillFilesDigest(one) {
		t.Fatal("digest of the files on disk differs from the digest of the same map")
	}
	if err := m.SaveSkillVersion("alpha", 1, one); err != nil {
		t.Fatal(err)
	}
	next := skillSet("alpha", "TWO")
	next["scripts/run.py"] = []byte("print(1)\n")
	if err := m.SaveSkillVersion("alpha", 2, next); err != nil {
		t.Fatal(err)
	}
	if err := m.ReplaceSkill("alpha", next); err != nil {
		t.Fatal(err)
	}
	got, _, _ := m.SkillFilesMap("alpha")
	if !strings.Contains(string(got["SKILL.md"]), "TWO") || len(got) != 2 {
		t.Fatalf("replaced files: %v", got)
	}
	added, removed, changed := DiffSkillFiles(one, next)
	if strings.Join(added, ",") != "scripts/run.py" || len(removed) != 0 || strings.Join(changed, ",") != "SKILL.md" {
		t.Fatalf("diff %v %v %v", added, removed, changed)
	}
	// An archived version restores exactly, and the archive never shows up as a skill.
	back, err := m.ReadSkillVersion("alpha", 1)
	if err != nil || SkillFilesDigest(back) != digestOne {
		t.Fatalf("archive read: %v", err)
	}
	if err := m.ReplaceSkill("alpha", back); err != nil {
		t.Fatal(err)
	}
	if _, d, _ := m.SkillFilesMap("alpha"); d != digestOne {
		t.Fatal("restored files differ")
	}
	views, diags := m.ListSkills()
	for _, v := range views {
		if strings.HasPrefix(v.Name, ".") {
			t.Fatalf("a dot folder was listed: %v", views)
		}
	}
	for _, d := range diags {
		if strings.Contains(d.Message, "versions") {
			t.Fatalf("archive produced a diagnostic: %v", d)
		}
	}
	// Versions are immutable.
	if err := m.SaveSkillVersion("alpha", 1, next); err != nil {
		t.Fatal(err)
	}
	if again, _ := m.ReadSkillVersion("alpha", 1); SkillFilesDigest(again) != digestOne {
		t.Fatal("an archived version was overwritten")
	}
	if err := m.PruneSkillVersions("alpha", map[int]bool{2: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadSkillVersion("alpha", 1); err == nil {
		t.Fatal("pruned version still readable")
	}
	if err := m.ReplaceSkill("missing", skillSet("missing", "x")); err == nil {
		t.Fatal("replace of a skill that is not installed")
	}
	if err := m.ReplaceSkill("alpha", skillSet("other", "x")); err == nil {
		t.Fatal("replace with a mismatched manifest")
	}
}

func TestParseSkillBundleFollowsThePluginLayout(t *testing.T) {
	files := map[string][]byte{
		".claude-plugin/plugin.json": []byte(`{"name":"kit","description":"d","version":"2"}`),
		"commands/x.md":              []byte("ignored"),
		"skills/one/SKILL.md":        skillSet("one", "1")["SKILL.md"],
		"skills/one/ref/a.txt":       []byte("a"),
		"skills/two/SKILL.md":        skillSet("two", "2")["SKILL.md"],
		"skills/README.md":           []byte("ignored"),
	}
	b, err := ParseSkillBundle(files)
	if err != nil || b.Name != "kit" || len(b.Skills) != 2 || b.Skills[0].Name != "one" || len(b.Skills[0].Files) != 2 || b.Ignored != 2 {
		t.Fatalf("%+v %v", b, err)
	}
	bad := map[string]map[string][]byte{
		"no manifest":   {"skills/one/SKILL.md": skillSet("one", "1")["SKILL.md"]},
		"no skills":     {".claude-plugin/plugin.json": []byte(`{"name":"kit"}`)},
		"bad name":      {".claude-plugin/plugin.json": []byte(`{"name":"Bad Name"}`), "skills/one/SKILL.md": skillSet("one", "1")["SKILL.md"]},
		"name mismatch": {".claude-plugin/plugin.json": []byte(`{"name":"kit"}`), "skills/one/SKILL.md": skillSet("two", "1")["SKILL.md"]},
		"escape":        {".claude-plugin/plugin.json": []byte(`{"name":"kit"}`), "skills/one/SKILL.md": skillSet("one", "1")["SKILL.md"], "skills/one/../x": []byte("x")},
	}
	for name, f := range bad {
		if _, err := ParseSkillBundle(f); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	m := NewManager(Config{SkillsDir: filepath.Join(t.TempDir(), "skills")})
	if err := m.InstallSkill("two", skillSet("two", "mine")); err != nil {
		t.Fatal(err)
	}
	if err := m.InstallSkillBundle(b); err == nil {
		t.Fatal("a name clash did not refuse the bundle")
	}
	if _, err := os.Stat(filepath.Join(m.cfg.SkillsDir, "one")); err == nil {
		t.Fatal("a refused bundle left a skill behind")
	}
}
