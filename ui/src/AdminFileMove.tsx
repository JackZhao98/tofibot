import {useCallback, useEffect, useRef, useState} from "react";
import {fileMoveApi, fileMoveError, type FileMoveDirection, type FileMoveView, type FileReport} from "./adminOrgApi";
import {useTranslation} from "./i18n";
import {ConfirmAction} from "./InteractionSystem";
import {formatBytes} from "./storageApi";

const REPORT_COUNTS = ["rows", "missing_in_store", "missing_on_disk", "size_mismatch", "sha_mismatch", "unreadable", "skipped_accounts", "orphans", "orphans_young"] as const;
const REPORT_PROBLEMS = new Set<string>(["missing_in_store", "missing_on_disk", "size_mismatch", "sha_mismatch", "unreadable", "skipped_accounts", "orphans"]);

/**
 * Inside the File storage card: move existing files between this server's disk and the bucket (progress, pause, resume, cancel;
 * the job lives on the server and resumes after a restart), reconcile rows against the bucket, and sweep orphaned objects.
 * Only stable codes reach this view; the bucket's own text is never shown.
 */
export function FileMovePanel({resourceKey}: {resourceKey?: string}) {
  const {t} = useTranslation("settings");
  const [view, setView] = useState<FileMoveView | null>(null);
  const [error, setError] = useState("");
  const [deep, setDeep] = useState(false);
  const seq = useRef(0);
  const load = useCallback(async () => {
    const mine = ++seq.current;
    try { const next = await fileMoveApi.state(); if (mine === seq.current) { setView(next); setError(""); } }
    catch (cause) { if (mine === seq.current) setError(fileMoveError(cause)); }
  }, []);
  useEffect(() => { void load(); }, [load, resourceKey]);
  const busy = Boolean(view?.busy) || view?.job?.status === "running" || view?.report?.status === "running";
  useEffect(() => {
    const timer = window.setInterval(() => { if (!document.hidden) void load(); }, busy ? 2000 : 30000);
    return () => window.clearInterval(timer);
  }, [load, busy]);
  const act = async (work: () => Promise<unknown>) => {
    setError("");
    try { await work(); await load(); } catch (cause) { setError(fileMoveError(cause)); await load(); }
  };
  if (!view) return error ? <p role="alert" className="admin-error">{error}</p> : null;
  const job = view.job;
  const active = Boolean(job && (job.status === "running" || job.status.startsWith("paused")));
  const paused = Boolean(job?.status.startsWith("paused"));
  const finished = job ? job.files_done + view.failures.count : 0;
  const percent = job && job.files_total > 0 ? Math.min(100, Math.round((finished / job.files_total) * 100)) : 0;
  const canStart = (direction: FileMoveDirection) => !busy && !active && (direction === "to_bucket" ? view.kind === "s3" && view.disk.files > 0 : view.kind === "disk" && view.bucket.files > 0);
  const report = view.report;
  return <div className="file-move" data-section="file-move">
    <h4>{t("admin.org.file_move.title")}</h4>
    <p className="field-note">{t("admin.org.file_move.help")}</p>
    <p className="field-note" data-field="counts">{t("admin.org.file_move.on_disk", {files: view.disk.files, size: formatBytes(view.disk.bytes)})} · {t("admin.org.file_move.in_bucket", {files: view.bucket.files, size: formatBytes(view.bucket.bytes)})}</p>
    {!active && <div className="provider-actions">
      <button type="button" className="st-button" data-action="move-to-bucket" disabled={!canStart("to_bucket")} onClick={() => void act(() => fileMoveApi.start("to_bucket"))}>{t("admin.org.file_move.to_bucket")}</button>
      <button type="button" className="st-button" data-action="move-to-disk" disabled={!canStart("to_disk")} onClick={() => void act(() => fileMoveApi.start("to_disk"))}>{t("admin.org.file_move.to_disk")}</button>
    </div>}
    {!active && view.kind === "disk" && view.disk.files > 0 && <p className="field-note">{t("admin.org.file_move.needs_bucket_destination")}</p>}
    {!active && view.kind === "s3" && view.bucket.files > 0 && <p className="field-note">{t("admin.org.file_move.needs_disk_destination")}</p>}
    {job && <div className="file-move-job" role="status" data-status={job.status}>
      <p>{t(`admin.org.file_move.direction_${job.direction}` as "admin.org.file_move.direction_to_bucket")} · {t(`admin.org.file_move.status.${job.status}` as "admin.org.file_move.status.running")}</p>
      {active && <>
        <span className="update-progress-bar" role="progressbar" aria-label={t("admin.org.file_move.title")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><i style={{width: `${percent}%`}}/></span>
        <p className="field-note">{t("admin.org.file_move.progress", {done: job.files_done, total: job.files_total, size: formatBytes(job.bytes_done), totalSize: formatBytes(job.bytes_total)})}</p>
      </>}
      {!active && <p className="field-note">{t("admin.org.file_move.progress", {done: job.files_done, total: job.files_total, size: formatBytes(job.bytes_done), totalSize: formatBytes(job.bytes_total)})}</p>}
      {active && <div className="provider-actions">
        {!paused && <button type="button" className="st-button" data-action="move-pause" onClick={() => void act(() => fileMoveApi.pause())}>{t("admin.org.file_move.pause")}</button>}
        {paused && <button type="button" className="st-button" data-action="move-resume" disabled={busy} onClick={() => void act(() => fileMoveApi.resume())}>{t("admin.org.file_move.resume")}</button>}
        <ConfirmAction label={t("admin.org.file_move.cancel")} question={t("admin.org.file_move.cancel_question")} onConfirm={async () => { await act(() => fileMoveApi.cancel()); }}/>
      </div>}
    </div>}
    {view.failures.count > 0 && <div className="file-move-failures" data-section="failures">
      <p className="is-failed">{t("admin.org.file_move.failed_count", {count: view.failures.count})}</p>
      <ul className="backup-snapshots">{Object.entries(view.failures.by_code).map(([code, count]) => <li key={code}>{t(`admin.org.file_move.code.${code}` as "admin.org.file_move.code.file_move_failed", {defaultValue: t("admin.org.file_move.code.file_move_failed")})} · {count}</li>)}</ul>
    </div>}

    <h4>{t("admin.org.file_move.reconcile_title")}</h4>
    <p className="field-note">{t("admin.org.file_move.reconcile_help")}</p>
    <label className="check-row"><input type="checkbox" name="file-reconcile-deep" checked={deep} disabled={busy} onChange={event => setDeep(event.target.checked)}/>{t("admin.org.file_move.deep")}</label>
    <div className="provider-actions">
      <button type="button" className="st-button" data-action="reconcile" disabled={busy || view.job?.status === "running"} onClick={() => void act(() => fileMoveApi.reconcile(deep))}>{t("admin.org.file_move.reconcile")}</button>
    </div>
    {report && <ReportView report={report} busy={busy} onSweep={dryRun => act(() => fileMoveApi.sweep(dryRun))}/>}
    {error && <p role="alert" className="admin-error">{error}</p>}
  </div>;
}

function ReportView({report, busy, onSweep}: {report: FileReport; busy: boolean; onSweep: (dryRun: boolean) => Promise<void>}) {
  const {t} = useTranslation("settings");
  if (report.status === "running") return <p className="field-note" role="status">{t("admin.org.file_move.report_running")}</p>;
  if (report.status === "failed") return <p className="is-failed" role="status" data-section="report">{t("admin.org.file_move.report_failed", {reason: t(`admin.org.file_move.report_code.${report.code}` as "admin.org.file_move.report_code.file_scan_failed", {defaultValue: t("admin.org.file_move.report_code.file_scan_failed")})})}</p>;
  const items = report.items ?? [];
  return <div className="file-move-report" role="status" data-section="report" data-kind={report.kind}>
    <p>{t(report.kind === "sweep" ? (report.dry_run ? "admin.org.file_move.report_sweep_dry" : "admin.org.file_move.report_sweep") : "admin.org.file_move.report_reconcile")}</p>
    <ul className="backup-snapshots">
      {REPORT_COUNTS.map(key => <li key={key} className={report[key] > 0 && REPORT_PROBLEMS.has(key) ? "is-failed" : undefined} data-count={key}>{t(`admin.org.file_move.count.${key}` as "admin.org.file_move.count.rows")}: {report[key]}{key === "orphans" && report.orphans > 0 ? ` (${formatBytes(report.orphan_bytes)})` : ""}</li>)}
      {report.kind === "sweep" && !report.dry_run && <li data-count="deleted">{t("admin.org.file_move.count.deleted")}: {report.deleted}</li>}
      {report.delete_failed > 0 && <li className="is-failed" data-count="delete_failed">{t("admin.org.file_move.count.delete_failed")}: {report.delete_failed}</li>}
    </ul>
    {items.length > 0 && <ul className="backup-snapshots" data-section="report-items">{items.map((item, index) => <li key={index}>{t(`admin.org.file_move.issue.${item.issue}` as "admin.org.file_move.issue.orphan", {defaultValue: item.issue})}{item.account ? ` · ${item.account}` : ""}{item.kind ? ` · ${item.kind}` : ""} · {formatBytes(item.size)}{item.age_days ? ` · ${t("admin.org.file_move.age_days", {count: item.age_days})}` : ""}</li>)}</ul>}
    {report.orphans > 0 && <div className="provider-actions">
      <button type="button" className="st-button" data-action="sweep-dry-run" disabled={busy} onClick={() => void onSweep(true)}>{t("admin.org.file_move.sweep_dry")}</button>
      <ConfirmAction label={t("admin.org.file_move.sweep")} question={t("admin.org.file_move.sweep_question", {count: report.orphans})} disabled={busy} onConfirm={() => onSweep(false)}/>
    </div>}
    <p className="field-note">{t("admin.org.file_move.orphan_note")}</p>
  </div>;
}
