import {useEffect, useState} from "react";
import {useTranslation} from "./i18n";
import {Banner, SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import {GIB} from "./adminApi";
import { Select } from "./Select";
import {formatBytes, storageApi, type AdminStorageAccount} from "./storageApi";

/** Limits offered in the menu, in GiB. The account's current limit is added when it is not one of these. */
const LIMIT_STEPS = [1, 5, 10, 25, 50, 100, 250, 500, 1000] as const;
const UNLIMITED = "unlimited";

/** Admin detail: the account's server storage and its limit. Saving "No limit" sends null. */
export function AdminStorageLimit({accountId, entry, busy, run}: {accountId: string; entry?: AdminStorageAccount; busy: boolean; run: (work: () => Promise<void>) => Promise<boolean>}) {
  const {t} = useTranslation("storage");
  const current = entry?.limit_bytes == null ? UNLIMITED : String(Math.round(entry.limit_bytes / GIB * 1000) / 1000);
  const [value, setValue] = useState(current);
  const [saved, setSaved] = useState(false);
  useEffect(() => { setValue(current); setSaved(false); }, [current, accountId]);
  const steps = Array.from(new Set<string>([...LIMIT_STEPS.map(String), ...(current === UNLIMITED ? [] : [current])])).sort((a, b) => Number(a) - Number(b));
  return <SettingsSection title={t("admin.limit_title")}>
    <SettingsCard>
      {entry && <SettingsRow label={t("admin.usage")} control={<span className="admin-value">{entry.limit_bytes == null ? formatBytes(entry.server_bytes) : t("used_of", {used: formatBytes(entry.server_bytes), limit: formatBytes(entry.limit_bytes)})}</span>}/>}
      <SettingsRow label={t("admin.limit_label")} labelFor="admin-storage-limit" description={saved ? t("admin.limit_saved") : t("admin.limit_help")}
        control={<Select id="admin-storage-limit" value={value} disabled={busy} onValueChange={next => { setValue(next); setSaved(false); }}>
          <option value={UNLIMITED}>{t("admin.limit_unlimited")}</option>
          {steps.map(step => <option key={step} value={step}>{t("admin.limit_option", {size: step})}</option>)}
        </Select>}/>
      <SettingsRow label={t("admin.limit_save")}
        control={<button type="button" className="secondary-button" disabled={busy || value === current}
          onClick={() => void run(async () => { await storageApi.setLimit(accountId, value === UNLIMITED ? null : Math.round(Number(value) * GIB)); setSaved(true); })}>{t("admin.limit_save")}</button>}/>
    </SettingsCard>
  </SettingsSection>;
}

/** Admin overview: the host disk line. Rendered only when the free space is low or known. */
export function AdminHostDisk({host}: {host: {free_bytes: number | null; total_bytes: number | null; min_free_bytes: number; low: boolean} | undefined}) {
  const {t} = useTranslation("storage");
  if (!host || host.free_bytes == null || host.total_bytes == null) return null;
  return <SettingsSection title={t("admin.host")}>
    {host.low ? <Banner tone="warn" title={t("admin.host_free", {free: formatBytes(host.free_bytes), total: formatBytes(host.total_bytes)})}>{t("admin.host_low", {floor: formatBytes(host.min_free_bytes)})}</Banner>
      : <SettingsCard><SettingsRow label={t("admin.host_free", {free: formatBytes(host.free_bytes), total: formatBytes(host.total_bytes)})}/></SettingsCard>}
  </SettingsSection>;
}
