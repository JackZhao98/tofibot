package app

import (
	"sort"
	"strings"
)

// The settings registry is the single source of truth for what a Bot may read
// or change about the workspace: key, scope, type, allowed values, risk class,
// the page that shows it and the manual page that explains it. The `settings`
// tool, the deep links and the manual's reference pages all derive from it.
//
//go:generate go run ../../cmd/tofi-settings-registry

// SettingScope says which id a call must carry.
type SettingScope string

const (
	ScopeAccount      SettingScope = "account"
	ScopeBot          SettingScope = "bot"
	ScopeConversation SettingScope = "conversation"
	ScopeConnection   SettingScope = "connection"
	ScopeSystem       SettingScope = "system"
)

// SettingKind is the value type.
type SettingKind string

const (
	KindString     SettingKind = "string"
	KindBool       SettingKind = "bool"
	KindInt        SettingKind = "int"
	KindEnum       SettingKind = "enum"
	KindModel      SettingKind = "model"
	KindEffort     SettingKind = "effort"
	KindTimezone   SettingKind = "timezone"
	KindLanguage   SettingKind = "language"
	KindBotList    SettingKind = "bot_list"
	KindCollection SettingKind = "collection"
	KindAction     SettingKind = "action"
	KindReadOnly   SettingKind = "readonly"
	KindNone       SettingKind = "none" // forbidden entries: no value is ever read or written
)

// RiskClass decides how a change is carried out.
type RiskClass string

const (
	RiskDirect    RiskClass = "direct"    // applied at once, recorded, undoable
	RiskConfirm   RiskClass = "confirm"   // applied only after a person approves the card
	RiskForbidden RiskClass = "forbidden" // never through the tool; the result carries the link
)

func (r RiskClass) rank() int {
	switch r {
	case RiskForbidden:
		return 2
	case RiskConfirm:
		return 1
	}
	return 0
}

// RiskSpec is the risk class of one setting. Self applies to account-level
// keys and to a Bot changing its own settings; Other applies when the target
// is another Bot; Loosen applies in addition when the change makes a limit
// looser (the stronger class wins).
type RiskSpec struct {
	Self   RiskClass `json:"self"`
	Other  RiskClass `json:"other,omitempty"`
	Loosen RiskClass `json:"loosen,omitempty"`
}

// ChangeContext is what the risk of one concrete change depends on.
type ChangeContext struct {
	SelfBot  bool // the target Bot is the Bot that acts
	Loosen   bool // the change makes a limit looser
	Attended bool // a person can see and answer the run
}

// RiskFor is the class of one concrete change.
func (d SettingDef) RiskFor(c ChangeContext) RiskClass {
	class := d.Risk.Self
	if d.Scope == ScopeBot && !c.SelfBot && d.Risk.Other != "" {
		class = d.Risk.Other
	}
	if c.Loosen && d.Risk.Loosen != "" && d.Risk.Loosen.rank() > class.rank() {
		class = d.Risk.Loosen
	}
	return class
}

// SettingDef is one entry of the registry.
type SettingDef struct {
	Key      string
	Scope    SettingScope
	Kind     SettingKind
	Options  []string // enum values; empty when the set is dynamic (models, Bots)
	Min, Max int      // int bounds
	MaxRunes int      // string bound
	Risk     RiskSpec
	// Link is the relative path that opens the setting in the app. Placeholders:
	// {bot}, {group}, {name} (a connection).
	Link string
	// Manual is the id of the manual page that explains the setting.
	Manual string
	// LabelKey is the locale key ("namespace:path") of the name the screen shows.
	LabelKey string
	// DescKey is the locale key of the one-line description, when the catalog has one.
	DescKey string
	// Effect is the fixed English sentence the confirmation card shows when this
	// key is changed with a confirm-class change; "{new}" is replaced by the new value.
	Effect string
	// Tools are the existing tools that manage a collection.
	Tools []string
	// Audit is false only for collections, whose tools keep their own records.
	Audit bool
}

// settingsSurface describes the pages the registry links to. The same lists are
// exported to the UI test, which checks them against the real components.
type settingsPage struct {
	ID       string
	NameKey  string
	Sections []string
}

