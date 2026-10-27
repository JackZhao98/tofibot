import { useEffect, useId, useRef, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { WIDGET_ORDER, type WidgetId } from "./surfaces";
import { useLeaving } from "./surfacePresence";
import "./context-column.css";

/**
 * One widget in the column inside the chat (docs/design/widgets-and-sidebar.md): title on the left; fold and close on
 * the right. A folded widget keeps its content mounted (a live computer session must survive it) and only hides it.
 * `leaving` is set while the widget plays its exit; the card stays in place until the motion is over.
 */
export function ContextCard({ id, order, collapsed = false, leaving = false, focusN = 0, onToggle, onClose, children }: {
  id: string; order: number; collapsed?: boolean; leaving?: boolean; focusN?: number; onToggle: (id: string) => void; onClose: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const title = t(id === "computer" ? "contextCard.title.desktop" : id === "members" ? "contextCard.title.members" : "contextCard.title.artifacts");
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
  return <div className="widget-slot" data-leaving={leaving || undefined} inert={leaving || undefined} style={{ order }}><section ref={ref} className={`context-card${collapsed ? " is-collapsed" : ""}${id === "computer" ? " is-bare" : ""}`} data-card={id} aria-label={title}>
    <header className="context-card-header">
      <h2 ref={heading} tabIndex={-1}>{title}</h2>
      <button type="button" className="context-card-button context-card-fold" aria-expanded={!collapsed} aria-controls={bodyId} aria-label={foldLabel} data-hint={foldLabel} onClick={() => onToggle(id)}><Icon name="chevron-down" size={16} /></button>
      <button type="button" className="context-card-button" aria-label={closeLabel} data-hint={closeLabel} onClick={() => onClose(id)}><Icon name="close" size={16} /></button>
    </header>
    <div id={bodyId} className="context-card-body" inert={collapsed || undefined}><div className="context-card-inner">{children}</div></div>
  </section></div>;
}

/**
 * The column of widgets inside the chat: the open ones in the fixed order, plus the ones playing their exit (the column itself
 * goes when the last one has left). `render` returns a widget's content; null skips it.
 */
export function WidgetColumn({ ids, folded, focus, covered, onFold, onClose, render }: {
  ids: readonly WidgetId[]; folded: readonly WidgetId[]; focus: { id: string | null; n: number }; covered: boolean;
  onFold: (id: string) => void; onClose: (id: WidgetId) => void; render: (id: WidgetId, leaving: boolean) => ReactNode;
}) {
  const { t } = useTranslation("chat");
  const { shown, leaving } = useLeaving(ids, WIDGET_ORDER, 220);
  if (!shown.length) return null;
  return <aside className={`detail-pane context-panel context-column widget-column visible${covered ? " is-covered" : ""}`} aria-label={t("detail.label")}>
    {shown.map(id => { const node = render(id, leaving.has(id)); return node && <ContextCard key={id} id={id} order={WIDGET_ORDER.indexOf(id)} collapsed={folded.includes(id)} leaving={leaving.has(id)} focusN={focus.id === id ? focus.n : 0} onToggle={onFold} onClose={onClose as (id: string) => void}>{node}</ContextCard>; })}
  </aside>;
}
