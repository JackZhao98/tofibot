import {useCallback, useEffect, useId, useRef, useState, type FormEvent} from "react";
import {adminApi, type AdminAccount} from "./adminApi";
import {adminOrgApi, backupApi, backupError, orgError, type BackupSnapshot, type BackupView, type OrgResource} from "./adminOrgApi";
import {ApiError} from "./api";
import {useTranslation} from "./i18n";
import {ConfirmAction} from "./ConfirmAction";
import {CardHead, formatVerified} from "./ProviderSettings";
import {SettingsCard, SettingsSection, type BadgeState} from "./settings/components";
import {useUserTimezone} from "./UserTimezone";
import {Select} from "./Select";
import { Button } from "./ds/Button/Button";

/** The Recovery Key is typed as 8 groups of 4; the last two groups confirm it was written down. */
const lastTwoGroups = (key: string) => key.split("-").slice(-2).join("").toUpperCase();

/** Settings > Administration > Organization resources > Backups: the off-site destination, the Recovery Key, the schedule and what it did. */
export function BackupsSection({resource, onResource}: {resource?: OrgResource; onResource: (resource: OrgResource) => void}) {
  const {t} = useTranslation("settings");
  const {timezone} = useUserTimezone();
  const titleId = useId(), formId = useId();
  const [view, setView] = useState<BackupView | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState({endpoint: "", region: "", bucket: "", prefix: "", pathStyle: false, accessKey: "", secret: ""});
  const [saving, setSaving] = useState(false);
  const [freshKey, setFreshKey] = useState("");
  const [typed, setTyped] = useState("");
  const [importKey, setImportKey] = useState("");
  const [snapshots, setSnapshots] = useState<BackupSnapshot[] | null>(null);
  const [draft, setDraft] = useState<BackupView["settings"] | null>(null);
  const seq = useRef(0);
  const load = useCallback(async () => {
    const mine = ++seq.current;
    try {
      const next = await backupApi.state();
      if (!next?.status || !next.settings || !next.key) throw new Error("Invalid backup view");
      if (mine === seq.current) { setView(next); setDraft(value => value ?? next.settings); setError(""); }
    }
    catch (cause) { if (mine === seq.current) setError(backupError(cause)); }
  }, []);
  useEffect(() => { void load(); }, [load, resource?.updated_at]);
  const running = Boolean(view?.status.running);
  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, running ? 3000 : 30000);
    return () => window.clearInterval(timer);
  }, [load, running]);
  const when = (iso?: string) => iso ? formatVerified(iso, timezone) : t("admin.org.backup.never");
  const act = async (work: () => Promise<unknown>, done?: string) => {
    setError(""); setNotice("");
    try { await work(); if (done) setNotice(done); await load(); } catch (cause) { setError(backupError(cause)); }
  };
  const status = view?.status;
  const failed = status?.last_status === "failed" || status?.last_status === "blocked";
  // Run, Verify and the snapshot list open the repository with the data key: before the Recovery Key is created and
  // confirmed they could only fail (and a failed check alerts every admin). Verify also needs a snapshot to check.
  const keyReady = Boolean(view?.key.present && view?.key.confirmed_at);
  const hasSnapshot = Boolean(status?.last_success_at);
  const state: BadgeState = !view?.configured ? "need" : failed || view.stale || status?.verify_status === "failed" ? "bad" : view.settings.remote === "on" ? "ok" : "need";
  const showForm = !view?.configured || editing;
  const set = (key: keyof typeof form) => (event: {target: {value: string}}) => { setForm(value => ({...value, [key]: event.target.value})); setError(""); };
  const ready = form.endpoint.trim() && form.bucket.trim() && (view?.configured || (form.accessKey.trim() && form.secret.trim()));
  async function saveDestination(event: FormEvent) {
    event.preventDefault();
    if (saving || !ready) return;
    setSaving(true); setError(""); setNotice("");
    try {
      onResource(await adminOrgApi.putResource("backup_store", {endpoint: form.endpoint.trim(), region: form.region.trim(), bucket: form.bucket.trim(), prefix: form.prefix.trim(), path_style: form.pathStyle,
        ...(form.accessKey.trim() && form.secret.trim() ? {access_key_id: form.accessKey.trim(), key: form.secret.trim()} : {})} as never));
      setForm(value => ({...value, accessKey: "", secret: ""})); setEditing(false); setNotice(t("admin.org.saved", {name: t("admin.org.backup.name")})); await load();
    } catch (cause) { setError(orgError(cause)); } finally { setSaving(false); }
  }
  async function createKey() {
    setError(""); setNotice("");
    try { const out = await backupApi.recoveryKey(); setFreshKey(out.recovery_key ?? ""); setTyped(""); await load(); } catch (cause) { setError(backupError(cause)); }
  }
  const keyOk = freshKey !== "" && typed.replace(/[-\s]/g, "").toUpperCase() === lastTwoGroups(freshKey);
  const settings = draft ?? view?.settings;
  const patch = (change: Partial<BackupView["settings"]>) => setDraft(value => ({...(value ?? view!.settings), ...change}));
  const num = (key: "keep_daily" | "keep_weekly" | "keep_monthly" | "stale_after_hours") => (event: {target: {value: string}}) => patch({[key]: Number(event.target.value) || 0});
  return <SettingsSection id="backups" title={t("admin.org.backup.title")} description={t("admin.org.backup.help")}>
    <SettingsCard className="provider-list org-card"><article className="detail-content provider-card org-resource" data-resource="backup_store" aria-labelledby={titleId} aria-busy={saving}>
      <CardHead id={titleId} title={t("admin.org.backup.name")} state={state} status={!view?.configured ? t("admin.org.backup.not_configured") : view.stale ? t("admin.org.backup.stale") : undefined} tag={t("admin.org.backup.tag")}/>
      {view?.configured && view.destination && <p className="provider-key-line"><code className="provider-key-mask">{t("admin.org.file_store.summary", {bucket: view.destination.bucket, endpoint: view.destination.endpoint})}</code></p>}
      {view?.configured && <div className="provider-actions">
        <button type="button" className="text-button" aria-expanded={editing} onClick={() => setEditing(value => !value)}>{t("admin.org.edit")}</button>
        <button type="button" className="text-button" data-action="test" onClick={() => void act(async () => { onResource(await adminOrgApi.testResource("backup_store")); }, t("admin.org.file_store.test_ok"))}>{t("admin.org.file_store.test")}</button>
        <ConfirmAction label={t("providers.remove")} question={t("admin.org.remove_question", {name: t("admin.org.backup.name")})} onConfirm={async () => { try { await adminOrgApi.deleteResource("backup_store"); setView(null); setDraft(null); await load(); } catch (cause) { throw new Error(orgError(cause)); } }}/>
      </div>}
      {showForm && <form className="provider-key-form" onSubmit={event => void saveDestination(event)}>
        <label htmlFor={`${formId}-endpoint`}>{t("admin.org.file_store.endpoint")}</label>
        <input id={`${formId}-endpoint`} name="backup-endpoint" type="url" autoComplete="off" spellCheck={false} placeholder="https://<account>.r2.cloudflarestorage.com" value={form.endpoint} disabled={saving} onChange={set("endpoint")}/>
        <label htmlFor={`${formId}-region`}>{t("admin.org.file_store.region")}</label>
        <input id={`${formId}-region`} name="backup-region" type="text" autoComplete="off" spellCheck={false} placeholder="auto" value={form.region} disabled={saving} onChange={set("region")}/>
        <label htmlFor={`${formId}-bucket`}>{t("admin.org.file_store.bucket")}</label>
        <input id={`${formId}-bucket`} name="backup-bucket" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" value={form.bucket} disabled={saving} onChange={set("bucket")}/>
        <label htmlFor={`${formId}-prefix`}>{t("admin.org.file_store.prefix")}</label>
        <input id={`${formId}-prefix`} name="backup-prefix" type="text" autoComplete="off" spellCheck={false} placeholder="tofi" value={form.prefix} disabled={saving} onChange={set("prefix")}/>
        <label className="check-row"><input type="checkbox" name="backup-path-style" checked={form.pathStyle} disabled={saving} onChange={event => setForm(value => ({...value, pathStyle: event.target.checked}))}/>{t("admin.org.file_store.path_style")}</label>
        <label htmlFor={`${formId}-access`}>{t("admin.org.file_store.access_key")}</label>
        <input id={`${formId}-access`} name="backup-access-key" type="text" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" value={form.accessKey} disabled={saving} onChange={set("accessKey")}/>
        <label htmlFor={`${formId}-secret`}>{t("admin.org.file_store.secret_key")}</label>
        <input id={`${formId}-secret`} name="backup-secret-key" type="password" autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" value={form.secret} disabled={saving} onChange={set("secret")}/>
        <div className="provider-key-row"><Button type="submit" disabled={saving || !ready}>{saving ? t("providers.verifying") : t("admin.org.file_store.save")}</Button>{view?.configured && <Button type="button" disabled={saving} onClick={() => setEditing(false)}>{t("action.cancel")}</Button>}</div>
        <p className="field-note">{t("admin.org.backup.separate_bucket")}</p>
      </form>}

      {view?.configured && <div className="backup-key" data-section="recovery-key">
        <h4>{t("admin.org.backup.key_title")}</h4>
        {freshKey ? <div role="group" aria-label={t("admin.org.backup.key_title")}>
          <code className="backup-recovery-key" data-testid="backup-recovery-key">{freshKey}</code>
          <p className="field-note">{t("admin.org.backup.key_show_once")}</p>
          <label htmlFor={`${formId}-confirm`}>{t("admin.org.backup.key_confirm_label")}</label>
          <input id={`${formId}-confirm`} name="backup-key-confirm" type="text" autoComplete="off" spellCheck={false} value={typed} onChange={event => setTyped(event.target.value)}/>
          <Button type="button" size="sm" disabled={!keyOk} onClick={() => void act(async () => { await backupApi.confirmKey(); setFreshKey(""); setTyped(""); }, t("admin.org.backup.key_confirmed_notice"))}>{t("admin.org.backup.key_confirm")}</Button>
        </div> : <>
          <p className="field-note">{view.key.fingerprint ? (view.key.confirmed_at ? t("admin.org.backup.key_state", {fingerprint: view.key.fingerprint, time: when(view.key.confirmed_at)}) : t("admin.org.backup.key_unconfirmed", {fingerprint: view.key.fingerprint})) : t("admin.org.backup.key_none")}</p>
          <div className="provider-actions">
            {view.key.present ? <ConfirmAction label={t("admin.org.backup.key_rotate")} question={t("admin.org.backup.key_rotate_question")} onConfirm={async () => { try { const out = await backupApi.recoveryKey(); setFreshKey(out.recovery_key ?? ""); setTyped(""); await load(); } catch (cause) { throw new Error(backupError(cause)); } }}/>
              : <Button type="button" size="sm" onClick={() => void createKey()}>{t("admin.org.backup.key_create")}</Button>}
            {view.key.fingerprint && <a className="text-button" href="/api/admin/backups/recovery-card" download>{t("admin.org.backup.card")}</a>}
          </div>
          {!view.key.present && <div className="provider-key-row">
            <input aria-label={t("admin.org.backup.key_import")} name="backup-key-import" type="password" autoComplete="off" spellCheck={false} value={importKey} onChange={event => setImportKey(event.target.value)}/>
            <Button type="button" disabled={!importKey.trim()} onClick={() => void act(async () => { await backupApi.recoveryKey({import: importKey.trim()}); setImportKey(""); })}>{t("admin.org.backup.key_import")}</Button>
          </div>}
        </>}
      </div>}

      {view?.configured && settings && <div className="backup-settings" data-section="settings">
        <label htmlFor={`${formId}-remote`}>{t("admin.org.backup.remote")}</label>
        <Select id={`${formId}-remote`} data-field="remote" value={settings.remote} onValueChange={next => patch({remote: next as "on" | "off"})}><option value="off">{t("admin.org.backup.off")}</option><option value="on">{t("admin.org.backup.on")}</option></Select>
        <label htmlFor={`${formId}-schedule`}>{t("admin.org.backup.schedule")}</label>
        <Select id={`${formId}-schedule`} data-field="schedule" value={settings.schedule} onValueChange={next => patch({schedule: next as typeof settings.schedule})}>{(["daily", "6h", "hourly", "manual"] as const).map(option => <option key={option} value={option}>{t(`admin.org.backup.schedule_${option}`)}</option>)}</Select>
        <label htmlFor={`${formId}-verify`}>{t("admin.org.backup.verify")}</label>
        <Select id={`${formId}-verify`} data-field="verify" value={settings.verify} onValueChange={next => patch({verify: next as typeof settings.verify})}>{(["weekly", "monthly", "manual"] as const).map(option => <option key={option} value={option}>{t(`admin.org.backup.verify_${option}`)}</option>)}</Select>
        <label htmlFor={`${formId}-before-update`}>{t("admin.org.backup.before_update")}</label>
        <Select id={`${formId}-before-update`} data-field="before_update" aria-describedby={`${formId}-before-update-hint`} value={settings.before_update ?? "attempt"} onValueChange={next => patch({before_update: next as typeof settings.before_update})}>{(["attempt", "require", "off"] as const).map(option => <option key={option} value={option}>{t(`admin.org.backup.before_update_${option}`)}</option>)}</Select>
        <p id={`${formId}-before-update-hint`} className="backup-hint">{t("admin.org.backup.before_update_hint")}</p>
        {(["keep_daily", "keep_weekly", "keep_monthly", "stale_after_hours"] as const).map(key => <div key={key} className="provider-key-row"><label htmlFor={`${formId}-${key}`}>{t(`admin.org.backup.${key}`)}</label><input id={`${formId}-${key}`} data-field={key} type="number" min={1} value={settings[key]} onChange={num(key)}/></div>)}
        <Button type="button" size="sm" data-action="save-settings" onClick={() => void act(async () => { setView(await backupApi.settings(settings)); setDraft(null); }, t("admin.org.backup.settings_saved"))}>{t("admin.org.backup.save_settings")}</Button>
        <p className="field-note">{t("admin.org.backup.retention_note")}</p>
      </div>}

      {view?.configured && status && <div className="backup-status" role="status" data-section="status">
        <p>{t("admin.org.backup.last_backup")}: {running ? t("admin.org.backup.running") : status.last_success_at ? when(status.last_success_at) : t("admin.org.backup.never")}</p>
        {failed && <p className="is-failed" data-status={status.last_status}>{t("admin.org.backup.failed", {reason: t(`admin.org.backup.error.${status.last_code}` as "admin.org.backup.error.generic", {defaultValue: t("admin.org.backup.error.generic")}), account: status.last_account ? ` (${status.last_account})` : ""})}</p>}
        {status.prune_code && <p className="is-failed">{t("admin.org.backup.error.backup_prune_failed")}</p>}
        <p>{t("admin.org.backup.last_verified")}: {status.verified_at ? `${when(status.verified_at)}${status.verify_duration_ms ? ` · ${Math.max(1, Math.round(status.verify_duration_ms / 1000))} s` : ""}` : t("admin.org.backup.never")}</p>
        {status.verify_status === "failed" && <p className="is-failed" data-status="verify_failed">{t("admin.org.backup.failed", {reason: t(`admin.org.backup.error.${status.verify_code}` as "admin.org.backup.error.generic", {defaultValue: t("admin.org.backup.error.generic")}), account: ""})}</p>}
        <div className="provider-actions">
          <Button type="button" size="sm" data-action="run" disabled={running || !keyReady} onClick={() => void act(() => backupApi.run(), t("admin.org.backup.started"))}>{t("admin.org.backup.run_now")}</Button>
          <Button type="button" size="sm" data-action="verify" disabled={running || !keyReady || !hasSnapshot} onClick={() => void act(() => backupApi.verify(), t("admin.org.backup.started"))}>{t("admin.org.backup.verify_now")}</Button>
          <button type="button" className="text-button" data-action="snapshots" disabled={!keyReady} onClick={() => void act(async () => setSnapshots((await backupApi.snapshots()).snapshots))}>{t("admin.org.backup.snapshots_show")}</button>
        </div>
        {!keyReady && <p className="field-note" data-hint="key-not-ready">{t("admin.org.backup.error.backup_not_ready")}</p>}
        {snapshots && (snapshots.length === 0 ? <p className="field-note">{t("admin.org.backup.snapshots_none")}</p> : <ul className="backup-snapshots">{[...snapshots].reverse().map(item => <li key={item.name}>{when(item.at)} · {item.reason}</li>)}</ul>)}
      </div>}
      {view?.configured && <AccountRestore onError={setError} onNotice={setNotice}/>}
      {error && <p role="alert" className="admin-error">{error}</p>}
      {notice && <p role="status" className="field-note">{notice}</p>}
    </article></SettingsCard>
  </SettingsSection>;
}

