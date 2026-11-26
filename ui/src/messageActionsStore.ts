import { useSyncExternalStore } from "react";

/**
 * Which message currently shows its action bar. The bar is mounted only for that message, so a hover or a focus move
 * touches two small slots and never re-renders the conversation, and a long history carries no hidden toolbars.
 * Touch has no bar: a long press opens the same actions as a menu.
 */
type Source = "hover" | "focus";
const state: Record<Source, string> = { hover: "", focus: "" };
const listeners = new Set<() => void>();

export function setMessageActions(source: Source, id: string) {
  if (state[source] === id) return;
  state[source] = id;
  for (const listener of listeners) listener();
}

const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };

export function useMessageActionsOpen(id: string) {
  return useSyncExternalStore(subscribe, () => state.hover === id || state.focus === id);
}
