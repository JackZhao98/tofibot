import { TaskRunBlock } from "./TaskRunBlock";
import { buildTaskOwners, presentTaskIssue, taskPhaseLabel, type TaskOwner } from "./taskIssuePresentation";
import { i18n, useTranslation } from "./i18n";
import { MemoryPanel } from "./MemoryPanel";
import { memoryDisplay } from "./displayMetadata";
import { mergeMessageTimeline } from "./messageTimeline";
import { textareaCaretRect } from "./textareaCaret";
import { DelayedFeedback } from "./DelayedFeedback";
import { WebFileDropOverlay } from "./WebFileDropOverlay";
import { SidebarAccount } from "./OwnerSession";
import { UpdateBanner } from "./UpdateNotice";
import { desktopState, updateDesktopState, isDesktop, type DesktopCommand } from "./desktop";
import {TimezoneProvider, useUserTimezone} from "./UserTimezone";
import {dateInTimezone, formatZonedTime} from "./timezone";
import {MessageAttachment} from "./MessageAttachment";
import {MessageReactions} from "./MessageReactions";
import { DisplayCard } from "./DisplayCard";
import { QuestionCard, useQuestions } from "./QuestionCard";
import { toolDisplayState, toolDisplayLabel } from "./toolTimeline";
import { taskStateLabel } from "./ConversationTaskStatus";
import { MailDraftCard, useMailDrafts } from "./MailDraftCard";
import { buildQuestionTimeline, compareQuestionTime } from "./questionTimeline";
import { BotInspector } from "./BotInspector";
import { useDebugMode } from "./debugMode";
import { SettingsShell, type SettingsEntry, type SettingsTab, type SettingsView } from "./SettingsShell";
import { SettingsPages } from "./settings/SettingsPages";
import { subscribeSettingsDeepLinks } from "./settings/deepLinks";
import { StatusBadge } from "./settings/components";
import { ModelFields } from "./ModelSettings";
import { followsGlobal } from "./modelCatalog";
import { SecretInputs } from "./SecretInputCard";
import { BotIdentityCard } from "./BotIdentityCard";
import { ActionHints, ConfirmAction, Disclosure, useAppearance, useSurfacePresence } from "./InteractionSystem";
import { useConversationScroll } from "./useConversationScroll";
import { conversationPath, readConversationRoute } from "./conversationRoute";
import { useConversationNavigation } from "./useConversationNavigation";
import { BotDesktopPanel } from "./BotDesktopPanel";
import { FloatingDesktop } from "./FloatingDesktop";
import { useDesktopPresence } from "./desktopPresence";
import { WorkPanel, WorkPreview } from "./WorkPanel";
import { TeamBoard } from "./TeamBoard";
import { ArchivePanel } from "./ArchivePanel";
import { DeleteConversationDialog, type DeleteTarget } from "./DeleteConversationDialog";
import { ViewOnlyChat, type ViewOnlyChatTarget } from "./ViewOnlyChat";
import { ScheduledTaskRow } from "./ScheduledTaskRow";
import { occurrenceRootKey, type ScheduleOccurrence } from "./scheduleOccurrences";
import { useScheduleOccurrences } from "./useScheduleOccurrences";
import { useScheduleMetadata } from "./useScheduleMetadata";
import type { Schedule } from "./types";
import { TofiIcon as Icon, type TofiIconName } from "./icons";
import { ComposerGlyph } from "./ComposerGlyph";
import { intlLocale } from "./i18n/format";
import { BrandLogo } from "./BrandLogo";
import { createContext, lazy, Suspense, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { api, ApiError, openConversationEvents, openWorkspaceEvents } from "./api";
import { MessageMarkdown } from "./MessageMarkdown";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { CatStage, type CatHandle } from "./CatStage";
import { CATS, EmptyCat, EmptyState } from "./EmptyCat";
import { FirstRunHero } from "./FirstRunHero";
import { Onboarding } from "./Onboarding";
import { FinishSetupChip, ModelBanner } from "./OnboardingChrome";
import { chipStep, onboardingMode, parseOnboardingState, resumeStep, type OnboardingState, type OnboardingStep } from "./onboardingFlow";
import { LoadingCat } from "./motion-lab/lib/LoadingCat";
import { flipList } from "./motion-lab/lib/flip";
import { flySentMessage } from "./sendFlight";
import { activeBotRuns, botRecentlyActive, latestBotWorkTime, latestHumanMessageTime } from "./botActivity";
import { reconcileToolActivity, activeToolForRun, buildToolRunAnchors, buildToolSummaryAnchors, buildToolTimeline, compareToolActivities, elapsedToolSeconds, orderToolActivities, toolActionLabel, toolArgumentPreview, toolAttemptIssues } from "./toolTimeline";
import { foldCompletedProgress } from "./runProgress";
import { mergeRunUpdates } from "./runMerge";
import { buildRetryFamilies, isTerminalRun, retryFamilyAnchor } from "./runFamily";
import { composerClientMessageId, composerDraftMatchesMessage, composerMessageContent, composerMessageSignature, type ComposerReply } from "./composerDraft";
import { useDictation } from "./useDictation";
import { useSmoothStreamText } from "./useSmoothStreamText";
import { PermissionCoach } from "./PermissionCoach";
import { observeVisualViewport, visualViewportMetrics } from "./visualViewport";
import { buildBotPackage, downloadBotPackage } from "./botPackage";
import { BotAvatarPicker } from "./BotAvatarPicker";
import { getBotAvatarConfig, saveBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import type { Attachment, Bot, ComputerHealth, Config, Conversation, EventEnvelope, Memory, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary, WorkspaceEventEnvelope } from "./types";

const TerminalPanel = lazy(() => import("./TerminalPanel").then(module => ({ default: module.TerminalPanel })));

type Panel = "group-create" | "bot-edit" | "memory" | "schedule" | "settings" | "members" | "desktop" | "terminal" | "archive" | null;
type SidebarLayout = { pinned: string[]; sections: Record<string, string> };
type SidebarContextMenu = { x: number; y: number; conversation: Conversation };
type ToolDetailState = { loaded: number; toolCount: number; hasMore: boolean; loading: boolean; error?: string };
type ToolSummaryState = { loading: boolean; error?: string };

function loadedToolRunIDs(messages: Message[], runs: Run[] = []) {
  const messageIDs = new Set(messages.map(message => message.id));
  const runIDs = new Set(messages.map(message => message.run_id).filter((id): id is string => Boolean(id)));
  for (const run of runs) if (run.trigger_message_id && messageIDs.has(run.trigger_message_id)) runIDs.add(run.id);
  return [...runIDs];
}

function terminalToolRunIDs(messages: Message[], runs: Run[]) {
  const runByID = new Map(runs.map(run => [run.id, run]));
  return loadedToolRunIDs(messages, runs).filter(id => {
    const status = runByID.get(id)?.status;
    return status !== undefined && status !== "queued" && status !== "running" && status !== "waiting";
  });
}

function readSidebarLayout(): SidebarLayout {
  try {
    const value = JSON.parse(localStorage.getItem("tofi:sidebar-layout") ?? "null") as Partial<SidebarLayout> | null;
    return { pinned: Array.isArray(value?.pinned) ? value!.pinned!.filter((id): id is string => typeof id === "string") : [], sections: value?.sections && typeof value.sections === "object" ? value.sections as Record<string, string> : {} };
  } catch {
    return { pinned: [], sections: {} };
  }
}

function relatedChatTarget(message: Message, source: Conversation, conversations: Conversation[], bots: Bot[]): ViewOnlyChatTarget | null {
  const targetID = message.notice?.target_conversation_id;
  if (!targetID || targetID === source.id || (message.kind !== "notice" && message.kind !== "forward_result" && message.kind !== "message_ref")) return null;
  const fromBotID = message.notice?.from_bot_id ?? "";
  const rawTargetIDs = message.notice?.target_bot_ids?.length ? message.notice.target_bot_ids : message.notice?.to_bot_ids ?? [message.notice?.to_bot_id].filter((id): id is string => Boolean(id));
  const targetBotIDs = Array.from(new Set(rawTargetIDs.filter((id) => id !== fromBotID)));
  const botName = (id: string) => bots.find((bot) => bot.id === id)?.name ?? id;
  const sourceName = fromBotID ? botName(fromBotID) : source.name;
  const syntheticName = message.notice?.target_name?.includes(" ↔ ") ? "" : message.notice?.target_name ?? "";
  const targetName = syntheticName || targetBotIDs.map(botName).join(i18n.t("chat:list.separator")) || i18n.t("chat:related.bot_conversation");
  const listed = conversations.find((conversation) => conversation.id === targetID);
  const target = listed ?? {
    id: targetID,
    kind: "group" as const,
    name: targetName,
    bot_ids: targetBotIDs,
    updated_at: message.created_at,
    user_visible: false,
  };
  if (!target) return null;
  const sentBySource = Boolean(fromBotID && (source.bot_id === fromBotID || source.bot_ids.includes(fromBotID)));
  return { source, target, sourceBotIds: fromBotID ? [fromBotID] : undefined, targetBotIds: targetBotIDs, sourceName, targetName, runId: message.notice?.target_run_id || undefined, label: sentBySource ? "Messaged" : "Message from" };
}

type ComposerAttachment = {
  key: string;
  id?: string;
  name: string;
  size: number;
  lastModified: number;
  type: string;
  file?: File;
};
type ComposerDraft = {
  content: string;
  attachments: ComposerAttachment[];
  reply?: ComposerReply;
  pending?: { signature: string; id: string };
};
type StoredComposerDraft = Omit<ComposerDraft, "attachments"> & { attachments: Omit<ComposerAttachment, "file">[] };
type ComposerDrafts = Record<string, ComposerDraft>;

const emptyComposerDraft = (): ComposerDraft => ({ content: "", attachments: [] });

function composerDraftStorageKey(instanceId: string) {
  return `tofi:composer-drafts:${window.location.origin}:${instanceId}`;
}

function readComposerDrafts(key: string): ComposerDrafts {
  try {
    const nativeDrafts = isDesktop ? desktopState().drafts : undefined;
    const raw = nativeDrafts ? JSON.stringify(nativeDrafts) : sessionStorage.getItem(key);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, StoredComposerDraft>;
    if (!parsed || typeof parsed !== "object") return {};
    return Object.fromEntries(Object.entries(parsed).flatMap(([id, draft]) => {
      if (!draft || typeof draft.content !== "string" || !Array.isArray(draft.attachments)) return [];
      const attachments = draft.attachments.filter((attachment) => attachment && typeof attachment.key === "string" && typeof attachment.name === "string" && Number.isFinite(attachment.size) && Number.isFinite(attachment.lastModified)).map((attachment) => ({ ...attachment }));
      const reply = draft.reply && typeof draft.reply.id === "string" && typeof draft.reply.name === "string" && typeof draft.reply.excerpt === "string" ? { id: draft.reply.id, name: draft.reply.name, excerpt: draft.reply.excerpt } : undefined;
      return [[id, { content: draft.content, attachments, reply, pending: draft.pending && typeof draft.pending.id === "string" && typeof draft.pending.signature === "string" ? draft.pending : undefined }]];
    }));
  } catch {
    return {};
  }
}

function writeComposerDrafts(key: string, drafts: ComposerDrafts) {
  try {
    const stored: Record<string, StoredComposerDraft> = Object.fromEntries(Object.entries(drafts).map(([id, draft]) => [id, { content: draft.content, reply: draft.reply, pending: draft.pending, attachments: draft.attachments.map(({ file: _file, ...attachment }) => attachment) }]));
    sessionStorage.setItem(key, JSON.stringify(stored));
    updateDesktopState({ drafts: stored });
  } catch {
    // The in-memory draft remains authoritative when storage is unavailable.
  }
}

function Avatar({ id, group = false, mini = false, motion }: { label: string; id: string; group?: boolean; mini?: boolean; motion?: AvatarMotion }) {
  if (group) return <span className={`avatar group-avatar ${mini ? "mini" : ""}`} aria-hidden="true"><Icon name="group" size={mini ? 14 : 19} /></span>;
  return <BotAvatar id={id} mini={mini} motion={motion} />;
}

const runStatusKeys = {
  queued: "run_status.queued",
  running: "run_status.running",
  waiting: "run_status.waiting",
  done: "run_status.done",
  failed: "run_status.failed",
  cancelled: "run_status.cancelled",
  interrupted: "run_status.interrupted",
} as const satisfies Record<Run["status"], string>;
/** The watchdog restarts an unresponsive computer; only these states need the person's attention. */
const computerHealthAlert = (health?: ComputerHealth) => health?.state === "restarting" || health?.state === "unresponsive";
const computerPhaseKeys: Record<string, "computer_phase.checking" | "computer_phase.storage" | "computer_phase.network" | "computer_phase.booting" | "computer_phase.restoring" | "computer_phase.verifying" | "computer_phase.ready"> = { checking: "computer_phase.checking", storage: "computer_phase.storage", network: "computer_phase.network", booting: "computer_phase.booting", restoring: "computer_phase.restoring", verifying: "computer_phase.verifying", ready: "computer_phase.ready" };

function asMessage(data: EventEnvelope): Message | null {
  const value = data.message ?? data;
  return typeof value === "object" && value !== null && "id" in value ? (value as Message) : null;
}

function asRun(data: EventEnvelope): (Partial<Run> & { id: string }) | null {
  const value = data.run ?? data;
  if (typeof value !== "object" || value === null) return null;
  const id = "id" in value && typeof value.id === "string" ? value.id : typeof data.run_id === "string" ? data.run_id : "";
  return id ? ({ ...value, id } as Partial<Run> & { id: string }) : null;
}

function asMemory(data: EventEnvelope): Memory | null {
  const value = data.memory ?? data;
  return typeof value === "object" && value !== null && "id" in value ? (value as Memory) : null;
}

function asToolActivity(data: EventEnvelope): ToolActivity | null {
  const value = data.activity ?? data.tool_activity ?? data;
  if (typeof value !== "object" || value === null) return null;
  const candidate = value as Partial<ToolActivity>;
  return typeof candidate.run_id === "string" && typeof candidate.call_id === "string" && typeof candidate.name === "string" && typeof candidate.status === "string" ? candidate as ToolActivity : null;
}

function asBot(data: EventEnvelope): Bot | null {
  const value = data.bot ?? data;
  if (typeof value !== "object" || value === null) return null;
  const candidate = value as Partial<Bot>;
  return typeof candidate.id === "string" && typeof candidate.name === "string" &&
    typeof candidate.dm_conversation_id === "string" ? value as Bot : null;
}

function toolActivityKey(activity: Pick<ToolActivity, "run_id" | "call_id">) {
  return `${activity.run_id}\u0000${activity.call_id}`;
}

function messageId() {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // LAN HTTP does not expose randomUUID, but still supports getRandomValues.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function formatHoverTime(value: string, timeZone: string) {
  const sameDay = dateInTimezone(value, timeZone) === dateInTimezone(new Date().toISOString(), timeZone);
  return new Intl.DateTimeFormat(intlLocale(), { ...(sameDay ? {} : { month: "numeric", day: "numeric" }), hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}

/** Browser-mode message actions, revealed on hover or keyboard focus instead of a context menu. */
function MessageHoverActions({ message, timezone, onReact, onReply }: { message: Message; timezone: string; onReact: (anchor: DOMRect) => void; onReply?: () => void }) {
  const { t } = useTranslation("chat");
  return <div className="message-hover-actions" role="toolbar" aria-label={t("hover.toolbar")}>
    <time dateTime={message.created_at} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}>{formatHoverTime(message.created_at, timezone)}</time>
    <button type="button" aria-label={t("hover.react")} data-hint={t("hover.react")} onClick={event => onReact(event.currentTarget.getBoundingClientRect())}><Icon name="emoji" size={16} /></button>
    {onReply && <button type="button" aria-label={t("hover.reply")} data-hint={t("hover.reply")} onClick={onReply}><Icon name="reply" size={16} /></button>}
  </div>;
}

/** Scrolls to a message and flashes it once without leaving a persistent frame. */
function jumpToMessage(target: HTMLElement | null | undefined) {
  if (!target) return;
  target.scrollIntoView({ behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "instant" : "smooth", block: "center" });
  target.focus({ preventScroll: true });
  const block = target.closest<HTMLElement>(".message-block") ?? target;
  block.classList.remove("is-jump-target");
  void block.offsetWidth;
  block.classList.add("is-jump-target");
  window.setTimeout(() => block.classList.remove("is-jump-target"), 1700);
}

/** Live rows in the conversation time the whole run, not just its first tool. */
const ExpiryFinishingContext = createContext<ReadonlySet<string>>(new Set());
const RunStartContext = createContext<ReadonlyMap<string, number>>(new Map());

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

/** Shown in the conversation while a run has neither tool steps nor reply text yet. */
function LiveRunStatus({ run, botName, showName }: { run: Run; botName: string; showName: boolean }) {
  const { t } = useTranslation("chat");
  const now = useNow(true);
  const started = Date.parse(run.created_at);
  const seconds = Number.isFinite(started) ? Math.max(0, Math.floor((now - started) / 1000)) : 0;
  const label = run.finishing_reason === "approval_expired" ? t("live.finishing") : run.status === "queued" ? t("live.waiting_start") : run.kind === "triage" ? t("live.triaging") : t("live.thinking");
  return <section className={`tool-message web-tool-run is-live live-run-status${showName ? " is-group" : ""}`} aria-label={t("live.status_label", { name: botName })}>
    <div className="web-tool-disclosure"><div className="web-tool-summary"><span className="web-tool-toggle"><span className="web-tool-live-label">{showName ? t("live.named", { name: botName, status: label }) : label}{run.finishing_reason === "approval_expired" ? "" : ` · ${formatRunDuration(seconds * 1000, false)}`}</span></span></div></div>
  </section>;
}

/** Short run durations keep their tenths; longer runs read as minutes and hours. */
function formatRunDuration(ms: number, precise = true) {
  const total = ms / 1000;
  if (total < 60) return precise ? `${total.toFixed(1)}s` : `${Math.floor(total)}s`;
  const rounded = Math.round(total), hours = Math.floor(rounded / 3600), minutes = Math.floor(rounded % 3600 / 60), secs = rounded % 60;
  return hours ? i18n.t("chat:duration.hours_minutes", { hours, minutes }) : i18n.t("chat:duration.minutes_seconds", { minutes, seconds: secs });
}
function formatTime(value: string, timeZone: string) {
  return new Intl.DateTimeFormat(intlLocale(), { hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}

function formatExactTime(value: string, timeZone?: string) {
  return new Intl.DateTimeFormat(intlLocale(), { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone }).format(new Date(value));
}

function formatListTime(value: string, timeZone: string) {
  const date = new Date(value);
  const now = new Date();
  if (dateInTimezone(value, timeZone) === dateInTimezone(now.toISOString(), timeZone)) return new Intl.DateTimeFormat(intlLocale(), { hour: "2-digit", minute: "2-digit", timeZone }).format(date);
  return new Intl.DateTimeFormat(intlLocale(), { month: "numeric", day: "numeric", timeZone }).format(date);
}

function formatDay(value: string, timeZone: string) {
  return new Intl.DateTimeFormat(intlLocale(), { month: "long", day: "numeric", weekday: "short", timeZone }).format(new Date(value));
}

function previewText(value: string, max = 160) {
  const normalized = readReplyMarker(value).body.replace(/\s+/g, " ").replace(/[*_`#>]/g, "").replace(/\s+/g, " ").trim();
  const chars = Array.from(normalized);
  return chars.length > max ? `${chars.slice(0, max - 1).join("")}…` : normalized;
}

function formatDateDivider(value: string, timeZone: string) {
  return `${formatDay(value, timeZone)} ${formatTime(value, timeZone)}`;
}

function errorText(error: unknown) {
  if (error instanceof ApiError && error.code === "archive_busy") return i18n.t("chat:apiError.archive_busy");
  if (error instanceof ApiError && error.code === "archive_blocked") return i18n.t("chat:apiError.archive_blocked");
  if (error instanceof ApiError && error.code === "no_active_members") return i18n.t("chat:apiError.no_active_members");
  if (error instanceof ApiError && error.status === 503) {
    return error.code === "model_unconfigured" ? i18n.t("chat:apiError.model_unconfigured") : i18n.t("chat:error.service_unavailable");
  }
  return error instanceof Error ? error.message : i18n.t("chat:error.generic");
}

function App() {
  return <TimezoneProvider><Workspace /></TimezoneProvider>;
}

function Workspace() {
  const { t } = useTranslation(["chat", "common"]);
  const {timezone} = useUserTimezone();
  const appearance = useAppearance();
  const [sidebarCollapsed, setSidebarCollapsed] = useState(Boolean(desktopState().sidebarCollapsed));
  const sidebarListRef = useRef<HTMLDivElement | null>(null);
  const sidebarRects = useRef(new Map<string, { x: number; y: number }>());
  const sidebarAnimations = useRef(new WeakMap<HTMLElement, Animation>());
  const sidebarWasCollapsed = useRef(sidebarCollapsed);
  useLayoutEffect(() => {
    if (isDesktop || !sidebarListRef.current) return;
    const pane = sidebarListRef.current.closest<HTMLElement>(".contact-pane");
    const scroll = pane?.scrollTop ?? 0;
    const next = new Map<string, { x: number; y: number }>();
    const previous = sidebarRects.current;
    const collapseChanged = sidebarWasCollapsed.current !== sidebarCollapsed;
    const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
    sidebarListRef.current.querySelectorAll<HTMLElement>("[data-sidebar-flip]").forEach(row => {
      const id = row.dataset.sidebarFlip;
      if (!id) return;
      sidebarAnimations.current.get(row)?.cancel();
      const rect = row.getBoundingClientRect();
      next.set(id, { x:rect.left, y:rect.top + scroll });
      if (collapseChanged || reduced || !previous.size) return;
      const before = previous.get(id);
      if (!before) {
        sidebarAnimations.current.set(row, row.animate([{ opacity:0, transform:"translateX(-18px) scale(.94)" }, { opacity:1, transform:"none" }], { duration:620, easing:"cubic-bezier(.22,1,.36,1)" }));
      } else if (Math.abs(before.y - (rect.top + scroll)) > 1) {
        const delta = before.y - (rect.top + scroll);
        sidebarAnimations.current.set(row, row.animate([{ transform:`translateY(${delta}px)` }, { transform:"translateY(0) scale(1.03)", offset:.7 }, { transform:"none" }], { duration:520, easing:"cubic-bezier(.22,1,.36,1)" }));
      }
    });
    sidebarRects.current = next;
    sidebarWasCollapsed.current = sidebarCollapsed;
  });
  const [bots, setBots] = useState<Bot[]>([]);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [config, setConfig] = useState<Config | null>(null);
  const indexInitialized = useRef(false);
  const { activeId, selectConversation, historyVersion, routeUnavailable } = useConversationNavigation(bots, conversations, indexInitialized.current);
  const [taskFocus, setTaskFocus] = useState<{ conversationId: string; kind: "question" | "draft" | "message" | "run"; id: string } | null>(null);
  const [teamBoardOpen, setTeamBoardOpen] = useState(false);
  useEffect(() => { setTeamBoardOpen(false); }, [activeId]);
  const arrivalIDs = useRef(new Set<string>());
  const messageListRef = useRef<HTMLDivElement | null>(null);
  const [messageRecords, setMessages] = useState<Message[]>([]);
  const [drafts, setDrafts] = useState<Record<string, StreamDraft>>({});
  // Transient per-run signals: the model's latest reasoning headline and a provider back-off.
  const [runSignals, setRunSignals] = useState<Record<string, RunSignal>>({});
  const [avatarMention, setAvatarMention] = useState<{ name: string; conversationId: string; nonce: number } | null>(null);
  const [composerDrafts, setComposerDrafts] = useState<ComposerDrafts>({});
  const [composerDraftReady, setComposerDraftReady] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMessages, setLoadingMessages] = useState(false);
  const [loadedId, setLoadedId] = useState<string | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [historyError, setHistoryError] = useState("");
  const [runs, setRuns] = useState<Run[]>([]);
  const terminalRunIDs = useRef(new Set<string>());
  const [toolActivities, setToolActivities] = useState<ToolActivity[]>([]);
  const [toolDetailActivities, setToolDetailActivities] = useState<ToolActivity[]>([]);
  const [toolSummaries, setToolSummaries] = useState<ToolActivityRunSummary[]>([]);
  const [toolDetailState, setToolDetailState] = useState<Record<string, ToolDetailState>>({});
  const [toolSummaryState, setToolSummaryState] = useState<Record<string, ToolSummaryState>>({});
  const toolDetailRequests = useRef(new Set<string>());
  const [memories, setMemories] = useState<Memory[]>([]);
  const [portabilityFile, setPortabilityFile] = useState<File>();
  const [portabilityBotID, setPortabilityBotID] = useState("");
  const [settingsTab, setSettingsTab] = useState<SettingsTab>("general");
  const [settingsEntry, setSettingsEntry] = useState<SettingsEntry>({ seq: 0, view: "home" });
  /** Opens Settings on a tab. On a narrow screen a deep link lands on the page itself; the plain Settings button lands on the list. */
  const openSettings = (tab: SettingsTab, view: SettingsView = "page") => { setSettingsTab(tab); setSettingsEntry(current => ({ seq: current.seq + 1, view })); setPanel("settings"); };
  const [usageBotId, setUsageBotId] = useState("");
  const [panel, setPanel] = useState<Panel>(null);
  const displayedPanel = useSurfacePresence(panel, 380);
  const [desktopReady, setDesktopReady] = useState(false);
  const desktopPresence = useDesktopPresence(Boolean(activeId));
  // Keep in sync with conversation-workspace.css: three columns need room for chat.
  const [compactViewport, setCompactViewport] = useState(() => typeof window !== "undefined" && window.matchMedia("(max-width: 1099px)").matches);
  // The computer opens as a small floating window; the last size lives only as long as this page.
  const [desktopSize, setDesktopSize] = useState<"small" | "big">("small");
  const [desktopSheet, setDesktopSheet] = useState(() => typeof window !== "undefined" && window.matchMedia("(max-width: 700px)").matches);
  useEffect(() => {
    const media = window.matchMedia("(max-width: 700px)");
    const sync = () => setDesktopSheet(media.matches);
    media.addEventListener("change", sync);
    return () => media.removeEventListener("change", sync);
  }, []);
  const [scheduleRefresh, setScheduleRefresh] = useState(0);
  const [error, setError] = useState("");
  const [snapshotError, setSnapshotError] = useState("");
  const [workspaceError, setWorkspaceError] = useState("");
  const [headerMenuOpen, setHeaderMenuOpen] = useState(false);
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [sidebarLayout, setSidebarLayout] = useState<SidebarLayout>(readSidebarLayout);
  const [hiddenBotsOpen, setHiddenBotsOpen] = useState(false);
  const [sidebarContextMenu, setSidebarContextMenu] = useState<SidebarContextMenu | null>(null);
  const [reactionMenu, setReactionMenu] = useState<{ messageId: string; x: number; y: number } | null>(null);
  const [viewOnlyChat, setViewOnlyChat] = useState<ViewOnlyChatTarget | null>(null);
  const viewOnlyChatTriggerRef = useRef<HTMLElement | null>(null);
  function openRelatedChat(target: ViewOnlyChatTarget) {
    viewOnlyChatTriggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    // On wide browser windows the read-only chat takes the same top-right card as context panels.
    if (!isDesktop && !compactViewport && panel && panel !== "settings" && panel !== "desktop") setPanel(null);
    setViewOnlyChat(target);
  }
  const showCreateMenu = useSurfacePresence(createMenuOpen ? "open" : null);
  const showHeaderMenu = useSurfacePresence(headerMenuOpen ? "open" : null);
  const [newBotBusy, setNewBotBusy] = useState(false);
  const [newBotError, setNewBotError] = useState("");
  const [botImportBusy, setBotImportBusy] = useState(false);
  const [botImportError, setBotImportError] = useState("");
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const knownBotNames = useRef(new Map<string, string>());
  useEffect(() => { for (const bot of bots) knownBotNames.current.set(bot.id, bot.name); }, [bots]);
  const [streamConnected, setStreamConnected] = useState(true);
  const [codexStatusRefresh, setCodexStatusRefresh] = useState(0);
  const [extensionRefresh, setExtensionRefresh] = useState(0);
  const [computerInfo, setComputerInfo] = useState<{ state: string; phase?: string; error?: string; health?: ComputerHealth } | null>(null);
  const headerMenuRef = useRef<HTMLDivElement | null>(null);
  const headerMenuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const createMenuRef = useRef<HTMLDivElement | null>(null);
  const createMenuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const botImportRef = useRef<HTMLInputElement | null>(null);
  const detailPaneRef = useRef<HTMLElement | null>(null);
  const panelTriggerRef = useRef<HTMLElement | null>(null);
  const previousPanelModal = useRef(false);
  const [mobileList, setMobileList] = useState(() => readConversationRoute(window.location.pathname).kind === "home");
  useEffect(() => {
    if (!historyVersion) return;
    setMobileList(false); setPanel(null); setViewOnlyChat(null); setTaskFocus(null);
  }, [historyVersion]);
  const [search, setSearch] = useState("");
  const [activityNow, setActivityNow] = useState(() => Date.now());
  const [lastUserActivity, setLastUserActivity] = useState<Record<string, number>>({});
  const [lastBotWorkActivity, setLastBotWorkActivity] = useState<Record<string, number>>({});
  const workingBotsSeen = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!activeId || loadedId !== activeId) return;
    const time = latestHumanMessageTime(messageRecords, runs);
    if (time) setLastUserActivity(current => time > (current[activeId] ?? 0) ? { ...current, [activeId]: time } : current);
  }, [activeId, loadedId, messageRecords, runs]);
  // The shared computer is a floating surface. It must not reserve the
  // detail-pane column or push the chat inward while it is open.
  const contextPanelOpen = Boolean(panel && panel !== "settings" && panel !== "desktop");
  // One top-right card at a time: opening a context panel closes the read-only chat card.
  useEffect(() => { if (contextPanelOpen && !isDesktop) setViewOnlyChat(null); }, [contextPanelOpen]);
  const modalPanelOpen = Boolean(panel && panel !== "desktop" && (panel === "settings" || compactViewport));
  const readSent = useRef<Record<string, number>>({});
  const readRetryAfter = useRef<Record<string, number>>({});
  const [viewportHeight, setViewportHeight] = useState<number>();
  const [keyboardViewportOpen, setKeyboardViewportOpen] = useState(false);
  useEffect(() => {
    try { localStorage.setItem("tofi:sidebar-layout", JSON.stringify(sidebarLayout)); } catch { /* local persistence is optional */ }
  }, [sidebarLayout]);
  useEffect(() => {
    const timer = window.setInterval(() => setActivityNow(Date.now()), 15_000);
    return () => window.clearInterval(timer);
  }, []);
  useEffect(() => {
    if (!streamConnected || loading) return;
    const current = new Set(conversations.flatMap(conversation => conversation.working_bot_ids ?? []));
    for (const run of activeBotRuns(runs, Object.values(drafts), toolActivities)) current.add(run.bot_id);
    const finished = [...workingBotsSeen.current].filter(id => !current.has(id));
    workingBotsSeen.current = current;
    if (finished.length) {
      const now = Date.now();
      setLastBotWorkActivity(previous => Object.fromEntries([
        ...Object.entries(previous), ...finished.map(id => [id, now]),
      ]));
    }
  }, [conversations, runs, drafts, toolActivities, streamConnected, loading]);
  useEffect(() => {
    if (!sidebarContextMenu) return;
    const close = () => setSidebarContextMenu(null);
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    window.addEventListener("click", close);
    window.addEventListener("keydown", escape);
    return () => { window.removeEventListener("click", close); window.removeEventListener("keydown", escape); };
  }, [sidebarContextMenu]);
  const notificationSeen = useRef<Record<string, number>>({});
  const eventCursor = useRef(0);
  const eventSource = useRef<EventSource | null>(null);
  const reconnectTimer = useRef<number | undefined>(undefined);
  const eventConnectTimer = useRef<number | undefined>(undefined);
  const reconnectAttempt = useRef(0);
  const activeIdRef = useRef<string | null>(null);
  const snapshotEventCursor = useRef(0);
  const snapshotMaxSeq = useRef(0);
  const snapshotCursorKnown = useRef(false);
  const newBotBusyRef = useRef(false);
  const newBotCreationId = useRef<string | null>(null);
  const newBotPending = useRef(false);
  const snapshotGeneration = useRef(0);
  const workspaceRefreshGeneration = useRef(0);
  const configRefreshGeneration = useRef(0);
  const workspaceEventCursor = useRef(0);
  const workspaceEventSource = useRef<EventSource | null>(null);
  const workspaceReconnectTimer = useRef<number | undefined>(undefined);
  const workspaceConnectTimer = useRef<number | undefined>(undefined);
  const workspaceReconnectAttempt = useRef(0);
  const configRef = useRef<Config | null>(null);
  const composerStorageKey = useRef<string | null>(null);

  const updateComposerDraft = useCallback((conversationId: string, update: (draft: ComposerDraft) => ComposerDraft) => {
    setComposerDrafts((current) => {
      const next = { ...current, [conversationId]: update(current[conversationId] ?? emptyComposerDraft()) };
      if (composerStorageKey.current) writeComposerDrafts(composerStorageKey.current, next);
      return next;
    });
  }, []);

  useEffect(() => {
    let alive = true;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 6_000);
    api.serverInfo(controller.signal).then((info) => {
      if (!alive || !info.instance_id) return;
      const key = composerDraftStorageKey(info.instance_id);
      composerStorageKey.current = key;
      const stored = readComposerDrafts(key);
      setComposerDrafts((current) => {
        const merged = { ...stored, ...current };
        writeComposerDrafts(key, merged);
        return merged;
      });
    }).catch(() => {
      // Chat remains usable with the in-memory draft store when discovery is unavailable.
    }).finally(() => { if (alive) { window.clearTimeout(timeout); setComposerDraftReady(true); } });
    return () => { alive = false; window.clearTimeout(timeout); controller.abort(); };
  }, []);

  useEffect(() => { updateDesktopState({ ...(activeId ? { activeConversation: activeId } : {}), sidebarCollapsed }); }, [activeId, sidebarCollapsed]);
  useEffect(() => window.tofiDesktop?.onCommand((command: DesktopCommand) => {
    // Native menu shortcuts must not steal keys while a remote machine owns input.
    const focus = document.activeElement;
    if (focus instanceof Element && focus.closest(".terminal-panel,.remote-desktop-surface,.remote-desktop.in-control,.xterm")) return;
    if (document.querySelector('[role="dialog"][aria-modal="true"]') && command !== "settings") return;
    if (command === "settings") { setSettingsEntry(current => ({ seq: current.seq + 1, view: "home" })); setPanel(current => current === "settings" ? null : "settings"); }
    else if (command === "toggle-sidebar") setSidebarCollapsed(value => !value);
    else if (command === "new-conversation") { setCreateMenuOpen(true); setSidebarCollapsed(false); setPanel(null); }
    else if (command === "search") {
      setSidebarCollapsed(false); setPanel(null);
      requestAnimationFrame(() => document.querySelector<HTMLInputElement>('.search-box input')?.focus());
    } else if (command === "focus-composer") document.querySelector<HTMLTextAreaElement>('.composer textarea')?.focus();
  }), []);

  const applyConfig = useCallback((nextConfig: Config) => {
    const previous = configRef.current;
    configRef.current = nextConfig;
    setConfig(nextConfig);
    if (previous && (previous.model_configured !== nextConfig.model_configured || previous.default_model !== nextConfig.default_model || previous.provider !== nextConfig.provider)) {
      setCodexStatusRefresh((current) => current + 1);
    }
  }, []);

  const refreshIndex = useCallback(async () => {
    const generation = snapshotGeneration.current;
    const requestGeneration = ++workspaceRefreshGeneration.current;
    const configRequestGeneration = ++configRefreshGeneration.current;
    setLoading(true);
    try {
      const [botResult, conversationResult, nextConfig] = await Promise.all([api.bots(true), api.conversations(true), api.config()]);
      if (requestGeneration !== workspaceRefreshGeneration.current || generation !== snapshotGeneration.current || newBotPending.current) return;
      setBots(botResult.bots);
      setConversations(conversationResult.conversations);
      for (const c of conversationResult.conversations) notificationSeen.current[c.id] = c.last_message?.seq ?? 0;
      indexInitialized.current = true;
      setWorkspaceError("");
      // A config-only refresh may have superseded this combined request while
      // its list data was still in flight. Keep the fresh list and let that
      // independent config request own model state.
      if (configRequestGeneration === configRefreshGeneration.current) applyConfig(nextConfig);
      setError("");
    } catch (cause) {
      setWorkspaceError(errorText(cause));
      setError(errorText(cause));
    } finally {
      if (requestGeneration === workspaceRefreshGeneration.current) setLoading(false);
    }
  }, [applyConfig]);

  const refreshConversations = useCallback(async () => {
    const generation = snapshotGeneration.current;
    const requestGeneration = ++workspaceRefreshGeneration.current;
    try {
      const [result, botResult] = await Promise.all([api.conversations(true), api.bots(true)]);
      if (requestGeneration !== workspaceRefreshGeneration.current || generation !== snapshotGeneration.current || newBotPending.current) return;
      setBots(botResult.bots);
      setWorkspaceError("");
      for (const c of result.conversations) {
        const message = c.last_message;
        const previous = notificationSeen.current[c.id] ?? 0;
        notificationSeen.current[c.id] = message?.seq ?? previous;
        if (indexInitialized.current && message && message.seq > previous && message.role === "assistant" && !message.internal &&
            (document.visibilityState !== "visible" || c.id !== activeIdRef.current) &&
            "Notification" in window && Notification.permission === "granted") {
          const notice = new Notification(c.name, {body: previewText(message.content), tag: `tofi-${c.id}`});
          notice.onclick = () => { window.focus(); selectConversation(c.id); setMobileList(false); notice.close(); };
        }
      }
      setConversations((current) => {
        const localById = new Map(current.map((conversation) => [conversation.id, conversation]));
        const merged = result.conversations.map((conversation) => {
          const local = localById.get(conversation.id);
          conversation.read_seq = Math.max(conversation.read_seq ?? 0, local?.read_seq ?? 0);
          if ((conversation.last_message?.seq ?? 0) <= conversation.read_seq) conversation.unread_count = 0;
          if (local?.last_message && (!conversation.last_message || local.last_message.seq > conversation.last_message.seq)) return { ...conversation, last_message: local.last_message, updated_at: local.updated_at, last_user_message_at: local.last_user_message_at ?? conversation.last_user_message_at };
          return conversation;
        });
        return merged;
      });
    } catch (cause) {
      // Conversation previews are best-effort while the app is in use.
      if (indexInitialized.current) setWorkspaceError(errorText(cause));
    } finally {
      // A lightweight refresh can supersede a full refresh (for example when
      // Codex settings close while the polling tick fires). Whichever current
      // workspace request wins must release the shared loading indicator.
      if (requestGeneration === workspaceRefreshGeneration.current) setLoading(false);
    }
  }, [selectConversation]);

  const refreshConfig = useCallback(async () => {
    const generation = snapshotGeneration.current;
    const requestGeneration = ++configRefreshGeneration.current;
    try {
      const nextConfig = await api.config();
      if (requestGeneration !== configRefreshGeneration.current || generation !== snapshotGeneration.current || newBotPending.current) return;
      applyConfig(nextConfig);
      setWorkspaceError("");
      setError("");
    } catch (cause) {
      if (indexInitialized.current) setWorkspaceError(errorText(cause));
    }
  }, [applyConfig]);

  // First-run onboarding. The state lives on the server, so it resumes on any device.
  const [onboarding, setOnboarding] = useState<OnboardingState | null>(null);
  const [onboardingForced, setOnboardingForced] = useState<OnboardingStep | null>(null);
  useEffect(() => {
    let alive = true;
    api.onboardingState().then(value => { if (alive) setOnboarding(parseOnboardingState(value)); }).catch(() => { /* no state, no onboarding */ });
    return () => { alive = false; };
  }, []);
  const saveOnboarding = useCallback((update: { step?: number; skipped?: boolean; completed?: boolean }) => {
    setOnboarding(current => current ? { step: Math.max(current.step, update.step ?? 0), completed: update.completed ?? current.completed, skipped: update.skipped ?? current.skipped } : current);
    void api.putOnboarding(update).then(value => { const next = parseOnboardingState(value); if (next) setOnboarding(next); }).catch(() => { /* the next change writes it again */ });
  }, []);
  const modelReady = config?.model_configured ?? false;
  const onboardingState = loading || !config ? "none" : onboardingMode(onboarding, { modelConfigured: modelReady, botCount: bots.length, forcedOpen: onboardingForced !== null });
  /** The composer banner's Connect: reopen setup at the model step, or Settings once setup is behind the account. */
  const connectModel = () => { if (onboarding && !onboarding.completed) setOnboardingForced(2); else openSettings("models"); };

  const handleWorkspaceEvent = useCallback((data: WorkspaceEventEnvelope, id: number) => {
    // If the server restored a database whose event log has a lower
    // high-water mark, it replays from zero. A lower revision is therefore a
    // reset signal; accept that replay instead of discarding it as a normal
    // duplicate. Equal revisions remain deduplicated.
    if (id > 0 && id < workspaceEventCursor.current) workspaceEventCursor.current = 0;
    else if (id > 0 && id === workspaceEventCursor.current) return;
    if (id > workspaceEventCursor.current) workspaceEventCursor.current = id;
    if (!indexInitialized.current) {
      void refreshIndex();
      return;
    }
    if (data.scope === "bots" || data.scope === "groups") { setScheduleRefresh(current => current + 1); void refreshConversations(); }
    else if (data.scope === "config") { setExtensionRefresh((current) => current + 1); void refreshConfig(); }
  }, [refreshConfig, refreshConversations, refreshIndex]);

  const refreshWorkspaceOnConnect = useCallback(() => {
    if (!indexInitialized.current) void refreshIndex();
    else { void refreshConversations(); void refreshConfig(); }
  }, [refreshConfig, refreshConversations, refreshIndex]);

  const resetWorkspaceEvents = useCallback(() => {
    workspaceEventCursor.current = 0;
    setExtensionRefresh((current) => current + 1);
    refreshWorkspaceOnConnect();
  }, [refreshWorkspaceOnConnect]);

  const connectWorkspaceEvents = useCallback(() => {
    // A selected conversation carries workspace invalidations on its own SSE.
    // Keep the dedicated stream only while there is no active conversation.
    if (activeIdRef.current) return;
    workspaceEventSource.current?.close();
    if (workspaceReconnectTimer.current !== undefined) window.clearTimeout(workspaceReconnectTimer.current);
    if (workspaceConnectTimer.current !== undefined) window.clearTimeout(workspaceConnectTimer.current);
    let source: EventSource;
    const current = () => workspaceEventSource.current === source && !activeIdRef.current;
    const failed = () => {
      source.close();
      if (!current()) return;
      if (workspaceConnectTimer.current !== undefined) window.clearTimeout(workspaceConnectTimer.current);
      workspaceEventSource.current = null;
      const delay = Math.min(1000 * 2 ** workspaceReconnectAttempt.current, 10_000);
      workspaceReconnectAttempt.current += 1;
      const timer = window.setTimeout(() => {
        if (workspaceReconnectTimer.current !== timer || workspaceEventSource.current) return;
        workspaceReconnectTimer.current = undefined;
        connectWorkspaceEvents();
      }, delay);
      workspaceReconnectTimer.current = timer;
    };
    source = openWorkspaceEvents(workspaceEventCursor.current, (data, id) => {
      if (current()) handleWorkspaceEvent(data, id);
    }, failed, () => { if (current()) resetWorkspaceEvents(); });
    for (const event of ["question", "question_answered", "question_cancelled", "question_updated"]) source.addEventListener(event, () => { if (current()) void questions.refresh(); });
    source.onopen = () => {
      if (!current()) return;
      if (workspaceConnectTimer.current !== undefined) window.clearTimeout(workspaceConnectTimer.current);
      workspaceReconnectAttempt.current = 0;
      // A reconnect can follow a restored database whose event log no longer
      // contains the old cursor. Refresh immediately, including the empty
      // snapshot case, instead of waiting for polling to repair the sidebar.
      refreshWorkspaceOnConnect();
      void questions.refresh();
    };
    workspaceEventSource.current = source;
    workspaceConnectTimer.current = window.setTimeout(failed, 10_000);
  }, [handleWorkspaceEvent, refreshWorkspaceOnConnect, resetWorkspaceEvents]);

  useEffect(() => { void refreshIndex(); }, [refreshIndex]);
  useEffect(() => { const refresh = () => { void refreshIndex(); }; window.addEventListener("tofi:portability-imported", refresh); return () => window.removeEventListener("tofi:portability-imported", refresh); }, [refreshIndex]);
  useEffect(() => {
    if (!activeId) connectWorkspaceEvents();
    return () => {
      workspaceEventSource.current?.close();
      workspaceEventSource.current = null;
      if (workspaceReconnectTimer.current !== undefined) window.clearTimeout(workspaceReconnectTimer.current);
      workspaceReconnectTimer.current = undefined;
      if (workspaceConnectTimer.current !== undefined) window.clearTimeout(workspaceConnectTimer.current);
    };
  }, [activeId, connectWorkspaceEvents]);
  useEffect(() => {
    const visual = window.visualViewport;
    if (!visual) return;
    let wasKeyboardOpen = false;
    const sync = () => {
      // On iPadOS the page can be panned upward while the keyboard is open.
      // `height` is measured from the visual viewport's current top, so using
      // it alone makes the flex column end above the keyboard by `offsetTop`.
      // Use the visible bottom edge only for a genuine keyboard resize. When
      // the keyboard is closed, let CSS `100dvh` follow browser chrome and
      // orientation changes without a second JS resize model.
      const result = visualViewportMetrics({ layoutHeight: window.innerHeight, visualHeight: visual.height, offsetTop: visual.offsetTop, scale: visual.scale, wasKeyboardOpen });
      if (result.ignored) return;
      wasKeyboardOpen = result.keyboardOpen;
      setViewportHeight((current) => current === result.height ? current : result.height);
      setKeyboardViewportOpen((current) => current === result.keyboardOpen ? current : result.keyboardOpen);
    };
    sync();
    return observeVisualViewport(visual, window, sync);
  }, []);
  useEffect(() => {
    const media = window.matchMedia("(max-width: 1099px)");
    const sync = () => setCompactViewport(media.matches);
    sync();
    media.addEventListener?.("change", sync);
    return () => media.removeEventListener?.("change", sync);
  }, []);
  useEffect(() => {
    let frame: number | undefined;
    if (modalPanelOpen && !previousPanelModal.current) {
      const focused = document.activeElement;
      const fallback = document.querySelector<HTMLElement>(panel === "settings" ? `[aria-label="${t("common:nav.settings")}"]` : `[aria-label="${t("header.more")}"]`);
      panelTriggerRef.current = focused instanceof HTMLElement && focused !== document.body && focused.isConnected && !detailPaneRef.current?.contains(focused) ? focused : fallback;
      frame = window.requestAnimationFrame(() => {
        const pane = detailPaneRef.current;
        if (!pane) return;
        // Desktop acquisition may already have focused its keyboard receiver.
        if (document.activeElement !== pane && pane.contains(document.activeElement)) return;
        const first = pane.querySelector<HTMLElement>("button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex=\"-1\"])");
        (first ?? pane).focus();
      });
    } else if (!modalPanelOpen && previousPanelModal.current) {
      const trigger = panelTriggerRef.current;
      panelTriggerRef.current = null;
      frame = window.requestAnimationFrame(() => {
        if (trigger?.isConnected) trigger.focus();
        else document.querySelector<HTMLElement>(`[aria-label="${t("header.more")}"], [aria-label="${t("common:nav.settings")}"]`)?.focus();
      });
    }
    previousPanelModal.current = modalPanelOpen;
    return () => {
      if (frame !== undefined) window.cancelAnimationFrame(frame);
    };
  }, [modalPanelOpen]);
  useEffect(() => {
    if (!modalPanelOpen) return;
    const trap = (event: KeyboardEvent) => {
      if (document.querySelector(".mcp-oauth-overlay, .delete-dialog-overlay")) return;
      if (event.key === "Escape" && !event.defaultPrevented) {
        if (detailPaneRef.current?.querySelector('.settings-tabs')) {
          if (document.querySelector('.settings-leave-dialog[open]')) return;
          event.preventDefault();
          window.dispatchEvent(new Event('tofi-settings-close'));
          return;
        }
        if (document.querySelector(".computer-detail.is-expanded") || detailPaneRef.current?.querySelector('[aria-busy="true"]')) return;
        event.preventDefault();
        setPanel(null);
        return;
      }
      if (event.key !== "Tab") return;
      const pane = detailPaneRef.current;
      if (!pane) return;
      const focusable = Array.from(pane.querySelectorAll<HTMLElement>("button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), a[href], summary, [tabindex]:not([tabindex=\"-1\"])")).filter(element => {
        if (element.matches(":disabled") || element.hidden || element.getAttribute("aria-hidden") === "true") return false;
        const style = window.getComputedStyle(element);
        return style.display !== "none" && style.visibility !== "hidden" && element.getClientRects().length > 0;
      });
      if (!focusable.length) {
        event.preventDefault();
        pane.focus();
        return;
      }
      const active = document.activeElement;
      const current = focusable.indexOf(active as HTMLElement);
      if (current < 0) {
        event.preventDefault();
        (event.shiftKey ? focusable.at(-1) : focusable[0])?.focus();
      } else if (event.shiftKey && current === 0) {
        event.preventDefault();
        focusable.at(-1)?.focus();
      } else if (!event.shiftKey && current === focusable.length - 1) {
        event.preventDefault();
        focusable[0]?.focus();
      }
    };
    document.addEventListener("keydown", trap);
    return () => document.removeEventListener("keydown", trap);
  }, [modalPanelOpen]);
  useEffect(() => {
    if (!headerMenuOpen) return;
    let frame: number | undefined;
    frame = window.requestAnimationFrame(() => {
      const first = headerMenuRef.current?.querySelector<HTMLElement>('.more-menu button:not([disabled])');
      if (first?.isConnected) first.focus();
    });
    const close = (event: MouseEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent && event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault();
        setHeaderMenuOpen(false);
        headerMenuTriggerRef.current?.focus();
      } else if (event instanceof MouseEvent && !headerMenuRef.current?.contains(event.target as Node) && !headerMenuTriggerRef.current?.contains(event.target as Node)) {
        // Let the outside target receive focus naturally; don't pull it back to
        // the trigger when a user clicks another control.
        setHeaderMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", close); document.addEventListener("keydown", close);
    return () => { if (frame !== undefined) window.cancelAnimationFrame(frame); document.removeEventListener("mousedown", close); document.removeEventListener("keydown", close); };
  }, [headerMenuOpen]);
  useEffect(() => {
    if (!createMenuOpen) return;
    let frame: number | undefined;
    frame = window.requestAnimationFrame(() => {
      const first = createMenuRef.current?.querySelector<HTMLElement>('.create-menu button:not([disabled])');
      if (first?.isConnected) first.focus();
    });
    const close = (event: MouseEvent | KeyboardEvent) => {
      if (event instanceof KeyboardEvent && event.key === "Escape" && !event.defaultPrevented) {
        event.preventDefault();
        setCreateMenuOpen(false);
        createMenuTriggerRef.current?.focus();
      } else if (event instanceof MouseEvent && !createMenuRef.current?.contains(event.target as Node) && !createMenuTriggerRef.current?.contains(event.target as Node)) {
        setCreateMenuOpen(false);
      }
    };
    document.addEventListener("mousedown", close); document.addEventListener("keydown", close);
    return () => { if (frame !== undefined) window.cancelAnimationFrame(frame); document.removeEventListener("mousedown", close); document.removeEventListener("keydown", close); };
  }, [createMenuOpen]);
  useEffect(() => {
    if (!panel || modalPanelOpen) return;
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented && panel !== "settings" && !document.querySelector(".computer-detail.is-expanded, .delete-dialog-overlay") && !detailPaneRef.current?.querySelector('[aria-busy="true"]')) {
        event.preventDefault();
        setPanel(null);
      }
    };
    document.addEventListener("keydown", onEscape);
    return () => document.removeEventListener("keydown", onEscape);
  }, [panel, modalPanelOpen]);
  useEffect(() => { setHeaderMenuOpen(false); }, [activeId]);
  useEffect(() => {
    let alive = true;
    const refreshComputer = async () => { try { const next = await api.computerInfo(); if (alive) setComputerInfo(next); } catch { if (alive) setComputerInfo(null); } };
    void refreshComputer();
    const timer = window.setInterval(() => void refreshComputer(), 4_000);
    return () => { alive = false; window.clearInterval(timer); };
  }, []);
  useEffect(() => {
    const refresh = () => {
      if (!indexInitialized.current) void refreshIndex();
      else { void refreshConversations(); void refreshConfig(); }
    };
    window.addEventListener("focus", refresh);
    const interval = window.setInterval(refresh, 5_000);
    return () => { window.removeEventListener("focus", refresh); window.clearInterval(interval); };
  }, [refreshConfig, refreshConversations, refreshIndex]);

  const active = conversations.find((item) => item.id === activeId);
  activeIdRef.current = activeId;
  const botById = useMemo(() => new Map(bots.map((bot) => [bot.id, bot])), [bots]);
  const botByConversationId = useMemo(() => new Map(bots.map((bot) => [bot.dm_conversation_id, bot])), [bots]);
  const activeBot = active?.bot_id ? botById.get(active.bot_id) : undefined;
  function transitionBotPanel(open: boolean) {
    const update = () => setPanel(open ? "bot-edit" : null);
    const start = (document as Document & { startViewTransition?: (callback: () => void) => unknown }).startViewTransition;
    if (isDesktop || !activeBot || !start || matchMedia("(prefers-reduced-motion: reduce)").matches) { update(); return; }
    start.call(document, () => flushSync(update));
  }
  const desktopBot = activeBot ?? (active?.kind === "group" ? botById.get(active.bot_ids[0]) : undefined);
  const desktopOpen = Boolean(desktopBot && panel === "desktop");
  // Narrow screens skip the small window: the big view is a full-screen sheet.
  const desktopExpanded = desktopOpen && (desktopSheet || desktopSize === "big");
  const desktopSmall = desktopOpen && !desktopExpanded;
  useEffect(() => {
    if (panel === "desktop" && !desktopBot) setPanel(null);
  }, [panel, desktopBot]);
  const currentDesktopOwner = desktopPresence.ownership?.owner;
  const activeDesktopOwner = !desktopPresence.unavailable && currentDesktopOwner?.kind === "bot" && currentDesktopOwner.conversation_id === active?.id ? currentDesktopOwner : null;
  const query = search.trim().toLowerCase();
  const sortedConversations = useMemo(() => [...conversations].sort((a, b) => +new Date(b.last_message?.created_at ?? b.updated_at) - +new Date(a.last_message?.created_at ?? a.updated_at)), [conversations]);
  const filteredConversations = sortedConversations.filter((conversation) => {
    if (conversation.archived) return false;
    if (!query) return true;
    const names = conversation.kind === "dm" ? [conversation.name, botByConversationId.get(conversation.id)?.instructions ?? ""] : [conversation.name, ...conversation.bot_ids.map((id) => botById.get(id)?.name ?? "")];
    return names.some((name) => name.toLowerCase().includes(query));
  });
  const hiddenBots = bots.filter((bot) => bot.archived && (!query || bot.name.toLowerCase().includes(query)));
  const visibleSidebarConversations = filteredConversations.filter((conversation) => !conversation.archived);
  const pinnedConversations = visibleSidebarConversations.filter((conversation) => sidebarLayout.pinned.includes(conversation.id));
  const customSidebarSections = Object.entries(sidebarLayout.sections).reduce<Record<string, Conversation[]>>((groups, [id, name]) => {
    const conversation = visibleSidebarConversations.find((item) => item.id === id && !sidebarLayout.pinned.includes(id));
    if (conversation && name.trim()) (groups[name] ??= []).push(conversation);
    return groups;
  }, {});
  const sectionedConversationIds = new Set(Object.values(customSidebarSections).flat().map((conversation) => conversation.id));
  const defaultSidebarConversations = visibleSidebarConversations.filter((conversation) => !sidebarLayout.pinned.includes(conversation.id) && !sectionedConversationIds.has(conversation.id));
  const runById = useMemo(() => new Map(runs.map((run) => [run.id, run])), [runs]);
  const activeRunIds = useMemo(() => new Set(runs.filter(run => run.status === "queued" || run.status === "running").map(run => run.id)), [runs]);
  const retryFamilies = useMemo(() => buildRetryFamilies(runs), [runs]);
  const runStarts = useMemo(() => new Map(runs.map(run => [run.id, Date.parse(run.created_at)] as const).filter(([, start]) => Number.isFinite(start))), [runs]);
  const retryAuditAnchors = useMemo(() => new Map(retryFamilies.map(family => [family.rootId, retryFamilyAnchor(family, messageRecords)])), [retryFamilies, messageRecords]);
  const supersededRunIDs = useMemo(() => new Set(retryFamilies.filter(family => retryAuditAnchors.get(family.rootId)).flatMap(family => family.previous.map(run => run.id))), [retryFamilies, retryAuditAnchors]);
  const visibleMessages = useMemo(() => mergeMessageTimeline(messageRecords.filter(message => message.role === "user" || !message.run_id || !supersededRunIDs.has(message.run_id)), Object.values(drafts).filter(draft => !runById.has(draft.run_id) || !isTerminalRun(runById.get(draft.run_id)!))), [messageRecords, drafts, supersededRunIDs, runById]);
  const foldedProgress = useMemo(() => foldCompletedProgress(visibleMessages, runs), [visibleMessages, runs]);
  const messages = foldedProgress.visible;
  const scheduledRootKey = useMemo(() => occurrenceRootKey(messageRecords.filter(message => message.kind === "scheduled_task" && message.conversation_id === activeId).map(message => message.run_id ?? "")), [messageRecords, activeId]);
  const scheduleOccurrences = useScheduleOccurrences(activeId, scheduledRootKey, scheduleRefresh);
  const scheduleMetadata = useScheduleMetadata(isDesktop ? null : activeId, scheduledRootKey, scheduleRefresh);
  const questions = useQuestions(activeId);
  const mailDrafts = useMailDrafts(activeId);
  const taskOwners = useMemo(() => buildTaskOwners(runs, messageRecords, questions.items, mailDrafts.items), [runs, messageRecords, questions.items, mailDrafts.items]);
  const ownedRunIDs = useMemo(() => new Set(taskOwners.flatMap(owner => owner.family.attempts.map(run => run.id))), [taskOwners]);
  const conversationItems = useMemo(() => buildQuestionTimeline(messages.filter(message => message.kind !== "progress" || !ownedRunIDs.has(message.run_id ?? "")), questions.items, hasMore, mailDrafts.items), [messages, questions.items, hasMore, mailDrafts.items, ownedRunIDs]);
  const messageById = useMemo(() => new Map(messages.map(message => [message.id, message])), [messages]);
  const terminalSummaryRunIDs = useMemo(() => [...new Set([...terminalToolRunIDs(messages, runs), ...supersededRunIDs])].sort(), [messages, runs, supersededRunIDs]);
  const terminalSummaryRunKey = terminalSummaryRunIDs.join("\u0000");
  const terminalSummaryRunIDSet = useMemo(() => new Set(terminalSummaryRunIDs), [terminalSummaryRunIDs]);
  const terminalToolSummaries = useMemo(() => toolSummaries.filter(summary => terminalSummaryRunIDSet.has(summary.run_id)), [toolSummaries, terminalSummaryRunIDSet]);
  const loadedToolActivities = useMemo(() => {
    const byKey = new Map<string, ToolActivity>();
    for (const activity of [...toolDetailActivities, ...toolActivities]) {
      const old = byKey.get(toolActivityKey(activity));
      byKey.set(toolActivityKey(activity), reconcileToolActivity(old, activity));
    }
    return [...byKey.values()].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
  }, [toolActivities, toolDetailActivities]);
  const summaryRunIDs = useMemo(() => new Set(terminalToolSummaries.map(summary => summary.run_id)), [terminalToolSummaries]);
  const summaryLoadingRunIDs = useMemo(() => new Set(Object.entries(toolSummaryState).filter(([, state]) => state.loading).map(([runID]) => runID)), [toolSummaryState]);
  const summaryErrorRunIDs = useMemo(() => new Set(Object.entries(toolSummaryState).filter(([, state]) => Boolean(state.error)).map(([runID]) => runID)), [toolSummaryState]);
  const aggregateToolRunIDs = useMemo(() => new Set([...summaryRunIDs, ...summaryLoadingRunIDs, ...summaryErrorRunIDs]), [summaryRunIDs, summaryLoadingRunIDs, summaryErrorRunIDs]);
  const timelineToolActivities = useMemo(() => loadedToolActivities.filter(activity => !aggregateToolRunIDs.has(activity.run_id) && !supersededRunIDs.has(activity.run_id) && !ownedRunIDs.has(activity.run_id)), [loadedToolActivities, aggregateToolRunIDs, supersededRunIDs, ownedRunIDs]);
  const toolTimeline = useMemo(() => {
    const visibleIds = new Set(messages.map(message => message.id));
    return buildToolTimeline(messageRecords.filter(message => visibleIds.has(message.id)), timelineToolActivities, runs);
  }, [messageRecords, messages, timelineToolActivities, runs]);
  const pendingRuns = useMemo(() => {
    const live = runs.filter(run => run.conversation_id === activeId && (run.status === "running" || run.status === "queued") && run.kind !== "schedule");
    const running = live.some(run => run.status === "running");
    const busy = new Set([
      ...loadedToolActivities.map(activity => activity.run_id),
      ...messageRecords.filter(message => message.kind === "progress" && message.run_id).map(message => message.run_id!),
      ...Object.values(drafts).filter(draft => draft.content.trim()).map(draft => draft.run_id),
    ]);
    return live.filter(run => (run.status === "running" || !running) && !busy.has(run.id)).sort((a, b) => a.created_at.localeCompare(b.created_at));
  }, [runs, activeId, loadedToolActivities, messageRecords, drafts]);
  const toolSummaryAnchors = useMemo(() => buildToolSummaryAnchors(messages, terminalToolSummaries.filter(summary => !supersededRunIDs.has(summary.run_id) && !ownedRunIDs.has(summary.run_id)), toolDetailActivities, runs), [messages, terminalToolSummaries, toolDetailActivities, runs, supersededRunIDs, ownedRunIDs]);
  const toolSummaryErrorAnchors = useMemo(() => buildToolRunAnchors(messages, terminalSummaryRunIDs.filter(runID => !supersededRunIDs.has(runID) && !ownedRunIDs.has(runID) && toolSummaryState[runID]?.error).map(run_id => ({ run_id })), runs), [messages, terminalSummaryRunIDs, toolSummaryState, runs, supersededRunIDs, ownedRunIDs]);
  const toolSummaryLoadingAnchors = useMemo(() => {
    const known = new Set(terminalToolSummaries.map(summary => summary.run_id));
    const evidence = new Set([
      ...loadedToolActivities.map(activity => activity.run_id),
      ...messageRecords.filter(message => message.kind === "progress" && message.run_id).map(message => message.run_id!),
    ]);
    return buildToolRunAnchors(messages, terminalSummaryRunIDs.filter(runID => !supersededRunIDs.has(runID) && !ownedRunIDs.has(runID) && toolSummaryState[runID]?.loading && !known.has(runID) && evidence.has(runID)).map(run_id => ({ run_id })), runs);
  }, [messages, messageRecords, loadedToolActivities, terminalSummaryRunIDs, toolSummaryState, terminalToolSummaries, runs, supersededRunIDs, ownedRunIDs]);

  useEffect(() => {
    if (!activeId || !terminalSummaryRunIDs.length) return;
    let cancelled = false;
    setToolSummaryState(current => {
      const next = { ...current };
      for (const runID of terminalSummaryRunIDs) next[runID] = { loading: true };
      return next;
    });
    void (async () => {
      for (let offset = 0; offset < terminalSummaryRunIDs.length; offset += 50) {
        const runIDs = terminalSummaryRunIDs.slice(offset, offset + 50);
        try {
          const page = await api.toolActivitySummaries(activeId, runIDs);
          if (cancelled || activeIdRef.current !== activeId) return;
          setToolSummaries(current => {
            const byRunID = new Map(current.map(summary => [summary.run_id, summary]));
            for (const summary of page.summaries) byRunID.set(summary.run_id, summary);
            return [...byRunID.values()];
          });
          setToolSummaryState(current => {
            const next = { ...current };
            for (const runID of runIDs) next[runID] = { loading: false };
            return next;
          });
        } catch (cause) {
          if (cancelled || activeIdRef.current !== activeId) return;
          const error = errorText(cause);
          setToolSummaryState(current => {
            const next = { ...current };
            for (const runID of runIDs) next[runID] = { loading: false, error };
            return next;
          });
        }
      }
    })();
    return () => { cancelled = true; };
  }, [activeId, terminalSummaryRunKey]);

  const retryToolSummary = useCallback((runId: string) => {
    const conversationId = activeIdRef.current;
    if (!conversationId) return;
    setToolSummaryState(current => ({ ...current, [runId]: { loading: true } }));
    void api.toolActivitySummaries(conversationId, [runId]).then(page => {
      if (activeIdRef.current !== conversationId) return;
      setToolSummaries(current => {
        const byRunID = new Map(current.map(summary => [summary.run_id, summary]));
        for (const summary of page.summaries) byRunID.set(summary.run_id, summary);
        return [...byRunID.values()];
      });
      setToolSummaryState(current => ({ ...current, [runId]: { loading: false } }));
    }).catch(cause => {
      if (activeIdRef.current !== conversationId) return;
      setToolSummaryState(current => ({ ...current, [runId]: { loading: false, error: errorText(cause) } }));
    });
  }, []);

  const markRead = useCallback((conversationId: string, seq: number) => {
    if (!seq || (readSent.current[conversationId] ?? 0) >= seq || (readRetryAfter.current[conversationId] ?? 0) > Date.now()) return;
    readSent.current[conversationId] = seq;
    void api.markRead(conversationId, seq).then(({ read_seq }) => setConversations(current => current.map(c =>
      c.id === conversationId ? { ...c, read_seq: Math.max(c.read_seq ?? 0, read_seq), unread_count: (c.last_message?.seq ?? 0) <= read_seq ? 0 : c.unread_count } : c)))
      .catch(() => { if (readSent.current[conversationId] === seq) { delete readSent.current[conversationId]; readRetryAfter.current[conversationId] = Date.now() + 5000; } });
  }, []);
  const viewport = useConversationScroll({ conversation: active, messages: messageRecords,
    ready: loadedId === activeId && !loadingMessages, hasMore, loadingOlder, mobileList,
    loadOlder, markRead });
  const jumpLatest = viewport.jumpLatest;
  useEffect(() => {
    if (!taskFocus) return;
    if (taskFocus.conversationId !== activeId) { setTaskFocus(null); return; }
    if (loadedId !== activeId || loadingMessages) return;
    const selector = taskFocus.kind === "run" ? "[data-run-id]" : taskFocus.kind === "question" ? "[data-question-id]" : taskFocus.kind === "draft" ? "[data-draft-id]" : "[data-message-id]";
    const key = taskFocus.kind === "run" ? "runId" : taskFocus.kind === "question" ? "questionId" : taskFocus.kind === "draft" ? "draftId" : "messageId";
    const target = [...(messageListRef.current?.querySelectorAll<HTMLElement>(selector) ?? [])].find(node => node.dataset[key] === taskFocus.id);
    if (target) {
      jumpToMessage(target);
      if (taskFocus.kind === "run") (target.querySelector<HTMLElement>("h3") ?? target).focus({ preventScroll:true });
      setTaskFocus(null);
    } else if (taskFocus.kind === "message" || taskFocus.kind === "run") {
      if (hasMore && !loadingOlder && !historyError) void loadOlder();
      else if (!hasMore) setTaskFocus(null);
    }
  }, [taskFocus, activeId, loadedId, loadingMessages, conversationItems, hasMore, loadingOlder, historyError]);

  function focusTask(conversation: Conversation) {
    const state = conversation.task_state;
    const kind = state?.run_id ? "run" : state?.question_id ? "question" : state?.draft_id ? "draft" : state?.result_message_id ? "message" : undefined;
    const id = state?.run_id || state?.question_id || state?.draft_id || state?.result_message_id;
    if (kind && id) setTaskFocus({ conversationId: conversation.id, kind, id });
    selectConversation(conversation.id);
    setMobileList(false);
  }
  useEffect(() => {
    return subscribeSettingsDeepLinks(tab => openSettings(tab));
  }, []);

  const updateConversationPreview = useCallback((next: Message) => {
    setConversations((current) => current.map((conversation) => {
      if (conversation.id !== next.conversation_id || (conversation.last_message && next.seq < conversation.last_message.seq)) return conversation;
      return { ...conversation, updated_at: next.created_at, ...(next.role === "user" && !next.run_id ? { last_user_message_at: next.created_at } : {}), last_message: { id: next.id, seq: next.seq, role: next.role, kind: next.kind, sender_bot_id: next.sender_bot_id, content: Array.from(next.content).slice(0, 160).join(""), created_at: next.created_at, internal: next.kind === "message_ref" } };
    }));
  }, []);

  const upsertMessage = useCallback((next: Message) => {
    setMessages((current) => {
      const byId = new Map(current.map((item) => [item.id, item]));
      byId.set(next.id, next);
      return [...byId.values()].sort((a, b) => a.seq - b.seq);
    });
    setDrafts((current) => {
      if (!(next.id in current)) return current;
      const copy = { ...current };
      delete copy[next.id];
      return copy;
    });
  }, []);

  const upsertToolActivity = useCallback((next: ToolActivity) => {
    setToolActivities((current) => {
      const byKey = new Map(current.map((item) => [toolActivityKey(item), item]));
      const old = byKey.get(toolActivityKey(next));
      byKey.set(toolActivityKey(next), reconcileToolActivity(old, next));
      return [...byKey.values()].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)).slice(0, 200);
    });
  }, []);

  const loadToolDetails = useCallback(async (runId: string, offset: number) => {
    const conversationId = activeIdRef.current;
    if (!conversationId || toolDetailRequests.current.has(runId)) return;
    toolDetailRequests.current.add(runId);
    setToolDetailState(current => ({ ...current, [runId]: { ...(current[runId] ?? { loaded: 0, toolCount: 0, hasMore: true }), loading: true, error: undefined } }));
    try {
      const page = await api.toolActivityDetails(conversationId, runId, offset);
      if (activeIdRef.current !== conversationId) return;
      setToolDetailActivities(current => {
        const byKey = new Map(current.map(item => [toolActivityKey(item), item]));
        for (const item of page.activities) {
          const old = byKey.get(toolActivityKey(item));
          byKey.set(toolActivityKey(item), reconcileToolActivity(old, item));
        }
        return [...byKey.values()].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
      });
      setToolDetailState(current => ({ ...current, [runId]: { loaded: Math.max(current[runId]?.loaded ?? 0, offset + page.activities.length), toolCount: page.tool_count, hasMore: page.has_more, loading: false } }));
    } catch (cause) {
      if (activeIdRef.current === conversationId) setToolDetailState(current => ({ ...current, [runId]: { ...(current[runId] ?? { loaded: 0, toolCount: 0, hasMore: true }), loading: false, error: errorText(cause) } }));
    } finally {
      toolDetailRequests.current.delete(runId);
    }
  }, []);

  const handleEvent = useCallback((conversationId: string, type: string, data: EventEnvelope, id: number) => {
    // EventSource callbacks can arrive after a conversation switch. Never let
    // an old stream mutate the newly selected conversation.
    if (conversationId !== activeIdRef.current || data.conversation_id !== conversationId) return;
    if (id > 0 && id <= eventCursor.current) return;
    if (id > eventCursor.current) eventCursor.current = id;
    if (type === "bot") {
      const bot = asBot(data);
      if (bot) {
        snapshotGeneration.current += 1;
        setBots((current) => [bot, ...current.filter((item) => item.id !== bot.id)]);
        setConversations((current) => current.map((conversation) => conversation.bot_id === bot.id ? { ...conversation, name: bot.name } : conversation));
      }
    } else if (type === "message") {
      const message = asMessage(data);
      if (message && (snapshotCursorKnown.current || message.seq > snapshotMaxSeq.current)) {
        if (message.seq > snapshotMaxSeq.current) arrivalIDs.current.add(message.id);
        upsertMessage(message); updateConversationPreview(message);
      }
    } else if (type === "reaction") {
      const messageId = typeof data.message_id === "string" ? data.message_id : "";
      if (messageId && Array.isArray(data.reactions)) setMessages(current => current.map(message => message.id === messageId ? { ...message, reactions: data.reactions as Message["reactions"] } : message));
    } else if (type === "draft_reset") {
      // A draft returned for review is replaced by the final answer, never shown twice.
      const messageIdValue = typeof data.message_id === "string" ? data.message_id : "";
      if (messageIdValue) setDrafts(current => { if (!current[messageIdValue]) return current; const next = { ...current }; delete next[messageIdValue]; return next; });
    } else if (type === "thinking" || type === "retrying") {
      const runId = typeof data.run_id === "string" ? data.run_id : "";
      if (!runId || terminalRunIDs.current.has(runId)) return;
      if (type === "thinking") {
        const headline = thinkingHeadline(typeof data.text === "string" ? data.text : "");
        if (headline) setRunSignals(current => ({ ...current, [runId]: { thinking: headline } }));
      } else {
        const wait = typeof data.wait_ms === "number" ? data.wait_ms : 0;
        setRunSignals(current => ({ ...current, [runId]: { ...current[runId], retryUntil: Date.now() + wait } }));
      }
    } else if (type === "delta") {
      const messageIdValue = typeof data.message_id === "string" ? data.message_id : "";
      const delta = typeof data.text === "string" ? data.text : "";
      if (messageIdValue && delta) arrivalIDs.current.add(messageIdValue);
      const runId = typeof data.run_id === "string" ? data.run_id : "";
      if (terminalRunIDs.current.has(runId)) return;
      const botId = typeof data.bot_id === "string" ? data.bot_id : "";
      const revision = typeof data.revision === "number" ? data.revision : 0;
      const full = typeof data.content === "string" ? data.content : "";
      if (messageIdValue && (delta || full)) setDrafts((current) => {
        const old = current[messageIdValue];
        if (old && revision > 0 && revision <= old.revision) return current;
        return { ...current, [messageIdValue]: { run_id: runId || old?.run_id || "", conversation_id: conversationId, bot_id: botId || old?.bot_id || "", message_id: messageIdValue, seq: typeof data.seq === "number" ? data.seq : old?.seq, content: full || `${old?.content ?? ""}${delta}`, status: "active", revision: revision || (old?.revision ?? 0) + 1, created_at: typeof data.created_at === "string" ? data.created_at : old?.created_at ?? new Date().toISOString(), updated_at: new Date().toISOString() } };
      });
    } else if (type === "run") {
      setScheduleRefresh(current => current + 1);
      const run = asRun(data);
      if (run) {
        if (run.status && isTerminalRun({ status: run.status })) terminalRunIDs.current.add(run.id);
        if (run.status && !["queued", "running"].includes(run.status)) {
          setDrafts((current) => Object.fromEntries(Object.entries(current).filter(([, draft]) => draft.run_id !== run.id)));
          setRunSignals(current => { if (!current[run.id]) return current; const next = { ...current }; delete next[run.id]; return next; });
        }
        setRuns((current) => {
          const existing = current.find((item) => item.id === run.id);
          if (!existing && !run.conversation_id) return current;
          return mergeRunUpdates(current, [run]);
        });
      }
    } else if (type === "memory") {
      if (data.deleted === true && typeof data.id === "string") {
        setMemories((current) => current.filter((item) => item.id !== data.id));
        return;
      }
      const memory = asMemory(data);
      if (memory) setMemories((current) => [memory, ...current.filter((item) => item.id !== memory.id)]);
    } else if (type === "memory_deleted" && typeof data.id === "string") {
      setMemories((current) => current.filter((item) => item.id !== data.id));
    } else if (type === "schedule" || type === "work_item") {
      setScheduleRefresh((current) => current + 1);
    } else if (type === "tool") {
      const activity = asToolActivity(data);
      if (activity && activity.conversation_id === conversationId && !(terminalRunIDs.current.has(activity.run_id) && ["queued", "running"].includes(activity.status))) upsertToolActivity(activity);
    }
  }, [upsertMessage, updateConversationPreview, upsertToolActivity]);

  const connectEvents = useCallback((conversationId: string) => {
    if (activeIdRef.current !== conversationId) return;
    eventSource.current?.close();
    if (reconnectTimer.current !== undefined) window.clearTimeout(reconnectTimer.current);
    if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current);
    let source: EventSource;
    setStreamConnected(false);
    const current = () => eventSource.current === source && activeIdRef.current === conversationId;
    const failed = () => {
      source.close();
      if (!current()) return;
      if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current);
      setStreamConnected(false);
      eventSource.current = null;
      const delay = Math.min(1000 * 2 ** reconnectAttempt.current, 10000);
      reconnectAttempt.current += 1;
      const timer = window.setTimeout(() => {
        if (reconnectTimer.current !== timer || eventSource.current) return;
        reconnectTimer.current = undefined;
        connectEvents(conversationId);
      }, delay);
      reconnectTimer.current = timer;
    };
    source = openConversationEvents(conversationId, eventCursor.current, (type, data, id) => {
      if (current()) handleEvent(conversationId, type, data, id);
    }, failed, {
      after: workspaceEventCursor.current,
      onEvent: (data, id) => { if (current()) handleWorkspaceEvent(data, id); },
      onReset: () => { if (current()) resetWorkspaceEvents(); },
    });
    for (const event of ["question", "question_answered", "question_cancelled", "question_updated"]) source.addEventListener(event, () => { if (current()) void questions.refresh(); });
    source.onopen = () => {
      if (!current()) return;
      void questions.refresh();
      if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current);
      reconnectAttempt.current = 0;
      setStreamConnected(true);
      refreshWorkspaceOnConnect();
    };
    eventSource.current = source;
    eventConnectTimer.current = window.setTimeout(failed, 10_000);
  }, [handleEvent, handleWorkspaceEvent, refreshWorkspaceOnConnect, resetWorkspaceEvents, questions.refresh]);


  useEffect(() => {
    if (!activeId) return;
    let cancelled = false;
    let snapshotRetryTimer: number | undefined;
    let snapshotRetryAttempt = 0;
    setSnapshotError("");
    arrivalIDs.current.clear();
    terminalRunIDs.current.clear();
    setLoadingMessages(true); setLoadedId(null);
    setLoadingOlder(false); setHistoryError("");
    setMessages([]); setDrafts({}); setRuns([]); setToolActivities([]); setToolDetailActivities([]); setToolSummaries([]); setToolDetailState({}); setToolSummaryState({}); setMemories([]); setHasMore(false); setError(""); eventCursor.current = 0; snapshotEventCursor.current = 0; snapshotMaxSeq.current = 0; snapshotCursorKnown.current = false;
    const loadSnapshot = () => {
      if (cancelled) return;
      setLoadingMessages(true);
      const requests = [api.messages(activeId), api.runs(activeId), api.memories(activeId), api.toolActivities(activeId).catch(() => ({ activities: [] as ToolActivity[] }))] as const;
      void Promise.all(requests).then(([page, runPage, memoryPage, toolPage]) => {
        if (cancelled) return;
        setMessages(page.messages.sort((a, b) => a.seq - b.seq));
        for (const run of runPage.runs) if (isTerminalRun(run)) terminalRunIDs.current.add(run.id);
        setDrafts(Object.fromEntries(page.drafts.filter((draft) => draft.status === "active" && !terminalRunIDs.current.has(draft.run_id)).map((draft) => [draft.message_id, draft])));
        setHasMore(page.has_more);
        setRuns(current => mergeRunUpdates(current, runPage.runs.sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at))));
        setToolActivities(current => {
          const byKey = new Map(current.map(item => [toolActivityKey(item), item]));
          for (const item of toolPage.activities) {
            const old = byKey.get(toolActivityKey(item));
            byKey.set(toolActivityKey(item), reconcileToolActivity(old, item));
          }
          return [...byKey.values()].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)).slice(0, 200);
        });
        setMemories(memoryPage.memories);
        snapshotMaxSeq.current = page.messages.reduce((max, message) => Math.max(max, message.seq), 0);
        snapshotCursorKnown.current = page.event_cursor !== undefined;
        snapshotEventCursor.current = page.event_cursor ?? 0;
        eventCursor.current = page.event_cursor ?? 0;
        setLoadedId(activeId);
        setSnapshotError("");
        connectEvents(activeId);
      }).catch(async (cause) => {
        if (cancelled) return;
        setSnapshotError(errorText(cause));
        // Promise.all can reject before sibling reads finish. Do not pile a
        // second attempt onto those in-flight requests on a limited HTTP pool.
        await Promise.allSettled(requests);
        if (cancelled) return;
        // A transient snapshot failure must not strand this conversation
        // without its combined SSE. Retry the snapshot before consuming any
        // chat cursor, with a finite budget and no change to effect identity.
        if (snapshotRetryAttempt < 3) {
          const delay = 1000 * 2 ** snapshotRetryAttempt++;
          snapshotRetryTimer = window.setTimeout(loadSnapshot, delay);
        }
      }).finally(() => { if (!cancelled) setLoadingMessages(false); });
    };
    loadSnapshot();
    return () => { cancelled = true; if (snapshotRetryTimer !== undefined) window.clearTimeout(snapshotRetryTimer); eventSource.current?.close(); eventSource.current = null; if (reconnectTimer.current !== undefined) window.clearTimeout(reconnectTimer.current); reconnectTimer.current = undefined; if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current); };
  }, [activeId, connectEvents]);

  async function loadOlder(): Promise<boolean> {
    if (!activeId || !messageRecords[0] || loadingOlder) return false;
    const conversationId = activeId;
    setLoadingOlder(true);
    try {
      const page = await api.messages(conversationId, messageRecords[0].seq);
      if (activeIdRef.current !== conversationId) return false;
      viewport.preservePrepend();
      setMessages(current => [...page.messages, ...current.filter(item => !page.messages.some(old => old.id === item.id))].sort((a, b) => a.seq - b.seq));
      setHasMore(page.has_more);
      setHistoryError("");
      return true;
    } catch (cause) {
      if (activeIdRef.current === conversationId) setHistoryError(errorText(cause));
      return false;
    } finally {
      if (activeIdRef.current === conversationId) setLoadingOlder(false);
    }
  }

  async function send(content: string, clientMessageId: string, attachmentIds: string[] = [], draftSignature = "", flightOrigin?: DOMRect): Promise<boolean> {
    if (!active || (!content.trim() && !attachmentIds.length)) return false;
    const conversationId = active.id;
    try {
      const result = await api.sendMessage(conversationId, { content: content.trim(), client_message_id: clientMessageId, attachment_ids: attachmentIds });
      // This response is known human ingress, even if its run event has not
      // arrived yet or the user has switched conversations in the meantime.
      const sentAt = Date.parse(result.message.created_at);
      if (Number.isFinite(sentAt)) setLastUserActivity(current => ({ ...current, [conversationId]: Math.max(current[conversationId] ?? 0, sentAt) }));
      // The server may have accepted the message after the user switched
      // conversations. Clear only the exact source draft that was sent.
      if (draftSignature) setComposerDrafts((current) => {
        const source = current[conversationId];
        if (!source || !composerDraftMatchesMessage(source, draftSignature, clientMessageId)) return current;
        const next = { ...current, [conversationId]: emptyComposerDraft() };
        if (composerStorageKey.current) writeComposerDrafts(composerStorageKey.current, next);
        return next;
      });
      if (activeIdRef.current !== conversationId) return true;
      arrivalIDs.current.add(result.message.id);
      if (!isDesktop && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        flipList(messageListRef.current, () => upsertMessage(result.message), { duration: 520, easing: "cubic-bezier(.22,1,.36,1)" });
        jumpLatest();
        const target = Array.from(messageListRef.current?.querySelectorAll<HTMLElement>(".message-block") ?? []).find(block => block.dataset.flip === result.message.id)?.querySelector<HTMLElement>(".message-user .message-content");
        if (flightOrigin && target) flySentMessage(flightOrigin, target);
      } else upsertMessage(result.message);
      updateConversationPreview(result.message);
      const newRuns = result.runs?.length ? result.runs : [result.run];
      setRuns((current) => mergeRunUpdates(current, newRuns));
      setError("");
      return true;
    } catch (cause) {
      if (activeIdRef.current === conversationId) setError(errorText(cause));
      return false;
    }
  }

  /** Adds a Bot the server created during onboarding to the lists, without moving the person off the sheet. */
  function adoptOnboardingBot(bot: Bot) {
    snapshotGeneration.current += 1;
    setBots((current) => [bot, ...current.filter((item) => item.id !== bot.id)]);
    setConversations((current) => current.some((item) => item.id === bot.dm_conversation_id) ? current : [{ id: bot.dm_conversation_id, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: bot.created_at }, ...current]);
    void refreshConversations();
  }
  /** Setup is over: close the sheet, open the first Bot's direct message, and put the cursor in its composer. */
  function finishOnboarding(botId?: string) {
    saveOnboarding({ step: 3, completed: true, skipped: false });
    setOnboardingForced(null);
    const bot = (botId ? bots.find((item) => item.id === botId) : undefined) ?? bots.find((item) => !item.archived);
    if (!bot) return;
    const conversation = conversations.find((item) => item.id === bot.dm_conversation_id);
    selectConversation(bot.dm_conversation_id, conversation);
    setMobileList(false);
    setPanel(null);
    let tries = 0;
    const focus = () => {
      const box = document.querySelector<HTMLTextAreaElement>(".composer textarea");
      if (box && !box.disabled) { box.focus(); return; }
      if (++tries < 25) window.setTimeout(focus, 80);
    };
    window.setTimeout(focus, 60);
  }

  async function createNewBot() {
    if (newBotBusyRef.current) return;
    newBotBusyRef.current = true;
    newBotPending.current = true;
    snapshotGeneration.current += 1;
    setNewBotBusy(true);
    setNewBotError("");
    const clientCreationId = newBotCreationId.current ?? messageId();
    newBotCreationId.current = clientCreationId;
    try {
      const bot = await api.createNewBot(clientCreationId);
      snapshotGeneration.current += 1;
      const conversation: Conversation = {
        id: bot.dm_conversation_id,
        kind: "dm",
        name: bot.name,
        bot_id: bot.id,
        bot_ids: [bot.id],
        updated_at: bot.created_at,
      };
      setBots((current) => [bot, ...current.filter((item) => item.id !== bot.id)]);
      setConversations((current) => [conversation, ...current.filter((item) => item.id !== conversation.id)]);
      setSearch("");
      selectConversation(bot.dm_conversation_id, conversation);
      setMobileList(false);
      setPanel(null);
      setCreateMenuOpen(false);
      newBotCreationId.current = null;
      setError("");
      newBotPending.current = false;
      void refreshConversations();
    } catch (cause) {
      setNewBotError(errorText(cause));
      setError(errorText(cause));
      newBotPending.current = false;
    } finally {
      newBotBusyRef.current = false;
      setNewBotBusy(false);
    }
  }

  async function importBotPackage(file: File) {
    setPortabilityFile(file);
    setPortabilityBotID("");
    openSettings("advanced");
    setCreateMenuOpen(false);
  }

  async function updateBot(id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) {
    const updated = await api.updateBot(id, input);
    snapshotGeneration.current += 1;
    setBots((current) => current.map((item) => item.id === id ? updated : item));
    if (input.name) setConversations((current) => current.map((item) => item.bot_id === id ? { ...item, name: updated.name } : item));
    setPanel(null);
  }

  async function createGroup(name: string, botIds: string[]) {
    const group = await api.createGroup({ name, bot_ids: botIds });
    snapshotGeneration.current += 1;
    setConversations((current) => [...current, group]);
    selectConversation(group.id, group); setMobileList(false); setPanel(null);
  }

  async function updateGroup(id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) {
    const updated = await api.updateConversation(id, input);
    // Apply the server response immediately so the open chat header and member
    // panel stay coherent, then refresh the full index for other clients'
    // changes. refreshIndex keeps the current conversation and its local draft.
    snapshotGeneration.current += 1;
    setConversations((current) => current.map((item) => item.id === id ? { ...item, ...updated } : item));
    await refreshIndex();
    return updated;
  }

  async function reloadGroup(id: string) {
    const result = await api.conversations();
    const fresh = result.conversations.find((item) => item.id === id);
    if (!fresh) throw new Error(t("error.group_unavailable"));
    snapshotGeneration.current += 1;
    setConversations((current) => current.map((item) => item.id === id ? fresh : item));
    return fresh;
  }

  const mergeArchived = useCallback((archivedConversations: Conversation[], archivedBots: Bot[]) => {
    setConversations((current) => [...current.filter((item) => !archivedConversations.some((archived) => archived.id === item.id)), ...archivedConversations]);
    setBots((current) => [...current.filter((item) => !archivedBots.some((archived) => archived.id === item.id)), ...archivedBots]);
  }, []);
  const refreshAfterArchive = useCallback(async () => { await refreshIndex(); }, [refreshIndex]);

  function confirmDelete(conversation: Conversation) {
    const botId = conversation.bot_id ?? botByConversationId.get(conversation.id)?.id;
    if (conversation.kind === "dm" && !botId) { setError(t("error.bot_missing_refresh")); return; }
    setDeleteTarget({ id: conversation.kind === "dm" ? botId! : conversation.id, conversationId: conversation.id, name: conversation.name, kind: conversation.kind === "dm" ? "bot" : "group" });
  }

  function togglePinned(conversation: Conversation) {
    setSidebarLayout((current) => ({ ...current, pinned: current.pinned.includes(conversation.id) ? current.pinned.filter((id) => id !== conversation.id) : [...current.pinned, conversation.id] }));
    setSidebarContextMenu(null);
  }

  function moveToSection(conversation: Conversation) {
    const name = window.prompt(t("sidebar.move_to_section"), sidebarLayout.sections[conversation.id] ?? "");
    if (name === null) return;
    const nextName = name.trim();
    setSidebarLayout((current) => {
      const sections = { ...current.sections };
      if (nextName) sections[conversation.id] = nextName;
      else delete sections[conversation.id];
      return { ...current, sections };
    });
    setSidebarContextMenu(null);
  }

  async function markUnread(conversation: Conversation) {
    const seq = Math.max(0, (conversation.last_message?.seq ?? 1) - 1);
    try {
      await api.markUnread(conversation.id, seq);
      setConversations((current) => current.map((item) => item.id === conversation.id ? { ...item, read_seq: seq, unread_count: Math.max(1, item.unread_count ?? 0) } : item));
    } catch (cause) { setError(errorText(cause)); }
    setSidebarContextMenu(null);
  }

  async function hideConversation(conversation: Conversation) {
    try {
      if (conversation.kind === "dm") {
        const bot = botByConversationId.get(conversation.id);
        if (!bot) throw new Error(t("error.bot_missing"));
        await api.setBotArchived(bot.id, true);
      } else {
        await api.setConversationArchived(conversation.id, true);
      }
      setSidebarContextMenu(null);
      await refreshIndex();
    } catch (cause) { setError(errorText(cause)); }
  }

  async function restoreConversation(conversation: Conversation) {
    try {
      if (conversation.kind === "dm") {
        const bot = botByConversationId.get(conversation.id);
        if (!bot) throw new Error(t("error.bot_missing"));
        await api.setBotArchived(bot.id, false);
      } else await api.setConversationArchived(conversation.id, false);
      setSidebarContextMenu(null);
      await refreshIndex();
    } catch (cause) { setError(errorText(cause)); }
  }

  function conversationMotion(conversation: Conversation, botId?: string): AvatarMotion {
    if (!botId || (conversation.kind === "group" && botId === conversation.id)) return "sleeping";
    const working = conversation.id === activeId
      ? activeBotRuns(runs, Object.values(drafts), toolActivities).some(run => run.bot_id === botId) || (conversation.working_bot_ids ?? []).includes(botId)
      : (conversation.working_bot_ids ?? []).includes(botId);
    if (working) return "working";
    const lastUser = conversation.kind === "dm" ? Math.max(lastUserActivity[conversation.id] ?? 0,
      Date.parse(conversation.last_user_message_at ?? "") || 0,
      conversation.id === activeId ? latestHumanMessageTime(messages, runs) : 0) : 0;
    const lastWork = Math.max(lastBotWorkActivity[botId] ?? 0,
      latestBotWorkTime(botId, conversation.last_message, conversation.id === activeId ? runs : []));
    return botRecentlyActive(Math.max(lastUser, lastWork), activityNow) ? "awake" : "sleeping";
  }

  async function copyConversationLink(conversation: Conversation) {
    const path = conversationPath(conversation, bots);
    if (!path) return;
    try {
      if (!navigator.clipboard) throw new Error(t("error.clipboard_unavailable"));
      await navigator.clipboard.writeText(new URL(path, window.location.origin).href);
    } catch (cause) { setError(errorText(cause)); }
  }

  async function refreshTaskStatus() {
    const conversationId = activeIdRef.current;
    if (!conversationId) return;
    const [runPage, toolPage, messagePage, memoryPage] = await Promise.all([api.runs(conversationId), api.toolActivities(conversationId), api.messages(conversationId), api.memories(conversationId), questions.refresh(), mailDrafts.refresh()]);
    if (activeIdRef.current !== conversationId) return;
    setRuns(current => mergeRunUpdates(current, runPage.runs));
    for (const tool of toolPage.activities) upsertToolActivity(tool);
    for (const message of messagePage.messages) upsertMessage(message);
    setMemories(memoryPage.memories);
    setLoadedId(conversationId); setSnapshotError("");
  }
  // One status line per working run: the current tool, the answer being
  // written, or the model's latest reasoning headline.
  function runLiveStatus(run: Run): string {
    if (run.status === "waiting") return t("live.waiting_confirm");
    const tool = activeToolForRun(toolActivities, run.id);
    if (tool) return toolActionLabel(tool);
    if (Object.values(drafts).some(d => d.run_id === run.id && d.status === "active" && d.content.trim())) return t("live.writing_fast");
    const signal = runSignals[run.id];
    if (signal?.retryUntil && signal.retryUntil > Date.now()) return t("live.busy_retry_later");
    return signal?.thinking ? t("live.thinking_about", { headline: signal.thinking }) : run.status === "queued" ? t("live.waiting_start") : t("live.thinking");
  }
  function renderTaskOwner(owner: TaskOwner) {
    return <TaskRunBlock key={owner.key} owner={owner} liveStatus={runLiveStatus(owner.family.latest)} liveAvatar={<BotAvatar id={owner.family.latest.bot_id} mini motion={owner.family.latest.status === "waiting" ? "awake" : "working"} />} tools={loadedToolActivities} questions={questions.items} drafts={mailDrafts.items} messages={messageRecords} summaries={terminalToolSummaries} details={toolDetailState} connected={streamConnected} botName={botById.get(owner.family.latest.bot_id)?.name ?? "Bot"} showName={active?.kind === "group"}
      onOpenTools={() => openSettings("connections")}
      onFeedback={text => window.dispatchEvent(new CustomEvent("tofi:task-feedback", {detail:{conversationId:owner.family.latest.conversation_id,text}}))}
      onRefresh={refreshTaskStatus} onLoadDetails={loadToolDetails}
      renderMessage={message => <MessageBubble key={message.id} message={message} sender={botById.get(owner.family.latest.bot_id)} run={message.run_id ? runById.get(message.run_id) : undefined} showAvatar={false} showIdentity={false} />}
      renderQuestion={question => <QuestionCard key={question.question_id} item={question} bot={botById.get(question.bot_id)} group={active?.kind === "group"} archived={active?.archived} onChanged={async resolved => { await questions.refresh(resolved); await refreshConversations(); }} />}
      renderDraft={draft => <MailDraftCard key={draft.draft_id} draft={draft} bot={botById.get(draft.bot_id)} group={active?.kind === "group"} archived={active?.archived} issueOwned onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} />} />;
  }
  const taskAnnouncements = new Map(taskOwners.map(owner => {
    const run = owner.family.latest;
    const input = {run, tools:loadedToolActivities.filter(tool => tool.run_id === run.id), questions:questions.items.filter(question => question.run_id === run.id), drafts:mailDrafts.items.filter(draft => draft.run_id === run.id), summary:terminalToolSummaries.find(summary => summary.run_id === run.id), recordsComplete:toolDetailState[run.id]?.hasMore === false, connected:streamConnected};
    const issue = presentTaskIssue({...input, family:owner.family, tools:loadedToolActivities, questions:questions.items, drafts:mailDrafts.items});
    const approved = input.questions.some(question => question.question_type === "approval" && question.status === "answered" && question.answer === true && !question.approval?.review_only) ? i18n.t("tasks:record.approved_unconfirmed") : "";
    return [run.id, issue ? [issue.title, ...issue.facts, ...issue.secondary.filter(cause => cause !== i18n.t("tasks:secondary.records_incomplete"))].join(" ") : [taskPhaseLabel(input), approved].filter(Boolean).join(" ")];
  }));

  function conversationRow(conversation: Conversation) {
    const bot = conversation.kind === "dm" ? botByConversationId.get(conversation.id) : undefined;
    const preview = previewText(conversation.last_message?.kind === "message_ref" ? localizeMessageRef(conversation.last_message.content) : conversation.last_message?.content?.trim() || "");
    const botId = conversation.kind === "dm" ? bot?.id || conversation.bot_id || conversation.bot_ids[0] || conversation.id : conversation.id;
    const motion = conversationMotion(conversation, bot?.id || conversation.bot_id || botId);
    const task = conversation.task_state;
    const taskGlyph = task?.status === "needs_attention" || task?.status === "waiting" ? "?" : task?.status === "failed" ? "!" : task?.status === "cancelled" ? "×" : "·";
    return <button key={conversation.id} data-sidebar-flip={conversation.id} data-hint={sidebarCollapsed ? conversation.name : undefined}
      aria-label={[conversation.name, task ? taskStateLabel(task) : "", conversation.unread_count ? t("sidebar.unread", { count: conversation.unread_count }) : ""].filter(Boolean).join(t("sidebar.label_separator"))}
      className={`contact-row conversation-row ${activeId === conversation.id ? "active" : ""}`}
      onClick={() => focusTask(conversation)}
      onContextMenu={(event) => { event.preventDefault(); setSidebarContextMenu({ conversation, x: Math.min(event.clientX, window.innerWidth - 248), y: Math.min(event.clientY, window.innerHeight - 360) }); }}>
      <Avatar label={conversation.name} id={botId} group={conversation.kind === "group"} motion={motion} />
      <span className="contact-copy"><strong>{conversation.name}</strong><small>{preview}</small></span>
      <time className="conversation-time">{conversation.last_message ? formatListTime(conversation.last_message.created_at, timezone) : ""}</time>
      {isDesktop ? (Boolean(conversation.unread_count) ? <span className="unread-badge" aria-label={t("sidebar.unread", { count: conversation.unread_count ?? 0 })}>{Math.min(conversation.unread_count ?? 0, 99)}</span> : motion === "working" ? <span className="conversation-working-indicator" aria-label={t("sidebar.working")} /> : null) : <>
        {Boolean(conversation.unread_count) && <span className="conversation-status" data-status="unread" aria-label={t("sidebar.unread", { count: conversation.unread_count ?? 0 })}>{Math.min(conversation.unread_count ?? 0, 99)}</span>}
        {task && task.status !== "completed" && <span className="conversation-task-badge" data-task-status={task.status} aria-label={taskStateLabel(task)}>{taskGlyph}</span>}
        {!task && !conversation.unread_count && motion === "working" && <span className="conversation-status" data-status="working" aria-label={t("sidebar.working")} />}
      </>}
    </button>;
  }

  function hiddenBotRow(bot: Bot) {
    const conversation = conversations.find((item) => item.kind === "dm" && (item.bot_id === bot.id || item.id === bot.dm_conversation_id));
    if (!conversation) return null;
    return <button key={bot.id} className="contact-row conversation-row hidden-bot-row" aria-label={bot.name} onClick={() => { selectConversation(conversation.id); setMobileList(false); }} onContextMenu={(event) => { event.preventDefault(); setSidebarContextMenu({ conversation, x: Math.min(event.clientX, window.innerWidth - 248), y: Math.min(event.clientY, window.innerHeight - 360) }); }}><Avatar label={bot.name} id={bot.id} motion={conversationMotion(conversation, bot.id)} /><span className="contact-copy"><strong>{bot.name}</strong><small>{t("sidebar.hidden_bot")}</small></span></button>;
  }

  async function deleteConversation(target: DeleteTarget) {
    try {
      if (target.kind === "bot") await api.deleteBot(target.id);
      else await api.deleteGroup(target.id);
    } catch (cause) {
      // A second client may have already completed this exact deletion.
      if (!(cause instanceof ApiError && cause.status === 404)) throw cause;
    }
    snapshotGeneration.current += 1;
    if (target.kind === "bot") {
      knownBotNames.current.set(target.id, target.name);
      setBots(current => current.filter(bot => bot.id !== target.id));
      setMessages(current => current.map(message => message.sender_bot_id === target.id ? { ...message, sender_bot_name: target.name } : message));
    }
    setConversations(current => current.filter(conversation => conversation.id !== target.conversationId).map(conversation => target.kind === "bot" ? { ...conversation, bot_ids: conversation.bot_ids.filter(id => id !== target.id) } : conversation));
    setComposerDrafts(current => {
      const next = { ...current }; delete next[target.conversationId];
      if (composerStorageKey.current) writeComposerDrafts(composerStorageKey.current, next);
      return next;
    });
    setPanel(null);
    setError("");
    await refreshIndex();
  }

  async function stopRun(id: string) {
    try {
      const run = await api.cancelRun(id);
      setRuns((current) => mergeRunUpdates(current, [run]));
      setError("");
    } catch (cause) { setError(errorText(cause)); }
  }

  async function retryRun(id: string) {
    try {
      const run = await api.retryRun(id);
      setRuns((current) => mergeRunUpdates(current, [run]));
      setError("");
    } catch (cause) { setError(errorText(cause)); }
  }

  if (!indexInitialized.current && workspaceError) return <div className="center-state startup-error"><h2>{t("workspace.connection_unavailable")}</h2><p>{workspaceError}</p><button className="primary-button" disabled={loading} onClick={() => void refreshIndex()}>{loading ? t("action.retrying") : t("common:action.retry")}</button></div>;
  if (loading && !conversations.length) return <div className="center-state"><DelayedFeedback><div className="spinner" />{t("workspace.loading")}</DelayedFeedback></div>;

  return (
    <div inert={Boolean(deleteTarget || (viewOnlyChat && (isDesktop || compactViewport))) || undefined} className={`workspace${keyboardViewportOpen ? " keyboard-viewport-open" : ""}`} style={viewportHeight ? { "--viewport-height": `${viewportHeight}px` } as React.CSSProperties : undefined}>
      <UpdateBanner />
      {workspaceError && indexInitialized.current && <div className="stream-status workspace-status" role="alert">{t("workspace.refresh_failed")}<button className="secondary-button" onClick={() => void refreshIndex()}>{t("common:action.retry")}</button></div>}
      {computerInfo && (computerInfo.state !== "ready" || computerHealthAlert(computerInfo.health)) && panel !== "desktop" && <div className="stream-status workspace-status computer-preparing" role="status"><strong>{computerInfo.health?.state === "unresponsive" ? t("computer.health_unresponsive") : computerInfo.health?.state === "restarting" ? t("computer.health_restarting") : computerInfo.state === "starting" ? t("computer.starting") : computerInfo.state === "hibernated" ? t("computer.hibernated") : computerInfo.state === "hibernating" ? t("computer.hibernating") : computerInfo.state === "resuming" ? t("computer.resuming") : computerInfo.state === "error" ? t("computer.failed") : computerInfo.state === "stopped" ? t("computer.stopped") : t("computer.unavailable")}</strong><span>{(computerInfo.state === "starting" || computerInfo.state === "resuming") && computerInfo.phase ? ` · ${computerPhaseKeys[computerInfo.phase] ? t(computerPhaseKeys[computerInfo.phase]) : computerInfo.phase}` : ""}{computerInfo.error ? ` · ${computerInfo.error}` : ""}</span>{(computerInfo.state === "error" || computerInfo.state === "stopped" || computerInfo.health?.state === "unresponsive") && <button className="secondary-button" onClick={() => void api.computerRetry().then(() => api.computerInfo()).then(setComputerInfo).catch(cause => setError(errorText(cause)))}>{t("common:action.retry")}</button>}</div>}
      <div className="native-titlebar" aria-hidden="true" /><ActionHints /><div className={`workspace-grid${sidebarCollapsed ? " sidebar-collapsed" : ""}${contextPanelOpen ? " context-open" : ""}${contextPanelOpen || viewOnlyChat ? " card-open" : ""}${activeBot ? " bot-panel-ready" : ""}${panel === "bot-edit" ? " bot-panel-open" : ""}${displayedPanel === "terminal" ? " terminal-context" : ""}`}>
        <aside className={`contact-pane ${mobileList ? "open" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          <div className="sidebar-top">{sidebarCollapsed ? <button className="sidebar-logo-expand" type="button" aria-label={t("sidebar.expand")} data-hint={t("sidebar.expand")} onClick={() => setSidebarCollapsed(false)}><span className="sidebar-logo-art"><BrandLogo variant="calico" /></span><span className="sidebar-logo-arrow" aria-hidden="true"><Icon name="sidebar" size={20} /></span></button> : <BrandLogo variant="calico" />}<div className="sidebar-actions">{!sidebarCollapsed && <button className="top-icon-button sidebar-toggle" aria-label={t("sidebar.collapse")} data-hint={t("sidebar.collapse")} onClick={() => setSidebarCollapsed(true)}><Icon name="sidebar" size={20} /></button>}<div className="create-menu-anchor" ref={createMenuRef}><button ref={createMenuTriggerRef} className="icon-button" onClick={() => setCreateMenuOpen((open) => !open)} data-hint={t("create.menu")} aria-label={t("create.menu")} aria-expanded={createMenuOpen} aria-controls={createMenuOpen ? "create-menu" : undefined}><Icon name="plus" size={18} animated /></button><input ref={botImportRef} type="file" hidden accept=".json,.tofi-bot,application/json" onChange={(event) => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importBotPackage(file); }} />{showCreateMenu && <div id="create-menu" className="create-menu" data-open={createMenuOpen} inert={!createMenuOpen || undefined}><button disabled={newBotBusy} onClick={() => { createMenuTriggerRef.current?.focus(); void createNewBot(); }}><Icon name="bot-add" size={18} /><span>{newBotBusy ? t("create.creating") : t("create.new_bot")}</span></button><button onClick={() => { createMenuTriggerRef.current?.focus(); setCreateMenuOpen(false); setPanel("group-create"); }}><Icon name="group-add" size={18} /><span>{t("create.new_group")}</span></button><button disabled={botImportBusy} onClick={() => { setCreateMenuOpen(false); botImportRef.current?.click(); }}><Icon name="upload" size={18} /><span>{botImportBusy ? t("create.importing") : t("create.import_bot")}</span></button>{(newBotError || botImportError) && <p className="error-text" role="alert">{newBotError || botImportError}</p>}</div>}</div></div></div>
          <label className="search-box"><Icon name="search" size={16} /><input aria-label={t("sidebar.search_label")} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("sidebar.search_placeholder")} /></label>
          <div className="contact-section" ref={sidebarListRef}>
            {pinnedConversations.length > 0 && <><div className="section-label"><span>{t("sidebar.pinned")}</span></div>{pinnedConversations.map(conversationRow)}</>}
            {Object.entries(customSidebarSections).map(([name, items]) => <section className="sidebar-custom-section" key={name}><div className="section-label"><span>{name}</span></div>{items.map(conversationRow)}</section>)}
            <div className="section-label"><span>{t("sidebar.messages")}</span></div>{defaultSidebarConversations.map(conversationRow)}
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && !query && !bots.length && !conversations.length && <FirstRunHero side onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} />}
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && <EmptyState name="sidebar" className={`empty-copy is-compact${!query && !bots.length && !conversations.length ? " first-run-hide-mobile" : ""}`} look={CATS.mochi} pose={query ? "curious" : "asleep"}><p>{query ? t("sidebar.no_match") : t("sidebar.empty")}</p>{!query && <button className="primary-button sidebar-empty-create" disabled={newBotBusy} onClick={() => void createNewBot()}>{newBotBusy ? t("create.creating") : t("create.create_bot")}</button>}</EmptyState>}
            {hiddenBots.length > 0 && <section className="hidden-bots-section"><button type="button" className="hidden-bots-toggle" aria-expanded={hiddenBotsOpen} onClick={() => setHiddenBotsOpen((open) => !open)}><span>{t("sidebar.hidden_bots")}</span><Icon name="chevron-right" size={16} /></button>{hiddenBotsOpen && hiddenBots.map(hiddenBotRow)}</section>}
          </div>
          <div className="sidebar-footer">{onboardingState === "chip" && <FinishSetupChip step={chipStep(modelReady)} onClick={() => setOnboardingForced(chipStep(modelReady))} />}<SidebarAccount connected={streamConnected} onSettings={() => { setPortabilityBotID(""); setPortabilityFile(undefined); openSettings("general", "home"); }} onUsage={() => { setUsageBotId(""); openSettings("usage"); }} onConnection={() => openSettings("models")} onArchive={() => setPanel("archive")} /></div>
        </aside>
        {onboardingState === "sheet" && onboarding && <Onboarding initialStep={onboardingForced ?? resumeStep(onboarding, modelReady)} modelConfigured={modelReady} bot={bots.find((item) => !item.archived)} onProgress={saveOnboarding} onModelChanged={refreshConfig} onBot={adoptOnboardingBot} onSkip={() => { saveOnboarding({ skipped: true }); setOnboardingForced(null); }} onFinish={finishOnboarding} />}
        {sidebarContextMenu && <div className="sidebar-context-menu" style={{ left: sidebarContextMenu.x, top: sidebarContextMenu.y }} role="menu" onClick={(event) => event.stopPropagation()}>
          <button role="menuitem" onClick={() => togglePinned(sidebarContextMenu.conversation)}><Icon name="pin" size={17} />{sidebarLayout.pinned.includes(sidebarContextMenu.conversation.id) ? t("sidebar.unpin") : t("sidebar.pin")}</button>
          <button role="menuitem" onClick={() => moveToSection(sidebarContextMenu.conversation)}><Icon name="plus" size={17} />{t("sidebar.move_to_section")}</button>
          {!sidebarContextMenu.conversation.archived && <button role="menuitem" onClick={() => void markUnread(sidebarContextMenu.conversation)}><Icon name="bell" size={17} />{t("sidebar.mark_unread")}</button>}
          <div className="sidebar-context-divider" />
          {sidebarContextMenu.conversation.kind === "dm" && <button role="menuitem" onClick={() => { selectConversation(sidebarContextMenu.conversation.id); setMobileList(false); setPanel("bot-edit"); setSidebarContextMenu(null); }}><Icon name="edit" size={17} />{t("sidebar.rename_bot")}</button>}
          <button role="menuitem" onClick={() => { void copyConversationLink(sidebarContextMenu.conversation); setSidebarContextMenu(null); }}><Icon name="copy" size={17} />{t("sidebar.copy_link")}</button>
          <button role="menuitem" onClick={() => { void navigator.clipboard?.writeText(sidebarContextMenu.conversation.id); setSidebarContextMenu(null); }}><Icon name="copy" size={17} />{t("sidebar.copy_id")}</button>
          <button role="menuitem" onClick={() => void (sidebarContextMenu.conversation.archived ? restoreConversation(sidebarContextMenu.conversation) : hideConversation(sidebarContextMenu.conversation))}><Icon name="eye-off" size={17} />{sidebarContextMenu.conversation.archived ? t("sidebar.show") : t("sidebar.hide")}</button>
          <button role="menuitem" className="delete-menu-action" onClick={() => { confirmDelete(sidebarContextMenu.conversation); setSidebarContextMenu(null); }}><Icon name="trash" size={17} />{t("sidebar.delete")}</button>
        </div>}

        <main className={`chat-pane ${!mobileList ? "mobile-chat" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          {!active ? routeUnavailable ? <div className="conversation-empty" role="alert"><EmptyCat look={CATS.nori} pose="curious" size={72} name="route-unavailable" /><h2>{t("route.unavailable_title")}</h2><p>{t("route.unavailable_body")}</p><button className="secondary-button" onClick={() => setMobileList(true)}>{t("route.back_to_list")}</button></div> : onboardingState === "sheet" ? null : <FirstRunHero onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} /> : <>
            <div className="chat-header"><div className="chat-title"><button className="back-button" onClick={() => setMobileList(true)} aria-label={t("route.back_to_list")}><Icon name="arrow-left" size={19} /></button><button className="chat-identity" onClick={() => active.kind === "group" ? setPanel("members") : transitionBotPanel(true)} aria-label={t("header.open_members")}><span className="member-avatar-stack">{(active.kind === "group" ? active.bot_ids : [active.bot_id ?? active.id]).slice(0, 4).map((id) => <Avatar key={id} label={botById.get(id)?.name ?? active.name} id={id} mini motion={conversationMotion(active, id)} />)}</span><span className="chat-name-pill"><h1>{active.name}</h1>{active.kind === "group" && <small>{t("header.member_count", { count: active.bot_ids.length })}</small>}</span></button></div><div className="header-actions">{active.kind === "group" && <button className="computer-button" data-hint={t("header.team_board")} aria-label={teamBoardOpen ? t("header.back_to_chat") : t("header.open_team_board")} aria-pressed={teamBoardOpen} onClick={() => { setPanel(null); setTeamBoardOpen(value => !value); }}><Icon name="layout-grid" size={18} variant={teamBoardOpen ? "filled" : "outline"} /></button>}{desktopBot && <button className={`computer-button${desktopReady || computerInfo?.state === "ready" ? " is-ready" : ""}`} data-hint={t("header.shared_computer")} aria-label={t("header.open_shared_computer")} aria-pressed={panel === "desktop"} onClick={() => { if (panel === "desktop") setPanel(null); else { setDesktopSize("small"); setPanel("desktop"); } }}><Icon name="monitor" size={17} variant={panel === "desktop" ? "filled" : "outline"} animated /></button>}{desktopBot && <button className="computer-button" data-hint={t("header.terminal")} aria-label={t("header.open_terminal")} aria-pressed={panel === "terminal"} onClick={() => setPanel(current => current === "terminal" ? null : "terminal")}><Icon name="terminal" size={18} variant={panel === "terminal" ? "filled" : "outline"} /></button>}<div className="header-more" ref={headerMenuRef}><button ref={headerMenuTriggerRef} className="ghost-button" data-hint={t("header.more")} aria-label={t("header.more")} aria-expanded={headerMenuOpen} aria-controls={headerMenuOpen ? "conversation-more-menu" : undefined} onClick={() => setHeaderMenuOpen((open) => !open)}><Icon name="more" size={17} /></button>{showHeaderMenu && <div id="conversation-more-menu" className="more-menu" data-open={headerMenuOpen} inert={!headerMenuOpen || undefined}><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); void copyConversationLink(active); }}>{t("header.copy_link")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setPanel("schedule"); }}>{t("header.schedule")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setPanel("memory"); }}>{active.kind === "group" ? t("header.group_memory") : t("header.bot_memory")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setUsageBotId(active.kind === "dm" ? active.bot_id ?? "" : ""); setSettingsTab("usage"); setPanel("settings"); }}>{t("header.usage")}</button>{active.kind === "dm" && <button onClick={() => { headerMenuTriggerRef.current?.focus(); setPanel("bot-edit"); setHeaderMenuOpen(false); }}>{t("header.bot_settings")}</button>}<button className="delete-menu-action" onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); confirmDelete(active); }}>{active.kind === "dm" ? t("header.delete_bot") : t("header.delete_group")}</button></div>}</div></div></div>
            <RunStatusAnnouncement key={active.id} conversationId={active.id} runs={runs} botById={botById} ready={loadedId === active.id && !loadingMessages} taskAnnouncements={taskAnnouncements} />
            {teamBoardOpen && active.kind === "group" ? <TeamBoard key={active.id} conversation={active} bots={bots} timezone={timezone} onClose={() => setTeamBoardOpen(false)} onOpenWork={() => { setTeamBoardOpen(false); setPanel("schedule"); }} /> : <>
            <div className="chat-body">
              <div className="message-scroll" ref={viewport.scrollRef} onScroll={viewport.onScroll}>
                <ExpiryFinishingContext.Provider value={new Set(runs.filter(run => run.finishing_reason === "approval_expired").map(run => run.id))}><RunStartContext.Provider value={runStarts}><div className="message-list" ref={messageListRef}>
                  {viewport.boundaryError && <div className="history-error" role="alert">{t("history.boundary_failed")}<button onClick={viewport.retryBoundary}>{t("common:action.retry")}</button><button onClick={jumpLatest}>{t("history.view_latest")}</button></div>}
                  {historyError && !viewport.boundaryError && <div className="history-error history-error-older" role="alert"><span>{t("history.older_failed", { error: historyError })}</span><button onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.retrying") : t("common:action.retry")}</button></div>}
                  {hasMore && <button className="load-older" onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.loading") : t("history.load_older")}</button>}
                  {(questions.error || mailDrafts.error || snapshotError) && <div className="task-state-read-error"><span>{i18n.t("tasks:issue.status_unavailable")}</span><button type="button" className="text-button" onClick={() => void refreshTaskStatus().catch(() => setSnapshotError("read_unavailable"))}>{i18n.t("tasks:issue.action.refresh_status")}</button></div>}
                  {loadingMessages ? <DelayedFeedback key={active.id}><div className={`inline-state${isDesktop ? "" : " web-loading-state"}`}>{isDesktop ? <div className="spinner" /> : <LoadingCat size={34} />}{t("history.loading_messages")}</div></DelayedFeedback> : conversationItems.length === 0 ?
                    <EmptyState name="conversation" className="conversation-empty" look={CATS.mochi} pose="curious" size={84}><h2>{t("empty.start_chat")}</h2><p>{t("empty.start_chat_body")}</p></EmptyState> :
                    conversationItems.map((item, index) => {
                      const previousItem = conversationItems[index - 1];
                      const previousTime = previousItem?.kind === "message" ? previousItem.message.created_at : previousItem?.kind === "question" ? previousItem.question.created_at : previousItem?.draft.created_at;
                      if (item.kind === "mail_draft") {
                        const draft = item.draft;
                        if (taskOwners.some(owner => owner.family.attempts.some(run => run.id === draft.run_id && run.bot_id === draft.bot_id && run.conversation_id === draft.conversation_id))) return taskOwners.filter(owner => owner.anchor?.kind === "draft" && owner.anchor.id === draft.draft_id).map(renderTaskOwner);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(draft.created_at, timezone) || +new Date(draft.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`mail-draft-${draft.draft_id}`}>{divider && <div className="date-divider"><span>{formatDateDivider(draft.created_at, timezone)}</span></div>}<MailDraftCard draft={draft} bot={botById.get(draft.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} /></div>;
                      }
                      if (item.kind === "question") {
                        const question = item.question;
                        if (taskOwners.some(owner => owner.family.attempts.some(run => run.id === question.run_id && run.bot_id === question.bot_id && run.conversation_id === question.conversation_id))) return taskOwners.filter(owner => owner.anchor?.kind === "question" && owner.anchor.id === question.question_id).map(renderTaskOwner);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(question.created_at, timezone) || +new Date(question.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`question-${question.question_id}`}>{divider && <div className="date-divider"><span>{formatDateDivider(question.created_at, timezone)}</span></div>}<QuestionCard item={question} bot={botById.get(question.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async (resolved) => { await questions.refresh(resolved); await refreshConversations(); }} /></div>;
                      }
                      const message = item.message;
                      const previous = previousItem?.kind === "message" ? previousItem.message : undefined;
                      const senderKey = message.role === "user" ? "user" : message.sender_bot_id ?? message.role;
                      const previousKey = previous ? previous.role === "user" ? "user" : previous.sender_bot_id ?? previous.role : "";
                      const sameDay = Boolean(previous && dateInTimezone(previous.created_at, timezone) === dateInTimezone(message.created_at, timezone));
                      const gap = previous ? +new Date(message.created_at) - +new Date(previous.created_at) : 0;
                      const unread = message.seq === viewport.unreadSeq;
                      const compact = Boolean(previous && !unread && !message.notice && !previous.notice && sameDay && gap < 5 * 60_000 && senderKey === previousKey);
                      const showDivider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(message.created_at, timezone) || +new Date(message.created_at) - +new Date(previousTime) >= 10 * 60_000;
                      const showIdentity = (active.kind === "group" || message.kind === "forward_result" || message.kind === "notice" && runById.get(message.run_id ?? "")?.stop_reason === "approval_expired") && message.role !== "user" && !compact;
                      const processTools = toolTimeline.beforeMessageId.get(message.id) ?? [];
                      const processProgress = (foldedProgress.progressByFinalId.get(message.id) ?? []).filter(note => !ownedRunIDs.has(note.run_id ?? ""));
                      const afterTools = toolTimeline.afterMessageId.get(message.id) ?? [];
                      const processSummaryByRunID = new Map(toolSummaryAnchors.beforeMessageId.get(message.id)?.map(summary => [summary.run_id, summary]) ?? []);
                      for (const summary of terminalToolSummaries) if (!ownedRunIDs.has(summary.run_id) && processTools.some(activity => activity.run_id === summary.run_id)) processSummaryByRunID.set(summary.run_id, summary);
                      const afterSummaries = terminalToolSummaries.filter(summary => !ownedRunIDs.has(summary.run_id) && afterTools.some(activity => activity.run_id === summary.run_id));
                      for (const summary of toolSummaryAnchors.afterMessageId.get(message.id) ?? []) afterSummaries.push(summary);
                      const processSummaryErrors = Object.fromEntries((toolSummaryErrorAnchors.beforeMessageId.get(message.id) ?? []).map(({ run_id }) => [run_id, toolSummaryState[run_id]?.error ?? t("tools.summary_unavailable")]));
                      const afterSummaryErrors = Object.fromEntries((toolSummaryErrorAnchors.afterMessageId.get(message.id) ?? []).map(({ run_id }) => [run_id, toolSummaryState[run_id]?.error ?? t("tools.summary_unavailable")]));
                      const processSummaryLoading = Object.fromEntries((toolSummaryLoadingAnchors.beforeMessageId.get(message.id) ?? []).map(({ run_id }) => [run_id, true]));
                      const afterSummaryLoading = Object.fromEntries((toolSummaryLoadingAnchors.afterMessageId.get(message.id) ?? []).map(({ run_id }) => [run_id, true]));
                      const processSummaries = [...processSummaryByRunID.values()];
                      const processAggregateRunIDs = new Set([...processSummaries.map(summary => summary.run_id), ...Object.keys(processSummaryErrors), ...Object.keys(processSummaryLoading)]);
                      const afterAggregateRunIDs = new Set([...afterSummaries.map(summary => summary.run_id), ...Object.keys(afterSummaryErrors), ...Object.keys(afterSummaryLoading)]);
                      const processAggregateTools = loadedToolActivities.filter(activity => processAggregateRunIDs.has(activity.run_id));
                      const afterAggregateTools = loadedToolActivities.filter(activity => afterAggregateRunIDs.has(activity.run_id));
                      const replyMarker = readReplyMarker(message.content || drafts[message.id]?.content || "");
                      // Older model replies sometimes echoed a fabricated ID. The run's
                      // trigger is the only trustworthy fallback for that legacy output.
                      const runTriggerId = message.run_id ? runById.get(message.run_id)?.trigger_message_id : undefined;
                      const isEarlierInChat = (candidate: Message | undefined): candidate is Message => Boolean(candidate && candidate.conversation_id === message.conversation_id && candidate.seq < message.seq);
                      const runTrigger = runTriggerId ? messageById.get(runTriggerId) : undefined;
                      const replyTarget = replyMarker.ids.length ? replyMarker.ids.map(id => messageById.get(id)).find(isEarlierInChat) ?? (isEarlierInChat(runTrigger) ? runTrigger : undefined) : undefined;
                      const canReact = messageRecords.some(record => record.id === message.id);
                      return <div className={`message-block${arrivalIDs.current.has(message.id) ? " new-arrival" : ""}`} data-message-seq={message.seq} data-message-id={message.id} data-flip={message.id} tabIndex={-1} key={message.id} onContextMenu={canReact && isDesktop ? event => { event.preventDefault(); setReactionMenu({ messageId: message.id, x: event.clientX, y: event.clientY }); } : undefined}>
                        {showDivider && <div className="date-divider"><span>{formatDateDivider(message.created_at, timezone)}</span></div>}
                        {unread && <div className="unread-divider" aria-label={t("history.unread_below")}><span>{t("history.unread")}</span></div>}
                        <div className={`message-hover-host${message.role === "user" ? " is-user" : ""}`}>
                        {!isDesktop && canReact && message.kind !== "notice" && message.kind !== "message_ref" && <MessageHoverActions message={message} timezone={timezone}
                          onReact={anchor => setReactionMenu({ messageId: message.id, x: anchor.left, y: anchor.bottom + 6 })}
                          onReply={active.archived ? undefined : () => { updateComposerDraft(active.id, current => ({ ...current, reply: { id: message.id, name: message.role === "user" ? t("message.you") : message.sender_bot_name ?? (message.sender_bot_id ? botById.get(message.sender_bot_id)?.name : undefined) ?? "Bot", excerpt: previewText(readReplyMarker(message.content).body) || t("message.attachment") } })); document.querySelector<HTMLTextAreaElement>(".composer textarea")?.focus(); }} />}
                        <MessageBubble message={message} replyTarget={replyTarget} replyTargetName={replyTarget?.role === "user" ? t("message.you") : replyTarget?.sender_bot_name ?? (replyTarget?.sender_bot_id ? botById.get(replyTarget.sender_bot_id)?.name ?? knownBotNames.current.get(replyTarget.sender_bot_id) : undefined)} senderName={message.sender_bot_name ?? (message.sender_bot_id ? knownBotNames.current.get(message.sender_bot_id) : undefined)} targetBot={message.notice?.to_bot_id ? botById.get(message.notice.to_bot_id) : undefined} sender={message.sender_bot_id ? botById.get(message.sender_bot_id) : undefined}
                          run={message.run_id ? runById.get(message.run_id) : undefined}
                          tools={processTools.length || processProgress.length || processSummaries.length || Object.keys(processSummaryErrors).length || Object.keys(processSummaryLoading).length ? <ToolActivityList activities={[...processTools, ...processAggregateTools]} progress={processProgress} summaries={processSummaries} summaryErrors={processSummaryErrors} summaryLoading={processSummaryLoading} details={toolDetailState} onLoadDetails={loadToolDetails} onRetrySummary={retryToolSummary} botById={botById} activeRunIds={activeRunIds} /> : undefined}
                          scheduleOccurrence={message.run_id ? scheduleOccurrences.get(message.run_id) : undefined} schedule={message.run_id ? scheduleMetadata.get(scheduleOccurrences.get(message.run_id)?.schedule_id ?? "") : undefined} onRetrySchedule={active.archived || ownedRunIDs.has(message.run_id ?? "") ? undefined : retryRun}
                          draft={drafts[message.id]?.status === "active" && streamConnected ? drafts[message.id] : undefined}
                          relatedChat={relatedChatTarget(message, active, conversations, bots)} onOpenRelatedChat={openRelatedChat}
                          compact={compact} showAvatar={active.kind === "group"} showIdentity={showIdentity} noticeTargetAvailable={!message.notice || conversations.some(item => item.id === message.notice?.target_conversation_id)} allowOpenDM={active.kind === "group" || message.kind === "forward_result"} onDraftReply={!active.archived && !isDesktop && message.card?.type === "mail" ? () => send(t("message.draft_reply_request", { from: message.card?.from, subject: message.card?.subject, source: message.card?.source }), crypto.randomUUID()) : undefined} onMention={active.archived ? undefined : bot => setAvatarMention({ name: bot.name, conversationId: active.id, nonce: Date.now() })} onNavigate={id => { if (conversations.some(item => item.id === id)) { selectConversation(id); setMobileList(false); } }} />
                        </div>
                        {canReact && <MessageReactions message={message} botNames={new Map(bots.map(bot => [bot.id, bot.name]))} menuPosition={reactionMenu?.messageId === message.id ? reactionMenu : null} onCloseMenu={() => setReactionMenu(null)} onSet={async (emoji, present) => {
                          const result = await api.setMessageReaction(active.id, message.id, emoji, present);
                          if (activeIdRef.current === active.id) setMessages(current => current.map(record => record.id === message.id ? { ...record, reactions: result.reactions } : record));
                        }} />}
                        {taskOwners.filter(owner => owner.anchor?.kind === "message" && owner.anchor.id === message.id).map(renderTaskOwner)}
                        {afterTools.length || afterSummaries.length || Object.keys(afterSummaryErrors).length || Object.keys(afterSummaryLoading).length ? <ToolActivityList activities={[...afterTools, ...afterAggregateTools]} summaries={afterSummaries} summaryErrors={afterSummaryErrors} summaryLoading={afterSummaryLoading} details={toolDetailState} onLoadDetails={loadToolDetails} onRetrySummary={retryToolSummary} botById={botById} activeRunIds={activeRunIds} /> : null}
                      </div>;
                    })}
                  <SecretInputs key={active.id} conversationId={active.id} bots={bots}/>
                  <ToolActivityList activities={toolTimeline.fallback} summaries={terminalToolSummaries.filter(summary => toolTimeline.fallback.some(activity => activity.run_id === summary.run_id))} details={toolDetailState} onLoadDetails={loadToolDetails} botById={botById} activeRunIds={activeRunIds} />
                  {taskOwners.filter(owner => !owner.anchor).map(renderTaskOwner)}
                  {!isDesktop && pendingRuns.filter(run => !ownedRunIDs.has(run.id)).map(run => <LiveRunStatus key={run.id} run={run} botName={run.kind === "triage" ? "Tofi" : botById.get(run.bot_id)?.name ?? "Bot"} showName={active.kind === "group"} />)}
                  {error && <div className="error-banner" role="alert">{error}</div>}
                </div></RunStartContext.Provider></ExpiryFinishingContext.Provider>
              </div>
              {viewport.showJump && <button className="jump-latest" aria-label={t("history.jump_latest")} onClick={jumpLatest}><Icon name="arrow-down" size={18} variant="filled" />{viewport.newCount > 0 && <span className="jump-new-count">{t("history.new_count", { count: viewport.newCount })}</span>}</button>}
            </div>
            {active.kind === "group" && !active.archived && !active.bot_ids.some(id => botById.has(id) && !botById.get(id)?.archived) ? <div className="composer empty-group-composer"><span>{t("composer.no_members")}</span><button className="secondary-button" onClick={() => setPanel("members")}>{t("composer.add_members")}</button></div> : <Composer workStatus={<WorkingMembers isGroup={active.kind === "group"} companionBotId={active.kind === "dm" ? active.bot_id || active.bot_ids[0] : undefined} companionMotion={active.kind === "dm" ? conversationMotion(active, active.bot_id || active.bot_ids[0]) : undefined} workingBotIds={active.working_bot_ids ?? []} drafts={Object.values(drafts)} runs={runs} botById={botById} toolActivities={toolActivities} signals={runSignals} connected={streamConnected} />} activeRun={runs.find(run => run.status === "running" || run.status === "queued" || run.status === "waiting")} latestReplyId={messages.filter(message => message.role === "assistant").at(-1)?.id} onStopRun={stopRun} avatarMention={avatarMention} conversation={active} bots={bots} draft={composerDrafts[active.id] ?? emptyComposerDraft()} onDraftChange={(update) => updateComposerDraft(active.id, update)} onSend={send} onSent={jumpLatest} modelConfigured={config?.model_configured ?? false} onConnectModel={connectModel} draftReady={composerDraftReady} readOnly={Boolean(active.archived)} />}
            </>}
          </>}
        </main>

        {modalPanelOpen && <button type="button" className="detail-backdrop" aria-label={t("detail.close")} onClick={() => { if (displayedPanel === "settings") window.dispatchEvent(new Event("tofi-settings-close")); else if (!detailPaneRef.current?.querySelector('[aria-busy="true"]')) setPanel(null); }} />}
        <aside ref={detailPaneRef} className={`detail-pane ${displayedPanel !== "settings" && displayedPanel !== "desktop" ? "context-panel" : ""} ${(panel === "bot-edit" || displayedPanel) && displayedPanel !== "desktop" ? "visible" : ""} ${!panel && displayedPanel && displayedPanel !== "desktop" ? "surface-exiting" : ""}`} inert={!panel || undefined} role={modalPanelOpen ? "dialog" : undefined} aria-modal={modalPanelOpen ? true : undefined} aria-label={panel === "settings" ? t("common:nav.settings") : panel === "terminal" ? t("header.terminal") : t("detail.label")} tabIndex={modalPanelOpen ? -1 : undefined}>
          {displayedPanel === "group-create" && <BotPanel key="new-group" bots={bots.filter((bot) => !bot.archived)} onClose={() => setPanel(null)} onUpdate={updateBot} onCreateGroup={createGroup} />}
          {(panel === "bot-edit" || displayedPanel === "bot-edit") && <BotPanel onExportData={id => { setPortabilityBotID(id); setPortabilityFile(undefined); openSettings("advanced"); }} refreshToken={scheduleRefresh} memories={memories} onOpenWork={() => setPanel("schedule")} onOpenMemory={() => setPanel("memory")} key={activeBot?.id ?? "edit-empty"} bots={bots} activeBot={activeBot} onClose={() => transitionBotPanel(false)} onUpdate={updateBot} onCreateGroup={createGroup} />}
          {displayedPanel === "settings" && <SettingsShell tab={settingsTab} onTab={setSettingsTab} onClose={() => setPanel(null)} entry={settingsEntry} refreshToken={extensionRefresh} renderPage={(page) => <SettingsPages page={page} bots={bots} conversation={active} timezone={timezone} usageBotId={usageBotId} portabilityBotID={portabilityBotID} portabilityFile={portabilityFile} onPortabilityFileConsumed={() => setPortabilityFile(undefined)} appearance={appearance} extensionRefresh={extensionRefresh} slots={{ codex: <CodexPanel refreshToken={codexStatusRefresh} onConfigured={() => void refreshIndex()} />, notifications: <NotificationSetting />, providersRefresh: codexStatusRefresh, onProvidersConfigured: () => void refreshIndex(), legacyArchive: conversations.some(conversation => conversation.archived) ? <div className="legacy-archive-entry"><span>{t("detail.legacy_archive")}</span><button className="text-button" onClick={() => setPanel("archive")}>{t("detail.manage")}</button></div> : null }} openTab={(tab) => openSettings(tab)} />} />}
          {displayedPanel === "archive" && <ArchivePanel onClose={() => setPanel(null)} onOpen={(id) => { selectConversation(id); setMobileList(false); setPanel(null); }} onLoaded={mergeArchived} onChanged={refreshAfterArchive} onDelete={confirmDelete} />}
          {displayedPanel === "terminal" && desktopBot && <Suspense fallback={<DelayedFeedback><div className="inline-state" role="status">{t("detail.loading_terminal")}</div></DelayedFeedback>}><TerminalPanel key={desktopBot.id} botId={desktopBot.id} botName={desktopBot.name} onClose={() => setPanel(null)} /></Suspense>}
          {displayedPanel === "memory" && activeId && <MemoryPanel key={activeId} memories={memories} conversationId={activeId} scope={active?.kind === "group" ? "group" : "bot"} onClose={() => setPanel(null)} onCreate={async (input) => { const memory = await api.createMemory(activeId, input); setMemories((current) => [...current.filter((item) => item.id !== memory.id), memory]); }} onUpdate={async (id, input) => { const memory = await api.updateMemory(id, input); setMemories((current) => current.map((item) => item.id === id ? memory : item)); }} onDelete={async (id) => { await api.deleteMemory(id); setMemories((current) => current.filter((item) => item.id !== id)); }} />}
          {displayedPanel === "memory" && !activeId && <div className="detail-empty"><button className="close-button" aria-label={t("detail.close_memory")} onClick={() => setPanel(null)}><Icon name="close" size={18} /></button><EmptyState name="memory-pick" look={CATS.azuki} pose="curious" size={56}><p>{t("detail.memory_pick")}</p></EmptyState></div>}
          {displayedPanel === "schedule" && active && <WorkPanel key={active.id} conversation={active} conversations={conversations} bots={bots} refreshToken={scheduleRefresh} onClose={() => setPanel(null)} onNavigate={id => { selectConversation(id); setMobileList(false); setPanel(null); }} />}
          {displayedPanel === "members" && active?.kind === "group" && <MembersPanel refreshToken={scheduleRefresh} onOpenWork={() => setPanel("schedule")} conversation={active} bots={bots} onClose={() => setPanel(null)} onOpen={(id) => { selectConversation(id); setMobileList(false); setPanel(null); }} onSaved={updateGroup} onReload={reloadGroup} />}
        </aside>
      </div>
      {desktopBot && (desktopOpen || activeDesktopOwner) && <FloatingDesktop expanded={desktopExpanded} small={desktopSmall} sheet={desktopSheet} activityLabel={activeDesktopOwner ? t("computer.in_use", { name: botById.get(activeDesktopOwner.bot_id)?.name ?? "Bot" }) : undefined} onOpen={() => { setDesktopSize("big"); setPanel("desktop"); }} onShrink={() => setDesktopSize("small")} onClose={() => setPanel(null)}><BotDesktopPanel presence={desktopPresence} autoConnect={Boolean(activeDesktopOwner)} passivePreview={!desktopOpen} expanded={desktopExpanded} sheet={desktopSheet} botId={desktopBot.id} botName={desktopBot.name} members={bots.map(bot => ({ id: bot.id, name: bot.name }))} onClose={(reason) => { if (reason === "shutdown") { setDesktopReady(false); setComputerInfo({ state: "stopped" }); } setPanel(null); }} onReadyChange={setDesktopReady} onExpandedChange={expanded => { setDesktopSize(expanded ? "big" : "small"); setPanel("desktop"); }} /></FloatingDesktop>}
      {deleteTarget && <DeleteConversationDialog target={deleteTarget} onDelete={deleteConversation} onClose={() => setDeleteTarget(null)} />}
      {viewOnlyChat && <ViewOnlyChat target={viewOnlyChat} bots={bots} card={!isDesktop && !compactViewport} returnFocus={viewOnlyChatTriggerRef.current} onClose={() => setViewOnlyChat(null)} />}
    </div>
  );
}

function MembersPanel({ conversation, bots, onClose, onOpen, onSaved, onReload, onOpenWork, refreshToken }: {
  onOpenWork: () => void;
  refreshToken: number;
  conversation: Conversation;
  bots: Bot[];
  onClose: () => void;
  onOpen: (id: string) => void;
  onSaved: (id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) => Promise<Conversation>;
  onReload: (id: string) => Promise<Conversation>;
}) {
  const { t } = useTranslation("chat");
  const [name, setName] = useState(conversation.name);
  const [selectedIds, setSelectedIds] = useState<string[]>(conversation.bot_ids);
  const [saving, setSaving] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const baselineIds = useRef<string[]>(conversation.bot_ids);
  const baselineName = useRef(conversation.name);
  const panelConversationId = useRef(conversation.id);

  // Workspace SSE refreshes replace the Conversation prop. Keep an in-progress
  // edit intact; a new conversation is the only event that resets this form.
  useEffect(() => {
    if (panelConversationId.current !== conversation.id) {
      panelConversationId.current = conversation.id;
      setName(conversation.name);
      setSelectedIds(conversation.bot_ids);
      baselineIds.current = conversation.bot_ids;
      baselineName.current = conversation.name;
      setError("");
      setConflict(false);
      return;
    }
    // A workspace event should update a pristine panel. Once the user starts
    // typing/selecting, retain the local draft until save or explicit reload.
    const localDirty = name.trim() !== baselineName.current || [...selectedIds].sort().join("\u0000") !== [...baselineIds.current].sort().join("\u0000");
    if (!localDirty && !saving) {
      setName(conversation.name);
      setSelectedIds(conversation.bot_ids);
      baselineIds.current = conversation.bot_ids;
      baselineName.current = conversation.name;
      setError("");
      setConflict(false);
    }
  }, [conversation.id, conversation.name, conversation.bot_ids, name, selectedIds, saving]);

  const changed = name.trim() !== baselineName.current || [...selectedIds].sort().join("\u0000") !== [...baselineIds.current].sort().join("\u0000");
  const valid = name.trim().length > 0 && selectedIds.length >= 2 && selectedIds.length <= 8;

  function toggleMember(id: string) {
    setError("");
    setSelectedIds((current) => current.includes(id) ? current.filter((memberId) => memberId !== id) : current.length >= 8 ? current : [...current, id]);
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (saving || !valid || !changed) return;
    setSaving(true);
    setError("");
    try {
      const next = await onSaved(conversation.id, { name: name.trim(), bot_ids: selectedIds, expected_bot_ids: baselineIds.current, expected_name: baselineName.current });
      setName(next.name);
      setSelectedIds(next.bot_ids);
      baselineName.current = next.name;
      baselineIds.current = next.bot_ids;
      setConflict(false);
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409 && cause.code !== "group_busy" && cause.code !== "conversation_busy") {
        setError(t("members.conflict"));
        setConflict(true);
      } else if (cause instanceof ApiError && (cause.status === 423 || cause.code === "group_busy" || cause.code === "conversation_busy")) {
        setError(t("members.busy"));
      } else {
        setError(errorText(cause));
      }
    } finally {
      setSaving(false);
    }
  }

  async function reload() {
    if (saving || reloading) return;
    setReloading(true);
    setError("");
    try {
      const fresh = await onReload(conversation.id);
      setName(fresh.name);
      setSelectedIds(fresh.bot_ids);
      baselineName.current = fresh.name;
      baselineIds.current = fresh.bot_ids;
      setConflict(false);
    } catch (cause) {
      setError(errorText(cause));
    } finally {
      setReloading(false);
    }
  }

  return <div className="detail-content member-details">
    <div className="detail-heading"><div><h2>{t("members.title")}</h2></div><button className="close-button" aria-label={t("members.close")} disabled={saving || reloading} onClick={onClose}><Icon name="close" size={18} /></button></div>
    <nav className="bot-v2-rows members-v2-rows" aria-label={t("members.title")}><WorkPreview disabled={saving || reloading} scope={{type:"conversations",id:conversation.id}} refreshToken={refreshToken} onOpen={onOpenWork} leading={<span className="bot-v2-row-icon" aria-hidden="true"><Icon name="checklist" size={18}/></span>}/></nav>
    <form className="members-manage-form" aria-busy={saving || reloading} onSubmit={(event) => void submit(event)}>
      <label>{t("members.name")}<input aria-label={t("members.name")} value={name} maxLength={200} disabled={saving || reloading} onChange={(event) => setName(event.target.value)} /></label>
      <div className="members-manage-label"><span>{t("members.members")}</span><span className="muted small-text">{selectedIds.length}/8</span></div>
      <div className="member-picker members-manage-picker" aria-label={t("members.pick")}>
        {bots.filter(bot => !bot.archived || baselineIds.current.includes(bot.id) || selectedIds.includes(bot.id)).map((bot) => <div key={bot.id} className={`member-option member-manage-row ${selectedIds.includes(bot.id) ? "selected" : ""}`}>
          <label><input type="checkbox" checked={selectedIds.includes(bot.id)} disabled={saving || reloading || (!selectedIds.includes(bot.id) && selectedIds.length >= 8)} onChange={() => toggleMember(bot.id)} />
          <BotAvatar id={bot.id} mini /><span>{bot.name}</span></label>
          <button type="button" className="text-button" disabled={saving || reloading} aria-label={t("members.open_dm", { name: bot.name })} onClick={() => onOpen(bot.dm_conversation_id)}>{t("members.dm")}</button>
        </div>)}
      </div>
      <p className="field-note">{t("members.pick_hint")}</p>
      {error && <div className="members-manage-error" role="alert"><p className="error-text">{error}</p>{conflict && <button type="button" className="secondary-button" disabled={reloading} onClick={() => void reload()}>{reloading ? t("members.reloading") : t("members.reload")}</button>}</div>}
      <button className="primary-button" disabled={saving || reloading || !valid || !changed}>{saving ? t("action.saving") : t("members.save")}</button>
    </form>
  </div>;
}



/** Cross-conversation capsules are stored with English labels; show them in the UI language. */
function localizeMessageRef(content: string) {
  const text = content.trim();
  if (text.startsWith("Message from ")) return i18n.t("chat:related.message_from", { name: text.slice(13) });
  if (text.startsWith("Messaged ")) return i18n.t("chat:related.messaged", { name: text.slice(9) });
  return text;
}

function RelatedMessageLink({ target, onOpen }: { target: ViewOnlyChatTarget; onOpen: (target: ViewOnlyChatTarget) => void }) {
  const { t } = useTranslation("chat");
  const targetIDs = target.targetBotIds?.length ? target.targetBotIds : target.target.kind === "group" ? target.target.bot_ids : [target.target.bot_id ?? target.target.id];
  const label = target.label ?? (target.target.kind === "group" ? "Messaged" : "Message from");
  const displayIDs = label === "Message from" && target.sourceBotIds?.length ? target.sourceBotIds : targetIDs;
  const displayName = label === "Message from" && target.sourceName ? target.sourceName : target.targetName ?? target.target.name;
  const visibleDisplayIDs = displayIDs.slice(0, 3);
  return <button type="button" className="related-message-link" aria-label={label === "Message from" ? t("related.from_aria", { name: displayName }) : t("related.to_aria", { name: displayName })} onClick={() => onOpen(target)}><span>{label === "Message from" ? t("related.from") : t("related.to")}</span><span className="related-message-avatars">{visibleDisplayIDs.map((id) => <BotAvatar key={id} id={id} mini />)}{displayIDs.length > visibleDisplayIDs.length && <em>+{displayIDs.length - visibleDisplayIDs.length}</em>}</span><span className="related-message-name">{displayName}</span><Icon name="chevron-right" size={14} /></button>;
}

function SmoothStreamMarkdown({ content, mention, active }: { content: string; mention?: React.ReactNode; active: boolean }) {
  const visible = useSmoothStreamText(content, active);
  return <MessageMarkdown content={visible} mention={mention} cursor={active} />;
}

function readReplyMarker(content: string): { ids: string[]; body: string } {
  const ids: string[] = [];
  let body = content;
  const markerPattern = /^\[message_id=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\]\s*/i;
  for (let marker = markerPattern.exec(body); marker; marker = markerPattern.exec(body)) {
    ids.push(marker[1].toLowerCase());
    body = body.slice(marker[0].length);
  }
  if (ids.length) return { ids, body: /^\[message_id(?:=[0-9a-f-]*)?$/i.test(body) ? "" : body };
  // Streaming can pause in the middle of the internal marker. Never flash it as text.
  if (/^\[message_id(?:=[0-9a-f-]*)?$/i.test(content)) return { ids, body: "" };
  return { ids, body: content };
}

export function MessageBubble({ message, replyTarget, replyTargetName, sender, senderName, targetBot, run, tools, scheduleOccurrence, schedule, onRetrySchedule, onDraftReply, draft, relatedChat, onOpenRelatedChat, compact = false, showAvatar = true, showIdentity = true, noticeTargetAvailable = true, allowOpenDM = false, onNavigate, onMention }: { relatedChat?: ViewOnlyChatTarget | null; onOpenRelatedChat?: (target: ViewOnlyChatTarget) => void; noticeTargetAvailable?: boolean; allowOpenDM?: boolean; onMention?: (bot: Bot) => void; message: Message; replyTarget?: Message; replyTargetName?: string; sender?: Bot; senderName?: string; targetBot?: Bot; run?: Run; tools?: React.ReactNode; scheduleOccurrence?: ScheduleOccurrence; schedule?: Schedule; onRetrySchedule?: (id: string) => Promise<void>; onDraftReply?: () => Promise<boolean>; draft?: string | StreamDraft; compact?: boolean; showAvatar?: boolean; showIdentity?: boolean; onNavigate?: (id: string) => void }) {
  const { t } = useTranslation("chat");
  const {timezone} = useUserTimezone();
  const toolsRunning = run?.status === "running" || run?.status === "queued";
  const answerRef = useRef<HTMLDivElement>(null);
  const previousToolLayout = useRef<{ live: boolean; rect: DOMRect } | null>(null);
  useLayoutEffect(() => {
    if (isDesktop || !answerRef.current) return;
    const answer = answerRef.current;
    const next = answer.getBoundingClientRect();
    const previous = previousToolLayout.current;
    if (previous?.live && !toolsRunning && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      const dy = previous.rect.top - next.top;
      if (Math.abs(dy) > 1) answer.animate({ transform: [`translateY(${dy}px)`, "translateY(0)"] }, { duration: 520, easing: "cubic-bezier(.22,1,.36,1)" });
    }
    previousToolLayout.current = { live: toolsRunning, rect: next };
  });
  if (message.kind === "ui_card" && message.card) {
    const label = sender?.name ?? senderName ?? "Bot";
    return <article className={`message message-bot message-ui-card${showAvatar ? " is-group" : " is-dm"}`} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}>
      {showAvatar && <div className="message-avatar"><Avatar label={label} id={message.sender_bot_id ?? "bot"} mini /></div>}
      <div className="message-body">{showIdentity && !compact && <div className="message-meta"><strong>{label}</strong></div>}<DisplayCard card={message.card} bot={message.sender_bot_id ? { id: message.sender_bot_id, name: label } : undefined} onDraftReply={onDraftReply} />{tools && <div className="message-tools">{tools}</div>}</div>
    </article>;
  }
  if (message.kind === "scheduled_task") {
    return <ScheduledTaskRow message={message} run={run} occurrence={scheduleOccurrence} schedule={schedule} timezone={timezone} onRetry={onRetrySchedule} />;
  }
  if (message.kind === "message_ref" && message.notice) {
    return <article className="message message-notice message-reference" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div>{relatedChat && onOpenRelatedChat ? <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} /> : <span>{localizeMessageRef(message.content)}</span>}</div></article>;
  }
  const assignment = message.kind === "notice" && message.sender_bot_id !== "system" && message.notice &&
    (message.notice.type === "handoff" || message.notice.type === "forward") &&
    message.notice.target_conversation_id === message.conversation_id;
  if (message.kind === "notice" && message.notice && !assignment) {
    const target = message.notice.target_conversation_id;
    const targetBot = sender?.name;
    const showSender = targetBot && !message.content.startsWith(targetBot);
    const content = <>{showSender && <><span>{targetBot}</span> · </>}{message.content}</>;
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div>{noticeTargetAvailable ? <button onClick={() => onNavigate?.(target)}>{content}</button> : <span>{content}</span>}{relatedChat && onOpenRelatedChat && <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} />}</div></article>;
  }
  const isUser = message.role === "user";
  const label = message.kind === "notice" && run?.stop_reason === "approval_expired" ? t("message.system") : isUser ? t("message.you") : sender?.name ?? senderName ?? (message.role === "tool" ? t("message.tool") : "Bot");
  const draftText = typeof draft === "string" ? draft : draft?.content ?? "";
  const scheduled = isUser && run?.kind === "schedule";
  const hideMeta = compact && !scheduled;
  const rawBody = message.content || draftText;
  const contextLabel = (sender?.name || senderName) && message.sender_bot_id ? `[sender ${sender?.name ?? senderName} id=${message.sender_bot_id}]` : "";
  let body = contextLabel && rawBody.startsWith(contextLabel) ? rawBody.slice(contextLabel.length).trimStart() : rawBody;
  body = readReplyMarker(body).body;
  if (assignment) {
    // Only collapse the structured recipient at the start; ordinary @ references stay text.
    const text = body.trimStart();
    const aliases = [targetBot?.name, message.notice!.to_bot_id].filter((name): name is string => Boolean(name)).sort((a, b) => b.length - a.length);
    const existing = aliases.find(name => text.startsWith(`@${name}`) && (text.length === name.length + 1 || /[\s,:\uFF0C\uFF1A]/.test(text[name.length + 1])));
    if (existing) body = text.slice(existing.length + 1).trimStart();
  }
  const mention = assignment ? <span className="message-mention"><Avatar label={targetBot?.name ?? t("message.member")} id={message.notice!.to_bot_id} mini /><span>@{targetBot?.name ?? t("message.member")}</span></span> : undefined;
  const inlineMention = mention && targetBot ? <BotIdentityCard bot={targetBot} onMention={onMention ? () => onMention(targetBot) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(targetBot.dm_conversation_id) : undefined}>{mention}</BotIdentityCard> : mention;
  const toolRow = tools && <div className="message-tools">{tools}</div>;
  return <article className={`message ${isUser ? "message-user" : "message-bot"} ${message.kind === "segment" ? "message-segment" : ""} ${message.kind === "forward_result" ? "message-forward-result" : ""} ${compact ? "message-compact" : ""}`} title={isDesktop ? `${formatExactTime(message.created_at, timezone)} · ${timezone}` : undefined}>{showAvatar && !isUser && <div className="message-avatar">{sender ? <BotIdentityCard bot={sender} onMention={onMention ? () => onMention(sender) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(sender.dm_conversation_id) : undefined}><Avatar label={label} id={sender.id} mini /></BotIdentityCard> : <Avatar label={label} id={message.sender_bot_id ?? "bot"} mini />}</div>}<div className={`message-body${tools && !isDesktop ? ` web-tool-body ${toolsRunning ? "is-tools-live" : "is-tools-done"}` : ""}`}><div className={`message-meta ${hideMeta ? "compact" : ""}`}>{scheduled && <span>{t("message.scheduled")}</span>}{showIdentity && !isUser && !compact && <strong>{label}</strong>}</div>{(toolsRunning || !isDesktop) && toolRow}{replyTarget && <button type="button" className="message-reply-quote" aria-label={t("message.jump_to_reply", { name: replyTargetName ?? t("message.original") })} onClick={() => jumpToMessage(document.querySelector<HTMLElement>(`[data-message-id="${replyTarget.id}"]`))}><span className="message-reply-author">{t("message.reply_to", { name: replyTargetName ?? t("message.original") })}</span><span className="message-reply-excerpt">{previewText(readReplyMarker(replyTarget.content).body) || t("message.attachment")}</span></button>}<div className="message-content" ref={answerRef}>{<SmoothStreamMarkdown content={body} mention={inlineMention} active={Boolean(draft) && !isDesktop} />}{message.attachments?.map((a) => <MessageAttachment attachment={a} compact={isUser} key={a.id} />)}{draft && isDesktop && <span className="typing-cursor" />}</div>{!toolsRunning && isDesktop && toolRow}{relatedChat && onOpenRelatedChat && <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} />}</div></article>;
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

type RunSignal = { thinking?: string; retryUntil?: number };

/** The headline of a reasoning summary: its bold title, else its first line, kept short. */
export function thinkingHeadline(text: string): string {
  const bold = [...text.matchAll(/\*\*([^*\n]{2,80})\*\*/g)].at(-1)?.[1];
  const line = (bold ?? text.split("\n").map(part => part.trim()).filter(Boolean)[0] ?? "").replace(/[*_`#]/g, "").trim();
  return line.length > 36 ? `${line.slice(0, 35)}…` : line;
}

function WorkingMembers({ isGroup, companionBotId, companionMotion, workingBotIds, runs, drafts, botById, toolActivities, signals = {}, connected }: { isGroup: boolean; companionBotId?: string; companionMotion?: AvatarMotion; workingBotIds: string[]; drafts: StreamDraft[]; runs: Run[]; botById: Map<string, Bot>; toolActivities: ToolActivity[]; signals?: Record<string, RunSignal>; connected: boolean }) {
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
        if (Math.abs(dx) > 1 || Math.abs(dy) > 1) element.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "translate(0, 0)" }], { duration: 220, easing: "cubic-bezier(.2,.8,.2,1)" });
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


function ToolActivityList({ activities, progress = [], summaries = [], summaryErrors = {}, summaryLoading = {}, details = {}, onLoadDetails, onRetrySummary, botById, activeRunIds }: { activities: ToolActivity[]; progress?: Message[]; summaries?: ToolActivityRunSummary[]; summaryErrors?: Record<string, string>; summaryLoading?: Record<string, boolean>; details?: Record<string, ToolDetailState>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; onRetrySummary?: (runId: string) => void; botById: Map<string, Bot>; activeRunIds: Set<string> }) {
  const { t } = useTranslation("chat");
  const { timezone } = useUserTimezone();
  const [now, setNow] = useState(() => Date.now());
  const hasLiveTool = activities.some(activity => activeRunIds.has(activity.run_id));
  useEffect(() => {
    if (!hasLiveTool) return;
    const interval = window.setInterval(() => setNow(Date.now()), isDesktop ? 1000 : 100);
    return () => window.clearInterval(interval);
  }, [hasLiveTool]);
  if (!activities.length && !progress.length && !summaries.length && !Object.keys(summaryErrors).length && !Object.keys(summaryLoading).length) return null;
  const summariesByRunID = new Map(summaries.map(summary => [summary.run_id, summary]));
  const grouped = [...new Set([...activities.map((a) => a.run_id), ...progress.map(message => message.run_id).filter((id): id is string => Boolean(id)), ...summaries.map(summary => summary.run_id), ...Object.keys(summaryErrors), ...Object.keys(summaryLoading)])].map((runId) => {
    const allItems = orderToolActivities(activities.filter((a) => a.run_id === runId));
    const notes = progress.filter(message => message.run_id === runId).sort((a, b) => a.seq - b.seq);
    return { runId, items: allItems, notes, summary: summariesByRunID.get(runId), summaryError: summaryErrors[runId], summaryLoading: summaryLoading[runId] };
  }).sort((a, b) => compareQuestionTime(a.notes[0]?.created_at ?? a.items[0]?.started_at ?? a.summary?.started_at ?? "", b.notes[0]?.created_at ?? b.items[0]?.started_at ?? b.summary?.started_at ?? "") || a.runId.localeCompare(b.runId));
  return <div className="work-details" aria-label={t("tools.details")}>{grouped.map(({ runId, items, notes, summary, summaryError, summaryLoading: loadingSummary }) => <ToolActivityRun key={runId} runId={runId} items={items} notes={notes} summary={summary} summaryError={summaryError} summaryLoading={loadingSummary} detail={details[runId]} onLoadDetails={onLoadDetails} onRetrySummary={onRetrySummary} live={activeRunIds.has(runId)} now={now} timezone={timezone} botName={botById.get(items[0]?.bot_id ?? notes[0]?.sender_bot_id ?? summary?.bot_id ?? "")?.name ?? "Bot"} />)}</div>;
}

type ToolRunProps = { runId: string; items: ToolActivity[]; notes: Message[]; summary?: ToolActivityRunSummary; summaryError?: string; summaryLoading?: boolean; detail?: ToolDetailState; onLoadDetails?: (runId: string, offset: number) => Promise<void>; onRetrySummary?: (runId: string) => void; live: boolean; now: number; timezone: string; botName: string };

function toolStepIcon(name: string): TofiIconName {
  // Labels may be Chinese (搜索/查找, 网页/打开, 读取/文件/文档, 修改/写入); escaped so the source holds no UI text.
  if (/search|\u641c\u7d22|\u67e5\u627e|find/i.test(name)) return "search";
  if (/browser|\u7f51\u9875|navigate|\u6253\u5f00|url/i.test(name)) return "globe";
  if (/read|\u8bfb\u53d6|\u6587\u4ef6|\u6587\u6863/i.test(name)) return "file-text";
  if (/write|edit|\u4fee\u6539|\u5199\u5165/i.test(name)) return "edit";
  return "terminal";
}

function toolStepLabel(activity: ToolActivity) {
  // Drop the Chinese progressive prefix 正在 ("in the middle of") from a step label.
  return toolActionLabel(activity).replace(/^\u6b63\u5728/, "");
}

function webToolSeconds(activity: ToolActivity, now: number): number | undefined {
  const started = Date.parse(activity.started_at);
  const ended = activity.status === "running" ? now : Date.parse(activity.updated_at);
  return Number.isFinite(started) && Number.isFinite(ended) && ended >= started ? (ended - started) / 1000 : undefined;
}

function toolSummaryIssues(summary?: ToolActivityRunSummary): string {
  if (!summary) return "";
  return [summary.failed_count ? i18n.t("chat:tools.failed_count", { count: summary.failed_count }) : "", summary.interrupted_count ? i18n.t("chat:tools.interrupted_count", { count: summary.interrupted_count }) : "", summary.pending_count ? i18n.t("chat:tools.pending_count", { count: summary.pending_count }) : "", summary.expired_count ? i18n.t("chat:tools.expired") : "", summary.skipped_count ? i18n.t("chat:tools.skipped_count", { count: summary.skipped_count }) : ""].filter(Boolean).join(" · ");
}

function toolRunTimes(items: ToolActivity[], notes: Message[], summary?: ToolActivityRunSummary) {
  const starts = [...items.map(item => Date.parse(item.started_at)), ...notes.map(note => Date.parse(note.created_at)), Date.parse(summary?.started_at ?? "")].filter(Number.isFinite);
  const ends = [...items.map(item => Date.parse(item.updated_at)), ...notes.map(note => Date.parse(note.created_at)), Date.parse(summary?.updated_at ?? "")].filter(Number.isFinite);
  return { first: starts.length ? Math.min(...starts) : Number.NaN, last: ends.length ? Math.max(...ends) : Number.NaN };
}

function WebToolActivityRun({ runId, items, notes, summary, summaryError, summaryLoading, detail, onLoadDetails, onRetrySummary, live, now, timezone, botName }: ToolRunProps) {
  const { t } = useTranslation(["chat", "common"]);
  const finishing = useContext(ExpiryFinishingContext).has(runId);
  const [expanded, setExpanded] = useState(false);
  const stepsRef = useRef<HTMLOListElement>(null);
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
    const animation = steps.animate([{ height: `${fromHeight}px` }, { height: `${nextHeight}px` }], { duration: 440, easing: "cubic-bezier(.22,1,.36,1)" });
    growthAnimation.current = animation;
    void animation.finished.finally(() => { if (growthAnimation.current === animation) { growthAnimation.current = null; steps.style.overflow = ""; } }).catch(() => undefined);
  }, [entries.length, expanded, live, stepKeys]);
  useEffect(() => () => { growthAnimation.current?.cancel(); }, []);
  return <section className={`tool-message web-tool-run${live ? " is-live" : ` is-complete${issues ? " has-tool-issues" : ""}`}`} aria-label={t("tools.steps_label", { name: botName })}>
    <div className="web-tool-disclosure">
      <div className="web-tool-summary">
        {!live && Number.isFinite(lastTime) && <time>{formatTime(new Date(lastTime).toISOString(), timezone)}</time>}
        <button type="button" className="web-tool-toggle" aria-expanded={expanded} aria-controls={`web-tool-drawer-${runId}`} onClick={() => setExpanded(value => { const next = !value; if (next) loadDetails(); return next; })}>
          <Icon className="web-tool-chevron" name="chevron-right" size={14} />
          <span className={live ? "web-tool-live-label" : undefined}>{activityLabel}{!live && count !== undefined ? ` · ${t("tools.call_count", { count })}` : ""}{issues ? ` · ${issues}` : ""}{notes.length ? ` · ${t("tools.report_count", { count: notes.length })}` : ""}{!live && duration !== undefined ? ` · ${duration}` : ""}</span>
        </button>
      </div>
      <div className={`web-tool-drawer${expanded ? " is-open" : ""}`} id={`web-tool-drawer-${runId}`} aria-hidden={!expanded} inert={!expanded}><div className="web-tool-drawer-inner">
      <ol className="web-tool-steps" ref={stepsRef}>{summaryLoading && <li className="web-tool-step is-note"><span>{t("tools.summary_loading")}</span></li>}{summaryError && <li className="web-tool-step is-note"><span>{t("tools.summary_failed", { error: summaryError })}</span><button type="button" className="secondary-button" onClick={() => onRetrySummary?.(runId)}>{t("common:action.retry")}</button></li>}{!entries.length && <li className="web-tool-step is-note"><span>{detail?.loading ? t("tools.details_loading") : detail?.error ? t("tools.details_failed", { error: detail.error }) : t("tools.expand_to_load")}</span></li>}{entries.map(entry => {
        if (entry.type === "note") return <li className="web-tool-step is-note" key={entry.note.id}><Icon name="file-text" size={16} /><div className="web-tool-step-copy"><span>{t("tools.progress_note")}</span><div className="web-tool-note"><MessageMarkdown content={entry.note.content} /></div></div><time>{formatExactTime(entry.note.created_at, timezone)}</time></li>;
        const activity = entry.activity;
        const duration = webToolSeconds(activity, now);
        const active = live && (activity.status === "running" || activity.status === "queued");
        return <li className={`web-tool-step is-${toolDisplayState(activity)}`} key={toolActivityKey(activity)}>
          <Icon name={toolStepIcon(toolStepLabel(activity))} size={16} />
          <details className="web-tool-step-detail"><summary><span className="web-tool-step-label">{toolStepLabel(activity)}{toolArgumentPreview(activity) ? ` · ${toolArgumentPreview(activity)}` : ""}</span></summary>
            <div className="tool-activity-details"><small>{Number.isFinite(Date.parse(activity.started_at)) ? formatExactTime(activity.started_at, timezone) : ""} · {timezone}</small><div><span>{t("tools.arguments")}</span><pre>{activity.arguments || t("tools.none")}</pre></div><div><span>{t("tools.result")}</span>{activity.outcome && <p className="tool-outcome" role="note">{activity.outcome.message}</p>}<pre>{activity.result || (active ? t("tools.waiting_result") : t("tools.none"))}</pre></div>{activity.truncated && <small>{t("tools.truncated")}</small>}</div>
          </details>
          <span className="web-tool-step-meta">{active ? <span className="web-tool-breath" aria-hidden="true" /> : <Icon name={activity.status === "completed" ? "check" : toolDisplayState(activity) !== activity.status ? "clock" : "alert"} size={14} variant="filled" />}{toolDisplayLabel(activity)}{duration === undefined ? "" : ` · ${duration.toFixed(1)}s`}</span>
        </li>;
      })}{hasMoreDetails && <li className="web-tool-step is-note"><button type="button" className="secondary-button" disabled={detail?.loading} onClick={loadDetails}>{detail?.loading ? t("action.loading") : t("tools.load_more", { shown: items.length, total: count ?? "?" })}</button></li>}</ol>
      </div></div>
    </div>
  </section>;
}

