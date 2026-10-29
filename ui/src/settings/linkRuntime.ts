import type { SettingsTab } from "../SettingsShell";
import type { SettingsLink } from "../settingsRoute";
import type { Bot, Conversation } from "../types";
import { openConnectionDeepLink, getLinkDeps } from "./deepLinks";
import { readSettingsHref, readSettingsLink } from "../settingsRoute";
import { setPendingSection } from "./pendingSection";

/** What opening a link needs from the app. */
export type OpenLinkDeps = {
  bots: Bot[];
  conversations: Conversation[];
  openSettings: (tab: SettingsTab) => void;
  selectConversation: (id: string) => void;
  setMobileList: (list: boolean) => void;
  openCover: (id: "bot-settings" | "group-settings") => void;
};

/**
 * Opens the page a settings link names. The link only says where to look: a Bot or group that is not in this account's index
 * opens nothing.
 */
export function openSettingsLink(link: SettingsLink, deps: OpenLinkDeps): void {
  switch (link.kind) {
    case "settings":
      setPendingSection(link.section ? { scope: "settings", page: link.tab, section: link.section } : null);
      deps.openSettings(link.tab);
      return;
    case "connection":
      openConnectionDeepLink({ server: link.name, tool: link.tool });
      return;
    case "bot-settings": {
      const bot = deps.bots.find(item => item.id === link.botId);
      if (!bot || !deps.conversations.some(item => item.id === bot.dm_conversation_id)) return;
      setPendingSection({ scope: "bot-settings", page: "", section: link.section });
      deps.selectConversation(bot.dm_conversation_id);
      deps.setMobileList(false);
      deps.openCover("bot-settings");
      return;
    }
    case "group-settings":
      if (!deps.conversations.some(item => item.id === link.groupId && item.kind === "group" && item.user_visible !== false)) return;
      setPendingSection({ scope: "group-settings", page: "", section: link.section });
      deps.selectConversation(link.groupId);
      deps.setMobileList(false);
      deps.openCover("group-settings");
  }
}

/** A Markdown link the entry took for a settings page: open it here, or in a new tab when it is not one. */
export function openHref(href: string): void {
  const link = readSettingsHref(href, window.location.origin);
  const deps = getLinkDeps();
  if (link && deps) openSettingsLink(link, deps);
  else if (!link) window.open(href, "_blank", "noopener,noreferrer");
}

/** An address that is a link into the settings (typed or reloaded): open the page it names, if there is one. */
export function openAddress(address: string): void {
  const url = new URL(address, window.location.origin);
  const link = readSettingsLink(url.pathname, url.hash, url.search);
  const deps = getLinkDeps();
  if (link && deps) openSettingsLink(link, deps);
}
