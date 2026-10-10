import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import { toolDisplayLabel, orderToolActivities, toolDisplayState, toolStepTitle, toolStepSeconds, formatStepSeconds, toolStepDetail } from "./toolTimeline";
import { TaskIssueCard } from "./TaskIssueCard";
import { canAnswerQuestion, presentTaskIssue, taskCertaintyText, taskOutcomeText, taskPhaseLabel, taskStatusText, type TaskOwner } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import { formatClock } from "./i18n/format";
import type { Question } from "./questionTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Message, ToolActivity, ToolActivityRunSummary } from "./types";
import { isTerminalRun } from "./runFamily";
import { SPRINGS, springEasing } from "./motion-lab/lib/spring";
import { prefersReducedMotion } from "./motion-lab/lib/hooks";
import "./task-issue-card.css";

const SETTLE = springEasing(SPRINGS.morph);
const settleVars = { "--spring": SETTLE.easing, "--spring-ms": `${SETTLE.durationMs}ms` } as React.CSSProperties;

export type TaskDetailState = { loaded: number; toolCount: number; hasMore: boolean; loading: boolean; error?: string };
export type TaskRunBlockProps = { owner: TaskOwner; tools: ToolActivity[]; questions: Question[]; drafts: MailDraft[]; messages: Message[]; summaries: ToolActivityRunSummary[]; details?: Record<string, TaskDetailState>; connected?: boolean; /** Pins one language (tests, fixtures); the active UI language otherwise. */ locale?: Language; botName?: string; showName?: boolean; onFeedback?: (text:string) => void; onOpenTools: () => void; onRefresh: () => Promise<void>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; liveStatus?: string; liveAvatar?: ReactNode; renderMessage?: (message: Message) => ReactNode; renderQuestion: (question: Question) => ReactNode; renderDraft: (draft: MailDraft) => ReactNode };

const GROW = springEasing(SPRINGS.settle);
const drawers = new WeakMap<HTMLElement, Animation[]>();
const drawerBody = (details: HTMLDetailsElement) => details.querySelector<HTMLElement>(":scope > :not(summary)");

/**
 * Motion Lab drawer for a native <details>: the body is clipped while its height
 * settles, its content fades and drops 6px into place, and a second click
 * reverses from wherever the drawer is. The open attribute stays the truth.
 */
export function animateDisclosure(details: HTMLDetailsElement, open: boolean) {
  const body = drawerBody(details);
  if (!body || prefersReducedMotion()) { details.open = open; return; }
  const from = drawers.has(body) ? body.getBoundingClientRect().height : open ? 0 : body.offsetHeight;
  drawers.get(body)?.forEach(animation => animation.cancel());
  if (open) details.open = true;
  details.dataset.drawer = open ? "opening" : "closing";
  const to = open ? body.scrollHeight + body.offsetHeight - body.clientHeight : 0;
  body.style.overflow = "clip";
  const height = body.animate({ height: [`${from}px`, `${to}px`] }, { duration: GROW.durationMs, easing: GROW.easing });
  const fade = body.animate(open ? { opacity: [0, 1], translate: ["0 -6px", "0 0"] } : { opacity: [1, 0], translate: ["0 0", "0 -4px"] }, { duration: open ? 320 : 200, easing: "cubic-bezier(.22,1,.36,1)", fill: "forwards" });
  drawers.set(body, [height, fade]);
  height.finished.then(() => {
    drawers.delete(body);
    delete details.dataset.drawer;
    body.style.overflow = "";
    if (!open) details.open = false;
    fade.cancel();
  }, () => {});
}

/** A summary click drives the drawer instead of the native instant toggle. */
export function toggleDisclosure(event: React.MouseEvent<HTMLElement>) {
  const details = event.currentTarget.parentElement;
  if (!(details instanceof HTMLDetailsElement)) return;
  event.preventDefault();
  // Mid-collapse the attribute is still open, so the drawer's direction decides.
  animateDisclosure(details, details.dataset.drawer ? details.dataset.drawer === "closing" : !details.open);
}

export function ActivityDisclosure({ children, locale, detail, problem, live, disclosureRef, onOpen }: { children: ReactNode; locale?: Language; detail?: string; problem?: string; live?: { label: string; avatar?: ReactNode }; disclosureRef: React.RefObject<HTMLDetailsElement | null>; onOpen: () => void }) {
  const { t } = useTranslation("tasks", { lng: locale });
  // While the Bot works the whole block is one quiet status line; the steps
  // stay folded until someone opens them.
  return <details className={`task-activity${live ? " is-live" : ""}`} ref={disclosureRef} style={settleVars} onToggle={event => { if (event.currentTarget.open) onOpen(); }}><summary onClick={toggleDisclosure}>{live ? <>{live.avatar && <span className="task-activity-cat" aria-hidden="true">{live.avatar}</span>}<span className="task-activity-live" role="status">{live.label}</span></> : <><TofiIcon name="chevron-right" size={16} aria-hidden="true" />{t("activity.title")}{detail && <span className="task-activity-detail"> · {detail}</span>}{problem && <span className="task-activity-problem"> · {problem}</span>}</>}</summary><div className="task-activity-records">{children}</div></details>;
}