/**
 * Restore one account from a snapshot: the account, the restore point, the typed username. Undo is offered for 14 days.
 * While the account has Bot runs at work the server refuses (`account_busy`) and nothing changes; the refusal offers to
 * interrupt them, which is the only way this panel stops a run.
 */
function AccountRestore({onError, onNotice}: {onError: (message: string) => void; onNotice: (message: string) => void}) {
  const {t} = useTranslation("settings");
  const {timezone} = useUserTimezone();
  const formId = useId();
  const [accounts, setAccounts] = useState<AdminAccount[]>([]);
  const [account, setAccount] = useState("");
  const [points, setPoints] = useState<BackupSnapshot[]>([]);
  const [undo, setUndo] = useState(false);
  const [snapshot, setSnapshot] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [held, setHeld] = useState<"restore" | "undo" | null>(null);
  useEffect(() => { void adminApi.accounts().then(list => setAccounts(list.filter(item => !item.deleting))).catch(() => {}); }, []);
  async function pick(id: string) {
    setAccount(id); setPoints([]); setSnapshot(""); setName(""); setHeld(null); onError("");
    if (!id) return;
    try { const out = await backupApi.accountSnapshots(id); setPoints(out.snapshots); setUndo(out.undo_available); setSnapshot(out.snapshots[0]?.name ?? ""); } catch (cause) { onError(backupError(cause)); }
  }
  const target = accounts.find(item => item.id === account);
  function failed(action: "restore" | "undo", cause: unknown) {
    setHeld(cause instanceof ApiError && cause.code === "account_busy" ? action : null);
    onError(backupError(cause));
  }
  async function restore(interrupt = false) {
    setBusy(true); setHeld(null); onError(""); onNotice("");
    try { await backupApi.restoreAccount(account, {snapshot, confirm_username: name, ...(interrupt ? {interrupt: true} : {})}); onNotice(t("admin.org.backup.restore_done")); setName(""); await pick(account); } catch (cause) { failed("restore", cause); } finally { setBusy(false); }
  }
  async function undoRestore(interrupt = false) {
    setBusy(true); setHeld(null); onError(""); onNotice("");
    try { await backupApi.undoRestore(account, interrupt); onNotice(t("admin.org.backup.undo_done")); await pick(account); } catch (cause) { failed("undo", cause); } finally { setBusy(false); }
  }
  return <div className="backup-restore" data-section="restore-account">
    <h4>{t("admin.org.backup.restore_title")}</h4>
    <p className="field-note">{t("admin.org.backup.restore_help")}</p>
    <label htmlFor={`${formId}-account`}>{t("admin.org.backup.restore_account")}</label>
    <Select id={`${formId}-account`} value={account} onValueChange={next => void pick(next)}><option value="">—</option>{accounts.map(item => <option key={item.id} value={item.id}>{item.username}</option>)}</Select>
    {account && points.length === 0 && <p className="field-note">{t("admin.org.backup.snapshots_none")}</p>}
    {points.length > 0 && <>
      <label htmlFor={`${formId}-point`}>{t("admin.org.backup.restore_point")}</label>
      <Select id={`${formId}-point`} value={snapshot} onValueChange={setSnapshot}>{points.map(item => <option key={item.name} value={item.name}>{formatVerified(item.at, timezone)} · {item.rows?.conversations ?? 0} / {item.rows?.messages ?? 0}</option>)}</Select>
      <label htmlFor={`${formId}-name`}>{t("admin.org.backup.restore_confirm_name")}</label>
      <input id={`${formId}-name`} name="backup-restore-confirm" type="text" autoComplete="off" spellCheck={false} value={name} onChange={event => setName(event.target.value)}/>
      <Button type="button" size="sm" data-action="restore-account" disabled={busy || !snapshot || !target || name !== target.username} onClick={() => void restore()}>{t("admin.org.backup.restore_button")}</Button>
    </>}
    {account && undo && <button type="button" className="text-button" data-action="undo-restore" disabled={busy} onClick={() => void undoRestore()}>{t("admin.org.backup.undo")}</button>}
    {held && <button type="button" className="text-button" data-action="interrupt-runs" disabled={busy || (held === "restore" && name !== target?.username)} onClick={() => void (held === "restore" ? restore(true) : undoRestore(true))}>{t(held === "restore" ? "admin.org.backup.interrupt_and_restore" : "admin.org.backup.interrupt_and_undo")}</button>}
  </div>;
}
