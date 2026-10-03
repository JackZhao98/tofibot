import { useCallback, useEffect, useRef, useState } from "react";
import { conversationPath, readConversationRoute, resolveConversationRoute } from "./conversationRoute";
import { desktopState } from "./desktop";
import type { Bot, Conversation } from "./types";

export function useConversationNavigation(bots: Bot[], conversations: Conversation[], ready: boolean) {
  const [route, setRoute] = useState(() => readConversationRoute(window.location.pathname));
  const [historyVersion, setHistoryVersion] = useState(0);
  const nativeFallback = useRef(desktopState().activeConversation);
  const index = useRef({ bots, conversations });
  index.current = { bots, conversations };
  const active = !ready ? undefined : route.kind === "home"
    ? conversations.find(conversation => conversation.id === nativeFallback.current && conversation.user_visible !== false)
      ?? conversations.find(conversation => !conversation.archived && conversation.user_visible !== false)
    : resolveConversationRoute(route, bots, conversations);

  const writePath = useCallback((path: string, replace = false) => {
    if (window.location.pathname !== path) {
      window.history[replace ? "replaceState" : "pushState"](window.history.state, "", path + window.location.search + window.location.hash);
    }
    setRoute(readConversationRoute(path));
  }, []);

  // Canonicalize the initial default selection without adding a history entry.
  // Explicit targets are never replaced by refreshes or native saved selection.
  useEffect(() => {
    if (route.kind !== "home" || !active) return;
    const path = conversationPath(active, bots);
    if (path) writePath(path, true);
  }, [route, active, bots, writePath]);

  useEffect(() => {
    const navigated = () => {
      setRoute(readConversationRoute(window.location.pathname));
      setHistoryVersion(version => version + 1);
    };
    window.addEventListener("popstate", navigated);
    return () => window.removeEventListener("popstate", navigated);
  }, []);

  const selectConversation = useCallback((id: string, created?: Conversation) => {
    const conversation = created ?? index.current.conversations.find(item => item.id === id);
    if (!conversation) return;
    const path = conversationPath(conversation, index.current.bots);
    if (path) writePath(path);
  }, [writePath]);

  return { activeId: active?.id ?? null, selectConversation, historyVersion, routeUnavailable: ready && route.kind !== "home" && !active };
}
