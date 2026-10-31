import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { flushSync } from "react-dom";
import { TofiIcon } from "./icons";
import { LiveMark } from "./LiveMark";
import { toolDisplayState, formatStepSeconds, orderToolActivities, toolActivityKey } from "./toolTimeline";
import { TaskIssueCard } from "./TaskIssueCard";
import { canAnswerQuestion, presentTaskIssue, taskPhaseLabel, taskPhaseVisible, type TaskOwner } from "./taskIssuePresentation";
import { useTranslation, type Language } from "./i18n";
import { REMEMBERED_APPROVAL_ACTOR, STANDING_ALLOWANCE_ACTOR, type Question } from "./questionTimeline";
import type { MailDraft } from "./MailDraftCard";
import type { Message, Run, ToolActivity, ToolActivityRunSummary } from "./types";
import { isTerminalRun } from "./runFamily";
import { prefersReducedMotion } from "./motion-lab/lib/hooks";
import { SilentNoteBody, TaskActivityBody, panelLoaded, usePanelLoaded, whenPanelLoaded } from "./lazyPanels";
import "./task-issue-card.css";
import { MOTION, EASE } from "./motion";

/**
 * One stretch of a run's calls in the conversation (see splitActivitySegments). A run cut by other items is drawn as several
 * blocks, one per segment, in time order: every block shows its own calls, the first (`index` 0) also the run's earlier attempts
 * and recorded answers, and only the last (`primary`) the live line, the issue card, pending decisions and drafts: the card
 * stays last while it waits. Without `segment` the block is the whole run, as before.
 */
export type TaskSegment = { tools: ToolActivity[]; index: number; count: number; primary: boolean; /** Progress notes created from this time (inclusive) up to `until` (exclusive) are kept in this segment. */ from?: string; until?: string };
export type TaskDetailState = { loaded: number; toolCount: number; hasMore: boolean; loading: boolean; error?: string };
export type TaskRunBlockProps = { senderHead?: React.ReactNode; owner: TaskOwner; tools: ToolActivity[]; questions: Question[]; drafts: MailDraft[]; messages: Message[]; summaries: ToolActivityRunSummary[]; details?: Record<string, TaskDetailState>; connected?: boolean; /** Sends a failed run again (the server accepts it for busy or dropped service). */ onRetryRun?: (runId: string) => void | Promise<void>; /** The run a retry of this one started: the failure card folds to a one-line note. */ retriedBy?: Run; /** Pins one language (tests, fixtures); the active UI language otherwise. */ locale?: Language; botName?: string; showName?: boolean; onFeedback?: (text:string) => void; /** The stretch of the run this block draws; absent: the whole run. */ segment?: TaskSegment; onOpenTools: () => void; onRefresh: () => Promise<void>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; liveStatus?: string; /** The run's reply is already on screen (a streaming draft with text, or its final message): the live line steps aside in that same frame instead of leaving later. */ replying?: boolean; /** Chat default: no Activity row. Cards (approvals, issues) and a live status line still show. */ hideActivity?: boolean; /** The run's Activity was asked for (message action); shows it although hidden. */ revealed?: boolean; renderMessage?: (message: Message) => ReactNode; renderQuestion: (question: Question) => ReactNode; renderDraft: (draft: MailDraft) => ReactNode };

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

/**
 * Opens the Activity drawer on its list, never empty: while the list's chunk is still downloading (it is warmed on
 * hover and in idle time) the open waits for it, then starts on the commit that has the steps in it.
 */
export function openActivity(details: HTMLDetailsElement) {
  if (panelLoaded("taskActivity")) { animateDisclosure(details, true); return; }
  void whenPanelLoaded("taskActivity").then(() => { flushSync(() => {}); animateDisclosure(details, true); });
}

function toggleActivity(event: React.MouseEvent<HTMLElement>) {
  const details = event.currentTarget.parentElement;
  if (details instanceof HTMLDetailsElement && !details.open && !details.dataset.drawer && !panelLoaded("taskActivity")) { event.preventDefault(); openActivity(details); return; }
  toggleDisclosure(event);
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
  return <details className={`task-activity${live ? " is-live" : ""}`} ref={disclosureRef} onToggle={event => { setOpen(event.currentTarget.open); if (event.currentTarget.open) onOpen(); }}><summary className="process-line" role="button" aria-expanded={open} aria-label={live ? undefined : name} onClick={toggleActivity}>{live ? <><LiveMark /><span className="process-line-text task-activity-live" role="status">{live.label}</span><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /></> : <><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /><TofiIcon className="activity-glyph" name="terminal" size={14} aria-hidden="true" /><span className="process-line-text" aria-hidden="true">{count !== undefined && <span>{count}</span>}{duration && <span className="task-activity-detail">{duration}</span>}{issues ? <span className="task-activity-problem"><TofiIcon name="alert" size={13} aria-hidden="true" />{issues}</span> : null}</span></>}</summary><div className="task-activity-records">{live && live.full.length > LONG_STATUS && <p className="task-live-full">{live.full}</p>}{children}</div></details>;
}

