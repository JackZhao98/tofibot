import {useEffect, useId, useRef, useState, type FormEvent, type ReactNode} from "react";
import {api, ApiError} from "./api";
import {ConfirmAction} from "./InteractionSystem";
import {useUserTimezone} from "./UserTimezone";
import type {ModelProviderStatus} from "./types";
import "./provider-settings.css";
import { intlLocale } from "./i18n/format";
import { useTranslation } from "./i18n";
import { SettingsCard, SettingsSection, StatusBadge } from "./settings/components";

type KeyProvider = "openai" | "anthropic";
const keyProviders = [
 {id: "openai", label: "OpenAI", description: "providers.openai_description", placeholder: "sk-…"},
 {id: "anthropic", label: "Claude", description: "providers.anthropic_description", placeholder: "sk-ant-…"},
] as const satisfies readonly {id: KeyProvider; label: string; description: string; placeholder: string}[];
type DescriptionKey = (typeof keyProviders)[number]["description"];

/** Codex sign-in plus the API-key providers, one card each. */
export function ModelProviders({codex, refreshToken, onConfigured}: {codex: ReactNode; refreshToken: number; onConfigured: () => void}) {
 const {t} = useTranslation("settings");
 const [providers, setProviders] = useState<ModelProviderStatus[] | null>(null);
 const [error, setError] = useState("");
 const [version, setVersion] = useState(0);
 useEffect(() => {
  const controller = new AbortController();
  setError("");
  api.listProviders(controller.signal).then(setProviders).catch(() => { if (!controller.signal.aborted) setError(t("providers.load_failed")); });
  return () => controller.abort();
 }, [refreshToken, version]);
 const update = (next: ModelProviderStatus) => setProviders(current => current ? current.some(item => item.id === next.id) ? current.map(item => item.id === next.id ? next : item) : [...current, next] : [next]);
 return <SettingsSection className="model-providers" title={t("providers.title")} description={t("providers.description")}>
  {error && <p className="error-text" role="alert">{error} <button type="button" className="text-button" onClick={() => setVersion(value => value + 1)}>{t("action.retry")}</button></p>}
  <SettingsCard className="provider-list">
   {codex}
   {keyProviders.map(item => <ApiKeyProviderCard key={item.id} {...item} status={providers?.find(provider => provider.id === item.id)} loading={!providers && !error} onChange={next => { if (next) update(next); else setVersion(value => value + 1); onConfigured(); }} />)}
  </SettingsCard>
 </SettingsSection>;
}

function ApiKeyProviderCard({id, label, description, placeholder, status, loading, onChange}: {id: KeyProvider; label: string; description: DescriptionKey; placeholder: string; status?: ModelProviderStatus; loading: boolean; onChange: (next: ModelProviderStatus | null) => void}) {
 const {t} = useTranslation("settings");
 const {timezone} = useUserTimezone();
 const inputId = useId();
 const [key, setKey] = useState("");
 const [verifying, setVerifying] = useState(false);
 const [workspace, setWorkspace] = useState("");
 const [error, setError] = useState("");
 const [notice, setNotice] = useState("");
 const input = useRef<HTMLInputElement>(null);
 const configured = Boolean(status?.configured);
 const failed = configured && Boolean(status?.error);
 const state = loading ? "unknown" : !configured ? "idle" : failed ? "missing" : "connected";
 const verifiedAt = status?.verified_at ? formatVerified(status.verified_at, timezone) : "";
 const configuredText = status?.key_hint ? t("providers.configured_hint", {hint: status.key_hint}) : t("providers.configured");
 const statusText = loading ? t("providers.status_loading") : !configured ? t("providers.not_configured") : failed ? `${configuredText} · ${t("providers.verify_failed", {error: status?.error})}` : verifiedAt ? `${configuredText} · ${t("providers.verified_at", {time: verifiedAt})}` : configuredText;
 async function save(event: FormEvent) {
  event.preventDefault();
  const value = key.trim();
  if (!value || verifying) return;
  setVerifying(true); setError(""); setNotice("");
  try {
   const next = await api.setProviderKey(id, value, id === "anthropic" ? workspace.trim() || undefined : undefined);
   setKey(""); setWorkspace("");
   setNotice(t("providers.saved", {provider: label}));
   onChange(next);
  } catch (cause) {
   setError(cause instanceof ApiError && cause.code === "invalid_workspace" ? t("providers.invalid_workspace") : cause instanceof ApiError && cause.code === "invalid_key" ? !cause.message ? t("providers.invalid_key", {provider: label}) : t(/workspace/i.test(cause.message) ? "providers.invalid_key_detail_workspace" : "providers.invalid_key_detail", {provider: label, detail: cause.message}) : cause instanceof ApiError && cause.status >= 500 ? t("providers.unreachable", {provider: label}) : t("action.save_failed"));
   input.current?.focus();
  } finally { setVerifying(false); }
 }
 async function remove() {
  try { await api.deleteProviderKey(id); }
  catch { throw new Error(t("providers.remove_failed")); }
  setNotice(t("providers.removed", {provider: label})); setError("");
  onChange(null);
 }
 const saveButton = <button type="submit" className="primary-button" disabled={verifying || !key.trim() || (id === "anthropic" && !workspace.trim())}>{verifying ? t("providers.verifying") : t("providers.save")}</button>;
 return <article className="detail-content provider-card" aria-labelledby={`${inputId}-title`} aria-busy={verifying || loading}>
  <div className="provider-card-head"><div><h3 id={`${inputId}-title`}>{label}</h3><p>{t(description)}</p></div><span className="provider-card-marks"><StatusBadge state={loading ? "testing" : !configured ? "need" : failed ? "bad" : "ok"} /><span className="settings-tag">{t("providers.api_key_tag")}</span></span></div>
  <div className={`codex-status provider-status ${state}`} role="status"><span className="status-dot" />{statusText}</div>
  <form className="provider-key-form" onSubmit={event => void save(event)}>
   <label htmlFor={inputId}>{configured ? t("providers.replace_label", {provider: label}) : t("providers.key_label", {provider: label})}</label>
   <div className="provider-key-row">
    <input ref={input} id={inputId} name={`${id}-api-key`} type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={placeholder} value={key} disabled={verifying} onChange={event => { setKey(event.target.value); setError(""); setNotice(""); }} aria-invalid={Boolean(error)} aria-describedby={error ? `${inputId}-error` : undefined} />
    {id !== "anthropic" && saveButton}
   </div>
   {id === "anthropic" && <><label htmlFor={`${inputId}-workspace`}>{t("providers.workspace_label")}</label>
    <input id={`${inputId}-workspace`} name="anthropic-workspace-id" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="wrkspc_…" value={workspace} required disabled={verifying} onChange={event => { setWorkspace(event.target.value); setError(""); }} />
    <p className="field-note">{t("providers.workspace_note")}</p>{saveButton}</>}
   {error && <p id={`${inputId}-error`} className="error-text" role="alert">{error}</p>}
   {notice && !error && <p className="field-note" role="status">{notice}</p>}
  </form>
  <div className="provider-card-foot"><p className="field-note">{t("providers.key_note")}</p>{configured && <ConfirmAction label={t("providers.remove")} question={t("providers.remove_question", {provider: label})} disabled={verifying} onConfirm={remove} />}</div>
 </article>;
}

function formatVerified(value: string, timeZone: string) {
 const date = new Date(value);
 if (Number.isNaN(date.getTime())) return "";
 try { return date.toLocaleString(intlLocale(), {timeZone, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
 catch { return date.toLocaleString(intlLocale()); }
}
