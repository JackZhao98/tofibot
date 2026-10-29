package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/manual"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

func TestManualCoversSettings(t *testing.T) {
	for _, p := range settingsTabPages {
		page, ok := manual.Get("settings/" + p.ID)
		if !ok || page.Link != "/settings/"+p.ID {
			t.Errorf("tab %s has no manual page with its link: %+v", p.ID, page)
		}
	}
	for _, p := range botSettingsPages {
		if page, ok := manual.Get("bot-settings/" + p.ID); !ok || page.Link != "/b/{bot}/settings#"+p.ID {
			t.Errorf("bot settings section %s: %+v", p.ID, page)
		}
	}
	for _, p := range groupSettingsPages {
		if page, ok := manual.Get("group-settings/" + p.ID); !ok || page.Link != "/g/{group}/settings#"+p.ID {
			t.Errorf("group settings section %s: %+v", p.ID, page)
		}
	}
	for _, d := range settingsRegistry {
		page, ok := manual.Get(d.Manual)
		if !ok {
			t.Errorf("%s names manual page %s, which does not exist", d.Key, d.Manual)
			continue
		}
		if page.Kind == "reference" && !strings.Contains(page.Body, "`"+d.Key+"`") {
			t.Errorf("%s is missing from its reference page %s", d.Key, d.Manual)
		}
	}
	for _, item := range automationItems {
		if item.Key == autoMCPReview {
			continue
		}
		page, _ := manual.Get("settings/automation")
		if !strings.Contains(page.Body, "`automation."+item.Key+"`") {
			t.Errorf("automation item %s has no row in the manual", item.Key)
		}
	}
	for _, id := range manualGuideIDs {
		if _, ok := manual.Get(id); !ok {
			t.Errorf("guide %s is listed but has no page", id)
		}
	}
}

