import { useEffect, useId, useRef, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import "./context-column.css";

/**
 * One card in the right-hand context column. With a `title` it carries its own header (title, collapse, close);
 * without one it is a plain surface for a panel that draws its own heading. A collapsed card keeps its content
 * mounted (a live computer session must survive being folded away) and only hides it.
 */
export function ContextCard({ id, title, order, collapsed = false, focusN = 0, onToggle, onClose, children }: {
  id: string; title?: string; order: number; collapsed?: boolean; focusN?: number; onToggle?: (id: string) => void; onClose?: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const ref = useRef<HTMLElement>(null);
  const bodyId = useId();
  useEffect(() => {
    if (focusN > 0) ref.current?.scrollIntoView({ block: "nearest", behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  }, [focusN]);
  return <section ref={ref} className={`context-card${title ? "" : " is-bare"}${collapsed ? " is-collapsed" : ""}`} data-card={id} style={{ order }} aria-label={title}>
    {title && <header className="context-card-header">
      <h2>{title}</h2>
      <button type="button" className="context-card-button" aria-expanded={!collapsed} aria-controls={bodyId} aria-label={t(collapsed ? "contextCard.expand" : "contextCard.collapse", { title })} title={t(collapsed ? "contextCard.expand" : "contextCard.collapse", { title })} onClick={() => onToggle?.(id)}><Icon name="chevron-down" size={16} /></button>
      <button type="button" className="context-card-button" aria-label={t("contextCard.close", { title })} title={t("contextCard.close", { title })} onClick={() => onClose?.(id)}><Icon name="close" size={16} /></button>
    </header>}
    <div id={bodyId} className="context-card-body" inert={collapsed || undefined}><div className="context-card-inner">{children}</div></div>
  </section>;
}
