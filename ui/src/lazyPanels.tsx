import { startTransition, useEffect, useState, useSyncExternalStore, type ComponentType, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { DesktopPlaceholder } from "./DesktopPlaceholder";

/**
 * Code-split panels. The workspace shell (sidebar, chat, composer) ships in the main chunk; everything a person
 * opens on purpose (Settings, the Bot desktop, onboarding) loads on its own. Each loader is warmed during idle
 * time after first paint and again when the pointer or focus reaches its entry point, so the click that opens a
 * panel finds the code already there and only has the skeleton to show on a cold, slow network.
 */
const loaders = {
  settings: () => import("./settings/SettingsPanel"),
  // The desktop window shell rides with the panel it frames: both open on the same click.
  desktop: () => Promise.all([import("./BotDesktopPanel"), import("./FloatingDesktop")]).then(([panel, shell]) => ({ ...panel, ...shell })),
  onboarding: () => import("./Onboarding"),
  work: () => import("./WorkPanel"),
  memory: () => import("./MemoryPanel"),
  team: () => import("./TeamBoard"),
  archive: () => import("./ArchivePanel"),
  artifacts: () => import("./ArtifactsPanel"),
  viewOnly: () => import("./ViewOnlyChat"),
  remembered: () => import("./RememberedApprovals"),
  deleteDialog: () => import("./DeleteConversationDialog"),
  reassignPrimary: () => import("./ReassignPrimaryDialog"),
  permissionCoach: () => import("./PermissionCoach"),
  updateBanner: () => import("./UpdateNotice"),
  contextPanels: () => import("./ContextPanels"),
  outsidePanel: () => import("./OutsidePanel"),
  sidePanel: () => import("./SidePanel"),
  greetingSetup: () => import("./GreetingSetup"),
  questionCard: () => import("./QuestionCard"),
  runContext: () => import("./RunContext"),
  stopMenu: () => import("./ComposerStopMenu"),
  taskCard: () => import("./TaskCard"),
  taskActivity: () => import("./TaskActivityBody"),
  toolSteps: () => import("./WebToolSteps"),
} as const;
export type PanelChunk = keyof typeof loaders;

type Loaded<K extends PanelChunk> = Awaited<ReturnType<(typeof loaders)[K]>>;
const loaded = new Map<PanelChunk, unknown>();
const pending = new Map<PanelChunk, Promise<void>>();
const failedAt = new Map<PanelChunk, number>();
const RETRY_AFTER_MS = 3000;
const listeners = new Set<() => void>();

function load(name: PanelChunk): Promise<void> {
  let promise = pending.get(name);
  if (!promise) {
    promise = loaders[name]().then(
      module => { loaded.set(name, module); failedAt.delete(name); for (const listener of listeners) listener(); },
      () => { failedAt.set(name, Date.now()); pending.delete(name); },
    );
    pending.set(name, promise);
  }
  return promise;
}

/**
 * Like React.lazy, but a chunk that is already in memory renders on the same commit. React.lazy always suspends once
 * per component, which would paint the skeleton for a frame even when the idle warm-up had long finished.
 * A failed download renders nothing and retries on the next mount instead of unmounting the app.
 */
function chunk<K extends PanelChunk, P extends object>(name: K, pick: (module: Loaded<K>) => ComponentType<P>): ComponentType<P> {
  return function PanelChunkBoundary(props: P) {
    if (!loaded.has(name)) {
      const failed = failedAt.get(name);
      if (failed !== undefined && Date.now() - failed < RETRY_AFTER_MS) return null;
      throw load(name);
    }
    const Component = pick(loaded.get(name) as Loaded<K>);
    return <Component {...props} />;
  };
}

export const SettingsPanel = chunk("settings", module => module.SettingsPanel);
export const BotDesktopPanel = chunk("desktop", module => module.BotDesktopPanel);
export const FloatingDesktop = chunk("desktop", module => module.FloatingDesktop);
export const WorkPanel = chunk("work", module => module.WorkPanel);
export const MemoryPanel = chunk("memory", module => module.MemoryPanel);
export const TeamBoard = chunk("team", module => module.TeamBoard);
export const ArchivePanel = chunk("archive", module => module.ArchivePanel);
export const ArtifactsPanel = chunk("artifacts", module => module.ArtifactsPanel);
export const ViewOnlyChat = chunk("viewOnly", module => module.ViewOnlyChat);
export const RememberedApprovalsSheet = chunk("remembered", module => module.RememberedApprovalsSheet);
export const DeleteConversationDialog = chunk("deleteDialog", module => module.DeleteConversationDialog);
export const ReassignPrimaryDialog = chunk("reassignPrimary", module => module.ReassignPrimaryDialog);
export const BotPanel = chunk("contextPanels", module => module.BotPanel);
export const MembersPanel = chunk("contextPanels", module => module.MembersPanel);
export const OutsideHeader = chunk("outsidePanel", module => module.OutsideHeader);
export const SidePanel = chunk("sidePanel", module => module.SidePanel);
export const GreetingSetup = chunk("greetingSetup", module => module.GreetingSetup);
export const PermissionCoach = chunk("permissionCoach", module => module.PermissionCoach);
export const UpdateBanner = chunk("updateBanner", module => module.UpdateBanner);
export const QuestionCard = chunk("questionCard", module => module.QuestionCard);
export const RunContextView = chunk("runContext", module => module.RunContextView);
export const TaskCard = chunk("taskCard", module => module.TaskCard);
export const ComposerStopMenu = chunk("stopMenu", module => module.ComposerStopMenu);
/** True once a chunk is in memory (the Stop button opens its menu only then). */
export const panelLoaded = (name: PanelChunk) => loaded.has(name);
export const whenPanelLoaded = (name: PanelChunk) => load(name);
const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };
/** Re-renders its caller once the chunk lands (steps that must be in the DOM to be found wait on it). */
export const usePanelLoaded = (name: PanelChunk) => useSyncExternalStore(subscribe, () => loaded.has(name), () => loaded.has(name));

