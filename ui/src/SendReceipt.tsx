import { useEffect, useRef, useState } from "react";
import { useTranslation } from "./i18n";
import { formatList } from "./i18n/format";
import type { Receipt } from "./sendReceipts";
import "./send-receipt.css";

const tick = "M3 8.6l3.1 3.1L13 4.8";
// Two overlapping ticks, the second shifted right: read.
const tickPair = ["M0.6 8.8l2.9 2.9L9 5.4", "M5.6 8.8l2.9 2.9L14.2 5.4"];

function Mark({ state }: { state: Receipt["state"] }) {
  if (state === "sending") return <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><circle cx="8" cy="8" r="5.6" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M8 4.8V8l2.2 1.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>;
  return <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">{(state === "read" ? tickPair : [tick]).map(d => <path key={d} d={d} fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />)}</svg>;
}

/**
 * Status of one of the person's own messages, below its bubble. The row has a fixed height and the mark a
 * fixed box, so a state change repaints in place and moves nothing. Only a change seen live plays the small
 * settle; a message loaded from history is drawn in its final state.
 */
export function SendReceipt({ receipt, names, group, onRetry }: { receipt: Receipt; names: string[]; group: boolean; onRetry?: () => void }) {
  const { t } = useTranslation("chat");
  const previous = useRef(receipt.state);
  const [settling, setSettling] = useState(false);
  useEffect(() => {
    if (previous.current === receipt.state) return;
    previous.current = receipt.state;
    setSettling(true);
    const timer = window.setTimeout(() => setSettling(false), 360);
    return () => window.clearTimeout(timer);
  }, [receipt.state]);
  if (receipt.state === "failed") {
    return <div className="send-receipt" data-state="failed"><button type="button" className="send-receipt-retry" title={receipt.error} aria-label={`${t("receipt.failed")}. ${t("receipt.retry_label")}`} onClick={onRetry}>{t("receipt.failed")} · {t("receipt.retry")}</button></div>;
  }
  const label = receipt.state === "sending" ? t("receipt.sending") : receipt.state === "delivered" ? t("receipt.delivered") : names.length && group ? t("receipt.read_by", { names: formatList(names) }) : t("receipt.read");
  const tip = receipt.state === "read" && group && names.length > 0;
  return <div className="send-receipt" data-state={receipt.state}>
    <span className={`send-receipt-mark${settling ? " is-settling" : ""}${tip ? " has-tip" : ""}`} role="img" aria-label={label} tabIndex={tip ? 0 : undefined}>
      <Mark state={receipt.state} />
      {tip && <span className="send-receipt-tip" role="tooltip">{label}</span>}
    </span>
  </div>;
}
