import { useEffect, useRef, useState, type ReactNode } from "react";
import { request } from "./api";
import { isDesktop } from "./desktop";
import { ServiceMark } from "./ServiceMark";
import { Disclosure } from "./InteractionSystem";
import { StatusBadge } from "./settings/components";
import { TofiIcon } from "./icons";
import { googleAPIEnableURL, googleAudienceURL, getIntegrationPreset, type IntegrationPreset } from "./integrationCatalog";
import { mcpOAuthRoute, type OAuthOptions } from "./mcpOAuthRoute";
import { mcpTokenHeaders } from "./mcpTokenHeaders";
import { useTranslation } from "./i18n";
import { VMOAuthDialog, type VMOAuthSession } from "./VMOAuthDialog";

type Server = { name: string; url: string; oauth?: { connected?: boolean } };
type Badge = "sign_in" | "official" | "token" | "none" | "setup";

/** Only services with a working connect path in the catalog: Slack and Discord are held back there, so they are not offered. */
export const onboardingTiles = [
  { id: "github", badge: "token" },
  { id: "notion", badge: "sign_in" },
  { id: "linear", badge: "token" },
  { id: "robinhood", badge: "official" },
  { id: "context7", badge: "token" },
  { id: "microsoft-learn", badge: "none" },
] as const satisfies readonly { id: string; badge: Badge }[];
type TileId = (typeof onboardingTiles)[number]["id"];
const purposeKeys = {
  github: "onboarding.purpose.github", notion: "onboarding.purpose.notion", linear: "onboarding.purpose.linear", robinhood: "onboarding.purpose.robinhood",
  context7: "onboarding.purpose.context7", "microsoft-learn": "onboarding.purpose.microsoft_learn",
} as const satisfies Record<TileId, string>;
const badgeKeys = { sign_in: "onboarding.badge.sign_in", official: "onboarding.badge.official", token: "onboarding.badge.token", none: "onboarding.badge.none", setup: "onboarding.badge.setup" } as const satisfies Record<Badge, string>;

/** Official pages where a person creates the token a service asks for. */
const tokenPages: Record<string, string> = {
  github: "https://github.com/settings/personal-access-tokens/new",
  linear: "https://linear.app/settings/account/security",
  context7: "https://context7.com/dashboard",
};
const howtoKeys: Record<string, "onboarding.howto.github" | "onboarding.howto.linear" | "onboarding.howto.context7" | "onboarding.howto.notion" | "onboarding.howto.robinhood" | "onboarding.howto.microsoft_learn"> = {
  github: "onboarding.howto.github", linear: "onboarding.howto.linear", context7: "onboarding.howto.context7",
  notion: "onboarding.howto.notion", robinhood: "onboarding.howto.robinhood", "microsoft-learn": "onboarding.howto.microsoft_learn",
};

const endpoint = (name: string) => `/api/extensions/mcp/${encodeURIComponent(name)}`;
const post = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });

/** What the sheet header says about this step: nothing yet, which service is up, or that the walk is over. */
export type ServicesStatus = { kind: "walk"; index: number; total: number; name: string } | { kind: "summary" } | null;
type Result = "done" | "skipped";

