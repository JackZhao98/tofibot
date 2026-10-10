package app

import (
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

// The scheduled effect fence counts only host-classified observations as
// harmless. Remote hints, model effect labels and unparseable records are
// effects.
func TestMCPUncertainEffectActivityClassification(t *testing.T) {
	unknown := tooloutcome.New(tooloutcome.Uncertain, "synthetic_uncertain", "unknown", "unknown", "verify_effect")
	cases := []struct {
		name, tool, args string
		truncated        bool
		effect           bool
	}{
		{"browser navigate", "computer_browser", `{"action":"browser.navigate","url":"https://fixture.invalid/"}`, false, false},
		{"browser navigate alias", "computer_browser", `{"action":"navigate","url":"https://fixture.invalid/"}`, false, false},
		{"browser read", "computer_browser", `{"action":"browser.read","find":"alpha"}`, false, false},
		{"browser snapshot", "computer_browser", `{"action":"browser.snapshot"}`, false, false},
		{"browser tab switch", "computer_browser", `{"action":"browser.action","target_action":"switch","target_id":"t1"}`, false, false},
		{"browser click with self-labelled no effect", "computer_browser", `{"action":"browser.click","click":"Send","effect":"none"}`, false, true},
		{"browser unparseable", "computer_browser", `{"action":`, false, true},
		{"browser truncated record", "computer_browser", `{"action":"browser.navigate"}`, true, true},
		{"desktop capture", "computer_desktop", `{"action":"desktop.capture"}`, false, false},
		{"desktop type", "computer_desktop", `{"action":"desktop.type","text":"hello"}`, false, true},
		{"computer file read", "computer_action", `{"computer_id":"c1","action":"files.read","args":{"path":"/tmp/a"}}`, false, false},
		{"computer shell", "computer_action", `{"computer_id":"c1","action":"shell.exec","args":{"command":"rm -rf x"}}`, false, true},
		{"computer browser action navigate", "computer_action", `{"computer_id":"c1","action":"browser.action","args":{"action":"navigate","url":"https://fixture.invalid/"}}`, false, false},
		{"mcp call", "call_mcp_tool", `{"name":"mcp_fixture__notion-update-page","arguments":{}}`, false, true},
		{"mcp read-looking call is still an effect", "call_mcp_tool", `{"name":"mcp_fixture__notion-fetch","arguments":{}}`, false, true},
		{"host observation tool", "inspect_recent_runs", `{}`, false, false},
		{"discovery", "search_mcp_tools", `{"query":"x"}`, false, false},
		{"unknown tool", "synthetic_public_read", `{"target":"alpha"}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := ToolActivity{Name: tc.tool, Arguments: tc.args, Truncated: tc.truncated, Outcome: &unknown}
			if got := mcpUncertainEffectActivity(a); got != tc.effect {
				t.Fatalf("effect=%v want %v", got, tc.effect)
			}
		})
	}
}
