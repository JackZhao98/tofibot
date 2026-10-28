import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { api } from "./api";
import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";
import { formatDateTime } from "./i18n/format";
import { TofiIcon as Icon, type TofiIconName } from "./icons";
import { ModalDialog } from "./overlays/ModalDialog";
import type { Conversation, SearchMessageHit } from "./types";
import "./search-palette.css";

/**
 * The search palette (Cmd/Ctrl+K or the magnifier in the sidebar header). A lazy chunk: the entry carries only the button
 * and the key. It is a combobox: focus stays in the input, `aria-activedescendant` names the selected row, arrows move it,
 * Enter opens it, Escape closes. Chats are filtered here from the list already loaded (no request per key); messages come
 * from GET /api/search, debounced and cancelled when the query changes. All state lives here, so typing never renders the
 * workspace.
 */
type Tab = "all" | "chats" | "messages";
type Item =
  | { key: string; type: "action"; icon: TofiIconName; label: string; run: () => void }
  | { key: string; type: "chat"; conversation: Conversation }
  | { key: string; type: "message"; hit: SearchMessageHit };
type Section = { id: string; label: string; items: Item[] };

const DEBOUNCE_MS = 150;
const MESSAGE_LIMIT = 20;
const CHAT_LIMIT = 8;
const TABS: Tab[] = ["all", "chats", "messages"];

export type SearchPaletteProps = {
  conversations: Conversation[];
  onClose: () => void;
  onOpenConversation: (id: string) => void;
  onOpenMessage: (conversationId: string, messageId: string) => void;
  actions: { newBot: () => void; newGroup: () => void; settings: () => void };
};

/** Splits `text` around the hit; offsets are UTF-16 units, which is what String.slice counts. */
function Highlighted({ text, start, length }: { text: string; start: number; length: number }) {
  if (length <= 0 || start < 0 || start + length > text.length) return <>{text}</>;
  return <>{text.slice(0, start)}<mark>{text.slice(start, start + length)}</mark>{text.slice(start + length)}</>;
}

function sameQuery(name: string, query: string) {
  return name.toLocaleLowerCase().indexOf(query.toLocaleLowerCase());
}

