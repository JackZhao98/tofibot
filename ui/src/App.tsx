import { useGraceFlag } from "./useGraceFlag";
import { readReplyMarker } from "./replyMarker";
import { thinkingHeadline } from "./thinkingHeadline";
import { TaskRunBlock, taskBlockVisible } from "./TaskRunBlock";
import { LiveMark } from "./LiveMark";
import { markSenderGroups } from "./senderGroups";
import { sameData } from "./sameData";
import { buildTaskOwners, presentTaskIssue, taskPhaseLabel, type TaskOwner } from "./taskIssuePresentation";
import { i18n, useTranslation } from "./i18n";
import { mergeMessageTimeline, precedingBlockBotIds, previousRenderedIndex } from "./messageTimeline";
import { textareaCaretRect } from "./textareaCaret";
import { DelayedFeedback } from "./DelayedFeedback";
import { BootScreen } from "./BootScreen";
import { WebFileDropOverlay } from "./WebFileDropOverlay";
import { SidebarAccount } from "./OwnerSession";
import { desktopState, updateDesktopState, isDesktop, type DesktopCommand } from "./desktop";
import {TimezoneProvider, useUserTimezone} from "./UserTimezone";
import {dateInTimezone, dateTimeFormat, formatZonedTime} from "./timezone";
import {AttachmentList} from "./MessageAttachment";
import {FilePreviewHost} from "./FileViewer";
import {MessageReactions} from "./MessageReactions";
import { applyUserReaction } from "./reactionState";
import { clearMessageActions, setMessageActions, tappedMessageActionsId, useMessageActionsOpen } from "./messageActionsStore";
import { CopyFeedbackIcon } from "./CopyFeedbackIcon";
import { attachmentUrl } from "./filePreviewModel";
import { DisplayCard } from "./DisplayCard";
import { useQuestions } from "./useQuestions";
import { toolDisplayState, toolDisplayLabel } from "./toolTimeline";
import { requestFullToolResult, setToolResultLoader } from "./toolResultLoader";
import { taskStateLabel } from "./ConversationTaskStatus";
import { MailDraftCard, useMailDrafts } from "./MailDraftCard";
import { buildQuestionTimeline, compareQuestionTime } from "./questionTimeline";
import { useDebugMode } from "./debugMode";
import type { SettingsEntry, SettingsTab, SettingsView } from "./SettingsShell";
import { AfterPaint, ArchivePanel, UpdateBanner, PermissionCoach, BotPanelSkeleton, BotDesktopPanel, DeleteConversationDialog, ReassignPrimaryDialog, RememberedApprovalsSheet, BotPanel, MembersPanel, DesktopSkeleton, MemoryPanel, TeamBoard, ViewOnlyChat, WorkPanel, Onboarding, QuestionCard, RunContextView, preloadPanel, SettingsPanel, SettingsSkeleton, warmPanelsWhenIdle } from "./lazyPanels";
import { subscribeSettingsDeepLinks } from "./settings/deepLinks";
import { BotIdentityCard } from "./BotIdentityCard";
import { arrowOpens, OverlayMenu, OverlaySlot, SlotView } from "./overlays/OverlayHost";
import { ActionHints, ConfirmAction, useAppearance, useSurfacePresence } from "./InteractionSystem";
import { useConversationScroll } from "./useConversationScroll";
import { conversationPath, readConversationRoute } from "./conversationRoute";
import { useConversationNavigation } from "./useConversationNavigation";
import { FloatingDesktop } from "./FloatingDesktop";
import { useDesktopPresence } from "./desktopPresence";
import type { DeleteTarget } from "./DeleteConversationDialog";
import { PrimaryBadge, otherActiveBots } from "./PrimaryAssistant";
import type { ViewOnlyChatTarget } from "./ViewOnlyChat";
import { ScheduledTaskRow } from "./ScheduledTaskRow";
import { occurrenceRootKey, type ScheduleOccurrence } from "./scheduleOccurrences";
import { useScheduleOccurrences } from "./useScheduleOccurrences";
import { useScheduleMetadata } from "./useScheduleMetadata";
import type { Schedule } from "./types";
import { TofiIcon as Icon, type TofiIconName } from "./icons";
import { ComposerGlyph } from "./ComposerGlyph";
import { formatRelativeTime, intlLocale } from "./i18n/format";
import { BrandLogo } from "./BrandLogo";
import { PinDock, PinDropZone, PinTile } from "./PinnedDock";
import { applyLayout, fillSlots, keepLocalLayout, listOrder, pinnedOf, type SidebarIds } from "./sidebarOrder";
import { useSidebarDnd, type DndAnnounce, type DndCommit } from "./useSidebarDnd";
import { usePhoneSwipeBack } from "./usePhoneSwipeBack";
import { createContext, lazy, memo, startTransition, Suspense, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal, flushSync } from "react-dom";
import { errorText } from "./errorText";
import { api, ApiError, openConversationEvents, openWorkspaceEvents } from "./api";
import { MessageMarkdown } from "./MessageMarkdown";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { holdAvatarLife } from "./lib/tofi-avatar/index.js";
import { CatStage, type CatHandle } from "./CatStage";
import { CATS, EmptyCat, EmptyState } from "./EmptyCat";
import { FirstRunHero } from "./FirstRunHero";
import { FinishSetupChip, ModelBanner } from "./OnboardingChrome";
import { chipStep, onboardingMode, parseOnboardingState, resumeStep, type OnboardingState, type OnboardingStep } from "./onboardingFlow";
import { LoadingCat } from "./motion-lab/lib/LoadingCat";
import { flipList } from "./motion-lab/lib/flip";
import { flySentMessage } from "./sendFlight";
import { SendReceipt, SendReceiptMark } from "./SendReceipt";
import { receiptFor, runsByTrigger } from "./sendReceipts";
import { activeBotRuns, botRecentlyActive, latestBotWorkTime, latestHumanMessageTime } from "./botActivity";
import { reconcileToolActivity, activeToolForRun, buildToolRunAnchors, buildToolSummaryAnchors, buildToolTimeline, compareToolActivities, elapsedToolSeconds, orderToolActivities, toolActionLabel, toolArgumentPreview, toolAttemptIssues } from "./toolTimeline";
import { foldCompletedProgress, isMidRunKind } from "./runProgress";
import { mergeRunUpdates } from "./runMerge";
import { draftDeltaApplies } from "./draftDelta";
import { DraftStream, useLiveDraft } from "./draftStream";
import { rowModelEqual } from "./timelineRow";
import { layerRoot } from "./layers";
import { WorkspaceToast } from "./WorkspaceToast";
import { shouldShowReplyQuote } from "./replyQuote";
import { buildRetryFamilies, indexMessages, isTerminalRun, retryFamilyAnchor } from "./runFamily";
import { composerClientMessageId, composerDraftMatchesMessage, composerMessageContent, composerMessageSignature, type ComposerReply } from "./composerDraft";
import { useDictation } from "./useDictation";
import { useSmoothStreamText } from "./useSmoothStreamText";
import { observeVisualViewport, visualViewportMetrics } from "./visualViewport";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import type { Attachment, Bot, ComputerHealth, Config, Conversation, EventEnvelope, Memory, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary, WorkspaceEventEnvelope } from "./types";
import { MOTION, EASE } from "./motion";

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

/**
 * One stable identity for a person's message from the moment it is typed until it is history. The optimistic copy and
 * the server's copy carry the same client_message_id, so React keeps one DOM node across the swap and nothing replays.
 */
function blockKey(message: Message) {
  return message.role === "user" && message.client_message_id ? `cm:${message.client_message_id}` : message.id;
}

/** The server's copy of a sent message replaces its optimistic one wherever either arrives first. */
function reconcileSent(list: Message[], server: Message): Message[] {
  const client = server.client_message_id;
  const rest = list.filter(item => item.id !== server.id && !(client && item.local && item.client_message_id === client));
  return [...rest, server].sort((a, b) => a.seq - b.seq);
}

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
  return dateTimeFormat(intlLocale(), { ...(sameDay ? {} : { month: "numeric", day: "numeric" }), hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}

/** The bar's quiet timestamp: relative while it is recent, the clock time once it is old. The exact time is the tooltip. */
function formatBarTime(value: string, timeZone: string, justNow: string) {
  const age = Date.now() - new Date(value).getTime();
  if (!Number.isFinite(age)) return formatHoverTime(value, timeZone);
  if (age < 45_000) return justNow;
  return age < 7 * 24 * 3600_000 ? formatRelativeTime(value) : formatHoverTime(value, timeZone);
}

/** What Copy puts on the clipboard: the message as the reader sees it, without the sender context label or reply marker. */
function copyableMessageText(message: Message) {
  const body = readReplyMarker(message.content.replace(/^\[sender [^\]\n]*? id=[^\]\n]+\]\s*/, "")).body.trim();
  return body.replace(/\]\(attachment:([^)\s]+)\)/g, (_, id) => `](${location.origin}${attachmentUrl(id)})`);
}
async function writeClipboard(text: string) {
  try { await navigator.clipboard.writeText(text); return true; } catch { /* LAN HTTP and blocked permissions have no async clipboard */ }
  const field = document.createElement("textarea");
  field.value = text; field.setAttribute("readonly", ""); field.style.cssText = "position:fixed;top:0;left:0;opacity:0";
  document.body.appendChild(field); field.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch { ok = false; }
  field.remove();
  return ok;
}

/** Browser-mode message actions, mounted only for the message the pointer, focus or a tap is on. */
const MessageHoverActions = memo(function MessageHoverActions({ message, timezone, onReact, onReply, onActivity, activityOpen, copyText, mark }: { message: Message; timezone: string; onReact: (anchor: DOMRect) => void; onReply?: () => void; onActivity?: () => void; activityOpen?: boolean; copyText?: string; mark?: { state: "delivered" | "read"; names: string[]; group: boolean } }) {
  const { t } = useTranslation("chat");
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(copiedTimer.current), []);
  async function copy() {
    if (!copyText || !await writeClipboard(copyText)) return;
    setCopied(true);
    window.clearTimeout(copiedTimer.current);
    copiedTimer.current = window.setTimeout(() => setCopied(false), 1200);
  }
  // One tab stop per bar (the first button); arrows, Home and End move inside it, as the toolbar role promises.
  function onKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const keys = ["ArrowLeft", "ArrowRight", "Home", "End"];
    if (!keys.includes(event.key)) return;
    const buttons = [...event.currentTarget.querySelectorAll<HTMLElement>("button, [data-roving]")];
    const at = buttons.indexOf(document.activeElement as HTMLElement);
    if (at < 0) return;
    event.preventDefault();
    const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (at + (event.key === "ArrowRight" ? 1 : -1) + buttons.length) % buttons.length;
    buttons.forEach((button, index) => { button.tabIndex = index === next ? 0 : -1; });
    buttons[next].focus();
  }
  let first = true;
  const stop = () => { const index = first ? 0 : -1; first = false; return index; };
  return <div className="message-hover-actions" role="toolbar" aria-label={t("hover.toolbar")} onKeyDown={onKeyDown}>
    {copyText && <button type="button" tabIndex={stop()} aria-label={copied ? t("hover.copied") : t("hover.copy")} data-hint={copied ? t("hover.copied") : t("hover.copy")} data-action="copy" onClick={() => void copy()}><CopyFeedbackIcon copied={copied} size={15} /></button>}
    <button type="button" tabIndex={stop()} aria-label={t("hover.react")} data-hint={t("hover.react")} data-action="react" onClick={event => onReact(event.currentTarget.getBoundingClientRect())}><Icon name="emoji" size={15} /></button>
    {onReply && <button type="button" tabIndex={stop()} aria-label={t("hover.reply")} data-hint={t("hover.reply")} data-action="reply" onClick={onReply}><Icon name="reply" size={15} /></button>}
    {onActivity && <button type="button" tabIndex={stop()} className="message-activity-action" aria-label={activityOpen ? t("hover.hide_activity") : t("hover.view_activity")} aria-pressed={activityOpen} data-hint={activityOpen ? t("hover.hide_activity") : t("hover.view_activity")} data-action="activity" onClick={onActivity}><Icon name="activity" size={15} /></button>}
    <time dateTime={message.created_at} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}>{formatBarTime(message.created_at, timezone, t("hover.just_now"))}</time>
    {mark && <SendReceiptMark state={mark.state} names={mark.names} group={mark.group} />}
  </div>;
}, (previous, next) => previous.message === next.message && previous.timezone === next.timezone && previous.activityOpen === next.activityOpen && previous.copyText === next.copyText
  && previous.mark?.state === next.mark?.state && previous.mark?.group === next.mark?.group && previous.mark?.names.join("\u0000") === next.mark?.names.join("\u0000")
  // The handlers are rebuilt every render; they act on the same message, so only their presence matters here.
  && Boolean(previous.onReply) === Boolean(next.onReply) && Boolean(previous.onActivity) === Boolean(next.onActivity));

