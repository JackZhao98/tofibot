import * as Collapsible from "@radix-ui/react-collapsible";
import type { HTMLAttributes, ReactNode, Ref } from "react";
import { Icon } from "../Icon/Icon";
import { IconButton } from "../IconButton/IconButton";
import { CollapsibleContent } from "../Collapsible/Collapsible";
import { cx } from "../cx";
import "./Widget.css";

export type WidgetProps = Omit<HTMLAttributes<HTMLElement>, "title"> & {
  title: string;
  /** column: a card in the widget column, folds and closes. sidebar: the frame of the one sidebar, with an expand control and close. */
  form?: "column" | "sidebar";
  /** No surface and no header: the body is the widget (the Bot computer). Its title stays for the screen reader. */
  bare?: boolean;
  folded?: boolean;
  onFold?: () => void;
  foldLabel?: string;
  onClose?: () => void;
  closeLabel?: string;
  /** Controls before the close button (the sidebar's expand). */
  actions?: ReactNode;
  headingRef?: Ref<HTMLHeadingElement>;
  ref?: Ref<HTMLElement>;
};

/** A panel in the widget column, or the frame of the sidebar. At rest the column header is "Title ⌄": the title folds the card, the
 *  close X shows on hover or keyboard focus. A folded card keeps its content mounted (a live computer session survives it). The
 *  hook classes `context-card*` and `sidebar-shell*` carry no rule here: tests and the layout sheets place by them. */
export function Widget({ title, form = "column", bare, folded, onFold, foldLabel, onClose, closeLabel, actions, headingRef, ref, className, children, ...rest }: WidgetProps) {
  if (form === "sidebar") return <section {...rest} ref={ref} className={cx("tb-widget", "tb-widget--sidebar", "sidebar-shell", className)} aria-label={title}>
    <header className="tb-widget-head sidebar-shell-header">
      <h2 key={title} ref={headingRef} tabIndex={-1} className="tb-widget-title">{title}</h2>
      {actions}
      {onClose && <IconButton size="sm" icon="close" label={closeLabel ?? ""} onClick={onClose} />}
    </header>
    <div className="tb-widget-body sidebar-shell-body">{children}</div>
  </section>;
  return <Collapsible.Root open={bare || !folded} onOpenChange={() => onFold?.()} asChild>
    <section {...rest} ref={ref} className={cx("tb-widget", bare && "is-bare", folded && !bare && "is-folded", "context-card", folded && !bare && "is-collapsed", className)} aria-label={title}>
      {bare ? <h2 ref={headingRef} tabIndex={-1} className="sr-only">{title}</h2> : <header className="tb-widget-head context-card-header">
        <h2 ref={headingRef} tabIndex={-1} className="tb-widget-title">
          <Collapsible.Trigger asChild><button type="button" className="tb-widget-fold context-card-fold" aria-label={foldLabel} data-hint={foldLabel}><span>{title}</span><Icon name="chevron-down" size={14} /></button></Collapsible.Trigger>
        </h2>
        {onClose && <span className="tb-widget-ctl context-card-actions"><IconButton size="sm" className="context-card-close" icon="close" label={closeLabel ?? ""} onClick={onClose} /></span>}
      </header>}
      <CollapsibleContent forceMount className="tb-widget-body context-card-body" inert={!bare && folded || undefined} aria-hidden={!bare && folded || undefined}><div className="tb-widget-inner context-card-inner">{children}</div></CollapsibleContent>
    </section>
  </Collapsible.Root>;
}
