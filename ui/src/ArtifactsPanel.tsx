import { Button } from "./ds/Button/Button";
import { TextInput } from "./ds/TextInput/TextInput";
import { IconButton } from "./ds/IconButton/IconButton";
import { Skeleton } from "./ds/Skeleton/Skeleton";
import * as Tabs from "@radix-ui/react-tabs";
import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import { useCallback, useEffect, useRef, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { CATS, EmptyState } from "./EmptyCat";
import { DelayedFeedback } from "./DelayedFeedback";
import { ArtifactCard as KitArtifactCard } from "./ds/ArtifactCard/ArtifactCard";
import { FileRow } from "./ds/FileRow/FileRow";
import type { FileKind as KitFileKind } from "./ds/FileTile/parts";
import { attachmentUrl, fileKind, fileSize, openFilePreview, tileLabel, type FileKind } from "./filePreviewModel";
import { formatRelativeTime } from "./i18n/format";
import { useTranslation } from "./i18n";
import { errorText } from "./i18n/errors";
import { TofiIcon } from "./icons";
import type { Bot, Conversation } from "./types";
import "./artifacts-panel.css";
import { Switch } from "./ds/Switch/Switch";
import { Note } from "./ds/Notice/Notice";

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

/** The product's file kinds on the kit glyph's hues (as MessageAttachment). */
const kitKind: Record<FileKind, KitFileKind> = { md: "md", txt: "text", code: "code", csv: "sheet", tsv: "sheet", json: "code", pdf: "pdf", image: "image", xlsx: "sheet", docx: "doc", pptx: "slides", zip: "archive", audio: "audio", video: "video", other: "text" };
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

  return <Tabs.Root value={mode} onValueChange={value => onScope?.(value as Props["mode"])} asChild><div className="detail-content artifacts-panel">

    <div className="detail-heading"><div><h2>{title}</h2></div><button type="button" className="close-button" aria-label={t("artifacts.close")} onClick={onClose}><TofiIcon name="close" size={20} /></button></div>
    <div className="artifacts-tab-layout">
    <div className="artifacts-toolbar">{scopes && onScope && <Tabs.List className="tb-tablist artifacts-tabs artifacts-scopes" aria-label={t("artifacts.scope_label")}>{scopes.map(item => <Tabs.Trigger key={item.mode} value={item.mode} className="tb-tab">{item.label}</Tabs.Trigger>)}</Tabs.List>}</div>
    {scopes?.filter(item => item.mode !== mode).map(item => <Tabs.Content key={item.mode} value={item.mode} forceMount hidden />)}
    <Tabs.Content value={mode} className="artifacts-scope-panel" aria-labelledby={undefined} aria-label={title}><Tabs.Root className="artifacts-kind-root" value={kind} onValueChange={value => setKind(value as Kind)}>
    <div className="artifacts-toolbar">
      <Tabs.List className="tb-tablist artifacts-tabs artifacts-kinds" aria-label={t("artifacts.tabs_label")}>{kinds.map(([value, label]) => <Tabs.Trigger key={value} value={value} className="tb-tab">{label}</Tabs.Trigger>)}</Tabs.List>
      <TextInput className="artifacts-search" icon="search" type="search" aria-label={t("artifacts.search_label")} placeholder={t("artifacts.search_placeholder")} value={search} onChange={event => setSearch(event.target.value)} />
      <label className="debug-toggle artifacts-uploads"><span>{t("artifacts.include_uploads")}</span><Switch checked={uploads} onChange={setUploads} /></label>
    </div>
    {kinds.filter(([value]) => value !== kind).map(([value]) => <Tabs.Content key={value} value={value} forceMount hidden />)}
    <Tabs.Content value={kind} className="artifacts-result-panel">
    {error && <Note tone="error" role="alert">{error} <Button type="button" variant="link" onClick={() => items.length ? void loadMore() : setAttempt(value => value + 1)}>{t("artifacts.retry")}</Button></Note>}
    {loading && !items.length ? <DelayedFeedback><div className="inline-state" role="status"><div className="spinner" />{t("artifacts.loading")}</div></DelayedFeedback>
      : !items.length ? !error && <EmptyState name="artifacts-empty" className="panel-empty is-card" look={CATS.kiwi} pose="asleep"><p>{filtered ? t("artifacts.empty_filtered") : t("artifacts.empty")}</p></EmptyState>
      : <ul className="artifacts-grid" aria-label={t("artifacts.list_label")} aria-busy={loading}>{items.map(item => <ArtifactCard key={item.id} item={item} showBot={scope.kind !== "bot"} showConversation={scope.kind !== "conversation"} onOpenMessage={onOpenMessage} />)}</ul>}
    {next && <Button className="artifacts-more" disabled={more} onClick={() => void loadMore()}>{more ? t("artifacts.loading") : t("artifacts.load_more")}</Button>}
    </Tabs.Content></Tabs.Root></Tabs.Content>
    </div>
  </div></Tabs.Root>;
}

