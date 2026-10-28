import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiError, request } from "./api";
import type { Bot } from "./types";
import { VMOAuthDialog, type VMOAuthSession } from "./VMOAuthDialog";
import { Disclosure } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import { integrationCatalog, LOCAL_PLUGIN_REPOS, type IntegrationPreset } from "./integrationCatalog";
import { useRepoStars } from "./connections/repoStars";
import { isDesktop } from "./desktop";
import { mcpOAuthRoute, type OAuthOptions, type OAuthRoute } from "./mcpOAuthRoute";
import { i18n, useTranslation } from "./i18n";
import { formatRelativeTime } from "./i18n/format";
import { ConnectionsEmpty, type EmptyTileId } from "./connections/ConnectionsEmpty";
import { ConnectDialog, type DialogRequest } from "./connections/ConnectDialog";
import { ConnectionRowView, type RowMenuItem, type RowPrimary } from "./connections/RowView";
import type { AdvancedFacts, DetailDanger } from "./connections/ConnectionDetail";
import { GoogleSetupLinks } from "./connections/RemoteForm";
import { PasteBackDialog } from "./connections/PasteBackDialog";
import { useLocalPlugins, type LocalState, type Plugin } from "./connections/localPlugins";
import { CONNECTIONS_CHANGED } from "./settings/useConnectionAttention";
import { useConnectionAllowances } from "./connections/allowances";
import { useConnectionBots } from "./connections/bots";
import { isOneClick, openSignInTab, presetDefaults } from "./connections/oneClick";
import { displayName, presetFor } from "./connections/names";
import { OPEN_CONNECTION_EVENT, takeConnectionTarget } from "./settings/deepLinks";
import { connectionKind, primaryAction, type Phase } from "./connections/status";
import { GOOGLE_SERVICE_NAME, googleServiceForAPI, googleServicesLabel, isConsoleURL, useGoogleOverview, type GoogleOverview } from "./connections/google";
import { endpoint, localPluginId, message, normalizeTiers, post, toolsEndpoint, type MCP, type TestResult, type ToolTierList } from "./connections/shared";
import "./connections/connections.css";

export type { ToolTier, ToolTierView, ToolTierList } from "./connections/shared";

/** The connection's page loads with the Settings chunk but not before a row is opened. */
const ConnectionDetailView = lazy(() => import("./connections/ConnectionDetail").then(module => ({ default: module.ConnectionDetailView })));

/** A refused removal says why (a task is using the plugin) and the row stays. */
const removeError = (e: unknown) => e instanceof ApiError && e.code === "plugin_busy" ? i18n.t("common:apiError.plugin_busy") : message(e);
/** The open-source repository behind a connection, when it has one. */
const repoFor = (server: MCP, preset: IntegrationPreset | undefined) => preset?.repo ?? LOCAL_PLUGIN_REPOS[localPluginId(server.name)] ?? "";

/** Where a connection comes from, in the words a person uses: the plugin and what it talks to, or the vendor host. */
function sourceLine(server: MCP, preset: IntegrationPreset | undefined, pluginId: string) {
  const t = i18n.getFixedT(null, "extensions");
  if (pluginId === "gog") return t("connections.source.gog");
  if (pluginId && server.provenance?.origin === "community" && server.provenance.package) return t("connections.source.package", { package: server.provenance.package, version: server.provenance.version ?? "" });
  if (pluginId) return t("connections.source.local", { name: pluginId });
  let host = "";
  try { host = new URL(preset?.url ?? server.url).host; } catch { host = ""; }
  return t(preset ? (preset.upstream === "community" ? "connections.source.community" : "connections.source.vendor") : "connections.source.custom", { host });
}

