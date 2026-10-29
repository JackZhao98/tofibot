import { useEffect, useRef, useState } from "react";
import { TofiIcon, type TofiIconName } from "./icons";
import { useTranslation } from "./i18n";

/** One icon control of a schedule card: a tooltip and an accessible name carry the label. */
export function ScheduleIconButton({ icon, label, disabled, onClick, danger }: { icon: TofiIconName; label: string; disabled?: boolean; onClick: () => void; danger?: boolean }) {
  return <button type="button" className={`work-icon-button${danger ? " is-danger" : ""}`} aria-label={label} data-hint={label} disabled={disabled} onClick={onClick}><TofiIcon name={icon} size={16} aria-hidden="true" /></button>;
}

/** Delete as an icon; a click opens the same confirmation the text action offers. */
export function ScheduleDeleteButton({ label, question, disabled, onConfirm }: { label: string; question: string; disabled?: boolean; onConfirm: () => Promise<void> }) {
  const { t } = useTranslation("settings");
  const [confirming, setConfirming] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const cancelButton = useRef<HTMLButtonElement>(null);
  const trigger = useRef<HTMLSpanElement>(null);
  const wasConfirming = useRef(false);
  useEffect(() => {
    if (confirming) cancelButton.current?.focus();
    else if (wasConfirming.current) trigger.current?.querySelector("button")?.focus();
    wasConfirming.current = confirming;
  }, [confirming]);
  function cancel() { if (pending || disabled) return; setError(""); setConfirming(false); }
  async function confirm() {
    if (pending || disabled) return;
    setPending(true); setError("");
    try { await onConfirm(); setConfirming(false); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("action.failed")); }
    finally { setPending(false); }
  }
  return <span className="confirm-action">
    <span ref={trigger} hidden={confirming}><ScheduleIconButton icon="trash" label={label} danger disabled={disabled} onClick={() => setConfirming(true)} /></span>
    {confirming && <span className="inline-confirmation" role="group" aria-label={question} onKeyDown={event => { if (event.key === "Escape") { event.stopPropagation(); cancel(); } }}>
      <span>{question}</span><button type="button" className="danger-link" disabled={disabled || pending} onClick={() => void confirm()}>{pending ? t("action.processing") : t("confirm.action", { action: label })}</button><button ref={cancelButton} type="button" disabled={disabled || pending} onClick={cancel}>{t("action.cancel")}</button>{error && <span className="error-text" role="alert">{error}</span>}
    </span>}
  </span>;
}
