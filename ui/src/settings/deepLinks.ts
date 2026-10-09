import type {SettingsTab} from "../SettingsShell";

/** Window events other surfaces dispatch to open Settings on a tab (chat cards, the model picker). */
export const settingsDeepLinks: Record<string, SettingsTab> = {
  "tofi:open-tool-settings": "connections",
  "tofi:open-codex-settings": "models",
};

/** Listens for every deep-link event and calls `open` with its tab. Returns the unsubscribe. */
export function subscribeSettingsDeepLinks(open: (tab: SettingsTab) => void): () => void {
  const handlers = Object.entries(settingsDeepLinks).map(([name, tab]) => {
    const handler = () => open(tab);
    window.addEventListener(name, handler);
    return [name, handler] as const;
  });
  return () => handlers.forEach(([name, handler]) => window.removeEventListener(name, handler));
}
