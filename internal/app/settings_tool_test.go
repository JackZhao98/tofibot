package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

type settingsEnv struct {
	t    *testing.T
	s    *Server
	bot  Bot
	conv Conversation
	run  Run
	tool Tool
}

// newSettingsEnv builds a workspace with one Bot and a running run that a person's message started (an attended run).
func newSettingsEnv(t *testing.T) *settingsEnv {
	t.Helper()
	root := t.TempDir()
	s, err := NewServer(Config{DataDir: root, DefaultModel: "model", MCPConfigPath: filepath.Join(root, "mcp.json"), SkillsDir: filepath.Join(root, "skills")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	settingsUpdateNotice = func() updateNotice { return updateNotice{Current: "v0.6.0", Latest: "v0.6.0"} }
	bot, err := s.store.CreateBot("alpha", "writer", "model")
	if err != nil {
		t.Fatal(err)
	}
	_, run, _, err := s.store.AddUserRun(bot.DMConversationID, bot.ID, "please look at my settings", "client-1")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.store.SetRunStatus(run.ID, "running", ""); err != nil || !ok {
		t.Fatalf("activate run: ok=%v err=%v", ok, err)
	}
	run, err = s.store.GetRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.store.GetConversation(bot.DMConversationID)
	if err != nil {
		t.Fatal(err)
	}
	e := &settingsEnv{t: t, s: s, bot: bot, conv: conv, run: run}
	e.tool = s.settingsTool(conv, run)
	return e
}

// call runs the tool and returns the decoded result, or the typed outcome when the call is refused.
func (e *settingsEnv) call(input string) (map[string]any, *tooloutcome.Outcome) {
	e.t.Helper()
	out, err := e.tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		if o, ok := tooloutcome.FromError(err); ok {
			return nil, &o
		}
		e.t.Fatalf("%s: %v", input, err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		e.t.Fatalf("result %q: %v", out, err)
	}
	return m, nil
}

func (e *settingsEnv) mustCall(input string) map[string]any {
	e.t.Helper()
	m, o := e.call(input)
	if o != nil {
		e.t.Fatalf("%s refused: %s %s", input, o.Code, o.Message)
	}
	return m
}

func rowsByKey(t *testing.T, m map[string]any) map[string]map[string]any {
	t.Helper()
	list, _ := m["settings"].([]any)
	out := map[string]map[string]any{}
	for _, item := range list {
		row := item.(map[string]any)
		out[row["key"].(string)] = row
	}
	return out
}

func TestSettingsListShowsAccountKeysWithValuesLinksAndClasses(t *testing.T) {
	e := newSettingsEnv(t)
	rows := rowsByKey(t, e.mustCall(`{"action":"list"}`))
	tz := rows["account.timezone"]
	if tz == nil || tz["risk"] != "direct" || tz["link"] != "/settings/general#region" || tz["manual"] != "settings/general" {
		t.Fatalf("timezone row: %+v", tz)
	}
	if label, _ := tz["link_label"].(string); label != "Settings › General › Timezone" {
		t.Fatalf("link label %q", label)
	}
	if rows["account.show_activity"]["value"] != false || rows["account.notify_chats"]["value"] != true {
		t.Fatalf("preferences: %+v %+v", rows["account.show_activity"], rows["account.notify_chats"])
	}
	if _, ok := rows["bot.name"]; ok {
		t.Fatal("the account scope does not list Bot keys")
	}
	if rows["account.sign_out"]["risk"] != "forbidden" || rows["account.sign_out"]["message"] != settingsForbiddenText {
		t.Fatalf("forbidden row: %+v", rows["account.sign_out"])
	}
}

func TestSettingsListBotScopeUsesThisBotAndOwnClasses(t *testing.T) {
	e := newSettingsEnv(t)
	rows := rowsByKey(t, e.mustCall(`{"action":"list","scope":"bot"}`))
	if rows["bot.name"]["value"] != "alpha" || rows["bot.instructions"]["value"] != "writer" {
		t.Fatalf("own Bot: %+v %+v", rows["bot.name"], rows["bot.instructions"])
	}
	if rows["bot.name"]["risk"] != "direct" || rows["bot.model"]["risk"] != "confirm" || rows["bot.connections"]["risk"] != "confirm" || rows["bot.delete"]["risk"] != "confirm" {
		t.Fatalf("classes: %v %v %v %v", rows["bot.name"]["risk"], rows["bot.model"]["risk"], rows["bot.connections"]["risk"], rows["bot.delete"]["risk"])
	}
	if got := rows["bot.name"]["link"]; got != "/b/"+e.bot.ID+"/settings#identity" {
		t.Fatalf("link %v", got)
	}
	if rows["bot.model"]["effective"] != "model" {
		t.Fatalf("effective model: %+v", rows["bot.model"])
	}
	if rows["bot.secrets"]["risk"] != "forbidden" || rows["bot.secrets"]["value"] != nil {
		t.Fatalf("secrets must carry no value: %+v", rows["bot.secrets"])
	}
	// Another Bot's name is confirm-class.
	other, err := e.s.store.CreateBot("beta", "reviewer", "model")
	if err != nil {
		t.Fatal(err)
	}
	got := e.mustCall(`{"action":"get","key":"bot.name","bot_id":"` + other.ID + `"}`)
	if got["value"] != "beta" || got["risk"] != "confirm" {
		t.Fatalf("another Bot: %+v", got)
	}
}

func TestSettingsGetRefusesUnknownKeyAndUnknownTarget(t *testing.T) {
	e := newSettingsEnv(t)
	if _, o := e.call(`{"action":"get","key":"no.such.key"}`); o == nil || o.Code != "settings_unknown_key" || o.NextAction != "repair_arguments" {
		t.Fatalf("unknown key: %+v", o)
	}
	if _, o := e.call(`{"action":"get","key":"bot.name","bot_id":"someone-elses-bot"}`); o == nil || o.Code != "settings_target_not_found" {
		t.Fatalf("unknown Bot: %+v", o)
	}
	if _, o := e.call(`{"action":"get","key":"connection.enabled"}`); o == nil || o.Code != "settings_target_required" {
		t.Fatalf("connection without name: %+v", o)
	}
	if _, o := e.call(`{"action":"get","key":"conversation.name","conversation_id":"nope"}`); o == nil || o.Code != "settings_target_not_found" {
		t.Fatalf("unknown conversation: %+v", o)
	}
	if _, o := e.call(`{"action":"get","extra":1}`); o == nil || o.Code != "settings_invalid_request" {
		t.Fatalf("extra field: %+v", o)
	}
}

func TestSettingsGetForbiddenKeysCarryTheLinkAndNoValue(t *testing.T) {
	e := newSettingsEnv(t)
	for _, key := range []string{"models.providers", "keys.secrets", "system.update", "system.purge", "system.data", "system.accounts", "system.computer_resources", "web_tools.keys"} {
		row := e.mustCall(`{"action":"get","key":"` + key + `"}`)
		if row["risk"] != "forbidden" || row["value"] != nil || row["effective"] != nil {
			t.Errorf("%s: %+v", key, row)
		}
		if link, _ := row["link"].(string); !strings.HasPrefix(link, "/settings/") {
			t.Errorf("%s: link %q", key, link)
		}
	}
}

func TestSettingsReadsWorkInUnattendedAndHiddenRuns(t *testing.T) {
	e := newSettingsEnv(t)
	hidden := e.conv
	hidden.UserVisible = false
	tool := e.s.settingsTool(hidden, e.run)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"get","key":"account.timezone"}`))
	if err != nil || !strings.Contains(out, `"key":"account.timezone"`) {
		t.Fatalf("hidden read: %q %v", out, err)
	}
	if e.s.runAttended(hidden, e.run) {
		t.Fatal("a hidden conversation is unattended")
	}
	if !e.s.runAttended(e.conv, e.run) {
		t.Fatal("a person's message started this run")
	}
	// A run with no message from a person behind it is unattended.
	bare, err := e.s.store.AddRun(e.bot.DMConversationID, e.bot.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if e.s.runAttended(e.conv, bare) {
		t.Fatal("a run no person started is unattended")
	}
}

func TestSettingsListAutomationUsesTheRegistryAndStaysBounded(t *testing.T) {
	e := newSettingsEnv(t)
	m := e.mustCall(`{"action":"list","scope":"automation"}`)
	rows := rowsByKey(t, m)
	for _, item := range automationItems {
		if item.Key == autoMCPReview {
			continue
		}
		if rows["automation."+item.Key] == nil {
			t.Errorf("missing automation.%s", item.Key)
		}
	}
	if row := rows["automation.group_triage"]; row["value"] != true || row["default"] != true || row["risk"] != "direct" {
		t.Fatalf("group_triage: %+v", row)
	}
	if row := rows["automation.conversation_model"]; row["risk"] != "confirm" {
		t.Fatalf("a model item is confirm-class: %+v", row)
	}
	raw, _ := json.Marshal(m)
	if len(raw) > maxSettingsResultBytes {
		t.Fatalf("result is %d bytes", len(raw))
	}
	all := e.mustCall(`{"action":"list","scope":"all"}`)
	raw, _ = json.Marshal(all)
	if len(raw) > maxSettingsResultBytes {
		t.Fatalf("list all is %d bytes, over the bound", len(raw))
	}
}

func TestSettingsPermissionsAndModelsReadStoredState(t *testing.T) {
	e := newSettingsEnv(t)
	rows := rowsByKey(t, e.mustCall(`{"action":"list","scope":"permissions"}`))
	if rows["permissions.approval_mode"]["value"] != "ask" || rows["permissions.approval_mode"]["risk"] != "confirm" {
		t.Fatalf("approval mode: %+v", rows["permissions.approval_mode"])
	}
	if rows["models.default_model"]["value"] != "model" {
		t.Fatalf("default model: %+v", rows["models.default_model"])
	}
	if err := e.s.store.putAutoReviewMode("auto"); err != nil {
		t.Fatal(err)
	}
	if got := e.mustCall(`{"action":"get","key":"permissions.approval_mode"}`)["value"]; got != "auto" {
		t.Fatalf("approval mode after the page changed it: %v", got)
	}
}

func TestSettingsStatusReportsStoredState(t *testing.T) {
	e := newSettingsEnv(t)
	st := e.mustCall(`{"action":"status"}`)
	bot, _ := st["bot"].(map[string]any)
	if bot["id"] != e.bot.ID || bot["model_effective"] != "model" {
		t.Fatalf("bot: %+v", bot)
	}
	account, _ := st["account"].(map[string]any)
	if account["default_model"] != "model" || account["approval_mode"] != "ask" {
		t.Fatalf("account: %+v", account)
	}
	computer, _ := st["computer"].(map[string]any)
	if computer["backend"] != "none" || computer["available"] != false {
		t.Fatalf("computer: %+v", computer)
	}
	version, _ := st["version"].(map[string]any)
	if version["installed"] != "v0.6.0" || version["link"] != "/settings/advanced#version" {
		t.Fatalf("version: %+v", version)
	}
	if _, ok := st["pending"].(map[string]any); !ok {
		t.Fatalf("pending: %+v", st["pending"])
	}
}

func TestSettingsLinkLabelUsesThePersonsLanguage(t *testing.T) {
	d, _ := settingDefFor("account.timezone")
	if got := settingLinkLabel(d, "en"); got != "Settings › General › Timezone" {
		t.Fatalf("en: %q", got)
	}
	got := settingLinkLabel(d, "zh-CN")
	if got == settingLinkLabel(d, "en") || !strings.Contains(got, "›") {
		t.Fatalf("zh-CN label %q is not translated", got)
	}
	if got := settingLinkLabel(d, "xx"); got != settingLinkLabel(d, "en") {
		t.Fatalf("an unknown language falls back to English: %q", got)
	}
}

func TestSettingsToolIsDeclaredAndTheTextHasNoExamples(t *testing.T) {
	e := newSettingsEnv(t)
	names := map[string]bool{}
	for _, tool := range e.s.tools(e.conv, e.run) {
		names[tool.Name] = true
	}
	if !names["settings"] {
		t.Fatal("settings is declared in every run")
	}
	text := strings.ToLower(e.tool.Description)
	for _, banned := range []string{"for example", "e.g.", "such as", "not just", "rather than", "instead of"} {
		if strings.Contains(text, banned) {
			t.Errorf("tool text contains %q", banned)
		}
	}
}
