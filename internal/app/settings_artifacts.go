package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Generated artifacts of the settings registry. `go generate ./internal/app`
// writes them; TestSettingsRegistryGenerated fails when they are stale.
//
//	ui/src/generated/settingsRegistry.json                  the UI test checks links and section ids against the real components
//	internal/manual/generated/<page id>.md                  the reference pages of the product manual

const (
	settingsRegistryJSONPath = "ui/src/generated/settingsRegistry.json"
	manualGeneratedDir       = "internal/manual/generated"
)

// localeCatalog is the flattened English catalog set: "namespace:path" -> text.
type localeCatalog map[string]string

// loadLocaleCatalog reads ui/src/locales/<lang>/*.json under repoRoot.
func loadLocaleCatalog(repoRoot, lang string) (localeCatalog, error) {
	dir := filepath.Join(repoRoot, "ui", "src", "locales", lang)
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no locale catalogs in %s", dir)
	}
	out := localeCatalog{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var tree map[string]any
		if err := json.Unmarshal(raw, &tree); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		ns := strings.TrimSuffix(filepath.Base(f), ".json")
		flat := localeCatalog{}
		flattenLocale(flat, "", tree)
		for k, v := range flat {
			out[ns+":"+k] = v
		}
	}
	return out, nil
}

func flattenLocale(out localeCatalog, path string, tree map[string]any) {
	for k, v := range tree {
		next := path + "." + k
		if path == "" {
			next = k
		}
		switch x := v.(type) {
		case map[string]any:
			flattenLocale(out, next, x)
		case string:
			out[next] = x
		}
	}
}

type registryJSONPage struct {
	ID       string   `json:"id"`
	Sections []string `json:"sections,omitempty"`
}

type registryJSONSetting struct {
	Key      string   `json:"key"`
	Scope    string   `json:"scope"`
	Kind     string   `json:"kind"`
	Options  []string `json:"options,omitempty"`
	Min      int      `json:"min,omitempty"`
	Max      int      `json:"max,omitempty"`
	MaxRunes int      `json:"max_runes,omitempty"`
	Risk     RiskSpec `json:"risk"`
	Link     string   `json:"link"`
	Manual   string   `json:"manual"`
	LabelKey string   `json:"label_key"`
	Tools    []string `json:"tools,omitempty"`
	Audit    bool     `json:"audit"`
}

// SettingsRegistryArtifacts renders every generated file, keyed by path
// relative to the repository root.
func SettingsRegistryArtifacts(repoRoot string) (map[string][]byte, error) {
	en, err := loadLocaleCatalog(repoRoot, "en")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}

	doc := struct {
		Version            int                   `json:"version"`
		Tabs               []registryJSONPage    `json:"tabs"`
		BotSections        []string              `json:"bot_sections"`
		GroupSections      []string              `json:"group_sections"`
		ConnectionSections []string              `json:"connection_sections"`
		SidebarViews       []string              `json:"sidebar_views"`
		Settings           []registryJSONSetting `json:"settings"`
	}{Version: 1, ConnectionSections: connectionSections, SidebarViews: sidebarViews}
	for _, p := range settingsTabPages {
		doc.Tabs = append(doc.Tabs, registryJSONPage{ID: p.ID, Sections: p.Sections})
	}
	for _, p := range botSettingsPages {
		doc.BotSections = append(doc.BotSections, p.ID)
	}
	for _, p := range groupSettingsPages {
		doc.GroupSections = append(doc.GroupSections, p.ID)
	}
	for _, d := range settingsRegistry {
		doc.Settings = append(doc.Settings, registryJSONSetting{Key: d.Key, Scope: string(d.Scope), Kind: string(d.Kind), Options: d.Options, Min: d.Min, Max: d.Max, MaxRunes: d.MaxRunes, Risk: d.Risk, Link: d.Link, Manual: d.Manual, LabelKey: d.LabelKey, Tools: d.Tools, Audit: d.Audit})
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	out[settingsRegistryJSONPath] = append(raw, '\n')

	for id, body := range manualReferencePages(en) {
		out[manualGeneratedDir+"/"+id+".md"] = []byte(body)
	}
	// The release notes of every version ride in the binary as manual pages (whats-new/<version>).
	notes, err := whatsNewPages(repoRoot)
	if err != nil {
		return nil, err
	}
	for id, body := range notes {
		out[manualGeneratedDir+"/"+id+".md"] = []byte(body)
	}
	// The words the screen shows, in every language: the manual and the settings tool put them in their answers. The hand-written guides
	// name theirs with {{ui:key}}, which this reads so a guide needs no second edit.
	guideKeys := guideUITerms(repoRoot)
	for _, lang := range append([]string{"en"}, sortedKeys(uiLanguagesWithoutEnglish())...) {
		cat := en
		if lang != "en" {
			if cat, err = loadLocaleCatalog(repoRoot, lang); err != nil {
				return nil, err
			}
		}
		terms := map[string]string{}
		for _, key := range uiTermKeys(en, guideKeys) {
			if v, ok := cat[key]; ok && v != "" {
				terms[key] = v
			} else if v, ok := en[key]; ok {
				terms[key] = v
			}
		}
		raw, err := json.MarshalIndent(terms, "", "  ")
		if err != nil {
			return nil, err
		}
		out[manualGeneratedDir+"/terms/"+lang+".json"] = append(raw, '\n')
	}
	return out, nil
}

