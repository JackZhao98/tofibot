import { useState } from "react";
import { request } from "./api";
import { useTranslation } from "./i18n";

export function WorkspacePurgeSettings() {
  const { t } = useTranslation("settings");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function purge() {
    if (busy || value.trim() !== "PURGE") return;
    setBusy(true);
    setError("");
    try {
      await request<{ purged: boolean }>("/api/admin/purge", { method: "POST", body: JSON.stringify({ confirm: "PURGE" }) });
      window.location.reload();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("purge.failed"));
      setBusy(false);
    }
  }
  return <section className="settings-section purge-section">
    <h3>{t("purge.title")}</h3>
    <p className="settings-description">{t("purge.description")}</p>
    <p className="field-note">{t("purge.note")}</p>
    <label>{t("purge.confirm_label")}<input value={value} onChange={event => setValue(event.target.value)} spellCheck={false} autoCapitalize="characters" disabled={busy} /></label>
    {error && <p className="error-text" role="alert">{error}</p>}
    <button type="button" className="purge-button" disabled={busy || value.trim() !== "PURGE"} onClick={() => void purge()}>{busy ? t("purge.purging") : t("purge.submit")}</button>
  </section>;
}
