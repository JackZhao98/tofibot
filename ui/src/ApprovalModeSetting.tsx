import {useEffect, useState} from "react";
import {request} from "./api";
import {useSettingsActive} from "./settingsActivity";
import {useTranslation} from "./i18n";
import {approvalChoiceFor, approvalChoices, reviewModeFor, type ReviewMode} from "./approvalMode";
import {SettingsCard, SettingsRow, Segmented} from "./settings/components";

/** Settings > Permissions: the approval mode for this account, as two standard choices. */
export function ApprovalModeSetting() {
  const {t} = useTranslation("settings");
  const active = useSettingsActive();
  const [mode, setMode] = useState<ReviewMode | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  // Re-read whenever the page is shown: the full reviewer under Advanced writes the same setting.
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    request<{mode: ReviewMode}>("/api/auto-review-settings", {signal: controller.signal}).then(value => {setMode(value.mode); setError("");}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("action.save_failed"));});
    return () => controller.abort();
  }, [active, version, t]);
  async function choose(next: (typeof approvalChoices)[number]) {
    if (busy || mode === null) return;
    const target = reviewModeFor(next, mode);
    if (target === mode) return;
    setBusy(true); setError("");
    try {
      await request("/api/auto-review-settings", {method: "PUT", body: JSON.stringify({mode: target})});
      setMode(target);
    } catch (cause) {setError(cause instanceof Error ? cause.message : t("action.save_failed"));}
    finally {setBusy(false);}
  }
  return <>
    <SettingsCard>
      <SettingsRow inline label={t("approval.label")} control={mode === null ? <span className="muted">{t("action.loading_settings")}</span> : <Segmented label={t("approval.label")} value={approvalChoiceFor(mode)} disabled={busy} options={approvalChoices.map(value => ({value, label: t(`approval.${value}`)}))} onChange={value => void choose(value)}/>}/>
    </SettingsCard>
    {error && <p className="error-text" role="alert">{error}{mode === null && <button type="button" className="text-button" onClick={() => setVersion(current => current + 1)}>{t("action.retry")}</button>}</p>}
  </>;
}
