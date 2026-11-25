package app

import (
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

// The review path resolves a tool's tier from inside a transaction, and the database has one connection: a lookup that queried it
// would wait for itself. The Bot's own tier comes from memory, so the lookup returns while a transaction is open.
func TestBotToolTierLookupNeverWaitsForTheDatabase(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	observedTool(t, env.s, "notion", "create_page", false)
	if status, _ := extensionHTTP(t, env.handler, "PUT", "/api/bots/"+env.a.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "high_risk"}); status != 200 {
		t.Fatalf("set: %d", status)
	}
	tx, err := env.s.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got := make(chan extensions.MCPToolTierView, 1)
	go func() { got <- env.s.extensions.MCPToolTierForBot(env.a.ID, "notion", "create_page") }()
	select {
	case v := <-got:
		if v.Tier != extensions.MCPTierHighRisk || v.BotTier != "high_risk" {
			t.Fatalf("tier inside a transaction: %+v", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the tier lookup waited for the database connection a transaction holds")
	}
	if !env.s.store.botToolTierRaised("notion", "create_page") {
		t.Fatal("the raised check must also come from memory")
	}
}

// The cache follows the table across a restart, a Bot's deletion and a connection's removal.
func TestBotToolTierCacheFollowsTheTable(t *testing.T) {
	env := newLibEnv(t)
	addConnection(t, env.s, "notion")
	observedTool(t, env.s, "notion", "create_page", false)
	extensionHTTP(t, env.handler, "PUT", "/api/bots/"+env.a.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "read_only"})
	extensionHTTP(t, env.handler, "PUT", "/api/bots/"+env.b.ID+"/tool-tiers/notion/create_page", map[string]any{"tier": "high_risk"})
	if err := env.s.store.loadBotToolTiers(); err != nil {
		t.Fatal(err)
	}
	if tier, _, ok := env.s.store.botToolTier(env.a.ID, "notion", "create_page"); !ok || tier != "read_only" {
		t.Fatalf("after a reload: %q %v", tier, ok)
	}
	if _, err := env.s.store.DeleteBot(env.b.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := env.s.store.botToolTier(env.b.ID, "notion", "create_page"); ok {
		t.Fatal("a deleted Bot kept its tier in memory")
	}
	if err := env.s.deleteMCPServer("notion"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := env.s.store.botToolTier(env.a.ID, "notion", "create_page"); ok {
		t.Fatal("a removed connection kept its tiers in memory")
	}
}
