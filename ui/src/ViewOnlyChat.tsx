import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type RefObject } from "react";
import { createPortal } from "react-dom";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { MessageMarkdown } from "./MessageMarkdown";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import type { Bot, Conversation, Message } from "./types";
import { mergeViewOnlyHistory, type ViewOnlyHistory, type ViewOnlyHistoryPage } from "./viewOnlyHistory";

export type ViewOnlyChatTarget = {
  source: Conversation;
  target: Conversation;
  sourceBotIds?: string[];
  targetBotIds?: string[];
  sourceName?: string;
  targetName?: string;
  runId?: string;
  label?: "Messaged" | "Message from";
};

function participantIds(conversation: Conversation, override?: string[]) {
  if (override?.length) return override;
  return conversation.kind === "group" ? conversation.bot_ids : [conversation.bot_id ?? conversation.id];
}

function AvatarStack({ ids }: { ids: string[] }) {
  const visible = ids.slice(0, 3);
  return <div className="view-only-chat-avatars">{visible.map((id) => <BotAvatar key={id} id={id} mini />)}{ids.length > visible.length && <em>+{ids.length - visible.length}</em>}</div>;
}

function Participant({ ids, name }: { ids: string[]; name: string }) {
  return <div className="view-only-chat-participant"><AvatarStack ids={ids} /><strong>{name}</strong></div>;
}

export function ViewOnlyChat({ target, bots, onClose, returnFocus, card = false }: { target: ViewOnlyChatTarget; bots: Bot[]; onClose: () => void; returnFocus?: HTMLElement | null; card?: boolean }) {
  const { t } = useTranslation("chat");
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const botById = useMemo(() => new Map(bots.map((bot) => [bot.id, bot])), [bots]);

  useLayoutEffect(() => {
    const trigger = returnFocus ?? (document.activeElement instanceof HTMLElement ? document.activeElement : null);
    const dialog = dialogRef.current;
    closeRef.current?.focus();
    // Restore after the caller removes inert. StrictMode's simulated cleanup
    // must not move focus out of a dialog that is still mounted.
    return () => { queueMicrotask(() => { if (!dialog?.isConnected && trigger?.isConnected) trigger.focus(); }); };
  }, [returnFocus]);

  useLayoutEffect(() => {
    // Resizing a usable card into a modal also makes the workspace inert.
    // Move any background focus into the newly modal surface.
    if (!card && !dialogRef.current?.contains(document.activeElement)) closeRef.current?.focus();
  }, [card]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return;
      if (event.key === "Escape") { event.preventDefault(); onClose(); return; }
      // As a top-right card the chat stays usable beside it, so focus is not trapped.
      if (event.key !== "Tab" || card) return;
      const controls = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], summary, [tabindex="0"]') ?? [])
        .filter(element => !element.closest('[hidden], [inert]') && element.getClientRects().length > 0);
      const index = controls.indexOf(document.activeElement as HTMLElement);
      if ((index < 0 && !dialogRef.current?.contains(document.activeElement)) || (event.shiftKey ? index === 0 : index === controls.length - 1)) {
        event.preventDefault();
        (event.shiftKey ? controls.at(-1) : controls[0])?.focus();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose, card]);

  const sourceIDs = participantIds(target.source, target.sourceBotIds);
  const targetIDs = participantIds(target.target, target.targetBotIds);

  return createPortal(<div ref={dialogRef} className={`view-only-chat${card ? " is-card" : ""}`} role="dialog" aria-modal={card ? undefined : true} aria-label={t("viewOnly.label")}>
    <header className="view-only-chat-header">
      <div className="view-only-chat-identity">
        <Participant ids={sourceIDs} name={target.sourceName ?? target.source.name} />
        <span className="view-only-chat-arrow" aria-hidden="true"><TofiIcon name="bot-handoff" size={24} style={{ verticalAlign: "middle" }} /></span>
        <Participant ids={targetIDs} name={target.targetName ?? target.target.name} />
      </div>
      <button ref={closeRef} type="button" className="view-only-chat-close" aria-label={t("viewOnly.close_label")} data-hint={t("viewOnly.close_label")} onClick={onClose}><TofiIcon name="close" size={20} /></button>
    </header>
    <ViewOnlyChatHistory key={JSON.stringify([target.target.id, target.runId ?? null])} conversationId={target.target.id} runId={target.runId} botById={botById} closeRef={closeRef} />
    <footer className="view-only-chat-footer"><span><TofiIcon name="lock" size={16} /> {t("viewOnly.read_only")}</span><button type="button" className="secondary-button" onClick={onClose}>{t("viewOnly.close")}</button></footer>
  </div>, document.body);
}