function ToolActivityRun(props: ToolRunProps) {
  return isDesktop ? <DesktopToolActivityRun {...props} /> : <WebToolActivityRun {...props} />;
}

function DesktopToolActivityRun({ runId, items, notes, summary: runSummary, summaryError, summaryLoading, detail, onLoadDetails, onRetrySummary, live, now, timezone, botName }: ToolRunProps) {
    const { t } = useTranslation(["chat", "common"]);
    const finishing = useContext(ExpiryFinishingContext).has(runId);
    const [expanded, setExpanded] = useState(false);
    const count = runSummary?.tool_count ?? (summaryError || summaryLoading ? undefined : items.length);
    const current = activeToolForRun(items, runId);
    const queued = items.find(item => item.status === "queued");
    const times = toolRunTimes(items, notes, runSummary);
    const firstTime = times.first;
    const lastTime = live && !finishing ? now : times.last;
    const duration = Number.isFinite(firstTime) && Number.isFinite(lastTime) && lastTime >= firstTime ? formatRunDuration(lastTime - firstTime, false) : undefined;
    const summary = finishing ? t("live.finishing") : live ? current ? t("tools.running_step", { name: current.name, step: items.indexOf(current) + 1 }) : queued ? t("tools.waiting_step", { name: queued.name, step: items.indexOf(queued) + 1 }) : t("tools.collecting") : summaryLoading ? t("tools.summary_loading") : summaryError ? t("tools.details_not_loaded") : notes.length ? [t("tools.process"), t("tools.report_count", { count: notes.length }), count ? t("tools.tool_count", { count }) : ""].filter(Boolean).join(" · ") : t("tools.tool_count", { count: count ?? 0 });
    const entries = [
      ...items.map(activity => ({ type: "tool" as const, at: activity.started_at, activity })),
      ...notes.map(note => ({ type: "note" as const, at: note.created_at, note })),
    ].sort((a, b) => compareQuestionTime(a.at, b.at));
    return <section className={`tool-message${live ? " is-live" : ""}`}>
      <div className="tool-sender">{botName}</div>
      <details className="tool-activity v2-tool-run" open={expanded} onToggle={event => { setExpanded(event.currentTarget.open); if (event.currentTarget.open && !detail?.loading && (count === undefined || items.length < count) && detail?.hasMore !== false) void onLoadDetails?.(runId, detail?.loaded ?? 0); }}>
        <summary className="v2-tool-summary"><span>{summary}{duration === undefined ? "" : ` · ${duration}`}</span></summary>
        <ol className="v2-tool-steps">{summaryLoading && <li className="v2-progress-note"><div>{t("tools.summary_loading")}</div></li>}{summaryError && <li className="v2-progress-note"><div>{t("tools.summary_failed", { error: summaryError })}</div><button type="button" className="secondary-button" onClick={() => onRetrySummary?.(runId)}>{t("common:action.retry")}</button></li>}{!entries.length && <li className="v2-progress-note"><div>{detail?.loading ? t("tools.details_loading") : detail?.error ? t("tools.details_failed", { error: detail.error }) : t("tools.expand_to_load")}</div></li>}{entries.map(entry => {
          if (entry.type === "note") return <li className="v2-progress-note" key={entry.note.id}><span className="v2-tool-step-marker" aria-hidden="true" /><div><small>{t("tools.progress_note")} · {formatExactTime(entry.note.created_at, timezone)}</small><MessageMarkdown content={entry.note.content} /></div></li>;
          const { activity } = entry;
          const start = Date.parse(activity.started_at);
          const end = activity.status === "running" ? now : Date.parse(activity.updated_at);
          const duration = Number.isFinite(start) && Number.isFinite(end) && end >= start ? (end - start) / 1000 : undefined;
          return <li className={`v2-tool-step tool-status-${toolDisplayState(activity)}`} key={toolActivityKey(activity)}>
            <details className="v2-tool-step-detail"><summary><span className="v2-tool-step-marker" aria-hidden="true" /><strong>{activity.name}</strong>{toolArgumentPreview(activity) && <span className="v2-tool-step-argument">{toolArgumentPreview(activity)}</span>}<span className="v2-tool-step-state">{toolDisplayLabel(activity)}</span><time>{duration === undefined ? "" : activity.status === "running" ? `${duration}s…` : `${duration}s`}</time></summary>
              <div className="tool-activity-details"><small>{Number.isFinite(Date.parse(activity.started_at)) ? formatExactTime(activity.started_at, timezone) : ""} · {timezone}</small><div><span>{t("tools.arguments")}</span><pre>{activity.arguments || t("tools.none")}</pre></div><div><span>{t("tools.result")}</span>{activity.outcome && <p className="tool-outcome" role="note">{activity.outcome.message}</p>}<pre>{activity.result || (activity.status === "running" || activity.status === "queued" ? t("tools.waiting_result") : t("tools.none"))}</pre></div>{activity.truncated && <small>{t("tools.truncated")}</small>}</div>
            </details>
          </li>;
        })}{detail?.hasMore && (count === undefined || items.length < count) && <li className="v2-progress-note"><button type="button" className="secondary-button" disabled={detail.loading} onClick={() => void onLoadDetails?.(runId, detail.loaded)}>{detail.loading ? t("action.loading") : t("tools.load_more", { shown: items.length, total: count ?? "?" })}</button></li>}</ol>
      </details>
    </section>;
}

