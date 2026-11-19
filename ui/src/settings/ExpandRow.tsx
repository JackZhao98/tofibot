import * as Collapsible from "@radix-ui/react-collapsible";
import { useCallback, useState, type ReactNode } from "react";
import { CollapsibleContent } from "../ds/Collapsible/Collapsible";
import { Icon } from "../ds/Icon/Icon";
import { cx } from "../ds/cx";
import { revealTarget, useReveal } from "./subpages";
import "./general.css";

/**
 * A folded row in a settings card: a chevron, the name and a one-line summary; the rows open under it. Closed content is not mounted.
 * `reveal` names the section a link can target: the row opens when a link to it arrives (see settings/subpages.tsx).
 */
export function ExpandRow({ label, summary, mono = false, attention = false, defaultOpen = false, reveal, children, ...data }: { label: ReactNode; defaultOpen?: boolean; summary?: ReactNode; /** The summary is an address. */ mono?: boolean; /** The summary names a problem: it reads as one. */ attention?: boolean; reveal?: { tab: string; section: string }; children: ReactNode; "data-setting"?: string }) {
  const [open, setOpen] = useState(() => defaultOpen || Boolean(reveal && revealTarget(reveal.tab) === reveal.section));
  useReveal(reveal?.tab ?? "", useCallback((section: string) => { if (reveal && section === reveal.section) setOpen(true); }, [reveal?.section])); // eslint-disable-line react-hooks/exhaustive-deps
  return <Collapsible.Root className="gen-expand" open={open} onOpenChange={setOpen} {...data}>
    <Collapsible.Trigger className="gen-expand-toggle">
      <Icon name="chevron-right" size={16} className="gen-expand-chev" />
      <span className="gen-expand-label">{label}</span>
      {summary ? <span className={cx("gen-expand-summary", mono && "tb-mono", attention && "is-attention")}>{summary}</span> : null}
    </Collapsible.Trigger>
    <CollapsibleContent><div className="gen-expand-panel">{children}</div></CollapsibleContent>
  </Collapsible.Root>;
}
