import { lazy, Suspense, useState } from "react";
import type { Message, Run, Schedule } from "./types";
import type { ScheduleOccurrence } from "./scheduleOccurrences";
import { ScheduledRun } from "./ScheduledRun";
import { MessageMarkdown } from "./MessageMarkdown";
import { scheduledRunMetadata } from "./scheduledRunMetadata";
import { useTranslation } from "./i18n";
import "./scheduled-task-row.css";

const WorkTrace = lazy(() => import("./WorkTrace"));

export function ScheduledTaskRow({ message, run, occurrence, pending = false, schedule, timezone, onRetry }: { message: Message; run?: Run; occurrence?: ScheduleOccurrence; pending?: boolean; schedule?: Schedule; timezone?: string; onRetry?: (id: string) => Promise<void> }) {
  const [retrying, setRetrying] = useState(false);
  const owningRun = message.run_id && run?.id === message.run_id ? run : undefined;
  const exactOccurrence = message.run_id && occurrence?.root_run_id === message.run_id ? occurrence : undefined;
  const state = exactOccurrence?.execution_status ?? owningRun?.status;
  // A whole-family execution outcome does not certify result quality. Only a
  // completed receipt plus a persisted final reply in this conversation can
  // justify the narrower publication label (not a notification/read claim).
  const { t } = useTranslation("schedules");
  // A done occurrence that recorded a conclusion and posted nothing completed quietly (D52).
  const quiet = state === "done" && !!exactOccurrence && !exactOccurrence.result_posted && !exactOccurrence.result_in_conversation && !!exactOccurrence.result_text;
  const status = t(state === "waiting" ? "row.status.waiting" : exactOccurrence
    ? state === "queued" ? "row.status.queued" : state === "running" ? "row.status.running" : state === "done" ? exactOccurrence.result_in_conversation ? "row.status.published" : quiet ? "row.status.quiet" : "row.status.done" : state === "failed" ? "row.status.failed" : state === "cancelled" ? "row.status.cancelled" : state === "interrupted" ? "row.status.interrupted" : "row.status.unknown"
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
  // The run's work trace, when it kept screen captures: the same folded line as in a chat run.
  const traceLine = owningRun?.trace ? <Suspense fallback={null}><WorkTrace run={owningRun} /></Suspense> : undefined;
  // The occurrence read is still in flight: a blank ticket of the same shape, never a guessed status.
  const blank = "\u00a0";
  if (pending) return <div className="scheduled-run-pending" aria-hidden="true" inert>
    <ScheduledRun title={blank} description={blank} content="" plannedTime={blank} plannedTimeDescription="" metadata={blank} statusLabel={blank} />
  </div>;
  return <ScheduledRun
    {...metadata}
    content={message.content}
    state={state}
    statusLabel={status}
    resultPublished={exactOccurrence?.result_in_conversation}
    quiet={quiet}
    resultText={exactOccurrence?.result_text}
    resultSources={exactOccurrence?.result_sources}
    failureSummary={receiptMissing ? t("row.failure.receipt_missing") : t("row.failure.incomplete")}
    error={error}
    notes={(!exactOccurrence || (exactOccurrence.execution_status === "done" && !exactOccurrence.result_in_conversation && !quiet)) || traceLine ? <>
      {!exactOccurrence && <p>{t("row.note.partial_status")}</p>}
      {exactOccurrence?.execution_status === "done" && !exactOccurrence.result_in_conversation && !quiet && <p>{t("row.note.unpublished")}</p>}
      {traceLine}
    </> : undefined}
    onRetry={onRetry && retryId ? () => onRetry(retryId) : undefined}
  />;
}
