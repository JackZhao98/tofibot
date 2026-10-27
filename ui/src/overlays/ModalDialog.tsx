import * as AlertDialog from "@radix-ui/react-alert-dialog";
import * as Dialog from "@radix-ui/react-dialog";
import { useEffect, useRef, useState, type ReactElement, type RefObject } from "react";
import { layerRoot } from "../layers";

/**
 * TOFI's modal dialogs, on Radix Dialog / AlertDialog (docs/design/migration.md step 3). One implementation of the
 * modal contract instead of a hand-written trap per dialog: Tab and Shift+Tab stay inside, Escape closes only the
 * top layer (Radix keeps one layer stack for every Radix surface and claims the key with preventDefault, so the
 * app's own Escape handlers, which all check `defaultPrevented`, do not also fire), focus returns to where it was,
 * and the rest of the page is hidden from screen readers.
 *
 * Only lazy chunks import this module: Radix must never reach the entry bundle (D31, `test:dialogs`).
 *
 * The caller renders the dialog element itself as `children` (its class names, role, labels stay as they were) and,
 * for dialogs with a scrim, names the scrim with `overlayClassName`; the content is rendered inside the scrim, which is
 * how the CSS was written before the migration. Escape that arrives in a new dialog's first frame is dropped (see
 * onEscapeKeyDown), and focus stranded on Radix's guard spans is sent back into the dialog. Radix sets neither `aria-modal` nor anything the app can query, so
 * the content always carries `aria-modal="true"` (the desktop-shortcut gate in App.tsx reads it) and
 * `data-tofi-modal` (the app's own detail-pane trap stands aside while one is open).
 */
type ModalDialogProps = {
  onClose: () => void;
  /** The dialog element, rendered through Radix `asChild`. */
  children: ReactElement;
  /** The scrim's class. Without it there is no scrim (the content covers the viewport itself). */
  overlayClassName?: string;
  /** Data attributes for the scrim. */
  overlayProps?: Record<`data-${string}`, string | number>;
  /** Escape closes (default). False leaves Escape alone, e.g. while a remote computer owns the keyboard. */
  closeOnEscape?: boolean;
  /** A press on the scrim closes (default). */
  closeOnOutside?: boolean;
  /** While true nothing closes the dialog: work in flight, a secret that must be acknowledged. */
  locked?: boolean;
  /** Where focus lands on open. Default: the dialog element itself. */
  initialFocus?: RefObject<HTMLElement | null>;
  /** Where focus returns on close. Default: whatever had focus when the dialog opened. */
  returnFocus?: HTMLElement | null;
  /** Where the dialog is portaled. Default: the layer root. A cover whose panels are styled under `.workspace` goes inside it. */
  container?: HTMLElement | null;
};

const TABBABLE = 'button:not(:disabled), a[href], input:not(:disabled):not([type="hidden"]), select:not(:disabled), textarea:not(:disabled), summary, [tabindex]:not([tabindex="-1"])';

