import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import type { Bot } from "./types";
import { VMOAuthDialog, type VMOAuthSession } from "./VMOAuthDialog";
import { Disclosure } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import { integrationCatalog, type IntegrationPreset } from "./integrationCatalog";
import { isDesktop } from "./desktop";
import { mcpOAuthRoute, type OAuthOptions, type OAuthRoute } from "./mcpOAuthRoute";
import { CatStage } from "./CatStage";
import { i18n, useTranslation } from "./i18n";
import { formatRelativeTime } from "./i18n/format";
import { CATS, EmptyState } from "./EmptyCat";
import { ConnectDialog, type DialogRequest } from "./connections/ConnectDialog";
import { ConnectionRowView, type RowMenuItem, type RowPrimary } from "./connections/RowView";
import { GoogleSetupLinks } from "./connections/RemoteForm";
import { ToolTierPanel } from "./connections/ToolTierPanel";
import { useLocalPlugins, type LocalState, type Plugin } from "./connections/localPlugins";
import { connectionKind, primaryAction, type Phase } from "./connections/status";
import { endpoint, localPluginId, message, normalizeTiers, post, toolsEndpoint, type MCP, type TestResult, type ToolTierList } from "./connections/shared";
import "./oauth-link-motion.css";
import "./connections/connections.css";

export type { ToolTier, ToolTierView, ToolTierList } from "./connections/shared";

/** Shown only while the user is signing in; never in a resting card. */
function OAuthLinkMotion({ title }: { title: string }) {
  const { t } = useTranslation("extensions");
  return <div className="web-oauth-link" aria-label={t("oauth.waiting", { title })}><span className="web-oauth-node"><CatStage config={{ shape: "curl", pattern: "calico", palette: "calico" }} initialState="asleep" /></span><svg viewBox="0 0 200 60" aria-hidden="true"><path className="web-oauth-wait" d="M6 44 Q100 -6 194 44" /></svg><span className="web-oauth-provider"><TofiIcon name="key" size={22} /><small>{title}</small></span></div>;
}

const presetFor = (server: MCP) => integrationCatalog.find(item => item.url === server.url);
/** The name a person sees: a catalog name, "Gmail" for the personal Gmail plugin, else what they called it. Never `local_gog`. */
function displayName(server: MCP, preset: IntegrationPreset | undefined) {
  if (preset) return preset.name;
  const plugin = localPluginId(server.name);
  return plugin === "gog" ? i18n.t("extensions:connections.local.gog.name") : plugin || server.name;
}

/** Where a connection comes from, in the words a person uses: the plugin and what it talks to, or the vendor host. */
function sourceLine(server: MCP, preset: IntegrationPreset | undefined, pluginId: string) {
  const t = i18n.getFixedT(null, "extensions");
  if (pluginId === "gog") return t("connections.source.gog");
  if (pluginId) return t("connections.source.local", { name: pluginId });
  let host = "";
  try { host = new URL(preset?.url ?? server.url).host; } catch { host = ""; }
  return t(preset ? (preset.upstream === "community" ? "connections.source.community" : "connections.source.vendor") : "connections.source.custom", { host });
}

/** Settings › Connections: one list, one row per connection, setup in a dialog. */
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
  const { local, refreshLocal } = useLocalPlugins(!isDesktop, refreshToken);
  // Focus returns to the control that opened the dialog (the settings window around it would otherwise keep it); a menu
  // item that opened it is gone by then, so the page's own Add button takes over.
  const opener = useRef<HTMLElement | null>(null);
  const addButton = useRef<HTMLButtonElement>(null);
  const openDialog = (next: DialogRequest) => { opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null; setDialog(next); };
  const closeDialog = useCallback(() => {
    setDialog(null);
    window.requestAnimationFrame(() => { const target = opener.current?.isConnected ? opener.current : addButton.current; target?.focus(); });
  }, []);
  const version = useRef(0);
  const refresh = useCallback(async () => {
    const current = ++version.current;
    try { const value = await request<{ servers: MCP[] }>("/api/extensions/mcp"); if (current !== version.current) return; setServers(value.servers ?? []); setListError(""); setLoaded(true); }
    catch (e) { if (current === version.current) { setListError(message(e)); setLoaded(true); } }
  }, []);
  useEffect(() => { void refresh(); return () => { version.current++; }; }, [refresh, refreshToken]);
  useEffect(() => { const focus = () => { if (!document.hidden) void refresh(); }; window.addEventListener("focus", focus); document.addEventListener("visibilitychange", focus); return () => { window.removeEventListener("focus", focus); document.removeEventListener("visibilitychange", focus); }; }, [refresh]);
  const refreshAll = useCallback(async () => { await Promise.all([refresh(), refreshLocal()]); }, [refresh, refreshLocal]);
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
  return <div className="extension-panel mcp-settings">
    <div className="mcp-toolbar"><h3>{t("connections.title")} <span>{total || ""}</span></h3><button ref={addButton} className="mcp-button" onClick={() => openDialog({ view: "directory" })}><TofiIcon name="plus" size={16} />{t("connections.add")}</button></div>
    {listError && <div className="mcp-list-error" role="alert"><span>{listError}</span><button className="mcp-button" onClick={() => void refresh()}>{t("common:action.retry")}</button></div>}
    {!loaded && <p className="mcp-placeholder">{t("mcp.loading")}</p>}
    {loaded && !listError && total === 0 && <EmptyState name="mcp-empty" className="extension-empty" look={CATS.yuzu} pose="curious" size={72} title={t("mcp.empty")} />}
    <div className="mcp-rows">
      {servers.map(server => <MCPRow key={server.name} route={route} botId={bots.find(bot => !bot.archived)?.id} server={server} local={local} checkRequest={checks[server.name] ?? 0} refresh={refresh} refreshLocal={refreshLocal}
        onEdit={() => openDialog({ view: "edit", server, preset: presetFor(server) })} onAuthorizeLocal={() => openDialog({ view: "gog" })}
        onRemoved={() => { setServers(items => items.filter(item => item.name !== server.name)); void refreshAll(); }} />)}
      {orphans.map(plugin => <PluginRow key={plugin.id} plugin={plugin} onChanged={refreshAll} />)}
    </div>
    {dialog && <ConnectDialog request={dialog} servers={servers} local={local} options={options} route={route} onSave={save} onLocalChanged={refreshAll} onClose={closeDialog} />}
  </div>;
}

