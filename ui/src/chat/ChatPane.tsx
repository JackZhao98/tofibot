import { formatTime } from "./shared";
import { type ComputerSource } from "../botBrowserSource";
import { readReplyMarker } from "../replyMarker";
import { i18n } from "../i18n";
import { type OwnerPlacement } from "../activityPlacements";
import { precedingBlockBotIds, previousRenderedIndex } from "../messageTimeline";
import { DelayedFeedback } from "../DelayedFeedback";
import { isDesktop } from "../desktop";
import { dateInTimezone } from "../timezone";
import { GreetingSetup, QuestionCard, PlanDock } from "../lazyPanels";
import { TofiIcon as Icon } from "../icons";
import { Suspense } from "react";
import { type AvatarMotion } from "../BotAvatar";
import { CATS, EmptyCat, EmptyState } from "../EmptyCat";
import { receiptFor } from "../sendReceipts";
import { mergeRunUpdates } from "../runMerge";
import { DraftStream } from "../draftStream";
import type { TaskCardModel } from "../taskCards";
import { shouldShowReplyQuote } from "../replyQuote";
import { type ComposerReply } from "../composerDraft";
import type { Bot, Config, Conversation, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary, WorkItem } from "../types";
import { ComputerLookContext } from "../ComputerLookButton";
import { type CoverId, type SidebarId } from "../surfaces";
import { blockKey } from "./shared";
import { ActivityViewContext, CompactLiveLine, ExpiryFinishingContext, LiveRunStatus, RunStartContext, RunStatusAnnouncement, ToolActivityList, WorkingMembers, type RunSignal, type ToolDetailState } from "./RunActivity";
import { MessageRow, type RowHandlers, type RowModel } from "./MessageBubble";
import { Composer, type ComposerAttachment, type ComposerDraft } from "./Composer";
import type { TFunction } from "i18next";
import type { Question } from "../questionTimeline";
import type { MailDraft } from "../mailDrafts";
import type { ConversationItem } from "../questionTimeline";
import type { GreetingPlayback } from "../onboardingGreeting";
import type { ToolTimeline } from "../toolTimeline";
import type { ToolRunAnchors } from "../toolTimeline";
import type { ScheduleOccurrence } from "../scheduleOccurrences";
import type { Schedule } from "../types";
import type { Dispatch, RefObject, SetStateAction } from "react";
import { ToolSummaryState } from "./RunActivity";
import { ComposerDrafts, emptyComposerDraft } from "./Composer";
import { FirstRunHero, MailDraftCard } from "./shared";
import { attachmentUrl } from "../filePreviewModel";
import { type ViewOnlyChatTarget } from "../ViewOnlyChat";
import { lazy } from "react";
import { Notice } from "../ds/Notice/Notice";
import { Button } from "../ds/Button/Button";
import { Divider } from "../ds/Divider/Divider";
import { Ground } from "../ds/Ground/Ground";
import { ProgressiveBlur } from "../ds/ProgressiveBlur/ProgressiveBlur";
import { JumpButton } from "../ds/JumpButton/JumpButton";
import { dateTimeFormat } from "../timezone";
import { intlLocale } from "../i18n/format";

