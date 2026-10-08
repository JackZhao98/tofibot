import { useState } from "react";
import { TofiIcon } from "./icons";
import { isDesktop } from "./desktop";
import type { Message, Run, Schedule } from "./types";
import type { ScheduleOccurrence } from "./scheduleOccurrences";
import { ScheduledRun } from "./ScheduledRun";
import { scheduledRunMetadata } from "./scheduledRunMetadata";
import { useTranslation } from "./i18n";
import "./scheduled-task-row.css";

export function ScheduledTaskRow({ message, run, occurrence, schedule, timezone, onRetry }: { message: Message; run?: Run; occurrence?: ScheduleOccurrence; schedule?: Schedule; timezone?: string; onRetry?: (id: string) => Promise<void> }) {
  const [retrying, setRetrying] = useState(false);
  const owningRun = message.run_id && run?.id === message.run_id ? run : undefined;
  const exactOccurrence = message.run_id && occurrence?.root_run_id === message.run_id ? occurrence : undefined;
  const state = exactOccurrence?.execution_status ?? owningRun?.status;
  // A whole-family execution outcome does not certify result quality. Only a
  // completed receipt plus a persisted final reply in this conversation can
  // justify the narrower publication label (not a notification/read claim).
  const { t } = useTranslation("schedules");
  const status = t(state === "waiting" ? "row.status.waiting" : exactOccurrence
    ? state === "queued" ? "row.status.queued" : state === "running" ? "row.status.running" : state === "done" ? exactOccurrence.result_in_conversation ? "row.status.published" : "row.status.done" : state === "failed" ? "row.status.failed" : state === "cancelled" ? "row.status.cancelled" : state === "interrupted" ? "row.status.interrupted" : "row.status.unknown"
    : state === "queued" ? "row.status.queued" : state === "running" ? "row.status.turn_running" : state === "done" ? "row.status.turn_done" : state === "failed" ? "row.status.turn_failed" : state === "cancelled" ? "row.status.turn_cancelled" : state === "interrupted" ? "row.status.turn_interrupted" : "row.status.unknown");
  const isFailure = state === "failed" || state === "interrupted";
  // The family may have failed in another Bot's hidden conversation. Its
  // selected diagnostic comes from the same snapshot as the aggregate, not a
  // stale ancestor or the schedule's most recent occurrence.
  const error = isFailure ? exactOccurrence?.status_error ?? (
    owningRun?.error && (!exactOccurrence || exactOccurrence.status_run_id === owningRun.id) ? owningRun.error : undefined
  ) : undefined;
  const retryId = exactOccurrence?.status_run_id ?? owningRun?.id;
  const receiptMissing = error?.includes("scheduled task returned without confirming a completed result");
  const metadata = scheduledRunMetadata(message, run, occurrence, schedule, timezone);
  if (!isDesktop) return <ScheduledRun
    {...metadata}
    content={message.content}
    state={state}
    statusLabel={status}
    resultPublished={exactOccurrence?.result_in_conversation}
    failureSummary={receiptMissing ? t("row.failure.receipt_missing") : t("row.failure.incomplete")}
    error={error}
    notes={(!exactOccurrence || (exactOccurrence.execution_status === "done" && !exactOccurrence.result_in_conversation)) ? <>
      {!exactOccurrence && <p>{t("row.note.partial_status")}</p>}
      {exactOccurrence?.execution_status === "done" && !exactOccurrence.result_in_conversation && <p>{t("row.note.unpublished")}</p>}
    </> : undefined}
    onRetry={onRetry && retryId ? () => onRetry(retryId) : undefined}
  />;
  // Preserve the native desktop row and its existing disclosure/retry behavior.
  return <article className={`scheduled-task-row scheduled-task-${state ?? "unknown"}`}>
    <details>
      <summary>
        <span className="scheduled-task-dot" aria-hidden="true" />
        <TofiIcon className="scheduled-task-clock" name="clock" size={13} aria-hidden="true" />
        <span className="scheduled-task-label" title={metadata.title}><strong>{metadata.title}</strong><small>{metadata.description}</small></span>
        <span className="scheduled-task-status">{status}</span>
        <TofiIcon className="scheduled-task-chevron" name="chevron-right" size={14} />
      </summary>
      <div className="scheduled-task-details"><p>{metadata.description}</p><details><summary>{t("row.manage_instructions")}</summary><p>{message.content}</p></details>{!exactOccurrence && <p className="field-note">{t("row.note.partial_status")}</p>}{exactOccurrence?.execution_status === "done" && !exactOccurrence.result_in_conversation && <p className="field-note">{t("row.note.unpublished")}</p>}{isFailure && <div className="scheduled-task-recovery"><p>{receiptMissing ? t("row.failure.receipt_missing_confirm") : t("row.failure.incomplete_confirm")}</p>{error && <details><summary>{t("row.technical_reason")}</summary><p className="error-text">{error}</p></details>}{onRetry && retryId && <button type="button" className="text-button" disabled={retrying} onClick={() => { setRetrying(true); void onRetry(retryId).finally(() => setRetrying(false)); }}>{retrying ? t("row.rerunning") : t("row.rerun")}</button>}</div>}</div>
    </details>
  </article>;
}
