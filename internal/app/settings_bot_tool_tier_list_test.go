package app

import (
	"encoding/json"
	"strings"
	"testing"
)

// Like the connection keys, the tier key is listed when the call names the connection and the tool, and not before.
func TestSettingsListShowsBotToolTierForAConnectionAndTool(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	observeTool(t, e, "notion", "search")
	without, _ := json.Marshal(e.mustCall(`{"action":"list","scope":"bot"}`))
	if strings.Contains(string(without), `"bot.tool_tier"`) {
		t.Fatalf("listed without a connection and a tool: %s", without)
	}
	with, _ := json.Marshal(e.mustCall(`{"action":"list","scope":"bot","connection":"notion","tool":"search"}`))
	if !strings.Contains(string(with), `"bot.tool_tier"`) || !strings.Contains(string(with), `"value":"global"`) {
		t.Fatalf("list: %s", with)
	}
}
