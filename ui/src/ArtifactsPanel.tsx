import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { CATS, EmptyState } from "./EmptyCat";
import { DelayedFeedback } from "./DelayedFeedback";
import { FileTile } from "./FileViewer";
import { attachmentUrl, fileKind, fileSize, openFilePreview } from "./filePreviewModel";
import { formatRelativeTime } from "./i18n/format";
import { useTranslation } from "./i18n";
import { errorText } from "./i18n/errors";
import { TofiIcon } from "./icons";
import type { Bot, Conversation } from "./types";
import "./artifacts-panel.css";

/** One file a Bot produced (or, with the toggle, one the person uploaded); the shape of GET /api/artifacts. */
export type ArtifactItem = {
  id: string; name: string; mime: string; size: number; created_at: string;
  conversation_id: string; conversation_name: string; conversation_kind: "dm" | "group"; conversation_bot_id?: string; conversation_archived: boolean;
  bot_id?: string; message_id: string; message_seq: number; uploaded: boolean; version_group: string; version_count: number;
};
type Page = { items: ArtifactItem[]; next_cursor: string };

type ArtifactScope = { kind: "all" } | { kind: "bot"; id: string; name: string } | { kind: "conversation"; id: string; name: string };
type Kind = "all" | "image" | "document";

const fetchArtifacts = (params: Record<string, string>, signal?: AbortSignal) =>
  request<Page>(`/api/artifacts?${new URLSearchParams(params)}`, { signal });

const isRaster = (item: ArtifactItem) => item.mime.startsWith("image/") && item.mime !== "image/svg+xml";
const openItem = (item: ArtifactItem) => openFilePreview({ id: item.id, name: item.name, mime: item.mime, size: item.size, conversationId: item.conversation_id });

type Props = {
  /** "artifacts" is the whole account; "-bot" and "-group" read the active Bot / group from the workspace. */
  mode: "artifacts" | "artifacts-bot" | "artifacts-group";
  conversation?: Conversation;
  bot?: Bot;
  onClose: () => void;
  /** Leave the panel and scroll to the message that carries the file. */
  onOpenMessage: (conversationId: string, messageId: string) => void;
  /** The cover's scope control: the views this panel can switch between, and the switch. */
  scopes?: Array<{ mode: Props["mode"]; label: string }>;
  onScope?: (mode: Props["mode"]) => void;
};

/** Arrow keys, Home and End move between the tabs of a tablist and select the one they land on (the buttons are a roving tab stop). */
function tabsKeys(event: React.KeyboardEvent<HTMLElement>) {
  const keys: Record<string, number> = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 };
  const tabs = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
  const at = tabs.indexOf(document.activeElement as HTMLButtonElement);
  if (at < 0) return;
  const to = event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : keys[event.key] === undefined ? -1 : (at + keys[event.key] + tabs.length) % tabs.length;
  if (to < 0) return;
  event.preventDefault();
  tabs[to].focus(); tabs[to].click();
}

