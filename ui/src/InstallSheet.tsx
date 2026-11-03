import { useId, useRef } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon } from "./icons";
import { ModalDialog } from "./overlays/ModalDialog";

/** Safari on iOS cannot be asked to install: this sheet says where the button is. Lazy chunk (Radix). */
export function InstallSheet({ onClose, returnFocus }: { onClose: () => void; returnFocus?: HTMLElement | null }) {
  const { t } = useTranslation(["auth"]);
  const heading = useId();
  const body = useId();
  const close = useRef<HTMLButtonElement>(null);
  return <ModalDialog onClose={onClose} overlayClassName="delete-dialog-overlay" initialFocus={close} returnFocus={returnFocus}>
    <section className="delete-dialog" role="dialog" aria-labelledby={heading} aria-describedby={body}>
      <h2 id={heading}><TofiIcon name="share" size={18}/> {t("account.install_app")}</h2>
      <p id={body}>{t("account.install_ios_steps")}</p>
      <div className="delete-dialog-actions"><button ref={close} type="button" className="primary-button" onClick={onClose}>{t("account.install_ios_done")}</button></div>
    </section>
  </ModalDialog>;
}
