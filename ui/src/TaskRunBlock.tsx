import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { MessageMarkdown } from "./MessageMarkdown";
import { toolDisplayLabel, orderToolActivities, toolDisplayState, toolStepTitle, toolStepSeconds, formatStepSeconds, toolStepDetail } from "./toolTimeline";
import { TaskIssueCard } from "./TaskIssueCard";
import { canAnswerQuestion, presentTaskIssue, taskCertaintyText, taskLocale, taskOutcomeText, taskPhaseLabel, taskStatusText, taskText, type TaskLocale, type TaskOwner } from "./taskIssuePresentation";
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
export type TaskRunBlockProps = { owner: TaskOwner; tools: ToolActivity[]; questions: Question[]; drafts: MailDraft[]; messages: Message[]; summaries: ToolActivityRunSummary[]; details?: Record<string, TaskDetailState>; connected?: boolean; locale?: TaskLocale; botName?: string; showName?: boolean; onFeedback?: (text:string) => void; onOpenTools: () => void; onRefresh: () => Promise<void>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; renderMessage?: (message: Message) => ReactNode; renderQuestion: (question: Question) => ReactNode; renderDraft: (draft: MailDraft) => ReactNode };

/** Animate the records' height; the <details> open state stays the source of truth. */
export function animateRecords(details: HTMLDetailsElement, open: boolean) {
  const records = details.querySelector<HTMLElement>(".task-activity-records");
  if (!records || prefersReducedMotion()) { details.open = open; return; }
  if (open) details.open = true;
  const height = records.scrollHeight;
  const motion = records.animate({ height: open ? ["0px", `${height}px`] : [`${height}px`, "0px"], opacity: open ? [0, 1] : [1, 0] }, { duration: SETTLE.durationMs, easing: SETTLE.easing });
  if (!open) motion.finished.then(() => { details.open = false; }, () => { details.open = false; });
}

export function ActivityDisclosure({ children, locale, detail, problem, disclosureRef, onOpen }: { children: ReactNode; locale: TaskLocale; detail?: string; problem?: string; disclosureRef: React.RefObject<HTMLDetailsElement | null>; onOpen: () => void }) {
  return <details className="task-activity" ref={disclosureRef} style={settleVars} onToggle={event => { if (event.currentTarget.open) onOpen(); }}><summary onClick={event => { const details = event.currentTarget.parentElement as HTMLDetailsElement | null; if (!details) return; event.preventDefault(); animateRecords(details, !details.open); }}><TofiIcon name="chevron-right" size={16} aria-hidden="true" />{taskText(locale, "工作过程", "Activity")}{detail && <span className="task-activity-detail"> · {detail}</span>}{problem && <span className="task-activity-problem"> · {problem}</span>}</summary><div className="task-activity-records">{children}</div></details>;
}

/** One step: marker, human action, short argument, then state and duration. */
const problemStates = ["failed", "not_executed", "interrupted"];
const stepDiagnostics = (tool: ToolActivity) => JSON.stringify({ tool: tool.name, run_id: tool.run_id, call_id: tool.call_id, status: tool.status, outcome_status: tool.outcome?.status, code: tool.outcome?.code, certainty: tool.outcome?.execution_certainty, started_at: tool.started_at, updated_at: tool.updated_at }, null, 2);

