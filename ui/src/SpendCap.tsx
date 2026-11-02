import {useCallback, useEffect, useState} from "react";
import {request} from "./api";
import {useTranslation} from "./i18n";
import {formatDateTime, formatNumber} from "./i18n/format";
import {Select} from "./Select";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import {useSettingsActive} from "./settingsActivity";
import "./settings/spend-cap.css";

/** GET /api/usage/spend: this month's spend, in the account's time zone, against the limit (0 = off). */
export type SpendStatus = {month: string; month_start: string; month_end: string; timezone: string; spend_usd: number; limit_usd: number; state: "off" | "ok" | "near" | "reached"};

const LIMIT_STEPS = [0, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000];
const money = (value: number) => formatNumber(value, {minimumFractionDigits: 2, maximumFractionDigits: 2});

/**
 * Settings › Usage › Monthly limit. The limit is the automation setting monthly_spend_limit_usd (whole dollars, off by default);
 * the server stops new runs on billed models at 100% and posts one notice at 80%.
 */
export function SpendCap() {
  const {t} = useTranslation("settings");
  const active = useSettingsActive();
  const [status, setStatus] = useState<SpendStatus | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);

  const load = useCallback(async () => {
    try { setStatus(await request<SpendStatus>("/api/usage/spend")); setError(""); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("spend.load_failed")); }
  }, [t]);
  useEffect(() => {
    if (!active) return;
    void load();
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, 30000);
    return () => window.clearInterval(timer);
  }, [active, load]);

  const change = async (limit: number) => {
    setBusy(true); setSaved(false);
    try {
      await request("/api/automation-settings", {method: "PUT", body: JSON.stringify({values: {monthly_spend_limit_usd: limit}})});
      setSaved(true);
      await load();
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("action.save_failed")); }
    finally { setBusy(false); }
  };

  if (!status) {
    return <SettingsSection title={t("spend.title")} description={t("spend.description")}>
      {error ? <p className="error-text" role="alert">{error}<button type="button" className="text-button" onClick={() => void load()}>{t("action.retry")}</button></p> : <p className="muted">{t("spend.loading")}</p>}
    </SettingsSection>;
  }
  const current = status.limit_usd;
  const steps = LIMIT_STEPS.includes(current) ? LIMIT_STEPS : [...LIMIT_STEPS, current].sort((a, b) => a - b);
  const label = (value: number) => value === 0 ? t("spend.off") : t("spend.amount", {amount: formatNumber(value)});
  const percent = current > 0 ? Math.min(100, Math.round(status.spend_usd / current * 100)) : 0;
  const stateText = status.state === "off" ? t("spend.state.off") : status.state === "reached" ? t("spend.state.reached") : status.state === "near" ? t("spend.state.near", {percent}) : t("spend.state.ok", {percent});
  const resets = formatDateTime(status.month_end, {dateStyle: "medium", timeZone: status.timezone});
  return <SettingsSection title={t("spend.title")} description={t("spend.description")}>
    <SettingsCard>
      <SettingsRow label={t("spend.this_month")}
        description={<>{stateText}{current > 0 && <> · {t("spend.resets", {date: resets})}</>}</>}
        control={<span className="spend-figure" data-state={status.state} data-testid="spend-figure">{t("spend.amount_exact", {amount: money(status.spend_usd)})}</span>}/>
      {current > 0 && <div className="spend-meter" role="meter" aria-label={t("spend.this_month")} aria-valuemin={0} aria-valuemax={current} aria-valuenow={Math.min(current, status.spend_usd)} data-state={status.state}><span style={{width: `${percent}%`}}/></div>}
      <SettingsRow label={t("spend.limit_label")} labelFor="spend-limit"
        description={<>{t("spend.limit_help")}{saved && <span role="status" className="auto-saved"> {t("automation.saved")}</span>}{error && <span className="error-text auto-error" role="alert"> {error}</span>}</>}
        control={<Select id="spend-limit" value={String(current)} disabled={busy} onValueChange={value => void change(Number(value))}>{steps.map(step => <option key={step} value={step}>{label(step)}</option>)}</Select>}/>
      <SettingsRow label={t("spend.plans_label")} description={t("spend.plans_note")}/>
    </SettingsCard>
  </SettingsSection>;
}
