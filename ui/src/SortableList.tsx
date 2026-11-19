import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MutableRefObject, type ReactNode, type RefObject } from "react";
import { createPortal, flushSync } from "react-dom";
import { closestCenter, DndContext, KeyboardCode, type CollisionDetection, type DragEndEvent, type DragStartEvent, KeyboardSensor, type Modifier, MouseSensor, TouchSensor, useDraggable, useSensor, useSensors } from "@dnd-kit/core";
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { restrictToParentElement, restrictToVerticalAxis } from "@dnd-kit/modifiers";
import { layerRoot } from "./layers";
import { EASE, MOTION } from "./motion";
import { moveItem } from "./sidebarOrder";

const modifiers = [restrictToVerticalAxis, restrictToParentElement];
// A pointer drag is over the card its middle is nearest (the design's "middle crosses middle"). A keyboard step lines the
// card up with the next one: tops when it moves up, bottoms when it moves down. Cards differ a lot in height (Usage is
// several times Artifacts), so for the keyboard the slot with the nearest top or bottom edge is the one it is over.
const nearestEdge: CollisionDetection = ({ collisionRect, droppableContainers, droppableRects }) => droppableContainers
  .flatMap(container => { const rect = droppableRects.get(container.id); return rect ? [{ id: container.id, data: { droppableContainer: container, value: Math.min(Math.abs(rect.top - collisionRect.top), Math.abs(rect.bottom - collisionRect.bottom)) } }] : []; })
  .sort((a, b) => a.data.value - b.data.value);
const noLayoutAnimation = () => false;
const reducedQuery = () => matchMedia("(prefers-reduced-motion: reduce)");
const watchReduced = (change: () => void) => {
  const query = reducedQuery();
  query.addEventListener("change", change);
  return () => query.removeEventListener("change", change);
};
const isReduced = () => reducedQuery().matches;

/** Shared only by lazy panels. The library owns activation, displacement, scrolling and cancellation; callers own order. */
export function SortableList({ items, onMove, announce, instructions, onDragging, children }: {
  items: string[]; onMove: (id: string, to: number) => void; announce: (id: string, index: number) => string;
  instructions: string; onDragging?: (dragging: boolean) => void; children: ReactNode;
}) {
  const sensors = useSensors(
    useSensor(MouseSensor, { activationConstraint: { distance: 6 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 400, tolerance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates,
      keyboardCodes: { start: [KeyboardCode.Space], cancel: [KeyboardCode.Esc], end: [KeyboardCode.Space, KeyboardCode.Enter, KeyboardCode.Tab] } }),
  );
  useEffect(() => {
    // Claim Escape before surface dismissal listeners run. Both library sensors still receive it and own cancellation.
    const claimEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape" && document.documentElement.hasAttribute("data-sortable-dragging")) event.preventDefault();
    };
    document.addEventListener("keydown", claimEscape, true);
    return () => {
      document.removeEventListener("keydown", claimEscape, true);
      document.documentElement.removeAttribute("data-sortable-dragging");
      onDragging?.(false);
    };
  }, [onDragging]);
  // Which kind of drag this is, set by the event that started it.
  const byKeyboard = useRef(false);
  const collision: CollisionDetection = args => byKeyboard.current ? nearestEdge(args) : closestCenter(args);
  const finish = () => { document.documentElement.removeAttribute("data-sortable-dragging"); onDragging?.(false); };
  return <DndContext sensors={sensors} collisionDetection={collision} modifiers={modifiers}
    autoScroll={{ threshold: { x: 0, y: 0.1 } }}
    accessibility={{ container: layerRoot(), screenReaderInstructions: { draggable: instructions }, announcements: {
      onDragStart: ({ active }) => announce(String(active.id), items.indexOf(String(active.id))),
      onDragOver: ({ active, over }) => over ? announce(String(active.id), items.indexOf(String(over.id))) : undefined,
      onDragEnd: ({ active, over }) => announce(String(active.id), items.indexOf(String(over?.id ?? active.id))),
      onDragCancel: ({ active }) => announce(String(active.id), items.indexOf(String(active.id))),
    } }}
    onDragStart={({ activatorEvent }) => { byKeyboard.current = activatorEvent instanceof KeyboardEvent; document.documentElement.setAttribute("data-sortable-dragging", ""); onDragging?.(true); }}
    onDragCancel={finish} onDragEnd={({ active, over }) => {
      finish();
      if (over && active.id !== over.id) onMove(String(active.id), items.indexOf(String(over.id)));
    }}>
    <SortableContext items={items} strategy={verticalListSortingStrategy}>{children}</SortableContext>
  </DndContext>;
}

