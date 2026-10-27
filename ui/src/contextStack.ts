/**
 * State of the on-demand card column (docs/design/side-panels.md).
 * `cards` are the open cards, newest first; the column exists only while there is at least one. `collapsed` cards
 * show only their header. `wide` is the one card (at most) moved into the full-height outside panel at the window's
 * right edge; the other cards stay open and come back when it goes back. Below 1100 px (`column` false) there is
 * one card at a time: the drawer or sheet.
 */
export type PanelState<P extends string> = { cards: P[]; collapsed: string[]; wide: string | null; focus: { id: string | null; n: number } };

export const emptyPanelState = <P extends string>(): PanelState<P> => ({ cards: [], collapsed: [], wide: null, focus: { id: null, n: 0 } });

/** The panel the user touched last: the newest card. */
export const currentPanel = <P extends string>(state: PanelState<P>): P | null => state.cards[0] ?? null;

/** Opens (or refocuses and expands) `next`. A card that is already open keeps its place; a new one goes on top. Settings is a window of its own and replaces everything. */
export function openPanel<P extends string>(state: PanelState<P>, next: P | null, column: boolean): PanelState<P> {
  if (next === null) return state.cards.length ? emptyPanelState<P>() : state;
  const focus = { id: next, n: state.focus.n + 1 };
  if (next === "settings" || !column) return { cards: [next], collapsed: [], wide: null, focus };
  const open = state.cards.filter(id => id !== "settings");
  return { cards: open.includes(next) ? open : [next, ...open], collapsed: state.collapsed.filter(id => id !== next), wide: state.wide === next ? next : null, focus };
}

/** Closes one card. The outside panel goes with it when that card was in it. */
export const closePanel = <P extends string>(state: PanelState<P>, id: string): PanelState<P> =>
  state.cards.includes(id as P) ? { ...state, cards: state.cards.filter(item => item !== id), collapsed: state.collapsed.filter(item => item !== id), wide: state.wide === id ? null : state.wide } : state;

export const toggleCollapsed = <P extends string>(state: PanelState<P>, id: string): PanelState<P> => ({ ...state, collapsed: state.collapsed.includes(id) ? state.collapsed.filter(item => item !== id) : [...state.collapsed, id] });

/** Moves a card into the outside panel (or back to the column when it is already there). */
export const toggleWide = <P extends string>(state: PanelState<P>, id: string): PanelState<P> =>
  state.cards.includes(id as P) ? { ...state, wide: state.wide === id ? null : id, collapsed: state.collapsed.filter(item => item !== id), focus: { id, n: state.focus.n + 1 } } : state;
