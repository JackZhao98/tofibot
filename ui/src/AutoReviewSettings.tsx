import {useEffect, useState} from "react";
import {request} from "./api";
import {useTranslation} from "./i18n";
import {SettingsCard, SettingsRow, SettingsSwitch} from "./settings/components";

type Mode = "off" | "shadow" | "auto";

/**
 * Permissions > Advanced: Observe mode. Off and Auto are the approval mode (Permissions); Observe records the reviewer's advice and
 * changes nothing else, so it is one switch. It means something only while approvals ask a person, so Auto leaves it off the table.
 */
export function ObserveModeRow() {
  const {t} = useTranslation("settings");
  const [mode, setMode] = useState<Mode | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    request<{mode: Mode}>("/api/auto-review-settings", {signal: controller.signal}).then(value => setMode(value.mode)).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("action.save_failed"));});
    return () => controller.abort();
  }, [t]);
  async function change(observe: boolean) {
    setError("");
    try {
      await request("/api/auto-review-settings", {method: "PUT", body: JSON.stringify({mode: observe ? "shadow" : "off"})});
      setMode(observe ? "shadow" : "off");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("action.save_failed"));
      return false;
    }
  }
  return <>
    <SettingsCard>
      <SettingsRow inline label={t("auto_review.observe")} description={t("auto_review.observe_hint")} control={<SettingsSwitch label={t("auto_review.observe")} checked={mode === "shadow"} disabled={mode === null || mode === "auto"} onChange={change}/>}/>
    </SettingsCard>
    {error && <p className="error-text" role="alert">{error}</p>}
  </>;
}
