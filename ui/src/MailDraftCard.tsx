import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import type { Bot } from "./types";
import { flyMailDraft } from "./mailSendFlight";
import { TaskIssueCard } from "./TaskIssueCard";
import { presentTaskIssue, taskDraftLabel } from "./taskIssuePresentation";
import { i18n, useTranslation } from "./i18n";
import { formatClock } from "./i18n/format";
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
    } catch (cause) { setError(cause instanceof Error ? cause.message : i18n.t("mail:draft.load_failed")); }
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
  const { t } = useTranslation("mail");
  useEffect(() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); }, [draft.revision, draft.to, draft.subject, draft.body]);
  const changed = to !== draft.to || subject !== draft.subject || body !== draft.body;
  async function act(action: "save" | "send" | "decline") {
    if (busy || archived || draft.status !== "pending") return;
    if (action === "send" && changed) { setError(t("draft.unsaved_changes")); return; }
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
      setError(cause instanceof Error ? cause.message : t("draft.action_failed"));
      if (action !== "save") void onChanged();
    } finally { setBusy(""); setAnimating(false); }
  }
  const pending = draft.status === "pending" || animating;
  if (draft.status === "sent" || draft.status === "declined") return <article className={`task-mail-record mail-draft-done is-${draft.status}${actedHere.current ? " is-arriving" : ""}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    <p><span className="mail-draft-done-dot" aria-hidden="true" />{taskDraftLabel(draft)}: {draft.status === "declined" ? t("draft.declined") : draft.demo ? t("draft.demo_done") : t("draft.sent")}<time>{t(draft.status === "declined" ? "draft.declined_at" : "draft.sent_at", { time: formatClock(draft.updated_at) })}</time></p>
    <details><summary>{t("draft.view_record")}</summary><dl><dt>TO</dt><dd>{draft.to}</dd><dt>{t("draft.subject")}</dt><dd>{draft.subject}</dd></dl><p>{draft.body}</p></details>
  </article>;
  return <article className={`mail-draft-message${group ? " is-group" : " is-dm"}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    {group && <div className="mail-draft-identity"><BotAvatar id={draft.bot_id} mini /><strong>{bot?.name ?? "Bot"}</strong></div>}
    {draft.status === "unknown" && !issueOwned && <TaskIssueCard issue={presentTaskIssue({run:{id:draft.run_id,conversation_id:draft.conversation_id,bot_id:draft.bot_id,status:"done",created_at:draft.created_at,updated_at:draft.updated_at},drafts:[draft]})!} />}
    {draft.status === "unknown" && !issueOwned && <p className="task-issue-fact">{t("draft.check_sent_records")}</p>}
    {draft.status === "unknown" && <p className="task-record-meta">{taskDraftLabel(draft)}: {t("draft.sending_needs_checking")}</p>}
    {(pending || draft.status === "sending" || draft.status === "unknown") && <section ref={letterRef} className={`mail-draft-letter${pending && !animating ? " is-pending" : ""}`} aria-label={t("draft.full_text")}>
      <div className="mail-draft-stripe" aria-hidden="true" />
      <div className="mail-draft-fields">
        <label>TO {editing ? <input type="text" value={to} onChange={event => setTo(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.to}</strong>}</label>
        <label>{t("draft.subject")} {editing ? <input type="text" value={subject} onChange={event => setSubject(event.target.value)} disabled={Boolean(busy)} /> : <strong>{draft.subject}</strong>}</label>
        <span className="mail-draft-stamp" aria-hidden="true"><BotAvatar id={draft.bot_id} mini /></span>
      </div>
      <div className="mail-draft-paper">{editing ? <textarea aria-label={t("draft.body")} value={body} onChange={event => setBody(event.target.value)} disabled={Boolean(busy)} rows={8} /> : <p>{draft.body}</p>}</div>
      <footer>{editing ? <><button type="button" disabled={Boolean(busy)} onClick={() => { setTo(draft.to); setSubject(draft.subject); setBody(draft.body); setEditing(false); setError(""); }}>{t("draft.discard")}</button><button type="button" className="mail-draft-save" disabled={Boolean(busy || !changed)} onClick={() => void act("save")}>{busy === "save" ? t("draft.saving") : t("draft.save")}</button></>
        : draft.status === "pending" ? <>
          <button type="button" className="mail-draft-text-action" disabled={Boolean(busy || archived)} onClick={() => setEditing(true)}><TofiIcon name="edit" size={15} aria-hidden="true" />{t("draft.edit")}</button>
          <span className="mail-draft-hint"><b>{draft.demo ? t("draft.demo_badge") : t("draft.needs_approval")}</b>{draft.demo ? t("draft.demo_hint") : t("draft.send_final")}</span>
          <button type="button" className="mail-draft-decline" disabled={Boolean(busy || archived)} onClick={() => void act("decline")}>{busy === "decline" ? t("draft.working") : t("draft.hold")}</button>
          <button type="button" className="mail-draft-send" disabled={Boolean(busy || archived || changed)} onClick={() => void act("send")}>{busy === "send" ? t("draft.sending") : draft.demo ? t("draft.demo_send") : t("draft.send")}</button>
        </>
        : <span>{draft.status === "unknown" ? t("draft.sending_needs_checking") : t("draft.sending_now")}</span>}</footer>
      {(error || onDemoAction || archived) && <p className={error ? "mail-draft-error" : "mail-draft-note"} role={error ? "alert" : undefined}>{error || (onDemoAction ? t("draft.motion_lab_note") : t("draft.restore_to_act"))}</p>}
    </section>}
  </article>;
}
