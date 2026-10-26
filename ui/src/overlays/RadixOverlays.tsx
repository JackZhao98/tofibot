// The lazy half of TOFI's menus and popovers: Radix DropdownMenu and Popover, non-modal, portaled into #tofi-layers.
// Loaded in idle time by OverlayHost.tsx and never part of the entry bundle (test:overlays and test:shell-budget hold it).
// One set of rules for every menu: arrows, Home/End and typeahead move between items; Enter/Space choose; Escape closes
// and returns focus to the trigger; Tab closes and moves on from the trigger; a press outside closes without eating the
// press; a keyboard open focuses the first item, a pointer open focuses the menu itself (no ring, arrows still work).
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import * as RadixPopover from "@radix-ui/react-popover";
import { Children, cloneElement, Fragment, isValidElement, useEffect, useLayoutEffect, useRef, useState, type ReactElement, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { layerRoot } from "../layers";
import { holdAvatarLife } from "../lib/tofi-avatar/index.js";
import { OverlaySubmenu, type OverlayMenuProps, type OverlayPopoverProps } from "./OverlayHost";

// Menus keep 8 px from the viewport edge (the account menu sits 10 px above the bottom), popovers 12 px as they did.
const MENU_PADDING = 8, POPOVER_PADDING = 12;
/** A surface opened from inside a modal (Settings) stacks just above it: data-layer="dialog" (interaction-system.css). */
const layerOf = (element: Element | null | undefined) => element?.closest('[aria-modal="true"]') ? "dialog" : undefined;
const byKeyboard = () => document.documentElement.dataset.input === "keyboard";
/** The cats' idle breathing waits out the frames a surface opens in, as it does for panel openings (App.tsx). */
const useQuietOpen = (open: boolean) => useLayoutEffect(() => { if (open) holdAvatarLife(320); }, [open]);

/** A press outside, Escape or Tab closes a surface; focus moving elsewhere on its own does not (as before Radix). The tail of
 * the touch long-press that opened a row menu moves focus to the row, and WebKit moves focus to a focusable ancestor such as
 * the Settings pane when the trigger is pressed again: neither is the person leaving the menu. */
const keepOnFocusMove = (event: Event) => event.preventDefault();

/** Focus goes back to `target` when closing left it nowhere (Escape, a chosen item); a press or focus elsewhere keeps its target. */
function restoreFocus(target: HTMLElement | null | undefined, outside: boolean) {
  if (outside || !target?.isConnected) return;
  const active = document.activeElement;
  if (!active || active === document.body) target.focus({ preventScroll: true });
}

/** Tab out of a menu continues from its trigger, as if the menu sat right after it in the page; inside a modal (Settings) it
 * stays in the modal and wraps, as the modal's own Tab trap would. */
function tabFrom(origin: HTMLElement | null | undefined, back: boolean) {
  if (!origin?.isConnected) return;
  if (back) { origin.focus(); return; }
  const modal = origin.closest<HTMLElement>('[aria-modal="true"]');
  const candidates = [...(modal ?? document).querySelectorAll<HTMLElement>("a[href],button,input,select,textarea,[tabindex]")].filter(element =>
    element.tabIndex >= 0 && !(element as HTMLButtonElement).disabled && !element.closest("[inert],#tofi-layers") && element.getClientRects().length > 0 && !origin.contains(element));
  const next = candidates.find(element => Boolean(origin.compareDocumentPosition(element) & Node.DOCUMENT_POSITION_FOLLOWING)) ?? (modal ? candidates[0] : undefined);
  (next ?? origin).focus();
}

type ItemProps = { children?: ReactNode; disabled?: boolean; "data-keep-open"?: string };
/** A flyout: the label is a Radix sub trigger. Escape closes only the flyout and returns to its label (Radix would close the menu). */
function Submenu({ label, icon, className, children }: { label: string; icon?: ReactNode; className: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLDivElement>(null);
  return <DropdownMenu.Sub open={open} onOpenChange={setOpen}>
    <DropdownMenu.SubTrigger ref={trigger} className="menu-subtrigger">{icon}<span>{label}</span><i className="menu-subtrigger-chevron" aria-hidden="true" /></DropdownMenu.SubTrigger>
    <DropdownMenu.Portal container={layerRoot()}>
      <DropdownMenu.SubContent className={className} aria-label={label} sideOffset={6} alignOffset={-8} collisionPadding={MENU_PADDING}
        onEscapeKeyDown={event => { event.preventDefault(); setOpen(false); trigger.current?.focus(); }}>
        {asItems(children)}
      </DropdownMenu.SubContent>
    </DropdownMenu.Portal>
  </DropdownMenu.Sub>;
}

/** Every <button> among the children (at any depth inside plain elements and fragments) becomes a Radix item on the same element. */
function asItems(children: ReactNode): ReactNode {
  return Children.map(children, child => {
    if (!isValidElement(child)) return child;
    const props = child.props as ItemProps;
    if (child.type === Fragment) return asItems(props.children);
    if (child.type === OverlaySubmenu) return <Submenu {...(props as unknown as Parameters<typeof Submenu>[0])} />;
    if (child.type === "button") {
      return <DropdownMenu.Item asChild disabled={props.disabled}
        // A press that began elsewhere (the long-press that opened a context menu) must not choose the item it ends on.
        onPointerUp={event => event.preventDefault()}
        onSelect={props["data-keep-open"] !== undefined ? event => event.preventDefault() : undefined}>{child}</DropdownMenu.Item>;
    }
    if (typeof child.type === "string" && props.children !== undefined) return cloneElement(child as ReactElement<ItemProps>, undefined, asItems(props.children));
    return child;
  });
}

/** An invisible element with the anchor's box, beside the anchor (so it scrolls with it), or fixed at a point. */
function useMirror(anchor: OverlayMenuProps["anchor"]) {
  const mirror = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => {
    const element = anchor?.current, span = mirror.current, parent = span?.offsetParent as HTMLElement | null;
    if (!element || !span) return;
    const box = element.getBoundingClientRect(), origin = parent?.getBoundingClientRect();
    const left = box.left - (origin ? origin.left + parent!.clientLeft - parent!.scrollLeft : 0);
    const top = box.top - (origin ? origin.top + parent!.clientTop - parent!.scrollTop : 0);
    Object.assign(span.style, { left: `${left}px`, top: `${top}px`, width: `${box.width}px`, height: `${box.height}px` });
  }, [anchor]);
  return mirror;
}

export function Menu({ open, onOpenChange, className, id, label, trigger, anchor, point, side = "bottom", align = "start", sideOffset = 0, alignOffset = 0, children }: OverlayMenuProps) {
  const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const returnTo = () => trigger?.current ?? opener.current;
  const outside = useRef(false);
  const mirror = useMirror(anchor ?? trigger);
  useQuietOpen(open);
  useEffect(() => { if (open) outside.current = false; }, [open]);
  const hidden = <DropdownMenu.Trigger asChild><span ref={mirror} aria-hidden="true" data-overlay-anchor=""
    style={point ? { position: "fixed", left: point.x, top: point.y, width: 0, height: 0, pointerEvents: "none" } : { position: "absolute", pointerEvents: "none", visibility: "hidden" }} /></DropdownMenu.Trigger>;
  return <DropdownMenu.Root open={open} onOpenChange={onOpenChange} modal={false}>
    {point ? createPortal(hidden, layerRoot()) : hidden}
    <DropdownMenu.Portal container={layerRoot()}>
      <DropdownMenu.Content className={className} id={id} aria-label={label} aria-labelledby={label ? undefined : trigger?.current?.id || undefined}
        side={side} align={align} sideOffset={sideOffset} alignOffset={alignOffset} collisionPadding={MENU_PADDING} data-layer={layerOf(returnTo())}
        onCloseAutoFocus={event => { event.preventDefault(); restoreFocus(returnTo(), outside.current); }}
        onFocusOutside={keepOnFocusMove}
        onInteractOutside={event => {
          if (event.defaultPrevented) return;
          // Pressing the trigger again toggles the menu closed through its own click.
          if (event.target instanceof Node && trigger?.current?.contains(event.target)) event.preventDefault();
          else outside.current = true;
        }}
        onKeyDown={event => {
          if (event.key !== "Tab") return;
          event.preventDefault();
          outside.current = true;
          onOpenChange(false);
          tabFrom(returnTo(), event.shiftKey);
        }}
        // Radix focuses the menu itself on open; after a keyboard open the first item takes it (WAI-ARIA menu button).
        onFocus={event => { if (event.target === event.currentTarget && byKeyboard()) event.currentTarget.querySelector<HTMLElement>("[role^=menuitem]:not([data-disabled])")?.focus(); }}>
        {asItems(children)}
      </DropdownMenu.Content>
    </DropdownMenu.Portal>
  </DropdownMenu.Root>;
}

export function Popover({ open, onOpenChange, className, anchor, takeFocus = true, closeOnScroll = false, side = "bottom", align = "start", sideOffset = 0, alignOffset = 0, children, ...rest }: OverlayPopoverProps) {
  const opener = useRef(document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const outside = useRef(false);
  const content = useRef<HTMLDivElement>(null);
  useQuietOpen(open);
  const change = useRef(onOpenChange);
  change.current = onOpenChange;
  useEffect(() => {
    if (!open) return;
    outside.current = false;
    if (!closeOnScroll) return;
    // Scrolling inside the surface (the emoji picker's categories) keeps it open.
    const close = (event: Event) => { if (!(event.target instanceof Node && content.current?.contains(event.target))) change.current(false); };
    window.addEventListener("scroll", close, true); window.addEventListener("resize", close);
    return () => { window.removeEventListener("scroll", close, true); window.removeEventListener("resize", close); };
  }, [open, closeOnScroll]);
  return <RadixPopover.Root open={open} onOpenChange={onOpenChange}>
    <RadixPopover.Anchor virtualRef={anchor} />
    <RadixPopover.Portal container={layerRoot()}>
      <RadixPopover.Content ref={content} className={className} side={side} align={align} sideOffset={sideOffset} alignOffset={alignOffset} collisionPadding={POPOVER_PADDING} data-layer={layerOf(opener.current)} {...rest}
        onOpenAutoFocus={event => { if (!takeFocus) event.preventDefault(); }}
        onCloseAutoFocus={event => { event.preventDefault(); restoreFocus(opener.current, outside.current); }}
        onFocusOutside={keepOnFocusMove}
        onInteractOutside={event => { if (!event.defaultPrevented) outside.current = true; }}>
        {children}
      </RadixPopover.Content>
    </RadixPopover.Portal>
  </RadixPopover.Root>;
}
