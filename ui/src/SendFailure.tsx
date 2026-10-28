import { useTranslation } from "./i18n";
import type { Receipt } from "./sendReceipts";

// The failed and "sending again" lines under a bubble: lazy (SendReceipt.tsx loads it), so the actions cost the entry bundle nothing.
function Spinner() {
  return <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><circle cx="8" cy="8" r="5.6" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M8 4.8V8l2.2 1.4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>;
}

export function SendFailure({ receipt, onRetry, onEdit, onDelete }: { receipt: Receipt; names?: string[]; group?: boolean; onRetry?: () => void; onEdit?: () => void; onDelete?: () => void }) {
  const { t } = useTranslation("chat");
  if (receipt.state === "failed") {
    // Network and server trouble say so, and promise one automatic try when the connection is back (only while that is true).
    const hint = receipt.reason === "offline" ? t("receipt.hint_offline") : receipt.reason === "network" ? t("receipt.hint_network") : receipt.reason === "server" ? t("receipt.hint_server") : "";
    const promise = hint && !receipt.auto && onRetry ? ` ${t("receipt.will_retry")}` : "";
    return <div className="send-receipt is-failed" data-state="failed" data-reason={receipt.reason}>
      {onRetry && <button type="button" className="send-receipt-retry" title={receipt.error} aria-label={`${t("receipt.failed")}. ${t("receipt.retry_label")}`} onClick={onRetry}>{t("receipt.failed")} · {t("receipt.retry")}</button>}
      {!onRetry && <span className="send-receipt-text" title={receipt.error}>{t("receipt.failed")}</span>}
      {onEdit && <button type="button" className="send-receipt-action send-receipt-edit" aria-label={t("receipt.edit_label")} onClick={onEdit}>{t("receipt.edit")}</button>}
      {onDelete && <button type="button" className="send-receipt-action send-receipt-delete" aria-label={t("receipt.delete_label")} onClick={onDelete}>{t("receipt.delete")}</button>}
      {hint && <span className="send-receipt-hint" role="status">{hint}{promise}</span>}
    </div>;
  }
  // Retrying is on show, not hidden: the person sees that the app is sending again by itself.
  if (receipt.state === "retrying") return <div className="send-receipt is-retrying" data-state="retrying" role="status"><Spinner /><span>{t("receipt.retrying")}</span></div>;
  return null;
}