export function CodexPanel({ refreshToken, onConfigured }: { refreshToken: number; onConfigured: () => void }) {
  const { t } = useTranslation("chat");
  const [connected, setConnected] = useState<boolean | null>(null);
  const [expiresAt, setExpiresAt] = useState<number | undefined>();
  const [session, setSession] = useState<{ id: string; url: string; code: string; expiresAt: number } | null>(null);
  const [working, setWorking] = useState(true);
  const [statusRefresh, setStatusRefresh] = useState(0);
  const [error, setError] = useState("");
  const statusRequest = useRef(0);
  const [needsReconnect, setNeedsReconnect] = useState(false);
  const [check, setCheck] = useState<"" | "checking" | "ok" | "unverified">("");
  useEffect(() => {
    const request = ++statusRequest.current;
    setWorking(true); setConnected(null); setExpiresAt(undefined); setError(""); setNeedsReconnect(false); setCheck("");
    api.codexStatus().then(async (status) => {
      if (request !== statusRequest.current) return;
      setConnected(status.connected); setExpiresAt(status.expires_at); setNeedsReconnect(Boolean(status.needs_reconnect));
      if (!status.connected) return;
      // A stored credential is not proof the provider still accepts it.
      setCheck("checking");
      const verified = await api.codexVerify().catch(() => null);
      if (request !== statusRequest.current) return;
      if (!verified) { setCheck("unverified"); return; }
      setConnected(verified.connected); setExpiresAt(verified.expires_at); setNeedsReconnect(Boolean(verified.needs_reconnect));
      setCheck(verified.check === "ok" ? "ok" : verified.check === "unverified" ? "unverified" : "");
      if (verified.check === "rejected") onConfigured();
    }).catch((cause) => { if (request === statusRequest.current) setError(errorText(cause)); }).finally(() => { if (request === statusRequest.current) setWorking(false); });
  }, [refreshToken, statusRefresh]);
  async function connect() {
    ++statusRequest.current;
    setWorking(true); setError("");
    try {
      const result = await api.codexConnect();
      const next = { id: result.session_id, url: result.verification_url, code: result.user_code, expiresAt: result.expires_at };
      setSession(next);
      const expires = result.expires_at;
      const delay = Math.max(1000, result.interval * 1000);
      while (Date.now() < expires) {
        await new Promise((resolve) => window.setTimeout(resolve, delay));
        const status = await api.codexPoll(result.session_id);
        if (status.connected) { setConnected(true); setNeedsReconnect(false); setExpiresAt(status.expires_at); setSession(null); onConfigured(); return; }
        if (!status.pending) break;
      }
      setError(t("codex.session_expired"));
    } catch (cause) { setError(errorText(cause)); }
    finally { setWorking(false); }
  }
  async function disconnect() { ++statusRequest.current; setWorking(true); setError(""); try { await api.codexDisconnect(); setConnected(false); setExpiresAt(undefined); onConfigured(); } catch (cause) { setError(errorText(cause)); } finally { setWorking(false); } }
  return <article className="detail-content provider-card" aria-labelledby="codex-provider-title" aria-busy={working}><div className="provider-card-head"><div><h3 id="codex-provider-title">Codex</h3><p>{t("codex.description")}</p></div><span className="provider-card-marks"><StatusBadge state={connected === true ? (needsReconnect ? "bad" : "ok") : connected === false ? "need" : "testing"} /><span className="settings-tag">{t("codex.tag")}</span></span></div><div className={`codex-status ${connected === true ? "connected" : connected === false ? "missing" : "unknown"}`} role="status"><span className="status-dot" />{connected === true ? check === "checking" ? t("codex.verifying") : check === "ok" ? t("codex.connected_verified") : check === "unverified" ? t("codex.connected_unverified") : t("codex.connected") : connected === false ? needsReconnect ? t("codex.needs_reconnect") : t("codex.not_connected") : working ? t("codex.reading_status") : t("codex.status_unknown")}</div>{expiresAt && connected && check !== "unverified" && <p className="field-note">{t("codex.expires", { date: new Date(expiresAt).toLocaleString(intlLocale()) })}</p>}{connected && check === "unverified" && <p className="field-note">{t("codex.unverified_note")} <button className="text-button" onClick={() => setStatusRefresh(value => value + 1)} disabled={working}>{t("codex.reverify")}</button></p>}{session ? <div className="verification-card"><p>{t("codex.enter_code")}</p><code>{session.code}</code><a href={session.url} target="_blank" rel="noreferrer">{t("codex.open_verification")} <Icon name="external-link" size={16} style={{ verticalAlign: "middle" }} /></a></div> : connected === true ? <button className="secondary-button" onClick={() => void disconnect()} disabled={working}>{t("codex.disconnect")}</button> : connected === null ? <button className="secondary-button" onClick={() => setStatusRefresh(value => value + 1)} disabled={working}>{working ? t("codex.reading") : t("codex.retry_status")}</button> : <button className="primary-button" onClick={() => void connect()} disabled={working}>{working ? t("codex.connecting") : needsReconnect ? t("codex.reconnect") : t("codex.connect")}</button>}{error && <p className="error-text">{error}</p>}</article>;
}

