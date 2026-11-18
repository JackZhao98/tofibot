import * as Collapsible from "@radix-ui/react-collapsible";
import { LiveMark } from "../LiveMark";
import { presentTaskIssue, taskPhaseLabel } from "../taskIssuePresentation";
import { i18n, useTranslation } from "../i18n";
import { isDesktop } from "../desktop";
import { useUserTimezone } from "../UserTimezone";
import { toolDisplayState, toolActivityKey } from "../toolTimeline";
import { compareQuestionTime } from "../questionTimeline";
import { RunContextView, panelLoaded, WebToolSteps, whenPanelLoaded } from "../lazyPanels";
import { TofiIcon as Icon } from "../icons";
import { createContext, Suspense, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import { BotAvatar, type AvatarMotion } from "../BotAvatar";
import { activeBotRuns } from "../botActivity";
import { activeToolForRun, elapsedToolSeconds, isComputerAction, orderToolActivities, toolActionLabel, toolAttemptIssues } from "../toolTimeline";
import { buildRetryFamilies, isTerminalRun } from "../runFamily";
import type { Bot, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary } from "../types";
import { MOTION, EASE } from "../motion";
import { ComputerLookButton } from "../ComputerLookButton";
import { SenderHead, formatTime } from "./shared";

export type ToolDetailState = { loaded: number; toolCount: number; hasMore: boolean; loading: boolean; error?: string };
const runStatusKeys = {
  queued: "run_status.queued",
  running: "run_status.running",
  waiting: "run_status.waiting",
  done: "run_status.done",
  failed: "run_status.failed",
  cancelled: "run_status.cancelled",
  interrupted: "run_status.interrupted",
} as const satisfies Record<Run["status"], string>;
/** Live rows in the conversation time the whole run, not just its first tool. */
export const ExpiryFinishingContext = createContext<ReadonlySet<string>>(new Set());
export const RunStartContext = createContext<ReadonlyMap<string, number>>(new Map());
/** Whether chat shows Activity rows, and which runs were opened one by one from a message's action menu. */
export const ActivityViewContext = createContext<{ show: boolean; revealed: ReadonlySet<string>; /** Opens one run's steps from its live line (adds it to `revealed`, never removes). */ reveal: (runId: string) => void }>({ show: false, revealed: new Set(), reveal: () => {} });
/** The run whose steps a live line just opened: its toggle takes focus where the line was. */
export let focusToggleOfRun = "";
export function setFocusToggleOfRun(runId: string) { focusToggleOfRun = runId; }
function useNow(active: boolean, interval = 1000) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), interval);
    return () => window.clearInterval(timer);
  }, [active, interval]);
  return now;
}
/** The one compact process line of a working run: loading mark, "Browsing the web · 10s". Part of the Bot's sender group; a button when the run's steps can be opened from it. */
export function CompactLiveLine({ label, botId, botName, at, showName, ariaLabel, onOpen }: { label: string; botId: string; botName: string; at: string; showName: boolean; ariaLabel: string; onOpen?: () => void }) {
  const line = <><LiveMark /><span className="process-line-text web-tool-live-label">{label}</span>{onOpen && <Icon className="web-tool-chevron process-line-chevron" name="chevron-right" size={14} aria-hidden="true" />}</>;
  return <section className={`tool-message web-tool-run is-live live-run-status${showName ? " is-group" : ""}`} aria-label={ariaLabel} data-sender={showName ? botId : undefined} data-at={showName ? at : undefined}>
    {showName && <SenderHead id={botId} name={botName} />}
    <div className="web-tool-disclosure"><div className="web-tool-summary">{onOpen ? <button type="button" className="web-tool-toggle" aria-expanded="false" onClick={onOpen}>{line}</button> : <span className="web-tool-toggle is-static">{line}</span>}</div></div>
  </section>;
}
export function LiveRunStatus({ run, botName, showName }: { run: Run; botName: string; showName: boolean }) {
  const { t } = useTranslation("chat");
  const now = useNow(true);
  const started = Date.parse(run.created_at);
  const seconds = Number.isFinite(started) ? Math.max(0, Math.floor((now - started) / 1000)) : 0;
  const label = run.finishing_reason === "approval_expired" ? t("live.finishing") : run.status === "queued" ? t("live.waiting_start") : run.kind === "triage" ? t("live.triaging") : t("live.thinking");
  return <CompactLiveLine label={`${label}${run.finishing_reason === "approval_expired" ? "" : ` · ${formatRunDuration(seconds * 1000, false)}`}`} botId={run.bot_id} botName={botName} at={run.created_at} showName={showName} ariaLabel={t("live.status_label", { name: botName })} />;
}
/** A working run outside a task block while Activity is hidden: its current step as one line, no step list. */
function LiveToolLine({ runId, items, now, botName, senderId }: { runId: string; items: ToolActivity[]; now: number; botName: string; senderId?: string }) {
  const { t } = useTranslation("chat");
  const finishing = useContext(ExpiryFinishingContext).has(runId);
  const runStart = useContext(RunStartContext).get(runId);
  const { reveal } = useContext(ActivityViewContext);
  const current = activeToolForRun(items, runId) ?? items.find(item => item.status === "queued");
  const seconds = runStart !== undefined ? Math.max(0, Math.floor((now - runStart) / 1000)) : undefined;
  const label = finishing ? t("live.finishing") : `${current ? toolActionLabel(current) : t("live.thinking")}${seconds === undefined ? "" : ` · ${formatRunDuration(seconds * 1000, false)}`}`;
  return <CompactLiveLine label={label} botId={senderId ?? items[0]?.bot_id ?? ""} botName={botName} at={new Date(runStart ?? now).toISOString()} showName={Boolean(senderId)} ariaLabel={t("live.status_label", { name: botName })} onOpen={() => reveal(runId)} />;
}
/** Short run durations keep their tenths; longer runs read as minutes and hours. */
function formatRunDuration(ms: number, precise = true) {
  const total = ms / 1000;
  if (total < 60) return precise ? `${total.toFixed(1)}s` : `${Math.floor(total)}s`;
  const rounded = Math.round(total), hours = Math.floor(rounded / 3600), minutes = Math.floor(rounded % 3600 / 60), secs = rounded % 60;
  return hours ? i18n.t("chat:duration.hours_minutes", { hours, minutes }) : i18n.t("chat:duration.minutes_seconds", { minutes, seconds: secs });
}
export function RunStatusAnnouncement({ conversationId, runs, botById, ready, taskAnnouncements }: { conversationId: string; runs: Run[]; botById: Map<string, Bot>; ready: boolean; taskAnnouncements?: Map<string, string> }) {
  const previous = useRef<Map<string, string> | null>(null);
  const silentHistory = useRef(new Set<string>());
  const [announcement, setAnnouncement] = useState("");
  useEffect(() => {
    if (!ready) { previous.current = null; silentHistory.current.clear(); setAnnouncement(""); return; }
    const current = buildRetryFamilies(runs.filter(run => run.conversation_id === conversationId && run.kind !== "triage")).map(family => family.latest);
    const before = previous.current;
    const labels = new Map(current.map(run => [run.id, taskAnnouncements?.get(run.id) ?? taskPhaseLabel({run})]));
    previous.current = labels;
    // Initial and replacement snapshots are silent; duplicate SSE is semantic no-op.
    if (!before) { silentHistory.current = new Set(current.filter(run => isTerminalRun(run)).map(run => run.id)); return; }
    const changed = current.filter(run => !silentHistory.current.has(run.id) && before.get(run.id) !== labels.get(run.id));
    if (changed.length) setAnnouncement(changed.slice(0, 3).map(run => `${botById.get(run.bot_id)?.name ?? "Bot"}: ${labels.get(run.id)}`).join("; "));
  }, [conversationId, runs, botById, ready, taskAnnouncements]);
  useEffect(() => {
    if (!ready) return;
    const feedback = (event: Event) => { const detail = (event as CustomEvent<{conversationId:string;text:string}>).detail; if (detail?.conversationId === conversationId && typeof detail.text === "string") setAnnouncement(detail.text); };
    window.addEventListener("tofi:task-feedback", feedback);
    return () => window.removeEventListener("tofi:task-feedback", feedback);
  }, [conversationId, ready]);
  return <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement}</div>;
}
export type RunSignal = { thinking?: string; retryUntil?: number };
export function WorkingMembers({ isGroup, companionBotId, companionMotion, workingBotIds, runs, drafts, botById, toolActivities, signals = {}, connected }: { isGroup: boolean; companionBotId?: string; companionMotion?: AvatarMotion; workingBotIds: string[]; drafts: StreamDraft[]; runs: Run[]; botById: Map<string, Bot>; toolActivities: ToolActivity[]; signals?: Record<string, RunSignal>; connected: boolean }) {
  const { t } = useTranslation("chat");
  const [now, setNow] = useState(() => Date.now());
  const rowRef = useRef<HTMLDivElement | null>(null);
  const priorPositions = useRef(new Map<string, DOMRect>());
  useLayoutEffect(() => {
    const row = rowRef.current;
    if (!row) { priorPositions.current.clear(); return; }
    const next = new Map<string, DOMRect>();
    for (const element of row.querySelectorAll<HTMLElement>("[data-working-key]")) {
      const key = element.dataset.workingKey;
      if (!key) continue;
      const bounds = element.getBoundingClientRect();
      const before = priorPositions.current.get(key);
      if (before && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        const dx = before.left - bounds.left;
        const dy = before.top - bounds.top;
        if (Math.abs(dx) > 1 || Math.abs(dy) > 1) element.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "translate(0, 0)" }], { duration: MOTION.base, easing: EASE.settle });
      }
      next.set(key, bounds);
    }
    priorPositions.current = next;
  });
  const hasLiveRun = runs.some(run => run.status === "running" || run.status === "queued");
  useEffect(() => {
    if (!hasLiveRun) return;
    const interval = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(interval);
  }, [hasLiveRun]);
  // Member presence stays separate from the task's outcome presentation.
  const liveRuns = new Map([...activeBotRuns(runs, drafts, toolActivities), ...runs.filter(run => run.status === "waiting")].map(run => [run.id, run]));
  const visible = [...liveRuns.values()];
  const external = workingBotIds.filter(botId => {
    if ([...liveRuns.values()].some(run => run.bot_id === botId)) return false;
    const latest = runs.filter(run => run.bot_id === botId).sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
    return !latest || !isTerminalRun(latest);
  });
  const companionRun = companionBotId ? visible.find(run => run.bot_id === companionBotId && ["queued", "running", "waiting"].includes(run.status)) : undefined;
  const companionExternal = Boolean(companionBotId && external.includes(companionBotId));
  if (!visible.length && !external.length && !companionBotId) return null;
  const labelForRun = (run: Run) => {
    if (run.finishing_reason === "approval_expired") return t("live.finishing");
    if (!connected && ["queued", "running", "waiting"].includes(run.status)) return t("working.disconnected");
    if (run.status === "waiting") return t(runStatusKeys.waiting);
    if (run.kind === "triage" && (run.status === "queued" || run.status === "running")) return t("working.triaging");
    if (run.status === "queued") return t("live.waiting_start");
    if (run.status !== "running") return t(runStatusKeys[run.status]);
    const currentTool = activeToolForRun(toolActivities, run.id);
    const elapsed = currentTool ? elapsedToolSeconds(currentTool, now) : undefined;
    const writing = drafts.some(d => d.run_id === run.id && d.status === "active" && d.content.trim());
    const runSeconds = Math.max(0, Math.floor((now - Date.parse(run.created_at)) / 1000));
    const signal = signals[run.id];
    const retryIn = signal?.retryUntil ? Math.ceil((signal.retryUntil - now) / 1000) : 0;
    const thinking = retryIn > 0 ? t("working.busy_retry_in", { count: retryIn }) : signal?.thinking ? t("live.thinking_about", { headline: signal.thinking }) : t("live.thinking");
    if (isDesktop) return currentTool ? `${toolActionLabel(currentTool)}${elapsed === undefined ? "" : ` · ${elapsed}s`}` : writing ? `${t("working.composing")}…` : `${thinking}…`;
    const seconds = Number.isFinite(runSeconds) ? runSeconds : 0;
    return currentTool ? `${toolActionLabel(currentTool)} · ${seconds}s` : writing ? `${t("working.composing")} · ${seconds}s` : `${thinking} · ${seconds}s`;
  };
  if (isGroup) {
    const groupRuns = visible.filter(run => ["running", "waiting", "queued"].includes(run.status)).sort((a, b) => {
      const rank = (run: Run) => run.status === "running" ? 0 : run.status === "waiting" ? 1 : 2;
      return rank(a) - rank(b);
    });
    const activeIds = new Set(groupRuns.map(run => run.bot_id));
    const groupExternal = external.filter(botId => !activeIds.has(botId));
    if (!groupRuns.length && !groupExternal.length) return null;
    let queuePosition = 0;
    return <div className="working-members group-working-members" ref={rowRef} aria-label={t("working.group_queue")}>
      {groupExternal.map(botId => <div className="working-member group-working-cat is-running" data-working-key={`external-${botId}`} key={`external-${botId}`} role="group" aria-label={t("working.aria", { name: botById.get(botId)?.name ?? "Bot", status: t("working.working") })}><BotAvatar id={botId} motion="working" /></div>)}
      {groupRuns.map(run => {
        const queued = run.status !== "running";
        const position = queued ? ++queuePosition : 0;
        const name = run.kind === "triage" ? "Tofi" : botById.get(run.bot_id)?.name ?? "Bot";
        return <div className={`working-member group-working-cat${queued ? " is-queued" : " is-running"}`} data-working-key={run.id} key={run.id} role="group" aria-label={t("working.aria", { name, status: queued ? t("working.queue_position", { position }) : labelForRun(run) })}>
          <BotAvatar id={run.bot_id} motion={!connected || queued ? "awake" : "working"} />
          {queued && <span className="group-queue-index" aria-hidden="true">{position}</span>}
        </div>;
      })}
    </div>;
  }
  return <div className="working-members" ref={rowRef} aria-label={t("working.status_label")}>
    {companionBotId && <div className="working-member work-active work-companion" key={`companion-${companionBotId}`} role="group" aria-label={t("working.aria", { name: botById.get(companionBotId)?.name ?? "Bot", status: companionRun ? labelForRun(companionRun) : companionExternal ? t("working.working") : companionMotion === "sleeping" ? t("working.resting") : t("working.awake") })}>
      <BotAvatar id={companionBotId} motion={companionRun ? companionRun.status === "running" ? "working" : "awake" : companionExternal ? "working" : companionMotion ?? "sleeping"} />
      {companionRun && isDesktop && <span className="working-label">{connected ? labelForRun(companionRun) : t("working.uncertain")}</span>}
      {!companionRun && companionExternal && <span className="working-label">{t("working.working")}</span>}
    </div>}
    {visible.filter(run => run !== companionRun).map(run => {
    const waiting = run.status === "waiting";
    const active = run.status === "queued" || run.status === "running" || waiting;
    const uncertain = !connected && active;
    const label = labelForRun(run);
    const name = run.kind === "triage" ? "Tofi" : botById.get(run.bot_id)?.name ?? "Bot";
    const visibleLabel = isGroup && run.kind !== "triage" ? t("live.named", { name, status: label }) : label;
    if (active) return <div className={`working-member work-active${uncertain ? " work-uncertain" : ""}`} key={run.id} role="group" title={`${name} · ${label}`} aria-label={t("working.aria", { name, status: label })}>
      <BotAvatar id={run.bot_id} motion={!uncertain && run.status === "running" ? "working" : "awake"} />
      {isDesktop && <span className="working-label">{uncertain ? t("working.uncertain") : visibleLabel}</span>}
    </div>;
    return null;
  })}{external.filter(botId => botId !== companionBotId).map(botId => {
    const name = botById.get(botId)?.name ?? "Bot";
    return <div className="working-member work-active" key={`external-${botId}`} role="group" title={`${name} · ${t("working.working")}`} aria-label={t("working.aria", { name, status: t("working.working") })}>
      <BotAvatar id={botId} motion="working" />
      <span className="working-label">{isGroup ? t("working.named_working", { name }) : t("working.working")}</span>
    </div>;
  })}</div>;
}
export function RetryFamilyAudit({ latest, previous, botName, children }: { latest: Run; previous: Run[]; botName: string; children?: React.ReactNode }) {
  const { t } = useTranslation("chat");
  const issue = presentTaskIssue({ run:latest });
  if (!issue && !previous.length) return null;
  return <section className="run-attempt-family" data-latest-run-id={latest.id} aria-label={t("retry.status_label", { name: botName })}>
    {issue && <p>{issue.title}</p>}
    {previous.length > 0 && <details className="run-attempt-history"><summary>{t("retry.previous", { count: previous.length })}</summary>{children}</details>}
  </section>;
}
export function ToolActivityList({ activities, progress = [], summaries = [], summaryErrors = {}, summaryLoading = {}, details = {}, onLoadDetails, onRetrySummary, botById, activeRunIds, group = false }: { group?: boolean; activities: ToolActivity[]; progress?: Message[]; summaries?: ToolActivityRunSummary[]; summaryErrors?: Record<string, string>; summaryLoading?: Record<string, boolean>; details?: Record<string, ToolDetailState>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; onRetrySummary?: (runId: string) => void; botById: Map<string, Bot>; activeRunIds: Set<string> }) {
  const { t } = useTranslation("chat");
  const { timezone } = useUserTimezone();
  const [now, setNow] = useState(() => Date.now());
  const hasLiveTool = activities.some(activity => activeRunIds.has(activity.run_id));
  useEffect(() => {
    if (!hasLiveTool) return;
    const interval = window.setInterval(() => setNow(Date.now()), isDesktop ? 1000 : 100);
    return () => window.clearInterval(interval);
  }, [hasLiveTool]);
  const view = useContext(ActivityViewContext);
  if (!activities.length && !progress.length && !summaries.length && !Object.keys(summaryErrors).length && !Object.keys(summaryLoading).length) return null;
  const summariesByRunID = new Map(summaries.map(summary => [summary.run_id, summary]));
  const grouped = [...new Set([...activities.map((a) => a.run_id), ...progress.map(message => message.run_id).filter((id): id is string => Boolean(id)), ...summaries.map(summary => summary.run_id), ...Object.keys(summaryErrors), ...Object.keys(summaryLoading)])].map((runId) => {
    const allItems = orderToolActivities(activities.filter((a) => a.run_id === runId));
    const notes = progress.filter(message => message.run_id === runId).sort((a, b) => a.seq - b.seq);
    return { runId, items: allItems, notes, summary: summariesByRunID.get(runId), summaryError: summaryErrors[runId], summaryLoading: summaryLoading[runId] };
  }).sort((a, b) => compareQuestionTime(a.notes[0]?.created_at ?? a.items[0]?.started_at ?? a.summary?.started_at ?? "", b.notes[0]?.created_at ?? b.items[0]?.started_at ?? b.summary?.started_at ?? "") || a.runId.localeCompare(b.runId));
  const shown = grouped.filter(group => view.show || view.revealed.has(group.runId) || activeRunIds.has(group.runId));
  if (!shown.length) return null;
  return <div className="work-details" aria-label={t("tools.details")}>{shown.map(({ runId, items, notes, summary, summaryError, summaryLoading: loadingSummary }) => !view.show && !view.revealed.has(runId) ? <LiveToolLine key={runId} runId={runId} items={items} now={now} botName={botById.get(items[0]?.bot_id ?? notes[0]?.sender_bot_id ?? summary?.bot_id ?? "")?.name ?? "Bot"} senderId={group ? items[0]?.bot_id ?? notes[0]?.sender_bot_id ?? summary?.bot_id ?? "" : undefined} /> : <ToolActivityRun key={runId} runId={runId} items={items} notes={notes} summary={summary} summaryError={summaryError} summaryLoading={loadingSummary} detail={details[runId]} onLoadDetails={onLoadDetails} onRetrySummary={onRetrySummary} live={activeRunIds.has(runId)} now={now} timezone={timezone} botName={botById.get(items[0]?.bot_id ?? notes[0]?.sender_bot_id ?? summary?.bot_id ?? "")?.name ?? "Bot"} senderId={group ? items[0]?.bot_id ?? notes[0]?.sender_bot_id ?? summary?.bot_id ?? "" : undefined} />)}</div>;
}
type ToolRunProps = { senderId?: string; runId: string; items: ToolActivity[]; notes: Message[]; summary?: ToolActivityRunSummary; summaryError?: string; summaryLoading?: boolean; detail?: ToolDetailState; onLoadDetails?: (runId: string, offset: number) => Promise<void>; onRetrySummary?: (runId: string) => void; live: boolean; now: number; timezone: string; botName: string };
function toolSummaryIssues(summary?: ToolActivityRunSummary): string {
  if (!summary) return "";
  return [summary.failed_count ? i18n.t("chat:tools.failed_count", { count: summary.failed_count }) : "", summary.interrupted_count ? i18n.t("chat:tools.interrupted_count", { count: summary.interrupted_count }) : "", summary.pending_count ? i18n.t("chat:tools.pending_count", { count: summary.pending_count }) : "", summary.expired_count ? i18n.t("chat:tools.expired") : "", summary.skipped_count ? i18n.t("chat:tools.skipped_count", { count: summary.skipped_count }) : ""].filter(Boolean).join(" · ");
}
function toolRunTimes(items: ToolActivity[], notes: Message[], summary?: ToolActivityRunSummary) {
  const starts = [...items.map(item => Date.parse(item.started_at)), ...notes.map(note => Date.parse(note.created_at)), Date.parse(summary?.started_at ?? "")].filter(Number.isFinite);
  const ends = [...items.map(item => Date.parse(item.updated_at)), ...notes.map(note => Date.parse(note.created_at)), Date.parse(summary?.updated_at ?? "")].filter(Number.isFinite);
  return { first: starts.length ? Math.min(...starts) : Number.NaN, last: ends.length ? Math.max(...ends) : Number.NaN };
}
/** The "Context" row at the foot of a reply's activity: what the model received (D53). The view is its own chunk, fetched on first open. */
function RunContextEntry({ runId, timezone }: { runId: string; timezone: string }) {
  const { t } = useTranslation("chat");
  const [open, setOpen] = useState(false);
  return <Collapsible.Root className="run-context-entry" open={open} onOpenChange={setOpen}>
    <Collapsible.Trigger className="run-context-toggle"><Icon className="web-tool-chevron" name="chevron-right" size={14} /><span>{t("runContext.toggle")}</span></Collapsible.Trigger>
    <Collapsible.Content className="collapsible-content"><Suspense fallback={null}><RunContextView runId={runId} timezone={timezone} /></Suspense></Collapsible.Content>
  </Collapsible.Root>;
}
export function WebToolActivityRun({ runId, items, notes, summary, summaryError, summaryLoading, detail, onLoadDetails, onRetrySummary, live, now, timezone, botName, senderId }: ToolRunProps) {
  const { t } = useTranslation(["chat", "common"]);
  const finishing = useContext(ExpiryFinishingContext).has(runId);
  const revealedHere = useContext(ActivityViewContext).revealed.has(runId);
  const [expanded, setExpanded] = useState(revealedHere);
  const stepsRef = useRef<HTMLOListElement>(null);
  const toggleRef = useRef<HTMLButtonElement>(null);
  const labelRef = useRef<HTMLSpanElement>(null);
  // Set when the one-line label was cut at the moment the steps opened: the drawer then carries it whole.
  const [clipped, setClipped] = useState(false);
  const priorStepKeys = useRef("");
  const priorStepsHeight = useRef(0);
  const growthAnimation = useRef<Animation | null>(null);
  const times = toolRunTimes(items, notes, summary);
  const lastTime = live && !finishing ? now : times.last;
  const firstTime = times.first;
  const duration = Number.isFinite(firstTime) && Number.isFinite(lastTime) && lastTime >= firstTime ? formatRunDuration(lastTime - firstTime) : undefined;
  const current = activeToolForRun(items, runId) ?? items.find(item => item.status === "queued");
  const count = summary?.tool_count ?? (summaryError || summaryLoading ? undefined : items.length);
  const issues = live
    ? [summary?.failed_count ?? items.filter(item => item.status === "failed" && toolDisplayState(item) === "failed").length, summary?.interrupted_count ?? items.filter(item => item.status === "interrupted").length]
        .map((value, index) => value ? t(index ? "tools.interrupted_count" : "tools.failed_count", { count: value }) : "").filter(Boolean).join(" · ")
    : summary ? toolSummaryIssues(summary) : toolAttemptIssues(items);
  const runStart = useContext(RunStartContext).get(runId);
  const liveSeconds = live && runStart !== undefined ? Math.max(0, Math.floor((now - runStart) / 1000)) : undefined;
  const liveTimer = liveSeconds === undefined ? "…" : ` · ${formatRunDuration(liveSeconds * 1000, false)}`;
  const activityLabel = finishing ? t("live.finishing") : live ? current?.status === "queued" ? `${t("tools.preparing")}${liveTimer}` : current ? `${t("tools.busy")}${liveTimer}` : `${t("live.thinking")}${liveTimer}` : summaryError ? t("tools.details_unavailable") : issues ? t("tools.incomplete") : t("tools.process");
  const entries = [...items.map(activity => ({ type: "tool" as const, at: activity.started_at, activity })), ...notes.map(note => ({ type: "note" as const, at: note.created_at, note }))].sort((a, b) => compareQuestionTime(a.at, b.at));
  const hasMoreDetails = Boolean(detail?.hasMore && (count === undefined || items.length < count));
  const loadDetails = () => {
    if (!onLoadDetails || detail?.loading || (count !== undefined && items.length >= count) || (detail?.hasMore === false && detail !== undefined)) return;
    void onLoadDetails(runId, detail?.loaded ?? 0);
  };
  const stepKeys = entries.map(entry => entry.type === "tool" ? toolActivityKey(entry.activity) : entry.note.id).join("\u001f");
  useLayoutEffect(() => {
    const steps = stepsRef.current;
    if (!steps) return;
    const nextHeight = steps.scrollHeight;
    const added = stepKeys && stepKeys !== priorStepKeys.current && entries.length > (priorStepKeys.current ? priorStepKeys.current.split("\u001f").length : 0);
    const fromHeight = priorStepsHeight.current;
    priorStepKeys.current = stepKeys;
    priorStepsHeight.current = nextHeight;
    if (!live || !expanded || !added || window.matchMedia("(prefers-reduced-motion: reduce)").matches || nextHeight <= fromHeight) return;
    growthAnimation.current?.cancel();
    steps.style.overflow = "hidden";
    const animation = steps.animate([{ height: `${fromHeight}px` }, { height: `${nextHeight}px` }], { duration: MOTION.panelIn, easing: EASE.out });
    growthAnimation.current = animation;
    void animation.finished.finally(() => { if (growthAnimation.current === animation) { growthAnimation.current = null; steps.style.overflow = ""; } }).catch(() => undefined);
  }, [entries.length, expanded, live, stepKeys]);
  useEffect(() => () => { growthAnimation.current?.cancel(); }, []);
  // Opened from a message's "View activity": the steps are wanted at once.
  useEffect(() => { if (revealedHere) loadDetails(); }, []); // eslint-disable-line react-hooks/exhaustive-deps
  // Opened from this run's live line: focus stays where the line was.
  useLayoutEffect(() => { if (focusToggleOfRun === runId) { focusToggleOfRun = ""; toggleRef.current?.focus({ preventScroll: true }); } }, [runId]);
  const issueCount = summary ? summary.failed_count + summary.interrupted_count + summary.pending_count : items.filter(item => ["failed", "not_executed", "interrupted"].includes(toolDisplayState(item))).length;
  const fullLabel = `${activityLabel}${!live && count !== undefined ? ` · ${t("tools.call_count", { count })}` : ""}${issues ? ` · ${issues}` : ""}${notes.length ? ` · ${t("tools.report_count", { count: notes.length })}` : ""}${!live && duration !== undefined ? ` · ${duration}` : ""}`;
  return <section data-activity-run={runId} className={`tool-message web-tool-run${live ? " is-live" : ` is-complete${issues ? " has-tool-issues" : ""}`}`} aria-label={t("tools.steps_label", { name: botName })} data-sender={senderId || undefined} data-at={senderId && Number.isFinite(lastTime) ? new Date(lastTime).toISOString() : undefined}>
    {senderId && <SenderHead id={senderId} name={botName} />}
    <Collapsible.Root className="web-tool-disclosure" open={expanded} onOpenChange={next => {
      const label = labelRef.current;
      if (next) { if (label && label.scrollWidth > label.clientWidth) setClipped(true); loadDetails(); }
      // The drawer opens on its steps, never empty: a cold chunk is waited for (it is warmed on hover) before the open starts.
      if (next && !panelLoaded("toolSteps")) void whenPanelLoaded("toolSteps").then(() => setExpanded(true));
      else setExpanded(next);
    }}>
      <div className="web-tool-summary">
        {!live && Number.isFinite(lastTime) && <time>{formatTime(new Date(lastTime).toISOString(), timezone)}</time>}
        <Collapsible.Trigger asChild><button type="button" ref={toggleRef} className="web-tool-toggle" aria-label={live ? undefined : fullLabel}>
          {live && <LiveMark />}
          {!live && <Icon className="web-tool-chevron" name="chevron-right" size={14} />}
          {live ? <span ref={labelRef} className="process-line-text web-tool-live-label">{fullLabel}</span>
            : <><Icon className="web-tool-glyph" name="terminal" size={14} aria-hidden="true" /><span ref={labelRef} className="process-line-text web-tool-compact" aria-hidden="true">{count !== undefined && <span>{count}</span>}{duration !== undefined && <span className="web-tool-quiet">{duration}</span>}{issueCount > 0 && <span className="web-tool-issues"><Icon name="alert" size={13} aria-hidden="true" />{issueCount}</span>}{notes.length > 0 && <span className="web-tool-quiet"><Icon name="file-text" size={13} aria-hidden="true" />{notes.length}</span>}</span></>}
          {live && <Icon className="web-tool-chevron" name="chevron-right" size={14} aria-hidden="true" />}
        </button></Collapsible.Trigger>
        {live && current && isComputerAction(current) && <ComputerLookButton activity={current} />}
      </div>
      <Collapsible.Content forceMount className={`web-tool-drawer collapsible-content${expanded ? " is-open" : ""}`} inert={!expanded}><div className="web-tool-drawer-inner">
      {clipped && <p className="web-tool-full">{fullLabel}</p>}
      <ol className="web-tool-steps" ref={stepsRef}>{summaryLoading && <li className="web-tool-step is-note"><span>{t("tools.summary_loading")}</span></li>}{summaryError && <li className="web-tool-step is-note"><span>{t("tools.summary_failed", { error: summaryError })}</span><button type="button" className="secondary-button" onClick={() => onRetrySummary?.(runId)}>{t("common:action.retry")}</button></li>}{!entries.length && <li className="web-tool-step is-note"><span>{detail?.loading ? t("tools.details_loading") : detail?.error ? t("tools.details_failed", { error: detail.error }) : t("tools.expand_to_load")}</span></li>}<WebToolSteps entries={entries} live={live} now={now} timezone={timezone} />{hasMoreDetails && <li className="web-tool-step is-note"><button type="button" className="secondary-button" disabled={detail?.loading} onClick={loadDetails}>{detail?.loading ? t("action.loading") : t("tools.load_more", { shown: items.length, total: count ?? "?" })}</button></li>}</ol>
      <RunContextEntry runId={runId} timezone={timezone} />
      </div></Collapsible.Content>
    </Collapsible.Root>
  </section>;
}
/** One tool run, the same in the browser and the desktop app. */
function ToolActivityRun(props: ToolRunProps) {
  return <WebToolActivityRun {...props} />;
}
