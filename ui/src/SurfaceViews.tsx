import { lazy, Suspense, type ReactNode } from "react";
import { api } from "./api";
import { ArchivePanel, ArtifactsPanel, ArtifactsWidget, chunk, MemoryPanel, ViewOnlyChat, WorkPanel } from "./lazyPanels";
import { GroupSettingsPanel } from "./GroupSettingsPanel";
import { MembersWidget } from "./MembersWidget";
import { NewGroupDialog } from "./NewGroupDialog";
import { DelayedFeedback } from "./DelayedFeedback";
import { CATS, EmptyState } from "./EmptyCat";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { closeCover, closeSidebar, closeWidget, openCover, type CoverId, type LegacyPanel, type SidebarId, type Surfaces, type WidgetId } from "./surfaces";
import type { Bot, Conversation, Memory } from "./types";
import type { TaskCardModel } from "./taskCards";
import type { ViewOnlyChatTarget } from "./ViewOnlyChat";

// Bot settings is the heavy cover (model list, Skills, Secrets, the avatar picker): its own chunk, declared here so the entry carries only the
// loader line. Group settings, the New group dialog and the Members widget are small and ride with this module (the entry budget has no room
// for a loader each).
const BotSettingsPanel = chunk("botSettings", module => module.BotSettingsPanel);
const TerminalPanel = lazy(() => import("./TerminalPanel").then(module => ({ default: module.TerminalPanel })));

/**
 * What the workspace hands to the views of its surfaces (docs/design/widgets-and-sidebar.md): the data the panels read and the few
 * actions they call. This module is a lazy chunk, so the markup of every widget, sidebar view and cover stays out of the entry bundle;
 * the workspace keeps only the state (surfaces.ts) and the frames.
 */
export type SurfaceCtx = {
  active?: Conversation; activeBot?: Bot; desktopBot?: Bot; bots: Bot[]; conversations: Conversation[]; botById: Map<string, Bot>;
  memories: Memory[]; setMemories: (update: (current: Memory[]) => Memory[]) => void;
  terminalBotId: string | null; viewOnlyChat: ViewOnlyChatTarget | null; setViewOnlyChat: (target: ViewOnlyChatTarget | null) => void;
  refreshToken: number; roster?: ReadonlyMap<string, TaskCardModel[]>; lastMessageId?: string;
  artifactsMode: "artifacts" | "artifacts-bot" | "artifacts-group"; setArtifactsMode: (mode: SurfaceCtx["artifactsMode"]) => void;
  setPanel: (panel: LegacyPanel | null) => void;
  /** The workspace's surface state: the views close and open surfaces with the functions of surfaces.ts. */
  surf: (update: (current: Surfaces) => Surfaces) => void;
  openConversation: (id: string) => void; openMessage: (conversationId: string, messageId: string) => void; openSettingsAdvanced: (botId: string) => void;
  mergeArchived: (conversations: Conversation[], bots: Bot[]) => void; refreshAfterArchive: () => Promise<void>; confirmDelete: (conversation: Conversation) => void;
  updateBot: (id: string, input: Partial<Pick<Bot, "name" | "instructions" | "model" | "reasoning_effort">>) => Promise<void>;
  createGroup: (name: string, botIds: string[]) => Promise<void>; makePrimary: (botId: string) => Promise<void>;
  updateGroup: (id: string, input: { name?: string; bot_ids?: string[]; expected_bot_ids?: string[]; expected_name?: string }) => Promise<Conversation>;
  reloadGroup: (id: string) => Promise<Conversation>;
};

function Loading() {
  return <DelayedFeedback><div className="inline-state" role="status"><div className="spinner" /></div></DelayedFeedback>;
}