function DictationControls({ elapsed, levels, busy, onCancel, onConfirm }: { elapsed: number; levels: number[]; busy: boolean; onCancel: () => void; onConfirm: () => void }) {
  const { t } = useTranslation("chat");
  const clock = `${Math.floor(elapsed / 60)}:${String(elapsed % 60).padStart(2, "0")}`;
  return <div className={`dictation-controls${busy ? " is-busy" : ""}`} aria-label={busy ? t("dictation.transcribing_label") : t("dictation.recording")}>
    <button type="button" className="dictation-cancel" aria-label={busy ? t("dictation.cancel_transcription") : t("dictation.cancel_recording")} onClick={onCancel}><Icon name="close" size={18} /></button>
    <span className="dictation-record-dot" aria-hidden="true" />
    <time className="dictation-timer" aria-label={t("dictation.timer", { time: clock })}>{clock}</time>
    {busy ? <span className="dictation-processing" role="status">{t("dictation.transcribing")}</span> : <div className="dictation-wave" aria-hidden="true">{levels.map((height, index) => <i key={index} className={index < 12 ? "is-old" : ""} style={{ height: `${height}px` }} />)}</div>}
    <button type="button" className="dictation-confirm" aria-label={t("dictation.finish")} disabled={busy} onClick={onConfirm}><Icon name="check" size={24} variant="filled" /></button>
  </div>;
}

