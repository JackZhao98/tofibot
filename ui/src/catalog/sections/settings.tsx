import { useState, type ReactNode } from "react";
import { openSettings } from "../drive";
import { Banner, DangerZone, FilePicker, Segmented, SettingsCard, SettingsDetails, SettingsRow, SettingsSection, StatusBadge, type BadgeState } from "../../settings/components";
import { AppearancePicker, ConfirmAction, Disclosure } from "../../InteractionSystem";
import { i18n } from "../../i18n";
import "../../extensions-system.css";
import "../../settings/section-jump.css";
import "../../connections/connections.css";
import { brandIcons } from "../../brandIcons";
import { ServiceMark } from "../../ServiceMark";
import { ConnectDialog, type DialogRequest } from "../../connections/ConnectDialog";
import { ConnectionRowView, type RowMenuItem, type RowPrimary } from "../../connections/RowView";
import { ConnectionDetailView, type ConnectionDetailViewProps } from "../../connections/ConnectionDetail";
import { ConnectionsEmpty } from "../../connections/ConnectionsEmpty";
import type { ToolTierList } from "../../connections/shared";
import { ComputerOptions } from "../../ComputerOptions";
import { PasteBackView } from "../../connections/PasteBackDialog";
import type { ConnectionKind } from "../../connections/status";
import type { GoogleOverview } from "../../connections/google";
import type { LocalState } from "../../connections/localPlugins";
import { mcpOAuthRoute } from "../../mcpOAuthRoute";
import { integrationCatalog } from "../../integrationCatalog";
import { SkillLibraryView, type SkillLibraryViewProps } from "../../settings/SkillLibrary";
import { BotSkillsView, type BotSkillsViewProps } from "../../BotSkills";
import type { LibraryListing, BotSkillListing } from "../../skillLibrary";
import { WebToolsSettings } from "../../WebToolsSettings";
import type { WebSourceEntry, WebToolsView } from "../../types";
import type { CatalogSection, CatalogState } from "../types";
import { Select } from "../../Select";

const noop = () => {};
const t = i18n.t.bind(i18n);
const never = () => new Promise<void>(() => {});

