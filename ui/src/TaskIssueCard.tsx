import { useEffect, useId, useRef, useState } from "react";
import { TofiIcon } from "./icons";
import { taskDiagnostics, type TaskIssueView } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import "./task-issue-card.css";

/** Neutral section: the task's single announcement channel owns live speech. */
export function TaskIssueCard({ issue, locale, onAction, onFeedback, onOpenStep }: { issue: TaskIssueView; /** Pins one language (tests, fixtures); the active UI language otherwise. */ locale?: Language; onAction?: () => void | Promise<void>; onFeedback?: (text:string) => void; onOpenStep?: (runId: string, callId: string) => void }) {
  const heading = useId();
  const details = useRef<HTMLDetailsElement>(null);
  const diagnostics = useRef<HTMLPreElement>(null);
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState("");
  const { t } = useTranslation("tasks", { lng: locale });
  const diagnosticText = taskDiagnostics(issue);
  useEffect(() => { setFeedback(""); }, [diagnosticText]);
  function report(text: string) { setFeedback(text); onFeedback?.(text); }
  async function act() {
    if (inFlight.current) return;
    inFlight.current = true; setBusy(true); setFeedback("");
    try {
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
      } else if (issue.action === "verify_steps") {
        if (details.current) details.current.open = true;
        diagnostics.current?.focus();
      } else await onAction?.();
    } catch {
      report(t("issue.status_unavailable"));
    } finally { inFlight.current = false; setBusy(false); }
  }
  return <section className="task-issue-card" data-issue-kind={issue.kind} data-issue-phase={issue.phase} aria-labelledby={heading} aria-busy={busy}>
    <div className="task-issue-heading"><TofiIcon name={issue.kind === "uncertain_effect" ? "clock" : issue.kind === "tool_setup" ? "plug" : "alert"} size={20} aria-hidden="true" /><h3 id={heading} tabIndex={-1}>{issue.title}</h3></div>
    {issue.facts.map((fact, index) => {
      const link = issue.links?.[index];
      // A named step opens its own record; the rest of the sentence stays plain text.
      return <p className="task-issue-fact" key={index}>{link && onOpenStep && fact.startsWith(link.label) ? <><button type="button" className="task-fact-link" onClick={() => onOpenStep(link.runId, link.callId)}>{link.label}</button>{fact.slice(link.label.length)}</> : fact}</p>;
    })}
    {issue.secondary.map((cause, index) => <p className="task-issue-context" key={index}>{cause}</p>)}
    <div className="task-issue-actions"><button type="button" className="secondary-button" disabled={busy} onClick={() => void act()}>{issue.action === "copy_diagnostics" && <TofiIcon name="copy" size={16} aria-hidden="true" />}{busy ? t("issue.working") : issue.actionLabel}</button></div>
    <details className="task-issue-technical" ref={details}><summary>{t("step.technical_details")}</summary><pre ref={diagnostics} tabIndex={0} aria-label={t("issue.diagnostics")}>{taskDiagnostics(issue)}</pre></details>
    {feedback && <p className="task-issue-feedback">{feedback}</p>}
  </section>;
}
