package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type skillAccessFixtureEnv struct {
	s       *Server
	handler http.Handler
	a, b, c Bot
}

func newSkillAccessEnv(t *testing.T) skillAccessFixtureEnv {
	t.Helper()
	s, err := NewServer(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	env := skillAccessFixtureEnv{s: s, handler: s.Handler()}
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

type listedSkill struct {
	Name   string `json:"name"`
	Access struct {
		Mode   string   `json:"mode"`
		BotIDs []string `json:"bot_ids"`
	} `json:"access"`
}

func (e skillAccessFixtureEnv) list(t *testing.T) map[string]listedSkill {
	t.Helper()
	status, body := extensionHTTP(t, e.handler, http.MethodGet, "/api/extensions/skills", nil)
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	var out struct {
		Skills []listedSkill `json:"skills"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	m := map[string]listedSkill{}
	for _, sk := range out.Skills {
		m[sk.Name] = sk
	}
	return m
}

func (e skillAccessFixtureEnv) put(t *testing.T, skill string, body any) (int, []byte) {
	t.Helper()
	return extensionHTTP(t, e.handler, http.MethodPut, "/api/extensions/skills/"+skill+"/access", body)
}

func TestSkillAccessCRUDAndDefaultAll(t *testing.T) {
	e := newSkillAccessEnv(t)
	got := e.list(t)
	if got["deploy"].Access.Mode != "all" || got["notes"].Access.Mode != "all" {
		t.Fatalf("default access = %+v", got)
	}
	cursor := e.s.store.workspaceEventCursor()
	status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID, e.a.ID, e.b.ID}})
	if status != http.StatusOK {
		t.Fatalf("put status=%d body=%s", status, body)
	}
	if events := configEventsAfter(t, e.s, cursor); len(events) != 1 {
		t.Fatalf("expected one config event, got %+v", events)
	}
	got = e.list(t)
	if got["deploy"].Access.Mode != "selected" || len(got["deploy"].Access.BotIDs) != 2 || got["notes"].Access.Mode != "all" {
		t.Fatalf("after selected = %+v", got)
	}
	// Reinstalling under the same name keeps the rows.
	if err := e.s.extensions.DeleteSkill("deploy"); err != nil {
		t.Fatal(err)
	}
	if status, body = extensionHTTP(t, e.handler, http.MethodPost, "/api/extensions/skills", map[string]any{
		"name": "deploy", "files": map[string]string{"SKILL.md": "---\nname: deploy\ndescription: deploy skill\n---\nBODY"},
	}); status != http.StatusCreated {
		t.Fatalf("reinstall status=%d body=%s", status, body)
	}
	if e.list(t)["deploy"].Access.Mode != "selected" {
		t.Fatal("reinstall dropped access rows")
	}
	if status, body = e.put(t, "deploy", map[string]any{"mode": "all"}); status != http.StatusOK {
		t.Fatalf("put all status=%d body=%s", status, body)
	}
	if e.list(t)["deploy"].Access.Mode != "all" {
		t.Fatal("mode all did not clear restriction")
	}
}

func TestSkillAccessPutValidation(t *testing.T) {
	e := newSkillAccessEnv(t)
	if _, err := e.s.store.SetBotArchived(e.c.ID, true); err != nil {
		t.Fatal(err)
	}
	cursor := e.s.store.workspaceEventCursor()
	cases := []struct {
		name  string
		skill string
		body  any
		want  int
		code  string
	}{
		{"unknown bot", "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID, "no-such-bot"}}, 400, "unknown_bot"},
		{"archived bot", "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.c.ID}}, 400, "unknown_bot"},
		{"empty selected", "deploy", map[string]any{"mode": "selected", "bot_ids": []string{}}, 400, "invalid_request"},
		{"missing ids", "deploy", map[string]any{"mode": "selected"}, 400, "invalid_request"},
		{"bad mode", "deploy", map[string]any{"mode": "some"}, 400, "invalid_request"},
		{"unknown skill", "ghost", map[string]any{"mode": "all"}, 404, ""},
	}
	for _, c := range cases {
		status, body := e.put(t, c.skill, c.body)
		if status != c.want || (c.code != "" && !strings.Contains(string(body), c.code)) {
			t.Fatalf("%s: status=%d body=%s", c.name, status, body)
		}
	}
	if events := configEventsAfter(t, e.s, cursor); len(events) != 0 {
		t.Fatalf("rejected PUTs emitted events: %+v", events)
	}
	if e.list(t)["deploy"].Access.Mode != "all" {
		t.Fatal("rejected PUT changed access")
	}
}

func TestSkillAccessCascadesOnDeleteSkillAndSurvivesBotArchive(t *testing.T) {
	e := newSkillAccessEnv(t)
	if status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != 200 {
		t.Fatalf("put %d %s", status, body)
	}
	if _, err := e.s.store.SetBotArchived(e.a.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := e.list(t)["deploy"].Access; got.Mode != "selected" || len(got.BotIDs) != 1 {
		t.Fatalf("archive changed rows: %+v", got)
	}
	status, body := extensionHTTP(t, e.handler, http.MethodDelete, "/api/extensions/skills/deploy", nil)
	if status != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", status, body)
	}
	m, err := e.s.store.skillAccessMap()
	if err != nil || len(m["deploy"]) != 0 {
		t.Fatalf("rows survived skill delete: %v err=%v", m, err)
	}
}

func TestSkillAccessRunPathFiltersByBot(t *testing.T) {
	e := newSkillAccessEnv(t)
	if status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != 200 {
		t.Fatalf("put %d %s", status, body)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		bot         Bot
		wantDeploy  bool
		wantNotes   bool
		botName     string
		readAllowed bool
	}{{e.a, true, true, "A", true}, {e.b, false, true, "B", false}} {
		p, err := e.s.extensions.PrepareDiscoverableForBot(ctx, tc.bot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p.Instructions, "deploy") != tc.wantDeploy || strings.Contains(p.Instructions, "notes") != tc.wantNotes {
			t.Fatalf("bot %s instructions = %q", tc.botName, p.Instructions)
		}
		for _, tool := range p.Tools {
			if tool.Name != "read_skill" {
				continue
			}
			_, err := tool.Execute(ctx, json.RawMessage(`{"name":"deploy"}`))
			if (err == nil) != tc.readAllowed {
				t.Fatalf("bot %s read_skill deploy err=%v", tc.botName, err)
			}
		}
		_ = p.Close()
	}
	views, _ := e.s.extensions.SkillsForBot(ctx, e.b.ID)
	if len(views) != 1 || views[0].Name != "notes" {
		t.Fatalf("manage_extensions listing for B = %+v", views)
	}
}

func TestSkillAccessStoreErrorWithholdsAllSkills(t *testing.T) {
	e := newSkillAccessEnv(t)
	if status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != 200 {
		t.Fatalf("put %d %s", status, body)
	}
	if _, err := e.s.store.db.Exec(`DROP TABLE skill_access`); err != nil {
		t.Fatal(err)
	}
	p, err := e.s.extensions.PrepareDiscoverableForBot(context.Background(), e.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if strings.Contains(p.Instructions, "deploy") || strings.Contains(p.Instructions, "notes") {
		t.Fatalf("skills granted with broken access store: %q", p.Instructions)
	}
	for _, tool := range p.Tools {
		if tool.Name == "read_skill" {
			if _, err := tool.Execute(context.Background(), json.RawMessage(`{"name":"deploy"}`)); err == nil {
				t.Fatal("read_skill succeeded with broken access store")
			}
		}
	}
	if views, _ := e.s.extensions.SkillsForBot(context.Background(), e.a.ID); len(views) != 0 {
		t.Fatalf("SkillsForBot granted: %+v", views)
	}
}
