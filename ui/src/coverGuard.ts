import { createContext, useContext, useEffect, useRef } from "react";

/**
 * What a cover tells the content inside it (docs/design/widgets-and-sidebar.md section 5.2): whether it is on its way out, how to close
 * it, and where focus goes back to. Lazy chunks only; CoverShell provides it, NewGroupDialog (a cover without the window frame) reads it.
 */
export type CoverState = { leaving: boolean; close: () => void; returnFocus?: HTMLElement | null };
export const CoverContext = createContext<CoverState>({ leaving: false, close: () => {} });
export const useCover = () => useContext(CoverContext);

/**
 * The close guard of the cover that is open. A cover with an edit form in it registers whether the form is dirty and what to ask;
 * every way out (the close button, Escape, a press on the scrim) goes through `requestCoverClose`, which asks first when there is
 * something to lose. One cover is open at a time, so one module-level slot is enough.
 */
type Guard = { dirty: boolean; ask: () => void };
let current: Guard | null = null;

export function requestCoverClose(close: () => void) {
  if (current?.dirty) current.ask();
  else close();
}

export function useCoverGuard(dirty: boolean, ask: () => void) {
  const guard = useRef<Guard>({ dirty, ask });
  guard.current.dirty = dirty;
  guard.current.ask = ask;
  useEffect(() => {
    const mine = guard.current;
    current = mine;
    return () => { if (current === mine) current = null; };
  }, []);
}

/** Where a surface opened from inside a cover is portaled. The cover is in `.workspace` (its panels are styled under it), and Radix hides
 * the layer root from assistive tech while the cover is open, so a popover or a question for the cover goes in there too. (A dialog the
 * workspace itself opens, such as the delete question, cannot: the workspace turns inert under it. The cover closes first.) */
export const coverContainer = () => document.querySelector<HTMLElement>(".workspace");
