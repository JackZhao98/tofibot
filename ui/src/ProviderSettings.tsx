import {useEffect, useId, useState, type FormEvent, type MouseEvent, type ReactNode} from "react";
import type {TFunction} from "i18next";
import {api, ApiError} from "./api";
import {useUserTimezone} from "./UserTimezone";
import type {ModelProviderStatus} from "./types";
import "./provider-settings.css";
import { intlLocale } from "./i18n/format";
import { useTranslation } from "./i18n";
import { SettingsCard, SettingsSection, type BadgeState } from "./settings/components";
import { useCodexConnection, type CodexConnection } from "./settings/AppSlots";
import { TofiIcon as Icon } from "./icons";
import {CardHead, ProviderConfirm, SwitchMethod, useDelayedBusy} from "./ProviderControls";
export {CardHead} from "./ProviderControls";
import { CompatCard } from "./CompatProviderCard";
import { usePolicy } from "./capabilityPolicy";
import { ProvidedModels } from "./ProvidedModels";

type KeyProvider = "openai" | "anthropic";
const labels: Record<KeyProvider, string> = {openai: "OpenAI", anthropic: "Claude"};
const placeholders: Record<KeyProvider, string> = {openai: "sk-…", anthropic: "sk-ant-…"};

/** The stored key as the server describes it: first four and last four characters with dots between, never the whole key. */
export function maskedKey(status?: {key_hint?: string; key_prefix?: string}) {
 if (!status?.key_hint) return "";
 return `${status.key_prefix ?? ""}••••••••${status.key_hint.replace(/^…/, "")}`;
}

/**
 * Model providers, one card each: OpenAI (a ChatGPT sign-in or an API key, never both shown at once) and Claude (an API key).
 * A connected row has one Manage action. Credentials, replacement and removal appear only when requested.
 */
export function ModelProviders(props: {refreshToken: number; codexRefresh: number; onConfigured: () => void; onCodexConfigured: () => void}) {
 const policy = usePolicy();
 // Provided by the organization: the organization's card is read-only. Where the administrator allows it, a "Your own" section
 // beside it holds the ChatGPT sign-in (and only that: own API keys and endpoints come later); otherwise one line says the
 // organization manages the models. Claude, OpenAI and compatible cards are never rendered while provided.
 if (!policy.provided("ai_models")) return <OwnModelProviders {...props}/>;
 return <ProvidedModels refreshToken={props.refreshToken} ownAllowed={policy.ownModels} own={policy.ownModels ? <ProvidedOwnModels codexRefresh={props.codexRefresh} onCodexConfigured={props.onCodexConfigured}/> : undefined}/>;
}

/** The "Your own" section while models are provided: the ChatGPT sign-in card, in the same quiet filled block as the rest. */
function ProvidedOwnModels({codexRefresh, onCodexConfigured}: {codexRefresh: number; onCodexConfigured: () => void}) {
 const {t} = useTranslation("settings");
 const codex = useCodexConnection(codexRefresh, onCodexConfigured);
 return <SettingsSection className="model-providers provided-own" title={t("policy.member.models.own_title")}>
  <SettingsCard className="provider-list provider-chatgpt"><ChatGPTCard codex={codex}/></SettingsCard>
 </SettingsSection>;
}

