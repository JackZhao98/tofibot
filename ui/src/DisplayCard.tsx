import { useState } from "react";
import { MessageMarkdown } from "./MessageMarkdown";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { isDesktop } from "./desktop";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import { Button } from "./ds/Button/Button";
import { Card } from "./ds/Card/Card";
import { Letter } from "./ds/Letter/Letter";
import type { Message } from "./types";
import { receivedParts } from "./postmark";
import { useTranslation } from "./i18n";
import "./layout/mail.css";

/** Mail a Bot read, or text it shows: a kit Letter (in) or a quiet Card. The Letter's fold, note and postmark are the kit's. */
export function DisplayCard({ card, bot, onDraftReply }: { card: NonNullable<Message["card"]>; bot?: { id: string; name: string }; onDraftReply?: () => Promise<boolean> }) {
  const { t } = useTranslation("mail");
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [replyBusy, setReplyBusy] = useState(false);
  const [replyError, setReplyError] = useState("");
  const isMail = card.type === "mail";
  const received = receivedParts(card.received_at);
  const long = card.body.length > 440;
  async function copy() {
    try {
      await navigator.clipboard.writeText(card.body);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch { setCopied(false); }
  }
  const copyButton = <Button variant="ghost" size="sm" onClick={() => void copy()}>{isDesktop ? <TofiIcon name={copied ? "check" : "copy"} size={16} variant={copied ? "filled" : "outline"} /> : <CopyFeedbackIcon copied={copied} />}{copied ? t("display.copied") : t("display.copy")}</Button>;
  const source = card.source ? t("display.source", { source: card.source }) : isMail ? t("display.mail_content") : t("display.text_content");
  const reply = isMail && onDraftReply ? <Button variant="ghost" size="sm" disabled={replyBusy} onClick={() => { setReplyBusy(true); setReplyError(""); void onDraftReply().then(ok => { if (!ok) setReplyError(t("display.draft_failed")); }).catch(() => setReplyError(t("display.draft_failed"))).finally(() => setReplyBusy(false)); }}>{replyBusy ? t("display.drafting") : t("display.draft_reply")}</Button> : null;
  const error = replyError ? <p className="display-card-action-error" role="alert">{replyError}</p> : null;
  if (!isMail) return <div className="display-card-wrap"><Card variant="quiet" className="display-card" aria-label={card.title || t("display.text_content")} eyebrow={t("display.text_label")} title={card.title || t("display.content_fallback")} actions={copyButton} footer={<span className="display-card-source">{source}</span>}>
    <div className={`display-card-copy${!expanded && long ? " is-folded" : ""}`}><MessageMarkdown content={card.body} /></div>
    {long && <Button variant="ghost" size="sm" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? t("display.collapse") : t("display.expand")}</Button>}
    {error}
  </Card></div>;
  return <div className="display-card-wrap"><Letter className="display-card" label={t("display.mail_aria", { subject: card.subject })} from={card.from} to={card.to} subject={card.subject}
    postmark={card.received_at ? (received.date ? [t("display.postmark_received"), ...(received.time ? [received.time] : []), received.date] : [t("display.postmark_received"), card.received_at]) : undefined}
    note={card.summary ? { avatar: bot ? <BotAvatar id={bot.id} mini /> : undefined, title: t("display.note_from", { name: bot ? bot.name : "Bot" }), text: card.summary } : undefined}
    folded={long ? !expanded : undefined} more={long ? (expanded ? t("display.collapse") : t("display.expand")) : undefined} onMore={() => setExpanded(value => !value)}
    actions={<>{reply}{copyButton}</>} hint={source}>
    <MessageMarkdown content={card.body} />
  </Letter>{error}</div>;
}
