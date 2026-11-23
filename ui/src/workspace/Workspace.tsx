// Bare imports keep the stylesheet order of the entry bundle: these modules' CSS loads at this point.
import "../MessageHoverActions";
import { FocusScope } from "@radix-ui/react-focus-scope";
import { keepFocus } from "../focusModality";
import { useNativeSidePanel } from "../useNativeSidePanel";
import { useBotBrowserSources } from "../useBotBrowserSources";
import { sourceKey, type ComputerSource } from "../botBrowserSource";
import { useGraceFlag } from "../useGraceFlag";
import { readReplyMarker } from "../replyMarker";
import { thinkingHeadline } from "../thinkingHeadline";
import { TaskRunBlock, taskBlockVisible } from "../TaskRunBlock";
import "../LiveMark";
import { markSenderGroups } from "../senderGroups";
import { sameData } from "../sameData";
import { buildTaskOwners, presentTaskIssue, taskPhaseLabel, type TaskOwner } from "../taskIssuePresentation";
import { i18n, useTranslation } from "../i18n";
import { placeOwners, type OwnerPlacement } from "../activityPlacements";
import { mergeMessageTimeline } from "../messageTimeline";
import { DelayedFeedback } from "../DelayedFeedback";
import { BootScreen } from "../BootScreen";
import { useOwnerSession } from "../OwnerSession";
import { desktopState, updateDesktopState, isDesktop, type DesktopCommand } from "../desktop";
import { useUserTimezone } from "../UserTimezone";
import { browserNotifier, type DeliveryItem } from "../attentionNotifier";
import "../MessageAttachment";
import { FilePreviewHost } from "../FileViewer";
import "../MessageReactions";
import { applyUserReaction } from "../reactionState";
import { applyMessageTask } from "../messageLink";
import { setMessageActions, tappedMessageActionsId } from "../messageActionsStore";
import "../DisplayCard";
import { useQuestions } from "../useQuestions";
import { toolActivityKey } from "../toolTimeline";
import { setToolResultLoader } from "../toolResultLoader";
import { taskStateLabel } from "../ConversationTaskStatus";
import { rowIndicator } from "../rowIndicator";
import { useMailDrafts } from "../mailDrafts";
import { buildQuestionTimeline } from "../questionTimeline";
import type { SettingsEntry, SettingsTab, SettingsView } from "../SettingsShell";
import { AfterPaint, CoverShell, CoverView, SidebarShell, SidebarView, WidgetColumn, WidgetView, whenPanelLoaded, UpdateBanner, BotDesktopPanel, DeleteConversationDialog, ReassignPrimaryDialog, SearchPalette, RememberedApprovalsSheet, DesktopSkeleton, ViewOnlyChat, Onboarding, QuestionCard, FloatingDesktop, preloadPanel, usePanelLoaded, SettingsPanel, SettingsSkeleton, warmPanelsWhenIdle } from "../lazyPanels";
import { applyRunPlan } from "../planMerge";
import { setPendingSection } from "../settings/pendingSection";
import { provideLinkDeps, subscribeSettingsDeepLinks, subscribeSidebarDeepLinks } from "../settings/deepLinks";
import type { OpenLinkDeps } from "../settings/linkRuntime";
import { OverlayMenu, OverlaySlot, SlotView } from "../overlays/OverlayHost";
import { ActionHints, useAppearance, useSurfacePresence } from "../InteractionSystem";
import { useConversationScroll } from "../useConversationScroll";
import { sidebarAvailable } from "../featureFlags";
import { conversationPath, readConversationRoute, readSideParam, withSideParam } from "../conversationRoute";
import { useConversationNavigation } from "../useConversationNavigation";
import { useDesktopPresence } from "../desktopPresence";
import { setComputerCapability } from "../computerCapability";
import { setCapabilityPolicy } from "../capabilityPolicy";
import type { DeleteTarget } from "../DeleteConversationDialog";
import { PrimaryBadge, otherActiveBots } from "../PrimaryAssistant";
import type { ViewOnlyChatTarget } from "../ViewOnlyChat";
import "../ScheduledTaskRow";
import { occurrenceRootKey } from "../scheduleOccurrences";
import { useScheduleOccurrences } from "../useScheduleOccurrences";
import { useScheduleMetadata } from "../useScheduleMetadata";
import { TofiIcon as Icon } from "../icons";
import type { ComputerEntry } from "../chat/HeaderMenus";
import "../BrandLogo";
import { SidebarRow } from "../ds/SidebarRow/SidebarRow";
import { RowLead, rowStatus } from "./SidebarRows";
import { applyLayout, keepLocalLayout, listOrder, pinnedOf, type SidebarIds } from "../sidebarOrder";
import type { DndAnnounce, DndCommit, RowBind, SidebarDndOptions } from "../SortableList";
import { usePhoneSwipeBack } from "../usePhoneSwipeBack";
import { Fragment, lazy, startTransition, Suspense, useCallback, useDeferredValue, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { errorText } from "../errorText";
import { api, ApiError, openConversationEvents, openWorkspaceEvents, request } from "../api";
import { codeText } from "../i18n/codeText";
import { type AvatarMotion } from "../BotAvatar";
import { holdAvatarLife } from "../lib/tofi-avatar/index.js";
import "../OnboardingChrome";
import "../EmptyCat";
import { useGreetingPlayback } from "../onboardingGreeting";
import { onboardingMode, parseOnboardingState, resumeStep, type OnboardingState, type OnboardingStep } from "../onboardingFlow";
import { flipList } from "../motion/legacy/flip";
import { flySentMessage } from "../sendFlight";
import "../FileTile";
import { preloadSendFailure } from "../SendReceipt";
import { applyInbox } from "../messageDelivery";
import { runsByTrigger } from "../sendReceipts";
import { activeBotRuns, botRecentlyActive, latestBotWorkTime, latestHumanMessageTime } from "../botActivity";
import { reconcileToolActivity, activeToolForRun, buildToolRunAnchors, buildToolSummaryAnchors, buildToolTimeline, compareActivityTime, toolActionLabel } from "../toolTimeline";
import { foldCompletedProgress, isMidRunKind } from "../runProgress";
import { mergeRunUpdates } from "../runMerge";
import { draftDeltaApplies } from "../draftDelta";
import { DraftStream } from "../draftStream";
import type { TaskCardModel, TaskFeed } from "../taskCards";
import { useProxyChallenge } from "../ProxyChallengeSlot";
import { buildRetryFamilies, indexMessages, isTerminalRun, retryFamilyAnchor } from "../runFamily";
import { clearSentDraft, parseStoredDrafts, storableDrafts, type ComposerReply } from "../composerDraft";
import { outboxStorageKey, writeOutbox, type OutboxEntry, type SendPayload } from "../outbox";
import type { OutboxContext, OutboxController } from "../outboxController";
import { adoptPreview, dropPreview, registerPreview } from "../attachmentImages";
import { observeVisualViewport, visualViewportMetrics } from "../visualViewport";
import type { Attachment, Bot, ComputerHealth, Config, Conversation, EventEnvelope, Memory, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary, WorkItem, WorkspaceEventEnvelope } from "../types";
import { MOTION, EASE } from "../motion";
import { useWidgetAutomation } from "../useWidgetAutomation";
import { autoOpenWidget, closeAll, closeCover, closeSidebar, closeWidget, emptySurfaces, enterConversation, foldWidget, forgetWidgets, isEmpty, moveWidget, normalizeWidgetOrder, openCover, openPage, openSidebar, openWidget, stashWidgets, toggleSidebar, WIDGET_ORDER, toggleSidebarExpanded, toggleWidget, type CoverId, type SidebarId, type Surfaces, type WidgetId, type WidgetsByConversation } from "../surfaces";
import { useSurfaceEscape } from "../useSurfaceEscape";
import type { SurfaceCtx } from "../SurfaceViews";
import { traceUnderReply } from "../workTraceLayout";
import { SenderHead, blockKey, jumpToMessage, messageId, previewText } from "../chat/shared";
import { setFocusToggleOfRun, type RunSignal, type ToolDetailState } from "../chat/RunActivity";
import { MessageBubble, localizeMessageRef, type RowHandlers } from "../chat/MessageBubble";
import { type ComposerAttachment, type ComposerDraft } from "../chat/Composer";
import { ChatHeader } from "../chat/ChatHeader";
import { ChatPane } from "../chat/ChatPane";
import { ComposerDrafts, emptyComposerDraft } from "../chat/Composer";
import { ToolSummaryState } from "../chat/RunActivity";
import { MailDraftCard } from "../chat/shared";
import { Sidebar } from "./Sidebar";

// The page-wide file drop target loads right after first paint; it is not needed to draw the shell.
const BotBrowserPanel = lazy(() => import("../BotBrowserPanel").then(m => ({default: m.BotBrowserPanel})));
/** The Mac app's narrow window (it is never under 820 px): the sidebar is a capsule that opens over the chat. */
const MAC_NARROW = "(min-width:768px) and (max-width:1023px)";
const WorkspaceToast = lazy(() => import("../WorkspaceToast").then(module => ({ default: module.WorkspaceToast })));
/** The sidebar a `?side=` address names opens with the page; widgets and covers never come from the address. */
/** Where the person's widget order is kept: this browser, this account (a per-viewer preference, the same in every conversation). */
const widgetOrderKey = (accountId?: string) => `tofi:widget-order:${accountId ?? "local"}`;
const initialSurfaces = (accountId?: string): Surfaces => {
  const rawSide = readSideParam(window.location.search);
  const side = sidebarAvailable(rawSide) ? rawSide : null;
  let order: WidgetId[] = [...WIDGET_ORDER];
  try { order = normalizeWidgetOrder(JSON.parse(localStorage.getItem(widgetOrderKey(accountId)) ?? "null")); } catch { /* storage unavailable or damaged: the default order */ }
  return { ...emptySurfaces(), order, ...(side ? { sidebar: { id: side, expanded: false } } : {}) };
};
type SidebarLayout = { pinned: string[]; sections: Record<string, string> };
type SidebarContextMenu = { x: number; y: number; conversation: Conversation };
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
type StoredComposerDraft = Omit<ComposerDraft, "attachments"> & { attachments: Omit<ComposerAttachment, "file">[] };
/** Per server instance and per owner: a shared browser never shows one person's unsent text to the next. */
function composerDraftStorageKey(instanceId: string, ownerId?: string) {
  return `tofi:composer-drafts:${window.location.origin}:${instanceId}${ownerId ? `:${ownerId}` : ""}`;
}
function readComposerDrafts(key: string): ComposerDrafts {
  try {
    const nativeDrafts = isDesktop ? desktopState().drafts : undefined;
    // Drafts live in localStorage (they outlive the tab); sessionStorage is read once for drafts saved by an earlier version.
    return parseStoredDrafts(nativeDrafts ? JSON.stringify(nativeDrafts) : localStorage.getItem(key) ?? sessionStorage.getItem(key)) as ComposerDrafts;
  } catch {
    return {};
  }
}
function writeComposerDrafts(key: string, drafts: ComposerDrafts) {
  try {
    const { stored, kept } = storableDrafts(drafts);
    // Empty drafts are not stored at all; with none left the key goes (and the old per-tab copy, so it cannot come back).
    if (Object.keys(kept).length) localStorage.setItem(key, JSON.stringify(kept)); else localStorage.removeItem(key);
    sessionStorage.removeItem(key);
    updateDesktopState({ drafts: stored });
  } catch {
    // The in-memory draft remains authoritative when storage is unavailable.
  }
}
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
/** Holds a jump to message `id` for its first moments. Just after a conversation opens the list can remount its rows (the
 * flash then sits on a node that is gone) and the scroll hook can still land on the newest message (a phone, once the
 * list pane has slid away). Within the settle window the flash goes back on the node that stayed, and the message is
 * scrolled to again if it fell out of view; afterwards the person's own scrolling is left alone. */
function keepJump(id: string, root: () => HTMLElement | null) {
  const start = Date.now();
  const tick = () => {
    const list = root();
    const node = list?.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(id)}"]`);
    if (list && node) {
      const elapsed = Date.now() - start;
      const pane = list.closest<HTMLElement>(".message-scroll") ?? list, a = node.getBoundingClientRect(), b = pane.getBoundingClientRect();
      if (elapsed < 900 && (a.bottom <= b.top || a.top >= b.bottom)) node.scrollIntoView({ behavior: "instant", block: "center" });
      if (!node.hasAttribute("data-jump-target") && elapsed < 1500) { node.setAttribute("data-jump-target", ""); window.setTimeout(() => node.removeAttribute("data-jump-target"), 1700); }
    }
    if (Date.now() - start < 1500) window.setTimeout(tick, 100);
  };
  window.setTimeout(tick, 100);
}
/** The server pings every 15s; this much silence on a stream means the link died without closing. */
const STREAM_STALE_MS = 30_000;
const LINK_LOSS_GRACE_MS = 4000;
const STREAM_WAKE_STALE_MS = 20_000;
export function Workspace() {
  const nativePanel = useNativeSidePanel();
  const { bridge: panelBridge, request: requestPanel } = nativePanel;
  const { t } = useTranslation(["chat", "common"]);
  const {timezone, showActivity} = useUserTimezone();
  const [revealedRuns, setRevealedRuns] = useState<ReadonlySet<string>>(() => new Set());
  const revealRun = useCallback((runId: string) => { setFocusToggleOfRun(runId); setRevealedRuns(current => current.has(runId) ? current : new Set(current).add(runId)); }, []);
  const activityView = useMemo(() => ({ show: showActivity, revealed: revealedRuns, reveal: revealRun }), [showActivity, revealedRuns, revealRun]);
  const appearance = useAppearance();
  const [sidebarCollapsed, setSidebarCollapsed] = useState(Boolean(desktopState().sidebarCollapsed));
  // Mac app between 768 and 1023 px (option C, owner 2026-11-14): the sidebar rests as the title-bar capsule and opens as a card over
  // the chat until a conversation is picked or the scrim is clicked. The saved wide-window choice (sidebarCollapsed) is not touched.
  const [macNarrow, setMacNarrow] = useState(() => isDesktop && window.tofiDesktop?.platform === "darwin" && matchMedia(MAC_NARROW).matches);
  const [narrowSidebarOpen, setNarrowSidebarOpen] = useState(false);
  useEffect(() => {
    if (!isDesktop || window.tofiDesktop?.platform !== "darwin") return;
    const query = matchMedia(MAC_NARROW);
    const change = () => { setMacNarrow(query.matches); setNarrowSidebarOpen(false); };
    query.addEventListener("change", change);
    return () => query.removeEventListener("change", change);
  }, []);
  const macNarrowRef = useRef(macNarrow);
  macNarrowRef.current = macNarrow;
  /** What the sidebar shows: the capsule (folded) or the card. */
  const sidebarFolded = macNarrow ? !narrowSidebarOpen : sidebarCollapsed;
  const setSidebarFolded = useCallback((next: boolean | ((folded: boolean) => boolean)) => {
    if (macNarrowRef.current) setNarrowSidebarOpen(open => !(typeof next === "function" ? next(!open) : next));
    else setSidebarCollapsed(next);
  }, []);
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
  const arrivalIDs = useRef(new Set<string>());
  /** What each unacknowledged outgoing message needs to go out again, files included (in memory; the outbox keeps the rest on disk). */
  const sendPayloads = useRef(new Map<string, SendPayload>());
  /** Every message the server has not confirmed, mirrored to localStorage so a failed one survives a reload (outbox.ts). */
  const outbox = useRef<OutboxEntry[]>([]);
  const outboxKey = useRef<string | null>(null);
  const [outboxVersion, setOutboxVersion] = useState(0);
  const reactionTokens = useRef(new Map<string, number>());
  /** Ids that appeared as a streaming draft: their text was already read in place, so the saved message must not replay an arrival. */
  const streamedIDs = useRef(new Set<string>());
  /** A press that lands while text is selected dismisses the selection; it must not also toggle the tap actions. */
  const selectionOnPress = useRef(false);
  const messageListRef = useRef<HTMLDivElement | null>(null);
  // Group chats: avatar and name open a sender group once; later units from the same Bot only indent.
  const [messageRecords, setMessages] = useState<Message[]>([]);
  const messageRecordsRef = useRef(messageRecords);
  messageRecordsRef.current = messageRecords;
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
  /** Opens the Settings page on a tab. `view` is not used any more: a narrow window shows the page with a strip of links (V3 PR 6.1). */
  const openSettings = (tab: SettingsTab, view: SettingsView = "page") => { setSettingsTab(tab); setSettingsEntry(current => ({ seq: current.seq + 1, view })); openSurface(state => openPage(state, "settings")); };
  // Notifications are the server's call (attention_delivery.go); this is the web outlet, see attentionNotifier.ts.
  const notifyOpen = useRef<(target: Pick<DeliveryItem, "kind"> & { conversation_id?: string }) => void>(() => {});
  const lastNotifierPoll = useRef(0);
  const attentionNotifier = useMemo(() => browserNotifier({
    watching: (conversationId) => document.visibilityState === "visible" && conversationId === activeIdRef.current,
    t: (key, options) => i18n.t(key as never, options as never) as unknown as string,
    open: (item) => notifyOpen.current(item),
  }), []);
  // A click on a web notification, and the Mac app's open-conversation command, go to the same place.
  notifyOpen.current = (item) => {
    if (item.conversation_id) { selectConversation(item.conversation_id); setMobileList(false); }
    else if (item.kind === "spend_alert") openSettings("usage");
    else if (item.kind === "admin_alert") { setPendingSection({ scope: "settings", page: "admin-resources", section: "backups" }); openSettings("admin-resources"); } // land on the Backups card
  };
  const [usageBotId, setUsageBotId] = useState("");
  // What is open (docs/design/widgets-and-sidebar.md): widgets in the chat, one sidebar at the window edge, one exclusive cover.
  // Every call site speaks the surfaces API (surfaces.ts) directly; `openSurface` only remembers where focus goes back to.
  const panelOpener = useRef<HTMLElement | null>(null);
  const accountId = useOwnerSession()?.owner?.id;
  const [surfaces, setSurfaces] = useState(() => initialSurfaces(accountId));
  // The order is only written once it differs from the default, so a later change of the default reaches people who never moved anything.
  useEffect(() => {
    try {
      if (surfaces.order.join() === WIDGET_ORDER.join()) localStorage.removeItem(widgetOrderKey(accountId));
      else localStorage.setItem(widgetOrderKey(accountId), JSON.stringify(surfaces.order));
    } catch { /* the order then lasts for this page view only */ }
  }, [accountId, surfaces.order]);
  const anyOpen = !isEmpty(surfaces);
  const rememberOpener = () => { const focused = document.activeElement; panelOpener.current = focused instanceof HTMLElement && focused !== document.body ? focused : null; };
  const openSurface = useCallback((open: (state: Surfaces) => Surfaces) => setSurfaces(state => { if (isEmpty(state)) rememberOpener(); return open(state); }), []);
  // Each conversation remembers its own widgets while another is on screen (in memory only). Switching saves the one being left and
  // brings back the one being entered, during render so the old widgets never paint under the new conversation; the sidebar is
  // re-scoped by staying (Archive is the account's, Tasks / Memory / Terminal draw whatever conversation is on screen) and a cover closes.
  const widgetMemory = useRef<WidgetsByConversation>(new Map());
  const [surfaceConversation, setSurfaceConversation] = useState<string | null>(activeId ?? null);
  if (surfaceConversation !== (activeId ?? null)) {
    if (surfaceConversation) stashWidgets(widgetMemory.current, surfaceConversation, surfaces);
    setSurfaceConversation(activeId ?? null);
    setSurfaces(state => enterConversation(state, widgetMemory.current, activeId ?? null));
  }
  // The widgets' own life (useWidgetAutomation, below) needs to hear about what the person closes or unfolds.
  const surfacesRef = useRef(surfaces);
  surfacesRef.current = surfaces;
  const widgetNotes = useRef<{ noteClose: (id: WidgetId) => void; noteUnfold: (id: WidgetId) => void }>({ noteClose: () => {}, noteUnfold: () => {} });
  const closeWidgetId = useCallback((id: WidgetId) => { widgetNotes.current.noteClose(id); setSurfaces(state => closeWidget(state, id)); }, []);
  const foldWidgetId = useCallback((id: string) => { if (surfacesRef.current.folded.includes(id as WidgetId)) widgetNotes.current.noteUnfold(id as WidgetId); setSurfaces(state => foldWidget(state, id as WidgetId)); }, []);
  const moveWidgetId = useCallback((id: WidgetId, to: number) => setSurfaces(state => moveWidget(state, id, to)), []);
  const closeSidebarPanel = useCallback(() => setSurfaces(state => closeSidebar(state)), []);
  const closeCoverPanel = useCallback(() => setSurfaces(state => closeCover(state)), []);
  const openCoverPanel = useCallback((id: CoverId) => openSurface(state => openCover(state, id)), [openSurface]);
  const openSidebarId = useCallback((id: SidebarId) => openSurface(state => sidebarAvailable(id) ? openSidebar(state, id) : state), [openSurface]);
  const openWidgetId = useCallback((id: WidgetId) => openSurface(state => openWidget(state, id)), [openSurface]);
  const toggleWidgetId = useCallback((id: WidgetId) => { if (surfacesRef.current.widgets.includes(id)) widgetNotes.current.noteClose(id); openSurface(state => toggleWidget(state, id)); }, [openSurface]);
  const toggleSidebarId = useCallback((id: SidebarId) => openSurface(state => sidebarAvailable(id) ? toggleSidebar(state, id) : state), [openSurface]);
  const sidebarId = surfaces.sidebar?.id ?? null;
  const cover = surfaces.cover;
  const settingsOpen = surfaces.page === "settings";
  // The widget column fills in right after the conversation itself: on a switch the chat paints first and the incoming widgets (a
  // computer session, the Artifacts list) mount in the next, interruptible pass, so they never sit on the path to the first frame.
  const deferredWidgets = useDeferredValue(surfaces.widgets);
  // The Bot card skips the deferral when it opens or closes: the cat changes places in one commit, with no second pass in the middle of its flight.
  const columnWidgets = surfaces.widgets.includes("usage") || surfaces.widgets.includes("bot") !== deferredWidgets.includes("bot") ? surfaces.widgets : deferredWidgets;
  const columnFolded = useDeferredValue(surfaces.folded);
  // The sidebar view is the one surface the address carries (`?side=tasks`): it is replaced in place, never a history entry, and
  // `tofi:open-sidebar` opens one from anywhere.
  useEffect(() => {
    const search = withSideParam(window.location.search, sidebarId === "conversation" || sidebarId === "usage" ? null : sidebarId);
    if (search !== window.location.search) window.history.replaceState(window.history.state, "", window.location.pathname + search + window.location.hash);
  }, [sidebarId]);
  useEffect(() => subscribeSidebarDeepLinks(id => id === "terminal" ? openTerminalRef.current() : openSidebarId(id)), [openSidebarId]);
  // Each surface plays its exit before it leaves the tree: widgets one by one, the sidebar's content while the track closes,
  // a cover while it fades (leaving is shorter than entering, motion tokens in the CSS).
  const displayedSidebar = useSurfacePresence(sidebarId, 220);
  const displayedCover = useSurfacePresence(cover, 240);
  const displayedPage = useSurfacePresence(surfaces.page, 240);
  // Bot settings is a heavy chunk: its cover frame waits for it, so the window never opens with an empty body (the enter animation starts
  // with the content in place). Hover and focus on the entry points and idle time warm it (lazyPanels.tsx), and opening asks for it at once.
  // Watched only while that cover is shown: otherwise the chunk landing (a row hover warms it) re-rendered the whole Workspace mid-drag.
  const botSettingsReady = usePanelLoaded("botSettings", displayedCover === "bot-settings");
  useEffect(() => { if (cover === "bot-settings" && !botSettingsReady) preloadPanel("botSettings"); }, [cover, botSettingsReady]);
  const lastSidebarExpanded = useRef(false);
  if (surfaces.sidebar) lastSidebarExpanded.current = surfaces.sidebar.expanded;
  /** The scope the Artifacts cover opens with: the account menu opens the whole account, the widget's All this conversation. */
  const [artifactsMode, setArtifactsMode] = useState<"artifacts" | "artifacts-bot" | "artifacts-group">("artifacts");
  const [desktopReady, setDesktopReady] = useState(false);
  // Keep in sync with conversation-workspace.css: three columns need room for chat.
  const [compactViewport, setCompactViewport] = useState(() => typeof window !== "undefined" && window.matchMedia("(max-width: 1099px)").matches);
  // Wide web windows give the context panels their own column; the drawer below keeps one panel at a time.
  const columnMode = !compactViewport;
  // Terminal is a widget in the column on a wide window and a sidebar view in the narrow drawer (owner 2026-11-14: cards, not a panel).
  const openTerminalSurface = useCallback(() => { if (columnMode) openWidgetId("terminal"); else openSidebarId("terminal"); }, [columnMode, openWidgetId, openSidebarId]);
  const openTerminalRef = useRef(openTerminalSurface);
  openTerminalRef.current = openTerminalSurface;
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
  const proxyChallenged = useProxyChallenge();
  const [workspaceError, setWorkspaceError] = useState("");
  const workspaceErrorShown = useGraceFlag(Boolean(workspaceError), LINK_LOSS_GRACE_MS);
  const [headerMenuOpen, setHeaderMenuOpen] = useState(false);
  const [createMenuOpen, setCreateMenuOpen] = useState(false);
  const [sidebarLayout, setSidebarLayout] = useState<SidebarLayout>(readSidebarLayout);
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
    // On wide browser windows the read-only chat is a sidebar view; elsewhere a full-screen modal.
    if (columnMode) openWidgetId("conversation");
    setViewOnlyChat(target);
  }
  const showCreateMenu = useSurfacePresence(createMenuOpen ? "open" : null);
  // Unmount this menu on close: retaining its exit animation also retains Radix's
  // Escape listener, which would consume the first Escape meant for the surface an item opened.
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
  // Task cards (decision 0060): the active conversation's tasks. Task events reach this state through one coalescer, at most once a second.
  const [taskItems, setTaskItems] = useState<readonly WorkItem[]>([]);
  const taskCoalescer = useRef<TaskFeed | null>(null);
  // A restart or a Wi-Fi blip is over before anyone can act on it: announce a lost link only once it has lasted.
  const linkLost = useGraceFlag(!streamConnected, LINK_LOSS_GRACE_MS);
  const linkConnected = !linkLost;
  const [codexStatusRefresh, setCodexStatusRefresh] = useState(0);
  const [extensionRefresh, setExtensionRefresh] = useState(0);
  const [computerInfo, setComputerInfo] = useState<{ state: string; phase?: string; error?: string; health?: ComputerHealth; guest_outdated?: boolean; guest_outdated_required?: boolean; error_code?: string } | null>(null);
  const chatHeaderRef = useRef<HTMLElement | null>(null);
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
    // Back and forward change the conversation (its widgets come back from memory) and the address names the sidebar.
    setMobileList(false); setViewOnlyChat(null); setTaskFocus(null);
    const rawSide = readSideParam(window.location.search);
    const side = sidebarAvailable(rawSide) ? rawSide : null;
    setSurfaces(state => {
      const next = closeCover(state);
      return (next.sidebar?.id ?? null) === side ? next : side ? { ...next, sidebar: { id: side, expanded: next.sidebar?.expanded ?? false } } : closeSidebar(next);
    });
  }, [historyVersion]);
  // The phone push and panel openings are the frames that matter most; the cats' idle breathing waits them out.
  useEffect(() => { holdAvatarLife(320); }, [mobileList, anyOpen]);
  // The row menu is its own chunk: fetch it once the workspace has mounted, so the first right click finds it here. Not React.lazy: a revealed Suspense boundary is held for about 300 ms.
  const [menus, setMenus] = useState<typeof import("./SidebarRowMenu") | null>(null);
  useEffect(() => { void import("./SidebarRowMenu").then(setMenus); }, []);
  const [searchOpen, setSearchOpen] = useState(false);
  const [activityNow, setActivityNow] = useState(() => Date.now());
  const [lastUserActivity, setLastUserActivity] = useState<Record<string, number>>({});
  const [lastBotWorkActivity, setLastBotWorkActivity] = useState<Record<string, number>>({});
  const workingBotsSeen = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!activeId || loadedId !== activeId) return;
    const time = latestHumanMessageTime(messageRecords, runs);
    if (time) setLastUserActivity(current => time > (current[activeId] ?? 0) ? { ...current, [activeId]: time } : current);
  }, [activeId, loadedId, messageRecords, runs]);
  // The widget column reserves room in the chat while any widget is open (the shared computer is a floating window below 1100 px).
  const contextPanelOpen = columnMode && surfaces.widgets.length > 0;
  // On the wide layout the read-only chat is a widget: closing the card releases it, and a card left behind without a chat (back in a
  // conversation whose widgets were stashed) closes.
  const conversationWidgetOpen = surfaces.widgets.includes("conversation");
  useEffect(() => { if (columnMode && viewOnlyChat && !conversationWidgetOpen && displayedSidebar !== "conversation") setViewOnlyChat(null); }, [columnMode, viewOnlyChat, conversationWidgetOpen, displayedSidebar]);
  useEffect(() => { if (columnMode && conversationWidgetOpen && !viewOnlyChat) setSurfaces(state => closeWidget(state, "conversation")); }, [columnMode, conversationWidgetOpen, viewOnlyChat]);
  // The Settings window and the narrow drawer are modal panes (focus trap, backdrop); the other covers are Radix dialogs of their own.
  const modalPanelOpen = Boolean(settingsOpen || (compactViewport && surfaces.sidebar && !cover));
  // Below 1100 px the widget column does not exist: the computer is a floating window, the other widgets open as covers.
  useEffect(() => { if (!columnMode) setSurfaces(state => state.widgets.some(id => id !== "computer") ? state.widgets.reduce((next, id) => id === "computer" ? next : closeWidget(next, id), state) : state); }, [columnMode]);
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
      const key = composerDraftStorageKey(info.instance_id, accountId);
      composerStorageKey.current = key;
      outboxKey.current = outboxStorageKey(window.location.origin, info.instance_id, accountId);
      // A previous page may have left unsent messages: read them (lazily) only if the key exists.
      try { if (localStorage.getItem(outboxKey.current) !== null) void outboxController().then(c => c.load()); } catch { /* storage unavailable */ }
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
  // Picking a conversation in the narrow Mac window closes the card it was picked from.
  useEffect(() => { setNarrowSidebarOpen(false); }, [activeId]);
  // The Mac app's main process notifies; it needs to know which conversation is in view. A browser ignores this.
  useEffect(() => { attentionNotifier.setView(activeId ?? null); }, [attentionNotifier, activeId]);
  // The cursor belongs to one account's database: a sign-out or another account starts from the tail again.
  useEffect(() => {
    const reset = () => attentionNotifier.reset();
    reset();
    window.addEventListener("tofi:unauthorized", reset);
    return () => window.removeEventListener("tofi:unauthorized", reset);
  }, [attentionNotifier, accountId]);
  useEffect(() => window.tofiDesktop?.onCommand((command: DesktopCommand) => {
    if (typeof command === "object") {
      if (command.type === "open-conversation" && command.id) notifyOpen.current({ kind: "reply", conversation_id: command.id });
      else if (command.type === "open-usage") notifyOpen.current({ kind: "spend_alert" });
      return;
    }
    // Native menu shortcuts must not steal keys while a remote machine owns input.
    const focus = document.activeElement;
    if (focus instanceof Element && focus.closest(".terminal-panel,.remote-desktop-surface,.remote-desktop.in-control,.xterm")) return;
    if (document.querySelector('[aria-modal="true"]') && command !== "settings") return;
    if (command === "settings") { setSettingsEntry(current => ({ seq: current.seq + 1, view: "home" })); setSurfaces(state => state.page === "settings" ? closeAll(state) : openPage(state, "settings")); }
    else if (command === "toggle-sidebar") setSidebarFolded(value => !value);
    else if (command === "new-conversation") { setCreateMenuOpen(true); setSidebarFolded(false); closeCoverPanel(); }
    else if (command === "search") setSearchOpen(true);
    else if (command === "focus-composer") document.querySelector<HTMLTextAreaElement>('.composer textarea')?.focus();
  }), []);

  // Cmd/Ctrl+K opens the search palette; the palette handles the same key itself to close.
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.defaultPrevented || !(event.metaKey || event.ctrlKey) || event.altKey || event.shiftKey || event.key.toLowerCase() !== "k") return;
      const focus = document.activeElement;
      if (focus instanceof Element && focus.closest(".terminal-panel,.remote-desktop-surface,.remote-desktop.in-control,.xterm")) return;
      if (document.querySelector("[data-tofi-modal]")) return;
      event.preventDefault();
      setSearchOpen(true);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const applyConfig = useCallback((nextConfig: Config) => {
    const previous = configRef.current;
    configRef.current = nextConfig;
    setComputerCapability(nextConfig.computer);
    setCapabilityPolicy(nextConfig.capabilities);
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
      // The 5 s beat also reads the notification view; the notifier keeps its own cursor and does nothing under the Mac app's bridge.
      if (indexInitialized.current && Date.now() - lastNotifierPoll.current >= 2000) { lastNotifierPoll.current = Date.now(); void attentionNotifier.poll(); }
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
  }, [attentionNotifier]);

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
      const fallback = document.querySelector<HTMLElement>(settingsOpen ? `[aria-label="${t("common:nav.settings")}"]` : `[aria-label="${t("header.more")}"]`);
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
  // Closing the outside panel gives focus back to what opened it.
  const hadPanel = useRef(false);
  useEffect(() => {
    if (anyOpen) { hadPanel.current = true; return; }
    if (!hadPanel.current) return;
    hadPanel.current = false;
    const opener = panelOpener.current;
    panelOpener.current = null;
    if (columnMode && opener?.isConnected) requestAnimationFrame(() => { if (!document.activeElement || document.activeElement === document.body || document.querySelector(".detail-pane")?.contains(document.activeElement)) opener.focus({ preventScroll: true }); });
  }, [anyOpen, columnMode]);
  useEffect(() => { setHeaderMenuOpen(false); }, [activeId]);
  // A server with no computer backend has nothing to poll: no /info request, no status bar, no presence poll. Until the first
  // config read lands the answer is unknown, and unknown shows no computer surface and asks nothing of /api/computers either.
  const noComputer = !config || config.computer?.backend === "none";
  useEffect(() => {
    if (noComputer) { setComputerInfo(null); return; }
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
  }, [noComputer]);
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
  // The header cat opens the Bot card: the cat leaves the header and lands at the top of the column (Motion matches the two by layout id). Everything
  // that could hitch the flight is done first: the card's chunks and Motion's layout features are in, the Bot's files are known (the Artifacts widget opens
  // in the same update as the card), and the update itself waits one frame so the flight starts on a clean one. A switch of conversation meanwhile cancels it.
  const openBotCard = useCallback(() => {
    const conversationId = active?.kind === "dm" ? active.id : null;
    if (!conversationId) return;
    const files = request<{ items?: unknown[] }>(`/api/artifacts?${new URLSearchParams({ scope: "conversation", id: conversationId, include_uploads: "0", limit: "1" })}`).then(page => (page.items?.length ?? 0) > 0, () => false);
    const early = Promise.race([files, new Promise<boolean>(resolve => window.setTimeout(() => resolve(false), 250))]);
    void Promise.all([whenPanelLoaded("contextCard"), whenPanelLoaded("surfaceViews"), import("../catMotion"), early]).then(async ([, , motion, has]) => {
      await Promise.all([motion.ensureCatMotion(), has ? whenPanelLoaded("artifacts") : undefined]);
      await new Promise(resolve => requestAnimationFrame(resolve));
      if (activeIdRef.current !== conversationId) return;
      motion.markFlight();
      openSurface(state => openWidget(state, "bot"));
      // The Artifacts widget joins once the cat has landed: its first render is the heaviest thing the card brings, and a frame lost in the flight is seen.
      void files.then(late => window.setTimeout(() => { if (late && activeIdRef.current === conversationId) setSurfaces(state => state.widgets.includes("bot") ? autoOpenWidget(state, "artifacts") : state); }, motion.CAT_TRAVEL.duration * 1000 + 60));
    });
  }, [active?.id, active?.kind, openSurface]);
  // VM surfaces keep their capability gate. Personal browser sources are selected independently below.
  const desktopBot = noComputer ? undefined : activeBot ?? (active?.kind === "group" ? botById.get(active.bot_ids[0]) : undefined);
  const {t: browserT} = useTranslation("computer");
  const browserSources = useBotBrowserSources(Boolean(activeId));
  const [chosenSource, setChosenSource] = useState<ComputerSource | null>(null);
  const computerShown = surfaces.widgets.includes("computer");
  const computerFolded = surfaces.folded.includes("computer");
  // Multiple Macs require an explicit choice, even when only one happens to be working.
  const automaticMac = browserSources.sources.length === 1 && browserSources.sources[0].activity?.conversation_id === activeId ? browserSources.sources[0] : undefined;
  const chosenMac = chosenSource?.kind === "mac_browser" ? browserSources.sources.find(item => item.computer_id === chosenSource.computer_id) : undefined;
  // Keep the selected identity while a window is open. Activity can never steal a manual window.
  // The Mac app shows its own browser in the native side panel, never in a widget.
  const macSource = nativePanel.bridge ? undefined : computerShown && chosenSource ? chosenMac : automaticMac;
  const viewDesktopBot = chosenSource?.kind === "bot_computer" ? botById.get(chosenSource.bot_id) : desktopBot;
  const observable = computerShown && chosenSource?.kind === "mac_browser" ? Boolean(macSource) : Boolean(macSource || viewDesktopBot);
  const desktopOpen = Boolean(observable && computerShown);
  const desktopExpanded = desktopOpen && (desktopSheet || desktopSize === "big");
  const desktopSmall = desktopOpen && !desktopExpanded;
  const chooseComputer = useCallback((source: ComputerSource) => {
    if (source.kind === "mac_browser" && panelBridge) {
      requestPanel(true);
      return;
    }
    setChosenSource(source);
    setDesktopSize("small");
    openWidgetId("computer");
  }, [openWidgetId, panelBridge, requestPanel]);
  const lookAtComputer = useCallback((source?: ComputerSource) => {
    if (panelBridge && (source?.kind === "mac_browser" || !source && automaticMac)) { requestPanel(true); return; }
    if (source) {
      if (source.kind === "mac_browser" && !browserSources.sources.some(s => s.computer_id === source.computer_id) || source.kind === "bot_computer" && noComputer) return;
      if (!computerShown || !chosenSource || sourceKey(source) !== sourceKey(chosenSource)) { chooseComputer(source); return; }
    }
    if (computerShown) window.dispatchEvent(new Event("tofi:computer-look"));
    else if (automaticMac) chooseComputer({kind: "mac_browser", computer_id: automaticMac.computer_id});
    else if (desktopBot) chooseComputer({kind: "bot_computer", bot_id: desktopBot.id});
  }, [computerShown, automaticMac, desktopBot, chooseComputer, chosenSource, browserSources.sources, noComputer, panelBridge, requestPanel]);
  const computerLook = useMemo(() => {
    const sources: ComputerSource[] = [
      ...(desktopBot ? [{kind: "bot_computer" as const, bot_id: desktopBot.id}] : []),
      ...browserSources.sources.map(source => ({kind: "mac_browser" as const, computer_id: source.computer_id})),
    ];
    return sources.length ? {open: lookAtComputer, sources, defaultSource: computerShown && chosenSource ? chosenSource : sources.length === 1 ? sources[0] : null} : null;
  }, [desktopBot, browserSources.sources, lookAtComputer, computerShown, chosenSource]);
  useEffect(() => {
    if (!computerShown) setChosenSource(null);
  }, [computerShown]);
  useEffect(() => {
    if (computerShown && !observable) closeWidgetId("computer");
  }, [computerShown, observable, closeWidgetId]);
  // The "command finished" line in the chat opens the Terminal tab of the Bot that ran the command.
  const [terminalBotId, setTerminalBotId] = useState<string | null>(null);
  useEffect(() => {
    const open = (event: Event) => {
      const botId = (event as CustomEvent<{ botId?: string }>).detail?.botId;
      if (!botId) return;
      setTerminalBotId(botId);
      openTerminalRef.current();
    };
    window.addEventListener("tofi:open-terminal", open);
    return () => window.removeEventListener("tofi:open-terminal", open);
  }, []);
  const terminalWidgetOpen = surfaces.widgets.includes("terminal");
  useEffect(() => {
    if (sidebarId !== "terminal" && !terminalWidgetOpen) setTerminalBotId(null);
  }, [sidebarId, terminalWidgetOpen]);
  // Ownership is only worth a 1 s poll while a computer is ready or the desktop panel is open.
  const desktopPresence = useDesktopPresence(Boolean(activeId) && !noComputer && (computerShown || (computerInfo ? computerInfo.state === "ready" : desktopReady)));
  const currentDesktopOwner = desktopPresence.ownership?.owner;
    const activeDesktopOwner = !desktopPresence.unavailable && currentDesktopOwner?.kind === "bot" && currentDesktopOwner.conversation_id === active?.id ? currentDesktopOwner : null;
  useEffect(() => {
    if (computerShown && !chosenSource) {
      // The Mac app shows a Mac browser in its native panel, so the widget only ever holds the Bot computer there.
      if (automaticMac && !panelBridge) setChosenSource({kind: "mac_browser", computer_id: automaticMac.computer_id});
      else if (desktopBot) setChosenSource({kind: "bot_computer", bot_id: desktopBot.id});
    }
  }, [computerShown, chosenSource, automaticMac, desktopBot, panelBridge]);
  const computerOn = computerInfo ? computerInfo.state === "ready" : desktopReady;
  widgetNotes.current = useWidgetAutomation({ activeId: activeId ?? undefined, ready: loadedId === activeId && !loadingMessages, messages: messageRecords, columnMode, hasComputer: observable, externalComputer: Boolean(macSource), surfaces, setSurfaces, botRunId: macSource ? (macSource.activity?.conversation_id === activeId ? macSource.activity.run_id : undefined) : activeDesktopOwner?.run_id, phoneSheet: desktopSheet, holderKind: macSource ? (macSource.state === "human" ? "human" : macSource.activity ? "bot" : undefined) : currentDesktopOwner?.kind, bigComputer: desktopExpanded, memory: widgetMemory });
  // Escape closes one surface at a time, in the order of useSurfaceEscape.ts.
  useSurfaceEscape({ surfaces: surfacesRef, bigComputer: desktopExpanded, phoneSheet: desktopSheet, shrinkComputer: () => setDesktopSize("small"), closeWidget: closeWidgetId, closeSidebar: closeSidebarPanel, closeSettings: () => {
    // The Settings shell answers with preventDefault (it asks about unsaved edits first); the frame shown while its code loads has no listener, so that one closes here.
    if (window.dispatchEvent(new Event("tofi-settings-close", { cancelable: true }))) closeCoverPanel();
  } });
  const computerStateLabel = !computerOn ? t("header.computer_off") : activeDesktopOwner ? t("header.computer_driving", { name: botById.get(activeDesktopOwner.bot_id)?.name ?? "Bot" }) : t("header.computer_on");
  const sortedConversations = useMemo(() => [...pinnedOf(conversations), ...listOrder(conversations)], [conversations]);
  const filteredConversations = sortedConversations.filter((conversation) => !conversation.archived);
  const visibleSidebarConversations = filteredConversations.filter((conversation) => !conversation.archived);
  const pinnedConversations = visibleSidebarConversations.filter((conversation) => conversation.pinned);
  const customSidebarSections = Object.entries(sidebarLayout.sections).reduce<Record<string, Conversation[]>>((groups, [id, name]) => {
    const conversation = visibleSidebarConversations.find((item) => item.id === id && !item.pinned);
    if (conversation && name.trim()) (groups[name] ??= []).push(conversation);
    return groups;
  }, {});
  const sectionedConversationIds = new Set(Object.values(customSidebarSections).flat().map((conversation) => conversation.id));
  const defaultSidebarConversations = visibleSidebarConversations.filter((conversation) => !conversation.pinned && !sectionedConversationIds.has(conversation.id));
  // Drag, drop and Alt+Arrow move work on the plain list and the pinned tiles.
  const sidebarDndEnabled = !sidebarFolded;
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
  // Drag and drop is the library's chunk, fetched in idle time once the workspace has content; until then rows are plain, clickable buttons.
  const [sortable, setSortable] = useState<typeof import("../SortableList") | null>(null);
  useEffect(() => {
    if (loading || sortable) return;
    const idle = (window as { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number }).requestIdleCallback;
    const load = () => void import("../SortableList").then(setSortable);
    const handle = idle ? idle(load, { timeout: 1200 }) : window.setTimeout(load, 400);
    return () => { if (idle) (window as { cancelIdleCallback?: (id: number) => void }).cancelIdleCallback?.(handle); else window.clearTimeout(handle); };
  }, [loading, sortable]);
  const sidebarDnd: SidebarDndOptions = {
    scrollRef: sidebarListRef,
    enabled: sidebarDndEnabled,
    getIds: () => sidebarIdsRef.current,
    onCommit: next => { sidebarSkipFlip.current = true; commitSidebarLayout(layoutFromDrop(next)); },
    onMenu: (id, x, y) => { const conversation = conversations.find(item => item.id === id); if (conversation) setSidebarContextMenu({ conversation, x, y }); },
    onAnnounce: announceSidebar,
  };
  // Runs with a tool call in flight: only those have a step the Stop menu can interrupt.
  const stepRunIds = useMemo(() => toolActivities.filter(item => item.status === "running").map(item => item.run_id), [toolActivities]);
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
  // First-Bot greeting: hidden behind a setup screen, then revealed one message at a time (onboardingGreeting.ts).
  const greetingTexts = useMemo(() => {
    if (!activeBot?.onboarding_pending || active?.kind !== "dm" || loadedId !== activeId || loadingMessages || hasMore || !messageRecords.length) return [];
    return messageRecords.every(message => !message.local && message.conversation_id === activeId && message.role === "assistant" && !message.kind) ? messageRecords.map(message => message.content) : [];
  }, [activeBot?.onboarding_pending, active?.kind, loadedId, activeId, loadingMessages, hasMore, messageRecords]);
  const greeting = useGreetingPlayback(activeBot?.id, Boolean(activeBot?.onboarding_pending), greetingTexts, matchMedia("(prefers-reduced-motion: reduce)").matches);
  const messages = useMemo(() => greeting.stage === "done" ? foldedProgress.visible : foldedProgress.visible.slice(0, greeting.shown), [foldedProgress.visible, greeting.stage, greeting.shown]);
  const scheduledRootKey = useMemo(() => occurrenceRootKey(messageRecords.filter(message => message.kind === "scheduled_task" && message.conversation_id === activeId).map(message => message.run_id ?? "")), [messageRecords, activeId]);
  const occurrenceRead = useScheduleOccurrences(activeId, scheduledRootKey, scheduleRefresh);
  const metadataRead = useScheduleMetadata(activeId, scheduledRootKey, scheduleRefresh);
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
  // The card logic is its own chunk (taskCards.ts), loaded once; the entry bundle holds only this glue.
  const [cardKit, setCardKit] = useState<typeof import("../taskCards") | null>(null);
  useEffect(() => { let live = true; void import("../taskCards").then(kit => { if (live) setCardKit(kit); }); return () => { live = false; }; }, []);
  useEffect(() => { setTaskItems([]); }, [activeId]);
  useEffect(() => {
    if (!activeId || !streamConnected || !cardKit) return;
    const feed = cardKit.startFeed(activeId, setTaskItems, api.taskCards);
    taskCoalescer.current = feed;
    return () => { feed.cancel(); if (taskCoalescer.current === feed) taskCoalescer.current = null; };
  }, [activeId, streamConnected, cardKit]);
  const cardModels = useRef<Map<string, TaskCardModel> | undefined>(undefined);
  const taskView = useMemo(() => {
    const view = cardKit?.deriveCards(taskItems, botById, messages, runById, cardModels.current);
    cardModels.current = view?.models;
    return view;
  }, [cardKit, taskItems, botById, messages, runById]);
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
  // Each task owner is drawn in stretches, in time order with everything else in the conversation (see activityPlacements.ts).
  // The inputs are keyed by what the cut depends on (who is drawn, when, which notes), not by the messages' text: a streamed chunk
  // rewrites a message's content many times a second and must not redo the cut.
  const boundaryMessages = conversationItems.flatMap(item => item.kind === "message" ? [item.message] : []);
  const boundaryKey = boundaryMessages.map(message => `${message.id}@${message.created_at}`).join("|");
  const noteMessages = messageRecords.filter(message => message.kind === "progress" && message.role === "assistant");
  const noteKey = noteMessages.map(message => `${message.id}@${message.created_at}:${message.run_id}`).join("|");
  const ownerPlacements = useMemo<OwnerPlacement[]>(() => {
    const timeOf = (anchor: NonNullable<(typeof taskOwners)[number]["anchor"]>) => anchor.kind === "message" ? messageById.get(anchor.id)?.created_at : anchor.kind === "question" ? questions.items.find(item => item.question_id === anchor.id)?.created_at : mailDrafts.items.find(item => item.draft_id === anchor.id)?.created_at;
    return placeOwners({ owners: taskOwners, tools: loadedToolActivities, messages: boundaryMessages, notes: noteMessages, anchorTime: timeOf });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskOwners, loadedToolActivities, boundaryKey, noteKey, questions.items, mailDrafts.items]);
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
    loadOlder, markRead, beforeFirstPosition: () => markSenderGroups(messageListRef.current) });
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
      // Hold it through the first moments of the conversation opening (see keepJump).
      if (taskFocus.kind === "message") keepJump(taskFocus.id, () => messageListRef.current);
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
  // A link into the settings (a message link, a card's Manage link, a typed address): open the cover it names. The link only says
  // where to look; an id that is not in this account's index opens nothing.
  const openLink = useRef<OpenLinkDeps | null>(null);
  openLink.current = { bots, conversations, openSettings, selectConversation, setMobileList, openCover: openCoverPanel };
  useEffect(() => provideLinkDeps(() => openLink.current), []);
  useEffect(() => { void import("../appearanceSync").then(sync => sync.startThemeSync(request)); }, []);
  useEffect(() => { if (bots.length) void import("../avatarSync").then(sync => sync.hydrateBotAvatars(request, bots)); }, [bots]);

  const updateConversationPreview = useCallback((next: Message) => {
    setConversations((current) => current.map((conversation) => {
      if (conversation.id !== next.conversation_id || (conversation.last_message && next.seq < conversation.last_message.seq)) return conversation;
      return { ...conversation, updated_at: next.created_at, ...(next.role === "user" && !next.run_id ? { last_user_message_at: next.created_at } : {}), last_message: { id: next.id, seq: next.seq, role: next.role, kind: next.kind, sender_bot_id: next.sender_bot_id, content: next.content.slice(0, 160), created_at: next.created_at, internal: next.kind === "message_ref" } };
    }));
  }, []);

  const upsertMessage = useCallback((next: Message) => {
    // The server holds it (it arrived over the stream): nothing is left to resend, even if the POST answer was lost.
    if (next.role === "user" && next.client_message_id && !sendPayloads.current.get(next.client_message_id)?.inFlight) {
      sendPayloads.current.delete(next.client_message_id);
      if (outbox.current.some(entry => entry.clientMessageId === next.client_message_id)) { outbox.current = outbox.current.filter(entry => entry.clientMessageId !== next.client_message_id); if (outboxKey.current) writeOutbox(outboxKey.current, outbox.current); }
    }
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
    } else if (type === "message_task") {
      // The link of a result to its part: merged into the message held, so reactions and delivery state stay.
      setMessages(current => applyMessageTask(current, data));
    } else if (type === "inbox") {
      setMessages(current => applyInbox(current, data));
    } else if (type === "run_plan") {
      setRuns(current => applyRunPlan(current, data));
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
      if (type === "work_item") taskCoalescer.current?.push(data);
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

  const saveOutbox = () => { if (outboxKey.current) writeOutbox(outboxKey.current, outbox.current); };
  const outboxPut = (entry: OutboxEntry) => { outbox.current = [...outbox.current.filter(item => item.clientMessageId !== entry.clientMessageId), entry]; saveOutbox(); };
  const canRetrySend = (clientMessageId: string) => sendPayloads.current.has(clientMessageId) || Boolean(outbox.current.find(item => item.clientMessageId === clientMessageId) && !outbox.current.find(item => item.clientMessageId === clientMessageId)!.attachments.some(a => !a.id));

  // Everything after the bubble is on screen (upload, post, retry, edit, delete, restore) is the lazy outbox controller.
  const outboxContext = useRef<OutboxContext>(null as unknown as OutboxContext);
  outboxContext.current = { payloads: sendPayloads, outbox, outboxKey, activeId: activeIdRef, messages: messageRecordsRef, patch: patchConversation, setMessages, setLastUserActivity, updatePreview: updateConversationPreview, setError, bump: () => setOutboxVersion(version => version + 1), updateDraft: updateComposerDraft, helpers: { errorText, mergeRunUpdates, adoptPreview, dropPreview }, api: { sendMessage: api.sendMessage, messages: id => api.messages(id), uploadViaRequest: (path, body) => request<Attachment>(path, { method: "POST", body }) }, text: (key, options) => String(i18n.t(key as never, options as never)), errors: { ApiError, codeText } };
  const controller = useRef<Promise<OutboxController> | undefined>(undefined);
  const outboxController = () => (controller.current ??= import("../outboxController").then(module => module.createOutboxController(() => outboxContext.current)).catch(cause => { controller.current = undefined; throw cause; }));
  const retrySend = (clientMessageId: string) => { void outboxController().then(c => c.retrySend(clientMessageId)); };
  const deleteUnsent = (clientMessageId: string) => { void outboxController().then(c => c.deleteUnsent(clientMessageId)); };
  const editUnsent = (clientMessageId: string) => { void outboxController().then(c => c.editUnsent(clientMessageId)); };
  /** The first attempt of a message. If the chunk cannot load (no connection, never cached) the bubble fails like any other send. */
  const deliver = (clientMessageId: string, conversationId: string) => void outboxController().then(c => c.deliver(clientMessageId), () => {
    outbox.current = outbox.current.map(entry => entry.clientMessageId === clientMessageId ? { ...entry, reason: "network" as const } : entry); saveOutbox();
    patchConversation(conversationId, list => list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local: { state: "failed" as const, reason: "network" as const } } : item));
  });

  // The lazy controller loads in idle time after start (so it is cached before the connection can drop) and then watches for the
  // connection coming back: the browser's online event, and the live stream reconnecting.
  useEffect(() => {
    const load = () => { void preloadSendFailure().catch(() => {}); void outboxController().catch(() => {}); };
    const idle = (window as unknown as { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number }).requestIdleCallback;
    if (idle) { const id = idle.call(window, load, { timeout: 4000 }); return () => (window as unknown as { cancelIdleCallback?: (id: number) => void }).cancelIdleCallback?.(id); }
    const timer = window.setTimeout(load, 2500);
    return () => window.clearTimeout(timer);
  }, []);
  useEffect(() => { void controller.current?.then(c => c.connection(streamConnected), () => {}); }, [streamConnected]);
  // Messages that were still unsent when the page closed come back as failed bubbles, once their conversation is on screen.
  useEffect(() => {
    if (!outboxKey.current || !activeId || loadedId !== activeId || !outbox.current.some(entry => entry.conversationId === activeId)) return;
    let alive = true;
    void outboxController().then(c => { if (alive) c.restore(activeId); }, () => {});
    return () => { alive = false; };
  }, [activeId, loadedId, outboxVersion]);

  /**
   * The bubble and the cleared composer land in one commit, then the request goes out in the background. The returned
   * promise settles as soon as the message is on screen: the person may type the next one while this one is in flight.
   * Files are on the bubble at once (local preview, progress) and upload in the background; the composer does not wait.
   */
  async function send(content: string, clientMessageId: string, files: ComposerAttachment[] = [], draftSignature = "", flightOrigin?: DOMRect, plain = content, reply?: ComposerReply): Promise<boolean> {
    if (!active || (!content.trim() && !files.length)) return false;
    const conversationId = active.id;
    const text = content.trim();
    const stamp = new Date().toISOString();
    const attachments = files.map(file => ({ ...file }));
    sendPayloads.current.set(clientMessageId, { conversationId, content: text, plain, reply, attachments, createdAt: stamp, inFlight: false, auto: false });
    // Lazy parts a failure needs (its actions, the controller) are fetched now too, while the connection still works.
    void preloadSendFailure().catch(() => {}); void outboxController().catch(() => {});
    outboxPut({ clientMessageId, conversationId, content: text, plain, reply, attachments: attachments.map(({ file: _file, ...rest }) => rest), createdAt: stamp });
    await Promise.all(attachments.map(file => file.file && !file.id ? registerPreview(`local:${file.key}`, file.file) : undefined));
    const shown: Attachment[] = attachments.map(file => ({ id: file.id ?? `local:${file.key}`, conversation_id: conversationId, name: file.name, mime: file.type, size: file.size, created_at: stamp, clientKey: file.key, ...(file.id ? {} : { pending: { progress: 0 } }) }));
    const key = `cm:${clientMessageId}`;
    // Clear only the exact source draft that was sent.
    const clearDraft = () => { if (draftSignature) setComposerDrafts((current) => {
      const next = clearSentDraft(current, conversationId, draftSignature, clientMessageId, emptyComposerDraft());
      if (next !== current && composerStorageKey.current) writeComposerDrafts(composerStorageKey.current, next);
      return next;
    }); };
    const addLocal = (list: Message[]) => {
      if (list.some(item => item.client_message_id === clientMessageId)) return list.map(item => item.local && item.client_message_id === clientMessageId ? { ...item, local: { state: "sending" as const } } : item);
      const seq = list.reduce((max, item) => item.local ? max : Math.max(max, item.seq), 0) + 1;
      return [...list, { id: `local:${clientMessageId}`, conversation_id: conversationId, seq, role: "user" as const, content: text, created_at: stamp, client_message_id: clientMessageId, attachments: shown.length ? shown : undefined, local: { state: "sending" as const } }];
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
      const target = Array.from(messageListRef.current?.querySelectorAll<HTMLElement>(".message-block") ?? []).find(block => block.dataset.flip === key)?.querySelector<HTMLElement>(".message-user .tb-bubble");
      if (flightOrigin && target) flySentMessage(flightOrigin, target);
    }
    setError("");
    deliver(clientMessageId, conversationId);
    return true;
  }

  /** Adds a Bot the server created during onboarding to the lists, without moving the person off the sheet. */
  function adoptOnboardingBot(bot: Bot) {
    // The sheet made this Bot, so setup is under way: without a saved step the "model and a Bot already there" rule would close the sheet in the same render.
    saveOnboarding({ step: 2 });
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
    closeCoverPanel();
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
      selectConversation(bot.dm_conversation_id, conversation);
      setMobileList(false);
      closeCoverPanel();
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
  }

  async function createGroup(name: string, botIds: string[]) {
    const group = await api.createGroup({ name, bot_ids: botIds });
    snapshotGeneration.current += 1;
    setConversations((current) => [...current, group]);
    selectConversation(group.id, group); setMobileList(false); closeCoverPanel();
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

  // The prompt and the request of the row menu live in its own chunk (SidebarRowMenu.tsx); these only write the state.
  const setSection = (id: string, name: string) => setSidebarLayout(current => { const sections = { ...current.sections }; if (name) sections[id] = name; else delete sections[id]; return { ...current, sections }; });
  const markUnread = (id: string, seq: number) => api.markUnread(id, seq).then(() => setConversations(current => current.map(item => item.id === id ? { ...item, read_seq: seq, unread_count: Math.max(1, item.unread_count ?? 0) } : item)));

  async function makePrimary(botId: string) {
    try { await api.setPrimaryAssistant(botId); await refreshIndex(); setError(""); }
    catch (cause) { setError(errorText(cause)); }
  }

  async function setArchived(conversation: Conversation, archived: boolean) {
    try {
      const bot = conversation.kind === "dm" ? botByConversationId.get(conversation.id) : undefined;
      if (conversation.kind === "dm" && !bot) throw new Error(t("error.bot_missing"));
      if (archived && bot?.is_primary && otherActiveBots(bots, bot.id).length > 0) { rememberDialogOpener(); setSidebarContextMenu(null); setHidePrimaryTarget(bot); return; }
      await (bot ? api.setBotArchived(bot.id, archived) : api.setConversationArchived(conversation.id, archived));
      setSidebarContextMenu(null);
      await refreshIndex();
    } catch (cause) { setError(errorText(cause)); }
  }

  function conversationMotion(conversation: Conversation, botId?: string): AvatarMotion {
    if (!botId) return "sleeping";
    // A group has no Bot of its own: it works when any member does.
    if (conversation.kind === "group" && botId === conversation.id) return conversation.working_bot_ids?.length || (conversation.id === activeId && activeBotRuns(runs, Object.values(drafts), toolActivities).length) ? "working" : "sleeping";
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
    const progress = taskView?.progressByRun.get(run.id);
    if (progress) return progress;
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
  const placementDraws = (placement: OwnerPlacement) => placement.segment ? taskBlockVisible({ owner: placement.owner, segment: placement.segment, tools: loadedToolActivities, questions: questions.items, drafts: mailDrafts.items, messages: messageRecords, summaries: terminalToolSummaries, details: toolDetailState, connected: linkConnected, hideActivity: !showActivity, revealed: ownerRevealed(placement.owner) }) : ownerDraws(placement.owner);
  const ownerAnchors = ownerPlacements.filter(placementDraws).map(placement => ({ anchor: placement.anchor, botId: placement.owner.family.latest.bot_id }));
  // Blocks that share an anchor go in time order (the sort is stable: owners keep their order among themselves).
  const placementIndex = new Map<string, OwnerPlacement[]>();
  for (const placement of ownerPlacements) { const at = placement.anchor ? `${placement.anchor.kind}:${placement.anchor.id}` : ""; const list = placementIndex.get(at); if (list) list.push(placement); else placementIndex.set(at, [placement]); }
  for (const list of placementIndex.values()) if (list.length > 1) list.sort((a, b) => compareActivityTime(a.at, b.at));
  const placementsAt = (kind: "message" | "question" | "draft" | undefined, id?: string) => placementIndex.get(kind ? `${kind}:${id}` : "") ?? [];
  const messageRecordIDs = new Set(messageRecords.map(record => record.id));
  // Runs whose reply is on screen: the live status line gives way to it in the same frame.
  const replyingRunIDs = new Set([...Object.values(drafts).filter(draft => draft.status === "active" && draft.content.trim()).map(draft => draft.run_id), ...messageRecords.filter(record => record.role === "assistant" && record.run_id && !record.kind).map(record => record.run_id as string)]);
  // The work trace shows under a run's final reply only when that run's Activity list is not drawn (otherwise it ends that list).
  const finalReplyByRun = new Map<string, string>();
  for (const record of messageRecords) if (record.role === "assistant" && record.run_id && !record.kind) finalReplyByRun.set(record.run_id, record.id);
  const traceRunFor = (message: Message): Run | undefined => {
    const run = message.run_id ? runById.get(message.run_id) : undefined;
    const owner = run && taskOwners.find(item => item.family.attempts.some(attempt => attempt.id === run.id));
    return run && traceUnderReply({ hasTrace: Boolean(run.trace), isFinalReply: message.role === "assistant" && !message.kind && finalReplyByRun.get(run.id) === message.id, hasTaskBlock: Boolean(owner), activityShown: Boolean(owner) && (showActivity || ownerRevealed(owner!)) }) ? run : undefined;
  };
  const ownedAttemptKeys = new Set(taskOwners.flatMap(owner => owner.family.attempts.map(run => `${run.id}|${run.bot_id}|${run.conversation_id}`)));
  const ownedBy = (item: { run_id: string; bot_id: string; conversation_id: string }) => ownedAttemptKeys.has(`${item.run_id}|${item.bot_id}|${item.conversation_id}`);
  function renderPlacement({ owner, key, segment }: OwnerPlacement) {
    return <TaskRunBlock key={key} segment={segment} replying={replyingRunIDs.has(owner.family.latest.id)} hideActivity={!showActivity} revealed={ownerRevealed(owner)} owner={owner} senderHead={active?.kind === "group" ? <SenderHead id={owner.family.latest.bot_id} name={botById.get(owner.family.latest.bot_id)?.name ?? "Bot"} /> : undefined} liveStatus={runLiveStatus(owner.family.latest)} onRetryRun={startRetry} retriedBy={retryChildByParent.get(owner.family.latest.id)} tools={loadedToolActivities} questions={questions.items} drafts={mailDrafts.items} messages={messageRecords} summaries={terminalToolSummaries} details={toolDetailState} connected={linkConnected} botName={botById.get(owner.family.latest.bot_id)?.name ?? "Bot"} showName={active?.kind === "group"}
      onOpenTools={() => openSettings("connections")}
      onFeedback={text => window.dispatchEvent(new CustomEvent("tofi:task-feedback", {detail:{conversationId:owner.family.latest.conversation_id,text}}))}
      onRefresh={refreshTaskStatus} onLoadDetails={loadToolDetails}
      renderMessage={message => <MessageBubble key={message.id} message={message} sender={botById.get(owner.family.latest.bot_id)} run={message.run_id ? runById.get(message.run_id) : undefined} showAvatar={false} showIdentity={false} />}
      renderQuestion={question => <Suspense key={question.question_id} fallback={null}><QuestionCard item={question} bot={botById.get(question.bot_id)} group={active?.kind === "group"} archived={active?.archived} onChanged={async resolved => { await questions.refresh(resolved); await refreshConversations(); }} /></Suspense>}
      renderDraft={draft => <Suspense key={draft.draft_id} fallback={null}><MailDraftCard draft={draft} bot={botById.get(draft.bot_id)} group={active?.kind === "group"} archived={active?.archived} issueOwned onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} /></Suspense>} />;
  }
  const taskAnnouncements = new Map(taskOwners.map(owner => {
    const run = owner.family.latest;
    const input = {run, tools:loadedToolActivities.filter(tool => tool.run_id === run.id), questions:questions.items.filter(question => question.run_id === run.id), drafts:mailDrafts.items.filter(draft => draft.run_id === run.id), summary:terminalToolSummaries.find(summary => summary.run_id === run.id), recordsComplete:toolDetailState[run.id]?.hasMore === false, connected:linkConnected};
    const issue = presentTaskIssue({...input, family:owner.family, tools:loadedToolActivities, questions:questions.items, drafts:mailDrafts.items});
    const approved = input.questions.some(question => question.question_type === "approval" && question.status === "answered" && question.answer === true && !question.approval?.review_only) ? i18n.t("tasks:record.approved_unconfirmed") : "";
    return [run.id, issue ? [issue.title, ...issue.facts, ...issue.secondary.filter(cause => cause !== i18n.t("tasks:secondary.records_incomplete"))].join(" ") : [taskPhaseLabel(input), approved].filter(Boolean).join(" ")];
  }));

  /** One conversation as a kit SidebarRow. `zone` is the drag engine's: list rows follow recency, pinned rows take a manual order. */
  function conversationRow(conversation: Conversation, draggable = false, pinned = false) {
    const bot = conversation.kind === "dm" ? botByConversationId.get(conversation.id) : undefined;
    const preview = previewText(conversation.last_message?.kind === "message_ref" ? localizeMessageRef(conversation.last_message.content) : conversation.last_message?.content?.trim() || "");
    const botId = conversation.kind === "dm" ? bot?.id || conversation.bot_id || conversation.bot_ids[0] || conversation.id : conversation.id;
    const motion = conversationMotion(conversation, bot?.id || conversation.bot_id || botId);
    const task = conversation.task_state;
    const unread = conversation.unread_count ?? 0;
    const indicator = rowIndicator(task, unread, motion === "working");
    const dnd = draggable || pinned;
    const draw = (bind?: RowBind) => <SidebarRow as="button" {...bind} data-sidebar-flip={conversation.id} data-dnd-id={dnd ? conversation.id : undefined} data-dnd-zone={dnd ? (pinned ? "dock" : "list") : undefined} aria-keyshortcuts={dnd ? "Alt+ArrowUp Alt+ArrowDown" : undefined} data-hint={sidebarFolded ? conversation.name : undefined}
      aria-label={[conversation.name, task ? taskStateLabel(task) : "", indicator === "working" && !task ? t("sidebar.working") : "", conversation.unread_count ? t("sidebar.unread", { count: conversation.unread_count }) : ""].filter(Boolean).join(t("sidebar.label_separator"))}
      className={`${pinned ? "contact-row pin-tile" : "contact-row conversation-row"}${bind?.className ? ` ${bind.className}` : ""}`} compact={sidebarFolded} active={activeId === conversation.id} unread={unread > 0} data-unread={unread > 0 ? "" : undefined}
      onClick={() => focusTask(conversation)}
      onPointerEnter={event => { if (event.pointerType === "mouse") { window.clearTimeout(prefetchTimer.current); prefetchTimer.current = window.setTimeout(() => prefetchConversation(conversation.id), 80); } }}
      onPointerLeave={() => window.clearTimeout(prefetchTimer.current)}
      onPointerDown={() => prefetchConversation(conversation.id)}
      onContextMenu={(event) => { event.preventDefault(); setSidebarContextMenu({ conversation, x: event.clientX, y: event.clientY }); }}
      avatar={<RowLead name={conversation.name} botId={botId} group={conversation.kind === "group"} working={indicator === "working"} />}
      label={<><span className="contact-name">{conversation.name}</span>{bot?.is_primary && <PrimaryBadge />}</>}
      meta={preview}
      {...rowStatus(indicator, unread)} />;
    // Once the drag chunk is here a movable row is wrapped by it; before, the same row is drawn plain.
    // A list row stays the plain row (the chunk takes hold of it when it is pressed); a pinned row is wrapped for its order.
    if (!pinned || !sortable) return <Fragment key={conversation.id}>{draw()}</Fragment>;
    return <sortable.SidebarDockRow key={conversation.id} id={conversation.id} disabled={!sidebarDndEnabled} render={draw} />;
  }

  function pinTile(conversation: Conversation) { return conversationRow(conversation, true, true); }

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
    // A deleted Bot or group takes its remembered widgets with it.
    forgetWidgets(widgetMemory.current, target.conversationId);
    setSurfaces(closeAll);
    setError("");
    await refreshIndex();
  }

  /** Starts the retry and resolves once the server returned the new run; rejects so the asking card can show the reason beside its own button. */
  async function startRetry(id: string): Promise<void> {
    const run = await api.retryRun(id);
    setRuns((current) => mergeRunUpdates(current, [run]));
    setError("");
  }
  async function retryRun(id: string): Promise<void> {
    try { await startRetry(id); } catch (cause) { setError(errorText(cause)); }
  }
  // A failed run that has a retry child is superseded: its card folds to a one-line note.
  const retryChildByParent = useMemo(() => { const map = new Map<string, Run>(); for (const run of runs) if (run.parent_run_id) map.set(run.parent_run_id, run); return map; }, [runs]);

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
      if (hadSelection || !window.matchMedia("(hover: none)").matches || !target.closest(".tb-bubble, .message-content") || target.closest("a,button,summary,input,textarea,pre,[role=button],.message-hover-actions")) return;
      setMessageActions("tap", tappedMessageActionsId() === messageId ? "" : messageId);
    },
    react: (messageId, anchor) => setReactionMenu({ messageId, x: anchor.left, y: anchor.top - 6, h: anchor.height + 12 }),
    toggleActivity: runId => { setMessageActions("tap", ""); toggleRunActivity(runId); },
    reply: message => {
      if (!active) return;
      updateComposerDraft(active.id, current => ({ ...current, reply: { id: message.id, name: message.role === "user" ? t("message.you") : message.sender_bot_name ?? (message.sender_bot_id ? botById.get(message.sender_bot_id)?.name : undefined) ?? "Bot", excerpt: previewText(readReplyMarker(message.content).body) || t("message.attachment") } }));
      document.querySelector<HTMLTextAreaElement>(".composer textarea")?.focus();
    },
    retrySend: clientMessageId => void retrySend(clientMessageId),
    editUnsent: clientMessageId => void editUnsent(clientMessageId),
    deleteUnsent: clientMessageId => deleteUnsent(clientMessageId),
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
    return { openMenu: call("openMenu"), closeMenu: call("closeMenu"), hostPointerDown: call("hostPointerDown"), hostClick: call("hostClick"), react: call("react"), toggleActivity: call("toggleActivity"), reply: call("reply"), retrySend: call("retrySend"), editUnsent: call("editUnsent"), deleteUnsent: call("deleteUnsent"), retryRun: call("retryRun"), loadDetails: call("loadDetails"), retrySummary: call("retrySummary"), navigate: call("navigate"), openRelated: call("openRelated"), draftReply: call("draftReply"), mention: call("mention"), setReaction: call("setReaction") };
  }, []);
  // Sender marks change the gaps between rows after the scroll position was set: the position is taken again once they are in.
  useLayoutEffect(() => { markSenderGroups(messageListRef.current); viewport.reposition(); }, [viewport.reposition, conversationItems, timelineFrom, taskOwners, toolTimeline, pendingRuns, drafts, loadingMessages, active?.id, active?.kind, active?.archived, viewport.unreadSeq, error, historyError, hasMore, runs, toolSummaryAnchors, toolSummaryErrorAnchors, toolSummaryLoadingAnchors, toolDetailState, toolSummaryState, loadedToolActivities, terminalToolSummaries, questions.items, mailDrafts.items, activityView, bots, conversations, messageRecords]);
  // The card column sits inside the chat area, to the left of the message scrollbar (context-card.css): its right edge stops at
  // the scrollbar, so the scrollbar's width is measured (0 for overlay scrollbars) and published as --scroll-bar-w.
  const chatShown = Boolean(active);
  // A group's open to-dos: one line under the plan cards opens the Work panel (it took over from the team board).
  const openTasks = active?.kind === "group" ? taskItems.filter(task => task.conversation_id === active.id && !["done", "failed", "cancelled"].includes(task.status)).length : 0;
  useLayoutEffect(() => {
    const el = viewport.scrollRef.current;
    const grid = el?.closest<HTMLElement>(".workspace-grid");
    if (!el || !grid || !columnMode) return;
    const measure = () => grid.style.setProperty("--scroll-bar-w", `${Math.max(0, el.offsetWidth - el.clientWidth)}px`);
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(el);
    return () => { observer.disconnect(); grid.style.removeProperty("--scroll-bar-w"); };
  }, [columnMode, chatShown, viewport.scrollRef]);
  // Opening or closing a card changes the scroller's right padding, which re-wraps the lines. WebKit has no native scroll
  // anchoring, so a reader in the middle of the history keeps their message at the same offset while the padding animates.
  const cardRoom = columnMode && (contextPanelOpen || Boolean(surfaces.sidebar));
  const cardRoomSeen = useRef(cardRoom);
  useLayoutEffect(() => {
    if (cardRoomSeen.current === cardRoom) return;
    cardRoomSeen.current = cardRoom;
    const el = viewport.scrollRef.current;
    if (!el || el.scrollHeight - el.scrollTop - el.clientHeight < 80) return; // at the bottom the viewport's own follow keeps it
    const top = el.getBoundingClientRect().top;
    const node = Array.from(el.querySelectorAll<HTMLElement>("[data-message-seq]")).find(candidate => candidate.getBoundingClientRect().bottom > top + 1);
    if (!node) return;
    const offset = node.getBoundingClientRect().top - top;
    const until = performance.now() + 450;
    let frame = 0;
    const stop = () => { cancelAnimationFrame(frame); el.removeEventListener("wheel", stop); el.removeEventListener("pointerdown", stop); el.removeEventListener("touchstart", stop); };
    const hold = () => {
      if (!node.isConnected) return stop();
      el.scrollTop += node.getBoundingClientRect().top - el.getBoundingClientRect().top - offset;
      if (performance.now() < until) frame = requestAnimationFrame(hold); else stop();
    };
    el.addEventListener("wheel", stop, { passive: true }); el.addEventListener("pointerdown", stop); el.addEventListener("touchstart", stop, { passive: true });
    frame = requestAnimationFrame(hold);
    return stop;
  }, [cardRoom, viewport.scrollRef]);

  if (!indexInitialized.current && workspaceError) return <BootScreen label={t("workspace.connection_unavailable")} immediate><h2>{t("workspace.connection_unavailable")}</h2><p>{workspaceError}</p><button className="primary-button" disabled={loading} onClick={() => void refreshIndex()}>{loading ? t("action.retrying") : t("common:action.retry")}</button></BootScreen>;
  if (loading && !conversations.length) return <BootScreen label={t("workspace.loading")} />;

  const artifactsOn = columnMode ? surfaces.widgets.includes("artifacts") : cover === "artifacts";
  const desktopNode = (leaving = false) => macSource && (desktopOpen || leaving) ? <Suspense key={`mac:${macSource.computer_id}`} fallback={null}><FloatingDesktop label={browserT("browser.source")} docked={columnMode} folded={columnMode && computerFolded} expanded={desktopExpanded} small={desktopSmall || leaving} sheet={desktopSheet} onClose={() => closeWidgetId("computer")}><BotBrowserPanel source={macSource} expanded={desktopExpanded} folded={columnMode && computerFolded} onClose={() => closeWidgetId("computer")} onExpand={() => setDesktopSize(desktopExpanded ? "small" : "big")} /></FloatingDesktop></Suspense> : chosenSource?.kind !== "mac_browser" && viewDesktopBot && (desktopOpen || leaving) ? <Suspense key={viewDesktopBot.id} fallback={<DesktopSkeleton botId={viewDesktopBot.id} />}><FloatingDesktop docked={columnMode} folded={columnMode && computerFolded} expanded={desktopExpanded} small={desktopSmall || leaving} sheet={desktopSheet} onClose={() => closeWidgetId("computer")}><Suspense fallback={<DesktopSkeleton botId={viewDesktopBot.id} />}><BotDesktopPanel presence={desktopPresence} folded={columnMode && computerFolded} onFold={columnMode ? () => foldWidgetId("computer") : undefined} onUnfold={() => foldWidgetId("computer")} autoConnect={currentDesktopOwner?.kind === "bot"} expanded={desktopExpanded} sheet={desktopSheet} botId={viewDesktopBot.id} botName={viewDesktopBot.name} members={bots.map(bot => ({ id: bot.id, name: bot.name }))} onClose={(reason) => { if (reason === "shutdown") { setDesktopReady(false); setComputerInfo({ state: "stopped" }); } closeWidgetId("computer"); }} onReadyChange={setDesktopReady} onExpandedChange={expanded => { setDesktopSize(expanded ? "big" : "small"); openWidgetId("computer"); }} /></Suspense></FloatingDesktop></Suspense> : null;
  const computers: ComputerEntry[] = [
    ...(desktopBot ? [{ key: "bot", label: computerStateLabel, icon: "monitor" as const, pressed: computerShown && !macSource, on: computerOn, driven: computerOn && !!activeDesktopOwner, attrs: { "data-computer-toggle": "", "data-computer-state": computerOn ? (activeDesktopOwner ? "driven" : "on") : "off" },
      toggle: () => { if (computerShown && !macSource) closeWidgetId("computer"); else chooseComputer({ kind: "bot_computer", bot_id: desktopBot.id }); } }] : []),
    ...(nativePanel.bridge ? [{ key: "native", label: browserT("browser.panel"), icon: "globe" as const, pressed: nativePanel.open, attrs: { "data-native-browser-toggle": "" }, toggle: () => nativePanel.request(!nativePanel.open) }]
      : browserSources.sources.map(source => ({ key: source.computer_id, label: browserT("browser.look", { id: source.computer_id }), icon: "globe" as const, pressed: computerShown && macSource?.computer_id === source.computer_id, attrs: { "data-browser-toggle": source.computer_id },
        toggle: () => { if (computerShown && macSource?.computer_id === source.computer_id) closeWidgetId("computer"); else chooseComputer({ kind: "mac_browser", computer_id: source.computer_id }); } }))),
  ];
  // The markup of the sidebar views, widgets and covers lives in the lazy chunk SurfaceViews; the workspace hands it the data and the few actions.
  // Opening a conversation from a panel: the cover has done its job; below 1100 px the drawer covers the chat and goes too. On the wide
  // layout the sidebar stays and re-scopes to the new conversation (the Archive stays as it is).
  const openFromPanel = (id: string) => { selectConversation(id); setMobileList(false); setSurfaces(state => closeCover(columnMode ? state : closeSidebar(state))); };
  const surfaceCtx: SurfaceCtx = {
    active, activeBot, desktopBot, bots, conversations, botById, memoriesReady: loadedId === activeId, memoriesError: snapshotError, retryMemories: () => snapshotRetryNow.current?.(), memories, setMemories, terminalBotId, viewOnlyChat, setViewOnlyChat, refreshToken: scheduleRefresh, roster: taskView?.roster, lastMessageId: messages.at(-1)?.id,
    botCard: active?.kind === "dm" && activeBot ? { runs, activities: toolActivities, asked: new Set(questions.items.filter(question => question.status === "pending").map(question => question.run_id)), asleep: conversationMotion(active, activeBot.id) === "sleeping",
      computerState: computerStateLabel, memoryCount: memories.length, taskCount: taskItems.filter(task => task.bot_id === activeBot.id && !["done", "failed", "cancelled"].includes(task.status)).length,
      openComputer: desktopBot ? () => chooseComputer({ kind: "bot_computer", bot_id: desktopBot.id }) : undefined, openMemory: () => openSidebarId("memory"), openTasks: () => openSidebarId("tasks") } : undefined,
    artifactsMode, setArtifactsMode, surf: setSurfaces, openSettings: tab => openSettings(tab),
    openUsageSettings: () => { setUsageBotId(active?.kind === "dm" ? active.bot_id ?? "" : ""); openSettings("usage"); },
    running: runs.some(run => run.conversation_id === activeId && (run.status === "running" || run.status === "queued")), openConversation: openFromPanel, openMessage: (conversationId, messageId) => { setTaskFocus({ conversationId, kind: "message", id: messageId }); openFromPanel(conversationId); },
    openSettingsAdvanced: botId => { setPortabilityBotID(botId); setPortabilityFile(undefined); openSettings("advanced"); },
    mergeArchived, refreshAfterArchive, confirmDelete, updateBot, createGroup, makePrimary, updateGroup, reloadGroup,
  };
  const sidebarLoading = <DelayedFeedback><div className="inline-state" role="status"><div className="spinner" /></div></DelayedFeedback>;
  const sidebarContent = (id: SidebarId) => <Suspense fallback={id === "memory" || id === "usage" ? <div className="detail-content surface-loading"><div className="detail-heading"><h2>{t(id === "memory" ? "contextCard.title.memory" : "contextCard.title.usage")}</h2><button className="close-button" aria-label={t("detail.close")} onClick={closeSidebarPanel}><Icon name="close" size={18}/></button></div><div className="surface-skeleton" aria-busy="true"><i/><i/><i/></div></div> : sidebarLoading}><SidebarView id={id} ctx={surfaceCtx} /></Suspense>;
  const sidebarExpandedShown = surfaces.sidebar ? surfaces.sidebar.expanded : lastSidebarExpanded.current;
  const settingsShown = displayedPage === "settings";
  const drawerShown = !columnMode && !settingsShown && displayedSidebar !== null;
  return (
    <div inert={Boolean(deleteTarget || hidePrimaryTarget || (viewOnlyChat && (isDesktop || compactViewport))) || undefined} className={`workspace${keyboardViewportOpen ? " keyboard-viewport-open" : ""}`} style={viewportHeight ? { "--viewport-height": `${viewportHeight}px` } as React.CSSProperties : undefined}>
      {!settingsShown && <Suspense fallback={null}><UpdateBanner /></Suspense>}
      {workspaceErrorShown && indexInitialized.current && !proxyChallenged && <Suspense fallback={null}><WorkspaceToast onRetry={() => void refreshIndex()} /></Suspense>}
      {nativePanel.failure && <Suspense fallback={null}><WorkspaceToast key={nativePanel.failure} failure={nativePanel.failure} message={browserT(`browser.status.${nativePanel.failure}`)} /></Suspense>}
      {computerInfo && ((computerInfo.state !== "ready" && computerInfo.state !== "hibernated" && computerInfo.state !== "hibernating") || computerHealthAlert(computerInfo.health)) && !computerShown && <div className="computer-preparing" role="status"><strong>{computerInfo.guest_outdated_required ? t("computer.outdated_required") : computerInfo.error_code === "org_resource_missing" ? t("computer.org_missing") : computerInfo.health?.state === "unresponsive" ? t("computer.health_unresponsive") : computerInfo.health?.state === "restarting" ? t("computer.health_restarting") : computerInfo.state === "starting" ? t("computer.starting") : computerInfo.state === "resuming" ? t("computer.resuming") : computerInfo.state === "error" ? t("computer.failed") : computerInfo.state === "stopped" ? t("computer.stopped") : t("computer.unavailable")}</strong><span>{(computerInfo.state === "starting" || computerInfo.state === "resuming") && computerInfo.phase ? ` · ${computerPhaseKeys[computerInfo.phase] ? t(computerPhaseKeys[computerInfo.phase]) : computerInfo.phase}` : ""}{computerInfo.error && !computerInfo.guest_outdated_required ? ` · ${computerInfo.error}` : ""}</span>{computerInfo.guest_outdated_required ? <button className="secondary-button" onClick={() => openSettings("computer")}>{t("computer.open_environments")}</button> : (computerInfo.state === "error" || computerInfo.state === "stopped" || computerInfo.health?.state === "unresponsive") && <button className="secondary-button" onClick={() => void api.computerRetry().then(() => api.computerInfo()).then(setComputerInfo).catch(cause => setError(errorText(cause)))}>{t("common:action.retry")}</button>}</div>}
      <div className="native-titlebar" aria-hidden="true" /><ActionHints /><div className={`workspace-grid${sidebarFolded ? " sidebar-collapsed" : ""}${macNarrow ? " mac-narrow" : ""}${contextPanelOpen ? " context-open" : ""}${columnMode && surfaces.sidebar ? " sidebar-open" : ""}${nativePanel.open ? " native-panel-open" : ""}${columnMode && sidebarExpandedShown ? " sidebar-expanded" : ""}${cover ? " cover-open" : ""}`}>
        {macNarrow && narrowSidebarOpen && <div className="sidebar-scrim" aria-hidden="true" onClick={() => setNarrowSidebarOpen(false)} />}
        <Sidebar CreateMenu={menus?.CreateMenu} sortable={sortable} sidebarDnd={sidebarDnd} mobileList={mobileList} modalPanelOpen={modalPanelOpen} desktopExpanded={desktopExpanded} sidebarFolded={sidebarFolded} t={t} setSidebarFolded={setSidebarFolded} setSearchOpen={setSearchOpen} createMenuRef={createMenuRef} createMenuTriggerRef={createMenuTriggerRef} setCreateMenuOpen={setCreateMenuOpen} createMenuOpen={createMenuOpen} botImportRef={botImportRef} importBotPackage={importBotPackage} showCreateMenu={showCreateMenu} newBotBusy={newBotBusy} createNewBot={createNewBot} openCoverPanel={openCoverPanel} botImportBusy={botImportBusy} newBotError={newBotError} botImportError={botImportError} sidebarListRef={sidebarListRef} pinnedConversations={pinnedConversations} pinTile={pinTile} customSidebarSections={customSidebarSections} conversationRow={conversationRow} defaultSidebarConversations={defaultSidebarConversations} sidebarDndEnabled={sidebarDndEnabled} sidebarLiveRef={sidebarLiveRef} onboardingState={onboardingState} visibleSidebarConversations={visibleSidebarConversations} bots={bots} conversations={conversations} modelReady={modelReady} setOnboardingForced={setOnboardingForced} linkConnected={linkConnected} setPortabilityBotID={setPortabilityBotID} setPortabilityFile={setPortabilityFile} openSettings={openSettings} setUsageBotId={setUsageBotId} setArtifactsMode={setArtifactsMode} />
        {onboardingState === "sheet" && onboarding && <Suspense fallback={null}><Onboarding initialStep={onboardingForced ?? resumeStep(onboarding, modelReady)} modelConfigured={modelReady} bot={bots.find((item) => !item.archived)} onProgress={saveOnboarding} onModelChanged={refreshConfig} onBot={adoptOnboardingBot} onSkip={() => { saveOnboarding({ skipped: true }); setOnboardingForced(null); }} onFinish={finishOnboarding} /></Suspense>}
        <SlotView slot={sidebarMenuSlot}>{menu => menus && <menus.default menu={menu} t={t} bot={botByConversationId.get(menu.conversation.id)} a={{ close: () => setSidebarContextMenu(null), togglePinned, sections: sidebarLayout.sections, setSection, markUnread, fail: (cause: unknown) => setError(errorText(cause)), openBotSettings: c => { selectConversation(c.id); setMobileList(false); openCoverPanel("bot-settings"); setSidebarContextMenu(null); }, makePrimary, copyLink: copyConversationLink, archive: setArchived, confirmDelete }} />}</SlotView>

        <ChatPane header={atTop => active ? <ChatHeader atTop={atTop} covered={!!cover} botCardOpen={columnWidgets.includes("bot")} openBotCard={openBotCard} cats={{ runs, activities: toolActivities, asked: new Set(questions.items.filter(question => question.status === "pending").map(question => question.run_id)), asleep: botId => active ? conversationMotion(active, botId) === "sleeping" : false }} chatHeaderRef={chatHeaderRef} setMobileList={setMobileList} t={t} active={active} columnMode={columnMode} toggleWidgetId={toggleWidgetId} openCoverPanel={openCoverPanel} botById={botById} computers={computers} artifactsOn={artifactsOn} setArtifactsMode={setArtifactsMode} toggleSidebarId={toggleSidebarId} headerMenuTriggerRef={headerMenuTriggerRef} headerMenuOpen={headerMenuOpen} setHeaderMenuOpen={setHeaderMenuOpen} desktopBot={desktopBot} openTerminalSurface={openTerminalSurface} openSidebarId={openSidebarId} openWidgetId={openWidgetId} copyConversationLink={copyConversationLink} confirmDelete={confirmDelete} /> : null} mobileList={mobileList} modalPanelOpen={modalPanelOpen} desktopExpanded={desktopExpanded} active={active} routeUnavailable={routeUnavailable} t={t} setMobileList={setMobileList} onboardingState={onboardingState} createNewBot={createNewBot} newBotBusy={newBotBusy} newBotError={newBotError} runs={runs} botById={botById} loadedId={loadedId} loadingMessages={loadingMessages} taskAnnouncements={taskAnnouncements} viewport={viewport} expiryFinishing={expiryFinishing} runStarts={runStarts} activityView={activityView} computerLook={computerLook} messageListRef={messageListRef} onMessageListPointerOver={onMessageListPointerOver} onMessageListPointerLeave={onMessageListPointerLeave} onMessageListFocus={onMessageListFocus} onMessageListBlur={onMessageListBlur} onMessageListKeyDown={onMessageListKeyDown} jumpLatest={jumpLatest} historyError={historyError} loadOlder={loadOlder} loadingOlder={loadingOlder} hasMore={hasMore} questions={questions} mailDrafts={mailDrafts} snapshotError={snapshotError} refreshTaskStatus={refreshTaskStatus} setSnapshotError={setSnapshotError} conversationItems={conversationItems} greeting={greeting} timelineFrom={timelineFrom} taskView={taskView} placementsAt={placementsAt} renderPlacement={renderPlacement} ownedBy={ownedBy} timezone={timezone} refreshConversations={refreshConversations} ownerAnchors={ownerAnchors} runById={runById} toolTimeline={toolTimeline} foldedProgress={foldedProgress} ownedRunIDs={ownedRunIDs} keepActivityRun={keepActivityRun} toolSummaryAnchors={toolSummaryAnchors} terminalToolSummaries={terminalToolSummaries} toolSummaryErrorAnchors={toolSummaryErrorAnchors} toolSummaryState={toolSummaryState} toolSummaryLoadingAnchors={toolSummaryLoadingAnchors} loadedToolActivities={loadedToolActivities} drafts={drafts} messageById={messageById} quoteTimeline={quoteTimeline} quoteIndexById={quoteIndexById} messageRecordIDs={messageRecordIDs} runHasActivity={runHasActivity} runsForMessage={runsForMessage} lastReplySeq={lastReplySeq} knownBotNames={knownBotNames} arrivalIDs={arrivalIDs} streamedIDs={streamedIDs} streamConnected={streamConnected} traceRunFor={traceRunFor} toolDetailState={toolDetailState} activeRunIds={activeRunIds} scheduleOccurrences={scheduleOccurrences} scheduleCardPending={scheduleCardPending} scheduleMetadata={scheduleMetadata} conversations={conversations} bots={bots} canRetrySend={canRetrySend} showActivity={showActivity} revealedRuns={revealedRuns} botNames={botNames} reactionMenu={reactionMenu} placementDraws={placementDraws} rowHandlers={rowHandlers} draftStream={draftStream} activeBot={activeBot} messages={messages} updateComposerDraft={updateComposerDraft} openSidebarId={openSidebarId} loadToolDetails={loadToolDetails} pendingRuns={pendingRuns} error={error} proxyChallenged={proxyChallenged} openCoverPanel={openCoverPanel} taskItems={taskItems} openTasks={openTasks} conversationMotion={conversationMotion} toolActivities={toolActivities} runSignals={runSignals} stepRunIds={stepRunIds} setRuns={setRuns} setError={setError} avatarMention={avatarMention} composerDrafts={composerDrafts} send={send} config={config} connectModel={connectModel} composerDraftReady={composerDraftReady} />

        {modalPanelOpen && !settingsShown && <button type="button" className="detail-backdrop" aria-label={t("detail.close")} onClick={closeSidebarPanel} />}
        {columnMode && <Suspense fallback={columnWidgets.includes("usage") ? <aside className="detail-pane widget-column"><section className="context-card surface-loading"><div className="detail-heading"><h2>{t("contextCard.title.usage")}</h2><button className="close-button" aria-label={t("detail.close")} onClick={() => closeWidgetId("usage")}><Icon name="close" size={18}/></button></div><div className="surface-skeleton" aria-busy="true"><i/><i/><i/></div></section></aside> : null}><WidgetColumn ids={columnWidgets} order={surfaces.order} folded={columnFolded} focus={surfaces.focus} covered={!!cover} onFold={foldWidgetId} onClose={closeWidgetId} onMove={moveWidgetId} render={(id, leaving) => id === "computer" ? desktopNode(leaving) : id === "members" && active?.kind !== "group" || !active ? null : <WidgetView key={active.id} id={id} ctx={surfaceCtx} leaving={leaving} focusN={surfaces.focus.id === id ? surfaces.focus.n : 0} />} /></Suspense>}
        {columnMode && displayedSidebar && <aside className={`sidebar-pane${surfaces.sidebar ? "" : " is-leaving"}${cover ? " is-covered" : ""}`} inert={!surfaces.sidebar || undefined} aria-label={t("detail.label")}>
          <Suspense fallback={<div className="sidebar-shell">{displayedSidebar === "memory" || displayedSidebar === "usage" ? sidebarContent(displayedSidebar) : sidebarLoading}</div>}><SidebarShell id={displayedSidebar} expanded={sidebarExpandedShown} focusN={surfaces.focus.id === displayedSidebar ? surfaces.focus.n : 0} onExpand={() => setSurfaces(toggleSidebarExpanded)} onClose={closeSidebarPanel} content={sidebarContent} /></Suspense>
        </aside>}
        {/* Settings and the narrow drawer are modal panes: Radix FocusScope keeps Tab inside while modalPanelOpen (a dialog or menu
            above it pauses the scope). Entry focus and return are the effect above. */}
        <FocusScope asChild trapped={modalPanelOpen} loop onMountAutoFocus={keepFocus} onUnmountAutoFocus={keepFocus}><aside ref={detailPaneRef} className={`detail-pane ${settingsShown || drawerShown ? "visible" : ""} ${drawerShown ? "context-panel" : ""} ${(settingsShown && !settingsOpen) || (drawerShown && !surfaces.sidebar) ? "surface-exiting" : ""}${settingsShown ? " settings-page" : ""}`} inert={!(settingsOpen || (!columnMode && surfaces.sidebar && !cover)) || undefined} role={modalPanelOpen ? "dialog" : undefined} aria-modal={modalPanelOpen ? true : undefined} aria-label={settingsShown ? t("common:nav.settings") : displayedSidebar === "terminal" ? t("header.terminal") : t("detail.label")} tabIndex={modalPanelOpen ? -1 : undefined}>
          {settingsShown && <Suspense fallback={<SettingsSkeleton onClose={closeCoverPanel} />}><AfterPaint fallback={<SettingsSkeleton onClose={closeCoverPanel} />}><SettingsPanel tab={settingsTab} onTab={setSettingsTab} onClose={closeCoverPanel} entry={settingsEntry} refreshToken={extensionRefresh} codexRefresh={codexStatusRefresh} onCodexConfigured={() => void refreshIndex()} pages={{ bots, conversation: active, timezone, usageBotId, portabilityBotID, portabilityFile, onPortabilityFileConsumed: () => setPortabilityFile(undefined), appearance, extensionRefresh, providersRefresh: codexStatusRefresh, onProvidersConfigured: () => void refreshIndex(), openTab: (tab) => openSettings(tab) }} /></AfterPaint></Suspense>}
          {drawerShown && displayedSidebar && sidebarContent(displayedSidebar)}
        </aside></FocusScope>
        {displayedCover && displayedCover !== "approvals" && (displayedCover !== "bot-settings" || botSettingsReady) && <Suspense fallback={null}><CoverShell id={displayedCover} leaving={!cover} wide={displayedCover === "artifacts"} returnFocus={panelOpener.current} onClose={closeCoverPanel}><CoverView id={displayedCover} ctx={surfaceCtx} /></CoverShell></Suspense>}
        {cover === "approvals" && active && <Suspense fallback={null}><RememberedApprovalsSheet conversationId={active.id} onClose={closeCoverPanel} /></Suspense>}
      </div>
      {!columnMode && desktopNode()}
      {searchOpen && <Suspense fallback={null}><SearchPalette conversations={conversations} onClose={() => setSearchOpen(false)} onOpenConversation={openFromPanel} onOpenMessage={surfaceCtx.openMessage} actions={{ newBot: () => void createNewBot(), newGroup: () => openCoverPanel("new-group"), settings: () => openSettings("general", "home") }} /></Suspense>}
      {deleteTarget && <Suspense fallback={null}><DeleteConversationDialog target={deleteTarget} bots={bots} onDelete={deleteConversation} onClose={() => setDeleteTarget(null)} returnFocus={dialogOpener.current} /></Suspense>}
      {hidePrimaryTarget && <Suspense fallback={null}><ReassignPrimaryDialog bot={hidePrimaryTarget} bots={bots} onConfirm={async (successorId) => { await api.setBotArchived(hidePrimaryTarget.id, true, successorId); setSidebarContextMenu(null); await refreshIndex(); }} onClose={() => setHidePrimaryTarget(null)} returnFocus={dialogOpener.current} /></Suspense>}
      {viewOnlyChat && !columnMode && <Suspense fallback={null}><ViewOnlyChat target={viewOnlyChat} bots={bots} returnFocus={viewOnlyChatTriggerRef.current} onClose={() => setViewOnlyChat(null)} /></Suspense>}
      <FilePreviewHost />
    </div>
  );
}
const SNAPSHOT_CACHE_LIMIT = 50;
/** Blocks drawn before the first paint of a conversation that opens at its newest message. */
const FIRST_PAINT_BLOCKS = 12;
const SNAPSHOT_CACHE_MESSAGES = 60;
interface ConversationSnapshot { messages: Message[]; drafts: Record<string, StreamDraft>; runs: Run[]; toolActivities: ToolActivity[]; toolSummaries: ToolActivityRunSummary[]; memories: Memory[]; hasMore: boolean }