function ArtifactCard({ item, showBot, showConversation, onOpenMessage }: { item: ArtifactItem; showBot: boolean; showConversation: boolean; onOpenMessage: Props["onOpenMessage"] }) {
  const { t } = useTranslation("bots");
  const [open, setOpen] = useState(false);
  const [versions, setVersions] = useState<ArtifactItem[] | null>(null);
  const [state, setState] = useState<"idle" | "loading" | "error">("idle");
  const kind = fileKind(item.name, item.mime);

  async function toggleVersions(next: boolean) {
    setOpen(next);
    if (!next) return;
    if (versions || state === "loading") return;
    setState("loading");
    try { setVersions((await fetchArtifacts({ scope: "versions", id: item.id })).items.filter(version => version.id !== item.id)); setState("idle"); }
    catch { setState("error"); }
  }

  return <li className="artifact-item">
    <KitArtifactCard wide name={item.name} kind={kitKind[kind]} ext={tileLabel(item.name, kind)} meta={[fileSize(item.size), item.uploaded ? t("artifacts.uploaded") : "", formatRelativeTime(item.created_at)].filter(Boolean).join(" · ")}
      preview={isRaster(item) ? { type: "image", src: attachmentUrl(item.id) } : undefined} tag={item.uploaded ? null : undefined} version={item.version_count > 1 ? `v${item.version_count}` : undefined}
      from={<>{showBot && (item.bot_id ? <BotAvatar id={item.bot_id} mini /> : <TofiIcon name="user" size={14} />)}{showConversation && <span className="artifact-conversation">{item.conversation_name}{item.conversation_archived && <em>{t("artifacts.archived")}</em>}</span>}</>}
      gotoLabel={t("artifacts.go_to_message")} onGoto={() => onOpenMessage(item.conversation_id, item.message_id)} onOpen={() => openItem(item)} />
    {item.version_count > 1 && <Collapsible.Root open={open} onOpenChange={next => void toggleVersions(next)}>
      <Collapsible.Trigger asChild><Button variant="ghost" size="sm" className="artifact-versions-toggle">{open ? t("artifacts.hide_versions") : t("artifacts.earlier_versions", { count: item.version_count - 1 })}</Button></Collapsible.Trigger>
      <CollapsibleContent><ul className="artifact-versions">
        {state === "loading" && <li className="artifact-versions-note" role="status">{t("artifacts.versions_loading")}</li>}
        {state === "error" && <li className="artifact-versions-note" role="alert">{t("artifacts.versions_error")}</li>}
        {versions?.map(version => <li key={version.id}><Button variant="ghost" size="sm" className="artifact-version-open" onClick={() => openItem(version)} aria-label={t("artifacts.open", { name: version.name })}><span>{formatRelativeTime(version.created_at)}</span><small>{fileSize(version.size)}</small></Button><IconButton size="sm" icon="arrow-right" label={t("artifacts.go_aria", { name: version.name })} onClick={() => onOpenMessage(version.conversation_id, version.message_id)} /></li>)}
      </ul></CollapsibleContent>
    </Collapsible.Root>}
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
    {items === null ? (failed ? <p className="artifacts-widget-note" role="alert">{t("artifacts.error")}</p> : <Skeleton className="artifacts-widget-skeleton" lines={2} label={t("artifacts.loading")} />)
      : !items.length ? <p className="artifacts-widget-note">{t("artifacts.widget_empty")}</p>
      : <ul className="artifacts-widget-list" aria-label={t("artifacts.list_label")}>{items.map(item => <li key={item.id} data-fresh={fresh.has(item.id) || undefined}>
        <FileRow name={item.name} kind={kitKind[fileKind(item.name, item.mime)]} ext={tileLabel(item.name, fileKind(item.name, item.mime))} thumb={isRaster(item) ? attachmentUrl(item.id) : undefined} meta={formatRelativeTime(item.created_at)}
          fresh={fresh.has(item.id)} aria-label={t("artifacts.open", { name: item.name })} onOpen={() => openItem(item)} />
      </li>)}</ul>}
    <span className="sr-only" role="status">{announce}</span>
    <Button variant="ghost" size="sm" className="artifacts-widget-all" iconAfter="arrow-right" onClick={onAll}>{t("artifacts.widget_all")}</Button>
  </div>;
}
