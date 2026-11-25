package app

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func observedTool(t *testing.T, s *Server, server, name string, readOnly bool) {
	t.Helper()
	d := extensions.MCPToolDescriptor{Server: server, RemoteName: name, Description: name + " tool", InputSchema: map[string]any{"type": "object"}, Annotations: extensions.MCPToolAnnotations{Present: true, ReadOnly: readOnly}}
	d.Digest = extensions.MCPToolDigest(d)
	if err := s.extensions.ObserveTools(server, extensions.MCPServerConfig{URL: "https://" + server + ".example.test/mcp"}, []extensions.MCPToolDescriptor{d}); err != nil {
		t.Fatal(err)
	}
}

func botTierOf(t *testing.T, env libEnv, bot, server, tool string) extensions.MCPToolTierView {
	t.Helper()
	status, body := extensionHTTP(t, env.handler, http.MethodGet, "/api/bots/"+bot+"/tool-tiers/"+server, nil)
	var list extensions.MCPToolTierList
	if status != http.StatusOK || json.Unmarshal(body, &list) != nil {
		t.Fatalf("list: %d %s", status, body)
	}
	for _, v := range list.Tools {
		if v.RemoteName == tool {
			return v
		}
	}
	t.Fatalf("no tool %s in %s", tool, body)
	return extensions.MCPToolTierView{}
}

// Every Bot follows the connection's tier until its own is set; the existing global value is the default with nothing copied.
func TestBotToolTierFollowsGlobalAndOverridesPerBot(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	observedTool(t, env.s, "notion", "create_page", false)
	if _, err := env.s.extensions.SetToolTierOverride("notion", "create_page", "high_risk"); err != nil {
		t.Fatal(err)
	}
	for _, bot := range []Bot{env.a, env.b} {
		if v := botTierOf(t, env, bot.ID, "notion", "create_page"); v.Tier != "high_risk" || v.BotTier != "" || v.GlobalTier != "high_risk" {
			t.Fatalf("%s must start on the global value: %+v", bot.Name, v)
		}
	}
	status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "read_only"})
	if status != http.StatusOK {
		t.Fatalf("set: %d %s", status, body)
	}
	if v := botTierOf(t, env, env.a.ID, "notion", "create_page"); v.Tier != "read_only" || v.BotTier != "read_only" || v.GlobalTier != "high_risk" || v.Source != "bot" {
		t.Fatalf("Bot A: %+v", v)
	}
	if v := botTierOf(t, env, env.b.ID, "notion", "create_page"); v.Tier != "high_risk" || v.BotTier != "" {
		t.Fatalf("Bot B moved with Bot A: %+v", v)
	}
	if g := env.s.extensions.MCPToolTier("notion", "create_page"); g.Tier != extensions.MCPTierHighRisk {
		t.Fatalf("the global tier moved: %+v", g)
	}
	// A global edit moves a Bot that has not chosen; the Bot that has keeps its own.
	if _, err := env.s.extensions.SetToolTierOverride("notion", "create_page", "write"); err != nil {
		t.Fatal(err)
	}
	if v := botTierOf(t, env, env.b.ID, "notion", "create_page"); v.Tier != "write" {
		t.Fatalf("Bot B must follow the new global: %+v", v)
	}
	if v := botTierOf(t, env, env.a.ID, "notion", "create_page"); v.Tier != "read_only" {
		t.Fatalf("Bot A keeps its own: %+v", v)
	}
	// Empty follows the global tier again.
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": ""}); status != http.StatusOK {
		t.Fatal(status)
	}
	if v := botTierOf(t, env, env.a.ID, "notion", "create_page"); v.Tier != "write" || v.BotTier != "" {
		t.Fatalf("cleared: %+v", v)
	}
}

func TestBotToolTierRefusals(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	observedTool(t, env.s, "notion", "create_page", false)
	put := func(bot, path string, body any) int {
		status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+bot+"/tool-tiers/"+path, body)
		return status
	}
	if s := put(env.a.ID, "notion/create_page", map[string]any{"tier": "loud"}); s != http.StatusBadRequest {
		t.Fatalf("an unknown tier is refused, got %d", s)
	}
	if s := put(env.a.ID, "notion/create_page", map[string]any{}); s != http.StatusBadRequest {
		t.Fatalf("a missing tier is refused, got %d", s)
	}
	if s := put(env.a.ID, "notion/never_seen", map[string]any{"tier": "write"}); s != http.StatusNotFound {
		t.Fatalf("an unobserved tool is 404, got %d", s)
	}
	if s := put(env.a.ID, "missing/create_page", map[string]any{"tier": "write"}); s != http.StatusNotFound {
		t.Fatalf("an unknown connection is 404, got %d", s)
	}
	if s := put("no-such-bot", "notion/create_page", map[string]any{"tier": "write"}); s != http.StatusNotFound {
		t.Fatalf("an unknown Bot is 404, got %d", s)
	}
}

// The migration only creates the table, so it can run on every start; raising a tool to always-ask ends that Bot's standing allowance
// and no other Bot's; deleting a Bot or a connection clears its rows.
func TestBotToolTierMigrationAllowanceAndCleanup(t *testing.T) {
	env := newLibEnv(t)
	if err := migrateBotToolTiers(env.s.store.db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	addConnection(t, env.s, "notion")
	observedTool(t, env.s, "notion", "create_page", false)
	for _, bot := range []Bot{env.a, env.b} {
		if _, err := env.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,credential_target,tool_digest,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, "al-"+bot.ID, "acct", bot.ID, "notion", "create_page", "f", "c", "d", "q", now(), "owner"); err != nil {
			t.Fatal(err)
		}
	}
	if status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "high_risk"}); status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	revoked := func(bot string) bool {
		var at string
		if err := env.s.store.db.QueryRow(`SELECT revoked_at FROM mcp_standing_allowances WHERE bot_id=?`, bot).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at != ""
	}
	if !revoked(env.a.ID) || revoked(env.b.ID) {
		t.Fatal("always-ask must end this Bot's allowance only")
	}
	count := func() int {
		var n int
		if err := env.s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_tool_tiers`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.b.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "read_only"})
	if count() != 2 {
		t.Fatalf("rows = %d", count())
	}
	if _, err := env.s.store.DeleteBot(env.b.ID); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("deleting a Bot must clear its rows, rows = %d", count())
	}
	if err := env.s.deleteMCPServer("notion"); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("removing a connection must clear its rows, rows = %d", count())
	}
}
