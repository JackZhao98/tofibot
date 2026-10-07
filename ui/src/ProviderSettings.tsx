import {useEffect, useId, useRef, useState, type FormEvent, type ReactNode} from "react";
import {api, ApiError} from "./api";
import {ConfirmAction} from "./InteractionSystem";
import {useUserTimezone} from "./UserTimezone";
import type {ModelProviderStatus} from "./types";
import "./provider-settings.css";

type KeyProvider = "openai" | "anthropic";
const keyProviders: {id: KeyProvider; label: string; description: string; placeholder: string}[] = [
 {id: "openai", label: "OpenAI", description: "用 OpenAI API key 调用 GPT 与 o 系列模型，按 API 用量计费。", placeholder: "sk-…"},
 {id: "anthropic", label: "Claude", description: "用 Anthropic API key 调用 Claude 模型，按 API 用量计费。", placeholder: "sk-ant-…"},
];

/** Codex sign-in plus the API-key providers, one card each. */
export function ModelProviders({codex, refreshToken, onConfigured}: {codex: ReactNode; refreshToken: number; onConfigured: () => void}) {
 const [providers, setProviders] = useState<ModelProviderStatus[] | null>(null);
 const [error, setError] = useState("");
 const [version, setVersion] = useState(0);
 useEffect(() => {
  const controller = new AbortController();
  setError("");
  api.listProviders(controller.signal).then(setProviders).catch(() => { if (!controller.signal.aborted) setError("暂时无法读取模型提供方状态。"); });
  return () => controller.abort();
 }, [refreshToken, version]);
 const update = (next: ModelProviderStatus) => setProviders(current => current ? current.some(item => item.id === next.id) ? current.map(item => item.id === next.id ? next : item) : [...current, next] : [next]);
 return <section className="model-providers" aria-labelledby="model-providers-title">
  <div className="model-providers-heading"><h3 id="model-providers-title">模型提供方</h3><p className="settings-description">连接任意一个即可使用。已连接提供方的模型都会出现在 Bot 和默认配置的模型列表里。</p></div>
  {error && <p className="error-text" role="alert">{error} <button type="button" className="text-button" onClick={() => setVersion(value => value + 1)}>重试</button></p>}
  <div className="provider-list">
   {codex}
   {keyProviders.map(item => <ApiKeyProviderCard key={item.id} {...item} status={providers?.find(provider => provider.id === item.id)} loading={!providers && !error} onChange={next => { if (next) update(next); else setVersion(value => value + 1); onConfigured(); }} />)}
  </div>
 </section>;
}

function ApiKeyProviderCard({id, label, description, placeholder, status, loading, onChange}: {id: KeyProvider; label: string; description: string; placeholder: string; status?: ModelProviderStatus; loading: boolean; onChange: (next: ModelProviderStatus | null) => void}) {
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
 const hint = status?.key_hint ? ` ${status.key_hint}` : "";
 const verifiedAt = status?.verified_at ? formatVerified(status.verified_at, timezone) : "";
 const statusText = loading ? "正在读取状态…" : !configured ? "未配置" : failed ? `已配置${hint} · 验证失败：${status?.error}` : verifiedAt ? `已配置${hint} · 已验证于 ${verifiedAt}` : `已配置${hint}`;
 async function save(event: FormEvent) {
  event.preventDefault();
  const value = key.trim();
  if (!value || verifying) return;
  setVerifying(true); setError(""); setNotice("");
  try {
   const next = await api.setProviderKey(id, value, id === "anthropic" ? workspace.trim() || undefined : undefined);
   setKey(""); setWorkspace("");
   setNotice(`${label} API key 已保存并通过验证。`);
   onChange(next);
  } catch (cause) {
   setError(cause instanceof ApiError && cause.code === "invalid_workspace" ? "Workspace ID 格式不对，应为 Claude Console 里 Workspaces 页面 ID 列的 wrkspc_… 。" : cause instanceof ApiError && cause.code === "invalid_key" ? `这个 API key 没有通过 ${label} 的验证，未保存。${cause.message ? `（${cause.message}）` : ""}${/workspace/i.test(cause.message) ? " 这个 key 没有限定 workspace：请在下方填 Workspace ID，或在 Console 新建一个限定 workspace 的 key。" : ""}` : cause instanceof ApiError && cause.status >= 500 ? `暂时无法连到 ${label} 完成验证，未保存。请稍后重试。` : "保存失败，请重试。");
   input.current?.focus();
  } finally { setVerifying(false); }
 }
 async function remove() {
  try { await api.deleteProviderKey(id); }
  catch { throw new Error("移除失败，请重试。"); }
  setNotice(`已移除 ${label} API key。使用 ${label} 模型的 Bot 需要换一个已连接的模型。`); setError("");
  onChange(null);
 }
 return <article className="detail-content provider-card" aria-labelledby={`${inputId}-title`} aria-busy={verifying || loading}>
  <div className="provider-card-head"><div><h3 id={`${inputId}-title`}>{label}</h3><p>{description}</p></div><span className="settings-tag">API key</span></div>
  <div className={`codex-status provider-status ${state}`} role="status"><span className="status-dot" />{statusText}</div>
  <form className="provider-key-form" onSubmit={event => void save(event)}>
   <label htmlFor={inputId}>{configured ? `替换 ${label} API key` : `${label} API key`}</label>
   <div className="provider-key-row">
    <input ref={input} id={inputId} name={`${id}-api-key`} type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={placeholder} value={key} disabled={verifying} onChange={event => { setKey(event.target.value); setError(""); setNotice(""); }} aria-invalid={Boolean(error)} aria-describedby={error ? `${inputId}-error` : undefined} />
    <button type="submit" className="primary-button" disabled={verifying || !key.trim()}>{verifying ? "验证中…" : "保存并验证"}</button>
   </div>
   {id === "anthropic" && <><label htmlFor={`${inputId}-workspace`}>Workspace ID（可选）</label>
    <input id={`${inputId}-workspace`} name="anthropic-workspace-id" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder="wrkspc_…" value={workspace} disabled={verifying} onChange={event => { setWorkspace(event.target.value); setError(""); }} />
    <p className="field-note">只有 key 没有限定某个 workspace 时才需要；在 Console 的 Settings → Workspaces 的 ID 列找到。</p></>}
   {error && <p id={`${inputId}-error`} className="error-text" role="alert">{error}</p>}
   {notice && !error && <p className="field-note" role="status">{notice}</p>}
  </form>
  <div className="provider-card-foot"><p className="field-note">保存前会用这个 key 读取一次模型列表来验证；key 加密保存，不会再显示。</p>{configured && <ConfirmAction label="移除" question={`移除 ${label} API key？`} disabled={verifying} onConfirm={remove} />}</div>
 </article>;
}

function formatVerified(value: string, timeZone: string) {
 const date = new Date(value);
 if (Number.isNaN(date.getTime())) return "";
 try { return date.toLocaleString("zh-CN", {timeZone, month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit"}); }
 catch { return date.toLocaleString("zh-CN"); }
}
