import { useId, useRef, useState } from "react";
import { ApiError } from "./api";
import { useTranslation } from "./i18n";
import { ModalDialog } from "./overlays/ModalDialog";
import { TofiIcon } from "./icons";

/** Confirm before the server updates itself (decision D69). Lazy chunk: Radix stays out of the entry bundle. */
export function UpdateDialog({ offer, onConfirm, onClose }: { offer: { version: string; channel: "stable" | "rc"; notes: string }; onConfirm: () => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation("settings");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const headingId = useId();
  const descriptionId = useId();
  const rc = offer.channel === "rc";
  async function confirm() {
    if (busy) return;
    setBusy(true); setError("");
    try { await onConfirm(); }
    catch (cause) { setError(cause instanceof ApiError ? cause.message : t("update.dialog.failed")); setBusy(false); }
  }
  return <ModalDialog onClose={onClose} locked={busy} overlayClassName="delete-dialog-overlay" initialFocus={cancel}>
    <section className="delete-dialog update-dialog" role="alertdialog" aria-labelledby={headingId} aria-describedby={descriptionId} aria-busy={busy} data-update-dialog={offer.channel}>
      <div className="update-dialog-channel">
        <TofiIcon name="info" size={16} aria-hidden="true" />
        <span className={`update-dialog-badge${rc ? " is-rc" : ""}`}>{rc ? t("update.dialog.channel_rc") : t("update.dialog.channel_stable")}</span>
        <code>{offer.version}</code>
      </div>
      <h2 id={headingId}>{rc ? t("update.dialog.title_rc", { version: offer.version }) : t("update.dialog.title", { version: offer.version })}</h2>
      <p id={descriptionId}>{rc ? t("update.dialog.body_rc", { version: offer.version }) : t("update.dialog.body", { version: offer.version })}</p>
      {offer.notes && <p className="delete-dialog-note"><a href={offer.notes} target="_blank" rel="noopener noreferrer">{t("update.dialog.notes")}</a></p>}
      {error && <p className="update-dialog-error" role="alert">{error}</p>}
      <div className="delete-dialog-actions">
        <button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("update.dialog.cancel")}</button>
        <button type="button" className="primary-button" disabled={busy} onClick={() => void confirm()}>{t("update.dialog.confirm")}</button>
      </div>
    </section>
  </ModalDialog>;
}
