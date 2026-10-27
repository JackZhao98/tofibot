/**
 * The three tiers of what a conversation can have open (docs/design/widgets-and-sidebar.md).
 *
 * - widgets: small live blocks inside the chat, to the right of the messages. One of each, in the person's own order (default WIDGET_ORDER), any may be folded.
 * - sidebar: one heavy working view in a real grid track at the window edge. A second one replaces the first.
 * - cover: an exclusive full-window panel (settings and the like). While it is open the widgets and the sidebar are
 *   hidden, not closed: closing the cover brings them back as they were.
 *
 * Everything here is pure: the workspace holds one `Surfaces` value and calls these to get the next one.
 */
export type WidgetId = "computer" | "members" | "artifacts" | "usage";
export type SidebarId = "tasks" | "memory" | "terminal" | "archive" | "conversation";
export type CoverId = "settings" | "bot-settings" | "group-settings" | "new-group" | "approvals" | "artifacts";

/**
 * The default order, top to bottom. It never depends on which widget was opened first. It is also the stable order the column draws its
 * nodes in (moving a live video in the DOM would stop it); where a widget sits on screen is `Surfaces.order`, the person's own choice.
 */
export const WIDGET_ORDER: readonly WidgetId[] = ["computer", "members", "artifacts", "usage"];

/** A saved order read back from storage: unknown ids and repeats are dropped, a widget the saved list lacks goes last in the default order. */
export function normalizeWidgetOrder(raw: unknown): WidgetId[] {
  const list = Array.isArray(raw) ? raw.filter((id): id is WidgetId => typeof id === "string" && (WIDGET_ORDER as readonly string[]).includes(id)) : [];
  const kept = list.filter((id, index) => list.indexOf(id) === index);
  return [...kept, ...WIDGET_ORDER.filter(id => !kept.includes(id))];
}

/** `ids` laid out in `order`. */
export const sortWidgets = (ids: readonly WidgetId[], order: readonly WidgetId[]): WidgetId[] => order.filter(id => ids.includes(id));

export type Surfaces = {
  /** Open widgets, always in `order`. */
  widgets: WidgetId[];
  /** Every widget id in the person's chosen order (a per-viewer preference, the same in every conversation). Closed widgets keep their slot. */
  order: WidgetId[];
  /** The open widgets that show only their header. */
  folded: WidgetId[];
  sidebar: { id: SidebarId; expanded: boolean } | null;
  cover: CoverId | null;
  /** Opening something that is already open bumps `n`: the surface scrolls into view and takes focus. */
  focus: { id: string | null; n: number };
};

export const emptySurfaces = (): Surfaces => ({ widgets: [], order: [...WIDGET_ORDER], folded: [], sidebar: null, cover: null, focus: { id: null, n: 0 } });

/** True when nothing is open at all. */
export const isEmpty = (s: Surfaces) => !s.widgets.length && !s.sidebar && !s.cover;

const inOrder = (ids: readonly WidgetId[], s: Surfaces) => sortWidgets(ids, s.order);
const bump = (s: Surfaces, id: string): Surfaces["focus"] => ({ id, n: s.focus.n + 1 });

/** Opens (or unfolds and refocuses) a widget. A cover that was open closes: the person asked for something in the chat. */
export const openWidget = (s: Surfaces, id: WidgetId): Surfaces => ({
  ...s, widgets: inOrder([...s.widgets, id], s), folded: s.folded.filter(item => item !== id), cover: null, focus: bump(s, id),
});

/** A widget that opens by itself (the first file, the Bot taking the computer): no focus move, and an open cover stays. */
export const autoOpenWidget = (s: Surfaces, id: WidgetId): Surfaces => s.widgets.includes(id) ? s : { ...s, widgets: inOrder([...s.widgets, id], s) };

export const closeWidget = (s: Surfaces, id: WidgetId): Surfaces =>
  s.widgets.includes(id) ? { ...s, widgets: s.widgets.filter(item => item !== id), folded: s.folded.filter(item => item !== id) } : s;

export const toggleWidget = (s: Surfaces, id: WidgetId): Surfaces => s.widgets.includes(id) ? closeWidget(s, id) : openWidget(s, id);

/** Folds an open widget to its header, or unfolds it. */
export const foldWidget = (s: Surfaces, id: WidgetId): Surfaces =>
  s.widgets.includes(id) ? { ...s, folded: s.folded.includes(id) ? s.folded.filter(item => item !== id) : [...s.folded, id] } : s;

/** Folds or unfolds a widget without touching anything else (the automatic folds: the idle computer, the space rule). */
export const setWidgetFolded = (s: Surfaces, id: WidgetId, folded: boolean): Surfaces =>
  !s.widgets.includes(id) || s.folded.includes(id) === folded ? s : { ...s, folded: folded ? [...s.folded, id] : s.folded.filter(item => item !== id) };

/** Opens a sidebar view. Another one already open is replaced and keeps the track's width (expanded or not). */
export const openSidebar = (s: Surfaces, id: SidebarId): Surfaces => ({
  ...s, sidebar: { id, expanded: s.sidebar?.expanded ?? false }, cover: null, focus: bump(s, id),
});

export const closeSidebar = (s: Surfaces): Surfaces => s.sidebar ? { ...s, sidebar: null } : s;

export const toggleSidebar = (s: Surfaces, id: SidebarId): Surfaces => s.sidebar?.id === id ? closeSidebar(s) : openSidebar(s, id);

