import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "./ds/Collapsible/Collapsible";
import { useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { sortWidgets, WIDGET_ORDER, type WidgetId } from "./surfaces";
import { useLeaving } from "./surfacePresence";
import "./context-column.css";

const TITLE_KEY = { bot: "botCard.title", computer: "contextCard.title.desktop", terminal: "contextCard.title.terminal", conversation: "contextCard.title.conversation", artifacts: "contextCard.title.artifacts" } as const;

/**
 * One widget in the column inside the chat (docs/design/widgets-and-sidebar.md): title on the left; fold and close on
 * the right. A folded widget keeps its content mounted (a live computer session must survive it) and only hides it.
 * `leaving` is set while the widget plays its exit; the card stays in place until the motion is over.
 */
export function ContextCard({ id, order, collapsed = false, leaving = false, focusN = 0, onToggle, onClose, children }: {
  id: WidgetId; order: number; collapsed?: boolean; leaving?: boolean; focusN?: number; onToggle: (id: string) => void; onClose: (id: string) => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const title = t(TITLE_KEY[id]);
  const ref = useRef<HTMLElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
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
  return <div className="widget-slot" data-slot={id} data-leaving={leaving || undefined} inert={leaving || undefined} style={{ order }}><Collapsible.Root open={bare || !collapsed} onOpenChange={() => onToggle(id)} asChild><section ref={ref} className={`context-card${collapsed && id !== "computer" ? " is-collapsed" : ""}${bare ? " is-bare" : ""}`} data-card={id} aria-label={title}>
    {bare ? <h2 ref={heading} tabIndex={-1} className="sr-only">{title}</h2> : <header className="context-card-header">
      {/* The title is the fold control: "Title ⌄", the chevron right after the words, turning as the card folds. */}
      <h2 ref={heading} tabIndex={-1}><Collapsible.Trigger asChild><button type="button" className="context-card-fold" aria-label={foldLabel} data-hint={foldLabel}><span>{title}</span><Icon name="chevron-down" size={14} /></button></Collapsible.Trigger></h2>
      <span className="context-card-actions">
      <button type="button" className="context-card-button context-card-close" aria-label={closeLabel} data-hint={closeLabel} onClick={() => onClose(id)}><Icon name="close" size={16} /></button></span>
    </header>}
    <CollapsibleContent forceMount className="context-card-body" inert={!bare && collapsed || undefined} aria-hidden={!bare && collapsed || undefined}><div className="context-card-inner">{children}</div></CollapsibleContent>
  </section></Collapsible.Root></div>;
}

/** The fixed order: the Bot card, the computer, the Terminal, the read-only chat, Artifacts. The DOM keeps that order (a live video must not move), CSS `order` places the cards. */
export function WidgetColumn({ ids, folded, focus, covered, onFold, onClose, render }: {
  ids: readonly WidgetId[]; folded: readonly WidgetId[]; focus: { id: string | null; n: number }; covered: boolean;
  onFold: (id: string) => void; onClose: (id: WidgetId) => void; render: (id: WidgetId, leaving: boolean) => ReactNode;
}) {
  const { t } = useTranslation("chat");
  const { shown, leaving } = useLeaving(ids, WIDGET_ORDER, 220);
  const visual = sortWidgets(shown);
  if (!shown.length) return null;
  return <aside className={`detail-pane context-panel context-column widget-column visible${covered ? " is-covered" : ""}`} aria-label={t("detail.label")}>
    {shown.map(id => { const node = render(id, leaving.has(id)); if (node && id === "bot") return <div key={id} className="widget-slot bot-slot" data-slot="bot" data-leaving={leaving.has(id) || undefined} inert={leaving.has(id) || undefined} style={{ order: visual.indexOf(id) }}>{node}</div>; return node && <ContextCard key={id} id={id} order={visual.indexOf(id)} collapsed={folded.includes(id)} leaving={leaving.has(id)} focusN={focus.id === id ? focus.n : 0} onToggle={onFold} onClose={onClose as (id: string) => void}>{node}</ContextCard>; })}
  </aside>;
}
