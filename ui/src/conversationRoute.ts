import type { Bot, Conversation } from "./types";

/** `settings` is the address of a link into the settings (`/settings/models#defaults`, `/b/<id>/settings#model`). The lazy link runtime resolves it: the cover opens once and the address goes back to the conversation. */
export type ConversationRoute = { kind: "home"; settings?: string } | { kind: "bot" | "group"; id: string; settings?: string } | { kind: "unavailable" };

export function readConversationRoute(pathname: string, hash = "", search = ""): ConversationRoute {
  if (pathname === "/" || pathname === "/index.html") return { kind: "home" };
  if (/^\/settings(\/|$)/.test(pathname)) return { kind: "home", settings: pathname + search + hash };
  const scoped = /^\/(b|g)\/([^/]+)\/settings\/?$/.exec(pathname);
  const match = scoped ?? /^\/(b|g)\/([^/]+)\/?$/.exec(pathname);
  if (!match) return { kind: "unavailable" };
  try {
    const id = decodeURIComponent(match[2]);
    if (!id || id.length > 200 || /[\x00-\x20/\\]/.test(id)) return { kind: "unavailable" };
    return { kind: match[1] === "b" ? "bot" : "group", id, ...(scoped ? { settings: pathname + search + hash } : {}) };
  } catch {
    return { kind: "unavailable" };
  }
}

export function conversationPath(conversation: Conversation, bots: Bot[]): string | null {
  if (conversation.user_visible === false) return null;
  if (conversation.kind === "group") return `/g/${encodeURIComponent(conversation.id)}`;
  const botID = conversation.bot_id ?? bots.find(bot => bot.dm_conversation_id === conversation.id)?.id;
  return botID ? `/b/${encodeURIComponent(botID)}` : null;
}

// Resolve only from the authenticated index. A URL never authorizes fetching
// another account's conversation, or opening an internal Bot-to-Bot chat.
export function resolveConversationRoute(route: ConversationRoute, bots: Bot[], conversations: Conversation[]): Conversation | undefined {
  if (route.kind === "group") return conversations.find(conversation => conversation.id === route.id && conversation.kind === "group" && conversation.user_visible !== false);
  if (route.kind !== "bot") return undefined;
  const bot = bots.find(bot => bot.id === route.id);
  return bot && conversations.find(conversation => conversation.id === bot.dm_conversation_id && conversation.kind === "dm" && conversation.user_visible !== false && (!conversation.bot_id || conversation.bot_id === bot.id));
}

/**
 * The one piece of surface state that lives in the address: which sidebar view is open (`?side=tasks`). A sidebar is a working view
 * worth sharing or reloading into; widgets are "right now" and covers are edit state, so neither is written. `conversation` (the
 * read-only chat of another conversation) needs a target the address cannot carry, so it is not in the list either.
 */
export const SIDE_PARAMS = ["tasks", "memory", "terminal", "archive"] as const;
export type SideParam = (typeof SIDE_PARAMS)[number];

export function readSideParam(search: string): SideParam | null {
  const value = new URLSearchParams(search).get("side");
  return SIDE_PARAMS.find(id => id === value) ?? null;
}

/** The query string for `side` (null removes it), every other parameter kept in place. */
export function withSideParam(search: string, side: SideParam | null): string {
  const params = new URLSearchParams(search);
  if (side) params.set("side", side);
  else params.delete("side");
  const text = params.toString();
  return text ? `?${text}` : "";
}
