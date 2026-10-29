package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// linkTarget checks that a registry link names a real page and section.
func linkTarget(link string) (ok bool, why string) {
	path, section, _ := strings.Cut(link, "#")
	path, query, _ := strings.Cut(path, "?")
	switch {
	case path == "/":
		return true, ""
	case strings.HasPrefix(path, "/settings/connections/"):
		if section != "" && !inList(connectionSections, section) {
			return false, "unknown connection section " + section
		}
		return true, ""
	case strings.HasPrefix(path, "/settings/"):
		tab := strings.TrimPrefix(path, "/settings/")
		for _, p := range settingsTabPages {
			if p.ID == tab {
				if section != "" && !inList(p.Sections, section) {
					if tab == "automation" && section != "" {
						return false, "unknown automation group " + section
					}
					return false, "unknown section " + section + " on " + tab
				}
				return true, ""
			}
		}
		return false, "unknown tab " + tab
	case strings.HasPrefix(path, "/b/{bot}/settings"):
		for _, p := range botSettingsPages {
			if p.ID == section {
				return true, ""
			}
		}
		return false, "unknown bot settings section " + section
	case strings.HasPrefix(path, "/g/{group}/settings"):
		for _, p := range groupSettingsPages {
			if p.ID == section {
				return true, ""
			}
		}
		return false, "unknown group settings section " + section
	case strings.HasPrefix(path, "/b/{bot}"):
		if v, found := strings.CutPrefix(query, "side="); found && inList(sidebarViews, v) {
			return true, ""
		}
		return false, "unknown sidebar view"
	}
	return false, "unknown link shape"
}

func TestSettingsRegistryEntries(t *testing.T) {
	en, err := loadLocaleCatalog(testRepoRoot(t), "en")
	if err != nil {
		t.Fatal(err)
	}
	generated := manualReferencePages(en)
	seen := map[string]bool{}
	for _, d := range settingsRegistry {
		if seen[d.Key] {
			t.Fatalf("duplicate key %s", d.Key)
		}
		seen[d.Key] = true
		if d.Scope == "" || d.Kind == "" || d.Link == "" || d.Manual == "" || d.LabelKey == "" {
			t.Errorf("%s: scope, kind, link, manual and label are required: %+v", d.Key, d)
		}
		if ok, why := linkTarget(d.Link); !ok {
			t.Errorf("%s: link %s: %s", d.Key, d.Link, why)
		}
		if _, ok := generated[d.Manual]; !ok && !inList(manualGuideIDs, d.Manual) {
			t.Errorf("%s: manual page %s is neither generated nor a listed guide", d.Key, d.Manual)
		}
		if _, ok := en[d.LabelKey]; !ok {
			t.Errorf("%s: label key %s is not in the English catalog", d.Key, d.LabelKey)
		}
		if d.DescKey != "" {
			if _, ok := en[d.DescKey]; !ok {
				t.Errorf("%s: description key %s is not in the English catalog", d.Key, d.DescKey)
			}
		}
		if d.Kind == KindEnum && len(d.Options) == 0 {
			t.Errorf("%s: an enum needs options", d.Key)
		}
		if d.Kind == KindInt && d.Max <= d.Min {
			t.Errorf("%s: an int needs bounds", d.Key)
		}
		if d.Kind == KindString && d.Scope != ScopeAccount && d.MaxRunes == 0 {
			t.Errorf("%s: a string needs a bound", d.Key)
		}
		if d.Risk.Self == "" && d.Kind != KindCollection {
			t.Errorf("%s: risk is required", d.Key)
		}
		if d.Risk.Self == RiskForbidden && d.Kind != KindNone && d.Kind != KindReadOnly {
			t.Errorf("%s: a forbidden key holds no value kind", d.Key)
		}
		if d.Kind == KindCollection && len(d.Tools) == 0 && d.Key != "permissions.standing_allowances" {
			t.Errorf("%s: a collection names its tools", d.Key)
		}
		if strings.Contains(strings.ToLower(d.Key), "secret") && d.Risk.Self != RiskForbidden {
			t.Errorf("%s: secrets are forbidden", d.Key)
		}
	}
	for _, item := range automationItems {
		if item.Key == autoMCPReview {
			continue
		}
		if !seen["automation."+item.Key] {
			t.Errorf("automation item %s is missing from the registry", item.Key)
		}
		if _, ok := en["settings:automation.item."+item.Key+".name"]; !ok {
			t.Errorf("automation item %s has no English name", item.Key)
		}
	}
	// Every Settings tab and every Bot settings section has a page.
	for _, p := range settingsTabPages {
		if _, ok := generated["settings/"+p.ID]; !ok {
			t.Errorf("no reference page for tab %s", p.ID)
		}
	}
	for _, key := range []string{"bot.secrets", "models.providers", "web_tools.keys", "system.update", "system.data", "system.purge", "system.accounts", "system.computer_resources", "keys.secrets", "account.sign_out"} {
		d, ok := settingDefFor(key)
		if !ok || d.Risk.Self != RiskForbidden {
			t.Errorf("%s must be a forbidden entry", key)
		}
	}
}

func TestSettingsRegistryRiskClasses(t *testing.T) {
	get := func(k string) SettingDef {
		d, ok := settingDefFor(k)
		if !ok {
			t.Fatalf("missing %s", k)
		}
		return d
	}
	if got := get("bot.name").RiskFor(ChangeContext{SelfBot: true}); got != RiskDirect {
		t.Errorf("own Bot name = %s", got)
	}
	if got := get("bot.name").RiskFor(ChangeContext{}); got != RiskConfirm {
		t.Errorf("another Bot's name = %s", got)
	}
	if got := get("bot.connections").RiskFor(ChangeContext{SelfBot: true}); got != RiskConfirm {
		t.Errorf("connection access = %s", got)
	}
	if got := get("permissions.approval_mode").RiskFor(ChangeContext{Loosen: true}); got != RiskConfirm {
		t.Errorf("approval mode loosening = %s", got)
	}
	if got := get("permissions.approval_mode").RiskFor(ChangeContext{}); got != RiskConfirm {
		t.Errorf("approval mode tightening = %s", got)
	}
	tier := get("connection.tool_tier")
	if tier.RiskFor(ChangeContext{}) != RiskDirect || tier.RiskFor(ChangeContext{Loosen: true}) != RiskConfirm {
		t.Error("tool tier: tighten direct, loosen confirm")
	}
	for _, k := range []string{"bot.delete", "bot.archived", "conversation.delete", "conversation.members", "conversation.archived", "connection.disconnect", "bot.model", "models.default_model"} {
		if got := get(k).RiskFor(ChangeContext{SelfBot: true}); got != RiskConfirm {
			t.Errorf("%s = %s, want confirm", k, got)
		}
	}
}

func TestSettingsRegistryGenerated(t *testing.T) {
	root := testRepoRoot(t)
	files, err := SettingsRegistryArtifacts(root)
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s is missing; run `go generate ./internal/app`: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run `go generate ./internal/app`", rel)
		}
	}
	existing, _ := listFilesUnder(filepath.Join(root, filepath.FromSlash(manualGeneratedDir)))
	for _, f := range existing {
		if _, ok := files[manualGeneratedDir+"/"+filepath.ToSlash(f)]; !ok {
			t.Errorf("%s is not generated any more; run `go generate ./internal/app`", f)
		}
	}
}
