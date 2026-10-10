import {useEffect, useRef, useState} from "react";
import {ApiError} from "./api";
import {AdminSheet, CopyButton, SecretField} from "./AdminSheets";
import {DELETE_STEPS, GIB, absoluteLink, adminApi, adminError, type AdminAccount, type DeletedExport} from "./adminApi";
import {formatDateTime, formatNumber} from "./i18n/format";
import {useTranslation} from "./i18n";
import {TofiIcon} from "./icons";
import {Banner} from "./settings/components";

/**
 * Confirmation, progress and retry for deleting an account. The server does the
 * work in one request; while it runs the account list is polled so each finished
 * step shows. A stopped deletion keeps the account in "Deleting" and offers Retry.
 */
export function DeleteSheet({account, diskBytes, retry, onClose, onDone, onRefresh}: {account: AdminAccount; diskBytes: number; /** Resuming a stopped deletion: no typing, the admin already confirmed. */ retry: boolean; onClose: () => void; onDone: (result?: DeletedExport) => void; onRefresh: () => Promise<AdminAccount[]>}) {
  const {t} = useTranslation("settings");
  const [typed, setTyped] = useState("");
  const [phase, setPhase] = useState<"confirm" | "working" | "stopped">(retry && !account.delete_error ? "confirm" : retry ? "stopped" : "confirm");
  const [error, setError] = useState("");
  const [progress, setProgress] = useState<{step: string; error: string}>({step: account.delete_step ?? "", error: account.delete_error ?? ""});
  const running = useRef(false);
  const matches = typed === account.username;
  const sizeGiB = Math.round(diskBytes / GIB);

  useEffect(() => {
    if (phase !== "working") return;
    let live = true;
    const timer = window.setInterval(() => {
      onRefresh().then(list => {
        const current = list.find(item => item.id === account.id);
        if (live && current) setProgress({step: current.delete_step ?? "", error: current.delete_error ?? ""});
      }).catch(() => {});
    }, 1000);
    return () => { live = false; window.clearInterval(timer); };
  }, [phase, account.id, onRefresh]);

  async function run() {
    if (running.current || (!retry && !matches)) return;
    running.current = true;
    setError("");
    setPhase("working");
    try {
      const result = await adminApi.remove(account.id, account.username);
      onDone(result.export);
    } catch (cause) {
      const stopped = cause instanceof ApiError && cause.code === "account_delete_failed";
      try {
        const list = await onRefresh();
        const current = list.find(item => item.id === account.id);
        if (current) setProgress({step: current.delete_step ?? "", error: current.delete_error ?? ""});
        else if (!stopped) { onDone(); return; }
      } catch { /* the list reload is best effort */ }
      if (stopped) setPhase("stopped");
      else { setError(adminError(cause)); setPhase("confirm"); }
    } finally {
      running.current = false;
    }
  }

  const doneIndex = progress.step ? DELETE_STEPS.indexOf(progress.step as (typeof DELETE_STEPS)[number]) : -1;
  const failedIndex = progress.error ? DELETE_STEPS.indexOf((progress.step || DELETE_STEPS[0]) as (typeof DELETE_STEPS)[number]) : -1;
  const steps = <ol className="admin-steps" aria-label={t("admin.delete.steps_label")}>
    {DELETE_STEPS.map((step, index) => {
      const failed = phase === "stopped" && progress.error && index === failedIndex;
      const done = !failed && index <= doneIndex && !(progress.error && index === doneIndex);
      const active = phase === "working" && !done && index === doneIndex + 1;
      const state = failed ? "failed" : done ? "done" : active ? "active" : "pending";
      return <li key={step} data-state={state}>
        <TofiIcon name={failed ? "error" : done ? "check-circle" : active ? "loading" : "minus"} size={16} aria-hidden="true"/>
        <span>{t(`admin.delete.step.${step}`)}</span>
      </li>;
    })}
  </ol>;

  const busy = phase === "working";
  const title = phase === "confirm" && !retry ? t("admin.delete.title", {name: account.username}) : retry || phase !== "confirm" ? t("admin.delete.progress_title", {name: account.username}) : "";
  return <AdminSheet title={title} onClose={onClose} locked={busy} className="admin-delete-sheet">
    {phase === "confirm" && !retry && <>
      <p className="admin-sheet-lead">{t("admin.delete.lead")}</p>
      <ul className="admin-delete-list">
        <li>{t("admin.delete.item_bots")}</li>
        <li>{t("admin.delete.item_chats")}</li>
        <li>{t("admin.delete.item_memory")}</li>
        <li>{t("admin.delete.item_files")}</li>
        <li>{diskBytes > 0 ? t("admin.delete.item_computer", {size: formatNumber(sizeGiB)}) : t("admin.delete.item_computer_none")}</li>
      </ul>
      <p className="admin-irreversible"><TofiIcon name="alert" size={16} aria-hidden="true"/>{t("admin.delete.irreversible")}</p>
      <p className="admin-sheet-note">{t("admin.delete.export_note")}</p>
      <label className="admin-confirm-field">{t("admin.delete.confirm_label", {name: account.username})}
        <input value={typed} onChange={event => setTyped(event.target.value)} autoComplete="off" autoCapitalize="off" spellCheck={false} aria-describedby="admin-delete-error" onKeyDown={event => { if (event.key === "Enter") { event.preventDefault(); void run(); } }}/>
      </label>
      {error && <p id="admin-delete-error" role="alert" className="admin-error">{error}</p>}
      <div className="sheet-actions">
        <button type="button" className="secondary-button" onClick={onClose}>{t("action.cancel")}</button>
        <button type="button" className="admin-danger-button" disabled={!matches} onClick={() => void run()}>{t("admin.delete.confirm_action")}</button>
      </div>
    </>}
    {(retry || phase !== "confirm") && <>
      {phase === "stopped" && <Banner tone="error" title={t("admin.deleting.stopped", {step: t(`admin.delete.step.${(progress.step || "export") as (typeof DELETE_STEPS)[number]}`)})}>{t(`admin.deleting.error.${progress.error || "generic"}` as "admin.deleting.error.generic", {defaultValue: t("admin.deleting.error.generic")})}</Banner>}
      {phase === "confirm" && retry && <p className="admin-sheet-lead">{t("admin.delete.retry_body")}</p>}
      {phase === "working" && <p className="admin-sheet-lead" role="status">{t("admin.delete.working")}</p>}
      {steps}
      {error && <p role="alert" className="admin-error">{error}</p>}
      <div className="sheet-actions">
        <button type="button" className="secondary-button" disabled={busy} onClick={onClose}>{t("admin.delete.close")}</button>
        <button type="button" className="admin-danger-button" disabled={busy} onClick={() => void run()}>{t("admin.delete.retry_action")}</button>
      </div>
    </>}
  </AdminSheet>;
}

