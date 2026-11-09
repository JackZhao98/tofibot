import {useCallback, useEffect, useRef, useState} from "react";
import {AccountDetail} from "./AdminAccountDetail";
import {GroupMenu, useGroups} from "./AdminGroupBits";
import {adminOrgApi, orgError} from "./adminOrgApi";
import {DeleteSheet, ExportResultSheet} from "./AdminDelete";
import {AccountBadges, AdminSheet, CopyButton} from "./AdminSheets";
import {GIB, absoluteLink, adminApi, adminError, initialOf, type AdminAccount, type AdminAccountSpend, type Capacity, type DeletedExport} from "./adminApi";
import "./admin-accounts.css";
import "./admin-org.css";
import {formatDateTime, formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {useOwnerSession} from "./OwnerSession";
import {useHostHasComputer} from "./computerCapability";
import {Banner, SettingsCard, SettingsSection} from "./settings/components";
import {AdminHostDisk} from "./AdminStorage";
import {adminSpendText} from "./AdminSpend";
import {formatBytes, storageApi, type AdminStorage} from "./storageApi";

const gib = (bytes: number) => formatNumber(Math.round((bytes / GIB) * 10) / 10);

export function AdminAccounts() {
  const {t} = useTranslation("settings");
  const {t: ts} = useTranslation("storage");
  const hasComputer = useHostHasComputer();
  const session = useOwnerSession();
  const allowed = Boolean(session?.multi_account && session.owner?.role === "admin");
  const selfId = session?.owner?.id ?? "";
  const [accounts, setAccounts] = useState<AdminAccount[] | null>(null);
  const [capacity, setCapacity] = useState<Capacity | null>(null);
  const [capacityError, setCapacityError] = useState(false);
  const [storage, setStorage] = useState<AdminStorage | null>(null);
  const [spend, setSpend] = useState<AdminAccountSpend[]>([]);
  const [exportsList, setExportsList] = useState<DeletedExport[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const busyRef = useRef(false);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState<{account: AdminAccount; diskBytes: number; retry: boolean} | null>(null);
  const [result, setResult] = useState<DeletedExport | null>(null);
  const [picked, setPicked] = useState<string[]>([]);
  const [exportAction, setExportAction] = useState<{kind: "delete" | "passphrase"; item: DeletedExport} | null>(null);

  const refreshAccounts = useCallback(async () => {
    const list = await adminApi.accounts();
    setAccounts(list);
    return list;
  }, []);
  const refresh = useCallback(async () => {
    const list = await refreshAccounts();
    const [capacityResult, exportsResult, storageResult, spendResult] = await Promise.allSettled([hasComputer ? adminApi.capacity() : Promise.reject(new Error("no computer")), adminApi.exports(), storageApi.admin(), adminApi.spend()]);
    if (spendResult.status === "fulfilled") setSpend(spendResult.value.accounts);
    if (storageResult.status === "fulfilled") setStorage(storageResult.value);
    if (capacityResult.status === "fulfilled") { setCapacity(capacityResult.value); setCapacityError(false); } else { setCapacity(null); setCapacityError(hasComputer); }
    if (exportsResult.status === "fulfilled") setExportsList(exportsResult.value);
    return list;
  }, [refreshAccounts, hasComputer]);

  useEffect(() => { if (allowed) void refresh().catch(cause => setError(adminError(cause))); }, [allowed, refresh]);
  const {groups, reload: reloadGroups} = useGroups(allowed);
  // A selection only holds accounts that still exist.
  useEffect(() => { if (accounts) setPicked(current => current.filter(id => accounts.some(account => account.id === id))); }, [accounts]);

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

  /** Moves accounts to a group in one call (the server moves all or none) and shows the new group at once. */
  const moveTo = async (ids: string[], groupId: string) => {
    const target = groups?.find(group => group.id === groupId);
    if (!target) return;
    let failure = "";
    const ok = await run(async () => { try { await adminOrgApi.moveMembers(groupId, ids); } catch (cause) { failure = orgError(cause); throw cause; } });
    if (ok) { reloadGroups(); setPicked([]); }
    else if (failure) setError(failure);
  };

  if (!allowed) return null;
  const detailAccount = selected ? accounts?.find(item => item.id === selected) : undefined;
  const diskOf = (id: string) => capacity?.accounts.find(item => item.account_id === id);
  const storageOf = (id: string) => storage?.accounts.find(item => item.account_id === id);
  const spendOf = (id: string) => spend.find(item => item.account_id === id);
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
      <AccountDetail account={detailAccount} accounts={accounts ?? []} disk={diskOf(detailAccount.id)} storage={storageOf(detailAccount.id)} spend={spendOf(detailAccount.id)} capacity={capacity} selfId={selfId} busy={busy} run={run}
        onBack={() => { setSelected(null); setError(""); }}
        onDelete={() => setDeleting({account: detailAccount, diskBytes: diskOf(detailAccount.id)?.quota_bytes ?? 0, retry: Boolean(detailAccount.deleting)})}/>
      {sheets}
    </section>;
  }

  return <section className="admin-accounts" aria-busy={busy}>
    <p className="admin-intro">{t(hasComputer ? "admin.intro" : "admin.intro_no_computer")}</p>
    {error && <p role="alert" className="admin-error">{error}</p>}

    {hasComputer && <SettingsSection title={t("admin.summary.title")}>
      {capacity ? <SettingsCard className="admin-capacity">
        <dl>
          <div><dt>{t("admin.summary.total")}</dt><dd>{gib(total)} <small>GiB</small></dd></div>
          <div><dt>{t("admin.summary.allocated")}</dt><dd>{gib(allocated)} <small>GiB</small></dd></div>
          <div><dt>{t("admin.summary.free")}</dt><dd>{gib(free)} <small>GiB</small></dd></div>
        </dl>
        {capacity.warning && <p className="admin-capacity-low"><TofiIcon name="alert" size={16} aria-hidden="true"/>{t("admin.summary.low")}</p>}
      </SettingsCard> : capacityError ? <Banner tone="warn" title={t("admin.capacity_unavailable")}/> : null}
    </SettingsSection>}

    <AdminHostDisk host={storage?.host}/>

    <SettingsSection title={t("admin.accounts.title")} hint={accounts ? formatNumber(accounts.length) : undefined}>
      <div className="admin-list-actions"><button type="button" className="primary-button" onClick={() => setAdding(true)} disabled={busy}><TofiIcon name="user-add" size={16} aria-hidden="true"/>{t("admin.add")}</button></div>
      <SettingsCard className="admin-list">
        <div className="admin-row-head" aria-hidden="true"><span/><span className="admin-row-head-main"><span/><span/><span/><span className="admin-row-usage"><span>{ts("admin.server")}</span><span>{t("spend.admin.column")}</span>{hasComputer && <span>{ts("admin.computer")}</span>}</span></span><span>{t("admin.accounts.group_col")}</span></div>
        {accounts?.length === 0 && <p className="settings-card-note admin-empty">{t("admin.accounts.empty")}</p>}
        {accounts?.map(account => {
          const disk = diskOf(account.id);
          const computer = disk ? `${t(`admin.computer.${disk.state === "ready" || disk.state === "disabled" ? disk.state : "reserved"}`)} · ${t("admin.detail.computer.option", {size: formatNumber(Math.round(disk.quota_bytes / GIB))})}` : t("admin.computer.none");
          const usage = storageOf(account.id);
          return <div key={account.id} className="admin-row-wrap" data-selected={picked.includes(account.id) || undefined}>
          <label className="admin-row-check"><input type="checkbox" data-select={account.id} checked={picked.includes(account.id)} disabled={busy || Boolean(account.deleting)} aria-label={t("admin.accounts.select", {name: account.username})} onChange={event => setPicked(current => event.target.checked ? [...current, account.id] : current.filter(id => id !== account.id))}/></label>
          <button type="button" className="admin-row" data-account={account.id} onClick={() => setSelected(account.id)} aria-label={t("admin.open", {name: account.username})}>
            <span className="admin-avatar" aria-hidden="true">{initialOf(account.username)}</span>
            <span className="admin-row-main">
              <strong><span className="admin-name" title={account.username}>{account.username}</span>{account.id === selfId && <small>{t("admin.you")}</small>}</strong>
              <span className="admin-row-email" title={account.email.endsWith("@account.invalid") ? undefined : account.email}>{account.email.endsWith("@account.invalid") ? t("admin.no_email") : account.email}</span>
            </span>
            <AccountBadges account={account}/>
            <span className="admin-row-usage">
              <span className={`admin-row-storage${usage?.over_limit ? " is-over" : ""}`} data-column="server">{usage && usage.source !== "none" ? `${formatBytes(usage.server_bytes)}${usage.limit_bytes == null ? "" : ` / ${formatBytes(usage.limit_bytes)}`}` : "—"}</span>
              <span className={`admin-row-storage${spendOf(account.id)?.state === "reached" ? " is-over" : ""}`} data-column="spend">{adminSpendText(t, spendOf(account.id))}</span>
              {hasComputer && <span className="admin-row-computer" data-column="computer">{computer}</span>}
            </span>
            <TofiIcon name="chevron-right" size={18} aria-hidden="true"/>
          </button>
          <span className="admin-row-group" data-column="group"><GroupMenu groups={groups ?? []} value={account.group?.id} disabled={busy || Boolean(account.deleting) || !account.group} label={t("admin.accounts.group_of", {name: account.username})} onPick={id => void moveTo([account.id], id)}/></span>
          </div>;
        })}
      </SettingsCard>
      {picked.length > 0 && <div className="admin-bulk" role="region" aria-label={t("admin.accounts.bulk")} data-testid="admin-bulk">
        <span className="admin-bulk-count" role="status">{t("admin.accounts.selected", {count: picked.length})}</span>
        <GroupMenu groups={groups ?? []} disabled={busy} label={t("admin.accounts.move_n", {count: picked.length})} text={t("admin.accounts.move_n", {count: picked.length})} onPick={id => void moveTo(picked, id)}/>
        <button type="button" className="text-button" onClick={() => setPicked([])}>{t("admin.accounts.clear")}</button>
      </div>}
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