/**
 * For content that only shows after a person opens something: draws nothing until the chunk is in memory, with no
 * Suspense boundary (React 19 would hold a revealed boundary), and on the same commit once it is.
 */
function quiet<K extends PanelChunk, P extends object>(name: K, pick: (module: Loaded<K>) => ComponentType<P>): ComponentType<P> {
  return function QuietChunk(props: P) {
    const ready = usePanelLoaded(name);
    useEffect(() => { if (!ready) preloadPanel(name); }, [ready]);
    if (!ready) return null;
    const Component = pick(loaded.get(name) as Loaded<K>);
    return <Component {...props} />;
  };
}
export const TaskActivityBody = quiet("taskActivity", module => module.TaskActivityBody);
export const WebToolSteps = quiet("toolSteps", module => module.WebToolSteps);
export const Onboarding = chunk("onboarding", module => module.Onboarding);

export function preloadPanel(name: PanelChunk) {
  if (!loaded.has(name)) void load(name);
}

/** Entry points, by selector, that warm a chunk when the pointer or keyboard focus arrives. */
const entryPoints: Array<[selector: string, chunk: PanelChunk]> = [
  [".sidebar-footer", "settings"],
  ["[data-computer-toggle]", "desktop"],
  [".chat-identity", "contextPanels"],
  [".composer-stop", "stopMenu"],
  [".task-activity > summary", "taskActivity"],
  [".web-tool-toggle", "toolSteps"],
];

/**
 * Warm the panel chunks a moment after the shell has painted, one every WARM_STEP_MS so no single task grows
 * and the first interaction is never queued behind a download. Returns a cancel.
 */
const WARM_START_MS = 500;
const WARM_STEP_MS = 200;
export function warmPanelsWhenIdle(): () => void {
  const order: PanelChunk[] = ["settings", "taskActivity", "toolSteps", "desktop", "contextPanels", "outsidePanel", "sidePanel", "work", "memory", "team", "archive", "viewOnly", "remembered", "deleteDialog", "reassignPrimary", "permissionCoach", "updateBanner", "questionCard", "stopMenu"];
  let timer = window.setTimeout(function step() {
    const next = order.shift();
    if (!next) return;
    preloadPanel(next);
    timer = window.setTimeout(step, WARM_STEP_MS);
  }, WARM_START_MS);
  const near = (event: Event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    for (const [selector, chunk] of entryPoints) if (target.closest(selector)) preloadPanel(chunk);
  };
  document.addEventListener("pointerover", near, { passive: true });
  document.addEventListener("focusin", near);
  return () => {
    window.clearTimeout(timer);
    document.removeEventListener("pointerover", near);
    document.removeEventListener("focusin", near);
  };
}

/**
 * Shell first: paint `fallback` (the panel frame) on the commit that opens the panel, then mount the real content
 * as an interruptible transition after that frame is on screen. The content's mount cost (tens of ms for Settings)
 * then lands outside the click-to-first-frame path and is sliced instead of one long task.
 */
export function AfterPaint({ fallback, children }: { fallback: ReactNode; children: ReactNode }) {
  const [ready, setReady] = useState(false);
  useEffect(() => {
    let second = 0;
    const first = requestAnimationFrame(() => { second = requestAnimationFrame(() => startTransition(() => setReady(true))); });
    return () => { cancelAnimationFrame(first); cancelAnimationFrame(second); };
  }, []);
  return <>{ready ? children : fallback}</>;
}

/** Settings frame drawn before its code arrives. It carries .settings-tabs so the pane takes the modal geometry from the first frame. */
export function SettingsSkeleton({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("settings");
  return <div className="settings-shell settings-shell-skeleton" data-view="home" aria-busy="true">
    <nav className="settings-tabs" aria-label={t("shell.category")} />
    <div className="settings-mhome">
      <header className="settings-mbar"><span aria-hidden="true" /><h2>{t("shell.title")}</h2><button className="settings-dismiss" aria-label={t("shell.close")} onClick={onClose}><Icon name="close" size={20} /></button></header>
    </div>
    <div className="settings-content">
      <header className="settings-page-heading"><div className="settings-page-title"><h2>{t("shell.title")}</h2></div></header>
    </div>
  </div>;
}

/** Bot panel frame: heading and close, so the card is recognisable while its rows mount. */
export function BotPanelSkeleton({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("chat");
  return <div className="detail-content bot-v2-panel" aria-busy="true">
    <div className="detail-heading bot-v2-heading"><h2>{t("botPanel.title")}</h2><button className="close-button" aria-label={t("botPanel.close")} onClick={onClose}><Icon name="close" size={18} /></button></div>
  </div>;
}

export function DesktopSkeleton({ botId }: { botId: string }) {
  const { t } = useTranslation("computer");
  return <DesktopPlaceholder tone="connecting" status={t("desktop.placeholder.connecting")} botId={botId} />;
}