/** Everything the Bots produced, at three levels: the whole account, one Bot, one conversation. Read-only. */
export function ArtifactsPanel({ mode, conversation, bot, onClose, onOpenMessage, scopes, onScope }: Props) {
  const { t } = useTranslation("bots");
  const scope: ArtifactScope = mode === "artifacts-group" && conversation ? { kind: "conversation", id: conversation.id, name: conversation.name }
    : mode === "artifacts-bot" && bot ? { kind: "bot", id: bot.id, name: bot.name } : { kind: "all" };
  const scopeKey = scope.kind === "all" ? "all" : `${scope.kind}:${scope.id}`;
  const [kind, setKind] = useState<Kind>("all");
  const [uploads, setUploads] = useState(false);
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<ArtifactItem[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => { const timer = window.setTimeout(() => setQuery(search.trim()), 250); return () => window.clearTimeout(timer); }, [search]);

  const params = useCallback((cursor?: string) => {
    const value: Record<string, string> = { scope: scope.kind, include_uploads: uploads ? "1" : "0" };
    if (scope.kind !== "all") value.id = scope.id;
    if (kind !== "all") value.type = kind;
    if (query) value.q = query;
    if (cursor) value.cursor = cursor;
    return value;
    // `scopeKey` stands for the scope object, which callers recreate on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey, uploads, kind, query]);

  useEffect(() => {
    const abort = new AbortController();
    setLoading(true); setError("");
    fetchArtifacts(params(), abort.signal).then(page => { setItems(page.items ?? []); setNext(page.next_cursor ?? ""); setLoading(false); }, cause => {
      if (abort.signal.aborted) return;
      setError(errorText(cause, "bots", t("artifacts.error"))); setLoading(false);
    });
    return () => abort.abort();
  }, [params, attempt, t]);

  async function loadMore() {
    if (!next || more) return;
    setMore(true); setError("");
    try {
      const page = await fetchArtifacts(params(next));
      setItems(current => [...current, ...page.items.filter(item => !current.some(known => known.id === item.id))]);
      setNext(page.next_cursor);
    } catch (cause) { setError(errorText(cause, "bots", t("artifacts.error"))); }
    finally { setMore(false); }
  }

  const title = scope.kind === "all" ? t("artifacts.title_all") : scope.kind === "bot" ? t("artifacts.title_bot", { name: scope.name }) : t("artifacts.title_conversation", { name: scope.name });
  const kinds: Array<[Kind, string]> = [["all", t("artifacts.type_all")], ["image", t("artifacts.type_images")], ["document", t("artifacts.type_documents")]];
  const filtered = kind !== "all" || query !== "";

  return <div className="detail-content artifacts-panel">
    <div className="detail-heading"><div><h2>{title}</h2></div><button type="button" className="close-button" aria-label={t("artifacts.close")} onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    <div className="artifacts-toolbar">
      {scopes && onScope && <div className="artifacts-tabs artifacts-scopes" role="tablist" aria-label={t("artifacts.scope_label")} onKeyDown={tabsKeys}>{scopes.map(item => <button key={item.mode} type="button" role="tab" tabIndex={mode === item.mode ? 0 : -1} aria-selected={mode === item.mode} onClick={() => onScope(item.mode)}>{item.label}</button>)}</div>}
      <div className="artifacts-tabs artifacts-kinds" role="tablist" aria-label={t("artifacts.tabs_label")} onKeyDown={tabsKeys}>{kinds.map(([value, label]) => <button key={value} type="button" role="tab" tabIndex={kind === value ? 0 : -1} aria-selected={kind === value} onClick={() => setKind(value)}>{label}</button>)}</div>
      <label className="artifacts-search"><TofiIcon name="search" size={16} /><input type="search" aria-label={t("artifacts.search_label")} placeholder={t("artifacts.search_placeholder")} value={search} onChange={event => setSearch(event.target.value)} /></label>
      <label className="debug-toggle artifacts-uploads"><span>{t("artifacts.include_uploads")}</span><input type="checkbox" role="switch" checked={uploads} onChange={event => setUploads(event.target.checked)} /></label>
    </div>
    {error && <p className="error-text" role="alert">{error} <button type="button" className="text-button" onClick={() => items.length ? void loadMore() : setAttempt(value => value + 1)}>{t("artifacts.retry")}</button></p>}
    {loading && !items.length ? <DelayedFeedback><div className="inline-state" role="status"><div className="spinner" />{t("artifacts.loading")}</div></DelayedFeedback>
      : !items.length ? !error && <EmptyState name="artifacts-empty" className="panel-empty is-card" look={CATS.kiwi} pose="asleep"><p>{filtered ? t("artifacts.empty_filtered") : t("artifacts.empty")}</p></EmptyState>
      : <ul className="artifacts-grid" aria-label={t("artifacts.list_label")} aria-busy={loading}>{items.map(item => <ArtifactCard key={item.id} item={item} showBot={scope.kind !== "bot"} showConversation={scope.kind !== "conversation"} onOpenMessage={onOpenMessage} />)}</ul>}
    {next && <button type="button" className="secondary-button artifacts-more" disabled={more} onClick={() => void loadMore()}>{more ? t("artifacts.loading") : t("artifacts.load_more")}</button>}
  </div>;
}

function ArtifactCard({ item, showBot, showConversation, onOpenMessage }: { item: ArtifactItem; showBot: boolean; showConversation: boolean; onOpenMessage: Props["onOpenMessage"] }) {
  const { t } = useTranslation("bots");
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<ArtifactItem[] | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const kind = fileKind(item.name, item.mime);

  async function toggleVersions() {
    if (open) { setOpen(false); return; }
    setOpen(true);
    if (versions || state === "loading") return;
    setState("loading");
    try { setVersions((await fetchArtifacts({ scope: "versions", id: item.id })).items.filter(version => version.id !== item.id)); setState("idle"); }
    catch { setState("error"); }
  }

  return <li className="artifact-card">
    <button type="button" className="artifact-open" aria-label={t("artifacts.open", { name: item.name })} onClick={() => openItem(item)}>
      <span className="artifact-thumb">{isRaster(item) ? <img src={attachmentUrl(item.id)} alt="" loading="lazy" decoding="async" /> : <FileTile name={item.name} kind={kind} />}{item.version_count > 1 && <span className="artifact-badge" aria-hidden="true">v{item.version_count}</span>}</span>
      <span className="artifact-copy"><b className="artifact-name">{item.name}</b><small>{fileSize(item.size)}{item.uploaded ? ` · ${t("artifacts.uploaded")}` : ""}</small></span>
    </button>
    <div className="artifact-meta">
      {showBot && (item.bot_id ? <BotAvatar id={item.bot_id} mini /> : <span className="artifact-you" title={t("artifacts.you")}><TofiIcon name="user" size={14} /></span>)}
      <span className="artifact-source">{showConversation && <span className="artifact-conversation">{item.conversation_name}{item.conversation_archived && <em>{t("artifacts.archived")}</em>}</span>}<time dateTime={item.created_at}>{formatRelativeTime(item.created_at)}</time></span>
      <button type="button" className="ghost-button artifact-goto" aria-label={t("artifacts.go_aria", { name: item.name })} data-hint={t("artifacts.go_to_message")} onClick={() => onOpenMessage(item.conversation_id, item.message_id)}><TofiIcon name="arrow-right" size={16} /></button>
    </div>
    {item.version_count > 1 && <>
      <button type="button" className="text-button artifact-versions-toggle" aria-expanded={open} onClick={() => void toggleVersions()}>{open ? t("artifacts.hide_versions") : t("artifacts.earlier_versions", { count: item.version_count - 1 })}</button>
      {open && <ul className="artifact-versions">
        {state === "loading" && <li className="artifact-versions-note" role="status">{t("artifacts.versions_loading")}</li>}
        {state === "error" && <li className="artifact-versions-note" role="alert">{t("artifacts.versions_error")}</li>}
        {versions?.map(version => <li key={version.id}><button type="button" onClick={() => openItem(version)} aria-label={t("artifacts.open", { name: version.name })}><span>{formatRelativeTime(version.created_at)}</span><small>{fileSize(version.size)}</small></button><button type="button" className="ghost-button" aria-label={t("artifacts.go_aria", { name: version.name })} onClick={() => onOpenMessage(version.conversation_id, version.message_id)}><TofiIcon name="arrow-right" size={14} /></button></li>)}
      </ul>}
    </>}
  </li>;
}

/**
 * The Artifacts widget (docs/design/widgets-and-sidebar.md section 4): this conversation's latest files in one column,
 * a 36px thumbnail, the name and the time. "All" opens the full view as a cover. The same GET /api/artifacts, limit 4.
 * The list stays on screen while it refreshes, so a new file adds a row instead of flashing the widget empty.
 */
export function ArtifactsWidget({ conversationId, refreshToken, onAll }: { conversationId: string; refreshToken?: string; onAll: () => void }) {
  const { t } = useTranslation("bots");
  const [items, setItems] = useState<ArtifactItem[] | null>(null);
  const [failed, setFailed] = useState(false);
  // A file that arrives after the list was first drawn is flagged for a moment (--m-quick fade) and read out once.
  const known = useRef<Set<string> | null>(null);
  const [fresh, setFresh] = useState<ReadonlySet<string>>(new Set());
  const [announce, setAnnounce] = useState("");
  useEffect(() => { known.current = null; setItems(null); setFresh(new Set()); }, [conversationId]);
  useEffect(() => {
    const abort = new AbortController();
    fetchArtifacts({ scope: "conversation", id: conversationId, include_uploads: "0", limit: "4" }, abort.signal).then(page => {
      const next = page.items ?? [];
      const before = known.current;
      known.current = new Set([...(before ?? []), ...next.map(item => item.id)]);
      const arrived = before ? next.filter(item => !before.has(item.id)) : [];
      setItems(next); setFailed(false);
      if (arrived.length) { setFresh(new Set(arrived.map(item => item.id))); setAnnounce(t("artifacts.widget_new", { name: arrived[0].name })); }
    }, () => { if (!abort.signal.aborted) setFailed(true); });
    return () => abort.abort();
  }, [conversationId, refreshToken, t]);
  useEffect(() => {
    if (!fresh.size) return;
    const timer = window.setTimeout(() => setFresh(new Set()), 1800);
    return () => window.clearTimeout(timer);
  }, [fresh]);
  return <div className="artifacts-widget">
    {items === null ? (failed ? <p className="artifacts-widget-note" role="alert">{t("artifacts.error")}</p> : <div className="artifacts-widget-skeleton" aria-busy="true" aria-label={t("artifacts.loading")}><i /><i /></div>)
      : !items.length ? <p className="artifacts-widget-note">{t("artifacts.widget_empty")}</p>
      : <ul className="artifacts-widget-list" aria-label={t("artifacts.list_label")}>{items.map(item => <li key={item.id} data-fresh={fresh.has(item.id) || undefined}>
        <button type="button" className="artifact-row" aria-label={t("artifacts.open", { name: item.name })} onClick={() => openItem(item)}>
          <span className="artifact-row-thumb">{isRaster(item) ? <img src={attachmentUrl(item.id)} alt="" loading="lazy" decoding="async" /> : <FileTile name={item.name} kind={fileKind(item.name, item.mime)} />}</span>
          <span className="artifact-row-name">{item.name}</span>
          <time dateTime={item.created_at}>{formatRelativeTime(item.created_at)}</time>
        </button>
      </li>)}</ul>}
    <span className="sr-only" role="status">{announce}</span>
    <button type="button" className="artifacts-widget-all" onClick={onAll}>{t("artifacts.widget_all")}<TofiIcon name="arrow-right" size={14} /></button>
  </div>;
}
