import { useRef } from "react";
import { useTranslation } from "./i18n";
import { coverContainer } from "./coverGuard";
import { ConfirmDialog } from "./overlays/ModalDialog";
import "./cover-form.css";

/**
 * The two pieces every edit form inside a cover shares (docs/design/widgets-and-sidebar.md section 5.2): the bar that slides up from
 * the bottom edge while the form has unsaved changes, and the question asked when the cover is closed with unsaved changes.
 * Lazy chunks only (the dialog is Radix).
 */
export function DirtyBar({ open, busy, error = "", canSave = true, onDiscard, onSave, children }: {
  open: boolean; busy: boolean; error?: string; canSave?: boolean; onDiscard: () => void; onSave: () => void;
  /** Extra controls between the text and the buttons (the group form's Reload after a conflict). */
  children?: React.ReactNode;
}) {
  const { t } = useTranslation("chat");
  const text = error || t("cover.unsaved");
  return <div className="cover-dirty-bar" data-open={open || undefined} data-error={error ? "true" : undefined} inert={!open || undefined}>
    <div className="cover-dirty-inner">
      <span className="cover-dirty-text" aria-hidden="true">{text}</span>
      <span className="sr-only" role="status" aria-live="polite">{open ? text : ""}</span>
      {children}
      <button type="button" className="cover-dirty-discard" disabled={busy} onClick={onDiscard}>{t("cover.discard")}</button>
      <button type="button" className="cover-dirty-save" disabled={busy || !canSave} onClick={onSave}>{busy ? t("action.saving") : t("cover.save")}</button>
    </div>
  </div>;
}

/** Closing a cover with unsaved changes: keep editing (the safe choice, focused) or throw the changes away. */
export function LeaveConfirm({ busy = false, onKeep, onLeave }: { busy?: boolean; onKeep: () => void; onLeave: () => void }) {
  const { t } = useTranslation("chat");
  const keep = useRef<HTMLButtonElement>(null);
  return <ConfirmDialog onClose={onKeep} locked={busy} initialFocus={keep} container={coverContainer()} overlayClassName="cover-leave-scrim">
    <div className="cover-leave-dialog" aria-labelledby="cover-leave-title">
      <h3 id="cover-leave-title">{t("cover.leave_title")}</h3>
      <p>{t("cover.leave_body")}</p>
      <div className="cover-leave-actions">
        <button type="button" className="cover-leave-discard" disabled={busy} onClick={onLeave}>{t("cover.leave_discard")}</button>
        <button ref={keep} type="button" className="cover-leave-keep" onClick={onKeep}>{t("cover.keep_editing")}</button>
      </div>
    </div>
  </ConfirmDialog>;
}