/** The ChatGPT (Codex) sign-in on its own: no OpenAI API key beside it. Sign in, reconnect and disconnect, nothing else. */
function ChatGPTCard({codex}: {codex: CodexConnection}) {
 const {t} = useTranslation("settings");
 const {t: tc} = useTranslation("chat");
 const titleId = useId();
 const [managing, setManaging] = useState(false);
 const on = codex.connected === true || (codex.connected === false && codex.needsReconnect);
 const waiting = codex.connected === null && codex.working && !codex.error;
 const state: BadgeState = waiting ? "testing" : on ? (codex.needsReconnect || codex.check === "unverified" || codex.connected !== true ? "bad" : "ok") : "need";
 let body: ReactNode;
 if (waiting) body = <p className="provider-key-line is-muted" role="status">{t("providers.status_loading")}</p>;
 else if (codex.session) body = <div className="verification-card"><p>{tc("codex.enter_code")}</p><code>{codex.session.code}</code><a href={codex.session.url} target="_blank" rel="noreferrer">{tc("codex.open_verification")} <Icon name="external-link" size={16} style={{verticalAlign: "middle"}}/></a></div>;
 else if (on) body = <>
  <div className="provider-actions">
   {codex.check === "unverified" && <button type="button" className="st-button" onClick={codex.refresh} disabled={codex.working}>{t("action.retry")}</button>}
   {codex.needsReconnect && <button type="button" className="st-button" onClick={() => void codex.connect()} disabled={codex.working}>{t("providers.reconnect")}</button>}
   <ProviderConfirm label={t("providers.disconnect")} question={t("providers.disconnect_chatgpt_question")} disabled={codex.working} onConfirm={async () => { if (!(await codex.disconnect())) throw new Error(codex.error || t("action.failed")); }}/>
  </div>
 </>;
 else body = <>
  {codex.connected === null && !codex.working && <p className="field-note"><button type="button" className="st-button" onClick={codex.refresh}>{tc("codex.retry_status")}</button></p>}
 </>;
 return <article className="detail-content provider-card" aria-labelledby={titleId} aria-busy={waiting || codex.working} data-provider="codex">
  <CardHead id={titleId} title="ChatGPT" state={state} action={on ? <button type="button" className="st-button" aria-expanded={managing} onClick={() => setManaging(value => !value)}>{t("providers.manage")}</button> : !waiting && !codex.session ? <button type="button" className="st-button" data-method="chatgpt" disabled={codex.working} onClick={() => void codex.connect()}>{t("providers.chatgpt_title")}</button> : undefined}/>
  {(!on || managing) && body}
  {codex.error && <p className="error-text" role="alert">{codex.error}</p>}
 </article>;
}

function OwnModelProviders({refreshToken, codexRefresh, onConfigured, onCodexConfigured}: {refreshToken: number; codexRefresh: number; onConfigured: () => void; onCodexConfigured: () => void}) {
 const {t} = useTranslation("settings");
 const [providers, setProviders] = useProviders(refreshToken);
 const codex = useCodexConnection(codexRefresh, onCodexConfigured);
 const update = (next: ModelProviderStatus | null) => { if (next) setProviders.set(next); else setProviders.reload(); onConfigured(); };
 return <SettingsSection className="model-providers" title={t("providers.title")}>
  {providers.error && <p className="error-text" role="alert">{providers.error} <button type="button" className="st-button" onClick={setProviders.reload}>{t("action.retry")}</button></p>}
  {/* One card per service: its sign-in or key, status and removal never share a box with another service. */}
  <SettingsCard className="provider-list provider-openai"><OpenAICard status={providers.list?.find(item => item.id === "openai")} loading={!providers.list && !providers.error} codex={codex} onChange={update}/></SettingsCard>
  <SettingsCard className="provider-list provider-anthropic"><KeyCard id="anthropic" status={providers.list?.find(item => item.id === "anthropic")} loading={!providers.list && !providers.error} onChange={update}/></SettingsCard>
  <SettingsCard className="provider-list provider-compatible"><CompatCard status={providers.list?.find(item => item.id === "compatible")} loading={!providers.list && !providers.error} onChange={update}/></SettingsCard>
 </SettingsSection>;
}

function useProviders(refreshToken: number) {
 const {t} = useTranslation("settings");
 const [list, setList] = useState<ModelProviderStatus[] | null>(null);
 const [error, setError] = useState("");
 const [version, setVersion] = useState(0);
 useEffect(() => {
  const controller = new AbortController();
  setError("");
  api.listProviders(controller.signal).then(setList).catch(() => { if (!controller.signal.aborted) setError(t("providers.load_failed")); });
  return () => controller.abort();
 }, [refreshToken, version]);
 return [{list, error}, {
  set: (next: ModelProviderStatus) => setList(current => current ? current.some(item => item.id === next.id) ? current.map(item => item.id === next.id ? next : item) : [...current, next] : [next]),
  reload: () => setVersion(value => value + 1),
 }] as const;
}


function verifiedText(status: ModelProviderStatus | undefined, timezone: string, t: TFunction<"settings">) {
 const failed = Boolean(status?.error);
 const at = status?.verified_at ? formatVerified(status.verified_at, timezone) : "";
 if (failed) return t("providers.verify_failed", {error: status?.error});
 return at ? t("providers.verified_at", {time: at}) : "";
}

