import { useEffect, useId, useRef, useState } from "react";
import { TofiIcon } from "./icons";
import { taskDiagnostics, taskLocale, taskText, type TaskIssueView, type TaskLocale } from "./taskIssuePresentation";
import "./task-issue-card.css";

/** Neutral section: the task's single announcement channel owns live speech. */
export function TaskIssueCard({ issue, locale = taskLocale(), onAction, onFeedback, onOpenStep }: { issue: TaskIssueView; locale?: TaskLocale; onAction?: () => void | Promise<void>; onFeedback?: (text:string) => void; onOpenStep?: (runId: string, callId: string) => void }) {
  const heading = useId();
  const details = useRef<HTMLDetailsElement>(null);
  const diagnostics = useRef<HTMLPreElement>(null);
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState("");
  const t = (zh: string, en: string) => taskText(locale, zh, en);
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
          report(t("已复制诊断信息。", "Diagnostics copied."));
        } catch {
          if (details.current) details.current.open = true;
          report(t("未能复制，请选择下方诊断信息后手动复制。", "Copy failed. Select the diagnostics below and copy them manually."));
          diagnostics.current?.focus();
        }
      } else if (issue.action === "verify_steps") {
        if (details.current) details.current.open = true;
        diagnostics.current?.focus();
      } else await onAction?.();
    } catch {
      report(t("部分状态暂时无法更新。已确认的记录保留。", "Some status updates are unavailable. Confirmed records are retained."));
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
    <div className="task-issue-actions"><button type="button" className="secondary-button" disabled={busy} onClick={() => void act()}>{issue.action === "copy_diagnostics" && <TofiIcon name="copy" size={16} aria-hidden="true" />}{busy ? t("处理中…", "Working…") : issue.actionLabel}</button></div>
    <details className="task-issue-technical" ref={details}><summary>{t("技术详情", "Technical details")}</summary><pre ref={diagnostics} tabIndex={0} aria-label={t("诊断信息", "Diagnostics")}>{taskDiagnostics(issue)}</pre></details>
    {feedback && <p className="task-issue-feedback">{feedback}</p>}
  </section>;
}
