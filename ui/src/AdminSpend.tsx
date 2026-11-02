import type {TFunction} from "i18next";
import {formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import type {AdminAccountSpend} from "./adminApi";

const money = (value: number) => formatNumber(value, {minimumFractionDigits: 2, maximumFractionDigits: 2});

/** "$12.40 / $50", "$12.40 · no limit" or a dash for an account that cannot be read. */
export function adminSpendText(t: TFunction<"settings">, entry?: AdminAccountSpend): string {
  if (!entry || !entry.available) return "—";
  const spend = money(entry.spend_usd);
  return entry.limit_usd > 0 ? t("spend.admin.of_limit", {spend, limit: formatNumber(entry.limit_usd)}) : t("spend.admin.no_limit", {spend});
}

/** Admin detail: the account's spend this month and its limit. Read only; the limit is the owner's setting. */
export function AdminSpend({entry}: {entry?: AdminAccountSpend}) {
  const {t} = useTranslation("settings");
  return <SettingsSection title={t("spend.admin.title")}>
    <SettingsCard>
      <SettingsRow label={t("spend.this_month")} description={entry?.available ? t(`spend.state.${entry.state}`, {percent: entry.limit_usd > 0 ? Math.min(100, Math.round(entry.spend_usd / entry.limit_usd * 100)) : 0}) : t("spend.admin.unavailable")}
        control={<span className="admin-value" data-testid="admin-spend">{adminSpendText(t, entry)}</span>}/>
    </SettingsCard>
  </SettingsSection>;
}
