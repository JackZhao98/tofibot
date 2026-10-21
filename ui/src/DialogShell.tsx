import type { ReactNode, RefObject } from "react";
import { ModalDialog } from "./overlays/ModalDialog";

/** The destructive dialogs (delete a conversation, hand over the Primary Assistant role): a scrim that closes on a press
 * and Escape unless `busy`, focus on the safe choice. Radix Dialog underneath (docs/design/migration.md step 3); lazy
 * chunks only. */
export function DialogShell({ busy, onClose, headingId, descriptionId, initialFocus, returnFocus, children }: { busy: boolean; onClose: () => void; headingId: string; descriptionId: string; initialFocus: RefObject<HTMLElement | null>; /** The control that opened it (the app reads it before the workspace turns inert and drops focus). */ returnFocus?: HTMLElement | null; children: ReactNode }) {
  return <ModalDialog onClose={onClose} locked={busy} overlayClassName="delete-dialog-overlay" initialFocus={initialFocus} returnFocus={returnFocus}>
    <section className="delete-dialog" role="alertdialog" aria-labelledby={headingId} aria-describedby={descriptionId} aria-busy={busy}>{children}</section>
  </ModalDialog>;
}
