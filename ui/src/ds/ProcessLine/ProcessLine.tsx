import * as Collapsible from "@radix-ui/react-collapsible";
import { CollapsibleContent } from "../Collapsible/Collapsible";
import { useState, type ReactNode } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../ruled.css";
import "./ProcessLine.css";

export type ProcessLineProps = {
  /** The section's title, the one bold thing in the work log: This round, Context, a day and a task. */
  label: ReactNode;
  /** Right edge, in mono: a duration or a count. */
  meta?: ReactNode;
  /** The Bot is working on it: the meta reads in teal. */
  live?: boolean;
  open?: boolean;
  defaultOpen?: boolean;
  onToggle?: (open: boolean) => void;
  className?: string;
  /** ToolSteps, or ruled rows of your own. */
  children?: ReactNode;
};

/** One section of a Bot's work log: a fold with its title on a ruled line and the work indented under it. Opened from a reply's
 *  hover actions or menu, in a popover or the side panel; never written into the conversation. */
export function ProcessLine({ label, meta, live, open, defaultOpen = false, onToggle, className, children }: ProcessLineProps) {
  const [inner, setInner] = useState(defaultOpen);
  const shown = open ?? inner;
  const change = (next: boolean) => { setInner(next); onToggle?.(next); };
  return <Collapsible.Root className={cx("tb-process", live && "is-live", className)} open={shown} onOpenChange={change}>
    <Collapsible.Trigger className="tb-process-head">
      <Icon name="chevron-down" size={14} className="tb-process-chev" />
      <span className="tb-process-label">{label}</span>
      {meta ? <span className="tb-process-meta">{meta}</span> : null}
    </Collapsible.Trigger>
    <CollapsibleContent forceMount inert={!shown || undefined} aria-hidden={!shown}><div className="tb-process-body">{children}</div></CollapsibleContent>
  </Collapsible.Root>;
}

export type ProcessPadProps = {
  /** The pad's head (a Widget or cover head); a dotted perforation runs under it. */
  head?: ReactNode;
  className?: string;
  children?: ReactNode;
};

/** The ruled paper the work log is written on: 28px lines and a tomato margin rule. Holds ProcessLine sections. */
export function ProcessPad({ head, className, children }: ProcessPadProps) {
  return <div className={cx("tb-pad", className)}>
    {head ? <div className="tb-pad-top">{head}</div> : null}
    <div className="tb-pad-body">{children}</div>
  </div>;
}
