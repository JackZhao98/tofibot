import { useLayoutEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { api, ApiError } from "./api";
import { ConfirmAction } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import type { ConversationWebhook, IssuedConversationWebhook } from "./types";

// Project metadata explicitly: never retain an issuance object in metadata state.
function metadata(value: ConversationWebhook): ConversationWebhook {
  return { configured: value.configured, enabled: value.enabled, hook_id: value.hook_id, version: value.version, url: value.url, created_at: value.created_at, rotated_at: value.rotated_at, last_accepted_at: value.last_accepted_at };
}

export function WebhookPanel({ conversationId, conversationName, accountKey, active, onClose }: {
  conversationId: string; conversationName: string; accountKey: string; active: boolean; onClose: () => void;
}) {
  const scope = JSON.stringify([accountKey, conversationId]);
  const [snapshot, setSnapshot] = useState<{ scope: string; value: ConversationWebhook } | null>(null);
  const [secret, setSecret] = useState<{ scope: string; value: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const current = useRef({ scope, active });
  current.current = { scope, active };
  const pending = useRef<AbortController | null>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const value = active && snapshot?.scope === scope ? snapshot.value : null;
  const visibleSecret = active && secret?.scope === scope ? secret.value : "";

  function clearSecret() { setSecret(null); setNotice(""); }
  function close() { pending.current?.abort(); clearSecret(); onClose(); }

  async function perform(operation: (signal: AbortSignal) => Promise<ConversationWebhook | IssuedConversationWebhook | void>, issue: boolean) {
    if (!current.current.active || pending.current) return;
    const requestedScope = scope;
    const controller = new AbortController();
    pending.current = controller;
    clearSecret(); setBusy(true); setError("");
    const timeout = window.setTimeout(() => controller.abort(), 12_000);
    const valid = () => !controller.signal.aborted && pending.current === controller && current.current.active && current.current.scope === requestedScope;
    try {
      const response = await operation(controller.signal);
      if (!valid()) return;
      if (!response) {
        setSnapshot({ scope: requestedScope, value: { ...value!, enabled: false } });
        setNotice("已撤销。已接受的工作可通过对话中的停止按钮管理。");
      } else {
        setSnapshot({ scope: requestedScope, value: metadata(response) });
        if (issue) {
          const issued = response as IssuedConversationWebhook;
          if (!issued.enabled || !issued.configured || !issued.secret || !Number.isSafeInteger(issued.version)) throw new Error("invalid_issuance");
          setSecret({ scope: requestedScope, value: issued.secret });
          setNotice("密钥只显示这一次。关闭后如需新密钥，请显式轮换。");
        }
      }
    } catch (cause) {
      if (current.current.active && current.current.scope === requestedScope && pending.current === controller) {
        clearSecret();
        // Never echo arbitrary response text into a secret-handling surface.
        setError(cause instanceof ApiError && cause.status === 409 ? "状态已更改，请重新载入后核对。" : controller.signal.aborted ? "请求超时。创建或轮换可能已完成；请重新载入，显式轮换可取得新密钥。" : "暂时无法完成操作，请重新载入后重试。");
      }
    } finally {
      window.clearTimeout(timeout);
      if (pending.current === controller) { pending.current = null; setBusy(false); }
    }
  }

  useLayoutEffect(() => {
    pending.current?.abort(); pending.current = null;
    setSnapshot(null); clearSecret(); setBusy(false); setError("");
    if (active) { closeRef.current?.focus(); void perform(signal => api.conversationWebhook(conversationId, signal), false); }
    return () => { pending.current?.abort(); pending.current = null; };
  }, [scope, active]);

  useLayoutEffect(() => {
    const leaving = () => {
      pending.current?.abort();
      // Clear before a browser back/forward cache snapshot can retain the page.
      flushSync(() => clearSecret());
    };
    window.addEventListener("pagehide", leaving);
    return () => window.removeEventListener("pagehide", leaving);
  }, []);

  async function copy(text: string, copied: string) {
    const requestedScope = scope;
    try {
      if (!navigator.clipboard) throw new Error("clipboard_unavailable");
      await navigator.clipboard.writeText(text);
      if (current.current.active && current.current.scope === requestedScope) setNotice(copied);
    } catch {
      if (current.current.active && current.current.scope === requestedScope) { clearSecret(); setError("复制失败。密钥已隐藏；如需新密钥，请显式轮换。"); }
    }
  }

  return <div className="detail-content" data-webhook-panel="true">
    <div className="detail-heading"><h2>Webhook</h2><button ref={closeRef} type="button" className="close-button" aria-label="关闭 Webhook" onClick={close}><TofiIcon name="close" size={18} /></button></div>
    <p className="muted">向 {conversationName} 接收外部事件，并在此对话中排队处理。</p>
    {error && <p className="error-text" role="alert">{error}</p>}
    <p role="status" aria-live="polite">{notice || (busy ? "处理中…" : value ? value.enabled ? "已启用" : value.configured ? "已撤销" : "尚未创建" : "")}</p>
    {value?.url && <label style={{ display: "grid", gap: 8 }}>端点 URL<input aria-label="Webhook 端点 URL" value={value.url} readOnly spellCheck={false} autoComplete="off" style={{ width: "100%", minWidth: 0, boxSizing: "border-box" }} /><button type="button" className="secondary-button" disabled={busy} onClick={() => void copy(value.url!, "已复制端点 URL。")}>复制 URL</button></label>}
    {visibleSecret && <div style={{ display: "grid", gap: 10, margin: "18px 0" }}><label style={{ display: "grid", gap: 8 }}>一次性密钥<input aria-label="Webhook 一次性密钥" value={visibleSecret} readOnly spellCheck={false} autoComplete="off" autoCapitalize="none" style={{ width: "100%", minWidth: 0, boxSizing: "border-box", fontFamily: "var(--mono)" }} /></label><button type="button" className="secondary-button" disabled={busy} onClick={() => void copy(visibleSecret, "已复制密钥。关闭后无法再次查看。")}>复制密钥</button><button type="button" className="text-button" onClick={clearSecret}>隐藏密钥</button></div>}
    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 12, margin: "18px 0" }}>
      {value && !value.enabled && <button type="button" className="primary-button" disabled={busy} onClick={() => void perform(signal => api.createConversationWebhook(conversationId, signal), true)}>{value.configured ? "重新启用 Webhook" : "创建 Webhook"}</button>}
      {value?.enabled && Number.isSafeInteger(value.version) && <><ConfirmAction label="轮换密钥" question="立即失效旧密钥并生成新密钥？" disabled={busy} onConfirm={() => perform(signal => api.rotateConversationWebhook(conversationId, value.version!, signal), true)} /><ConfirmAction label="撤销 Webhook" question="停止接收新事件？已接受的工作会保留。" disabled={busy} onConfirm={() => perform(signal => api.revokeConversationWebhook(conversationId, value.version!, signal), false)} /></>}
      <button type="button" className="secondary-button" disabled={busy} onClick={() => void perform(signal => api.conversationWebhook(conversationId, signal), false)}>重新载入</button>
    </div>
    <p className="field-note" style={{ margin: "12px 0", overflowWrap: "anywhere" }}>使用 Authorization: Bearer 密钥发送事件。密钥不能放入 URL。外部事件不能代替你的审批或授予工具权限。</p>
    {value?.last_accepted_at && <p className="muted">最近接受：{value.last_accepted_at}</p>}
  </div>;
}