/** One step: marker, human action, short argument, then state and duration. */
const problemStates = ["failed", "not_executed", "interrupted"];
const stepDiagnostics = (tool: ToolActivity) => JSON.stringify({ tool: tool.name, run_id: tool.run_id, call_id: tool.call_id, status: tool.status, outcome_status: tool.outcome?.status, code: tool.outcome?.code, certainty: tool.outcome?.execution_certainty, started_at: tool.started_at, updated_at: tool.updated_at }, null, 2);

function ToolStepRecord({ tool, locale, now, onOpenTools }: { tool: ToolActivity; locale?: Language; now?: number; onOpenTools?: () => void }) {
  const [copied, setCopied] = useState("");
  const { t } = useTranslation("tasks", { lng: locale });
  const state = toolDisplayState(tool), preview = toolStepDetail(tool);
  const live = tool.status === "running" && now !== undefined ? Math.max(0, (now - Date.parse(tool.started_at)) / 1000) : undefined;
  const seconds = toolStepSeconds(tool) ?? (Number.isFinite(live) ? live : undefined);
  const started = Date.parse(tool.started_at);
  const problem = problemStates.includes(state);
  // A problem step explains itself in words first; codes stay in technical details.
  const reason = tool.outcome?.code || tool.outcome?.status ? taskOutcomeText(tool.outcome, locale) : state === "interrupted" ? t("step.interrupted") : t("step.failed");
  async function copyDiagnostics() {
    try { await navigator.clipboard.writeText(stepDiagnostics(tool)); setCopied(t("step.copied")); }
    catch { setCopied(t("step.copy_failed")); }
  }
  return <li className={`task-step is-${state}${problem ? " is-problem" : ""}`} data-step={`${tool.run_id}:${tool.call_id}`}>
    <details><summary onClick={toggleDisclosure}><span className="task-step-marker" aria-hidden="true" /><span className="task-step-title">{toolStepTitle(tool)}{preview && <span className="task-step-argument"> · {preview}</span>}{problem && state === "not_executed" && tool.outcome?.code && <span className="task-step-argument task-step-reason"> · {reason}</span>}</span><span className="task-step-state">{toolDisplayLabel(tool, locale)}{seconds !== undefined && <time> · {formatStepSeconds(seconds)}</time>}</span></summary>
      <div className="task-step-body">{problem && <div className="task-step-problem" role="group" aria-label={t("step.problem")}>
        <p><strong>{reason}</strong>{tool.outcome?.execution_certainty && <> · {taskCertaintyText(tool.outcome.execution_certainty, locale)}</>}</p>
        <div className="task-step-actions"><button type="button" className="secondary-button" onClick={() => void copyDiagnostics()}><TofiIcon name="copy" size={15} aria-hidden="true" />{t("step.copy_diagnostics")}</button>{tool.outcome?.code === "mcp_review_setup_missing" && onOpenTools && <button type="button" className="secondary-button" onClick={onOpenTools}>{t("step.open_tool_settings")}</button>}</div>
        {copied && <p className="task-step-feedback" role="status">{copied}</p>}
        <details className="task-step-technical"><summary>{t("step.technical_details")}</summary>{tool.outcome?.message && <p>{tool.outcome.message}</p>}<pre tabIndex={0}>{stepDiagnostics(tool)}</pre></details>
      </div>}
      <div className="tool-activity-details">{Number.isFinite(started) && <span className="task-step-time">{t("step.started", { time: formatClock(started, { seconds: true, language: locale }) })}</span>}<span>{t("step.arguments")}</span><pre tabIndex={0}>{tool.arguments || t("step.none")}</pre>{!problem && tool.outcome?.message && <p className="tool-outcome">{tool.outcome.message}</p>}<span>{t("step.result")}</span><pre tabIndex={0}>{tool.result || t("step.no_result")}</pre></div></div></details>
  </li>;
}

