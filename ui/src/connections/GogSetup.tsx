import { useEffect, useId, useRef, useState } from "react";
import { request } from "../api";
import { Banner, FilePicker } from "../settings/components";
import { Trans, useTranslation } from "../i18n";
import { message } from "./shared";
import { GOOGLE_SERVICE_API, GOOGLE_SERVICE_NAME, googleEnableURL, parseGoogleClient, type GoogleOverview } from "./google";
import type { GoogleStatus, LocalState } from "./localPlugins";
import { CallbackAddress } from "./RemoteForm";

type Scope = "readonly" | "read-send";

/**
 * Personal Gmail through a Google account the person authorises: install the plugin if it is missing, register the
 * OAuth client, then sign in on Google's page. Copy keys stay under `local.google.*` and `local.gog.*`.
 */
export function GogSetup({ local, google, callbackOrigin, onChanged, onDone, onCancel }: {
  local: LocalState; google: GoogleOverview | null; callbackOrigin?: string; onChanged: () => Promise<void>; onDone: () => void; onCancel: () => void;
}) {
  const { t } = useTranslation("extensions");
  const callbackURL = `${callbackOrigin ?? window.location.origin}/api/extensions/local-mcp/gog/oauth/callback`;
  const oauthReady = Boolean(callbackOrigin?.startsWith("https://")) && callbackOrigin === window.location.origin;
  const installed = local.plugins.some(plugin => plugin.id === "gog");
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [email, setEmail] = useState("");
  const [scope, setScope] = useState<Scope>("readonly");
  const [credentials, setCredentials] = useState("");
  const [credentialsName, setCredentialsName] = useState("");
  const [authURL, setAuthURL] = useState("");
  const [waiting, setWaiting] = useState(false);
  const [replaceClient, setReplaceClient] = useState(false);
  // The workspace already has a Google client: no upload, unless the person chooses to replace it.
  const savedClient = Boolean(google?.client.configured) && !replaceClient;
  const uploaded = credentials ? parseGoogleClient(credentials) : null;
  const project = savedClient ? google!.client.project_id || google!.client.project_number || "" : uploaded?.project ?? "";
  const fileLabel = useId();
  const latest = useRef({ onChanged, onDone });
  latest.current = { onChanged, onDone };

  async function install() {
    setBusy("install"); setError("");
    try { await request("/api/extensions/local-mcp/install", { method: "POST", body: JSON.stringify({ id: "gog", kind: "builtin_gog" }) }); await onChanged(); }
    catch (caught) { setError(message(caught)); }
    finally { setBusy(""); }
  }
  async function authorize() {
    let client: unknown;
    if (!savedClient) { try { client = JSON.parse(credentials); } catch { setError(t("local.google.invalid_json")); return; } }
    // Open during the click gesture so browsers do not block the Google popup.
    const popup = window.open("about:blank", "tofi-google-oauth");
    if (popup) popup.document.body.textContent = t("local.google.preparing_popup");
    setBusy("oauth"); setError(""); setAuthURL("");
    try {
      const result = await request<{ authorization_url: string }>("/api/extensions/local-mcp/gog/oauth/start", { method: "POST", body: JSON.stringify(savedClient ? { email, scope, services: ["gmail"] } : { email, scope, services: ["gmail"], credentials_json: client }) });
      setCredentials(""); setCredentialsName("");
      setAuthURL(result.authorization_url); setWaiting(true);
      if (popup && !popup.closed) popup.location.replace(result.authorization_url);
    } catch (caught) { popup?.close(); setError(message(caught)); }
    finally { setBusy(""); }
  }
  // Finished when the Google account shows up: confirm Gmail answers, then hand the list its new state.
  useEffect(() => {
    if (!waiting) return;
    let live = true;
    const timer = window.setInterval(() => {
      void request<GoogleStatus>("/api/extensions/local-mcp/gog/oauth/status").then(async account => {
        if (!live || !account.connected) return;
        live = false; window.clearInterval(timer);
        try { await request("/api/extensions/local-mcp/gog/oauth/check", { method: "POST", body: "{}" }); } catch (caught) { setError(message(caught)); }
        await latest.current.onChanged(); latest.current.onDone();
      }).catch(() => {});
    }, 2000);
    return () => { live = false; window.clearInterval(timer); };
  }, [waiting]);

  if (!installed) return <div className="connect-body">
    <p className="connect-lead">{t("local.gog.intro")}</p>
    {error && <Banner tone="error" title={error} />}
    <div className="sheet-actions"><button type="button" className="secondary-button" onClick={onCancel}>{t("action.cancel")}</button><button type="button" className="primary-button" disabled={Boolean(busy)} onClick={() => void install()}>{busy === "install" ? t("local.gog.installing") : t("local.gog.install")}</button></div>
  </div>;

  if (waiting) return <div className="connect-body" role="status">
    <p className="connect-lead">{t("local.google.finish_in_google")}</p>
    {authURL && <a className="connect-link" href={authURL} target="_blank" rel="noopener noreferrer">{t("local.google.continue")}</a>}
    {error && <Banner tone="error" title={error} />}
    <div className="sheet-actions"><button type="button" className="secondary-button" onClick={onCancel}>{t("action.close")}</button></div>
  </div>;

  return <div className="connect-body">
    <p className="connect-lead">{t("local.gog.intro")}</p>
    <ol className="connect-steps">
      {!savedClient && <li>
          <p><Trans t={t} i18nKey="local.google.client_setup" components={{ strong: <strong /> }} /> <a href="https://console.cloud.google.com/auth/clients" target="_blank" rel="noopener noreferrer">{t("local.google.open_console")}</a></p>
          <CallbackAddress label={t("callback.web")} value={callbackURL} />
        </li>}
      <li>
        {savedClient ? <p>{t("connections.google.using_client", { project })} <button type="button" className="text-button" onClick={() => setReplaceClient(true)}>{t("connections.google.replace_client")}</button></p> : <>
        <div className="connect-field"><span id={fileLabel}>{t("local.google.client_json")}</span><FilePicker accept=".json,application/json" labelledBy={fileLabel} label={t("local.google.choose_file")} fileName={credentialsName} dropHint={t("local.google.no_file")} disabled={Boolean(busy)} onFile={file => { setError(""); void file.text().then(text => { setCredentials(text); setCredentialsName(file.name); }).catch(caught => setError(message(caught))); }} /></div>
        {uploaded && <p>{t("connections.google.project_found", { project: uploaded.project || uploaded.clientId })}</p>}</>}
        {project && <a className="connect-link" href={googleEnableURL(GOOGLE_SERVICE_API.gmail, project)} target="_blank" rel="noopener noreferrer">{t("connections.action.enable_api", { name: GOOGLE_SERVICE_NAME.gmail })}</a>}
      </li>
      <li>
        <label className="connect-field">{t("local.google.email")}<input type="email" value={email} onChange={event => setEmail(event.target.value)} placeholder="you@gmail.com" /></label>
        <label className="connect-field">{t("local.google.scope")}<select value={scope} onChange={event => setScope(event.target.value as Scope)}><option value="readonly">{t("local.google.scope_readonly")}</option><option value="read-send">{t("local.google.scope_read_send")}</option></select></label>
      </li>
    </ol>
    {!oauthReady && <p className="field-note">{t("local.google.needs_https")}</p>}
    {error && <Banner tone="error" title={error} />}
    <div className="sheet-actions"><button type="button" className="secondary-button" onClick={onCancel}>{t("action.cancel")}</button><button type="button" className="primary-button" disabled={Boolean(busy) || !email || (!savedClient && !credentials) || !oauthReady} onClick={() => void authorize()}>{busy === "oauth" ? t("local.google.preparing") : t("local.google.connect")}</button></div>
  </div>;
}
