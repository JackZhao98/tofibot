import { arrowOpens, OverlayMenu, pressToggle } from "../overlays/OverlayHost";
import { TofiIcon as Icon } from "../icons";
import { type AvatarMotion } from "../BotAvatar";
import type { Bot, Conversation } from "../types";
import { type CoverId, type SidebarId, type WidgetId } from "../surfaces";
import { Avatar } from "./shared";
import type { TFunction } from "i18next";
import type { BotBrowserBridge } from "../desktop";
import type { PanelFailure } from "../useNativeSidePanel";
import type { Dispatch, RefObject, SetStateAction } from "react";

export interface ChatHeaderProps {
  chatHeaderRef: RefObject<HTMLDivElement | null>;
  setMobileList: Dispatch<SetStateAction<boolean>>;
  t: TFunction<readonly ["chat", "common"], undefined>;
  active: Conversation;
  columnMode: boolean;
  toggleWidgetId: (id: WidgetId) => void;
  openCoverPanel: (id: CoverId) => void;
  botById: Map<string, Bot>;
  conversationMotion: (conversation: Conversation, botId?: string | undefined) => AvatarMotion;
  nativePanel: { bridge: BotBrowserBridge | undefined; open: boolean; request: (next: boolean) => void; failure: PanelFailure | null; };
  browserT: TFunction<"computer", undefined>;
  computerButton: React.JSX.Element;
  artifactsOn: boolean;
  setArtifactsMode: Dispatch<SetStateAction<"artifacts" | "artifacts-bot" | "artifacts-group">>;
  sidebarId: string | null;
  toggleSidebarId: (id: SidebarId) => void;
  headerMenuTriggerRef: RefObject<HTMLButtonElement | null>;
  headerMenuOpen: boolean;
  setHeaderMenuOpen: Dispatch<SetStateAction<boolean>>;
  desktopBot: Bot | undefined;
  openTerminalSurface: () => void;
  openSidebarId: (id: SidebarId) => void;
  openWidgetId: (id: WidgetId) => void;
  copyConversationLink: (conversation: Conversation) => Promise<void>;
  confirmDelete: (conversation: Conversation) => void;
}

export function ChatHeader({ chatHeaderRef, setMobileList, t, active, columnMode, toggleWidgetId, openCoverPanel, botById, conversationMotion, nativePanel, browserT, computerButton, artifactsOn, setArtifactsMode, sidebarId, toggleSidebarId, headerMenuTriggerRef, headerMenuOpen, setHeaderMenuOpen, desktopBot, openTerminalSurface, openSidebarId, openWidgetId, copyConversationLink, confirmDelete }: ChatHeaderProps) {
  return (
    <div className="chat-header" ref={chatHeaderRef}><span className="chat-header-drag" aria-hidden="true" /><div className="chat-title"><button className="back-button" onClick={() => setMobileList(true)} aria-label={t("route.back_to_list")}><Icon name="arrow-left" size={19} /></button><button className="chat-identity" onClick={() => active.kind === "group" ? (columnMode ? toggleWidgetId("members") : openCoverPanel("group-settings")) : openCoverPanel("bot-settings")} aria-label={t("header.open_members")}><span className="member-avatar-stack">{(active.kind === "group" ? active.bot_ids : [active.bot_id ?? active.id]).slice(0, 4).map((id) => <Avatar key={id} label={botById.get(id)?.name ?? active.name} id={id} mini motion={conversationMotion(active, id)} />)}</span><span className="chat-name-pill"><h1>{active.name}</h1>{active.kind === "group" && <small>{t("header.member_count", { count: active.bot_ids.length })}</small>}</span></button></div><div className="header-actions">{nativePanel.bridge && <button type="button" className="computer-button" data-native-browser-toggle="" title={browserT("browser.panel")} aria-label={browserT("browser.panel")} aria-pressed={nativePanel.open} onClick={() => nativePanel.request(!nativePanel.open)}><Icon name="globe" size={17}/></button>}{active.kind === "dm" && computerButton}<button className="computer-button" data-hint={t("header.open_artifacts")} aria-label={t("header.open_artifacts")} aria-pressed={artifactsOn} onClick={() => { if (columnMode) toggleWidgetId("artifacts"); else { setArtifactsMode("artifacts-group"); openCoverPanel("artifacts"); } }}><Icon name="folder" size={18} variant={artifactsOn ? "filled" : "outline"} /></button>{active.kind === "group" && computerButton}<button className="computer-button" data-hint={t("header.tasks")} aria-label={t("header.tasks")} aria-pressed={sidebarId === "tasks"} onClick={() => toggleSidebarId("tasks")}><Icon name="checklist" size={18} variant={sidebarId === "tasks" ? "filled" : "outline"} /></button><div className="header-more"><button ref={headerMenuTriggerRef} className="ghost-button" data-hint={t("header.more")} aria-label={t("header.more")} aria-haspopup="menu" aria-expanded={headerMenuOpen} aria-controls={headerMenuOpen ? "conversation-more-menu" : undefined} {...pressToggle(() => setHeaderMenuOpen(open => !open))} onKeyDown={arrowOpens(setHeaderMenuOpen)}><Icon name="more" size={17} /></button>{headerMenuOpen && <OverlayMenu open={headerMenuOpen} onOpenChange={setHeaderMenuOpen} id="conversation-more-menu" className="more-menu" label={t("header.more")} trigger={headerMenuTriggerRef} align="end" sideOffset={6}>{desktopBot && <button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); openTerminalSurface(); }}>{t("header.terminal")}</button>}<button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); openSidebarId("memory"); }}>{active.kind === "group" ? t("header.group_memory") : t("header.bot_memory")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); if (columnMode) openWidgetId("usage"); else openSidebarId("usage"); }}>{t("header.usage")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); openCoverPanel("approvals"); }}>{t("header.remembered_approvals")}</button><button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); void copyConversationLink(active); }}>{t("header.copy_link")}</button>{active.kind === "dm" ? <button onClick={() => { headerMenuTriggerRef.current?.focus(); openCoverPanel("bot-settings"); setHeaderMenuOpen(false); }}>{t("header.bot_settings")}</button> : <button onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); openCoverPanel("group-settings"); }}>{t("header.group_settings")}</button>}<button className="delete-menu-action" onClick={() => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); confirmDelete(active); }}>{active.kind === "dm" ? t("header.delete_bot") : t("header.delete_group")}</button></OverlayMenu>}</div></div></div>
  );
}
