import {useEffect, useId, useState, type FormEvent} from "react";
import {api, ApiError, request} from "./api";
import {useTranslation} from "./i18n";
import {Select} from "./Select";
import type {CompatListedModel} from "./types";

const OLLAMA_URL = "http://host.docker.internal:11434/v1";
const isOllama = (url: string) => /:11434(\/|$)/.test(url.trim());

/**
 * First-run option for a person with a model server of their own (Ollama, LM Studio, vLLM, ...): address with the Ollama
 * preset filled, optional key, a connection test that lists the models, a model to start with, then Connect. It uses the
 * same endpoints as the provider card in Settings, and is a lazy chunk loaded only when the option is chosen.
 */
export default function OnboardingServer({formId, onBusy, onConnected}: {formId: string; onBusy: (busy: boolean) => void; onConnected: () => Promise<void>}) {
 const {t} = useTranslation(["chat", "settings"]);
 const ids = useId();
 const [baseUrl, setBaseUrl] = useState(OLLAMA_URL);
 const [key, setKey] = useState("");
 const [models, setModels] = useState<CompatListedModel[] | null>(null);
 const [model, setModel] = useState("");
 const [working, setWorking] = useState<"" | "test" | "connect">("");
 const [error, setError] = useState("");
 useEffect(() => { onBusy(Boolean(working)); }, [working, onBusy]);

 function failure(cause: unknown) {
  const code = cause instanceof ApiError ? cause.code : "";
  setError(code === "endpoint_not_allowed" ? t("chat:onboarding.server_not_allowed")
   : code === "invalid_base_url" ? t("settings:providers.compat.invalid_base_url")
   : code === "invalid_key" ? t("settings:providers.compat.invalid_key")
   : code === "no_models" ? t("settings:providers.compat.no_models")
   : code === "provider_unreachable" || (cause instanceof ApiError && cause.status >= 500) ? t("settings:providers.compat.unreachable")
   : t("chat:onboarding.server_failed"));
 }
 const input = () => ({base_url: baseUrl.trim(), key: key.trim() || undefined});
 async function test() {
  if (!baseUrl.trim() || working) return;
  setWorking("test"); setError("");
  try {
   const found = await api.probeCompatModels(input());
   setModels(found);
   setModel(current => found.some(item => item.id === current) ? current : found[0]?.id ?? "");
   if (!found.length) setError(t("settings:providers.compat.none_found"));
  } catch (cause) { setModels(null); failure(cause); }
  finally { setWorking(""); }
 }
 async function connect(event: FormEvent) {
  event.preventDefault();
  if (working) return;
  if (!models) { await test(); return; }
  if (!model) return;
  setWorking("connect"); setError("");
  try {
   await api.setCompatEndpoint({...input(), models: []});
   await request("/api/model-settings", {method: "PUT", body: JSON.stringify({model: `compat:${model}`, reasoning_effort: ""})});
   await onConnected();
  } catch (cause) { failure(cause); setWorking(""); }
 }
 return <form id={formId} className="onb-panel onb-server" onSubmit={event => void connect(event)} aria-busy={Boolean(working)}>
  <div className="onb-presets" role="group" aria-label={t("settings:providers.compat.presets")}>
   <button type="button" className="secondary-button" aria-pressed={isOllama(baseUrl)} disabled={Boolean(working)} onClick={() => { setBaseUrl(OLLAMA_URL); setModels(null); setError(""); }}>Ollama</button>
  </div>
  <label htmlFor={`${ids}-url`}>{t("settings:providers.compat.url_label")}</label>
  <input id={`${ids}-url`} type="url" inputMode="url" required autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" value={baseUrl} disabled={Boolean(working)} data-autofocus onChange={event => { setBaseUrl(event.target.value); setModels(null); setError(""); }}/>
  {isOllama(baseUrl) && <p className="onb-small" data-compat-note="ollama">{t("settings:providers.compat.ollama_note")}</p>}
  <label htmlFor={`${ids}-key`}>{t("settings:providers.compat.key_label")}</label>
  <input id={`${ids}-key`} type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="sk-…" value={key} disabled={Boolean(working)} onChange={event => { setKey(event.target.value); setModels(null); setError(""); }}/>
  <div className="onb-server-test">
   <button type="button" className="secondary-button" disabled={Boolean(working) || !baseUrl.trim()} onClick={() => void test()}>{working === "test" ? t("settings:providers.compat.fetching") : t("chat:onboarding.server_test")}</button>
   {models && models.length > 0 && <span className="onb-small" role="status">{t("settings:providers.compat.found", {count: models.length})}</span>}
  </div>
  {models && models.length > 0 && <>
   <label>{t("chat:onboarding.server_pick")}<Select aria-label={t("chat:onboarding.server_pick")} value={model} disabled={Boolean(working)} onValueChange={setModel} options={models.map(item => ({value: item.id, label: item.id}))}/></label>
  </>}
  {error && <p className="onb-error" role="alert">{error}</p>}
 </form>;
}
