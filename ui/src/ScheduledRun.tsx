import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import type { RunStatus } from "./types";
import "./scheduled-run.css";

export interface ScheduledRunProps {
  title: string;
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
  failureSummary?: string;
  error?: string;
  notes?: ReactNode;
  onRetry?: () => Promise<void>;
  defaultExpanded?: boolean;
}

const reduceMotion = () => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** Shared ticket used by the conversation and the explicitly synthetic Lab. */
export function ScheduledRun({ title, content, plannedTime, plannedAt, plannedTimeDescription, metadata, metadataDescription, state, statusLabel, statusAt, statusTime, statusTimeDescription, runningSince, resultPublished = false, failureSummary, error, notes, onRetry, defaultExpanded = false }: ScheduledRunProps) {
  const [expanded, setExpanded] = useState(defaultExpanded);
  const [retrying, setRetrying] = useState(false);
  const [retryError, setRetryError] = useState<string>();
  const [now, setNow] = useState(Date.now);
  const contentId = useId();
  const titleId = useId();
  const ticketRef = useRef<HTMLElement>(null);
  const retryIconRef = useRef<HTMLSpanElement>(null);
  const previousState = useRef(state);
  const failed = state === "failed" || state === "interrupted";
  const tone = failed ? "error" : state === "running" ? "running" : state === "done" && resultPublished ? "done" : "neutral";
  const startedAt = runningSince ? Date.parse(runningSince) : NaN;
  const seconds = state === "running" && Number.isFinite(startedAt) ? Math.max(0, Math.floor((now - startedAt) / 1000)) : undefined;

  useEffect(() => {
    if (state !== "running" || !runningSince) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [state, runningSince]);

  useEffect(() => {
    if (failed && previousState.current !== state && !reduceMotion()) {
      ticketRef.current?.animate?.({ transform: ["translateX(0)", "translateX(-4px)", "translateX(3px)", "translateX(0)"] }, { duration: 280, easing: "ease-out" });
    }
    previousState.current = state;
    setRetryError(undefined);
  }, [state, failed]);

  async function retry() {
    if (!onRetry || retrying) return;
    setRetrying(true);
    setRetryError(undefined);
    if (!reduceMotion()) retryIconRef.current?.animate?.({ rotate: ["0deg", "-360deg"] }, { duration: 480, easing: "cubic-bezier(.22,1,.36,1)" });
    try {
      await onRetry();
    } catch (cause) {
      setRetryError(cause instanceof Error ? cause.message : "重新执行请求未成功，请稍后重试。");
    } finally {
      setRetrying(false);
    }
  }

  return <article ref={ticketRef} className={`scheduled-run is-${tone}`} aria-labelledby={titleId}>
    <div className="scheduled-run-stub" title={plannedTimeDescription}>
      <TofiIcon name="clock" size={22} aria-hidden="true" />
      <time dateTime={plannedAt} aria-label={plannedTimeDescription}>{plannedTime}</time>
    </div>
    <div className="scheduled-run-body">
      <button type="button" className="scheduled-run-toggle" aria-expanded={expanded} aria-controls={contentId} onClick={() => setExpanded(value => !value)}>
        <span className="scheduled-run-heading">
          <strong id={titleId} className="scheduled-run-title" title={title}>{title}</strong>
          <span className="scheduled-run-status" title={statusTimeDescription ? `${statusLabel} · ${statusTimeDescription}` : statusLabel}>
            <i className="scheduled-run-dot" aria-hidden="true">{failed && "!"}</i>
            {tone === "done" && statusTime
              ? <><span className="scheduled-run-sr">{statusLabel} · </span><time dateTime={statusAt}>{statusTime}</time></>
              : <span>{statusLabel}{seconds !== undefined && <span aria-hidden="true"> · {seconds}s</span>}</span>}
          </span>
          <TofiIcon className="scheduled-run-chevron" name="chevron-right" size={14} aria-hidden="true" />
        </span>
        <span className="scheduled-run-meta" title={metadataDescription}>{metadata}</span>
      </button>
      {failed && <div className="scheduled-run-failure">
        <p>{failureSummary || "本轮未完成，任务记录已保留。"}</p>
        {onRetry && <button type="button" className="scheduled-run-retry" onClick={() => void retry()} disabled={retrying}>
          <span ref={retryIconRef}><TofiIcon name="retry" size={14} aria-hidden="true" /></span>{retrying ? "正在重试…" : "重试"}
        </button>}
      </div>}
      {retryError && <p className="scheduled-run-retry-error" role="alert">{retryError}</p>}
      <div className="scheduled-run-disclosure" data-expanded={expanded} id={contentId} inert={!expanded} aria-hidden={!expanded}>
        <div className="scheduled-run-disclosure-inner">
          <div className="scheduled-run-details">
            <strong>任务内容</strong>
            <p>{content}</p>
          </div>
          {notes && <div className="scheduled-run-notes">{notes}</div>}
          {failed && <div className="scheduled-run-recovery">
            <p>为避免重复执行外部操作，请确认后重试。</p>
            {error && <details><summary>查看技术原因</summary><p className="scheduled-run-error">{error}</p></details>}
          </div>}
        </div>
      </div>
    </div>
  </article>;
}
