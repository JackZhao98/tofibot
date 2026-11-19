import { ConfirmQuestion, useConfirmation } from "./ConfirmAction";
import { TofiIcon, type TofiIconName } from "./icons";
import { useTranslation } from "./i18n";

/** One icon control of a schedule card: a tooltip and an accessible name carry the label. */
export function ScheduleIconButton({ icon, label, disabled, onClick, danger }: { icon: TofiIconName; label: string; disabled?: boolean; onClick: () => void; danger?: boolean }) {
  return <button type="button" className={`work-icon-button${danger ? " is-danger" : ""}`} aria-label={label} data-hint={label} disabled={disabled} onClick={onClick}><TofiIcon name={icon} size={16} aria-hidden="true" /></button>;
}

/** Delete as an icon; a click opens the same confirmation dialog the text action offers. */
export function ScheduleDeleteButton({ label, question, disabled, onConfirm }: { label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void> }) {
  const { t } = useTranslation("settings");
  const ask = useConfirmation(onConfirm, disabled);
  return <span className="confirm-action">
    <ScheduleIconButton icon="trash" label={label} danger disabled={disabled} onClick={ask.ask} />
    {ask.asking && <ConfirmQuestion question={question} confirmLabel={t("confirm.action", { action: label })} pending={ask.pending} error={ask.error} disabled={disabled} onConfirm={() => void ask.confirm()} onCancel={ask.cancel} />}
  </span>;
}