function ViewOnlyChatHistory({ conversationId, runId, botById, closeRef }: { conversationId: string; runId?: string; botById: Map<string, Bot>; closeRef: RefObject<HTMLButtonElement | null> }) {
  const { t } = useTranslation(["chat", "common"]);
  const [history, setHistory] = useState<ViewOnlyHistory | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [announcement, setAnnouncement] = useState("");
  const latest = useRef<ViewOnlyHistory | null>(null);
  const requestRef = useRef<AbortController | null>(null);
  const bodyRef = useRef<HTMLElement>(null);
  const loadRef = useRef<HTMLButtonElement>(null);
  const startRef = useRef<HTMLParagraphElement>(null);
  const listId = useId();
  const prepend = useRef<{ anchor?: HTMLElement; top?: number; focusId?: string; focusStart?: boolean } | null>(null);

  async function loadPage() {
    if (requestRef.current && !requestRef.current.signal.aborted) return;
    const previous = latest.current;
    if (previous && !previous.hasMore) return;
    const controller = new AbortController();
    requestRef.current = controller;
    if (!previous && document.activeElement === loadRef.current) closeRef.current?.focus();
    setLoading(true); setError(""); setAnnouncement("");
    try {
      const query = new URLSearchParams({ limit: "50" });
      if (previous?.beforeSeq !== undefined) query.set("before_seq", String(previous.beforeSeq));
      // Same read-only endpoint as api.messages, with cancellation scoped to this view.
      const page = await request<ViewOnlyHistoryPage>(`/api/conversations/${encodeURIComponent(conversationId)}/messages?${query}`, { signal: controller.signal });
      if (controller.signal.aborted) return;
      const next = mergeViewOnlyHistory(previous ?? { messages: [], hasMore: false }, page, conversationId, runId);
      if (previous) {
        const known = new Set(previous.messages.map(message => message.id));
        const added = next.messages.filter(message => !known.has(message.id));
        const body = bodyRef.current;
        const anchor = Array.from(body?.querySelectorAll<HTMLElement>('[data-message-id]') ?? []).find(element => element.getBoundingClientRect().bottom > (body?.getBoundingClientRect().top ?? 0));
        const focused = document.activeElement === loadRef.current;
        prepend.current = { anchor, top: anchor?.getBoundingClientRect().top, focusId: focused ? added[0]?.id : undefined, focusStart: focused && !added.length && !next.hasMore };
        setAnnouncement(added.length ? t("viewOnly.loaded_older", { count: added.length }) : next.hasMore ? t("viewOnly.no_match_older") : t("viewOnly.at_start"));
      }
      latest.current = next;
      setHistory(next);
    } catch {
      if (!controller.signal.aborted) setError(previous ? t("viewOnly.older_failed") : t("viewOnly.load_failed"));
    } finally {
      if (!controller.signal.aborted) setLoading(false);
      if (requestRef.current === controller) requestRef.current = null;
    }
  }

  // This child is keyed by conversation and run: no old content can flash on switch.
  useEffect(() => {
    closeRef.current?.focus();
    void loadPage();
    return () => { requestRef.current?.abort(); prepend.current = null; };
  }, []);

  useLayoutEffect(() => {
    const pending = prepend.current;
    const body = bodyRef.current;
    prepend.current = null;
    if (!pending || !body) return;
    if (pending.anchor?.isConnected && pending.top !== undefined) body.scrollTop += pending.anchor.getBoundingClientRect().top - pending.top;
    const firstNew = Array.from(body.querySelectorAll<HTMLElement>('[data-message-id]')).find(element => element.dataset.messageId === pending.focusId);
    // Only move focus if the reader was still using the pagination control.
    if (firstNew) {
      firstNew.focus({ preventScroll: true });
      body.scrollTop += firstNew.getBoundingClientRect().top - body.getBoundingClientRect().top - 12;
    } else if (pending.focusStart) startRef.current?.focus({ preventScroll: true });
  }, [history]);

  return <main ref={bodyRef} className="view-only-chat-body" aria-busy={loading}>
    {!history && loading && <div className="inline-state" role="status"><div className="spinner" />{t("viewOnly.loading")}</div>}
    {error && <div className="history-error" role="alert">{error}</div>}
    {(history?.hasMore || error) && <button ref={loadRef} type="button" className="load-older" style={{ minHeight: 44 }} aria-controls={listId} aria-disabled={loading} onClick={() => void loadPage()}>{loading ? t("viewOnly.loading_older") : error ? history ? t("viewOnly.retry_older") : t("common:action.retry") : t("history.load_older")}</button>}
    {history && !history.hasMore && <p ref={startRef} tabIndex={-1} className="muted" style={{ textAlign: "center", fontSize: 12 }}>{t("viewOnly.at_start")}</p>}
    <span className="sr-only" role="status" aria-live="polite">{announcement}</span>
    {history && !history.messages.length && <div className="conversation-empty"><h2>{history.hasMore ? t("viewOnly.no_match") : t("viewOnly.empty")}</h2></div>}
    <div id={listId} className="view-only-message-list">{history?.messages.map((message: Message) => {
      const bot = message.sender_bot_id ? botById.get(message.sender_bot_id) : undefined;
      const label = bot?.name ?? message.sender_bot_name ?? (message.role === "user" ? t("message.you") : "Bot");
      const user = message.role === "user" && !message.sender_bot_id;
      return <article className={`view-only-message ${user ? "is-user" : "is-bot"}`} key={message.id} data-message-id={message.id} tabIndex={-1} data-focused={(runId !== undefined && message.run_id === runId) || undefined}>
        {!user && <BotAvatar id={message.sender_bot_id ?? "bot"} mini />}
        <div className="view-only-message-copy"><small>{label}</small><div className="view-only-message-bubble"><MessageMarkdown content={message.content} /></div></div>
      </article>;
    })}</div>
  </main>;
}
