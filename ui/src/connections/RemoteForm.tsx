import { useState } from "react";
import { Disclosure } from "../InteractionSystem";
import { TofiIcon } from "../icons";
import { Banner } from "../settings/components";
import { googleAPIEnableURL, googleAudienceURL, type IntegrationPreset } from "../integrationCatalog";
import type { OAuthOptions, OAuthRoute } from "../mcpOAuthRoute";
import { mcpTokenHeaders } from "../mcpTokenHeaders";
import { useTranslation } from "../i18n";
import { endpoint, message, transportLabels, words, type MCP, type MCPTransport } from "./shared";
import type { GoogleOverview } from "./google";
import { Select } from "../Select";

/** Google Cloud links for one Google connector. Shown while connecting, and after a failure; never on a healthy row. */
export function GoogleSetupLinks({ preset, docs = false }: { preset: IntegrationPreset; docs?: boolean }) {
  const { t } = useTranslation("extensions");
  if (!preset.googleAPIs) return null;
  return <section className="mcp-google-setup" aria-label={t("google.links_label", { name: preset.name })}>
    <p>{t("google.project_hint")}</p>
    <p>{t("google.preview_hint")}</p>
    <p>{t("google.agentic_hint")}</p>
    <nav aria-label={t("google.nav_label", { name: preset.name })}>
      <a href={googleAPIEnableURL(preset.googleAPIs.api)} target="_blank" rel="noopener noreferrer">{t("google.enable_api")}</a>
      <a href={googleAPIEnableURL(preset.googleAPIs.mcp)} target="_blank" rel="noopener noreferrer">{t("google.enable_mcp")}</a>
      <a href={googleAudienceURL} target="_blank" rel="noopener noreferrer">{t("google.test_users")}</a>
      {docs && <a href={preset.docsURL} target="_blank" rel="noopener noreferrer">{t("google.docs")}</a>}
    </nav>
  </section>;
}

function OAuthLoginHelp({ route }: { route: OAuthRoute }) {
  const { t } = useTranslation("extensions");
  return <Disclosure title={t("oauth.help_title")}><div className="mcp-login-help">
    <p>{route.note}</p>
    <p>{t("oauth.help_body")}</p>
  </div></Disclosure>;
}

/** A URL the person must register elsewhere, with a copy button. */
export function CallbackAddress({ label, value }: { label: string; value: string }) {
  const { t } = useTranslation("extensions");
  const [notice, setNotice] = useState("");
  async function copy() { try { await navigator.clipboard.writeText(value); setNotice(t("callback.copied")); } catch { setNotice(t("callback.copy_failed")); } }
  return <div className="integration-callback"><span>{label}</span><code>{value}</code><button className="secondary-button" type="button" onClick={() => void copy()}>{t("callback.copy")}</button>{notice && <small role="status">{notice}</small>}</div>;
}

