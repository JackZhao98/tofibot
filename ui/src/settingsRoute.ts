import {
  BOT_SETTINGS_SECTIONS, CONNECTION_SECTIONS, GROUP_SETTINGS_SECTIONS, LEGACY_SETTINGS_TABS, SETTINGS_SECTIONS,
  type BotSettingsSection, type GroupSettingsSection, type SettingsTabId,
} from "./settings/sections";

/**
 * Where a link into the app's settings points. A link only says where to look: it never authorizes anything, and an id that is
 * not in the signed-in account's index opens nothing (the same rule as `resolveConversationRoute`).
 */
export type SettingsLink =
  | { kind: "settings"; tab: SettingsTabId; section?: string }
  | { kind: "connection"; name: string; section?: string; tool?: string }
  | { kind: "bot-settings"; botId: string; section: BotSettingsSection }
  | { kind: "group-settings"; groupId: string; section: GroupSettingsSection };

const safeId = (value: string): string | null => {
  try {
    const id = decodeURIComponent(value);
    return id && id.length <= 200 && !/[\x00-\x20/\\]/.test(id) ? id : null;
  } catch { return null; }
};
const sectionFrom = (hash: string, known: readonly string[]): string | undefined => {
  const id = hash.replace(/^#/, "");
  return known.includes(id) ? id : undefined;
};

/** Reads `/settings/<tab>[#section]`, `/settings/connections/<name>[?tool=][#section]`, `/b/<id>/settings#section` and `/g/<id>/settings#section`. */
export function readSettingsLink(pathname: string, hash = "", search = ""): SettingsLink | null {
  const connection = /^\/settings\/connections\/([^/]+)\/?$/.exec(pathname);
  if (connection) {
    const name = safeId(connection[1]);
    if (!name) return null;
    const tool = new URLSearchParams(search).get("tool") ?? undefined;
    const section = sectionFrom(hash, CONNECTION_SECTIONS);
    return { kind: "connection", name, ...(section ? { section } : {}), ...(tool && tool.length <= 200 && !/[\x00-\x1f]/.test(tool) ? { tool } : {}) };
  }
  const tabMatch = /^\/settings\/([a-z]+)\/?$/.exec(pathname);
  if (tabMatch) {
    const id = LEGACY_SETTINGS_TABS[tabMatch[1]] ?? tabMatch[1];
    if (!Object.prototype.hasOwnProperty.call(SETTINGS_SECTIONS, id)) return null;
    const tab = id as SettingsTabId;
    const section = sectionFrom(hash, SETTINGS_SECTIONS[tab]);
    return { kind: "settings", tab, ...(section ? { section } : {}) };
  }
  const bot = /^\/b\/([^/]+)\/settings\/?$/.exec(pathname);
  if (bot) {
    const botId = safeId(bot[1]);
    return botId ? { kind: "bot-settings", botId, section: (sectionFrom(hash, BOT_SETTINGS_SECTIONS) ?? "identity") as BotSettingsSection } : null;
  }
  const group = /^\/g\/([^/]+)\/settings\/?$/.exec(pathname);
  if (group) {
    const groupId = safeId(group[1]);
    return groupId ? { kind: "group-settings", groupId, section: (sectionFrom(hash, GROUP_SETTINGS_SECTIONS) ?? "general") as GroupSettingsSection } : null;
  }
  return null;
}

/** The relative path for a link; `readSettingsLink` reads it back. */
export function settingsLinkPath(link: SettingsLink): string {
  switch (link.kind) {
    case "settings": return `/settings/${link.tab}${link.section ? `#${link.section}` : ""}`;
    case "connection": return `/settings/connections/${encodeURIComponent(link.name)}${link.tool ? `?tool=${encodeURIComponent(link.tool)}` : ""}${link.section ? `#${link.section}` : ""}`;
    case "bot-settings": return `/b/${encodeURIComponent(link.botId)}/settings#${link.section}`;
    case "group-settings": return `/g/${encodeURIComponent(link.groupId)}/settings#${link.section}`;
  }
}

/**
 * For a Markdown link: the settings link behind `href` when it is a relative path or an address on this origin. Anything else
 * (another origin, another scheme) is not ours and opens as a normal link.
 */
export function readSettingsHref(href: string | undefined, origin: string): SettingsLink | null {
  if (!href || href.length > 600) return null;
  let url: URL;
  try { url = new URL(href, origin); } catch { return null; }
  if (url.origin !== origin) return null;
  return readSettingsLink(url.pathname, url.hash, url.search);
}