/** Settings › Connections: one list, one row per connection; a row opens that connection's page. Setup stays in a dialog. */
export function MCPSettings({ refreshToken = 0, bots = [] }: { refreshToken?: number; bots?: Bot[] }) {
  const { t } = useTranslation(["extensions", "common"]);
  const [options, setOptions] = useState<OAuthOptions | null>(null);
  useEffect(() => { let alive = true; void request<OAuthOptions>("/api/extensions/oauth-options").then(result => { if (alive) setOptions(result); }).catch(() => { if (alive) setOptions({ vm_available: false, web_callback_origin: "", desktop_redirect_uri: "" }); }); return () => { alive = false; }; }, []);
  const route = mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options);
  const [servers, setServers] = useState<MCP[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [listError, setListError] = useState("");
  const [dialog, setDialog] = useState<DialogRequest | null>(null);
  const [checks, setChecks] = useState<Record<string, number>>({});
  // The open connection's page: rows stay mounted behind it (one probe per connection however it is shown) and portal it here.
  const [selected, setSelected] = useState<string | null>(null);
  const [nav, setNav] = useState<"" | "forward" | "back">("");
  const [detailSlot, setDetailSlot] = useState<HTMLElement | null>(null);
  const [dialogSlot, setDialogSlot] = useState<HTMLElement | null>(null);
  const { local, refreshLocal } = useLocalPlugins(!isDesktop, refreshToken);
  const { google, refreshGoogle } = useGoogleOverview(refreshToken);
  // Focus returns to the control that opened the dialog (the settings window around it would otherwise keep it); a menu
  // item that opened it is gone by then, so the page's own Add button (or the open page's Back button) takes over.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const openDialog = (next: DialogRequest) => { opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setDialog(next); };
  const closeDialog = useCallback(() => {
    setDialog(null);
    window.requestAnimationFrame(() => { const target = opener.current?.isConnected ? opener.current : detailSlot?.querySelector<HTMLElement>(".mcp-detail-back") ?? addButton.current; target?.focus(); });
  }, [detailSlot]);
  const version = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++version.current;
    try { const value = await request<{ servers: MCP[] }>("/api/extensions/mcp"); if (current !== version.current) return; setServers(value.servers ?? []); setListError(""); setLoaded(true); window.dispatchEvent(new Event(CONNECTIONS_CHANGED)); }
    catch (e) { if (current === version.current) { setListError(message(e)); setLoaded(true); } }
  }, []);
  useEffect(() => { void refresh(); return () => { version.current++; }; }, [refresh, refreshToken]);
  useEffect(() => { const focus = () => { if (!document.hidden) void refresh(); }; window.addEventListener("focus", focus); document.addEventListener("visibilitychange", focus); return () => { window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); }; }, [refresh]);
  const refreshAll = useCallback(async () => { await Promise.all([refresh(), refreshLocal(), refreshGoogle()]); }, [refresh, refreshLocal, refreshGoogle]);
  async function save(value: MCP, editing: boolean) {
    await request("/api/extensions/mcp", { method: editing ? "PUT" : "POST", body: JSON.stringify(value) });
    // Keep the saved row visible immediately, even if refreshing the full list fails.
    setServers(items => [...items.filter(item => item.name !== value.name), value]);
    setChecks(values => ({ ...values, [value.name]: (values[value.name] ?? 0) + 1 }));
    closeDialog(); await refresh();
  }
  // A plugin installed on this server but not yet connected to Tofi still counts as a connection the person has.
  const orphans = local.plugins.filter(plugin => !servers.some(server => server.name === `local_${plugin.id}`));
  const total = servers.length + orphans.length;
  const stars = useRepoStars(servers.map(server => repoFor(server, presetFor(server))));
  const empty = loaded && !listError && total === 0;
  // One click: save the preset's defaults and go straight to its sign-in (the row takes it from here, with the tab opened in the click).
  const [signIn, setSignIn] = useState<{ name: string; popup: Window | null } | null>(null);
  async function oneClick(value: MCP, popup: Window | null) {
    await request("/api/extensions/mcp", { method: "POST", body: JSON.stringify(value) });
    setServers(items => [...items.filter(item => item.name !== value.name), value]);
    setSignIn({ name: value.name, popup });
    closeDialog(); await refresh();
  }
  const startTile = (id: EmptyTileId) => {
    const preset = integrationCatalog.find(item => item.id === id);
    if (id === "google") { openDialog({ view: "google" }); return; }
    if (preset && isOneClick(preset, servers, route)) { openDialog({ view: "connecting", preset, popup: openSignInTab(preset.id, preset.oauthRedirect === "loopback" ? mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options, true) : route) }); return; }
    openDialog(preset ? { view: "preset", preset } : { view: "directory" });
  };
  const [focusTool, setFocusTool] = useState<string | undefined>();
  const open = (name: string, tool?: string) => { setNav("forward"); setFocusTool(tool); setSelected(name); };
  // The approval card's "Manage" link: a target waits for this page when Settings was closed, or arrives as an event when it is open.
  useEffect(() => {
    const take = () => { const target = takeConnectionTarget(); if (target) open(target.server, target.tool); };
    take();
    window.addEventListener(OPEN_CONNECTION_EVENT, take);
    return () => window.removeEventListener(OPEN_CONNECTION_EVENT, take);
  }, []);
  // A link to a connection that is gone (removed since the card was written) leaves the list showing, not a blank page.
  useEffect(() => { if (loaded && selected && !servers.some(server => server.name === selected)) { setSelected(null); setNav(""); } }, [loaded, selected, servers]);
  const close = () => { setNav("back"); setSelected(null); };
  return <div className="extension-panel mcp-settings" data-view={selected ? "detail" : "list"} data-nav={nav || undefined}>
    <div className="mcp-list-pane" hidden={selected !== null} inert={selected !== null || undefined}>
      <div className="mcp-toolbar"><h3>{t("connections.title")} <span>{total || ""}</span></h3><button ref={addButton} className="mcp-button" onClick={() => openDialog({ view: "directory" })}><TofiIcon name="plus" size={16} />{t("connections.add")}</button></div>
      {listError && <div className="mcp-list-error" role="alert"><span>{listError}</span><button className="mcp-button" onClick={() => void refresh()}>{t("common:action.retry")}</button></div>}
      {!loaded && <p className="mcp-placeholder">{t("mcp.loading")}</p>}
      {empty && <ConnectionsEmpty onStart={startTile} onBrowse={() => openDialog({ view: "directory" })} />}
      <div className="mcp-rows">
        {servers.map(server => <MCPRow key={server.name} route={route} options={options} botId={bots.find(bot => !bot.archived)?.id} server={server} local={local} google={google} stars={stars} checkRequest={checks[server.name] ?? 0} refresh={refresh} refreshLocal={refreshLocal}
          signIn={signIn?.name === server.name ? signIn : null} onSignInStarted={() => setSignIn(null)} selected={selected === server.name} focusTool={selected === server.name ? focusTool : undefined} detailSlot={detailSlot} dialogSlot={dialogSlot} onOpen={() => open(server.name)} onBack={close}
          onEdit={() => openDialog({ view: "edit", server, preset: presetFor(server) })} onAuthorizeLocal={() => openDialog({ view: "gog" })}
          onRemoved={() => { setServers(items => items.filter(item => item.name !== server.name)); setSelected(null); setNav("back"); void refreshAll(); }} />)}
        {orphans.map(plugin => <PluginRow key={plugin.id} plugin={plugin} onChanged={refreshAll} />)}
      </div>
    </div>
    <div className="mcp-detail-pane" ref={setDetailSlot} hidden={selected === null} />
    <div ref={setDialogSlot} />
    {dialog && <ConnectDialog request={dialog} servers={servers} local={local} google={google} options={options} route={route} onSave={save} onOneClick={oneClick} onLocalChanged={refreshAll} onClose={closeDialog} />}
  </div>;
}

