import { useState, type ReactNode } from "react";
import { openSettings } from "../drive";
import { Banner, DangerZone, FilePicker, Segmented, SettingsCard, SettingsDetails, SettingsRow, SettingsSection, StatusBadge, type BadgeState } from "../../settings/components";
import { AppearancePicker, ConfirmAction, Disclosure } from "../../InteractionSystem";
import { i18n } from "../../i18n";
import "../../extensions-system.css";
import "../../connections/connections.css";
import { brandIcons } from "../../brandIcons";
import { ServiceMark } from "../../ServiceMark";
import { ConnectDialog, type DialogRequest } from "../../connections/ConnectDialog";
import { ConnectionRowView, type RowMenuItem, type RowPrimary } from "../../connections/RowView";
import { RepoLink } from "../../connections/repoStars";
import { PasteBackView } from "../../connections/PasteBackDialog";
import type { ConnectionKind } from "../../connections/status";
import type { GoogleOverview } from "../../connections/google";
import type { LocalState } from "../../connections/localPlugins";
import { mcpOAuthRoute } from "../../mcpOAuthRoute";
import { integrationCatalog } from "../../integrationCatalog";
import { SkillLibraryView, type SkillLibraryViewProps } from "../../settings/SkillLibrary";
import { BotSkillsView, type BotSkillsViewProps } from "../../BotSkills";
import type { LibraryListing, BotSkillListing } from "../../skillLibrary";
import type { CatalogSection, CatalogState } from "../types";

const noop = () => {};
const t = i18n.t.bind(i18n);
const never = () => new Promise<void>(() => {});