/** A local plugin the server has but Tofi is not yet using. One action: connect it. */
function PluginRow({ plugin, onChanged }: { plugin: Plugin; onChanged: () => Promise<void> }) {
  const { t } = useTranslation("extensions");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState(false);
  const name = plugin.id === "gog" ? t("connections.local.gog.name") : plugin.id;
  async function act(run: () => Promise<unknown>) { setBusy(true); setError(""); try { await run(); await onChanged(); setConfirm(false); } catch (e) { setError(message(e)); } finally { setBusy(false); } }
  const menu: RowMenuItem[] = [{ key: "remove", label: t("mcp.menu.remove"), danger: true, onSelect: () => setConfirm(true) }];
  return <ConnectionRowView name={name} mark={plugin.id === "gog" ? "Gmail" : name} state="idle" kind="unverified" statusText={t("status.not_connected")} error={error} menuBusy={busy} menu={menu}
    primary={{ label: t("mcp.connect"), disabled: busy, onClick: () => void act(() => request(`/api/extensions/local-mcp/${encodeURIComponent(plugin.id)}/attach`, post({}))) }}>
    {confirm && <div className="mcp-confirm"><span>{t("mcp.confirm.remove", { name })}</span><button className="mcp-button" disabled={busy} onClick={() => setConfirm(false)}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={busy} onClick={() => void act(() => request(`/api/extensions/local-mcp/${encodeURIComponent(plugin.id)}`, { method: "DELETE" }))}>{t("mcp.confirm.remove_submit")}</button></div>}
  </ConnectionRowView>;
}

