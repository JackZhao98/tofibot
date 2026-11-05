import { useRef } from "react";
import { ConfirmDialog } from "../overlays/ModalDialog";
import { useTranslation } from "../i18n";

/** Lazy block: the Radix confirmation behind "Deny" and "Revoke" on the Mac settings page. Only rendered when open. */
export default function MacConfirm({ title, body, confirmLabel, onCancel, onConfirm }: {
  title: string; body: string; confirmLabel: string; onCancel: () => void; onConfirm: () => void;
}) {
  const { t } = useTranslation("settings");
  const cancel = useRef<HTMLButtonElement>(null);
  return <ConfirmDialog onClose={onCancel} initialFocus={cancel} overlayClassName="mac-confirm-overlay">
    <div className="mac-confirm-dialog" aria-labelledby="mac-confirm-title" aria-describedby="mac-confirm-body">
      <h3 id="mac-confirm-title">{title}</h3>
      <p id="mac-confirm-body">{body}</p>
      <div className="mac-confirm-actions">
        <button ref={cancel} type="button" className="secondary-button" onClick={onCancel}>{t("mac.confirm.cancel")}</button>
        <button type="button" className="primary-button" onClick={onConfirm}>{confirmLabel}</button>
      </div>
    </div>
  </ConfirmDialog>;
}