export const problemStates = ["failed", "not_executed", "interrupted"];

/** One durable owner replaces progress in place; actual final answers stay in chat. */
type BlockScopeInput = Pick<TaskRunBlockProps, "owner" | "tools" | "questions" | "drafts" | "messages" | "summaries" | "details" | "connected" | "locale" | "segment">;

/** Everything the block shows is derived here, so the timeline can ask whether a block draws anything without rendering it. */
function scopeTaskBlock({ owner, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale, segment }: BlockScopeInput) {
  const run = owner.family.latest;
  const ids = new Set(owner.family.attempts.map(attempt => attempt.id));
  const matches = (item: { run_id: string; conversation_id: string; bot_id: string }) => ids.has(item.run_id) && item.conversation_id === run.conversation_id && item.bot_id === run.bot_id;
  tools = orderToolActivities(tools.filter(matches));
  questions = questions.filter(matches); drafts = drafts.filter(matches);
  const segmentKeys = segment ? new Set(segment.tools.map(toolActivityKey)) : undefined;
  const shownTools = segmentKeys ? tools.filter(tool => segmentKeys.has(toolActivityKey(tool))) : tools;
  const currentTools = (segmentKeys ? shownTools : tools).filter(tool => tool.run_id === run.id);
  const currentQuestions = questions.filter(question => question.run_id === run.id);
  const currentDrafts = drafts.filter(draft => draft.run_id === run.id);
  const summary = summaries.find(item => item.run_id === run.id);
  const hasFinal = messages.some(message => message.run_id === run.id && message.conversation_id === run.conversation_id && message.role === "assistant" && !["notice", "progress", "segment"].includes(message.kind ?? "") && Boolean(message.content || message.attachments?.length));
  // A finished run that left notes but no answer (a co-addressed Bot that ended silent) would otherwise show nothing at all.
  const ofRun = (message: Message) => ids.has(message.run_id ?? "") && message.conversation_id === run.conversation_id && message.role === "assistant";
  const answered = messages.some(message => ofRun(message) && (!message.kind || message.kind === "segment") && Boolean(message.content || message.attachments?.length));
  const silentNotes = run.status === "done" && !answered && (!segment || segment.primary) ? messages.filter(message => ofRun(message) && message.kind === "progress" && Boolean(message.content.trim())).sort((a, b) => a.seq - b.seq) : [];
  const input = { run, tools: currentTools, questions: currentQuestions, drafts: currentDrafts, summary, recordsComplete: details[run.id]?.hasMore === false, connected, replied: hasFinal, locale };
  // Only the last stretch of a run carries its cards: an earlier one is history, the card belongs to what is happening now.
  const primary = !segment || segment.primary;
  const issue = primary ? presentTaskIssue({...input, family:owner.family, tools, questions, drafts}) : undefined;
  const decisions = primary ? currentQuestions.filter(question => canAnswerQuestion(question)) : [];
  return { run, ids, silentNotes, tools, shownTools, questions, drafts, currentTools, currentQuestions, currentDrafts: primary ? currentDrafts : [], input, issue, decisions };
}

/**
 * Whether the block draws anything. With Activity hidden a finished run that raised no card (no issue, no pending
 * decision or draft) draws nothing at all: no name label, no row.
 */
export function taskBlockVisible(props: BlockScopeInput & { hideActivity?: boolean; revealed?: boolean }): boolean {
  if (!props.hideActivity || props.revealed) return true;
  if (props.segment && !props.segment.primary) return false;
  const { run, issue, decisions, currentDrafts, silentNotes } = scopeTaskBlock(props);
  return !isTerminalRun(run) || silentNotes.length > 0 || Boolean(issue) || taskPhaseVisible(run) || decisions.length > 0 || currentDrafts.some(draft => draft.status !== "unknown");
}

