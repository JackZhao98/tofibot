import { SidebarAccount } from "../OwnerSession";
import { isDesktop } from "../desktop";
import type { SettingsTab, SettingsView } from "../SettingsShell";
import { arrowOpens, pressToggle } from "../overlays/OverlayHost";
import { TofiIcon as Icon } from "../icons";
import { IconButton } from "../ds/IconButton/IconButton";
import { BrandLogo } from "../BrandLogo";
import { PinDock, PinDropZone } from "../PinnedDock";
import { Suspense } from "react";
import { CATS, EmptyState } from "../EmptyCat";
import { FinishSetupChip } from "../OnboardingChrome";
import { chipStep, type OnboardingStep } from "../onboardingFlow";
import type { Bot, Conversation } from "../types";
import { type CoverId } from "../surfaces";
import { FirstRunHero } from "../chat/shared";
import type { TFunction } from "i18next";
import type { Dispatch, RefObject, SetStateAction } from "react";


export interface SidebarProps {
  /** The New menu, a chunk the workspace fetches once it has mounted. */
  CreateMenu?: typeof import("./CreateMenu").default;
  mobileList: boolean;
  modalPanelOpen: boolean;
  desktopExpanded: boolean;
  sidebarFolded: boolean;
  t: TFunction<readonly ["chat", "common"], undefined>;
  setSidebarFolded: (next: boolean | ((folded: boolean) => boolean)) => void;
  setSearchOpen: Dispatch<SetStateAction<boolean>>;
  createMenuRef: RefObject<HTMLDivElement | null>;
  createMenuTriggerRef: RefObject<HTMLButtonElement | null>;
  setCreateMenuOpen: Dispatch<SetStateAction<boolean>>;
  createMenuOpen: boolean;
  botImportRef: RefObject<HTMLInputElement | null>;
  importBotPackage: (file: File) => Promise<void>;
  showCreateMenu: string | null;
  newBotBusy: boolean;
  createNewBot: () => Promise<void>;
  openCoverPanel: (id: CoverId) => void;
  botImportBusy: boolean;
  newBotError: string;
  botImportError: string;
  sidebarListRef: RefObject<HTMLDivElement | null>;
  pinnedConversations: Conversation[];
  pinTile: (conversation: Conversation) => React.JSX.Element;
  customSidebarSections: Record<string, Conversation[]>;
  conversationRow: (conversation: Conversation, draggable?: boolean) => React.JSX.Element;
  defaultSidebarConversations: Conversation[];
  sidebarDndEnabled: boolean;
  sidebarLiveRef: RefObject<HTMLDivElement | null>;
  onboardingState: string;
  visibleSidebarConversations: Conversation[];
  bots: Bot[];
  conversations: Conversation[];
  modelReady: boolean;
  setOnboardingForced: Dispatch<SetStateAction<OnboardingStep | null>>;
  linkConnected: boolean;
  setPortabilityBotID: Dispatch<SetStateAction<string>>;
  setPortabilityFile: Dispatch<SetStateAction<File | undefined>>;
  openSettings: (tab: SettingsTab, view?: SettingsView) => void;
  setUsageBotId: Dispatch<SetStateAction<string>>;
  setArtifactsMode: Dispatch<SetStateAction<"artifacts" | "artifacts-bot" | "artifacts-group">>;
}