function composerMentionParts(content: string, names: string[]) {
  const ordered = [...names].sort((a, b) => b.length - a.length);
  const parts: React.ReactNode[] = [];
  let plainStart = 0;
  for (let index = 0; index < content.length; index += 1) {
    if (content[index] !== "@" || (index > 0 && !/\s/.test(content[index - 1]))) continue;
    const name = ordered.find((candidate) => content.startsWith(candidate, index + 1) && (index + candidate.length + 1 === content.length || /[\s,\uFF0C\u3002!?\uFF1F;\uFF1B:\uFF1A]/.test(content[index + candidate.length + 1])));
    if (!name) continue;
    if (plainStart < index) parts.push(content.slice(plainStart, index));
    parts.push(<span key={index} className="composer-at-chip">@{name}</span>);
    index += name.length;
    plainStart = index + 1;
  }
  if (plainStart < content.length) parts.push(content.slice(plainStart));
  return parts.length > 0 && parts.some((part) => typeof part !== "string") ? parts : null;
}

function ComposerPerch({ botId, sending, working, latestReplyId }: { botId: string; sending: boolean; working: boolean; latestReplyId?: string }) {
  const [config, setConfig] = useState(() => getBotAvatarConfig(botId));
  const cat = useRef<CatHandle>(null);
  const previousReply = useRef(latestReplyId);
  const sentSinceReply = useRef(false);
  useEffect(() => { setConfig(getBotAvatarConfig(botId)); return subscribeBotAvatar(botId, setConfig); }, [botId]);
  useEffect(() => { if (sending) { sentSinceReply.current = true; void cat.current?.play("look"); } }, [sending]);
  useEffect(() => { if (working) void cat.current?.play("work"); }, [working]);
  useEffect(() => {
    if (previousReply.current !== latestReplyId && latestReplyId && sentSinceReply.current) {
      sentSinceReply.current = false;
      void cat.current?.play("happy");
    }
    previousReply.current = latestReplyId;
  }, [latestReplyId]);
  return <span className="composer-perch" aria-hidden="true"><CatStage config={config} ref={cat} autoplay /></span>;
}

