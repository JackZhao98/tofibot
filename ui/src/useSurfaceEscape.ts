import { useEffect, useRef, type RefObject } from "react";
import { topLayerDialog } from "./layers";
import { escapeTarget, type EscapeTarget, type Surfaces } from "./surfaces";

/**
 * Escape for every surface, in one place (docs/design/widgets-and-sidebar.md section 7). One press closes one thing, in this order:
 *
 *  1. a native top-layer `<dialog>` (the image lightbox, the file preview): the browser closes it, this hook does nothing;
 *  2. a Radix layer (menu, popover, `ModalDialog`, `ConfirmDialog`, and the covers, which are `ModalDialog`s): it claims the key with
 *     `preventDefault`, so this hook sees `defaultPrevented` (or `[data-tofi-modal]`) and does nothing;
 *  3. the big computer window steps back to the compact one (a phone sheet has no compact form and closes);
 *  4. the cover, when it is not a Radix dialog of its own (Settings): its own close, which asks first when something is unsaved;
 *  5. the sidebar;
 *  6. the widgets from the bottom up: Artifacts, Members, and the computer last.
 *
 * A surface that is busy (`aria-busy`) is not closed by Escape. Step 3 listens in the capture phase: the remote screen's keyboard
 * receiver stops the keys it forwards from bubbling, and Escape must still step the window back. Every other step is the bubble phase,
 * after Radix has had its turn.
 */
export function useSurfaceEscape({ surfaces, bigComputer, phoneSheet, shrinkComputer, closeWidget, closeSidebar, closeSettings }: {
  surfaces: RefObject<Surfaces>;
  /** The computer is in its big window. */
  bigComputer: boolean;
  /** The big window is a full-screen sheet (no compact form to go back to). */
  phoneSheet: boolean;
  shrinkComputer: () => void;
  closeWidget: (id: "computer" | "members" | "artifacts") => void;
  closeSidebar: () => void;
  /** Settings is a cover that is not a Radix dialog: it owns its unsaved-changes question. */
  closeSettings: () => void;
}) {
  const live = useRef({ bigComputer, phoneSheet, shrinkComputer, closeWidget, closeSidebar, closeSettings });
  live.current = { bigComputer, phoneSheet, shrinkComputer, closeWidget, closeSidebar, closeSettings };

  useEffect(() => {
    const busy = () => Boolean(document.querySelector('.widget-column [aria-busy="true"], .sidebar-pane [aria-busy="true"], .detail-pane [aria-busy="true"]'));
    const run = (event: KeyboardEvent, target: EscapeTarget) => {
      const act = live.current;
      // Every cover but Settings is a Radix dialog that took the key before this ran; one that is already leaving takes nothing.
      if (target.layer === "cover" && target.id !== "settings") return;
      // A surface that is still loading is not closed from under the person (Settings answers at once: its frame is the loading state).
      if (target.layer !== "cover" && target.layer !== "computer-big" && busy()) return;
      event.preventDefault();
      if (target.layer === "computer-big") { if (act.phoneSheet) act.closeWidget("computer"); else act.shrinkComputer(); event.stopPropagation(); }
      else if (target.layer === "cover") act.closeSettings();
      else if (target.layer === "sidebar") act.closeSidebar();
      else act.closeWidget(target.id);
    };
    // The capture phase only for the big computer window, and only when nothing is open above it.
    const capture = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || event.isComposing || !live.current.bigComputer) return;
      if (topLayerDialog() || document.querySelector("[data-tofi-modal], #tofi-layers [data-state='open']")) return;
      const target = escapeTarget(surfaces.current, { bigComputer: true });
      if (target?.layer === "computer-big") run(event, target);
    };
    const bubble = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || event.isComposing) return;
      if (topLayerDialog() || document.querySelector("[data-tofi-modal]")) return;
      const target = escapeTarget(surfaces.current, { bigComputer: live.current.bigComputer });
      if (target) run(event, target);
    };
    window.addEventListener("keydown", capture, true);
    document.addEventListener("keydown", bubble);
    return () => { window.removeEventListener("keydown", capture, true); document.removeEventListener("keydown", bubble); };
  }, [surfaces]);
}
