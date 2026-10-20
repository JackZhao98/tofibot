import { useSyncExternalStore } from "react";

/**
 * Which message currently shows its action bar. The bar is mounted only for that message, so a hover, a focus move or a tap
 * touches two small slots and never re-renders the conversation, and a long history carries no hidden toolbars.
 */
type Source = "hover" | "focus" | "tap";
const state: Record<Source, string> = { hover: "", focus: "", tap: "" };
const listeners = new Set<() => void>();

export function setMessageActions(source: Source, id: string) {
  if (state[source] === id) return;
  state[source] = id;
  for (const listener of listeners) listener();
}
export const tappedMessageActionsId = () => state.tap;
export function clearMessageActions() {
  if (!state.hover && !state.focus && !state.tap) return;
  state.hover = state.focus = state.tap = "";
  for (const listener of listeners) listener();
}

const subscribe = (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener); }; };

export function useMessageActionsOpen(id: string) {
  return useSyncExternalStore(subscribe, () => state.hover === id || state.focus === id || state.tap === id);
}
