package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func skillToolFor(t *testing.T, s *Server, bot Bot) Tool {
	t.Helper()
	c, r := teamTestRun(t, s, bot)
	return s.extensionManagementTools(c, r)[0]
}

func callSkillTool(t *testing.T, tool Tool, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), raw)
}

func TestRestrictedSkillCannotBeDeletedOrReplacedThroughTool(t *testing.T) {
	e := newSkillAccessEnv(t)
	if status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != 200 {
		t.Fatalf("put %d %s", status, body)
	}
	toolA, toolB := skillToolFor(t, e.s, e.a), skillToolFor(t, e.s, e.b)
	install := map[string]any{"action": "skill_install", "name": "deploy", "files": map[string]string{"SKILL.md": "---\nname: deploy\ndescription: attacker\n---\nEVIL"}}
	del := map[string]any{"action": "skill_delete", "name": "deploy"}
	for _, tc := range []struct {
		who  string
		tool Tool
		args map[string]any
		want string
	}{
		{"B delete", toolB, del, "not found"},
		{"B install", toolB, install, "not found"},
		{"A delete", toolA, del, "Settings"},
		{"A install", toolA, install, "Settings"},
	} {
		_, err := callSkillTool(t, tc.tool, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v want %q", tc.who, err, tc.want)
		}
	}
	body, err := os.ReadFile(filepath.Join(e.skills, "deploy", "SKILL.md"))
	if err != nil || strings.Contains(string(body), "EVIL") {
		t.Fatalf("skill files changed: %v %q", err, body)
	}
	if m, _ := e.s.store.skillAccessMap(); len(m["deploy"]) != 1 {
		t.Fatalf("rows changed: %v", m)
	}
	// Unrestricted skills still work for any Bot.
	if _, err := callSkillTool(t, toolB, map[string]any{"action": "skill_delete", "name": "notes"}); err != nil {
		t.Fatalf("unrestricted delete: %v", err)
	}
}

func TestSkillAccessRowsKeptWhileSkillStillInstalled(t *testing.T) {
	e := newSkillAccessEnv(t)
	if status, body := e.put(t, "deploy", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != 200 {
		t.Fatalf("put %d %s", status, body)
	}
	// Archived Bot ids are hidden from the GET view but the rows stay.
	if _, err := e.s.store.SetBotArchived(e.a.ID, true); err != nil {
		t.Fatal(err)
	}
	got := e.list(t)["deploy"].Access
	if got.Mode != "selected" || len(got.BotIDs) != 0 {
		t.Fatalf("view = %+v", got)
	}
	if m, _ := e.s.store.skillAccessMap(); len(m["deploy"]) != 1 {
		t.Fatal("rows removed")
	}
}

func TestSkillAccessPutAllowsMalformedSkillDirectory(t *testing.T) {
	e := newSkillAccessEnv(t)
	dir := filepath.Join(e.skills, "broken")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("not a manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, body := e.put(t, "broken", map[string]any{"mode": "selected", "bot_ids": []string{e.a.ID}}); status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
}

func TestSkillAccessPutIsAdminOnlyInMultiAccountMode(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "admin@example.test", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	adminCookie := accountCookie(t, g, admin)
	w := accountRequest(g, "POST", "/api/admin/accounts", `{"username":"alice","password":"SyntheticPassword123!"}`, adminCookie)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var user Account
	if json.Unmarshal(w.Body.Bytes(), &user) != nil {
		t.Fatal("decode")
	}
	cookie := accountCookie(t, g, user)
	changed := accountRequest(g, "POST", "/api/auth/password", `{"current_password":"SyntheticPassword123!","password":"ChangedPassword123!"}`, cookie)
	if changed.Code != 200 {
		t.Fatalf("change %d", changed.Code)
	}
	cookie = changed.Result().Cookies()[0]
	body := `{"mode":"all"}`
	w = accountRequest(g, "PUT", "/api/extensions/skills/ghost/access", body, cookie)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("user PUT %d %s", w.Code, w.Body.String())
	}
	w = accountRequest(g, "PUT", "/api/extensions/skills/ghost/access", body, adminCookie)
	if w.Code != 404 {
		t.Fatalf("admin PUT should pass the gate and 404 on the missing skill, got %d %s", w.Code, w.Body.String())
	}
}
