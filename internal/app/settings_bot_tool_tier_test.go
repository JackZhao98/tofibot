package app

import "testing"

// A Bot may ask to change how often it asks about one tool, in either direction and for itself as for another Bot, and nothing changes
// until a person approves a card for exactly that change. The connection's own tier does not move.
func TestSettingsBotToolTierAlwaysNeedsACard(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	observeTool(t, e, "notion", "search")
	got := e.mustCall(`{"action":"get","key":"bot.tool_tier","connection":"notion","tool":"search"}`)
	if got["value"] != "global" || got["risk"] != "confirm" {
		t.Fatalf("read: %+v", got)
	}
	e.refused(`{"action":"set","key":"bot.tool_tier","value":"read_only"}`, "settings_target_required")
	e.refused(`{"action":"set","key":"bot.tool_tier","connection":"nowhere","tool":"search","value":"read_only"}`, "settings_target_not_found")

	declined := `{"action":"set","key":"bot.tool_tier","connection":"notion","tool":"search","value":"high_risk"}`
	e.decide(e.needApproval(declined), false)
	e.refused(declined, "settings_change_denied")
	if tier, _, ok := e.s.store.botToolTier(e.bot.ID, "notion", "search"); ok {
		t.Fatalf("a declined card changed the tier: %s", tier)
	}

	in := `{"action":"set","key":"bot.tool_tier","connection":"notion","tool":"search","value":"read_only"}`
	card := e.decide(e.needApproval(in), true)
	if card.Approval.Facts.Setting.EffectCode != "bot_tool_tier" || card.Approval.Facts.Setting.TargetName == "" {
		t.Fatalf("facts: %+v", card.Approval.Facts.Setting)
	}
	e.mustCall(in)
	if tier, _, ok := e.s.store.botToolTier(e.bot.ID, "notion", "search"); !ok || tier != "read_only" {
		t.Fatalf("stored: %q %v", tier, ok)
	}
	if g := e.s.extensions.MCPToolTier("notion", "search"); g.Tier != "write" {
		t.Fatalf("the connection's tier moved: %+v", g)
	}
	if got := e.mustCall(`{"action":"get","key":"bot.tool_tier","connection":"notion","tool":"search"}`)["value"]; got != "read_only" {
		t.Fatalf("read back: %v", got)
	}

	// Following the connection again is a change like any other.
	back := `{"action":"set","key":"bot.tool_tier","connection":"notion","tool":"search","value":"global"}`
	e.decide(e.needApproval(back), true)
	e.mustCall(back)
	if _, _, ok := e.s.store.botToolTier(e.bot.ID, "notion", "search"); ok {
		t.Fatal("global must clear the Bot's own tier")
	}
}