function ToolStepRecord({ tool, locale, now, onOpenTools }: { tool: ToolActivity; locale: TaskLocale; now?: number; onOpenTools?: () => void }) {
  const [copied, setCopied] = useState("");
  const t = (zh: string, en: string) => taskText(locale, zh, en);
  const state = toolDisplayState(tool), preview = toolStepDetail(tool);
  const live = tool.status === "running" && now !== undefined ? Math.max(0, (now - Date.parse(tool.started_at)) / 1000) : undefined;
  const seconds = toolStepSeconds(tool) ?? (Number.isFinite(live) ? live : undefined);
  const started = Date.parse(tool.started_at);
  const problem = problemStates.includes(state);
  // A problem step explains itself in words first; codes stay in technical details.
  const reason = tool.outcome?.code || tool.outcome?.status ? taskOutcomeText(tool.outcome, locale) : state === "interrupted" ? t("执行中断", "Interrupted") : t("工具调用失败", "The tool call failed");
  async function copyDiagnostics() {
    try { await navigator.clipboard.writeText(stepDiagnostics(tool)); setCopied(t("已复制诊断信息。", "Diagnostics copied.")); }
    catch { setCopied(t("未能复制，请在技术详情里手动选择。", "Copy failed; select the technical details manually.")); }
  }
  return <li className={`task-step is-${state}${problem ? " is-problem" : ""}`} data-step={`${tool.run_id}:${tool.call_id}`}>
    <details><summary><span className="task-step-marker" aria-hidden="true" /><span className="task-step-title">{toolStepTitle(tool)}{preview && <span className="task-step-argument"> · {preview}</span>}</span><span className="task-step-state">{toolDisplayLabel(tool, locale)}{seconds !== undefined && <time> · {formatStepSeconds(seconds)}</time>}</span></summary>
      {problem && <div className="task-step-problem" role="group" aria-label={t("问题说明", "Problem")}>
        <p><strong>{reason}</strong>{tool.outcome?.execution_certainty && <> · {taskCertaintyText(tool.outcome.execution_certainty, locale)}</>}</p>
        <div className="task-step-actions"><button type="button" className="secondary-button" onClick={() => void copyDiagnostics()}><TofiIcon name="copy" size={15} aria-hidden="true" />{t("复制诊断信息", "Copy diagnostics")}</button>{(tool.outcome?.status === "setup_required" || tool.outcome?.code === "mcp_review_setup_missing") && onOpenTools && <button type="button" className="secondary-button" onClick={onOpenTools}>{t("打开工具设置", "Open tool settings")}</button>}</div>
        {copied && <p className="task-step-feedback" role="status">{copied}</p>}
        <details className="task-step-technical"><summary>{t("技术详情", "Technical details")}</summary>{tool.outcome?.message && <p>{tool.outcome.message}</p>}<pre tabIndex={0}>{stepDiagnostics(tool)}</pre></details>
      </div>}
      <div className="tool-activity-details">{Number.isFinite(started) && <span className="task-step-time">{t("开始于", "Started")} {new Date(started).toLocaleTimeString(locale === "en" ? "en-US" : "zh-CN", { hour12: false })}</span>}<span>{t("参数", "Arguments")}</span><pre tabIndex={0}>{tool.arguments || t("（无）", "(none)")}</pre>{!problem && tool.outcome?.message && <p className="tool-outcome">{tool.outcome.message}</p>}<span>{t("结果", "Result")}</span><pre tabIndex={0}>{tool.result || t("尚无结果", "No result yet")}</pre></div></details>
  </li>;
}