export function SearchPalette({ conversations, onClose, onOpenConversation, onOpenMessage, actions }: SearchPaletteProps) {
  const { t } = useTranslation("chat");
  const [query, setQuery] = useState("");
  const [tab, setTab] = useState<Tab>("all");
  const [active, setActive] = useState(0);
  const [closing, setClosing] = useState(false);
  const [hits, setHits] = useState<{ query: string; messages: SearchMessageHit[] } | null>(null);
  const [status, setStatus] = useState<"idle" | "loading" | "error">("idle");
  const [attempt, setAttempt] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const listId = useId();
  const q = query.trim();

  const requestClose = useCallback(() => {
    if (closing) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) { onClose(); return; }
    setClosing(true);
    // animationend ends it; the timer covers a browser that never fires it.
    window.setTimeout(onClose, 400);
  }, [closing, onClose]);

  // Messages: one debounced request per settled query; a newer query aborts the older one.
  useEffect(() => {
    if (!q || tab === "chats") { setStatus("idle"); setHits(null); return; }
    const controller = new AbortController();
    setStatus("loading");
    const timer = window.setTimeout(() => {
      api.search(q, "messages", MESSAGE_LIMIT, controller.signal).then(result => {
        if (controller.signal.aborted) return;
        setHits({ query: q, messages: result.messages });
        setStatus("idle");
      }).catch(cause => {
        if (controller.signal.aborted || (cause instanceof DOMException && cause.name === "AbortError")) return;
        setStatus("error");
      });
    }, DEBOUNCE_MS);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [q, tab, attempt]);

  const recent = useMemo(() => [...conversations].filter(c => !c.archived).sort((a, b) => b.updated_at.localeCompare(a.updated_at)), [conversations]);

  const sections = useMemo<Section[]>(() => {
    const out: Section[] = [];
    const chatItem = (conversation: Conversation): Item => ({ key: `c:${conversation.id}`, type: "chat", conversation });
    if (!q) {
      if (tab === "all") out.push({ id: "actions", label: t("search.group_actions"), items: [
        { key: "a:bot", type: "action", icon: "bot-add", label: t("search.action_new_bot"), run: actions.newBot },
        { key: "a:group", type: "action", icon: "group-add", label: t("search.action_new_group"), run: actions.newGroup },
        { key: "a:settings", type: "action", icon: "settings", label: t("search.action_settings"), run: actions.settings },
      ] });
      if (tab !== "messages") out.push({ id: "recent", label: t("search.group_recent"), items: recent.slice(0, CHAT_LIMIT).map(chatItem) });
      return out.filter(section => section.items.length > 0);
    }
    if (tab !== "messages") {
      const chats = recent.filter(c => sameQuery(c.name, q) >= 0).slice(0, CHAT_LIMIT);
      if (chats.length) out.push({ id: "chats", label: t("search.group_chats"), items: chats.map(chatItem) });
    }
    if (tab !== "chats" && hits && hits.query === q && hits.messages.length) {
      out.push({ id: "messages", label: t("search.group_messages"), items: hits.messages.map(hit => ({ key: `m:${hit.id}`, type: "message" as const, hit })) });
    }
    return out;
  }, [q, tab, recent, hits, actions, t]);

  const flat = useMemo(() => sections.flatMap(section => section.items), [sections]);
  const current = Math.min(active, Math.max(flat.length - 1, 0));
  const optionId = (item: Item) => `${listId}-${item.key}`;

  // The selected row follows the arrows; a pointer move selects without scrolling.
  useEffect(() => {
    const item = flat[current];
    if (item) document.getElementById(optionId(item))?.scrollIntoView({ block: "nearest" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current, flat]);

  function choose(item: Item | undefined) {
    if (!item) return;
    if (item.type === "action") item.run();
    else if (item.type === "chat") onOpenConversation(item.conversation.id);
    else onOpenMessage(item.hit.conversation_id, item.hit.id);
    requestClose();
  }

  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if ((event.metaKey || event.ctrlKey) && !event.altKey && event.key.toLowerCase() === "k") { event.preventDefault(); requestClose(); return; }
    // An IME composition owns Enter and the arrows until it commits.
    if (event.nativeEvent.isComposing || event.keyCode === 229) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!flat.length) return;
      setActive((current + (event.key === "ArrowDown" ? 1 : -1) + flat.length) % flat.length);
    } else if (event.key === "Home" && event.target === input.current && !query) { event.preventDefault(); setActive(0); }
    else if (event.key === "Enter" && event.target === input.current) { event.preventDefault(); choose(flat[current]); }
  }

  const empty = Boolean(q) && !flat.length && status !== "loading" && status !== "error" && (tab === "chats" || hits?.query === q);
  const count = flat.length;
  const time = (value: string) => formatDateTime(value, { dateStyle: "medium" });
  let row = -1;

  return <ModalDialog onClose={requestClose} overlayClassName="search-palette-scrim" overlayProps={{ "data-state": closing ? "closing" : "open" }} initialFocus={input}>
    <div ref={panel} className="search-palette" role="dialog" aria-label={t("search.title")} data-state={closing ? "closing" : "open"} onKeyDown={onKeyDown}
      onAnimationEnd={event => { if (closing && event.target === event.currentTarget) onClose(); }}>
      <div className="search-palette-head">
        <Icon name="search" size={20} />
        <input ref={input} type="text" role="combobox" aria-expanded={flat.length > 0} aria-controls={listId} aria-autocomplete="list" aria-activedescendant={flat[current] ? optionId(flat[current]) : undefined}
          aria-label={t("search.title")} placeholder={t("search.placeholder")} value={query} autoComplete="off" autoCorrect="off" autoCapitalize="off" spellCheck={false} enterKeyHint="search"
          onChange={event => { setQuery(event.target.value); setActive(0); }} />
        <button type="button" className="search-palette-close" aria-label={t("search.hint_close")} onClick={requestClose}><Icon name="close" size={18} /></button>
      </div>
      <div className="search-palette-tabs" role="tablist" aria-label={t("search.tabs_label")}>
        {TABS.map(id => <button key={id} type="button" role="tab" id={`${listId}-tab-${id}`} aria-selected={tab === id} tabIndex={tab === id ? 0 : -1} onClick={() => { setTab(id); setActive(0); input.current?.focus(); }}
          onKeyDown={event => { const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0; if (!step) return; event.preventDefault(); event.stopPropagation(); const next = TABS[(TABS.indexOf(id) + step + TABS.length) % TABS.length]; setTab(next); setActive(0); requestAnimationFrame(() => document.getElementById(`${listId}-tab-${next}`)?.focus()); }}>{t(`search.tab_${id}`)}</button>)}
      </div>
      <div className="search-palette-list" id={listId} role="listbox" aria-label={t("search.title")} aria-busy={status === "loading"}>
        {sections.map(section => <div className="search-palette-group" key={section.id} role="group" aria-label={section.label}>
          <div className="search-palette-label" aria-hidden="true">{section.label}</div>
          {section.items.map(item => {
            row += 1;
            const index = row;
            const selected = index === current;
            const common = { id: optionId(item), role: "option" as const, "aria-selected": selected, "data-selected": selected ? "" : undefined, onMouseMove: () => { if (index !== current) setActive(index); }, onClick: () => choose(item) };
            if (item.type === "action") return <div key={item.key} className="search-palette-row is-action" {...common}><span className="search-palette-icon"><Icon name={item.icon} size={18} /></span><span className="search-palette-title">{item.label}</span></div>;
            if (item.type === "chat") {
              const c = item.conversation;
              const at = q ? sameQuery(c.name, q) : -1;
              return <div key={item.key} className="search-palette-row is-chat" {...common}>
                <span className="search-palette-icon">{c.kind === "dm" && c.bot_id ? <BotAvatar id={c.bot_id} mini still /> : <Icon name={c.kind === "group" ? "group" : "chat"} size={18} />}</span>
                <span className="search-palette-title">{at >= 0 ? <Highlighted text={c.name} start={at} length={q.length} /> : c.name}</span>
                <time className="search-palette-time" dateTime={c.updated_at}>{time(c.updated_at)}</time>
              </div>;
            }
            const h = item.hit;
            return <div key={item.key} className="search-palette-row is-message" {...common}>
              <span className="search-palette-icon"><Icon name="chat" size={18} /></span>
              <span className="search-palette-body">
                <span className="search-palette-snippet"><Highlighted text={h.snippet} start={h.match_start} length={h.match_len} /></span>
                <span className="search-palette-meta"><span className="search-palette-conversation">{h.conversation_name}</span><time dateTime={h.created_at}>{time(h.created_at)}</time></span>
              </span>
            </div>;
          })}
        </div>)}
        {status === "loading" && !flat.some(item => item.type === "message") && <div className="search-palette-note" role="status">{t("search.searching")}</div>}
        {status === "error" && <div className="search-palette-note is-error" role="alert">{t("search.error")} <button type="button" className="search-palette-retry" onClick={() => setAttempt(n => n + 1)}>{t("search.retry")}</button></div>}
        {empty && <div className="search-palette-note" role="status">{t("search.no_results", { query: q })}</div>}
        {!q && tab === "messages" && <div className="search-palette-note">{t("search.type_to_search")}</div>}
        {!q && tab === "chats" && !flat.length && <div className="search-palette-note">{t("search.no_recent")}</div>}
      </div>
      <div className="search-palette-foot" aria-hidden="true">
        <span><kbd>↑</kbd><kbd>↓</kbd>{t("search.hint_move")}</span>
        <span><kbd>↵</kbd>{t("search.hint_open")}</span>
        <span><kbd>esc</kbd>{t("search.hint_close")}</span>
      </div>
      <div className="sr-only" role="status" aria-live="polite">{q && status === "idle" ? t("search.results", { count }) : ""}</div>
    </div>
  </ModalDialog>;
}