export function SortableStatus({ children }: { children: ReactNode }) {
  return createPortal(<span className="sr-only" role="status" aria-live="polite">{children}</span>, layerRoot());
}

export function useSortableItem(id: string, disabled: boolean, moveStep: (step: -1 | 1) => void, dropDisabled = disabled) {
  const reduced = useSyncExternalStore(watchReduced, isReduced, () => false);
  const sortable = useSortable({ id, disabled: { draggable: disabled, droppable: dropDisabled }, animateLayoutChanges: noLayoutAnimation,
    transition: { duration: reduced ? 0 : MOTION.base, easing: EASE.settle } });
  const draggedUntil = useRef(0);
  useEffect(() => {
    if (sortable.isDragging) return () => { draggedUntil.current = Date.now() + 400; };
  }, [sortable.isDragging]);
  const onKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (!disabled && event.altKey && !sortable.active && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
      event.preventDefault(); event.stopPropagation();
      moveStep(event.key === "ArrowUp" ? -1 : 1);
    } else sortable.listeners?.onKeyDown?.(event);
  };
  const transform = sortable.transform;
  const style: CSSProperties = {
    transform: transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)` : undefined,
    transition: reduced ? "none" : sortable.transition ?? (sortable.isDragging ? "none" : `transform ${MOTION.base}ms ${EASE.settle}`),
  };
  return { ...sortable, style, activator: { ...sortable.listeners, onKeyDown },
    wasDragged: () => sortable.isDragging || Date.now() < draggedUntil.current };
}

/* ---- The sidebar: pinned rows take a manual order, a list row can only be dragged into the pinned area (the list is recency).
   Rows are the kit's SidebarRow; the workspace draws them, this chunk gives them the library's refs, transforms and sensors. ---- */

export type DndAnnounce = { kind: "lift" | "move" | "pin" | "unpin"; id: string; pos: number; total: number };
export type DndCommit = { pinned: string[]; list: string[] };
export type SidebarDndOptions = {
  /** False while the rail is folded: the rows stay as they are, nothing starts a drag. */
  enabled: boolean;
  scrollRef: RefObject<HTMLElement | null>;
  /** Draggable ids as currently displayed: the pinned rows in order, and the plain list rows in order. */
  getIds: () => { pinned: string[]; list: string[] };
  onCommit: (next: DndCommit) => void;
  onMenu: (id: string, x: number, y: number) => void;
  onAnnounce: (event: DndAnnounce) => void;
};
/** What a row spreads on its SidebarRow. */
export type RowBind = {
  ref: (node: HTMLElement | null) => void;
  className: string;
  style?: CSSProperties;
  onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => void;
} & Record<string, unknown>;
type Zone = "dock" | "list";
type Flight = { rect: DOMRect; kind: "lift" | "cross" | "slide"; at: number; focus?: boolean };

// The list is drawn plain until this chunk arrives, then its rows are made again: the row that had focus is found again by this.
let heldFocus = (typeof document !== "undefined" && document.activeElement?.closest?.("[data-sidebar-flip]")?.getAttribute("data-sidebar-flip")) || null;
const LONG_PRESS_MS = 380;
const MOUSE_SLOP = 4;
const TOUCH_SLOP = 9;
const LIFT = 1.02;
/** Where each element was when it was let go, until the row that now shows it flies in from there. */
const flights = new Map<string, Flight>();
const nodeOf = (scroller: HTMLElement | null, id: string) => scroller?.querySelector<HTMLElement>(`[data-dnd-id="${CSS.escape(id)}"]`) ?? null;
const rectTop = (child: Element) => child.getBoundingClientRect().top;

/** The mouse starts a drag with the primary button only; Control-click is the context menu on a Mac. */
class RowMouseSensor extends MouseSensor {
  static activators = [{ eventName: "onMouseDown" as const, handler: ({ nativeEvent }: { nativeEvent: MouseEvent }, { onActivation }: { onActivation?: (arg: { event: MouseEvent }) => void }) => {
    if (nativeEvent.button !== 0 || nativeEvent.ctrlKey) return false;
    onActivation?.({ event: nativeEvent });
    return true;
  } }] as typeof MouseSensor.activators;
}

/** The library renders once per pointer event; a mouse reports faster than frames are drawn, so once a drag is running the moves are applied one
 *  per frame (the last one wins). Before the drag starts they pass straight through, so the activation rules see every event. */
function coalesceMoves(Sensor: { prototype: object }) {
  const proto = Sensor.prototype as Record<string, (...args: unknown[]) => void> & { activated?: boolean; latest?: Event; frame?: number };
  const move = proto.handleMove, end = proto.handleEnd, cancel = proto.handleCancel;
  proto.handleMove = function (this: typeof proto, event: unknown) {
    if (!this.activated) return move.call(this, event);
    if ((event as Event).cancelable) (event as Event).preventDefault();
    this.latest = event as Event;
    if (!this.frame) this.frame = requestAnimationFrame(() => { this.frame = 0; move.call(this, this.latest); });
  };
  const stop = (original: (...args: unknown[]) => void) => function (this: typeof proto, event: unknown) { cancelAnimationFrame(this.frame ?? 0); this.frame = 0; return original.call(this, event); };
  proto.handleEnd = stop(end);
  proto.handleCancel = stop(cancel);
}
class RowTouchSensor extends TouchSensor {}
coalesceMoves(RowMouseSensor);
coalesceMoves(RowTouchSensor);

/** Viewport tops of the list's visible children: what the eye sees before the layout changes under it. */
function flowTops(scroller: HTMLElement, skip: Array<Element | null>) {
  const box = scroller.getBoundingClientRect();
  const tops = new Map<HTMLElement, number>();
  for (const child of Array.from(scroller.children) as HTMLElement[]) {
    if (skip.includes(child)) continue;
    const r = child.getBoundingClientRect();
    if (r.height > 0 && r.bottom > box.top && r.top < box.bottom) tops.set(child, r.top);
  }
  return tops;
}
/** The layout moved under what was on screen (the pinned area opened or closed): each child glides from where it was seen. */
function glideFrom(tops: Map<HTMLElement, number>) {
  if (isReduced()) return;
  tops.forEach((top, child) => {
    if (!child.isConnected) return;
    const delta = top - rectTop(child);
    if (Math.abs(delta) > 1) child.animate([{ transform: `translateY(${delta}px)` }, { transform: "none" }], { duration: MOTION.base, easing: EASE.out });
  });
}
/** The settle: the element flies from the pose it was let go in to the slot it now has. */
function land(el: HTMLElement, flight: Flight) {
  const to = el.getBoundingClientRect();
  const dx = flight.rect.left + flight.rect.width / 2 - (to.left + to.width / 2);
  const dy = flight.rect.top + flight.rect.height / 2 - (to.top + to.height / 2);
  if (flight.kind === "slide") {
    if (Math.abs(dx) + Math.abs(dy) > 1) el.animate([{ transform: `translate(${dx}px,${dy}px)` }, { transform: "none" }], { duration: MOTION.base, easing: EASE.out });
    return;
  }
  el.classList.add("is-dnd-landing");
  const resting = getComputedStyle(el).backgroundColor;
  const animation = el.animate(flight.kind === "lift"
    ? [{ transform: `translate(${dx}px,${dy}px) scale(${LIFT})`, boxShadow: "var(--shadow)", backgroundColor: "var(--surface)" }, { transform: "none", boxShadow: "0 0 0 transparent", backgroundColor: resting }]
    // The row became a pinned row (or the reverse): a new element, so it flies out of the lifted row's place.
    : [{ transform: `translate(${dx}px,${dy}px) scale(.6)`, opacity: .55 }, { transform: "none", opacity: 1 }],
  { duration: MOTION.base, easing: EASE.settle });
  const landed = () => el.classList.remove("is-dnd-landing");
  animation.addEventListener("finish", landed);
  animation.addEventListener("cancel", landed);
}

type Shared = { live: MutableRefObject<SidebarDndOptions>; settle: MutableRefObject<Map<HTMLElement, number> | null>; refresh: () => void };
const SidebarDndShared = createContext<Shared | null>(null);

/** A list row has no hooks of its own (sixty hook sets would all render on every pointer move): the row that is pressed gets this, which
 *  registers its element with the library, hands it the press that is under way and moves it with the library's transform. */
function ListProxy({ id, el, press }: { id: string; el: HTMLElement; press: MouseEvent | TouchEvent }) {
  const draggable = useDraggable({ id });
  const { setNodeRef, listeners, transform, isDragging } = draggable;
  const wasDragging = useRef(false);
  useLayoutEffect(() => {
    setNodeRef(el);
    const start = ("touches" in press ? listeners?.onTouchStart : listeners?.onMouseDown) as ((event: { nativeEvent: Event }) => void) | undefined;
    start?.({ nativeEvent: press });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [el, press]);
  useLayoutEffect(() => {
    if (isDragging && transform) {
      el.classList.add("is-dnd-dragging");
      el.style.transform = `translate3d(${transform.x}px, ${transform.y}px, 0) scale(${LIFT})`;
    } else if (wasDragging.current) {
      el.classList.remove("is-dnd-dragging");
      el.style.transform = "";
      const flight = flights.get(id);
      // A row that was pinned is gone by now (a tile took its place); one that stays flies back to its slot.
      if (flight && el.isConnected) { flights.delete(id); if (!isReduced()) land(el, flight); }
    }
    wasDragging.current = isDragging;
  });
  return null;
}

export function SidebarSortable({ options, children }: { options: SidebarDndOptions; children: ReactNode }) {
  const live = useRef(options);
  live.current = options;
  const settle = useRef<Map<HTMLElement, number> | null>(null);
  const [, refresh] = useReducer((count: number) => count + 1, 0);
  const shared = useMemo<Shared>(() => ({ live, settle, refresh }), []);
  const sensors = useSensors(
    useSensor(RowMouseSensor, { activationConstraint: { distance: MOUSE_SLOP } }),
    useSensor(RowTouchSensor, { activationConstraint: { delay: LONG_PRESS_MS, tolerance: TOUCH_SLOP } }),
  );
  const drag = useRef<{ id: string; source: Zone; touch: boolean; startX: number; startY: number; shift: number } | null>(null);
  const pointer = useRef({ x: 0, y: 0 });
  const swallowUntil = useRef(0);
  const [pressed, setPressed] = useState<{ id: string; el: HTMLElement; press: MouseEvent | TouchEvent } | null>(null);
  const scroller = () => live.current.scrollRef.current;
  const zoneEl = () => scroller()?.querySelector<HTMLElement>("[data-dnd-dropzone]") ?? null;
  const zoneOf = (id: string): Zone | null => { const { pinned, list } = live.current.getIds(); return pinned.includes(id) ? "dock" : list.includes(id) ? "list" : null; };

  /** Where the pointer is: a list row is over the pinned area when it is in the frame or above the list; a pinned row has left the dock below it. */
  const edges = useRef<{ listTop: number; frameBottom: number; dockBottom: number; scroll: number } | null>(null);
  const measureEdges = (list: HTMLElement) => ({
    listTop: list.getBoundingClientRect().top, scroll: list.scrollTop,
    frameBottom: zoneEl()?.getBoundingClientRect().bottom ?? 0, dockBottom: list.querySelector("[data-dnd-dock]")?.getBoundingClientRect().bottom ?? 0,
  });
  const aim = (x: number, y: number, source: Zone): { zone: Zone; over: "pin" | "" } => {
    const list = scroller();
    if (!list) return { zone: source, over: "" };
    // Edges are measured once, when the drag starts, and moved by the scroll since: no layout is read on a pointer move.
    const e = edges.current ?? measureEdges(list);
    const gone = list.scrollTop - e.scroll;
    if (source === "list") return y < e.listTop || y < e.frameBottom - gone ? { zone: "dock", over: "pin" } : { zone: "list", over: "" };
    return y <= e.dockBottom - gone + 12 ? { zone: "dock", over: "" } : { zone: "list", over: "" };
  };
  // Only pinned rows take part in collisions: a list row is never "over" another row, and a pinned row below the dock is over nothing.
  const collision: CollisionDetection = args => {
    const p = args.pointerCoordinates;
    if (!p || zoneOf(String(args.active.id)) !== "dock" || aim(p.x, p.y, "dock").zone === "list") return [];
    return closestCenter(args);
  };
  // A list row's own slot moves when the pinned area opens above it; the transform takes that shift back out so it stays under the pointer.
  const keepUnderPointer: Modifier = ({ transform }) => ({ ...transform, y: transform.y - (drag.current?.shift ?? 0) });

  const track = useCallback((event: MouseEvent | TouchEvent) => {
    const point = "touches" in event ? event.touches[0] ?? event.changedTouches[0] : event;
    if (point) pointer.current = { x: point.clientX, y: point.clientY };
  }, []);
  // The first layout: give focus back to the row that had it before the rows were made again.
  useLayoutEffect(() => {
    if (!heldFocus) return;
    nodeOf(scroller(), heldFocus)?.focus({ preventScroll: true });
    heldFocus = null;
  }, []);
  useEffect(() => {
    const list = scroller();
    if (list) list.dataset.dndReady = "1";
    // Claim Escape before surface dismissal listeners run; the sensors still receive it and cancel the drag.
    const claimEscape = (event: KeyboardEvent) => { if (event.key === "Escape" && document.documentElement.hasAttribute("data-sortable-dragging")) event.preventDefault(); };
    // The click that follows a release belongs to the drag, not to whatever is under the finger afterwards (a menu may have just opened there).
    const swallowClick = (event: MouseEvent) => { if (performance.now() < swallowUntil.current) { event.preventDefault(); event.stopPropagation(); } };
    // A long press fires the native menu on some phones; the press owns that gesture.
    const holdMenu = (event: Event) => { if (drag.current?.touch) { event.preventDefault(); event.stopPropagation(); } };
    // A press on a list row hands it to the library (see ListProxy); pinned rows have their own hooks.
    const arm = (event: MouseEvent | TouchEvent) => {
      if (!live.current.enabled || drag.current || ("button" in event && (event.button !== 0 || event.ctrlKey))) return;
      const row = (event.target as Element).closest<HTMLElement>('[data-dnd-zone="list"]');
      if (row?.dataset.dndId && list?.contains(row)) flushSync(() => setPressed({ id: row.dataset.dndId!, el: row, press: event }));
    };
    // Menu key and Shift+F10 open the row's menu (some platforms send no contextmenu event for them).
    const menuKey = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "ContextMenu" && !(event.key === "F10" && event.shiftKey)) return;
      const row = (event.target as Element).closest<HTMLElement>("[data-dnd-id]");
      if (!row || !list?.contains(row)) return;
      const r = row.getBoundingClientRect();
      event.preventDefault();
      live.current.onMenu(row.dataset.dndId!, r.left + Math.min(40, r.width / 2), r.top + r.height / 2);
    };
    list?.addEventListener("mousedown", arm, true);
    list?.addEventListener("touchstart", arm, { capture: true, passive: true });
    list?.addEventListener("keydown", menuKey);
    document.addEventListener("keydown", claimEscape, true);
    window.addEventListener("click", swallowClick, true);
    list?.addEventListener("contextmenu", holdMenu, true);
    return () => {
      if (list) delete list.dataset.dndReady;
      window.removeEventListener("mousemove", track);
      window.removeEventListener("touchmove", track);
      document.removeEventListener("keydown", claimEscape, true);
      window.removeEventListener("click", swallowClick, true);
      list?.removeEventListener("contextmenu", holdMenu, true);
      list?.removeEventListener("mousedown", arm, true);
      list?.removeEventListener("touchstart", arm, true);
      list?.removeEventListener("keydown", menuKey);
      document.documentElement.removeAttribute("data-sortable-dragging");
      zoneEl()?.removeAttribute("data-dnd");
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [track]);

  // The pinned area opening or closing moves rows; they glide from where they were seen, once the new layout is in.
  useLayoutEffect(() => {
    const tops = settle.current;
    settle.current = null;
    if (tops) glideFrom(tops);
    // A pinned row dropped into the list is a plain row now: no hook of its own to fly it in, so it is done here.
    flights.forEach((flight, id) => {
      const el = nodeOf(scroller(), id);
      if (!el) return;
      flights.delete(id);
      if (performance.now() - flight.at < 800 && !isReduced()) land(el, flight);
    });
  });

  const stopTracking = () => { window.removeEventListener("mousemove", track); window.removeEventListener("touchmove", track); };

  const onDragStart = ({ active, activatorEvent }: DragStartEvent) => {
    const id = String(active.id);
    const list = scroller(), el = nodeOf(list, id), zone = zoneEl(), source = zoneOf(id);
    if (!list || !el || !source) return;
    const touch = "touches" in activatorEvent;
    const start = touch ? (activatorEvent as TouchEvent).touches[0] : (activatorEvent as MouseEvent);
    pointer.current = { x: start.clientX, y: start.clientY };
    window.addEventListener("mousemove", track);
    window.addEventListener("touchmove", track, { passive: true });
    document.documentElement.setAttribute("data-sortable-dragging", "");
    // A dragged row lights up the pinned area, the first block of the list: the hint inside it moves everything below down at once,
    // and the list goes back to the top so the whole frame is on screen. The lifted row cancels the shift, the rest glide.
    let shift = 0;
    if (source === "list" && zone) {
      const seen = flowTops(list, [el, zone]);
      const before = rectTop(el);
      zone.setAttribute("data-dnd", "row");
      if (list.scrollTop) list.scrollTop = 0;
      shift = rectTop(el) - before;
      glideFrom(seen);
    } else zone?.setAttribute("data-dnd", "tile");
    // Every row is "live" for the glide and the hover rule, as long as the drag lasts (one pass, not one render per row).
    list.querySelectorAll('[data-dnd-id]').forEach(row => row.classList.add("dnd-live"));
    edges.current = measureEdges(list);
    drag.current = { id, source, touch, startX: start.clientX, startY: start.clientY, shift };
    try { navigator.vibrate?.(8); } catch { /* haptics are optional */ }
    const ids = live.current.getIds()[source === "dock" ? "pinned" : "list"];
    live.current.onAnnounce({ kind: "lift", id, pos: ids.indexOf(id) + 1, total: ids.length });
  };

  const onDragMove = () => {
    const current = drag.current, zone = zoneEl();
    if (!current || !zone || current.source !== "list") return;
    const over = aim(pointer.current.x, pointer.current.y, "list").over;
    if ((zone.getAttribute("data-dnd-over") ?? "") !== over) zone.setAttribute("data-dnd-over", over);
  };

  const end = (event: DragEndEvent | null) => {
    const current = drag.current;
    stopTracking();
    document.documentElement.removeAttribute("data-sortable-dragging");
    drag.current = null;
    const list = scroller(), zone = zoneEl();
    if (!current || !list) return;
    const { id, source } = current;
    list.querySelectorAll('[data-dnd-zone="list"]').forEach(row => row.classList.remove("dnd-live"));
    swallowUntil.current = performance.now() + (current.touch ? 400 : 60);
    const el = nodeOf(list, id);
    const rect = el?.getBoundingClientRect();
    const seen = flowTops(list, [el, zone]);
    // Where the pointer is is read while the pinned area is still open.
    const where = aim(pointer.current.x, pointer.current.y, source);
    edges.current = null;
    // The pinned area closes before the new order renders, so the landing is measured in the final layout.
    zone?.removeAttribute("data-dnd-over");
    zone?.removeAttribute("data-dnd");
    settle.current = seen;
    const { pinned, list: rows } = live.current.getIds();
    const moved = Math.hypot(pointer.current.x - current.startX, pointer.current.y - current.startY) > 6;
    let next: DndCommit | null = null, kind: DndAnnounce["kind"] = "move";
    if (event && current.touch && !moved) {
      // A long press that never moved: show the row's menu instead of leaving it lifted.
      live.current.onMenu(id, pointer.current.x, pointer.current.y);
    } else if (event) {
      if (source === "list" && where.over === "pin") { next = { pinned: [...pinned, id], list: rows.filter(row => row !== id) }; kind = "pin"; }
      else if (source === "dock" && where.zone === "list") { next = { pinned: pinned.filter(row => row !== id), list: [id, ...rows] }; kind = "unpin"; }
      else if (source === "dock" && event.over && String(event.over.id) !== id) {
        const to = pinned.indexOf(String(event.over.id));
        if (to >= 0) next = { pinned: moveItem(pinned, pinned.indexOf(id), to), list: rows };
      }
    }
    // A change flies the element into its new slot; no change flies it back to the old one.
    if (rect) flights.set(id, { rect, kind: next && kind !== "move" ? "cross" : "lift", at: performance.now() });
    if (next) {
      const into = kind === "unpin" ? next.list : next.pinned;
      live.current.onAnnounce({ kind, id, pos: into.indexOf(id) + 1, total: into.length });
      live.current.onCommit(next);
    }
    shared.refresh();
  };

  return <SidebarDndShared.Provider value={shared}>
    <DndContext sensors={sensors} collisionDetection={collision} modifiers={[keepUnderPointer]}
      autoScroll={isReduced() ? false : { threshold: { x: 0, y: 0.1 }, layoutShiftCompensation: false }}
      accessibility={{ container: layerRoot(), screenReaderInstructions: { draggable: "" }, announcements: { onDragStart: () => undefined, onDragOver: () => undefined, onDragEnd: () => undefined, onDragCancel: () => undefined } }}
      onDragStart={onDragStart} onDragMove={onDragMove} onDragEnd={event => end(event)} onDragCancel={() => end(null)}>
      {children}
      {pressed && <ListProxy key={pressed.press.timeStamp} id={pressed.id} el={pressed.el} press={pressed.press} />}
    </DndContext>
  </SidebarDndShared.Provider>;
}

/** Wraps the pinned rows: the one list that takes an order. */
export function SidebarDock({ ids, children }: { ids: string[]; children: ReactNode }) {
  return <SortableContext items={ids} strategy={verticalListSortingStrategy}>{children}</SortableContext>;
}

/** Runs when a row (or the element that took its place) renders: it flies from where it was let go. */
function useLanding(id: string, node: RefObject<HTMLElement | null>) {
  useLayoutEffect(() => {
    const flight = flights.get(id), el = node.current;
    if (!flight || !el) return;
    flights.delete(id);
    if (performance.now() - flight.at > 800) return;
    if (flight.focus) el.focus({ preventScroll: true });
    if (!isReduced()) land(el, flight);
  });
}

/** Alt+Arrow moves a pinned row. */
function useRowKeys(id: string, zone: Zone, shared: Shared, disabled: boolean) {
  return useCallback((event: ReactKeyboardEvent<HTMLElement>) => {
    const { live } = shared;
    const step = ({ ArrowUp: -1, ArrowLeft: -1, ArrowDown: 1, ArrowRight: 1 } as Record<string, number>)[event.key];
    // List rows follow recency; only pinned rows take a manual order.
    if (disabled || zone !== "dock" || !step || !event.altKey || event.ctrlKey || event.metaKey) return;
    event.preventDefault();
    const { pinned, list } = live.current.getIds();
    const from = pinned.indexOf(id), to = from + step;
    if (from < 0 || to < 0 || to >= pinned.length) return;
    const now = performance.now();
    pinned.forEach(row => { const el = nodeOf(live.current.scrollRef.current, row); if (el) flights.set(row, { rect: el.getBoundingClientRect(), kind: "slide", at: now, focus: row === id }); });
    live.current.onAnnounce({ kind: "move", id, pos: to + 1, total: pinned.length });
    live.current.onCommit({ pinned: moveItem(pinned, from, to), list });
  }, [id, zone, shared, disabled]);
}

type Binding = { setNodeRef: (node: HTMLElement | null) => void; attributes: object; listeners?: object; transform: { x: number; y: number } | null; transition?: string; isDragging: boolean; active: unknown };
function useBind(id: string, zone: Zone, disabled: boolean, binding: Binding, render: (bind: RowBind) => ReactNode) {
  const shared = useContext(SidebarDndShared)!;
  const node = useRef<HTMLElement | null>(null);
  const { setNodeRef, attributes: dndAttributes, listeners, transform, transition, isDragging, active } = binding;
  const ref = useCallback((el: HTMLElement | null) => { node.current = el; setNodeRef(el); }, [setNodeRef]);
  const onKeyDown = useRowKeys(id, zone, shared, disabled);
  // The row stays a button to a screen reader: not "draggable", and no pointer to the library's empty instructions.
  const attributes = useMemo(() => { const { "aria-roledescription": _role, "aria-describedby": _by, ...rest } = dndAttributes as Record<string, unknown>; return rest; }, [dndAttributes]);
  useLanding(id, node);
  const className = `${isDragging ? "is-dnd-dragging " : ""}${active ? "dnd-live" : ""}`.trim();
  const transformCss = transform ? `translate3d(${transform.x}px, ${transform.y}px, 0)${isDragging ? ` scale(${LIFT})` : ""}` : undefined;
  // The rows that are not moving keep the same element, so a frame of a drag does not render sixty rows.
  return useMemo(() => render({ ...attributes, ...listeners, ref, className, onKeyDown, style: transformCss || transition ? { transform: transformCss, transition: isDragging ? "none" : transition } : undefined }),
    [render, attributes, listeners, ref, className, onKeyDown, transformCss, transition, isDragging]);
}

/** A pinned row: sortable inside the dock. */
export function SidebarDockRow({ id, disabled = false, render }: { id: string; disabled?: boolean; render: (bind: RowBind) => ReactNode }) {
  const reduced = useSyncExternalStore(watchReduced, isReduced, () => false);
  const sortable = useSortable({ id, disabled, animateLayoutChanges: noLayoutAnimation, transition: { duration: reduced ? 0 : MOTION.base, easing: EASE.settle } });
  return useBind(id, "dock", disabled, { ...sortable, transition: reduced ? "none" : sortable.transition ?? undefined }, render);
}
