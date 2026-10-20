import type { MutableRefObject, RefObject } from "react";
import { EASE, MOTION } from "./motion";
import { moveItem } from "./sidebarOrder";

/**
 * Drag, drop and keyboard reordering (the engine, loaded after first paint) for the sidebar list and the pinned tile dock.
 *
 * It works on the DOM, not on React state: rows and tiles carry data-dnd-id / data-dnd-zone ("list" | "dock"), the
 * dragged element follows the pointer through one transform per frame, neighbours glide to the slot they will
 * occupy through one transform each (only when the target slot changes), and React renders once, on drop. No layout
 * is read while the pointer moves: every rect is measured at pickup and kept in scroll-content coordinates.
 *
 * Mouse and pen drag after a few pixels; touch drags after a long press (a held finger that moves first is a scroll).
 * A long press released without moving opens the row's context menu, like a native list. Alt+Arrow moves the focused
 * row or tile. A drag that ends where it began changes nothing and the element glides back.
 */
export type Zone = "dock" | "list";
export type Where = Record<Zone, string[]>;
export type DndAnnounce = { kind: "lift" | "move" | "pin" | "unpin"; id: string; pos: number; total: number };
export type DndCommit = { pinned: string[]; list: string[] };

export type Options = {
  scrollRef: RefObject<HTMLElement | null>;
  enabled: boolean;
  /** Draggable ids as currently displayed: pinned tiles in order, and the reorderable list rows in order. */
  getIds: () => { pinned: string[]; list: string[] };
  onCommit: (next: DndCommit) => void;
  onMenu: (id: string, x: number, y: number) => void;
  onAnnounce: (event: DndAnnounce) => void;
  /** Set on a drop so the row-move animation of the list does not replay on top of the settle. */
  skipFlip: MutableRefObject<boolean>;
  /** Changes whenever the displayed order changes; the settle runs after that commit. */
  signature: string;
};

type Rect = { left: number; top: number; width: number; height: number };
export type Pending = { id: string; from?: Rect; source: Zone; target: Zone; reduced: boolean; keyboard?: Map<string, Rect> };

const LONG_PRESS_MS = 380;
const MOUSE_SLOP = 4;
const TOUCH_SLOP = 9;
const LIFT = 1.02;
const EDGE = 44;
const DRAGGING = "is-dnd-dragging";
const SETTLING = "is-dnd-settling";
// Every row gets this while a drag is live; the neighbours' glide is keyed on it. Nothing in the CSS depends on an attribute
// of an ancestor of the list: such a selector makes the browser restyle every element under it (all the cats) on pickup.
const LIVE = "dnd-live";

/** What the hook owns across renders and hands to the engine. */
export type DndContext = { live: MutableRefObject<Options>; pending: MutableRefObject<Pending | null>; settleTimer: MutableRefObject<number | undefined> };

const sameWhere = (a: Where, b: Where) => a.dock.join("\n") === b.dock.join("\n") && a.list.join("\n") === b.list.join("\n");
const reducedMotion = () => typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;

export function settleSidebarDnd(scroller: HTMLElement, ctx: DndContext) {
  const { pending, settleTimer } = ctx;
  const p = pending.current;
  pending.current = null;
  window.clearTimeout(settleTimer.current);
  scroller.querySelectorAll<HTMLElement>("[data-dnd-id]").forEach(el => { el.style.transform = ""; el.classList.remove(DRAGGING, SETTLING, LIVE); });
  scroller.closest<HTMLElement>(".contact-pane")?.querySelector("[data-dnd-dropzone]")?.removeAttribute("data-dnd");
  if (!p) return;
  const find = (id: string) => scroller.querySelector<HTMLElement>(`[data-dnd-id="${CSS.escape(id)}"]`);
  if (p.keyboard) {
    const el = find(p.id);
    el?.focus({ preventScroll: true });
    el?.scrollIntoView({ block: "nearest" });
    if (p.reduced) return;
    scroller.querySelectorAll<HTMLElement>("[data-dnd-zone='dock']").forEach(tile => {
      const before = p.keyboard!.get(tile.dataset.dndId!);
      if (!before) return;
      const now = tile.getBoundingClientRect();
      const dx = before.left - now.left, dy = before.top - now.top;
      if (Math.abs(dx) + Math.abs(dy) > 1) tile.animate([{ transform: `translate(${dx}px,${dy}px)` }, { transform: "none" }], { duration: MOTION.base, easing: EASE.out });
    });
    return;
  }
  const el = find(p.id);
  if (!el || p.reduced || !p.from) return;
  const to = el.getBoundingClientRect();
  if (p.source === p.target) {
    const dx = p.from.left + p.from.width / 2 - (to.left + to.width / 2);
    const dy = p.from.top + p.from.height / 2 - (to.top + to.height / 2);
    el.animate([
      { transform: `translate(${dx}px,${dy}px) scale(${LIFT})`, boxShadow: "0 12px 26px rgba(0,0,0,.2)" },
      { transform: "none", boxShadow: "0 0 0 rgba(0,0,0,0)" },
    ], { duration: MOTION.panelIn, easing: EASE.settle });
  } else {
    // The row became a tile (or the reverse): a new element, so it settles in instead of flying.
    el.animate([{ opacity: .5, transform: "scale(.92)" }, { opacity: 1, transform: "none" }], { duration: MOTION.base, easing: EASE.out });
  }
}


