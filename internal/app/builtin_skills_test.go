package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

type listedBuiltin struct {
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
	Enabled *bool  `json:"enabled"`
}

func listSkillsHTTP(t *testing.T, h http.Handler) map[string]listedBuiltin {
	t.Helper()
	status, body := extensionHTTP(t, h, http.MethodGet, "/api/extensions/skills", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	var out struct {
		Skills []listedBuiltin `json:"skills"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	m := map[string]listedBuiltin{}
	for _, s := range out.Skills {
		m[s.Name] = s
	}
	return m
}

func directoryOf(t *testing.T, s *Server, bot string) string {
	t.Helper()
	p, err := s.extensions.PrepareDiscoverableForBot(context.Background(), bot)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	return p.Instructions
}

func TestBuiltinSkillsAreListedAsBuiltinAndOnByDefault(t *testing.T) {
	env := newSkillAccessEnv(t)
	got := env.list(t)
	if len(got) == 0 {
		t.Fatal("no skills")
	}
	listed := listSkillsHTTP(t, env.handler)
	for _, name := range []string{"spreadsheet", "document-report", "slides", "web-page", "code-delivery"} {
		item, ok := listed[name]
		if !ok || !item.Builtin || item.Enabled == nil || !*item.Enabled {
			t.Fatalf("%s = %+v ok=%v", name, item, ok)
		}
		if !strings.Contains(directoryOf(t, env.s, env.a.ID), `"name":"`+name+`"`) {
			t.Fatalf("%s is not offered to a Bot by default", name)
		}
	}
	if listed["deploy"].Builtin || listed["deploy"].Enabled != nil {
		t.Fatalf("installed skill reported as built-in: %+v", listed["deploy"])
	}
}

func TestBuiltinSkillCanBeTurnedOffAndOn(t *testing.T) {
	env := newSkillAccessEnv(t)
	status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/skills/slides/enabled", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", status, body)
	}
	if item := listSkillsHTTP(t, env.handler)["slides"]; item.Enabled == nil || *item.Enabled {
		t.Fatalf("slides still reported on: %+v", item)
	}
	dir := directoryOf(t, env.s, env.b.ID)
	if strings.Contains(dir, `"name":"slides"`) || !strings.Contains(dir, `"name":"spreadsheet"`) {
		t.Fatalf("directory after turning slides off: %q", dir)
	}
	// The switch survives a restart of the store: it is a row, not memory.
	off, err := env.s.store.builtinSkillsOff(context.Background())
	if err != nil || !off["slides"] || len(off) != 1 {
		t.Fatalf("off=%v err=%v", off, err)
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/skills/slides/enabled", map[string]any{"enabled": true}); status != http.StatusOK {
		t.Fatalf("enable status=%d", status)
	}
	if !strings.Contains(directoryOf(t, env.s, env.b.ID), `"name":"slides"`) {
		t.Fatal("slides did not come back")
	}
	// Only built-in skills have the switch; the body must say true or false.
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/skills/deploy/enabled", map[string]any{"enabled": false}); status != http.StatusNotFound {
		t.Fatalf("installed skill switch status=%d", status)
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/skills/slides/enabled", map[string]any{}); status != http.StatusBadRequest {
		t.Fatalf("empty body status=%d", status)
	}
}

func TestBuiltinSkillsCannotBeDeletedOrReplacedAsUserSkills(t *testing.T) {
	env := newSkillAccessEnv(t)
	status, body := extensionHTTP(t, env.handler, http.MethodDelete, "/api/extensions/skills/spreadsheet", nil)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "built-in") {
		t.Fatalf("delete status=%d body=%s", status, body)
	}
	status, _ = extensionHTTP(t, env.handler, http.MethodPost, "/api/extensions/skills", map[string]any{
		"name": "spreadsheet", "files": map[string]string{"SKILL.md": "---\nname: spreadsheet\ndescription: mine\n---\nbody"}})
	if status != http.StatusBadRequest {
		t.Fatalf("install over built-in status=%d", status)
	}
	if _, ok := listSkillsHTTP(t, env.handler)["spreadsheet"]; !ok {
		t.Fatal("spreadsheet vanished")
	}
	// The model-facing manage_extensions route reaches the same guard.
	if err := env.s.deleteSkill("spreadsheet"); err == nil {
		t.Fatal("deleteSkill removed a built-in skill")
	}
}

func TestBuiltinSkillAccessCanStillLimitItToSelectedBots(t *testing.T) {
	env := newSkillAccessEnv(t)
	status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/skills/web-page/access", map[string]any{"mode": "selected", "bot_ids": []string{env.a.ID}})
	if status != http.StatusOK {
		t.Fatalf("access status=%d body=%s", status, body)
	}
	if !strings.Contains(directoryOf(t, env.s, env.a.ID), `"name":"web-page"`) || strings.Contains(directoryOf(t, env.s, env.b.ID), `"name":"web-page"`) {
		t.Fatal("selected-Bot access did not apply to a built-in skill")
	}
}

func TestBuiltinSkillLoadsOnDemandAndItsScriptsReachTheComputer(t *testing.T) {
	s, root, statuses := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	conv, _ := s.store.GetConversation(bot.DMConversationID)
	run := Run{ID: "skill-run", BotID: bot.ID, ConversationID: conv.ID}
	prepared, err := s.extensions.PrepareDiscoverableForBot(context.Background(), bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	// Nothing is on the computer, and no body is in the prompt, until a skill is read.
	if len(*statuses) != 0 || strings.Contains(prepared.Instructions, "## Principles") {
		t.Fatalf("eager work: transfers=%v", *statuses)
	}
	tools := s.withBuiltinSkillStaging(run, prepared.Tools)
	read, ok := toolNamed(tools, "read_skill")
	if !ok {
		t.Fatal("no read_skill tool")
	}
	out, err := read.Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet"}`))
	if err != nil {
		t.Fatal(err)
	}
	files, digest, err := extensions.BuiltinSkillFiles("spreadsheet")
	if err != nil {
		t.Fatal(err)
	}
	onDisk := filepath.Join(root, "home", "uploads", ".skills", "spreadsheet", digest)
	dir := guestSkillsDir("spreadsheet", digest)
	if !strings.Contains(out, dir) || !strings.Contains(out, "# Spreadsheet work") {
		t.Fatalf("read_skill result lacks the folder or the body:\n%.300s", out)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(onDisk, filepath.FromSlash(f.Path)))
		if err != nil || string(got) != string(f.Data) {
			t.Fatalf("%s on the computer: %d bytes, %v", f.Path, len(got), err)
		}
	}
	// A second read sends nothing new, and a fresh tool set (a later run) finds the files already there.
	before := len(*statuses)
	if _, err = read.Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet"}`)); err != nil || len(*statuses) != before {
		t.Fatalf("repeat read transferred %d more (err %v)", len(*statuses)-before, err)
	}
	again, _ := toolNamed(s.withBuiltinSkillStaging(run, prepared.Tools), "read_skill")
	if _, err = again.Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet"}`)); err != nil {
		t.Fatal(err)
	}
	for _, status := range (*statuses)[before:] {
		if status != "exists" {
			t.Fatalf("second run wrote %q", status)
		}
	}
	// The staged script is a working program.
	if py, lookErr := exec.LookPath("python3"); lookErr == nil {
		cmd := exec.Command(py, filepath.Join(onDisk, "scripts", "reconcile.py"), "--help")
		if help, runErr := cmd.CombinedOutput(); runErr != nil || !strings.Contains(string(help), "Reconcile figures") {
			t.Fatalf("staged script: %v %s", runErr, help)
		}
	} else {
		t.Log("python3 not installed: staged script not executed")
	}
}

