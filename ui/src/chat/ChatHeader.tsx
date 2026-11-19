import { arrowOpens, OverlayMenu, pressToggle } from "../overlays/OverlayHost";
import { BotAvatar } from "../ds/BotAvatar/BotAvatar";
import type { BotAvatarState } from "../ds/BotAvatar/BotAvatar";
import { ChatHeader as KitChatHeader, type ChatHeaderPosition } from "../ds/ChatHeader/ChatHeader";
import { IconButton } from "../ds/IconButton/IconButton";
import type { Bot, Conversation } from "../types";
import { type CoverId, type SidebarId, type WidgetId } from "../surfaces";
import type { TFunction } from "i18next";
import type { BotBrowserBridge } from "../desktop";
import type { PanelFailure } from "../useNativeSidePanel";
import type { Dispatch, RefObject, SetStateAction } from "react";

export interface ChatHeaderProps {
  chatHeaderRef: RefObject<HTMLElement | null>;
  setMobileList: Dispatch<SetStateAction<boolean>>;
  t: TFunction<readonly ["chat", "common"], undefined>;
  active: Conversation;
  columnMode: boolean;
  /** At the start of the conversation (the sentinel at the top of the list is in view). */
  atTop: boolean;
  /** A cover is open: the cat steps aside. */
  covered: boolean;
  toggleWidgetId: (id: WidgetId) => void;
  openCoverPanel: (id: CoverId) => void;
  botById: Map<string, Bot>;
  /** What a Bot is doing, for the cat's motion and dot. Nothing: awake and idle. */
  catState: (botId: string) => BotAvatarState | undefined;
  nativePanel: { bridge: BotBrowserBridge | undefined; open: boolean; request: (next: boolean) => void; failure: PanelFailure | null; };
  browserT: TFunction<"computer", undefined>;
  /** The 电脑 slot: the Bot computer and the Macs this Bot can look at. */
  computerButton: React.JSX.Element | null;
  artifactsOn: boolean;
  setArtifactsMode: Dispatch<SetStateAction<"artifacts" | "artifacts-bot" | "artifacts-group">>;
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

export function ChatHeader({ chatHeaderRef, setMobileList, t, active, columnMode, atTop, covered, toggleWidgetId, openCoverPanel, botById, catState, nativePanel, browserT, computerButton, artifactsOn, setArtifactsMode, toggleSidebarId, headerMenuTriggerRef, headerMenuOpen, setHeaderMenuOpen, desktopBot, openTerminalSurface, openSidebarId, openWidgetId, copyConversationLink, confirmDelete }: ChatHeaderProps) {
  const ids = active.kind === "group" ? active.bot_ids : [active.bot_id ?? active.id];
  const position: ChatHeaderPosition = covered ? "aside" : atTop ? "top" : "scrolled";
  const pick = (action: () => void) => () => { headerMenuTriggerRef.current?.focus(); setHeaderMenuOpen(false); action(); };
  // The Mac app's browser panel takes the 电脑 slot when the Bot has no computer; with both, it is a menu item.
  const native = Boolean(nativePanel.bridge);
  const artifacts = <IconButton icon="folder" label={t("header.open_artifacts")} filled={artifactsOn} aria-pressed={artifactsOn} onClick={() => { if (columnMode) toggleWidgetId("artifacts"); else { setArtifactsMode("artifacts-group"); openCoverPanel("artifacts"); } }} />;
  const actions = <>
    {native && !desktopBot && <IconButton icon="globe" label={browserT("browser.panel")} className="computer-button" data-native-browser-toggle="" aria-pressed={nativePanel.open} onClick={() => nativePanel.request(!nativePanel.open)} />}
    {computerButton}
    {artifacts}
    <div className="header-more">
      <IconButton ref={headerMenuTriggerRef} icon="more" label={t("header.more")} aria-haspopup="menu" aria-expanded={headerMenuOpen} aria-controls={headerMenuOpen ? "conversation-more-menu" : undefined} {...pressToggle(() => setHeaderMenuOpen(open => !open))} onKeyDown={arrowOpens(setHeaderMenuOpen)} />
      {headerMenuOpen && <OverlayMenu open={headerMenuOpen} onOpenChange={setHeaderMenuOpen} id="conversation-more-menu" className="more-menu" label={t("header.more")} trigger={headerMenuTriggerRef} align="end" sideOffset={6}>
        <button onClick={pick(() => toggleSidebarId("tasks"))}>{t("header.tasks")}</button>
        {active.kind === "group" && <button onClick={pick(() => { if (columnMode) openWidgetId("members"); else openCoverPanel("group-settings"); })}>{t("header.open_members")}</button>}
        {native && desktopBot && <button onClick={pick(() => nativePanel.request(!nativePanel.open))} data-native-browser-toggle="">{browserT("browser.panel")}</button>}
        {desktopBot && <button onClick={pick(openTerminalSurface)}>{t("header.terminal")}</button>}
        <button onClick={pick(() => openSidebarId("memory"))}>{active.kind === "group" ? t("header.group_memory") : t("header.bot_memory")}</button>
        <button onClick={pick(() => { if (columnMode) openWidgetId("usage"); else openSidebarId("usage"); })}>{t("header.usage")}</button>
        <button onClick={pick(() => openCoverPanel("approvals"))}>{t("header.remembered_approvals")}</button>
        <button onClick={pick(() => void copyConversationLink(active))}>{t("header.copy_link")}</button>
        {active.kind === "dm" ? <button onClick={() => { headerMenuTriggerRef.current?.focus(); openCoverPanel("bot-settings"); setHeaderMenuOpen(false); }}>{t("header.bot_settings")}</button> : <button onClick={pick(() => openCoverPanel("group-settings"))}>{t("header.group_settings")}</button>}
        <button className="delete-menu-action" onClick={pick(() => confirmDelete(active))}>{active.kind === "dm" ? t("header.delete_bot") : t("header.delete_group")}</button>
      </OverlayMenu>}
    </div>
  </>;
  return <KitChatHeader
    headerRef={chatHeaderRef}
    className="chat-header"
    position={position}
    title={active.name}
    subtitle={active.kind === "group" ? t("header.member_count", { count: active.bot_ids.length }) : undefined}
    avatars={ids.map(id => <BotAvatar key={id} id={id} name={botById.get(id)?.name ?? active.name} coat="paper" size="var(--cat)" state={catState(id)} />)}
    onOpen={() => active.kind === "group" ? (columnMode ? toggleWidgetId("members") : openCoverPanel("group-settings")) : openCoverPanel("bot-settings")}
    openLabel={active.kind === "group" ? t("header.open_members") : t("header.bot_settings")}
    menu={<IconButton icon="arrow-left" label={t("route.back_to_list")} className="back-button" onClick={() => setMobileList(true)} />}
    menuMode="narrow"
    drag={<span className="chat-header-drag" aria-hidden="true" />}
    actions={actions}
  />;
}
