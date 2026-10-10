import {useEffect, useState} from "react";
import {AccountBadges, AdminSheet, SecretField} from "./AdminSheets";
import {DISK_SIZES, GIB, LEGACY_OWNER_ID, adminApi, generatePassword, initialOf, type AdminAccount, type Capacity, type DiskInfo} from "./adminApi";
import {formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {Banner, DangerZone, Segmented, SettingsCard, SettingsRow, SettingsSection} from "./settings/components";

type Props = {
  account: AdminAccount;
  accounts: AdminAccount[];
  disk?: DiskInfo;
  capacity: Capacity | null;
  selfId: string;
  busy: boolean;
  run: (work: () => Promise<void>) => Promise<boolean>;
  onBack: () => void;
  onDelete: () => void;
};

/** One account: Role, Computer, Sign-in and the Danger zone. Destructive actions the server forbids are explained, not offered. */
export function AccountDetail({account, accounts, disk, capacity, selfId, busy, run, onBack, onDelete}: Props) {
  const {t} = useTranslation("settings");
  const isSelf = account.id === selfId;
  const isLegacy = account.id === LEGACY_OWNER_ID;
  const activeAdmins = accounts.filter(item => item.role === "admin" && !item.disabled && !item.deleting);
  const lastAdmin = account.role === "admin" && !account.disabled && activeAdmins.length <= 1;
  const deleting = Boolean(account.deleting);
  const roleLocked = isSelf || lastAdmin || deleting;
  const roleWhy = deleting ? t("admin.detail.role.deleting") : isSelf ? t("admin.detail.role.self") : lastAdmin ? t("admin.detail.role.last") : t("admin.detail.role.help");
  const currentGiB = disk ? Math.round(disk.quota_bytes / GIB) : 0;
  const freeGiB = capacity ? Math.max(0, Math.floor(capacity.admission_remaining_bytes / GIB)) : 0;
  const [size, setSize] = useState(currentGiB);
  useEffect(() => setSize(currentGiB), [currentGiB, account.id]);
  const sizes = Array.from(new Set<number>([...DISK_SIZES.filter(value => value >= currentGiB), currentGiB])).filter(value => value > 0).sort((a, b) => a - b);
  const [reset, setReset] = useState<"confirm" | {password: string} | null>(null);
  const [diskDone, setDiskDone] = useState(false);
  useEffect(() => setDiskDone(false), [account.id]);
  const stateLabel = disk ? t(`admin.computer.${disk.state === "ready" || disk.state === "disabled" ? disk.state : "reserved"}`) : t("admin.computer.none");

  async function resetPassword() {
    const password = generatePassword();
    const ok = await run(async () => { await adminApi.patch(account.id, {initial_password: password}); });
    setReset(ok ? {password} : null);
  }

  const deactivateWhy = isSelf ? t("admin.detail.danger.deactivate_self") : lastAdmin ? t("admin.detail.danger.deactivate_last") : "";
  const deleteWhy = isSelf ? t("admin.detail.danger.delete_self") : isLegacy ? t("admin.detail.danger.delete_legacy") : deleting ? t("admin.detail.danger.delete_running") : !account.disabled ? t("admin.detail.danger.delete_need_deactivate") : "";

  return <div className="admin-detail" data-testid="admin-detail">
    <button type="button" className="settings-ghost-button admin-back" onClick={onBack}><TofiIcon name="chevron-left" size={18} aria-hidden="true"/>{t("admin.back")}</button>
    <div className="admin-detail-head">
      <span className="admin-avatar is-large" aria-hidden="true">{initialOf(account.username)}</span>
      <div>
        <h3>{account.username}{isSelf && <small>{t("admin.you")}</small>}</h3>
        <p>{account.email.endsWith("@account.invalid") ? t("admin.no_email") : account.email}</p>
        <AccountBadges account={account}/>
      </div>
    </div>

    {deleting && <Banner tone={account.delete_error ? "error" : "info"} title={account.delete_error ? t("admin.deleting.stopped", {step: t(`admin.delete.step.${(account.delete_step || "export") as "export"}`)}) : t("admin.deleting.running")}
      action={{label: t("admin.delete.retry_action"), onClick: onDelete, disabled: busy}}>
      {account.delete_error ? t(`admin.deleting.error.${account.delete_error}` as "admin.deleting.error.generic", {defaultValue: t("admin.deleting.error.generic")}) : t("admin.deleting.body")}
    </Banner>}

    <SettingsSection title={t("admin.detail.role.title")}>
      <SettingsCard>
        <SettingsRow inline label={t("admin.detail.role.label")} description={roleWhy}
          control={<Segmented<"admin" | "user"> label={t("admin.detail.role.label")} value={account.role} disabled={roleLocked || busy}
            options={[{value: "admin", label: t("admin.role.admin")}, {value: "user", label: t("admin.role.user")}]}
            onChange={role => { if (role !== account.role) void run(async () => { await adminApi.patch(account.id, {role}); }); }}/>}/>
      </SettingsCard>
    </SettingsSection>

    <SettingsSection title={t("admin.detail.computer.title")}>
      <SettingsCard>
        <SettingsRow label={t("admin.detail.computer.state")} control={<span className="admin-value">{stateLabel}</span>}/>
        {disk ? <>
          <SettingsRow label={t("admin.detail.computer.disk")} labelFor="admin-disk-size" description={t("admin.detail.computer.disk_help")}
            control={<select id="admin-disk-size" value={size} disabled={busy || deleting} onChange={event => { setSize(Number(event.target.value)); setDiskDone(false); }}>
              {sizes.map(value => <option key={value} value={value} disabled={value - currentGiB > freeGiB}>{t("admin.detail.computer.option", {size: formatNumber(value)})}{value === currentGiB ? ` · ${t("admin.detail.computer.current")}` : value - currentGiB > freeGiB ? ` · ${t("admin.detail.computer.no_room")}` : ""}</option>)}
            </select>}/>
          <SettingsRow label={t("admin.detail.computer.save_label")} description={disk.pending_quota ? t("admin.detail.computer.disk_pending") : diskDone ? t("admin.detail.computer.saved") : t("admin.detail.computer.disk_note")}
            control={<button type="button" className="secondary-button" disabled={busy || deleting || size === currentGiB}
              onClick={() => void run(async () => { await adminApi.quota(account.id, size); setDiskDone(true); })}>{t("admin.detail.computer.save", {size: formatNumber(size)})}</button>}/>
        </> : <p className="settings-card-note">{t("admin.detail.computer.none")}</p>}
      </SettingsCard>
    </SettingsSection>

    <SettingsSection title={t("admin.detail.signin.title")}>
      <SettingsCard>
        <SettingsRow label={t("admin.detail.signin.label")} description={deleting ? t("admin.detail.signin.deleting") : t("admin.detail.signin.help")}
          control={<button type="button" className="secondary-button" disabled={busy || deleting} onClick={() => setReset("confirm")}>{t("admin.detail.signin.action")}</button>}/>
      </SettingsCard>
    </SettingsSection>

    <DangerZone title={t("admin.detail.danger.title")}>
      <div className="admin-danger-rows">
        {account.disabled
          ? <SettingsRow label={t("admin.detail.danger.reactivate_label")} description={deleting ? t("admin.detail.danger.reactivate_deleting") : t("admin.detail.danger.reactivate_help")}
              control={<button type="button" className="secondary-button" disabled={busy || deleting} onClick={() => void run(async () => { await adminApi.patch(account.id, {disabled: false}); })}>{t("admin.detail.danger.reactivate_action")}</button>}/>
          : <SettingsRow label={t("admin.detail.danger.deactivate_label")} description={deactivateWhy || t("admin.detail.danger.deactivate_help")}
              control={<button type="button" className="admin-danger-button" disabled={busy || Boolean(deactivateWhy)} onClick={() => void run(async () => { await adminApi.patch(account.id, {disabled: true}); })}>{t("admin.detail.danger.deactivate_action")}</button>}/>}
        <SettingsRow label={t("admin.detail.danger.delete_label")} description={deleteWhy || t("admin.detail.danger.delete_help")}
          control={<button type="button" className="admin-danger-button" disabled={busy || Boolean(deleteWhy)} onClick={onDelete}>{t("admin.detail.danger.delete_action")}</button>}/>
      </div>
    </DangerZone>

    {reset === "confirm" && <AdminSheet title={t("admin.detail.reset.confirm_title", {name: account.username})} onClose={() => setReset(null)} locked={busy}>
      <p className="admin-sheet-lead">{t("admin.detail.reset.confirm_body")}</p>
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={() => setReset(null)}>{t("action.cancel")}</button>
        <button type="button" className="primary-button" disabled={busy} onClick={() => void resetPassword()}>{t("admin.detail.reset.confirm_action")}</button>
      </div>
    </AdminSheet>}
    {reset && reset !== "confirm" && <AdminSheet title={t("admin.detail.reset.done_title")} onClose={() => setReset(null)} locked>
      <p className="admin-sheet-lead">{t("admin.detail.reset.done_body", {name: account.username})}</p>
      <SecretField label={t("admin.detail.reset.password")} value={reset.password} note={t("admin.detail.reset.once")}/>
      <div className="sheet-actions"><button type="button" className="primary-button" onClick={() => setReset(null)}>{t("admin.detail.reset.done")}</button></div>
    </AdminSheet>}
  </div>;
}
