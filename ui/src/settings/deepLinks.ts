import type {SettingsTab} from "../SettingsShell";
import {ARCHIVE_ENABLED} from "../featureFlags";

/** Opens Settings > Computer (a file whose bytes are on a detached computer points here). */
export const OPEN_COMPUTER_SETTINGS_EVENT = "tofi:open-computer-settings";

/** Window events other surfaces dispatch to open Settings on a tab (chat cards, the model picker). */
export const settingsDeepLinks: Record<string, SettingsTab> = {
  "tofi:open-tool-settings": "connections",
  "tofi:open-codex-settings": "models",
  "tofi:open-connection": "connections",
  "tofi:open-permissions": "permissions",
  [OPEN_COMPUTER_SETTINGS_EVENT]: "computer",
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
export const legacySettingsTabs: Record<string, SettingsTab> = { approvals: "permissions" };

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
