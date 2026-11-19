import { useRef } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon } from "./icons";
import { Button } from "./ds/Button/Button";
import { Dialog, DIALOG_SCRIM } from "./ds/Dialog/Dialog";
import { ModalDialog } from "./overlays/ModalDialog";

/** Safari on iOS cannot be asked to install: this sheet says where the button is. Lazy chunk (Radix). */
export function InstallSheet({ onClose, returnFocus }: { onClose: () => void; returnFocus?: HTMLElement | null }) {
  const { t } = useTranslation(["auth"]);
  const close = useRef<HTMLButtonElement>(null);
  return <ModalDialog onClose={onClose} overlayClassName={DIALOG_SCRIM} initialFocus={close} returnFocus={returnFocus}>
    <Dialog className="delete-dialog" title={<><TofiIcon name="share" size={18}/> {t("account.install_app")}</>}
      actions={<Button ref={close} variant="primary" onClick={onClose}>{t("account.install_ios_done")}</Button>}>
      <p>{t("account.install_ios_steps")}</p>
    </Dialog>
  </ModalDialog>;
}
