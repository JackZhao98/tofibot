import { MessageHoverActions } from "../MessageHoverActions";
import { readReplyMarker } from "../replyMarker";
import { i18n, useTranslation } from "../i18n";
import { isDesktop } from "../desktop";
import { useUserTimezone } from "../UserTimezone";
import { formatExactTime } from "../timezone";
import { AttachmentList } from "../MessageAttachment";
import { MessageReactions } from "../MessageReactions";
import { useMessageActionsOpen } from "../messageActionsStore";
import { DisplayCard } from "../DisplayCard";
import { TaskCard } from "../lazyPanels";
import { linkRuntime } from "../settings/deepLinks";
import { BotIdentityCard } from "../BotIdentityCard";
import { useHasComputer } from "../computerCapability";
import type { ViewOnlyChatTarget } from "../ViewOnlyChat";
import { ScheduledTaskRow } from "../ScheduledTaskRow";
import { type ScheduleOccurrence } from "../scheduleOccurrences";
import type { Schedule } from "../types";
import { TofiIcon as Icon } from "../icons";
import { lazy, memo, Suspense, useLayoutEffect, useMemo, useRef } from "react";
import { MessageMarkdown } from "../MessageMarkdown";
import { BotAvatar } from "../BotAvatar";
import { DeliveryNote, SendReceipt } from "../SendReceipt";
import { receiptFor } from "../sendReceipts";
import { DraftStream, useLiveDraft } from "../draftStream";
import { rowModelEqual } from "../timelineRow";
import type { TaskCardModel } from "../taskCards";
import { useSmoothStreamText } from "../useSmoothStreamText";
import type { Bot, Message, Run, StreamDraft, ToolActivity, ToolActivityRunSummary } from "../types";
import { MOTION, EASE } from "../motion";
import { Avatar, blockKey, jumpToMessage, messageId, previewText } from "./shared";
import { Notice } from "../ds/Notice/Notice";
import { Button } from "../ds/Button/Button";
import { Divider } from "../ds/Divider/Divider";
import { ToolActivityList, type ToolDetailState } from "./RunActivity";
import { MessageBubble as KitBubble, type BubbleTone } from "../ds/MessageBubble/MessageBubble";
import { useBotGround } from "../useBotGround";
import { getBotGround } from "../avatarStore";
import "./message-bubble.css";