function Composer({ workStatus, activeRun, latestReplyId, onStopRun, avatarMention, conversation, bots, draft, onDraftChange, onSend, onSent, modelConfigured, onConnectModel, draftReady, readOnly = false }: { workStatus: React.ReactNode; activeRun?: Run; latestReplyId?: string; onStopRun: (id: string) => Promise<void>; avatarMention: { name: string; conversationId: string; nonce: number } | null; conversation: Conversation; bots: Bot[]; draft: ComposerDraft; onDraftChange: (update: (draft: ComposerDraft) => ComposerDraft) => void; onSend: (content: string, clientMessageId: string, attachmentIds?: string[], draftSignature?: string, flightOrigin?: DOMRect) => Promise<boolean>; onSent: () => void; modelConfigured: boolean; onConnectModel: () => void; draftReady: boolean; readOnly?: boolean }) {
  const { t } = useTranslation("chat");
  const [sending, setSending] = useState(false);
  const [fileError, setFileError] = useState("");
  const [uploadingKey, setUploadingKey] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dropOrigin = useRef<{ x: number; y: number; name: string } | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);
  const imageRef = useRef<HTMLInputElement | null>(null);
  const addMenuRef = useRef<HTMLDivElement | null>(null);
  const [addMenuOpen, setAddMenuOpen] = useState(false);
  const [mentionQuery, setMentionQuery] = useState<string | null>(null);
  const [mentionStart, setMentionStart] = useState(-1);
  const [mentionIndex, setMentionIndex] = useState(0);
  const mentionMenuRef = useRef<HTMLDivElement | null>(null);
  const mentionWrapRef = useRef<HTMLDivElement | null>(null);
  const [mentionAnchor, setMentionAnchor] = useState<{ left: number; bottom: number } | null>(null);
  const [mentionPill, setMentionPill] = useState<{ top: number; height: number } | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const conversationIdRef = useRef(conversation.id);
  const operationRef = useRef(0);
  const mentionBots = conversation.kind === "group" ? conversation.bot_ids.map((id) => bots.find((bot) => bot.id === id)).filter((bot): bot is Bot => Boolean(bot)) : bots.filter((bot) => !bot.archived && bot.id !== conversation.bot_id);
  const mentionMatches = mentionQuery === null ? [] : mentionBots.filter((bot) => bot.name.toLocaleLowerCase().startsWith(mentionQuery.toLocaleLowerCase()));
  const content = draft.content;
  const reply = draft.reply;
  const mentionParts = composerMentionParts(content, mentionBots.map((bot) => bot.name));
  const [composerScrollTop, setComposerScrollTop] = useState(0);
  const attachments = draft.attachments;
  const canCompose = modelConfigured && draftReady && !readOnly;
  useLayoutEffect(() => {
    const origin = dropOrigin.current;
    if (isDesktop || !origin) return;
    const form = textareaRef.current?.closest("form");
    const pill = Array.from(form?.querySelectorAll<HTMLElement>(".composer-attachment-pill") ?? []).find(node => node.dataset.name === origin.name);
    if (!pill) { dropOrigin.current = null; return; }
    dropOrigin.current = null;
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const to = pill.getBoundingClientRect();
    const ghost = pill.cloneNode(true) as HTMLElement;
    ghost.classList.add("web-drop-ghost");
    Object.assign(ghost.style, { left:`${to.left}px`, top:`${to.top}px`, width:`${to.width}px` });
    document.body.append(ghost);
    pill.style.visibility = "hidden";
    const dx = origin.x - to.left - to.width / 2;
    const dy = origin.y - to.top - to.height / 2;
    void ghost.animate([
      { transform:`translate(${dx}px,${dy}px) rotate(-8deg) scale(1.1)` },
      { transform:`translate(${dx * .45}px,${dy * .33 - 40}px) rotate(-4deg) scale(1.04)`, offset:.6 },
      { transform:"translate(0,0) scale(1.06,.9)", offset:.9 },
      { transform:"translate(0,0) scale(1)" },
    ], { duration:620, easing:"cubic-bezier(.45,0,.3,1)" }).finished.finally(() => { ghost.remove(); pill.style.visibility = ""; });
  }, [attachments]);
  useLayoutEffect(() => {
    if (isDesktop || mentionQuery === null || mentionStart < 0 || mentionMatches.length === 0) return;
    const textarea = textareaRef.current;
    const wrap = mentionWrapRef.current;
    if (!textarea || !wrap) return;
    const caret = textareaCaretRect(textarea, mentionStart);
    const bounds = wrap.getBoundingClientRect();
    setMentionAnchor({ left: Math.max(0, Math.min(caret.left - bounds.left - 12, bounds.width - 300)), bottom: Math.max(8, bounds.bottom - caret.top + 8) });
  }, [mentionQuery, mentionStart, mentionMatches.length, content, composerScrollTop]);
  useLayoutEffect(() => {
    if (isDesktop || mentionQuery === null) return;
    const button = mentionMenuRef.current?.querySelectorAll<HTMLElement>("button[role=option]")[mentionIndex];
    if (button) setMentionPill({ top: button.offsetTop, height: button.offsetHeight });
  }, [mentionQuery, mentionIndex, mentionMatches.length]);
  const dictation = useDictation({
    value: content,
    textareaRef,
    disabled: !canCompose || sending,
    onChange: (value) => { onDraftChange((current) => ({ ...current, content: value })); setMentionQuery(null); },
  });
  useEffect(() => { dictation.cancel(); }, [conversation.id, dictation.cancel]);
  useEffect(() => {
    if (!avatarMention || avatarMention.conversationId !== conversation.id || readOnly) return;
    const textarea = textareaRef.current;
    const start = textarea?.selectionStart ?? content.length;
    const end = textarea?.selectionEnd ?? start;
    const prefix = start > 0 && !/\s/.test(content[start - 1]) ? " " : "";
    const mention = `${prefix}@${avatarMention.name} `;
    onDraftChange(current => ({ ...current, content: current.content.slice(0, start) + mention + current.content.slice(end) }));
    setMentionQuery(null);
    const frame = requestAnimationFrame(() => {
      textarea?.focus();
      textarea?.setSelectionRange(start + mention.length, start + mention.length);
    });
    return () => cancelAnimationFrame(frame);
    // A request is consumed once, never replayed when switching conversations.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [avatarMention]);

  // Keep stale async sends from touching the newly selected conversation even
  // during the small render-to-effect window after a switch.
  conversationIdRef.current = conversation.id;
  useEffect(() => {
    conversationIdRef.current = conversation.id;
    operationRef.current += 1;
    setSending(false);
    setFileError("");
    setMentionQuery(null);
    setMentionIndex(0);
    setAddMenuOpen(false);
    return () => { operationRef.current += 1; };
  }, [conversation.id]);
  useEffect(() => {
    if (!addMenuOpen) return;
    const closeOutside = (event: PointerEvent) => {
      if (!addMenuRef.current?.contains(event.target as Node)) setAddMenuOpen(false);
    };
    const closeEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setAddMenuOpen(false);
    };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeEscape);
    };
  }, [addMenuOpen]);
  useEffect(() => {
    const shortcut = (event: KeyboardEvent) => {
      if (!canCompose || sending || !event.metaKey || !event.shiftKey) return;
      if (event.key.toLowerCase() === "i") { event.preventDefault(); imageRef.current?.click(); }
      if (event.key.toLowerCase() === "f") { event.preventDefault(); fileRef.current?.click(); }
    };
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, [canCompose, sending]);
  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    const height = Math.min(textarea.scrollHeight, 120);
    textarea.style.height = `${Math.max(48, height)}px`;
    textarea.style.overflowY = textarea.scrollHeight > 120 ? "auto" : "hidden";
  }, [content, conversation.id]);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if ((!content.trim() && !attachments.length) || !canCompose || sending || dictation.listening || dictation.busy) return;
    const conversationId = conversation.id;
    const operation = ++operationRef.current;
    const isCurrent = () => operationRef.current === operation && conversationIdRef.current === conversationId;
    // A reply carries the same leading marker bots use, so the quote renders and the model sees the target id.
    const draftContent = composerMessageContent(content, reply);
    const signature = composerMessageSignature(content, attachments, reply);
    const clientMessageId = composerClientMessageId(signature, draft.pending, messageId);
    onDraftChange((current) => ({ ...current, pending: { signature, id: clientMessageId } }));
    setSending(true);
    const attachmentIds: string[] = [];
    try {
      for (const attachment of attachments) {
        if (!isCurrent()) return;
        let id = attachment.id;
        if (!id) {
          if (!attachment.file) throw new Error(t("composer.reselect_file", { name: attachment.name }));
          setUploadingKey(attachment.key);
          id = (await api.uploadAttachment(conversationId, attachment.file)).id;
          if (isCurrent()) setUploadingKey(null);
          onDraftChange((current) => ({ ...current, attachments: current.attachments.map((item) => item.key === attachment.key ? { ...item, id } : item) }));
        }
        attachmentIds.push(id);
      }
    } catch (cause) {
      if (isCurrent()) { setFileError(errorText(cause)); setSending(false); setUploadingKey(null); }
      return;
    }
    if (!isCurrent()) return;
    const flightOrigin = !isDesktop ? textareaRef.current?.getBoundingClientRect() : undefined;
    const sent = await onSend(draftContent, clientMessageId, attachmentIds, signature, flightOrigin);
    if (!isCurrent()) return;
    // Workspace owns acknowledgement cleanup for the exact sent draft, even
    // across conversation switches. A newer edit/reply must survive this callback.
    if (sent) { setFileError(""); onSent(); }
    setSending(false);
  }
  function handleKeyDown(event: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === "Escape" && mentionQuery !== null) { event.preventDefault(); setMentionQuery(null); return; }
    if (mentionQuery !== null && mentionMatches.length > 0 && (event.key === "ArrowDown" || event.key === "ArrowUp")) { event.preventDefault(); setMentionIndex((current) => (current + (event.key === "ArrowDown" ? 1 : mentionMatches.length - 1)) % mentionMatches.length); return; }
    if (mentionQuery !== null && mentionMatches.length > 0 && event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) { event.preventDefault(); chooseMention(mentionMatches[mentionIndex]); return; }
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || event.keyCode === 229) return;
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  }
  function chooseMention(bot: Bot) {
    if (mentionStart < 0 || mentionQuery === null) return;
    const fromIndex = mentionMatches.findIndex(candidate => candidate.id === bot.id);
    const from = !isDesktop ? mentionMenuRef.current?.querySelectorAll<HTMLElement>("button[role=option] .mention-name")[fromIndex]?.getBoundingClientRect() : undefined;
    const textarea = textareaRef.current;
    const cursor = textarea?.selectionStart ?? content.length;
    const before = content.slice(0, mentionStart);
    const after = content.slice(cursor);
    const next = `${before}@${bot.name} ${after}`;
    onDraftChange((current) => ({ ...current, content: next }));
    setMentionQuery(null); setMentionIndex(0);
    requestAnimationFrame(() => {
      const position = before.length + bot.name.length + 2;
      textareaRef.current?.setSelectionRange(position, position); textareaRef.current?.focus();
      const chip = Array.from(mentionWrapRef.current?.querySelectorAll<HTMLElement>(".composer-at-chip") ?? []).find(node => node.textContent === `@${bot.name}`);
      if (!from || !chip || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
      const to = chip.getBoundingClientRect();
      const ghost = document.createElement("span");
      ghost.className = "web-mention-ghost";
      ghost.textContent = `@${bot.name}`;
      Object.assign(ghost.style, { left: `${from.left}px`, top: `${from.top}px` });
      document.body.append(ghost);
      void ghost.animate([
        { transform: "translate(0,0) scale(.9)", opacity: 1 },
        { transform: `translate(${(to.left - from.left) * .5}px, ${Math.min(from.top,to.top) - from.top - 38}px) scale(1.08)`, opacity: 1, offset:.45 },
        { transform: `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(1)`, opacity: 0 },
      ], { duration:520, easing:"cubic-bezier(.3,.7,.3,1)" }).finished.finally(() => ghost.remove());
    });
  }
  function updateContent(value: string, cursor: number) {
    onDraftChange((current) => ({ ...current, content: value }));
    const prefix = value.slice(0, cursor); const match = prefix.match(/(?:^|\s)@([^\s@]*)$/);
    if (!match) { setMentionQuery(null); return; }
    setMentionStart(cursor - match[1].length - 1); setMentionQuery(match[1]); setMentionIndex(0);
  }
  function addFiles(list: FileList | File[] | null) {
    if (!list || sending || !canCompose) return;
    setFileError("");
    const next = [...attachments];
    for (const file of Array.from(list)) {
      if (file.size > 20 * 1024 * 1024) { setFileError(t("composer.too_large", { name: file.name })); continue; }
      const placeholder = next.findIndex((attachment) => !attachment.file && !attachment.id && attachment.name === file.name && attachment.size === file.size && attachment.lastModified === file.lastModified);
      if (placeholder < 0 && next.length >= 8) { setFileError(t("composer.max_files")); break; }
      const replacement = { key: placeholder >= 0 ? next[placeholder].key : messageId(), file, name: file.name, size: file.size, lastModified: file.lastModified, type: file.type };
      if (placeholder >= 0) next[placeholder] = replacement;
      else next.push(replacement);
    }
    onDraftChange((current) => ({ ...current, attachments: next }));
  }
  async function captureScreen() {
    setAddMenuOpen(false);
    if (!navigator.mediaDevices?.getDisplayMedia) {
      setFileError(t("composer.screenshot_unsupported"));
      return;
    }
    let stream: MediaStream | undefined;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
      const video = document.createElement("video");
      video.srcObject = stream;
      await video.play();
      if (!video.videoWidth || !video.videoHeight) throw new Error(t("composer.screenshot_read_failed"));
      const canvas = document.createElement("canvas");
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      canvas.getContext("2d")?.drawImage(video, 0, 0);
      const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/png"));
      if (!blob) throw new Error(t("composer.screenshot_failed"));
      addFiles([new File([blob], t("composer.screenshot_name", { time: new Date().toISOString().replace(/[:.]/g, "-") }), { type: "image/png" })]);
    } catch (cause) {
      if ((cause as DOMException)?.name !== "NotAllowedError") setFileError(errorText(cause));
    } finally {
      stream?.getTracks().forEach((track) => track.stop());
    }
  }
  useEffect(() => {
    const form = textareaRef.current?.closest("form");
    // Measured on the workspace so the floating desktop, mounted outside the chat pane, docks above the composer too.
    const pane = form?.closest<HTMLElement>(".workspace") ?? form?.closest<HTMLElement>(".chat-pane");
    if (!form || !pane) return;
    const measure = () => pane.style.setProperty("--composer-height", `${form.getBoundingClientRect().height}px`);
    const observer = new ResizeObserver(measure);
    observer.observe(form); measure();
    return () => { observer.disconnect(); pane.style.removeProperty("--composer-height"); };
  }, [conversation.id]);
  useEffect(() => {
    if (!isDesktop) return;
    const pane = textareaRef.current?.closest(".chat-pane");
    if (!pane) return;
    const over = (event: Event) => { const e = event as DragEvent; if (e.dataTransfer?.types.includes("Files")) { e.preventDefault(); setDragging(true); } };
    const leave = (event: Event) => { if (!pane.contains((event as DragEvent).relatedTarget as Node)) setDragging(false); };
    const drop = (event: Event) => { const e = event as DragEvent; setDragging(false); if (e.dataTransfer?.files.length) { e.preventDefault(); if (!isDesktop) dropOrigin.current = { x:e.clientX, y:e.clientY, name:e.dataTransfer.files[0].name }; addFiles(e.dataTransfer.files); textareaRef.current?.focus(); } };
    pane.addEventListener("dragover", over); pane.addEventListener("dragleave", leave); pane.addEventListener("drop", drop);
    return () => { pane.removeEventListener("dragover", over); pane.removeEventListener("dragleave", leave); pane.removeEventListener("drop", drop); };
  });
  return <form className={`composer${dragging ? " is-dragging" : ""}${!modelConfigured ? " is-no-model" : ""}${!content.trim() && !attachments.length && !sending ? " is-send-empty" : ""}`} onSubmit={(event) => void submit(event)}>{readOnly ? <div className="composer-tools"><span className="config-warning">{t("composer.archived")}</span></div> : !draftReady ? <DelayedFeedback><div className="composer-tools"><span className="config-warning">{t("composer.restoring_draft")}</span></div></DelayedFeedback> : !modelConfigured && <ModelBanner botId={conversation.kind === "dm" ? conversation.bot_id || conversation.bot_ids[0] : undefined} name={conversation.name} onConnect={onConnectModel} />}{dictation.permissionNeeded && <PermissionCoach permissions={["microphone"]} force onDismiss={dictation.clearPermission} />}{!isDesktop && dictation.starting && <div className="dictation-status is-starting" role="status">{t("composer.mic_connecting")}</div>}{dictation.error && !dictation.permissionNeeded && <div className="dictation-status" role="status">{dictation.error}</div>}<input ref={imageRef} type="file" accept="image/*" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} /><input ref={fileRef} type="file" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} />{workStatus}{!isDesktop && <WebFileDropOverlay key={conversation.id} inputRef={textareaRef} enabled={canCompose && !sending && !dictation.listening && !dictation.busy} onDrop={(files, origin) => { dropOrigin.current = { ...origin, name:files[0].name }; addFiles(files); }} />}<div className="composer-row">{!isDesktop && uploadingKey && <span className="web-upload-ring" aria-hidden="true" />}{!isDesktop && !readOnly && conversation.kind === "dm" && (conversation.bot_id || conversation.bot_ids[0]) && <ComposerPerch key={conversation.id} botId={conversation.bot_id || conversation.bot_ids[0]} sending={sending} working={activeRun?.status === "running"} latestReplyId={latestReplyId} />}{!(dictation.listening || dictation.busy) && <div className="composer-add-wrap" ref={addMenuRef}><button type="button" className="secondary-button" aria-label={t("composer.add_attachment")} aria-expanded={addMenuOpen} aria-haspopup="menu" disabled={sending || !canCompose} onClick={() => setAddMenuOpen((open) => !open)}><Icon name="plus" size={22} /></button>{addMenuOpen && <div className="composer-add-menu" role="menu"><button type="button" role="menuitem" onClick={() => { setAddMenuOpen(false); imageRef.current?.click(); }}><Icon className="composer-add-icon" name="image" size={20} />{t("composer.image")}<kbd>⌘⇧I</kbd></button><button type="button" role="menuitem" onClick={() => { setAddMenuOpen(false); fileRef.current?.click(); }}><Icon className="composer-add-icon" name="file" size={20} />{t("composer.file")}<kbd>⌘⇧F</kbd></button><button type="button" role="menuitem" onClick={() => void captureScreen()}><span className="composer-add-icon"><ComposerGlyph name="screenshot" size={20} /></span>{t("composer.screenshot")}</button><p>{t("composer.drop_hint")}</p></div>}</div>}<div ref={mentionWrapRef} className={`composer-input-wrap${mentionParts ? " has-mentions" : ""}`}>{reply && !readOnly && <div className="composer-reply" role="status"><Icon name="reply" size={15} /><span className="composer-reply-copy"><strong>{t("composer.replying_to", { name: reply.name })}</strong><span>{reply.excerpt}</span></span><button type="button" aria-label={t("composer.cancel_reply")} data-hint={t("composer.cancel_reply")} onClick={() => { onDraftChange(current => ({ ...current, reply: undefined })); textareaRef.current?.focus(); }}><Icon name="close" size={14} /></button></div>}{attachments.length > 0 && <div className="composer-attachments">{attachments.map((attachment) => <span key={attachment.key} className="composer-attachment-pill" data-name={attachment.name}><span className="composer-attachment-type" aria-hidden="true">{attachment.type.startsWith("image/") ? "IMG" : (attachment.name.split(".").pop() || "FILE").slice(0, 4).toUpperCase()}</span><span className="composer-attachment-name">{attachment.name}</span>{uploadingKey === attachment.key && ` · ${t("composer.uploading")}`}{!attachment.file && !attachment.id && ` · ${t("composer.reselect")}`}<button type="button" aria-label={t("composer.remove_attachment", { name: attachment.name })} disabled={sending || !canCompose || dictation.listening || dictation.busy} onClick={() => onDraftChange((current) => ({ ...current, attachments: current.attachments.filter((item) => item.key !== attachment.key) }))}><Icon name="close" size={18} /></button></span>)}</div>}{mentionParts && <div className="composer-rich-text" aria-hidden="true"><div style={{ transform:`translateY(-${composerScrollTop}px)` }}>{mentionParts}</div></div>}<textarea ref={textareaRef} aria-label={t("composer.input_label", { name: conversation.name })} value={content} onPaste={event => { const files = Array.from(event.clipboardData.items).filter(item => item.kind === "file").map(item => item.getAsFile()).filter((file): file is File => file !== null); if (files.length) { event.preventDefault(); addFiles(files); } }} onKeyDown={handleKeyDown} onScroll={(event) => setComposerScrollTop(event.currentTarget.scrollTop)} onChange={(event) => updateContent(event.target.value, event.target.selectionStart)} placeholder={dictation.listening ? t("composer.placeholder_recording") : dictation.busy ? t("composer.placeholder_transcribing") : readOnly ? t("composer.placeholder_archived") : !draftReady ? t("composer.placeholder", { name: conversation.name }) : canCompose ? t("composer.placeholder", { name: conversation.name }) : t("composer.placeholder_no_model")} disabled={!canCompose || sending || dictation.listening || dictation.busy} rows={1} />{mentionQuery !== null && mentionMatches.length > 0 && <div ref={mentionMenuRef} className="mention-menu" role="listbox" aria-label={isDesktop ? undefined : t("composer.mention_label", { name: conversation.name })} style={!isDesktop && mentionAnchor ? { left:mentionAnchor.left, bottom:mentionAnchor.bottom } : undefined}>{!isDesktop && <div className="mention-menu-heading" role="presentation">{t("composer.mention_heading", { name: conversation.name })}</div>}{!isDesktop && mentionPill && <span className="web-mention-pill" aria-hidden="true" style={{ top:mentionPill.top, height:mentionPill.height }} />}{mentionMatches.map((bot, index) => <button type="button" role="option" aria-selected={index === mentionIndex} className={index === mentionIndex ? "selected" : ""} key={bot.id} onMouseEnter={() => setMentionIndex(index)} onMouseDown={(event) => { event.preventDefault(); chooseMention(bot); }}><BotAvatar id={bot.id} mini /><span className="mention-name">{mentionQuery && bot.name.toLocaleLowerCase().startsWith(mentionQuery.toLocaleLowerCase()) ? <><mark>{bot.name.slice(0, mentionQuery.length)}</mark>{bot.name.slice(mentionQuery.length)}</> : bot.name}</span></button>)}{!isDesktop && <div className="mention-menu-hint" role="presentation">{t("composer.mention_hint")}</div>}</div>}</div>{dictation.listening || dictation.busy ? <DictationControls elapsed={dictation.elapsed} levels={dictation.levels} busy={dictation.busy} onCancel={dictation.cancel} onConfirm={dictation.confirm} /> : <>{dictation.supported && <button type="button" className={`dictation-button${!isDesktop && dictation.starting ? " is-starting" : ""}`} aria-label={!isDesktop && dictation.starting ? t("composer.cancel_mic") : t("composer.dictate")} title={!isDesktop && dictation.starting ? t("composer.cancel_mic") : dictation.notice || t("composer.dictate")} disabled={!dictation.starting && (sending || !canCompose)} onClick={!isDesktop && dictation.starting ? dictation.cancel : dictation.start}><ComposerGlyph name="mic" size={20} /></button>}{activeRun && !readOnly && !content.trim() && !attachments.length && <button type="button" className="composer-stop" aria-label={t("composer.stop_label", { name: bots.find(bot => bot.id === activeRun.bot_id)?.name ?? "Bot" })} disabled={!canCompose} onClick={() => void onStopRun(activeRun.id)}><Icon name="stop" size={16} variant="filled" />{t("composer.stop")}</button>}{(Boolean(content.trim()) || attachments.length > 0 || sending) && <button className="send-button" aria-label={t("composer.send")} disabled={(!content.trim() && !attachments.length) || !canCompose || sending || dictation.listening || dictation.busy}>{sending ? "…" : <Icon name="arrow-up" size={26} variant="filled" />}</button>}</>}</div>{fileError && <div className="error-text" role="alert">{fileError}</div>}</form>;
}
function BotPanel({ bots, activeBot, onExportData, onClose, onUpdate, onCreateGroup, onOpenWork, onOpenMemory, memories = [], refreshToken = 0 }: { onExportData?: (id: string) => void; onOpenWork?: () => void; onOpenMemory?: () => void; memories?: Memory[]; refreshToken?: number; bots: Bot[]; activeBot?: Bot; onClose: () => void; onUpdate: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => Promise<void>; onCreateGroup: (name: string, botIds: string[]) => Promise<void> }) {
  const { t } = useTranslation("chat");
  const mode = activeBot ? "bot" : "group";
  const [view, setView] = useState<"home" | "appearance" | "config">("home");
  const [name, setName] = useState(activeBot?.name ?? "");
  const [instructions, setInstructions] = useState(activeBot?.instructions ?? "");
  const [model, setModel] = useState(activeBot?.model ?? "");
  const [effort, setEffort] = useState(activeBot?.reasoning_effort ?? "");
  const [avatarConfig, setAvatarConfig] = useState(() => activeBot ? getBotAvatarConfig(activeBot.id) : undefined);
  const [members, setMembers] = useState<string[]>([]);
  useEffect(() => {
    const available = new Set(bots.map(bot => bot.id));
    setMembers(current => current.every(id => available.has(id)) ? current : current.filter(id => available.has(id)));
  }, [bots]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const valid = Boolean(name.trim()) && (mode === "bot" || members.length >= 2 && members.length <= 8);
  async function submit(event: React.FormEvent) {
    event.preventDefault(); if (!valid || saving) return;
    setSaving(true); setError("");
    try {
      if (mode === "group") await onCreateGroup(name.trim(), members);
      else if (activeBot) await onUpdate(activeBot.id, { name: name.trim(), instructions, model, reasoning_effort: effort });
    } catch (cause) { setError(errorText(cause)); }
    finally { setSaving(false); }
  }
  if (activeBot) {
    const role = instructions.trim().split(/[\n\u3002\uFF01\uFF1F.!?]/)[0]?.trim();
    // "default" is a stored sentinel; show what the Bot runs with now.
    const follows = followsGlobal(activeBot.model);
    const modelLine = follows ? (activeBot.effective_model ? `${t("botPanel.follows_global")} · ${activeBot.effective_model}` : t("botPanel.follows_global")) : activeBot.model;
    const shownEffort = activeBot.effective_reasoning_effort || (activeBot.reasoning_effort !== "default" ? activeBot.reasoning_effort : "");
    const effortLabel = shownEffort ? ` · ${shownEffort}` : "";
    const latestMemoryDisplay = memories.length ? memoryDisplay(memories.at(-1)) : undefined;
    const latestMemory = latestMemoryDisplay ? `${latestMemoryDisplay.title} · ${latestMemoryDisplay.description}` : undefined;
    return <div className="detail-content bot-v2-panel">
      <div className="detail-heading bot-v2-heading">
        {view === "home" ? <h2>{t("botPanel.title")}</h2> : <button type="button" className="bot-v2-back" onClick={() => setView("home")}><Icon name="arrow-left" size={18}/> {t("botPanel.back")}</button>}
        <button className="close-button" aria-label={t("botPanel.close")} data-hint={t("botPanel.close_hint")} onClick={onClose}><Icon name="close" size={18} /></button>
      </div>
      {view === "home" ? <>
        {/* One entry per destination: the cat opens its style, each row opens one page. */}
        <div className="bot-v2-profile">
          <button type="button" className="bot-v2-hero" onClick={() => setView("appearance")} aria-label={t("botPanel.change_style_label", { name: activeBot.name })} data-hint={t("botPanel.change_style")}>
            <span className="bot-v2-cat"><BotAvatar id={activeBot.id} config={avatarConfig} motion="awake" /></span>
            <span className="bot-v2-change" aria-hidden="true"><Icon name="edit" size={13}/></span>
          </button>
          <div className="bot-v2-identity"><h2>{activeBot.name}</h2>{role && <p>{role}</p>}<span className="bot-v2-model">{modelLine}{effortLabel}</span></div>
        </div>
        <nav className="bot-v2-rows" aria-label={t("botPanel.title")}>
          {onOpenWork && <WorkPreview disabled={saving} scope={{type:"bots",id:activeBot.id}} refreshToken={refreshToken} onOpen={onOpenWork} leading={<span className="bot-v2-row-icon" aria-hidden="true"><Icon name="checklist" size={18}/></span>}/>}
          {onOpenMemory && <button type="button" className="bot-v2-row" onClick={onOpenMemory}><span className="bot-v2-row-icon" aria-hidden="true"><Icon name="memory" size={18}/></span><span className="bot-v2-row-copy"><strong>{t("botPanel.memory")} <span>{memories.length}</span></strong><small>{latestMemory ?? t("botPanel.no_memory")}</small></span><Icon name="chevron-right" size={16}/></button>}
          <button type="button" className="bot-v2-row" onClick={() => setView("config")}><span className="bot-v2-row-icon" aria-hidden="true"><Icon name="bot-config" size={18}/></span><span className="bot-v2-row-copy"><strong>{t("botPanel.config")}</strong><small>{role || t("botPanel.no_role")}</small></span><Icon name="chevron-right" size={16}/></button>
        </nav>
        <div className="bot-v2-footer"><button type="button" className="text-button" onClick={() => downloadBotPackage(buildBotPackage(activeBot))}><Icon name="share" size={15}/> {t("botPanel.export_share")}</button><button type="button" className="text-button" onClick={() => onExportData?.(activeBot.id)}>{t("botPanel.export_data")}</button></div>
      </> : view === "appearance" ? <><div className="bot-v2-subtitle"><h2>{t("botPanel.style")}</h2><p>{t("botPanel.style_hint")}</p></div><BotAvatarPicker botId={activeBot.id} config={avatarConfig!} onChange={(next) => setAvatarConfig(saveBotAvatarConfig(activeBot.id, next))}/><button type="button" className="primary-button bot-v2-done" onClick={() => setView("home")}>{t("botPanel.done")}</button></> : <><div className="bot-v2-subtitle"><h2>{t("botPanel.config")}</h2><p>{t("botPanel.config_hint")}</p></div><form className="detail-form" aria-busy={saving} onSubmit={(event) => void submit(event)}><fieldset className="form-fields" disabled={saving}><label>{t("botPanel.name")}<input value={name} maxLength={200} onChange={(event) => setName(event.target.value)}/></label><label>{t("botPanel.instructions")}<textarea value={instructions} onChange={(event) => setInstructions(event.target.value)} placeholder={t("botPanel.instructions_placeholder")} rows={6}/></label><Disclosure title={t("botPanel.model_section")}><ModelFields disabled={saving} model={model} effort={effort} onChange={(next,level)=>{setModel(next);setEffort(level)}}/></Disclosure>{error && <p className="error-text" role="alert">{error}</p>}<button className="primary-button" disabled={!valid || saving}>{saving ? t("action.saving") : t("botPanel.save")}</button></fieldset></form><BotInspector botId={activeBot.id}/></>}
    </div>;
  }
  return <div className="detail-content">
    <div className="detail-heading"><h2>{t("create.new_group")}</h2><button className="close-button" aria-label={t("groupCreate.close")} disabled={saving} onClick={onClose}><Icon name="close" size={18} /></button></div>
    <form className="detail-form" aria-busy={saving} onSubmit={(event) => void submit(event)}>
      <fieldset className="form-fields" disabled={saving}>
        <label>{t("botPanel.name")}<input value={name} maxLength={200} onChange={(event) => setName(event.target.value)} placeholder={t("members.name")} /></label>
          <div className="members-manage-label"><span>{t("members.members")}</span><span className="muted small-text">{members.length}/8</span></div>
          <div className="member-picker create-member-picker">{bots.map((bot) => <label key={bot.id} className={`member-option ${members.includes(bot.id) ? "selected" : ""}`}>
            <BotAvatar id={bot.id} mini /><span>{bot.name}</span><input type="checkbox" checked={members.includes(bot.id)} disabled={!members.includes(bot.id) && members.length >= 8} onChange={() => setMembers((current) => current.includes(bot.id) ? current.filter((id) => id !== bot.id) : [...current, bot.id])} />
          </label>)}</div><p className="field-note">{t("members.pick_hint")}</p>
        {error && <p className="error-text" role="alert">{error}</p>}
        <button className="primary-button" disabled={!valid || saving}>{saving ? t("action.saving") : t("groupCreate.create")}</button>
      </fieldset>
    </form>
  </div>;
}


export default App;
export { WorkingMembers, Composer, DictationControls, WebToolActivityRun };

export function NotificationSetting() {
  const { t } = useTranslation("chat");
  const [permission, setPermission] = useState("Notification" in window ? Notification.permission : "unsupported");
  return <div className="notification-setting"><strong>{t("notifications.title")}</strong><p className="field-note">{t("notifications.description")}</p>{permission === "default" ? <button className="secondary-button" onClick={() => void Notification.requestPermission().then(setPermission)}>{t("notifications.enable")}</button> : <p className="notification-status" role="status">{permission === "granted" ? t("notifications.on") : permission === "unsupported" ? t("notifications.unsupported") : t("notifications.off")}</p>}</div>;
}