/** A local plugin the server has but Tofi is not yet using. One action: connect it. */
function PluginRow({ plugin, onChanged }: { plugin: Plugin; onChanged: () => Promise<void> }) {
  const { t } = useTranslation("extensions");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState(false);
  const name = plugin.id === "gog" ? t("connections.local.gog.name") : plugin.id;
  async function act(run: () => Promise<unknown>) { setBusy(true); setError(""); try { await run(); await onChanged(); setConfirm(false); } catch (e) { setError(removeError(e)); } finally { setBusy(false); } }
  const menu: RowMenuItem[] = [{ key: "remove", label: t("mcp.menu.remove"), danger: true, onSelect: () => setConfirm(true) }];
  return <ConnectionRowView name={name} mark={plugin.id === "gog" ? "Gmail" : name} state="idle" kind="needs_auth" statusText={t("status.setup")} error={error} menuBusy={busy} menu={menu}
    primary={{ label: t("mcp.connect"), disabled: busy, onClick: () => void act(() => request(`/api/extensions/local-mcp/${encodeURIComponent(plugin.id)}/attach`, post({}))) }}>
    {confirm && <div className="mcp-confirm"><span>{t("mcp.confirm.remove", { name })}</span><button className="mcp-button" disabled={busy} onClick={() => setConfirm(false)}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={busy} onClick={() => void act(() => request(`/api/extensions/local-mcp/${encodeURIComponent(plugin.id)}`, { method: "DELETE" }))}>{t("mcp.confirm.remove_submit")}</button></div>}
  </ConnectionRowView>;
}

