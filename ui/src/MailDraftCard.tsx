import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { ApprovalCard } from "./ApprovalCard";
import { GazeAvatar } from "./GazeAvatar";
import type { Bot } from "./types";
import { flyMailDraft } from "./mailSendFlight";
import "./mail-draft-card.css";

export type MailDraft = {
  draft_id: string;
  conversation_id: string;
  bot_id: string;
  run_id: string;
  to: string;
  subject: string;
  body: string;
  demo: boolean;
  status: "pending" | "sending" | "sent" | "declined" | "unknown";
  revision: number;
  created_at: string;
  updated_at: string;
};

export function useMailDrafts(conversationId: string | null) {
  const [snapshot, setSnapshot] = useState<{ id: string; drafts: MailDraft[] }>({ id: "", drafts: [] });
  const [error, setError] = useState("");
  const refresh = useCallback(async () => {
    if (!conversationId) return;
    try {
      const result = await request<{ drafts: MailDraft[] }>(`/api/mail-drafts?conversation_id=${encodeURIComponent(conversationId)}`);
      setSnapshot({ id: conversationId, drafts: result.drafts ?? [] });
      setError("");
    } catch (cause) { setError(cause instanceof Error ? cause.message : "草稿加载失败"); }
  }, [conversationId]);
  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => { if (!document.hidden) void refresh(); }, 3000);
    const focus = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("focus", focus);
    return () => { window.clearInterval(timer); window.removeEventListener("focus", focus); };
  }, [refresh]);
  return { items: snapshot.id === conversationId ? snapshot.drafts : [], refresh, error };
}

export function MailDraftCard({ draft, bot, group, archived, onDemoAction, onChanged }: { draft: MailDraft; bot?: Bot; group: boolean; archived?: boolean; onDemoAction?: (action: "save" | "send" | "decline", fields: { to: string; subject: string; body: string }) => Promise<void>; onChanged: () => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [to, setTo] = useState(draft.to);
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState(draft.body);
  const [busy, setBusy] = useState<"save" | "send" | "decline" | "">("");
  const [animating, setAnimating] = useState(false);
  const letterRef = useRef<HTMLElement | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); }, [draft.revision, draft.to, draft.subject, draft.body]);
  const changed = to !== draft.to || subject !== draft.subject || body !== draft.body;
  async function act(action: "save" | "send" | "decline") {
    if (busy || archived || draft.status !== "pending") return;
    if (action === "send" && changed) { setError("草稿有未保存的修改，请先保存再发送。"); return; }
    setBusy(action); setError("");
    try {
      if (onDemoAction) {
        if (action === "send") setAnimating(true);
        await onDemoAction(action, { to, subject, body });
        if (action === "save") setEditing(false);
        if (action === "send" && letterRef.current) {
          try { await flyMailDraft(letterRef.current); } catch { /* The approved demo outcome stands even if motion fails. */ }
        }
        return;
      }
      if (action === "save") {
        await request(`/api/mail-drafts/${encodeURIComponent(draft.draft_id)}`, { method: "PATCH", body: JSON.stringify({ to, subject, body, revision: draft.revision }) });
        setEditing(false);
      } else {
        await request(`/api/mail-drafts/${encodeURIComponent(draft.draft_id)}/${action}`, { method: "POST", body: JSON.stringify({ revision: draft.revision }) });
      }
      if (action === "send" && letterRef.current) {
        setAnimating(true);
        try { await flyMailDraft(letterRef.current); } catch { /* A confirmed send must still update the card. */ } finally { setAnimating(false); }
      }
      await onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "操作失败，请刷新后查看状态。");
      if (action !== "save") void onChanged();
    } finally { setBusy(""); setAnimating(false); }
  }
  const pending = draft.status === "pending" || animating;
  const resolution = pending ? undefined : { label: draft.status === "sent" ? draft.demo ? "演示完成 · 未实际发送" : "已发送" : draft.status === "declined" ? "已取消" : draft.status === "unknown" ? "发送结果待核实" : "正在发送", accepted: draft.status === "sent" };
  return <article className={`mail-draft-message${group ? " is-group" : " is-dm"}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    {group && <div className="mail-draft-identity"><GazeAvatar id={draft.bot_id} mini /><strong>{bot?.name ?? "Bot"}</strong></div>}
    <ApprovalCard
      title={`要把这封邮件发给 ${draft.to} 吗？`}
      badge={draft.demo ? "演示 · 不会发送" : "需要你批准"}
      time={new Date(draft.created_at).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}
      facts={[{ label: "动作", value: `${draft.demo ? "演示发送" : "发送邮件"}「${draft.subject}」` }, { label: "对象", value: draft.to }, { label: "影响", value: draft.demo ? "只演示界面，不联系 Gmail" : "发出后不能撤回" }]}
      acceptLabel={draft.demo ? "演示发送" : "发送邮件"} declineLabel="先别发" onAnswer={accepted => void act(accepted ? "send" : "decline")}
      busy={busy === "send" ? "accept" : busy === "decline" ? "decline" : undefined}
      disabled={Boolean(archived || editing || changed || draft.status !== "pending")}
      resolution={resolution}
      error={error}
      note={draft.status === "unknown" ? "请先检查 Gmail 的已发送邮件；系统不会自动重发。" : onDemoAction ? "Motion Lab 演示数据，不会联系 Gmail。" : archived ? "恢复会话后可以处理草稿。" : editing ? "先保存修改，再批准发送。" : undefined}
      secondaryAction={pending && !editing ? <button type="button" disabled={Boolean(busy || archived)} onClick={() => setEditing(true)}>编辑草稿</button> : undefined}
    />
    {(pending || draft.status === "sending" || draft.status === "unknown") && <section ref={letterRef} className="mail-draft-letter" aria-label="邮件草稿全文">
      <div className="mail-draft-stripe" aria-hidden="true" />
      <div className="mail-draft-fields">
        <label>TO {editing ? <input type="text" value={to} onChange={event => setTo(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.to}</strong>}</label>
        <label>主题 {editing ? <input type="text" value={subject} onChange={event => setSubject(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.subject}</strong>}</label>
        <span className="mail-draft-stamp" aria-hidden="true"><GazeAvatar id={draft.bot_id} mini /></span>
      </div>
      <div className="mail-draft-paper">{editing ? <textarea aria-label="邮件正文" value={body} onChange={event => setBody(event.target.value)} disabled={Boolean(busy)} rows={8} /> : <p>{draft.body}</p>}</div>
      <footer>{editing ? <><button type="button" disabled={Boolean(busy)} onClick={() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); setEditing(false); setError(""); }}>放弃修改</button><button type="button" className="mail-draft-save" disabled={Boolean(busy || !changed)} onClick={() => void act("save")}>{busy === "save" ? "保存中…" : "保存草稿"}</button></> : <span>{draft.demo ? "演示草稿，不会实际发信" : "发送前请核对全文"}</span>}</footer>
    </section>}
  </article>;
}