// manualFailureCodes are the stable codes a person or a Bot meets. A code in one of these sources has a page; a validation message
// that never reaches a person does not need one.
func manualFailureCodes(t *testing.T) []string {
	t.Helper()
	set := map[string]bool{}
	grep := func(pattern string, files ...string) {
		re := regexp.MustCompile(pattern)
		for _, f := range files {
			raw, err := readRepoFile(t, f)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range re.FindAllStringSubmatch(raw, -1) {
				set[m[1]] = true
			}
		}
	}
	// Schedule errors.
	grep(`schedErr\("(schedule_[a-z_]+)"`, "internal/app/schedule.go", "internal/app/schedule_edit.go")
	grep(`"(schedule_[a-z_]+_(?:invalid|required|past|conflict|locked))"`, "internal/app/schedule.go", "internal/app/schedule_edit.go")
	// The settings tool.
	grep(`"(settings?_[a-z_]+)"`, "internal/app/settings_tool.go", "internal/app/settings_write.go", "internal/app/settings_writers.go", "internal/app/settings_writers_confirm.go", "internal/app/settings_gate.go", "internal/app/settings_changes.go", "internal/app/questions.go")
	delete(set, "settings_change")
	delete(set, "setting_change")
	// Update errors the page shows.
	en, err := loadLocaleCatalog(testRepoRoot(t), "en")
	if err != nil {
		t.Fatal(err)
	}
	for key := range en {
		if code, ok := strings.CutPrefix(key, "settings:update.error."); ok && code != "generic" {
			set[code] = true
		}
	}
	// Outcomes of the gates and the connections a person meets.
	for _, code := range []string{"conversation_not_user_visible", "extension_change_needs_owner", "extension_change_needs_approval", "extension_change_unknown",
		"mcp_internal_host", "mcp_host_unverified", "api_disabled", "oauth_loopback_only", "oauth_https_required", "connection_not_ready",
		"model_auth_invalid", "connection_interrupted", "approval_window_expired", "task_message_limit"} {
		set[code] = true
	}
	codes := make([]string, 0, len(set))
	for c := range set {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return codes
}

func TestManualFailureCodes(t *testing.T) {
	codes := manualFailureCodes(t)
	if len(codes) < 50 {
		t.Fatalf("only %d codes found; the sources moved", len(codes))
	}
	for _, code := range codes {
		p, ok := manual.Explain(code)
		if !ok {
			t.Errorf("failure code %s has no page failures/%s.md", code, code)
			continue
		}
		for _, want := range []string{"**Meaning.**", "**Usual cause.**", "**Steps.**"} {
			if !strings.Contains(p.Body, want) {
				t.Errorf("failures/%s lacks %s", code, want)
			}
		}
	}
	// A page for a code that nothing produces any more is a stale page.
	known := map[string]bool{}
	for _, c := range codes {
		known[c] = true
	}
	for _, id := range manual.IDs() {
		if code, ok := strings.CutPrefix(id, "failures/"); ok && !known[code] {
			t.Errorf("failures/%s has no source that produces the code; remove the page or add the source to manualFailureCodes", code)
		}
	}
}

var (
	mdLinkPattern = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	secretPattern = regexp.MustCompile(`(sk-[A-Za-z0-9]{16,}|-----BEGIN [A-Z ]*PRIVATE KEY|AKIA[0-9A-Z]{16}|xox[bp]-[0-9A-Za-z-]{10,})`)
)

func TestManualTextAndLinks(t *testing.T) {
	terms := manual.Terms("en")
	for _, p := range manual.Pages() {
		if strings.Count(p.Body, "\n# ")+manualBool(strings.HasPrefix(p.Body, "# ")) != 1 {
			t.Errorf("%s: exactly one # title is required", p.ID)
		}
		if p.Title == "" || p.Summary == "" || p.Kind == "" {
			t.Errorf("%s: title, summary and kind are required: %+v", p.ID, p)
		}
		if (p.Kind == "guide" || p.Kind == "reference") && p.Link != "" && !strings.Contains(p.Body, "Open this page:") {
			t.Errorf("%s: a page with a link has the line 'Open this page:'", p.ID)
		}
		if len(p.Body) > maxManualPageBytes {
			t.Errorf("%s: %d bytes is over the page limit", p.ID, len(p.Body))
		}
		if secretPattern.MatchString(p.Body) {
			t.Errorf("%s: looks like it holds a secret", p.ID)
		}
		if p.Link != "" {
			if ok, why := linkTarget(p.Link); !ok {
				t.Errorf("%s: link %s: %s", p.ID, p.Link, why)
			}
		}
		for _, link := range mdLinkPattern.FindAllStringSubmatch(p.Body, -1) {
			target := link[1]
			if strings.HasPrefix(target, "manual:") {
				if _, ok := manual.Get(strings.TrimPrefix(target, "manual:")); !ok {
					t.Errorf("%s: link to a manual page that does not exist: %s", p.ID, target)
				}
				continue
			}
			if ok, why := linkTarget(target); !ok {
				t.Errorf("%s: link %s does not resolve: %s", p.ID, target, why)
			}
		}
		for _, id := range p.SeeAlso {
			if _, ok := manual.Get(id); !ok {
				t.Errorf("%s: see_also names %s, which does not exist", p.ID, id)
			}
		}
		// Every screen word a page names is in the term tables of every language.
		for _, key := range manual.UIKeys(p.Title + p.Summary + p.Body) {
			for lang := range uiLanguages {
				if _, ok := manual.Terms(lang)[key]; !ok {
					t.Errorf("%s: term %s is missing for %s", p.ID, key, lang)
				}
			}
			if _, ok := terms[key]; !ok {
				t.Errorf("%s: term %s is not in the English table", p.ID, key)
			}
		}
	}
}

func readRepoFile(t *testing.T, rel string) (string, error) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(testRepoRoot(t), filepath.FromSlash(rel)))
	return string(raw), err
}