/** Attach drag, long-press and keyboard handling to the sidebar list. Returns the detach function. */
export function mountSidebarDnd(scroller: HTMLElement, ctx: DndContext): () => void {
  const { live, pending, settleTimer } = ctx;
  const settle = (target: HTMLElement) => settleSidebarDnd(target, ctx);
  const pane = scroller.closest<HTMLElement>(".contact-pane");
  const zone = pane?.querySelector<HTMLElement>("[data-dnd-dropzone]") ?? null;

  type Down = { id: string; zone: Zone; x: number; y: number; pointerId: number; type: string; timer?: number; pressed: boolean };
  type Drag = {
    id: string; source: Zone; el: HTMLElement; pointerId: number;
    startX: number; startY: number; startScroll: number; px: number; py: number;
    srect: DOMRect; rects: Map<string, Rect>; els: Map<string, HTMLElement>;
    orig: Where; hyp: Where; key: string; dockBox: Rect | null; shown: Map<string, string>;
    type: string; reduced: boolean;
  };
  let down: Down | null = null;
  let drag: Drag | null = null;
  let raf = 0;
  // The click that follows a release belongs to the drag, not to whatever is under the finger afterwards (a menu may have just opened there).
  let swallowUntil = 0;

  const idsOf = (): Where => { const { pinned, list } = live.current.getIds(); return { dock: [...pinned], list: [...list] }; };
  const zoneOf = (where: Where, id: string): Zone | null => where.dock.includes(id) ? "dock" : where.list.includes(id) ? "list" : null;

  function removeListeners() {
    window.removeEventListener("pointermove", onMove);
    window.removeEventListener("pointerup", onUp);
    window.removeEventListener("pointercancel", onCancel);
    window.removeEventListener("touchmove", blockTouch);
    window.removeEventListener("selectstart", noSelect);
    window.removeEventListener("keydown", onEscape, true);
    window.removeEventListener("blur", onCancel);
  }

  function noSelect(event: Event) { event.preventDefault(); }
  function blockTouch(event: TouchEvent) { if (drag && event.cancelable) event.preventDefault(); }
  function onEscape(event: KeyboardEvent) { if (event.key === "Escape" && drag) { event.stopPropagation(); finish(false); } }

  function onDown(event: PointerEvent) {
    if (down || drag || !event.isPrimary) return;
    if (event.pointerType === "mouse" && (event.button !== 0 || event.ctrlKey)) return;
    const el = (event.target as Element).closest<HTMLElement>("[data-dnd-id]");
    if (!el || !scroller!.contains(el)) return;
    down = { id: el.dataset.dndId!, zone: el.dataset.dndZone as Zone, x: event.clientX, y: event.clientY, pointerId: event.pointerId, type: event.pointerType, pressed: false };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onCancel);
    window.addEventListener("blur", onCancel);
    if (event.pointerType === "touch") {
      window.addEventListener("touchmove", blockTouch, { passive: false });
      const started = down;
      down.timer = window.setTimeout(() => { if (down === started) { started.pressed = true; begin(); } }, LONG_PRESS_MS);
    }
  }

  function onMove(event: PointerEvent) {
    if (!down || event.pointerId !== down.pointerId) return;
    if (!drag) {
      const moved = Math.hypot(event.clientX - down.x, event.clientY - down.y);
      if (down.type === "touch") { if (moved > TOUCH_SLOP) cancelPress(); }
      else if (moved > MOUSE_SLOP) { begin(); if (!drag) return; }
      else return;
    }
    if (drag) { drag.px = event.clientX; drag.py = event.clientY; schedule(); }
  }

  function cancelPress() {
    if (down?.timer) window.clearTimeout(down.timer);
    down = null;
    removeListeners();
  }

  function onUp(event: PointerEvent) {
    if (!down || event.pointerId !== down.pointerId) return;
    if (!drag) { cancelPress(); return; }
    drag.px = event.clientX; drag.py = event.clientY;
    finish(true);
  }
  function onCancel() { if (drag) finish(false); else cancelPress(); }

  function begin() {
    if (!down || drag) return;
    const el = scroller!.querySelector<HTMLElement>(`[data-dnd-id="${CSS.escape(down.id)}"]`);
    const orig = idsOf();
    if (!el || !zoneOf(orig, down.id)) { cancelPress(); return; }
    const srect = scroller!.getBoundingClientRect();
    const scrollTop = scroller!.scrollTop;
    const rects = new Map<string, Rect>();
    const els = new Map<string, HTMLElement>();
    scroller!.querySelectorAll<HTMLElement>("[data-dnd-id]").forEach(item => {
      const r = item.getBoundingClientRect();
      rects.set(item.dataset.dndId!, { left: r.left - srect.left, top: r.top - srect.top + scrollTop, width: r.width, height: r.height });
      els.set(item.dataset.dndId!, item);
    });
    const dock = scroller!.querySelector<HTMLElement>("[data-dnd-dock]");
    let dockBox: Rect | null = null;
    if (dock) { const r = dock.getBoundingClientRect(); dockBox = { left: r.left - srect.left, top: r.top - srect.top + scrollTop, width: r.width, height: r.height }; }
    const source = zoneOf(orig, down.id)!;
    drag = {
      id: down.id, source, el, pointerId: down.pointerId, startX: down.x, startY: down.y, startScroll: scrollTop,
      px: down.x, py: down.y, srect, rects, els, orig, hyp: { dock: [...orig.dock], list: [...orig.list] }, key: "",
      dockBox, shown: new Map(), type: down.type, reduced: reducedMotion(),
    };
    if (down.timer) window.clearTimeout(down.timer);
    el.classList.add(DRAGGING);
    if (pane && zone) {
      // The pin zone fills the sidebar above the list, whatever the platform puts there. The variable lives on the zone,
      // not the pane: a custom property set on the pane would restyle every cat in the list.
      const paneBox = pane.getBoundingClientRect();
      zone.style.setProperty("--dnd-zone-bottom", `${Math.max(72, Math.round(srect.top - paneBox.top - 6))}px`);
    }
    els.forEach(item => item.classList.add(LIVE));
    zone?.setAttribute("data-dnd", source === "list" ? "row" : "tile");
    window.addEventListener("selectstart", noSelect);
    window.addEventListener("keydown", onEscape, true);
    try { navigator.vibrate?.(8); } catch { /* haptics are optional */ }
    const zoneIds = orig[source];
    live.current.onAnnounce({ kind: "lift", id: drag.id, pos: zoneIds.indexOf(drag.id) + 1, total: zoneIds.length });
    schedule();
  }

  function schedule() { if (!raf) raf = requestAnimationFrame(update); }

  function slot(d: Drag, zone: Zone, index: number): Rect {
    const ids = d.orig[zone];
    if (index < ids.length) return d.rects.get(ids[index])!;
    const last = d.rects.get(ids[ids.length - 1])!;
    const prev = ids.length > 1 ? d.rects.get(ids[ids.length - 2])! : null;
    const stepX = prev ? last.left - prev.left : 0;
    const stepY = prev ? last.top - prev.top : last.height + 2;
    const extra = index - (ids.length - 1);
    return { ...last, left: last.left + stepX * extra, top: last.top + stepY * extra };
  }

  function aim(d: Drag, scrollTop: number): { where: Where; over: "pin" | "" } {
    const cx = d.px - d.srect.left;
    const cy = d.py - d.srect.top + scrollTop;
    const { dock, list } = d.orig;
    const middle = (id: string) => { const r = d.rects.get(id)!; return r.top + r.height / 2; };
    if (d.source === "list") {
      // Above the list: over the pin zone that covers the search area.
      const overDrop = d.py < d.srect.top;
      const overDock = Boolean(d.dockBox && dock.length && cy >= d.dockBox.top && cy <= d.dockBox.top + d.dockBox.height);
      if (overDrop || overDock) return { where: { dock: [...dock, d.id], list: list.filter(id => id !== d.id) }, over: "pin" };
      const rest = list.filter(id => id !== d.id);
      const at = rest.filter(id => middle(id) < cy).length;
      return { where: { dock: dock, list: moveItem([...rest, d.id], rest.length, at) }, over: "" };
    }
    const dockBottom = d.dockBox ? d.dockBox.top + d.dockBox.height : 0;
    if (cy <= dockBottom + 12) {
      let best = 0, bestDistance = Infinity;
      dock.forEach((id, index) => {
        const r = d.rects.get(id)!;
        const distance = Math.hypot(cx - (r.left + r.width / 2), cy - (r.top + r.height / 2));
        if (distance < bestDistance) { best = index; bestDistance = distance; }
      });
      return { where: { dock: moveItem(dock, dock.indexOf(d.id), best), list }, over: "" };
    }
    const at = list.filter(id => middle(id) < cy).length;
    return { where: { dock: dock.filter(id => id !== d.id), list: moveItem([...list, d.id], list.length, at) }, over: "" };
  }

  function update() {
    raf = 0;
    const d = drag;
    if (!d) return;
    const scrollTop = scroller!.scrollTop;
    const dx = d.px - d.startX;
    const dy = d.py - d.startY + (scrollTop - d.startScroll);
    d.el.style.transform = `translate3d(${dx}px,${dy}px,0) scale(${LIFT})`;
    const { where, over } = aim(d, scrollTop);
    const key = `${where.dock.join(",")}|${where.list.join(",")}`;
    if (key !== d.key) {
      d.key = key;
      d.hyp = where;
      zone?.setAttribute("data-dnd-over", over);
      for (const [id, el] of d.els) {
        if (id === d.id) continue;
        const zone = zoneOf(d.orig, id)!;
        const from = d.orig[zone].indexOf(id), to = where[zone].indexOf(id);
        let value = "";
        if (from !== to && to >= 0) {
          const a = slot(d, zone, from), b = slot(d, zone, to);
          value = `translate3d(${b.left - a.left}px,${b.top - a.top}px,0)`;
        }
        if (d.shown.get(id) !== value) { d.shown.set(id, value); el.style.transform = value; }
      }
    }
    let speed = 0;
    const top = d.py - d.srect.top, bottom = d.srect.bottom - d.py;
    if (top < EDGE) speed = -Math.ceil((EDGE - Math.max(0, top)) / EDGE * 14);
    else if (bottom < EDGE) speed = Math.ceil((EDGE - Math.max(0, bottom)) / EDGE * 14);
    if (speed && !d.reduced) {
      const before = scroller!.scrollTop;
      scroller!.scrollTop = before + speed;
      if (scroller!.scrollTop !== before) schedule();
    }
  }

  function finish(commit: boolean) {
    const d = drag, press = down;
    if (raf) { cancelAnimationFrame(raf); raf = 0; }
    removeListeners();
    drag = null; down = null;
    if (!d || !press) return;
    swallowUntil = performance.now() + (press.type === "touch" ? 400 : 60);
    zone?.removeAttribute("data-dnd-over");
    const moved = Math.hypot(d.px - d.startX, d.py - d.startY) > 6;
    const changed = commit && !sameWhere(d.hyp, d.orig);
    if (commit && press.type === "touch" && !moved) {
      // A long press that never moved: show the row's menu instead of leaving it lifted.
      revert(d);
      live.current.onMenu(d.id, d.px, d.py);
      return;
    }
    if (!changed) { revert(d); return; }
    const from = d.el.getBoundingClientRect();
    const target = zoneOf(d.hyp, d.id)!;
    pending.current = { id: d.id, from, source: d.source, target, reduced: d.reduced };
    live.current.skipFlip.current = true;
    const ids = d.hyp[target];
    live.current.onAnnounce({ kind: d.source === target ? "move" : target === "dock" ? "pin" : "unpin", id: d.id, pos: ids.indexOf(d.id) + 1, total: ids.length });
    live.current.onCommit({ pinned: d.hyp.dock, list: d.hyp.list });
    // If the commit renders nothing new, do not leave the rows displaced.
    settleTimer.current = window.setTimeout(() => { if (pending.current && scroller) settle(scroller); }, 400);
  }

  function revert(d: Drag) {
    zone?.removeAttribute("data-dnd-over");
    d.el.classList.add(SETTLING);
    d.els.forEach(el => { el.style.transform = ""; });
    window.setTimeout(() => {
      d.els.forEach(el => el.classList.remove(DRAGGING, SETTLING, LIVE));
      zone?.removeAttribute("data-dnd");
    }, d.reduced ? 0 : MOTION.panelIn);
    // The pane stays in drag mode for the glide back; drop mode (the pin zone) closes at once.
    zone?.setAttribute("data-dnd", "settling");
  }

  function onClickCapture(event: MouseEvent) {
    if (performance.now() < swallowUntil) { event.preventDefault(); event.stopPropagation(); }
  }
  function onContextMenuCapture(event: MouseEvent) {
    // A long press fires the native menu on some phones; the press owns that gesture.
    if (drag || (down && down.type === "touch")) { event.preventDefault(); event.stopPropagation(); }
  }
  function onDragStart(event: Event) { if ((event.target as Element).closest("[data-dnd-id]")) event.preventDefault(); }

  function onKeyDown(event: KeyboardEvent) {
    // The Menu key and Shift+F10 open the row's menu; some platforms never send a contextmenu event for them.
    if (event.key === "ContextMenu" || (event.key === "F10" && event.shiftKey)) {
      const target = (event.target as Element).closest<HTMLElement>("[data-dnd-id]");
      if (!target || !scroller!.contains(target)) return;
      const r = target.getBoundingClientRect();
      event.preventDefault();
      live.current.onMenu(target.dataset.dndId!, r.left + Math.min(40, r.width / 2), r.top + r.height / 2);
      return;
    }
    if (!event.altKey || event.ctrlKey || event.metaKey || drag) return;
    const el = (event.target as Element).closest<HTMLElement>("[data-dnd-id]");
    if (!el || !scroller!.contains(el)) return;
    const zone = el.dataset.dndZone as Zone;
    const step = ({ ArrowUp: -1, ArrowLeft: -1, ArrowDown: 1, ArrowRight: 1 } as Record<string, number>)[event.key];
    if (!step || (zone === "list" && (event.key === "ArrowLeft" || event.key === "ArrowRight"))) return;
    event.preventDefault();
    const where = idsOf();
    const ids = where[zone];
    const from = ids.indexOf(el.dataset.dndId!);
    const to = from + step;
    if (from < 0 || to < 0 || to >= ids.length) return;
    const next = { ...where, [zone]: moveItem(ids, from, to) } as Where;
    const keyboard = new Map<string, Rect>();
    scroller!.querySelectorAll<HTMLElement>("[data-dnd-zone='dock']").forEach(tile => { const r = tile.getBoundingClientRect(); keyboard.set(tile.dataset.dndId!, { left: r.left, top: r.top, width: r.width, height: r.height }); });
    pending.current = { id: el.dataset.dndId!, source: zone, target: zone, reduced: reducedMotion(), keyboard };
    live.current.onAnnounce({ kind: "move", id: el.dataset.dndId!, pos: to + 1, total: ids.length });
    live.current.onCommit({ pinned: next.dock, list: next.list });
    settleTimer.current = window.setTimeout(() => { if (pending.current && scroller) settle(scroller); }, 400);
  }

  scroller.addEventListener("pointerdown", onDown);
  window.addEventListener("click", onClickCapture, true);
  scroller.addEventListener("contextmenu", onContextMenuCapture, true);
  scroller.addEventListener("dragstart", onDragStart);
  scroller.addEventListener("keydown", onKeyDown);
  scroller.dataset.dndReady = "1";
  return () => {
    delete scroller.dataset.dndReady;
    scroller.removeEventListener("pointerdown", onDown);
    window.removeEventListener("click", onClickCapture, true);
    scroller.removeEventListener("contextmenu", onContextMenuCapture, true);
    scroller.removeEventListener("dragstart", onDragStart);
    scroller.removeEventListener("keydown", onKeyDown);
    if (down?.timer) window.clearTimeout(down.timer);
    if (raf) cancelAnimationFrame(raf);
    removeListeners();
    if (drag) { drag.el.style.transform = ""; drag.el.classList.remove(DRAGGING); drag.els.forEach(el => { el.style.transform = ""; }); }
    zone?.removeAttribute("data-dnd");
  };

}
