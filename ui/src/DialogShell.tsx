import type { ReactNode } from "react";

/** Overlay + focus trap shared by the destructive dialogs. */
export function DialogShell({ busy, onClose, headingId, descriptionId, children }: { busy: boolean; onClose: () => void; headingId: string; descriptionId: string; children: ReactNode }) {
  return <div className="delete-dialog-overlay" onClick={event => { if (event.target === event.currentTarget && !busy) onClose(); }} onKeyDown={event => {
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!busy) onClose(); }
    if (event.key === "Tab") {
      const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>("button:not(:disabled), select:not(:disabled)"));
      if (!controls.length) { event.preventDefault(); return; }
      const index = controls.indexOf(document.activeElement as HTMLElement);
      if (event.shiftKey && index <= 0 || !event.shiftKey && (index < 0 || index === controls.length - 1)) { event.preventDefault(); (event.shiftKey ? controls.at(-1) : controls[0])?.focus(); }
    }
  }}><section className="delete-dialog" role="alertdialog" aria-modal="true" aria-labelledby={headingId} aria-describedby={descriptionId} aria-busy={busy}>{children}</section></div>;
}
