// The entry-side half of TOFI's menus and popovers (docs/design/migration.md step 2). Radix lives only in the lazy
// RadixOverlays chunk, fetched in idle time; the entry carries these two thin wrappers and one import() stub. Each caller
// keeps its own open state next to its trigger and renders the wrapper only while the surface is open or leaving, so a
// closed menu costs nothing per render. Triggers stay TOFI's own <button>s: no component is added to a row.
import { useEffect, useState, useSyncExternalStore, type HTMLAttributes, type KeyboardEvent, type ReactNode, type RefObject } from "react";

type Placement = { side?: "top" | "right" | "bottom" | "left"; align?: "start" | "center" | "end"; sideOffset?: number; alignOffset?: number };
type Measurable = { getBoundingClientRect(): DOMRect };
export type OverlayMenuProps = Placement & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Class of the menu surface: the existing class keeps every selector and style. */
  className: string;
  id?: string;
  label?: string;
  /** TOFI's own trigger button: focus returns here, and pressing it while open toggles instead of dismiss-then-reopen. */
  trigger?: RefObject<HTMLElement | null>;
  /** The box the menu sits against (defaults to the trigger); mirrored by an invisible anchor beside it. */
  anchor?: RefObject<HTMLElement | null>;
  /** Or a viewport point, for context menus. */
  point?: { x: number; y: number };
  children: ReactNode;
};
export type OverlayPopoverProps = Placement & Omit<HTMLAttributes<HTMLElement>, "children"> & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  className: string;
  anchor: RefObject<Measurable | null>;
  /** Move focus into the surface on open (the first control). Hover cards leave focus where it is. */
  takeFocus?: boolean;
  /** Close when anything outside the surface scrolls or the window resizes. */
  closeOnScroll?: boolean;
  children: ReactNode;
};

type Radix = typeof import("./RadixOverlays");
let radix: Radix | undefined;
const load = () => import("./RadixOverlays").then(module => (radix = module));
/** The Radix module once fetched. No Suspense: React holds a revealed boundary back for ~300 ms, which a menu cannot afford.
 * Warm (the usual case) the menu renders in the same commit as the click; cold it renders as soon as the chunk arrives. */
function useRadix() {
  const [, loaded] = useState(0);
  useEffect(() => { if (!radix) void load().then(() => loaded(1)); }, []);
  return radix;
}

/** Radix DropdownMenu (non-modal) on TOFI's markup. Children are the existing items; every <button> becomes a menu item. */
export function OverlayMenu(props: OverlayMenuProps) { const module = useRadix(); return module ? <module.Menu {...props} /> : null; }
/** Radix Popover (non-modal) anchored to an element or a virtual rect. */
export function OverlayPopover(props: OverlayPopoverProps) { const module = useRadix(); return module ? <module.Popover {...props} /> : null; }
/** State for a surface its owner sets but never reads while rendering (the sidebar row menu): opening or closing it
 * re-renders only the <SlotView>, not the whole workspace around it (D02). */
export class OverlaySlot<T> {
  private value: T | null = null;
  private listeners = new Set<() => void>();
  get = () => this.value;
  set = (value: T | null) => { this.value = value; this.listeners.forEach(listener => listener()); };
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
}
export function SlotView<T>({ slot, children }: { slot: OverlaySlot<T>; children: (value: T) => ReactNode }) {
  const value = useSyncExternalStore(slot.subscribe, slot.get);
  return value === null ? null : children(value);
}
/** ArrowDown on a menu button opens the menu (Enter and Space already click it); the menu then focuses its first item. */
export const arrowOpens = (open: (value: boolean) => void) => (event: KeyboardEvent) => { if (event.key === "ArrowDown") { event.preventDefault(); open(true); } };

// The first open usually finds the chunk already here: same idle-time fetch as the sidebar drag engine.
const idle = (window as { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number }).requestIdleCallback;
if (idle) idle(() => void load(), { timeout: 1500 }); else window.setTimeout(() => void load(), 600);