/** One durable owner replaces progress in place; actual final answers stay in chat. */
export function TaskRunBlock({ owner, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale, botName, showName, onFeedback, onOpenTools, onRefresh, onLoadDetails, liveStatus, liveAvatar, renderMessage, renderQuestion, renderDraft }: TaskRunBlockProps) {
  const disclosure = useRef<HTMLDetailsElement>(null);
  const block = useRef<HTMLElement>(null);
  const needsFocus = useRef(false);
  const run = owner.family.latest;
  const ids = new Set(owner.family.attempts.map(attempt => attempt.id));
  const matches = (item: { run_id: string; conversation_id: string; bot_id: string }) => ids.has(item.run_id) && item.conversation_id === run.conversation_id && item.bot_id === run.bot_id;
  tools = orderToolActivities(tools.filter(matches));
  questions = questions.filter(matches); drafts = drafts.filter(matches);
  const currentTools = tools.filter(tool => tool.run_id === run.id);
  const currentQuestions = questions.filter(question => question.run_id === run.id);
  const currentDrafts = drafts.filter(draft => draft.run_id === run.id);
  const summary = summaries.find(item => item.run_id === run.id);
  const input = { run, tools: currentTools, questions: currentQuestions, drafts: currentDrafts, summary, recordsComplete: details[run.id]?.hasMore === false, connected, locale };
  const issue = presentTaskIssue({...input, family:owner.family, tools, questions, drafts});
  const { t } = useTranslation("tasks", { lng: locale });
  const hasFinal = messages.some(message => message.run_id === run.id && message.conversation_id === run.conversation_id && message.role === "assistant" && !["notice", "progress", "segment"].includes(message.kind ?? "") && Boolean(message.content || message.attachments?.length));
  const decisions = currentQuestions.filter(question => canAnswerQuestion(question));
  const decisionKey = decisions.map(question => question.question_id).join(":");
  useLayoutEffect(() => {
    if (needsFocus.current && !block.current?.contains(document.activeElement)) (block.current?.querySelector<HTMLElement>("h3") ?? block.current)?.focus({preventScroll:true});
    needsFocus.current = false;
    return () => { needsFocus.current = Boolean(block.current?.contains(document.activeElement) && document.activeElement?.closest(".approval-actions")); };
  }, [decisionKey, issue?.kind]);
  // Approval proposals are bookkeeping: the step itself shows whether the call
  // ran. Only answers a person gave to a real question stay as records.
  const recordedQuestions = questions.filter(question => question.question_type !== "approval" && !decisions.some(item => item.question_id === question.question_id));
  const notes = messages.filter(message => ids.has(message.run_id ?? "") && message.conversation_id === run.conversation_id && message.role === "assistant" && message.kind === "progress");
  function loadInitial() {
    for (const attempt of owner.family.attempts) {
      const detail = details[attempt.id];
      if (!detail && onLoadDetails) void onLoadDetails(attempt.id, 0);
    }
  }
  const attemptStatus = (status: string) => ({ done: t("activity.attempt.done"), failed: t("activity.attempt.failed"), cancelled: t("activity.attempt.cancelled"), interrupted: t("activity.attempt.interrupted") } as Record<string, string>)[status] ?? t("activity.attempt.ended");
  const toolTotal = owner.family.attempts.reduce<number | undefined>((sum, attempt) => { const count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? details[attempt.id]?.toolCount; return count === undefined || sum === undefined ? undefined : sum + count; }, 0);
  const runSeconds = run.status !== "running" && run.status !== "queued" ? (Date.parse(run.updated_at) - Date.parse(run.created_at)) / 1000 : NaN;
  const activityDetail = [toolTotal ? t("activity.tool_count", { count: toolTotal }) : "", Number.isFinite(runSeconds) && runSeconds >= 0 && toolTotal ? formatStepSeconds(runSeconds) : ""].filter(Boolean).join(" · ") || undefined;
  const [pendingStep, setPendingStep] = useState("");
  // While the Bot works the steps are open and the running step's clock ticks;
  // when the run ends they fold into the summary line (Motion Lab ToolSteps).
  const live = !isTerminalRun(run);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!live) return;
    const ticker = window.setInterval(() => setNow(Date.now()), currentTools.some(tool => tool.status === "running") ? 100 : 1000);
    return () => window.clearInterval(ticker);
  }, [live, currentTools]);
  const liveSeconds = Math.max(0, Math.floor((now - Date.parse(run.created_at)) / 1000));
  const liveLine = live ? { label: `${liveStatus || t("activity.thinking")} · ${formatStepSeconds(liveSeconds)}`, avatar: liveAvatar } : undefined;
  function openStep(runId: string, callId: string) {
    if (disclosure.current && !disclosure.current.open) disclosure.current.open = true;
    setPendingStep(`${runId}:${callId}`);
  }
  // The step may arrive with a later activity page; reveal it once it renders.
  useEffect(() => {
    if (!pendingStep) return;
    const step = block.current?.querySelector<HTMLLIElement>(`li[data-step="${CSS.escape(pendingStep)}"]`);
    if (!step) return;
    const record = step.querySelector("details");
    if (record) record.open = true;
    step.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    step.querySelector<HTMLElement>("summary")?.focus({ preventScroll: true });
    setPendingStep("");
  }, [pendingStep, tools]);
  function viewActivity() { if (disclosure.current) { disclosure.current.open = true; disclosure.current.querySelector("summary")?.focus(); } }
  return <section ref={block} className="task-run-block" data-task-owner={owner.key} data-run-id={run.id} data-latest-run-id={run.id} tabIndex={-1} aria-label={t("activity.records_label")}>
    {showName && botName && <p className="task-run-identity">{botName}</p>}
    {issue ? <TaskIssueCard issue={issue} locale={locale} onFeedback={onFeedback} onOpenStep={openStep} onAction={issue.action === "open_tools" ? onOpenTools : issue.action === "open_codex" ? () => { window.dispatchEvent(new Event("tofi:open-codex-settings")); } : issue.action === "refresh_status" ? onRefresh : viewActivity} /> : !live && !(run.status === "done" && hasFinal) && <p className="task-run-phase">{taskPhaseLabel(input)}</p>}
    {/* A separate valid proposal survives another call's failure/unknown effect. */}
    {decisions.map(renderQuestion)}
    {currentDrafts.filter(draft => draft.status !== "unknown").map(renderDraft)}
    <ActivityDisclosure locale={locale} detail={activityDetail} live={liveLine} disclosureRef={disclosure} onOpen={loadInitial}>
      {owner.family.previous.map((attempt, index) => <p key={attempt.id} className="task-record-meta">{t("activity.previous_attempt", { number: index + 1, status: attemptStatus(attempt.status) })}</p>)}
      {recordedQuestions.map(question => <section className="task-question-record" key={question.question_id} data-question-record-id={question.question_id} tabIndex={-1}>
        <p>{question.question_type === "approval" ? question.approval?.review_only || question.approval?.review?.status.startsWith("shadow") ? t("record.observation_only") : question.status === "answered" && question.answer === true ? t("record.approved_unconfirmed") : question.status === "expired" ? t("record.approval_expired") : t("record.decision") : t("record.answer")}</p>
        <dl><div><dt>{t("record.status")}</dt><dd>{taskStatusText(question.approval?.review?.status ?? question.status, locale)}</dd></div>{question.outcome && <div><dt>{t("record.execution")}</dt><dd>{taskCertaintyText(question.outcome.execution_certainty, locale)}</dd></div>}</dl>
        {question.question_type === "approval" && <details><summary>{t("record.view_proposal")}</summary><MessageMarkdown content={question.question} />{question.approval && <dl><div><dt>{t("record.action")}</dt><dd>{question.approval.action}</dd></div><div><dt>{t("record.target")}</dt><dd>{question.approval.target}</dd></div><div><dt>{t("record.impact")}</dt><dd>{question.approval.impact}</dd></div></dl>}{question.approval?.payload && <details><summary>{t("record.view_full_arguments")}</summary><pre tabIndex={0}>{question.approval.payload}</pre></details>}</details>}
        {question.question_type !== "approval" && renderQuestion(question)}
      </section>)}
      <ol className={`task-tool-records${live ? " is-live" : ""}`}>{tools.map(tool => <ToolStepRecord key={`${tool.run_id}:${tool.call_id}`} tool={tool} locale={locale} now={live ? now : undefined} onOpenTools={onOpenTools} />)}</ol>
      {drafts.filter(draft => draft.run_id !== run.id || draft.status === "unknown").map(renderDraft)}
      {notes.map(note => <details className="task-progress-record" key={note.id}><summary>{t("activity.progress_report")}</summary><MessageMarkdown content={note.content} /></details>)}
      {owner.family.previous.flatMap(attempt => messages.filter(message => message.run_id === attempt.id && message.role === "assistant" && message.kind !== "progress").map(message => <div key={message.id}>{renderMessage ? renderMessage(message) : <MessageMarkdown content={message.content} />}</div>))}
      {!tools.length && !recordedQuestions.length && !notes.length && <p className="task-record-meta">{owner.family.attempts.every(attempt => summaries.find(item => item.run_id === attempt.id)?.tool_count === 0) ? t("activity.no_tool_steps") : t("activity.no_records")}</p>}
      {owner.family.attempts.map(attempt => {
        const detail = details[attempt.id], count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? detail?.toolCount;
        const loaded = tools.filter(tool => tool.run_id === attempt.id).length;
        return <div key={attempt.id} className="task-record-page">
          {detail?.error && <p>{t("activity.records_unavailable")}</p>}
          {count !== 0 && (detail?.hasMore !== false || (count !== undefined && loaded < count)) && <button type="button" className="secondary-button" disabled={detail?.loading} onClick={() => void onLoadDetails?.(attempt.id, detail?.loaded ?? 0)}>{detail?.loading ? t("activity.loading") : t("activity.load")}{count !== undefined ? ` (${loaded}/${count})` : ""}</button>}
        </div>;
      })}
    </ActivityDisclosure>
  </section>;
}
