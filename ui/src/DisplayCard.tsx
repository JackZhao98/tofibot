import { useState } from "react";
import { MessageMarkdown } from "./MessageMarkdown";
import { TofiIcon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { isDesktop } from "./desktop";
import type { Message } from "./types";
import "./display-card.css";

export function DisplayCard({ card, onDraftReply }: { card: NonNullable<Message["card"]>; onDraftReply?: () => Promise<boolean> }) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);
  const [replyBusy, setReplyBusy] = useState(false);
  const [replyError, setReplyError] = useState("");
  const isMail = card.type === "mail";
  const long = card.body.length > 440;
  async function copy() {
    try {
      await navigator.clipboard.writeText(card.body);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch { setCopied(false); }
  }
  return <section className={`display-card ${isMail ? "display-card-mail" : "display-card-text"}`} aria-label={isMail ? `邮件：${card.subject} · 历史邮件卡片 · 来源未核验` : card.title || "文本内容"}>
    {isMail ? <>
      <div className="display-card-poststripe" aria-hidden="true" />
      <aside className="display-card-note"><strong>历史邮件卡片 · 来源未核验</strong></aside>
      <div className="display-card-mailhead">
        <span>FROM</span><strong>{card.from}</strong>
        {card.to && <><span>TO</span><strong>{card.to}</strong></>}
        <span>主题</span><strong>{card.subject}</strong>
        {card.received_at && <time className="display-card-postmark">已收到<br />{card.received_at}</time>}
      </div>
      {card.summary && <aside className="display-card-note"><strong>Bot 的便签</strong><span>{card.summary}</span></aside>}
    </> : <header className="display-card-texthead"><span>文本</span><strong>{card.title || "内容"}</strong></header>}
    <div className={`display-card-copy${!expanded && long ? " is-folded" : ""}`}><MessageMarkdown content={card.body} /></div>
    {long && <button className="display-card-expand" type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? "收起内容" : "展开全文"}</button>}
    <footer className="display-card-footer"><span>{card.source ? `${isMail ? "Bot 提供的来源" : "来源"} · ${card.source}` : isMail ? "邮件内容" : "文本内容"}</span>{isMail && onDraftReply && <button type="button" disabled={replyBusy} onClick={() => { setReplyBusy(true); setReplyError(""); void onDraftReply().then(ok => { if (!ok) setReplyError("起草请求未送达，请重试。"); }).catch(() => setReplyError("起草请求未送达，请重试。")).finally(() => setReplyBusy(false)); }}>{replyBusy ? "正在起草…" : "让 Bot 起草回复"}</button>}<button type="button" onClick={() => void copy()}>{isDesktop ? <TofiIcon name={copied ? "check" : "copy"} size={16} variant={copied ? "filled" : "outline"} /> : <CopyFeedbackIcon copied={copied} />}{copied ? "已复制" : "复制"}</button></footer>
    {replyError && <p className="display-card-action-error" role="alert">{replyError}</p>}
  </section>;
}