// settingsTabPages lists every Settings tab with the stable ids of the sections
// the app can scroll to (ui/src/settings/sections.ts mirrors this).
var settingsTabPages = []settingsPage{
	{"general", "settings:shell.page.general.name", []string{"account", "appearance", "primary", "region", "chats"}},
	{"connections", "settings:shell.page.connections.name", []string{"list"}},
	{"skills", "settings:shell.page.skills.name", []string{"library"}},
	{"permissions", "settings:shell.page.permissions.name", []string{"mode"}},
	{"allowances", "settings:shell.page.allowances.name", []string{"reviewer", "how", "standing", "remembered"}},
	{"followups", "settings:shell.page.followups.name", []string{"bots", "nudges", "digest", "today", "limits"}},
	{"models", "settings:shell.page.models.name", []string{"providers", "defaults", "dictation"}},
	{"tools", "settings:shell.page.tools.name", []string{"web"}},
	{"automation", "settings:shell.page.automation.name", []string{"models", "replies", "history", "checks", "reliability", "schedules", "computer", "widgets"}},
	{"computer", "settings:shell.page.computer.name", []string{"resources", "environment"}},
	{"devices", "settings:shell.page.devices.name", []string{"devices"}},
	{"keys", "settings:shell.page.keys.name", []string{"keys"}},
	{"usage", "settings:shell.page.usage.name", []string{"health", "usage"}},
	{"advanced", "settings:shell.page.advanced.name", []string{"version", "server", "data", "debug", "danger"}},
	{"admin", "settings:shell.page.admin.name", []string{"accounts"}},
}

var botSettingsPages = []settingsPage{
	{"identity", "chat:botSettings.section.identity", nil},
	{"role", "chat:botSettings.section.role", nil},
	{"model", "chat:botSettings.section.model", nil},
	{"tools", "chat:botSettings.section.tools", nil},
	{"secrets", "chat:botSettings.section.secrets", nil},
	{"advanced", "chat:botSettings.section.advanced", nil},
}

var groupSettingsPages = []settingsPage{
	{"general", "chat:members.title", nil},
	{"members", "chat:members.members", nil},
}

// connectionSections are the sections of one connection's page.
var connectionSections = []string{"status", "tools", "advanced", "danger"}

// sidebarViews are the views `?side=` opens.
var sidebarViews = []string{"tasks", "memory", "terminal", "archive"}

// manualGuideIDs are the manual pages a person writes (the reference pages are
// generated from this file and the locale catalogs).
var manualGuideIDs = []string{
	"setup", "permissions", "memory", "schedules", "sidebar", "update", "bots", "groups",
	"computer/none", "computer/kvm",
	"connections/connect", "connections/google", "connections/pause", "connections/disconnect", "connections/tool-permissions",
}

const (
	linkAccountGeneral = "/settings/general"
	linkBotSettings    = "/b/{bot}/settings"
	linkGroupSettings  = "/g/{group}/settings"
	linkConnection     = "/settings/connections/{name}"
)

var (
	riskDirect  = RiskSpec{Self: RiskDirect}
	riskConfirm = RiskSpec{Self: RiskConfirm}
	// riskOwn: a Bot changes its own value directly; another Bot's value needs a card.
	riskOwn       = RiskSpec{Self: RiskDirect, Other: RiskConfirm}
	riskForbidden = RiskSpec{Self: RiskForbidden}
)

func entry(key string, scope SettingScope, kind SettingKind, risk RiskSpec, link, manual, label string) SettingDef {
	return SettingDef{Key: key, Scope: scope, Kind: kind, Risk: risk, Link: link, Manual: manual, LabelKey: label, Audit: true}
}

// settingsRegistry is the registry, built once. Automation entries come from
// automationItems, so a new automatic behavior enters it with no second edit.
var settingsRegistry = buildSettingsRegistry()