func manualBool(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestManualTermsAreLocalizedForEveryLanguage(t *testing.T) {
	zh := manual.Localize("zh-CN", "{{ui:settings:shell.page.models.name}}")
	en := manual.Localize("en", "{{ui:settings:shell.page.models.name}}")
	if zh == en || zh == "" || strings.Contains(zh, "{{") {
		t.Fatalf("zh-CN %q, en %q", zh, en)
	}
	if got := manual.Localize("xx", "{{ui:settings:shell.page.models.name}}"); got != en {
		t.Fatalf("an unknown language falls back to English: %q", got)
	}
	if got := manual.Localize("en", "{{ui:settings:no.such.key}}"); got != "settings:no.such.key" {
		t.Fatalf("an unknown key stays visible: %q", got)
	}
}

func (e *settingsEnv) manualTool() Tool { return e.s.manualTool(e.conv, e.run) }

func (e *settingsEnv) manualCall(input string) (map[string]any, *tooloutcome.Outcome) {
	e.t.Helper()
	out, err := e.manualTool().Execute(e.t.Context(), json.RawMessage(input))
	if err != nil {
		if o, ok := tooloutcome.FromError(err); ok {
			return nil, &o
		}
		e.t.Fatalf("%s: %v", input, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		e.t.Fatalf("%q: %v", out, err)
	}
	return m, nil
}

func TestManualToolIndexReadSearchExplain(t *testing.T) {
	e := newSettingsEnv(t)
	idx, o := e.manualCall(`{"action":"index"}`)
	if o != nil {
		t.Fatal(o)
	}
	pages, _ := idx["pages"].([]any)
	if len(pages) < 30 || len(pages) > 80 {
		t.Fatalf("index has %d pages", len(pages))
	}
	raw, _ := json.Marshal(idx)
	if len(raw) > maxSettingsResultBytes {
		t.Fatalf("index is %d bytes", len(raw))
	}
	for _, item := range pages {
		row := item.(map[string]any)
		if strings.HasPrefix(row["id"].(string), "failures/") || strings.HasPrefix(row["id"].(string), "whats-new/") {
			t.Errorf("the index lists %v; failures and release notes have their own actions", row["id"])
		}
		if strings.Contains(row["title"].(string), "{{") || strings.Contains(row["summary"].(string), "{{") {
			t.Errorf("unlocalized placeholder in %+v", row)
		}
	}

	// A page carries the link with this Bot's id, the title in the person's words and its see-also titles.
	page, o := e.manualCall(`{"action":"read","page":"bot-settings/model"}`)
	if o != nil {
		t.Fatal(o)
	}
	if page["link"] != "/b/"+e.bot.ID+"/settings#model" || page["title"] != "Bot settings › Model" {
		t.Fatalf("read: %+v", page)
	}
	body, _ := page["body"].(string)
	if strings.Contains(body, "{bot}") || strings.Contains(body, "{{ui:") || !strings.Contains(body, "`bot.model`") {
		t.Fatalf("body: %s", body)
	}

	// The names follow the person's language.
	if err := e.s.store.putUserLanguage("zh-CN"); err != nil {
		t.Fatal(err)
	}
	zh, _ := e.manualCall(`{"action":"read","page":"settings/models"}`)
	if zh["title"] == "Models" || zh["title"] == "" || strings.Contains(zh["title"].(string), "{{") {
		t.Fatalf("title in zh-CN: %v", zh["title"])
	}
	_ = e.s.store.putUserLanguage("")

	// A group page with no group in the workspace loses its link but keeps its words.
	gp, _ := e.manualCall(`{"action":"read","page":"group-settings/members"}`)
	if gp["link"] != "" || strings.Contains(gp["body"].(string), "{group}") {
		t.Fatalf("group page without a group: %+v", gp)
	}
	other, _ := e.s.store.CreateBot("beta", "", "model")
	g, err := e.s.store.CreateGroup("review", []string{e.bot.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	gp, _ = e.manualCall(`{"action":"read","page":"group-settings/members"}`)
	if gp["link"] != "/g/"+g.ID+"/settings#members" {
		t.Fatalf("group page link: %v", gp["link"])
	}

	if _, o := e.manualCall(`{"action":"read","page":"no/such/page"}`); o == nil || o.Code != "manual_page_not_found" || o.NextAction != "repair_arguments" {
		t.Fatalf("unknown page: %+v", o)
	}
	hits, _ := e.manualCall(`{"action":"search","query":"standing allowance"}`)
	list, _ := hits["hits"].([]any)
	if len(list) == 0 || len(list) > manualSearchLimit {
		t.Fatalf("search hits: %+v", hits)
	}
	found := false
	for _, h := range list {
		found = found || h.(map[string]any)["id"] == "permissions"
	}
	if !found {
		t.Fatalf("search for a phrase in the permissions guide: %+v", hits)
	}
	if _, o := e.manualCall(`{"action":"search"}`); o == nil || o.Code != "manual_invalid_request" {
		t.Fatalf("empty search: %+v", o)
	}

	fail, o := e.manualCall(`{"action":"explain","code":"settings_change_limit"}`)
	if o != nil || !strings.Contains(fail["body"].(string), "**Steps.**") || fail["link"] == "" {
		t.Fatalf("explain: %+v %+v", fail, o)
	}
	_, o = e.manualCall(`{"action":"explain","code":"settings_chang"}`)
	if o == nil || o.Code != "manual_page_not_found" || !strings.Contains(o.Message, "settings_change_limit") {
		t.Fatalf("explain unknown code should name the nearest pages: %+v", o)
	}
}

func TestManualToolWorksInHiddenAndUnattendedRuns(t *testing.T) {
	e := newSettingsEnv(t)
	hidden := e.conv
	hidden.UserVisible = false
	out, err := e.s.manualTool(hidden, e.run).Execute(t.Context(), json.RawMessage(`{"action":"read","page":"setup"}`))
	if err != nil || !strings.Contains(out, "Set up") {
		t.Fatalf("%q %v", out, err)
	}
}

func TestManualSetupStateNamesTheNextStepAndNeverClaimsAComputer(t *testing.T) {
	e := newSettingsEnv(t)
	page, o := e.manualCall(`{"action":"read","page":"setup"}`)
	if o != nil {
		t.Fatal(o)
	}
	state, _ := page["state"].(map[string]any)
	if state["computer"] != "none" || state["model_connected"] == nil || state["next_step"] == nil {
		t.Fatalf("setup state: %+v", state)
	}
	if strings.Contains(strings.ToLower(page["body"].(string)), "your computer is") {
		t.Fatal("the setup page must not claim a computer")
	}
}

func TestManualWhatsNewReturnsTheInstalledVersionFirst(t *testing.T) {
	e := newSettingsEnv(t)
	old := Version
	defer func() { Version = old }()
	Version = "v0.6.0-rc.1"
	out, o := e.manualCall(`{"action":"whats_new"}`)
	if o != nil {
		t.Fatal(o)
	}
	notes, _ := out["notes"].([]any)
	if len(notes) == 0 || notes[0].(map[string]any)["version"] != "v0.6.0-rc.1" || !strings.Contains(notes[0].(map[string]any)["body"].(string), "Settings") {
		t.Fatalf("notes: %+v", out)
	}
	// A source build gets the newest notes, and a release older than every note gets the oldest one only if it is the installed one.
	Version = "dev"
	out, _ = e.manualCall(`{"action":"whats_new"}`)
	if n, _ := out["notes"].([]any); len(n) == 0 || len(n) > 3 {
		t.Fatalf("dev build notes: %+v", out)
	}
}

func TestManualVersionOrder(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.6.0", "v0.6.0-rc.2", true}, {"v0.6.0-rc.2", "v0.6.0-rc.1", true}, {"v0.6.0-rc.1", "v0.6.0", false}, {"v0.10.0", "v0.9.9", true}, {"dev", "v0.1.0", false}, {"v1.0.0", "v1.0.0", false}} {
		if got := manual.Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestManualToolTextHasNoExamples(t *testing.T) {
	text := strings.ToLower(manualToolDescription())
	for _, banned := range []string{"for example", "e.g.", "such as", "not just", "rather than", "instead of"} {
		if strings.Contains(text, banned) {
			t.Errorf("tool text contains %q", banned)
		}
	}
	if !strings.Contains(runtimeCapabilityPrompt, "manual") {
		t.Error("the prompt points to the manual in one line")
	}
}
