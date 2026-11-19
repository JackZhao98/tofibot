import { useEffect, useRef, useState } from "react";
import { arrowOpens, pressToggle } from "../overlays/OverlayHost";
import { BotAvatar } from "../ds/BotAvatar/BotAvatar";
import { ChatHeader as KitChatHeader, type ChatHeaderPosition } from "../ds/ChatHeader/ChatHeader";
import { IconButton } from "../ds/IconButton/IconButton";
import type { Bot, Conversation, Run, ToolActivity } from "../types";
import { type CoverId, type SidebarId, type WidgetId } from "../surfaces";
import type { TFunction } from "i18next";
import type { ComputerEntry } from "./HeaderMenus";
import type { Dispatch, RefObject, SetStateAction } from "react";

type HeaderMenus = typeof import("./HeaderMenus");

export interface ChatHeaderProps {
  chatHeaderRef: RefObject<HTMLElement | null>;
  setMobileList: Dispatch<SetStateAction<boolean>>;
  t: TFunction<readonly ["chat", "common"], undefined>;
  active: Conversation;
  columnMode: boolean;
  /** At the start of the conversation (the mark at the top of the list is in view). */
  atTop: boolean;
  /** A cover is open: the cat steps aside. */
  covered: boolean;
  toggleWidgetId: (id: WidgetId) => void;
  openCoverPanel: (id: CoverId) => void;
  botById: Map<string, Bot>;
  /** What the cat shows is worked out in the menus chunk (`botCatState`) from these. */
  cats: { runs: Run[]; activities: ToolActivity[]; asked: ReadonlySet<string>; asleep: (botId: string) => boolean };
  /** The 电脑 slot: the Bot computer, the Mac app's browser panel, the Macs this Bot can look at. One shows as a button; more open a menu. */
  computers: ComputerEntry[];
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

export function ChatHeader({ chatHeaderRef, setMobileList, t, active, columnMode, atTop, covered, toggleWidgetId, openCoverPanel, botById, cats, computers, artifactsOn, setArtifactsMode, toggleSidebarId, headerMenuTriggerRef, headerMenuOpen, setHeaderMenuOpen, desktopBot, openTerminalSurface, openSidebarId, openWidgetId, copyConversationLink, confirmDelete }: ChatHeaderProps) {
  const [menus, setMenus] = useState<HeaderMenus | null>(null);
  useEffect(() => { void import("./HeaderMenus").then(setMenus); }, []);
  const [sourcesOpen, setSourcesOpen] = useState(false);
  const sourcesRef = useRef<HTMLButtonElement | null>(null);
  const ids = active.kind === "group" ? active.bot_ids : [active.bot_id ?? active.id];
  const position: ChatHeaderPosition = covered ? "aside" : atTop ? "top" : "scrolled";
  const any = (key: "pressed" | "on" | "driven") => computers.some(entry => entry[key]);
  const only = computers.length === 1 ? computers[0] : undefined;
  const artifacts = <IconButton icon="folder" label={t("header.open_artifacts")} filled={artifactsOn} aria-pressed={artifactsOn} onClick={() => { if (columnMode) toggleWidgetId("artifacts"); else { setArtifactsMode("artifacts-group"); openCoverPanel("artifacts"); } }} />;
  const actions = <>
    {computers.length > 0 && <IconButton ref={sourcesRef} icon={only?.icon ?? "monitor"} animated={!only || only.icon === "monitor"} filled={any("pressed")} label={only?.label ?? t("header.shared_computer")}
      className={`computer-button${any("on") ? " is-on" : ""}${any("driven") ? " is-driven" : ""}`}
      {...(only ? { ...only.attrs, "aria-pressed": only.pressed, onClick: only.toggle } : { "aria-haspopup": "menu", "aria-expanded": sourcesOpen, "aria-controls": sourcesOpen ? "computer-source-menu" : undefined, ...pressToggle(() => setSourcesOpen(open => !open)), onKeyDown: arrowOpens(setSourcesOpen) })}>{any("driven") && <span className="computer-driven-dot tb-chathead-driven" aria-hidden="true" />}</IconButton>}
    {sourcesOpen && !only && menus && <menus.SourceMenu entries={computers} label={t("header.shared_computer")} trigger={sourcesRef} setOpen={setSourcesOpen} />}
    {artifacts}
    <div className="header-more">
      <IconButton ref={headerMenuTriggerRef} icon="more" label={t("header.more")} aria-haspopup="menu" aria-expanded={headerMenuOpen} aria-controls={headerMenuOpen ? "conversation-more-menu" : undefined} {...pressToggle(() => setHeaderMenuOpen(open => !open))} onKeyDown={arrowOpens(setHeaderMenuOpen)} />
      {headerMenuOpen && menus && <menus.MoreMenu t={t} active={active} trigger={headerMenuTriggerRef} setOpen={setHeaderMenuOpen} columnMode={columnMode} hasComputer={!!desktopBot}
        tasks={() => toggleSidebarId("tasks")} members={() => { if (columnMode) openWidgetId("members"); else openCoverPanel("group-settings"); }} terminal={openTerminalSurface}
        memory={() => openSidebarId("memory")} usage={() => { if (columnMode) openWidgetId("usage"); else openSidebarId("usage"); }} approvals={() => openCoverPanel("approvals")}
        copyLink={() => void copyConversationLink(active)} settings={() => openCoverPanel(active.kind === "dm" ? "bot-settings" : "group-settings")} remove={() => confirmDelete(active)} />}
    </div>
  </>;
  return <KitChatHeader
    headerRef={chatHeaderRef}
    className="chat-header"
    position={position}
    title={active.name}
    subtitle={active.kind === "group" ? t("header.member_count", { count: active.bot_ids.length }) : undefined}
    avatars={ids.map(id => <BotAvatar key={id} id={id} name={botById.get(id)?.name ?? active.name} coat="paper" size="var(--cat)" state={menus?.botCatState(id, active.id, cats.runs, cats.activities, cats.asleep(id), cats.asked)} />)}
    onOpen={() => active.kind === "group" ? (columnMode ? toggleWidgetId("members") : openCoverPanel("group-settings")) : openCoverPanel("bot-settings")}
    openLabel={active.kind === "group" ? t("header.open_members") : t("header.bot_settings")}
    menu={<IconButton icon="arrow-left" label={t("route.back_to_list")} className="back-button" onClick={() => setMobileList(true)} />}
    menuMode="narrow"
    drag={<span className="chat-header-drag" aria-hidden="true" />}
    actions={actions}
  />;
}
