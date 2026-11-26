import { TextInput } from "./ds/TextInput/TextInput";
import { Button } from "./ds/Button/Button";
import { useState } from "react";
import { request } from "./api";
import { useTranslation } from "./i18n";
import { useHasComputer } from "./computerCapability";
import { SettingsRow } from "./settings/components";
import { ComputerLossList } from "./ComputerLoss";
import { Note } from "./ds/Notice/Notice";

export function WorkspacePurgeSettings() {
  const { t } = useTranslation("settings");
  const hasComputer = useHasComputer();
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
    <p className="settings-description">{t(hasComputer ? "purge.description" : "purge.description_no_computer")}</p>
    <Note >{t(hasComputer ? "purge.note" : "purge.note_no_computer")}</Note>
    {hasComputer && <ComputerLossList />}
    <SettingsRow label={t("purge.confirm_label")} labelFor="purge-confirm" control={<TextInput id="purge-confirm" value={value} onChange={event => setValue(event.target.value)} spellCheck={false} autoCapitalize="characters" disabled={busy} />}/>
    {error && <Note tone="error" role="alert">{error}</Note>}
    <div className="purge-actions"><Button variant="danger" className="purge-button" busy={busy} disabled={busy || value.trim() !== "PURGE"} onClick={() => void purge()}>{busy ? t("purge.purging") : t("purge.submit")}</Button></div>
  </section>;
}