/** One durable owner replaces progress in place; actual final answers stay in chat. */
export function TaskRunBlock({ owner, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale = taskLocale(), botName, showName, onFeedback, onOpenTools, onRefresh, onLoadDetails, renderMessage, renderQuestion, renderDraft }: TaskRunBlockProps) {
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
  const t = (zh: string, en: string) => taskText(locale, zh, en);
  const hasFinal = messages.some(message => message.run_id === run.id && message.conversation_id === run.conversation_id && message.role === "assistant" && !["notice", "progress"].includes(message.kind ?? "") && Boolean(message.content || message.attachments?.length));
  const decisions = currentQuestions.filter(question => canAnswerQuestion(question));
  const decisionKey = decisions.map(question => question.question_id).join(":");
  useLayoutEffect(() => {
    if (needsFocus.current && !block.current?.contains(document.activeElement)) (block.current?.querySelector<HTMLElement>("h3") ?? block.current)?.focus({preventScroll:true});
    needsFocus.current = false;
    return () => { needsFocus.current = Boolean(block.current?.contains(document.activeElement) && document.activeElement?.closest(".approval-actions")); };
  }, [decisionKey, issue?.kind]);
  // Automatic pre-execution review outcomes are explained on the step they blocked;
  // only decisions and answers a person gave stay as separate records.
  const reviewOnlyStates = ["context_required", "setup_required", "unavailable", "policy_denied", "reviewing"];
  const recordedQuestions = questions.filter(question => !decisions.some(item => item.question_id === question.question_id)
    && !(question.question_type === "approval" && (question.approval?.review_only || question.approval?.review?.status.startsWith("shadow") || reviewOnlyStates.includes(question.approval?.review?.status ?? "") || reviewOnlyStates.includes(question.outcome?.status ?? ""))));
  const notes = messages.filter(message => ids.has(message.run_id ?? "") && message.conversation_id === run.conversation_id && message.role === "assistant" && message.kind === "progress");
  function loadInitial() {
    for (const attempt of owner.family.attempts) {
      const detail = details[attempt.id];
      if (!detail && onLoadDetails) void onLoadDetails(attempt.id, 0);
    }
  }
  const attemptStatus = (status: string) => ({ done: t("已完成", "Done"), failed: t("失败", "Failed"), cancelled: t("已取消", "Cancelled"), interrupted: t("已中断", "Interrupted") } as Record<string, string>)[status] ?? t("已结束", "Ended");
  const toolTotal = owner.family.attempts.reduce<number | undefined>((sum, attempt) => { const count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? details[attempt.id]?.toolCount; return count === undefined || sum === undefined ? undefined : sum + count; }, 0);
  const runSeconds = run.status !== "running" && run.status !== "queued" ? (Date.parse(run.updated_at) - Date.parse(run.created_at)) / 1000 : NaN;
  const problemCount = owner.family.attempts.reduce((sum, attempt) => { const summary = summaries.find(item => item.run_id === attempt.id); return sum + (summary ? summary.failed_count + summary.interrupted_count : tools.filter(tool => tool.run_id === attempt.id && problemStates.includes(toolDisplayState(tool))).length); }, 0);
  const activityDetail = [toolTotal ? t(`${toolTotal} 个工具`, `${toolTotal} ${toolTotal === 1 ? "tool" : "tools"}`) : "", Number.isFinite(runSeconds) && runSeconds >= 0 && toolTotal ? formatStepSeconds(runSeconds) : ""].filter(Boolean).join(" · ") || undefined;
  const [pendingStep, setPendingStep] = useState("");
  // While the Bot works the steps are open and the running step's clock ticks;
  // when the run ends they fold into the summary line (Motion Lab ToolSteps).
  const live = !isTerminalRun(run);
  const [now, setNow] = useState(() => Date.now());
  const wasLive = useRef(live);
  useEffect(() => {
    if (!live || !currentTools.some(tool => tool.status === "running")) return;
    const ticker = window.setInterval(() => setNow(Date.now()), 100);
    return () => window.clearInterval(ticker);
  }, [live, currentTools]);
  useEffect(() => {
    const details = disclosure.current;
    if (!details) return;
    if (live && !wasLive.current) wasLive.current = true;
    if (live && !details.open && currentTools.length) { details.open = true; }
    if (!live && wasLive.current) {
      wasLive.current = false;
      if (details.open) foldSteps(details);
    }
  }, [live, currentTools.length]);
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
  function foldSteps(details: HTMLDetailsElement) {
    const records = details.querySelector<HTMLElement>(".task-activity-records");
    const summary = details.querySelector<HTMLElement>(":scope > summary");
    if (!records || !summary || prefersReducedMotion()) { details.open = false; return; }
    const from = records.getBoundingClientRect(), to = summary.getBoundingClientRect();
    const ghost = records.cloneNode(true) as HTMLElement;
    ghost.classList.add("task-steps-ghost");
    // The ghost covers the answer arriving underneath, so it takes the page's real background.
    let surface: Element | null = details, background = "";
    while (surface && (!background || background === "transparent" || background === "rgba(0, 0, 0, 0)")) { background = getComputedStyle(surface).backgroundColor; surface = surface.parentElement; }
    Object.assign(ghost.style, { position: "fixed", left: `${from.left}px`, top: `${from.top}px`, width: `${from.width}px`, margin: "0", pointerEvents: "none", transformOrigin: "0 0", zIndex: "3", background });
    document.body.append(ghost);
    details.open = false;
    const fold = ghost.animate({ transform: ["none", `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(${Math.min(1, to.width / from.width)}, ${Math.max(0.05, to.height / from.height)})`], opacity: [1, 0.6, 0] }, { duration: 520, easing: "cubic-bezier(.5,0,.2,1)" });
    summary.animate({ opacity: [0.4, 1], transform: ["translateY(-4px)", "none"] }, { duration: 360, delay: 260, easing: "cubic-bezier(.22,1,.36,1)", fill: "backwards" });
    fold.finished.finally(() => ghost.remove());
  }
  function viewActivity() { if (disclosure.current) { disclosure.current.open = true; disclosure.current.querySelector("summary")?.focus(); } }
  return <section ref={block} className="task-run-block" data-task-owner={owner.key} data-run-id={run.id} data-latest-run-id={run.id} tabIndex={-1} aria-label={t("任务记录", "Task records")}>
    {showName && botName && <p className="task-run-identity">{botName}</p>}
    {issue ? <TaskIssueCard issue={issue} locale={locale} onFeedback={onFeedback} onOpenStep={openStep} onAction={issue.action === "open_tools" ? onOpenTools : issue.action === "open_codex" ? () => { window.dispatchEvent(new Event("tofi:open-codex-settings")); } : issue.action === "refresh_status" ? onRefresh : viewActivity} /> : !(run.status === "done" && hasFinal) && <p className="task-run-phase">{taskPhaseLabel(input)}</p>}
    {/* A separate valid proposal survives another call's failure/unknown effect. */}
    {decisions.map(renderQuestion)}
    {currentQuestions.filter(question => question.question_type === "approval" && question.status === "answered" && question.answer === true && !question.approval?.review_only).map(question => <p className="task-record-meta" data-question-id={question.question_id} key={question.question_id}>{t("已批准，操作尚未确认完成。", "Approved; execution is not yet confirmed.")}</p>)}
    {currentDrafts.filter(draft => draft.status !== "unknown").map(renderDraft)}
    <ActivityDisclosure locale={locale} detail={activityDetail} problem={problemCount ? t(`${problemCount} 步未完成`, `${problemCount} ${problemCount === 1 ? "step" : "steps"} did not finish`) : undefined} disclosureRef={disclosure} onOpen={loadInitial}>
      {owner.family.previous.map((attempt, index) => <p key={attempt.id} className="task-record-meta">{t("此前尝试", "Previous attempt")} {index + 1} · {attemptStatus(attempt.status)}</p>)}
      {recordedQuestions.map(question => <section className="task-question-record" key={question.question_id} data-question-record-id={question.question_id} tabIndex={-1}>
        <p>{question.question_type === "approval" ? question.approval?.review_only || question.approval?.review?.status.startsWith("shadow") ? t("观察记录，不提供执行权限。", "Observation only; it does not grant execution permission.") : question.status === "answered" && question.answer === true ? t("已批准，操作尚未确认完成。", "Approved; execution is not yet confirmed.") : question.status === "expired" ? t("批准已过期。", "Approval expired.") : t("决定与执行前检查记录", "Decision and pre-execution check record") : t("回答记录", "Answer record")}</p>
        <dl><div><dt>{t("状态", "Status")}</dt><dd>{taskStatusText(question.approval?.review?.status ?? question.status, locale)}</dd></div>{question.outcome && <div><dt>{t("执行情况", "Execution")}</dt><dd>{taskCertaintyText(question.outcome.execution_certainty, locale)}</dd></div>}</dl>
        {question.question_type === "approval" && <details><summary>{t("查看提案记录", "View proposal record")}</summary><MessageMarkdown content={question.question} />{question.approval && <dl><div><dt>{t("动作", "Action")}</dt><dd>{question.approval.action}</dd></div><div><dt>{t("对象", "Target")}</dt><dd>{question.approval.target}</dd></div><div><dt>{t("影响", "Impact")}</dt><dd>{question.approval.impact}</dd></div></dl>}{question.approval?.payload && <details><summary>{t("查看完整参数", "View full arguments")}</summary><pre tabIndex={0}>{question.approval.payload}</pre></details>}</details>}
        {question.question_type !== "approval" && renderQuestion(question)}
      </section>)}
      <ol className={`task-tool-records${live ? " is-live" : ""}`}>{tools.map(tool => <ToolStepRecord key={`${tool.run_id}:${tool.call_id}`} tool={tool} locale={locale} now={live ? now : undefined} onOpenTools={onOpenTools} />)}</ol>
      {drafts.filter(draft => draft.run_id !== run.id || draft.status === "unknown").map(renderDraft)}
      {notes.map(note => <details className="task-progress-record" key={note.id}><summary>{t("进度汇报", "Progress report")}</summary><MessageMarkdown content={note.content} /></details>)}
      {owner.family.previous.flatMap(attempt => messages.filter(message => message.run_id === attempt.id && message.role === "assistant" && message.kind !== "progress").map(message => <div key={message.id}>{renderMessage ? renderMessage(message) : <MessageMarkdown content={message.content} />}</div>))}
      {!tools.length && !recordedQuestions.length && !notes.length && <p className="task-record-meta">{owner.family.attempts.every(attempt => summaries.find(item => item.run_id === attempt.id)?.tool_count === 0) ? t("本次没有执行任何工具步骤。", "No tool steps ran.") : t("尚无已加载的执行记录。", "No execution records are loaded yet.")}</p>}
      {owner.family.attempts.map(attempt => {
        const detail = details[attempt.id], count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? detail?.toolCount;
        const loaded = tools.filter(tool => tool.run_id === attempt.id).length;
        return <div key={attempt.id} className="task-record-page">
          {detail?.error && <p>{t("部分执行记录暂时无法更新。", "Some execution records are unavailable.")}</p>}
          {count !== 0 && (detail?.hasMore !== false || (count !== undefined && loaded < count)) && <button type="button" className="secondary-button" disabled={detail?.loading} onClick={() => void onLoadDetails?.(attempt.id, detail?.loaded ?? 0)}>{detail?.loading ? t("读取中…", "Loading…") : t("读取执行记录", "Load activity")}{count !== undefined ? ` (${loaded}/${count})` : ""}</button>}
        </div>;
      })}
    </ActivityDisclosure>
  </section>;
}