/** Holds a message's action bar and mounts it only while that message is the active one. */
function MessageActionsSlot({ id, children }: { id: string; children: React.ReactNode }) {
  return useMessageActionsOpen(id) ? <>{children}</> : null;
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
/** Whether chat shows Activity rows, and which runs were opened one by one from a message's action menu. */
const ActivityViewContext = createContext<{ show: boolean; revealed: ReadonlySet<string>; /** Opens one run's steps from its live line (adds it to `revealed`, never removes). */ reveal: (runId: string) => void }>({ show: false, revealed: new Set(), reveal: () => {} });
/** The run whose steps a live line just opened: its toggle takes focus where the line was. */
let focusToggleOfRun = "";

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
/** Avatar + name that opens a sender group in a group chat (hidden by CSS when the unit continues the previous one). */
function SenderHead({ id, name }: { id: string; name: string }) {
  return <div className="sender-head"><div className="sender-avatar"><Avatar label={name} id={id} mini /></div><span className="sender-name">{name}</span></div>;
}

/** The one compact process line of a working run: loading mark, "Browsing the web · 10s". Part of the Bot's sender group; a button when the run's steps can be opened from it. */
function CompactLiveLine({ label, botId, botName, at, showName, ariaLabel, onOpen }: { label: string; botId: string; botName: string; at: string; showName: boolean; ariaLabel: string; onOpen?: () => void }) {
  const line = <><LiveMark /><span className="process-line-text web-tool-live-label">{label}</span>{onOpen && <Icon className="web-tool-chevron process-line-chevron" name="chevron-right" size={14} aria-hidden="true" />}</>;
  return <section className={`tool-message web-tool-run is-live live-run-status${showName ? " is-group" : ""}`} aria-label={ariaLabel} data-sender={showName ? botId : undefined} data-at={showName ? at : undefined}>
    {showName && <SenderHead id={botId} name={botName} />}
    <div className="web-tool-disclosure"><div className="web-tool-summary">{onOpen ? <button type="button" className="web-tool-toggle" aria-expanded="false" onClick={onOpen}>{line}</button> : <span className="web-tool-toggle is-static">{line}</span>}</div></div>
  </section>;
}

function LiveRunStatus({ run, botName, showName }: { run: Run; botName: string; showName: boolean }) {
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
function formatTime(value: string, timeZone: string) {
  return dateTimeFormat(intlLocale(), { hour: "2-digit", minute: "2-digit", timeZone }).format(new Date(value));
}

function formatExactTime(value: string, timeZone?: string) {
  return dateTimeFormat(intlLocale(), { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZone }).format(new Date(value));
}

function formatListTime(value: string, timeZone: string) {
  const date = new Date(value);
  const now = new Date();
  if (dateInTimezone(value, timeZone) === dateInTimezone(now.toISOString(), timeZone)) return dateTimeFormat(intlLocale(), { hour: "2-digit", minute: "2-digit", timeZone }).format(date);
  return dateTimeFormat(intlLocale(), { month: "numeric", day: "numeric", timeZone }).format(date);
}

function formatDay(value: string, timeZone: string) {
  return dateTimeFormat(intlLocale(), { month: "long", day: "numeric", weekday: "short", timeZone }).format(new Date(value));
}

function previewText(value: string, max = 160) {
  const normalized = readReplyMarker(value).body.replace(/\s+/g, " ").replace(/[*_`#>]/g, "").replace(/\s+/g, " ").trim();
  const chars = Array.from(normalized);
  return chars.length > max ? `${chars.slice(0, max - 1).join("")}…` : normalized;
}

function formatDateDivider(value: string, timeZone: string) {
  return `${formatDay(value, timeZone)} ${formatTime(value, timeZone)}`;
}

/** The server pings every 15s; this much silence on a stream means the link died without closing. */
const STREAM_STALE_MS = 30_000;
const LINK_LOSS_GRACE_MS = 4000;
const STREAM_WAKE_STALE_MS = 20_000;

function App() {
  return <TimezoneProvider><Workspace /></TimezoneProvider>;
}

function Workspace() {
  const { t } = useTranslation(["chat", "common"]);
  const {timezone, showActivity} = useUserTimezone();
  const [revealedRuns, setRevealedRuns] = useState<ReadonlySet<string>>(() => new Set());
  const revealRun = useCallback((runId: string) => { focusToggleOfRun = runId; setRevealedRuns(current => current.has(runId) ? current : new Set(current).add(runId)); }, []);
  const activityView = useMemo(() => ({ show: showActivity, revealed: revealedRuns, reveal: revealRun }), [showActivity, revealedRuns, revealRun]);
  const appearance = useAppearance();
  const [sidebarCollapsed, setSidebarCollapsed] = useState(Boolean(desktopState().sidebarCollapsed));
  const sidebarListRef = useRef<HTMLDivElement | null>(null);
  const sidebarRects = useRef(new Map<string, { x: number; y: number }>());
  const sidebarAnimations = useRef(new WeakMap<HTMLElement, Animation>());
  const sidebarWasCollapsed = useRef(sidebarCollapsed);
  // Set by a drag-and-drop commit: the drop settles the moved row itself, so the list's own move animation stands down once.
  const sidebarSkipFlip = useRef(false);
  useLayoutEffect(() => {
    if (isDesktop || !sidebarListRef.current) return;
    const skipFlip = sidebarSkipFlip.current;
    sidebarSkipFlip.current = false;
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
      if (collapseChanged || reduced || skipFlip || !previous.size) return;
      const before = previous.get(id);
      if (!before) {
        sidebarAnimations.current.set(row, row.animate([{ opacity:0, transform:"translateX(-18px) scale(.94)" }, { opacity:1, transform:"none" }], { duration:MOTION.flight, easing:EASE.out }));
      } else if (Math.abs(before.y - (rect.top + scroll)) > 1) {
        const delta = before.y - (rect.top + scroll);
        sidebarAnimations.current.set(row, row.animate([{ transform:`translateY(${delta}px)` }, { transform:"translateY(0) scale(1.03)", offset:.7 }, { transform:"none" }], { duration:MOTION.panelIn, easing: EASE.out }));
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
  /** What each unacknowledged outgoing message needs to be sent again: its text and uploaded attachment ids. In memory only. */
  const sendPayloads = useRef(new Map<string, { conversationId: string; content: string; attachmentIds: string[] }>());
  const reactionTokens = useRef(new Map<string, number>());
  /** Ids that appeared as a streaming draft: their text was already read in place, so the saved message must not replay an arrival. */
  const streamedIDs = useRef(new Set<string>());
  /** A press that lands while text is selected dismisses the selection; it must not also toggle the tap actions. */
  const selectionOnPress = useRef(false);
  const messageListRef = useRef<HTMLDivElement | null>(null);
  // Group chats: avatar and name open a sender group once; later units from the same Bot only indent.
  const [messageRecords, setMessages] = useState<Message[]>([]);
  const [drafts, setDraftsState] = useState<Record<string, StreamDraft>>({});
  // The conversation whose whole loaded page is drawn. A switch draws the newest blocks first and the rest right after.
  const [fullTimelineFor, setFullTimelineFor] = useState<string | null>(null);
  // Streamed text lives in `draftStream`; `drafts` changes only in structure (see draftStream.ts). `draftsRef` is the newest record, committed or not.
  const draftStream = useRef(new DraftStream()).current;
  const draftsRef = useRef<Record<string, StreamDraft>>({});
  const setDrafts = useCallback((update: Record<string, StreamDraft> | ((current: Record<string, StreamDraft>) => Record<string, StreamDraft>)) => {
    const next = typeof update === "function" ? update(draftsRef.current) : update;
    if (next === draftsRef.current) return;
    draftsRef.current = next;
    draftStream.reconcile(next);
    setDraftsState(next);
  }, [draftStream]);
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
  const workspaceErrorShown = useGraceFlag(Boolean(workspaceError), LINK_LOSS_GRACE_MS);
  const [headerMenuOpen, setHeaderMenuOpen] = useState(false);
  const [rememberedOpen, setRememberedOpen] = useState(false);
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [sidebarLayout, setSidebarLayout] = useState<SidebarLayout>(readSidebarLayout);
  const [hiddenBotsOpen, setHiddenBotsOpen] = useState(false);
  const [sidebarMenuSlot] = useState(() => new OverlaySlot<SidebarContextMenu>());
  const setSidebarContextMenu = sidebarMenuSlot.set;
  // The element that had focus when the sidebar row menu opened (the row, from the keyboard): read synchronously by the
  // slot's listener, before the menu renders and takes focus.
  const sidebarMenuOrigin = useRef<HTMLElement | null>(null);
  useEffect(() => sidebarMenuSlot.subscribe(() => {
    if (!sidebarMenuSlot.get()) return;
    const active = document.activeElement;
    sidebarMenuOrigin.current = active instanceof HTMLElement && active !== document.body && !active.closest("#tofi-layers") ? active : null;
  }), [sidebarMenuSlot]);
  // Where focus returns when the delete or successor dialog closes. Read when it opens: the workspace turns inert in the
  // same commit, which drops focus to <body> before the dialog renders. From the sidebar menu, the row it belongs to.
  const dialogOpener = useRef<HTMLElement | null>(null);
  const rememberDialogOpener = () => {
    const active = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null;
    dialogOpener.current = active?.closest(".sidebar-context-menu") ? sidebarMenuOrigin.current : active;
  };
  const [reactionMenu, setReactionMenu] = useState<{ messageId: string; x: number; y: number; h?: number } | null>(null);
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
  // Hiding the Primary Assistant asks for its successor first (same rule as deleting it).
  const [hidePrimaryTarget, setHidePrimaryTarget] = useState<Bot | null>(null);
  const knownBotNames = useRef(new Map<string, string>());
  useEffect(() => { for (const bot of bots) knownBotNames.current.set(bot.id, bot.name); }, [bots]);
  const [streamConnected, setStreamConnected] = useState(true);
  // A restart or a Wi-Fi blip is over before anyone can act on it: announce a lost link only once it has lasted.
  const linkLost = useGraceFlag(!streamConnected, LINK_LOSS_GRACE_MS);
  const linkConnected = !linkLost;
  const [codexStatusRefresh, setCodexStatusRefresh] = useState(0);
  const [extensionRefresh, setExtensionRefresh] = useState(0);
  const [computerInfo, setComputerInfo] = useState<{ state: string; phase?: string; error?: string; health?: ComputerHealth } | null>(null);
  const chatHeaderRef = useRef<HTMLDivElement | null>(null);
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
  // The phone push and panel openings are the frames that matter most; the cats' idle breathing waits them out.
  useEffect(() => { holdAvatarLife(320); }, [mobileList, panel]);
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
  const notificationSeen = useRef<Record<string, number>>({});
  const eventCursor = useRef(0);
  const eventSource = useRef<EventSource | null>(null);
  const reconnectTimer = useRef<number | undefined>(undefined);
  const eventConnectTimer = useRef<number | undefined>(undefined);
  const reconnectAttempt = useRef(0);
  // Liveness of the open streams: the server pings every 15s, so a long silence means the link died without closing.
  const lastFrameAt = useRef(Date.now());
  const eventFailNow = useRef<(() => void) | null>(null);
  const eventWatchdog = useRef<number | undefined>(undefined);
  const snapshotRetryNow = useRef<(() => void) | null>(null);
  const workspaceLastFrameAt = useRef(Date.now());
  const workspaceFailNow = useRef<(() => void) | null>(null);
  const workspaceWatchdog = useRef<number | undefined>(undefined);
  const activeIdRef = useRef<string | null>(null);
  const snapshotEventCursor = useRef(0);
  const snapshotMaxSeq = useRef(0);
  const snapshotCursorKnown = useRef(false);
  const newBotBusyRef = useRef(false);
  const newBotCreationId = useRef<string | null>(null);
  const newBotPending = useRef(false);
  const snapshotGeneration = useRef(0);
  // Workspace list requests are ordered by when they started. A response is
  // applied unless a request that started later has already been applied, so a
  // slow list can never be starved by the next poll superseding it.
  const workspaceRequestSeq = useRef(0);
  const workspaceAppliedSeq = useRef(0);
  const workspaceInFlight = useRef(0);
  const workspaceInFlightAt = useRef(0);
  const archivedRequested = useRef(false);
  // Workspace events arrive in bursts (an import or an eval can write hundreds).
  // They are coalesced into one list refresh, with one request in flight.
  const workspaceEventPending = useRef(false);
  const workspaceEventTimer = useRef<number | undefined>(undefined);
  const flushWorkspaceEvents = useRef<() => void>(() => {});
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
    if (document.querySelector('[aria-modal="true"]') && command !== "settings") return;
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
    setConfig((current) => sameData(current, nextConfig) ? current : nextConfig);
    if (previous && (previous.model_configured !== nextConfig.model_configured || previous.default_model !== nextConfig.default_model || previous.provider !== nextConfig.provider)) {
      setCodexStatusRefresh((current) => current + 1);
    }
  }, []);

  const beginWorkspaceRequest = () => {
    workspaceInFlight.current += 1;
    workspaceInFlightAt.current = Date.now();
    return ++workspaceRequestSeq.current;
  };
  // True when this response may be applied; it then becomes the newest applied one.
  const claimWorkspaceResult = (seq: number, generation: number) => {
    if (generation !== snapshotGeneration.current || newBotPending.current || seq < workspaceAppliedSeq.current) return false;
    workspaceAppliedSeq.current = seq;
    return true;
  };
  const endWorkspaceRequest = (applied: boolean) => {
    workspaceInFlight.current = Math.max(0, workspaceInFlight.current - 1);
    if (applied || workspaceInFlight.current === 0) setLoading(false);
    if (workspaceInFlight.current === 0 && workspaceEventPending.current && indexInitialized.current) flushWorkspaceEvents.current();
  };
  // A poll tick or focus event must not stack another request behind a slow
  // one. A request older than this is presumed hung and no longer blocks ticks.
  const workspaceRequestBusy = () => workspaceInFlight.current > 0 && Date.now() - workspaceInFlightAt.current < 20_000;

  const mergeArchived = useCallback((archivedConversations: Conversation[], archivedBots: Bot[]) => {
    setConversations((current) => [...current.filter((item) => !archivedConversations.some((archived) => archived.id === item.id)), ...archivedConversations]);
    setBots((current) => [...current.filter((item) => !archivedBots.some((archived) => archived.id === item.id)), ...archivedBots]);
  }, []);

  // Archived rows are not part of the polled lists; they load once in the
  // background after the workspace is on screen, and again when the archive
  // view opens or an explicit refresh asks for them.
  const loadArchivedInBackground = useCallback(async () => {
    if (archivedRequested.current) return;
    archivedRequested.current = true;
    try {
      const [conversationResult, botResult] = await Promise.all([api.conversations(true), api.bots(true)]);
      mergeArchived(conversationResult.conversations.filter((item) => item.archived), botResult.bots.filter((item) => item.archived));
    } catch {
      archivedRequested.current = false;
    }
  }, [mergeArchived]);

  const refreshIndex = useCallback(async () => {
    const generation = snapshotGeneration.current;
    const requestSeq = beginWorkspaceRequest();
    const configRequestGeneration = ++configRefreshGeneration.current;
    // The first load only needs what the sidebar shows. Explicit refreshes
    // after a mutation keep asking for archived rows too.
    const firstLoad = !indexInitialized.current;
    let applied = false;
    setLoading(true);
    try {
      const [botResult, conversationResult, nextConfig] = await Promise.all([api.bots(!firstLoad), api.conversations(!firstLoad), api.config()]);
      if (!claimWorkspaceResult(requestSeq, generation)) return;
      applied = true;
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
      if (firstLoad) void loadArchivedInBackground();
    } catch (cause) {
      setWorkspaceError(errorText(cause));
      setError(errorText(cause));
    } finally {
      endWorkspaceRequest(applied);
    }
  }, [applyConfig, loadArchivedInBackground]);

  // Sidebar layout saves are queued so the server sees them in the order they were made; while one is in flight a poll
  // must not put the older stored layout back over the optimistic one.
  const layoutInFlight = useRef(0);
  const layoutQueue = useRef<Promise<void>>(Promise.resolve());
  const refreshConversations = useCallback(async () => {
    const generation = snapshotGeneration.current;
    const requestSeq = beginWorkspaceRequest();
    let applied = false;
    try {
      const [result, botResult] = await Promise.all([api.conversations(), api.bots()]);
      if (!claimWorkspaceResult(requestSeq, generation)) return;
      applied = true;
      // The 5s poll usually returns what is already shown; keeping the old objects keeps the whole workspace from re-rendering.
      // Archived rows are not polled: keep the ones already loaded.
      setBots((current) => {
        const fresh = new Set(botResult.bots.map((bot) => bot.id));
        const merged = [...botResult.bots, ...current.filter((bot) => bot.archived && !fresh.has(bot.id))];
        return sameData(current, merged) ? current : merged;
      });
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
        const fresh = new Set(merged.map((conversation) => conversation.id));
        const withArchived = [...(layoutInFlight.current > 0 ? keepLocalLayout(merged, current) : merged), ...current.filter((conversation) => conversation.archived && !fresh.has(conversation.id))];
        return sameData(current, withArchived) ? current : withArchived;
      });
    } catch (cause) {
      // Conversation previews are best-effort while the app is in use.
      if (indexInitialized.current) setWorkspaceError(errorText(cause));
    } finally {
      // Whichever workspace request lands last must release the shared loading indicator.
      endWorkspaceRequest(applied);
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

  const commitSidebarLayout = useCallback((layout: SidebarIds) => {
    layoutInFlight.current += 1;
    setConversations(current => applyLayout(current, layout));
    layoutQueue.current = layoutQueue.current.then(async () => {
      let failed: unknown;
      try { await api.setSidebarLayout(layout); } catch (cause) { failed = cause; }
      layoutInFlight.current -= 1;
      if (failed !== undefined) { setError(errorText(failed)); await refreshConversations(); }
    });
  }, [refreshConversations]);

  // First-run onboarding. The state lives on the server, so it resumes on any device.
  const [onboarding, setOnboarding] = useState<OnboardingState | null>(null);
  const [onboardingForced, setOnboardingForced] = useState<OnboardingStep | null>(null);
  useEffect(() => {
    let alive = true;
    api.onboardingState().then(value => { if (!alive) return; const state = parseOnboardingState(value); if (state && !state.completed) preloadPanel("onboarding"); setOnboarding(state); }).catch(() => { /* no state, no onboarding */ });
    return () => { alive = false; };
  }, []);
  // Settings and the Bot desktop code arrive in idle slots once the shell has content to show.
  useEffect(() => (loading ? undefined : warmPanelsWhenIdle()), [loading]);
  const saveOnboarding = useCallback((update: { step?: number; skipped?: boolean; completed?: boolean }) => {
    setOnboarding(current => current ? { step: Math.max(current.step, update.step ?? 0), completed: update.completed ?? current.completed, skipped: update.skipped ?? current.skipped } : current);
    void api.putOnboarding(update).then(value => { const next = parseOnboardingState(value); if (next) setOnboarding(next); }).catch(() => { /* the next change writes it again */ });
  }, []);
  const modelReady = config?.model_configured ?? false;
  const onboardingState = loading || !config ? "none" : onboardingMode(onboarding, { modelConfigured: modelReady, botCount: bots.length, forcedOpen: onboardingForced !== null });
  /** The composer banner's Connect: reopen setup at the model step, or Settings once setup is behind the account. */
  const connectModel = () => { if (onboarding && !onboarding.completed) setOnboardingForced(2); else openSettings("models"); };

  const scheduleWorkspaceEventRefresh = useCallback(() => {
    if (workspaceEventTimer.current !== undefined) return;
    workspaceEventTimer.current = window.setTimeout(() => {
      workspaceEventTimer.current = undefined;
      flushWorkspaceEvents.current();
    }, 200);
  }, []);
  flushWorkspaceEvents.current = () => {
    // One refresh per burst. While a request is in flight it re-flushes when it ends.
    if (!workspaceEventPending.current || workspaceInFlight.current > 0 || !indexInitialized.current) return;
    workspaceEventPending.current = false;
    setScheduleRefresh(current => current + 1);
    void refreshConversations();
  };
  useEffect(() => () => { if (workspaceEventTimer.current !== undefined) window.clearTimeout(workspaceEventTimer.current); }, []);

  const handleWorkspaceEvent = useCallback((data: WorkspaceEventEnvelope, id: number) => {
    // If the server restored a database whose event log has a lower
    // high-water mark, it replays from zero. A lower revision is therefore a
    // reset signal; accept that replay instead of discarding it as a normal
    // duplicate. Equal revisions remain deduplicated.
    if (id > 0 && id < workspaceEventCursor.current) workspaceEventCursor.current = 0;
    else if (id > 0 && id === workspaceEventCursor.current) return;
    if (id > workspaceEventCursor.current) workspaceEventCursor.current = id;
    if (!indexInitialized.current) {
      // The initial load already covers every event the replay can carry.
      if (!workspaceRequestBusy()) void refreshIndex();
      else workspaceEventPending.current = true;
      return;
    }
    if (data.scope === "bots" || data.scope === "groups") { workspaceEventPending.current = true; scheduleWorkspaceEventRefresh(); }
    else if (data.scope === "config") { setExtensionRefresh((current) => current + 1); void refreshConfig(); }
  }, [refreshConfig, refreshIndex, scheduleWorkspaceEventRefresh]);

  const refreshWorkspaceOnConnect = useCallback(() => {
    if (!indexInitialized.current) { if (!workspaceRequestBusy()) void refreshIndex(); }
    else { workspaceEventPending.current = true; scheduleWorkspaceEventRefresh(); void refreshConfig(); }
  }, [refreshConfig, refreshIndex, scheduleWorkspaceEventRefresh]);

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
    window.clearInterval(workspaceWatchdog.current);
    workspaceLastFrameAt.current = Date.now();
    const failed = () => {
      source.close();
      if (!current()) return;
      window.clearInterval(workspaceWatchdog.current);
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
    }, failed, () => { if (current()) resetWorkspaceEvents(); }, () => { workspaceLastFrameAt.current = Date.now(); });
    for (const event of ["question", "question_answered", "question_cancelled", "question_updated"]) source.addEventListener(event, () => { if (current()) void questions.refresh(); });
    source.onopen = () => {
      if (!current()) return;
      workspaceLastFrameAt.current = Date.now();
      if (workspaceConnectTimer.current !== undefined) window.clearTimeout(workspaceConnectTimer.current);
      workspaceReconnectAttempt.current = 0;
      // A reconnect can follow a restored database whose event log no longer
      // contains the old cursor. Refresh immediately, including the empty
      // snapshot case, instead of waiting for polling to repair the sidebar.
      refreshWorkspaceOnConnect();
      void questions.refresh();
    };
    workspaceEventSource.current = source;
    workspaceFailNow.current = failed;
    workspaceWatchdog.current = window.setInterval(() => { if (current() && Date.now() - workspaceLastFrameAt.current > STREAM_STALE_MS) failed(); }, 5000);
    workspaceConnectTimer.current = window.setTimeout(failed, 10_000);
  }, [handleWorkspaceEvent, refreshWorkspaceOnConnect, resetWorkspaceEvents]);

  useEffect(() => { void refreshIndex(); }, [refreshIndex]);
  useEffect(() => { const refresh = () => { void refreshIndex(); }; window.addEventListener("tofi:portability-imported", refresh); return () => window.removeEventListener("tofi:portability-imported", refresh); }, [refreshIndex]);
  useEffect(() => {
    if (!activeId) connectWorkspaceEvents();
    return () => {
      workspaceEventSource.current?.close();
      workspaceEventSource.current = null;
      window.clearInterval(workspaceWatchdog.current);
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
      // A dialog above the pane (ui/src/overlays/ModalDialog.tsx) owns Tab and Escape.
      if (document.querySelector("[data-tofi-modal]")) return;
      if (event.key === "Escape" && !event.defaultPrevented) {
        if (detailPaneRef.current?.querySelector('.settings-tabs')) {
          event.preventDefault();
          window.dispatchEvent(new Event('tofi-settings-close'));
          return;
        }
        if (document.querySelector(".computer-detail.is-expanded") || detailPaneRef.current?.querySelector('[aria-busy="true"]')) return;
        event.preventDefault();
        setPanel(null);
        return;
      }
      // A menu over the pane has already moved focus on this Tab (RadixOverlays tabFrom) and cancelled the key.
      if (event.key !== "Tab" || event.defaultPrevented) return;
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
    if (!panel || modalPanelOpen) return;
    const onEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !event.defaultPrevented && panel !== "settings" && !document.querySelector(".computer-detail.is-expanded, [data-tofi-modal]") && !detailPaneRef.current?.querySelector('[aria-busy="true"]')) {
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
    let timer = 0;
    // Steady states (no computer, ready, hibernated) change rarely, so they re-check slowly; transitions poll fast.
    const settled = (state?: string) => !state || state === "ready" || state === "hibernated" || state === "stopped";
    const refreshComputer = async () => {
      let state: string | undefined;
      if (!document.hidden) {
        try { const next = await api.computerInfo(); state = next.state; if (alive) setComputerInfo((current) => JSON.stringify(current) === JSON.stringify(next) ? current : next); } catch { if (alive) setComputerInfo(null); }
      }
      if (alive) timer = window.setTimeout(() => void refreshComputer(), settled(state) ? 30_000 : 4_000);
    };
    const onVisible = () => { if (!document.hidden) { window.clearTimeout(timer); void refreshComputer(); } };
    document.addEventListener("visibilitychange", onVisible);
    void refreshComputer();
    return () => { alive = false; window.clearTimeout(timer); document.removeEventListener("visibilitychange", onVisible); };
  }, []);
  useEffect(() => {
    const refresh = () => {
      if (workspaceRequestBusy()) return;
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
  // Ownership is only worth a 1 s poll while a computer is ready or the desktop panel is open.
  const desktopPresence = useDesktopPresence(Boolean(activeId) && (panel === "desktop" || (computerInfo ? computerInfo.state === "ready" : desktopReady)));
  const currentDesktopOwner = desktopPresence.ownership?.owner;
    const activeDesktopOwner = !desktopPresence.unavailable && currentDesktopOwner?.kind === "bot" && currentDesktopOwner.conversation_id === active?.id ? currentDesktopOwner : null;
  const computerOn = computerInfo ? computerInfo.state === "ready" : desktopReady;
  const computerStateLabel = !computerOn ? t("header.computer_off") : activeDesktopOwner ? t("header.computer_driving", { name: botById.get(activeDesktopOwner.bot_id)?.name ?? "Bot" }) : t("header.computer_on");
  const query = search.trim().toLowerCase();
  const sortedConversations = useMemo(() => [...pinnedOf(conversations), ...listOrder(conversations)], [conversations]);
  const filteredConversations = sortedConversations.filter((conversation) => {
    if (conversation.archived) return false;
    if (!query) return true;
    const names = conversation.kind === "dm" ? [conversation.name, botByConversationId.get(conversation.id)?.instructions ?? ""] : [conversation.name, ...conversation.bot_ids.map((id) => botById.get(id)?.name ?? "")];
    return names.some((name) => name.toLowerCase().includes(query));
  });
  const hiddenBots = bots.filter((bot) => bot.archived && (!query || bot.name.toLowerCase().includes(query)));
  const visibleSidebarConversations = filteredConversations.filter((conversation) => !conversation.archived);
  const pinnedConversations = visibleSidebarConversations.filter((conversation) => conversation.pinned);
  const customSidebarSections = Object.entries(sidebarLayout.sections).reduce<Record<string, Conversation[]>>((groups, [id, name]) => {
    const conversation = visibleSidebarConversations.find((item) => item.id === id && !item.pinned);
    if (conversation && name.trim()) (groups[name] ??= []).push(conversation);
    return groups;
  }, {});
  const sectionedConversationIds = new Set(Object.values(customSidebarSections).flat().map((conversation) => conversation.id));
  const defaultSidebarConversations = visibleSidebarConversations.filter((conversation) => !conversation.pinned && !sectionedConversationIds.has(conversation.id));
  // Drag, drop and Alt+Arrow move work on the plain list and the pinned tiles; a search shows a subset, so it cannot be reordered.
  const sidebarDndEnabled = !query && !sidebarCollapsed;
  // Spoken status for moves. Written straight to the live region: a lift must not re-render the whole workspace.
  const sidebarLiveRef = useRef<HTMLDivElement | null>(null);
  const setSidebarAnnouncement = (text: string) => { if (sidebarLiveRef.current) sidebarLiveRef.current.textContent = text; };
  const sidebarIdsRef = useRef({ pinned: [] as string[], list: [] as string[], unpinned: [] as string[] });
  sidebarIdsRef.current = { pinned: pinnedConversations.map(item => item.id), list: defaultSidebarConversations.map(item => item.id), unpinned: sortedConversations.filter(item => !item.pinned && !item.archived).map(item => item.id) };
  // Pins used to live in this browser only. Once the list has loaded, hand them to the server (when it holds none) and forget them here.
  useEffect(() => {
    if (!sidebarLayout.pinned.length || !conversations.length) return;
    const stored = sidebarLayout.pinned.filter(id => conversations.some(item => item.id === id && !item.archived));
    setSidebarLayout(current => ({ ...current, pinned: [] }));
    if (stored.length && !conversations.some(item => item.pinned)) {
      const ids = sidebarIdsRef.current;
      commitSidebarLayout({ pinned: stored, order: ids.unpinned.filter(id => !stored.includes(id)) });
    }
  }, [sidebarLayout.pinned, conversations, commitSidebarLayout]);
  /** Turn what the drop produced (pinned tiles, plain list rows) into the full stored layout. */
  const layoutFromDrop = (next: DndCommit): SidebarIds => {
    const listed = new Set(sidebarIdsRef.current.list);
    const pinned = new Set(next.pinned);
    return { pinned: next.pinned, order: [...sidebarIdsRef.current.unpinned.filter(id => !listed.has(id) && !pinned.has(id)), ...next.list] };
  };
  const announceSidebar = (event: DndAnnounce) => {
    const name = conversations.find(item => item.id === event.id)?.name ?? "";
    const key = event.kind === "lift" ? "sidebar.dnd_lift" : event.kind === "pin" ? "sidebar.dnd_pinned" : event.kind === "unpin" ? "sidebar.dnd_unpinned" : "sidebar.dnd_moved";
    setSidebarAnnouncement(t(key, { name, pos: event.pos, total: event.total }));
  };
  useSidebarDnd({
    scrollRef: sidebarListRef, enabled: sidebarDndEnabled, signature: `${sidebarIdsRef.current.pinned.join(",")}|${sidebarIdsRef.current.list.join(",")}`, skipFlip: sidebarSkipFlip,
    getIds: () => sidebarIdsRef.current,
    onCommit: next => commitSidebarLayout(layoutFromDrop(next)),
    onMenu: (id, x, y) => { const conversation = conversations.find(item => item.id === id); if (conversation) setSidebarContextMenu({ conversation, x, y }); },
    onAnnounce: announceSidebar,
  });
  const runById = useMemo(() => new Map(runs.map((run) => [run.id, run])), [runs]);
  const activeRunIds = useMemo(() => new Set(runs.filter(run => run.status === "queued" || run.status === "running").map(run => run.id)), [runs]);
  // Activity rows are off by default: only a working run (as one status line) or a run opened from its message's menu draws steps.
  const keepActivityRun = useCallback((runId?: string) => showActivity || (runId !== undefined && (revealedRuns.has(runId) || activeRunIds.has(runId))), [showActivity, revealedRuns, activeRunIds]);
  const retryFamilies = useMemo(() => buildRetryFamilies(runs), [runs]);
  const runStarts = useMemo(() => new Map(runs.map(run => [run.id, Date.parse(run.created_at)] as const).filter(([, start]) => Number.isFinite(start))), [runs]);
  const retryAuditAnchors = useMemo(() => { const index = indexMessages(messageRecords); return new Map(retryFamilies.map(family => [family.rootId, retryFamilyAnchor(family, index)])); }, [retryFamilies, messageRecords]);
  const supersededRunIDs = useMemo(() => new Set(retryFamilies.filter(family => retryAuditAnchors.get(family.rootId)).flatMap(family => family.previous.map(run => run.id))), [retryFamilies, retryAuditAnchors]);
  const runsForMessage = useMemo(() => runsByTrigger(runs), [runs]);
  const lastReplySeq = useMemo(() => messageRecords.reduce((max, item) => item.role === "assistant" && !item.local && (!item.kind || item.kind === "segment") ? Math.max(max, item.seq) : max, 0), [messageRecords]);
  const visibleMessages = useMemo(() => mergeMessageTimeline(messageRecords.filter(message => message.role === "user" || !message.run_id || !supersededRunIDs.has(message.run_id)), Object.values(drafts).filter(draft => !runById.has(draft.run_id) || !isTerminalRun(runById.get(draft.run_id)!))), [messageRecords, drafts, supersededRunIDs, runById]);
  const foldedProgress = useMemo(() => foldCompletedProgress(visibleMessages, runs), [visibleMessages, runs]);
  const messages = foldedProgress.visible;
  const scheduledRootKey = useMemo(() => occurrenceRootKey(messageRecords.filter(message => message.kind === "scheduled_task" && message.conversation_id === activeId).map(message => message.run_id ?? "")), [messageRecords, activeId]);
  const occurrenceRead = useScheduleOccurrences(activeId, scheduledRootKey, scheduleRefresh);
  const metadataRead = useScheduleMetadata(isDesktop ? null : activeId, scheduledRootKey, scheduleRefresh);
  const scheduleOccurrences = occurrenceRead.items;
  const scheduleMetadata = metadataRead.items;
  /** A scheduled card whose occurrence data has not arrived yet draws a blank ticket instead of placeholder status text. */
  const scheduleCardPending = (runId?: string) => Boolean(runId) && !(occurrenceRead.ready && metadataRead.ready) && !scheduleOccurrences.has(runId!);
  // Settled questions older than the loaded message window are not drawn, so they are not requested.
  const questionsSince = useMemo(() => { const first = messageRecords.find(message => !message.local); return first && first.conversation_id === activeId ? first.created_at : undefined; }, [messageRecords, activeId]);
  const questions = useQuestions(activeId, questionsSince);
  const mailDrafts = useMailDrafts(activeId);
  const taskOwners = useMemo(() => buildTaskOwners(runs, messageRecords, questions.items, mailDrafts.items), [runs, messageRecords, questions.items, mailDrafts.items]);
  const ownedRunIDs = useMemo(() => new Set(taskOwners.flatMap(owner => owner.family.attempts.map(run => run.id))), [taskOwners]);
  const conversationItems = useMemo(() => buildQuestionTimeline(messages.filter(message => message.kind !== "progress" || !ownedRunIDs.has(message.run_id ?? "")), questions.items, hasMore, mailDrafts.items), [messages, questions.items, hasMore, mailDrafts.items, ownedRunIDs]);
  const quoteTimeline = useMemo(() => conversationItems.flatMap(item => item.kind === "message" ? [item.message] : []), [conversationItems]);
  const quoteIndexById = useMemo(() => new Map(quoteTimeline.map((item, i) => [item.id, i])), [quoteTimeline]);
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
  const timelineToolActivities = useMemo(() => loadedToolActivities.filter(activity => !aggregateToolRunIDs.has(activity.run_id) && !supersededRunIDs.has(activity.run_id) && !ownedRunIDs.has(activity.run_id) && keepActivityRun(activity.run_id)), [loadedToolActivities, aggregateToolRunIDs, supersededRunIDs, ownedRunIDs, keepActivityRun]);
  const toolTimeline = useMemo(() => {
    const visibleIds = new Set(messages.map(message => message.id));
    return buildToolTimeline(messageRecords.filter(message => visibleIds.has(message.id)), timelineToolActivities, runs);
  }, [messageRecords, messages, timelineToolActivities, runs]);
  const pendingRuns = useMemo(() => {
    const live = runs.filter(run => run.conversation_id === activeId && (run.status === "running" || run.status === "queued") && run.kind !== "schedule");
    const running = live.some(run => run.status === "running");
    const busy = new Set([
      ...loadedToolActivities.map(activity => activity.run_id),
      ...messageRecords.filter(message => isMidRunKind(message.kind) && message.run_id).map(message => message.run_id!),
      ...Object.values(drafts).filter(draft => draft.content.trim()).map(draft => draft.run_id),
    ]);
    return live.filter(run => (run.status === "running" || !running) && !busy.has(run.id)).sort((a, b) => a.created_at.localeCompare(b.created_at));
  }, [runs, activeId, loadedToolActivities, messageRecords, drafts]);
  const toolSummaryAnchors = useMemo(() => buildToolSummaryAnchors(messages, terminalToolSummaries.filter(summary => !supersededRunIDs.has(summary.run_id) && !ownedRunIDs.has(summary.run_id) && keepActivityRun(summary.run_id)), toolDetailActivities, runs), [messages, terminalToolSummaries, toolDetailActivities, runs, supersededRunIDs, ownedRunIDs, keepActivityRun]);
  const toolSummaryErrorAnchors = useMemo(() => buildToolRunAnchors(messages, terminalSummaryRunIDs.filter(runID => !supersededRunIDs.has(runID) && !ownedRunIDs.has(runID) && keepActivityRun(runID) && toolSummaryState[runID]?.error).map(run_id => ({ run_id })), runs), [messages, terminalSummaryRunIDs, toolSummaryState, runs, supersededRunIDs, ownedRunIDs, keepActivityRun]);
  const toolSummaryLoadingAnchors = useMemo(() => {
    const known = new Set(terminalToolSummaries.map(summary => summary.run_id));
    const evidence = new Set([
      ...loadedToolActivities.map(activity => activity.run_id),
      ...messageRecords.filter(message => isMidRunKind(message.kind) && message.run_id).map(message => message.run_id!),
    ]);
    return buildToolRunAnchors(messages, terminalSummaryRunIDs.filter(runID => !supersededRunIDs.has(runID) && !ownedRunIDs.has(runID) && keepActivityRun(runID) && toolSummaryState[runID]?.loading && !known.has(runID) && evidence.has(runID)).map(run_id => ({ run_id })), runs);
  }, [messages, messageRecords, loadedToolActivities, terminalSummaryRunIDs, toolSummaryState, terminalToolSummaries, runs, supersededRunIDs, ownedRunIDs, keepActivityRun]);

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
  // Phone: a finger dragging the chat to the right returns to the list (D35); the gesture commits the same state as the back button.
  usePhoneSwipeBack(() => flushSync(() => setMobileList(true)));
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
    } else if (taskFocus.kind === "run" && (!runById.has(taskFocus.id) || !runDrawsHere(taskFocus.id))) {
      // The run is not part of this conversation, or its block draws nothing: older pages would never reveal a target.
      setTaskFocus(null);
    } else if (taskFocus.kind === "message" || taskFocus.kind === "run") {
      if (hasMore && !loadingOlder && !historyError) void loadOlder();
      else if (!hasMore) setTaskFocus(null);
    }
  }, [taskFocus, activeId, loadedId, loadingMessages, conversationItems, hasMore, loadingOlder, historyError]);

  function runDrawsHere(runId: string) {
    const owner = taskOwners.find(o => o.family.attempts.some(attempt => attempt.id === runId));
    return !owner || ownerDraws(owner);
  }
  function focusTask(conversation: Conversation) {
    const state = conversation.task_state;
    const kind = state?.run_id ? "run" : state?.question_id ? "question" : state?.draft_id ? "draft" : state?.result_message_id ? "message" : undefined;
    const id = state?.run_id || state?.question_id || state?.draft_id || state?.result_message_id;
    // A finished run draws no badge, so there is nothing to scroll to; paging history for it only blocks the main thread.
    if (kind && id && state?.status !== "completed") setTaskFocus({ conversationId: conversation.id, kind, id });
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
      // A person's own message arriving over the stream before the POST answers takes the place of its optimistic copy.
      const byId = new Map(current.filter((item) => !(item.local && next.client_message_id && item.client_message_id === next.client_message_id)).map((item) => [item.id, item]));
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

  // Expanding a step whose list entry carried only a result preview loads that one full result.
  const toolResultRequests = useRef(new Set<string>());
  const loadFullToolResult = useCallback((activity: ToolActivity) => {
    const conversationId = activeIdRef.current;
    if (!conversationId || activity.conversation_id !== conversationId) return;
    const key = `${toolActivityKey(activity)}\u0000${activity.updated_at}`;
    if (toolResultRequests.current.has(key)) return;
    toolResultRequests.current.add(key);
    api.toolActivityResult(conversationId, activity.run_id, activity.call_id).then(({ activity: full }) => {
      if (activeIdRef.current !== conversationId) return;
      setToolDetailActivities(current => {
        const byKey = new Map(current.map(item => [toolActivityKey(item), item]));
        byKey.set(toolActivityKey(full), reconcileToolActivity(byKey.get(toolActivityKey(full)), full));
        return [...byKey.values()].sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at));
      });
    }).catch(() => { toolResultRequests.current.delete(key); });
  }, []);
  useEffect(() => { setToolResultLoader(loadFullToolResult); return () => setToolResultLoader(undefined); }, [loadFullToolResult]);

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
        if (message.seq > snapshotMaxSeq.current) arrivalIDs.current.add(blockKey(message));
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
      if (messageIdValue && delta) { arrivalIDs.current.add(messageIdValue); streamedIDs.current.add(messageIdValue); }
      const runId = typeof data.run_id === "string" ? data.run_id : "";
      if (terminalRunIDs.current.has(runId)) return;
      const botId = typeof data.bot_id === "string" ? data.bot_id : "";
      const revision = typeof data.revision === "number" ? data.revision : 0;
      const full = typeof data.content === "string" ? data.content : "";
      if (messageIdValue && (delta || full)) {
        const old = draftsRef.current[messageIdValue];
        const live = draftStream.get(messageIdValue);
        const oldRevision = live?.revision ?? old?.revision;
        if (draftDeltaApplies(oldRevision, revision, Boolean(full))) {
          const before = live?.content ?? old?.content ?? "";
          const content = full || `${before}${delta}`;
          const updatedAt = new Date().toISOString();
          const next: StreamDraft = { run_id: runId || old?.run_id || "", conversation_id: conversationId, bot_id: botId || old?.bot_id || "", message_id: messageIdValue, seq: typeof data.seq === "number" ? data.seq : old?.seq, content, status: "active", revision: revision || (oldRevision ?? 0) + 1, created_at: typeof data.created_at === "string" ? data.created_at : old?.created_at ?? updatedAt, updated_at: updatedAt };
          // Only a change the timeline's structure depends on goes through React state; plain growth of the text does not.
          const structural = !old || old.status !== "active" || old.run_id !== next.run_id || old.bot_id !== next.bot_id || old.seq !== next.seq
            || Boolean(before.trim()) !== Boolean(content.trim()) || readReplyMarker(before).ids.join() !== readReplyMarker(content).ids.join();
          if (structural) setDrafts(current => ({ ...current, [messageIdValue]: next }));
          else draftStream.setLive(messageIdValue, { content, revision: next.revision, updated_at: updatedAt });
        }
      }
    } else if (type === "run") {
      setScheduleRefresh(current => current + 1);
      const run = asRun(data);
      if (run) {
        if (run.status && isTerminalRun({ status: run.status })) terminalRunIDs.current.add(run.id);
        // A restart sends a half-streamed run back to the queue: its old draft must not stay beside the regenerated one.
        if (run.status === "queued") setDrafts((current) => Object.values(current).some(draft => draft.run_id === run.id) ? Object.fromEntries(Object.entries(current).filter(([, draft]) => draft.run_id !== run.id)) : current);
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

  // connectEvents reads the render-scoped handlers through this ref so its identity never changes; a changing
  // identity chained every old Workspace render (and its message arrays) together through keepActivityRun.
  const connectLatest = useRef<{ handleEvent: typeof handleEvent; handleWorkspaceEvent: typeof handleWorkspaceEvent; refreshWorkspaceOnConnect: typeof refreshWorkspaceOnConnect; resetWorkspaceEvents: typeof resetWorkspaceEvents; refreshQuestions: () => unknown }>(null as never);
  connectLatest.current = { handleEvent, handleWorkspaceEvent, refreshWorkspaceOnConnect, resetWorkspaceEvents, refreshQuestions: questions.refresh };
  const connectEvents = useCallback((conversationId: string) => {
    if (activeIdRef.current !== conversationId) return;
    eventSource.current?.close();
    if (reconnectTimer.current !== undefined) window.clearTimeout(reconnectTimer.current);
    if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current);
    let source: EventSource;
    setStreamConnected(false);
    const current = () => eventSource.current === source && activeIdRef.current === conversationId;
    window.clearInterval(eventWatchdog.current);
    lastFrameAt.current = Date.now();
    const failed = () => {
      source.close();
      if (!current()) return;
      window.clearInterval(eventWatchdog.current);
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
      if (current()) connectLatest.current.handleEvent(conversationId, type, data, id);
    }, failed, {
      after: workspaceEventCursor.current,
      onEvent: (data, id) => { if (current()) connectLatest.current.handleWorkspaceEvent(data, id); },
      onReset: () => { if (current()) connectLatest.current.resetWorkspaceEvents(); },
    }, () => { lastFrameAt.current = Date.now(); });
    for (const event of ["question", "question_answered", "question_cancelled", "question_updated"]) source.addEventListener(event, () => { if (current()) void connectLatest.current.refreshQuestions(); });
    source.onopen = () => {
      if (!current()) return;
      lastFrameAt.current = Date.now();
      void connectLatest.current.refreshQuestions();
      if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current);
      reconnectAttempt.current = 0;
      setStreamConnected(true);
      connectLatest.current.refreshWorkspaceOnConnect();
    };
    eventSource.current = source;
    eventFailNow.current = failed;
    eventWatchdog.current = window.setInterval(() => { if (current() && Date.now() - lastFrameAt.current > STREAM_STALE_MS) failed(); }, 5000);
    eventConnectTimer.current = window.setTimeout(failed, 10_000);
  }, []);


  // Last snapshot of each conversation the reader has seen, newest last. Switching back shows it at once
  // and reconciles with the server in the background, so the pane never blanks and then pops in.
  const snapshotCache = useRef(new Map<string, ConversationSnapshot>());
  const shownSnapshotId = useRef<string | null>(null);
  useEffect(() => {
    if (!activeId || loadedId !== activeId) return;
    const cache = snapshotCache.current;
    cache.delete(activeId);
    // Only the newest page is kept for a revisit: the reader's place older than that is paged back in on demand, so a long history costs nothing to leave.
    const trimmed = messageRecords.length > SNAPSHOT_CACHE_MESSAGES;
    cache.set(activeId, { messages: trimmed ? messageRecords.slice(-SNAPSHOT_CACHE_MESSAGES) : messageRecords, drafts: draftStream.materialize(drafts), runs, toolActivities, toolSummaries, memories, hasMore: trimmed || hasMore });
    while (cache.size > SNAPSHOT_CACHE_LIMIT) cache.delete(cache.keys().next().value as string);
  }, [activeId, loadedId, messageRecords, drafts, runs, toolActivities, toolSummaries, memories, hasMore]);
  // Pointing at a conversation (after a short dwell) or pressing its row reads its newest page into the same cache, so the first visit is a revisit.
  const prefetchInFlight = useRef(new Set<string>());
  const prefetchTimer = useRef<number | undefined>(undefined);
  const prefetchConversation = useCallback((id: string) => {
    if (id === activeIdRef.current || snapshotCache.current.has(id) || prefetchInFlight.current.has(id)) return;
    prefetchInFlight.current.add(id);
    void Promise.all([api.messages(id), api.runs(id)]).then(([page, runPage]) => {
      if (snapshotCache.current.has(id) || !page.messages.length) return;
      snapshotCache.current.set(id, { messages: page.messages.sort((a, b) => a.seq - b.seq), drafts: {}, runs: runPage.runs.sort((a, b) => +new Date(b.updated_at) - +new Date(a.updated_at)), toolActivities: [], toolSummaries: [], memories: [], hasMore: page.has_more });
      while (snapshotCache.current.size > SNAPSHOT_CACHE_LIMIT) snapshotCache.current.delete(snapshotCache.current.keys().next().value as string);
    }).catch(() => {}).finally(() => { prefetchInFlight.current.delete(id); });
  }, []);
  // The switch itself runs before paint: the pane never shows the previous conversation under the new header.
  const leftConversationId = useRef<string | null>(null);

  useLayoutEffect(() => {
    // What was being written when the reader left is what a revisit shows first.
    const left = leftConversationId.current;
    leftConversationId.current = activeId || null;
    const leftEntry = left && left !== activeId ? snapshotCache.current.get(left) : undefined;
    if (leftEntry) leftEntry.drafts = draftStream.materialize(draftsRef.current);
    if (!activeId) return;
    const cached = snapshotCache.current.get(activeId);
    setSnapshotError("");
    arrivalIDs.current.clear(); streamedIDs.current.clear();
    terminalRunIDs.current.clear();
    setLoadingOlder(false); setHistoryError(""); setError("");
    setToolDetailActivities([]); setToolDetailState({}); setToolSummaryState({});
    eventCursor.current = 0; snapshotEventCursor.current = 0; snapshotMaxSeq.current = 0; snapshotCursorKnown.current = false;
    setFullTimelineFor(null);
    if (cached) {
      for (const run of cached.runs) if (isTerminalRun(run)) terminalRunIDs.current.add(run.id);
      setMessages(cached.messages); setDrafts(cached.drafts); setRuns(cached.runs); setToolActivities(cached.toolActivities);
      setToolSummaries(cached.toolSummaries); setMemories(cached.memories); setHasMore(cached.hasMore);
      setLoadedId(activeId); setLoadingMessages(false);
      shownSnapshotId.current = activeId;
    } else {
      setMessages([]); setDrafts({}); setRuns([]); setToolActivities([]); setToolSummaries([]); setMemories([]); setHasMore(false);
      setLoadedId(null); setLoadingMessages(true);
      shownSnapshotId.current = null;
    }
  }, [activeId]);
  useEffect(() => {
    if (!activeId) return;
    let cancelled = false;
    let snapshotRetryTimer: number | undefined;
    let snapshotRetryAttempt = 0;
    let snapshotInFlight = false;
    const cached = snapshotCache.current.get(activeId);
    const loadSnapshot = () => {
      if (cancelled || snapshotInFlight) return;
      snapshotInFlight = true;
      snapshotRetryTimer = undefined;
      // A conversation already on screen from the cache is refreshed quietly.
      if (shownSnapshotId.current !== activeId) setLoadingMessages(true);
      const requests = [api.messages(activeId), api.runs(activeId), api.memories(activeId), api.toolActivities(activeId).catch(() => ({ activities: [] as ToolActivity[] }))] as const;
      void Promise.all(requests).then(([page, runPage, memoryPage, toolPage]) => {
        if (cancelled) return;
        const fresh = page.messages.sort((a, b) => a.seq - b.seq);
        // Older pages the reader already loaded stay; the fresh page replaces everything from its first message on.
        const kept = cached && fresh.length ? cached.messages.filter(message => message.conversation_id === activeId && message.seq < fresh[0].seq) : [];
        // Messages still on their way out (or failed) are not on the server yet: the refresh must not drop them.
        setMessages(current => {
          const settled = kept.length ? [...kept, ...fresh] : fresh;
          const pending = current.filter(item => item.local && item.conversation_id === activeId && !fresh.some(row => row.client_message_id && row.client_message_id === item.client_message_id));
          return pending.length ? [...settled, ...pending] : settled;
        });
        for (const run of runPage.runs) if (isTerminalRun(run)) terminalRunIDs.current.add(run.id);
        setDrafts(Object.fromEntries(page.drafts.filter((draft) => draft.status === "active" && !terminalRunIDs.current.has(draft.run_id)).map((draft) => [draft.message_id, draft])));
        setHasMore(kept.length ? cached!.hasMore : page.has_more);
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
        shownSnapshotId.current = activeId;
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
        // Never give up: a conversation opened during an outage must fill in by itself once the server is back.
        const delay = Math.min(1000 * 2 ** Math.min(snapshotRetryAttempt++, 4), 15_000);
        snapshotRetryTimer = window.setTimeout(loadSnapshot, delay);
      }).finally(() => { snapshotInFlight = false; if (!cancelled) setLoadingMessages(false); });
    };
    // Network back, tab shown or window focused: try again at once instead of waiting out the backoff.
    snapshotRetryNow.current = () => {
      if (snapshotRetryTimer === undefined || snapshotInFlight) return;
      window.clearTimeout(snapshotRetryTimer);
      snapshotRetryAttempt = 0;
      loadSnapshot();
    };
    loadSnapshot();
    return () => { cancelled = true; snapshotRetryNow.current = null; window.clearInterval(eventWatchdog.current); if (snapshotRetryTimer !== undefined) window.clearTimeout(snapshotRetryTimer); eventSource.current?.close(); eventSource.current = null; if (reconnectTimer.current !== undefined) window.clearTimeout(reconnectTimer.current); reconnectTimer.current = undefined; if (eventConnectTimer.current !== undefined) window.clearTimeout(eventConnectTimer.current); };
  }, [activeId, connectEvents]);

  // After sleep, a Wi-Fi handoff or a restart the streams may be dead without having closed. Check them the
  // moment the page can tell: the network returns, the tab is shown, the window is focused.
  useEffect(() => {
    const wake = () => {
      if (document.visibilityState === "hidden") return;
      const id = activeIdRef.current;
      if (id) {
        snapshotRetryNow.current?.();
        if (eventSource.current) {
          if (Date.now() - lastFrameAt.current > STREAM_WAKE_STALE_MS) { reconnectAttempt.current = 0; eventFailNow.current?.(); }
        } else if (shownSnapshotId.current === id) { reconnectAttempt.current = 0; connectEvents(id); }
      } else if (workspaceEventSource.current) {
        if (Date.now() - workspaceLastFrameAt.current > STREAM_WAKE_STALE_MS) { workspaceReconnectAttempt.current = 0; workspaceFailNow.current?.(); }
      } else { workspaceReconnectAttempt.current = 0; connectWorkspaceEvents(); }
    };
    window.addEventListener("online", wake);
    window.addEventListener("focus", wake);
    document.addEventListener("visibilitychange", wake);
    return () => { window.removeEventListener("online", wake); window.removeEventListener("focus", wake); document.removeEventListener("visibilitychange", wake); };
  }, [connectEvents, connectWorkspaceEvents]);

  async function loadOlder(): Promise<boolean> {
    const oldest = messageRecords.find(item => !item.local);
    if (!activeId || !oldest || loadingOlder) return false;
    const conversationId = activeId;
    setLoadingOlder(true);
    try {
      // Runs are paged like messages: the page of messages needs the runs created before the oldest one already shown.
      const [page, olderRuns] = await Promise.all([api.messages(conversationId, oldest.seq), api.runs(conversationId, oldest.created_at).catch(() => ({ runs: [] as Run[] }))]);
      if (activeIdRef.current !== conversationId) return false;
      if (olderRuns.runs.length) setRuns(current => mergeRunUpdates(current, olderRuns.runs));
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

  /** Edits one conversation's messages and runs wherever they live: on screen, or in the snapshot cache after a switch away. */
  function patchConversation(conversationId: string, changeMessages?: (list: Message[]) => Message[], changeRuns?: (list: Run[]) => Run[]) {
    if (activeIdRef.current === conversationId) {
      if (changeMessages) setMessages(changeMessages);
      if (changeRuns) setRuns(changeRuns);
      return;
    }
    const cached = snapshotCache.current.get(conversationId);
    if (cached) snapshotCache.current.set(conversationId, { ...cached, messages: changeMessages ? changeMessages(cached.messages) : cached.messages, runs: changeRuns ? changeRuns(cached.runs) : cached.runs });
  }

  /** Sends (or re-sends) one stored outgoing message. The same client_message_id on every attempt makes a retry safe. */
  async function deliver(clientMessageId: string) {
    const payload = sendPayloads.current.get(clientMessageId);
    if (!payload) return;
    const { conversationId } = payload;
    try {
      const result = await api.sendMessage(conversationId, { content: payload.content, client_message_id: clientMessageId, attachment_ids: payload.attachmentIds });
      sendPayloads.current.delete(clientMessageId);
      // This response is known human ingress, even if its run event has not arrived yet.
      const sentAt = Date.parse(result.message.created_at);
      if (Number.isFinite(sentAt)) setLastUserActivity(current => ({ ...current, [conversationId]: Math.max(current[conversationId] ?? 0, sentAt) }));
      const newRuns = result.runs?.length ? result.runs : [result.run];
      // The bubble keeps its identity (blockKey); only its content, status and runs update.
      patchConversation(conversationId, list => reconcileSent(list, { ...result.message, client_message_id: result.message.client_message_id ?? clientMessageId }), list => mergeRunUpdates(list, newRuns));
      updateConversationPreview(result.message);
      if (activeIdRef.current === conversationId) setError("");
    } catch (cause) {
      const reason = errorText(cause);
      patchConversation(conversationId, list => list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local: { state: "failed", error: reason } } : item));
    }
  }

  /** Tapping "Not sent": the same message goes out again under the same id, with nothing retyped. */
  function retrySend(clientMessageId: string) {
    const payload = sendPayloads.current.get(clientMessageId);
    if (!payload) return;
    patchConversation(payload.conversationId, list => list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local: { state: "sending" } } : item));
    void deliver(clientMessageId);
  }

  /**
   * The bubble and the cleared composer land in one commit, then the request goes out in the background. The returned
   * promise settles as soon as the message is on screen: the person may type the next one while this one is in flight.
   */
  async function send(content: string, clientMessageId: string, attachmentIds: string[] = [], draftSignature = "", flightOrigin?: DOMRect, attachments: Attachment[] = []): Promise<boolean> {
    if (!active || (!content.trim() && !attachmentIds.length)) return false;
    const conversationId = active.id;
    const text = content.trim();
    sendPayloads.current.set(clientMessageId, { conversationId, content: text, attachmentIds });
    const key = `cm:${clientMessageId}`;
    const stamp = new Date().toISOString();
    // Clear only the exact source draft that was sent.
    const clearDraft = () => { if (draftSignature) setComposerDrafts((current) => {
      const source = current[conversationId];
      if (!source || !composerDraftMatchesMessage(source, draftSignature, clientMessageId)) return current;
      const next = { ...current, [conversationId]: emptyComposerDraft() };
      if (composerStorageKey.current) writeComposerDrafts(composerStorageKey.current, next);
      return next;
    }); };
    const addLocal = (list: Message[]) => {
      if (list.some(item => item.client_message_id === clientMessageId)) return list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local: { state: "sending" as const } } : item);
      const seq = list.reduce((max, item) => item.local ? max : Math.max(max, item.seq), 0) + 1;
      return [...list, { id: `local:${clientMessageId}`, conversation_id: conversationId, seq, role: "user" as const, content: text, created_at: stamp, client_message_id: clientMessageId, attachments: attachments.length ? attachments : undefined, local: { state: "sending" as const } }];
    };
    arrivalIDs.current.add(key);
    const apply = () => { clearDraft(); patchConversation(conversationId, addLocal); };
    const animate = !isDesktop && !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    // Scrolled up in history: nothing visible moves, so there is nothing to FLIP. Return to the bottom in
    // the same task as the commit and play the arrival there, once, with the bubble in view.
    const away = viewport.awayFromBottom();
    if (animate && !away) flipList(messageListRef.current, apply, { duration: MOTION.panelIn, easing: EASE.out, last: 16 });
    else flushSync(apply);
    jumpLatest();
    if (animate) {
      const target = Array.from(messageListRef.current?.querySelectorAll<HTMLElement>(".message-block") ?? []).find(block => block.dataset.flip === key)?.querySelector<HTMLElement>(".message-user .message-content");
      if (flightOrigin && target) flySentMessage(flightOrigin, target);
    }
    setError("");
    void deliver(clientMessageId);
    return true;
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

  const refreshAfterArchive = useCallback(async () => { await refreshIndex(); }, [refreshIndex]);

  function confirmDelete(conversation: Conversation) {
    const botId = conversation.bot_id ?? botByConversationId.get(conversation.id)?.id;
    if (conversation.kind === "dm" && !botId) { setError(t("error.bot_missing_refresh")); return; }
    rememberDialogOpener();
    setDeleteTarget({ id: conversation.kind === "dm" ? botId! : conversation.id, conversationId: conversation.id, name: conversation.name, kind: conversation.kind === "dm" ? "bot" : "group", isPrimary: conversation.kind === "dm" ? Boolean(botById.get(botId!)?.is_primary) : false });
  }

  function togglePinned(conversation: Conversation) {
    const ids = sidebarIdsRef.current;
    const pinning = !conversation.pinned;
    const pinned = pinning ? [...ids.pinned, conversation.id] : ids.pinned.filter((id) => id !== conversation.id);
    // A pinned conversation leaves the list; an unpinned one comes back at the top, as a new conversation does.
    const rest = ids.unpinned.filter((id) => id !== conversation.id);
    commitSidebarLayout({ pinned, order: pinning ? rest : [conversation.id, ...rest] });
    setSidebarAnnouncement(t(pinning ? "sidebar.dnd_pinned" : "sidebar.dnd_unpinned", { name: conversation.name, pos: pinning ? pinned.length : 1, total: pinning ? pinned.length : rest.length + 1 }));
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

  async function makePrimary(botId: string) {
    try { await api.setPrimaryAssistant(botId); await refreshIndex(); setError(""); }
    catch (cause) { setError(errorText(cause)); }
  }

  async function hideConversation(conversation: Conversation) {
    try {
      if (conversation.kind === "dm") {
        const bot = botByConversationId.get(conversation.id);
        if (!bot) throw new Error(t("error.bot_missing"));
        if (bot.is_primary && otherActiveBots(bots, bot.id).length > 0) { rememberDialogOpener(); setSidebarContextMenu(null); setHidePrimaryTarget(bot); return; }
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
  const runHasActivity = (runId: string) => loadedToolActivities.some(activity => activity.run_id === runId) || terminalToolSummaries.some(summary => summary.run_id === runId && summary.tool_count > 0) || messageRecords.some(message => message.run_id === runId && message.kind === "progress");
  function toggleRunActivity(runId: string) {
    const opening = !revealedRuns.has(runId);
    setRevealedRuns(current => { const next = new Set(current); if (opening) next.add(runId); else next.delete(runId); return next; });
    if (opening) window.setTimeout(() => messageListRef.current?.querySelector<HTMLElement>(`[data-activity-run="${CSS.escape(runId)}"], [data-run-id="${CSS.escape(runId)}"]`)?.scrollIntoView({ block: "nearest", behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" }), 60);
  }
  useEffect(() => {
    // A press anywhere but on the tapped message (or its bar) closes the touch actions; the bar is mounted only while it is open.
    const close = (event: Event) => {
      const open = tappedMessageActionsId();
      if (open && (event.target as HTMLElement).closest?.(".message-hover-host, .message-actions-row")?.getAttribute("data-host-id") !== open) setMessageActions("tap", "");
    };
    document.addEventListener("pointerdown", close);
    return () => document.removeEventListener("pointerdown", close);
  }, []);
  // Message actions follow the pointer and focus through one delegated handler each: no per-message listeners, no App state.
  const messageIdOf = (target: EventTarget | null) => (target as HTMLElement | null)?.closest?.<HTMLElement>(".message-block")?.dataset.messageId ?? "";
  // Entering a message raises its bar in the same event; leaving it (into the gap between messages, or out of the list) lets go after a short grace.
  const hoverExitTimer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(hoverExitTimer.current), []);
  const onMessageListPointerOver = (event: React.PointerEvent<HTMLElement>) => {
    if (event.pointerType === "touch") return;
    const id = messageIdOf(event.target);
    window.clearTimeout(hoverExitTimer.current);
    if (id) setMessageActions("hover", id);
    else hoverExitTimer.current = window.setTimeout(() => setMessageActions("hover", ""), 120);
  };
  const onMessageListPointerLeave = (event: React.PointerEvent<HTMLElement>) => {
    if (event.pointerType === "touch") return;
    window.clearTimeout(hoverExitTimer.current);
    hoverExitTimer.current = window.setTimeout(() => setMessageActions("hover", ""), 120);
  };
  // Only keyboard focus raises the bar: pressing on a message focuses it too, and a press must not open actions by itself.
  const onMessageListFocus = (event: React.FocusEvent<HTMLElement>) => { if (event.target.matches(":focus-visible")) setMessageActions("focus", messageIdOf(event.target)); };
  const onMessageListBlur = (event: React.FocusEvent<HTMLElement>) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setMessageActions("focus", ""); };
  // The list is one tab stop (the newest message); arrows move between messages, Tab enters the focused message's bar, Escape comes back.
  const onMessageListKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    const target = event.target as HTMLElement;
    if (event.key === "Escape" && target.closest(".message-hover-actions")) { event.preventDefault(); target.closest<HTMLElement>(".message-block")?.focus(); return; }
    if (!target.matches(".message-block") || !["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
    const blocks = [...event.currentTarget.querySelectorAll<HTMLElement>(".message-block")];
    const at = blocks.indexOf(target);
    const next = blocks[event.key === "Home" ? 0 : event.key === "End" ? blocks.length - 1 : at + (event.key === "ArrowDown" ? 1 : -1)];
    if (!next) return;
    event.preventDefault();
    target.tabIndex = -1; next.tabIndex = 0;
    next.focus();
  };
  const ownerRevealed = (owner: TaskOwner) => owner.family.attempts.some(attempt => revealedRuns.has(attempt.id));
  // A block with nothing to show (Activity hidden, no card raised) draws nothing, so it must not count as a name label above a message.
  const ownerDraws = (owner: TaskOwner) => taskBlockVisible({ owner, tools: loadedToolActivities, questions: questions.items, drafts: mailDrafts.items, messages: messageRecords, summaries: terminalToolSummaries, details: toolDetailState, connected: linkConnected, hideActivity: !showActivity, revealed: ownerRevealed(owner) });
  // Computed once per render, not once per message: per-message scans made every render cubic in loaded history.
  const ownerAnchors = taskOwners.filter(ownerDraws).map(owner => ({ anchor: owner.anchor, botId: owner.family.latest.bot_id }));
  const messageRecordIDs = new Set(messageRecords.map(record => record.id));
  // Runs whose reply is on screen: the live status line gives way to it in the same frame.
  const replyingRunIDs = new Set([...Object.values(drafts).filter(draft => draft.status === "active" && draft.content.trim()).map(draft => draft.run_id), ...messageRecords.filter(record => record.role === "assistant" && record.run_id && !record.kind).map(record => record.run_id as string)]);
  const ownedAttemptKeys = new Set(taskOwners.flatMap(owner => owner.family.attempts.map(run => `${run.id}|${run.bot_id}|${run.conversation_id}`)));
  const ownedBy = (item: { run_id: string; bot_id: string; conversation_id: string }) => ownedAttemptKeys.has(`${item.run_id}|${item.bot_id}|${item.conversation_id}`);
  function renderTaskOwner(owner: TaskOwner) {
    return <TaskRunBlock key={owner.key} replying={replyingRunIDs.has(owner.family.latest.id)} hideActivity={!showActivity} revealed={ownerRevealed(owner)} owner={owner} senderHead={active?.kind === "group" ? <SenderHead id={owner.family.latest.bot_id} name={botById.get(owner.family.latest.bot_id)?.name ?? "Bot"} /> : undefined} liveStatus={runLiveStatus(owner.family.latest)} onRetryRun={retryRun} tools={loadedToolActivities} questions={questions.items} drafts={mailDrafts.items} messages={messageRecords} summaries={terminalToolSummaries} details={toolDetailState} connected={linkConnected} botName={botById.get(owner.family.latest.bot_id)?.name ?? "Bot"} showName={active?.kind === "group"}
      onOpenTools={() => openSettings("connections")}
      onFeedback={text => window.dispatchEvent(new CustomEvent("tofi:task-feedback", {detail:{conversationId:owner.family.latest.conversation_id,text}}))}
      onRefresh={refreshTaskStatus} onLoadDetails={loadToolDetails}
      renderMessage={message => <MessageBubble key={message.id} message={message} sender={botById.get(owner.family.latest.bot_id)} run={message.run_id ? runById.get(message.run_id) : undefined} showAvatar={false} showIdentity={false} />}
      renderQuestion={question => <Suspense key={question.question_id} fallback={null}><QuestionCard item={question} bot={botById.get(question.bot_id)} group={active?.kind === "group"} archived={active?.archived} onChanged={async resolved => { await questions.refresh(resolved); await refreshConversations(); }} /></Suspense>}
      renderDraft={draft => <MailDraftCard key={draft.draft_id} draft={draft} bot={botById.get(draft.bot_id)} group={active?.kind === "group"} archived={active?.archived} issueOwned onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} />} />;
  }
  const taskAnnouncements = new Map(taskOwners.map(owner => {
    const run = owner.family.latest;
    const input = {run, tools:loadedToolActivities.filter(tool => tool.run_id === run.id), questions:questions.items.filter(question => question.run_id === run.id), drafts:mailDrafts.items.filter(draft => draft.run_id === run.id), summary:terminalToolSummaries.find(summary => summary.run_id === run.id), recordsComplete:toolDetailState[run.id]?.hasMore === false, connected:linkConnected};
    const issue = presentTaskIssue({...input, family:owner.family, tools:loadedToolActivities, questions:questions.items, drafts:mailDrafts.items});
    const approved = input.questions.some(question => question.question_type === "approval" && question.status === "answered" && question.answer === true && !question.approval?.review_only) ? i18n.t("tasks:record.approved_unconfirmed") : "";
    return [run.id, issue ? [issue.title, ...issue.facts, ...issue.secondary.filter(cause => cause !== i18n.t("tasks:secondary.records_incomplete"))].join(" ") : [taskPhaseLabel(input), approved].filter(Boolean).join(" ")];
  }));

  function conversationRow(conversation: Conversation, draggable = false) {
    const bot = conversation.kind === "dm" ? botByConversationId.get(conversation.id) : undefined;
    const preview = previewText(conversation.last_message?.kind === "message_ref" ? localizeMessageRef(conversation.last_message.content) : conversation.last_message?.content?.trim() || "");
    const botId = conversation.kind === "dm" ? bot?.id || conversation.bot_id || conversation.bot_ids[0] || conversation.id : conversation.id;
    const motion = conversationMotion(conversation, bot?.id || conversation.bot_id || botId);
    const task = conversation.task_state;
    const taskGlyph = task?.status === "needs_attention" || task?.status === "waiting" ? "?" : task?.status === "failed" ? "!" : task?.status === "cancelled" ? "×" : "·";
    return <button key={conversation.id} data-sidebar-flip={conversation.id} data-dnd-id={draggable ? conversation.id : undefined} data-dnd-zone={draggable ? "list" : undefined} aria-keyshortcuts={draggable ? "Alt+ArrowUp Alt+ArrowDown" : undefined} data-hint={sidebarCollapsed ? conversation.name : undefined}
      aria-label={[conversation.name, task ? taskStateLabel(task) : "", conversation.unread_count ? t("sidebar.unread", { count: conversation.unread_count }) : ""].filter(Boolean).join(t("sidebar.label_separator"))}
      className={`contact-row conversation-row ${activeId === conversation.id ? "active" : ""}`}
      onClick={() => focusTask(conversation)}
      onPointerEnter={event => { if (event.pointerType === "mouse") { window.clearTimeout(prefetchTimer.current); prefetchTimer.current = window.setTimeout(() => prefetchConversation(conversation.id), 80); } }}
      onPointerLeave={() => window.clearTimeout(prefetchTimer.current)}
      onPointerDown={() => prefetchConversation(conversation.id)}
      onContextMenu={(event) => { event.preventDefault(); setSidebarContextMenu({ conversation, x: event.clientX, y: event.clientY }); }}>
      <Avatar label={conversation.name} id={botId} group={conversation.kind === "group"} motion={motion} />
      <span className="contact-copy"><strong><span className="contact-name">{conversation.name}</span>{bot?.is_primary && <PrimaryBadge />}</strong><small>{preview}</small></span>
      <time className="conversation-time">{conversation.last_message ? formatListTime(conversation.last_message.created_at, timezone) : ""}</time>
      {isDesktop ? (Boolean(conversation.unread_count) ? <span className="unread-badge" aria-label={t("sidebar.unread", { count: conversation.unread_count ?? 0 })}>{Math.min(conversation.unread_count ?? 0, 99)}</span> : motion === "working" ? <span className="conversation-working-indicator" aria-label={t("sidebar.working")} /> : null) : <>
        {Boolean(conversation.unread_count) && <span className="conversation-status" data-status="unread" aria-label={t("sidebar.unread", { count: conversation.unread_count ?? 0 })}>{Math.min(conversation.unread_count ?? 0, 99)}</span>}
        {task && task.status !== "completed" && <span className="conversation-task-badge" data-task-status={task.status} aria-label={taskStateLabel(task)}>{taskGlyph}</span>}
        {!task && !conversation.unread_count && motion === "working" && <span className="conversation-status" data-status="working" aria-label={t("sidebar.working")} />}
      </>}
    </button>;
  }

  function pinTile(conversation: Conversation) {
    const bot = conversation.kind === "dm" ? botByConversationId.get(conversation.id) : undefined;
    const botId = conversation.kind === "dm" ? bot?.id || conversation.bot_id || conversation.bot_ids[0] || conversation.id : conversation.id;
    const motion = conversationMotion(conversation, bot?.id || conversation.bot_id || botId);
    const task = conversation.task_state;
    const unread = conversation.unread_count ?? 0;
    return <PinTile key={conversation.id}
      tile={{ id: conversation.id, name: conversation.name, avatarId: botId, group: conversation.kind === "group", motion, active: activeId === conversation.id, unread, working: !unread && motion === "working",
        label: [conversation.name, task ? taskStateLabel(task) : "", unread ? t("sidebar.unread", { count: unread }) : ""].filter(Boolean).join(t("sidebar.label_separator")), unreadLabel: t("sidebar.unread", { count: unread }), workingLabel: t("sidebar.working") }}
      onOpen={() => focusTask(conversation)} onWarm={() => prefetchConversation(conversation.id)}
      onMenu={event => setSidebarContextMenu({ conversation, x: event.clientX, y: event.clientY })} />;
  }

  function hiddenBotRow(bot: Bot) {
    const conversation = conversations.find((item) => item.kind === "dm" && (item.bot_id === bot.id || item.id === bot.dm_conversation_id));
    if (!conversation) return null;
    return <button key={bot.id} className="contact-row conversation-row hidden-bot-row" aria-label={bot.name} onClick={() => { selectConversation(conversation.id); setMobileList(false); }} onContextMenu={(event) => { event.preventDefault(); setSidebarContextMenu({ conversation, x: event.clientX, y: event.clientY }); }}><Avatar label={bot.name} id={bot.id} motion={conversationMotion(conversation, bot.id)} /><span className="contact-copy"><strong>{bot.name}</strong><small>{t("sidebar.hidden_bot")}</small></span></button>;
  }

  async function deleteConversation(target: DeleteTarget, successorId?: string) {
    try {
      if (target.kind === "bot") await api.deleteBot(target.id, successorId);
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

  async function retryRun(id: string): Promise<void> {
    try {
      const run = await api.retryRun(id);
      setRuns((current) => mergeRunUpdates(current, [run]));
      setError("");
    } catch (cause) { setError(errorText(cause)); }
  }

  // Sender grouping reads and writes the list's DOM: run it when what the list shows changes, not on every render.
  // A visit that lands on the newest messages draws only those first (about a screen); the earlier blocks of the loaded
  // page follow in the next task, kept in place by the scroll anchor. A visit that lands elsewhere, or that has a task to
  // scroll to, draws everything.
  const timelineFrom = active && loadedId === active.id && !loadingMessages && fullTimelineFor !== active.id && viewport.opensAtBottom && !taskFocus && conversationItems.length > FIRST_PAINT_BLOCKS + 8
    ? conversationItems.length - FIRST_PAINT_BLOCKS : 0;
  const timelineWindowed = timelineFrom > 0;
  const timelineReady = !viewport.unpositioned;
  const preserveScroll = viewport.preservePrepend;
  const awayFromBottom = viewport.awayFromBottom;
  // If the newest blocks do not fill the pane (short blocks, a tall window), the rest is drawn before the first paint instead.
  useLayoutEffect(() => {
    if (!timelineWindowed || !timelineReady || !activeId) return;
    const pane = viewport.scrollRef.current;
    const first = pane?.querySelector<HTMLElement>(".message-block");
    if (pane && first && first.getBoundingClientRect().top > pane.getBoundingClientRect().top + 1) { preserveScroll(); setFullTimelineFor(activeId); }
  }, [timelineWindowed, timelineReady, activeId, preserveScroll, viewport.scrollRef]);
  useEffect(() => {
    if (!timelineWindowed || !timelineReady || !activeId) return;
    const id = activeId;
    let timer = 0;
    const frame = requestAnimationFrame(() => {
      timer = window.setTimeout(() => startTransition(() => { if (awayFromBottom()) preserveScroll(); setFullTimelineFor(id); }), 0);
    });
    return () => { cancelAnimationFrame(frame); window.clearTimeout(timer); };
  }, [timelineWindowed, timelineReady, activeId, preserveScroll, awayFromBottom]);
  const botNames = useMemo(() => new Map(bots.map(bot => [bot.id, bot.name] as const)), [bots]);
  const expiryFinishing = useMemo(() => new Set(runs.filter(run => run.finishing_reason === "approval_expired").map(run => run.id)), [runs]);
  // Rows are memoized; what they call reaches the newest closures through one object that never changes identity.
  const rowApi = useRef<RowHandlers>(null as unknown as RowHandlers);
  rowApi.current = {
    openMenu: (messageId, x, y) => setReactionMenu({ messageId, x, y }),
    closeMenu: () => setReactionMenu(null),
    hostPointerDown: () => { selectionOnPress.current = Boolean(window.getSelection()?.toString()); },
    hostClick: (event, messageId) => {
      const hadSelection = selectionOnPress.current || Boolean(window.getSelection()?.toString());
      selectionOnPress.current = false;
      const target = event.target as HTMLElement;
      if (hadSelection || !window.matchMedia("(hover: none)").matches || !target.closest(".message-content") || target.closest("a,button,summary,input,textarea,pre,[role=button],.message-hover-actions")) return;
      setMessageActions("tap", tappedMessageActionsId() === messageId ? "" : messageId);
    },
    react: (messageId, anchor) => setReactionMenu({ messageId, x: anchor.left, y: anchor.top - 6, h: anchor.height + 12 }),
    toggleActivity: runId => { setMessageActions("tap", ""); toggleRunActivity(runId); },
    reply: message => {
      if (!active) return;
      updateComposerDraft(active.id, current => ({ ...current, reply: { id: message.id, name: message.role === "user" ? t("message.you") : message.sender_bot_name ?? (message.sender_bot_id ? botById.get(message.sender_bot_id)?.name : undefined) ?? "Bot", excerpt: previewText(readReplyMarker(message.content).body) || t("message.attachment") } }));
      document.querySelector<HTMLTextAreaElement>(".composer textarea")?.focus();
    },
    retrySend: clientMessageId => retrySend(clientMessageId),
    retryRun: runId => retryRun(runId),
    loadDetails: (runId, offset) => loadToolDetails(runId, offset),
    retrySummary: runId => retryToolSummary(runId),
    navigate: id => { if (conversations.some(item => item.id === id)) { selectConversation(id); setMobileList(false); } },
    openRelated: target => openRelatedChat(target),
    draftReply: message => send(t("message.draft_reply_request", { from: message.card?.from, subject: message.card?.subject, source: message.card?.source }), crypto.randomUUID()),
    mention: bot => { if (active) setAvatarMention({ name: bot.name, conversationId: active.id, nonce: Date.now() }); },
    setReaction: async (message, emoji, present) => {
      // Optimistic: the chip changes in this frame; the server's list replaces it only if no newer toggle on this message has started.
      const conversationId = active!.id;
      const token = (reactionTokens.current.get(message.id) ?? 0) + 1;
      reactionTokens.current.set(message.id, token);
      const edit = (change: (reactions: Message["reactions"]) => Message["reactions"]) => { if (activeIdRef.current === conversationId) setMessages(current => current.map(record => record.id === message.id ? { ...record, reactions: change(record.reactions) } : record)); };
      edit(reactions => applyUserReaction(reactions, emoji, present));
      try {
        const result = await api.setMessageReaction(conversationId, message.id, emoji, present);
        if (reactionTokens.current.get(message.id) === token) edit(() => result.reactions);
      } catch (cause) {
        edit(reactions => applyUserReaction(reactions, emoji, !present));
        throw cause;
      }
    },
  };
  const rowHandlers = useMemo<RowHandlers>(() => {
    const call = <K extends keyof RowHandlers>(key: K) => ((...args: unknown[]) => (rowApi.current[key] as (...a: unknown[]) => unknown)(...args)) as unknown as RowHandlers[K];
    return { openMenu: call("openMenu"), closeMenu: call("closeMenu"), hostPointerDown: call("hostPointerDown"), hostClick: call("hostClick"), react: call("react"), toggleActivity: call("toggleActivity"), reply: call("reply"), retrySend: call("retrySend"), retryRun: call("retryRun"), loadDetails: call("loadDetails"), retrySummary: call("retrySummary"), navigate: call("navigate"), openRelated: call("openRelated"), draftReply: call("draftReply"), mention: call("mention"), setReaction: call("setReaction") };
  }, []);
  // Sender marks change the gaps between rows after the scroll position was set: the position is taken again once they are in.
  useLayoutEffect(() => { markSenderGroups(messageListRef.current); viewport.reposition(); }, [viewport.reposition, conversationItems, timelineFrom, taskOwners, toolTimeline, pendingRuns, drafts, loadingMessages, active?.id, active?.kind, active?.archived, teamBoardOpen, viewport.unreadSeq, error, historyError, hasMore, runs, toolSummaryAnchors, toolSummaryErrorAnchors, toolSummaryLoadingAnchors, toolDetailState, toolSummaryState, loadedToolActivities, terminalToolSummaries, questions.items, mailDrafts.items, activityView, bots, conversations, messageRecords]);

  if (!indexInitialized.current && workspaceError) return <BootScreen label={t("workspace.connection_unavailable")} immediate><h2>{t("workspace.connection_unavailable")}</h2><p>{workspaceError}</p><button className="primary-button" disabled={loading} onClick={() => void refreshIndex()}>{loading ? t("action.retrying") : t("common:action.retry")}</button></BootScreen>;
  if (loading && !conversations.length) return <BootScreen label={t("workspace.loading")} />;

  return (
    <div inert={Boolean(deleteTarget || hidePrimaryTarget || (viewOnlyChat && (isDesktop || compactViewport))) || undefined} className={`workspace${keyboardViewportOpen ? " keyboard-viewport-open" : ""}`} style={viewportHeight ? { "--viewport-height": `${viewportHeight}px` } as React.CSSProperties : undefined}>
      <Suspense fallback={null}><UpdateBanner /></Suspense>
      {workspaceErrorShown && indexInitialized.current && <WorkspaceToast onRetry={() => void refreshIndex()} />}
      {computerInfo && ((computerInfo.state !== "ready" && computerInfo.state !== "hibernated" && computerInfo.state !== "hibernating") || computerHealthAlert(computerInfo.health)) && panel !== "desktop" && <div className="stream-status workspace-status computer-preparing" role="status"><strong>{computerInfo.health?.state === "unresponsive" ? t("computer.health_unresponsive") : computerInfo.health?.state === "restarting" ? t("computer.health_restarting") : computerInfo.state === "starting" ? t("computer.starting") : computerInfo.state === "resuming" ? t("computer.resuming") : computerInfo.state === "error" ? t("computer.failed") : computerInfo.state === "stopped" ? t("computer.stopped") : t("computer.unavailable")}</strong><span>{(computerInfo.state === "starting" || computerInfo.state === "resuming") && computerInfo.phase ? ` · ${computerPhaseKeys[computerInfo.phase] ? t(computerPhaseKeys[computerInfo.phase]) : computerInfo.phase}` : ""}{computerInfo.error ? ` · ${computerInfo.error}` : ""}</span>{(computerInfo.state === "error" || computerInfo.state === "stopped" || computerInfo.health?.state === "unresponsive") && <button className="secondary-button" onClick={() => void api.computerRetry().then(() => api.computerInfo()).then(setComputerInfo).catch(cause => setError(errorText(cause)))}>{t("common:action.retry")}</button>}</div>}
      <div className="native-titlebar" aria-hidden="true" /><ActionHints /><div className={`workspace-grid${sidebarCollapsed ? " sidebar-collapsed" : ""}${contextPanelOpen ? " context-open" : ""}${contextPanelOpen || viewOnlyChat ? " card-open" : ""}${activeBot ? " bot-panel-ready" : ""}${panel === "bot-edit" ? " bot-panel-open" : ""}${displayedPanel === "terminal" ? " terminal-context" : ""}`}>
        <aside className={`contact-pane ${mobileList ? "open" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          <div className="sidebar-top">{sidebarCollapsed ? <button className="sidebar-logo-expand" type="button" aria-label={t("sidebar.expand")} data-hint={t("sidebar.expand")} onClick={() => setSidebarCollapsed(false)}><span className="sidebar-logo-art"><BrandLogo variant="calico" /></span><span className="sidebar-logo-arrow" aria-hidden="true"><Icon name="sidebar" size={20} /></span></button> : <BrandLogo variant="calico" />}<div className="sidebar-actions">{!sidebarCollapsed && <button className="top-icon-button sidebar-toggle" aria-label={t("sidebar.collapse")} data-hint={t("sidebar.collapse")} onClick={() => setSidebarCollapsed(true)}><Icon name="sidebar" size={20} /></button>}<div className="create-menu-anchor" ref={createMenuRef}><button ref={createMenuTriggerRef} className="icon-button" onClick={() => setCreateMenuOpen((open) => !open)} onKeyDown={arrowOpens(setCreateMenuOpen)} data-hint={t("create.menu")} aria-label={t("create.menu")} aria-haspopup="menu" aria-expanded={createMenuOpen} aria-controls={createMenuOpen ? "create-menu" : undefined}><Icon name="plus" size={18} animated /></button><input ref={botImportRef} type="file" hidden accept=".json,.tofi-bot,application/json" onChange={(event) => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importBotPackage(file); }} />{showCreateMenu && <OverlayMenu open={createMenuOpen} onOpenChange={setCreateMenuOpen} id="create-menu" className="create-menu" label={t("create.menu")} trigger={createMenuTriggerRef} anchor={createMenuRef} {...(sidebarCollapsed && matchMedia("(min-width:768px)").matches ? { side: "right", sideOffset: 12 } : { align: "end", sideOffset: 8 })}><button disabled={newBotBusy} onClick={() => { createMenuTriggerRef.current?.focus(); void createNewBot(); }}><Icon name="bot-add" size={18} /><span>{newBotBusy ? t("create.creating") : t("create.new_bot")}</span></button><button onClick={() => { createMenuTriggerRef.current?.focus(); setCreateMenuOpen(false); setPanel("group-create"); }}><Icon name="group-add" size={18} /><span>{t("create.new_group")}</span></button><button disabled={botImportBusy} onClick={() => { setCreateMenuOpen(false); botImportRef.current?.click(); }}><Icon name="upload" size={18} /><span>{botImportBusy ? t("create.importing") : t("create.import_bot")}</span></button>{(newBotError || botImportError) && <p className="error-text" role="alert">{newBotError || botImportError}</p>}</OverlayMenu>}</div></div></div>
          <label className="search-box"><Icon name="search" size={16} /><input aria-label={t("sidebar.search_label")} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("sidebar.search_placeholder")} /></label>
          <div className="contact-section" ref={sidebarListRef}>
            <PinDropZone label={t("sidebar.drop_to_pin")} hint={t("sidebar.drop_to_pin_hint")}>{pinnedConversations.length > 0 ? <PinDock label={t("sidebar.pinned")}>{pinnedConversations.map(pinTile)}</PinDock> : null}</PinDropZone>
            {Object.entries(customSidebarSections).map(([name, items]) => <section className="sidebar-custom-section" key={name}><div className="section-label"><span>{name}</span></div>{items.map(item => conversationRow(item))}</section>)}
            <div className="section-label"><span>{t("sidebar.messages")}</span></div>{defaultSidebarConversations.map(item => conversationRow(item, sidebarDndEnabled))}
            <div className="sr-only" role="status" aria-live="polite" ref={sidebarLiveRef} />
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && !query && !bots.length && !conversations.length && <FirstRunHero side onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} />}
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && <EmptyState name="sidebar" className={`empty-copy is-compact${!query && !bots.length && !conversations.length ? " first-run-hide-mobile" : ""}`} look={CATS.mochi} pose={query ? "curious" : "asleep"}><p>{query ? t("sidebar.no_match") : t("sidebar.empty")}</p>{!query && <button className="primary-button sidebar-empty-create" disabled={newBotBusy} onClick={() => void createNewBot()}>{newBotBusy ? t("create.creating") : t("create.create_bot")}</button>}</EmptyState>}
            {hiddenBots.length > 0 && <section className="hidden-bots-section"><button type="button" className="hidden-bots-toggle" aria-expanded={hiddenBotsOpen} onClick={() => setHiddenBotsOpen((open) => !open)}><span>{t("sidebar.hidden_bots")}</span><Icon name="chevron-right" size={16} /></button>{hiddenBotsOpen && hiddenBots.map(hiddenBotRow)}</section>}
          </div>
          <div className="sidebar-footer">{onboardingState === "chip" && <FinishSetupChip step={chipStep(modelReady)} onClick={() => setOnboardingForced(chipStep(modelReady))} />}<SidebarAccount connected={linkConnected} onSettings={() => { setPortabilityBotID(""); setPortabilityFile(undefined); openSettings("general", "home"); }} onUsage={() => { setUsageBotId(""); openSettings("usage"); }} onConnection={() => openSettings("models")} onArchive={() => setPanel("archive")} /></div>
        </aside>
        {onboardingState === "sheet" && onboarding && <Suspense fallback={null}><Onboarding initialStep={onboardingForced ?? resumeStep(onboarding, modelReady)} modelConfigured={modelReady} bot={bots.find((item) => !item.archived)} onProgress={saveOnboarding} onModelChanged={refreshConfig} onBot={adoptOnboardingBot} onSkip={() => { saveOnboarding({ skipped: true }); setOnboardingForced(null); }} onFinish={finishOnboarding} /></Suspense>}
        <SlotView slot={sidebarMenuSlot}>{menu => <OverlayMenu open onOpenChange={open => { if (!open) setSidebarContextMenu(null); }} className="sidebar-context-menu" label={menu.conversation.name} point={menu} key={`${menu.conversation.id}:${menu.x}:${menu.y}`}>
          <button onClick={() => togglePinned(menu.conversation)}><Icon name="pin" size={17} />{menu.conversation.pinned ? t("sidebar.unpin") : t("sidebar.pin")}</button>
          <button onClick={() => moveToSection(menu.conversation)}><Icon name="plus" size={17} />{t("sidebar.move_to_section")}</button>
          {!menu.conversation.archived && <button onClick={() => void markUnread(menu.conversation)}><Icon name="bell" size={17} />{t("sidebar.mark_unread")}</button>}
          <div className="sidebar-context-divider" />
          {menu.conversation.kind === "dm" && <button onClick={() => { selectConversation(menu.conversation.id); setMobileList(false); setPanel("bot-edit"); setSidebarContextMenu(null); }}><Icon name="edit" size={17} />{t("sidebar.rename_bot")}</button>}
          {menu.conversation.kind === "dm" && !menu.conversation.archived && botByConversationId.get(menu.conversation.id) && !botByConversationId.get(menu.conversation.id)?.is_primary && <button onClick={() => { const target = botByConversationId.get(menu.conversation.id); setSidebarContextMenu(null); if (target) void makePrimary(target.id); }}><Icon name="star" size={17} />{t("primary.make")}</button>}
          <button onClick={() => { void copyConversationLink(menu.conversation); setSidebarContextMenu(null); }}><Icon name="copy" size={17} />{t("sidebar.copy_link")}</button>
          <button onClick={() => { void navigator.clipboard?.writeText(menu.conversation.id); setSidebarContextMenu(null); }}><Icon name="copy" size={17} />{t("sidebar.copy_id")}</button>
          <button onClick={() => void (menu.conversation.archived ? restoreConversation(menu.conversation) : hideConversation(menu.conversation))}><Icon name="eye-off" size={17} />{menu.conversation.archived ? t("sidebar.show") : t("sidebar.hide")}</button>
          <button className="delete-menu-action" onClick={() => { confirmDelete(menu.conversation); setSidebarContextMenu(null); }}><Icon name="trash" size={17} />{t("sidebar.delete")}</button>
        </OverlayMenu>}</SlotView>

        <main className={`chat-pane ${!mobileList ? "mobile-chat" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          {!active ? routeUnavailable ? <div className="conversation-empty" role="alert"><EmptyCat look={CATS.nori} pose="curious" size={72} name="route-unavailable" /><h2>{t("route.unavailable_title")}</h2><p>{t("route.unavailable_body")}</p><button className="secondary-button" onClick={() => setMobileList(true)}>{t("route.back_to_list")}</button></div> : onboardingState === "sheet" ? null : <FirstRunHero onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} /> : <>
            <div className="chat-header" ref={chatHeaderRef}><div className="chat-title"><button className="back-button" onClick={() => setMobileList(true)} aria-label={t("route.back_to_list")}><Icon name="arrow-left" size={19} /></button><button className="chat-identity" onClick={() => active.kind === "group" ? setPanel("members") : transitionBotPanel(true)} aria-label={t("header.open_members")}><span className="member-avatar-stack">{(active.kind === "group" ? active.bot_ids : [active.bot_id ?? active.id]).slice(0, 4).map((id) => <Avatar key={id} label={botById.get(id)?.name ?? active.name} id={id} mini motion={conversationMotion(active, id)} />)}</span><span className="chat-name-pill"><h1>{active.name}</h1>{active.kind === "group" && <small>{t("header.member_count", { count: active.bot_ids.length })}</small>}</span></button></div><div className="header-actions">{active.kind === "group" && <button className="computer-button" data-hint={t("header.team_board")} aria-label={teamBoardOpen ? t("header.back_to_chat") : t("header.open_team_board")} aria-pressed={teamBoardOpen} onClick={() => { setPanel(null); setTeamBoardOpen(value => !value); }}><Icon name="layout-grid" size={18} variant={teamBoardOpen ? "filled" : "outline"} /></button>}{desktopBot && <button className={`computer-button${computerOn ? " is-on" : ""}${computerOn && activeDesktopOwner ? " is-driven" : ""}`} data-computer-toggle="" data-computer-state={computerOn ? (activeDesktopOwner ? "driven" : "on") : "off"} data-hint={computerStateLabel} title={computerStateLabel} aria-label={computerStateLabel} aria-pressed={panel === "desktop"} onClick={() => { if (panel === "desktop") setPanel(null); else { setDesktopSize("small"); setPanel("desktop"); } }}><Icon name="monitor" size={17} variant={panel === "desktop" ? "filled" : "outline"} animated />{computerOn && activeDesktopOwner && <span className="computer-driven-dot" aria-hidden="true" />}</button>}{desktopBot && <button className="computer-button" data-hint={t("header.terminal")} aria-label={t("header.open_terminal")} aria-pressed={panel === "terminal"} onClick={() => setPanel(current => current === "terminal" ? null : "terminal")}><Icon name="terminal" size={18} variant={panel === "terminal" ? "filled" : "outline"} /></button>}<div className="header-more"><button ref={headerMenuTriggerRef} className="ghost-button" data-hint={t("header.more")} aria-label={t("header.more")} aria-haspopup="menu" aria-expanded={headerMenuOpen} aria-controls={headerMenuOpen ? "conversation-more-menu" : undefined} onClick={() => setHeaderMenuOpen((open) => !open)} onKeyDown={arrowOpens(setHeaderMenuOpen)}><Icon name="more" size={17} /></button>{showHeaderMenu && <OverlayMenu open={headerMenuOpen} onOpenChange={setHeaderMenuOpen} id="conversation-more-menu" className="more-menu" label={t("header.more")} trigger={headerMenuTriggerRef} anchor={chatHeaderRef} align="end" sideOffset={8} alignOffset={14}><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); void copyConversationLink(active); }}>{t("header.copy_link")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setPanel("schedule"); }}>{t("header.schedule")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setPanel("memory"); }}>{active.kind === "group" ? t("header.group_memory") : t("header.bot_memory")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setUsageBotId(active.kind === "dm" ? active.bot_id ?? "" : ""); setSettingsTab("usage"); setPanel("settings"); }}>{t("header.usage")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); setRememberedOpen(true); }}>{t("header.remembered_approvals")}</button>{active.kind === "dm" && activeBot && !activeBot.archived && !activeBot.is_primary && <button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); void makePrimary(activeBot.id); }}>{t("primary.make")}</button>}{active.kind === "dm" && <button onClick={() => { headerMenuTriggerRef.current?.focus(); setPanel("bot-edit"); setHeaderMenuOpen(false); }}>{t("header.bot_settings")}</button>}<button className="delete-menu-action" onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); confirmDelete(active); }}>{active.kind === "dm" ? t("header.delete_bot") : t("header.delete_group")}</button></OverlayMenu>}</div></div></div>
            <RunStatusAnnouncement key={`run-status-${active.id}`} conversationId={active.id} runs={runs} botById={botById} ready={loadedId === active.id && !loadingMessages} taskAnnouncements={taskAnnouncements} />
            {teamBoardOpen && active.kind === "group" ? <Suspense fallback={null}><TeamBoard key={`team-board-${active.id}`} conversation={active} bots={bots} timezone={timezone} onClose={() => setTeamBoardOpen(false)} onOpenWork={() => { setTeamBoardOpen(false); setPanel("schedule"); }} /></Suspense> : <>
            <div className="chat-body">
              <div className="message-scroll" ref={viewport.scrollRef} onScroll={viewport.onScroll}>
                <ExpiryFinishingContext.Provider value={expiryFinishing}><RunStartContext.Provider value={runStarts}><ActivityViewContext.Provider value={activityView}><div className={`message-list${viewport.unpositioned ? " is-positioning" : ""}`} ref={messageListRef} onPointerOver={isDesktop ? undefined : onMessageListPointerOver} onPointerLeave={isDesktop ? undefined : onMessageListPointerLeave} onFocus={isDesktop ? undefined : onMessageListFocus} onBlur={isDesktop ? undefined : onMessageListBlur} onKeyDown={isDesktop ? undefined : onMessageListKeyDown}>
                  {viewport.boundaryError && <div className="history-error" role="alert">{t("history.boundary_failed")}<button onClick={viewport.retryBoundary}>{t("common:action.retry")}</button><button onClick={jumpLatest}>{t("history.view_latest")}</button></div>}
                  {historyError && !viewport.boundaryError && <div className="history-error history-error-older" role="alert"><span>{t("history.older_failed", { error: historyError })}</span><button onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.retrying") : t("common:action.retry")}</button></div>}
                  {hasMore && <button className="load-older" onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.loading") : t("history.load_older")}</button>}
                  {(questions.error || mailDrafts.error || snapshotError) && <div className="task-state-read-error"><span>{i18n.t("tasks:issue.status_unavailable")}</span><button type="button" className="text-button" onClick={() => void refreshTaskStatus().catch(() => setSnapshotError("read_unavailable"))}>{i18n.t("tasks:issue.action.refresh_status")}</button></div>}
                  {snapshotError && loadedId !== active.id && !loadingMessages ? <div className="inline-state" role="status" data-snapshot-unavailable="true"><div className="spinner" />{t("history.unavailable_retrying")}</div> : loadingMessages ? <DelayedFeedback key={`loading-${active.id}`}><div className={`inline-state${isDesktop ? "" : " web-loading-state"}`}>{isDesktop ? <div className="spinner" /> : <LoadingCat size={34} />}{t("history.loading_messages")}</div></DelayedFeedback> : loadedId !== active.id ? null : conversationItems.length === 0 ?
                    <EmptyState name="conversation" className="conversation-empty" look={CATS.mochi} pose="curious" size={84}><h2>{t("empty.start_chat")}</h2><p>{t("empty.start_chat_body")}</p></EmptyState> :
                    conversationItems.map((item, index) => {
                      if (index < timelineFrom) return null;
                      const previousItem = conversationItems[index - 1];
                      const previousTime = previousItem?.kind === "message" ? previousItem.message.created_at : previousItem?.kind === "question" ? previousItem.question.created_at : previousItem?.draft.created_at;
                      if (item.kind === "mail_draft") {
                        const draft = item.draft;
                        if (ownedBy(draft)) return taskOwners.filter(owner => owner.anchor?.kind === "draft" && owner.anchor.id === draft.draft_id).map(renderTaskOwner);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(draft.created_at, timezone) || +new Date(draft.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`mail-draft-${draft.draft_id}`}>{divider && <div className="date-divider"><span>{formatDateDivider(draft.created_at, timezone)}</span></div>}<MailDraftCard draft={draft} bot={botById.get(draft.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} /></div>;
                      }
                      if (item.kind === "question") {
                        const question = item.question;
                        if (ownedBy(question)) return taskOwners.filter(owner => owner.anchor?.kind === "question" && owner.anchor.id === question.question_id).map(renderTaskOwner);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(question.created_at, timezone) || +new Date(question.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`question-${question.question_id}`}>{divider && <div className="date-divider"><span>{formatDateDivider(question.created_at, timezone)}</span></div>}<Suspense fallback={null}><QuestionCard item={question} bot={botById.get(question.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async (resolved) => { await questions.refresh(resolved); await refreshConversations(); }} /></Suspense></div>;
                      }
                      const message = item.message;
                      const previous = previousItem?.kind === "message" ? previousItem.message : undefined;
                      const senderKey = message.role === "user" ? "user" : message.sender_bot_id ?? message.role;
                      const previousKey = previous ? previous.role === "user" ? "user" : previous.sender_bot_id ?? previous.role : "";
                      const sameDay = Boolean(previous && dateInTimezone(previous.created_at, timezone) === dateInTimezone(message.created_at, timezone));
                      const gap = previous ? +new Date(message.created_at) - +new Date(previous.created_at) : 0;
                      const unread = message.seq === viewport.unreadSeq;
                      // A task block (name label + Activity) directly above already names its Bot: show the name once.
                      const anchorOf = (candidate: (typeof conversationItems)[number] | undefined) => candidate?.kind === "message" ? { kind: "message" as const, id: candidate.message.id } : candidate?.kind === "question" ? { kind: "question" as const, id: candidate.question.question_id } : candidate?.kind === "mail_draft" ? { kind: "draft" as const, id: candidate.draft.draft_id } : undefined;
                      const itemTime = (candidate: (typeof conversationItems)[number] | undefined) => candidate?.kind === "message" ? candidate.message.created_at : candidate?.kind === "question" ? candidate.question.created_at : candidate?.draft.created_at;
                      const drawnAbove = conversationItems[previousRenderedIndex(conversationItems, index, candidate => (candidate.kind === "question" ? ownedBy(candidate.question) : candidate.kind === "mail_draft" ? ownedBy(candidate.draft) : false) && !precedingBlockBotIds(anchorOf(candidate), ownerAnchors).length)];
                      const blockBotIds = active.kind === "group" ? precedingBlockBotIds(anchorOf(drawnAbove), ownerAnchors) : [];
                      const blockTime = itemTime(drawnAbove);
                      const compact = Boolean(previous && !unread && !message.notice && !previous.notice && sameDay && gap < 5 * 60_000 && senderKey === previousKey) || Boolean(blockBotIds.length && message.role !== "user" && !unread && !message.notice && blockBotIds.includes(senderKey) && blockTime && dateInTimezone(blockTime, timezone) === dateInTimezone(message.created_at, timezone) && +new Date(message.created_at) - +new Date(blockTime) < 5 * 60_000);
                      const showDivider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(message.created_at, timezone) || +new Date(message.created_at) - +new Date(previousTime) >= 10 * 60_000;
                      const showIdentity = (active.kind === "group" || message.kind === "forward_result" || message.kind === "notice" && runById.get(message.run_id ?? "")?.stop_reason === "approval_expired") && message.role !== "user" && (active.kind === "group" || !compact);
                      const processTools = toolTimeline.beforeMessageId.get(message.id) ?? [];
                      const processProgress = (foldedProgress.progressByFinalId.get(message.id) ?? []).filter(note => !ownedRunIDs.has(note.run_id ?? "") && keepActivityRun(note.run_id));
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
                      const showReplyQuote = shouldShowReplyQuote(quoteTimeline, quoteIndexById.get(message.id) ?? -1, replyTarget);
                      const canReact = messageRecordIDs.has(message.id) && !message.local;
                      // "View activity" is offered on a Bot's message whose run did work (steps or progress notes), not on notices or user text.
                      const activityRunId = message.role === "assistant" && message.run_id && message.kind !== "notice" && message.kind !== "message_ref" && runHasActivity(message.run_id) ? message.run_id : undefined;
                      const receiptState = receiptFor(message, runsForMessage.get(message.id), lastReplySeq > message.seq);
                      const receiptNames = receiptState ? receiptState.readBy.map(id => botById.get(id)?.name ?? knownBotNames.current.get(id) ?? "Bot") : undefined;
                      const hasBar = !isDesktop && canReact && message.kind !== "notice" && message.kind !== "message_ref";
                      const cardLike = message.kind === "ui_card" || message.kind === "scheduled_task";
                      const copyText = !cardLike && message.content.trim() ? copyableMessageText(message) : undefined;
                      const isStub = !messageRecordIDs.has(message.id);
                      const hasProcessTools = Boolean(processTools.length || processProgress.length || processSummaries.length || Object.keys(processSummaryErrors).length || Object.keys(processSummaryLoading).length);
                      const hasAfterTools = Boolean(afterTools.length || afterSummaries.length || Object.keys(afterSummaryErrors).length || Object.keys(afterSummaryLoading).length);
                      const ownersHere = taskOwners.filter(owner => owner.anchor?.kind === "message" && owner.anchor.id === message.id);
                      const model: RowModel = {
                        message, isLast: index === conversationItems.length - 1, arrival: arrivalIDs.current.has(blockKey(message)), streamed: streamedIDs.current.has(message.id),
                        dividerLabel: showDivider ? formatDateDivider(message.created_at, timezone) : undefined, unread, timezone, canReact, isGroup: active.kind === "group", archived: Boolean(active.archived),
                        isStub, liveId: isStub || drafts[message.id]?.status === "active" ? message.id : undefined, drafting: drafts[message.id]?.status === "active" && streamConnected,
                        replyTarget: showReplyQuote ? replyTarget : undefined, replyTargetName: replyTarget?.role === "user" ? t("message.you") : replyTarget?.sender_bot_name ?? (replyTarget?.sender_bot_id ? botById.get(replyTarget.sender_bot_id)?.name ?? knownBotNames.current.get(replyTarget.sender_bot_id) : undefined),
                        senderName: message.sender_bot_name ?? (message.sender_bot_id ? knownBotNames.current.get(message.sender_bot_id) : undefined),
                        targetBot: message.notice?.to_bot_id ? botById.get(message.notice.to_bot_id) : undefined, sender: message.sender_bot_id ? botById.get(message.sender_bot_id) : undefined,
                        run: message.run_id ? runById.get(message.run_id) : undefined,
                        toolLists: hasProcessTools || hasAfterTools ? { details: toolDetailState, botById, activeRunIds } : undefined,
                        processTools: hasProcessTools ? [...processTools, ...processAggregateTools] : undefined, processProgress: hasProcessTools ? processProgress : undefined, processSummaries: hasProcessTools ? processSummaries : undefined, processSummaryErrors: hasProcessTools ? processSummaryErrors : undefined, processSummaryLoading: hasProcessTools ? processSummaryLoading : undefined,
                        afterTools: hasAfterTools ? [...afterTools, ...afterAggregateTools] : undefined, afterSummaries: hasAfterTools ? afterSummaries : undefined, afterSummaryErrors: hasAfterTools ? afterSummaryErrors : undefined, afterSummaryLoading: hasAfterTools ? afterSummaryLoading : undefined,
                        scheduleOccurrence: message.run_id ? scheduleOccurrences.get(message.run_id) : undefined, schedulePending: scheduleCardPending(message.run_id), schedule: message.run_id ? scheduleMetadata.get(scheduleOccurrences.get(message.run_id)?.schedule_id ?? "") : undefined,
                        canRetrySchedule: !(active.archived || ownedRunIDs.has(message.run_id ?? "")),
                        relatedChat: relatedChatTarget(message, active, conversations, bots), compact, showIdentity,
                        noticeTargetAvailable: !message.notice || conversations.some(item => item.id === message.notice?.target_conversation_id),
                        allowOpenDM: active.kind === "group" || message.kind === "forward_result",
                        canDraftReply: !active.archived && !isDesktop && message.card?.type === "mail", canMention: !active.archived,
                        receipt: receiptState ?? undefined, receiptNames, canRetryReceipt: Boolean(message.client_message_id),
                        hasBar, copyText, activityRunId, activityToggle: Boolean(activityRunId && !showActivity), activityOpen: activityRunId ? revealedRuns.has(activityRunId) : false, canReply: !active.archived,
                        botNames, menuPosition: reactionMenu?.messageId === message.id ? reactionMenu : null,
                        ownerNodes: ownersHere.length ? ownersHere.map(renderTaskOwner) : undefined,
                      };
                      return <MessageRow key={blockKey(message)} model={model} h={rowHandlers} stream={draftStream} />;
                    })}
                  <ToolActivityList activities={toolTimeline.fallback} summaries={terminalToolSummaries.filter(summary => toolTimeline.fallback.some(activity => activity.run_id === summary.run_id))} details={toolDetailState} onLoadDetails={loadToolDetails} botById={botById} activeRunIds={activeRunIds} group={active.kind === "group"} />
                  {taskOwners.filter(owner => !owner.anchor).map(renderTaskOwner)}
                  {!isDesktop && pendingRuns.filter(run => !ownedRunIDs.has(run.id)).map(run => <LiveRunStatus key={run.id} run={run} botName={run.kind === "triage" ? "Tofi" : botById.get(run.bot_id)?.name ?? "Bot"} showName={active.kind === "group"} />)}
                  {error && <div className="error-banner system-notice" role="alert">{error}</div>}
                </div></ActivityViewContext.Provider></RunStartContext.Provider></ExpiryFinishingContext.Provider>
              </div>
              <div className="edge-blur is-bottom" aria-hidden="true"><i /><i /></div>
              {viewport.showJump && <button className="jump-latest" aria-label={t("history.jump_latest")} onClick={jumpLatest}><Icon name="arrow-down" size={18} variant="filled" />{viewport.newCount > 0 && <span className="jump-new-count">{t("history.new_count", { count: viewport.newCount })}</span>}</button>}
            </div>
            {active.kind === "group" && !active.archived && !active.bot_ids.some(id => botById.has(id) && !botById.get(id)?.archived) ? <div className="composer empty-group-composer"><span>{t("composer.no_members")}</span><button className="secondary-button" onClick={() => setPanel("members")}>{t("composer.add_members")}</button></div> : <Composer workStatus={<WorkingMembers isGroup={active.kind === "group"} companionBotId={active.kind === "dm" ? active.bot_id || active.bot_ids[0] : undefined} companionMotion={active.kind === "dm" ? conversationMotion(active, active.bot_id || active.bot_ids[0]) : undefined} workingBotIds={active.working_bot_ids ?? []} drafts={Object.values(drafts)} runs={runs} botById={botById} toolActivities={toolActivities} signals={runSignals} connected={streamConnected} />} activeRun={runs.find(run => run.status === "running" || run.status === "queued" || run.status === "waiting")} latestReplyId={messages.filter(message => message.role === "assistant").at(-1)?.id} onStopRun={stopRun} avatarMention={avatarMention} conversation={active} bots={bots} draft={composerDrafts[active.id] ?? emptyComposerDraft()} onDraftChange={updateComposerDraft} onSend={send} onSendStart={jumpLatest} onSent={jumpLatest} modelConfigured={config?.model_configured ?? false} onConnectModel={connectModel} draftReady={composerDraftReady} readOnly={Boolean(active.archived)} />}
            </>}
          </>}
        </main>

        {modalPanelOpen && <button type="button" className="detail-backdrop" aria-label={t("detail.close")} onClick={() => { if (displayedPanel === "settings") window.dispatchEvent(new Event("tofi-settings-close")); else if (!detailPaneRef.current?.querySelector('[aria-busy="true"]')) setPanel(null); }} />}
        <aside ref={detailPaneRef} className={`detail-pane ${displayedPanel !== "settings" && displayedPanel !== "desktop" ? "context-panel" : ""} ${(panel === "bot-edit" || displayedPanel) && displayedPanel !== "desktop" ? "visible" : ""} ${!panel && displayedPanel && displayedPanel !== "desktop" ? "surface-exiting" : ""}`} inert={!panel || undefined} role={modalPanelOpen ? "dialog" : undefined} aria-modal={modalPanelOpen ? true : undefined} aria-label={panel === "settings" ? t("common:nav.settings") : panel === "terminal" ? t("header.terminal") : t("detail.label")} tabIndex={modalPanelOpen ? -1 : undefined}>
          {displayedPanel === "group-create" && <Suspense fallback={null}><BotPanel key="new-group" bots={bots.filter((bot) => !bot.archived)} onClose={() => setPanel(null)} onUpdate={updateBot} onCreateGroup={createGroup} /></Suspense>}
          {rememberedOpen && active && <Suspense fallback={null}><RememberedApprovalsSheet conversationId={active.id} onClose={() => setRememberedOpen(false)} /></Suspense>}
          {(panel === "bot-edit" || displayedPanel === "bot-edit") && <Suspense fallback={<BotPanelSkeleton onClose={() => transitionBotPanel(false)} />}><AfterPaint fallback={<BotPanelSkeleton onClose={() => transitionBotPanel(false)} />}><BotPanel onExportData={id => { setPortabilityBotID(id); setPortabilityFile(undefined); openSettings("advanced"); }} refreshToken={scheduleRefresh} memories={memories} onOpenWork={() => setPanel("schedule")} onOpenMemory={() => setPanel("memory")} key={activeBot?.id ?? "edit-empty"} bots={bots} activeBot={activeBot} onClose={() => transitionBotPanel(false)} onUpdate={updateBot} onCreateGroup={createGroup} onMakePrimary={makePrimary} /></AfterPaint></Suspense>}
          {displayedPanel === "settings" && <Suspense fallback={<SettingsSkeleton onClose={() => setPanel(null)} />}><AfterPaint fallback={<SettingsSkeleton onClose={() => setPanel(null)} />}><SettingsPanel tab={settingsTab} onTab={setSettingsTab} onClose={() => setPanel(null)} entry={settingsEntry} refreshToken={extensionRefresh} codexRefresh={codexStatusRefresh} onCodexConfigured={() => void refreshIndex()} legacyArchive={conversations.some(conversation => conversation.archived)} onOpenArchive={() => setPanel("archive")} pages={{ bots, conversation: active, timezone, usageBotId, portabilityBotID, portabilityFile, onPortabilityFileConsumed: () => setPortabilityFile(undefined), appearance, extensionRefresh, providersRefresh: codexStatusRefresh, onProvidersConfigured: () => void refreshIndex(), openTab: (tab) => openSettings(tab), onBotsChanged: () => refreshIndex() }} /></AfterPaint></Suspense>}
          {displayedPanel === "archive" && <Suspense fallback={null}><ArchivePanel onClose={() => setPanel(null)} onOpen={(id) => { selectConversation(id); setMobileList(false); setPanel(null); }} onLoaded={mergeArchived} onChanged={refreshAfterArchive} onDelete={confirmDelete} /></Suspense>}
          {displayedPanel === "terminal" && desktopBot && <Suspense fallback={<DelayedFeedback><div className="inline-state" role="status">{t("detail.loading_terminal")}</div></DelayedFeedback>}><TerminalPanel key={desktopBot.id} botId={desktopBot.id} botName={desktopBot.name} onClose={() => setPanel(null)} /></Suspense>}
          {displayedPanel === "memory" && activeId && <Suspense fallback={null}><MemoryPanel key={activeId} memories={memories} conversationId={activeId} scope={active?.kind === "group" ? "group" : "bot"} onClose={() => setPanel(null)} onCreate={async (input) => { const memory = await api.createMemory(activeId, input); setMemories((current) => [...current.filter((item) => item.id !== memory.id), memory]); }} onUpdate={async (id, input) => { const memory = await api.updateMemory(id, input); setMemories((current) => current.map((item) => item.id === id ? memory : item)); }} onDelete={async (id) => { await api.deleteMemory(id); setMemories((current) => current.filter((item) => item.id !== id)); }} /></Suspense>}
          {displayedPanel === "memory" && !activeId && <div className="detail-empty"><button className="close-button" aria-label={t("detail.close_memory")} onClick={() => setPanel(null)}><Icon name="close" size={18} /></button><EmptyState name="memory-pick" look={CATS.azuki} pose="curious" size={56}><p>{t("detail.memory_pick")}</p></EmptyState></div>}
          {displayedPanel === "schedule" && active && <Suspense fallback={null}><WorkPanel key={active.id} conversation={active} conversations={conversations} bots={bots} refreshToken={scheduleRefresh} onClose={() => setPanel(null)} onNavigate={id => { selectConversation(id); setMobileList(false); setPanel(null); }} /></Suspense>}
          {displayedPanel === "members" && active?.kind === "group" && <Suspense fallback={null}><MembersPanel refreshToken={scheduleRefresh} onOpenWork={() => setPanel("schedule")} conversation={active} bots={bots} onClose={() => setPanel(null)} onOpen={(id) => { selectConversation(id); setMobileList(false); setPanel(null); }} onSaved={updateGroup} onReload={reloadGroup} /></Suspense>}
        </aside>
      </div>
      {desktopBot && desktopOpen && <FloatingDesktop expanded={desktopExpanded} small={desktopSmall} sheet={desktopSheet} onShrink={() => setDesktopSize("small")} onClose={() => setPanel(null)}><Suspense fallback={<DesktopSkeleton botId={desktopBot.id} />}><BotDesktopPanel presence={desktopPresence} autoConnect={currentDesktopOwner?.kind === "bot"} expanded={desktopExpanded} sheet={desktopSheet} botId={desktopBot.id} botName={desktopBot.name} members={bots.map(bot => ({ id: bot.id, name: bot.name }))} onClose={(reason) => { if (reason === "shutdown") { setDesktopReady(false); setComputerInfo({ state: "stopped" }); } setPanel(null); }} onReadyChange={setDesktopReady} onExpandedChange={expanded => { setDesktopSize(expanded ? "big" : "small"); setPanel("desktop"); }} /></Suspense></FloatingDesktop>}
      {deleteTarget && <Suspense fallback={null}><DeleteConversationDialog target={deleteTarget} bots={bots} onDelete={deleteConversation} onClose={() => setDeleteTarget(null)} returnFocus={dialogOpener.current} /></Suspense>}
      {hidePrimaryTarget && <Suspense fallback={null}><ReassignPrimaryDialog bot={hidePrimaryTarget} bots={bots} onConfirm={async (successorId) => { await api.setBotArchived(hidePrimaryTarget.id, true, successorId); setSidebarContextMenu(null); await refreshIndex(); }} onClose={() => setHidePrimaryTarget(null)} returnFocus={dialogOpener.current} /></Suspense>}
      {viewOnlyChat && <Suspense fallback={null}><ViewOnlyChat target={viewOnlyChat} bots={bots} card={!isDesktop && !compactViewport} returnFocus={viewOnlyChatTriggerRef.current} onClose={() => setViewOnlyChat(null)} /></Suspense>}
      <FilePreviewHost />
    </div>
  );
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

function SmoothStreamMarkdown({ content, mention, active, streaming = false }: { content: string; mention?: React.ReactNode; active: boolean; streaming?: boolean }) {
  const visible = useSmoothStreamText(content, active);
  return <MessageMarkdown content={visible} mention={mention} cursor={active} reuse={!streaming} />;
}

const SNAPSHOT_CACHE_LIMIT = 50;
/** Blocks drawn before the first paint of a conversation that opens at its newest message. */
const FIRST_PAINT_BLOCKS = 12;
const SNAPSHOT_CACHE_MESSAGES = 60;
interface ConversationSnapshot { messages: Message[]; drafts: Record<string, StreamDraft>; runs: Run[]; toolActivities: ToolActivity[]; toolSummaries: ToolActivityRunSummary[]; memories: Memory[]; hasMore: boolean }

export function MessageBubble({ receipt, message, replyTarget, replyTargetName, sender, senderName, targetBot, run, tools, scheduleOccurrence, schedulePending, schedule, onRetrySchedule, onDraftReply, draft, relatedChat, onOpenRelatedChat, compact = false, showAvatar = true, showIdentity = true, noticeTargetAvailable = true, allowOpenDM = false, onNavigate, onMention }: { receipt?: React.ReactNode; relatedChat?: ViewOnlyChatTarget | null; onOpenRelatedChat?: (target: ViewOnlyChatTarget) => void; noticeTargetAvailable?: boolean; allowOpenDM?: boolean; onMention?: (bot: Bot) => void; message: Message; replyTarget?: Message; replyTargetName?: string; sender?: Bot; senderName?: string; targetBot?: Bot; run?: Run; tools?: React.ReactNode; scheduleOccurrence?: ScheduleOccurrence; schedulePending?: boolean; schedule?: Schedule; onRetrySchedule?: (id: string) => Promise<void>; onDraftReply?: () => Promise<boolean>; draft?: string | StreamDraft; compact?: boolean; showAvatar?: boolean; showIdentity?: boolean; onNavigate?: (id: string) => void }) {
  const { t } = useTranslation("chat");
  const {timezone} = useUserTimezone();
  const toolsRunning = run?.status === "running" || run?.status === "queued";
  const answerRef = useRef<HTMLDivElement>(null);
  const previousToolLayout = useRef<{ live: boolean; offset: number } | null>(null);
  useLayoutEffect(() => {
    if (isDesktop || !answerRef.current) return;
    // Only an answer that sat under live tool rows can slide when they go: text with no tool rows above it has nothing to measure.
    if (!tools && !previousToolLayout.current?.live) return;
    const answer = answerRef.current;
    // Measured against the message's own body, so scrolling or a change above the message can never read as the answer moving.
    const body = answer.closest<HTMLElement>(".message-body");
    const offset = answer.getBoundingClientRect().top - (body?.getBoundingClientRect().top ?? 0);
    const previous = previousToolLayout.current;
    if (previous?.live && !toolsRunning && !window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      const dy = previous.offset - offset;
      if (Math.abs(dy) > 1) answer.animate({ transform: [`translateY(${dy}px)`, "translateY(0)"] }, { duration: MOTION.panelIn, easing: EASE.out });
    }
    previousToolLayout.current = { live: toolsRunning, offset };
  });
  if (message.kind === "ui_card" && message.card) {
    const label = sender?.name ?? senderName ?? "Bot";
    return <article className={`message message-bot message-ui-card${showAvatar ? " is-group" : " is-dm"}`} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}>
      {showAvatar && <div className="message-avatar"><Avatar label={label} id={message.sender_bot_id ?? "bot"} mini /></div>}
      <div className="message-body">{showIdentity && (!compact || showAvatar) && <div className="message-meta"><strong>{label}</strong></div>}<DisplayCard card={message.card} bot={message.sender_bot_id ? { id: message.sender_bot_id, name: label } : undefined} onDraftReply={onDraftReply} />{tools && <div className="message-tools">{tools}</div>}</div>
    </article>;
  }
  if (message.kind === "scheduled_task") {
    return <ScheduledTaskRow message={message} run={run} occurrence={scheduleOccurrence} pending={schedulePending} schedule={schedule} timezone={timezone} onRetry={onRetrySchedule} />;
  }
  if (message.kind === "message_ref" && message.notice) {
    return <article className="message message-notice message-reference" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div>{relatedChat && onOpenRelatedChat ? <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} /> : <span>{localizeMessageRef(message.content)}</span>}</div></article>;
  }
  if (message.kind === "notice" && message.notice?.type === "resume") {
    // Posted by the server when a run continues from a checkpoint; the text is localized here.
    const resumeKey = message.notice.reason === "restart" ? "message.resume_restart" : "message.resume_continuing";
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div><span>{t(resumeKey)}</span></div></article>;
  }
  const assignment = message.kind === "notice" && message.sender_bot_id !== "system" && message.notice &&
    (message.notice.type === "handoff" || message.notice.type === "forward") &&
    message.notice.target_conversation_id === message.conversation_id;
  if (message.kind === "notice" && message.notice && !assignment) {
    const target = message.notice.target_conversation_id;
    const noticeSender = sender?.name;
    // The server's English sentence is the fallback; a forward between known bots is worded here, in the UI's language.
    const noticeText = message.notice.type !== "forward" || !targetBot?.name ? message.content
      : message.sender_bot_id === "system" ? t("message.routed_to", { to: targetBot.name })
      : noticeSender ? t("message.handed_over", { from: noticeSender, to: targetBot.name }) : message.content;
    const showSender = noticeSender && !noticeText.startsWith(noticeSender);
    const content = <>{showSender && <><span>{noticeSender}</span> · </>}{noticeText}</>;
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div>{noticeTargetAvailable ? <button onClick={() => onNavigate?.(target)}>{content}</button> : <span>{content}</span>}{relatedChat && onOpenRelatedChat && <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} />}</div></article>;
  }
  const isUser = message.role === "user";
  const label = message.kind === "notice" && run?.stop_reason === "approval_expired" ? t("message.system") : isUser ? t("message.you") : sender?.name ?? senderName ?? (message.role === "tool" ? t("message.tool") : "Bot");
  const draftText = typeof draft === "string" ? draft : draft?.content ?? "";
  const scheduled = isUser && run?.kind === "schedule";
  const hideMeta = compact && !scheduled && !showAvatar;
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
  if (assignment) {
    // A delegation is the colleague's brief: the chat shows one handoff line, the brief on request.
    const toName = targetBot?.name ?? t("message.member");
    return <article className="message message-notice message-assignment" data-run-id={message.run_id} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><div><span>{t("message.handed_over", { from: label, to: toName })}</span>{body.trim() && <details className="assignment-brief"><summary>{t("message.view_brief")}</summary><MessageMarkdown content={body} /></details>}</div></article>;
  }
  const mention = assignment ? <span className="message-mention"><Avatar label={targetBot?.name ?? t("message.member")} id={message.notice!.to_bot_id} mini /><span>@{targetBot?.name ?? t("message.member")}</span></span> : undefined;
  const inlineMention = mention && targetBot ? <BotIdentityCard bot={targetBot} onMention={onMention ? () => onMention(targetBot) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(targetBot.dm_conversation_id) : undefined}>{mention}</BotIdentityCard> : mention;
  const toolRow = tools && <div className="message-tools">{tools}</div>;
  const grouped = showAvatar && !isUser;
  return <article data-sender={grouped ? message.sender_bot_id : undefined} data-at={grouped ? message.created_at : undefined} className={`message ${isUser ? "message-user" : "message-bot"}${message.local?.state === "sending" ? " is-sending" : ""}${grouped ? " is-group" : !isUser ? " is-dm" : ""} ${message.kind === "segment" ? "message-segment" : ""} ${message.kind === "forward_result" ? "message-forward-result" : ""} ${compact ? "message-compact" : ""}`} title={isDesktop ? `${formatExactTime(message.created_at, timezone)} · ${timezone}` : undefined}><div className={`message-body${tools && !isDesktop ? ` web-tool-body ${toolsRunning ? "is-tools-live" : "is-tools-done"}` : ""}`}><div className={`message-meta ${hideMeta ? "compact" : ""}`}>{scheduled && <span>{t("message.scheduled")}</span>}{showIdentity && !isUser && (!compact || showAvatar) && <strong>{label}</strong>}</div>{(toolsRunning || !isDesktop) && toolRow}{replyTarget && <button type="button" className="message-reply-quote" aria-label={t("message.jump_to_reply", { name: replyTargetName ?? t("message.original") })} onClick={() => jumpToMessage(document.querySelector<HTMLElement>(`[data-message-id="${replyTarget.id}"]`))}><span className="message-reply-author">{t("message.reply_to", { name: replyTargetName ?? t("message.original") })}</span><span className="message-reply-excerpt">{previewText(readReplyMarker(replyTarget.content).body) || t("message.attachment")}</span></button>}<div className="message-content" ref={answerRef}>{showAvatar && !isUser && <div className="sender-avatar">{sender ? <BotIdentityCard bot={sender} onMention={onMention ? () => onMention(sender) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(sender.dm_conversation_id) : undefined}><Avatar label={label} id={sender.id} mini /></BotIdentityCard> : <Avatar label={label} id={message.sender_bot_id ?? "bot"} mini />}</div>}{<SmoothStreamMarkdown content={body} mention={inlineMention} active={draft !== undefined && !isDesktop} streaming={draft !== undefined} />}<AttachmentList attachments={message.attachments} compact={isUser} />{draft !== undefined && isDesktop && <span className="typing-cursor" />}</div>{receipt}{!toolsRunning && isDesktop && toolRow}{relatedChat && onOpenRelatedChat && <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} />}</div></article>;
}

/** Everything one timeline row draws, derived by the workspace. Plain data only: the row is skipped when none of it changed. */
interface RowModel {
  message: Message; isLast: boolean; arrival: boolean; streamed: boolean; dividerLabel?: string; unread: boolean; timezone: string;
  canReact: boolean; isGroup: boolean; archived: boolean;
  /** The draft whose live text this row follows, and whether the bubble shows it as still being written. */
  liveId?: string; isStub: boolean; drafting: boolean;
  replyTarget?: Message; replyTargetName?: string; senderName?: string; targetBot?: Bot; sender?: Bot; run?: Run;
  toolLists?: { details: Record<string, ToolDetailState>; botById: Map<string, Bot>; activeRunIds: Set<string> };
  processTools?: ToolActivity[]; processProgress?: Message[]; processSummaries?: ToolActivityRunSummary[]; processSummaryErrors?: Record<string, string>; processSummaryLoading?: Record<string, boolean>;
  afterTools?: ToolActivity[]; afterSummaries?: ToolActivityRunSummary[]; afterSummaryErrors?: Record<string, string>; afterSummaryLoading?: Record<string, boolean>;
  scheduleOccurrence?: ScheduleOccurrence; schedulePending: boolean; schedule?: Schedule; canRetrySchedule: boolean;
  relatedChat: ViewOnlyChatTarget | null; compact: boolean; showIdentity: boolean; noticeTargetAvailable: boolean; allowOpenDM: boolean; canDraftReply: boolean; canMention: boolean;
  receipt?: NonNullable<ReturnType<typeof receiptFor>>; receiptNames?: string[]; canRetryReceipt: boolean;
  hasBar: boolean; copyText?: string; activityRunId?: string; activityToggle: boolean; activityOpen: boolean; canReply: boolean;
  botNames: Map<string, string>; menuPosition: { messageId: string; x: number; y: number; h?: number } | null;
  ownerNodes?: React.ReactNode[];
}

/** Stable entry points a row calls; each forwards to the workspace's newest closure, so they never change identity. */
interface RowHandlers {
  openMenu: (messageId: string, x: number, y: number) => void;
  closeMenu: () => void;
  hostPointerDown: () => void;
  hostClick: (event: React.MouseEvent<HTMLElement>, messageId: string) => void;
  react: (messageId: string, anchor: DOMRect) => void;
  toggleActivity: (runId: string) => void;
  reply: (message: Message) => void;
  retrySend: (clientMessageId: string) => void;
  retryRun: (runId: string) => Promise<void>;
  loadDetails: (runId: string, offset: number) => Promise<void>;
  retrySummary: (runId: string) => void;
  navigate: (id: string) => void;
  openRelated: (target: ViewOnlyChatTarget) => void;
  draftReply: (message: Message) => Promise<boolean>;
  mention: (bot: Bot) => void;
  setReaction: (message: Message, emoji: string, present: boolean) => Promise<void>;
}

/**
 * One message in the timeline. It is skipped unless its own model changed, and the text of a message still
 * being written reaches it directly from the draft stream: a streamed chunk redraws this row and nothing else.
 */
const MessageRow = memo(function MessageRow({ model: m, h, stream }: { model: RowModel; h: RowHandlers; stream: DraftStream }) {
  const { t } = useTranslation("chat");
  const live = useLiveDraft(stream, m.liveId);
  const message = useMemo(() => m.isStub && live !== undefined ? { ...m.message, content: live } : m.message, [m.message, m.isStub, live]);
  const draft = m.drafting ? live ?? "" : undefined;
  const tools = m.processTools ? <ToolActivityList activities={m.processTools} progress={m.processProgress} summaries={m.processSummaries} summaryErrors={m.processSummaryErrors} summaryLoading={m.processSummaryLoading} details={m.toolLists!.details} onLoadDetails={h.loadDetails} onRetrySummary={h.retrySummary} botById={m.toolLists!.botById} activeRunIds={m.toolLists!.activeRunIds} /> : undefined;
  const receipt = m.receipt && (m.receipt.state === "failed" || m.receipt.state === "sending") ? <SendReceipt receipt={m.receipt} group={m.isGroup} names={m.receiptNames ?? []} onRetry={m.canRetryReceipt ? () => h.retrySend(message.client_message_id!) : undefined} /> : undefined;
  const mark = m.receipt && (m.receipt.state === "delivered" || m.receipt.state === "read") ? { state: m.receipt.state, names: m.receiptNames ?? [], group: m.isGroup } : undefined;
  const actions = m.hasBar ? <MessageActionsSlot id={message.id}><MessageHoverActions message={message} timezone={m.timezone} copyText={m.copyText} mark={mark}
      onReact={anchor => h.react(message.id, anchor)}
      onActivity={m.activityToggle && m.activityRunId ? () => h.toggleActivity(m.activityRunId!) : undefined} activityOpen={m.activityOpen}
      onReply={m.canReply ? () => h.reply(message) : undefined} /></MessageActionsSlot> : undefined;
  return <div className={`message-block${m.arrival ? ` new-arrival${m.streamed ? " was-streamed" : ""}` : ""}`} data-message-seq={message.seq} data-message-id={message.id} data-flip={blockKey(message)} tabIndex={m.isLast ? 0 : -1} onContextMenu={m.canReact && isDesktop ? event => { event.preventDefault(); h.openMenu(message.id, event.clientX, event.clientY); } : undefined}>
    {m.dividerLabel !== undefined && <div className="date-divider"><span>{m.dividerLabel}</span></div>}
    {m.unread && <div className="unread-divider" aria-label={t("history.unread_below")}><span>{t("history.unread")}</span></div>}
    <div className={`message-hover-host${message.role === "user" ? " is-user" : ""}`} data-host-id={message.id} onPointerDownCapture={!isDesktop && m.canReact ? h.hostPointerDown : undefined} onClick={!isDesktop && m.canReact ? event => h.hostClick(event, message.id) : undefined}>
      <MessageBubble receipt={receipt} message={message} replyTarget={m.replyTarget} replyTargetName={m.replyTargetName} senderName={m.senderName} targetBot={m.targetBot} sender={m.sender}
        run={m.run} tools={tools}
        scheduleOccurrence={m.scheduleOccurrence} schedulePending={m.schedulePending} schedule={m.schedule} onRetrySchedule={m.canRetrySchedule ? h.retryRun : undefined}
        draft={draft}
        relatedChat={m.relatedChat} onOpenRelatedChat={h.openRelated}
        compact={m.compact} showAvatar={m.isGroup} showIdentity={m.showIdentity} noticeTargetAvailable={m.noticeTargetAvailable} allowOpenDM={m.allowOpenDM} onDraftReply={m.canDraftReply ? () => h.draftReply(message) : undefined} onMention={m.canMention ? h.mention : undefined} onNavigate={h.navigate} />
    </div>
    {m.canReact && <MessageReactions message={message} botNames={m.botNames} menuPosition={m.menuPosition} onCloseMenu={h.closeMenu} onSet={(emoji, present) => h.setReaction(message, emoji, present)} />}
    {actions && <div className="message-actions-row" data-host-id={message.id} data-side={message.role === "user" ? "user" : "bot"} data-gutter={m.isGroup && message.role !== "user" && message.kind !== "ui_card" ? "" : undefined}>{actions}</div>}
    {m.ownerNodes}
    {m.afterTools ? <ToolActivityList activities={m.afterTools} summaries={m.afterSummaries} summaryErrors={m.afterSummaryErrors} summaryLoading={m.afterSummaryLoading} details={m.toolLists!.details} onLoadDetails={h.loadDetails} onRetrySummary={h.retrySummary} botById={m.toolLists!.botById} activeRunIds={m.toolLists!.activeRunIds} group={m.isGroup} /> : null}
  </div>;
}, (previous, next) => previous.h === next.h && previous.stream === next.stream && rowModelEqual(previous.model, next.model));

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


function ToolActivityList({ activities, progress = [], summaries = [], summaryErrors = {}, summaryLoading = {}, details = {}, onLoadDetails, onRetrySummary, botById, activeRunIds, group = false }: { group?: boolean; activities: ToolActivity[]; progress?: Message[]; summaries?: ToolActivityRunSummary[]; summaryErrors?: Record<string, string>; summaryLoading?: Record<string, boolean>; details?: Record<string, ToolDetailState>; onLoadDetails?: (runId: string, offset: number) => Promise<void>; onRetrySummary?: (runId: string) => void; botById: Map<string, Bot>; activeRunIds: Set<string> }) {
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

/** The "Context" row at the foot of a reply's activity: what the model received (D53). The view is its own chunk, fetched on first open. */
function RunContextEntry({ runId, timezone }: { runId: string; timezone: string }) {
  const { t } = useTranslation("chat");
  const [open, setOpen] = useState(false);
  return <div className="run-context-entry">
    <button type="button" className="run-context-toggle" aria-expanded={open} aria-controls={`run-context-${runId}`} onClick={() => setOpen(value => !value)}><Icon className="web-tool-chevron" name="chevron-right" size={14} /><span>{t("runContext.toggle")}</span></button>
    {open && <div id={`run-context-${runId}`}><Suspense fallback={null}><RunContextView runId={runId} timezone={timezone} /></Suspense></div>}
  </div>;
}

function WebToolActivityRun({ runId, items, notes, summary, summaryError, summaryLoading, detail, onLoadDetails, onRetrySummary, live, now, timezone, botName, senderId }: ToolRunProps) {
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
  const fullLabel = `${activityLabel}${!live && count !== undefined ? ` · ${t("tools.call_count", { count })}` : ""}${issues ? ` · ${issues}` : ""}${notes.length ? ` · ${t("tools.report_count", { count: notes.length })}` : ""}${!live && duration !== undefined ? ` · ${duration}` : ""}`;
  return <section data-activity-run={runId} className={`tool-message web-tool-run${live ? " is-live" : ` is-complete${issues ? " has-tool-issues" : ""}`}`} aria-label={t("tools.steps_label", { name: botName })} data-sender={senderId || undefined} data-at={senderId && Number.isFinite(lastTime) ? new Date(lastTime).toISOString() : undefined}>
    {senderId && <SenderHead id={senderId} name={botName} />}
    <div className="web-tool-disclosure">
      <div className="web-tool-summary">
        {!live && Number.isFinite(lastTime) && <time>{formatTime(new Date(lastTime).toISOString(), timezone)}</time>}
        <button type="button" ref={toggleRef} className="web-tool-toggle" aria-expanded={expanded} aria-controls={`web-tool-drawer-${runId}`} onClick={() => {
          const next = !expanded;
          setExpanded(next);
          if (next) { const label = labelRef.current; if (label && label.scrollWidth > label.clientWidth) setClipped(true); loadDetails(); }
        }}>
          {live && <LiveMark />}
          {!live && <Icon className="web-tool-chevron" name="chevron-right" size={14} />}
          <span ref={labelRef} className={`process-line-text${live ? " web-tool-live-label" : ""}`}>{fullLabel}</span>
          {live && <Icon className="web-tool-chevron" name="chevron-right" size={14} aria-hidden="true" />}
        </button>
      </div>
      <div className={`web-tool-drawer${expanded ? " is-open" : ""}`} id={`web-tool-drawer-${runId}`} aria-hidden={!expanded} inert={!expanded}><div className="web-tool-drawer-inner">
      {clipped && <p className="web-tool-full">{fullLabel}</p>}
      <ol className="web-tool-steps" ref={stepsRef}>{summaryLoading && <li className="web-tool-step is-note"><span>{t("tools.summary_loading")}</span></li>}{summaryError && <li className="web-tool-step is-note"><span>{t("tools.summary_failed", { error: summaryError })}</span><button type="button" className="secondary-button" onClick={() => onRetrySummary?.(runId)}>{t("common:action.retry")}</button></li>}{!entries.length && <li className="web-tool-step is-note"><span>{detail?.loading ? t("tools.details_loading") : detail?.error ? t("tools.details_failed", { error: detail.error }) : t("tools.expand_to_load")}</span></li>}{entries.map(entry => {
        if (entry.type === "note") return <li className="web-tool-step is-note" key={entry.note.id}><Icon name="file-text" size={16} /><div className="web-tool-step-copy"><span>{t("tools.progress_note")}</span><div className="web-tool-note"><MessageMarkdown content={entry.note.content} /></div></div><time>{formatExactTime(entry.note.created_at, timezone)}</time></li>;
        const activity = entry.activity;
        const duration = webToolSeconds(activity, now);
        const active = live && (activity.status === "running" || activity.status === "queued");
        return <li className={`web-tool-step is-${toolDisplayState(activity)}`} key={toolActivityKey(activity)}>
          <Icon name={toolStepIcon(toolStepLabel(activity))} size={16} />
          <details className="web-tool-step-detail" onToggle={event => { if (event.currentTarget.open) requestFullToolResult(activity); }}><summary><span className="web-tool-step-label">{toolStepLabel(activity)}{toolArgumentPreview(activity) ? ` · ${toolArgumentPreview(activity)}` : ""}</span></summary>
            <div className="tool-activity-details"><small>{Number.isFinite(Date.parse(activity.started_at)) ? formatExactTime(activity.started_at, timezone) : ""} · {timezone}</small><div><span>{t("tools.arguments")}</span><pre>{activity.arguments || t("tools.none")}</pre></div><div><span>{t("tools.result")}</span>{activity.outcome && <p className="tool-outcome" role="note">{activity.outcome.message}</p>}<pre>{activity.result || (active ? t("tools.waiting_result") : t("tools.none"))}</pre></div>{activity.truncated && <small>{t("tools.truncated")}</small>}</div>
          </details>
          <span className="web-tool-step-meta">{active ? <span className="web-tool-breath" aria-hidden="true" /> : <Icon name={activity.status === "completed" ? "check" : toolDisplayState(activity) !== activity.status ? "clock" : "alert"} size={14} variant="filled" />}{toolDisplayLabel(activity)}{duration === undefined ? "" : ` · ${duration.toFixed(1)}s`}</span>
        </li>;
      })}{hasMoreDetails && <li className="web-tool-step is-note"><button type="button" className="secondary-button" disabled={detail?.loading} onClick={loadDetails}>{detail?.loading ? t("action.loading") : t("tools.load_more", { shown: items.length, total: count ?? "?" })}</button></li>}</ol>
      <RunContextEntry runId={runId} timezone={timezone} />
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
            <details className="v2-tool-step-detail" onToggle={event => { if (event.currentTarget.open) requestFullToolResult(activity); }}><summary><span className="v2-tool-step-marker" aria-hidden="true" /><strong>{activity.name}</strong>{toolArgumentPreview(activity) && <span className="v2-tool-step-argument">{toolArgumentPreview(activity)}</span>}<span className="v2-tool-step-state">{toolDisplayLabel(activity)}</span><time>{duration === undefined ? "" : activity.status === "running" ? `${duration}s…` : `${duration}s`}</time></summary>
              <div className="tool-activity-details"><small>{Number.isFinite(Date.parse(activity.started_at)) ? formatExactTime(activity.started_at, timezone) : ""} · {timezone}</small><div><span>{t("tools.arguments")}</span><pre>{activity.arguments || t("tools.none")}</pre></div><div><span>{t("tools.result")}</span>{activity.outcome && <p className="tool-outcome" role="note">{activity.outcome.message}</p>}<pre>{activity.result || (activity.status === "running" || activity.status === "queued" ? t("tools.waiting_result") : t("tools.none"))}</pre></div>{activity.truncated && <small>{t("tools.truncated")}</small>}</div>
            </details>
          </li>;
        })}{detail?.hasMore && (count === undefined || items.length < count) && <li className="v2-progress-note"><button type="button" className="secondary-button" disabled={detail.loading} onClick={() => void onLoadDetails?.(runId, detail.loaded)}>{detail.loading ? t("action.loading") : t("tools.load_more", { shown: items.length, total: count ?? "?" })}</button></li>}</ol>
      </details>
    </section>;
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

/** True when `next` is `previous` with text inserted somewhere and nothing removed or replaced. */
function isPureInsertion(previous: string, next: string) {
  if (next.length < previous.length) return false;
  let start = 0;
  while (start < previous.length && previous[start] === next[start]) start++;
  let end = 0;
  while (end < previous.length - start && previous[previous.length - 1 - end] === next[next.length - 1 - end]) end++;
  return start + end >= previous.length;
}

function Composer({ workStatus, activeRun, latestReplyId, onStopRun, avatarMention, conversation, bots, draft, onDraftChange: updateDraftFor, onSend, onSendStart, onSent, modelConfigured, onConnectModel, draftReady, readOnly = false }: { workStatus: React.ReactNode; activeRun?: Run; latestReplyId?: string; onStopRun: (id: string) => Promise<void>; avatarMention: { name: string; conversationId: string; nonce: number } | null; conversation: Conversation; bots: Bot[]; draft: ComposerDraft; onDraftChange: (conversationId: string, update: (draft: ComposerDraft) => ComposerDraft) => void; onSend: (content: string, clientMessageId: string, attachmentIds?: string[], draftSignature?: string, flightOrigin?: DOMRect, attachments?: Attachment[]) => Promise<boolean>; onSendStart?: () => void; onSent: () => void; modelConfigured: boolean; onConnectModel: () => void; draftReady: boolean; readOnly?: boolean }) {
  const { t } = useTranslation("chat");
  const [sending, setSending] = useState(false);
  const [fileError, setFileError] = useState("");
  const [uploadingKey, setUploadingKey] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dropOrigin = useRef<{ x: number; y: number; name: string } | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);
  const imageRef = useRef<HTMLInputElement | null>(null);
  const addMenuRef = useRef<HTMLDivElement | null>(null);
  const addMenuTriggerRef = useRef<HTMLButtonElement | null>(null);
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
  // The text being typed lives here, not in App: a keystroke re-renders only the composer. App's draft store
  // receives it debounced and on blur / send / conversation switch / page hide (see flushContent).
  const [typed, setTyped] = useState({ id: conversation.id, text: draft.content, synced: draft.content });
  let typedNow = typed;
  if (typed.id !== conversation.id) typedNow = { id: conversation.id, text: draft.content, synced: draft.content };
  // Content changed in App by something other than this composer (restored draft, send acknowledgement): adopt it.
  else if (draft.content !== typed.synced) typedNow = { ...typed, text: draft.content, synced: draft.content };
  if (typedNow !== typed) setTyped(typedNow);
  const typedRef = useRef(typedNow);
  typedRef.current = typedNow;
  const unsyncedRef = useRef<{ id: string; text: string } | null>(null);
  const flushTimer = useRef<number | undefined>(undefined);
  const updateDraftForRef = useRef(updateDraftFor);
  updateDraftForRef.current = updateDraftFor;
  // Bound to this render's conversation: an upload that finishes after a switch must edit the draft it started in.
  const onDraftChange = (update: (draft: ComposerDraft) => ComposerDraft) => updateDraftFor(conversation.id, update);
  const flushContent = useCallback((background = false) => {
    window.clearTimeout(flushTimer.current);
    const pending = unsyncedRef.current;
    if (!pending) return;
    unsyncedRef.current = null;
    // `synced` and App's draft must change in the same render, or the composer would take its own flush for an outside edit.
    const push = () => {
      updateDraftForRef.current(pending.id, (current) => current.content === pending.text ? current : { ...current, content: pending.text });
      if (typedRef.current.id === pending.id) setTyped((current) => current.id === pending.id ? { ...current, synced: pending.text } : current);
    };
    // An idle-time flush may yield to the next keystroke; the forced ones (blur, send, switch, hide) may not.
    if (background) startTransition(push); else push();
  }, []);
  // Typed or dictated text; `now` commits to the draft store immediately.
  const setContent = useCallback((text: string, now = false) => {
    const id = conversationIdRef.current;
    unsyncedRef.current = { id, text };
    setTyped((current) => current.id === id ? { ...current, text } : current);
    typedRef.current = { ...typedRef.current, text };
    window.clearTimeout(flushTimer.current);
    if (now) flushContent(); else flushTimer.current = window.setTimeout(() => flushContent(true), 400);
  }, [flushContent]);
  useLayoutEffect(() => {
    const id = conversation.id;
    return () => { if (unsyncedRef.current?.id === id) flushContent(); };
  }, [conversation.id, flushContent]);
  useEffect(() => {
    const hide = () => flushContent();
    const visibility = () => { if (document.visibilityState === "hidden") flushContent(); };
    window.addEventListener("pagehide", hide);
    document.addEventListener("visibilitychange", visibility);
    return () => { window.removeEventListener("pagehide", hide); document.removeEventListener("visibilitychange", visibility); };
  }, [flushContent]);
  const content = typedNow.text;
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
    layerRoot().append(ghost);
    pill.style.visibility = "hidden";
    const dx = origin.x - to.left - to.width / 2;
    const dy = origin.y - to.top - to.height / 2;
    void ghost.animate([
      { transform:`translate(${dx}px,${dy}px) rotate(-8deg) scale(1.1)` },
      { transform:`translate(${dx * .45}px,${dy * .33 - 40}px) rotate(-4deg) scale(1.04)`, offset:.6 },
      { transform:"translate(0,0) scale(1.06,.9)", offset:.9 },
      { transform:"translate(0,0) scale(1)" },
    ], { duration:MOTION.flight, easing:EASE.inout }).finished.finally(() => { ghost.remove(); pill.style.visibility = ""; });
  }, [attachments]);
  // The @ menu lives in #tofi-layers (so the composer's stacking context cannot cap it): place it in viewport terms.
  useLayoutEffect(() => {
    if (mentionQuery === null || mentionStart < 0 || mentionMatches.length === 0) return;
    const textarea = textareaRef.current;
    const wrap = mentionWrapRef.current;
    if (!textarea || !wrap) return;
    const bounds = wrap.getBoundingClientRect(), floor = document.documentElement.clientHeight;
    if (isDesktop) { setMentionAnchor({ left: bounds.left, bottom: floor - bounds.top + 8 }); return; }
    const caret = textareaCaretRect(textarea, mentionStart);
    setMentionAnchor({ left: bounds.left + Math.max(0, Math.min(caret.left - bounds.left - 12, bounds.width - 300)), bottom: floor - bounds.bottom + Math.max(8, bounds.bottom - caret.top + 8) });
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
    onChange: (value) => { setContent(value, true); setMentionQuery(null); },
  });
  useEffect(() => { dictation.cancel(); }, [conversation.id, dictation.cancel]);
  useEffect(() => {
    if (!avatarMention || avatarMention.conversationId !== conversation.id || readOnly) return;
    const textarea = textareaRef.current;
    const text = typedRef.current.text;
    const start = textarea?.selectionStart ?? text.length;
    const end = textarea?.selectionEnd ?? start;
    const prefix = start > 0 && !/\s/.test(text[start - 1]) ? " " : "";
    const mention = `${prefix}@${avatarMention.name} `;
    setContent(text.slice(0, start) + mention + text.slice(end), true);
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
    const shortcut = (event: KeyboardEvent) => {
      if (!canCompose || sending || !event.metaKey || !event.shiftKey) return;
      if (event.key.toLowerCase() === "i") { event.preventDefault(); imageRef.current?.click(); }
      if (event.key.toLowerCase() === "f") { event.preventDefault(); fileRef.current?.click(); }
    };
    document.addEventListener("keydown", shortcut);
    return () => document.removeEventListener("keydown", shortcut);
  }, [canCompose, sending]);
  const fittedRef = useRef<{ id: string; text: string } | null>(null);
  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    if (!textarea) return;
    const previous = fittedRef.current;
    fittedRef.current = { id: conversation.id, text: content };
    // Typing only adds text, and added text never makes the wrapped content shorter. If it still fits the current
    // box there is nothing to resize, so skip the reset-to-auto that costs an extra forced layout on every key.
    if (previous && previous.id === conversation.id && textarea.style.height && isPureInsertion(previous.text, content)) {
      const fits = textarea.scrollHeight <= textarea.clientHeight;
      if (fits || (textarea.clientHeight >= 120 && textarea.style.overflowY === "auto")) return;
    }
    // One forced layout: the content height is read once and reused for the overflow decision.
    textarea.style.height = "auto";
    const contentHeight = textarea.scrollHeight;
    textarea.style.height = `${Math.max(48, Math.min(contentHeight, 120))}px`;
    textarea.style.overflowY = contentHeight > 120 ? "auto" : "hidden";
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
    flushContent();
    // The list is at the newest message before anything else happens; the bubble then arrives into view.
    onSendStart?.();
    onDraftChange((current) => ({ ...current, pending: { signature, id: clientMessageId } }));
    setSending(true);
    const attachmentIds: string[] = [];
    const sentAttachments: Attachment[] = [];
    try {
      for (const attachment of attachments) {
        if (!isCurrent()) return;
        let id = attachment.id;
        let uploaded: Attachment | undefined;
        if (!id) {
          if (!attachment.file) throw new Error(t("composer.reselect_file", { name: attachment.name }));
          setUploadingKey(attachment.key);
          uploaded = await api.uploadAttachment(conversationId, attachment.file);
          id = uploaded.id;
          if (isCurrent()) setUploadingKey(null);
          onDraftChange((current) => ({ ...current, attachments: current.attachments.map((item) => item.key === attachment.key ? { ...item, id } : item) }));
        }
        attachmentIds.push(id);
        sentAttachments.push(uploaded ?? { id, conversation_id: conversationId, name: attachment.name, mime: attachment.type, size: attachment.size, created_at: new Date().toISOString() });
      }
    } catch (cause) {
      if (isCurrent()) { setFileError(errorText(cause)); setSending(false); setUploadingKey(null); }
      return;
    }
    if (!isCurrent()) return;
    const flightOrigin = !isDesktop ? textareaRef.current?.getBoundingClientRect() : undefined;
    const sent = await onSend(draftContent, clientMessageId, attachmentIds, signature, flightOrigin, sentAttachments);
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
    setContent(next, true);
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
      layerRoot().append(ghost);
      void ghost.animate([
        { transform: "translate(0,0) scale(.9)", opacity: 1 },
        { transform: `translate(${(to.left - from.left) * .5}px, ${Math.min(from.top,to.top) - from.top - 38}px) scale(1.08)`, opacity: 1, offset:.45 },
        { transform: `translate(${to.left - from.left}px, ${to.top - from.top}px) scale(1)`, opacity: 0 },
      ], { duration:MOTION.panelIn, easing: EASE.out }).finished.finally(() => ghost.remove());
    });
  }
  function updateContent(value: string, cursor: number) {
    setContent(value);
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
  // Measured before paint: the message pane's bottom padding follows the composer, so a late (post-paint)
  // measurement moved the whole conversation by the composer's height right after every switch.
  useLayoutEffect(() => {
    const form = textareaRef.current?.closest("form");
    // Measured on the workspace so the floating desktop, mounted outside the chat pane, docks above the composer too.
    const pane = form?.closest<HTMLElement>(".workspace") ?? form?.closest<HTMLElement>(".chat-pane");
    if (!form || !pane) return;
    // The header height too: on the web the message pane runs up under the header's frosted glass (progressive-blur.css).
    const header = pane.querySelector<HTMLElement>(".chat-header");
    const measure = () => {
      pane.style.setProperty("--composer-height", `${form.getBoundingClientRect().height}px`);
      if (header) pane.style.setProperty("--chat-header-height", `${header.getBoundingClientRect().height}px`);
    };
    const observer = new ResizeObserver(measure);
    observer.observe(form, { box: "border-box" }); if (header) observer.observe(header, { box: "border-box" }); measure();
    return () => observer.disconnect();
  }, [conversation.id]);
  // The measurement stays across a conversation switch (the next composer re-measures in the same commit) and goes with the composer.
  useEffect(() => () => { const workspace = document.querySelector<HTMLElement>(".workspace"); workspace?.style.removeProperty("--composer-height"); workspace?.style.removeProperty("--chat-header-height"); }, []);
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
  return <form className={`composer${dragging ? " is-dragging" : ""}${!modelConfigured ? " is-no-model" : ""}${!content.trim() && !attachments.length && !sending ? " is-send-empty" : ""}`} onSubmit={(event) => void submit(event)}>{readOnly ? <div className="composer-tools"><span className="config-warning">{t("composer.archived")}</span></div> : !draftReady ? <DelayedFeedback><div className="composer-tools"><span className="config-warning">{t("composer.restoring_draft")}</span></div></DelayedFeedback> : !modelConfigured && <ModelBanner botId={conversation.kind === "dm" ? conversation.bot_id || conversation.bot_ids[0] : undefined} name={conversation.name} onConnect={onConnectModel} />}{dictation.permissionNeeded && <Suspense fallback={null}><PermissionCoach permissions={["microphone"]} force onDismiss={dictation.clearPermission} /></Suspense>}{!isDesktop && dictation.starting && <div className="dictation-status is-starting" role="status">{t("composer.mic_connecting")}</div>}{dictation.error && !dictation.permissionNeeded && <div className="dictation-status" role="status">{dictation.error}</div>}<input ref={imageRef} type="file" accept="image/*" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} /><input ref={fileRef} type="file" hidden multiple onChange={(event) => { addFiles(event.target.files); event.currentTarget.value = ""; }} />{workStatus}{!isDesktop && <WebFileDropOverlay key={conversation.id} inputRef={textareaRef} enabled={canCompose && !sending && !dictation.listening && !dictation.busy} onDrop={(files, origin) => { dropOrigin.current = { ...origin, name:files[0].name }; addFiles(files); }} />}<div className="composer-row">{!isDesktop && uploadingKey && <span className="web-upload-ring" aria-hidden="true" />}{!isDesktop && !readOnly && conversation.kind === "dm" && (conversation.bot_id || conversation.bot_ids[0]) && <ComposerPerch key={conversation.id} botId={conversation.bot_id || conversation.bot_ids[0]} sending={sending} working={activeRun?.status === "running"} latestReplyId={latestReplyId} />}{!(dictation.listening || dictation.busy) && <div className="composer-add-wrap" ref={addMenuRef}><button ref={addMenuTriggerRef} type="button" className="secondary-button" aria-label={t("composer.add_attachment")} aria-expanded={addMenuOpen} aria-haspopup="menu" disabled={sending || !canCompose} onClick={() => setAddMenuOpen((open) => !open)} onKeyDown={arrowOpens(setAddMenuOpen)}><Icon name="plus" size={22} /></button>{addMenuOpen && <OverlayMenu open onOpenChange={setAddMenuOpen} className="composer-add-menu" label={t("composer.add_attachment")} trigger={addMenuTriggerRef} anchor={addMenuRef} side="top" sideOffset={12}><button type="button" onClick={() => { setAddMenuOpen(false); imageRef.current?.click(); }}><Icon className="composer-add-icon" name="image" size={20} />{t("composer.image")}<kbd>⌘⇧I</kbd></button><button type="button" onClick={() => { setAddMenuOpen(false); fileRef.current?.click(); }}><Icon className="composer-add-icon" name="file" size={20} />{t("composer.file")}<kbd>⌘⇧F</kbd></button><button type="button" onClick={() => void captureScreen()}><span className="composer-add-icon"><ComposerGlyph name="screenshot" size={20} /></span>{t("composer.screenshot")}</button><p>{t("composer.drop_hint")}</p></OverlayMenu>}</div>}<div ref={mentionWrapRef} className={`composer-input-wrap${mentionParts ? " has-mentions" : ""}`}>{reply && !readOnly && <div className="composer-reply" role="status"><Icon name="reply" size={15} /><span className="composer-reply-copy"><strong>{t("composer.replying_to", { name: reply.name })}</strong><span>{reply.excerpt}</span></span><button type="button" aria-label={t("composer.cancel_reply")} data-hint={t("composer.cancel_reply")} onClick={() => { onDraftChange(current => ({ ...current, reply: undefined })); textareaRef.current?.focus(); }}><Icon name="close" size={14} /></button></div>}{attachments.length > 0 && <div className="composer-attachments">{attachments.map((attachment) => <span key={attachment.key} className="composer-attachment-pill" data-name={attachment.name}><span className="composer-attachment-type" aria-hidden="true">{attachment.type.startsWith("image/") ? "IMG" : (attachment.name.split(".").pop() || "FILE").slice(0, 4).toUpperCase()}</span><span className="composer-attachment-name">{attachment.name}</span>{uploadingKey === attachment.key && ` · ${t("composer.uploading")}`}{!attachment.file && !attachment.id && ` · ${t("composer.reselect")}`}<button type="button" aria-label={t("composer.remove_attachment", { name: attachment.name })} disabled={sending || !canCompose || dictation.listening || dictation.busy} onClick={() => onDraftChange((current) => ({ ...current, attachments: current.attachments.filter((item) => item.key !== attachment.key) }))}><Icon name="close" size={18} /></button></span>)}</div>}{mentionParts && <div className="composer-rich-text" aria-hidden="true"><div style={{ transform:`translateY(-${composerScrollTop}px)` }}>{mentionParts}</div></div>}<textarea ref={textareaRef} aria-label={t("composer.input_label", { name: conversation.name })} value={content} onPaste={event => { const files = Array.from(event.clipboardData.items).filter(item => item.kind === "file").map(item => item.getAsFile()).filter((file): file is File => file !== null); if (files.length) { event.preventDefault(); addFiles(files); } }} onKeyDown={handleKeyDown} onScroll={(event) => setComposerScrollTop(event.currentTarget.scrollTop)} onBlur={() => flushContent()} onChange={(event) => updateContent(event.target.value, event.target.selectionStart)} placeholder={dictation.listening ? t("composer.placeholder_recording") : dictation.busy ? t("composer.placeholder_transcribing") : readOnly ? t("composer.placeholder_archived") : !draftReady ? t("composer.placeholder", { name: conversation.name }) : canCompose ? t("composer.placeholder", { name: conversation.name }) : t("composer.placeholder_no_model")} disabled={!canCompose || sending || dictation.listening || dictation.busy} rows={1} />{mentionQuery !== null && mentionMatches.length > 0 && createPortal(<div ref={mentionMenuRef} className="mention-menu" role="listbox" aria-label={isDesktop ? undefined : t("composer.mention_label", { name: conversation.name })} style={mentionAnchor ? { left:mentionAnchor.left, bottom:mentionAnchor.bottom } : undefined}>{!isDesktop && <div className="mention-menu-heading" role="presentation">{t("composer.mention_heading", { name: conversation.name })}</div>}{!isDesktop && mentionPill && <span className="web-mention-pill" aria-hidden="true" style={{ top:mentionPill.top, height:mentionPill.height }} />}{mentionMatches.map((bot, index) => <button type="button" role="option" aria-selected={index === mentionIndex} className={index === mentionIndex ? "selected" : ""} key={bot.id} onMouseEnter={() => setMentionIndex(index)} onMouseDown={(event) => { event.preventDefault(); chooseMention(bot); }}><BotAvatar id={bot.id} mini /><span className="mention-name">{mentionQuery && bot.name.toLocaleLowerCase().startsWith(mentionQuery.toLocaleLowerCase()) ? <><mark>{bot.name.slice(0, mentionQuery.length)}</mark>{bot.name.slice(mentionQuery.length)}</> : bot.name}</span></button>)}{!isDesktop && <div className="mention-menu-hint" role="presentation">{t("composer.mention_hint")}</div>}</div>, layerRoot())}</div>{dictation.listening || dictation.busy ? <DictationControls elapsed={dictation.elapsed} levels={dictation.levels} busy={dictation.busy} onCancel={dictation.cancel} onConfirm={dictation.confirm} /> : <>{dictation.supported && <button type="button" className={`dictation-button${!isDesktop && dictation.starting ? " is-starting" : ""}`} aria-label={!isDesktop && dictation.starting ? t("composer.cancel_mic") : t("composer.dictate")} title={!isDesktop && dictation.starting ? t("composer.cancel_mic") : dictation.notice || t("composer.dictate")} disabled={!dictation.starting && (sending || !canCompose)} onClick={!isDesktop && dictation.starting ? dictation.cancel : dictation.start}><ComposerGlyph name="mic" size={20} /></button>}{activeRun && !readOnly && !content.trim() && !attachments.length && <button type="button" className="composer-stop" aria-label={t("composer.stop_label", { name: bots.find(bot => bot.id === activeRun.bot_id)?.name ?? "Bot" })} disabled={!canCompose} onClick={() => void onStopRun(activeRun.id)}><Icon name="stop" size={16} variant="filled" />{t("composer.stop")}</button>}{(Boolean(content.trim()) || attachments.length > 0 || sending) && <button className="send-button" aria-label={t("composer.send")} disabled={(!content.trim() && !attachments.length) || !canCompose || sending || dictation.listening || dictation.busy}>{sending ? "…" : <Icon name="arrow-up" size={26} variant="filled" />}</button>}</>}</div>{fileError && <div className="error-text" role="alert">{fileError}</div>}</form>;
}

export default App;
export { WorkingMembers, Composer, DictationControls, WebToolActivityRun, CompactLiveLine, RunStartContext };