// Settings › Connections (components.md §17): one row per connection, setup in a dialog. Synthetic data only.
const connectionMenu: RowMenuItem[] = [{ key: "details", label: "View details", onSelect: noop }, { key: "refresh", label: "Refresh tools", onSelect: noop }, { key: "remove", label: "Remove service", danger: true, onSelect: noop }];
function connectionRow(id: string, label: string, props: { name: string; mark?: string; kind: ConnectionKind; state?: string; status: string; detail?: string; primary?: RowPrimary; error?: string; details?: boolean }): CatalogState {
  return { id: `connection-${id}`, kind: "inline", label,
    render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name={props.name} mark={props.mark} state={props.state ?? props.kind} kind={props.kind} statusText={props.status} detail={props.detail} primary={props.primary} error={props.error} menu={connectionMenu}>
      {props.details && <div className="mcp-service-details"><p className="mcp-service-source">Official · mcp.notion.com</p><p className="mcp-service-source"><RepoLink repo="makenotion/notion-mcp-server" stars={4673} /></p></div>}
    </ConnectionRowView></div></div> };
}
const synthOptions = { vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: "" };
const synthLocal: LocalState = { loaded: true, available: true, plugins: [{ id: "gog", state: "sleeping", tool_count: 3 }], google: { connected: false, email: "" } };
const synthGoogle: GoogleOverview = { client: { configured: true, project_id: "tofi-sample-123456", project_number: "622599621014" }, apis: [] };
function Reopen({ children }: { children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(true);
  return open ? <>{children(() => setOpen(false))}</> : <button type="button" className="secondary-button catalog-reopen" onClick={() => setOpen(true)}>Add connection</button>;
}
function connectDialog(id: string, label: string, request: DialogRequest, google: GoogleOverview | null = null, extra: Partial<CatalogState> = {}): CatalogState {
  return { id: `connect-${id}`, kind: "overlay", label, probe: ".connect-dialog", keyboard: { close: "Escape", reopen: { focus: ".catalog-reopen", key: "Enter" } }, ...extra,
    render: () => <Reopen>{close => <ConnectDialog request={request} servers={[]} local={synthLocal} google={google} options={synthOptions} route={mcpOAuthRoute("https:", false, false, synthOptions)} onSave={async () => {}} onLocalChanged={async () => {}} onClose={close} />}</Reopen> };
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
  ({ id: `bot-skills-${id}`, kind: "inline", label, context: ["detail-content", "bot-v2-panel"], render: () => <BotSkillsView {...botProps(typeof props === "function" ? props() : props)} /> });

function Theme() {
  const [value, setValue] = useState<"system" | "light" | "dark">("system");
  return <AppearancePicker value={value} onChange={setValue} />;
}
function Choice({ disabled = false }: { disabled?: boolean }) {
  const [value, setValue] = useState<"system" | "light" | "dark">("light");
  return <Segmented label={t("settings:appearance.label")} value={value} disabled={disabled} onChange={setValue}
    options={(["system", "light", "dark"] as const).map(key => ({ value: key, label: t(`settings:appearance.${key}`) }))} />;
}

export const settings: CatalogSection = {
  id: "settings", number: "17", title: "Settings rows and components", context: ["settings-page-body"],
  states: [
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
    connectionRow("ready", "Connection row, connected", { name: "Notion", kind: "ready", status: "Connected", details: false }),
    connectionRow("details", "Connection row, details open (source and GitHub link)", { name: "Notion", kind: "ready", status: "Connected", details: true }),
    connectionRow("needs-auth", "Connection row, needs authorization", { name: "Gmail (personal account)", mark: "Gmail", kind: "needs_auth", status: "Needs authorization", primary: { label: "Authorize", onClick: noop } }),
    connectionRow("api-disabled", "Connection row, API not enabled", { name: "Google Gmail", kind: "api_disabled", state: "error", status: "API not enabled", primary: { label: "Enable Google Gmail API", href: "https://console.cloud.google.com/apis/library/gmail.googleapis.com?project=tofi-sample-123456" }, error: "The Google Gmail API isn’t enabled in your Google Cloud project. Enable it, then refresh." }),
    connectionRow("error", "Connection row, failed with a named reason", { name: "acme_tracker", kind: "error", status: "Connection failed", primary: { label: "Retry", onClick: noop }, error: "Couldn’t load tools. Check the server URL and credentials, then try again." }),
    connectionRow("checking", "Connection row, checking", { name: "Notion", kind: "checking", status: "Loading tools…" }),
    connectionRow("waiting", "Connection row, waiting for authorization", { name: "Notion", kind: "waiting", status: "Waiting for authorization", primary: { label: "Continue authorization", href: "https://auth.example.test/", pending: true } }),
    { id: "connection-waiting-loopback", kind: "inline", label: "Connection row, waiting (loopback-only service): one quiet route line, breathing Continue button", note: "no illustration; the ring is opacity and scale only, static under reduced motion",
      render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name="Robinhood" kind="waiting" state="waiting" statusText="Waiting for authorization" primary={{ label: "Continue authorization", href: "https://auth.example.test/", pending: true }} menu={connectionMenu}>
        <div className="mcp-login-note"><p role="status">{mcpOAuthRoute("https:", false, false, synthOptions, true).note}</p></div>
      </ConnectionRowView></div></div> },
    { id: "connection-needs-auth-loopback", kind: "inline", label: "Connection row, needs authorization (loopback-only service): one quiet route line",
      render: () => <div className="extension-panel mcp-settings"><div className="mcp-rows"><ConnectionRowView name="Robinhood" kind="needs_auth" state="auth" statusText="Needs authorization" primary={{ label: "Authorize", onClick: noop }} menu={connectionMenu}>
        <div className="mcp-login-note"><p role="status">{mcpOAuthRoute("https:", false, false, synthOptions, true).note}</p></div>
      </ConnectionRowView></div></div> },
    { id: "paste-back", kind: "inline", label: "Paste-back sign-in, address field", note: "the pasted text is never echoed; only a named reason shows",
      render: () => <PasteBackView title="Robinhood" session={{ authorization_url: "https://auth.example.test/authorize", redirect_uri: "http://127.0.0.1:43821/oauth/callback" }} pasted="" busy={false} error="" onPasted={noop} onSubmit={noop} onCancel={noop} /> },
    { id: "paste-back-error", kind: "inline", label: "Paste-back sign-in, address refused", render: () => <PasteBackView title="Robinhood" session={{ authorization_url: "https://auth.example.test/authorize", redirect_uri: "http://127.0.0.1:43821/oauth/callback" }} pasted="http://127.0.0.1:43821/oauth/callback?code=sample&state=wrong" busy={false} error={t("common:apiError.oauth_paste_invalid")} onPasted={noop} onSubmit={noop} onCancel={noop} /> },
    { id: "paste-back-starting", kind: "inline", label: "Paste-back sign-in, preparing", render: () => <PasteBackView title="Robinhood" session={null} pasted="" busy={false} error="" onPasted={noop} onSubmit={noop} onCancel={noop} /> },
    connectDialog("directory", "Add connection: directory", { view: "directory" }, null, { note: "Gmail (gogcli) first, official services, the Workspace preview grouping with its enrolment line, then Custom" }),
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