export interface ChatPaneProps {
  header: React.ReactNode;
  mobileList: boolean;
  modalPanelOpen: boolean;
  desktopExpanded: boolean;
  active: Conversation | undefined;
  routeUnavailable: boolean;
  t: TFunction<readonly ["chat", "common"], undefined>;
  setMobileList: Dispatch<SetStateAction<boolean>>;
  onboardingState: string;
  createNewBot: () => Promise<void>;
  newBotBusy: boolean;
  newBotError: string;
  runs: Run[];
  botById: Map<string, Bot>;
  loadedId: string | null;
  loadingMessages: boolean;
  taskAnnouncements: Map<string, string>;
  viewport: { scrollRef: RefObject<HTMLDivElement | null>; onScroll: () => void; preservePrepend: () => void; jumpLatest: () => void; awayFromBottom: () => boolean; unpositioned: boolean; unreadSeq: number | undefined; opensAtBottom: boolean; reposition: () => void; showJump: boolean; newCount: number; boundaryError: boolean; retryBoundary: () => void; };
  expiryFinishing: Set<string>;
  runStarts: Map<string, number>;
  activityView: { show: boolean; revealed: ReadonlySet<string>; reveal: (runId: string) => void; };
  computerLook: { open: (source?: ComputerSource | undefined) => void; sources: ComputerSource[]; defaultSource: ComputerSource | null; } | null;
  messageListRef: RefObject<HTMLDivElement | null>;
  onMessageListPointerOver: (event: React.PointerEvent<HTMLElement>) => void;
  onMessageListPointerLeave: (event: React.PointerEvent<HTMLElement>) => void;
  onMessageListFocus: (event: React.FocusEvent<HTMLElement>) => void;
  onMessageListBlur: (event: React.FocusEvent<HTMLElement>) => void;
  onMessageListKeyDown: (event: React.KeyboardEvent<HTMLElement>) => void;
  jumpLatest: () => void;
  historyError: string;
  loadOlder: () => Promise<boolean>;
  loadingOlder: boolean;
  hasMore: boolean;
  questions: { items: Question[]; error: string; loading: boolean; refresh: (resolved?: Question) => Promise<void>; };
  mailDrafts: { items: MailDraft[]; refresh: () => Promise<void>; error: string; };
  snapshotError: string;
  refreshTaskStatus: () => Promise<void>;
  setSnapshotError: Dispatch<SetStateAction<string>>;
  conversationItems: ConversationItem[];
  greeting: GreetingPlayback;
  timelineFrom: number;
  taskView: { models: Map<string, TaskCardModel>; folded: Set<string>; byMessage: Map<string, TaskCardModel[]>; roster: Map<string, TaskCardModel[]>; progressByRun: Map<string, string>; } | undefined;
  placementsAt: (kind: "message" | "question" | "draft" | undefined, id?: string) => OwnerPlacement[];
  renderPlacement: ({ owner, key, segment }: OwnerPlacement) => React.JSX.Element;
  ownedBy: (item: { run_id: string; bot_id: string; conversation_id: string; }) => boolean;
  timezone: string;
  refreshConversations: () => Promise<void>;
  ownerAnchors: { anchor: { kind: "draft" | "message" | "question"; id: string; } | undefined; botId: string; }[];
  runById: Map<string, Run>;
  toolTimeline: ToolTimeline;
  foldedProgress: { visible: Message[]; progressByFinalId: Map<string, Message[]>; };
  ownedRunIDs: Set<string>;
  keepActivityRun: (runId?: string) => boolean;
  toolSummaryAnchors: ToolRunAnchors<ToolActivityRunSummary>;
  terminalToolSummaries: ToolActivityRunSummary[];
  toolSummaryErrorAnchors: ToolRunAnchors<{ run_id: string; }>;
  toolSummaryState: Record<string, ToolSummaryState>;
  toolSummaryLoadingAnchors: ToolRunAnchors<{ run_id: string; }>;
  loadedToolActivities: ToolActivity[];
  drafts: Record<string, StreamDraft>;
  messageById: Map<string, Message>;
  quoteTimeline: Message[];
  quoteIndexById: Map<string, number>;
  messageRecordIDs: Set<string>;
  runHasActivity: (runId: string) => boolean;
  runsForMessage: Map<string, Run[]>;
  lastReplySeq: number;
  knownBotNames: RefObject<Map<string, string>>;
  arrivalIDs: RefObject<Set<string>>;
  streamedIDs: RefObject<Set<string>>;
  streamConnected: boolean;
  traceRunFor: (message: Message) => Run | undefined;
  toolDetailState: Record<string, ToolDetailState>;
  activeRunIds: Set<string>;
  scheduleOccurrences: ReadonlyMap<string, ScheduleOccurrence>;
  scheduleCardPending: (runId?: string) => boolean;
  scheduleMetadata: ReadonlyMap<string, Schedule>;
  conversations: Conversation[];
  bots: Bot[];
  canRetrySend: (clientMessageId: string) => boolean;
  showActivity: boolean;
  revealedRuns: ReadonlySet<string>;
  botNames: Map<string, string>;
  reactionMenu: { messageId: string; x: number; y: number; h?: number | undefined; } | null;
  placementDraws: (placement: OwnerPlacement) => boolean;
  rowHandlers: RowHandlers;
  draftStream: DraftStream;
  activeBot: Bot | undefined;
  messages: Message[];
  updateComposerDraft: (conversationId: string, update: (draft: ComposerDraft) => ComposerDraft) => void;
  openSidebarId: (id: SidebarId) => void;
  loadToolDetails: (runId: string, offset: number) => Promise<void>;
  pendingRuns: Run[];
  error: string;
  proxyChallenged: boolean;
  openCoverPanel: (id: CoverId) => void;
  taskItems: readonly WorkItem[];
  openTasks: number;
  conversationMotion: (conversation: Conversation, botId?: string | undefined) => AvatarMotion;
  toolActivities: ToolActivity[];
  runSignals: Record<string, RunSignal>;
  stepRunIds: string[];
  setRuns: Dispatch<SetStateAction<Run[]>>;
  setError: Dispatch<SetStateAction<string>>;
  avatarMention: { name: string; conversationId: string; nonce: number; } | null;
  composerDrafts: ComposerDrafts;
  send: (content: string, clientMessageId: string, files?: ComposerAttachment[], draftSignature?: string, flightOrigin?: DOMRect | undefined, plain?: string, reply?: ComposerReply | undefined) => Promise<boolean>;
  config: Config | null;
  connectModel: () => void;
  composerDraftReady: boolean;
}