export function OnboardingServices({ titleId, botId, onQueue, onFinish }: { titleId: string; botId?: string; onQueue: (status: ServicesStatus) => void; onFinish: () => void }) {
  const { t } = useTranslation(["chat", "extensions"]);
  const [servers, setServers] = useState<Server[] | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [queue, setQueue] = useState<string[] | null>(null);
  const [index, setIndex] = useState(0);
  const [results, setResults] = useState<Record<string, Result>>({});
  const [summary, setSummary] = useState(false);
  const refresh = async () => {
    try { const value = await request<{ servers: Server[] | null }>("/api/extensions/mcp"); setServers(value.servers ?? []); return value.servers ?? []; }
    catch { setServers(current => current ?? []); return []; }
  };
  useEffect(() => { void refresh(); }, []);
  useEffect(() => {
    if (summary) onQueue({ kind: "summary" });
    else if (queue) onQueue({ kind: "walk", index: Math.min(index + 1, queue.length), total: queue.length, name: getIntegrationPreset(queue[Math.min(index, queue.length - 1)])?.name ?? "" });
    else onQueue(null);
  }, [queue, index, summary, onQueue]);
  const added = (id: string) => {
    const preset = getIntegrationPreset(id);
    const server = preset && servers?.find(item => item.url === preset.url);
    return Boolean(server && (!server.oauth || server.oauth.connected));
  };

  if (summary && queue) {
    return (
      <>
        <div className="onb-body">
          <h2 id={titleId} className="onb-title onb-title-sm">{t("onboarding.done_title")}</h2>
          <p className="onb-lede onb-lede-left">{t(queue.some(id => results[id] === "done") ? "onboarding.done_body" : "onboarding.done_body_none")}</p>
          <ul className="onb-summary" aria-label={t("onboarding.queue_label")}>
            {queue.map(id => {
              const preset = getIntegrationPreset(id)!;
              const ok = results[id] === "done";
              return <li key={id} data-service-result={ok ? "done" : "skipped"}><ServiceMark name={preset.name} /><strong>{preset.name}</strong>{ok ? <StatusBadge state="ok">{t("onboarding.connected")}</StatusBadge> : <StatusBadge state="asleep">{t("onboarding.skipped")}</StatusBadge>}</li>;
            })}
          </ul>
          <p className="onb-note"><TofiIcon name="info" size={15} /> {t("onboarding.services_hint")}</p>
        </div>
        <footer className="onb-foot">
          <span />
          <button type="button" className="primary-button onb-primary" data-autofocus onClick={onFinish}>{t("onboarding.continue")}</button>
        </footer>
      </>
    );
  }

  if (queue) {
    const id = queue[index];
    const preset = getIntegrationPreset(id)!;
    const last = index + 1 >= queue.length;
    const next = (result: Result) => {
      setResults(current => ({ ...current, [id]: result }));
      if (last) setSummary(true); else setIndex(index + 1);
    };
    const lead = (
      <>
        <h2 id={titleId} className="onb-title onb-title-sm">{t("onboarding.service_title", { name: preset.name })}</h2>
        <p className="onb-lede onb-lede-left">{t(purposeKeys[id as TileId])}</p>
        <ol className="onb-queue" aria-label={t("onboarding.queue_label")}>
          {queue.map((item, position) => {
            const p = getIntegrationPreset(item)!;
            const state = results[item] === "skipped" ? "skipped" : position < index ? "done" : position === index ? "now" : "next";
            return <li key={item} data-state={state}><ServiceMark name={p.name} /><span>{p.name} · {t(`onboarding.queue_${state}`)}</span>{state === "done" && <TofiIcon name="check" size={13} />}</li>;
          })}
        </ol>
      </>
    );
    return <ServiceConnect key={preset.id} lead={lead} preset={preset} last={last} existing={servers?.find(item => item.url === preset.url)} botId={botId} refreshServers={refresh} onResult={next} />;
  }

  const toggle = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : [...current, id]);
  const anyConnected = onboardingTiles.some(tile => added(tile.id));
  const start = () => { setQueue(onboardingTiles.map(tile => tile.id).filter(id => selected.includes(id))); setIndex(0); };
  const primary = selected.length > 0
    ? { label: t("onboarding.connect_selected", { count: selected.length }), enabled: true, run: start }
    : anyConnected ? { label: t("onboarding.continue"), enabled: true, run: onFinish }
    : { label: t("onboarding.connect_none"), enabled: false, run: start };
  return (
    <>
      <div className="onb-body">
        <h2 id={titleId} className="onb-title onb-title-sm">{t("onboarding.services_title")}</h2>
        <p className="onb-lede onb-lede-left">{t("onboarding.services_body")}</p>
        <div className="onb-tiles" role="group" aria-label={t("onboarding.services_title")}>
          {onboardingTiles.map(tile => {
            const preset = getIntegrationPreset(tile.id)!;
            const done = added(tile.id);
            const on = selected.includes(tile.id);
            const body = (
              <>
                {!done && <span className="onb-tile-check" aria-hidden="true">{on && <TofiIcon name="check" size={12} />}</span>}
                <ServiceMark name={preset.name} />
                <strong>{preset.name}</strong>
                <small>{t(purposeKeys[tile.id])}</small>
                {done ? <StatusBadge state="ok">{t("onboarding.connected")}</StatusBadge> : <code>{t(badgeKeys[tile.badge])}</code>}
              </>
            );
            return done
              ? <div key={tile.id} className="onb-tile" data-integration-id={tile.id} data-connected="true" role="group" aria-label={`${preset.name}, ${t("onboarding.connected")}`}>{body}</div>
              : <button type="button" key={tile.id} className="onb-tile" data-integration-id={tile.id} aria-pressed={on} onClick={() => toggle(tile.id)}>{body}</button>;
          })}
        </div>
        <p className="onb-note"><TofiIcon name="info" size={15} /> {t("onboarding.services_hint")}</p>
      </div>
      <footer className="onb-foot onb-foot-services">
        {selected.length > 0 || !anyConnected ? <button type="button" className="onb-later" data-autofocus={selected.length === 0 || undefined} onClick={onFinish}>{t("onboarding.later")}</button> : <span />}
        <button type="button" className="primary-button onb-primary" disabled={!primary.enabled} data-autofocus={(selected.length === 0 && anyConnected) || undefined} onClick={primary.run}>{primary.label}</button>
      </footer>
    </>
  );
}