function MCPRow({ server, checkRequest, refresh, refreshLocal, onEdit, onRemoved, onAuthorizeLocal, route: pageRoute, options, botId, local, google, stars, selected, focusTool, signIn, onSignInStarted, detailSlot, dialogSlot, onOpen, onBack }: {
  stars: Record<string, number>; route: OAuthRoute; options: OAuthOptions | null; botId?: string; server: MCP; local: LocalState; google: GoogleOverview | null; checkRequest: number; refresh: () => Promise<void>; refreshLocal: () => Promise<void>;
  onEdit: () => void; onRemoved: () => void; onAuthorizeLocal: () => void;
  signIn: { popup: Window | null } | null; onSignInStarted: () => void;
  selected: boolean; focusTool?: string; detailSlot: HTMLElement | null; dialogSlot: HTMLElement | null; onOpen: () => void; onBack: () => void;
}) {
  const { t } = useTranslation(["extensions", "common"]);
  const known = server.status;
  // A provider that accepts only a 127.0.0.1 redirect never takes the https popup: the shared computer, else paste-back.
  const loopbackOnly = server.oauth?.redirect_policy === "loopback";
  const route = loopbackOnly ? mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options, true) : pageRoute;
  const [phase, setPhase] = useState<Phase>(server.oauth && !server.oauth.connected ? "auth" : known ? (known.ok ? "ready" : known.error_class === "auth" ? "auth" : "error") : "idle");
  const [checkedAt, setCheckedAt] = useState<string | undefined>(known?.checked_at);
  const [error, setError] = useState(known && !known.ok && known.error_class !== "auth" ? known.error_code === "api_disabled" ? "" : i18n.t("extensions:error.tools_failed") : "");
  const [authURL, setAuthURL] = useState("");
  // Google said the API is not enabled in the project: which API, and the console page that enables it.
  const [apiIssue, setApiIssue] = useState<{ api: string; url: string } | null>(known?.error_code === "api_disabled" && isConsoleURL(known.enable_url) ? { api: known.api ?? "", url: known.enable_url } : null);
  const [vmSession, setVMSession] = useState<VMOAuthSession | null>(null);
  const [pasteOpen, setPasteOpen] = useState(false);
  const [pastePopup, setPastePopup] = useState<Window | null>(null);
  const [confirm, setConfirm] = useState<"remove" | "disconnect" | "disconnect_google" | null>(null);
  const openButton = useRef<HTMLButtonElement>(null);
  const [tiers, setTiers] = useState<ToolTierList | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const [toolsError, setToolsError] = useState("");
  const loadTiers = useCallback(async () => { try { const value = await request<ToolTierList>(toolsEndpoint(server.name)); if (alive.current) setTiers(normalizeTiers(value)); } catch { /* The row keeps its last tool list; the details view shows the empty state. */ } }, [server.name]);
  const alive = useRef(true); const operation = useRef(0); const abort = useRef<AbortController | null>(null);
  const previouslyConnected = useRef(server.oauth?.connected);
  const authorizationPopup = useRef<Window | null>(null);
  const nativePending = useRef(false);
  const preset = presetFor(server);
  const display = displayName(server, preset);
  const pluginId = localPluginId(server.name);
  const isGog = pluginId === "gog";
  const account = isGog ? local.google : null;
  // A local plugin answers tools without ever having been given a Google account: that is "needs authorization".
  const accountMissing = isGog && account !== null && !account.connected;
  const paused = server.enabled === false;
  const [pausing, setPausing] = useState(false);
  const allowances = useConnectionAllowances(server.name, selected);
  const connectionBots = useConnectionBots(server.name, selected);
  const busy = pausing || vmSession !== null || pasteOpen || (route.mode === "desktop" && phase === "waiting") || ["checking", "authorizing", "removing"].includes(phase);
  useEffect(() => { alive.current = true; return () => { alive.current = false; operation.current++; abort.current?.abort(); if (nativePending.current) void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(() => {}); }; }, [server.name]);
  const check = useCallback(async () => {
    const id = ++operation.current; abort.current?.abort(); const controller = new AbortController(); abort.current = controller;
    setError(""); setPhase("checking");
    try {
      const result = await request<TestResult>(endpoint(server.name) + "/test", { ...post({}), signal: controller.signal });
      if (!alive.current || operation.current !== id) return;
      if (result.ok) { setCheckedAt(new Date().toISOString()); setPhase("ready"); setAuthURL(""); setApiIssue(null); }
      else if (result.auth_required) { setPhase("auth"); }
      else {
        const disabled = result.diagnostics?.find(item => item.code === "api_disabled" && isConsoleURL(item.enable_url));
        setApiIssue(disabled ? { api: disabled.api ?? "", url: disabled.enable_url! } : null);
        setPhase("error"); setError(disabled ? "" : result.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("error.tools_failed"));
      }
    } catch (e) { if (alive.current && operation.current === id) { setPhase("error"); setError(message(e)); } }
    // The server stored this check's outcome: the list (and the rail badge) read it back.
    if (alive.current && operation.current === id) void refresh();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name, server.oauth?.connected]);
  useEffect(() => { if (checkRequest > 0) void check(); }, [checkRequest, check]);
  useEffect(() => { void loadTiers(); }, [loadTiers]);
  useEffect(() => { if (phase === "ready") void loadTiers(); }, [phase, loadTiers]);
  // Only a queue in motion is polled; a settled list is read once per check.
  // Quiet re-verification on open: no phase change, no motion. One retry absorbs a transient blip; only a repeated failure changes the row.
  useEffect(() => {
    if ((server.oauth && !server.oauth.connected) || checkRequest > 0 || server.enabled === false) return;
    const base = operation.current; let live = true; const controller = new AbortController(); let timer = 0;
    const probe = async () => {
      try { return await request<TestResult>(endpoint(server.name) + "/test", { ...post({}), signal: controller.signal }); }
      catch (e) { return controller.signal.aborted ? null : { ok: false, diagnostics: [{ message: e instanceof Error ? e.message : String(e) }] } as TestResult; }
    };
    void (async () => {
      let r = await probe();
      if (!live || !r) return;
      if (!r.ok && !r.auth_required) {
        await new Promise<void>(done => { timer = window.setTimeout(done, 1500); });
        if (!live) return;
        r = await probe();
        if (!live || !r) return;
      }
      if (!alive.current || operation.current !== base) return;
      if (r.ok) { setCheckedAt(new Date().toISOString()); setError(""); setPhase(p => p === "idle" || p === "ready" || p === "error" ? "ready" : p); }
      else if (r.auth_required) { setPhase("auth"); }
      else {
        const disabled = r.diagnostics?.find(item => item.code === "api_disabled" && isConsoleURL(item.enable_url));
        if (disabled) setApiIssue({ api: disabled.api ?? "", url: disabled.enable_url! });
        setPhase(p => p === "idle" || p === "ready" || p === "error" ? "error" : p); setError(disabled ? "" : r.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("error.tools_failed"));
      }
    })();
    return () => { live = false; window.clearTimeout(timer); controller.abort(); };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name, server.url]);
  useEffect(() => { if (!vmSession && !previouslyConnected.current && server.oauth?.connected && checkRequest === 0) { setAuthURL(""); void check(); } else if (previouslyConnected.current && !server.oauth?.connected) { setPhase("auth"); } previouslyConnected.current = server.oauth?.connected; }, [server.oauth?.connected, checkRequest, check, vmSession]);
  useEffect(() => {
    if (phase !== "waiting" || vmSession || pasteOpen || isDesktop) return;
    const timer = window.setInterval(() => {
      const popup = authorizationPopup.current;
      try {
        if (popup && !popup.closed && popup.location.origin === window.location.origin && popup.location.pathname === endpoint(server.name) + "/oauth/callback") {
          if (popup.document.title === "Connection failed") { setPhase("auth"); setError(i18n.t("extensions:error.auth_incomplete")); return; }
          if (popup.document.title === "Connection complete") { authorizationPopup.current = null; void refresh(); void check(); return; }
        }
      } catch { /* The provider page is cross-origin until it returns to Tofi. */ }
      if (!document.hidden) void refresh();
    }, 2000);
    const timeout = window.setTimeout(() => { setPhase("auth"); setError(i18n.t("extensions:error.auth_timeout")); }, 10 * 60 * 1000);
    return () => { window.clearInterval(timer); window.clearTimeout(timeout); };
  }, [phase, refresh, check, server.name, vmSession, pasteOpen]);
  // After the person enables the API in the console and comes back, look again.
  useEffect(() => {
    if (!apiIssue) return;
    const back = () => { if (!document.hidden) void check(); };
    window.addEventListener("focus", back);
    return () => window.removeEventListener("focus", back);
  }, [apiIssue, check]);
  async function authorize(preopened: Window | null = null) {
    const id = ++operation.current; abort.current?.abort(); const controller = new AbortController(); abort.current = controller;
    setError(""); setAuthURL(""); setPhase("authorizing");
    if (route.mode === "blocked") { preopened?.close(); setPhase("auth"); setError(route.note); return; }
    if (route.mode === "desktop") {
      preopened?.close();
      nativePending.current = true; setPhase("waiting");
      try {
        await window.tofiDesktop!.authorizeMCP!(server.name);
        if (alive.current && id === operation.current) { await refresh(); void check(); }
      } catch (e) { if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); } }
      finally { nativePending.current = false; }
      return;
    }
    if (route.mode === "vm") {
      preopened?.close();
      try {
        if (!botId) throw new Error(t("error.needs_bot"));
        const result = await request<VMOAuthSession>(endpoint(server.name) + "/oauth/vm/start", { ...post({ bot_id: botId }), signal: controller.signal });
        if (!alive.current || id !== operation.current) { void request(endpoint(server.name) + `/oauth/vm/${encodeURIComponent(result.session_id)}/cancel`, { ...post({}), keepalive: true }).catch(() => {}); return; }
        setVMSession(result); setPhase("waiting");
      } catch (e) { if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); } }
      return;
    }
    if (route.mode === "paste") {
      // The sign-in tab opens inside the click (a later window.open is blocked); the dialog points it at the provider.
      const tab = preopened ?? window.open(endpoint(server.name) + "/oauth/pending", "_blank"); if (tab) tab.opener = null;
      setPastePopup(tab); setPasteOpen(true); setPhase("waiting"); return;
    }
    // Open during the user gesture; asynchronous window.open is blocked by many browsers.
    let popup: Window | null = null;
    try {
      popup = preopened ?? window.open(endpoint(server.name) + "/oauth/pending", "_blank"); authorizationPopup.current = popup; if (popup) popup.opener = null;
      const result = await request<{ authorization_url: string }>(endpoint(server.name) + "/oauth/start", { ...post({}), signal: controller.signal });
      if (!alive.current || id !== operation.current) { popup?.close(); return; }
      const url = new URL(result.authorization_url); if (!["https:", "http:"].includes(url.protocol)) throw new Error(t("error.invalid_auth_url"));
      setAuthURL(url.href); setPhase("waiting"); if (popup && !popup.closed) popup.location.replace(url.href);
    } catch (e) {
      // The provider refused the public callback at registration: the server remembered that, so sign in by paste-back with the tab already open.
      if (e instanceof ApiError && e.code === "oauth_loopback_required" && alive.current && id === operation.current) { setPastePopup(popup); setPasteOpen(true); setPhase("waiting"); void refresh(); return; }
      popup?.close(); if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); }
    }
  }
  // A one-click connection: the dialog saved it and handed over the tab opened in the click; sign in right away.
  useEffect(() => { if (signIn) { onSignInStarted(); void authorize(signIn.popup); } }, [signIn]); // eslint-disable-line react-hooks/exhaustive-deps
  async function verifyGoogle() {
    const id = ++operation.current; setPhase("checking"); setError("");
    try { await request("/api/extensions/local-mcp/gog/oauth/check", post({})); if (alive.current && operation.current === id) await check(); }
    catch (e) {
      if (!alive.current || operation.current !== id) return;
      const url = e instanceof ApiError && e.code === "api_disabled" ? e.data?.enable_url : undefined;
      if (isConsoleURL(typeof url === "string" ? url : undefined)) { setApiIssue({ api: String(e instanceof ApiError ? e.data?.api ?? "gmail.googleapis.com" : ""), url: url as string }); setPhase("error"); setError(""); return; }
      setPhase("error"); setError(message(e));
    }
  }
  async function confirmAction() {
    const id = ++operation.current; setPhase("removing"); setError("");
    try {
      if (confirm === "disconnect_google") { await request("/api/extensions/local-mcp/gog/oauth/disconnect", post({})); await refreshLocal(); }
      else if (confirm === "disconnect") await request(endpoint(server.name) + "/oauth/disconnect", post({}));
      else await request(pluginId ? `/api/extensions/local-mcp/${encodeURIComponent(pluginId)}` : endpoint(server.name), { method: "DELETE" });
      if (!alive.current || id !== operation.current) return;
      if (confirm === "remove") onRemoved();
      else { setAuthURL(""); setPhase(confirm === "disconnect" ? "auth" : "ready"); await refresh(); }
      setConfirm(null);
    } catch (e) { if (alive.current && id === operation.current) { if (confirm === "remove") { setPhase("ready"); setConfirm(null); } else setPhase("error"); setError(confirm === "remove" ? removeError(e) : message(e)); } }
  }
  // Pause keeps the configuration, sign-in, tool marks and allowances; only the Bots lose sight of it.
  async function setPaused(next: boolean) {
    setPausing(true); setError("");
    abort.current?.abort(); operation.current++;
    try {
      await request(endpoint(server.name) + "/enabled", { method: "PUT", body: JSON.stringify({ enabled: !next }) });
      if (!alive.current) return;
      await refresh();
      if (!next && alive.current) void check();
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { if (alive.current) setPausing(false); }
  }
  const needsAuth = Boolean(server.oauth) && (!server.oauth?.connected || phase === "auth");
  const apiService = apiIssue ? googleServiceForAPI(apiIssue.api) : "";
  const apiName = apiService ? GOOGLE_SERVICE_NAME[apiService] : display;
  const kind = connectionKind({ phase, needsAuth, error, accountMissing, apiDisabled: Boolean(apiIssue), paused });
  const statusText = kind === "checking" ? t("status.checking") : kind === "ready" ? t("status.connected") : kind === "working" ? (phase === "authorizing" ? t("status.authorizing") : t("status.removing")) : kind === "waiting" ? t("status.waiting") : kind === "error" ? t("status.failed") : kind === "needs_auth" ? t("status.needs_auth") : kind === "paused" ? t("status.paused") : t("status.setup");
  const action = primaryAction(kind, Boolean(authURL), route.mode === "desktop");
  const authorizeNow = () => { if (accountMissing && !needsAuth) onAuthorizeLocal(); else void authorize(); };
  const primary: RowPrimary | null = action === "enable_api" && apiIssue ? { label: t("connections.action.enable_api", { name: apiName }), href: apiIssue.url }
    : action === "resume" ? { label: t("connections.action.resume"), disabled: busy, onClick: () => void setPaused(false) }
    : action === "authorize" ? { label: t("connections.action.authorize"), disabled: busy || (route.mode === "blocked" && !accountMissing), onClick: authorizeNow }
    : action === "retry" ? { label: t("common:action.retry"), disabled: busy, onClick: () => { if (needsAuth || accountMissing) authorizeNow(); else void check(); } }
    : action === "cancel" ? { label: t("mcp.cancel_auth"), pending: true, onClick: () => void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(e => setError(message(e))) }
    : action === "continue" ? { label: t("mcp.continue_auth"), href: authURL, pending: true } : null;
  const source = sourceLine(server, preset, pluginId);
  const repo = repoFor(server, preset);
  const toolCount = tiers?.tools.length ?? server.status?.tool_count;
  const services = account?.connected ? googleServicesLabel(account.services) : "";
  const detail = kind === "ready" ? [toolCount ? t("connections.row.tools", { count: toolCount }) : "", account?.connected ? account.email : "", services].filter(Boolean).join(" · ") || undefined : undefined;
  const checked = kind === "ready" && checkedAt ? t("status.checked", { when: formatRelativeTime(checkedAt) }) : undefined;
  const rowError = kind === "api_disabled" ? t("connections.error.api_disabled", { name: apiName }) : error;
  const loginNote = (route.mode === "blocked" ? needsAuth : loopbackOnly && (needsAuth || phase === "waiting")) ? route.note : "";
  async function refreshTools() {
    setRefreshing(true); setToolsError("");
    try {
      const value = normalizeTiers(await request<ToolTierList>(toolsEndpoint(server.name) + "/refresh", post({})));
      if (!alive.current) return;
      setTiers(value);
      if (value.error_class) setToolsError(value.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("tools.error.refresh_failed"));
    } catch (e) { if (alive.current) setToolsError(message(e)); }
    finally { if (alive.current) setRefreshing(false); }
  }
  const menu: RowMenuItem[] = [];
  if (!pluginId) menu.push({ key: "edit", label: t("mcp.menu.edit"), onSelect: onEdit });
  if (isGog && account?.connected) menu.push({ key: "sign_in_again", label: t("connections.menu.sign_in_again"), onSelect: onAuthorizeLocal }, { key: "verify", label: t("local.google.verify"), onSelect: () => void verifyGoogle() });
  const danger: DetailDanger = [{ key: "pause", label: paused ? t("detail.resume") : t("detail.pause"), onSelect: () => void setPaused(!paused) }];
  if (!pluginId && server.oauth?.connected) danger.push({ key: "disconnect", label: t("mcp.menu.disconnect"), onSelect: () => setConfirm("disconnect") });
  if (isGog && account?.connected) danger.push({ key: "disconnect_google", label: t("local.google.disconnect"), onSelect: () => setConfirm("disconnect_google") });
  danger.push({ key: "remove", label: t("mcp.menu.remove"), remove: true, onSelect: () => setConfirm("remove") });
  const confirmText = confirm === "remove" ? t("mcp.confirm.remove", { name: display }) : confirm === "disconnect_google" ? t("local.google.disconnect_confirm") : t("mcp.confirm.disconnect", { name: display });
  let address = ""; try { const url = new URL(server.url); address = url.origin + url.pathname; } catch { address = ""; }
  const facts: string[] = [];
  if (account?.connected) facts.push(`${t("connections.details.account", { email: account.email })} · ${account.scope === "read-send" ? t("local.google.granted_read_send") : t("local.google.granted_readonly")}`);
  const advanced: AdvancedFacts = {
    source, repo: repo ? { name: repo, stars: stars[repo] } : undefined, address: pluginId ? undefined : address, transport: pluginId ? undefined : server.transport ?? "streamable_http",
    headers: server.headers ? Object.keys(server.headers) : undefined, allow: server.tool_allowlist, deny: server.tool_denylist, scopes: server.oauth?.scopes, redirect: server.oauth?.redirect_policy,
    pkg: server.provenance?.origin === "community" && server.provenance.package ? `${server.provenance.package} ${server.provenance.version ?? ""}`.trim() : undefined,
    project: isGog && google?.client.configured ? google.client.project_id || google.client.project_number : undefined,
    previewNote: Boolean(preset?.googleAPIs), communityNote: server.provenance?.origin === "community",
  };
  const help = (kind === "error" || kind === "api_disabled") && preset?.googleAPIs ? <div className="mcp-service-setup"><Disclosure title={t("google.help")} defaultOpen><GoogleSetupLinks preset={preset} docs /></Disclosure></div> : undefined;
  const leave = () => { onBack(); window.requestAnimationFrame(() => openButton.current?.focus()); };
  return <>
    <ConnectionRowView name={display} mark={isGog ? "Gmail" : display} state={error ? "error" : phase} kind={kind} statusText={statusText} source={source}
      detail={detail} checkedText={checked} primary={primary} onOpen={onOpen} openRef={openButton} error={rowError}>
      {loginNote && <div className="mcp-login-note"><p role="status">{loginNote}</p></div>}
    </ConnectionRowView>
    {selected && detailSlot && createPortal(<Suspense fallback={null}><ConnectionDetailView name={display} mark={isGog ? "Gmail" : display} source={source} serverName={server.name} kind={kind} statusText={statusText} detail={detail} checkedText={checked}
      error={rowError} note={paused ? t("detail.paused_note") : loginNote || undefined} focusTool={focusTool} bots={connectionBots.value} onBotsAll={() => void connectionBots.setAll()} onBotsChosen={ids => void connectionBots.setChosen(ids)} allowances={allowances.allowances} onRevoke={allowances.revoke} onDropAllowance={allowances.drop} primary={primary} menu={menu} busy={busy} facts={facts} tiers={tiers} onTiers={setTiers} community={server.provenance?.origin === "community"} toolsError={toolsError} advanced={advanced} help={help}
      danger={danger} confirm={confirm ? { text: confirmText, submit: confirm === "remove" ? t("mcp.confirm.remove_submit") : t("mcp.confirm.disconnect_submit"), onCancel: () => setConfirm(null), onConfirm: () => void confirmAction() } : null}
      onBack={leave} onRefresh={() => void refreshTools()} refreshing={refreshing} /></Suspense>, detailSlot)}
    {dialogSlot && createPortal(<>
      {vmSession && <VMOAuthDialog session={vmSession} serverName={server.name} title={display} onFinish={(status, detail) => { setVMSession(null); if (status === "complete") { void refresh(); void check(); } else { setPhase("auth"); setError(detail || ""); } }} />}
      {pasteOpen && <PasteBackDialog serverName={server.name} title={display} popup={pastePopup} onFinish={(status, detail) => { setPasteOpen(false); setPastePopup(null); if (status === "complete") { void refresh(); void check(); } else { setPhase("auth"); setError(status === "cancelled" ? "" : detail || ""); } }} />}
    </>, dialogSlot)}
  </>;
}