func TestInstalledSkillsAndComputerlessBotsAreLeftAlone(t *testing.T) {
	env := newSkillAccessEnv(t)
	prepared, err := env.s.extensions.PrepareDiscoverableForBot(context.Background(), env.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	// No computer: the body is returned unchanged.
	plain, _ := toolNamed(env.s.withBuiltinSkillStaging(Run{BotID: env.a.ID}, prepared.Tools), "read_skill")
	body, err := plain.Execute(context.Background(), json.RawMessage(`{"name":"spreadsheet"}`))
	if err != nil || strings.Contains(body, "files are on the computer") || !strings.Contains(body, "# Spreadsheet work") {
		t.Fatalf("computerless read: %v %.120s", err, body)
	}
	s, _, statuses := newGuestInputServer(t)
	bot, _ := s.store.CreateBot("synthetic", "", "model")
	if err := s.extensions.InstallSkill("notes", map[string][]byte{"SKILL.md": []byte("---\nname: notes\ndescription: notes skill\n---\nBODY_notes")}); err != nil {
		t.Skipf("no skills directory in this fixture: %v", err)
	}
	p2, err := s.extensions.PrepareDiscoverableForBot(context.Background(), bot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	read, _ := toolNamed(s.withBuiltinSkillStaging(Run{ID: "r", BotID: bot.ID}, p2.Tools), "read_skill")
	got, err := read.Execute(context.Background(), json.RawMessage(`{"name":"notes"}`))
	if err != nil || got != "BODY_notes" || len(*statuses) != 0 {
		t.Fatalf("installed skill read=%q err=%v transfers=%v", got, err, *statuses)
	}
}

// installedSkills lists the skills a person installed, leaving out the built-in ones that ship with the product.
func installedSkills(s *Server) []extensions.SkillView {
	var out []extensions.SkillView
	all, _ := s.extensions.ListSkills()
	for _, v := range all {
		if !v.Builtin {
			out = append(out, v)
		}
	}
	return out
}
