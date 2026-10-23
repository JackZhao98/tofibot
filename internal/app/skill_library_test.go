package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Tests for the per-Bot skill library (decision 0058).

type libEnv struct {
	s       *Server
	handler http.Handler
	a, b, c Bot
	skills  string
}

// newLibEnv is a workspace with three Bots and two skills the owner added in
// Settings. Nothing is turned on for any Bot yet.
func newLibEnv(t *testing.T) libEnv {
	t.Helper()
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	env := libEnv{s: s, handler: s.Handler(), skills: filepath.Join(root, "skills")}
	for i, dst := range []*Bot{&env.a, &env.b, &env.c} {
		bot, err := s.store.CreateBot([]string{"Alpha Bot", "Bravo Bot", "Charlie Bot"}[i], "", "model")
		if err != nil {
			t.Fatal(err)
		}
		*dst = bot
	}
	for _, name := range []string{"deploy", "notes"} {
		status, body := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills", map[string]any{
			"name":  name,
			"files": map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: " + name + " skill\n---\nBODY_" + name},
		})
		if status != http.StatusCreated {
			t.Fatalf("install %s status=%d body=%s", name, status, body)
		}
	}
	return env
}

func enableSkillsFor(t *testing.T, s *Server, botID string, names ...string) {
	t.Helper()
	if err := s.store.setSkillsEnabled(botID, names, true, skillSetByOwner); err != nil {
		t.Fatal(err)
	}
}

func hasInDirectory(t *testing.T, s *Server, botID, name string) bool {
	t.Helper()
	return strings.Contains(directoryOf(t, s, botID), `"name":"`+name+`"`)
}

// botTool is manage_extensions as one Bot's run sees it. With approve set, a
// person's approval is recorded for every gated call first.
func botTool(t *testing.T, s *Server, bot Bot, approve bool) Tool {
	t.Helper()
	c, r := teamTestRun(t, s, bot)
	tool := s.extensionManagementTools(c, r)[0]
	if !approve {
		return tool
	}
	inner := tool.Execute
	tool.Execute = func(ctx context.Context, raw json.RawMessage) (string, error) {
		var x extensionToolArgs
		if json.Unmarshal(raw, &x) == nil && extensionChangeGated(x.Action) {
			approveExtensionChange(t, s, c, r, x)
		}
		return inner(ctx, raw)
	}
	return tool
}

func callSkillToolRaw(t *testing.T, tool Tool, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), raw)
}

func skillFiles(name, body string) map[string]string {
	return map[string]string{"SKILL.md": "---\nname: " + name + "\ndescription: " + name + " skill\n---\n" + body}
}

func enabledNames(t *testing.T, s *Server, botID string) []string {
	t.Helper()
	views, _ := s.extensions.SkillsForBot(context.Background(), botID)
	out := []string{}
	for _, v := range views {
		out = append(out, v.Name)
	}
	sort.Strings(out)
	return out
}