function useModalBehaviour({ onClose, closeOnEscape = true, closeOnOutside = true, locked = false, initialFocus, returnFocus }: ModalDialogProps) {
  // Read before Radix (or the dialog's own effects) move focus.
  const [opener] = useState(() => document.activeElement instanceof HTMLElement ? document.activeElement : null);
  const state = useRef({ locked, returnFocus, closeOnEscape, closeOnOutside });
  state.current = { locked, returnFocus, closeOnEscape, closeOnOutside };
  const ref = useRef<HTMLDivElement>(null); // the dialog element (a <section> or <div>; Radix types it as a div)
  useEffect(() => {
    // Radix answers focus that leaves the dialog by focusing the last element focused inside it. When that element is
    // gone (a step of the dialog was replaced) focus stays on one of Radix's invisible guard spans at the edges of
    // <body>, where Tab draws nothing. Send it to the dialog's first (or, from the end guard, last) control instead.
    const rescue = () => {
      const content = ref.current, active = document.activeElement;
      if (!content || !(active instanceof HTMLElement) || !active.hasAttribute("data-radix-focus-guard")) return;
      const open = document.querySelectorAll("[data-tofi-modal]");
      if (open[open.length - 1] !== content) return;
      const controls = [...content.querySelectorAll<HTMLElement>(TABBABLE)].filter(element => !element.closest("[inert], [hidden]") && element.getClientRects().length > 0);
      (active === document.body.lastElementChild ? controls.at(-1) : controls[0])?.focus();
    };
    const onFocusIn = () => queueMicrotask(rescue);
    document.addEventListener("focusin", onFocusIn);
    return () => document.removeEventListener("focusin", onFocusIn);
  }, []);
  return {
    onOpenChange: (open: boolean) => { if (!open && !state.current.locked) onClose(); },
    content: {
      ref,
      "aria-modal": true,
      "data-tofi-modal": "",
      onOpenAutoFocus: (event: Event) => {
        // The named control, else the dialog element itself (Radix makes it focusable; it draws no ring).
        event.preventDefault();
        const target = initialFocus?.current ?? (event.currentTarget instanceof HTMLElement ? event.currentTarget : null);
        target?.focus({ preventScroll: true });
      },
      onCloseAutoFocus: (event: Event) => {
        event.preventDefault();
        // Only when focus was lost with the dialog: a surface that replaced it (the view-only chat turning into its
        // card) has already placed focus where it belongs.
        const active = document.activeElement;
        if (active && active !== document.body && active.isConnected) return;
        const target = state.current.returnFocus ?? opener;
        if (target?.isConnected) target.focus();
      },
      onEscapeKeyDown: (event: KeyboardEvent) => {
        // Radix registers a newly opened dialog as the top layer in an effect, a frame after it is on screen. An Escape
        // in that frame would close the dialog underneath it (and the new one with it): drop the key instead.
        const open = document.querySelectorAll("[data-tofi-modal]");
        if (state.current.locked || !state.current.closeOnEscape || open[open.length - 1] !== ref.current) event.preventDefault();
      },
      onPointerDownOutside: (event: Event) => {
        // The workspace toast sits above every modal (--z-toast) and its Retry must keep working while one is open.
        const onToast = event.target instanceof Element && event.target.closest(".workspace-toast");
        if (state.current.locked || !state.current.closeOnOutside || onToast) event.preventDefault();
      },
    },
  };
}

/** The scrim is a plain element, not Radix `Overlay`: the Overlay's scroll lock (react-remove-scroll) restyles <body>
 * on every open and TOFI's page never scrolls behind a dialog (the scrim covers it and is not scrollable). Radix turns
 * pointer events off on <body> while a modal is open, so the scrim turns them back on to receive its press. */
function Scrim({ className, data, children }: { className: string; data?: ModalDialogProps["overlayProps"]; children: ReactElement }) {
  return <div className={className} style={{ pointerEvents: "auto" }} {...data}>{children}</div>;
}

/** A modal dialog. A destructive confirmation that may still be dismissed by a press outside sets `role="alertdialog"` on
 * its element and uses this, not {@link ConfirmDialog} (Radix AlertDialog never closes on an outside press). */
export function ModalDialog(props: ModalDialogProps) {
  const { onOpenChange, content } = useModalBehaviour(props);
  const body = <Dialog.Content asChild {...content}>{props.children}</Dialog.Content>;
  return <Dialog.Root open onOpenChange={onOpenChange}>
    <Dialog.Portal container={props.container ?? layerRoot()}>{props.overlayClassName ? <Scrim className={props.overlayClassName} data={props.overlayProps}>{body}</Scrim> : body}</Dialog.Portal>
  </Dialog.Root>;
}

/** A confirmation that must be answered with one of its buttons or Escape (Radix AlertDialog: `role="alertdialog"`, a
 * press outside never closes it). */
export function ConfirmDialog(props: Omit<ModalDialogProps, "closeOnOutside" | "overlayClassName"> & { overlayClassName: string }) {
  const { onOpenChange, content } = useModalBehaviour({ ...props, closeOnOutside: false });
  return <AlertDialog.Root open onOpenChange={onOpenChange}>
    <AlertDialog.Portal container={props.container ?? layerRoot()}>
      <Scrim className={props.overlayClassName} data={props.overlayProps}><AlertDialog.Content asChild {...content}>{props.children}</AlertDialog.Content></Scrim>
    </AlertDialog.Portal>
  </AlertDialog.Root>;
}
