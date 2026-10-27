import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { TofiIcon } from "./icons";
import { LiveMark } from "./LiveMark";
import { MessageMarkdown } from "./MessageMarkdown";
import { requestFullToolResult } from "./toolResultLoader";
import { ToolStatusIcon } from "./ToolStatusIcon";
import { toolDisplayLabel, foldToolRuns, orderToolActivities, toolDisplayState, toolStepTitle, toolStepSeconds, formatStepSeconds, toolStepDetail } from "./toolTimeline";
import { TaskIssueCard } from "./TaskIssueCard";
import { canAnswerQuestion, presentTaskIssue, taskCertaintyText, taskOutcomeText, taskPhaseLabel, taskPhaseVisible, taskStatusText, type TaskOwner } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import { formatClock } from "./i18n/format";
import { approvalFactRows } from "./extensionFacts";
import { REMEMBERED_APPROVAL_ACTOR, STANDING_ALLOWANCE_ACTOR, type Question } from "./questionTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Message, ToolActivity, ToolActivityRunSummary } from "./types";
import { isTerminalRun } from "./runFamily";
import { prefersReducedMotion } from "./motion-lab/lib/hooks";
import "./task-issue-card.css";
import { MOTION, EASE } from "./motion";

export type TaskDetailState = { loaded: number; toolCount: number; hasMore: boolean; loading: boolean; error?: string };
export type TaskRunBlockProps = { senderHead?: React.ReactNode; owner: TaskOwner; tools: ToolActivity[]; questions: Question[]; drafts: MailDraft[]; messages: Message[]; summaries: ToolActivityRunSummary[]; details?: Record<string, TaskDetailState>; connected?: boolean; /** Sends a failed run again (the server accepts it for busy or dropped service). */ onRetryRun?: (runId: string) => void | Promise<void>; /** Pins one language (tests, fixtures); the active UI language otherwise. */ locale?: Language; botName?: string; showName?: boolean; onFeedback?: (text:string) => void; onOpenTools: () => void; onRefresh: () => Promise<void>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; liveStatus?: string; /** The run's reply is already on screen (a streaming draft with text, or its final message): the live line steps aside in that same frame instead of leaving later. */ replying?: boolean; /** Chat default: no Activity row. Cards (approvals, issues) and a live status line still show. */ hideActivity?: boolean; /** The run's Activity was asked for (message action); shows it although hidden. */ revealed?: boolean; renderMessage?: (message: Message) => ReactNode; renderQuestion: (question: Question) => ReactNode; renderDraft: (draft: MailDraft) => ReactNode };

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
  // A border-box height cannot go below the body's own padding and border, so a body that has either would stop short
  // of zero and snap the last pixels when <details> finally hides it (the old close ended 17px high, then jumped). They
  // ride the same animation, from what is there to nothing and back.
  const box = getComputedStyle(body);
  const edges = { paddingBlockStart: box.paddingBlockStart, paddingBlockEnd: box.paddingBlockEnd, borderBlockStartWidth: box.borderBlockStartWidth, borderBlockEndWidth: box.borderBlockEndWidth };
  const none = { paddingBlockStart: "0px", paddingBlockEnd: "0px", borderBlockStartWidth: "0px", borderBlockEndWidth: "0px" };
  body.style.overflow = "clip";
  // fill keeps the closed pose until the attribute flips, so no frame shows the body at full height again.
  const height = body.animate(open ? [{ height: `${from}px`, ...(from ? {} : none) }, { height: `${to}px`, ...edges }] : [{ height: `${from}px`, ...edges }, { height: "0px", ...none }], { duration: open ? MOTION.base : MOTION.panelOut, easing: open ? EASE.out : EASE.settle, fill: "forwards" });
  const fade = body.animate(open ? { opacity: [0, 1], translate: ["0 -6px", "0 0"] } : { opacity: [1, 0], translate: ["0 0", "0 -4px"] }, { duration: open ? MOTION.base : MOTION.quick, easing: open ? EASE.out : EASE.settle, fill: "forwards" });
  drawers.set(body, [height, fade]);
  height.finished.then(() => {
    drawers.delete(body);
    delete details.dataset.drawer;
    body.style.overflow = "";
    if (!open) details.open = false;
    height.cancel();
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

/** A live status this long can be cut on a phone; opened, the first row carries it whole. */
const LONG_STATUS = 28;

export function ActivityDisclosure({ children, locale, detail, count, duration, issues, live, disclosureRef, onOpen }: { children: ReactNode; locale?: Language; /** The whole line in words (the accessible name). */ detail?: string; count?: number; duration?: string; /** Steps that did not finish; shown red. */ issues?: number; live?: { label: string; full: string }; disclosureRef: React.RefObject<HTMLDetailsElement | null>; onOpen: () => void }) {
  const { t } = useTranslation("tasks", { lng: locale });
  const [open, setOpen] = useState(false);
  // While the Bot works the whole block is one quiet process line (loading mark, what it is doing, seconds); the
  // steps stay folded until someone opens it. The line is the <summary>, so Enter and Space open it natively.
  // Finished: a chevron, the tool glyph, the count and the time; the words are the accessible name.
  const name = [t("activity.title"), detail, issues ? t("step.state.failed") + " " + issues : ""].filter(Boolean).join(" · ");
  return <details className={`task-activity${live ? " is-live" : ""}`} ref={disclosureRef} onToggle={event => { setOpen(event.currentTarget.open); if (event.currentTarget.open) onOpen(); }}><summary className="process-line" role="button" aria-expanded={open} aria-label={live ? undefined : name} onClick={toggleDisclosure}>{live ? <><LiveMark /><span className="process-line-text task-activity-live" role="status">{live.label}</span><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /></> : <><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /><TofiIcon className="activity-glyph" name="terminal" size={14} aria-hidden="true" /><span className="process-line-text" aria-hidden="true">{count !== undefined && <span>{count}</span>}{duration && <span className="task-activity-detail">{duration}</span>}{issues ? <span className="task-activity-problem"><TofiIcon name="alert" size={13} aria-hidden="true" />{issues}</span> : null}</span></>}</summary><div className="task-activity-records">{live && live.full.length > LONG_STATUS && <p className="task-live-full">{live.full}</p>}{children}</div></details>;
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
    <details onToggle={event => { if (event.currentTarget.open) requestFullToolResult(tool); }}><summary onClick={toggleDisclosure}><ToolStatusIcon state={state} label={toolDisplayLabel(tool, locale)} /><span className="task-step-title">{toolStepTitle(tool)}{preview && <span className="task-step-argument"> · {preview}</span>}{problem && state === "not_executed" && tool.outcome?.code && <span className="task-step-argument task-step-reason"> · {reason}</span>}</span>{problem ? <span className="task-step-state">{toolDisplayLabel(tool, locale)}{seconds !== undefined && seconds >= 1 && <time> · {formatStepSeconds(seconds)}</time>}</span> : seconds !== undefined && seconds >= 1 && <time className="task-step-time-quiet">{formatStepSeconds(seconds)}</time>}</summary>
      <div className="task-step-body">{problem && <div className="task-step-problem" role="group" aria-label={t("step.problem")}>
        <p><strong>{reason}</strong>{tool.outcome?.execution_certainty && <> · {taskCertaintyText(tool.outcome.execution_certainty, locale)}</>}</p>
        <div className="task-step-actions"><button type="button" className="secondary-button" onClick={() => void copyDiagnostics()}><TofiIcon name="copy" size={15} aria-hidden="true" />{t("step.copy_diagnostics")}</button>{tool.outcome?.code === "mcp_review_setup_missing" && onOpenTools && <button type="button" className="secondary-button" onClick={onOpenTools}>{t("step.open_tool_settings")}</button>}</div>
        {copied && <p className="task-step-feedback" role="status">{copied}</p>}
        <details className="task-step-technical"><summary>{t("step.technical_details")}</summary>{tool.outcome?.message && <p>{tool.outcome.message}</p>}<pre tabIndex={0}>{stepDiagnostics(tool)}</pre></details>
      </div>}
      <div className="tool-activity-details">{Number.isFinite(started) && <span className="task-step-time">{t("step.started", { time: formatClock(started, { seconds: true, language: locale }) })}</span>}<span>{t("step.arguments")}</span><pre tabIndex={0}>{tool.arguments || t("step.none")}</pre>{!problem && tool.outcome?.message && <p className="tool-outcome">{tool.outcome.message}</p>}<span>{t("step.result")}</span><pre tabIndex={0}>{tool.result || t("step.no_result")}</pre></div></div></details>
  </li>;
}

/** A run of identical finished calls as one row with a count; opening it lists the calls. */
function ToolStepGroup({ items, locale, onOpenTools }: { items: ToolActivity[]; locale?: Language; onOpenTools?: () => void }) {
  const { t } = useTranslation("tasks", { lng: locale });
  const total = items.reduce((sum, tool) => sum + (toolStepSeconds(tool) ?? 0), 0);
  return <li className="task-step task-step-group is-completed">
    <details><summary onClick={toggleDisclosure}><ToolStatusIcon state="completed" label={toolDisplayLabel(items[0], locale)} /><span className="task-step-title">{toolStepTitle(items[0])}<span className="task-step-count" aria-label={t("step.group_count", { count: items.length })}> ×{items.length}</span><TofiIcon className="task-step-chevron" name="chevron-right" size={12} aria-hidden="true" /></span>{total >= 1 && <time className="task-step-time-quiet">{formatStepSeconds(total)}</time>}</summary>
      <div className="task-step-body"><ol className="task-step-nested">{items.map(tool => <ToolStepRecord key={`${tool.run_id}:${tool.call_id}`} tool={tool} locale={locale} onOpenTools={onOpenTools} />)}</ol></div></details>
  </li>;
}

/** One durable owner replaces progress in place; actual final answers stay in chat. */
type BlockScopeInput = Pick<TaskRunBlockProps, "owner" | "tools" | "questions" | "drafts" | "messages" | "summaries" | "details" | "connected" | "locale">;

/** Everything the block shows is derived here, so the timeline can ask whether a block draws anything without rendering it. */
function scopeTaskBlock({ owner, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale }: BlockScopeInput) {
  const run = owner.family.latest;
  const ids = new Set(owner.family.attempts.map(attempt => attempt.id));
  const matches = (item: { run_id: string; conversation_id: string; bot_id: string }) => ids.has(item.run_id) && item.conversation_id === run.conversation_id && item.bot_id === run.bot_id;
  tools = orderToolActivities(tools.filter(matches));
  questions = questions.filter(matches); drafts = drafts.filter(matches);
  const currentTools = tools.filter(tool => tool.run_id === run.id);
  const currentQuestions = questions.filter(question => question.run_id === run.id);
  const currentDrafts = drafts.filter(draft => draft.run_id === run.id);
  const summary = summaries.find(item => item.run_id === run.id);
  const hasFinal = messages.some(message => message.run_id === run.id && message.conversation_id === run.conversation_id && message.role === "assistant" && !["notice", "progress", "segment"].includes(message.kind ?? "") && Boolean(message.content || message.attachments?.length));
  const input = { run, tools: currentTools, questions: currentQuestions, drafts: currentDrafts, summary, recordsComplete: details[run.id]?.hasMore === false, connected, replied: hasFinal, locale };
  const issue = presentTaskIssue({...input, family:owner.family, tools, questions, drafts});
  const decisions = currentQuestions.filter(question => canAnswerQuestion(question));
  return { run, ids, tools, questions, drafts, currentTools, currentQuestions, currentDrafts, input, issue, decisions };
}

/**
 * Whether the block draws anything. With Activity hidden a finished run that raised no card (no issue, no pending
 * decision or draft) draws nothing at all: no name label, no row.
 */
export function taskBlockVisible(props: BlockScopeInput & { hideActivity?: boolean; revealed?: boolean }): boolean {
  if (!props.hideActivity || props.revealed) return true;
  const { run, issue, decisions, currentDrafts } = scopeTaskBlock(props);
  return !isTerminalRun(run) || Boolean(issue) || taskPhaseVisible(run) || decisions.length > 0 || currentDrafts.some(draft => draft.status !== "unknown");
}

export function TaskRunBlock({ owner, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale, botName, showName, senderHead, onFeedback, onOpenTools, onRefresh, onRetryRun, onLoadDetails, liveStatus, replying = false, hideActivity = false, revealed: revealedByMenu = false, renderMessage, renderQuestion, renderDraft }: TaskRunBlockProps) {
  const disclosure = useRef<HTMLDetailsElement>(null);
  const block = useRef<HTMLElement>(null);
  const needsFocus = useRef(false);
  const { run, ids, tools: scopedTools, questions: scopedQuestions, drafts: scopedDrafts, currentTools, currentQuestions, currentDrafts, input, issue, decisions } = scopeTaskBlock({ owner, tools, questions, drafts, messages, summaries, details, connected, locale });
  tools = scopedTools; questions = scopedQuestions; drafts = scopedDrafts;
  const { t } = useTranslation("tasks", { lng: locale });
  const decisionKey = decisions.map(question => question.question_id).join(":");
  useLayoutEffect(() => {
    if (needsFocus.current && !block.current?.contains(document.activeElement)) (block.current?.querySelector<HTMLElement>("h3") ?? block.current)?.focus({preventScroll:true});
    needsFocus.current = false;
    return () => { needsFocus.current = Boolean(block.current?.contains(document.activeElement) && document.activeElement?.closest(".approval-actions")); };
  }, [decisionKey, issue?.kind]);
  // Approval proposals are bookkeeping: the step itself shows whether the call
  // ran. Only answers a person gave to a real question stay as records.
  const recordedQuestions = questions.filter(question => question.question_type !== "approval" && !decisions.some(item => item.question_id === question.question_id));
  // A card decided by a remembered approval ("Approve and remember in this chat") stays visible as a record: nobody was asked.
  const rememberedDecisions = questions.filter(question => question.question_type === "approval" && (question.answered_by === REMEMBERED_APPROVAL_ACTOR || question.answered_by === STANDING_ALLOWANCE_ACTOR) && !decisions.some(item => item.question_id === question.question_id));
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
  const runSecondsOk = Number.isFinite(runSeconds) && runSeconds >= 0 && Boolean(toolTotal);
  const activityIssues = currentTools.filter(tool => problemStates.includes(toolDisplayState(tool))).length;
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
  const liveFull = liveStatus || t("activity.thinking");
  const liveLine = live && !(replying && hideActivity && !revealedByMenu) ? { label: `${liveFull} · ${formatStepSeconds(liveSeconds)}`, full: liveFull } : undefined;
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
  // Hidden Activity is mounted on request (a message's "View activity", or a card's own action) and opened once it exists.
  const [revealedHere, setRevealedHere] = useState(false);
  const revealed = revealedByMenu || revealedHere;
  const showActivity = !hideActivity || revealed;
  const openWhenShown = useRef(false);
  useEffect(() => { if (revealedByMenu) openWhenShown.current = true; }, [revealedByMenu]);
  useEffect(() => {
    if (!showActivity || !openWhenShown.current || !disclosure.current) return;
    openWhenShown.current = false;
    // The record opens as a drawer (--m-base) instead of pushing the list down in one frame.
    animateDisclosure(disclosure.current, true);
    disclosure.current.querySelector("summary")?.focus({ preventScroll: true });
    disclosure.current.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }, [showActivity, revealedByMenu]);
  function viewActivity() {
    if (disclosure.current) { animateDisclosure(disclosure.current, true); disclosure.current.querySelector("summary")?.focus(); }
    else { openWhenShown.current = true; setRevealedHere(true); }
  }
  if (!taskBlockVisible({ owner, tools, questions, drafts, messages, summaries, details, connected, locale, hideActivity, revealed })) return null;
  // With Activity hidden a group block holds only cards (which carry their own sender) and the live line,
  // so it is not a sender unit: the live line names the Bot itself instead of a second avatar and caption.
  const head = senderHead && (showActivity || issue) ? senderHead : undefined;
  const namedLive = Boolean(senderHead && !head && botName);
  return <section ref={block} className={`task-run-block${senderHead ? " in-group" : ""}`} data-sender={head ? run.bot_id : undefined} data-at={head ? run.created_at : undefined} data-task-owner={owner.key} data-run-id={run.id} data-latest-run-id={run.id} tabIndex={-1} aria-label={t("activity.records_label")}>
    {senderHead ? head : showName && botName && <p className="task-run-identity">{botName}</p>}
    {issue ? <TaskIssueCard issue={issue} locale={locale} onRetry={onRetryRun ? () => onRetryRun(run.id) : undefined} onFeedback={onFeedback} onOpenStep={openStep} onAction={issue.action === "open_tools" ? onOpenTools : issue.action === "open_codex" ? () => { window.dispatchEvent(new Event("tofi:open-codex-settings")); } : issue.action === "refresh_status" ? onRefresh : viewActivity} /> : taskPhaseVisible(run) && <p className="task-run-phase">{taskPhaseLabel(input)}</p>}
    {/* A separate valid proposal survives another call's failure/unknown effect. */}
    {decisions.map(renderQuestion)}
    {currentDrafts.filter(draft => draft.status !== "unknown").map(renderDraft)}
    {!showActivity && liveLine && <p className="task-run-live-line"><button type="button" className="process-line" aria-expanded="false" onClick={viewActivity}><LiveMark />{namedLive && <span className="task-activity-name">{botName}</span>}<span className="process-line-text task-activity-live" role="status">{liveLine.label}</span><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /></button></p>}
    {showActivity && <ActivityDisclosure locale={locale} detail={activityDetail} count={toolTotal || undefined} duration={runSecondsOk ? formatStepSeconds(runSeconds) : undefined} issues={activityIssues} live={liveLine} disclosureRef={disclosure} onOpen={loadInitial}>
      {owner.family.previous.map((attempt, index) => <p key={attempt.id} className="task-record-meta">{t("activity.previous_attempt", { number: index + 1, status: attemptStatus(attempt.status) })}</p>)}
      {rememberedDecisions.map(question => <p key={question.question_id} className="task-record-meta task-remembered-record" data-question-record-id={question.question_id}>{t(question.answered_by === STANDING_ALLOWANCE_ACTOR ? "record.standing_decision" : "record.remembered_decision")}</p>)}
      {recordedQuestions.map(question => <section className="task-question-record" key={question.question_id} data-question-record-id={question.question_id} tabIndex={-1}>
        <p>{question.question_type === "approval" ? question.approval?.review_only || question.approval?.review?.status.startsWith("shadow") ? t("record.observation_only") : question.status === "answered" && question.answer === true ? t("record.approved_unconfirmed") : question.status === "expired" ? t("record.approval_expired") : t("record.decision") : t("record.answer")}</p>
        <dl><div><dt>{t("record.status")}</dt><dd>{taskStatusText(question.approval?.review?.status ?? question.status, locale)}</dd></div>{question.outcome && <div><dt>{t("record.execution")}</dt><dd>{taskCertaintyText(question.outcome.execution_certainty, locale)}</dd></div>}</dl>
        {question.question_type === "approval" && <details><summary>{t("record.view_proposal")}</summary><MessageMarkdown content={question.question} />{question.approval?.facts && <dl className="approval-verified-record">{approvalFactRows(question.approval.facts, t).map((row, index) => <div key={index}><dt>{row.label}</dt><dd>{row.value}</dd></div>)}</dl>}{question.approval && <dl><div><dt>{t("record.action")}</dt><dd>{question.approval.action}</dd></div><div><dt>{t("record.target")}</dt><dd>{question.approval.target}</dd></div><div><dt>{t("record.impact")}</dt><dd>{question.approval.impact}</dd></div></dl>}{question.approval?.payload && <details><summary>{t("record.view_full_arguments")}</summary><pre tabIndex={0}>{question.approval.payload}</pre></details>}</details>}
        {question.question_type !== "approval" && renderQuestion(question)}
      </section>)}
      <ol className={`task-tool-records${live ? " is-live" : ""}`}>{foldToolRuns(tools).map(group => group.items.length > 1 ? <ToolStepGroup key={`${group.items[0].run_id}:${group.items[0].call_id}`} items={group.items} locale={locale} onOpenTools={onOpenTools} /> : <ToolStepRecord key={`${group.items[0].run_id}:${group.items[0].call_id}`} tool={group.items[0]} locale={locale} now={live ? now : undefined} onOpenTools={onOpenTools} />)}</ol>
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
    </ActivityDisclosure>}
  </section>;
}