export const toggleSidebarExpanded = (s: Surfaces): Surfaces => s.sidebar ? { ...s, sidebar: { ...s.sidebar, expanded: !s.sidebar.expanded } } : s;

/** Opens a cover. Covers do not stack: another one is replaced. Widgets and sidebar stay as they are, hidden. */
export const openCover = (s: Surfaces, id: CoverId): Surfaces => ({ ...s, cover: id, focus: bump(s, id) });

export const closeCover = (s: Surfaces): Surfaces => s.cover ? { ...s, cover: null } : s;

export const closeAll = (s: Surfaces): Surfaces => isEmpty(s) ? s : { ...emptySurfaces(), order: s.order, focus: s.focus };

/**
 * Moves an open widget to place `to` (0 = top) among the open ones. The slots of closed widgets stay where they were, so a widget that
 * opens later still lands where the person put it relative to the others. Unchanged when the widget is closed or already there.
 */
export function moveWidget(s: Surfaces, id: WidgetId, to: number): Surfaces {
  const open = s.widgets;
  const from = open.indexOf(id);
  if (from < 0) return s;
  const target = Math.max(0, Math.min(open.length - 1, Math.trunc(to)));
  if (target === from) return s;
  const next = [...open];
  next.splice(from, 1);
  next.splice(target, 0, id);
  const slots = s.order.flatMap((item, index) => open.includes(item) ? [index] : []);
  const order = [...s.order];
  slots.forEach((slot, index) => { order[slot] = next[index]; });
  return { ...s, order, widgets: next };
}

/**
 * The widget the space rule folds next on a short window with three or more open: from the bottom of the person's order upwards,
 * never the computer, never one they unfolded themselves. The first fold is unconditional; each further one waits for the column to
 * overflow, so the person keeps as much open as fits.
 */
export function spaceFoldTarget(s: Surfaces, o: { skip?: ReadonlySet<WidgetId>; overflow: boolean }): WidgetId | null {
  if (s.widgets.length < 3) return null;
  const candidates = s.widgets.filter(id => id !== "computer");
  const next = [...candidates].reverse().find(id => !s.folded.includes(id) && !o.skip?.has(id));
  if (!next) return null;
  return candidates.some(id => s.folded.includes(id)) && !o.overflow ? null : next;
}

/** What one Escape closes (section 7 of the design, after native dialogs and Radix layers, which claim the key themselves). */
export type EscapeTarget =
  | { layer: "computer-big" }
  | { layer: "cover"; id: CoverId }
  | { layer: "sidebar"; id: SidebarId }
  | { layer: "widget"; id: WidgetId };

/**
 * The order of `useSurfaceEscape`: the big computer window steps back to compact, then the cover, then the sidebar, then the widgets
 * from the bottom up (Artifacts, Members, and the computer last: it is the live one).
 */
export function escapeTarget(s: Surfaces, o: { bigComputer?: boolean } = {}): EscapeTarget | null {
  if (o.bigComputer && s.widgets.includes("computer")) return { layer: "computer-big" };
  if (s.cover) return { layer: "cover", id: s.cover };
  if (s.sidebar) return { layer: "sidebar", id: s.sidebar.id };
  // From the bottom of the person's order up; the computer is the live one, so it goes last wherever it sits.
  const last = [...s.widgets].reverse().find(id => id !== "computer") ?? s.widgets[0];
  return last ? { layer: "widget", id: last } : null;
}

/**
 * What a conversation remembers of its widgets while another one is on screen (in memory only: a reload keeps just the sidebar,
 * which lives in the address). `suppressedRuns` are the computer runs the person closed the widget in, so they never bring it back.
 */
export type WidgetMemo = { widgets: WidgetId[]; folded: WidgetId[]; suppressedRuns: Set<string> };
export type WidgetsByConversation = Map<string, WidgetMemo>;

/** Writes down the widgets of the conversation that is being left. A record with nothing in it is dropped. */
export function stashWidgets(memory: WidgetsByConversation, id: string, s: Surfaces): void {
  const suppressedRuns = memory.get(id)?.suppressedRuns ?? new Set<string>();
  if (!s.widgets.length && !suppressedRuns.size) memory.delete(id);
  else memory.set(id, { widgets: s.widgets, folded: s.folded, suppressedRuns });
}

/** The runs the person closed the computer widget in, for this conversation (created on first use). */
export function suppressedRunsOf(memory: WidgetsByConversation, id: string): Set<string> {
  let memo = memory.get(id);
  if (!memo) { memo = { widgets: [], folded: [], suppressedRuns: new Set() }; memory.set(id, memo); }
  return memo.suppressedRuns;
}

/** Forgets a conversation (deleted, archived): nothing of it comes back. */
export const forgetWidgets = (memory: WidgetsByConversation, id: string): void => { memory.delete(id); };

/**
 * Entering a conversation: its remembered widgets come back (none if it has none), the cover closes, and the sidebar stays: Archive
 * is about the account, and Tasks, Memory and Terminal are drawn for whatever conversation is on screen, so they follow it.
 */
export function enterConversation(s: Surfaces, memory: WidgetsByConversation, id: string | null): Surfaces {
  const memo = id ? memory.get(id) : undefined;
  return { ...s, widgets: sortWidgets(memo?.widgets ?? [], s.order), folded: memo?.folded ?? [], cover: null };
}
