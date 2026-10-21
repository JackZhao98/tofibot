import { useId } from "react";
import { useTranslation } from "./i18n";
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
