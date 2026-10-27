import { useEffect, useId, useRef, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import "./context-column.css";

/**
 * One card in the on-demand column at the right (docs/design/side-panels.md): title on the left; fold, expand and close
 * on the right. `wide` is the card that was moved into the full-height outside panel: its header has the way back
 * (instead of fold and expand) and close. A folded card keeps its content mounted (a live computer session must
 * survive it) and only hides it; a card moved to the outside panel keeps its place in the tree, so it is not remounted.
 */
export function ContextCard({ id, title, order, collapsed = false, wide = false, focusN = 0, onToggle, onWide, onClose, children }: {
  id: string; title: string; order: number; collapsed?: boolean; wide?: boolean; focusN?: number; onToggle: (id: string) => void; onWide: (id: string) => void; onClose: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
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
  const wideLabel = t(wide ? "contextCard.back" : "contextCard.widen", { title });
  const closeLabel = t("contextCard.close", { title });
  return <section ref={ref} className={`context-card${collapsed ? " is-collapsed" : ""}${wide ? " is-wide" : ""}`} data-card={id} style={{ order }} aria-label={title}>
    <header className="context-card-header">
      <h2 ref={heading} tabIndex={-1}>{title}</h2>
      {!wide && <button type="button" className="context-card-button context-card-fold" aria-expanded={!collapsed} aria-controls={bodyId} aria-label={foldLabel} data-hint={foldLabel} onClick={() => onToggle(id)}><Icon name="chevron-down" size={16} /></button>}
      <button type="button" className="context-card-button context-card-wide" aria-pressed={wide} aria-label={wideLabel} data-hint={wideLabel} onClick={() => onWide(id)}><Icon name={wide ? "chevron-right" : "external-link"} size={16} /></button>
      <button type="button" className="context-card-button" aria-label={closeLabel} data-hint={closeLabel} onClick={() => onClose(id)}><Icon name="close" size={16} /></button>
    </header>
    <div id={bodyId} className="context-card-body" inert={collapsed || undefined}><div className="context-card-inner">{children}</div></div>
  </section>;
}
