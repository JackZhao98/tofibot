import {useCallback, useEffect, useRef, useState} from "react";
import {AccountDetail} from "./AdminAccountDetail";
import {DeleteSheet, ExportResultSheet} from "./AdminDelete";
import {AccountBadges, AdminSheet, CopyButton} from "./AdminSheets";
import {GIB, absoluteLink, adminApi, adminError, initialOf, type AdminAccount, type Capacity, type DeletedExport} from "./adminApi";
import "./admin-accounts.css";
import {formatDateTime, formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {useOwnerSession} from "./OwnerSession";
import {Banner, SettingsCard, SettingsSection} from "./settings/components";

const gib = (bytes: number) => formatNumber(Math.round((bytes / GIB) * 10) / 10);

export function AdminAccounts() {
  const {t} = useTranslation("settings");
  const session = useOwnerSession();
  const allowed = Boolean(session?.multi_account && session.owner?.role === "admin");
  const selfId = session?.owner?.id ?? "";
  const [accounts, setAccounts] = useState<AdminAccount[] | null>(null);
  const [capacity, setCapacity] = useState<Capacity | null>(null);
  const [capacityError, setCapacityError] = useState(false);
  const [exportsList, setExportsList] = useState<DeletedExport[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<{account: AdminAccount; diskBytes: number; retry: boolean} | null>(null);
  const [result, setResult] = useState<DeletedExport | null>(null);
  const [exportAction, setExportAction] = useState<{kind: "delete" | "passphrase"; item: DeletedExport} | null>(null);

  const refreshAccounts = useCallback(async () => {
    const list = await adminApi.accounts();
    setAccounts(list);
    return list;
  }, []);
  const refresh = useCallback(async () => {
    const list = await refreshAccounts();
    const [capacityResult, exportsResult] = await Promise.allSettled([adminApi.capacity(), adminApi.exports()]);
    if (capacityResult.status === "fulfilled") { setCapacity(capacityResult.value); setCapacityError(false); } else { setCapacity(null); setCapacityError(true); }
    if (exportsResult.status === "fulfilled") setExportsList(exportsResult.value);
    return list;
  }, [refreshAccounts]);

  useEffect(() => { if (allowed) void refresh().catch(cause => setError(adminError(cause))); }, [allowed, refresh]);

  /** One change at a time. Returns whether it succeeded; the failure text is shown on the page. */
  const run = useCallback(async (work: () => Promise<void>) => {
    if (busyRef.current) return false;
    busyRef.current = true; setBusy(true); setError("");
    let ok = false;
    try { await work(); ok = true; } catch (cause) { setError(adminError(cause)); }
    try { await refresh(); } catch { /* the page keeps what it had */ }
    busyRef.current = false; setBusy(false);
    return ok;
  }, [refresh]);

  if (!allowed) return null;
  const detailAccount = selected ? accounts?.find(item => item.id === selected) : undefined;
  const diskOf = (id: string) => capacity?.accounts.find(item => item.account_id === id);
  const total = capacity?.total_bytes ?? capacity?.available_bytes ?? 0;
  const allocated = capacity?.promised_bytes ?? capacity?.allocated_bytes ?? 0;
  const free = Math.max(0, capacity?.admission_remaining_bytes ?? 0);

  const sheets = <>
    {adding && <CreateSheet onClose={() => setAdding(false)} onCreated={async () => { setAdding(false); await refresh().catch(() => {}); }}/>}
    {deleting && <DeleteSheet account={deleting.account} diskBytes={deleting.diskBytes} retry={deleting.retry} onRefresh={refreshAccounts}
      onClose={() => { setDeleting(null); void refresh().catch(() => {}); }}
      onDone={async export_ => { setDeleting(null); setSelected(null); await refresh().catch(() => {}); if (export_) setResult(export_); }}/>}
    {result && <ExportResultSheet result={result} onClose={() => { setResult(null); void refresh().catch(() => {}); }}/>}
    {exportAction?.kind === "passphrase" && <ExportResultSheet result={exportAction.item} onClose={() => { setExportAction(null); void refresh().catch(() => {}); }}/>}
    {exportAction?.kind === "delete" && <AdminSheet title={t("admin.exports.delete_title", {name: exportAction.item.username})} onClose={() => setExportAction(null)} locked={busy}>
      <p className="admin-sheet-lead">{t("admin.exports.delete_body")}</p>
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={() => setExportAction(null)}>{t("action.cancel")}</button>
        <button type="button" className="admin-danger-button" disabled={busy} onClick={() => void run(async () => { await adminApi.removeExport(exportAction.item.id); }).then(() => setExportAction(null))}>{t("admin.exports.delete")}</button>
      </div>
    </AdminSheet>}
  </>;

  if (detailAccount) {
    return <section className="admin-accounts" aria-busy={busy}>
      {error && <p role="alert" className="admin-error">{error}</p>}
      <AccountDetail account={detailAccount} accounts={accounts ?? []} disk={diskOf(detailAccount.id)} capacity={capacity} selfId={selfId} busy={busy} run={run}
        onBack={() => { setSelected(null); setError(""); }}
        onDelete={() => setDeleting({account: detailAccount, diskBytes: diskOf(detailAccount.id)?.quota_bytes ?? 0, retry: Boolean(detailAccount.deleting)})}/>
      {sheets}
    </section>;
  }

  return <section className="admin-accounts" aria-busy={busy}>
    <p className="admin-intro">{t("admin.intro")}</p>
    {error && <p role="alert" className="admin-error">{error}</p>}

    <SettingsSection title={t("admin.summary.title")}>
      {capacity ? <SettingsCard className="admin-capacity">
        <dl>
          <div><dt>{t("admin.summary.total")}</dt><dd>{gib(total)} <small>GiB</small></dd></div>
          <div><dt>{t("admin.summary.allocated")}</dt><dd>{gib(allocated)} <small>GiB</small></dd></div>
          <div><dt>{t("admin.summary.free")}</dt><dd>{gib(free)} <small>GiB</small></dd></div>
        </dl>
        {capacity.warning && <p className="admin-capacity-low"><TofiIcon name="alert" size={16} aria-hidden="true"/>{t("admin.summary.low")}</p>}
      </SettingsCard> : capacityError ? <Banner tone="warn" title={t("admin.capacity_unavailable")}/> : null}
    </SettingsSection>

    <SettingsSection title={t("admin.accounts.title")} hint={accounts ? formatNumber(accounts.length) : undefined}>
      <div className="admin-list-actions"><button type="button" className="primary-button" onClick={() => setAdding(true)} disabled={busy}><TofiIcon name="user-add" size={16} aria-hidden="true"/>{t("admin.add")}</button></div>
      <SettingsCard className="admin-list">
        {accounts?.length === 0 && <p className="settings-card-note">{t("admin.accounts.empty")}</p>}
        {accounts?.map(account => {
          const disk = diskOf(account.id);
          const computer = disk ? `${t(`admin.computer.${disk.state === "ready" || disk.state === "disabled" ? disk.state : "reserved"}`)} · ${t("admin.detail.computer.option", {size: formatNumber(Math.round(disk.quota_bytes / GIB))})}` : t("admin.computer.none");
          return <button type="button" key={account.id} className="admin-row" data-account={account.id} onClick={() => setSelected(account.id)} aria-label={t("admin.open", {name: account.username})}>
            <span className="admin-avatar" aria-hidden="true">{initialOf(account.username)}</span>
            <span className="admin-row-main">
              <strong>{account.username}{account.id === selfId && <small>{t("admin.you")}</small>}</strong>
              <span className="admin-row-email">{account.email.endsWith("@account.invalid") ? t("admin.no_email") : account.email}</span>
            </span>
            <AccountBadges account={account}/>
            <span className="admin-row-computer">{computer}</span>
            <TofiIcon name="chevron-right" size={18} aria-hidden="true"/>
          </button>;
        })}
      </SettingsCard>
    </SettingsSection>

    <SettingsSection title={t("admin.exports.title")} description={t("admin.exports.help")}>
      <SettingsCard className="admin-exports">
        {exportsList.length === 0 && <p className="settings-card-note">{t("admin.exports.empty")}</p>}
        {exportsList.map(item => <div className="admin-export" key={item.id} data-export={item.id}>
          <div className="admin-export-main">
            <strong>{item.username}</strong>
            <span>{t("admin.exports.created", {date: formatDateTime(item.created_at * 1000, {dateStyle: "medium"})})} · {t("admin.exports.expires", {date: formatDateTime(item.expires_at * 1000, {dateStyle: "medium"})})} · {t("admin.exports.size", {value: formatNumber(Math.max(0.1, Math.round((item.size / 2 ** 20) * 10) / 10))})}</span>
            {!item.passphrase_pending && <span className="admin-export-gone">{t("admin.exports.passphrase_gone")}</span>}
            {item.link_unavailable && <span className="admin-export-gone" role="status">{t("admin.exports.link_unavailable")}</span>}
          </div>
          <div className="admin-export-actions">
            {!item.link_unavailable && <CopyButton value={absoluteLink(item.link_path)} label={t("admin.exports.copy_link")}/>}
            {item.passphrase_pending && !item.passphrase_unavailable && <button type="button" className="secondary-button" onClick={() => setExportAction({kind: "passphrase", item})}>{t("admin.exports.show_passphrase")}</button>}
            <button type="button" className="admin-danger-button" disabled={busy} onClick={() => setExportAction({kind: "delete", item})}>{t("admin.exports.delete")}</button>
          </div>
        </div>)}
      </SettingsCard>
    </SettingsSection>
    {sheets}
  </section>;
}

function CreateSheet({onClose, onCreated}: {onClose: () => void; onCreated: () => Promise<void>}) {
  const {t} = useTranslation("settings");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(form: HTMLFormElement) {
    const data = new FormData(form);
    const username = String(data.get("username") ?? "").trim();
    const email = String(data.get("email") ?? "").trim();
    if (!username && !email) { setError(t("admin.create.name_required")); return; }
    setBusy(true); setError("");
    try { await adminApi.create({username, email, password: String(data.get("password") ?? "")}); await onCreated(); }
    catch (cause) { setError(adminError(cause)); setBusy(false); }
  }
  return <AdminSheet title={t("admin.create.title")} onClose={onClose} locked={busy}>
    <form className="admin-create" onSubmit={event => { event.preventDefault(); void submit(event.currentTarget); }}>
      <label>{t("admin.create.username")}<input name="username" autoComplete="off" maxLength={64} disabled={busy}/></label>
      <label>{t("admin.create.email")}<input name="email" type="email" autoComplete="off" maxLength={254} disabled={busy}/></label>
      <label>{t("admin.create.password")}<input name="password" type="password" autoComplete="new-password" minLength={12} maxLength={1024} required disabled={busy}/></label>
      <p className="admin-sheet-note">{t("admin.create.password_note")}</p>
      {error && <p role="alert" className="admin-error">{error}</p>}
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("action.cancel")}</button>
        <button type="submit" className="primary-button" disabled={busy}>{t("admin.create.submit")}</button>
      </div>
    </form>
  </AdminSheet>;
}
