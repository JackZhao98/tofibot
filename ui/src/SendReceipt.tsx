import { useEffect, useState } from "react";
import { useTranslation } from "./i18n";
import type { Receipt } from "./sendReceipts";
import type { DeliveryState } from "./messageDelivery";
import "./send-receipt.css";

/**
 * Status of one of the person's own messages. There are no delivery ticks or read marks (V3): a sent message shows nothing. The two
 * states that must be seen without asking stay on the bubble: sending (the bubble is dimmed, the words are for assistive tech) and
 * failed (the retry line).
 */
type FailureModule = typeof import("./SendFailure");
let failureChunk: Promise<FailureModule> | undefined;
/** Called when a message is sent, so the chunks are in the cache before a failure (maybe a lost connection) needs them. */
export const preloadSendFailure = () => {
  void import("./chat/UnsentMenu").catch(() => {}); // the failed message's right-click menu: needed exactly when the connection is gone
  return (failureChunk ??= import("./SendFailure"));
};

export function SendReceipt({ receipt, onRetry, onEdit, onDelete }: { receipt: Receipt; names?: string[]; group?: boolean; onRetry?: () => void; onEdit?: () => void; onDelete?: () => void }) {
  const { t } = useTranslation("chat");
  const [chunk, setChunk] = useState<FailureModule | null>(null);
  const needsChunk = receipt.state === "failed" || receipt.state === "retrying";
  useEffect(() => {
    if (!needsChunk || chunk) return;
    let alive = true;
    preloadSendFailure().then(module => { if (alive) setChunk(module); }, () => { failureChunk = undefined; });
    return () => { alive = false; };
  }, [needsChunk, chunk]);
  if (needsChunk && chunk) return <chunk.SendFailure receipt={receipt} onRetry={onRetry} onEdit={onEdit} onDelete={onDelete} />;
  // Until the chunk is there (or when it cannot load, offline after a reload): the minimum, with Retry.
  if (receipt.state === "failed") return <div className="send-receipt is-failed" data-state="failed">{onRetry ? <button type="button" className="send-receipt-retry" title={receipt.error} onClick={onRetry}>{t("receipt.failed")} · {t("receipt.retry")}</button> : <span className="send-receipt-text" title={receipt.error}>{t("receipt.failed")}</span>}</div>;
  if (receipt.state === "retrying") return <div className="send-receipt is-retrying" data-state="retrying" role="status"><span>{t("receipt.retrying")}</span></div>;
  if (receipt.state === "sending") return <span className="sr-only send-receipt-status" data-state="sending" role="status">{t("receipt.sending")}</span>;
  return null;
}

/**
 * A message sent while the Bot was working: one quiet line under the bubble. "Queued, read at next step" holds until the
 * run reads it, then "Read" shows for a moment (the owner removes it, messageDelivery.ts). Same slot as the failed line.
 */
export function DeliveryNote({ state }: { state: DeliveryState }) {
  const { t } = useTranslation("chat");
  return <div className="send-receipt send-receipt-note" data-state={state} role="status">{t(`delivery.${state}`)}</div>;
}