// Shown only before the first Bot exists.
/** The work trace line under a run's final reply, for a run whose Activity list is not drawn; the same chunk as in Activity. */
const WorkTrace = lazy(() => import("../WorkTrace"));
const SettingsChangeNotice = lazy(() => import("../SettingsChangeNotice"));
/** Holds a message's action bar and mounts it only while that message is the active one. */
function MessageActionsSlot({ id, children }: { id: string; children: React.ReactNode }) {
  return useMessageActionsOpen(id) ? <>{children}</> : null;
}
/** Cross-conversation capsules are stored with English labels; show them in the UI language. */
export function localizeMessageRef(content: string) {
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
export function MessageBubble({ receipt, message, replyTarget, replyTargetName, sender, senderName, targetBot, run, tools, scheduleOccurrence, schedulePending, schedule, onRetrySchedule, onDraftReply, draft, relatedChat, onOpenRelatedChat, compact = false, showAvatar = true, showIdentity = true, noticeTargetAvailable = true, allowOpenDM = false, onNavigate, onMention }: { receipt?: React.ReactNode; relatedChat?: ViewOnlyChatTarget | null; onOpenRelatedChat?: (target: ViewOnlyChatTarget) => void; noticeTargetAvailable?: boolean; allowOpenDM?: boolean; onMention?: (bot: Bot) => void; message: Message; replyTarget?: Message; replyTargetName?: string; sender?: Bot; senderName?: string; targetBot?: Bot; run?: Run; tools?: React.ReactNode; scheduleOccurrence?: ScheduleOccurrence; schedulePending?: boolean; schedule?: Schedule; onRetrySchedule?: (id: string) => Promise<void>; onDraftReply?: () => Promise<boolean>; draft?: string | StreamDraft; compact?: boolean; showAvatar?: boolean; showIdentity?: boolean; onNavigate?: (id: string) => void }) {
  const hasComputer = useHasComputer();
  const { t } = useTranslation("chat");
  const ground = useBotGround(sender?.id ?? (message.sender_bot_id && message.sender_bot_id !== "system" ? message.sender_bot_id : undefined));
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
      {showAvatar && <div className="message-avatar tb-cat-only"><Avatar label={label} id={message.sender_bot_id ?? "bot"} mini /></div>}
      <div className="message-body">{showIdentity && (!compact || showAvatar) && <div className="message-meta"><strong>{label}</strong></div>}<DisplayCard card={message.card} bot={message.sender_bot_id ? { id: message.sender_bot_id, name: label } : undefined} onDraftReply={onDraftReply} />{tools && <div className="message-tools">{tools}</div>}</div>
    </article>;
  }
  if (message.kind === "scheduled_task") {
    return <ScheduledTaskRow message={message} run={run} occurrence={scheduleOccurrence} pending={schedulePending} schedule={schedule} timezone={timezone} onRetry={onRetrySchedule} />;
  }
  if (message.kind === "message_ref" && message.notice) {
    return <article className="message message-notice message-reference" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="link">{relatedChat && onOpenRelatedChat ? <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} /> : localizeMessageRef(message.content)}</Notice></article>;
  }
  if (message.kind === "notice" && message.notice?.type === "resume") {
    // Posted by the server when a run continues from a checkpoint; the text is localized here.
    const resumeKey = `message.resume_${message.notice.reason || "continuing"}` as "message.resume_task";
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="retry">{t(resumeKey, message.notice)}</Notice></article>;
  }
  if (message.kind === "notice" && message.notice?.type === "settings_change") {
    // A setting a Bot changed by itself: the line and its Undo are worded in a lazy chunk, in the UI's language.
    return <Suspense fallback={null}><SettingsChangeNotice notice={message.notice} fallback={message.content} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`} /></Suspense>;
  }
  if (message.kind === "notice" && message.notice?.type === "spend_alert") {
    // Posted once by the server when the month's spend reaches 80% of the limit; the text is localized here.
    const notice = message.notice;
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="alert" action={<Button variant="ghost" size="sm" onClick={() => void linkRuntime().then(module => module.openHref("/settings/usage#spend"))}>{t("message.spend_alert_open")}</Button>}>{t(notice.reason === "reached" ? "message.spend_alert_reached" : "message.spend_alert", { spend: notice.new_value, limit: notice.old_value, percent: Math.round(Number(notice.new_value) / Math.max(1, Number(notice.old_value)) * 100) })}</Notice></article>;
  }
  if (message.kind === "notice" && message.notice?.type === "command_exit") {
    // Posted by the server when a command a Bot waited on has ended; the text is localized here.
    const notice = message.notice;
    const label = notice.reason === "interrupted" ? t("message.command_exit_interrupted")
      : notice.reason === "closed" ? t("message.command_exit_closed")
      : notice.exit_code !== undefined ? t("message.command_exit", { code: notice.exit_code }) : t("message.command_exit_unknown");
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="terminal" action={hasComputer ? <Button variant="ghost" size="sm" onClick={() => window.dispatchEvent(new CustomEvent("tofi:open-terminal", { detail: { botId: notice.from_bot_id } }))}>{t("message.open_terminal")}</Button> : undefined}>{label}</Notice></article>;
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
    return <article className="message message-notice" title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="bot-handoff" action={relatedChat && onOpenRelatedChat ? <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} /> : undefined}>{noticeTargetAvailable ? <button className="tb-notice-link" onClick={() => onNavigate?.(target)}>{content}</button> : content}</Notice></article>;
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
    return <article className="message message-notice message-assignment" data-run-id={message.run_id} title={`${formatExactTime(message.created_at, timezone)} · ${timezone}`}><Notice icon="bot-handoff">{t("message.handed_over", { from: label, to: toName })}{body.trim() && <details className="assignment-brief"><summary>{t("message.view_brief")}</summary><MessageMarkdown content={body} /></details>}</Notice></article>;
  }
  const mention = assignment ? <span className="message-mention"><Avatar label={targetBot?.name ?? t("message.member")} id={message.notice!.to_bot_id} mini /><span>@{targetBot?.name ?? t("message.member")}</span></span> : undefined;
  const inlineMention = mention && targetBot ? <BotIdentityCard bot={targetBot} onMention={onMention ? () => onMention(targetBot) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(targetBot.dm_conversation_id) : undefined}>{mention}</BotIdentityCard> : mention;
  const toolRow = tools && <div className="message-tools">{tools}</div>;
  const grouped = showAvatar && !isUser;
  // A person's attachments sit above the text bubble, on their own (a photo is not a bubble); an image-only message has no bubble at all.
  const hasAttachments = Boolean(message.attachments?.length);
  const mediaOnly = hasAttachments && !body.trim();
  const avatar = showAvatar && !isUser ? <span className="sender-avatar sender-avatar--kit tb-cat-only">{sender ? <BotIdentityCard bot={sender} onMention={onMention ? () => onMention(sender) : undefined} onOpen={allowOpenDM && onNavigate ? () => onNavigate(sender.dm_conversation_id) : undefined}><Avatar label={label} id={sender.id} mini /></BotIdentityCard> : <Avatar label={label} id={message.sender_bot_id ?? "bot"} mini />}</span> : undefined;
  const userAttachments = isUser && hasAttachments && !mediaOnly ? <div className="message-attachments"><AttachmentList attachments={message.attachments} /></div> : null;
  const sending = message.local?.state === "sending" || message.local?.state === "retrying";
  // The quote takes the quoted sender's colour: blue for you, the Bot's coat for a Bot.
  const quoteName = replyTargetName ?? t("message.original");
  const quoteTone: BubbleTone | undefined = replyTarget ? (replyTarget.role === "user" ? "you" : replyTarget.sender_bot_id && replyTarget.sender_bot_id !== "system" ? getBotGround(replyTarget.sender_bot_id) : undefined) : undefined;
  const quote = replyTarget ? { author: t("message.reply_to", { name: quoteName }), text: previewText(readReplyMarker(replyTarget.content).body) || t("message.attachment"), tone: quoteTone, jumpLabel: t("message.jump_to_reply", { name: quoteName }), onJump: () => jumpToMessage(document.querySelector<HTMLElement>(`[data-message-id="${replyTarget.id}"]`)) } : undefined;
  const author = showIdentity && !isUser && (!compact || showAvatar) ? label : undefined;
  const botAttachments = !isUser && hasAttachments ? <div className="message-attachments bot-attachments"><AttachmentList attachments={message.attachments} /></div> : null;
  const bubble = mediaOnly ? undefined : <div className="message-content" ref={answerRef}><SmoothStreamMarkdown content={body} mention={inlineMention} active={draft !== undefined && !isDesktop} streaming={draft !== undefined} />{draft !== undefined && isDesktop && <span className="typing-cursor tb-caret" />}</div>;
  return <article data-sender={grouped ? message.sender_bot_id : undefined} data-from={isUser ? "user" : message.sender_bot_id || "bot"} data-at={message.created_at} className={`message ${isUser ? "message-user" : "message-bot"}${sending ? " is-sending" : ""}${mediaOnly ? " is-media-only" : ""}${grouped ? " is-group" : !isUser ? " is-dm" : ""} ${message.kind === "segment" ? "message-segment" : ""} ${message.kind === "forward_result" ? "message-forward-result" : ""} ${compact ? "message-compact" : ""}`} title={isDesktop ? `${formatExactTime(message.created_at, timezone)} · ${timezone}` : undefined}><div className={`message-body${tools && !isDesktop ? ` web-tool-body ${toolsRunning ? "is-tools-live" : "is-tools-done"}` : ""}`}>{scheduled && <div className={`message-meta ${hideMeta ? "compact" : ""}`}><span>{t("message.scheduled")}</span></div>}{(toolsRunning || !isDesktop) && toolRow}{userAttachments}<KitBubble role={isUser ? "user" : "bot"} botColor={!isUser && message.sender_bot_id && message.sender_bot_id !== "system" ? ground ?? "tomato" : undefined} author={author} avatar={avatar} avatarColumn={grouped} hangAvatar={grouped} quote={quote} sending={sending} attachments={mediaOnly ? (isUser ? <div className="message-attachments"><AttachmentList attachments={message.attachments} /></div> : botAttachments) : undefined}>{bubble}</KitBubble>{!isUser && hasAttachments && !mediaOnly && botAttachments}{receipt}{!toolsRunning && isDesktop && toolRow}{relatedChat && onOpenRelatedChat && <RelatedMessageLink target={relatedChat} onOpen={onOpenRelatedChat} />}</div></article>;
}
/** Everything one timeline row draws, derived by the workspace. Plain data only: the row is skipped when none of it changed. */
export interface RowModel {
  message: Message; isLast: boolean; arrival: boolean; streamed: boolean; dividerLabel?: string; unread: boolean; timezone: string;
  canReact: boolean; isGroup: boolean; archived: boolean;
  /** The draft whose live text this row follows, and whether the bubble shows it as still being written. */
  liveId?: string; isStub: boolean; drafting: boolean;
  replyTarget?: Message; replyTargetName?: string; senderName?: string; targetBot?: Bot; sender?: Bot; run?: Run; /** Set when this is the final reply of a run that kept a work trace and its Activity list is not drawn. */ traceRun?: Run;
  toolLists?: { details: Record<string, ToolDetailState>; botById: Map<string, Bot>; activeRunIds: Set<string> };
  processTools?: ToolActivity[]; processProgress?: Message[]; processSummaries?: ToolActivityRunSummary[]; processSummaryErrors?: Record<string, string>; processSummaryLoading?: Record<string, boolean>;
  afterTools?: ToolActivity[]; afterSummaries?: ToolActivityRunSummary[]; afterSummaryErrors?: Record<string, string>; afterSummaryLoading?: Record<string, boolean>;
  scheduleOccurrence?: ScheduleOccurrence; schedulePending: boolean; schedule?: Schedule; canRetrySchedule: boolean;
  relatedChat: ViewOnlyChatTarget | null; compact: boolean; showIdentity: boolean; noticeTargetAvailable: boolean; allowOpenDM: boolean; canDraftReply: boolean; canMention: boolean;
  receipt?: NonNullable<ReturnType<typeof receiptFor>>; receiptNames?: string[]; canRetryReceipt: boolean;
  hasBar: boolean; copyText?: string; activityRunId?: string; activityToggle: boolean; activityOpen: boolean; canReply: boolean;
  botNames: Map<string, string>; menuPosition: { messageId: string; x: number; y: number; h?: number } | null;
  ownerNodes?: React.ReactNode[];
  /** A task block that draws something follows this message directly: its action bar floats instead of reserving a row, so the block sits at the normal item gap. */
  ownsNext?: boolean;
  /** Task cards under this message: plain models whose identity holds until the task changes. */
  taskCards?: TaskCardModel[];
}
/** Stable entry points a row calls; each forwards to the workspace's newest closure, so they never change identity. */
export interface RowHandlers {
  openMenu: (messageId: string, x: number, y: number) => void;
  closeMenu: () => void;
  hostPointerDown: () => void;
  hostClick: (event: React.MouseEvent<HTMLElement>, messageId: string) => void;
  react: (messageId: string, anchor: DOMRect) => void;
  toggleActivity: (runId: string) => void;
  reply: (message: Message) => void;
  retrySend: (clientMessageId: string) => void;
  editUnsent: (clientMessageId: string) => void;
  deleteUnsent: (clientMessageId: string) => void;
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
export const MessageRow = memo(function MessageRow({ model: m, h, stream }: { model: RowModel; h: RowHandlers; stream: DraftStream }) {
  const { t } = useTranslation("chat");
  const live = useLiveDraft(stream, m.liveId);
  const message = useMemo(() => m.isStub && live !== undefined ? { ...m.message, content: live } : m.message, [m.message, m.isStub, live]);
  const draft = m.drafting ? live ?? "" : undefined;
  const tools = m.processTools ? <ToolActivityList activities={m.processTools} progress={m.processProgress} summaries={m.processSummaries} summaryErrors={m.processSummaryErrors} summaryLoading={m.processSummaryLoading} details={m.toolLists!.details} onLoadDetails={h.loadDetails} onRetrySummary={h.retrySummary} botById={m.toolLists!.botById} activeRunIds={m.toolLists!.activeRunIds} /> : undefined;
  const receipt = m.receipt && (m.receipt.state === "failed" || m.receipt.state === "sending" || m.receipt.state === "retrying") ? <SendReceipt receipt={m.receipt} group={m.isGroup} names={m.receiptNames ?? []} onRetry={m.canRetryReceipt ? () => h.retrySend(message.client_message_id!) : undefined} onEdit={() => h.editUnsent(message.client_message_id!)} onDelete={() => h.deleteUnsent(message.client_message_id!)} /> : message.delivery ? <DeliveryNote state={message.delivery} /> : undefined;
  const mark = m.receipt && (m.receipt.state === "delivered" || m.receipt.state === "read") ? { state: m.receipt.state, names: m.receiptNames ?? [], group: m.isGroup } : undefined;
  const actions = m.hasBar ? <MessageActionsSlot id={message.id}><MessageHoverActions message={message} timezone={m.timezone} copyText={m.copyText} mark={mark}
      onReact={anchor => h.react(message.id, anchor)}
      onActivity={m.activityToggle && m.activityRunId ? () => h.toggleActivity(m.activityRunId!) : undefined} activityOpen={m.activityOpen}
      onReply={m.canReply ? () => h.reply(message) : undefined} /></MessageActionsSlot> : undefined;
  return <div className={`message-block${m.arrival ? ` new-arrival${m.streamed ? " was-streamed" : ""}` : ""}`} data-message-seq={message.seq} data-message-id={message.id} data-owns-next={m.ownsNext ? "" : undefined} data-flip={blockKey(message)} tabIndex={m.isLast ? 0 : -1} onContextMenu={m.canReact && isDesktop && message.role !== "user" ? event => { event.preventDefault(); h.openMenu(message.id, event.clientX, event.clientY); } : undefined}>
    {m.dividerLabel !== undefined && <Divider className="date-divider">{m.dividerLabel}</Divider>}
    {m.unread && <Divider tone="unread" className="unread-divider" label={t("history.unread_below")}>{t("history.unread")}</Divider>}
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
    {m.traceRun && <div className="message-work-trace"><Suspense fallback={null}><WorkTrace run={m.traceRun} /></Suspense></div>}
    {m.ownerNodes}
    {m.taskCards && <Suspense fallback={null}>{m.taskCards.map(card => <TaskCard key={card.id} card={card} />)}</Suspense>}
    {m.afterTools ? <ToolActivityList activities={m.afterTools} summaries={m.afterSummaries} summaryErrors={m.afterSummaryErrors} summaryLoading={m.afterSummaryLoading} details={m.toolLists!.details} onLoadDetails={h.loadDetails} onRetrySummary={h.retrySummary} botById={m.toolLists!.botById} activeRunIds={m.toolLists!.activeRunIds} group={m.isGroup} /> : null}
  </div>;
}, (previous, next) => previous.h === next.h && previous.stream === next.stream && rowModelEqual(previous.model, next.model));
