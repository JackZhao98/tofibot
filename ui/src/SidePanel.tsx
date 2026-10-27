import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { request } from "./api";
import type { ArtifactItem } from "./ArtifactsPanel";
import { BotAvatar } from "./BotAvatar";
import { FileTile } from "./FileViewer";
import { fileKind, type PreviewTarget } from "./filePreviewModel";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { layerRoot } from "./layers";
import type { Bot, Conversation } from "./types";
import "./side-panel.css";

/** What the side panel opens in the outside panel. */
export type SideTarget = "desktop" | "bot-edit" | "members" | "schedule" | "artifacts-group";
export type SideTasks = { open: number; done: number; total: number };

const FOLD_KEY = "tofi.sidePanel.folded";
const PREVIEW_ROWS = 5;
const readFolded = (): string[] => { try { const value = JSON.parse(localStorage.getItem(FOLD_KEY) ?? "[]"); return Array.isArray(value) ? value.filter(item => typeof item === "string") : []; } catch { return []; } };
const writeFolded = (ids: string[]) => { try { localStorage.setItem(FOLD_KEY, JSON.stringify(ids)); } catch { /* the fold state only lasts for this page */ } };

/** One summary card: a header that folds it (title and chevron) and a few lines of key facts. */
function SideCard({ id, title, folded, onFold, children }: { id: string; title: string; folded: boolean; onFold: (id: string) => void; children: ReactNode }) {
  const { t } = useTranslation("chat");
  const body = useId();
  return <section className={`side-card${folded ? " is-folded" : ""}`} data-side-card={id}>
    <h3><button type="button" className="side-card-head" aria-expanded={!folded} aria-controls={body} aria-label={t(folded ? "sidePanel.unfold" : "sidePanel.fold", { title })} onClick={() => onFold(id)}><span>{title}</span><Icon name="chevron-down" size={16} /></button></h3>
    <div className="side-card-fold" id={body} inert={folded || undefined}><div className="side-card-inner">{children}</div></div>
  </section>;
}

function Row({ onClick, label, children }: { onClick: () => void; label?: string; children: ReactNode }) {
  return <button type="button" className="side-row" aria-label={label} onClick={onClick}>{children}<Icon name="chevron-right" size={15} /></button>;
}

const computerKey = (state: string) => state === "ready" ? "running" : state === "hibernated" || state === "hibernating" || state === "stopped" ? "asleep" : state === "starting" || state === "resuming" ? "starting" : "off";

/**
 * The in-chat side panel: compact summary cards for what this conversation has (computer, outputs, Bot, tasks), each with
 * its key facts only. Details open in the outside panel (docs/design/side-panels.md). Without room beside the
 * conversation it is a sheet from the right edge (`sheet`).
 */