export function TaskRunBlock({ owner, segment, tools, questions, drafts, messages, summaries, details = {}, connected = true, locale, botName, showName, senderHead, onFeedback, onOpenTools, onRefresh, onRetryRun, retriedBy, onLoadDetails, liveStatus, replying = false, hideActivity = false, revealed: revealedByMenu = false, renderMessage, renderQuestion, renderDraft }: TaskRunBlockProps) {
  const disclosure = useRef<HTMLDetailsElement>(null);
  const block = useRef<HTMLElement>(null);
  const needsFocus = useRef(false);
  const { run, ids, silentNotes, tools: scopedTools, shownTools, questions: scopedQuestions, drafts: scopedDrafts, currentTools, currentQuestions, currentDrafts, input, issue, decisions } = scopeTaskBlock({ owner, tools, questions, drafts, messages, summaries, details, connected, locale, segment });
  const primary = !segment || segment.primary, firstPart = !segment || segment.index === 0;
  tools = scopedTools; questions = scopedQuestions; drafts = scopedDrafts;
  const { t } = useTranslation("tasks", { lng: locale });
  const superseded = Boolean(issue && retriedBy);
  const retriedNote = useRef<HTMLParagraphElement>(null);
  const cardHadFocus = useRef(false);
  // The Retry button leaves the page when the card folds: keep focus in the block, without scrolling.
  useLayoutEffect(() => {
    if (superseded && cardHadFocus.current) retriedNote.current?.focus({ preventScroll: true });
    cardHadFocus.current = false;
  }, [superseded]);
  const decisionKey = decisions.map(question => question.question_id).join(":");
  useLayoutEffect(() => {
    if (needsFocus.current && !block.current?.contains(document.activeElement)) (block.current?.querySelector<HTMLElement>("h3") ?? block.current)?.focus({preventScroll:true});
    needsFocus.current = false;
    return () => { needsFocus.current = Boolean(block.current?.contains(document.activeElement) && document.activeElement?.closest(".approval-actions")); };
  }, [decisionKey, issue?.kind]);
  // Approval proposals are bookkeeping: the step itself shows whether the call
  // ran. Only answers a person gave to a real question stay as records.
  const recordedQuestions = !firstPart ? [] : questions.filter(question => question.question_type !== "approval" && !decisions.some(item => item.question_id === question.question_id));
  // A card decided by a remembered approval ("Approve and remember in this chat") stays visible as a record: nobody was asked.
  const rememberedDecisions = !firstPart ? [] : questions.filter(question => question.question_type === "approval" && (question.answered_by === REMEMBERED_APPROVAL_ACTOR || question.answered_by === STANDING_ALLOWANCE_ACTOR) && !decisions.some(item => item.question_id === question.question_id));
  const inWindow = (message: Message) => !segment || (!segment.from || Date.parse(message.created_at) >= Date.parse(segment.from)) && (!segment.until || Date.parse(message.created_at) < Date.parse(segment.until));
  const notes = messages.filter(message => ids.has(message.run_id ?? "") && message.conversation_id === run.conversation_id && message.role === "assistant" && message.kind === "progress" && inWindow(message));
  function loadInitial() {
    for (const attempt of owner.family.attempts) {
      const detail = details[attempt.id];
      if (!detail && onLoadDetails) void onLoadDetails(attempt.id, 0);
    }
  }
  const segmentSeconds = segment && segment.tools.every(tool => tool.status !== "running" && tool.status !== "queued") ? (Math.max(...segment.tools.map(tool => Date.parse(tool.updated_at))) - Math.min(...segment.tools.map(tool => Date.parse(tool.started_at)))) / 1000 : NaN;
  const toolTotal = segment ? segment.tools.length : owner.family.attempts.reduce<number | undefined>((sum, attempt) => { const count = summaries.find(item => item.run_id === attempt.id)?.tool_count ?? details[attempt.id]?.toolCount; return count === undefined || sum === undefined ? undefined : sum + count; }, 0);
  const runSeconds = segment ? segmentSeconds : run.status !== "running" && run.status !== "queued" ? (Date.parse(run.updated_at) - Date.parse(run.created_at)) / 1000 : NaN;
  const runSecondsOk = Number.isFinite(runSeconds) && runSeconds >= 0 && Boolean(toolTotal);
  const activityIssues = currentTools.filter(tool => problemStates.includes(toolDisplayState(tool))).length;
  const activityDetail = [toolTotal ? t("activity.tool_count", { count: toolTotal }) : "", Number.isFinite(runSeconds) && runSeconds >= 0 && toolTotal ? formatStepSeconds(runSeconds) : ""].filter(Boolean).join(" · ") || undefined;
  const [pendingStep, setPendingStep] = useState("");
  const bodyReady = usePanelLoaded("taskActivity");
  // While the Bot works the steps are open and the running step's clock ticks;
  // when the run ends they fold into the summary line (Motion Lab ToolSteps).
  const runLive = !isTerminalRun(run);
  const live = runLive && primary;
  const [now, setNow] = useState(() => Date.now());
  // An earlier stretch of a live run only ticks while one of its own calls is still running.
  const ticking = runLive && (primary || currentTools.some(tool => tool.status === "running"));
  useEffect(() => {
    if (!ticking) return;
    const ticker = window.setInterval(() => setNow(Date.now()), currentTools.some(tool => tool.status === "running") ? 100 : 1000);
    return () => window.clearInterval(ticker);
  }, [ticking, currentTools]);
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
    // The step may sit in an earlier stretch of the run, drawn as another block under the same owner.
    const step = block.current?.parentElement?.querySelector<HTMLLIElement>(`section[data-task-owner="${CSS.escape(owner.key)}"] li[data-step="${CSS.escape(pendingStep)}"]`) ?? block.current?.querySelector<HTMLLIElement>(`li[data-step="${CSS.escape(pendingStep)}"]`);
    if (!step) return;
    const holder = step.closest("details.task-activity");
    if (holder instanceof HTMLDetailsElement && !holder.open) holder.open = true;
    const record = step.querySelector("details");
    if (record) record.open = true;
    step.scrollIntoView({ block: "center", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    step.querySelector<HTMLElement>("summary")?.focus({ preventScroll: true });
    setPendingStep("");
  }, [pendingStep, tools, bodyReady]);
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
    openActivity(disclosure.current);
    disclosure.current.querySelector("summary")?.focus({ preventScroll: true });
    disclosure.current.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }, [showActivity, revealedByMenu]);
  function viewActivity() {
    if (disclosure.current) { openActivity(disclosure.current); disclosure.current.querySelector("summary")?.focus(); }
    else { openWhenShown.current = true; setRevealedHere(true); }
  }
  if (!taskBlockVisible({ owner, tools, questions, drafts, messages, summaries, details, connected, locale, hideActivity, revealed })) return null;
  // With Activity hidden a group block holds only cards (which carry their own sender) and the live line,
  // so it is not a sender unit: the live line names the Bot itself instead of a second avatar and caption.
  const head = senderHead && (showActivity || issue || silentNotes.length) ? senderHead : undefined;
  const namedLive = Boolean(senderHead && !head && botName);
  return <section ref={block} className={`task-run-block${senderHead ? " in-group" : ""}`} data-sender={head ? run.bot_id : undefined} data-at={head ? run.created_at : undefined} data-task-owner={owner.key} data-run-id={run.id} data-latest-run-id={run.id} data-segment={segment ? segment.index : undefined} tabIndex={-1} aria-label={t("activity.records_label")} onFocusCapture={event => { if ((event.target as Element).closest(".task-issue-card")) cardHadFocus.current = true; }} onBlurCapture={event => { if ((event.target as Element).closest(".task-issue-card") && event.relatedTarget) cardHadFocus.current = false; }}>
    {senderHead ? head : showName && botName && <p className="task-run-identity">{botName}</p>}
    {silentNotes.length > 0 && <details className="task-silent-note"><summary>{t("activity.left_note")}</summary><SilentNoteBody notes={silentNotes} /></details>}
    {issue && superseded ? <p ref={retriedNote} className="task-issue-retried" tabIndex={-1} role="status" data-issue-retried="true"><TofiIcon name="retry" size={16} aria-hidden="true" />{t("issue.retried")}</p> : issue ? <TaskIssueCard issue={issue} locale={locale} onRetry={onRetryRun ? () => onRetryRun(run.id) : undefined} onFeedback={onFeedback} onOpenStep={openStep} onAction={issue.action === "open_tools" ? onOpenTools : issue.action === "open_codex" ? () => { window.dispatchEvent(new Event("tofi:open-codex-settings")); } : issue.action === "refresh_status" ? onRefresh : viewActivity} /> : taskPhaseVisible(run) && <p className="task-run-phase">{taskPhaseLabel(input)}</p>}
    {/* A separate valid proposal survives another call's failure/unknown effect. */}
    {!showActivity && liveLine && <p className="task-run-live-line"><button type="button" className="process-line" aria-expanded="false" onClick={viewActivity}><LiveMark />{namedLive && <span className="task-activity-name">{botName}</span>}<span className="process-line-text task-activity-live" role="status">{liveLine.label}</span><TofiIcon className="process-line-chevron" name="chevron-right" size={14} aria-hidden="true" /></button></p>}
    {showActivity && <ActivityDisclosure locale={locale} detail={activityDetail} count={toolTotal || undefined} duration={runSecondsOk ? formatStepSeconds(runSeconds) : undefined} issues={activityIssues} live={liveLine} disclosureRef={disclosure} onOpen={loadInitial}>
      <TaskActivityBody owner={owner} tools={shownTools} head={firstPart} tail={primary} allTools={tools} drafts={drafts} messages={messages} summaries={summaries} details={details} live={runLive} now={now} locale={locale} recordedQuestions={recordedQuestions} rememberedDecisions={rememberedDecisions} notes={notes} onOpenTools={onOpenTools} onLoadDetails={onLoadDetails} renderMessage={renderMessage} renderQuestion={renderQuestion} renderDraft={renderDraft} />
    </ActivityDisclosure>}
    {/* The card the person must answer is the newest, bottom-most item of the turn: the process line and the activity list sit above it. */}
    {currentDrafts.filter(draft => draft.status !== "unknown").map(renderDraft)}
    {decisions.map(renderQuestion)}
  </section>;
}
