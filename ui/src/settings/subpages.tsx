import * as Collapsible from "@radix-ui/react-collapsible";
import {useCallback, useEffect, useState, type ReactNode} from "react";
import {TofiIcon} from "../icons";

/**
 * A link to a section that sits inside a subpage or a folded group has to open that first. The shell calls `revealSection` before it
 * scrolls; subpages and folded groups listen (`useReveal`), and one that mounts after the call (a group inside a subpage) reads the
 * target it left behind (`revealTarget`). The shell clears it when the scroll is done.
 */
const REVEAL_EVENT = "tofi:settings-reveal";
let target: {tab: string; section: string} | null = null;
export const revealTarget = (tab: string) => (target && target.tab === tab ? target.section : null);
export function revealSection(tab: string, section: string | null) {
  target = section ? {tab, section} : null;
  if (section) window.dispatchEvent(new CustomEvent(REVEAL_EVENT, {detail: {tab, section}}));
}
function useReveal(tab: string, reveal: (section: string) => void) {
  useEffect(() => {
    const listen = (event: Event) => {
      const detail = (event as CustomEvent<{tab: string; section: string}>).detail;
      if (detail.tab === tab) reveal(detail.section);
    };
    window.addEventListener(REVEAL_EVENT, listen);
    return () => window.removeEventListener(REVEAL_EVENT, listen);
  }, [tab, reveal]);
}

/** Which subpage of a page is open (null: the page itself). `owner` says which subpage holds a section; a section it does not list is on the page. */
export function useSubpage<T extends string>(tab: string, owner: Record<string, T>) {
  const [sub, setSub] = useState<T | null>(() => owner[revealTarget(tab) ?? ""] ?? null);
  useReveal(tab, useCallback(section => setSub(owner[section] ?? null), [owner]));
  return [sub, setSub] as const;
}

/** A folded group of rows: one line with a title and a count; the rows show when it is opened (or when a link names it). */
export function FoldGroup({tab, id, title, summary, children}: {tab: string; id: string; title: ReactNode; summary?: ReactNode; children: ReactNode}) {
  const [open, setOpen] = useState(() => revealTarget(tab) === id);
  useReveal(tab, useCallback(section => { if (section === id) setOpen(true); }, [id]));
  return <Collapsible.Root open={open} onOpenChange={setOpen} className="st-fold" data-section={id} data-open={open || undefined}>
    <Collapsible.Trigger className="settings-row-v2 is-link is-inline">
      <div className="settings-row-copy"><strong>{title}</strong></div>
      {summary && <span className="settings-row-value">{summary}</span>}
      <TofiIcon name={open ? "chevron-down" : "chevron-right"} size={14} aria-hidden="true"/>
    </Collapsible.Trigger>
    <Collapsible.Content className="collapsible-content"><div>{children}</div></Collapsible.Content>
  </Collapsible.Root>;
}
