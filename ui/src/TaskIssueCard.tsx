import { useEffect, useId, useRef, useState } from "react";
import { TofiIcon } from "./icons";
import { taskDiagnostics, type TaskIssueView } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import type { DockStep } from "./planModel";
import "./ds/StepList/StepList.css";
import "./task-issue-card.css";

/** Neutral section: the task's single announcement channel owns live speech. */
export function TaskIssueCard({ issue, locale, onAction, onRetry, onFeedback, onOpenStep, steps }: { /** The ended run's list (planHistory): its finished steps tick, the step it stopped on carries the failure, the rest stay open. */ steps?: readonly DockStep[]; issue: TaskIssueView; /** Pins one language (tests, fixtures); the active UI language otherwise. */ locale?: Language; onAction?: () => void | Promise<void>; onRetry?: () => void | Promise<void>; onFeedback?: (text:string) => void; onOpenStep?: (runId: string, callId: string) => void }) {
  const heading = useId();
  const details = useRef<HTMLDetailsElement>(null);
  const diagnostics = useRef<HTMLPreElement>(null);
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [feedback, setFeedback] = useState("");
  const { t } = useTranslation("tasks", { lng: locale });
  const diagnosticText = taskDiagnostics(issue);
  useEffect(() => { setFeedback(""); }, [diagnosticText]);
  function report(text: string) { setFeedback(text); onFeedback?.(text); }
  async function act(retry = false) {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true); setRetrying(retry); setFeedback("");
    try {
      if (retry) await onRetry?.();
      else
      if (issue.action === "copy_diagnostics") {
        try {
          if (!navigator.clipboard) throw new Error("clipboard unavailable");
          await navigator.clipboard.writeText(taskDiagnostics(issue));
          report(t("step.copied"));
        } catch {
          if (details.current) details.current.open = true;
          report(t("issue.copy_failed"));
          diagnostics.current?.focus();
        }
      } else await onAction?.();
    } catch (cause) {
      // A refused retry names its reason beside the button that asked; the button comes back.
      report(retry ? (cause instanceof Error && cause.message ? t("issue.retry_failed_reason", { error: cause.message }) : t("issue.retry_failed")) : t("issue.status_unavailable"));
    } finally { inFlight.current = false; setBusy(false); setRetrying(false); }
  }
  // The step the run stopped on; a list that stopped between steps puts the failure on the first step not done.
  const list = steps ?? [];
  const at = list.findIndex(step => step.state === "stopped") >= 0 ? list.findIndex(step => step.state === "stopped") : list.findIndex(step => step.state !== "done");
  const stopped = at >= 0 ? list[at] : undefined;
  const before = at >= 0 ? list.slice(0, at) : list;
  const after = at >= 0 ? list.slice(at + 1) : [];
  // V3 4.4: a run failure reads as the failed step of a one-step StepList (kit paper, stamp cross, reason, one action, Details folded).
  return <section className="task-issue-card tb-plan" data-issue-kind={issue.kind} data-issue-phase={issue.phase} aria-labelledby={heading} aria-busy={busy}>
    <ol className="tb-plan-list">{before.map(step => <PlainStep key={step.id} step={step} />)}<li className="tb-plan-step is-failed" data-step-id={stopped?.id}><span className="tb-plan-sig" aria-hidden="true"><i /></span><span className="tb-plan-body task-issue-body">
    {stopped && <span className="tb-plan-text task-issue-step">{stopped.text}</span>}
    <div className="task-issue-heading"><h3 id={heading} className={stopped ? "task-issue-reason" : "tb-plan-text"} tabIndex={-1}>{issue.title}</h3></div>
    {issue.facts.map((fact, index) => {
      const link = issue.links?.[index];
      // A named step opens its own record; the rest of the sentence stays plain text.
      return <p className="task-issue-fact" key={index}>{link && onOpenStep && fact.startsWith(link.label) ? <><button type="button" className="task-fact-link" onClick={() => onOpenStep(link.runId, link.callId)}>{link.label}</button>{fact.slice(link.label.length)}</> : fact}</p>;
    })}
    {issue.secondary.map((cause, index) => <p className="task-issue-context" key={index}>{cause}</p>)}
    <div className="task-issue-actions">{issue.retryable && onRetry && <button type="button" className="primary-button" disabled={busy} data-issue-retry="true" onClick={() => void act(true)}>{retrying ? t("issue.retrying") : t("issue.action.retry")}</button>}<button type="button" className="secondary-button" disabled={busy} onClick={() => void act()}>{issue.action === "copy_diagnostics" && <TofiIcon name="copy" size={16} aria-hidden="true" />}{busy && !retrying ? t("issue.working") : issue.actionLabel}</button></div>
    <details className="task-issue-technical" ref={details}><summary>{t("step.technical_details")}</summary><pre ref={diagnostics} tabIndex={0} aria-label={t("issue.diagnostics")}>{taskDiagnostics(issue)}</pre></details>
    {feedback && <p className="task-issue-feedback">{feedback}</p>}
    </span></li>{after.map(step => <PlainStep key={step.id} step={step} />)}</ol>
  </section>;
}

function PlainStep({ step }: { step: DockStep }) {
  return <li className={`tb-plan-step is-${step.state === "done" ? "done" : "pending"}`} data-state={step.state}><span className="tb-plan-sig" aria-hidden="true"><i /></span><span className="tb-plan-text">{step.text}</span></li>;
}
