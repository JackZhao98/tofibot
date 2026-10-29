package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/extensions"
)

func (e *settingsEnv) changes() []SettingsChange {
	e.t.Helper()
	rows, err := e.s.store.recentSettingsChanges(100)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *settingsEnv) refused(input, code string) {
	e.t.Helper()
	if m, o := e.call(input); o == nil || o.Code != code {
		e.t.Fatalf("%s: want %s, got result %v outcome %+v", input, code, m, o)
	}
}

func TestSettingsDirectAccountKeysRoundTripAndAreRecorded(t *testing.T) {
	e := newSettingsEnv(t)
	cases := []struct {
		key, value string
		read       func() any
		want       any
	}{
		{"account.timezone", `"Asia/Shanghai"`, func() any { z, _ := e.s.store.userTimezone(); return z }, "Asia/Shanghai"},
		{"account.language", `"ja"`, func() any { p, _ := e.s.store.userPreferences(); return p.Language }, "ja"},
		{"account.show_activity", `true`, func() any { p, _ := e.s.store.userPreferences(); return p.ShowActivity }, true},
		{"account.notify_chats", `false`, func() any { p, _ := e.s.store.userPreferences(); return p.NotifyChats }, false},
		{"account.notify_tasks", `false`, func() any { p, _ := e.s.store.userPreferences(); return p.NotifyTasks }, false},
	}
	for _, c := range cases {
		out := e.mustCall(`{"action":"set","key":"` + c.key + `","value":` + c.value + `}`)
		if out["applied"] != true || out["change_id"] == "" || out["link"] == "" {
			t.Fatalf("%s: %+v", c.key, out)
		}
		if got := c.read(); got != c.want {
			t.Fatalf("%s: store holds %v, want %v", c.key, got, c.want)
		}
	}
	rows := e.changes()
	if len(rows) != len(cases) {
		t.Fatalf("%d audit rows for %d changes", len(rows), len(cases))
	}
	for _, row := range rows {
		if row.ActorKind != "bot" || row.BotID != e.bot.ID || row.RunID != e.run.ID || row.Risk != "direct" || row.ConversationID != e.conv.ID {
			t.Fatalf("audit row: %+v", row)
		}
	}
	// A change to the value it already has changes nothing and records nothing.
	again := e.mustCall(`{"action":"set","key":"account.language","value":"ja"}`)
	if again["applied"] != false || again["unchanged"] != true || len(e.changes()) != len(cases) {
		t.Fatalf("unchanged set: %+v", again)
	}
	// The next read shows the new value.
	if got := e.mustCall(`{"action":"get","key":"account.timezone"}`)["value"]; got != "Asia/Shanghai" {
		t.Fatalf("read after write: %v", got)
	}
}

func TestSettingsInvalidValuesAreRefusedBeforeAnythingChanges(t *testing.T) {
	e := newSettingsEnv(t)
	for _, in := range []string{
		`{"action":"set","key":"account.timezone","value":"Mars/Olympus"}`,
		`{"action":"set","key":"account.language","value":"xx"}`,
		`{"action":"set","key":"account.show_activity","value":"maybe"}`,
		`{"action":"set","key":"account.show_activity"}`,
		`{"action":"set","key":"automation.answer_gate","value":99}`,
		`{"action":"set","key":"automation.answer_gate","value":1.5}`,
		`{"action":"set","key":"automation.group_triage","value":null,"extra":1}`,
		`{"action":"set","key":"account.timezone","value":null}`,
		`{"action":"set","key":"bot.name","value":""}`,
		`{"action":"set","key":"bot.name","value":["a"]}`,
	} {
		_, o := e.call(in)
		if o == nil || (o.Code != "settings_invalid_value" && o.Code != "settings_invalid_request") || o.NextAction != "repair_arguments" {
			t.Errorf("%s: %+v", in, o)
		}
	}
	if len(e.changes()) != 0 {
		t.Fatal("a refused value leaves no audit row")
	}
}

