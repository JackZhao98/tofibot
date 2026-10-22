import {useCallback, useEffect, useState} from "react";
import {api} from "../api";
import {errorText} from "../i18n/errors";
import {formatNumber} from "../i18n/format";
import {useTranslation} from "../i18n";
import {useSettingsActive} from "../settingsActivity";
import type {RunHealth} from "../types";
import {SettingsCard, SettingsRow, SettingsSection} from "./components";
import "./run-health.css";

const statusOrder = ["done", "failed", "cancelled", "interrupted", "running", "waiting", "queued"] as const;
const reasonKeys = ["execution_failed", "no_final_answer", "tool_stuck", "resumed_repeatedly", "approval_expired", "model_unconfigured", "model_auth_invalid", "model_quota_exhausted", "connection_interrupted", "budget_exhausted"] as const;
type Reason = (typeof reasonKeys)[number];
const isReason = (code: string): code is Reason => (reasonKeys as readonly string[]).includes(code);
const REFRESH_MS = 30000;

/** Read-only run health for the last seven days: runs by status, named failure reasons, tool time-outs. Counts only; no provider text. */
export function RunHealthCard() {
  const {t} = useTranslation("settings");
  const active = useSettingsActive();
  const [health, setHealth] = useState<RunHealth | null>(null);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  const load = useCallback(async (signal: AbortSignal) => {
    try {
      const next = await api.usageRuns(7, signal);
      if (signal.aborted) return;
      setHealth(next);
      setError("");
    } catch (cause) {
      if (!signal.aborted) setError(errorText(cause, "settings", t("usage.health.load_failed")));
    }
  }, [t]);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void load(controller.signal);
    const timer = window.setInterval(() => { if (!document.hidden) void load(controller.signal); }, REFRESH_MS);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [active, load, version]);

  const title = t("usage.health.title");
  const hint = t("usage.range.7d");
  if (!health) {
    return <SettingsSection title={title} hint={hint}>
      {error ? <p className="error-text" role="alert">{error}<button type="button" className="text-button" onClick={() => setVersion(n => n + 1)}>{t("action.retry")}</button></p> : <p className="muted" role="status">{t("usage.health.loading")}</p>}
    </SettingsSection>;
  }
  const shown = statusOrder.filter(status => (health.by_status[status] ?? 0) > 0);
  return <SettingsSection title={title} hint={hint}>
    <SettingsCard className="run-health">
      <SettingsRow label={t("usage.health.runs")} description={health.runs === 0 ? t("usage.health.no_runs") : undefined} control={shown.length > 0 && <ul className="run-health-stats" aria-label={t("usage.health.runs")}>
        {shown.map(status => <li key={status}><strong>{formatNumber(health.by_status[status])}</strong><span>{t(`usage.health.status.${status}`)}</span></li>)}
      </ul>} />
      <SettingsRow label={t("usage.health.reasons")} description={health.failures.length === 0 ? t("usage.health.no_failures") : undefined} control={health.failures.length > 0 && <ul className="run-health-reasons" aria-label={t("usage.health.reasons")}>
        {health.failures.map(item => <li key={item.code}><span>{t(`usage.health.reason.${isReason(item.code) ? item.code : "execution_failed"}`)}</span><strong>{formatNumber(item.count)}</strong></li>)}
      </ul>} />
      <SettingsRow label={t("usage.health.timeouts")} description={t("usage.health.timeouts_note")} control={<strong className="run-health-count">{formatNumber(health.tool_timeouts)}</strong>} />
    </SettingsCard>
    {error && <p className="error-text" role="alert">{error}</p>}
  </SettingsSection>;
}
