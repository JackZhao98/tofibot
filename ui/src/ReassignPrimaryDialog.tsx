import { useRef, useState } from "react";
import { BotAvatar } from "./BotAvatar";
import { Button } from "./ds/Button/Button";
import { Dialog, DIALOG_SCRIM } from "./ds/Dialog/Dialog";
import { ModalDialog } from "./overlays/ModalDialog";
import { useTranslation } from "./i18n";
import { otherActiveBots, PrimarySuccessorSelect } from "./PrimaryAssistant";
import type { Bot } from "./types";

/**
 * Hiding (archiving) the Primary Assistant asks for a successor first, the same
 * rule as deleting it. Rendered by the app only for that case.
 */
export function ReassignPrimaryDialog({ bot, bots, onConfirm, onClose, returnFocus }: { bot: Bot; bots: Bot[]; onConfirm: (successorId: string) => Promise<void>; onClose: () => void; returnFocus?: HTMLElement | null }) {
  const { t } = useTranslation("chat");
  const [successor, setSuccessor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const candidates = otherActiveBots(bots, bot.id);
  async function confirm() {
    if (busy || !successor) return;
    setBusy(true); setError("");
    try { await onConfirm(successor); onClose(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("primary.hide_failed")); }
    finally { setBusy(false); }
  }
  return <ModalDialog onClose={onClose} locked={busy} overlayClassName={DIALOG_SCRIM} initialFocus={cancel} returnFocus={returnFocus}>
    <Dialog className="delete-dialog" role="alertdialog" busy={busy} title={t("primary.hide_title")}
      actions={<>
        <Button ref={cancel} variant="secondary" disabled={busy} onClick={onClose}>{t("deleteDialog.cancel")}</Button>
        <Button variant="primary" busy={busy} disabled={busy || !successor} onClick={() => void confirm()}>{busy ? t("primary.working") : t("primary.hide_confirm")}</Button>
      </>}>
      <div className="tb-dialog-target"><BotAvatar id={bot.id} mini /><span>{bot.name}</span></div>
      <p>{t("primary.hide_body")}</p>
      <PrimarySuccessorSelect bots={candidates} value={successor} onChange={setSuccessor} disabled={busy} />
      {error && <p className="error-text" role="alert">{error}</p>}
    </Dialog>
  </ModalDialog>;
}
