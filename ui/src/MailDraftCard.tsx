import { useEffect, useRef, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { Button } from "./ds/Button/Button";
import { DraftCard } from "./ds/DraftCard/DraftCard";
import { Letter } from "./ds/Letter/Letter";
import { TextArea } from "./ds/TextArea/TextArea";
import { TextInput } from "./ds/TextInput/TextInput";
import type { Bot } from "./types";
import { taskDraftLabel } from "./taskIssuePresentation";
import { useTranslation } from "./i18n";
import { formatClock } from "./i18n/format";
import "./layout/mail.css";
import type { MailDraft, MailDraftAttachment } from "./mailDrafts";
import { fileSize } from "./filePreviewModel";

export type { MailDraft, MailDraftAttachment } from "./mailDrafts";

function DraftAttachments({ items, label }: { items?: MailDraftAttachment[]; label: string }) {
  if (!items?.length) return null;
  return <span className="mail-draft-attachments" role="group" aria-label={label}>{items.map(item => <a key={item.id} href={`/api/attachments/${encodeURIComponent(item.id)}`} target="_blank" rel="noreferrer">{item.name} · {fileSize(item.size)}</a>)}</span>;
}

export function MailDraftCard({ draft, bot, group, archived, onDemoAction, onChanged, issueOwned = false }: { issueOwned?: boolean; draft: MailDraft; bot?: Bot; group: boolean; archived?: boolean; onDemoAction?: (action: "save" | "send" | "decline", fields: { to: string; subject: string; body: string }) => Promise<void>; onChanged: () => Promise<void> }) {
  const [editing, setEditing] = useState(false);
  const [to, setTo] = useState(draft.to);
  const [cc, setCc] = useState(draft.cc ?? "");
  const [bcc, setBcc] = useState(draft.bcc ?? "");
  const [subject, setSubject] = useState(draft.subject);
  const [body, setBody] = useState(draft.body);
  const [busy, setBusy] = useState<"save" | "send" | "decline" | "">("");
  // Only a send or cancel made in this view settles in; history renders still.
  const actedHere = useRef(false);
  const [error, setError] = useState("");
  const { t } = useTranslation("mail");
  const { t: tc } = useTranslation();
  useEffect(() => { setTo(draft.to); setCc(draft.cc ?? ""); setBcc(draft.bcc ?? ""); setSubject(draft.subject); setBody(draft.body); }, [draft.revision, draft.to, draft.cc, draft.bcc, draft.subject, draft.body]);
  const changed = to !== draft.to || cc !== (draft.cc ?? "") || bcc !== (draft.bcc ?? "") || subject !== draft.subject || body !== draft.body;
  async function act(action: "save" | "send" | "decline") {
    if (busy || archived || draft.status !== "pending") return;
    if (action === "send" && changed) { setError(t("draft.unsaved_changes")); return; }
    setBusy(action); setError("");
    if (action !== "save") actedHere.current = true;
    try {
      if (onDemoAction) {
        await onDemoAction(action, { to, subject, body });
        if (action === "save") setEditing(false);
        return;
      }
      if (action === "save") {
        await request(`/api/mail-drafts/${encodeURIComponent(draft.draft_id)}`, { method: "PATCH", body: JSON.stringify({ to, cc, bcc, subject, body, revision: draft.revision }) });
        setEditing(false);
      } else {
        await request(`/api/mail-drafts/${encodeURIComponent(draft.draft_id)}/${action}`, { method: "POST", body: JSON.stringify({ revision: draft.revision }) });
      }
      await onChanged();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("draft.action_failed"));
      if (action !== "save") void onChanged();
    } finally { setBusy(""); }
  }
  const pending = draft.status === "pending";
  const lock = Boolean(busy);
  if (draft.status === "sent" || draft.status === "declined") return <div className={`mail-draft-done is-${draft.status}${actedHere.current ? " is-arriving" : ""}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    <DraftCard state={draft.status === "sent" ? "sent" : "canceled"} label={taskDraftLabel(draft)} sentLabel={draft.demo ? t("draft.demo_done") : t("draft.sent")} canceledLabel={t("draft.declined")}
      receipt={`${taskDraftLabel(draft)} · ${t(draft.status === "declined" ? "draft.declined_at" : "draft.sent_at", { time: formatClock(draft.updated_at) })}`}
      after={<DraftAttachments items={draft.attachments} label={t("draft.attachments")} />}>
      <b>{draft.subject}</b> · {draft.to}
    </DraftCard>
  </div>;
  return <article className={`mail-draft-message${group ? " is-group" : " is-dm"}`} data-draft-id={draft.draft_id} tabIndex={-1}>
    {group && <div className="mail-draft-identity"><BotAvatar id={draft.bot_id} mini /><strong>{bot?.name ?? "Bot"}</strong></div>}
    {draft.status === "unknown" && <p className="task-record-meta">{taskDraftLabel(draft)}: {t("draft.sending_needs_checking")}</p>}
    {(pending || draft.status === "sending" || draft.status === "unknown") && <Letter direction="out" className="mail-draft-letter" label={t("draft.full_text")}
      to={editing ? <TextInput aria-label={tc("ds.letter.to")} value={to} placeholder={t("draft.add_recipient")} onChange={event => setTo(event.target.value)} disabled={lock} /> : draft.to || <span className="mail-draft-missing">{t("draft.add_recipient")}</span>}
      cc={editing ? <TextInput aria-label="CC" value={cc} placeholder={t("draft.add_cc")} onChange={event => setCc(event.target.value)} disabled={lock} /> : draft.cc || undefined}
      bcc={editing ? <TextInput aria-label="BCC" value={bcc} placeholder={t("draft.add_bcc")} onChange={event => setBcc(event.target.value)} disabled={lock} /> : draft.bcc || undefined}
      subject={editing ? <TextInput aria-label={t("draft.subject")} value={subject} onChange={event => setSubject(event.target.value)} disabled={lock} /> : draft.subject}
      subjectLabel={t("draft.subject")}
      stamp={<BotAvatar id={draft.bot_id} bare />}
      attachment={draft.attachments?.length ? <DraftAttachments items={draft.attachments} label={t("draft.attachments")} /> : undefined}
      actions={editing ? <Button variant="ghost" size="sm" disabled={lock} onClick={() => { setTo(draft.to); setCc(draft.cc ?? ""); setBcc(draft.bcc ?? ""); setSubject(draft.subject); setBody(draft.body); setEditing(false); setError(""); }}>{t("draft.discard")}</Button>
        : pending ? <><Button variant="ghost" size="sm" icon="edit" disabled={lock || Boolean(archived)} onClick={() => setEditing(true)}>{t("draft.edit")}</Button><Button variant="ghost" size="sm" disabled={lock || Boolean(archived)} onClick={() => void act("decline")}>{busy === "decline" ? t("draft.working") : t("draft.hold")}</Button></> : undefined}
      hint={editing ? undefined : pending ? (draft.demo ? <><b>{t("draft.demo_badge")}</b> {t("draft.demo_hint")}</> : undefined) : draft.status === "unknown" ? t("draft.sending_needs_checking") : t("draft.sending_now")}
      primary={editing ? <Button variant="primary" size="sm" disabled={lock || !changed} busy={busy === "save"} onClick={() => void act("save")}>{busy === "save" ? t("draft.saving") : t("draft.save")}</Button>
        : pending ? <Button variant="primary" size="sm" icon="arrow-up" disabled={lock || Boolean(archived) || changed || !draft.to.trim()} busy={busy === "send"} onClick={() => void act("send")}>{busy === "send" ? t("draft.sending") : draft.demo ? t("draft.demo_send") : t("draft.send")}</Button> : undefined}>
      {editing ? <TextArea aria-label={t("draft.body")} value={body} onChange={event => setBody(event.target.value)} disabled={lock} rows={8} /> : <p className="mail-draft-body">{draft.body}</p>}
    </Letter>}
    {(error || onDemoAction || archived) && <p className={error ? "mail-draft-error" : "mail-draft-note"} role={error ? "alert" : undefined}>{error || (onDemoAction ? t("draft.motion_lab_note") : t("draft.restore_to_act"))}</p>}
  </article>;
}