export function SidePanel({ conversation, bots, activeBot, computer, tasks, refreshKey, sheet = false, onOpen, onOpenFile, onClose }: {
  conversation: Conversation; bots: readonly Bot[]; activeBot?: Bot; computer?: { state: string; botName: string } | null; tasks?: SideTasks | null;
  /** Changes when the conversation gets new messages; the outputs are read again. */
  refreshKey: string; sheet?: boolean; onOpen: (target: SideTarget) => void; onOpenFile: (target: PreviewTarget) => void; onClose?: () => void;
}) {
  const { t } = useTranslation("chat");
  const [folded, setFolded] = useState(readFolded);
  const toggle = (id: string) => setFolded(current => { const next = current.includes(id) ? current.filter(item => item !== id) : [...current, id]; writeFolded(next); return next; });
  const [outputs, setOutputs] = useState<{ items: ArtifactItem[]; more: boolean }>({ items: [], more: false });
  const conversationId = conversation.id;
  useEffect(() => {
    const abort = new AbortController();
    const timer = window.setTimeout(() => {
      request<{ items: ArtifactItem[]; next_cursor: string }>(`/api/artifacts?${new URLSearchParams({ scope: "conversation", id: conversationId, include_uploads: "0" })}`, { signal: abort.signal })
        .then(page => { const list = Array.isArray(page?.items) ? page.items : []; if (!abort.signal.aborted) setOutputs({ items: list, more: Boolean(page?.next_cursor) || list.length > PREVIEW_ROWS }); }, () => undefined);
    }, 400);
    return () => { window.clearTimeout(timer); abort.abort(); };
  }, [conversationId, refreshKey]);
  useEffect(() => { setOutputs({ items: [], more: false }); }, [conversationId]);
  const ref = useRef<HTMLElement>(null);
  useEffect(() => {
    if (!sheet) return;
    ref.current?.querySelector<HTMLElement>("button")?.focus({ preventScroll: true });
    const key = (event: KeyboardEvent) => { if (event.key === "Escape" && !event.defaultPrevented) { event.preventDefault(); onClose?.(); } };
    document.addEventListener("keydown", key);
    return () => document.removeEventListener("keydown", key);
  }, [sheet, onClose]);

  const members = conversation.kind === "group" ? conversation.bot_ids.map(id => bots.find(bot => bot.id === id)).filter((bot): bot is Bot => Boolean(bot)) : [];
  const modelLine = activeBot ? (activeBot.model === "default" ? (activeBot.effective_model || t("sidePanel.model_default")) : activeBot.model) : "";
  const state = computer ? computerKey(computer.state) : "off";
  const fold = (id: string) => folded.includes(id);
  const shown = outputs.items.slice(0, PREVIEW_ROWS);

  const panel = <aside ref={ref} className={`side-panel${sheet ? " is-sheet" : ""}`} aria-label={t("sidePanel.label")}>
    {sheet && <div className="side-sheet-bar"><strong>{t("sidePanel.label")}</strong><button type="button" className="outside-button" aria-label={t("sidePanel.hide")} onClick={onClose}><Icon name="close" size={16} /></button></div>}
    {computer && <SideCard id="computer" title={t("sidePanel.computer")} folded={fold("computer")} onFold={toggle}>
      <Row onClick={() => onOpen("desktop")} label={t("sidePanel.open_computer")}>
        <span className="side-thumb" data-state={state} aria-hidden="true"><Icon name="monitor" size={20} /></span>
        <span className="side-lines"><strong><i className="side-dot" data-state={state} aria-hidden="true" />{t(`sidePanel.computer_${state}`)}</strong><small>{computer.botName}</small></span>
      </Row>
    </SideCard>}
    {shown.length > 0 && <SideCard id="outputs" title={t("sidePanel.outputs")} folded={fold("outputs")} onFold={toggle}>
      {shown.map(item => <Row key={item.id} onClick={() => onOpenFile({ id: item.id, name: item.name, mime: item.mime, size: item.size, conversationId: item.conversation_id })}>
        <FileTile name={item.name} kind={fileKind(item.name, item.mime)} /><span className="side-lines"><strong>{item.name}</strong></span>
      </Row>)}
      {outputs.more && <button type="button" className="side-more" onClick={() => onOpen("artifacts-group")}>{t("sidePanel.show_all")}</button>}
    </SideCard>}
    <SideCard id="bot" title={conversation.kind === "group" ? t("sidePanel.members") : t("sidePanel.bot")} folded={fold("bot")} onFold={toggle}>
      {conversation.kind === "group"
        ? <Row onClick={() => onOpen("members")} label={t("header.open_members")}>
            <span className="member-avatar-stack side-avatars">{members.slice(0, 4).map(bot => <BotAvatar key={bot.id} id={bot.id} mini still />)}</span>
            <span className="side-lines"><strong>{t("header.member_count", { count: conversation.bot_ids.length })}</strong><small>{members.map(bot => bot.name).join(", ")}</small></span>
          </Row>
        : activeBot && <Row onClick={() => onOpen("bot-edit")} label={t("header.bot_settings")}>
            <BotAvatar id={activeBot.id} still />
            <span className="side-lines"><strong>{activeBot.name}</strong><small>{modelLine}</small></span>
          </Row>}
    </SideCard>
    {tasks && tasks.total > 0 && <SideCard id="tasks" title={t("sidePanel.tasks")} folded={fold("tasks")} onFold={toggle}>
      <Row onClick={() => onOpen("schedule")} label={t("sidePanel.open_tasks")}>
        <span className="side-lines"><strong>{t("sidePanel.tasks_open", { open: tasks.open })}</strong><small>{t("sidePanel.tasks_done", { done: tasks.done, total: tasks.total })}</small>
          <span className="side-progress" role="presentation"><i style={{ inlineSize: `${Math.round((tasks.done / tasks.total) * 100)}%` }} /></span></span>
      </Row>
    </SideCard>}
  </aside>;
  if (!sheet) return panel;
  return createPortal(<><button type="button" className="side-backdrop" aria-label={t("sidePanel.hide")} tabIndex={-1} onClick={onClose} />{panel}</>, layerRoot());
}
