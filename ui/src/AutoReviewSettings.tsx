import {useEffect, useState} from "react";
import {request} from "./api";
import {useSettingsDraft} from "./settingsDraft";
import {useTranslation} from "./i18n";

type Mode = "off" | "shadow" | "auto";
type Settings = {mode: Mode; review_scope: "all_external_tools"};

export function AutoReviewSettings() {
  const {t} = useTranslation("settings");
  const [settings, setSettings] = useState<Settings | null>(null);
  const [mode, setMode] = useState<Mode>("off");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    request<Settings>("/api/auto-review-settings", {signal: controller.signal}).then(value => {setSettings(value); setMode(value.mode); setError("");}).catch(cause => {if (!controller.signal.aborted) setError(cause.message);});
    return () => controller.abort();
  }, [version]);
  async function save() {
    if (busy || !settings) return false;
    setBusy(true); setError(""); setStatus("");
    try {
      await request("/api/auto-review-settings", {method: "PUT", body: JSON.stringify({mode})});
      setSettings({...settings, mode}); setStatus(t("auto_review.saved")); return true;
    } catch (cause) {setError(cause instanceof Error ? cause.message : t("action.save_failed")); return false;}
    finally {setBusy(false);}
  }
  useSettingsDraft({label: "AutoReview", dirty: Boolean(settings && settings.mode !== mode), busy, save, discard: () => {if (settings) setMode(settings.mode); setError(""); setStatus("");}});
  return <section className="settings-section"><h3>AutoReview</h3><p className="settings-description">{t("auto_review.description")}</p>
    {settings ? <><label>{t("auto_review.mode_label")}<select value={mode} disabled={busy} onChange={event => {setMode(event.target.value as Mode); setStatus("");}}><option value="off">{t("auto_review.mode_off")}</option><option value="shadow">{t("auto_review.mode_shadow")}</option><option value="auto">{t("auto_review.mode_auto")}</option></select></label><p className="field-note" data-autoreview-mode={mode}>{t(`auto_review.note_${mode}`)}</p><span role="status">{status}</span></> : !error && <p className="muted">{t("action.loading_settings")}</p>}
    {error && <p className="error-text" role="alert">{error}{!settings && <button className="text-button" onClick={() => setVersion(current => current + 1)}>{t("action.retry")}</button>}</p>}
  </section>;
}
