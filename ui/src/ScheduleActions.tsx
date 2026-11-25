import { IconButton } from "./ds/IconButton/IconButton";
import { ConfirmQuestion, useConfirmation } from "./ConfirmAction";
import type { IconName } from "./ds/Icon/Icon";
import { useTranslation } from "./i18n";

/** One icon control of a schedule card: a tooltip and an accessible name carry the label. */
export function ScheduleIconButton({ icon, label, disabled, onClick, danger }: { icon: IconName; label: string; disabled?: boolean; onClick: () => void; danger?: boolean }) {
  return <IconButton size="sm" className={`work-icon-button${danger ? " is-danger" : ""}`} icon={icon} label={label} disabled={disabled} onClick={onClick} />;
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
