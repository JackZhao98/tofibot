import { useEffect, useRef, useState } from "react";
import { useTranslation } from "./i18n";
import { formatList } from "./i18n/format";
import type { Receipt } from "./sendReceipts";
import type { DeliveryState } from "./messageDelivery";
import "./send-receipt.css";

const tick = "M3 8.6l3.1 3.1L13 4.8";
// Two overlapping ticks, the second shifted right: read.
const tickPair = ["M0.6 8.8l2.9 2.9L9 5.4", "M5.6 8.8l2.9 2.9L14.2 5.4"];

function Mark({ state }: { state: Receipt["state"] }) {
  if (state === "sending") return <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><circle cx="8" cy="8" r="5.6" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M8 4.8V8l2.2 1.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>;
  return <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">{(state === "read" ? tickPair : [tick]).map(d => <path key={d} d={d} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />)}</svg>;
}

/**
 * Status of one of the person's own messages. Delivered and read are quiet: they show only in the hover/focus/tap action row,
 * as a mark at its end next to the time (SendReceiptMark), and own no row of their own. The two states that must be seen without
 * asking stay on the bubble: sending (the bubble is dimmed, the words are for assistive tech) and failed (the retry line).
 */
export function SendReceipt({ receipt, onRetry }: { receipt: Receipt; names?: string[]; group?: boolean; onRetry?: () => void }) {
  const { t } = useTranslation("chat");
  if (receipt.state === "failed") {
    return <div className="send-receipt" data-state="failed"><button type="button" className="send-receipt-retry" title={receipt.error} aria-label={`${t("receipt.failed")}. ${t("receipt.retry_label")}`} onClick={onRetry}>{t("receipt.failed")} · {t("receipt.retry")}</button></div>;
  }
  if (receipt.state === "sending") return <span className="sr-only send-receipt-status" data-state="sending" role="status">{t("receipt.sending")}</span>;
  return null;
}

/** The delivered / read mark at the end of the action row. A group's read-by names are its hover/focus hint (ActionHints, quiet tone). */
export function SendReceiptMark({ state, names, group }: { state: "delivered" | "read"; names: string[]; group: boolean }) {
  const { t } = useTranslation("chat");
  const previous = useRef(state);
  const [settling, setSettling] = useState(false);
  useEffect(() => {
    if (previous.current === state) return;
    previous.current = state;
    setSettling(true);
    const timer = window.setTimeout(() => setSettling(false), 360);
    return () => window.clearTimeout(timer);
  }, [state]);
  const label = state === "delivered" ? t("receipt.delivered") : names.length && group ? t("receipt.read_by", { names: formatList(names) }) : t("receipt.read");
  const tip = state === "read" && group && names.length > 0;
  return <span className={`send-receipt-mark${settling ? " is-settling" : ""}${tip ? " has-tip" : ""}`} data-state={state} role="img" aria-label={label} tabIndex={tip ? -1 : undefined} data-roving={tip ? "" : undefined} data-hint={tip ? label : undefined} data-hint-tone={tip ? "quiet" : undefined}>
    <Mark state={state} />
  </span>;
}

/**
 * A message sent while the Bot was working: one quiet line under the bubble. "Queued, read at next step" holds until the
 * run reads it, then "Read" shows for a moment (the owner removes it, messageDelivery.ts). Same slot as the failed line.
 */
export function DeliveryNote({ state }: { state: DeliveryState }) {
  const { t } = useTranslation("chat");
  return <div className="send-receipt send-receipt-note" data-state={state} role="status">{t(`delivery.${state}`)}</div>;
}
