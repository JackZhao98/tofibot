package extensions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A Bot's own tier is laid over the global one: no row follows the global tier
// with the same binding, a fresh row wins in either direction, a stale row only
// tightens, and the binding of a call moves with the Bot's choice.
func TestMCPToolTierPerBotLayer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	cfg := MCPServerConfig{URL: "https://synthetic.invalid/mcp"}
	raw, _ := json.Marshal(serverFile{MCPServers: map[string]MCPServerConfig{"svc": cfg}})
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	type key struct{ bot, server, tool string }
	rows := map[key][2]string{}
	m := NewManager(Config{MCPConfigPath: path, BotToolTier: func(bot, server, tool string) (string, string, bool) {
		row, ok := rows[key{bot, server, tool}]
		return row[0], row[1], ok
	}})
	read := tierDescriptor("fetch", "Fetch a record", MCPToolAnnotations{Present: true, ReadOnly: true})
	write := tierDescriptor("update", "Update a record", MCPToolAnnotations{})
	if err := m.ObserveTools("svc", cfg, []MCPToolDescriptor{read, write}); err != nil {
		t.Fatal(err)
	}
	global := m.MCPToolTier("svc", "update")
	if v := m.MCPToolTierForBot("bot-a", "svc", "update"); v.Tier != MCPTierWrite || v.Binding != global.Binding || v.BotTier != "" || v.GlobalTier != MCPTierWrite {
		t.Fatalf("a Bot with no tier of its own follows the global one unchanged: %+v", v)
	}
	// Tighter, and looser, for one Bot only.
	rows[key{"bot-a", "svc", "update"}] = [2]string{string(MCPTierHighRisk), write.Digest}
	rows[key{"bot-a", "svc", "fetch"}] = [2]string{string(MCPTierWrite), read.Digest}
	rows[key{"bot-b", "svc", "update"}] = [2]string{string(MCPTierReadOnly), write.Digest}
	if v := m.MCPToolTierForBot("bot-a", "svc", "update"); v.Tier != MCPTierHighRisk || v.Source != MCPTierSourceBot || v.GlobalTier != MCPTierWrite || v.Binding == global.Binding {
		t.Fatalf("Bot A tightened: %+v", v)
	}
	if v := m.MCPToolTierForBot("bot-b", "svc", "update"); v.Tier != MCPTierReadOnly || v.Source != MCPTierSourceBot {
		t.Fatalf("Bot B loosened: %+v", v)
	}
	if v := m.MCPToolTierForBot("bot-c", "svc", "update"); v.Tier != MCPTierWrite || v.Binding != global.Binding {
		t.Fatalf("another Bot is untouched: %+v", v)
	}
	if v := m.MCPToolTier("svc", "update"); v.Tier != MCPTierWrite {
		t.Fatalf("the global tier did not move: %+v", v)
	}
	// The call gate re-resolves with the Bot and goes stale with the choice.
	fingerprint, _ := m.MCPConfigVersion("svc")
	callA := MCPCallApproval{BotID: "bot-a", Server: "svc", Tool: "update", ConfigVersion: fingerprint}
	callA.TierBinding = m.MCPToolTierForBot("bot-a", "svc", "update").Binding
	if tier, current := m.MCPCallTier(callA); !current || tier != MCPTierHighRisk {
		t.Fatalf("call tier for Bot A = %s current=%v", tier, current)
	}
	delete(rows, key{"bot-a", "svc", "update"})
	if _, current := m.MCPCallTier(callA); current {
		t.Fatal("clearing the Bot's tier left its proposal current")
	}
	// A stale choice (the tool changed since) only tightens.
	rows[key{"bot-b", "svc", "update"}] = [2]string{string(MCPTierReadOnly), "an-older-digest"}
	if v := m.MCPToolTierForBot("bot-b", "svc", "update"); v.Tier != MCPTierWrite || !v.BotChanged {
		t.Fatalf("a stale loosening must not apply: %+v", v)
	}
	rows[key{"bot-b", "svc", "update"}] = [2]string{string(MCPTierHighRisk), "an-older-digest"}
	if v := m.MCPToolTierForBot("bot-b", "svc", "update"); v.Tier != MCPTierHighRisk || !v.BotChanged {
		t.Fatalf("a stale tightening still applies: %+v", v)
	}
	// The listing shows both tiers.
	seen := false
	for _, tool := range m.ListToolTiersForBot("bot-a", "svc").Tools {
		if tool.RemoteName == "fetch" {
			seen = true
			if tool.Tier != MCPTierWrite || tool.GlobalTier != MCPTierReadOnly || tool.BotTier != string(MCPTierWrite) {
				t.Fatalf("listing for Bot A: %+v", tool)
			}
		}
	}
	if !seen {
		t.Fatal("fetch is not listed")
	}
	// A tool some Bot made stricter is never an observation.
	if !m.RunToolReadOnly("mcp_svc__fetch") {
		t.Fatal("fetch is read only for everyone who has not chosen")
	}
	m.cfg.BotToolTierRaised = func(server, tool string) bool { return tool == "fetch" }
	if m.RunToolReadOnly("mcp_svc__fetch") {
		t.Fatal("a tool a Bot made stricter was taken for an observation")
	}
}