type Phase = "connect" | "authorizing" | "testing" | "done" | "error";

/** The standard Connect, Test, Done walk for one service, driven by the same endpoints as Settings > Connections. */
function ServiceConnect({ lead, preset, last, existing, botId, refreshServers, onResult }: { lead: ReactNode; preset: IntegrationPreset; last: boolean; existing?: Server; botId?: string; refreshServers: () => Promise<Server[]>; onResult: (result: Result) => void }) {
  const { t } = useTranslation(["chat", "extensions"]);
  const [phase, setPhase] = useState<Phase>("connect");
  const [token, setToken] = useState("");
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [tools, setTools] = useState(0);
  const [error, setError] = useState("");
  const [options, setOptions] = useState<OAuthOptions | null>(null);
  const [vm, setVM] = useState<VMOAuthSession | null>(null);
  const op = useRef(0);
  const alive = useRef(true);
  const popup = useRef<Window | null>(null);
  useEffect(() => { alive.current = true; return () => { alive.current = false; op.current++; }; }, []);
  useEffect(() => { if (preset.auth === "oauth") request<OAuthOptions>("/api/extensions/oauth-options").then(setOptions).catch(() => setOptions({ vm_available: false, web_callback_origin: "", desktop_redirect_uri: "" })); }, [preset.auth]);
  const route = mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options);
  const google = Boolean(preset.googleAPIs);
  const name = preset.id;
  const fail = (cause: unknown, id: number) => { if (alive.current && op.current === id) { setPhase("error"); setError(cause instanceof Error && cause.message ? cause.message : t("onboarding.connect_failed", { name: preset.name })); } };

  async function save(id: number) {
    const headers = preset.auth === "token" ? mcpTokenHeaders("{}", token, preset) : {};
    const oauth = preset.auth === "oauth" ? { oauth: { client_id: preset.dcr ? "" : clientId.trim(), client_secret: preset.dcr ? "" : clientSecret.trim(), scopes: preset.scopes ?? [], auth_server_metadata_url: preset.metadataURL ?? "" } } : {};
    await request("/api/extensions/mcp", { method: existing ? "PUT" : "POST", body: JSON.stringify({ name, url: preset.url, transport: "streamable_http", headers, tool_allowlist: [], tool_denylist: [], ...oauth }) });
    if (op.current !== id) throw new Error("cancelled");
  }
  async function test(id: number) {
    setPhase("testing"); setError("");
    try {
      const result = await request<{ ok: boolean; tool_count?: number; auth_required?: boolean; diagnostics?: { message: string }[] }>(endpoint(name) + "/test", post({}));
      if (!alive.current || op.current !== id) return;
      if (result.ok) { setTools(result.tool_count ?? 0); setPhase("done"); void refreshServers(); }
      else { setPhase("error"); setError(result.auth_required ? t("onboarding.connect_not_signed_in", { name: preset.name }) : result.diagnostics?.map(item => item.message).join("; ") || t("onboarding.connect_failed", { name: preset.name })); }
    } catch (cause) { fail(cause, id); }
  }
  async function authorize(id: number) {
    setPhase("authorizing");
    if (route.mode === "blocked") { setPhase("error"); setError(route.note); return; }
    if (route.mode === "desktop") {
      try { await window.tofiDesktop!.authorizeMCP!(name); if (alive.current && op.current === id) await test(id); } catch (cause) { fail(cause, id); }
      return;
    }
    if (route.mode === "vm") {
      try {
        if (!botId) throw new Error(t("extensions:error.needs_bot"));
        const session = await request<VMOAuthSession>(endpoint(name) + "/oauth/vm/start", post({ bot_id: botId }));
        if (!alive.current || op.current !== id) return;
        setVM(session);
      } catch (cause) { fail(cause, id); }
      return;
    }
    // Web: the window is opened inside the click (the caller), then pointed at the provider.
    try {
      const result = await request<{ authorization_url: string }>(endpoint(name) + "/oauth/start", post({}));
      if (!alive.current || op.current !== id) { popup.current?.close(); return; }
      const url = new URL(result.authorization_url);
      if (!["https:", "http:"].includes(url.protocol)) throw new Error(t("extensions:error.invalid_auth_url"));
      if (popup.current && !popup.current.closed) popup.current.location.replace(url.href);
      else popup.current = window.open(url.href, "_blank");
      const until = Date.now() + 10 * 60 * 1000;
      let closedPolls = 0;
      while (Date.now() < until) {
        await new Promise(resolve => window.setTimeout(resolve, 2000));
        if (!alive.current || op.current !== id) return;
        const list = await refreshServers();
        if (list.find(item => item.name === name)?.oauth?.connected) { await test(id); return; }
        closedPolls = popup.current?.closed ? closedPolls + 1 : 0;
        if (closedPolls >= 3) break;
      }
      throw new Error(t("extensions:error.auth_incomplete"));
    } catch (cause) { popup.current?.close(); fail(cause, id); }
  }
  async function connect() {
    const id = ++op.current;
    setError("");
    // Open the sign-in window now, while the click still counts as a user gesture.
    if (preset.auth === "oauth" && route.mode === "web") { popup.current = window.open(endpoint(name) + "/oauth/pending", "_blank"); if (popup.current) popup.current.opener = null; }
    setPhase(preset.auth === "oauth" ? "authorizing" : "testing");
    try { await save(id); } catch (cause) { popup.current?.close(); if ((cause as Error).message !== "cancelled") fail(cause, id); return; }
    if (preset.auth === "oauth") await authorize(id); else await test(id);
  }

  const stepIndex = phase === "connect" || phase === "authorizing" ? 0 : phase === "testing" ? 1 : phase === "done" ? 2 : error && !tools ? 0 : 1;
  const stepNames = [t("onboarding.step_connect"), t("onboarding.step_test"), t("onboarding.step_done")];
  const busy = phase === "authorizing" || phase === "testing";
  const needsToken = preset.auth === "token";
  const needsClient = google;
  const canConnect = !busy && (!needsToken || token.trim()) && (!needsClient || (clientId.trim() && clientSecret.trim()));
  const mainLabel = preset.auth === "oauth" ? t("onboarding.sign_in_with", { name: preset.name }) : preset.auth === "none" ? t("onboarding.connect_plain") : t("onboarding.connect_test");
  const tokenPage = tokenPages[preset.id];
  const howto = howtoKeys[preset.id];
  const primaryLabel = phase === "authorizing" ? t("onboarding.waiting") : phase === "testing" ? t("onboarding.checking") : phase === "error" ? t("onboarding.try_again") : mainLabel;
  return (
    <>
      <div className="onb-body">
      {lead}
      <ol className="onb-steps" aria-label={t("onboarding.steps_label")}>
        {stepNames.map((label, position) => <li key={label} data-state={position < stepIndex ? "done" : position === stepIndex ? "current" : "todo"}><i /><span>{label}</span></li>)}
      </ol>
      {phase === "done" ? (
        <div className="onb-panel onb-service-done" role="status"><strong><TofiIcon name="check-circle" size={18} /> {t("onboarding.service_ready", { name: preset.name })}</strong><p>{t("onboarding.service_tools", { count: tools })}</p></div>
      ) : (
        <div className="onb-service-form">
          {howto && <p className="onb-small onb-howto">{t(howto)}{tokenPage && <> <a className="onb-link" href={tokenPage} target="_blank" rel="noopener noreferrer">{t("onboarding.where_token")}</a></>}</p>}
          {google && <p className="onb-small">{t("onboarding.google_setup")} <a className="onb-link" href={googleAPIEnableURL(preset.googleAPIs!.api)} target="_blank" rel="noopener noreferrer">{t("extensions:google.enable_api")}</a> · <a className="onb-link" href={googleAPIEnableURL(preset.googleAPIs!.mcp)} target="_blank" rel="noopener noreferrer">{t("extensions:google.enable_mcp")}</a> · <a className="onb-link" href={googleAudienceURL} target="_blank" rel="noopener noreferrer">{t("extensions:google.test_users")}</a></p>}
          {needsClient && <><label>{t("extensions:form.client_id")}<input value={clientId} autoComplete="off" disabled={busy} onChange={event => setClientId(event.target.value)} /></label><label>{t("extensions:form.client_secret")}<input type="password" value={clientSecret} autoComplete="off" disabled={busy} onChange={event => setClientSecret(event.target.value)} /></label>{options?.web_callback_origin && <p className="onb-small">{t("extensions:callback.web")}: <code>{`${options.web_callback_origin}${endpoint(name)}/oauth/callback`}</code></p>}</>}
          {needsToken && <label>{t("onboarding.token_label", { name: preset.name })}<input type="password" value={token} autoComplete="off" spellCheck={false} placeholder={t("extensions:form.token_paste")} disabled={busy} data-autofocus onChange={event => setToken(event.target.value)} /></label>}
          {busy && <p className="onb-waiting" role="status"><i aria-hidden="true" />{phase === "authorizing" ? t("onboarding.service_waiting", { name: preset.name }) : t("onboarding.service_checking", { name: preset.name })}</p>}
          {phase === "error" && error && <p className="onb-error" role="alert"><TofiIcon name="error" size={15} /> {error}</p>}
          {preset.setup.length > 0 && <div className="onb-details"><Disclosure title={t("onboarding.details")}><ul className="onb-service-notes">{preset.setup.slice(0, 3).map(line => <li key={line}><TofiIcon name="check" size={14} /> {line}</li>)}</ul></Disclosure></div>}
        </div>
      )}
      </div>
      {vm && <VMOAuthDialog session={vm} serverName={name} title={preset.name} onFinish={(status, detail) => { setVM(null); const id = op.current; if (status === "complete") { void refreshServers(); void test(id); } else { setPhase("error"); setError(detail || t("onboarding.connect_failed", { name: preset.name })); } }} />}
      <footer className="onb-foot">
        <button type="button" className="onb-quiet" onClick={() => { op.current++; popup.current?.close(); onResult("skipped"); }}>{t("onboarding.skip_service", { name: preset.name })}</button>
        {phase === "done"
          ? <button type="button" className="primary-button onb-primary" data-autofocus onClick={() => onResult("done")}>{last ? t("onboarding.finish") : t("onboarding.next_service")} <TofiIcon name="arrow-right" size={16} /></button>
          : <button type="button" className="primary-button onb-primary onb-service-main" data-autofocus={!needsToken && !needsClient || undefined} disabled={!canConnect} onClick={() => void connect()}>{primaryLabel}</button>}
      </footer>
    </>
  );
}
