import { memo, useEffect, useRef, useState } from "react";
import * as Toolbar from "@radix-ui/react-toolbar";
import { DismissableLayer } from "@radix-ui/react-dismissable-layer";
import { TofiIcon as Icon } from "./icons";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { SendReceiptMark } from "./SendReceipt";
import { dateInTimezone, dateTimeFormat, formatExactTime } from "./timezone";
import { useTranslation } from "./i18n";
import { intlLocale, formatRelativeTime } from "./i18n/format";
import { tappedMessageActionsId, setMessageActions } from "./messageActionsStore";
import type { Message } from "./types";
function formatHoverTime(value: string, timeZone: string) {
  const sameDay = dateInTimezone(value, timeZone) === dateInTimezone(new Date().toISOString(), timeZone);
  return dateTimeFormat(intlLocale(), { ...(sameDay ? {} : { month: "numeric", day: "numeric" }), hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}

/** The bar's quiet timestamp: relative while it is recent, the clock time once it is old. The exact time is the tooltip. */
function formatBarTime(value: string, timeZone: string, justNow: string) {
  const age = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(age)) return formatHoverTime(value, timeZone);
  if (age < 45_000) return justNow;
  return age < 7 * 24 * 3600_000 ? formatRelativeTime(value) : formatHoverTime(value, timeZone);
}

async function writeClipboard(text: string) {
  try { await navigator.clipboard.writeText(text); return true; } catch { /* LAN HTTP and blocked permissions have no async clipboard */ }
  const field = document.createElement("textarea");
  field.value = text; field.setAttribute("readonly", ""); field.style.cssText = "position:fixed;top:0;left:0;opacity:0";
  document.body.appendChild(field); field.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  field.remove();
  return ok;
}

/** Browser-mode message actions, mounted only for the message the pointer, focus or a tap is on. In the entry: the first hover
 * must not wait for a download. Radix Toolbar gives one tab stop and arrow keys; DismissableLayer closes the touch bar. */
export const MessageHoverActions = memo(function MessageHoverActions({ message, timezone, onReact, onReply, onActivity, activityOpen, copyText, mark }: { message: Message; timezone: string; onReact: (anchor: DOMRect) => void; onReply?: () => void; onActivity?: () => void; activityOpen?: boolean; copyText?: string; mark?: { state: "delivered" | "read"; names: string[]; group: boolean } }) {
  const { t } = useTranslation("chat");
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(copiedTimer.current), []);
  async function copy() {
    if (!copyText || !await writeClipboard(copyText)) return;
    setCopied(true);
    window.clearTimeout(copiedTimer.current);
    copiedTimer.current = window.setTimeout(() => setCopied(false), 1200);
  }
  return <DismissableLayer asChild onPointerDownOutside={event => {
    const host = (event.detail.originalEvent.target as HTMLElement).closest?.("[data-host-id]");
    if (host?.getAttribute("data-host-id") === message.id) event.preventDefault();
    else if (tappedMessageActionsId() === message.id) setMessageActions("tap", "");
  }} onEscapeKeyDown={() => { if (tappedMessageActionsId() === message.id) setMessageActions("tap", ""); }}>
  <Toolbar.Root className="message-hover-actions" aria-label={t("hover.toolbar")} loop>
    {copyText && <Toolbar.Button type="button" aria-label={copied ? t("hover.copied") : t("hover.copy")} data-hint={copied ? t("hover.copied") : t("hover.copy")} data-action="copy" onClick={() => void copy()}><CopyFeedbackIcon copied={copied} size={15} /></Toolbar.Button>}
    {message.role !== "user" && <Toolbar.Button type="button" aria-label={t("hover.react")} data-hint={t("hover.react")} data-action="react" onClick={event => onReact(event.currentTarget.getBoundingClientRect())}><Icon name="emoji" size={15} /></Toolbar.Button>}
    {onReply && <Toolbar.Button type="button" aria-label={t("hover.reply")} data-hint={t("hover.reply")} data-action="reply" onClick={onReply}><Icon name="reply" size={15} /></Toolbar.Button>}
    {onActivity && <Toolbar.Button type="button" className="message-activity-action" aria-label={activityOpen ? t("hover.hide_activity") : t("hover.view_activity")} aria-pressed={activityOpen} data-hint={activityOpen ? t("hover.hide_activity") : t("hover.view_activity")} data-action="activity" onClick={onActivity}><Icon name="activity" size={15} /></Toolbar.Button>}
    <time dateTime={message.created_at} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}>{formatBarTime(message.created_at, timezone, t("hover.just_now"))}</time>
    {mark && <SendReceiptMark state={mark.state} names={mark.names} group={mark.group} />}
  </Toolbar.Root></DismissableLayer>;
}, (previous, next) => previous.message === next.message && previous.timezone === next.timezone && previous.activityOpen === next.activityOpen && previous.copyText === next.copyText
  && previous.mark?.state === next.mark?.state && previous.mark?.group === next.mark?.group && previous.mark?.names.join("\u0000") === next.mark?.names.join("\u0000")
  // The handlers are rebuilt every render; they act on the same message, so only their presence matters here.
  && Boolean(previous.onReply) === Boolean(next.onReply) && Boolean(previous.onActivity) === Boolean(next.onActivity));