export function Sidebar({ CreateMenu, mobileList, modalPanelOpen, desktopExpanded, sidebarFolded, t, setSidebarFolded, setSearchOpen, createMenuRef, createMenuTriggerRef, setCreateMenuOpen, createMenuOpen, botImportRef, importBotPackage, showCreateMenu, newBotBusy, createNewBot, openCoverPanel, botImportBusy, newBotError, botImportError, sidebarListRef, pinnedConversations, pinTile, customSidebarSections, conversationRow, defaultSidebarConversations, sidebarDndEnabled, sidebarLiveRef, onboardingState, visibleSidebarConversations, bots, conversations, modelReady, setOnboardingForced, linkConnected, setPortabilityBotID, setPortabilityFile, openSettings, setUsageBotId, setArtifactsMode }: SidebarProps) {
  return (
    <aside className={`contact-pane ${mobileList ? "open" : ""}`} inert={modalPanelOpen || desktopExpanded || undefined}>
          <div className="sidebar-top">{sidebarFolded ? <button className="sidebar-logo-expand" type="button" aria-label={t("sidebar.expand")} data-hint={t("sidebar.expand")} onClick={() => setSidebarFolded(false)}><span className="sidebar-logo-art"><BrandLogo variant="calico" /></span><span className="sidebar-logo-arrow" aria-hidden="true"><Icon name="sidebar" size={20} /></span></button> : <BrandLogo variant="calico" />}<div className="sidebar-actions">{!sidebarFolded && <span className="sidebar-toggle-slot"><IconButton size="sm" className="sidebar-toggle" icon="sidebar" label={t("sidebar.collapse")} onClick={() => setSidebarFolded(true)} /></span>}<span className="search-slot"><IconButton size="sm" className="search-trigger" icon="search" label={t("search.open")} aria-haspopup="dialog" aria-keyshortcuts="Control+K Meta+K" onClick={() => setSearchOpen(true)} /></span><div className="create-menu-anchor" ref={createMenuRef}><IconButton size="sm" ref={createMenuTriggerRef} icon="plus" animated label={t("create.menu")} {...pressToggle(() => setCreateMenuOpen(open => !open))} onKeyDown={arrowOpens(setCreateMenuOpen)} aria-haspopup="menu" aria-expanded={createMenuOpen} aria-controls={createMenuOpen ? "create-menu" : undefined} /><input ref={botImportRef} type="file" hidden accept=".json,.tofi-bot,application/json" onChange={(event) => { const file = event.currentTarget.files?.[0]; event.currentTarget.value = ""; if (file) void importBotPackage(file); }} />{showCreateMenu && CreateMenu && <CreateMenu open={createMenuOpen} onOpenChange={setCreateMenuOpen} t={t} triggerRef={createMenuTriggerRef} anchorRef={createMenuRef} placement={sidebarFolded && !isDesktop && matchMedia("(min-width:768px)").matches ? { side: "right", sideOffset: 12 } : { align: "end", sideOffset: 8 }} busy={newBotBusy} importBusy={botImportBusy} error={newBotError || botImportError} importRef={botImportRef} createNewBot={createNewBot} openCoverPanel={openCoverPanel} />}</div></div></div>
          <div className="contact-section" ref={sidebarListRef}>
            <PinDropZone label={t("sidebar.drop_to_pin")} hint={t("sidebar.drop_to_pin_hint")}>{pinnedConversations.length > 0 ? <PinDock label={t("sidebar.pinned")}>{pinnedConversations.map(pinTile)}</PinDock> : null}</PinDropZone>
            {Object.entries(customSidebarSections).map(([name, items]) => <section className="sidebar-custom-section" key={name}><div className="tb-eyebrow sidebar-eyebrow">{name}</div>{items.map(item => conversationRow(item))}</section>)}
            <div className="tb-eyebrow sidebar-eyebrow">{t("sidebar.messages")}</div>{defaultSidebarConversations.map(item => conversationRow(item, sidebarDndEnabled))}
            <div className="sr-only" role="status" aria-live="polite" ref={sidebarLiveRef} />
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && !bots.length && !conversations.length && <Suspense fallback={null}><FirstRunHero side onCreate={() => void createNewBot()} busy={newBotBusy} error={newBotError} /></Suspense>}
            {onboardingState !== "sheet" && !visibleSidebarConversations.length && <EmptyState name="sidebar" className={`empty-copy is-compact${!bots.length && !conversations.length ? " first-run-hide-mobile" : ""}`} look={CATS.mochi} pose="asleep"><p>{t("sidebar.empty")}</p><button className="primary-button sidebar-empty-create" disabled={newBotBusy} onClick={() => void createNewBot()}>{newBotBusy ? t("create.creating") : t("create.create_bot")}</button></EmptyState>}
          </div>
          <div className="sidebar-footer">{onboardingState === "chip" && <FinishSetupChip compact={sidebarFolded} step={chipStep(modelReady)} onClick={() => setOnboardingForced(chipStep(modelReady))} />}<SidebarAccount compact={sidebarFolded} connected={linkConnected} onSettings={() => { setPortabilityBotID(""); setPortabilityFile(undefined); openSettings("general", "home"); }} onUsage={() => { setUsageBotId(""); openSettings("usage"); }} onConnection={() => openSettings("models")} onArtifacts={() => { setArtifactsMode("artifacts"); openCoverPanel("artifacts"); }} /></div>
        </aside>
  );
}
