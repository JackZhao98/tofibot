import { useRef, useState } from "react";
import { Button } from "./ds/Button/Button";
import { Dialog, DIALOG_SCRIM } from "./ds/Dialog/Dialog";
import { useTranslation } from "./i18n";
import { ModalDialog } from "./overlays/ModalDialog";
import { Note } from "./ds/Notice/Notice";

/** The one question dialog behind every confirmation (V3 has no inline confirmation): the question is the title and names
 *  what is lost, Cancel takes the focus, and Escape, the scrim or Cancel leave nothing behind until a request is in
 *  flight. A failure shows inside the dialog, which stays open to try again. Lazy chunks only: it carries Radix through
 *  ModalDialog (D31). */
export function ConfirmQuestion({ question, confirmLabel, tone = "danger", pending, error, disabled, onConfirm, onCancel }: {
  question: string; confirmLabel: string; tone?: "danger" | "neutral"; pending: boolean; error: string; disabled?: boolean; onConfirm: () => void; onCancel: () => void;
}) {
  const { t } = useTranslation("settings");
  const cancelButton = useRef<HTMLButtonElement>(null);
  return <ModalDialog onClose={onCancel} locked={pending} overlayClassName={DIALOG_SCRIM} initialFocus={cancelButton}>
    <Dialog className="confirm-dialog" tone={tone} role="alertdialog" busy={pending} title={question}
      actions={<>
        <Button ref={cancelButton} variant="secondary" disabled={pending} onClick={onCancel}>{t("action.cancel")}</Button>
        <Button variant={tone === "danger" ? "danger" : "primary"} busy={pending} disabled={disabled || pending} onClick={onConfirm}>{pending ? t("action.processing") : confirmLabel}</Button>
      </>}>
      {error ? <Note tone="error" role="alert">{error}</Note> : null}
    </Dialog>
  </ModalDialog>;
}

/** What a confirmation does once it is asked: run the request, close on success, show the failure and stay open. */
export function useConfirmation(onConfirm: () => Promise<void>, disabled?: boolean) {
  const { t } = useTranslation("settings");
  const [asking, setAsking] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  return {
    asking, pending, error,
    ask: () => setAsking(true),
    cancel: () => { if (pending) return; setError(""); setAsking(false); }, // `disabled` blocks the action only: a modal must always have a way out
    confirm: async () => {
      if (pending || disabled) return;
      setPending(true); setError("");
      try { await onConfirm(); setAsking(false); }
      catch (cause) { setError(cause instanceof Error ? cause.message : t("action.failed")); }
      finally { setPending(false); }
    },
  };
}

/** A destructive action asks in a Dialog: the trigger stays where it is. */
export function ConfirmAction({ label, question, disabled, onConfirm }: { label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void> }) {
  const { t } = useTranslation("settings");
  const ask = useConfirmation(onConfirm, disabled);
  return <span className="confirm-action">
    <button type="button" className="danger-link" disabled={disabled} onClick={ask.ask}>{label}</button>
    {ask.asking && <ConfirmQuestion question={question} confirmLabel={t("confirm.action", { action: label })} pending={ask.pending} error={ask.error} disabled={disabled} onConfirm={() => void ask.confirm()} onCancel={ask.cancel} />}
  </span>;
}
