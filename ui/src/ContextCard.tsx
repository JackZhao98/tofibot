import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { sortWidgets, WIDGET_ORDER, type WidgetId } from "./surfaces";
import { useLeaving } from "./surfacePresence";
import { WidgetGrip, WidgetGripContext, type WidgetGripApi } from "./widgetGrip";
import "./context-column.css";

const TITLE_KEY = { computer: "contextCard.title.desktop", members: "contextCard.title.members", artifacts: "contextCard.title.artifacts", usage: "contextCard.title.usage" } as const;

/**
 * One widget in the column inside the chat (docs/design/widgets-and-sidebar.md): title on the left; fold and close on
 * the right. A folded widget keeps its content mounted (a live computer session must survive it) and only hides it.
 * `leaving` is set while the widget plays its exit; the card stays in place until the motion is over.
 */
export function ContextCard({ id, order, collapsed = false, leaving = false, focusN = 0, grip, onToggle, onClose, children }: {
  id: WidgetId; order: number; collapsed?: boolean; leaving?: boolean; focusN?: number; grip: WidgetGripApi; onToggle: (id: string) => void; onClose: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const title = t(TITLE_KEY[id]);
  const ref = useRef<HTMLElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const bodyId = useId();
  useEffect(() => {
    if (focusN <= 0) return;
    ref.current?.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
    // Keyboard users land on the card they opened; a pointer user who is already working inside the column keeps their place.
    const frame = requestAnimationFrame(() => {
      const pane = ref.current?.closest(".detail-pane");
      if (pane && !pane.contains(document.activeElement)) heading.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [focusN]);
  const foldLabel = t(collapsed ? "contextCard.expand" : "contextCard.collapse", { title });
  const closeLabel = t("contextCard.close", { title });
  // The computer is the bare monitor: no header row. Its title is the chin's status text (and a hidden heading for focus), it closes
  // with the chin's chevron and folds into its own strip (docs/design/widgets-and-sidebar.md section 6).
  const bare = id === "computer";
  // Dragging starts from the header (any part that is not a button), from the grip, or by pressing and moving on the title; Alt+Up / Alt+Down anywhere in the header moves it too.
  const startFromHeader = (event: ReactPointerEvent<HTMLElement>) => {
    const target = event.target as Element;
    if (!target.closest("button") && event.pointerType !== "touch") grip.onPointerDown(event);
  };
  const keyFromHeader = (event: ReactKeyboardEvent<HTMLElement>) => { if (event.altKey) grip.onKeyDown(event); };
  return <div className="widget-slot" data-slot={id} data-leaving={leaving || undefined} inert={leaving || undefined} style={{ order }}><section ref={ref} className={`context-card${collapsed ? " is-collapsed" : ""}${bare ? " is-bare" : ""}`} data-card={id} aria-label={title}>
    {bare ? <h2 ref={heading} tabIndex={-1} className="sr-only">{title}</h2> : <header className="context-card-header" onPointerDown={startFromHeader} onKeyDown={keyFromHeader}>
      {/* The title is the fold control: "Title ⌄", the chevron right after the words, turning as the card folds. A press on it that travels
          (or is held) is a drag of the card instead (WidgetGripApi.onPress), so a click still folds. */}
      <h2 ref={heading} tabIndex={-1}><button type="button" className="context-card-fold" aria-expanded={!collapsed} aria-controls={bodyId} aria-label={foldLabel} data-hint={foldLabel}
        onPointerDown={grip.onPress} onClick={() => { if (!grip.wasDragged()) onToggle(id); }}><span>{title}</span><Icon name="chevron-down" size={14} /></button></h2>
      <span className="context-card-actions"><WidgetGrip grip={grip} className="context-card-button context-card-grip" />
      <button type="button" className="context-card-button context-card-close" aria-label={closeLabel} data-hint={closeLabel} onClick={() => onClose(id)}><Icon name="close" size={16} /></button></span>
    </header>}
    <div id={bodyId} className="context-card-body" inert={collapsed || undefined}><div className="context-card-inner">{bare ? <WidgetGripContext.Provider value={grip}>{children}</WidgetGripContext.Provider> : children}</div></div>
  </section></div>;
}

type Drag = { id: WidgetId; startY: number; pointerY: number; grabDy: number; startIndex: number; moved: boolean };
/** Press-to-drag on a surface that also clicks (the computer's screen): travel beyond the slop starts a drag; held, a smaller move does. */
const PRESS_SLOP = 6, PRESS_MS = 350, PRESS_TOUCH_MS = 400;
const translateY = (el: HTMLElement) => parseFloat(getComputedStyle(el).translate.split(" ")[1] ?? "0") || 0;

/**
 * The column of widgets inside the chat: the open ones in the person's order (`order`), plus the ones playing their exit (the column
 * itself goes when the last one has left). `render` returns a widget's content; null skips it.
 *
 * Nodes are drawn in the fixed default order and placed with CSS `order`: moving a node in the DOM would stop a playing video, so a
 * reorder never touches the tree. A move is a state change (`onMove`), and the slots that changed place slide there (FLIP on the
 * `translate` property with the motion tokens; reduced motion is the stylesheet's instant). The dragged slot follows the pointer, and the
 * others make room live as it crosses their middles. Keyboard: Up / Down on the grip (Alt+Up / Alt+Down anywhere in a header), announced.
 */
export function WidgetColumn({ ids, order, folded, focus, covered, onFold, onClose, onMove, render }: {
  ids: readonly WidgetId[]; order: readonly WidgetId[]; folded: readonly WidgetId[]; focus: { id: string | null; n: number }; covered: boolean;
  onFold: (id: string) => void; onClose: (id: WidgetId) => void; onMove: (id: WidgetId, to: number) => void; render: (id: WidgetId, leaving: boolean) => ReactNode;
}) {
  const { t } = useTranslation("chat");
  const { shown, leaving } = useLeaving(ids, WIDGET_ORDER, 220);
  const visual = sortWidgets(shown, order);
  const live = visual.filter(id => !leaving.has(id));
  const colRef = useRef<HTMLElement>(null);
  const [announce, setAnnounce] = useState("");
  const latest = useRef({ live, onMove, t });
  latest.current = { live, onMove, t };
  const drag = useRef<Drag | null>(null);
  const before = useRef<Map<string, number> | null>(null);
  const spoke = useRef(0);
  const justDragged = useRef(false);

  const slotOf = (id: string) => colRef.current?.querySelector<HTMLElement>(`.widget-slot[data-slot="${id}"]`) ?? null;
  const say = (id: WidgetId, index: number, count: number) => setAnnounce(`${latest.current.t("widgetOrder.moved", { title: latest.current.t(TITLE_KEY[id]), position: index + 1, count })}${spoke.current++ % 2 ? "\u200b" : ""}`);
  /** Remembers where every slot is on screen right now (its layout place plus any slide still running), before a move changes the layout. */
  const snapshot = () => {
    const map = new Map<string, number>();
    colRef.current?.querySelectorAll<HTMLElement>(".widget-slot").forEach(el => map.set(el.dataset.slot ?? "", el.offsetTop + translateY(el)));
    before.current = map;
  };
  const moveTo = (id: WidgetId, to: number) => {
    const { live: now, onMove: move } = latest.current;
    const from = now.indexOf(id);
    if (from < 0 || from === to || to < 0 || to >= now.length) return false;
    snapshot();
    move(id, to);
    return true;
  };

  /** Puts the dragged slot under the pointer and moves it to the place its middle has reached. */
  const follow = () => {
    const d = drag.current, col = colRef.current, el = d && slotOf(d.id);
    if (!d || !d.moved || !col || !el) return;
    const top = d.pointerY - col.getBoundingClientRect().top + col.scrollTop - d.grabDy;
    el.style.translate = `0 ${top - el.offsetTop}px`;
    const middle = top + el.offsetHeight / 2;
    let index = 0;
    for (const other of latest.current.live) { if (other === d.id) continue; const node = slotOf(other); if (node && node.offsetTop + node.offsetHeight / 2 < middle) index++; }
    moveTo(d.id, index);
  };

  // After a move: the slots that changed place start from where they were and slide to where they are; the dragged one is not slid.
  const key = live.join();
  useLayoutEffect(() => {
    const prev = before.current;
    before.current = null;
    if (prev) colRef.current?.querySelectorAll<HTMLElement>(".widget-slot").forEach(el => {
      const was = prev.get(el.dataset.slot ?? "");
      if (was === undefined || el.hasAttribute("data-dragging") || el.hasAttribute("data-leaving")) return;
      const delta = was - el.offsetTop;
      if (!delta) return;
      el.style.transition = "none";
      el.style.translate = `0 ${delta}px`;
      void el.offsetHeight;
      el.style.transition = "";
      el.style.translate = "";
    });
    follow();
  }, [key]);

  const startDrag = useCallback((id: WidgetId, event: ReactPointerEvent<HTMLElement>, press = false) => {
    const el = slotOf(id);
    if (event.button !== 0 || drag.current || !el) return;
    justDragged.current = false;
    if (!press && !(event.target as Element).closest("button")) event.preventDefault();
    const touch = event.pointerType === "touch";
    const startX = event.clientX;
    let held = false;
    // A press that also clicks: held long enough it is a drag-in-waiting (a touch must be held, so a swipe still scrolls the column).
    const hold = press ? window.setTimeout(() => { held = true; }, touch ? PRESS_TOUCH_MS : PRESS_MS) : 0;
    const d: Drag = { id, startY: event.clientY, pointerY: event.clientY, grabDy: event.clientY - el.getBoundingClientRect().top, startIndex: latest.current.live.indexOf(id), moved: false };
    drag.current = d;
    const finish = (cancel: boolean) => {
      window.clearTimeout(hold);
      window.removeEventListener("touchmove", onTouchMove);
      window.removeEventListener("pointermove", onMoveEvent);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onCancel);
      document.removeEventListener("keydown", onKey, true);
      drag.current = null;
      document.documentElement.removeAttribute("data-widget-dragging");
      const node = slotOf(id);
      if (!d.moved) return;
      justDragged.current = true;
      window.setTimeout(() => { justDragged.current = false; }, 400);
      if (cancel && latest.current.live.indexOf(id) !== d.startIndex) moveTo(id, d.startIndex);
      node?.removeAttribute("data-dragging");
      if (node) node.style.translate = "";
      const index = latest.current.live.indexOf(id);
      if (!cancel && index !== d.startIndex) say(id, index, latest.current.live.length);
    };
    const onMoveEvent = (e: PointerEvent) => {
      d.pointerY = e.clientY;
      if (!d.moved) {
        if (press) {
          const dist = Math.hypot(e.clientX - startX, e.clientY - d.startY);
          if (touch && !held) { if (dist > PRESS_SLOP) finish(true); return; }
          if (dist < (held ? 2 : PRESS_SLOP)) return;
        } else if (Math.abs(e.clientY - d.startY) < 4) return;
        d.moved = true;
        slotOf(id)?.setAttribute("data-dragging", "");
        document.documentElement.setAttribute("data-widget-dragging", "");
      }
      follow();
    };
    // A held touch that starts moving is the drag: keep the page and the column from scrolling under it.
    const onTouchMove = (e: TouchEvent) => { if (d.moved || held) e.preventDefault(); };
    const onUp = () => finish(false);
    const onCancel = () => finish(true);
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); finish(true); } };
    window.addEventListener("pointermove", onMoveEvent);
    if (touch && press) window.addEventListener("touchmove", onTouchMove, { passive: false });
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onCancel);
    document.addEventListener("keydown", onKey, true);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => () => document.documentElement.removeAttribute("data-widget-dragging"), []);

  const keyMove = useCallback((id: WidgetId, event: ReactKeyboardEvent<HTMLElement>) => {
    const step = event.key === "ArrowUp" ? -1 : event.key === "ArrowDown" ? 1 : event.key === "Home" ? -Infinity : event.key === "End" ? Infinity : 0;
    if (!step || (!(event.target as Element).closest(".widget-grip") && !event.altKey)) return;
    event.preventDefault();
    event.stopPropagation();
    const now = latest.current.live;
    const from = now.indexOf(id);
    const to = Math.max(0, Math.min(now.length - 1, from + step));
    if (to === from) { setAnnounce(`${latest.current.t(from === 0 ? "widgetOrder.at_top" : "widgetOrder.at_bottom", { title: latest.current.t(TITLE_KEY[id]) })}${spoke.current++ % 2 ? "\u200b" : ""}`); return; }
    moveTo(id, to);
    say(id, to, now.length);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const grips = useMemo(() => Object.fromEntries(WIDGET_ORDER.map(id => {
    const title = t(TITLE_KEY[id]);
    return [id, { label: t("widgetOrder.grip", { title }), hint: t("widgetOrder.grip_hint"), onPointerDown: (event: ReactPointerEvent<HTMLElement>) => startDrag(id, event), onPress: (event: ReactPointerEvent<HTMLElement>) => startDrag(id, event, true), wasDragged: () => justDragged.current, onKeyDown: (event: ReactKeyboardEvent<HTMLElement>) => keyMove(id, event) } satisfies WidgetGripApi];
  })) as Record<WidgetId, WidgetGripApi>, [t, startDrag, keyMove]);

  if (!shown.length) return null;
  return <aside ref={colRef} className={`detail-pane context-panel context-column widget-column visible${covered ? " is-covered" : ""}`} aria-label={t("detail.label")}>
    {shown.map(id => { const node = render(id, leaving.has(id)); return node && <ContextCard key={id} id={id} order={visual.indexOf(id)} collapsed={id !== "computer" && folded.includes(id)} leaving={leaving.has(id)} focusN={focus.id === id ? focus.n : 0} grip={grips[id]} onToggle={onFold} onClose={onClose as (id: string) => void}>{node}</ContextCard>; })}
    <span className="sr-only" role="status" aria-live="polite">{announce}</span>
  </aside>;
}