/** The saved key: masked, with the time it was verified, and Replace key / Remove as secondary actions. */
function KeySummary({provider, status, replacing, onReplace, onRemove, extra}: {provider: KeyProvider; status: ModelProviderStatus; replacing: boolean; onReplace: () => void; onRemove: () => Promise<void>; extra?: ReactNode}) {
 const {t} = useTranslation("settings");
 const {timezone} = useUserTimezone();
 const verified = verifiedText(status, timezone, t);
 const failed = Boolean(status.error);
 return <>
  <p className={`provider-key-line${failed ? " is-failed" : ""}`} role="status"><code className="provider-key-mask">{maskedKey(status)}</code>{verified && <span className="provider-key-meta">{verified}</span>}</p>
  <div className="provider-actions">
   <button type="button" className="st-button" aria-expanded={replacing} onClick={onReplace}>{t("providers.replace_key")}</button>
   <ProviderConfirm label={t("providers.remove")} question={t("providers.remove_question", {provider: labels[provider]})} onConfirm={onRemove}/>
   {extra}
  </div>
 </>;
}

/** What entering a key takes: the key, plus the Workspace ID for Claude. Verified before it is stored. */
function useKeyEntry(id: KeyProvider, onSaved: (next: ModelProviderStatus) => void) {
 const {t} = useTranslation("settings");
 const [key, setKey] = useState("");
 const [workspace, setWorkspace] = useState("");
 const [verifying, setVerifying] = useState(false);
 const showVerifying = useDelayedBusy(verifying);
 const [error, setError] = useState("");
 const label = labels[id];
 async function save(event: FormEvent) {
  event.preventDefault();
  const value = key.trim();
  if (!value || verifying) return;
  setVerifying(true); setError("");
  try {
   const next = await api.setProviderKey(id, value, id === "anthropic" ? workspace.trim() || undefined : undefined);
   setKey(""); setWorkspace("");
   onSaved(next);
  } catch (cause) {
   setError(cause instanceof ApiError && cause.code === "no_quota" ? t("providers.no_quota", {provider: label}) : cause instanceof ApiError && cause.code === "no_models" ? t("providers.no_models", {provider: label}) : cause instanceof ApiError && cause.code === "invalid_workspace" ? t("providers.invalid_workspace") : cause instanceof ApiError && cause.code === "invalid_key" ? !cause.message ? t("providers.invalid_key", {provider: label}) : t(/workspace/i.test(cause.message) ? "providers.invalid_key_detail_workspace" : "providers.invalid_key_detail", {provider: label, detail: cause.message}) : cause instanceof ApiError && cause.status >= 500 ? t("providers.unreachable", {provider: label}) : t("action.save_failed"));
  } finally { setVerifying(false); }
 }
 return {key, setKey, workspace, setWorkspace, verifying, showVerifying, error, setError, save};
}

function KeyForm({id, entry, onCancel, autoFocus = false}: {id: KeyProvider; entry: ReturnType<typeof useKeyEntry>; onCancel?: () => void; autoFocus?: boolean}) {
 const {t} = useTranslation("settings");
 const inputId = useId();
 const {key, setKey, workspace, setWorkspace, verifying, showVerifying, error, setError, save} = entry;
 function cancel(event: MouseEvent<HTMLButtonElement>) {
  const card = event.currentTarget.closest(".provider-card");
  const trigger = card?.querySelector<HTMLButtonElement>('.provider-actions > button[aria-expanded="true"]') ?? card?.querySelector<HTMLButtonElement>('.provider-card-head button[aria-expanded="true"]');
  trigger?.focus();
  onCancel?.();
 }
 const saveButton = <button type="submit" className="st-button" disabled={verifying || !key.trim() || (id === "anthropic" && !workspace.trim())}>{showVerifying ? t("providers.verifying") : t("providers.save")}</button>;
 return <form className="provider-key-form" onSubmit={event => void save(event)} aria-busy={verifying}>
  <label htmlFor={inputId}>{t("providers.key_label", {provider: labels[id]})}</label>
  <div className="provider-key-row">
   <input id={inputId} name={`${id}-api-key`} type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={placeholders[id]} value={key} disabled={verifying} autoFocus={autoFocus} onChange={event => { setKey(event.target.value); setError(""); }} aria-invalid={Boolean(error)} aria-describedby={error ? `${inputId}-error` : undefined}/>
   {id !== "anthropic" && saveButton}
   {id !== "anthropic" && onCancel && <button type="button" className="st-button" disabled={verifying} onClick={cancel}>{t("action.cancel")}</button>}
  </div>
  {id === "anthropic" && <>
   <label htmlFor={`${inputId}-workspace`}>{t("providers.workspace_label")}</label>
   <input id={`${inputId}-workspace`} name="anthropic-workspace-id" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="wrkspc_…" value={workspace} required disabled={verifying} onChange={event => { setWorkspace(event.target.value); setError(""); }}/>
   <p className="field-note">{t("providers.workspace_note")}</p>
   <div className="provider-key-row">{saveButton}{onCancel && <button type="button" className="st-button" disabled={verifying} onClick={cancel}>{t("action.cancel")}</button>}</div>
  </>}
  {error && <p id={`${inputId}-error`} className="error-text" role="alert">{error}</p>}
 </form>;
}

