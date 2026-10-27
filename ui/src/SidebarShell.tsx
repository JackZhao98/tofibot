import { useEffect, useRef, type ReactNode } from "react";
import { usePreviousWhileFading } from "./surfacePresence";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import type { SidebarId } from "./surfaces";
import "./sidebar-shell.css";

/**
 * The frame of the sidebar (docs/design/widgets-and-sidebar.md section 2.2): a 15px title, an expand control and close.
 * Only the wide web layout draws it (the narrow drawer lets each panel keep its own heading and close). The body is the
 * layer stack the workspace fills: the panel that is arriving fades in over the one that is leaving with --m-quick, and
 * the track keeps its width while that happens.
 */
export function SidebarShell({ id, expanded, focusN = 0, onExpand, onClose, content }: {
  id: SidebarId; expanded: boolean; focusN?: number; onExpand: () => void; onClose: () => void; content: (id: SidebarId) => ReactNode;
}) {
  const fading = usePreviousWhileFading(id, 180);
  const { t } = useTranslation("chat");
  const title = t(id === "tasks" ? "contextCard.title.schedule" : id === "memory" ? "contextCard.title.memory" : id === "terminal" ? "contextCard.title.terminal" : id === "archive" ? "contextCard.title.archive" : "viewOnly.label");
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusN <= 0) return;
    // Keyboard users land on the title of what they opened; a pointer user working inside the sidebar keeps their place.
    const frame = requestAnimationFrame(() => {
      const pane = heading.current?.closest(".sidebar-pane");
      if (pane && !pane.contains(document.activeElement)) heading.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [focusN]);
  const expandLabel = t(expanded ? "sidebar.collapse_panel" : "sidebar.expand_panel", { title });
  return <section className="sidebar-shell" data-sidebar={id} aria-label={title}>
    <header className="sidebar-shell-header">
      <h2 key={id} ref={heading} tabIndex={-1}>{title}</h2>
      <button type="button" className="context-card-button sidebar-shell-expand" aria-pressed={expanded} aria-label={expandLabel} data-hint={expandLabel} onClick={onExpand}><Icon name={expanded ? "chevron-right" : "chevron-left"} size={16} /></button>
      <button type="button" className="context-card-button" aria-label={t("sidebar.close_panel", { title })} data-hint={t("sidebar.close_panel", { title })} onClick={onClose}><Icon name="close" size={16} /></button>
    </header>
    <div className="sidebar-shell-body">
      {fading && <div key={`out-${fading}`} className="sidebar-layer" data-leaving="" aria-hidden="true" inert>{content(fading)}</div>}
      <div key={id} className={`sidebar-layer${id === "terminal" || id === "conversation" ? " is-fill" : ""}`}>{content(id)}</div>
    </div>
  </section>;
}