func TestSettingsOwnBotIdentityIsDirectAndNoticeCarriesUndo(t *testing.T) {
	e := newSettingsEnv(t)
	out := e.mustCall(`{"action":"set","key":"bot.name","value":"Mochi"}`)
	if out["applied"] != true || out["old"] != "alpha" || out["new"] != "Mochi" {
		t.Fatalf("rename: %+v", out)
	}
	if b, _ := e.s.store.GetBot(e.bot.ID); b.Name != "Mochi" {
		t.Fatalf("bot name %q", b.Name)
	}
	e.mustCall(`{"action":"set","key":"bot.instructions","value":"You write short notes."}`)
	if b, _ := e.s.store.GetBot(e.bot.ID); b.Instructions != "You write short notes." {
		t.Fatalf("instructions %q", b.Instructions)
	}
	// The notice is a system line (context.go leaves those out of the Bot history) with the change id for Undo.
	msgs, _, err := e.s.store.Messages(e.conv.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var notices []Message
	for _, m := range msgs {
		if m.Kind == "notice" && m.Notice != nil && m.Notice.Type == "settings_change" {
			notices = append(notices, m)
		}
	}
	if len(notices) != 2 {
		t.Fatalf("%d notices", len(notices))
	}
	n := notices[0].Notice
	if n.ChangeID != out["change_id"] || n.SettingKey != "bot.name" || n.OldValue != "alpha" || n.NewValue != "Mochi" || !n.Revertable || n.LabelKey != "chat:botPanel.name" || notices[0].SenderBotID != "system" {
		t.Fatalf("notice: %+v sender=%s", n, notices[0].SenderBotID)
	}
}

func TestSettingsSkillsOwnDirectUnknownRefused(t *testing.T) {
	e := newSettingsEnv(t)
	cur := e.mustCall(`{"action":"get","key":"bot.skills"}`)
	options, _ := cur["options"].([]any)
	if len(options) == 0 {
		t.Skip("no built-in skills in this build")
	}
	first := options[0].(string)
	e.mustCall(`{"action":"set","key":"bot.skills","value":[]}`)
	if on, _ := e.mustCall(`{"action":"get","key":"bot.skills"}`)["value"].([]any); len(on) != 0 {
		t.Fatalf("skills after turning all off: %v", on)
	}
	e.mustCall(`{"action":"set","key":"bot.skills","value":["` + first + `"]}`)
	if on, _ := e.mustCall(`{"action":"get","key":"bot.skills"}`)["value"].([]any); len(on) != 1 || on[0] != first {
		t.Fatalf("skills after turning one on: %v", on)
	}
	_, o := e.call(`{"action":"set","key":"bot.skills","value":["no-such-skill"]}`)
	if o == nil || o.Code != "settings_invalid_value" {
		t.Fatalf("unknown skill: %+v", o)
	}
}

func TestSettingsGroupNameAndPinAreDirect(t *testing.T) {
	e := newSettingsEnv(t)
	other, err := e.s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	g, err := e.s.store.CreateGroup("review", []string{e.bot.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	e.mustCall(`{"action":"set","key":"conversation.name","conversation_id":"` + g.ID + `","value":"standup"}`)
	if got, _ := e.s.store.GetConversation(g.ID); got.Name != "standup" {
		t.Fatalf("group name %q", got.Name)
	}
	e.refused(`{"action":"set","key":"conversation.name","value":"x"}`, "settings_invalid_value") // a direct message is not renamed
	e.mustCall(`{"action":"set","key":"conversation.pinned","conversation_id":"` + g.ID + `","value":true}`)
	if ids, _ := e.s.store.pinnedConversationIDs(); len(ids) != 1 || ids[0] != g.ID {
		t.Fatalf("pinned %v", ids)
	}
	e.mustCall(`{"action":"set","key":"conversation.pinned","conversation_id":"` + g.ID + `","value":false}`)
	if ids, _ := e.s.store.pinnedConversationIDs(); len(ids) != 0 {
		t.Fatalf("pinned after unpin %v", ids)
	}
}

func TestSettingsAutomationDirectAndResetToDefault(t *testing.T) {
	e := newSettingsEnv(t)
	e.mustCall(`{"action":"set","key":"automation.group_triage","value":false}`)
	if e.s.groupTriageEnabled() {
		t.Fatal("triage should be off")
	}
	e.mustCall(`{"action":"set","key":"automation.answer_gate","value":4}`)
	if e.s.answerGateRounds() != 4 {
		t.Fatalf("answer gate %d", e.s.answerGateRounds())
	}
	out := e.mustCall(`{"action":"set","key":"automation.group_triage","value":null}`)
	if out["applied"] != true || !e.s.groupTriageEnabled() {
		t.Fatalf("reset: %+v enabled=%v", out, e.s.groupTriageEnabled())
	}
	// A model item and the approval window are confirm-class; the cap on the tool is the person's.
	e.refused(`{"action":"set","key":"automation.settings_change_limit","value":50}`, "settings_forbidden")
	e.refused(`{"action":"set","key":"automation.remembered_approval_hours","value":720}`, "settings_change_needs_approval")
}

func TestSettingsConnectionPauseResumeAndTierTightening(t *testing.T) {
	e := newSettingsEnv(t)
	addConnection(t, e.s, "notion")
	e.mustCall(`{"action":"set","key":"connection.enabled","connection":"notion","value":false}`)
	servers, _ := e.s.extensions.ListMCP()
	if servers[0].Enabled == nil || *servers[0].Enabled {
		t.Fatalf("connection should be paused: %+v", servers[0])
	}
	e.mustCall(`{"action":"set","key":"connection.enabled","connection":"notion","value":true}`)
	servers, _ = e.s.extensions.ListMCP()
	if servers[0].Enabled != nil && !*servers[0].Enabled {
		t.Fatalf("connection should run again: %+v", servers[0])
	}
	cfg := extensions.MCPServerConfig{URL: "https://notion.example.test/mcp"}
	d := extensions.MCPToolDescriptor{Server: "notion", RemoteName: "search", Description: "Search", InputSchema: map[string]any{"type": "object"}}
	d.Digest = extensions.MCPToolDigest(d)
	if err := e.s.extensions.ObserveTools("notion", cfg, []extensions.MCPToolDescriptor{d}); err != nil {
		t.Fatal(err)
	}
	// The tool starts as write. Tightening to high_risk is direct; moving back is looser and needs a card.
	e.mustCall(`{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"search","value":"high_risk"}`)
	if got := e.mustCall(`{"action":"get","key":"connection.tool_tier","connection":"notion","tool":"search"}`)["value"]; got != "high_risk" {
		t.Fatalf("tier %v", got)
	}
	e.refused(`{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"search","value":"read_only"}`, "settings_change_needs_approval")
	e.refused(`{"action":"set","key":"connection.tool_tier","connection":"notion","tool":"missing","value":"high_risk"}`, "settings_target_not_found")
}

func TestSettingsUnattendedRunsCannotWriteAtAll(t *testing.T) {
	e := newSettingsEnv(t)
	bare, err := e.s.store.AddRun(e.bot.DMConversationID, e.bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.s.store.SetRunStatus(bare.ID, "running", "")
	tool := e.s.settingsTool(e.conv, bare)
	for _, in := range []string{`{"action":"set","key":"account.language","value":"fr"}`, `{"action":"set","key":"bot.name","value":"Zed"}`} {
		_, err := tool.Execute(t.Context(), json.RawMessage(in))
		if err == nil || !strings.Contains(err.Error(), "No person attends") {
			t.Fatalf("%s: %v", in, err)
		}
	}
	if p, _ := e.s.store.userPreferences(); p.Language != "" {
		t.Fatal("an unattended run changed a setting")
	}
	hidden := e.conv
	hidden.UserVisible = false
	if _, err := e.s.settingsTool(hidden, e.run).Execute(t.Context(), json.RawMessage(`{"action":"set","key":"account.language","value":"fr"}`)); err == nil {
		t.Fatal("a hidden conversation cannot write")
	}
	// Reading stays open.
	if _, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"get","key":"account.language"}`)); err != nil {
		t.Fatal(err)
	}
	if len(e.changes()) != 0 {
		t.Fatal("no audit row for a refused write")
	}
}

func TestSettingsPerRunCapStopsTheNinthDirectChange(t *testing.T) {
	e := newSettingsEnv(t)
	if err := e.s.saveAutomationValue(autoSettingsChangeLimit, ptr("2")); err != nil {
		t.Fatal(err)
	}
	e.mustCall(`{"action":"set","key":"account.show_activity","value":true}`)
	e.mustCall(`{"action":"set","key":"account.notify_chats","value":false}`)
	e.refused(`{"action":"set","key":"account.notify_tasks","value":false}`, "settings_change_limit")
	// Another run has its own allowance.
	bot2, err := e.s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	_, run2, _, err := e.s.store.AddUserRun(bot2.DMConversationID, bot2.ID, "again", "client-2")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = e.s.store.SetRunStatus(run2.ID, "running", "")
	run2, _ = e.s.store.GetRun(run2.ID)
	conv2, _ := e.s.store.GetConversation(bot2.DMConversationID)
	if _, err := e.s.settingsTool(conv2, run2).Execute(t.Context(), json.RawMessage(`{"action":"set","key":"account.notify_tasks","value":false}`)); err != nil {
		t.Fatal(err)
	}
}

func ptr(s string) *string { return &s }

func TestSettingsForbiddenAndConfirmClassKeysAreNotApplied(t *testing.T) {
	e := newSettingsEnv(t)
	for _, key := range []string{"models.providers", "keys.secrets", "system.update", "system.purge", "bot.secrets", "account.sign_out"} {
		_, o := e.call(`{"action":"set","key":"` + key + `","value":"x"}`)
		if o == nil || o.Code != "settings_forbidden" || o.NextAction != "explain_blocker" || !strings.Contains(o.Message, "/") {
			t.Errorf("%s: %+v", key, o)
		}
	}
	// Confirm-class keys have no writer in this slice: nothing changes.
	for _, in := range []string{
		`{"action":"set","key":"permissions.approval_mode","value":"auto"}`,
		`{"action":"set","key":"bot.model","value":"model"}`,
		`{"action":"set","key":"bot.delete"}`,
	} {
		_, o := e.call(in)
		if o == nil || o.Code != "settings_not_settable" {
			t.Errorf("%s: %+v", in, o)
		}
	}
	if mode, _ := e.s.store.getAutoReviewSettings(); mode.Mode != "off" {
		t.Fatalf("approval mode moved to %s", mode.Mode)
	}
}

func TestSettingsRevertRestoresTheOldValueOnceAndOnlyWhileUnchanged(t *testing.T) {
	e := newSettingsEnv(t)
	set := e.mustCall(`{"action":"set","key":"bot.name","value":"Mochi"}`)
	id := set["change_id"].(string)
	// The setting moved again: the old change cannot be undone over it.
	e.mustCall(`{"action":"set","key":"bot.name","value":"Taro"}`)
	e.refused(`{"action":"revert","change_id":"`+id+`"}`, "settings_changed_since")
	// Undo the newest change instead.
	second := e.changes()[0]
	if second.New != "Taro" {
		t.Fatalf("newest change: %+v", second)
	}
	out := e.mustCall(`{"action":"revert","change_id":"` + second.ID + `"}`)
	if out["applied"] != true || out["value"] != "Mochi" {
		t.Fatalf("revert: %+v", out)
	}
	if b, _ := e.s.store.GetBot(e.bot.ID); b.Name != "Mochi" {
		t.Fatalf("name after revert %q", b.Name)
	}
	e.refused(`{"action":"revert","change_id":"`+second.ID+`"}`, "settings_change_reverted")
	e.refused(`{"action":"revert","change_id":"no-such-change"}`, "settings_change_not_found")
	// The notice of the reverted change no longer offers Undo.
	msg, err := e.s.store.GetMessage(second.NoticeMessageID)
	if err != nil || msg.Notice == nil || !msg.Notice.Reverted {
		t.Fatalf("notice after revert: %+v %v", msg.Notice, err)
	}
}

func TestSettingsRevertOfALongValueIsNotOffered(t *testing.T) {
	e := newSettingsEnv(t)
	long := strings.Repeat("a long line of instructions. ", 400) // over the 4 KiB the audit keeps
	out := e.mustCall(`{"action":"set","key":"bot.instructions","value":"` + long + `"}`)
	if out["revertable"] != false {
		t.Fatalf("a cut value must not be offered for revert: %+v", out)
	}
	e.refused(`{"action":"revert","change_id":"`+out["change_id"].(string)+`"}`, "settings_change_not_revertable")
}

func TestSettingsPersonRevertRouteAndMaintenanceList(t *testing.T) {
	e := newSettingsEnv(t)
	out := e.mustCall(`{"action":"set","key":"account.language","value":"de"}`)
	h := e.s.Handler()
	do := func(method, path string) (int, map[string]any) {
		r := httptest.NewRequest(method, "http://settings.local"+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var m map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		return w.Code, m
	}
	code, list := do(http.MethodGet, "/api/settings-changes")
	rows, _ := list["changes"].([]any)
	if code != 200 || len(rows) != 1 || rows[0].(map[string]any)["label_key"] != "settings:language.label" || rows[0].(map[string]any)["bot_name"] != "alpha" {
		t.Fatalf("list: %d %+v", code, list)
	}
	if code, _ := do(http.MethodPost, "/api/settings-changes/"+out["change_id"].(string)+"/revert"); code != 200 {
		t.Fatalf("revert status %d", code)
	}
	if p, _ := e.s.store.userPreferences(); p.Language != "" {
		t.Fatalf("language after revert %q", p.Language)
	}
	if code, body := do(http.MethodPost, "/api/settings-changes/"+out["change_id"].(string)+"/revert"); code != 409 {
		t.Fatalf("second revert: %d %+v", code, body)
	}
	if code, _ := do(http.MethodPost, "/api/settings-changes/missing/revert"); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	// Reverting a change the setting has moved past is refused, not forced.
	first := e.mustCall(`{"action":"set","key":"account.language","value":"fr"}`)
	e.mustCall(`{"action":"set","key":"account.language","value":"ko"}`)
	if code, _ := do(http.MethodPost, "/api/settings-changes/"+first["change_id"].(string)+"/revert"); code != 409 {
		t.Fatalf("stale revert: %d", code)
	}
}

func TestSettingsMigrationIsIdempotent(t *testing.T) {
	e := newSettingsEnv(t)
	for i := 0; i < 2; i++ {
		if err := migrateSettingsChanges(e.s.store.db); err != nil {
			t.Fatal(err)
		}
	}
}