/** Remote MCP setup (a catalog preset, a custom URL, or editing a saved connection), as the body of the connect dialog. */
export function RemoteForm({ initial, preset, options, route, busy, onSave, onCancel, google }: {
  google: GoogleOverview | null; initial: MCP | null; preset?: IntegrationPreset; options: OAuthOptions | null; route: OAuthRoute; busy: boolean;
  onSave: (v: MCP) => Promise<void>; onCancel: () => void;
}) {
  const { t } = useTranslation("extensions");
  const [name, setName] = useState(initial?.name ?? preset?.id ?? "");
  const [url, setURL] = useState(initial?.url ?? preset?.url ?? "");
  const [transport, setTransport] = useState<MCPTransport>(initial?.transport ?? "streamable_http");
  const [headers, setHeaders] = useState(JSON.stringify(initial?.headers ?? {}, null, 2));
  const [token, setToken] = useState("");
  const [allow, setAllow] = useState(initial?.tool_allowlist?.join(", ") ?? "");
  const [deny, setDeny] = useState(initial?.tool_denylist?.join(", ") ?? "");
  const [oauth, setOAuth] = useState(initial ? Boolean(initial.oauth) : preset?.auth === "oauth");
  const [clientID, setClientID] = useState(initial?.oauth?.client_id ?? "");
  const [secret, setSecret] = useState(initial?.oauth?.client_secret ?? "");
  const [scopes, setScopes] = useState(initial?.oauth?.scopes?.join(", ") ?? preset?.scopes?.join(", ") ?? "");
  const [metadata, setMetadata] = useState(initial?.oauth?.auth_server_metadata_url ?? preset?.metadataURL ?? "");
  const [error, setError] = useState("");
  const [replaceClient, setReplaceClient] = useState(false);
  // A Google service after the first signs in with the workspace's saved Google client: no ID or secret to type again.
  const savedClient = Boolean(google?.client.configured) && preset?.category === "google" && !initial && !replaceClient;
  return <form className="connect-form" aria-busy={busy} onSubmit={async e => {
    e.preventDefault(); if (busy) return;
    try {
      if (!/^[a-zA-Z0-9_.-]{1,64}$/.test(name) || name === "." || name === "..") throw new Error(t("form.name_invalid"));
      const parsed = mcpTokenHeaders(headers, token, preset);
      setError("");
      await onSave({ name, url, transport, headers: parsed, tool_allowlist: words(allow), tool_denylist: words(deny), ...(oauth ? { oauth: { client_id: savedClient ? "" : clientID, client_secret: savedClient ? "" : secret, scopes: words(scopes), auth_server_metadata_url: metadata, ...(initial?.oauth?.redirect_policy ?? preset?.oauthRedirect ? { redirect_policy: initial?.oauth?.redirect_policy ?? preset?.oauthRedirect } : {}) } } : {}), ...(savedClient ? { google_client: true } : {}) });
    } catch (cause) { setError(message(cause)); }
  }}>
    <fieldset className="form-fields" disabled={busy}>
      {preset && <Disclosure title={t("form.guide")} defaultOpen={preset.category === "google"}><div className="integration-guide"><div><strong>{preset.category === "google" ? t("form.google_prepare") : t("form.guide_heading")}</strong><a href={preset.docsURL} target="_blank" rel="noopener noreferrer">{preset.upstream === "community" ? t("docs.community") : t("docs.vendor")} <TofiIcon name="external-link" size={16} style={{ verticalAlign: "middle" }} /></a></div><ol>{preset.setup.map(step => <li key={step}>{step}</li>)}</ol>{preset.note && <p>{preset.note}</p>}<GoogleSetupLinks preset={preset} /></div></Disclosure>}
      {oauth && <OAuthLoginHelp route={route} />}
      {!preset && <label>{t("form.name")}<input required maxLength={64} disabled={Boolean(initial)} value={name} onChange={e => setName(e.target.value)} placeholder="my-service" /></label>}
      {!preset && <label>{t("form.url")}<input required type="url" value={url} onChange={e => { setURL(e.target.value); setToken(""); }} placeholder="https://example.com/mcp" /></label>}
      {preset?.auth === "token" && <label>{t("form.token")}<input type="password" autoComplete="off" value={token} onChange={e => setToken(e.target.value)} placeholder={initial ? t("form.token_keep") : t("form.token_paste")} required={!initial} /></label>}
      {!preset && <label className="extension-check"><input type="checkbox" checked={oauth} onChange={e => setOAuth(e.target.checked)} disabled={Boolean(initial?.oauth)} />{t("auth.oauth")}</label>}
      {oauth && !preset?.dcr && savedClient && <p className="connect-lead">{t("connections.google.using_client", { project: google!.client.project_id || google!.client.project_number })} <button type="button" className="text-button" onClick={() => setReplaceClient(true)}>{t("connections.google.replace_client")}</button></p>}
      {oauth && !preset?.dcr && !savedClient && <>
        <label>{preset?.category !== "google" ? t("form.client_id_optional") : t("form.client_id")}<input required={preset?.category === "google"} value={clientID} onChange={e => setClientID(e.target.value)} autoComplete="off" /></label>
        <label>{t("form.client_secret")}<input required={preset?.category === "google"} type="password" autoComplete="off" value={secret} onChange={e => setSecret(e.target.value)} placeholder={t("form.client_secret_hint")} /></label>
        {options?.web_callback_origin && <CallbackAddress label={t("callback.web")} value={`${options.web_callback_origin}${endpoint(name || t("form.name"))}/oauth/callback`} />}
        {options?.desktop_redirect_uri && <CallbackAddress label={t("callback.desktop")} value={options.desktop_redirect_uri} />}
        {preset?.category === "google" && <p className="field-note">{t("google.redirect_hint")}</p>}
      </>}
      <Disclosure title={t("form.advanced")}>
        {preset && <label>{t("form.name")}<input required maxLength={64} disabled={Boolean(initial)} value={name} onChange={e => setName(e.target.value)} placeholder="my-service" /></label>}
        {preset && <label>{t("form.url")}<input required type="url" value={url} onChange={e => { setURL(e.target.value); setToken(""); }} /></label>}
        <label>{t("form.transport")}<Select value={transport} disabled={busy} onValueChange={value => setTransport(value as MCPTransport)}><option value="streamable_http">{transportLabels.streamable_http}</option><option value="sse">{transportLabels.sse}</option></Select></label>
        <label>{t("form.headers")}<textarea rows={3} value={headers} onChange={e => setHeaders(e.target.value)} spellCheck={false} /></label>{initial && <p className="field-note">{t("form.headers_note")}</p>}
        {oauth && <><label>{t("form.scopes")}<input value={scopes} onChange={e => setScopes(e.target.value)} placeholder={t("form.comma_separated")} /></label><label>{t("form.metadata_url")}<input type="url" value={metadata} onChange={e => setMetadata(e.target.value)} /></label></>}
        <label>{t("form.allowlist")}<input value={allow} onChange={e => setAllow(e.target.value)} placeholder={t("form.allowlist_hint")} /></label><label>{t("form.denylist")}<input value={deny} onChange={e => setDeny(e.target.value)} placeholder={t("form.comma_separated")} /></label>
      </Disclosure>
      {error && <Banner tone="error" title={error} />}
      <div className="sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("action.cancel")}</button><button type="submit" className="primary-button" disabled={busy}>{busy ? t("form.saving") : initial ? t("form.save") : t("form.add")}</button></div>
    </fieldset>
  </form>;
}