/** One sidebar view. The wide layout draws it under SidebarShell, the narrow drawer bare (the panels carry their own heading and close then). */
export function SidebarView({ id, ctx }: { id: SidebarId; ctx: SurfaceCtx }) {
  const { t } = useTranslation("chat");
  const { active, desktopBot } = ctx;
  const close = () => ctx.surf(closeSidebar);
  if (id === "archive") return <Suspense fallback={<Loading />}><ArchivePanel onClose={close} onOpen={ctx.openConversation} onLoaded={ctx.mergeArchived} onChanged={ctx.refreshAfterArchive} onDelete={ctx.confirmDelete} /></Suspense>;
  if (id === "terminal") {
    const bot = (ctx.terminalBotId && ctx.botById.get(ctx.terminalBotId)) || desktopBot;
    return bot ? <Suspense fallback={<DelayedFeedback><div className="inline-state" role="status">{t("detail.loading_terminal")}</div></DelayedFeedback>}><TerminalPanel key={bot.id} botId={bot.id} botName={bot.name} onClose={close} /></Suspense> : null;
  }
  if (id === "memory") {
    if (!active) return <div className="detail-empty"><button className="close-button" aria-label={t("detail.close_memory")} onClick={close}><Icon name="close" size={18} /></button><EmptyState name="memory-pick" look={CATS.azuki} pose="curious" size={56}><p>{t("detail.memory_pick")}</p></EmptyState></div>;
    return <Suspense fallback={<Loading />}><MemoryPanel key={active.id} memories={ctx.memories} conversationId={active.id} scope={active.kind === "group" ? "group" : "bot"} onClose={close}
      onCreate={async input => { const memory = await api.createMemory(active.id, input); ctx.setMemories(current => [...current.filter(item => item.id !== memory.id), memory]); }}
      onUpdate={async (memoryId, input) => { const memory = await api.updateMemory(memoryId, input); ctx.setMemories(current => current.map(item => item.id === memoryId ? memory : item)); }}
      onDelete={async memoryId => { await api.deleteMemory(memoryId); ctx.setMemories(current => current.filter(item => item.id !== memoryId)); }} /></Suspense>;
  }
  if (id === "tasks") return active ? <Suspense fallback={<Loading />}><WorkPanel key={active.id} conversation={active} conversations={ctx.conversations} bots={ctx.bots} refreshToken={ctx.refreshToken} onClose={close} onNavigate={ctx.openConversation} /></Suspense> : null;
  return ctx.viewOnlyChat ? <Suspense fallback={<Loading />}><ViewOnlyChat target={ctx.viewOnlyChat} bots={ctx.bots} sidebar onClose={() => { ctx.setViewOnlyChat(null); close(); }} /></Suspense> : null;
}

/** One widget's content (inside its card). */
export function WidgetView({ id, ctx }: { id: Exclude<WidgetId, "computer">; ctx: SurfaceCtx }) {
  const { active } = ctx;
  if (id === "members") return active?.kind === "group" ? <MembersWidget conversation={active} bots={ctx.bots} tasksByBot={ctx.roster} onOpen={ctx.openConversation} onSettings={() => ctx.surf(current => openCover(current, "group-settings"))} /> : null;
  return active ? <Suspense fallback={null}><ArtifactsWidget conversationId={active.id} refreshToken={ctx.lastMessageId} onAll={() => { ctx.setArtifactsMode("artifacts-group"); ctx.surf(current => openCover(current, "artifacts")); }} /></Suspense> : null;
}

/** One cover's content (inside CoverShell, which draws the title and the close). Settings and Approvals draw their own windows. */
export function CoverView({ id, ctx }: { id: CoverId; ctx: SurfaceCtx }) {
  const { t } = useTranslation("chat");
  const { active, activeBot } = ctx;
  const close = () => ctx.surf(closeCover);
  if (id === "bot-settings") {
    if (!activeBot) return null;
    const dm = ctx.conversations.find(item => item.id === activeBot.dm_conversation_id) ?? active;
    return <Suspense fallback={null}><BotSettingsPanel key={activeBot.id} bot={activeBot} refreshToken={ctx.refreshToken} onUpdate={ctx.updateBot} onMakePrimary={ctx.makePrimary} onExportData={ctx.openSettingsAdvanced} onDelete={() => { if (dm) ctx.confirmDelete(dm); }} /></Suspense>;
  }
  if (id === "new-group") return <NewGroupDialog bots={ctx.bots.filter(bot => !bot.archived)} onCreate={ctx.createGroup} />;
  if (id === "group-settings") return active?.kind === "group" ? <GroupSettingsPanel key={active.id} conversation={active} bots={ctx.bots} onSaved={ctx.updateGroup} onReload={ctx.reloadGroup} onOpenMemory={() => ctx.setPanel("memory")} onDelete={() => ctx.confirmDelete(active)} /> : null;
  if (id === "artifacts") {
    const scopes = active ? [
      { mode: "artifacts-group" as const, label: t(active.kind === "group" ? "header.artifacts_scope_group" : "header.artifacts_scope_chat") },
      ...(active.kind === "dm" && activeBot ? [{ mode: "artifacts-bot" as const, label: t("header.artifacts_scope_bot") }] : []),
      { mode: "artifacts" as const, label: t("header.artifacts_scope_all") },
    ] : undefined;
    const mode = ctx.artifactsMode;
    return <Suspense fallback={null}><ArtifactsPanel key={`${mode}:${mode === "artifacts-group" ? active?.id : mode === "artifacts-bot" ? activeBot?.id : ""}`} mode={mode} conversation={active} bot={activeBot} scopes={scopes} onScope={ctx.setArtifactsMode} onClose={close} onOpenMessage={ctx.openMessage} /></Suspense>;
  }
  return null as ReactNode;
}