function MCPRow({ server, checkRequest, refresh, refreshLocal, onEdit, onRemoved, onAuthorizeLocal, route, botId, local }: {
  route: OAuthRoute; botId?: string; server: MCP; local: LocalState; checkRequest: number; refresh: () => Promise<void>; refreshLocal: () => Promise<void>;
  onEdit: () => void; onRemoved: () => void; onAuthorizeLocal: () => void;
}) {
  const { t } = useTranslation(["extensions", "common"]);
  const known = server.status;
  const [phase, setPhase] = useState<Phase>(server.oauth && !server.oauth.connected ? "auth" : known ? (known.ok ? "ready" : known.error_class === "auth" ? "auth" : "error") : "idle");
  const [checkedAt, setCheckedAt] = useState<string | undefined>(known?.checked_at);
  const [error, setError] = useState(known && !known.ok && known.error_class !== "auth" ? i18n.t("extensions:error.tools_failed") : "");
  const [authURL, setAuthURL] = useState("");
  const [vmSession, setVMSession] = useState<VMOAuthSession | null>(null);
  const [confirm, setConfirm] = useState<"remove" | "disconnect" | "disconnect_google" | null>(null);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [tiers, setTiers] = useState<ToolTierList | null>(null);
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
  const busy = vmSession !== null || (route.mode === "desktop" && phase === "waiting") || ["checking", "authorizing", "removing"].includes(phase);
  useEffect(() => { alive.current = true; return () => { alive.current = false; operation.current++; abort.current?.abort(); if (nativePending.current) void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(() => {}); }; }, [server.name]);
  const check = useCallback(async () => {
    const id = ++operation.current; abort.current?.abort(); const controller = new AbortController(); abort.current = controller;
    setError(""); setPhase("checking");
    try {
      const result = await request<TestResult>(endpoint(server.name) + "/test", { ...post({}), signal: controller.signal });
      if (!alive.current || operation.current !== id) return;
      if (result.ok) { setCheckedAt(new Date().toISOString()); setPhase("ready"); setAuthURL(""); }
      else if (result.auth_required) { setPhase("auth"); }
      else { setPhase("error"); setError(result.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("error.tools_failed")); }
    } catch (e) { if (alive.current && operation.current === id) { setPhase("error"); setError(message(e)); } }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name, server.oauth?.connected]);
  useEffect(() => { if (checkRequest > 0) void check(); }, [checkRequest, check]);
  useEffect(() => { void loadTiers(); }, [loadTiers]);
  useEffect(() => { if (phase === "ready") void loadTiers(); }, [phase, loadTiers]);
  // Only a queue in motion is polled; a settled list is read once per check.
  // Quiet re-verification on open: no phase change, no motion. One retry absorbs a transient blip; only a repeated failure changes the row.
  useEffect(() => {
    if ((server.oauth && !server.oauth.connected) || checkRequest > 0) return;
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
      else { setPhase(p => p === "idle" || p === "ready" || p === "error" ? "error" : p); setError(r.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("error.tools_failed")); }
    })();
    return () => { live = false; window.clearTimeout(timer); controller.abort(); };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server.name, server.url]);
  useEffect(() => { if (!vmSession && !previouslyConnected.current && server.oauth?.connected && checkRequest === 0) { setAuthURL(""); void check(); } else if (previouslyConnected.current && !server.oauth?.connected) { setPhase("auth"); } previouslyConnected.current = server.oauth?.connected; }, [server.oauth?.connected, checkRequest, check, vmSession]);
  useEffect(() => {
    if (phase !== "waiting" || vmSession || isDesktop) return;
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
  }, [phase, refresh, check, server.name, vmSession]);
  async function authorize() {
    const id = ++operation.current; abort.current?.abort(); const controller = new AbortController(); abort.current = controller;
    setError(""); setAuthURL(""); setPhase("authorizing");
    if (route.mode === "blocked") { setPhase("auth"); setError(route.note); return; }
    if (route.mode === "desktop") {
      nativePending.current = true; setPhase("waiting");
      try {
        await window.tofiDesktop!.authorizeMCP!(server.name);
        if (alive.current && id === operation.current) { await refresh(); void check(); }
      } catch (e) { if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); } }
      finally { nativePending.current = false; }
      return;
    }
    if (route.mode === "vm") {
      try {
        if (!botId) throw new Error(t("error.needs_bot"));
        const result = await request<VMOAuthSession>(endpoint(server.name) + "/oauth/vm/start", { ...post({ bot_id: botId }), signal: controller.signal });
        if (!alive.current || id !== operation.current) { void request(endpoint(server.name) + `/oauth/vm/${encodeURIComponent(result.session_id)}/cancel`, { ...post({}), keepalive: true }).catch(() => {}); return; }
        setVMSession(result); setPhase("waiting");
      } catch (e) { if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); } }
      return;
    }
    // Open during the user gesture; asynchronous window.open is blocked by many browsers.
    let popup: Window | null = null;
    try {
      popup = window.open(endpoint(server.name) + "/oauth/pending", "_blank"); authorizationPopup.current = popup; if (popup) popup.opener = null;
      const result = await request<{ authorization_url: string }>(endpoint(server.name) + "/oauth/start", { ...post({}), signal: controller.signal });
      if (!alive.current || id !== operation.current) { popup?.close(); return; }
      const url = new URL(result.authorization_url); if (!["https:", "http:"].includes(url.protocol)) throw new Error(t("error.invalid_auth_url"));
      setAuthURL(url.href); setPhase("waiting"); if (popup && !popup.closed) popup.location.replace(url.href);
    } catch (e) { popup?.close(); if (alive.current && id === operation.current) { setPhase("auth"); setError(message(e)); } }
  }
  async function verifyGoogle() {
    const id = ++operation.current; setPhase("checking"); setError("");
    try { await request("/api/extensions/local-mcp/gog/oauth/check", post({})); if (alive.current && operation.current === id) await check(); }
    catch (e) { if (alive.current && operation.current === id) { setPhase("error"); setError(message(e)); } }
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
    } catch (e) { if (alive.current && id === operation.current) { setPhase("error"); setError(message(e)); } }
  }
  const needsAuth = Boolean(server.oauth) && (!server.oauth?.connected || phase === "auth");
  const kind = connectionKind({ phase, needsAuth, error, accountMissing });
  const statusText = kind === "checking" ? t("status.checking") : kind === "ready" ? t("status.connected") : kind === "working" ? (phase === "authorizing" ? t("status.authorizing") : t("status.removing")) : kind === "waiting" ? t("status.waiting") : kind === "error" ? t("status.failed") : kind === "needs_auth" ? t("status.needs_auth") : t("status.unverified");
  const action = primaryAction(kind, Boolean(authURL), route.mode === "desktop");
  const authorizeNow = () => { if (accountMissing && !needsAuth) onAuthorizeLocal(); else void authorize(); };
  const primary: RowPrimary | null = action === "authorize" ? { label: t("connections.action.authorize"), disabled: busy || (route.mode === "blocked" && !accountMissing), onClick: authorizeNow }
    : action === "retry" ? { label: t("common:action.retry"), disabled: busy, onClick: () => { if (needsAuth || accountMissing) authorizeNow(); else void check(); } }
    : action === "connect" ? { label: t("mcp.connect"), disabled: busy, onClick: () => void check() }
    : action === "cancel" ? { label: t("mcp.cancel_auth"), onClick: () => void window.tofiDesktop?.cancelMCPAuthorization?.(server.name).catch(e => setError(message(e))) }
    : action === "continue" ? { label: t("mcp.continue_auth"), href: authURL } : null;
  const hasTools = Boolean(tiers) && (tiers!.tools.length > 0 || phase === "ready");
  const source = sourceLine(server, preset, pluginId);
  const menu: RowMenuItem[] = [];
  menu.push({ key: "details", label: detailsOpen ? t("connections.menu.hide_details") : t("connections.menu.details"), onSelect: () => setDetailsOpen(open => !open) });
  if (!pluginId) menu.push({ key: "edit", label: t("mcp.menu.edit"), onSelect: onEdit });
  menu.push({ key: "refresh", label: t("mcp.menu.refresh"), onSelect: () => void check() });
  if (isGog && account?.connected) menu.push({ key: "verify", label: t("local.google.verify"), onSelect: () => void verifyGoogle() }, { key: "disconnect_google", label: t("local.google.disconnect"), onSelect: () => setConfirm("disconnect_google") });
  if (!pluginId && server.oauth?.connected) menu.push({ key: "disconnect", label: t("mcp.menu.disconnect"), onSelect: () => setConfirm("disconnect") });
  menu.push({ key: "remove", label: t("mcp.menu.remove"), danger: true, onSelect: () => setConfirm("remove") });
  const confirmText = confirm === "remove" ? t("mcp.confirm.remove", { name: display }) : confirm === "disconnect_google" ? t("local.google.disconnect_confirm") : t("mcp.confirm.disconnect", { name: display });
  return <ConnectionRowView name={display} mark={isGog ? "Gmail" : display} state={error ? "error" : phase} kind={kind} statusText={statusText}
    detail={kind === "ready" && account?.connected ? account.email : undefined} checkedText={kind === "ready" && checkedAt ? t("status.checked", { when: formatRelativeTime(checkedAt) }) : undefined}
    primary={primary} menu={menu} menuBusy={busy} error={error}>
    {!isDesktop && server.oauth && ["authorizing", "waiting"].includes(phase) && <OAuthLinkMotion title={display} />}
    {route.mode === "blocked" && needsAuth && <div className="mcp-login-note"><p role="status">{route.note}</p></div>}
    {detailsOpen && <div className="mcp-service-details">
      <p className="mcp-service-source">{source}</p>
      {account?.connected && <p className="mcp-service-source">{t("connections.details.account", { email: account.email })} · {account.scope === "read-send" ? t("local.google.granted_read_send") : t("local.google.granted_readonly")}</p>}
      {preset?.googleAPIs && <p className="mcp-service-source">{t("connections.preview_note")}</p>}
      {hasTools && tiers && <div className="mcp-service-tools"><div className="mcp-tools-title"><strong>{t("tools.title", { count: tiers.tools.length })}</strong></div><ToolTierPanel server={server.name} tiers={tiers} onChange={setTiers} busy={busy} /></div>}
    </div>}
    {kind === "error" && preset?.googleAPIs && <div className="mcp-service-setup"><Disclosure title={t("google.help")} defaultOpen><GoogleSetupLinks preset={preset} docs /></Disclosure></div>}
    {vmSession && <VMOAuthDialog session={vmSession} serverName={server.name} title={display} onFinish={(status, detail) => { setVMSession(null); if (status === "complete") { void refresh(); void check(); } else { setPhase("auth"); setError(detail || ""); } }} />}
    {confirm && <div className="mcp-confirm"><span>{confirmText}</span><button className="mcp-button" disabled={busy} onClick={() => setConfirm(null)}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={busy} onClick={() => void confirmAction()}>{confirm === "remove" ? t("mcp.confirm.remove_submit") : t("mcp.confirm.disconnect_submit")}</button></div>}
  </ConnectionRowView>;
}
