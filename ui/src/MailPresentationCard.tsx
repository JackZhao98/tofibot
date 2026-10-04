import { useEffect, useId, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { useUserTimezone } from "./UserTimezone";
import { TofiIcon } from "./icons";
import type { MailDetail, Message, PresentedEmail } from "./types";
import "./mail-presentation.css";

// Mail bodies are source text, never HTML or Markdown. External content is only
// exposed as deliberate HTTPS links: no inline images, previews or tracking loads.
function safeLink(value?: string) {
  if (!value || /[\u0000-\u0020]/.test(value)) return undefined;
  try { const url = new URL(value); return url.protocol === "https:" && !url.username && !url.password ? url.href : undefined; } catch { return undefined; }
}
function mailTime(value: string, timezone: string, date = false) {
  const parsed = new Date(value);
  return Number.isFinite(parsed.getTime()) ? new Intl.DateTimeFormat("zh-CN", { timeZone: timezone, ...(date ? { month: "numeric", day: "numeric" } : { hour: "2-digit", minute: "2-digit", hourCycle: "h23" }) }).format(parsed) : value;
}
function IncomingMail({ detail, botName, onClose }: { detail: MailDetail; botName: string; onClose: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const email = detail.email;
  const { timezone } = useUserTimezone();
  const sourceURL = safeLink(detail.source_url);
  const folded = detail.body.length > 440;
  return <article className="incoming-mail" aria-label={`邮件：${email.subject}`}>
    <div className="incoming-mail-stripe" aria-hidden="true" />
    <header className="incoming-mail-head">
      <span>FROM</span><strong><span className="incoming-mail-monogram" aria-hidden="true">{Array.from(email.from)[0]}</span><bdi>{email.from}</bdi></strong>
      {email.to && <><span>TO</span><strong><bdi>{email.to}</bdi></strong></>}
      <span>主题</span><strong>{email.subject}</strong>
      {email.received_at && <time className="incoming-mail-postmark" dateTime={email.received_at} title={`${email.received_at} · ${timezone}`}>已收到<br />{mailTime(email.received_at, timezone)}<br />{mailTime(email.received_at, timezone, true)}</time>}
    </header>
    {email.summary && <aside className="incoming-mail-note"><strong><TofiIcon name="bot-chat" size={18} />{botName} 的便签</strong><p>{email.summary}</p></aside>}
    {detail.body_available ? <>
      <div className={`incoming-mail-body${folded && !expanded ? " is-folded" : ""}`}><p>{detail.body}</p></div>
      {folded && <button className="mail-text-button incoming-mail-expand" type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><TofiIcon name={expanded ? "arrow-up" : "chevron-down"} size={16} />{expanded ? "收起全文" : "展开全文"}{detail.attachments.length > 0 && ` · 附件 ${detail.attachments.length}`}</button>}
    </> : <p className="incoming-mail-unavailable" role="status">这次工具结果只返回了邮件摘要，尚未返回正文。需要正文时，请让 Bot 单独读取这封邮件。</p>}
    {detail.attachments.length > 0 && <ul className="incoming-mail-attachments" aria-label="邮件附件">{detail.attachments.map((attachment, index) => {
      const href = safeLink(attachment.url);
      return <li key={`${index}:${attachment.name}`}><TofiIcon name="file" size={16} />{href ? <a href={href} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">{attachment.name}</a> : <span>{attachment.name} · 未返回可打开的链接</span>}</li>;
    })}</ul>}
    <footer className="incoming-mail-footer"><div><span>来源 · <bdi>{email.provider}</bdi>{email.account && <> · <bdi>{email.account}</bdi></>}</span><small>邮件 ID · <bdi>{email.message_id}</bdi> · 读取于 {mailTime(email.retrieved_at, timezone)}</small></div>{sourceURL && <a href={sourceURL} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">打开原邮件<TofiIcon name="external-link" size={16} /></a>}<button className="mail-text-button" type="button" onClick={onClose}>收起邮件</button></footer>
  </article>;
}

export function MailPresentationCard({ message, botName = "Bot" }: { message: Message; botName?: string }) {
  const presentation = message.card?.mail;
  const { timezone } = useUserTimezone();
  const [selectedKey, setSelectedKey] = useState(presentation?.selected_key ?? "");
  const [detail, setDetail] = useState<MailDetail | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const generation = useRef(0);
  const regionId = useId();
  const buttonRefs = useRef(new Map<string, HTMLButtonElement>());
  const selected = presentation?.emails.find(email => email.key === selectedKey);

  useEffect(() => {
    const token = ++generation.current;
    const controller = new AbortController();
    setDetail(null); setError(""); setLoading(Boolean(selected));
    if (selected && presentation) {
      void api.presentedEmail(message.conversation_id, message.id, selected.key, presentation.revision, controller.signal).then(value => {
        if (controller.signal.aborted || token !== generation.current) return;
        if (value.email.key !== selected.key || value.email.source.digest !== selected.source.digest) throw new Error("stale identity");
        setDetail(value);
      }).catch(cause => {
        if (controller.signal.aborted || token !== generation.current) return;
        setError(cause instanceof ApiError && cause.status === 409 ? "邮件展示已更新，请重新载入会话后打开。" : "这封邮件暂时无法打开。请重试；若仍无法打开，请让 Bot 重新展示已读取的邮件。");
      }).finally(() => { if (!controller.signal.aborted && token === generation.current) setLoading(false); });
    }
    return () => { controller.abort(); };
  }, [message.conversation_id, message.id, presentation, selected, attempt]);

  if (!presentation) return null;
  function open(email: PresentedEmail) {
    if (selectedKey === email.key) return;
    // Clear the previous message synchronously; it must never appear under a new subject.
    setDetail(null); setError(""); setLoading(true); setSelectedKey(email.key);
  }
  function close() { const key = selectedKey; setSelectedKey(""); setDetail(null); setError(""); buttonRefs.current.get(key)?.focus(); }
  return <section className="mail-presentation" aria-label={message.card?.title || "邮件列表"} onKeyDown={event => { if (event.key === "Escape" && selectedKey) { event.stopPropagation(); close(); } }}>
    {message.card?.title && <h3>{message.card.title}</h3>}
    {presentation.emails.length === 0 ? <p className="mail-empty" role="status"><TofiIcon name="inbox" size={22} />暂无已展示的邮件。</p> : <ul className="mail-tray" aria-label="已读取的邮件">{presentation.emails.map(email => <li key={email.key} className={email.priority ? "is-priority" : ""}>
      <button ref={node => { if (node) buttonRefs.current.set(email.key, node); else buttonRefs.current.delete(email.key); }} type="button" className={`mail-envelope${selectedKey === email.key ? " is-selected" : ""}`} aria-expanded={selectedKey === email.key} aria-controls={regionId} aria-describedby={email.summary ? `${regionId}-${email.key}` : undefined} onClick={() => open(email)}>
        <strong className="mail-envelope-from"><bdi>{email.from}</bdi></strong><span className="mail-envelope-subject">{email.subject}</span>{email.tag && <span className={`mail-envelope-tag${email.priority ? " is-urgent" : ""}`}>{email.tag}</span>}{email.received_at && <time className="mail-envelope-time" dateTime={email.received_at} title={`${email.received_at} · ${timezone}`}>{mailTime(email.received_at, timezone)}</time>}
      </button>{email.summary && <span className="mail-sr-only" id={`${regionId}-${email.key}`}>Bot 摘要：{email.summary}</span>}
    </li>)}</ul>}
    <div id={regionId} className="mail-selection" aria-busy={loading} onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); close(); } }}>
      {loading && <p className="mail-loading" role="status"><TofiIcon name="inbox" size={18} />正在打开已展示的邮件…</p>}
      {error && <div className="mail-open-error" role="alert"><p>{error}</p><button className="mail-text-button" type="button" onClick={() => setAttempt(value => value + 1)}>重试打开</button><button className="mail-text-button" type="button" onClick={() => location.reload()}>重新载入会话</button><button className="mail-text-button" type="button" onClick={close}>收起</button></div>}
      {detail && <IncomingMail key={detail.email.key} detail={detail} botName={botName} onClose={close} />}
      <span className="mail-sr-only" role="status">{detail ? `已打开邮件：${detail.email.subject}` : ""}</span>
    </div>
  </section>;
}