export function ChatPane({ header, mobileList, modalPanelOpen, desktopExpanded, active, routeUnavailable, t, setMobileList, onboardingState, createNewBot, newBotBusy, newBotError, runs, botById, loadedId, loadingMessages, taskAnnouncements, viewport, expiryFinishing, runStarts, activityView, computerLook, messageListRef, onMessageListPointerOver, onMessageListPointerLeave, onMessageListFocus, onMessageListBlur, onMessageListKeyDown, jumpLatest, historyError, loadOlder, loadingOlder, hasMore, questions, mailDrafts, snapshotError, refreshTaskStatus, setSnapshotError, conversationItems, greeting, timelineFrom, taskView, placementsAt, renderPlacement, ownedBy, timezone, refreshConversations, ownerAnchors, runById, toolTimeline, foldedProgress, ownedRunIDs, keepActivityRun, toolSummaryAnchors, terminalToolSummaries, toolSummaryErrorAnchors, toolSummaryState, toolSummaryLoadingAnchors, loadedToolActivities, drafts, messageById, quoteTimeline, quoteIndexById, messageRecordIDs, runHasActivity, runsForMessage, lastReplySeq, knownBotNames, arrivalIDs, streamedIDs, streamConnected, traceRunFor, toolDetailState, activeRunIds, scheduleOccurrences, scheduleCardPending, scheduleMetadata, conversations, bots, canRetrySend, showActivity, revealedRuns, botNames, reactionMenu, placementDraws, rowHandlers, draftStream, activeBot, messages, updateComposerDraft, openSidebarId, loadToolDetails, pendingRuns, error, proxyChallenged, openCoverPanel, taskItems, openTasks, conversationMotion, toolActivities, runSignals, stepRunIds, setRuns, setError, avatarMention, composerDrafts, send, config, connectModel, composerDraftReady }: ChatPaneProps) {
  return (
    <main className={`chat-pane ${!mobileList ? "mobile-chat" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          {!active ? routeUnavailable ? <div className="conversation-empty" role="alert"><EmptyCat look={CATS.nori} pose="curious" size={72} name="route-unavailable" /><h2>{t("route.unavailable_title")}</h2><p>{t("route.unavailable_body")}</p><button className="secondary-button" onClick={() => setMobileList(true)}>{t("route.back_to_list")}</button></div> : onboardingState === "sheet" ? null : <Suspense fallback={null}><FirstRunHero onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} /></Suspense> : <>
            <Ground />
            {header}
            <RunStatusAnnouncement key={`run-status-${active.id}`} conversationId={active.id} runs={runs} botById={botById} ready={loadedId === active.id && !loadingMessages} taskAnnouncements={taskAnnouncements} />
            <div className="chat-body">
              <div className="message-scroll" ref={viewport.scrollRef} onScroll={viewport.onScroll}>
                <ExpiryFinishingContext.Provider value={expiryFinishing}><RunStartContext.Provider value={runStarts}><ActivityViewContext.Provider value={activityView}><ComputerLookContext.Provider value={computerLook}><div className={`message-list${viewport.unpositioned ? " is-positioning" : ""}`} ref={messageListRef} onPointerOver={isDesktop ? undefined : onMessageListPointerOver} onPointerLeave={isDesktop ? undefined : onMessageListPointerLeave} onFocus={isDesktop ? undefined : onMessageListFocus} onBlur={isDesktop ? undefined : onMessageListBlur} onKeyDown={isDesktop ? undefined : onMessageListKeyDown}>
                  {viewport.boundaryError && <Notice tone="danger" icon="alert" className="history-error" action={<Button variant="ghost" size="sm" onClick={viewport.retryBoundary}>{t("common:action.retry")}</Button>}>{t("history.boundary_failed")}</Notice>}
                  {historyError && !viewport.boundaryError && <Notice tone="danger" icon="alert" className="history-error history-error-older" action={<Button variant="ghost" size="sm" onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.retrying") : t("common:action.retry")}</Button>}>{t("history.older_failed", { error: historyError })}</Notice>}
                  {hasMore && <Notice icon="history" className="load-older-row" action={<Button variant="ghost" size="sm" className="load-older" onClick={() => void loadOlder()} disabled={loadingOlder}>{loadingOlder ? t("action.loading") : t("history.load_older")}</Button>} />}
                  {(questions.error || mailDrafts.error || snapshotError) && <div className="task-state-read-error"><span>{i18n.t("tasks:issue.status_unavailable")}</span><button type="button" className="text-button" onClick={() => void refreshTaskStatus().catch(() => setSnapshotError("read_unavailable"))}>{i18n.t("tasks:issue.action.refresh_status")}</button></div>}
                  {snapshotError && loadedId !== active.id && !loadingMessages ? <div className="inline-state" role="status" data-snapshot-unavailable="true"><div className="spinner" />{t("history.unavailable_retrying")}</div> : loadingMessages ? <DelayedFeedback key={`loading-${active.id}`}><div className="inline-state"><div className="spinner" />{t("history.loading_messages")}</div></DelayedFeedback> : loadedId !== active.id ? null : conversationItems.length === 0 ? greeting.stage === "setup" ? <GreetingSetup /> : greeting.stage === "typing" ? null :
                    <EmptyState name="conversation" className="conversation-empty" look={CATS.mochi} pose="curious" size={84}><h2>{t("empty.start_chat")}</h2><p>{t("empty.start_chat_body")}</p></EmptyState> :
                    conversationItems.map((item, index) => {
                      if (index < timelineFrom) return null;
                      // A part holder's message folds under its parent's task card (taskCards.ts); an Activity block anchored to it stays.
                      if (item.kind === "message" && taskView?.folded.has(item.message.id)) return placementsAt("message", item.message.id).map(renderPlacement);
                      const previousItem = conversationItems[index - 1];
                      const previousTime = previousItem?.kind === "message" ? previousItem.message.created_at : previousItem?.kind === "question" ? previousItem.question.created_at : previousItem?.draft.created_at;
                      if (item.kind === "mail_draft") {
                        const draft = item.draft;
                        if (ownedBy(draft)) return placementsAt("draft", draft.draft_id).map(renderPlacement);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(draft.created_at, timezone) || +new Date(draft.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`mail-draft-${draft.draft_id}`}>{divider && <Divider className="date-divider">{formatDateDivider(draft.created_at, timezone)}</Divider>}<Suspense fallback={null}><MailDraftCard draft={draft} bot={botById.get(draft.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async () => { await mailDrafts.refresh(); await refreshConversations(); }} /></Suspense></div>;
                      }
                      if (item.kind === "question") {
                        const question = item.question;
                        if (ownedBy(question)) return placementsAt("question", question.question_id).map(renderPlacement);
                        const divider = !previousTime || dateInTimezone(previousTime, timezone) !== dateInTimezone(question.created_at, timezone) || +new Date(question.created_at) - +new Date(previousTime) >= 10 * 60_000;
                        return <div className="message-block" key={`question-${question.question_id}`}>{divider && <Divider className="date-divider">{formatDateDivider(question.created_at, timezone)}</Divider>}<Suspense fallback={null}><QuestionCard item={question} bot={botById.get(question.bot_id)} group={active.kind === "group"} archived={active.archived} onChanged={async (resolved) => { await questions.refresh(resolved); await refreshConversations(); }} /></Suspense></div>;
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
                      const ownersHere = placementsAt("message", message.id);
                      const model: RowModel = {
                        message, isLast: index === conversationItems.length - 1, arrival: arrivalIDs.current.has(blockKey(message)), streamed: streamedIDs.current.has(message.id),
                        dividerLabel: showDivider ? formatDateDivider(message.created_at, timezone) : undefined, unread, timezone, canReact, isGroup: active.kind === "group", archived: Boolean(active.archived),
                        isStub, liveId: isStub || drafts[message.id]?.status === "active" ? message.id : undefined, drafting: drafts[message.id]?.status === "active" && streamConnected,
                        replyTarget: showReplyQuote ? replyTarget : undefined, replyTargetName: replyTarget?.role === "user" ? t("message.you") : replyTarget?.sender_bot_name ?? (replyTarget?.sender_bot_id ? botById.get(replyTarget.sender_bot_id)?.name ?? knownBotNames.current.get(replyTarget.sender_bot_id) : undefined),
                        senderName: message.sender_bot_name ?? (message.sender_bot_id ? knownBotNames.current.get(message.sender_bot_id) : undefined),
                        targetBot: message.notice?.to_bot_id ? botById.get(message.notice.to_bot_id) : undefined, sender: message.sender_bot_id ? botById.get(message.sender_bot_id) : undefined,
                        run: message.run_id ? runById.get(message.run_id) : undefined, traceRun: traceRunFor(message),
                        toolLists: hasProcessTools || hasAfterTools ? { details: toolDetailState, botById, activeRunIds } : undefined,
                        processTools: hasProcessTools ? [...processTools, ...processAggregateTools] : undefined, processProgress: hasProcessTools ? processProgress : undefined, processSummaries: hasProcessTools ? processSummaries : undefined, processSummaryErrors: hasProcessTools ? processSummaryErrors : undefined, processSummaryLoading: hasProcessTools ? processSummaryLoading : undefined,
                        afterTools: hasAfterTools ? [...afterTools, ...afterAggregateTools] : undefined, afterSummaries: hasAfterTools ? afterSummaries : undefined, afterSummaryErrors: hasAfterTools ? afterSummaryErrors : undefined, afterSummaryLoading: hasAfterTools ? afterSummaryLoading : undefined,
                        scheduleOccurrence: message.run_id ? scheduleOccurrences.get(message.run_id) : undefined, schedulePending: scheduleCardPending(message.run_id), schedule: message.run_id ? scheduleMetadata.get(scheduleOccurrences.get(message.run_id)?.schedule_id ?? "") : undefined,
                        canRetrySchedule: !(active.archived || ownedRunIDs.has(message.run_id ?? "")),
                        relatedChat: relatedChatTarget(message, active, conversations, bots), compact, showIdentity,
                        noticeTargetAvailable: !message.notice || conversations.some(item => item.id === message.notice?.target_conversation_id),
                        allowOpenDM: active.kind === "group" || message.kind === "forward_result",
                        canDraftReply: !active.archived && !isDesktop && message.card?.type === "mail", canMention: !active.archived,
                        receipt: receiptState ?? undefined, receiptNames, canRetryReceipt: Boolean(message.local && message.client_message_id && canRetrySend(message.client_message_id)),
                        hasBar, copyText, activityRunId, activityToggle: Boolean(activityRunId && !showActivity), activityOpen: activityRunId ? revealedRuns.has(activityRunId) : false, canReply: !active.archived,
                        botNames, menuPosition: reactionMenu?.messageId === message.id ? reactionMenu : null,
                        ownerNodes: ownersHere.length ? ownersHere.map(renderPlacement) : undefined, ownsNext: ownersHere.some(placementDraws),
                        taskCards: taskView?.byMessage.get(message.id),
                      };
                      return <MessageRow key={blockKey(message)} model={model} h={rowHandlers} stream={draftStream} />;
                    })}
                  {greeting.stage === "typing" && activeBot && <CompactLiveLine key={`greeting-${greeting.shown}`} label={`${t("live.thinking")}…`} botId={activeBot.id} botName={activeBot.name} at={activeBot.created_at} showName={false} ariaLabel={t("live.status_label", { name: activeBot.name })} />}
                  {active.kind === "dm" && !active.archived && activeBot && !activeBot.archived && loadedId === active.id && !loadingMessages && !snapshotError && greeting.stage === "done" && messages.every(message => message.role !== "user") && <Suspense fallback={null}>{activeBot.onboarding_pending
                    ? <HandoverChips botId={activeBot.id} onPick={message => { updateComposerDraft(active.id, draft => ({ ...draft, content: message })); requestAnimationFrame(() => { const box = document.querySelector<HTMLTextAreaElement>("form.composer textarea"); box?.focus(); box?.setSelectionRange(message.length, message.length); }); }} />
                    : <DayOneTemplates conversationId={active.id} botId={activeBot.id} onOpenTasks={() => openSidebarId("tasks")} />}</Suspense>}
                  <ToolActivityList activities={toolTimeline.fallback} summaries={terminalToolSummaries.filter(summary => toolTimeline.fallback.some(activity => activity.run_id === summary.run_id))} details={toolDetailState} onLoadDetails={loadToolDetails} botById={botById} activeRunIds={activeRunIds} group={active.kind === "group"} />
                  {placementsAt(undefined).map(renderPlacement)}
                  {!isDesktop && pendingRuns.filter(run => !ownedRunIDs.has(run.id)).map(run => <LiveRunStatus key={run.id} run={run} botName={run.kind === "triage" ? "Tofi" : botById.get(run.bot_id)?.name ?? "Bot"} showName={active.kind === "group"} />)}
                  {error && !proxyChallenged && <Notice tone="danger" icon="error" className="error-banner system-notice">{error}</Notice>}
                </div></ComputerLookContext.Provider></ActivityViewContext.Provider></RunStartContext.Provider></ExpiryFinishingContext.Provider>
              </div>
              {viewport.showJump && <JumpButton className="jump-latest" label={t("history.jump_latest")} onClick={jumpLatest} />}
            </div>
            <ProgressiveBlur edge="top" className="chat-edge-top" />
            <ProgressiveBlur edge="bottom" className="chat-edge-bottom" />
            {active.kind === "group" && !active.archived && !active.bot_ids.some(id => botById.has(id) && !botById.get(id)?.archived) ? <div className="composer empty-group-composer"><span>{t("composer.no_members")}</span><button className="secondary-button" onClick={() => openCoverPanel("group-settings")}>{t("composer.add_members")}</button></div> : <Composer workStatus={<>{(runs.some(run => run.plan) || taskItems.some(task => task.checklist?.length) || openTasks > 0) && <PlanDock key={active.id} runs={runs} tasks={taskItems} botById={botById} group={active.kind === "group"} openTasks={openTasks} onOpenTasks={() => openSidebarId("tasks")} />}<WorkingMembers isGroup={active.kind === "group"} companionBotId={active.kind === "dm" ? active.bot_id || active.bot_ids[0] : undefined} companionMotion={active.kind === "dm" ? conversationMotion(active, active.bot_id || active.bot_ids[0]) : undefined} workingBotIds={active.working_bot_ids ?? []} drafts={Object.values(drafts)} runs={runs} botById={botById} toolActivities={toolActivities} signals={runSignals} connected={streamConnected} /></>} activeRun={runs.find(run => run.status === "running" || run.status === "queued" || run.status === "waiting")} allRuns={runs} stepRunIds={stepRunIds} latestReplyId={messages.filter(message => message.role === "assistant").at(-1)?.id} onRunsStopped={stopped => setRuns(current => mergeRunUpdates(current, stopped))} onStopError={setError} avatarMention={avatarMention} conversation={active} bots={bots} draft={composerDrafts[active.id] ?? emptyComposerDraft()} onDraftChange={updateComposerDraft} onSend={send} onSendStart={jumpLatest} onSent={jumpLatest} modelConfigured={config?.model_configured ?? false} dictationReady={config?.dictation_configured === true} onConnectModel={connectModel} draftReady={composerDraftReady} readOnly={Boolean(active.archived)} />}
          </>}
        </main>
  );
}

/** What Copy puts on the clipboard: the message as the reader sees it, without the sender context label or reply marker. */
export function copyableMessageText(message: Message) {
  const body = readReplyMarker(message.content.replace(/^\[sender [^\]\n]*? id=[^\]\n]+\]\s*/, "")).body.trim();
  return body.replace(/\]\(attachment:([^)\s]+)\)/g, (_, id) => `](${location.origin}${attachmentUrl(id)})`);
}

export function relatedChatTarget(message: Message, source: Conversation, conversations: Conversation[], bots: Bot[]): ViewOnlyChatTarget | null {
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

export const HandoverChips = lazy(() => import("../HandoverChips"));

export const DayOneTemplates = lazy(() => import("../DayOneTemplates"));

export function formatDateDivider(value: string, timeZone: string) {
  return `${formatDay(value, timeZone)} ${formatTime(value, timeZone)}`;
}

export function formatDay(value: string, timeZone: string) {
  return dateTimeFormat(intlLocale(), { month: "long", day: "numeric", weekday: "short", timeZone }).format(new Date(value));
}
