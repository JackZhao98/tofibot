package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// Per-Bot connection access (docs/design/tools-and-connections.md section 3.7): no row means open, the owner limits a
// connection to some Bots, and a closed connection is not listed, searched or described to that Bot.

func addConnection(t *testing.T, s *Server, name string) {
	t.Helper()
	if err := s.extensions.SaveMCP(name, extensions.MCPServerConfig{URL: "https://" + name + ".example.test/mcp"}, false); err != nil {
		t.Fatal(err)
	}
}

func namesIn(t *testing.T, s *Server, botID, name string) bool {
	t.Helper()
	return strings.Contains(directoryOf(t, s, botID), `"`+name+`"`)
}

func TestConnectionAccessDefaultsOpen(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	for _, bot := range []Bot{env.a, env.b, env.c} {
		if !namesIn(t, env.s, bot.ID, "notion") {
			t.Fatalf("%s cannot see a connection nobody limited", bot.Name)
		}
	}
	status, body := extensionHTTP(t, env.handler, http.MethodGet, "/api/bots/"+env.a.ID+"/connections", nil)
	var listed struct {
		Connections []connectionAccessRow `json:"connections"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &listed) != nil || len(listed.Connections) != 1 || listed.Connections[0].Server != "notion" || !listed.Connections[0].Enabled || listed.Connections[0].SetBy != "" {
		t.Fatalf("default listing: %d %s", status, body)
	}
}

func TestConnectionAccessFiltersDiscovery(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	addConnection(t, env.s, "github")
	status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/connections/notion", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("close for one Bot: %d %s", status, body)
	}
	if namesIn(t, env.s, env.a.ID, "notion") || !namesIn(t, env.s, env.a.ID, "github") {
		t.Fatalf("Bot A: notion closed, github open, got %s", directoryOf(t, env.s, env.a.ID))
	}
	if !namesIn(t, env.s, env.b.ID, "notion") {
		t.Fatal("closing a connection for one Bot closed it for another")
	}
	// The tool search facade is built from the same list, so a closed connection has nothing to search.
	p, err := env.s.extensions.PrepareDiscoverableForBot(context.Background(), env.a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range p.Tools {
		if strings.Contains(tool.Description, "notion") {
			t.Fatalf("a tool still names the closed connection: %s", tool.Name)
		}
	}
	p.Close()
	// "Chosen Bots": exactly the listed ones, through the connection's own route.
	status, body = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/mcp/github/bots", map[string]any{"bot_ids": []string{env.b.ID}})
	if status != http.StatusOK {
		t.Fatalf("chosen Bots: %d %s", status, body)
	}
	if namesIn(t, env.s, env.a.ID, "github") || !namesIn(t, env.s, env.b.ID, "github") || namesIn(t, env.s, env.c.ID, "github") {
		t.Fatal("only the chosen Bot sees github")
	}
	var bots struct {
		All  bool               `json:"all"`
		Bots []connectionBotRow `json:"bots"`
	}
	status, body = extensionHTTP(t, env.handler, http.MethodGet, "/api/extensions/mcp/github/bots", nil)
	if status != http.StatusOK || json.Unmarshal(body, &bots) != nil || bots.All || len(bots.Bots) != 3 {
		t.Fatalf("connection Bots: %d %s", status, body)
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/mcp/github/bots", map[string]any{"bot_ids": []string{"no-such-bot"}}); status != http.StatusBadRequest {
		t.Fatalf("an unknown Bot id is refused, got %d", status)
	}
	status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/extensions/mcp/github/bots", map[string]any{"all": true})
	if status != http.StatusOK || !namesIn(t, env.s, env.a.ID, "github") || !namesIn(t, env.s, env.c.ID, "github") {
		t.Fatal("All Bots opens it again")
	}
	if status, _ = extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/connections/missing", map[string]any{"enabled": false}); status != http.StatusNotFound {
		t.Fatalf("an unknown connection is 404, got %d", status)
	}
}

// A lookup that fails must hide every connection, never show them all.
func TestConnectionAccessFailsClosed(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	if _, err := env.s.store.db.Exec(`DROP TABLE bot_connection_access`); err != nil {
		t.Fatal(err)
	}
	if namesIn(t, env.s, env.a.ID, "notion") {
		t.Fatal("an unreadable access table opened every connection")
	}
}

func TestConnectionAccessClearedOnBotDelete(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	if status, body := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.c.ID+"/connections/notion", map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatalf("%d %s", status, body)
	}
	if _, err := env.s.store.DeleteBot(env.c.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := env.s.store.db.QueryRow(`SELECT COUNT(*) FROM bot_connection_access WHERE bot_id=?`, env.c.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows left for a deleted Bot: %d %v", n, err)
	}
	// Removing the connection clears its rows too, so one added later under the same name starts open.
	if status, _ := extensionHTTP(t, env.handler, http.MethodPut, "/api/bots/"+env.a.ID+"/connections/notion", map[string]any{"enabled": false}); status != http.StatusOK {
		t.Fatal("close for A")
	}
	if err := env.s.deleteMCPServer("notion"); err != nil {
		t.Fatal(err)
	}
	addConnection(t, env.s, "notion")
	if !namesIn(t, env.s, env.a.ID, "notion") {
		t.Fatal("a stale choice outlived its connection")
	}
}

func TestManageExtensionsListsOnlyAccessible(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	addConnection(t, env.s, "github")
	if err := env.s.store.setConnectionAccess(env.a.ID, "notion", false); err != nil {
		t.Fatal(err)
	}
	list := func(bot Bot) string {
		out, err := botTool(t, env.s, bot, false).Execute(context.Background(), json.RawMessage(`{"action":"mcp_list"}`))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if a := list(env.a); strings.Contains(a, `"notion"`) || !strings.Contains(a, `"github"`) {
		t.Fatalf("Bot A lists a closed connection: %s", a)
	}
	if b := list(env.b); !strings.Contains(b, `"notion"`) || !strings.Contains(b, `"github"`) {
		t.Fatalf("Bot B lists too little: %s", b)
	}
}

func TestStandingAllowancesFilterByBot(t *testing.T) {
	f := newAutoReviewFixture(t)
	approveAlways(t, f)
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 1}, {"?bot=" + f.r.BotID, 1}, {"?bot=someone-else", 0}} {
		status, body := extensionHTTP(t, f.s.Handler(), http.MethodGet, "/api/standing-allowances"+tc.query, nil)
		var out struct {
			Allowances []MCPStandingAllowance `json:"allowances"`
		}
		if status != http.StatusOK || json.Unmarshal(body, &out) != nil || len(out.Allowances) != tc.want {
			t.Fatalf("%q: %d %s", tc.query, status, body)
		}
	}
}