// Settings › Connections (components.md §17): one row per connection that opens its page, setup in a dialog. Synthetic data only.
const connectionMenu: RowMenuItem[] = [{ key: "remove", label: "Remove service", danger: true, onSelect: noop }];
function connectionRow(id: string, label: string, props: { name: string; mark?: string; kind: ConnectionKind; state?: string; status: string; source?: string; detail?: string; primary?: RowPrimary; error?: string }): CatalogState {
  return { id: `connection-${id}`, kind: "inline", label,
    render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name={props.name} mark={props.mark} state={props.state ?? props.kind} kind={props.kind} statusText={props.status} source={props.source} detail={props.detail} primary={props.primary} error={props.error} onOpen={noop} /></div></div> };
}
/** The tool list a synthetic Notion has: the service marks four as read only; one the owner raised. */
const NOTION_TOOLS: ToolTierList = { read_only: 3, write: 2, high_risk: 1, checked: true, tools: [
  { name: "search_pages", description: "Search pages by text", tier: "read_only", source: "provider" },
  { name: "get_page", description: "Read one page", tier: "read_only", source: "provider" },
  { name: "list_databases", description: "List the databases you can see", tier: "read_only", source: "owner", override: "read_only" },
  { name: "create_page", description: "Create a page", tier: "write", source: "default" },
  { name: "update_page", description: "Edit a page", tier: "write", source: "default", changed: true },
  { name: "delete_page", description: "Delete a page", tier: "high_risk", source: "owner", override: "high_risk" },
] };
const detailBase: ConnectionDetailViewProps = {
  name: "Notion", mark: "Notion", source: "Official · mcp.notion.com", serverName: "notion", kind: "ready", statusText: "Connected", detail: "6 tools", checkedText: "Checked 2 minutes ago",
  primary: null, menu: [{ key: "edit", label: "Edit settings", onSelect: noop }], busy: false, facts: [], tiers: NOTION_TOOLS, onTiers: noop, community: false,
  advanced: { source: "Official · mcp.notion.com", repo: { name: "makenotion/notion-mcp-server", stars: 4673 }, address: "https://mcp.notion.com/mcp", transport: "streamable_http", scopes: ["read", "write"] },
  danger: [{ key: "pause", label: "Pause connection", onSelect: noop }, { key: "disconnect", label: "Disconnect", onSelect: noop }, { key: "remove", label: "Remove service", remove: true, onSelect: noop }], confirm: null, bots: { state: "ready", all: true, saving: false, error: "", bots: [{ bot_id: "bot_mochi", bot_name: "Mochi", enabled: true }, { bot_id: "bot_taro", bot_name: "Taro", enabled: true }, { bot_id: "bot_miso", bot_name: "Miso", enabled: true }] }, onBotsAll: noop, onBotsChosen: noop, allowances: [], onRevoke: async () => {}, onDropAllowance: noop, onBack: noop, onRefresh: noop, refreshing: false,
};
function connectionDetail(id: string, label: string, props: Partial<ConnectionDetailViewProps>, extra: Partial<CatalogState> = {}): CatalogState {
  return { id: `connection-detail-${id}`, kind: "inline", label, ...extra, render: () => <div className="extension-panel mcp-settings" data-view="detail"><div className="mcp-detail-pane"><ConnectionDetailView {...detailBase} {...props} /></div></div> };
}
const synthOptions = { vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: "" };
const synthLocal: LocalState = { loaded: true, available: true, plugins: [{ id: "gog", state: "sleeping", tool_count: 3 }], google: { connected: false, email: "" } };
const synthGoogle: GoogleOverview = { client: { configured: true, project_id: "tofi-sample-123456", project_number: "622599621014" }, apis: [] };
function Reopen({ children }: { children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(true);
  return open ? <>{children(() => setOpen(false))}</> : <button type="button" className="secondary-button catalog-reopen" onClick={() => setOpen(true)}>Add connection</button>;
}
function connectDialog(id: string, label: string, request: DialogRequest, google: GoogleOverview | null = null, extra: Partial<CatalogState> = {}, onOneClick: () => Promise<void> = never): CatalogState {
  return { id: `connect-${id}`, kind: "overlay", label, probe: ".connect-dialog", keyboard: { close: "Escape", reopen: { focus: ".catalog-reopen", key: "Enter" } }, ...extra,
    render: () => <Reopen>{close => <ConnectDialog request={request} servers={[]} local={synthLocal} google={google} options={synthOptions} route={mcpOAuthRoute("https:", false, false, synthOptions)} onSave={async () => {}} onOneClick={onOneClick} onLocalChanged={async () => {}} onClose={close} />}</Reopen> };
}

// Settings › Skills (the library) and a Bot's Skills section (decision 0058). Synthetic data only; no request is made.
const LIB: LibraryListing = {
  skills: [
    { name: "invoice-helper", description: "Summarize invoices and flag odd totals.", installed_by: { kind: "owner" }, installed_at: "2026-10-20T10:00:00Z", version: 1, enabled_bots: 1, bots_total: 3 },
    { name: "weekly-report", description: "Draft the weekly status report.", installed_by: { kind: "bot", bot_id: "bot_mochi", bot_name: "Mochi" }, installed_at: "2026-10-21T10:00:00Z", version: 3, updated: true, updated_at: "2026-10-22T10:00:00Z", enabled_bots: 2, bots_total: 3,
      versions: [{ version: 3, created_at: "2026-10-22T10:00:00Z", created_by: { kind: "bot", bot_name: "Mochi" }, note: "Shorter summary", current: true }, { version: 2, created_at: "2026-10-21T18:00:00Z", created_by: { kind: "bot", bot_name: "Mochi" }, current: false }, { version: 1, created_at: "2026-10-21T10:00:00Z", created_by: { kind: "bot", bot_name: "Mochi" }, current: false }] },
    { name: "old-import", description: "Found on disk; the installer was not recorded.", installed_by: { kind: "legacy" }, version: 1, enabled_bots: 0, bots_total: 3 },
    { name: "alpha-tool", description: "First tool of the office kit.", bundle_id: "office-kit", installed_by: { kind: "owner" }, installed_at: "2026-10-19T10:00:00Z", version: 1, enabled_bots: 1, bots_total: 3 },
    { name: "beta-tool", description: "Second tool of the office kit.", bundle_id: "office-kit", installed_by: { kind: "owner" }, installed_at: "2026-10-19T10:00:00Z", version: 1, enabled_bots: 0, bots_total: 3 },
    { name: "spreadsheet", description: "Build and reconcile XLSX workbooks with real numbers and formulas; verify the saved file.", builtin: true, enabled_bots: 3, bots_total: 3 },
    { name: "slides", description: "Build slide decks and check every slide as the audience sees it.", builtin: true, enabled_bots: 2, bots_total: 3 },
  ],
  bundles: [{ id: "office-kit", description: "Office helpers", version: "1.0.0", installed_by: { kind: "owner" }, installed_at: "2026-10-19T10:00:00Z", skills: ["alpha-tool", "beta-tool"] }],
};
const SYNTH_BOTS = [{ id: "bot_mochi", name: "Mochi" }, { id: "bot_taro", name: "Taro" }, { id: "bot_miso", name: "Miso" }];
const libProps = (extra: Partial<SkillLibraryViewProps> = {}): SkillLibraryViewProps => ({
  listing: LIB, loaded: true, loading: false, busy: false, error: "", notice: "", form: false, formState: { name: "", text: "" }, showReload: false,
  onToggleForm: noop, onFormChange: noop, onSubmitForm: noop, onPickFolder: noop, onStarter: noop, onRemove: never, onRemoveBundle: never, onRollback: noop, onReload: noop, bots: SYNTH_BOTS, pending: null, onCancelPending: noop, onConfirmPending: noop, ...extra,
});
const libraryState = (id: string, label: string, props: Partial<SkillLibraryViewProps> | (() => Partial<SkillLibraryViewProps>), extra: Partial<CatalogState> = {}): CatalogState =>
  ({ id: `skills-${id}`, kind: "inline", label, render: () => <SkillLibraryView {...libProps(typeof props === "function" ? props() : props)} />, ...extra });
const BOT_LIST: BotSkillListing = {
  skills: [
    { name: "invoice-helper", description: "Summarize invoices and flag odd totals.", enabled: true, set_by: "owner", set_at: "2026-10-21T09:00:00Z" },
    { name: "weekly-report", description: "Draft the weekly status report.", enabled: true, set_by: "bot", set_at: "2026-10-22T11:30:00Z" },
    { name: "old-import", description: "Found on disk; the installer was not recorded.", enabled: false },
    { name: "alpha-tool", description: "First tool of the office kit.", bundle_id: "office-kit", enabled: true, set_by: "migration", set_at: "2026-10-23T08:00:00Z" },
    { name: "beta-tool", description: "Second tool of the office kit.", bundle_id: "office-kit", enabled: false },
    { name: "spreadsheet", description: "Build and reconcile XLSX workbooks with real numbers and formulas; verify the saved file.", builtin: true, enabled: true },
    { name: "slides", description: "Build slide decks and check every slide as the audience sees it.", builtin: true, enabled: false, set_by: "owner", set_at: "2026-10-22T12:00:00Z" },
  ],
  bundles: LIB.bundles,
};
const botProps = (extra: Partial<BotSkillsViewProps> = {}): BotSkillsViewProps => ({ botId: "bot_mochi", listing: BOT_LIST, loading: false, busy: false, error: "", onToggle: noop, onToggleBundle: noop, ...extra });
const botState = (id: string, label: string, props: Partial<BotSkillsViewProps> | (() => Partial<BotSkillsViewProps>)): CatalogState =>
  ({ id: `bot-skills-${id}`, kind: "inline", label, context: ["cover-window", "bot-settings", "bs-page", "bs-scroll", "bs-sections", "bs-section"], render: () => <BotSkillsView {...botProps(typeof props === "function" ? props() : props)} /> });

function Theme() {
  const [value, setValue] = useState<"system" | "light" | "dark">("system");
  return <AppearancePicker value={value} onChange={setValue} />;
}
function PickOne({ disabled = false, empty = false }: { disabled?: boolean; empty?: boolean }) {
  const [value, setValue] = useState(empty ? "" : "balanced");
  return <Select aria-label="Reasoning effort" value={value} onValueChange={setValue} disabled={disabled} placeholder="Choose one" options={[
    { value: "fast", label: "Fast", description: "Short answers, lowest cost" },
    { value: "balanced", label: "Balanced", description: "The default for most work", tag: "Default" },
    { value: "deep", label: "Deep", description: "Longer reasoning for hard problems" },
    { value: "max", label: "Maximum", disabled: true }]} />;
}
function Choice({ disabled = false }: { disabled?: boolean }) {
  const [value, setValue] = useState<"system" | "light" | "dark">("light");
  return <Segmented label={t("settings:appearance.label")} value={value} disabled={disabled} onChange={setValue}
    options={(["system", "light", "dark"] as const).map(key => ({ value: key, label: t(`settings:appearance.${key}`) }))} />;
}

// Settings › Web search & fetch (components.md §17): two ordered lists of sources, synthetic keys and addresses only.
const webSource = (id: string, label: string, kind: WebSourceEntry["kind"], extra: Partial<WebSourceEntry> = {}): WebSourceEntry =>
  ({ id, label, kind, paid: false, fixed: false, enabled: true, status: "needs_key", testable: kind !== "computer", ...extra });
const webNative = webSource("native", "Model web search", "native", { fixed: true, status: "always", testable: false });
const webBuiltin = webSource("builtin", "Built-in fetch", "builtin", { fixed: true, status: "always" });
const webView = (search: WebSourceEntry[], fetch: WebSourceEntry[], extra: Partial<WebToolsView> = {}): WebToolsView =>
  ({ enabled: true, computer: false, search: [webNative, ...search], fetch: [webBuiltin, ...fetch], search_offered: true, ...extra });
const webTavily = (extra: Partial<WebSourceEntry> = {}) => webSource("tavily", "Tavily", "api", { paid: true, fields: ["key"], ...extra });
const webBrave = (extra: Partial<WebSourceEntry> = {}) => webSource("brave", "Brave Search", "api", { paid: true, fields: ["key"], ...extra });
const webSearx = (extra: Partial<WebSourceEntry> = {}) => webSource("searxng", "SearXNG", "self_hosted", { fields: ["base_url"], status: "needs_address", ...extra });
const webFirecrawl = (extra: Partial<WebSourceEntry> = {}) => webSource("firecrawl", "Firecrawl", "api", { paid: true, fields: ["key"], ...extra });
const webBright = (extra: Partial<WebSourceEntry> = {}) => webSource("brightdata", "Bright Data", "api", { paid: true, fields: ["key", "zone"], ...extra });
const webComputer = (extra: Partial<WebSourceEntry> = {}) => webSource("computer", "Bot computer", "computer", { status: "ready", ...extra });
const webState = (id: string, label: string, view: WebToolsView, extra: Partial<CatalogState> = {}): CatalogState =>
  ({ id: `web-tools-${id}`, kind: "inline", label: `Web search & fetch: ${label}`, render: () => <WebToolsSettings initial={view} />, ...extra });

export const settings: CatalogSection = {
  id: "settings", number: "17", title: "Settings rows and components", context: ["settings-page-body"],
  states: [
    webState("needs-setup", "nothing set up (the notice says Bots cannot search)", webView([webTavily(), webBrave(), webSearx()], [webFirecrawl(), webBright()], { search_offered: false })),
    webState("ready", "ready, one key rejected, one out of credit", webView(
      [webTavily({ status: "ready", key_hint: "…9a2f" }), webBrave({ status: "error", error_code: "quota_exceeded", key_hint: "…77c1" }), webSearx({ status: "ready", base_url: "http://searxng.example.test:8080" })],
      [webFirecrawl({ status: "error", error_code: "auth_failed", key_hint: "…b3d0" }), webBright({ status: "ready", key_hint: "…4e18", zone: "web_unlocker1" })])),
    webState("limit", "a paid source at its daily limit", webView([webTavily({ status: "limit", key_hint: "…9a2f", used_today: 300 }), webBrave({ status: "ready", key_hint: "…77c1", used_today: 12 }), webSearx({ status: "ready", base_url: "http://searxng.example.test:8080" })], [webFirecrawl(), webBright()], { limits: { search: 300, fetch: 300 } })),
    webState("computer", "with a Bot computer, one source switched off", webView(
      [webTavily({ status: "off", enabled: false, key_hint: "…9a2f" }), webBrave(), webSearx(), webComputer()], [webFirecrawl(), webBright(), webComputer()], { computer: true })),
    webState("form-open", "key form open", webView([webTavily({ status: "ready", key_hint: "…9a2f" }), webBrave(), webSearx()], [webFirecrawl(), webBright()]),
      { drive: step => step.click('.web-row[data-id="tavily"] button[aria-expanded]').then(() => undefined) }),
    webState("master-off", "web search switched off in Automatic behavior", webView([webTavily({ status: "ready", key_hint: "…9a2f" }), webBrave(), webSearx()], [webFirecrawl(), webBright()], { enabled: false })),
    { id: "computer-none", kind: "inline", label: "Computer page on a server with no computer", note: "what works without one, and the ways to add a computer", render: () => <ComputerOptions /> },
    libraryState("library", "Skills library: owner, Bot-installed, bundle, official", {}),
    libraryState("library-open", "Skills library: bundle expanded", () => ({ openBundles: ["office-kit"], notice: t("extensions:skills.library.added_notice") })),
    libraryState("library-pending", "Skills library: choose Bots for a new Skill", { pending: "Weekly report" }),
    libraryState("library-paste", "Skills library: paste form", { form: true, formState: { name: "research-assistant", text: "---\nname: research-assistant\ndescription: Research and verify sources\n---\nWorkflow instructions…" } }),
    libraryState("library-empty", "Skills library: nothing added yet", { listing: { skills: LIB.skills.filter(skill => skill.builtin), bundles: [] } }),
    libraryState("library-loading", "Skills library: loading", { loaded: false, loading: true, listing: { skills: [], bundles: [] } }),
    libraryState("library-error", "Skills library: could not load", { error: "Synthetic: the library could not be read", showReload: true, listing: { skills: [], bundles: [] }, loaded: false }),
    libraryState("library-busy", "Skills library: working", { busy: true }),
    botState("list", "Bot settings, Skills: library as checkboxes", {}),
    botState("bundle-open", "Bot settings, Skills: bundle expanded", { openBundles: ["office-kit"] }),
    botState("empty", "Bot settings, Skills: library without Skills of its own", { listing: { skills: BOT_LIST.skills.filter(skill => skill.builtin), bundles: [] } }),
    botState("loading", "Bot settings, Skills: loading", { loading: true, listing: { skills: [], bundles: [] } }),
    botState("error", "Bot settings, Skills: could not save", () => ({ error: t("bots:skills.error.save") })),
    { id: "rows", kind: "inline", label: "Section, card and rows", render: () => <SettingsSection title={t("settings:appearance.title")} hint="3" description="A synthetic description under the section title.">
      <SettingsCard>
        <SettingsRow label="Row with a control" description="The control sits on the right; on a phone it drops below." control={<button type="button" className="secondary-button">{t("common:action.retry")}</button>} />
        <SettingsRow inline label="Inline row" description="Switches and segmented controls stay beside the text on a phone." control={<Choice />} />
        <SettingsRow label="Row without a control" description="Read-only information." />
      </SettingsCard>
    </SettingsSection> },
    { id: "settings-section-jump", kind: "inline", label: "Section reached by a link", note: "a link such as /settings/general#region scrolls here and frames the section once (data-jump-target, cleared after 1.7 s)", render: () => <div data-section="region" data-jump-target=""><SettingsSection title={t("settings:general.region")}><SettingsCard><SettingsRow label={t("settings:language.label")} description="A synthetic description under the label." control={<StatusBadge state="ok" />} /></SettingsCard></SettingsSection></div> },
    { id: "badges", kind: "inline", label: "Status badges", render: () => <div style={{ display: "flex", gap: 10, flexWrap: "wrap" }}>
      {(["ok", "need", "bad", "asleep", "testing"] as BadgeState[]).map(state => <StatusBadge key={state} state={state} />)}</div> },
    { id: "banners", kind: "inline", label: "Banners", note: "error announces itself (role=alert); the others are silent", render: () => <div style={{ display: "grid", gap: 10 }}>
      <Banner tone="error" title="Couldn't reach the provider" action={{ label: t("common:action.retry"), onClick: noop }} details={"HTTP 503 from synthetic.example.test\nretry-after: 30"}>Check the key, then try again.</Banner>
      <Banner tone="warn" title="The key expires in 3 days">Replace it before then.</Banner>
      <Banner tone="info" title="Changes apply from the next run" />
    </div> },
    { id: "banner-details", kind: "inline", label: "Banner, details open", drive: step => step.click(".settings-banner-more").then(() => undefined),
      render: () => <Banner tone="error" title="Couldn't reach the provider" details={"HTTP 503 from synthetic.example.test\nretry-after: 30"}>Check the key, then try again.</Banner> },
    { id: "segmented", kind: "inline", label: "Segmented", render: () => <Choice /> },
    { id: "select", kind: "inline", label: "Select: closed, placeholder, disabled", note: "Radix Select behind src/Select.tsx; the open list is a layer in #tofi-layers", render: () => <div style={{ display: "grid", gap: 10 }}><PickOne /><PickOne empty /><PickOne disabled /></div> },
    { id: "segmented-focus", kind: "inline", label: "Segmented, keyboard focus", force: [{ selector: ".segmented button:nth-child(2)", state: "focus" }], render: () => <Choice /> },
    { id: "segmented-hover", kind: "inline", label: "Segmented, hover", force: [{ selector: ".segmented button:nth-child(3)", state: "hover" }], render: () => <Choice /> },
    { id: "segmented-disabled", kind: "inline", label: "Segmented, disabled", render: () => <Choice disabled /> },
    { id: "appearance", kind: "inline", label: "Appearance picker", render: () => <Theme /> },
    { id: "details", kind: "inline", label: "Details disclosure (closed / open)", render: () => <div style={{ display: "grid", gap: 8 }}>
      <SettingsDetails><p>The long version of the help text.</p></SettingsDetails>
      <Disclosure title={t("settings:disclosure.details")} defaultOpen><p>The long version of the help text.</p></Disclosure>
    </div> },
    { id: "file-picker", kind: "inline", label: "File picker (empty / chosen / disabled)", render: () => <div style={{ display: "grid", gap: 10 }}>
      <FilePicker label="Choose a file" dropHint="or drop it here" onFile={noop} />
      <FilePicker label="Choose a file" fileName="synthetic-export.tofi" onFile={noop} />
      <FilePicker label="Choose a file" dropHint="or drop it here" disabled onFile={noop} />
    </div> },
    { id: "danger", kind: "inline", label: "Danger zone with an in-place confirm", render: () => <DangerZone title="Danger zone">
      <SettingsRow label="Delete synthetic data" description="Removes every synthetic record." control={<ConfirmAction label="Delete" question="Delete everything?" onConfirm={async () => {}} />} />
    </DangerZone> },
    { id: "confirm-open", kind: "inline", label: "In-place confirm, asking", note: "focus moves to Cancel; Escape steps back",
      drive: step => step.click(".confirm-action .danger-link").then(() => undefined),
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={async () => {}} /> },
    { id: "confirm-busy", kind: "inline", label: "In-place confirm, working", drive: async step => { await step.click(".confirm-action .danger-link"); await step.click(".inline-confirmation .danger-link"); },
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={never} /> },
    { id: "confirm-error", kind: "inline", label: "In-place confirm, failed", drive: async step => { await step.click(".confirm-action .danger-link"); await step.click(".inline-confirmation .danger-link"); await step.find(".inline-confirmation .error-text"); },
      render: () => <ConfirmAction label="Delete" question="Delete everything?" onConfirm={() => Promise.reject(new Error("Synthetic: the server refused"))} /> },
    { id: "service-marks", kind: "inline", label: "Service marks (every directory service, light/dark)", note: "real vendor marks from the brand registry on a neutral tile; the last one is the generic fallback for a custom service",
      render: () => <ul data-service-marks style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(168px, 1fr))", gap: 10, margin: 0, padding: 0, listStyle: "none" }}>
        {brandIcons.map(icon => <li key={icon.key} style={{ display: "flex", alignItems: "center", gap: 10 }}><ServiceMark name={icon.label} id={icon.ids[0]} /><span style={{ fontSize: 13 }}>{icon.label}</span></li>)}
        <li style={{ display: "flex", alignItems: "center", gap: 10 }}><ServiceMark name="My own server" /><span style={{ fontSize: 13 }}>Custom service</span></li>
      </ul> },
    { id: "connections-list", kind: "inline", label: "Connections list: connected, needs sign-in, can't connect, paused, built in", note: "a row is the entry to the connection's page; the chevron says so, nothing expands inline",
      render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows">
        <ConnectionRowView name="Gmail (personal account)" mark="Gmail" state="ready" kind="ready" statusText="Connected" detail="3 tools · you@example.test · Gmail, Google Calendar" source="Built in · gogcli · Gmail API" onOpen={noop} />
        <ConnectionRowView name="Notion" state="ready" kind="ready" statusText="Connected" detail="6 tools" checkedText="Checked now" source="Official · mcp.notion.com" onOpen={noop} />
        <ConnectionRowView name="GitHub (read-only)" state="auth" kind="needs_auth" statusText="Needs sign-in" source="Official · api.githubcopilot.com" primary={{ label: "Sign in", onClick: noop }} onOpen={noop} />
        <ConnectionRowView name="Discord" state="ready" kind="paused" statusText="Paused" source="Community · @discord-mcp/cli 0.32.1" onOpen={noop} />
        <ConnectionRowView name="acme_tracker" state="error" kind="error" statusText="Can’t connect" source="Custom · mcp.example.test" primary={{ label: "Retry", onClick: noop }} error="Couldn’t load tools. Check the server URL and credentials, then try again." onOpen={noop} />
      </div></div> },
    { id: "connections-empty", kind: "inline", label: "Connections, nothing connected yet", note: "the cat, one line, three tiles that start a connection, Browse all",
      render: () => <div className="extension-panel mcp-settings"><ConnectionsEmpty onStart={noop} onBrowse={noop} /></div> },
    connectionRow("ready", "Connection row, connected", { name: "Notion", kind: "ready", status: "Connected", detail: "6 tools", source: "Official · mcp.notion.com" }),
    connectionRow("needs-auth", "Connection row, needs sign-in", { name: "Gmail (personal account)", mark: "Gmail", kind: "needs_auth", status: "Needs sign-in", source: "Built in · gogcli · Gmail API", primary: { label: "Sign in", onClick: noop } }),
    connectionRow("api-disabled", "Connection row, setup needed (API not enabled)", { name: "Google Gmail", kind: "api_disabled", state: "error", status: "Setup needed", source: "Official · gmailmcp.googleapis.com", primary: { label: "Enable Google Gmail API", href: "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456" }, error: "The Google Gmail API isn’t enabled in your Google Cloud project. Enable it, then refresh." }),
    connectionRow("error", "Connection row, can't connect with a named reason", { name: "acme_tracker", kind: "error", status: "Can’t connect", source: "Custom · mcp.example.test", primary: { label: "Retry", onClick: noop }, error: "Couldn’t load tools. Check the server URL and credentials, then try again." }),
    connectionRow("paused", "Connection row, paused", { name: "Discord", kind: "paused", status: "Paused", source: "Community · @discord-mcp/cli 0.32.1" }),
    connectionRow("checking", "Connection row, checking", { name: "Notion", kind: "checking", status: "Checking…", source: "Official · mcp.notion.com" }),
    connectionRow("waiting", "Connection row, waiting for sign-in", { name: "Notion", kind: "waiting", status: "Waiting for authorization", source: "Official · mcp.notion.com", primary: { label: "Continue authorization", href: "https://auth.example.test/", pending: true } }),
    { id: "connection-waiting-loopback", kind: "inline", label: "Connection row, waiting (loopback-only service): one quiet route line, breathing Continue button", note: "no illustration; the ring is opacity and scale only, static under reduced motion",
      render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name="Robinhood" kind="waiting" state="waiting" statusText="Waiting for authorization" primary={{ label: "Continue authorization", href: "https://auth.example.test/", pending: true }} onOpen={noop}>
        <div className="mcp-login-note"><p role="status">{mcpOAuthRoute("https:", false, false, synthOptions, true).note}</p></div>
      </ConnectionRowView></div></div> },
    { id: "connection-needs-auth-loopback", kind: "inline", label: "Connection row, needs sign-in (loopback-only service): one quiet route line",
      render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name="Robinhood" kind="needs_auth" state="auth" statusText="Needs sign-in" primary={{ label: "Sign in", onClick: noop }} onOpen={noop}>
        <div className="mcp-login-note"><p role="status">{mcpOAuthRoute("https:", false, false, synthOptions, true).note}</p></div>
      </ConnectionRowView></div></div> },
    { id: "connection-plugin-menu", kind: "inline", label: "Connection row, a plugin the server has but Tofi does not use yet (menu instead of a page)", render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name="acme_pkg" state="idle" kind="needs_auth" statusText="Setup needed" primary={{ label: "Connect", onClick: noop }} menu={connectionMenu} /></div></div> },
    connectionDetail("ready", "Connection page: connected, tools by permission", {}, { note: "Status, Tools (the owner's three words, marks), Advanced, Danger" }),
    connectionDetail("permission-menu", "Connection page: a tool's permission menu open", {}, { note: "an overlay menu (Radix), never a native select; the current choice is marked", drive: async step => { await step.click('button[aria-label="Permission for create_page"]'); } }),
    connectionDetail("advanced", "Connection page: Advanced open", {}, { drive: async step => { await step.click(".disclosure-toggle"); } }),
    connectionDetail("needs-auth", "Connection page: needs sign-in", { name: "Gmail (personal account)", mark: "Gmail", serverName: "local_gog", source: "Built in · gogcli · Gmail API", kind: "needs_auth", statusText: "Needs sign-in", detail: undefined, checkedText: undefined, primary: { label: "Sign in", onClick: noop }, tiers: { tools: [], read_only: 0, write: 0, high_risk: 0 }, advanced: { source: "Built in · gogcli · Gmail API", repo: { name: "openclaw/gogcli", stars: 8520 }, project: "tofi-sample-123456" } }),
    connectionDetail("error", "Connection page: can't connect, with the named reason and Google setup help", { name: "Google Gmail", mark: "Gmail", serverName: "google-gmail", source: "Official · gmailmcp.googleapis.com", kind: "api_disabled", statusText: "Setup needed", detail: undefined, checkedText: undefined,
      primary: { label: "Enable Google Gmail API", href: "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456" }, error: "The Google Gmail API isn’t enabled in your Google Cloud project. Enable it, then refresh.", tiers: { tools: [], read_only: 0, write: 0, high_risk: 0 },
      help: <div className="mcp-service-setup"><p className="conn-lead">Enable the product API and its MCP service in the same Google Cloud project as the OAuth client.</p></div>, advanced: { source: "Official · gmailmcp.googleapis.com", address: "https://gmailmcp.googleapis.com/mcp/v1", transport: "streamable_http", previewNote: true } }),
    connectionDetail("community", "Connection page: community package", { name: "acme_pkg", serverName: "local_acme_pkg", source: "Community · acme-mcp 1.0.0", community: true,
      tiers: { read_only: 1, write: 2, high_risk: 0, tools: [{ name: "get_thing", description: "Marked by the owner", tier: "read_only", source: "owner", override: "read_only" }, { name: "list_things", description: "The package says read only; ignored", tier: "write", source: "default", read_only_hint: true }, { name: "send_thing", description: "Writes", tier: "write", source: "default" }] },
      advanced: { source: "Community · acme-mcp 1.0.0", pkg: "acme-mcp 1.0.0", communityNote: true } }),
    connectionDetail("paused", "Connection page: paused, Resume leads, allowances kept", { name: "Discord", mark: "Discord", serverName: "discord", source: "Community · @discord-mcp/cli 0.32.1", kind: "paused", statusText: "Paused", detail: undefined, checkedText: undefined, community: true,
      note: "Paused. Bots can't use this connection. Its sign-in, tool settings and allowances are kept.", primary: { label: "Resume", onClick: noop },
      danger: [{ key: "pause", label: "Resume connection", onSelect: noop }, { key: "remove", label: "Remove service", remove: true, onSelect: noop }] }),
    connectionDetail("allowances", "Connection page: Always allowed chips under a tool (one active, one voided)", { allowances: [
      { id: "al_1", bot_id: "bot_taro", bot_name: "Taro", server: "notion", tool: "create_page", state: "active", created_at: "2026-10-21T10:00:00Z", use_count: 5, last_used_at: "2026-10-26T14:02:00Z" },
      { id: "al_2", bot_id: "bot_mochi", bot_name: "Mochi", server: "notion", tool: "create_page", state: "tool_changed", created_at: "2026-10-22T10:00:00Z", use_count: 0 },
    ] }, { note: "each chip has its own Revoke; the chip fades out over --m-press, in over --m-quick" }),
    connectionDetail("remove-confirm", "Connection page: confirming removal", { confirm: { text: "Remove Notion?", submit: "Remove", onCancel: noop, onConfirm: noop } }),
    connectDialog("directory", "Add connection: directory", { view: "directory" }, null, { note: "Google as one tile first, then the official services, then Custom; the Workspace servers are not here any more" }),
    connectDialog("google", "Add connection: the Google tile (Gmail, Calendar, Drive) with Advanced: Google Workspace MCP", { view: "google" }, null, { note: "services, optional Sign in as, Read only / Read and send as two cards, then the folded Workspace servers" }),
    connectDialog("google-workspace", "Google step, Workspace MCP open: the dated warning, the enrolment line, six servers", { view: "google" }, null, { note: "the warning says what Google requires of the OAuth client; after 2026-11-11 it is stated as fact", drive: async step => { await step.click(".connect-workspace .disclosure-toggle"); await step.wait(500); } }),
    connectDialog("one-click", "Add connection: one click for a service that registers its own OAuth client (Notion), saving", { view: "connecting", preset: integrationCatalog.find(item => item.id === "notion")! }, null, { keyboard: undefined, note: "no form: the dialog says it is adding the service and opening sign-in; it is locked (no Escape, no outside press) while the save runs" }),
    connectDialog("one-click-failed", "Add connection: one click that failed, with the reason and the way to the form", { view: "connecting", preset: integrationCatalog.find(item => item.id === "notion")! }, null, { note: "Advanced settings opens the form; Cancel closes" }, () => Promise.reject(new Error("Synthetic: the server could not save this connection"))),
    connectDialog("token", "Add connection: a token service (Linear) is one password field plus the steps", { view: "preset", preset: integrationCatalog.find(item => item.id === "linear")! }),
    connectDialog("gmail", "Add connection: Gmail via gogcli (first Google service)", { view: "gog" }),
    connectDialog("gmail-saved", "Gmail via gogcli with the workspace's Google client", { view: "gog" }, synthGoogle, { note: "no JSON to upload; the project's Enable Gmail API link" }),
    connectDialog("workspace", "Google Workspace MCP server with the saved client", { view: "preset", preset: integrationCatalog.find(item => item.id === "google-calendar")! }, synthGoogle),
    connectDialog("robinhood", "Robinhood (loopback-only redirect): the route line explains why", { view: "preset", preset: integrationCatalog.find(item => item.id === "robinhood")! }),
    connectDialog("custom", "Custom connector", { view: "custom" }),
    connectDialog("package", "Local package", { view: "package" }),
    { id: "shell", kind: "app", label: "Settings window", note: "the real settings shell, opened from the gear beside the account",
      app: { path: "/b/bot_mochi" }, probe: ".settings-shell", keyboard: { close: "Escape" },
      drive: async step => { await openSettings(step); } },
  ],
};
