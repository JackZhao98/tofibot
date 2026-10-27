/**
 * Right-hand context column state (docs/design/context-column.md).
 * `panel` is the one the user touched last. `stack` is the set of cards that can sit open together
 * (the computer and the Bot settings), newest first, and only grows on wide web windows (`column`).
 * Every other panel stays exclusive: it replaces the previous exclusive panel but leaves the stack alone.
 */
export type StackId = "desktop" | "bot-edit";
export type PanelState<P extends string> = { panel: P | null; stack: StackId[]; collapsed: string[]; focus: { id: string | null; n: number } };
export const isStackable = (id: string | null): id is StackId => id === "desktop" || id === "bot-edit";

export const emptyPanelState = <P extends string>(): PanelState<P> => ({ panel: null, stack: [], collapsed: [], focus: { id: null, n: 0 } });

/** Opens (or refocuses and expands) `next`. A stacked card that is already open keeps its place. */
export function openPanel<P extends string>(state: PanelState<P>, next: P | null, column: boolean): PanelState<P> {
  if (next === null) return state.panel === null && !state.stack.length ? state : emptyPanelState<P>();
  const stack: StackId[] = next === "settings" || !column ? (isStackable(next) ? [next] : []) : isStackable(next) && !state.stack.includes(next) ? [next, ...state.stack] : state.stack;
  return { panel: next, stack, collapsed: state.collapsed.filter(id => id !== next), focus: { id: next, n: state.focus.n + 1 } };
}

/** Closes one card; the next card down becomes current. */
export function closePanel<P extends string>(state: PanelState<P>, id: string): PanelState<P> {
  const stack = state.stack.filter(item => item !== id);
  const panel = state.panel === id ? ((stack[0] as P | undefined) ?? null) : state.panel;
  return { ...state, panel, stack, collapsed: state.collapsed.filter(item => item !== id) };
}

export const toggleCollapsed = <P extends string>(state: PanelState<P>, id: string): PanelState<P> => ({ ...state, collapsed: state.collapsed.includes(id) ? state.collapsed.filter(item => item !== id) : [...state.collapsed, id] });
