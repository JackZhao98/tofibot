import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiError, api } from "./api";
import { BotAvatar } from "./BotAvatar";
import { DialogShell } from "./DialogShell";
import { useTranslation } from "./i18n";
import { SettingsCard, SettingsRow, SettingsSection } from "./settings/components";
import type { Bot } from "./types";
import "./primary-assistant.css";

/** Small "Primary" tag for the account's Primary Assistant (sidebar, Bot panel, pickers). */
export function PrimaryBadge() {
  const { t } = useTranslation("chat");
  return <span className="primary-badge" data-hint={t("primary.badge_hint")}>{t("primary.badge")}</span>;
}

/** Active Bots other than `exceptId`, in list order. */
export function otherActiveBots(bots: Bot[], exceptId: string) {
  return bots.filter(bot => !bot.archived && bot.id !== exceptId);
}

export function primaryBotOf(bots: Bot[]) {
  return bots.find(bot => bot.is_primary && !bot.archived);
}

/**
 * The owner must name a successor before the Primary Assistant is removed:
 * an empty choice keeps the confirm button disabled.
 */
export function PrimarySuccessorSelect({ bots, value, onChange, disabled }: { bots: Bot[]; value: string; onChange: (id: string) => void; disabled?: boolean }) {
  const { t } = useTranslation("chat");
  const id = useId();
  return <div className="primary-successor">
    <label htmlFor={id}>{t("primary.successor_label")}</label>
    <select id={id} value={value} disabled={disabled} onChange={event => onChange(event.target.value)}>
      <option value="">{t("primary.successor_placeholder")}</option>
      {bots.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}
    </select>
  </div>;
}

/** Settings › General › Primary Assistant. A choice applies at once. */
export function PrimaryAssistantSetting({ bots, onChanged }: { bots: Bot[]; onChanged?: () => void | Promise<void> }) {
  const { t } = useTranslation("settings");
  const active = bots.filter(bot => !bot.archived);
  const current = primaryBotOf(bots)?.id ?? "";
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  async function choose(id: string) {
    if (!id || id === current || busy) return;
    setBusy(true); setError("");
    try { await api.setPrimaryAssistant(id); await onChanged?.(); }
    catch (cause) { if (mounted.current) setError(cause instanceof ApiError && cause.code === "primary_invalid" ? t("primary.invalid") : t("primary.save_failed")); }
    finally { if (mounted.current) setBusy(false); }
  }
  return <SettingsSection title={t("primary.title")} description={t("primary.description")}>
    <SettingsCard>
      {active.length === 0
        ? <SettingsRow label={t("primary.label")} description={t("primary.none")} />
        : <SettingsRow labelFor="primary-assistant" label={t("primary.label")} description={t("primary.hint")}
            control={<select id="primary-assistant" value={current} disabled={busy} onChange={event => void choose(event.target.value)}>
              {!current && <option value="">{t("primary.choose")}</option>}
              {active.map(bot => <option key={bot.id} value={bot.id}>{bot.name}</option>)}
            </select>} />}
    </SettingsCard>
    {error && <p className="error-text" role="alert">{error}</p>}
  </SettingsSection>;
}

/**
 * Hiding (archiving) the Primary Assistant asks for a successor first, the same
 * rule as deleting it. Rendered by the app only for that case.
 */
export function ReassignPrimaryDialog({ bot, bots, onConfirm, onClose }: { bot: Bot; bots: Bot[]; onConfirm: (successorId: string) => Promise<void>; onClose: () => void }) {
  const { t } = useTranslation("chat");
  const [successor, setSuccessor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const cancel = useRef<HTMLButtonElement>(null);
  const headingId = useId();
  const descriptionId = useId();
  const candidates = otherActiveBots(bots, bot.id);
  useEffect(() => {
    const previous = document.activeElement;
    cancel.current?.focus();
    return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
  }, []);
  async function confirm() {
    if (busy || !successor) return;
    setBusy(true); setError("");
    try { await onConfirm(successor); onClose(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("primary.hide_failed")); }
    finally { setBusy(false); }
  }
  return createPortal(<DialogShell busy={busy} onClose={onClose} headingId={headingId} descriptionId={descriptionId}>
    <div className="delete-dialog-identity"><BotAvatar id={bot.id} mini /><span>{bot.name}</span></div>
    <h2 id={headingId}>{t("primary.hide_title")}</h2>
    <p id={descriptionId}>{t("primary.hide_body")}</p>
    <PrimarySuccessorSelect bots={candidates} value={successor} onChange={setSuccessor} disabled={busy} />
    {error && <p className="error-text" role="alert">{error}</p>}
    <div className="delete-dialog-actions"><button ref={cancel} type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("deleteDialog.cancel")}</button><button type="button" className="primary-button" disabled={busy || !successor} onClick={() => void confirm()}>{busy ? t("primary.working") : t("primary.hide_confirm")}</button></div>
  </DialogShell>, document.body);
}
