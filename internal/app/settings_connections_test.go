package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func seedStandingAllowance(t *testing.T, e *settingsEnv, server, tool string) string {
	t.Helper()
	id := "allow-" + server + "-" + tool
	if _, err := e.s.store.db.Exec(`INSERT INTO mcp_standing_allowances(id,account_id,bot_id,server,tool,config_fingerprint,credential_target,tool_digest,source_question_id,created_at,created_by) VALUES(?,?,?,?,?,'fp','c','d','',?,'human')`, id, e.s.reviewAccountID(), e.bot.ID, server, tool, now()); err != nil {
		t.Fatal(err)
	}
	return id
}

func allowanceRevoked(t *testing.T, e *settingsEnv, id string) bool {
	t.Helper()
	var revoked string
	if err := e.s.store.db.QueryRow(`SELECT revoked_at FROM mcp_standing_allowances WHERE id=?`, id).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked != ""
}

func observeTool(t *testing.T, e *settingsEnv, server, tool string) {
	t.Helper()
	d := extensions.MCPToolDescriptor{Server: server, RemoteName: tool, Description: "A tool", InputSchema: map[string]any{"type": "object"}}
	d.Digest = extensions.MCPToolDigest(d)
	if err := e.s.extensions.ObserveTools(server, extensions.MCPServerConfig{URL: "https://" + server + ".example.test/mcp"}, []extensions.MCPToolDescriptor{d}); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsConnectReturnsTheLinkAndChangesNothing(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	out := e.mustCall(`{"action":"set","key":"connection.connect","connection":"notion"}`)
	if out["applied"] != false || out["link"] != "/settings/connections/notion" || out["connection"] != "notion" {
		t.Fatalf("connect: %+v", out)
	}
	if next, _ := out["next"].(string); !strings.Contains(next, "request_connection") {
		t.Fatalf("in a conversation a person can see, the Bot starts the sign-in with request_connection: %q", next)
	}
	if label, _ := out["link_label"].(string); label != "Settings › Connections › Connect" {
		t.Fatalf("label %q", label)
	}
	if len(e.changes()) != 0 {
		t.Fatal("connect records no change: nothing changed")
	}
	// A hidden conversation gets the link, not an instruction to open a card.
	hidden := e.conv
	hidden.UserVisible = false
	raw, err := e.s.settingsTool(hidden, e.run).Execute(context.Background(), json.RawMessage(`{"action":"set","key":"connection.connect","connection":"notion"}`))
	if err != nil || strings.Contains(raw, "request_connection") {
		t.Fatalf("hidden: %q %v", raw, err)
	}
	e.refused(`{"action":"set","key":"connection.connect","connection":"nowhere"}`, "settings_target_not_found")
	e.refused(`{"action":"set","key":"connection.connect"}`, "settings_target_required")
}

func TestSettingsDisconnectNeedsACardAndRevokesTheAllowances(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	allow := seedStandingAllowance(t, e, "notion", "search")
	in := `{"action":"set","key":"connection.disconnect","connection":"notion"}`
	token := e.needApproval(in)
	if allowanceRevoked(t, e, allow) {
		t.Fatal("nothing may change before the card is approved")
	}
	card := e.decide(token, true)
	f := card.Approval.Facts.Setting
	if f.EffectCode != "disconnect" || f.TargetName != "notion" || f.Link != "/settings/connections/notion#danger" {
		t.Fatalf("facts: %+v", f)
	}
	e.mustCall(in)
	if !allowanceRevoked(t, e, allow) {
		t.Fatal("the tools the person always allowed on the connection stop being allowed")
	}
	rows := e.changes()
	if len(rows) != 1 || rows[0].Key != "connection.disconnect" || rows[0].Risk != "confirm" || rows[0].TargetConnection != "notion" || rows[0].Revertable != true {
		t.Fatalf("audit: %+v", rows)
	}
	// An action has nothing to restore.
	e.refused(`{"action":"revert","change_id":"`+rows[0].ID+`"}`, "settings_change_not_revertable")
	// The connection stays in the list: disconnect is not removal.
	servers, _ := e.s.extensions.ListMCP()
	if len(servers) != 1 {
		t.Fatalf("servers: %+v", servers)
	}
}

func TestSettingsPauseAndResumeAreAuditedAndRevertable(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	paused := e.mustCall(`{"action":"set","key":"connection.enabled","connection":"notion","value":false}`)
	rows := e.changes()
	if len(rows) != 1 || rows[0].Old != "true" || rows[0].New != "false" || rows[0].Risk != "direct" || rows[0].TargetConnection != "notion" {
		t.Fatalf("audit: %+v", rows)
	}
	status := e.mustCall(`{"action":"status"}`)
	conns, _ := status["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["status"] != "paused" {
		t.Fatalf("status: %+v", status["connections"])
	}
	e.mustCall(`{"action":"revert","change_id":"` + paused["change_id"].(string) + `"}`)
	servers, _ := e.s.extensions.ListMCP()
	if servers[0].Enabled != nil && !*servers[0].Enabled {
		t.Fatal("the revert resumed the connection")
	}
	// A hidden run cannot pause anything.
	hidden := e.conv
	hidden.UserVisible = false
	if _, err := e.s.settingsTool(hidden, e.run).Execute(context.Background(), json.RawMessage(`{"action":"set","key":"connection.enabled","connection":"notion","value":false}`)); err == nil {
		t.Fatal("hidden runs write nothing")
	}
}

func TestSettingsToolTierDirectionDecidesTheClassAlsoForARevert(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	observeTool(t, e, "notion", "search")
	tighten := e.mustCall(`{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"search","value":"high_risk"}`)
	// Undoing a tightening loosens: it is confirm class and needs its own card.
	revert := `{"action":"revert","change_id":"` + tighten["change_id"].(string) + `"}`
	card := e.decide(e.needApproval(revert), true)
	if !card.Approval.Facts.Setting.Loosens || card.Approval.Facts.Setting.EffectCode != "revert" {
		t.Fatalf("facts: %+v", card.Approval.Facts.Setting)
	}
	e.mustCall(revert)
	if got := e.mustCall(`{"action":"get","key":"connection.tool_tier","connection":"notion","tool":"search"}`)["value"]; got != "write" {
		t.Fatalf("tier after the approved revert: %v", got)
	}
}

func TestSettingsConnectionAccessOfABotIsWrittenOnlyThroughAnApprovedCard(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	addConnection(t, e.s, "linear")
	// Reading shows every connection open to a Bot nobody limited.
	got := e.mustCall(`{"action":"get","key":"bot.connections"}`)
	if open, _ := got["value"].([]any); len(open) != 2 || got["risk"] != "confirm" {
		t.Fatalf("read: %+v", got)
	}
	// Even a Bot's own list is confirm class: it widens or narrows what the Bot can reach.
	in := `{"action":"set","key":"bot.connections","value":["linear"]}`
	e.decide(e.needApproval(in), false)
	e.refused(in, "settings_change_denied")
	rows, _ := e.s.botConnectionAccess(e.bot.ID)
	for _, row := range rows {
		if !row.Enabled {
			t.Fatalf("a declined card changed the list: %+v", rows)
		}
	}
	// After approval the list is written through the store method the owner's page uses.
	in = `{"action":"set","key":"bot.connections","value":["notion"]}`
	card := e.decide(e.needApproval(in), true)
	if card.Approval.Facts.Setting.EffectCode != "connection_access" {
		t.Fatalf("facts: %+v", card.Approval.Facts.Setting)
	}
	e.mustCall(in)
	rows, _ = e.s.botConnectionAccess(e.bot.ID)
	open := map[string]bool{}
	for _, row := range rows {
		open[row.Server] = row.Enabled
	}
	if !open["notion"] || open["linear"] {
		t.Fatalf("access: %+v", rows)
	}
	var setBy string
	_ = e.s.store.db.QueryRow(`SELECT set_by FROM bot_connection_access WHERE bot_id=? AND server='linear'`, e.bot.ID).Scan(&setBy)
	if setBy != connectionSetByOwner {
		t.Fatalf("set_by %q", setBy)
	}
	// An unknown connection name is refused without a row.
	e.refused(`{"action":"set","key":"bot.connections","value":["nowhere"]}`, "settings_change_needs_approval")
	token := e.needApproval(`{"action":"set","key":"bot.connections","value":["nowhere"]}`)
	e.decide(token, true)
	e.refused(`{"action":"set","key":"bot.connections","value":["nowhere"]}`, "settings_invalid_value")
}

func TestSettingsConnectionWritesAreRefusedForUnattendedRuns(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	bare, _ := e.s.store.AddRun(e.bot.DMConversationID, e.bot.ID, "")
	_, _ = e.s.store.SetRunStatus(bare.ID, "running", "")
	tool := e.s.settingsTool(e.conv, bare)
	for _, in := range []string{
		`{"action":"set","key":"connection.disconnect","connection":"notion"}`,
		`{"action":"set","key":"connection.enabled","connection":"notion","value":false}`,
		`{"action":"set","key":"bot.connections","value":[]}`,
	} {
		_, err := tool.Execute(context.Background(), json.RawMessage(in))
		if err == nil || !strings.Contains(err.Error(), "No person attends") {
			t.Errorf("%s: %v", in, err)
		}
	}
	servers, _ := e.s.extensions.ListMCP()
	if servers[0].Enabled != nil && !*servers[0].Enabled {
		t.Fatal("an unattended run paused a connection")
	}
}
