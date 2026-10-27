import { useEffect, useRef } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import "./outside-panel.css";

/**
 * Header of the outside panel (docs/design/side-panels.md): the title, an expand/restore button and a close button.
 * The panel's content sits below it in the same `aside`. Focus moves to the title when a view opens; the opener gets
 * it back in App when the panel closes.
 */
export function OutsideHeader({ id, title, expanded, onToggleExpand, onClose }: { id: string; title: string; expanded: boolean; onToggleExpand: () => void; onClose: () => void }) {
  const { t } = useTranslation("chat");
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const pane = heading.current?.closest(".detail-pane");
      if (pane && !pane.contains(document.activeElement)) heading.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [id]);
  const expandLabel = t(expanded ? "outsidePanel.restore" : "outsidePanel.expand");
  const closeLabel = t("outsidePanel.close", { title });
  return <header className="outside-header" data-outside-header={id}>
    <h2 ref={heading} tabIndex={-1}>{title}</h2>
    <button type="button" className="outside-button outside-expand" aria-pressed={expanded} aria-label={expandLabel} data-hint={expandLabel} onClick={onToggleExpand}><Icon name={expanded ? "arrow-right" : "arrow-left"} size={16} /></button>
    <button type="button" className="outside-button" aria-label={closeLabel} data-hint={closeLabel} onClick={onClose}><Icon name="close" size={16} /></button>
  </header>;
}
