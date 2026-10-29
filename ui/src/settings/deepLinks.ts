import type {SettingsTab} from "../SettingsShell";
import {ARCHIVE_ENABLED} from "../featureFlags";
import {LEGACY_SETTINGS_TABS} from "./sections";
import type {SettingsLink} from "../settingsRoute";

/** Window events other surfaces dispatch to open Settings on a tab (chat cards, the model picker). */
export const settingsDeepLinks: Record<string, SettingsTab> = {
  "tofi:open-tool-settings": "connections",
  "tofi:open-codex-settings": "models",
  "tofi:open-connection": "connections",
  "tofi:open-permissions": "permissions",
};

/**
 * `tofi:open-connection` opens Settings on one connection's page and, with `tool`, scrolls to that tool's row and flashes it.
 * The target waits here until the Connections page is mounted (it may not be yet), then `takeConnectionTarget` hands it over once.
 */
export const OPEN_CONNECTION_EVENT = "tofi:open-connection";
export type ConnectionTarget = { server: string; tool?: string };
let pendingTarget: ConnectionTarget | null = null;
export const openConnectionDeepLink = (target: ConnectionTarget) => { pendingTarget = target; window.dispatchEvent(new CustomEvent(OPEN_CONNECTION_EVENT, { detail: target })); };
export const takeConnectionTarget = (): ConnectionTarget | null => { const target = pendingTarget; pendingTarget = null; return target; };
export const openPermissionsDeepLink = () => window.dispatchEvent(new Event("tofi:open-permissions"));

/** Settings tab ids that were renamed: the old id still opens the page that replaced it. */
export const legacySettingsTabs: Record<string, SettingsTab> = LEGACY_SETTINGS_TABS;

/** Listens for every deep-link event and calls `open` with its tab. Returns the unsubscribe. */
export function subscribeSettingsDeepLinks(open: (tab: SettingsTab) => void): () => void {
  const handlers = Object.entries(settingsDeepLinks).map(([name, tab]) => {
    const handler = () => open(tab);
    window.addEventListener(name, handler);
    return [name, handler] as const;
  });
  return () => handlers.forEach(([name, handler]) => window.removeEventListener(name, handler));
}

/**
 * `tofi:open-link` opens a link into the app's settings (a Markdown link in a message, an approval card's Manage link, an address
 * typed or reloaded). The app listens once and opens the right cover; the section to scroll to waits here until the page that owns
 * it is mounted, then `takeSettingsSection` hands it over once (the `takeConnectionTarget` pattern).
 */
export const OPEN_LINK_EVENT = "tofi:open-link";
export type SectionScope = "settings" | "bot-settings" | "group-settings";
export type PendingSection = { scope: SectionScope; page: string; section: string };
let pendingSection: PendingSection | null = null;
export const setPendingSection = (target: PendingSection | null) => { pendingSection = target; };
/** Returns the pending section for this scope (and page, when given) once; a section meant for another page stays. */
export const takeSettingsSection = (scope: SectionScope, page?: string): string | null => {
  if (!pendingSection || pendingSection.scope !== scope || (page !== undefined && pendingSection.page !== page)) return null;
  const { section } = pendingSection;
  pendingSection = null;
  return section;
};
export const openDeepLink = (link: SettingsLink) => window.dispatchEvent(new CustomEvent(OPEN_LINK_EVENT, { detail: link }));

/** Listens for `tofi:open-link`. Returns the unsubscribe. */
export function subscribeDeepLink(open: (link: SettingsLink) => void): () => void {
  const handler = (event: Event) => { const link = (event as CustomEvent<SettingsLink | undefined>).detail; if (link && typeof link === "object" && "kind" in link) open(link); };
  window.addEventListener(OPEN_LINK_EVENT, handler);
  return () => window.removeEventListener(OPEN_LINK_EVENT, handler);
}

/**
 * `tofi:open-sidebar` opens one sidebar view in the workspace (Tasks, Memory, Terminal or Archive), the way the `?side=` address does.
 * Anything that can reach `window` can dispatch it: `window.dispatchEvent(new CustomEvent("tofi:open-sidebar", { detail: { id: "tasks" } }))`.
 */
export const OPEN_SIDEBAR_EVENT = "tofi:open-sidebar";
export type SidebarDeepLink = "tasks" | "memory" | "terminal" | "archive";

export const openSidebarDeepLink = (id: SidebarDeepLink) => window.dispatchEvent(new CustomEvent(OPEN_SIDEBAR_EVENT, { detail: { id } }));

/** Listens for `tofi:open-sidebar` and calls `open` with a known id. Returns the unsubscribe. */
export function subscribeSidebarDeepLinks(open: (id: SidebarDeepLink) => void): () => void {
  const handler = (event: Event) => {
    const id = (event as CustomEvent<{ id?: string }>).detail?.id;
    if (id === "tasks" || id === "memory" || id === "terminal" || (id === "archive" && ARCHIVE_ENABLED)) open(id);
  };
  window.addEventListener(OPEN_SIDEBAR_EVENT, handler);
  return () => window.removeEventListener(OPEN_SIDEBAR_EVENT, handler);
}