func officialNames() []string {
	out := []string{}
	for _, s := range extensions.BuiltinSkills() {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}

// Migration equivalence: what each existing Bot could use before is what it
// can use after, for skills open to all Bots, skills limited to listed Bots and
// official skills the owner had switched off.
func TestSkillMigrationKeepsEachBotsEffectiveSet(t *testing.T) {
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var bots []Bot
	for _, name := range []string{"Old A", "Old B", "Old C"} {
		b, err := s.store.CreateBot(name, "", "model")
		if err != nil {
			t.Fatal(err)
		}
		bots = append(bots, b)
	}
	if _, err := s.store.SetBotArchived(bots[2].ID, true); err != nil {
		t.Fatal(err)
	}
	// The old world: skills on disk, restrictions in skill_access, one official skill off.
	for _, name := range []string{"open-skill", "also-open", "limited-skill"} {
		if err := s.extensions.InstallSkill(name, map[string][]byte{"SKILL.md": []byte("---\nname: " + name + "\ndescription: d\n---\nb")}); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`DELETE FROM skill_library`, `DELETE FROM skill_versions`, `DELETE FROM skill_enablement`, `DELETE FROM skill_library_markers`,
	} {
		if _, err := s.store.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.store.db.Exec(`INSERT INTO skill_access(skill_name,bot_id) VALUES('limited-skill',?)`, bots[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.db.Exec(`INSERT INTO builtin_skill_off(skill_name,updated_at) VALUES('slides',?)`, now()); err != nil {
		t.Fatal(err)
	}
	oldRule := func(bot Bot) []string {
		out := []string{}
		for _, name := range []string{"open-skill", "also-open"} {
			out = append(out, name)
		}
		if bot.ID == bots[0].ID {
			out = append(out, "limited-skill")
		}
		for _, name := range officialNames() {
			if name != "slides" {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	if err := s.migrateSkillLibraryData(); err != nil {
		t.Fatal(err)
	}
	for _, bot := range bots {
		got, want := enabledNames(t, s, bot.ID), oldRule(bot)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: effective set changed\n got  %v\n want %v", bot.Name, got, want)
		}
	}
	// A Bot made after the migration starts with the official skills only.
	late, err := s.store.CreateBot("Late", "", "model")
	if err != nil {
		t.Fatal(err)
	}
	if got := enabledNames(t, s, late.ID); strings.Join(got, ",") != strings.Join(officialNames(), ",") {
		t.Fatalf("new Bot has %v, want only the official skills %v", got, officialNames())
	}
	// Every migrated row says it came from the migration.
	var by string
	if err := s.store.db.QueryRow(`SELECT DISTINCT set_by FROM skill_enablement`).Scan(&by); err != nil || by != skillSetByMigration {
		t.Fatalf("set_by = %q err=%v", by, err)
	}
	// The old tables are kept.
	for _, table := range []string{"skill_access", "builtin_skill_off"} {
		var n int
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n == 0 {
			t.Fatalf("%s: %d rows, %v", table, n, err)
		}
	}
}

func TestSkillMigrationRunsOnceAndOldTablesAreNotRead(t *testing.T) {
	env := newLibEnv(t)
	enableSkillsFor(t, env.s, env.a.ID, "deploy")
	if err := env.s.migrateSkillLibraryData(); err != nil {
		t.Fatal(err)
	}
	// The marker is set, so a restart does not rebuild anything from the old tables.
	status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skills/deploy", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatal(status)
	}
	if _, err := env.s.store.db.Exec(`INSERT INTO skill_access(skill_name,bot_id) VALUES('deploy',?)`, env.b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.s.store.db.Exec(`INSERT INTO builtin_skill_off(skill_name,updated_at) VALUES('slides',?)`, now()); err != nil {
		t.Fatal(err)
	}
	if err := env.s.migrateSkillLibraryData(); err != nil {
		t.Fatal(err)
	}
	if hasInDirectory(t, env.s, env.a.ID, "deploy") || hasInDirectory(t, env.s, env.b.ID, "deploy") || !hasInDirectory(t, env.s, env.a.ID, "slides") {
		t.Fatal("the old tables changed what a Bot can use")
	}
}

func TestNewBotGetsOnlyOfficialSkills(t *testing.T) {
	env := newLibEnv(t)
	got := enabledNames(t, env.s, env.a.ID)
	if strings.Join(got, ",") != strings.Join(officialNames(), ",") {
		t.Fatalf("a new Bot has %v; the owner's skills must be off until turned on", got)
	}
}

func TestDisabledSkillIsAbsentFromDirectoryAndRefusedByReadSkill(t *testing.T) {
	env := newLibEnv(t)
	enableSkillsFor(t, env.s, env.a.ID, "deploy")
	if !hasInDirectory(t, env.s, env.a.ID, "deploy") {
		t.Fatal("enabled skill missing from the directory")
	}
	read := func(bot Bot, name string) error {
		p, err := env.s.extensions.PrepareDiscoverableForBot(context.Background(), bot.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		tool, ok := toolNamed(p.Tools, "read_skill")
		if !ok {
			t.Fatal("no read_skill")
		}
		_, err = tool.Execute(context.Background(), json.RawMessage(`{"name":"`+name+`"}`))
		return err
	}
	if err := read(env.a, "deploy"); err != nil {
		t.Fatalf("enabled skill unreadable: %v", err)
	}
	if err := read(env.b, "deploy"); err == nil {
		t.Fatal("a Bot that never turned the skill on could read it")
	}
	if hasInDirectory(t, env.s, env.b.ID, "deploy") {
		t.Fatal("a skill that is off is in the directory")
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skills/deploy", map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatal(status)
	}
	if hasInDirectory(t, env.s, env.a.ID, "deploy") || read(env.a, "deploy") == nil {
		t.Fatal("a skill turned off is still offered or readable")
	}
}

func TestBotCannotChangAnotherBotsColumn(t *testing.T) {
	env := newLibEnv(t)
	toolA := botTool(t, env.s, env.a, false)
	props := toolA.Parameters["properties"].(map[string]any)
	if _, ok := props["bot_id"]; ok {
		t.Fatal("manage_extensions names a Bot")
	}
	// A Bot id smuggled into the call is refused as an unknown argument.
	if _, err := callSkillToolRaw(t, toolA, map[string]any{"action": "skill_enable", "name": "deploy", "bot_id": env.b.ID}); err == nil {
		t.Fatal("an unknown bot_id argument was accepted")
	}
	if _, err := callSkillToolRaw(t, toolA, map[string]any{"action": "skill_enable", "name": "deploy"}); err != nil {
		t.Fatal(err)
	}
	if !hasInDirectory(t, env.s, env.a.ID, "deploy") || hasInDirectory(t, env.s, env.b.ID, "deploy") {
		t.Fatal("the Bot's choice reached another Bot")
	}
	var n int
	if err := env.s.store.db.QueryRow(`SELECT COUNT(*) FROM skill_enablement WHERE bot_id<>? AND skill_name='deploy'`, env.a.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("another Bot's column has %d rows (%v)", n, err)
	}
}

func TestBotEnableAndDisableAreRecordedAndNeedNoApproval(t *testing.T) {
	env := newLibEnv(t)
	tool := botTool(t, env.s, env.a, false)
	if _, err := callSkillToolRaw(t, tool, map[string]any{"action": "skill_enable", "name": "deploy"}); err != nil {
		t.Fatalf("enable without approval: %v", err)
	}
	var by, at string
	if err := env.s.store.db.QueryRow(`SELECT set_by,set_at FROM skill_enablement WHERE bot_id=? AND skill_name='deploy'`, env.a.ID).Scan(&by, &at); err != nil || by != skillSetByBot || at == "" {
		t.Fatalf("who/when: %q %q %v", by, at, err)
	}
	if _, err := callSkillToolRaw(t, tool, map[string]any{"action": "skill_disable", "name": "slides"}); err != nil {
		t.Fatal(err)
	}
	if hasInDirectory(t, env.s, env.a.ID, "slides") || !hasInDirectory(t, env.s, env.b.ID, "slides") {
		t.Fatal("an official skill did not turn off for this Bot only")
	}
	// The owner sees who set it.
	status, body := extensionHTTP(t, env.handler, http.MethodGet, "/api/bots/"+env.a.ID+"/skills", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"set_by":"bot"`) {
		t.Fatalf("bot view %d %s", status, body)
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skills/deploy", map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatal(status)
	}
	_, body = extensionHTTP(t, env.handler, http.MethodGet, "/api/bots/"+env.a.ID+"/skills", nil)
	var view struct {
		Skills []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			SetBy   string `json:"set_by"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	for _, sk := range view.Skills {
		if sk.Name == "deploy" && (sk.Enabled || sk.SetBy != "owner") {
			t.Fatalf("owner change not recorded: %+v", sk)
		}
	}
}

func TestBotInstallEnablesOnlyItselfAndNeedsNoApproval(t *testing.T) {
	env := newLibEnv(t)
	tool := botTool(t, env.s, env.a, false)
	if _, err := callSkillToolRaw(t, tool, map[string]any{"action": "skill_install", "name": "mine", "files": skillFiles("mine", "BODY_mine")}); err != nil {
		t.Fatalf("install without approval: %v", err)
	}
	if !hasInDirectory(t, env.s, env.a.ID, "mine") {
		t.Fatal("the installing Bot does not have its skill")
	}
	if hasInDirectory(t, env.s, env.b.ID, "mine") || hasInDirectory(t, env.s, env.c.ID, "mine") {
		t.Fatal("a Bot's install reached other Bots")
	}
	row, found, err := env.s.store.libraryRow("mine")
	if err != nil || !found || row.InstallerKind != skillActorBot || row.InstallerBotID != env.a.ID || row.Version != 1 {
		t.Fatalf("library row %+v found=%v err=%v", row, found, err)
	}
	// It never overwrites, whoever asks.
	if _, err := callSkillToolRaw(t, tool, map[string]any{"action": "skill_install", "name": "mine", "files": skillFiles("mine", "OTHER")}); err == nil {
		t.Fatal("install overwrote a skill")
	}
	if _, err := callSkillToolRaw(t, tool, map[string]any{"action": "skill_install", "name": "deploy", "files": skillFiles("deploy", "EVIL")}); err == nil {
		t.Fatal("a Bot overwrote the owner's skill")
	}
	if body, _, _ := env.s.extensions.SkillFilesMap("deploy"); !strings.Contains(string(body["SKILL.md"]), "BODY_deploy") {
		t.Fatal("the owner's skill changed")
	}
	// Another Bot turns it on for itself.
	if _, err := callSkillToolRaw(t, botTool(t, env.s, env.b, false), map[string]any{"action": "skill_enable", "name": "mine"}); err != nil {
		t.Fatal(err)
	}
	if !hasInDirectory(t, env.s, env.b.ID, "mine") {
		t.Fatal("a Bot could not turn a library skill on for itself")
	}
}

func TestPublishVersionNeedsApprovalOnlyInstallerAndUpdatesEveryBot(t *testing.T) {
	env := newLibEnv(t)
	toolA := botTool(t, env.s, env.a, false)
	if _, err := callSkillToolRaw(t, toolA, map[string]any{"action": "skill_install", "name": "mine", "files": skillFiles("mine", "BODY_ONE")}); err != nil {
		t.Fatal(err)
	}
	enableSkillsFor(t, env.s, env.b.ID, "mine")
	readBody := func(bot Bot) string {
		p, err := env.s.extensions.PrepareDiscoverableForBot(context.Background(), bot.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		tool, _ := toolNamed(p.Tools, "read_skill")
		out, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"mine"}`))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	publish := map[string]any{"action": "skill_publish_version", "name": "mine", "note": "second", "files": skillFiles("mine", "BODY_TWO")}
	if code := extensionOutcomeCode(func() error { _, err := callSkillToolRaw(t, toolA, publish); return err }()); code != "extension_change_needs_approval" {
		t.Fatalf("publish without approval: %q", code)
	}
	if got := readBody(env.b); got != "BODY_ONE" {
		t.Fatalf("an unapproved publish changed the skill: %q", got)
	}
	if _, err := callSkillToolRaw(t, botTool(t, env.s, env.a, true), publish); err != nil {
		t.Fatalf("approved publish: %v", err)
	}
	// Another Bot that has the skill follows the newest version.
	if got := readBody(env.b); got != "BODY_TWO" {
		t.Fatalf("Bot B reads %q after the publish", got)
	}
	row, _, _ := env.s.store.libraryRow("mine")
	if row.Version != 2 {
		t.Fatalf("version %d", row.Version)
	}
	// Only the installer may publish, approval or not.
	other := map[string]any{"action": "skill_publish_version", "name": "mine", "files": skillFiles("mine", "HIJACK")}
	for name, tool := range map[string]Tool{"other Bot": botTool(t, env.s, env.b, true), "owner-installed skill": botTool(t, env.s, env.a, true)} {
		args := other
		if name == "owner-installed skill" {
			args = map[string]any{"action": "skill_publish_version", "name": "deploy", "files": skillFiles("deploy", "HIJACK")}
		}
		if code := extensionOutcomeCode(func() error { _, err := callSkillToolRaw(t, tool, args); return err }()); code != "skill_not_installer" {
			t.Fatalf("%s: %q", name, code)
		}
	}
	if got := readBody(env.b); got != "BODY_TWO" {
		t.Fatalf("a refused publish changed the skill: %q", got)
	}
	// The same files again change nothing.
	if _, err := callSkillToolRaw(t, botTool(t, env.s, env.a, true), publish); err == nil {
		t.Fatal("publishing identical files made a version")
	}
	// The library marks it updated and lists both versions.
	_, body := extensionHTTP(t, env.handler, http.MethodGet, "/api/extensions/skills", nil)
	var list struct {
		Skills []struct {
			Name     string `json:"name"`
			Version  int    `json:"version"`
			Updated  bool   `json:"updated"`
			Versions []struct {
				Version int  `json:"version"`
				Current bool `json:"current"`
			} `json:"versions"`
			InstalledBy struct {
				Kind    string `json:"kind"`
				BotName string `json:"bot_name"`
			} `json:"installed_by"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sk := range list.Skills {
		if sk.Name == "mine" {
			found = true
			if sk.Version != 2 || !sk.Updated || len(sk.Versions) != 2 || !sk.Versions[0].Current || sk.InstalledBy.Kind != "bot" || sk.InstalledBy.BotName != "Alpha Bot" {
				t.Fatalf("listing: %+v", sk)
			}
		}
	}
	if !found {
		t.Fatal("skill missing from the listing")
	}
	// Rollback is the owner's, and it applies to every Bot.
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/mine/rollback", map[string]any{"version": 1}); status != http.StatusOK {
		t.Fatalf("rollback status %d", status)
	}
	if got := readBody(env.b); got != "BODY_ONE" {
		t.Fatalf("after rollback Bot B reads %q", got)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/mine/rollback", map[string]any{"version": 9}); status != http.StatusNotFound {
		t.Fatalf("rollback to a missing version: %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/mine/rollback", map[string]any{"version": 1}); status != http.StatusConflict {
		t.Fatalf("rollback to the current version: %d", status)
	}
	// And forward again.
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/mine/rollback", map[string]any{"version": 2}); status != http.StatusOK {
		t.Fatalf("roll forward status %d", status)
	}
	if got := readBody(env.a); got != "BODY_TWO" {
		t.Fatalf("after roll forward Bot A reads %q", got)
	}
}

func bundleFiles(name string, skills ...string) map[string]string {
	files := map[string]string{
		".claude-plugin/plugin.json": `{"name":"` + name + `","description":"A bundle of skills","version":"1.0.0"}`,
		"commands/hello.md":          "not a skill",
		"README.md":                  "readme",
	}
	for _, sk := range skills {
		for p, text := range skillFiles(sk, "BODY_"+sk) {
			files["skills/"+sk+"/"+p] = text
		}
		files["skills/"+sk+"/scripts/run.py"] = "print('x')\n"
	}
	return files
}

func TestBundleInstallToggleAndPerMemberToggle(t *testing.T) {
	env := newLibEnv(t)
	status, body := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skill-bundles", map[string]any{"files": bundleFiles("office-kit", "alpha-tool", "beta-tool")})
	if status != http.StatusCreated {
		t.Fatalf("install bundle %d %s", status, body)
	}
	_, body = extensionHTTP(t, env.handler, http.MethodGet, "/api/extensions/skills", nil)
	var list struct {
		Skills []struct {
			Name     string `json:"name"`
			BundleID string `json:"bundle_id"`
		} `json:"skills"`
		Bundles []struct {
			ID     string   `json:"id"`
			Skills []string `json:"skills"`
		} `json:"bundles"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Bundles) != 1 || list.Bundles[0].ID != "office-kit" || strings.Join(list.Bundles[0].Skills, ",") != "alpha-tool,beta-tool" {
		t.Fatalf("bundles %+v", list.Bundles)
	}
	// Files outside skills/ are not installed.
	if _, err := os.Stat(filepath.Join(env.skills, "alpha-tool", "scripts", "run.py")); err != nil {
		t.Fatalf("skill file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.skills, "commands")); err == nil {
		t.Fatal("a non-skill folder was installed")
	}
	// Nobody has the bundle yet; turning it on for A turns on both members.
	if hasInDirectory(t, env.s, env.a.ID, "alpha-tool") {
		t.Fatal("an owner-installed bundle is on for a Bot by itself")
	}
	if status, body = extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skill-bundles/office-kit", map[string]any{"enabled": true}); status != http.StatusOK {
		t.Fatalf("bundle toggle %d %s", status, body)
	}
	if !hasInDirectory(t, env.s, env.a.ID, "alpha-tool") || !hasInDirectory(t, env.s, env.a.ID, "beta-tool") || hasInDirectory(t, env.s, env.b.ID, "alpha-tool") {
		t.Fatal("bundle toggle did not apply to exactly the one Bot's members")
	}
	// One member off, the other stays on.
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skills/beta-tool", map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatal(status)
	}
	if !hasInDirectory(t, env.s, env.a.ID, "alpha-tool") || hasInDirectory(t, env.s, env.a.ID, "beta-tool") {
		t.Fatal("per-member toggle did not isolate one skill")
	}
	// A Bot may turn a whole bundle on for itself by its name.
	if _, err := callSkillToolRaw(t, botTool(t, env.s, env.b, false), map[string]any{"action": "skill_enable", "name": "office-kit"}); err != nil {
		t.Fatal(err)
	}
	if !hasInDirectory(t, env.s, env.b.ID, "alpha-tool") || !hasInDirectory(t, env.s, env.b.ID, "beta-tool") {
		t.Fatal("a Bot could not turn on a bundle for itself")
	}
	// Removal is the bundle as a whole, never one member.
	if status, _ = extensionHTTP(t, env.handler, http.MethodDelete, "/api/extensions/skills/alpha-tool", nil); status != http.StatusConflict {
		t.Fatalf("member delete %d", status)
	}
	// A duplicate or a broken bundle installs nothing.
	if status, _ = extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skill-bundles", map[string]any{"files": bundleFiles("office-kit", "gamma-tool")}); status != http.StatusConflict {
		t.Fatalf("duplicate bundle %d", status)
	}
	broken := bundleFiles("broken-kit", "delta-tool", "epsilon-tool")
	broken["skills/epsilon-tool/SKILL.md"] = "---\nname: wrong-name\ndescription: d\n---\nb"
	if status, _ = extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skill-bundles", map[string]any{"files": broken}); status != http.StatusBadRequest {
		t.Fatalf("broken bundle %d", status)
	}
	if _, err := os.Stat(filepath.Join(env.skills, "delta-tool")); err == nil {
		t.Fatal("a broken bundle left a skill behind")
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodDelete, "/api/extensions/skill-bundles/office-kit", nil); status != http.StatusOK {
		t.Fatalf("bundle delete %d", status)
	}
	if hasInDirectory(t, env.s, env.a.ID, "alpha-tool") || len(installedSkills(env.s)) != 2 {
		t.Fatal("bundle removal left members behind")
	}
	var rows int
	if err := env.s.store.db.QueryRow(`SELECT COUNT(*) FROM skill_enablement WHERE skill_name IN ('alpha-tool','beta-tool')`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("enablement rows left: %d %v", rows, err)
	}
}

func TestDeleteIsOwnerOnlyAndBotHasNoDelete(t *testing.T) {
	env := newLibEnv(t)
	toolA := botTool(t, env.s, env.a, true)
	if _, err := callSkillToolRaw(t, toolA, map[string]any{"action": "skill_install", "name": "mine", "files": skillFiles("mine", "BODY")}); err != nil {
		t.Fatal(err)
	}
	enum := toolA.Parameters["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	for _, action := range enum {
		if strings.Contains(action, "delete") && strings.HasPrefix(action, "skill") {
			t.Fatalf("the Bot tool offers %s", action)
		}
	}
	for _, name := range []string{"mine", "deploy"} {
		if _, err := callSkillToolRaw(t, toolA, map[string]any{"action": "skill_delete", "name": name}); err == nil {
			t.Fatalf("a Bot deleted %s", name)
		}
		if !env.s.extensions.SkillExists(name) {
			t.Fatalf("%s is gone", name)
		}
	}
	if _, found, _ := env.s.store.libraryRow("mine"); !found {
		t.Fatal("library row removed by a Bot")
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodDelete, "/api/extensions/skills/mine", nil); status != http.StatusOK {
		t.Fatalf("owner delete %d", status)
	}
	if env.s.extensions.SkillExists("mine") || hasInDirectory(t, env.s, env.a.ID, "mine") {
		t.Fatal("owner delete left the skill")
	}
	if _, found, _ := env.s.store.libraryRow("mine"); found {
		t.Fatal("library row kept after delete")
	}
	var n int
	if err := env.s.store.db.QueryRow(`SELECT COUNT(*) FROM skill_enablement WHERE skill_name='mine'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("enablement rows left: %d %v", n, err)
	}
	// A skill installed again under that name is not enabled by the old rows.
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills", map[string]any{"name": "mine", "files": skillFiles("mine", "AGAIN")}); status != http.StatusCreated {
		t.Fatal(status)
	}
	if hasInDirectory(t, env.s, env.a.ID, "mine") {
		t.Fatal("a reinstalled skill came back on")
	}
}

func TestBotExportCarriesEnabledSkillNamesOnly(t *testing.T) {
	env := newLibEnv(t)
	enableSkillsFor(t, env.s, env.a.ID, "deploy")
	bundle, err := env.s.store.exportPortable(context.Background(), "inst", portableSelection{Categories: []string{"bot_config"}, BotIDs: []string{env.a.ID}}, "bot")
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Bots) != 1 || strings.Join(bundle.Bots[0].EnabledSkills, ",") != "deploy" {
		t.Fatalf("enabled skills: %+v", bundle.Bots)
	}
	raw, _ := json.Marshal(bundle)
	for _, leak := range []string{"BODY_deploy", "SKILL.md", "set_by", "installed_by"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("export carries more than a name: %q", leak)
		}
	}
	other, err := env.s.store.exportPortable(context.Background(), "inst", portableSelection{Categories: []string{"bot_config"}, BotIDs: []string{env.b.ID}}, "bot")
	if err != nil || len(other.Bots[0].EnabledSkills) != 0 {
		t.Fatalf("Bot B export: %+v %v", other.Bots, err)
	}
}

func TestUnattendedRunsMayToggleOwnSkillsButNotInstall(t *testing.T) {
	f := newScheduledMCPFixture(t, false, scheduleSourceChat)
	run := f.r
	if rooted, err := mcpRunRootedInSchedule(f.s.store.db, run); err != nil || !rooted {
		t.Fatalf("fixture run is not schedule-rooted: %v %v", rooted, err)
	}
	if _, err := f.s.applySkillAction(context.Background(), f.c, run, extensionToolArgs{Action: "skill_disable", Name: "slides"}); err != nil {
		t.Fatalf("unattended disable refused: %v", err)
	}
	if _, err := f.s.applySkillAction(context.Background(), f.c, run, extensionToolArgs{Action: "skill_enable", Name: "slides"}); err != nil {
		t.Fatalf("unattended enable refused: %v", err)
	}
	_, err := f.s.applySkillAction(context.Background(), f.c, run, extensionToolArgs{Action: "skill_install", Name: "sched", Files: skillFiles("sched", "b")})
	if extensionOutcomeCode(err) != "skill_install_needs_person" {
		t.Fatalf("unattended install: %v", err)
	}
}

func TestSkillLibraryReconcilesWithFiles(t *testing.T) {
	env := newLibEnv(t)
	enableSkillsFor(t, env.s, env.a.ID, "deploy")
	// A folder removed behind the library's back: the row goes, with its enablement.
	if err := os.RemoveAll(filepath.Join(env.skills, "deploy")); err != nil {
		t.Fatal(err)
	}
	// A folder put there by hand gets a row with an unknown installer and is on for nobody.
	if err := os.MkdirAll(filepath.Join(env.skills, "by-hand"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.skills, "by-hand", "SKILL.md"), []byte("---\nname: by-hand\ndescription: d\n---\nb"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Files changed in place: the table records what is on disk.
	if err := os.WriteFile(filepath.Join(env.skills, "notes", "SKILL.md"), []byte("---\nname: notes\ndescription: edited\n---\nEDITED"), 0o600); err != nil {
		t.Fatal(err)
	}
	diags, err := env.s.reconcileSkillLibrary()
	if err != nil || len(diags) != 3 {
		t.Fatalf("diagnostics %v err=%v", diags, err)
	}
	if _, found, _ := env.s.store.libraryRow("deploy"); found {
		t.Fatal("row of a deleted folder kept")
	}
	row, found, _ := env.s.store.libraryRow("by-hand")
	if !found || row.InstallerKind != skillActorLegacy {
		t.Fatalf("by-hand row %+v found=%v", row, found)
	}
	if hasInDirectory(t, env.s, env.a.ID, "by-hand") || hasInDirectory(t, env.s, env.a.ID, "deploy") {
		t.Fatal("a reconciled skill came on by itself")
	}
	notes, _, _ := env.s.store.libraryRow("notes")
	_, digest, _ := env.s.extensions.SkillFilesMap("notes")
	if notes.Digest != digest {
		t.Fatalf("digest %q want %q", notes.Digest, digest)
	}
	// A second pass finds nothing to say.
	if diags, _ := env.s.reconcileSkillLibrary(); len(diags) != 0 {
		t.Fatalf("second pass: %v", diags)
	}
}

func TestSkillLibraryRoutesRejectBadInput(t *testing.T) {
	env := newLibEnv(t)
	if status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/nobody/skills/deploy", map[string]any{"enabled": true}); status != http.StatusNotFound {
		t.Fatalf("unknown Bot %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/skill-bundles/nothing", map[string]any{"enabled": true}); status != http.StatusNotFound {
		t.Fatalf("unknown bundle %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills", map[string]any{"name": "deploy", "files": skillFiles("deploy", "x")}); status != http.StatusBadRequest {
		t.Fatalf("duplicate install %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/deploy/versions", map[string]any{"files": skillFiles("deploy", "BODY_deploy")}); status != http.StatusConflict {
		t.Fatalf("unchanged owner publish %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills/deploy/versions", map[string]any{"files": skillFiles("deploy", "NEW"), "note": "n"}); status != http.StatusOK {
		t.Fatalf("owner publish %d", status)
	}
	if status, _ := extensionHTTP(t, env.handler, http.MethodDelete, "/api/bots/"+env.a.ID+"/skills", nil); status != http.StatusMethodNotAllowed {
		t.Fatalf("bad method %d", status)
	}
}