/** Claude: Add opens a key form; Manage reveals a saved key and its actions. */
function KeyCard({id, status, loading, onChange}: {id: KeyProvider; status?: ModelProviderStatus; loading: boolean; onChange: (next: ModelProviderStatus | null) => void}) {
 const {t} = useTranslation("settings");
 const titleId = useId();
 const [managing, setManaging] = useState(false);
 const [replacing, setReplacing] = useState(false);
 const [notice, setNotice] = useState("");
 const entry = useKeyEntry(id, next => { setReplacing(false); setNotice(t("providers.saved", {provider: labels[id]})); onChange(next); });
 const configured = Boolean(status?.configured);
 const failed = configured && Boolean(status?.error);
 async function remove() {
  try { await api.deleteProviderKey(id); }
  catch { throw new Error(t("providers.remove_failed")); }
  setNotice(t("providers.removed", {provider: labels[id]}));
  onChange(null);
 }
 return <article className="detail-content provider-card" aria-labelledby={titleId} aria-busy={entry.verifying || loading}>
  <CardHead id={titleId} title={labels[id]} state={loading ? "testing" : !configured ? "need" : failed ? "bad" : "ok"} action={!loading && <button type="button" className="st-button" aria-expanded={configured ? managing : replacing} onClick={() => configured ? setManaging(value => !value) : setReplacing(value => !value)}>{t(configured ? "providers.manage" : "providers.add")}</button>}/>
  {loading ? <p className="provider-key-line is-muted" role="status">{t("providers.status_loading")}</p> : configured && status ? managing && <>
   <KeySummary provider={id} status={status} replacing={replacing} onReplace={() => { setReplacing(value => !value); setNotice(""); }} onRemove={remove}/>
   {replacing && <KeyForm id={id} entry={entry} autoFocus onCancel={() => { setReplacing(false); entry.setError(""); }}/>}
  </> : <>
   {replacing && <KeyForm id={id} entry={entry} autoFocus onCancel={() => setReplacing(false)}/>}
  </>}
  {notice && !entry.error && <p className="field-note" role="status">{notice}</p>}
 </article>;
}

/**
 * OpenAI: the ChatGPT account sign-in (Codex) or an API key. The two are alternatives, so a card that has one shows only
 * that one; the other is offered as "use ... instead", which first disconnects the current method after a confirmation.
 * A server that has both keeps working (the ChatGPT account is the one Bots use); the card shows it and lets the person remove the key.
 */
