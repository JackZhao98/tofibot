import {useId, useState, type FormEvent} from "react";
import {api, ApiError} from "./api";
import {ConfirmAction} from "./InteractionSystem";
import {useUserTimezone} from "./UserTimezone";
import type {CompatListedModel, ModelProviderStatus} from "./types";
import {intlLocale, formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {StatusBadge} from "./settings/components";
import {ProviderCost} from "./ProviderCost";

const SHOWN_MODELS = 12;
/** The address that reaches an Ollama running on the computer that hosts the App container (Docker Desktop). */
const OLLAMA_URL = "http://host.docker.internal:11434/v1";
const isOllama = (url: string) => /:11434(\/|$)/.test(url.trim());

/** Comma, space or line separated model IDs, in the order typed, without repeats. */
function parseModelIds(text: string) {
 return [...new Set(text.split(/[\s,]+/).map(id => id.trim()).filter(Boolean))];
}

function formatVerified(value: string, timeZone: string) {
 const date = new Date(value);
 if (Number.isNaN(date.getTime())) return "";
 try { return date.toLocaleString(intlLocale(), {timeZone, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
 catch { return date.toLocaleString(intlLocale()); }
}

/**
 * OpenAI-compatible endpoint: the address of a server that speaks the OpenAI API (Ollama, LM Studio, vLLM,
 * OpenRouter, ...), an optional key, and model IDs fetched from /v1/models or typed. Saving verifies the endpoint
 * from the server, which is where the requests come from.
 */
export function CompatCard({status, loading, onChange}: {status?: ModelProviderStatus; loading: boolean; onChange: (next: ModelProviderStatus | null) => void}) {
 const {t} = useTranslation("settings");
 const titleId = useId();
 const urlId = useId();
 const keyId = useId();
 const modelsId = useId();
 const configured = Boolean(status?.configured);
 const [editing, setEditing] = useState(false);
 const [baseUrl, setBaseUrl] = useState("");
 const [key, setKey] = useState("");
 const [modelText, setModelText] = useState("");
 const [listed, setListed] = useState<CompatListedModel[] | null>(null);
 const [working, setWorking] = useState<"" | "fetch" | "save">("");
 const [error, setError] = useState("");
 const [notice, setNotice] = useState("");
 const {timezone} = useUserTimezone();
 const showForm = !loading && (!configured || editing);
 const failed = configured && Boolean(status?.error);

 function open() {
  setBaseUrl(status?.base_url ?? ""); setKey(""); setModelText((status?.models ?? []).join(", ")); setListed(null); setError(""); setNotice(""); setEditing(true);
 }
 function close() { setEditing(false); setError(""); }
 function failure(cause: unknown) {
  const code = cause instanceof ApiError ? cause.code : "";
  setError(code === "invalid_base_url" ? t("providers.compat.invalid_base_url") : code === "invalid_models" ? t("providers.compat.invalid_models") : code === "endpoint_not_allowed" ? t("providers.compat.endpoint_not_allowed") : code === "invalid_key" ? t("providers.compat.invalid_key") : code === "no_models" ? t("providers.compat.no_models") : code === "provider_unreachable" || (cause instanceof ApiError && cause.status >= 500) ? t("providers.compat.unreachable") : t("action.save_failed"));
 }
 const input = () => ({base_url: baseUrl.trim(), key: key.trim() || undefined, models: parseModelIds(modelText)});
 async function fetchModels() {
  if (!baseUrl.trim() || working) return;
  setWorking("fetch"); setError("");
  try { setListed(await api.probeCompatModels({...input(), models: undefined})); }
  catch (cause) { setListed(null); failure(cause); }
  finally { setWorking(""); }
 }
 async function save(event: FormEvent) {
  event.preventDefault();
  if (!baseUrl.trim() || working) return;
  setWorking("save"); setError("");
  try {
   const next = await api.setCompatEndpoint(input());
   setKey(""); setEditing(false); setNotice(t("providers.compat.saved"));
   onChange(next);
  } catch (cause) { failure(cause); }
  finally { setWorking(""); }
 }
 async function remove() {
  try { await api.deleteCompatEndpoint(); }
  catch { throw new Error(t("providers.remove_failed")); }
  setNotice(t("providers.compat.removed")); setEditing(false);
  onChange(null);
 }
 const verified = status?.verified_at ? formatVerified(status.verified_at, timezone) : "";
 const summaryMeta = status?.error ? t("providers.verify_failed", {error: status.error}) : verified ? t("providers.verified_at", {time: verified}) : "";
 return <article className="detail-content provider-card" aria-labelledby={titleId} aria-busy={Boolean(working) || loading}>
  <div className="provider-card-head"><h3 id={titleId}>{t("providers.compat.title")}</h3><span className="provider-card-marks"><StatusBadge state={loading ? "testing" : !configured ? "need" : failed ? "bad" : "ok"}/><span className="settings-tag">{t("providers.compat.tag")}</span></span></div>
  {loading ? <p className="provider-key-line is-muted" role="status">{t("providers.status_loading")}</p> : <>
   {!configured && <p className="provider-card-note">{t("providers.compat.description")}</p>}
   {configured && status && <>
    <p className={`provider-key-line${failed ? " is-failed" : ""}`} role="status"><code className="provider-key-mask">{status.base_url}</code>{summaryMeta && <span className="provider-key-meta">{summaryMeta}</span>}</p>
    <p className="provider-card-note compat-summary">{status.key_hint ? t("providers.compat.key_saved", {key: status.key_hint}) : t("providers.compat.no_key")}{(status.models?.length ?? 0) > 0 && <> · {t("providers.compat.typed_models", {models: status.models!.join(", ")})}</>}</p>
    <div className="provider-actions">
     <button type="button" className="text-button" aria-expanded={editing} onClick={() => editing ? close() : open()}>{t("providers.compat.edit")}</button>
     <ConfirmAction label={t("providers.remove")} question={t("providers.compat.remove_question")} onConfirm={remove}/>
    </div>
   </>}
   {showForm && <form className="provider-key-form compat-form" onSubmit={event => void save(event)} aria-busy={Boolean(working)}>
    <div className="compat-presets" role="group" aria-label={t("providers.compat.presets")}>
     <button type="button" className="secondary-button" aria-pressed={isOllama(baseUrl)} disabled={Boolean(working)} onClick={() => { setBaseUrl(OLLAMA_URL); setListed(null); setError(""); }}>Ollama</button>
    </div>
    <label htmlFor={urlId}>{t("providers.compat.url_label")}</label>
    <input id={urlId} name="compat-base-url" type="url" inputMode="url" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="http://host:11434/v1" value={baseUrl} disabled={Boolean(working)} required autoFocus={configured} onChange={event => { setBaseUrl(event.target.value); setListed(null); setError(""); }}/>
    <p className="field-note">{t("providers.compat.url_note")}</p>
    {isOllama(baseUrl) && <p className="field-note compat-ollama" data-compat-note="ollama">{t("providers.compat.ollama_note")}</p>}
    <label htmlFor={keyId}>{t("providers.compat.key_label")}</label>
    <input id={keyId} name="compat-api-key" type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={configured && status?.key_hint ? t("providers.compat.key_keep") : "sk-…"} value={key} disabled={Boolean(working)} onChange={event => { setKey(event.target.value); setError(""); }}/>
    <label htmlFor={modelsId}>{t("providers.compat.models_label")}</label>
    <textarea id={modelsId} name="compat-models" rows={2} autoComplete="off" spellCheck={false} placeholder="llama3.1, qwen3:8b" value={modelText} disabled={Boolean(working)} onChange={event => { setModelText(event.target.value); setError(""); }}/>
    <p className="field-note">{t("providers.compat.models_note")}</p>
    {listed && <div className="compat-listed" role="status">
     <p className="field-note">{listed.length ? t("providers.compat.found", {count: listed.length}) : t("providers.compat.none_found")}</p>
     {listed.length > 0 && <ul>{listed.slice(0, SHOWN_MODELS).map(model => <li key={model.id}><code>{model.id}</code>{model.context_window ? <span>{t("providers.compat.window", {tokens: formatNumber(model.context_window)})}</span> : null}</li>)}{listed.length > SHOWN_MODELS && <li className="compat-more">{t("providers.compat.more", {count: listed.length - SHOWN_MODELS})}</li>}</ul>}
    </div>}
    <div className="provider-key-row">
     <button type="submit" className="primary-button" disabled={Boolean(working) || !baseUrl.trim()}>{working === "save" ? t("providers.verifying") : t("providers.save")}</button>
     <button type="button" className="secondary-button" disabled={Boolean(working) || !baseUrl.trim()} onClick={() => void fetchModels()}>{working === "fetch" ? t("providers.compat.fetching") : t("providers.compat.fetch")}</button>
     {configured && <button type="button" className="secondary-button" disabled={Boolean(working)} onClick={close}>{t("action.cancel")}</button>}
    </div>
    {error && <p className="error-text" role="alert">{error}</p>}
    <p className="field-note">{t("providers.compat.network_note")}</p>
   </form>}
  </>}
  {!loading && <ProviderCost kind="endpoint"/>}
  {notice && !error && <p className="field-note" role="status">{notice}</p>}
 </article>;
}
