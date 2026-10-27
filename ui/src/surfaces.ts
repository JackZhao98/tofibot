/**
 * The three tiers of what a conversation can have open (docs/design/widgets-and-sidebar.md).
 *
 * - widgets: small live blocks inside the chat, to the right of the messages. Up to three, fixed order, any may be folded.
 * - sidebar: one heavy working view in a real grid track at the window edge. A second one replaces the first.
 * - cover: an exclusive full-window panel (settings and the like). While it is open the widgets and the sidebar are
 *   hidden, not closed: closing the cover brings them back as they were.
 *
 * Everything here is pure: the workspace holds one `Surfaces` value and calls these to get the next one.
 */
export type WidgetId = "computer" | "members" | "artifacts";
export type SidebarId = "tasks" | "memory" | "terminal" | "archive" | "conversation";
export type CoverId = "settings" | "bot-settings" | "group-settings" | "new-group" | "approvals" | "artifacts";

/** Top to bottom. The order never depends on which widget was opened first. */
export const WIDGET_ORDER: readonly WidgetId[] = ["computer", "members", "artifacts"];

export type Surfaces = {
  /** Open widgets, always in WIDGET_ORDER. */
  widgets: WidgetId[];
  /** The open widgets that show only their header. */
  folded: WidgetId[];
  sidebar: { id: SidebarId; expanded: boolean } | null;
  cover: CoverId | null;
  /** Opening something that is already open bumps `n`: the surface scrolls into view and takes focus. */
  focus: { id: string | null; n: number };
};

export const emptySurfaces = (): Surfaces => ({ widgets: [], folded: [], sidebar: null, cover: null, focus: { id: null, n: 0 } });

/** True when nothing is open at all. */
export const isEmpty = (s: Surfaces) => !s.widgets.length && !s.sidebar && !s.cover;

const inOrder = (ids: readonly WidgetId[]) => WIDGET_ORDER.filter(id => ids.includes(id));
const bump = (s: Surfaces, id: string): Surfaces["focus"] => ({ id, n: s.focus.n + 1 });

/** Opens (or unfolds and refocuses) a widget. A cover that was open closes: the person asked for something in the chat. */
export const openWidget = (s: Surfaces, id: WidgetId): Surfaces => ({
  ...s, widgets: inOrder([...s.widgets, id]), folded: s.folded.filter(item => item !== id), cover: null, focus: bump(s, id),
});

export const closeWidget = (s: Surfaces, id: WidgetId): Surfaces =>
  s.widgets.includes(id) ? { ...s, widgets: s.widgets.filter(item => item !== id), folded: s.folded.filter(item => item !== id) } : s;

export const toggleWidget = (s: Surfaces, id: WidgetId): Surfaces => s.widgets.includes(id) ? closeWidget(s, id) : openWidget(s, id);

/** Folds an open widget to its header, or unfolds it. */
export const foldWidget = (s: Surfaces, id: WidgetId): Surfaces =>
  s.widgets.includes(id) ? { ...s, folded: s.folded.includes(id) ? s.folded.filter(item => item !== id) : [...s.folded, id] } : s;

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

export const closeAll = (s: Surfaces): Surfaces => isEmpty(s) ? s : { ...emptySurfaces(), focus: s.focus };

/** What Escape closes first: the cover, else the sidebar, else the lowest widget (the computer, the one that is live, last). */
export function escapeTarget(s: Surfaces): { layer: "cover"; id: CoverId } | { layer: "sidebar"; id: SidebarId } | { layer: "widget"; id: WidgetId } | null {
  if (s.cover) return { layer: "cover", id: s.cover };
  if (s.sidebar) return { layer: "sidebar", id: s.sidebar.id };
  const last = s.widgets.at(-1);
  return last ? { layer: "widget", id: last } : null;
}

/** The ids the old single-panel code used. `setPanel(id)` call sites still speak them; `applyPanel` maps them to a tier. */
export type LegacyPanel = "group-create" | "bot-edit" | "memory" | "schedule" | "settings" | "members" | "desktop" | "terminal" | "archive" | "artifacts" | "artifacts-bot" | "artifacts-group" | "file";

/**
 * The `setPanel` adapter: an old panel id opens its tier. `column` is the wide web layout; below it widgets other than
 * the computer (a floating window there) do not exist, so the same entry opens the matching cover instead.
 * "artifacts" is the account menu's whole-account view (a cover); "artifacts-bot" / "artifacts-group" are this
 * conversation's (the widget).
 */
export function applyPanel(s: Surfaces, panel: LegacyPanel | null, column: boolean): Surfaces {
  switch (panel) {
    case null: return closeAll(s);
    case "desktop": return openWidget(s, "computer");
    case "members": return column ? openWidget(s, "members") : openCover(s, "group-settings");
    case "artifacts-bot": case "artifacts-group": return column ? openWidget(s, "artifacts") : openCover(s, "artifacts");
    case "artifacts": return openCover(s, "artifacts");
    case "bot-edit": return openCover(s, "bot-settings");
    case "group-create": return openCover(s, "new-group");
    case "settings": return openCover(s, "settings");
    case "schedule": return openSidebar(s, "tasks");
    case "memory": case "terminal": case "archive": return openSidebar(s, panel);
    case "file": return s;
  }
}
