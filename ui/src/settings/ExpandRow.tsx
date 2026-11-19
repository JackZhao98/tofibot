import * as Collapsible from "@radix-ui/react-collapsible";
import { useState, type ReactNode } from "react";
import { CollapsibleContent } from "../ds/Collapsible/Collapsible";
import { Icon } from "../ds/Icon/Icon";
import { cx } from "../ds/cx";
import "./general.css";

/** A folded row in a settings card: a chevron, the name and a one-line summary; the rows open under it. Closed content is not mounted. */
export function ExpandRow({ label, summary, mono = false, defaultOpen = false, children, ...data }: { label: ReactNode; defaultOpen?: boolean; summary?: ReactNode; /** The summary is an address. */ mono?: boolean; children: ReactNode; "data-setting"?: string }) {
  const [open, setOpen] = useState(defaultOpen);
  return <Collapsible.Root className="gen-expand" open={open} onOpenChange={setOpen} {...data}>
    <Collapsible.Trigger className="gen-expand-toggle">
      <Icon name="chevron-right" size={16} className="gen-expand-chev" />
      <span className="gen-expand-label">{label}</span>
      {summary ? <span className={cx("gen-expand-summary", mono && "tb-mono")}>{summary}</span> : null}
    </Collapsible.Trigger>
    <CollapsibleContent><div className="gen-expand-panel">{children}</div></CollapsibleContent>
  </Collapsible.Root>;
}
