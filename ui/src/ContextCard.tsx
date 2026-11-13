import { useCallback, useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { sortWidgets, WIDGET_ORDER, type WidgetId } from "./surfaces";
import { useLeaving } from "./surfacePresence";
import { WidgetGrip, WidgetGripContext, type WidgetGripApi } from "./widgetGrip";
import { SortableList, SortableStatus, useSortableItem } from "./SortableList";
import "./context-column.css";

const TITLE_KEY = { computer: "contextCard.title.desktop", members: "contextCard.title.members", artifacts: "contextCard.title.artifacts", usage: "contextCard.title.usage" } as const;

/**
 * One widget in the column inside the chat (docs/design/widgets-and-sidebar.md): title on the left; fold and close on
 * the right. A folded widget keeps its content mounted (a live computer session must survive it) and only hides it.
 * `leaving` is set while the widget plays its exit; the card stays in place until the motion is over.
 */
export function ContextCard({ id, order, collapsed = false, leaving = false, focusN = 0, moveStep, onToggle, onClose, children }: {
  id: WidgetId; order: number; collapsed?: boolean; leaving?: boolean; focusN?: number; moveStep: (step: -1 | 1) => void; onToggle: (id: string) => void; onClose: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const title = t(TITLE_KEY[id]);
  const ref = useRef<HTMLElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const bodyId = useId();
  const sortable = useSortableItem(id, leaving || (id === "computer" && collapsed), moveStep, leaving);
  const grip: WidgetGripApi = { label: t("widgetOrder.grip", { title }), hint: t("widgetOrder.grip_hint"),
    ref: sortable.setActivatorNodeRef, attributes: sortable.attributes, activator: sortable.activator, wasDragged: sortable.wasDragged };
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
  const keyFromHeader = (event: ReactKeyboardEvent<HTMLElement>) => { if (event.altKey) grip.activator.onKeyDown?.(event); };
  return <div ref={sortable.setNodeRef} className="widget-slot" data-dragging={sortable.isDragging || undefined} data-slot={id} data-leaving={leaving || undefined} inert={leaving || undefined} style={{ ...sortable.style, transition: leaving ? undefined : sortable.style.transition, order }}><section ref={ref} className={`context-card${collapsed && id !== "computer" ? " is-collapsed" : ""}${bare ? " is-bare" : ""}`} data-card={id} aria-label={title}>
    {bare ? <h2 ref={heading} tabIndex={-1} className="sr-only">{title}</h2> : <header className="context-card-header" onMouseDown={event => { if (!(event.target as Element).closest("button")) grip.activator.onMouseDown?.(event); }}
      onTouchStart={event => { if (!(event.target as Element).closest("button")) grip.activator.onTouchStart?.(event); }} onKeyDown={keyFromHeader}>
      {/* The title is the fold control: "Title ⌄", the chevron right after the words, turning as the card folds. A press on it that travels
          (or is held) is a drag of the card instead (dnd-kit activation), so a click still folds. */}
      <h2 ref={heading} tabIndex={-1}><button type="button" className="context-card-fold" aria-expanded={!collapsed} aria-controls={bodyId} aria-label={foldLabel} data-hint={foldLabel}
        {...grip.activator} onClick={() => { if (!grip.wasDragged()) onToggle(id); }}><span>{title}</span><Icon name="chevron-down" size={14} /></button></h2>
      <span className="context-card-actions"><WidgetGrip grip={grip} className="context-card-button context-card-grip" />
      <button type="button" className="context-card-button context-card-close" aria-label={closeLabel} data-hint={closeLabel} onClick={() => onClose(id)}><Icon name="close" size={16} /></button></span>
    </header>}
    <div id={bodyId} className="context-card-body" inert={!bare && collapsed || undefined}><div className="context-card-inner">{bare ? <WidgetGripContext.Provider value={grip}>{children}</WidgetGripContext.Provider> : children}</div></div>
  </section></div>;
}

/** Fixed DOM order preserves live video; CSS order and dnd-kit transforms place the cards. */
export function WidgetColumn({ ids, order, folded, focus, covered, onFold, onClose, onMove, render }: {
  ids: readonly WidgetId[]; order: readonly WidgetId[]; folded: readonly WidgetId[]; focus: { id: string | null; n: number }; covered: boolean;
  onFold: (id: string) => void; onClose: (id: WidgetId) => void; onMove: (id: WidgetId, to: number) => void; render: (id: WidgetId, leaving: boolean) => ReactNode;
}) {
  const { t } = useTranslation("chat");
  const { shown, leaving } = useLeaving(ids, WIDGET_ORDER, 220);
  const visual = sortWidgets(shown, order);
  const live = visual.filter(id => !leaving.has(id));
  const [announce, setAnnounce] = useState("");
  const spoke = useRef(0);
  const say = (id: string, index: number) => t("widgetOrder.moved", { title: t(TITLE_KEY[id as WidgetId]), position: index + 1, count: live.length });
  const dragging = useCallback((active: boolean) => {
    document.documentElement.toggleAttribute("data-widget-dragging", active);
  }, []);
  const step = (id: WidgetId, delta: -1 | 1) => {
    const from = live.indexOf(id), to = from + delta;
    if (to < 0 || to >= live.length) {
      setAnnounce(t(from === 0 ? "widgetOrder.at_top" : "widgetOrder.at_bottom", { title: t(TITLE_KEY[id]) }) + (spoke.current++ % 2 ? "\u200b" : ""));
      return;
    }
    onMove(id, to);
    setAnnounce(say(id, to) + (spoke.current++ % 2 ? "\u200b" : ""));
  };
  if (!shown.length) return null;
  return <SortableList items={live} onMove={(id, to) => onMove(id as WidgetId, to)} announce={say} instructions={t("widgetOrder.grip_hint")} onDragging={dragging}>
    <aside className={`detail-pane context-panel context-column widget-column visible${covered ? " is-covered" : ""}`} aria-label={t("detail.label")}>
      {shown.map(id => { const node = render(id, leaving.has(id)); return node && <ContextCard key={id} id={id} order={visual.indexOf(id)} collapsed={folded.includes(id)} leaving={leaving.has(id)} focusN={focus.id === id ? focus.n : 0} moveStep={delta => step(id, delta)} onToggle={onFold} onClose={onClose as (id: string) => void}>{node}</ContextCard>; })}
    </aside>
    <SortableStatus>{announce}</SortableStatus>
  </SortableList>;
}
