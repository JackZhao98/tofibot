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

func skillAccessFixture(t *testing.T, access func(context.Context, string, []string) (map[string]bool, error)) *Manager {
	t.Helper()
	dir := t.TempDir()
	mgr := NewManager(Config{SkillsDir: filepath.Join(dir, "skills"), SkillAccess: access})
	for _, name := range []string{"alpha", "beta"} {
		manifest := "---\nname: " + name + "\ndescription: " + name + " desc\n---\nBODY_" + name
		if err := mgr.InstallSkill(name, map[string][]byte{"SKILL.md": []byte(manifest), "guide.txt": []byte("file_" + name)}); err != nil {
			t.Fatal(err)
		}
	}
	return mgr
}

// alpha is limited to bot-a; beta is open to everyone.
func alphaOnlyForBotA(_ context.Context, bot string, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, n := range names {
		out[n] = n != "alpha" || bot == "bot-a"
	}
	return out, nil
}

func TestRestrictedSkillInvisibleAndUnreadableForOtherBots(t *testing.T) {
	mgr := skillAccessFixture(t, alphaOnlyForBotA)
	for _, discoverable := range []bool{true, false} {
		prep := func(bot string) *Prepared {
			var p *Prepared
			var err error
			if discoverable {
				p, err = mgr.PrepareDiscoverableForBot(context.Background(), bot)
			} else {
				p, err = mgr.PrepareForBot(context.Background(), bot)
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Close() })
			return p
		}
		denied := prep("bot-b")
		if strings.Contains(denied.Instructions, "alpha") || !strings.Contains(denied.Instructions, "beta") {
			t.Fatalf("discoverable=%v denied instructions leak or lose beta: %q", discoverable, denied.Instructions)
		}
		list, err := discoveryTool(t, denied, "list_skills").Execute(context.Background(), json.RawMessage(`{}`))
		if err != nil || strings.Contains(list, "alpha") || !strings.Contains(list, "beta") {
			t.Fatalf("discoverable=%v list=%q err=%v", discoverable, list, err)
		}
		if _, err := discoveryTool(t, denied, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`)); err == nil {
			t.Fatal("read_skill returned a restricted skill")
		}
		if _, err := discoveryTool(t, denied, "read_skill_file").Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"guide.txt"}`)); err == nil {
			t.Fatal("read_skill_file returned a restricted skill file")
		}
		if got, err := discoveryTool(t, denied, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"beta"}`)); err != nil || got != "BODY_beta" {
			t.Fatalf("open skill read=%q err=%v", got, err)
		}
		allowed := prep("bot-a")
		if !strings.Contains(allowed.Instructions, "alpha") {
			t.Fatalf("allowed bot lost alpha: %q", allowed.Instructions)
		}
		if got, err := discoveryTool(t, allowed, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"alpha"}`)); err != nil || got != "BODY_alpha" {
			t.Fatalf("allowed read=%q err=%v", got, err)
		}
		if got, err := discoveryTool(t, allowed, "read_skill_file").Execute(context.Background(), json.RawMessage(`{"name":"alpha","path":"guide.txt"}`)); err != nil || got != "file_alpha" {
			t.Fatalf("allowed file=%q err=%v", got, err)
		}
	}
}

func TestSkillAccessLookupErrorFailsClosed(t *testing.T) {
	mgr := skillAccessFixture(t, func(context.Context, string, []string) (map[string]bool, error) {
		return nil, errors.New("database is locked")
	})
	p, err := mgr.PrepareDiscoverableForBot(context.Background(), "bot-a")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if strings.Contains(p.Instructions, "alpha") || strings.Contains(p.Instructions, "beta") {
		t.Fatalf("skills granted despite lookup failure: %q", p.Instructions)
	}
	list, err := discoveryTool(t, p, "list_skills").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil || strings.Contains(list, "alpha") || strings.Contains(list, "beta") {
		t.Fatalf("list=%q err=%v", list, err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := discoveryTool(t, p, "read_skill").Execute(context.Background(), json.RawMessage(`{"name":"`+name+`"}`)); err == nil {
			t.Fatalf("read_skill %s succeeded despite lookup failure", name)
		}
		if _, err := discoveryTool(t, p, "read_skill_file").Execute(context.Background(), json.RawMessage(`{"name":"`+name+`","path":"guide.txt"}`)); err == nil {
			t.Fatalf("read_skill_file %s succeeded despite lookup failure", name)
		}
	}
	if views, _ := mgr.SkillsForBot(context.Background(), "bot-a"); len(views) != 0 {
		t.Fatalf("SkillsForBot granted on error: %v", views)
	}
}

func TestNoAccessHookLeavesEverySkillGlobalAndLegacyStateIgnored(t *testing.T) {
	mgr := skillAccessFixture(t, nil)
	legacy := `{"bot-x":{"alpha":false,"beta":false}}`
	if err := os.WriteFile(filepath.Join(mgr.cfg.SkillsDir, ".enabled.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := mgr.PrepareDiscoverableForBot(context.Background(), "bot-x")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !strings.Contains(p.Instructions, "alpha") || !strings.Contains(p.Instructions, "beta") {
		t.Fatalf("legacy file hid skills: %q", p.Instructions)
	}
}
