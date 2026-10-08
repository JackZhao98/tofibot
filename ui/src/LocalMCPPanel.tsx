import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import { Trans, useTranslation } from "./i18n";
import "./local-mcp.css";

type Plugin = { id: string; state: "sleeping" | "starting" | "ready"; tool_count: number };
type RunnerStatus = { available: boolean; reason?: string; plugins?: Plugin[] };
type GoogleStatus = { connected: boolean; email: string; scope?: "readonly" | "read-send" };
type PackageKind = "npm" | "pypi";

const pluginStateKeys = { ready: "local.state.ready", starting: "local.state.starting", sleeping: "local.state.sleeping" } as const;
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error);

export function LocalMCPPanel({ onChanged, attachedIDs, callbackOrigin }: { onChanged: () => Promise<void>; attachedIDs: string[]; callbackOrigin?: string }) {
 const { t } = useTranslation("extensions");
 const callbackURL = `${callbackOrigin ?? window.location.origin}/api/extensions/local-mcp/gog/oauth/callback`;
 const oauthReady = callbackOrigin?.startsWith("https://") && callbackOrigin === window.location.origin;
 const [runner, setRunner] = useState<RunnerStatus | null>(null);
 const [google, setGoogle] = useState<GoogleStatus | null>(null);
 const [busy, setBusy] = useState("");
 const [error, setError] = useState("");
 const [notice, setNotice] = useState("");
 const [authURL, setAuthURL] = useState("");
 const [email, setEmail] = useState("");
 const [googleScope, setGoogleScope] = useState<"readonly" | "read-send">("readonly");
 const [credentials, setCredentials] = useState("");
 const [kind, setKind] = useState<PackageKind>("npm");
 const [id, setID] = useState("");
 const [packageName, setPackageName] = useState("");
 const [version, setVersion] = useState("");
 const [binary, setBinary] = useState("");
 const [args, setArgs] = useState("");
 const [environment, setEnvironment] = useState("");
 const [confirmRemove, setConfirmRemove] = useState("");
 const [confirmDisconnect, setConfirmDisconnect] = useState(false);
 const refresh = useCallback(async () => {
  try {
   const status = await request<RunnerStatus>("/api/extensions/local-mcp");
   setRunner(status);
   if (status.plugins?.some(plugin => plugin.id === "gog")) {
    try { const account = await request<GoogleStatus>("/api/extensions/local-mcp/gog/oauth/status"); setGoogle(account); }
    catch { setGoogle(null); }
   } else setGoogle(null);
  } catch (caught) { setRunner({ available: false, reason: errorText(caught) }); }
 }, []);
 useEffect(() => { void refresh(); const focus = () => { if (!document.hidden) void refresh(); }; window.addEventListener("focus", focus); return () => window.removeEventListener("focus", focus); }, [refresh]);
 if (!runner) return null;
 if (!runner.available) return <section className="local-mcp-panel" aria-label={t("local.title")}><h3>{t("local.unavailable")}</h3><p>{t("local.unavailable_hint")}</p>{runner.reason && <p className="field-note">{runner.reason}</p>}</section>;
 const gog = runner.plugins?.find(plugin => plugin.id === "gog");
 const installed = runner.plugins ?? [];
 async function install(payload: { id: string; kind: string; package?: string; version?: string; binary?: string; args?: string[]; env?: Record<string,string> }) {
  setBusy(`install:${payload.id}`); setError(""); setNotice("");
  try {
   await request("/api/extensions/local-mcp/install", { method: "POST", body: JSON.stringify(payload) });
   await Promise.all([refresh(), onChanged()]);
   setNotice(t("local.notice.installed", { id: payload.id }));
  } catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function connectGoogle() {
  let client: unknown;
  try { client = JSON.parse(credentials); } catch { setError(t("local.google.invalid_json")); return; }
  // Open during the click gesture so browsers do not block the Google popup.
  const popup = window.open("about:blank", "tofi-google-oauth");
  if (popup) popup.document.body.textContent = t("local.google.preparing_popup");
  setBusy("oauth"); setError(""); setNotice(t("local.google.generating_link")); setAuthURL("");
  try {
   const result = await request<{ authorization_url: string }>("/api/extensions/local-mcp/gog/oauth/start", { method: "POST", body: JSON.stringify({ email, scope: googleScope, credentials_json: client }) });
   setCredentials("");
   setAuthURL(result.authorization_url);
   setNotice(t("local.google.finish_in_google"));
   if (popup && !popup.closed) popup.location.replace(result.authorization_url);
  } catch (caught) { popup?.close(); setError(errorText(caught)); setNotice(""); }
  finally { setBusy(""); }
 }
 async function checkGoogle() {
  setBusy("check"); setError(""); setNotice("");
  try { await request("/api/extensions/local-mcp/gog/oauth/check", { method: "POST", body: "{}" }); setNotice(t("local.google.verified")); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function disconnectGoogle() {
  setBusy("disconnect");setError("");
  try { await request("/api/extensions/local-mcp/gog/oauth/disconnect",{method:"POST",body:"{}"});await refresh();setConfirmDisconnect(false);setNotice(t("local.google.disconnected")); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function attach(pluginID: string) {
  setBusy(`attach:${pluginID}`); setError("");
  try { await request(`/api/extensions/local-mcp/${encodeURIComponent(pluginID)}/attach`, { method: "POST", body: "{}" }); await onChanged(); setNotice(t("local.notice.attached", { id: pluginID })); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function remove(pluginID: string) {
  setBusy(`remove:${pluginID}`); setError("");
  try { await request(`/api/extensions/local-mcp/${encodeURIComponent(pluginID)}`, { method: "DELETE" }); await Promise.all([refresh(), onChanged()]); setNotice(t("local.notice.removed", { id: pluginID })); setConfirmRemove(""); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 const packagePreview = kind === "npm"
  ? `npm install --prefix <${id || t("local.custom.plugin_id")}> --ignore-scripts --no-audit --no-fund -- ${packageName || t("local.custom.package")}@${version || t("local.custom.version")}`
  : `python3 -m venv <${id || t("local.custom.plugin_id")}> && pip install ${packageName || t("local.custom.package")}==${version || t("local.custom.version")}`;
 const environmentEntries = environment.split("\n").map(line => line.trim()).filter(Boolean);
 const environmentValid = environmentEntries.every(line => /^[A-Za-z_][A-Za-z0-9_]*=/.test(line));
 return <section className="local-mcp-panel" aria-label={t("local.title")}>
  <div className="local-mcp-heading"><div><h3>{t("local.title")}</h3><p>{t("local.intro")}</p></div><span className="local-mcp-count">{t("local.plugin_count", { count: installed.length })}</span></div>
  <div className="local-mcp-card">
   <div className="local-mcp-card-title"><strong>{t("local.gog.title")}</strong><span>{gog ? t(pluginStateKeys[gog.state]) : t("local.state.not_installed")}</span></div>
   <p>{t("local.gog.intro")}</p>
   {!gog ? <><code>gog mcp --allow-tool gmail</code><button className="mcp-button" disabled={!!busy} onClick={() => void install({ id: "gog", kind: "builtin_gog" })}>{busy === "install:gog" ? t("local.gog.installing") : t("local.gog.install")}</button></> : <>
    <div className="local-mcp-google-state">{google?.connected ? t("local.google.connected_as", { email: google.email }) : t("local.google.waiting")}</div>
    {!google?.connected && <div className="local-mcp-auth-form">
     <p><Trans t={t} i18nKey="local.google.client_setup" components={{ strong: <strong /> }} /><a href="https://console.cloud.google.com/auth/clients" target="_blank" rel="noopener noreferrer">{t("local.google.open_console")}</a></p>
     <code>{callbackURL}</code>
     <label>{t("local.google.email")}<input type="email" value={email} onChange={event => setEmail(event.target.value)} placeholder="you@gmail.com" /></label>
     <label>{t("local.google.scope")}<select value={googleScope} onChange={event => setGoogleScope(event.target.value as "readonly" | "read-send")}><option value="readonly">{t("local.google.scope_readonly")}</option><option value="read-send">{t("local.google.scope_read_send")}</option></select></label>
     <label>{t("local.google.client_json")}<input type="file" accept=".json,application/json" onChange={event => { const file = event.target.files?.[0]; if (file) void file.text().then(setCredentials).catch(caught => setError(errorText(caught))); }} /></label>
     <button className="mcp-button" disabled={!!busy || !email || !credentials || !oauthReady} onClick={() => void connectGoogle()}>{busy === "oauth" ? t("local.google.preparing") : t("local.google.connect")}</button>
     {!oauthReady && <small>{t("local.google.needs_https")}</small>}
     {authURL && <a href={authURL} target="_blank" rel="noopener noreferrer">{t("local.google.continue")}</a>}
    </div>}
    {google?.connected && <><p>{google.scope === "read-send" ? t("local.google.granted_read_send") : t("local.google.granted_readonly")}</p><button className="mcp-button" disabled={!!busy} onClick={() => void checkGoogle()}>{busy === "check" ? t("local.google.verifying") : t("local.google.verify")}</button></>}
    {google?.connected && (confirmDisconnect ? <div className="local-mcp-confirm"><span>{t("local.google.disconnect_confirm")}</span><button className="mcp-button" onClick={() => setConfirmDisconnect(false)}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={!!busy} onClick={() => void disconnectGoogle()}>{t("local.google.disconnect_submit")}</button></div> : <button className="mcp-button" disabled={!!busy} onClick={() => setConfirmDisconnect(true)}>{t("local.google.disconnect")}</button>)}
    {!attachedIDs.includes("gog") && <button className="mcp-button" disabled={!!busy} onClick={() => void attach("gog")}>{t("local.attach")}</button>}
   </>}
  </div>
  {installed.length>0 && <div className="local-mcp-installed">{installed.map(plugin => <div key={plugin.id} className="local-mcp-installed-row"><span><strong>{plugin.id}</strong> · {t(pluginStateKeys[plugin.state])}</span>{!attachedIDs.includes(plugin.id) && <button className="mcp-button" disabled={!!busy} onClick={() => void attach(plugin.id)}>{t("local.attach")}</button>}{confirmRemove === plugin.id ? <><span>{t("local.remove_confirm")}</span><button className="mcp-button" onClick={() => setConfirmRemove("")}>{t("action.cancel")}</button><button className="mcp-button mcp-danger" disabled={!!busy} onClick={() => void remove(plugin.id)}>{t("local.remove_submit")}</button></> : <button className="mcp-button" disabled={!!busy} onClick={() => setConfirmRemove(plugin.id)}>{t("local.remove")}</button>}</div>)}</div>}
  <details className="local-mcp-custom"><summary>{t("local.custom.title")}</summary>
   <p>{t("local.custom.intro")}</p>
   <div className="local-mcp-fields"><label>{t("local.custom.runtime")}<select value={kind} onChange={event => setKind(event.target.value as PackageKind)}><option value="npm">Node / npm</option><option value="pypi">Python / PyPI</option></select></label><label>{t("local.custom.plugin_id")}<input value={id} onChange={event => setID(event.target.value)} placeholder="my-plugin" /></label><label>{t("local.custom.package")}<input value={packageName} onChange={event => setPackageName(event.target.value)} /></label><label>{t("local.custom.version")}<input value={version} onChange={event => setVersion(event.target.value)} placeholder="1.2.3" /></label><label>{t("local.custom.binary")}<input value={binary} onChange={event => setBinary(event.target.value)} /></label><label>{t("local.custom.args")}<textarea rows={2} value={args} onChange={event => setArgs(event.target.value)} /></label><label>{t("local.custom.env")}<textarea rows={3} value={environment} onChange={event => setEnvironment(event.target.value)} /></label></div>
   <code>{packagePreview}</code>
   <button className="mcp-button" disabled={!!busy || !id || !packageName || !version || !binary || !environmentValid} onClick={() => void install({ id, kind, package: packageName, version, binary, args: args.split("\n").map(item => item.trim()).filter(Boolean), env: Object.fromEntries(environmentEntries.map(line => { const index=line.indexOf("="); return [line.slice(0,index),line.slice(index+1)]; })) })}>{t("local.custom.install")}</button>
  </details>
  {error && <p className="local-mcp-error" role="alert">{error}</p>}{notice && <p className="local-mcp-notice" role="status">{notice}</p>}
 </section>;
}
