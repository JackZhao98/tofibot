import { useState } from "react";
import { MessageMarkdown } from "./MessageMarkdown";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { isDesktop } from "./desktop";
import { BotAvatar } from "./BotAvatar";
import type { Message } from "./types";
import "./display-card.css";
import { postmarkSVG, receivedParts } from "./postmark";
import { useTranslation } from "./i18n";

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
  return <section className={`display-card ${isMail ? "display-card-mail" : "display-card-text"}`} aria-label={isMail ? t("display.mail_aria", { subject: card.subject }) : card.title || t("display.text_content")}>
    {isMail ? <>
      <div className="display-card-poststripe" aria-hidden="true" />
      <div className="display-card-mailhead">
        <span>FROM</span><strong>{card.from}</strong>
        {card.to && <><span>TO</span><strong>{card.to}</strong></>}
        <span>{t("display.subject")}</span><strong>{card.subject}</strong>
        {card.received_at && !received.date && <><span>{t("display.received_label")}</span><strong>{card.received_at}</strong></>}
        {card.received_at && <span className="display-card-postmark" role="img" aria-label={t("display.received_aria", { time: card.received_at })} dangerouslySetInnerHTML={{ __html: postmarkSVG({ ring: t("display.postmark_ring"), date: received.date ?? t("display.postmark_received"), time: received.date ? received.time : undefined }) }} />}
      </div>
      {card.summary && <aside className="display-card-note"><strong>{bot && <BotAvatar id={bot.id} mini />}{t("display.note_from", { name: bot ? bot.name : "Bot" })}</strong><span>{card.summary}</span></aside>}
    </> : <header className="display-card-texthead"><span>{t("display.text_label")}</span><strong>{card.title || t("display.content_fallback")}</strong></header>}
    <div className={`display-card-copy${!expanded && long ? " is-folded" : ""}`}><MessageMarkdown content={card.body} /></div>
    {long && <button className="display-card-expand" type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? t("display.collapse") : t("display.expand")}</button>}
    <footer className="display-card-footer"><span>{card.source ? t("display.source", { source: card.source }) : isMail ? t("display.mail_content") : t("display.text_content")}</span>{isMail && onDraftReply && <button type="button" disabled={replyBusy} onClick={() => { setReplyBusy(true); setReplyError(""); void onDraftReply().then(ok => { if (!ok) setReplyError(t("display.draft_failed")); }).catch(() => setReplyError(t("display.draft_failed"))).finally(() => setReplyBusy(false)); }}>{replyBusy ? t("display.drafting") : t("display.draft_reply")}</button>}<button type="button" onClick={() => void copy()}>{isDesktop ? <TofiIcon name={copied ? "check" : "copy"} size={16} variant={copied ? "filled" : "outline"} /> : <CopyFeedbackIcon copied={copied} />}{copied ? t("display.copied") : t("display.copy")}</button></footer>
    {replyError && <p className="display-card-action-error" role="alert">{replyError}</p>}
  </section>;
}