func uiLanguagesWithoutEnglish() map[string]bool {
	out := map[string]bool{}
	for l := range uiLanguages {
		if l != "en" {
			out[l] = true
		}
	}
	return out
}

// uiTermKeys are the catalog keys whose text the manual and the settings tool quote: every registry label, every page and section name.
func uiTermKeys(en localeCatalog, extra []string) []string {
	set := map[string]bool{"settings:shell.title": true, "chat:header.bot_settings": true, "chat:header.group_settings": true}
	for _, d := range settingsRegistry {
		set[d.LabelKey] = true
		if d.DescKey != "" {
			set[d.DescKey] = true
		}
	}
	for _, list := range [][]settingsPage{settingsTabPages, botSettingsPages, groupSettingsPages} {
		for _, p := range list {
			set[p.NameKey] = true
		}
	}
	for _, p := range settingsTabPages {
		set["settings:shell.page."+p.ID+".description"] = true
	}
	for _, p := range botSettingsPages {
		if text, ok := en["chat:botSettings.desc."+p.ID]; ok && !strings.Contains(text, "{{") {
			set["chat:botSettings.desc."+p.ID] = true
		}
	}
	for _, key := range extra {
		set[key] = true
	}
	for _, g := range []string{"models", "replies", "history", "checks", "reliability", "schedules", "computer", "widgets"} {
		set["settings:automation.group."+g+".title"] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WriteSettingsRegistryArtifacts writes the generated files and removes stale
// generated pages.
func WriteSettingsRegistryArtifacts(repoRoot string) error {
	files, err := SettingsRegistryArtifacts(repoRoot)
	if err != nil {
		return err
	}
	root := filepath.Join(repoRoot, filepath.FromSlash(manualGeneratedDir))
	if existing, _ := listFilesUnder(root); len(existing) > 0 {
		for _, f := range existing {
			rel := manualGeneratedDir + "/" + filepath.ToSlash(f)
			if _, keep := files[rel]; !keep {
				_ = os.Remove(filepath.Join(root, f))
			}
		}
	}
	for rel, body := range files {
		target := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func listFilesUnder(root string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, rel)
		return nil
	})
	return out, err
}

// manualReferencePages builds one page per Settings tab, Bot settings section
// and group settings section: page id -> Markdown.
func manualReferencePages(en localeCatalog) map[string]string {
	pages := map[string]string{}
	byPage := map[string][]SettingDef{}
	for _, d := range settingsRegistry {
		byPage[d.Manual] = append(byPage[d.Manual], d)
	}
	for _, p := range settingsTabPages {
		id := "settings/" + p.ID
		pages[id] = referencePage(id, uiTerm(p.NameKey), uiTerm("settings:shell.page."+p.ID+".description"), "/settings/"+p.ID, byPage[id])
	}
	for _, p := range botSettingsPages {
		id := "bot-settings/" + p.ID
		summary := ""
		if text, ok := en["chat:botSettings.desc."+p.ID]; ok && !strings.Contains(text, "{{") {
			summary = uiTerm("chat:botSettings.desc." + p.ID)
		}
		pages[id] = referencePage(id, uiTerm("chat:header.bot_settings")+" › "+uiTerm(p.NameKey), summary, "/b/{bot}/settings#"+p.ID, byPage[id])
	}
	for _, p := range groupSettingsPages {
		id := "group-settings/" + p.ID
		pages[id] = referencePage(id, uiTerm("chat:header.group_settings")+" › "+uiTerm(p.NameKey), "", "/g/{group}/settings#"+p.ID, byPage[id])
	}
	return pages
}

func (c localeCatalog) name(key string) string {
	if v, ok := c[key]; ok {
		return v
	}
	return key
}

// uiTerm is a placeholder for a word the screen shows. The manual replaces it with the person's language when a page is read, so the
// Bot says the names the person sees.
func uiTerm(key string) string { return "{{ui:" + key + "}}" }

func referencePage(id, title, summary, link string, defs []SettingDef) string {
	if summary == "" {
		summary = "Settings on this page."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\ntitle: %s\nsummary: %s\nlink: %s\nkind: reference\n---\n", id, title, strings.ReplaceAll(summary, "\n", " "), link)
	fmt.Fprintf(&b, "# %s\n\nOpen this page: [%s](%s)\n\n", title, title, link)
	if len(defs) == 0 {
		b.WriteString("The settings tool has no key on this page. A person changes it on the page.\n")
		return b.String()
	}
	sort.SliceStable(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
	b.WriteString("| Name | Key | Values | Default | Change class | Where |\n|---|---|---|---|---|---|\n")
	for _, d := range defs {
		def := ""
		if item, ok := automationItemFor(strings.TrimPrefix(d.Key, "automation.")); ok && strings.HasPrefix(d.Key, "automation.") {
			def = item.Default
			if item.Unit != "" && def != "" {
				def += " " + item.Unit
			}
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s | %s | `%s` |\n", uiTerm(d.LabelKey), d.Key, escapeCell(valueSummary(d)), escapeCell(def), riskSummary(d), d.Link)
	}
	for _, d := range defs {
		if d.DescKey != "" {
			fmt.Fprintf(&b, "\n**%s.** %s\n", uiTerm(d.LabelKey), uiTerm(d.DescKey))
		}
	}
	return b.String()
}

func escapeCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func valueSummary(d SettingDef) string {
	switch d.Kind {
	case KindBool:
		return "on or off"
	case KindEnum:
		return strings.Join(d.Options, ", ")
	case KindInt:
		return fmt.Sprintf("whole number %d to %d", d.Min, d.Max)
	case KindString:
		if d.MaxRunes > 0 {
			return "text, at most " + strconv.Itoa(d.MaxRunes) + " characters"
		}
		return "text"
	case KindModel:
		return "a configured model ID, or default"
	case KindEffort:
		return "a reasoning effort the model supports, or default"
	case KindTimezone:
		return "an IANA timezone name"
	case KindLanguage:
		return "a language code, or empty for automatic"
	case KindBotList:
		return "a list of names or IDs"
	case KindCollection:
		return "managed with tools: " + strings.Join(d.Tools, ", ")
	case KindAction:
		return "action"
	case KindReadOnly:
		return "read only"
	}
	return "not available to the tool"
}

func riskSummary(d SettingDef) string {
	switch {
	case d.Risk.Self == RiskForbidden:
		return "forbidden: the person changes it on the page"
	case d.Kind == KindCollection && d.Risk.Self == "":
		return "managed by its tools"
	case d.Scope == ScopeBot && d.Risk.Other != "" && d.Risk.Other != d.Risk.Self:
		return fmt.Sprintf("own Bot: %s; another Bot: %s", d.Risk.Self, d.Risk.Other)
	case d.Risk.Loosen != "":
		return fmt.Sprintf("%s; looser: %s", d.Risk.Self, d.Risk.Loosen)
	}
	return string(d.Risk.Self)
}

var uiTermPattern = regexp.MustCompile(`\{\{ui:([a-z]+:[A-Za-z0-9_.]+)\}\}`)

// guideUITerms lists the catalog keys the hand-written manual pages use.
func guideUITerms(repoRoot string) []string {
	seen := map[string]bool{}
	_ = filepath.Walk(filepath.Join(repoRoot, "internal", "manual", "pages"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		for _, m := range uiTermPattern.FindAllStringSubmatch(string(raw), -1) {
			seen[m[1]] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// whatsNewPages turns release-notes/<version>.md into manual pages: front matter for the index, the notes as the body.
func whatsNewPages(repoRoot string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(repoRoot, "release-notes", "*.md"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		version := strings.TrimSuffix(filepath.Base(f), ".md")
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		date, body := "", text
		if strings.HasPrefix(text, "---\n") {
			if end := strings.Index(text[4:], "\n---\n"); end >= 0 {
				for _, line := range strings.Split(text[4:4+end], "\n") {
					if v, ok := strings.CutPrefix(line, "date:"); ok {
						date = strings.TrimSpace(v)
					}
				}
				body = text[4+end+5:]
			}
		}
		id := "whats-new/" + version
		out[id] = fmt.Sprintf("---\nid: %s\ntitle: What's new in %s\nsummary: Changes in %s, released %s.\nlink: /settings/advanced#version\nkind: whats-new\nsince: %s\n---\n%s", id, version, version, date, version, strings.TrimLeft(body, "\n"))
	}
	return out, nil
}