/** Shown once after a deletion: link and passphrase separately, plus a message to send. The sheet only closes on acknowledgement. */
export function ExportResultSheet({result, onClose}: {result: DeletedExport; onClose: () => void}) {
  const {t} = useTranslation("settings");
  const [passphrase, setPassphrase] = useState(result.passphrase ?? "");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const link = absoluteLink(result.link_path);
  const expires = formatDateTime(result.expires_at * 1000, {dateStyle: "long"});
  useEffect(() => {
    if (passphrase) return;
    adminApi.passphrase(result.id).then(value => setPassphrase(value.passphrase)).catch(cause => setError(adminError(cause)));
  }, [passphrase, result.id]);
  async function done() {
    setBusy(true);
    try { await adminApi.acknowledge(result.id); onClose(); } catch (cause) { setError(adminError(cause)); setBusy(false); }
  }
  const message = t("admin.result.message", {link, date: expires});
  return <AdminSheet title={t("admin.result.title", {name: result.username})} onClose={onClose} locked className="admin-result-sheet">
    <p className="admin-sheet-lead">{t("admin.result.body")}</p>
    <SecretField label={t("admin.result.link")} value={link} note={t("admin.result.expires", {date: expires})}/>
    <SecretField label={t("admin.result.passphrase")} value={passphrase} note={t("admin.result.once")}/>
    <div className="admin-secret">
      <label htmlFor="admin-result-message">{t("admin.result.message_label")}</label>
      <textarea id="admin-result-message" readOnly rows={4} value={message} onFocus={event => event.currentTarget.select()}/>
      <div className="admin-secret-actions"><CopyButton value={message} label={t("admin.result.copy_message")}/></div>
      <p>{t("admin.result.message_note")}</p>
    </div>
    {error && <p role="alert" className="admin-error">{error}</p>}
    <div className="sheet-actions">
      <button type="button" className="primary-button" disabled={busy || !passphrase} onClick={() => void done()}>{t("admin.result.done")}</button>
    </div>
  </AdminSheet>;
}