function OpenAICard({status, loading, codex, onChange}: {status?: ModelProviderStatus; loading: boolean; codex: CodexConnection; onChange: (next: ModelProviderStatus | null) => void}) {
 const {t} = useTranslation("settings");
 const {t: tc} = useTranslation("chat");
 const titleId = useId();
 const [managing, setManaging] = useState(false);
 const [replacing, setReplacing] = useState(false);
 const [choosingKey, setChoosingKey] = useState(false);
 const [notice, setNotice] = useState("");
 const entry = useKeyEntry("openai", next => { setReplacing(false); setChoosingKey(false); setNotice(t("providers.saved", {provider: labels.openai})); onChange(next); });
 const keyOn = Boolean(status?.configured);
 const keyFailed = keyOn && Boolean(status?.error);
 const codexOn = codex.connected === true || (codex.connected === false && codex.needsReconnect);
 const waiting = loading || (codex.connected === null && codex.working && !codex.error);
 async function removeKey() {
  try { await api.deleteProviderKey("openai"); }
  catch { throw new Error(t("providers.remove_failed")); }
  setNotice(t("providers.removed", {provider: labels.openai}));
  onChange(null);
 }
 async function switchToKey() { if (await codex.disconnect()) { setChoosingKey(true); setNotice(""); } else throw new Error(codex.error || t("action.failed")); }
 async function switchToChatGPT() { await removeKey(); setNotice(""); void codex.connect(); }
 const state: BadgeState = waiting ? "testing" : codexOn ? (codex.needsReconnect || codex.check === "unverified" || codex.connected !== true ? "bad" : "ok") : keyOn ? (keyFailed ? "bad" : "ok") : "need";
 const verification = codex.session && <div className="verification-card"><p>{tc("codex.enter_code")}</p><code>{codex.session.code}</code><a href={codex.session.url} target="_blank" rel="noreferrer">{tc("codex.open_verification")} <Icon name="external-link" size={16} style={{verticalAlign: "middle"}}/></a></div>;
 const choices = (
  <div className="provider-actions provider-choices" role="group" aria-label={t("providers.choose_method")}>
   <button type="button" className="st-button provider-choice" data-method="chatgpt" disabled={codex.working} onClick={() => { setChoosingKey(false); void codex.connect(); }}>
    {t("providers.chatgpt_title")}
   </button>
   <button type="button" className="st-button provider-choice" data-method="key" aria-expanded={choosingKey} onClick={() => setChoosingKey(true)}>
    {t("providers.key_title")}
   </button>
  </div>
 );
 let body: ReactNode;
 if (waiting) body = <p className="provider-key-line is-muted" role="status">{t("providers.status_loading")}</p>;
 else if (codex.session) body = verification;
 else if (codexOn) body = <>
  <div className="provider-actions">
   {codex.check === "unverified" && <button type="button" className="st-button" onClick={codex.refresh} disabled={codex.working}>{t("action.retry")}</button>}
   {codex.needsReconnect && <button type="button" className="st-button" onClick={() => void codex.connect()} disabled={codex.working}>{t("providers.reconnect")}</button>}
   <SwitchMethod label={t("providers.switch_to_key")} question={t("providers.switch_to_key_question")} confirm={t("providers.switch")} onConfirm={switchToKey}/>
   <ProviderConfirm label={t("providers.disconnect")} question={t("providers.disconnect_chatgpt_question")} disabled={codex.working} onConfirm={async () => { if (!(await codex.disconnect())) throw new Error(codex.error || t("action.failed")); }}/>
  </div>
  {keyOn && status && <div className="provider-also"><span>{t("providers.also_key", {key: maskedKey(status)})}</span><ProviderConfirm label={t("providers.remove")} question={t("providers.remove_question", {provider: labels.openai})} onConfirm={removeKey}/></div>}
 </>;
 else if (keyOn && status) body = <>
  <KeySummary provider="openai" status={status} replacing={replacing} onReplace={() => { setReplacing(value => !value); setNotice(""); }} onRemove={removeKey}
   extra={<SwitchMethod label={t("providers.switch_to_chatgpt")} question={t("providers.switch_to_chatgpt_question")} confirm={t("providers.switch")} onConfirm={switchToChatGPT}/>}/>
  {replacing && <KeyForm id="openai" entry={entry} autoFocus onCancel={() => { setReplacing(false); entry.setError(""); }}/>}
 </>;
 else body = <>

  {choosingKey && <KeyForm id="openai" entry={entry} autoFocus onCancel={() => { setChoosingKey(false); entry.setError(""); }}/>}
  {codex.connected === null && !codex.working && <p className="field-note"><button type="button" className="st-button" onClick={codex.refresh}>{tc("codex.retry_status")}</button></p>}
 </>;
 return <article className="detail-content provider-card" aria-labelledby={titleId} aria-busy={waiting || entry.verifying || codex.working}>
  <CardHead id={titleId} title={labels.openai} state={state} action={(codexOn || keyOn) ? <button type="button" className="st-button" aria-expanded={managing} onClick={() => setManaging(value => !value)}>{t("providers.manage")}</button> : !waiting && !codex.session ? choices : undefined}/>
  {(!(codexOn || keyOn) || managing || codex.session) && body}
  {notice && !entry.error && <p className="field-note" role="status">{notice}</p>}
  {codex.error && <p className="error-text" role="alert">{codex.error}</p>}
 </article>;
}


export function formatVerified(value: string, timeZone: string) {
 const date = new Date(value);
 if (Number.isNaN(date.getTime())) return "";
 try { return date.toLocaleString(intlLocale(), {timeZone, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
 catch { return date.toLocaleString(intlLocale()); }
}
