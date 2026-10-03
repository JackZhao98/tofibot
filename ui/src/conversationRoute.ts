import type { Bot, Conversation } from "./types";

export type ConversationRoute = { kind: "home" } | { kind: "bot" | "group"; id: string } | { kind: "unavailable" };

export function readConversationRoute(pathname: string): ConversationRoute {
  if (pathname === "/" || pathname === "/index.html") return { kind: "home" };
  const match = /^\/(b|g)\/([^/]+)\/?$/.exec(pathname);
  if (!match) return { kind: "unavailable" };
  try {
    const id = decodeURIComponent(match[2]);
    if (!id || id.length > 200 || /[\x00-\x20/\\]/.test(id)) return { kind: "unavailable" };
    return { kind: match[1] === "b" ? "bot" : "group", id };
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
