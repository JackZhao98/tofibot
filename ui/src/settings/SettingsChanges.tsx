import { useCallback, useEffect, useState } from "react";
import { ApiError, request } from "../api";
import { useTranslation } from "../i18n";
import { formatRelativeTime } from "../i18n/format";
import { useSettingLabel } from "../SettingsChangeNotice";
import type { SettingsChangeRow } from "../types";
import { SettingsCard, SettingsRow } from "./components";

const clip = (value: string) => { const text = value.replace(/\s+/g, " ").trim(); return text.length > 80 ? `${text.slice(0, 79)}…` : text; };

function ChangeRow({ change, busy, error, onRevert }: { change: SettingsChangeRow; busy: boolean; error: string; onRevert: (id: string) => void }) {
  const { t } = useTranslation("settings");
  const label = useSettingLabel(change.label_key) || change.key;
  const empty = t("changes.empty_value");
  const reverted = Boolean(change.reverted_by);
  const where = change.target_name ? ` · ${change.target_name}` : "";
  const description = <>
    {`${clip(change.old_value) || empty} → ${clip(change.new_value) || empty}`}
    <br />{`${change.bot_name ?? ""} · ${formatRelativeTime(change.at)}${where}`}
    {error && <><br /><span role="alert" className="error-text">{error}</span></>}
  </>;
  return <SettingsRow label={label} description={description}
    control={reverted ? <span className="muted">{t("changes.reverted")}</span>
      : change.revertable ? <button type="button" className="secondary-button" disabled={busy} onClick={() => onRevert(change.id)}>{busy ? t("changes.reverting") : t("changes.revert")}</button>
      : <span className="muted">{t("changes.not_revertable")}</span>} />;
}

/** Settings > Maintenance > Changes by Bots: the last settings a Bot changed through the settings tool, each with Revert. */
export function SettingsChanges() {
  const { t } = useTranslation("settings");
  const [rows, setRows] = useState<SettingsChangeRow[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const load = useCallback((signal?: AbortSignal) => request<{ changes: SettingsChangeRow[] }>("/api/settings-changes", { signal }).then(
    result => { setRows(result.changes); setFailed(false); },
    cause => { if (!signal?.aborted && !(cause instanceof DOMException)) setFailed(true); }), []);
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort(); }, [load]);
  async function revert(id: string) {
    setBusy(id);
    setErrors(current => ({ ...current, [id]: "" }));
    try {
      await request(`/api/settings-changes/${encodeURIComponent(id)}/revert`, { method: "POST" });
      await load();
    } catch (cause) {
      const message = cause instanceof ApiError && cause.code === "settings_changed_since" ? t("changes.stale") : t("changes.revert_failed");
      setErrors(current => ({ ...current, [id]: message }));
    } finally { setBusy(""); }
  }
  return <SettingsCard>
    {rows === null && !failed && <p className="muted settings-card-note" aria-live="polite">{t("changes.loading")}</p>}
    {failed && <p role="alert" className="error-text settings-card-note">{t("changes.load_failed")}</p>}
    {rows !== null && rows.length === 0 && <p className="muted settings-card-note">{t("changes.none")}</p>}
    {rows?.map(change => <ChangeRow key={change.id} change={change} busy={busy === change.id} error={errors[change.id] ?? ""} onRevert={id => void revert(id)} />)}
  </SettingsCard>;
}
