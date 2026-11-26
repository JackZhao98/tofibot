import { useEffect, useState, type ReactNode } from "react";
import type { RunStatus } from "./types";
import { useTranslation } from "./i18n";
import { ScheduleCard } from "./ds/ScheduleCard/ScheduleCard";
import "./layout/scheduled-run.css";
import { MessageMarkdown } from "./MessageMarkdown";

export interface ScheduledRunProps {
  title: string;
  description?: string;
  content: string;
  plannedTime: string;
  plannedAt?: string;
  plannedTimeDescription: string;
  metadata: string;
  metadataDescription?: string;
  state?: RunStatus;
  statusLabel: string;
  statusAt?: string;
  statusTime?: string;
  statusTimeDescription?: string;
  runningSince?: string;
  resultPublished?: boolean;
  /** Done with a recorded conclusion and no chat message (D52). */
  quiet?: boolean;
  resultText?: string;
  resultSources?: string[];
  failureSummary?: string;
  error?: string;
  notes?: ReactNode;
  onRetry?: () => Promise<void>;
  defaultExpanded?: boolean;
}

/** A scheduled run in the conversation: the kit ScheduleCard, its rows placed by layout/scheduled-run.css. */
export function ScheduledRun({ title, description, content, plannedTime, plannedAt, plannedTimeDescription, metadata, metadataDescription, state, statusLabel, statusAt, statusTime, statusTimeDescription, runningSince, resultPublished = false, quiet = false, resultText, resultSources, failureSummary, error, notes, onRetry, defaultExpanded = false }: ScheduledRunProps) {
  const { t, i18n } = useTranslation("schedules");
  const plannedDay = (() => { const d = plannedAt ? new Date(plannedAt) : null; return d && !Number.isNaN(d.getTime()) ? d.toLocaleDateString(i18n.language, { weekday: "short" }) : ""; })();
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string>();
  const [now, setNow] = useState(Date.now);
  const failed = state === "failed" || state === "interrupted";
  // pending (queued, waiting) | running | done (posted) | quiet (done, nothing posted) | error (failed).
  const tone = failed ? "error" : state === "running" ? "running" : state === "done" && quiet ? "quiet" : state === "done" && resultPublished ? "done" : "neutral";
  const hasResult = state === "done" && !!resultText;
  const startedAt = runningSince ? Date.parse(runningSince) : NaN;
  const seconds = state === "running" && Number.isFinite(startedAt) ? Math.max(0, Math.floor((now - startedAt) / 1000)) : undefined;

  useEffect(() => {
    if (state !== "running" || !runningSince) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [state, runningSince]);

  useEffect(() => { setRetryError(undefined); }, [state, failed]);

  async function retry() {
    if (!onRetry || retrying) return;
    setRetrying(true);
    setRetryError(undefined);
    try {
      await onRetry();
    } catch (cause) {
      setRetryError(cause instanceof Error ? cause.message : t("run.retry_failed"));
    } finally {
      setRetrying(false);
    }
  }

  const statusText = (tone === "done" || tone === "quiet") && statusTime ? `${statusLabel} · ${statusTime}` : `${statusLabel}${seconds !== undefined ? ` · ${seconds}s` : ""}`;
  const cardState = failed ? "error" : state === "running" ? "run" : "done";
  return <ScheduleCard className={`scheduled-run is-${tone}`} state={cardState} statusLabel={statusText} time={plannedTime} day={plannedDay || undefined} title={title} description={description || undefined} meta={metadata} open={defaultExpanded}
    error={failed ? <>{failureSummary || t("row.failure.incomplete")}{retryError && <span role="alert"> {retryError}</span>}</> : undefined}
    onRetry={failed && onRetry ? () => void retry() : undefined} retryBusy={retrying} retryLabel={retrying ? t("run.retrying") : t("run.retry")}>
    {hasResult && <section className="scheduled-run-result" aria-label={t("row.result.heading")}>
      <small>{t("row.result.heading")}</small>
      <MessageMarkdown content={resultText} />
      {resultSources && resultSources.length > 0 && <div className="scheduled-run-sources">
        <small>{t("row.result.sources")}</small>
        <ul>{resultSources.map(source => <li key={source}>{source}</li>)}</ul>
      </div>}
    </section>}
    <details className="scheduled-run-details"><summary>{t("row.manage_instructions")}</summary><p>{content}</p></details>
    {notes && <div className="scheduled-run-notes">{notes}</div>}
    {failed && <div className="scheduled-run-recovery">
      <p>{t("run.confirm_before_retry")}</p>
      {error && <details><summary>{t("row.technical_reason")}</summary><p className="scheduled-run-error">{error}</p></details>}
    </div>}
  </ScheduleCard>;
}