func buildSettingsRegistry() []SettingDef {
	var defs []SettingDef
	add := func(d SettingDef) { defs = append(defs, d) }
	acct := func(key string, kind SettingKind, risk RiskSpec, section, label string) SettingDef {
		return entry(key, ScopeAccount, kind, risk, linkAccountGeneral+"#"+section, "settings/general", label)
	}

	d := acct("account.timezone", KindTimezone, riskDirect, "region", "settings:timezone.label")
	add(d)
	d = acct("account.language", KindLanguage, riskDirect, "region", "settings:language.label")
	d.Options = append([]string{""}, sortedKeys(uiLanguages)...)
	add(d)
	d = acct("account.appearance", KindEnum, riskDirect, "appearance", "settings:appearance.label")
	d.Options = []string{"system", "light", "dark"}
	add(d)
	d = acct("account.show_activity", KindBool, riskDirect, "chats", "settings:general.activity_title")
	add(d)
	d = acct("account.notify_chats", KindBool, riskDirect, "chats", "settings:chats.chat_notifications")
	add(d)
	d = acct("account.notify_tasks", KindBool, riskDirect, "chats", "settings:chats.task_notifications")
	add(d)
	d = acct("account.primary_bot", KindString, riskConfirm, "primary", "settings:primary.label")
	d.Effect = "The Primary Assistant becomes {new}. It takes the general chores first."
	add(d)

	bot := func(key string, kind SettingKind, risk RiskSpec, section, page, label string) SettingDef {
		return entry(key, ScopeBot, kind, risk, linkBotSettings+"#"+section, "bot-settings/"+page, label)
	}
	d = bot("bot.name", KindString, riskOwn, "identity", "identity", "chat:botPanel.name")
	d.MaxRunes = maxTeamNameRunes
	d.Effect = "The Bot is renamed to {new}."
	add(d)
	d = bot("bot.instructions", KindString, riskOwn, "role", "role", "chat:botPanel.instructions")
	d.MaxRunes = maxSystemRunes
	d.Effect = "The Bot's durable instructions are replaced. It reads them before every reply."
	add(d)
	d = bot("bot.avatar", KindString, riskOwn, "identity", "identity", "chat:botSettings.avatar_title")
	d.Effect = "The Bot's avatar changes."
	add(d)
	d = bot("bot.model", KindModel, riskConfirm, "model", "model", "settings:model.label")
	d.Effect = "The Bot runs on {new} from its next message. Model choice changes cost and quality."
	add(d)
	d = bot("bot.reasoning_effort", KindEffort, riskConfirm, "model", "model", "settings:model.effort_label")
	d.Effect = "The Bot reasons at effort {new} from its next message. Higher effort costs more."
	add(d)
	d = bot("bot.skills", KindBotList, riskOwn, "tools", "tools", "settings:shell.page.skills.name")
	d.Effect = "The set of Skills this Bot may use changes."
	d.Tools = []string{"skill_enable", "skill_disable"}
	add(d)
	d = bot("bot.connections", KindBotList, riskConfirm, "tools", "tools", "settings:shell.page.connections.name")
	d.Effect = "The list of connections this Bot may use changes."
	add(d)
	d = bot("bot.archived", KindBool, riskConfirm, "advanced", "advanced", "chat:botSettings.archived_title")
	d.Effect = "The Bot is archived or restored. Archived Bots leave the list; chats and memory are kept."
	add(d)
	d = bot("bot.delete", KindAction, riskConfirm, "advanced", "advanced", "chat:header.delete_bot")
	d.Effect = "The Bot is deleted with its chat, memory, tasks and schedules. This cannot be undone."
	d.Audit = true
	add(d)
	d = entry("bot.memory", ScopeBot, KindCollection, RiskSpec{}, "/b/{bot}?side=memory", "memory", "work:memory.title")
	d.Tools, d.Audit = []string{"list_memory", "save_memory", "update_memory", "delete_memory"}, false
	add(d)
	d = entry("bot.schedules", ScopeBot, KindCollection, RiskSpec{}, "/b/{bot}?side=tasks", "schedules", "chat:header.tasks")
	d.Tools, d.Audit = []string{"list_schedules", "create_schedule", "update_schedule", "pause_schedule", "resume_schedule", "delete_schedule"}, false
	add(d)
	d = bot("bot.secrets", KindNone, riskForbidden, "secrets", "secrets", "chat:botSettings.section.secrets")
	add(d)

	conv := func(key string, kind SettingKind, risk RiskSpec, link, manual, label string) SettingDef {
		return entry(key, ScopeConversation, kind, risk, link, manual, label)
	}
	d = conv("conversation.name", KindString, riskDirect, linkGroupSettings+"#general", "group-settings/general", "chat:members.name")
	d.MaxRunes = maxTeamNameRunes
	add(d)
	d = conv("conversation.pinned", KindBool, riskDirect, "/", "sidebar", "chat:sidebar.pinned")
	add(d)
	d = conv("conversation.members", KindBotList, riskConfirm, linkGroupSettings+"#members", "group-settings/members", "chat:members.members")
	d.Effect = "The members of the group change. A member that leaves ends its open tasks in the group."
	add(d)
	d = conv("conversation.archived", KindBool, riskConfirm, linkGroupSettings+"#general", "group-settings/general", "chat:botSettings.archived_title")
	d.Effect = "The group is archived or restored."
	add(d)
	d = conv("conversation.delete", KindAction, riskConfirm, linkGroupSettings+"#general", "group-settings/general", "chat:header.delete_group")
	d.Effect = "The conversation is deleted with its history, memory, tasks and schedules. This cannot be undone."
	add(d)

	d = entry("permissions.approval_mode", ScopeAccount, KindEnum, riskConfirm, "/settings/permissions#mode", "settings/permissions", "settings:approval.label")
	d.Options = []string{"ask", "auto"}
	d.Effect = "Approval mode becomes {new}. In auto, the reviewer decides tool calls that would ask you; in ask, you decide them."
	add(d)
	d = entry("permissions.standing_allowances", ScopeAccount, KindCollection, riskConfirm, "/settings/allowances#standing", "settings/allowances", "settings:permissionsPage.standing.title")
	d.Audit = false
	add(d)

	for _, item := range automationItems {
		if item.Key == autoMCPReview {
			continue // the approval mode owns this value
		}
		d := entry("automation."+item.Key, ScopeAccount, automationSettingKind(item), automationRisk(item), "/settings/automation#"+item.Group, "settings/automation", "settings:automation.item."+item.Key+".name")
		d.DescKey = "settings:automation.item." + item.Key + ".what"
		d.Options, d.Min, d.Max = item.Options, item.Min, item.Max
		if d.Kind == KindModel {
			d.Effect = "The model used for this kind of work becomes {new}. Model choice changes cost and quality."
		}
		add(d)
	}

	d = entry("models.default_model", ScopeAccount, KindModel, riskConfirm, "/settings/models#defaults", "settings/models", "settings:model.global_title")
	d.Effect = "The default model becomes {new}. Bots that follow the default use it from their next message."
	add(d)
	d = entry("models.default_effort", ScopeAccount, KindEffort, riskConfirm, "/settings/models#defaults", "settings/models", "settings:model.effort_label")
	d.Effect = "The default reasoning effort becomes {new}."
	add(d)
	add(entry("models.providers", ScopeAccount, KindNone, riskForbidden, "/settings/models#providers", "settings/models", "settings:providers.title"))

	conn := func(key string, kind SettingKind, risk RiskSpec, section, manual, label string) SettingDef {
		link := linkConnection
		if section != "" {
			link += "#" + section
		}
		return entry(key, ScopeConnection, kind, risk, link, manual, label)
	}
	d = conn("connection.connect", KindAction, riskDirect, "", "connections/connect", "extensions:mcp.connect")
	add(d)
	d = conn("connection.enabled", KindBool, riskDirect, "", "connections/pause", "extensions:connections.action.resume")
	add(d)
	d = conn("connection.disconnect", KindAction, riskConfirm, "danger", "connections/disconnect", "extensions:mcp.menu.disconnect")
	d.Effect = "The connection is removed. Bots lose access and its standing allowances are revoked."
	add(d)
	d = conn("connection.tool_tier", KindEnum, RiskSpec{Self: RiskDirect, Loosen: RiskConfirm}, "tools", "connections/tool-permissions", "extensions:tools.tier.write")
	d.Options = []string{"read_only", "write", "high_risk"}
	d.Effect = "A tool of this connection is allowed to run with less asking. Its new setting is {new}."
	add(d)

	add(entry("web_tools.order", ScopeAccount, KindString, riskDirect, "/settings/tools#web", "settings/tools", "settings:webtools.title"))
	add(entry("web_tools.keys", ScopeAccount, KindNone, riskForbidden, "/settings/tools#web", "settings/tools", "settings:webtools.title"))

	for _, f := range []struct{ key, link, page, label string }{
		{"system.version", "/settings/advanced#version", "settings/advanced", "settings:advanced.version"},
		{"system.update", "/settings/advanced#version", "settings/advanced", "settings:advanced.version"},
		{"system.data", "/settings/advanced#data", "settings/advanced", "settings:advanced.data"},
		{"system.purge", "/settings/advanced#danger", "settings/advanced", "settings:advanced.danger"},
		{"system.accounts", "/settings/admin#accounts", "settings/admin", "settings:shell.page.admin.name"},
		{"system.computer_resources", "/settings/computer#resources", "settings/computer", "settings:shell.page.computer.name"},
		{"keys.secrets", "/settings/keys#keys", "settings/keys", "settings:shell.page.keys.name"},
		{"account.sign_out", "/settings/general#account", "settings/general", "settings:shell.page.general.name"},
	} {
		scope := ScopeSystem
		if strings.HasPrefix(f.key, "account.") {
			scope = ScopeAccount
		}
		add(entry(f.key, scope, KindNone, riskForbidden, f.link, f.page, f.label))
	}
	return defs
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func automationSettingKind(item automationItem) SettingKind {
	switch item.Kind {
	case autoKindBool:
		return KindBool
	case autoKindInt:
		return KindInt
	case autoKindEnum:
		return KindEnum
	case autoKindModel:
		return KindModel
	}
	return KindReadOnly
}

// automationRisk: model items cost money; everything else is a reversible switch or number.
func automationRisk(item automationItem) RiskSpec {
	if item.Kind == autoKindModel {
		return riskConfirm
	}
	if item.Kind == autoKindReadOnly {
		return riskForbidden
	}
	return riskDirect
}

// settingDefFor finds an entry by key.
func settingDefFor(key string) (SettingDef, bool) {
	for _, d := range settingsRegistry {
		if d.Key == key {
			return d, true
		}
	}
	return SettingDef{}, false
}
