import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import type { Bot } from "./types";
import { flyMailDraft } from "./mailSendFlight";
import { TaskIssueCard } from "./TaskIssueCard";
import { presentTaskIssue, taskDraftLabel, taskLocale, taskText } from "./taskIssuePresentation";
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

export function MailDraftCard({ draft, bot, group, archived, onDemoAction, onChanged, issueOwned = false }: { issueOwned?: boolean; draft: MailDraft; bot?: Bot; group: boolean; archived?: boolean; onDemoAction?: (action: "save" | "send" | "decline", fields: { to: string; subject: string; body: string }) => Promise<void>; onChanged: () => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [to, setTo] = useState(draft.to);
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState(draft.body);
  const [busy, setBusy] = useState<"save" | "send" | "decline" | "">("");
  const [animating, setAnimating] = useState(false);
  const letterRef = useRef<HTMLElement | null>(null);
  // Only a send or cancel made in this view settles in; history renders still.
  const actedHere = useRef(false);
  const [error, setError] = useState("");
  useEffect(() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); }, [draft.revision, draft.to, draft.subject, draft.body]);
  const changed = to !== draft.to || subject !== draft.subject || body !== draft.body;
  async function act(action: "save" | "send" | "decline") {
    if (busy || archived || draft.status !== "pending") return;
    if (action === "send" && changed) { setError("草稿有未保存的修改，请先保存再发送。"); return; }
    setBusy(action); setError("");
    if (action !== "save") actedHere.current = true;
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
  if (draft.status === "sent" || draft.status === "declined") return <article className={`task-mail-record mail-draft-done is-${draft.status}${actedHere.current ? " is-arriving" : ""}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    <p><span className="mail-draft-done-dot" aria-hidden="true" />{taskDraftLabel(draft)}: {taskText(taskLocale(), draft.status === "declined" ? "已取消发送" : draft.demo ? "演示完成，未实际发送" : "邮件已发送", draft.status === "declined" ? "Sending was cancelled" : draft.demo ? "Demo completed; no email was sent" : "Email was sent")}<time>{taskText(taskLocale(), draft.status === "declined" ? "取消于" : "发送于", draft.status === "declined" ? "Cancelled" : "Sent")} {new Date(draft.updated_at).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hourCycle: "h23" })}</time></p>
    <details><summary>{taskText(taskLocale(), "查看草稿记录", "View draft record")}</summary><dl><dt>TO</dt><dd>{draft.to}</dd><dt>{taskText(taskLocale(), "主题", "Subject")}</dt><dd>{draft.subject}</dd></dl><p>{draft.body}</p></details>
  </article>;
  return <article className={`mail-draft-message${group ? " is-group" : " is-dm"}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    {group && <div className="mail-draft-identity"><BotAvatar id={draft.bot_id} mini /><strong>{bot?.name ?? "Bot"}</strong></div>}
    {draft.status === "unknown" && !issueOwned && <TaskIssueCard issue={presentTaskIssue({run:{id:draft.run_id,conversation_id:draft.conversation_id,bot_id:draft.bot_id,status:"done",created_at:draft.created_at,updated_at:draft.updated_at},drafts:[draft],locale:taskLocale()})!} />}
    {draft.status === "unknown" && !issueOwned && <p className="task-issue-fact">{taskText(taskLocale(), "请先检查邮件服务的已发送记录，核对时间、收件人与主题。", "Check sent-mail records in your email service and compare the time, recipient, and subject.")}</p>}
    {draft.status === "unknown" && <p className="task-record-meta">{taskDraftLabel(draft)}: {taskText(taskLocale(), "发送结果待核实", "Sending result needs checking")}</p>}
    {(pending || draft.status === "sending" || draft.status === "unknown") && <section ref={letterRef} className={`mail-draft-letter${pending && !animating ? " is-pending" : ""}`} aria-label="邮件草稿全文">
      <div className="mail-draft-stripe" aria-hidden="true" />
      <div className="mail-draft-fields">
        <label>TO {editing ? <input type="text" value={to} onChange={event => setTo(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.to}</strong>}</label>
        <label>主题 {editing ? <input type="text" value={subject} onChange={event => setSubject(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.subject}</strong>}</label>
        <span className="mail-draft-stamp" aria-hidden="true"><BotAvatar id={draft.bot_id} mini /></span>
      </div>
      <div className="mail-draft-paper">{editing ? <textarea aria-label="邮件正文" value={body} onChange={event => setBody(event.target.value)} disabled={Boolean(busy)} rows={8} /> : <p>{draft.body}</p>}</div>
      <footer>{editing ? <><button type="button" disabled={Boolean(busy)} onClick={() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); setEditing(false); setError(""); }}>放弃修改</button><button type="button" className="mail-draft-save" disabled={Boolean(busy || !changed)} onClick={() => void act("save")}>{busy === "save" ? "保存中…" : "保存草稿"}</button></>
        : draft.status === "pending" ? <>
          <button type="button" className="mail-draft-text-action" disabled={Boolean(busy || archived)} onClick={() => setEditing(true)}><TofiIcon name="edit" size={15} aria-hidden="true" />编辑</button>
          <span className="mail-draft-hint"><b>{draft.demo ? "演示 · 不会发送" : "需要你批准"}</b>{draft.demo ? "只演示界面，不联系 Gmail" : "发出后不能撤回"}</span>
          <button type="button" className="mail-draft-decline" disabled={Boolean(busy || archived)} onClick={() => void act("decline")}>{busy === "decline" ? "处理中…" : "先别发"}</button>
          <button type="button" className="mail-draft-send" disabled={Boolean(busy || archived || changed)} onClick={() => void act("send")}>{busy === "send" ? "发送中…" : draft.demo ? "演示发送" : "发送邮件"}</button>
        </>
        : <span>{draft.status === "unknown" ? taskText(taskLocale(), "发送结果待核实", "Sending result needs checking") : "正在发送…"}</span>}</footer>
      {(error || onDemoAction || archived) && <p className={error ? "mail-draft-error" : "mail-draft-note"} role={error ? "alert" : undefined}>{error || (onDemoAction ? "Motion Lab 演示数据，不会联系 Gmail。" : "恢复会话后可以处理草稿。")}</p>}
    </section>}
  </article>;
}
