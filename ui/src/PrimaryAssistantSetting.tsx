import { useEffect, useRef, useState } from "react";
import { ApiError, api } from "./api";
import { useTranslation } from "./i18n";
import { primaryBotOf } from "./PrimaryAssistant";
import { SettingsCard, SettingsRow, SettingsSection } from "./settings/components";
import type { Bot } from "./types";

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
